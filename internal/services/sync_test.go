package services_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"spotter/ent"
	"spotter/ent/enttest"
	"spotter/ent/listen"
	"spotter/ent/playlist"
	"spotter/ent/playlisttrack"
	"spotter/internal/config"
	"spotter/internal/events"
	"spotter/internal/providers"
	"spotter/internal/services"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockProvider implements providers.Provider and providers.HistoryFetcher for testing
type mockProvider struct {
	providerType providers.Type
	tracks       []providers.Track
	playlists    []providers.Playlist
	err          error
}

func (m *mockProvider) Type() providers.Type {
	return m.providerType
}

func (m *mockProvider) GetRecentListens(ctx context.Context, since time.Time, callback func([]providers.Track) error) error {
	if m.err != nil {
		return m.err
	}
	if len(m.tracks) > 0 {
		return callback(m.tracks)
	}
	return nil
}

func (m *mockProvider) GetPlaylists(ctx context.Context) ([]providers.Playlist, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.playlists, nil
}

func (m *mockProvider) CreatePlaylist(ctx context.Context, name, description string, tracks []providers.Track) error {
	return nil
}

// mockFactory creates a factory that returns the given provider
func mockFactory(p providers.Provider) providers.Factory {
	return func(ctx context.Context, user *ent.User) (providers.Provider, error) {
		return p, nil
	}
}

func setupTestSyncer(t *testing.T) (*ent.Client, *services.Syncer, *events.Bus) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{}
	bus := events.NewBus()

	syncer := services.NewSyncer(client, cfg, logger, bus, nil)
	return client, syncer, bus
}

func createTestUser(t *testing.T, client *ent.Client) *ent.User {
	ctx := context.Background()
	user, err := client.User.Create().
		SetUsername("testuser").
		Save(ctx)
	require.NoError(t, err)
	return user
}

func TestSyncer_UpdatesLastSyncedAt_Navidrome(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	// Create user with Navidrome auth
	user := createTestUser(t, client)
	navidromeAuth, err := client.NavidromeAuth.Create().
		SetUser(user).
		SetPassword("testpassword").
		Save(ctx)
	require.NoError(t, err)
	assert.True(t, navidromeAuth.LastSyncedAt.IsZero(), "LastSyncedAt should be zero initially")

	// Register mock Navidrome provider that returns some tracks
	mockNavidrome := &mockProvider{
		providerType: providers.TypeNavidrome,
		tracks: []providers.Track{
			{
				ID:       "track1",
				Name:     "Test Track",
				Artist:   "Test Artist",
				Album:    "Test Album",
				PlayedAt: time.Now(),
			},
		},
	}
	syncer.Register(mockFactory(mockNavidrome))

	// Run sync
	err = syncer.Sync(ctx, user)
	require.NoError(t, err)

	// Verify LastSyncedAt was updated
	updatedAuth, err := client.NavidromeAuth.Get(ctx, navidromeAuth.ID)
	require.NoError(t, err)
	assert.False(t, updatedAuth.LastSyncedAt.IsZero(), "LastSyncedAt should be updated after sync")
	assert.WithinDuration(t, time.Now(), updatedAuth.LastSyncedAt, 5*time.Second)
}

func TestSyncer_UpdatesLastSyncedAt_Spotify(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	// Create user with Spotify auth
	user := createTestUser(t, client)
	spotifyAuth, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test_access_token").
		SetRefreshToken("test_refresh_token").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)
	assert.True(t, spotifyAuth.LastSyncedAt.IsZero(), "LastSyncedAt should be zero initially")

	// Register mock Spotify provider
	mockSpotify := &mockProvider{
		providerType: providers.TypeSpotify,
		tracks: []providers.Track{
			{
				ID:       "spotify:track:123",
				Name:     "Spotify Track",
				Artist:   "Spotify Artist",
				Album:    "Spotify Album",
				PlayedAt: time.Now(),
			},
		},
	}
	syncer.Register(mockFactory(mockSpotify))

	// Run sync
	err = syncer.Sync(ctx, user)
	require.NoError(t, err)

	// Verify LastSyncedAt was updated
	updatedAuth, err := client.SpotifyAuth.Get(ctx, spotifyAuth.ID)
	require.NoError(t, err)
	assert.False(t, updatedAuth.LastSyncedAt.IsZero(), "LastSyncedAt should be updated after sync")
	assert.WithinDuration(t, time.Now(), updatedAuth.LastSyncedAt, 5*time.Second)
}

