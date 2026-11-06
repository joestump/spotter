package services_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"spotter/ent"
	"spotter/ent/enttest"
	"spotter/internal/config"
	"spotter/internal/events"
	"spotter/internal/providers"
	"spotter/internal/providers/spotify"
	"spotter/internal/services"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-0016 says adding a provider should not require modifying service layer
// code. These tests pin that for the two things the Syncer used to do itself:
// writing the sync cursor onto the provider's auth record, and eager-loading the
// auth edge a provider's factory reads.

// newProvider is a hypothetical provider: a type nothing in internal/services has
// ever heard of, registered with nothing but a factory, an auth loader, and its
// own cursor writer.
type newProvider struct {
	tracks  []providers.Track
	since   []time.Time
	cursors []time.Time
}

func (p *newProvider) Type() providers.Type { return providers.Type("testfake") }

func (p *newProvider) GetRecentListens(ctx context.Context, since time.Time, callback func([]providers.Track) error) error {
	p.since = append(p.since, since)
	if len(p.tracks) == 0 {
		return nil
	}
	return callback(p.tracks)
}

func (p *newProvider) SetSyncCursor(ctx context.Context, client *ent.Client, at time.Time) error {
	p.cursors = append(p.cursors, at)
	return nil
}

// cursorlessProvider fetches history but implements no cursor contract: the
// Syncer has nowhere to put its cursor and must say so.
type cursorlessProvider struct{}

func (cursorlessProvider) Type() providers.Type { return providers.Type("nocursor") }

func (cursorlessProvider) GetRecentListens(ctx context.Context, since time.Time, callback func([]providers.Track) error) error {
	return nil
}

func setupLoggingSyncer(t *testing.T) (*ent.Client, *services.Syncer, *bytes.Buffer) {
	t.Helper()

	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	syncer := services.NewSyncer(client, &config.Config{}, logger, events.NewBus(), nil)
	return client, syncer, logs
}

// A provider nobody has heard of gets its cursor persisted, because the Syncer
// asks the provider to write it instead of switching on provider types.
func TestSyncer_PersistsCursorThroughTheProvider(t *testing.T) {
	client, syncer, _ := setupLoggingSyncer(t)
	ctx := context.Background()

	user := createTestUser(t, client)
	playedAt := time.Now().Add(-time.Hour)
	fake := &newProvider{tracks: []providers.Track{{
		ID:       "fake:1",
		Name:     "Fake Track",
		Artist:   "Fake Artist",
		PlayedAt: playedAt,
	}}}

	syncer.Register(mockFactory(fake))

	require.NoError(t, syncer.Sync(ctx, user))

	require.Len(t, fake.cursors, 1, "the provider should have been asked to write its cursor once")
	assert.WithinDuration(t, time.Now(), fake.cursors[0], 5*time.Second)

	listens, err := client.Listen.Query().All(ctx)
	require.NoError(t, err)
	assert.Len(t, listens, 1, "the fake provider's history should still sync")
}

// A provider registered without a cursor is a configuration mistake, and it is
// reported instead of silently syncing from the beginning of time every run.
func TestSyncer_ReportsProviderThatCannotPersistACursor(t *testing.T) {
	client, syncer, logs := setupLoggingSyncer(t)
	ctx := context.Background()

	user := createTestUser(t, client)
	syncer.Register(mockFactory(cursorlessProvider{}))

	require.NoError(t, syncer.Sync(ctx, user), "a cursor failure must not fail the sync itself")

	assert.Contains(t, logs.String(), "does not implement providers.SyncCursor")
	assert.Contains(t, logs.String(), "provider=nocursor",
		"the warning should name the provider that has no cursor contract")
}

// The auth edges a factory needs come from what the provider registered, so a
// provider that reads an auth edge sees it without the Syncer naming the edge.
func TestSyncer_ActiveProvidersUseRegisteredAuthLoaders(t *testing.T) {
	client, syncer, _ := setupLoggingSyncer(t)
	ctx := context.Background()

	user := createTestUser(t, client)
	_, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test_access_token").
		SetRefreshToken("test_refresh_token").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	// A factory written the way provider factories are written: it decides whether
	// the user is connected by reading its own auth edge.
	factory := func(ctx context.Context, u *ent.User) (providers.Provider, error) {
		if u.Edges.SpotifyAuth == nil {
			return nil, nil
		}
		return &newProvider{tracks: []providers.Track{{
			ID:       "fake:1",
			Name:     "Fake Track",
			Artist:   "Fake Artist",
			PlayedAt: time.Now().Add(-time.Hour),
		}}}, nil
	}

	syncer.Register(factory, spotify.LoadAuth)

	require.NoError(t, syncer.Sync(ctx, user))

	listens, err := client.Listen.Query().All(ctx)
	require.NoError(t, err)
	assert.Len(t, listens, 1, "the loader registered with the provider should have made it active")
}

// Without a loader the same factory stays inactive: the Syncer loads only what
// providers ask for, and nothing is hardcoded here.
func TestSyncer_WithoutAnAuthLoaderTheFactoryStaysInactive(t *testing.T) {
	client, syncer, _ := setupLoggingSyncer(t)
	ctx := context.Background()

	user := createTestUser(t, client)
	_, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test_access_token").
		SetRefreshToken("test_refresh_token").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	factory := func(ctx context.Context, u *ent.User) (providers.Provider, error) {
		if u.Edges.SpotifyAuth == nil {
			return nil, nil
		}
		return &newProvider{}, nil
	}

	syncer.Register(factory)

	require.NoError(t, syncer.Sync(ctx, user))

	listens, err := client.Listen.Query().All(ctx)
	require.NoError(t, err)
	assert.Empty(t, listens, "a factory whose auth edge was never loaded reports the user as unconnected")
}
