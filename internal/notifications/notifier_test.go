package notifications

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"spotter/ent"
	"spotter/internal/services"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) *ent.Client {
	t.Helper()
	client, err := ent.Open("sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

// mockMailer records calls to Send.
type mockMailer struct {
	sendCalled bool
	sendErr    error
}

func (m *mockMailer) Send(to, subject, body string) error {
	m.sendCalled = true
	return m.sendErr
}

func (m *mockMailer) IsConfigured() bool { return true }

func TestNoopNotifier_NotifyIfNeeded(t *testing.T) {
	n := NewNoopNotifier()
	err := n.NotifyIfNeeded(context.Background(), nil, "navidrome", fmt.Errorf("test error"))
	if err != nil {
		t.Errorf("NoopNotifier.NotifyIfNeeded should return nil, got %v", err)
	}
}

func TestNoopNotifier_ClearCooldown(t *testing.T) {
	n := NewNoopNotifier()
	err := n.ClearCooldown(context.Background(), 1, "navidrome")
	if err != nil {
		t.Errorf("NoopNotifier.ClearCooldown should return nil, got %v", err)
	}
}

func TestDBNotifier_SkipsRetriableErrors(t *testing.T) {
	client := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mailer := &mockMailer{}

	n := NewDBNotifier(client, mailer, 7, "http://localhost:8080", logger)

	u, err := client.User.Create().
		SetUsername("testuser").
		SetEmail("test@example.com").
		SetPaginationSize(25).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Use a retriable error (network timeout)
	retriableErr := services.NewHTTPStatusError(503, fmt.Errorf("service unavailable"))
	err = n.NotifyIfNeeded(context.Background(), u, "navidrome", retriableErr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mailer.sendCalled {
		t.Error("should not send email for retriable error")
	}
}

func TestDBNotifier_SendsOnFatalError(t *testing.T) {
	client := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mailer := &mockMailer{}

	n := NewDBNotifier(client, mailer, 7, "http://localhost:8080", logger)

	u, err := client.User.Create().
		SetUsername("testuser_fatal").
		SetEmail("test@example.com").
		SetPaginationSize(25).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Use a fatal error (401 unauthorized)
	fatalErr := services.NewHTTPStatusError(401, fmt.Errorf("unauthorized"))
	err = n.NotifyIfNeeded(context.Background(), u, "spotify", fatalErr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mailer.sendCalled {
		t.Error("should send email for fatal error")
	}
}

// TestDBNotifier_SendsOnRevokedNavidromeCredentials verifies the fix for
// issue #325: a revoked Navidrome credential — formatted exactly the way the
// Navidrome client emits it ("navidrome API returned status: 401") — reaches
// the email notification path, both via the typed HTTPStatusError the client
// now constructs and via the string-matching fallback for unwrapped errors.
// Governing: ADR-0020, ADR-0026, SPEC error-handling REQ-ERR-003
func TestDBNotifier_SendsOnRevokedNavidromeCredentials(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			"typed_http_status_error",
			services.NewHTTPStatusError(401, fmt.Errorf("navidrome API returned status: %d", 401)),
		},
		{
			"legacy_unwrapped_string",
			fmt.Errorf("navidrome API returned status: %d", 401),
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := setupTestDB(t)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			mailer := &mockMailer{}
			n := NewDBNotifier(client, mailer, 7, "http://localhost:8080", logger)

			u, err := client.User.Create().
				SetUsername(fmt.Sprintf("testuser_navidrome_revoked_%d", i)).
				SetEmail("test@example.com").
				SetPaginationSize(25).
				Save(context.Background())
			if err != nil {
				t.Fatalf("create user: %v", err)
			}

			if err := n.NotifyIfNeeded(context.Background(), u, "navidrome", tt.err); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !mailer.sendCalled {
				t.Error("should send email for revoked Navidrome credentials (401)")
			}
		})
	}
}

func TestDBNotifier_CooldownPreventsSecondNotification(t *testing.T) {
	client := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mailer := &mockMailer{}

	n := NewDBNotifier(client, mailer, 7, "http://localhost:8080", logger)

	u, err := client.User.Create().
		SetUsername("testuser_cooldown").
		SetEmail("test@example.com").
		SetPaginationSize(25).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	fatalErr := services.NewHTTPStatusError(401, fmt.Errorf("unauthorized"))

	// First notification should send
	err = n.NotifyIfNeeded(context.Background(), u, "spotify", fatalErr)
	if err != nil {
		t.Fatalf("first notify: %v", err)
	}
	if !mailer.sendCalled {
		t.Fatal("first notification should send")
	}

	// Reset mock
	mailer.sendCalled = false

	// Second notification within cooldown should be skipped
	err = n.NotifyIfNeeded(context.Background(), u, "spotify", fatalErr)
	if err != nil {
		t.Fatalf("second notify: %v", err)
	}
	if mailer.sendCalled {
		t.Error("second notification within cooldown should be skipped")
	}
}

func TestDBNotifier_ClearCooldownAllowsNewNotification(t *testing.T) {
	client := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mailer := &mockMailer{}

	n := NewDBNotifier(client, mailer, 7, "http://localhost:8080", logger)

	u, err := client.User.Create().
		SetUsername("testuser_clear").
		SetEmail("test@example.com").
		SetPaginationSize(25).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	fatalErr := services.NewHTTPStatusError(401, fmt.Errorf("unauthorized"))

	// Send first notification
	err = n.NotifyIfNeeded(context.Background(), u, "spotify", fatalErr)
	if err != nil {
		t.Fatalf("first notify: %v", err)
	}
	if !mailer.sendCalled {
		t.Fatal("first notification should send")
	}
	mailer.sendCalled = false

	// Clear cooldown
	err = n.ClearCooldown(context.Background(), u.ID, "spotify")
	if err != nil {
		t.Fatalf("clear cooldown: %v", err)
	}

	// Should send again after cooldown cleared
	err = n.NotifyIfNeeded(context.Background(), u, "spotify", fatalErr)
	if err != nil {
		t.Fatalf("second notify after clear: %v", err)
	}
	if !mailer.sendCalled {
		t.Error("notification after ClearCooldown should send")
	}
}

func TestDBNotifier_SkipsWhenNoEmail(t *testing.T) {
	client := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mailer := &mockMailer{}

	n := NewDBNotifier(client, mailer, 7, "http://localhost:8080", logger)

	u, err := client.User.Create().
		SetUsername("testuser_noemail").
		SetPaginationSize(25).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	fatalErr := services.NewHTTPStatusError(401, fmt.Errorf("unauthorized"))
	err = n.NotifyIfNeeded(context.Background(), u, "spotify", fatalErr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mailer.sendCalled {
		t.Error("should not send when user has no email")
	}
}

func TestDBNotifier_DefaultCooldownDays(t *testing.T) {
	client := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	n := NewDBNotifier(client, &mockMailer{}, 0, "http://localhost:8080", logger)
	if n.cooldownDays != 7 {
		t.Errorf("expected default cooldown 7, got %d", n.cooldownDays)
	}

	n2 := NewDBNotifier(client, &mockMailer{}, -1, "http://localhost:8080", logger)
	if n2.cooldownDays != 7 {
		t.Errorf("expected default cooldown 7 for negative input, got %d", n2.cooldownDays)
	}
}

func TestDBNotifier_CooldownExpired_SendsAgain(t *testing.T) {
	client := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mailer := &mockMailer{}

	// Use cooldown of 1 day
	n := NewDBNotifier(client, mailer, 1, "http://localhost:8080", logger)

	u, err := client.User.Create().
		SetUsername("testuser_expired").
		SetEmail("test@example.com").
		SetPaginationSize(25).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Manually create a sync_notification with an old timestamp
	_, err = client.SyncNotification.Create().
		SetProvider("spotify").
		SetNotifiedAt(time.Now().AddDate(0, 0, -2)). // 2 days ago
		SetUserID(u.ID).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create old notification: %v", err)
	}

	fatalErr := services.NewHTTPStatusError(401, fmt.Errorf("unauthorized"))
	err = n.NotifyIfNeeded(context.Background(), u, "spotify", fatalErr)
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if !mailer.sendCalled {
		t.Error("should send when cooldown has expired")
	}
}