func TestSyncer_UpdatesLastSyncedAt_LastFM(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	// Create user with LastFM auth
	user := createTestUser(t, client)
	lastfmAuth, err := client.LastFMAuth.Create().
		SetUser(user).
		SetUsername("lastfm_user").
		SetSessionKey("test_session_key").
		Save(ctx)
	require.NoError(t, err)
	assert.True(t, lastfmAuth.LastSyncedAt.IsZero(), "LastSyncedAt should be zero initially")

	// Register mock LastFM provider
	mockLastFM := &mockProvider{
		providerType: providers.TypeLastFM,
		tracks: []providers.Track{
			{
				ID:       "lastfm:track:456",
				Name:     "LastFM Track",
				Artist:   "LastFM Artist",
				Album:    "LastFM Album",
				PlayedAt: time.Now(),
			},
		},
	}
	syncer.Register(mockFactory(mockLastFM))

	// Run sync
	err = syncer.Sync(ctx, user)
	require.NoError(t, err)

	// Verify LastSyncedAt was updated
	updatedAuth, err := client.LastFMAuth.Get(ctx, lastfmAuth.ID)
	require.NoError(t, err)
	assert.False(t, updatedAuth.LastSyncedAt.IsZero(), "LastSyncedAt should be updated after sync")
	assert.WithinDuration(t, time.Now(), updatedAuth.LastSyncedAt, 5*time.Second)
}

func TestSyncer_UpdatesLastSyncedAt_EvenWithNoNewTracks(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	// Create user with Spotify auth
	user := createTestUser(t, client)
	spotifyAuth, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test_access_token").
		SetRefreshToken("test_refresh_token").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	// Register mock Spotify provider that returns no tracks
	mockSpotify := &mockProvider{
		providerType: providers.TypeSpotify,
		tracks:       []providers.Track{}, // Empty - no new tracks
	}
	syncer.Register(mockFactory(mockSpotify))

	// Run sync
	err = syncer.Sync(ctx, user)
	require.NoError(t, err)

	// Verify LastSyncedAt was still updated (sync was attempted)
	updatedAuth, err := client.SpotifyAuth.Get(ctx, spotifyAuth.ID)
	require.NoError(t, err)
	assert.False(t, updatedAuth.LastSyncedAt.IsZero(), "LastSyncedAt should be updated even with no new tracks")
}

func TestSyncer_UpdatesLastSyncedAt_AfterPlaylistSync(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	// Create user with Spotify auth
	user := createTestUser(t, client)
	spotifyAuth, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test_access_token").
		SetRefreshToken("test_refresh_token").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	// Register mock Spotify provider with playlists
	mockSpotify := &mockProvider{
		providerType: providers.TypeSpotify,
		tracks:       []providers.Track{},
		playlists: []providers.Playlist{
			{
				ID:          "playlist1",
				Name:        "My Playlist",
				Description: "Test playlist",
			},
		},
	}
	syncer.Register(mockFactory(mockSpotify))

	// Run sync
	err = syncer.Sync(ctx, user)
	require.NoError(t, err)

	// Verify LastSyncedAt was updated
	updatedAuth, err := client.SpotifyAuth.Get(ctx, spotifyAuth.ID)
	require.NoError(t, err)
	assert.False(t, updatedAuth.LastSyncedAt.IsZero(), "LastSyncedAt should be updated after playlist sync")
}

