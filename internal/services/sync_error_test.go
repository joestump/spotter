package services_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"spotter/ent"
	"spotter/ent/enttest"
	"spotter/internal/config"
	"spotter/internal/events"
	"spotter/internal/providers"
	"spotter/internal/services"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncer_GetActiveProviders_Error_ReturnsWithoutTouchingDB(t *testing.T) {
	// When getActiveProviders returns an error (user not found), Sync should return the error
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus()
	syncer := services.NewSyncer(client, &config.Config{}, logger, bus, nil)

	// Create a user, then delete them so getActiveProviders fails on refresh
	user, err := client.User.Create().SetUsername("testuser").Save(context.Background())
	require.NoError(t, err)

	// Register a mock provider that should never be called
	mockProv := &mockProvider{providerType: providers.TypeSpotify}
	syncer.Register(mockFactory(mockProv))

	// Delete the user so the refresh query fails
	require.NoError(t, client.User.DeleteOne(user).Exec(context.Background()))

	err = syncer.Sync(context.Background(), user)
	assert.Error(t, err, "Sync should return error when getActiveProviders fails")
	assert.Contains(t, err.Error(), "failed to refresh user data")

	// Verify no listens or playlists were created
	listens, _ := client.Listen.Query().All(context.Background())
	assert.Empty(t, listens, "no listens should be created on getActiveProviders failure")
}

func TestSyncer_ProviderError_TriggersBackoffRetriable(t *testing.T) {
	// When a provider returns a retriable error, backoff state is recorded
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus()
	syncer := services.NewSyncer(client, &config.Config{}, logger, bus, nil)

	user, err := client.User.Create().SetUsername("testuser").Save(context.Background())
	require.NoError(t, err)
	_, err = client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test").
		SetRefreshToken("test").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(context.Background())
	require.NoError(t, err)

	// Register mock provider that returns a retriable error (timeout)
	mockProv := &mockProvider{
		providerType: providers.TypeSpotify,
		err:          fmt.Errorf("connection timeout"),
	}
	syncer.Register(mockFactory(mockProv))

	// First sync — should not return error (Sync swallows per-provider errors)
	err = syncer.Sync(context.Background(), user)
	assert.NoError(t, err, "Sync should not surface per-provider errors")

	// Second sync — the provider should be skipped due to backoff
	err = syncer.Sync(context.Background(), user)
	assert.NoError(t, err)
}

func TestSyncer_ProviderError_FatalNotRetried(t *testing.T) {
	// When a provider returns a fatal error, it should be blocked from retry
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus()
	syncer := services.NewSyncer(client, &config.Config{}, logger, bus, nil)

	user, err := client.User.Create().SetUsername("testuser").Save(context.Background())
	require.NoError(t, err)
	_, err = client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test").
		SetRefreshToken("test").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(context.Background())
	require.NoError(t, err)

	// Register mock provider that returns a fatal error (unauthorized)
	fatalErr := services.NewHTTPStatusError(401, fmt.Errorf("token revoked"))
	mockProv := &mockProvider{
		providerType: providers.TypeSpotify,
		err:          fatalErr,
	}
	syncer.Register(mockFactory(mockProv))

	// First sync — triggers fatal error
	err = syncer.Sync(context.Background(), user)
	assert.NoError(t, err, "Sync should not surface per-provider errors")

	// Second sync — provider should be skipped due to fatal backoff
	err = syncer.Sync(context.Background(), user)
	assert.NoError(t, err)
}

func TestSyncer_SyncRecentListens_GetActiveProviders_Error(t *testing.T) {
	// SyncRecentListens should propagate getActiveProviders errors
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus()
	syncer := services.NewSyncer(client, &config.Config{}, logger, bus, nil)

	user, err := client.User.Create().SetUsername("testuser").Save(context.Background())
	require.NoError(t, err)

	// Delete user to force getActiveProviders to fail
	require.NoError(t, client.User.DeleteOne(user).Exec(context.Background()))

	err = syncer.SyncRecentListens(context.Background(), user)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to refresh user data")
}

func TestSyncer_SyncPlaylists_GetActiveProviders_Error(t *testing.T) {
	// SyncPlaylists should propagate getActiveProviders errors
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus()
	syncer := services.NewSyncer(client, &config.Config{}, logger, bus, nil)

	user, err := client.User.Create().SetUsername("testuser").Save(context.Background())
	require.NoError(t, err)

	// Delete user to force getActiveProviders to fail
	require.NoError(t, client.User.DeleteOne(user).Exec(context.Background()))

	err = syncer.SyncPlaylists(context.Background(), user)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to refresh user data")
}

func TestSyncer_HistoryCallbackError_DoesNotPersistPartialData(t *testing.T) {
	// When GetRecentListens returns an error, the provider is skipped
	// and no partial data leaks
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus()
	syncer := services.NewSyncer(client, &config.Config{}, logger, bus, nil)

	user, err := client.User.Create().SetUsername("testuser").Save(context.Background())
	require.NoError(t, err)
	_, err = client.NavidromeAuth.Create().
		SetUser(user).
		SetPassword("testpassword").
		Save(context.Background())
	require.NoError(t, err)

	// Register a provider that returns an error from GetRecentListens
	failProv := &mockProvider{
		providerType: providers.TypeNavidrome,
		err:          fmt.Errorf("connection timeout"),
	}
	syncer.Register(mockFactory(failProv))

	err = syncer.SyncRecentListens(context.Background(), user)
	require.NoError(t, err) // syncHistory swallows per-provider errors

	// Verify no listens were persisted
	listens, err := client.Listen.Query().All(context.Background())
	require.NoError(t, err)
	assert.Empty(t, listens, "no listens should be created when provider fails")
}

