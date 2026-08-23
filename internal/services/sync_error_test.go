package services_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

	// First sync — the provider failure must surface as an error (issue #326)
	err = syncer.Sync(context.Background(), user)
	assert.Error(t, err, "Sync must surface aggregated per-provider errors")

	// Second sync — the provider is skipped due to backoff; a deliberate
	// backoff skip is throttling, not a new failure, so no error surfaces
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

	// First sync — triggers fatal error, which must surface (issue #326)
	err = syncer.Sync(context.Background(), user)
	assert.Error(t, err, "Sync must surface aggregated per-provider errors")

	// Second sync — provider is skipped due to fatal backoff (throttling, not a new failure)
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
	require.Error(t, err, "syncHistory must surface aggregated per-provider errors (issue #326)")

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

	// First sync — hits the 401, records fatal state, notifies, and surfaces the error (issue #326)
	require.Error(t, syncer.Sync(context.Background(), user))
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

	require.Error(t, syncer.Sync(context.Background(), user), "fatal provider error must surface (issue #326)")
	require.GreaterOrEqual(t, notifier.notifyCalls, 1, "NotifyIfNeeded must fire via the string fallback")
	callsAfterFirst := prov.calls

	require.NoError(t, syncer.Sync(context.Background(), user))
	assert.Equal(t, callsAfterFirst, prov.calls, "backoff must stop after fatal classification")
}

// metricSyncEvent mirrors the metric.sync attribute schema for log-capture assertions.
// Governing: ADR-0019 (structured metrics), SPEC observability REQ-BG-003
type metricSyncEvent struct {
	Msg             string `json:"msg"`
	Provider        string `json:"provider"`
	ListensSynced   int    `json:"listens_synced"`
	PlaylistsSynced int    `json:"playlists_synced"`
	DurationMs      int64  `json:"duration_ms"`
	Success         bool   `json:"success"`
	Error           string `json:"error"`
}

// captureMetricSyncEvents parses JSON log output and returns metric.sync events keyed by provider.
func captureMetricSyncEvents(t *testing.T, logs *bytes.Buffer) map[string]metricSyncEvent {
	t.Helper()
	events := make(map[string]metricSyncEvent)
	scanner := bufio.NewScanner(logs)
	for scanner.Scan() {
		line := scanner.Bytes()
		var ev metricSyncEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Msg == "metric.sync" {
			events[ev.Provider] = ev
		}
	}
	require.NoError(t, scanner.Err())
	return events
}

// TestSyncer_AllProvidersFail_SyncReturnsErrorAndMetricSyncFails is the issue #326
// acceptance test: when every provider fails, Sync() returns an aggregated error
// and each provider's metric.sync event logs success=false.
// Governing: ADR-0019, ADR-0020, SPEC observability REQ-BG-003
func TestSyncer_AllProvidersFail_SyncReturnsErrorAndMetricSyncFails(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	bus := events.NewBus()
	syncer := services.NewSyncer(client, &config.Config{}, logger, bus, nil)

	user, err := client.User.Create().SetUsername("testuser").Save(context.Background())
	require.NoError(t, err)

	failingSpotify := &mockProvider{
		providerType: providers.TypeSpotify,
		err:          fmt.Errorf("connection timeout"),
	}
	failingNavidrome := &mockProvider{
		providerType: providers.TypeNavidrome,
		err:          fmt.Errorf("connection refused"),
	}
	syncer.Register(mockFactory(failingSpotify))
	syncer.Register(mockFactory(failingNavidrome))

	err = syncer.Sync(context.Background(), user)
	require.Error(t, err, "Sync must return an error when every provider fails")
	assert.Contains(t, err.Error(), string(providers.TypeSpotify))
	assert.Contains(t, err.Error(), string(providers.TypeNavidrome))

	metrics := captureMetricSyncEvents(t, &logs)
	require.Len(t, metrics, 2, "one metric.sync event per active provider")
	for provider, ev := range metrics {
		assert.False(t, ev.Success, "metric.sync for %s must log success=false", provider)
		assert.NotEmpty(t, ev.Error, "metric.sync for %s must carry the error", provider)
		assert.Zero(t, ev.ListensSynced, "no listens synced for failing provider %s", provider)
	}
}

// TestSyncer_PartialFailure_SurfacesErrorButPreservesPartialSuccess asserts that
// one provider failing does not prevent other providers from syncing, that the
// aggregated error still surfaces, and that metric.sync attributes each
// provider's own counts and success flag (not aggregate totals).
// Governing: ADR-0019, ADR-0020, SPEC observability REQ-BG-003, SPEC listen-playlist-sync REQ-SYNC-011
func TestSyncer_PartialFailure_SurfacesErrorButPreservesPartialSuccess(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	bus := events.NewBus()
	syncer := services.NewSyncer(client, &config.Config{}, logger, bus, nil)

	user, err := client.User.Create().SetUsername("testuser").Save(context.Background())
	require.NoError(t, err)

	failingSpotify := &mockProvider{
		providerType: providers.TypeSpotify,
		err:          fmt.Errorf("connection timeout"),
	}
	workingNavidrome := &mockProvider{
		providerType: providers.TypeNavidrome,
		tracks: []providers.Track{
			{ID: "t1", Name: "Track One", Artist: "Artist One", Album: "Album One", PlayedAt: time.Now()},
			{ID: "t2", Name: "Track Two", Artist: "Artist Two", Album: "Album Two", PlayedAt: time.Now().Add(-10 * time.Minute)},
		},
	}
	syncer.Register(mockFactory(failingSpotify))
	syncer.Register(mockFactory(workingNavidrome))

	err = syncer.Sync(context.Background(), user)
	require.Error(t, err, "partial failure must still surface an error")
	assert.Contains(t, err.Error(), string(providers.TypeSpotify))
	assert.NotContains(t, err.Error(), string(providers.TypeNavidrome),
		"the healthy provider must not appear in the aggregated error")

	// Partial success preserved: the healthy provider's listens persisted
	listens, err := client.Listen.Query().All(context.Background())
	require.NoError(t, err)
	assert.Len(t, listens, 2, "healthy provider's listens must persist despite the other failing")

	// Per-provider metric attribution (REQ-BG-003)
	metrics := captureMetricSyncEvents(t, &logs)
	require.Len(t, metrics, 2, "one metric.sync event per active provider")

	spotifyEv := metrics[string(providers.TypeSpotify)]
	assert.False(t, spotifyEv.Success, "failing provider must log success=false")
	assert.NotEmpty(t, spotifyEv.Error)
	assert.Zero(t, spotifyEv.ListensSynced)

	navidromeEv := metrics[string(providers.TypeNavidrome)]
	assert.True(t, navidromeEv.Success, "healthy provider must log success=true")
	assert.Empty(t, navidromeEv.Error)
	assert.Equal(t, 2, navidromeEv.ListensSynced, "listens_synced must be the provider's own count")
}