func TestSyncer_MultipleProviders_UpdatesAllLastSyncedAt(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	// Create user with multiple auth providers
	user := createTestUser(t, client)

	navidromeAuth, err := client.NavidromeAuth.Create().
		SetUser(user).
		SetPassword("testpassword").
		Save(ctx)
	require.NoError(t, err)

	spotifyAuth, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test_access_token").
		SetRefreshToken("test_refresh_token").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	// Register mock providers for both
	mockNavidrome := &mockProvider{
		providerType: providers.TypeNavidrome,
		tracks:       []providers.Track{},
	}
	mockSpotify := &mockProvider{
		providerType: providers.TypeSpotify,
		tracks:       []providers.Track{},
	}
	syncer.Register(mockFactory(mockNavidrome))
	syncer.Register(mockFactory(mockSpotify))

	// Run sync
	err = syncer.Sync(ctx, user)
	require.NoError(t, err)

	// Verify both LastSyncedAt were updated
	updatedNavidrome, err := client.NavidromeAuth.Get(ctx, navidromeAuth.ID)
	require.NoError(t, err)
	assert.False(t, updatedNavidrome.LastSyncedAt.IsZero(), "Navidrome LastSyncedAt should be updated")

	updatedSpotify, err := client.SpotifyAuth.Get(ctx, spotifyAuth.ID)
	require.NoError(t, err)
	assert.False(t, updatedSpotify.LastSyncedAt.IsZero(), "Spotify LastSyncedAt should be updated")
}

func TestSyncer_SyncRecentListens_UpdatesLastSyncedAt(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	// Create user with Spotify auth
	user := createTestUser(t, client)
	spotifyAuth, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test_access_token").
		SetRefreshToken("test_refresh_token").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	// Register mock Spotify provider
	mockSpotify := &mockProvider{
		providerType: providers.TypeSpotify,
		tracks:       []providers.Track{},
	}
	syncer.Register(mockFactory(mockSpotify))

	// Run SyncRecentListens only
	err = syncer.SyncRecentListens(ctx, user)
	require.NoError(t, err)

	// Verify LastSyncedAt was updated
	updatedAuth, err := client.SpotifyAuth.Get(ctx, spotifyAuth.ID)
	require.NoError(t, err)
	assert.False(t, updatedAuth.LastSyncedAt.IsZero(), "LastSyncedAt should be updated after SyncRecentListens")
}

func TestSyncer_SyncPlaylists_UpdatesLastSyncedAt(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	// Create user with Spotify auth
	user := createTestUser(t, client)
	spotifyAuth, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test_access_token").
		SetRefreshToken("test_refresh_token").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	// Register mock Spotify provider
	mockSpotify := &mockProvider{
		providerType: providers.TypeSpotify,
		playlists:    []providers.Playlist{},
	}
	syncer.Register(mockFactory(mockSpotify))

	// Run SyncPlaylists only
	err = syncer.SyncPlaylists(ctx, user)
	require.NoError(t, err)

	// Verify LastSyncedAt was updated
	updatedAuth, err := client.SpotifyAuth.Get(ctx, spotifyAuth.ID)
	require.NoError(t, err)
	assert.False(t, updatedAuth.LastSyncedAt.IsZero(), "LastSyncedAt should be updated after SyncPlaylists")
}

func TestSyncer_PersistsListens(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	// Create user with Navidrome auth
	user := createTestUser(t, client)
	_, err := client.NavidromeAuth.Create().
		SetUser(user).
		SetPassword("testpassword").
		Save(ctx)
	require.NoError(t, err)

	playedAt := time.Now().Add(-5 * time.Minute)

	// Register mock Navidrome provider with tracks
	mockNavidrome := &mockProvider{
		providerType: providers.TypeNavidrome,
		tracks: []providers.Track{
			{
				ID:       "track1",
				Name:     "Test Track 1",
				Artist:   "Test Artist",
				Album:    "Test Album",
				PlayedAt: playedAt,
				URL:      "http://example.com/track1",
			},
			{
				ID:       "track2",
				Name:     "Test Track 2",
				Artist:   "Another Artist",
				Album:    "Another Album",
				PlayedAt: playedAt.Add(time.Minute),
				URL:      "http://example.com/track2",
			},
		},
	}
	syncer.Register(mockFactory(mockNavidrome))

	// Run sync
	err = syncer.Sync(ctx, user)
	require.NoError(t, err)

	// Verify listens were persisted
	listens, err := client.Listen.Query().All(ctx)
	require.NoError(t, err)
	assert.Len(t, listens, 2, "Should have persisted 2 listens")

	// Verify listen details
	assert.Equal(t, "Test Track 1", listens[0].TrackName)
	assert.Equal(t, "Test Artist", listens[0].ArtistName)
	assert.Equal(t, "navidrome", listens[0].Source)
}