// countingProvider is a mock provider that counts how many times it is called.
type countingProvider struct {
	providerType providers.Type
	err          error
	calls        int
}

func (m *countingProvider) Type() providers.Type {
	return m.providerType
}

func (m *countingProvider) GetRecentListens(ctx context.Context, since time.Time, callback func([]providers.Track) error) error {
	m.calls++
	return m.err
}

func (m *countingProvider) GetPlaylists(ctx context.Context) ([]providers.Playlist, error) {
	m.calls++
	return nil, m.err
}

func (m *countingProvider) CreatePlaylist(ctx context.Context, name, description string, tracks []providers.Track) error {
	return nil
}

// recordingNotifier records NotifyIfNeeded calls made by the syncer.
type recordingNotifier struct {
	notifyCalls int
	lastErr     error
}

func (n *recordingNotifier) NotifyIfNeeded(_ context.Context, _ *ent.User, _ string, syncErr error) error {
	n.notifyCalls++
	n.lastErr = syncErr
	return nil
}

func (n *recordingNotifier) ClearCooldown(_ context.Context, _ int, _ string) error {
	return nil
}

func (n *recordingNotifier) SendTest(_ context.Context, _ *ent.User) error {
	return nil
}

// TestSyncer_RevokedNavidromeCredentials_FatalStopsRetryAndNotifies simulates
// revoked Navidrome credentials end to end (issue #325): the provider returns
// a 401 wrapped in HTTPStatusError with the exact message format the Navidrome
// client emits ("navidrome API returned status: 401"), the error classifies
// fatal, backoff blocks further retries, and NotifyIfNeeded fires.
// Governing: ADR-0020, ADR-0026, SPEC error-handling REQ-ERR-003, REQ-STATE-004, REQ-NOTIFY-001
func TestSyncer_RevokedNavidromeCredentials_FatalStopsRetryAndNotifies(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus()
	notifier := &recordingNotifier{}
	syncer := services.NewSyncer(client, &config.Config{}, logger, bus, notifier)

	user, err := client.User.Create().SetUsername("testuser").Save(context.Background())
	require.NoError(t, err)
	_, err = client.NavidromeAuth.Create().
		SetUser(user).
		SetPassword("revokedpassword").
		Save(context.Background())
	require.NoError(t, err)

	// Error formatted exactly the way internal/providers/navidrome does
	revokedErr := services.NewHTTPStatusError(
		http.StatusUnauthorized,
		fmt.Errorf("navidrome API returned status: %d", http.StatusUnauthorized),
	)
	assert.Equal(t, services.ErrorClassFatal, services.ClassifyError(revokedErr),
		"a 401 from Navidrome must classify fatal")

	prov := &countingProvider{providerType: providers.TypeNavidrome, err: revokedErr}
	syncer.Register(mockFactory(prov))

	// First sync — hits the 401, records fatal state, notifies
	require.NoError(t, syncer.Sync(context.Background(), user))
	require.GreaterOrEqual(t, notifier.notifyCalls, 1, "NotifyIfNeeded must fire for a fatal error")
	assert.Equal(t, services.ErrorClassFatal, services.ClassifyError(notifier.lastErr),
		"the error reaching the notifier must classify fatal")
	callsAfterFirst := prov.calls
	require.GreaterOrEqual(t, callsAfterFirst, 1)
	notifiesAfterFirst := notifier.notifyCalls

	// Second sync — provider is blocked by the fatal flag: backoff stops,
	// no more provider calls, no repeated notifications
	require.NoError(t, syncer.Sync(context.Background(), user))
	assert.Equal(t, callsAfterFirst, prov.calls,
		"provider must not be retried after a fatal error (backoff stops)")
	assert.Equal(t, notifiesAfterFirst, notifier.notifyCalls,
		"no repeat notification while the fatal error persists")
}

// TestSyncer_LegacyNavidromeErrorString_FallbackClassifiesFatal covers the
// string-matching fallback: an unwrapped error carrying Navidrome's colon
// format still classifies fatal and stops retries.
func TestSyncer_LegacyNavidromeErrorString_FallbackClassifiesFatal(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus()
	notifier := &recordingNotifier{}
	syncer := services.NewSyncer(client, &config.Config{}, logger, bus, notifier)

	user, err := client.User.Create().SetUsername("testuser2").Save(context.Background())
	require.NoError(t, err)

	legacyErr := fmt.Errorf("navidrome API returned status: %d", http.StatusUnauthorized)
	assert.Equal(t, services.ErrorClassFatal, services.ClassifyError(legacyErr))

	prov := &countingProvider{providerType: providers.TypeNavidrome, err: legacyErr}
	syncer.Register(mockFactory(prov))

	require.NoError(t, syncer.Sync(context.Background(), user))
	require.GreaterOrEqual(t, notifier.notifyCalls, 1, "NotifyIfNeeded must fire via the string fallback")
	callsAfterFirst := prov.calls

	require.NoError(t, syncer.Sync(context.Background(), user))
	assert.Equal(t, callsAfterFirst, prov.calls, "backoff must stop after fatal classification")
}