func TestSyncer_PersistsPlaylistsWithStats(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	// Create user with Spotify auth
	user := createTestUser(t, client)
	_, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test_access_token").
		SetRefreshToken("test_refresh_token").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	// Register mock Spotify provider with playlists that have stats
	mockSpotify := &mockProvider{
		providerType: providers.TypeSpotify,
		tracks:       []providers.Track{},
		playlists: []providers.Playlist{
			{
				ID:            "playlist-1",
				Name:          "My Test Playlist",
				Description:   "A playlist for testing",
				ImageURL:      "https://example.com/image.jpg",
				ExternalURL:   "https://open.spotify.com/playlist/playlist-1",
				TrackCount:    15,
				UniqueArtists: 8,
				UniqueAlbums:  5,
			},
			{
				ID:            "playlist-2",
				Name:          "Empty Playlist",
				Description:   "",
				ImageURL:      "",
				ExternalURL:   "https://open.spotify.com/playlist/playlist-2",
				TrackCount:    0,
				UniqueArtists: 0,
				UniqueAlbums:  0,
			},
		},
	}
	syncer.Register(mockFactory(mockSpotify))

	// Run sync
	err = syncer.Sync(ctx, user)
	require.NoError(t, err)

	// Verify playlists were persisted with all fields
	playlists, err := client.Playlist.Query().All(ctx)
	require.NoError(t, err)
	assert.Len(t, playlists, 2, "Should have persisted 2 playlists")

	// Find the playlist with stats
	var playlistWithStats *ent.Playlist
	var emptyPlaylist *ent.Playlist
	for _, pl := range playlists {
		if pl.RemoteID == "playlist-1" {
			playlistWithStats = pl
		} else if pl.RemoteID == "playlist-2" {
			emptyPlaylist = pl
		}
	}

	require.NotNil(t, playlistWithStats)
	assert.Equal(t, "My Test Playlist", playlistWithStats.Name)
	assert.Equal(t, "A playlist for testing", playlistWithStats.Description)
	assert.Equal(t, "https://example.com/image.jpg", playlistWithStats.ImageURL)
	assert.Equal(t, "https://open.spotify.com/playlist/playlist-1", playlistWithStats.ExternalURL)
	assert.Equal(t, 15, playlistWithStats.TrackCount)
	assert.Equal(t, 8, playlistWithStats.UniqueArtists)
	assert.Equal(t, 5, playlistWithStats.UniqueAlbums)
	assert.Equal(t, "spotify", playlistWithStats.Source)

	require.NotNil(t, emptyPlaylist)
	assert.Equal(t, "Empty Playlist", emptyPlaylist.Name)
	assert.Equal(t, 0, emptyPlaylist.TrackCount)
	assert.Equal(t, 0, emptyPlaylist.UniqueArtists)
	assert.Equal(t, 0, emptyPlaylist.UniqueAlbums)
}

func TestSyncer_UpdatesPlaylistStats(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	// Create user with Spotify auth
	user := createTestUser(t, client)
	_, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test_access_token").
		SetRefreshToken("test_refresh_token").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	// Create an existing playlist
	_, err = client.Playlist.Create().
		SetUser(user).
		SetRemoteID("playlist-1").
		SetName("Old Name").
		SetDescription("Old description").
		SetSource("spotify").
		SetTrackCount(5).
		SetUniqueArtists(3).
		SetUniqueAlbums(2).
		Save(ctx)
	require.NoError(t, err)

	// Register mock Spotify provider with updated playlist data
	mockSpotify := &mockProvider{
		providerType: providers.TypeSpotify,
		tracks:       []providers.Track{},
		playlists: []providers.Playlist{
			{
				ID:            "playlist-1",
				Name:          "Updated Name",
				Description:   "Updated description",
				ImageURL:      "https://example.com/new-image.jpg",
				ExternalURL:   "https://open.spotify.com/playlist/playlist-1",
				TrackCount:    25,
				UniqueArtists: 12,
				UniqueAlbums:  8,
			},
		},
	}
	syncer.Register(mockFactory(mockSpotify))

	// Run sync
	err = syncer.Sync(ctx, user)
	require.NoError(t, err)

	// Verify playlist was updated
	playlists, err := client.Playlist.Query().All(ctx)
	require.NoError(t, err)
	assert.Len(t, playlists, 1, "Should still have 1 playlist")

	pl := playlists[0]
	assert.Equal(t, "Updated Name", pl.Name)
	assert.Equal(t, "Updated description", pl.Description)
	assert.Equal(t, "https://example.com/new-image.jpg", pl.ImageURL)
	assert.Equal(t, 25, pl.TrackCount)
	assert.Equal(t, 12, pl.UniqueArtists)
	assert.Equal(t, 8, pl.UniqueAlbums)
}

func TestSyncer_SendsNotifications(t *testing.T) {
	client, syncer, bus := setupTestSyncer(t)
	ctx := context.Background()

	// Create user with Navidrome auth
	user := createTestUser(t, client)
	_, err := client.NavidromeAuth.Create().
		SetUser(user).
		SetPassword("testpassword").
		Save(ctx)
	require.NoError(t, err)

	// Subscribe to events
	eventChan, cancel := bus.Subscribe(user.ID)
	defer cancel()

	// Register mock Navidrome provider with tracks
	mockNavidrome := &mockProvider{
		providerType: providers.TypeNavidrome,
		tracks: []providers.Track{
			{
				ID:       "track1",
				Name:     "Test Track",
				Artist:   "Test Artist",
				Album:    "Test Album",
				PlayedAt: time.Now(),
			},
		},
	}
	syncer.Register(mockFactory(mockNavidrome))

	// Run sync in goroutine
	done := make(chan error)
	go func() {
		done <- syncer.Sync(ctx, user)
	}()

	// Collect events
	var receivedEvents []events.Event
	timeout := time.After(2 * time.Second)

collectLoop:
	for {
		select {
		case event := <-eventChan:
			receivedEvents = append(receivedEvents, event)
		case err := <-done:
			require.NoError(t, err)
			// Drain any events that were buffered before the done signal was received
		drainLoop:
			for {
				select {
				case event := <-eventChan:
					receivedEvents = append(receivedEvents, event)
				default:
					break drainLoop
				}
			}
			break collectLoop
		case <-timeout:
			t.Fatal("Timeout waiting for sync to complete")
		}
	}

	// Verify we received notification events
	assert.NotEmpty(t, receivedEvents, "Should have received events")

	// Check for notification event
	hasNotification := false
	for _, event := range receivedEvents {
		if event.Type == events.EventTypeNotification {
			hasNotification = true
			break
		}
	}
	assert.True(t, hasNotification, "Should have received a notification event")
}

func TestSyncer_PersistsPlaylistTracks(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	// Create user with Spotify auth
	user := createTestUser(t, client)
	_, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test_access_token").
		SetRefreshToken("test_refresh_token").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	// Register mock Spotify provider with playlists that have tracks
	mockSpotify := &mockProvider{
		providerType: providers.TypeSpotify,
		tracks:       []providers.Track{},
		playlists: []providers.Playlist{
			{
				ID:            "playlist-1",
				Name:          "My Test Playlist",
				Description:   "A playlist for testing",
				ImageURL:      "https://example.com/image.jpg",
				ExternalURL:   "https://open.spotify.com/playlist/playlist-1",
				TrackCount:    3,
				UniqueArtists: 2,
				UniqueAlbums:  2,
				Tracks: []providers.Track{
					{
						ID:         "track-1",
						Name:       "First Track",
						Artist:     "Artist A",
						Album:      "Album X",
						DurationMs: 180000,
						URL:        "https://open.spotify.com/track/track-1",
					},
					{
						ID:         "track-2",
						Name:       "Second Track",
						Artist:     "Artist B",
						Album:      "Album Y",
						DurationMs: 240000,
						URL:        "https://open.spotify.com/track/track-2",
					},
					{
						ID:         "track-3",
						Name:       "Third Track",
						Artist:     "Artist A",
						Album:      "Album X",
						DurationMs: 200000,
						URL:        "https://open.spotify.com/track/track-3",
					},
				},
			},
		},
	}
	syncer.Register(mockFactory(mockSpotify))

	// Run sync
	err = syncer.Sync(ctx, user)
	require.NoError(t, err)

	// Verify playlist was persisted
	playlists, err := client.Playlist.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, playlists, 1, "Should have persisted 1 playlist")

	// Verify playlist tracks were persisted
	playlistTracks, err := client.PlaylistTrack.Query().All(ctx)
	require.NoError(t, err)
	assert.Len(t, playlistTracks, 3, "Should have persisted 3 playlist tracks")

	// Verify track details
	for i, pt := range playlistTracks {
		assert.Equal(t, i, pt.Position, "Track position should match index")
		assert.NotEmpty(t, pt.TrackName, "Track name should not be empty")
		assert.NotEmpty(t, pt.ArtistName, "Artist name should not be empty")
	}

	// Verify first track details
	assert.Equal(t, "First Track", playlistTracks[0].TrackName)
	assert.Equal(t, "Artist A", playlistTracks[0].ArtistName)
	assert.Equal(t, "Album X", playlistTracks[0].AlbumName)
	assert.Equal(t, 180000, *playlistTracks[0].DurationMs)
	assert.Equal(t, "track-1", playlistTracks[0].RemoteID)
}

func TestSyncer_UpdatesPlaylistTracks(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	// Create user with Spotify auth
	user := createTestUser(t, client)
	_, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test_access_token").
		SetRefreshToken("test_refresh_token").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	// Create an existing playlist with tracks
	existingPlaylist, err := client.Playlist.Create().
		SetUser(user).
		SetRemoteID("playlist-1").
		SetName("My Playlist").
		SetSource("spotify").
		SetTrackCount(1).
		Save(ctx)
	require.NoError(t, err)

	// Create an existing track
	_, err = client.PlaylistTrack.Create().
		SetPlaylist(existingPlaylist).
		SetTrackName("Old Track").
		SetArtistName("Old Artist").
		SetPosition(0).
		Save(ctx)
	require.NoError(t, err)

	// Register mock Spotify provider with updated playlist
	mockSpotify := &mockProvider{
		providerType: providers.TypeSpotify,
		tracks:       []providers.Track{},
		playlists: []providers.Playlist{
			{
				ID:            "playlist-1",
				Name:          "My Playlist",
				TrackCount:    2,
				UniqueArtists: 2,
				UniqueAlbums:  2,
				Tracks: []providers.Track{
					{
						ID:     "new-track-1",
						Name:   "New Track 1",
						Artist: "New Artist 1",
						Album:  "New Album 1",
					},
					{
						ID:     "new-track-2",
						Name:   "New Track 2",
						Artist: "New Artist 2",
						Album:  "New Album 2",
					},
				},
			},
		},
	}
	syncer.Register(mockFactory(mockSpotify))

	// Run sync
	err = syncer.Sync(ctx, user)
	require.NoError(t, err)

	// Verify playlist tracks were replaced
	playlistTracks, err := client.PlaylistTrack.Query().All(ctx)
	require.NoError(t, err)
	assert.Len(t, playlistTracks, 2, "Should have 2 tracks after update")

	// Verify the old track is gone and new tracks are present
	trackNames := make([]string, len(playlistTracks))
	for i, pt := range playlistTracks {
		trackNames[i] = pt.TrackName
	}
	assert.Contains(t, trackNames, "New Track 1")
	assert.Contains(t, trackNames, "New Track 2")
	assert.NotContains(t, trackNames, "Old Track")
}

func TestSyncer_PreservesCatalogLinksOnPlaylistSync(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus()
	syncer := services.NewSyncer(client, &config.Config{}, logger, bus, nil)

	// Create user
	user, err := client.User.Create().
		SetUsername("testuser").
		SetPaginationSize(25).
		Save(ctx)
	require.NoError(t, err)

	// Create catalog artist, album, and track
	catalogArtist, err := client.Artist.Create().
		SetUser(user).
		SetName("Test Artist").
		Save(ctx)
	require.NoError(t, err)

	catalogAlbum, err := client.Album.Create().
		SetUser(user).
		SetArtist(catalogArtist).
		SetName("Test Album").
		Save(ctx)
	require.NoError(t, err)

	catalogTrack, err := client.Track.Create().
		SetArtist(catalogArtist).
		SetAlbum(catalogAlbum).
		SetName("Test Track").
		Save(ctx)
	require.NoError(t, err)

	// Create playlist
	existingPlaylist, err := client.Playlist.Create().
		SetUser(user).
		SetRemoteID("playlist-1").
		SetName("My Playlist").
		SetSource("spotify").
		SetTrackCount(1).
		Save(ctx)
	require.NoError(t, err)

	// Create playlist track with catalog links
	_, err = client.PlaylistTrack.Create().
		SetPlaylist(existingPlaylist).
		SetTrackName("Test Track").
		SetArtistName("Test Artist").
		SetAlbumName("Test Album").
		SetRemoteID("track-1").
		SetPosition(0).
		SetTrack(catalogTrack).
		SetArtist(catalogArtist).
		SetAlbum(catalogAlbum).
		Save(ctx)
	require.NoError(t, err)

	// Register mock Spotify provider with same track (simulating a re-sync)
	mockSpotify := &mockProvider{
		providerType: providers.TypeSpotify,
		tracks:       []providers.Track{},
		playlists: []providers.Playlist{
			{
				ID:            "playlist-1",
				Name:          "My Playlist",
				TrackCount:    1,
				UniqueArtists: 1,
				UniqueAlbums:  1,
				Tracks: []providers.Track{
					{
						ID:     "track-1",
						Name:   "Test Track",
						Artist: "Test Artist",
						Album:  "Test Album",
					},
				},
			},
		},
	}
	syncer.Register(mockFactory(mockSpotify))

	// Run sync
	err = syncer.Sync(ctx, user)
	require.NoError(t, err)

	// Verify playlist track still has catalog links
	playlistTracks, err := client.PlaylistTrack.Query().
		Where(playlisttrack.HasPlaylistWith(playlist.ID(existingPlaylist.ID))).
		WithTrack().
		WithArtist().
		WithAlbum().
		All(ctx)
	require.NoError(t, err)
	require.Len(t, playlistTracks, 1, "Should have 1 track")

	pt := playlistTracks[0]
	assert.NotNil(t, pt.Edges.Track, "Track catalog link should be preserved")
	assert.NotNil(t, pt.Edges.Artist, "Artist catalog link should be preserved")
	assert.NotNil(t, pt.Edges.Album, "Album catalog link should be preserved")

	if pt.Edges.Track != nil {
		assert.Equal(t, catalogTrack.ID, pt.Edges.Track.ID, "Should link to same catalog track")
	}
	if pt.Edges.Artist != nil {
		assert.Equal(t, catalogArtist.ID, pt.Edges.Artist.ID, "Should link to same catalog artist")
	}
	if pt.Edges.Album != nil {
		assert.Equal(t, catalogAlbum.ID, pt.Edges.Album.ID, "Should link to same catalog album")
	}
}

// Governing: SPEC listen-playlist-sync REQ-SYNC-021 (the ±window absorbs
// cross-provider echoes, never a genuine same-provider repeat)
func TestPersistListens_SameProviderRepeatWithinWindowPersists(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()
	user := createTestUser(t, client)

	firstPlay := time.Now().Add(-10 * time.Minute).Truncate(time.Second)

	// A 1:50 track played twice from the same provider two minutes apart:
	// both plays are genuine and must persist.
	mock := &mockProvider{
		providerType: providers.TypeSpotify,
		tracks: []providers.Track{
			{ID: "sp-repeat-a", Name: "Quick Listen", Artist: "Some Artist", Album: "Some Album", PlayedAt: firstPlay},
			{ID: "sp-repeat-b", Name: "Quick Listen", Artist: "Some Artist", Album: "Some Album", PlayedAt: firstPlay.Add(2 * time.Minute)},
		},
	}
	syncer.Register(mockFactory(mock))
	require.NoError(t, syncer.SyncRecentListens(ctx, user))

	count, err := client.Listen.Query().Where(listen.TrackName("Quick Listen")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count, "same-provider repeats two minutes apart must both persist")
}

// Governing: SPEC listen-playlist-sync REQ-SYNC-021 (a cross-provider echo inside the ±window still dedupes)
func TestPersistListens_CrossProviderEchoWithinWindowDedupes(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()
	user := createTestUser(t, client)

	playedAt := time.Now().Add(-10 * time.Minute).Truncate(time.Second)

	spotify := &mockProvider{
		providerType: providers.TypeSpotify,
		tracks: []providers.Track{
			{ID: "sp-echo-1", Name: "Echo Track", Artist: "Some Artist", Album: "Some Album", PlayedAt: playedAt},
		},
	}
	syncer.Register(mockFactory(spotify))
	require.NoError(t, syncer.SyncRecentListens(ctx, user))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	echo := &mockProvider{
		providerType: providers.TypeNavidrome,
		tracks: []providers.Track{
			{ID: "nd-echo-1", Name: "Echo Track", Artist: "Some Artist", Album: "Some Album", PlayedAt: playedAt.Add(30 * time.Second)},
		},
	}
	echoSyncer := services.NewSyncer(client, &config.Config{}, logger, events.NewBus(), nil)
	echoSyncer.Register(mockFactory(echo))
	require.NoError(t, echoSyncer.SyncRecentListens(ctx, user))

	count, err := client.Listen.Query().Where(listen.TrackName("Echo Track")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "cross-provider echo within the ±window must still dedupe")
}

// sinceCapturingProvider records the since timestamp passed to GetRecentListens.
type sinceCapturingProvider struct {
	providerType  providers.Type
	receivedSince time.Time
}

func (m *sinceCapturingProvider) Type() providers.Type {
	return m.providerType
}

func (m *sinceCapturingProvider) GetRecentListens(ctx context.Context, since time.Time, callback func([]providers.Track) error) error {
	m.receivedSince = since
	return nil
}

// Governing: SPEC listen-playlist-sync REQ-SYNC-020 (first sync is bounded by the configured lookback)
func TestSyncHistory_ConfiguredLookback_NoListens(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent_lookback?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{}
	cfg.Sync.HistoryLookback = "24h"
	syncer := services.NewSyncer(client, cfg, logger, events.NewBus(), nil)

	ctx := context.Background()
	user := createTestUser(t, client)

	mock := &sinceCapturingProvider{providerType: providers.TypeSpotify}
	syncer.Register(mockFactory(mock))

	before := time.Now()
	require.NoError(t, syncer.SyncRecentListens(ctx, user))
	after := time.Now()

	assert.False(t, mock.receivedSince.Before(before.Add(-24*time.Hour)), "since should not predate now-24h")
	assert.False(t, mock.receivedSince.After(after.Add(-24*time.Hour)), "since should not postdate now-24h")
}

// Governing: SPEC listen-playlist-sync REQ-SYNC-021 (dedup by provider+provider_track_id+played_at)
func TestPersistListens_ProviderTrackIDDedup(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()
	user := createTestUser(t, client)

	playedAt := time.Now().Add(-time.Hour).Truncate(time.Second)

	// First sync stores the listen with the provider track ID.
	first := &mockProvider{
		providerType: providers.TypeSpotify,
		tracks: []providers.Track{
			{ID: "sp-track-1", Name: "Original Title", Artist: "Some Artist", Album: "Some Album", PlayedAt: playedAt},
		},
	}
	syncer.Register(mockFactory(first))
	require.NoError(t, syncer.SyncRecentListens(ctx, user))

	listens, err := client.Listen.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, listens, 1)
	assert.Equal(t, "sp-track-1", listens[0].ProviderTrackID)

	// Second sync reports the same play with a different display title (e.g. the
	// provider renamed the track). The provider ID + played_at dedup key must
	// still recognize it as the same listen.
	second := &mockProvider{
		providerType: providers.TypeSpotify,
	}
	second.tracks = []providers.Track{
		{ID: "sp-track-1", Name: "Original Title (2011 Remaster)", Artist: "Some Artist", Album: "Some Album", PlayedAt: playedAt},
	}
	secondSyncer := services.NewSyncer(client, &config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), events.NewBus(), nil)
	secondSyncer.Register(mockFactory(second))
	require.NoError(t, secondSyncer.SyncRecentListens(ctx, user))

	count, err := client.Listen.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "same provider track ID + played_at must not create a second listen")
}
