package services_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"fmt"

	"spotter/ent"
	"spotter/ent/enttest"
	"spotter/ent/playlist"
	user_ent "spotter/ent/user"
	"spotter/internal/config"
	"spotter/internal/events"
	"spotter/internal/providers"
	"spotter/internal/services"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPlaylistSyncer implements providers.PlaylistSyncer for testing
type mockPlaylistSyncer struct {
	providerType   providers.Type
	syncedPlaylist *providers.SyncPlaylistRequest
	syncedID       string
	syncErr        error
	deletedID      string
	deleteErr      error
	updatedID      string
	updatedTracks  []providers.Track
	updateErr      error
}

func (m *mockPlaylistSyncer) Type() providers.Type {
	return m.providerType
}

func (m *mockPlaylistSyncer) SyncPlaylist(ctx context.Context, playlist providers.SyncPlaylistRequest) (string, error) {
	if m.syncErr != nil {
		return "", m.syncErr
	}
	m.syncedPlaylist = &playlist
	return m.syncedID, nil
}

func (m *mockPlaylistSyncer) DeletePlaylist(ctx context.Context, remotePlaylistID string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedID = remotePlaylistID
	return nil
}

func (m *mockPlaylistSyncer) UpdatePlaylistTracks(ctx context.Context, remotePlaylistID string, tracks []providers.Track) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updatedID = remotePlaylistID
	m.updatedTracks = tracks
	return nil
}

// mockPlaylistSyncerFactory creates a factory that returns the mock syncer
func mockPlaylistSyncerFactory(p providers.Provider) providers.Factory {
	return func(ctx context.Context, user *ent.User) (providers.Provider, error) {
		return p, nil
	}
}

func setupPlaylistSyncService(t *testing.T) (*ent.Client, *services.PlaylistSyncService, *events.Bus, *mockPlaylistSyncer) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{}
	cfg.PlaylistSync.MinMatchConfidence = 0.8
	cfg.PlaylistSync.DeleteOnUnsync = true

	bus := events.NewBus()

	svc := services.NewPlaylistSyncService(client, cfg, logger, bus)

	// Create mock syncer
	mockSyncer := &mockPlaylistSyncer{
		providerType: providers.TypeNavidrome,
		syncedID:     "nav-playlist-123",
	}

	// Register the mock factory
	svc.Register(mockPlaylistSyncerFactory(mockSyncer))

	return client, svc, bus, mockSyncer
}

func createTestUserWithNavidromeAuth(t *testing.T, client *ent.Client) *ent.User {
	ctx := context.Background()

	// Create user first
	user, err := client.User.Create().
		SetUsername("testuser").
		Save(ctx)
	require.NoError(t, err)

	// Create Navidrome auth with required user edge
	_, err = client.NavidromeAuth.Create().
		SetPassword("testpassword").
		SetUser(user).
		Save(ctx)
	require.NoError(t, err)

	// Reload user with auth edge
	user, err = client.User.Query().
		Where(user_ent.ID(user.ID)).
		WithNavidromeAuth().
		Only(ctx)
	require.NoError(t, err)

	return user
}

var playlistCounter int

func createTestPlaylistForSync(t *testing.T, client *ent.Client, user *ent.User, source string, syncEnabled bool) *ent.Playlist {
	ctx := context.Background()
	playlistCounter++

	pl, err := client.Playlist.Create().
		SetUser(user).
		SetRemoteID(fmt.Sprintf("remote-%d", playlistCounter)).
		SetName("Test Playlist").
		SetDescription("A test playlist").
		SetSource(source).
		SetTrackCount(5).
		SetSyncToNavidrome(syncEnabled).
		Save(ctx)
	require.NoError(t, err)

	return pl
}

func createTestPlaylistTracksForSync(t *testing.T, client *ent.Client, pl *ent.Playlist) {
	ctx := context.Background()

	tracks := []struct {
		name   string
		artist string
		album  string
	}{
		{"Song One", "Artist A", "Album 1"},
		{"Song Two", "Artist B", "Album 2"},
		{"Song Three", "Artist C", "Album 3"},
	}

	for i, track := range tracks {
		_, err := client.PlaylistTrack.Create().
			SetPlaylist(pl).
			SetTrackName(track.name).
			SetArtistName(track.artist).
			SetAlbumName(track.album).
			SetPosition(i + 1).
			Save(ctx)
		require.NoError(t, err)
	}
}

// createNavidromeTracksForMatching creates matching Navidrome tracks in the library
// so the TrackMatcher can find matches for the playlist tracks
func createNavidromeTracksForMatching(t *testing.T, client *ent.Client, user *ent.User) {
	ctx := context.Background()

	tracks := []struct {
		name         string
		artist       string
		navidromeID  string
	}{
		{"Song One", "Artist A", "nav-track-1"},
		{"Song Two", "Artist B", "nav-track-2"},
		{"Song Three", "Artist C", "nav-track-3"},
	}

	for _, track := range tracks {
		// Create artist with user
		artist, err := client.Artist.Create().
			SetName(track.artist).
			SetUser(user).
			Save(ctx)
		require.NoError(t, err)

		// Create track with navidrome ID
		_, err = client.Track.Create().
			SetName(track.name).
			SetArtist(artist).
			SetNillableNavidromeID(&track.navidromeID).
			Save(ctx)
		require.NoError(t, err)
	}
}

func TestPlaylistSyncService_SyncPlaylistToNavidrome_NewPlaylist(t *testing.T) {
	client, svc, bus, mockSyncer := setupPlaylistSyncService(t)
	ctx := context.Background()

	// Subscribe to notifications
	user := createTestUserWithNavidromeAuth(t, client)
	notifCh, cleanup := bus.Subscribe(user.ID)
	defer cleanup()

	// Create Navidrome tracks in the library for matching
	createNavidromeTracksForMatching(t, client, user)

	// Create a Spotify playlist with sync enabled
	pl := createTestPlaylistForSync(t, client, user, "spotify", true)
	createTestPlaylistTracksForSync(t, client, pl)

	// Sync the playlist
	err := svc.SyncPlaylistToNavidrome(ctx, pl.ID)
	require.NoError(t, err)

	// Verify the playlist was synced
	assert.NotNil(t, mockSyncer.syncedPlaylist)
	assert.Contains(t, mockSyncer.syncedPlaylist.Name, "Test Playlist")
	assert.Equal(t, "A test playlist", mockSyncer.syncedPlaylist.Description)

	// Verify database was updated
	updatedPl, err := client.Playlist.Get(ctx, pl.ID)
	require.NoError(t, err)
	assert.Equal(t, "nav-playlist-123", updatedPl.NavidromePlaylistID)
	assert.NotNil(t, updatedPl.LastSyncedAt)
	assert.Empty(t, updatedPl.SyncError)

	// Verify notifications were sent (we send "Syncing Playlist" first, then "Playlist Synced")
	receivedSyncedNotification := false
	for i := 0; i < 3; i++ { // Drain up to 3 notifications
		select {
		case event := <-notifCh:
			assert.Equal(t, events.EventTypeNotification, event.Type)
			payload := event.Payload.(events.NotificationPayload)
			if payload.Title == "Playlist Synced" {
				receivedSyncedNotification = true
			}
		case <-time.After(100 * time.Millisecond):
			break
		}
	}
	assert.True(t, receivedSyncedNotification, "Expected 'Playlist Synced' notification")
}

func TestPlaylistSyncService_SyncPlaylistToNavidrome_ExistingPlaylist(t *testing.T) {
	client, svc, _, mockSyncer := setupPlaylistSyncService(t)
	ctx := context.Background()

	user := createTestUserWithNavidromeAuth(t, client)

	// Create a playlist that was already synced
	pl, err := client.Playlist.Create().
		SetUser(user).
		SetRemoteID("remote-456").
		SetName("Existing Playlist").
		SetSource("spotify").
		SetSyncToNavidrome(true).
		SetNavidromePlaylistID("existing-nav-id").
		SetTrackCount(3).
		Save(ctx)
	require.NoError(t, err)

	createTestPlaylistTracksForSync(t, client, pl)

	// Sync the playlist (should update, not create)
	err = svc.SyncPlaylistToNavidrome(ctx, pl.ID)
	require.NoError(t, err)

	// Verify update was called, not create
	assert.Equal(t, "existing-nav-id", mockSyncer.updatedID)
	assert.Nil(t, mockSyncer.syncedPlaylist) // SyncPlaylist should not be called

	// Verify database still has the original ID
	updatedPl, err := client.Playlist.Get(ctx, pl.ID)
	require.NoError(t, err)
	assert.Equal(t, "existing-nav-id", updatedPl.NavidromePlaylistID)
}

func TestPlaylistSyncService_SyncPlaylistToNavidrome_SyncDisabled(t *testing.T) {
	client, svc, _, mockSyncer := setupPlaylistSyncService(t)
	ctx := context.Background()

	user := createTestUserWithNavidromeAuth(t, client)

	// Create a playlist with sync disabled
	pl := createTestPlaylistForSync(t, client, user, "spotify", false)

	// Try to sync
	err := svc.SyncPlaylistToNavidrome(ctx, pl.ID)
	require.NoError(t, err)

	// Verify no sync was attempted
	assert.Nil(t, mockSyncer.syncedPlaylist)
	assert.Empty(t, mockSyncer.updatedID)
}

func TestPlaylistSyncService_SyncPlaylistToNavidrome_NavidromePlaylistError(t *testing.T) {
	client, svc, _, _ := setupPlaylistSyncService(t)
	ctx := context.Background()

	user := createTestUserWithNavidromeAuth(t, client)

	// Create a Navidrome playlist (should not be syncable to Navidrome)
	pl := createTestPlaylistForSync(t, client, user, "navidrome", true)

	// Try to sync - should fail
	err := svc.SyncPlaylistToNavidrome(ctx, pl.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot sync Navidrome playlist")
}

func TestPlaylistSyncService_SyncPlaylistToNavidrome_SyncError(t *testing.T) {
	client, svc, bus, mockSyncer := setupPlaylistSyncService(t)
	ctx := context.Background()

	// Set up mock to return error
	mockSyncer.syncErr = assert.AnError

	user := createTestUserWithNavidromeAuth(t, client)
	notifCh, cleanup := bus.Subscribe(user.ID)
	defer cleanup()

	pl := createTestPlaylistForSync(t, client, user, "spotify", true)
	createTestPlaylistTracksForSync(t, client, pl)

	// Try to sync
	err := svc.SyncPlaylistToNavidrome(ctx, pl.ID)
	assert.Error(t, err)

	// Verify sync_error was saved
	updatedPl, err := client.Playlist.Get(ctx, pl.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, updatedPl.SyncError)

	// Verify error notification was sent (we send "Syncing Playlist" first, then "Playlist Sync Failed")
	receivedFailedNotification := false
	for i := 0; i < 3; i++ { // Drain up to 3 notifications
		select {
		case event := <-notifCh:
			assert.Equal(t, events.EventTypeNotification, event.Type)
			payload := event.Payload.(events.NotificationPayload)
			if payload.Title == "Playlist Sync Failed" {
				receivedFailedNotification = true
			}
		case <-time.After(100 * time.Millisecond):
			break
		}
	}
	assert.True(t, receivedFailedNotification, "Expected 'Playlist Sync Failed' notification")
}

func TestPlaylistSyncService_RemovePlaylistFromNavidrome_Success(t *testing.T) {
	client, svc, bus, mockSyncer := setupPlaylistSyncService(t)
	ctx := context.Background()

	user := createTestUserWithNavidromeAuth(t, client)
	notifCh, cleanup := bus.Subscribe(user.ID)
	defer cleanup()

	// Create a playlist that was synced
	pl, err := client.Playlist.Create().
		SetUser(user).
		SetRemoteID("remote-789").
		SetName("Synced Playlist").
		SetSource("spotify").
		SetSyncToNavidrome(false). // Sync was just disabled
		SetNavidromePlaylistID("nav-to-delete").
		SetMatchedTrackCount(5).
		Save(ctx)
	require.NoError(t, err)

	// Remove from Navidrome
	err = svc.RemovePlaylistFromNavidrome(ctx, pl.ID)
	require.NoError(t, err)

	// Verify delete was called
	assert.Equal(t, "nav-to-delete", mockSyncer.deletedID)

	// Verify database was cleared
	updatedPl, err := client.Playlist.Get(ctx, pl.ID)
	require.NoError(t, err)
	assert.Empty(t, updatedPl.NavidromePlaylistID)
	assert.Nil(t, updatedPl.LastSyncedAt)
	assert.Equal(t, 0, updatedPl.MatchedTrackCount)

	// Verify notification was sent
	select {
	case event := <-notifCh:
		assert.Equal(t, events.EventTypeNotification, event.Type)
		payload := event.Payload.(events.NotificationPayload)
		assert.Contains(t, payload.Title, "Playlist Removed")
	case <-time.After(100 * time.Millisecond):
		t.Log("No notification received (may be expected if async)")
	}
}

func TestPlaylistSyncService_RemovePlaylistFromNavidrome_NoNavidromeID(t *testing.T) {
	client, svc, _, mockSyncer := setupPlaylistSyncService(t)
	ctx := context.Background()

	user := createTestUserWithNavidromeAuth(t, client)

	// Create a playlist without Navidrome ID
	pl := createTestPlaylistForSync(t, client, user, "spotify", false)

	// Try to remove - should succeed without calling delete
	err := svc.RemovePlaylistFromNavidrome(ctx, pl.ID)
	require.NoError(t, err)

	// Verify delete was NOT called
	assert.Empty(t, mockSyncer.deletedID)
}

func TestPlaylistSyncService_RemovePlaylistFromNavidrome_DeleteDisabled(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{}
	cfg.PlaylistSync.MinMatchConfidence = 0.8
	cfg.PlaylistSync.DeleteOnUnsync = false // Delete disabled

	bus := events.NewBus()
	svc := services.NewPlaylistSyncService(client, cfg, logger, bus)

	mockSyncer := &mockPlaylistSyncer{
		providerType: providers.TypeNavidrome,
	}
	svc.Register(mockPlaylistSyncerFactory(mockSyncer))

	ctx := context.Background()
	user := createTestUserWithNavidromeAuth(t, client)

	// Create a playlist that was synced
	pl, err := client.Playlist.Create().
		SetUser(user).
		SetRemoteID("remote-abc").
		SetName("Keep Me").
		SetSource("spotify").
		SetSyncToNavidrome(false).
		SetNavidromePlaylistID("keep-this-id").
		Save(ctx)
	require.NoError(t, err)

	// Try to remove - should NOT call delete because DeleteOnUnsync is false
	err = svc.RemovePlaylistFromNavidrome(ctx, pl.ID)
	require.NoError(t, err)

	// Verify delete was NOT called
	assert.Empty(t, mockSyncer.deletedID)
}

func TestPlaylistSyncService_SyncAllEnabledPlaylists(t *testing.T) {
	client, svc, _, mockSyncer := setupPlaylistSyncService(t)
	ctx := context.Background()

	user := createTestUserWithNavidromeAuth(t, client)

	// Create multiple playlists
	pl1 := createTestPlaylistForSync(t, client, user, "spotify", true)
	createTestPlaylistTracksForSync(t, client, pl1)

	pl2, err := client.Playlist.Create().
		SetUser(user).
		SetRemoteID("remote-pl2").
		SetName("Playlist 2").
		SetSource("lastfm").
		SetSyncToNavidrome(true).
		SetTrackCount(2).
		Save(ctx)
	require.NoError(t, err)
	createTestPlaylistTracksForSync(t, client, pl2)

	// Create a playlist with sync disabled (should be skipped)
	_ = createTestPlaylistForSync(t, client, user, "spotify", false)

	// Create a Navidrome playlist (should be skipped)
	_, err = client.Playlist.Create().
		SetUser(user).
		SetRemoteID("nav-remote").
		SetName("Native Playlist").
		SetSource("navidrome").
		SetSyncToNavidrome(true). // Even though enabled, should be skipped
		Save(ctx)
	require.NoError(t, err)

	// Reset sync counter
	mockSyncer.syncedPlaylist = nil

	// Sync all enabled playlists
	err = svc.SyncAllEnabledPlaylists(ctx, user.ID)
	require.NoError(t, err)

	// Verify playlists were synced (check database for LastSyncedAt)
	syncedPlaylists, err := client.Playlist.Query().
		Where(
			playlist.SyncToNavidrome(true),
			playlist.SourceNEQ("navidrome"),
			playlist.LastSyncedAtNotNil(),
		).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, syncedPlaylists)
}

func TestPlaylistSyncService_GetPlaylistSyncStatus(t *testing.T) {
	client, svc, _, _ := setupPlaylistSyncService(t)
	ctx := context.Background()

	user := createTestUserWithNavidromeAuth(t, client)

	syncTime := time.Now().Add(-time.Hour)

	// Create a synced playlist
	pl, err := client.Playlist.Create().
		SetUser(user).
		SetRemoteID("remote-status").
		SetName("Status Test").
		SetSource("spotify").
		SetSyncToNavidrome(true).
		SetNavidromePlaylistID("nav-status-123").
		SetLastSyncedAt(syncTime).
		SetMatchedTrackCount(8).
		SetTrackCount(10).
		Save(ctx)
	require.NoError(t, err)

	// Get status
	status, err := svc.GetPlaylistSyncStatus(ctx, pl.ID)
	require.NoError(t, err)

	assert.True(t, status.SyncEnabled)
	assert.Equal(t, "nav-status-123", status.NavidromeID)
	assert.NotNil(t, status.LastSyncedAt)
	assert.Equal(t, 8, status.MatchedTracks)
	assert.Equal(t, 10, status.TotalTracks)
	assert.InDelta(t, 80.0, status.MatchPercentage, 0.1)
	assert.Empty(t, status.SyncError)
}

func TestPlaylistSyncService_GetPlaylistSyncStatus_WithError(t *testing.T) {
	client, svc, _, _ := setupPlaylistSyncService(t)
	ctx := context.Background()

	user := createTestUserWithNavidromeAuth(t, client)

	// Create a playlist with sync error
	pl, err := client.Playlist.Create().
		SetUser(user).
		SetRemoteID("remote-error").
		SetName("Error Test").
		SetSource("spotify").
		SetSyncToNavidrome(true).
		SetSyncError("Connection refused").
		SetTrackCount(5).
		Save(ctx)
	require.NoError(t, err)

	// Get status
	status, err := svc.GetPlaylistSyncStatus(ctx, pl.ID)
	require.NoError(t, err)

	assert.True(t, status.SyncEnabled)
	assert.Empty(t, status.NavidromeID)
	assert.Equal(t, "Connection refused", status.SyncError)
}

func TestPlaylistSyncService_SyncPlaylistToNavidrome_PlaylistNotFound(t *testing.T) {
	_, svc, _, _ := setupPlaylistSyncService(t)
	ctx := context.Background()

	// Try to sync a non-existent playlist
	err := svc.SyncPlaylistToNavidrome(ctx, 99999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load playlist")
}

func TestPlaylistSyncService_RebuildPlaylistSync_Success(t *testing.T) {
	client, svc, bus, mockSyncer := setupPlaylistSyncService(t)
	ctx := context.Background()

	user := createTestUserWithNavidromeAuth(t, client)
	notifCh, cleanup := bus.Subscribe(user.ID)
	defer cleanup()

	// Create Navidrome tracks in the library for matching
	createNavidromeTracksForMatching(t, client, user)

	// Create a playlist that was already synced
	pl, err := client.Playlist.Create().
		SetUser(user).
		SetRemoteID("remote-rebuild").
		SetName("Rebuild Test Playlist").
		SetSource("spotify").
		SetSyncToNavidrome(true).
		SetNavidromePlaylistID("old-nav-id").
		SetMatchedTrackCount(5).
		SetTrackCount(10).
		Save(ctx)
	require.NoError(t, err)

	createTestPlaylistTracksForSync(t, client, pl)

	// Rebuild the playlist sync
	err = svc.RebuildPlaylistSync(ctx, pl.ID)
	require.NoError(t, err)

	// Verify old playlist was deleted
	assert.Equal(t, "old-nav-id", mockSyncer.deletedID)

	// Verify new playlist was created (syncedPlaylist should be set)
	assert.NotNil(t, mockSyncer.syncedPlaylist)
	assert.Contains(t, mockSyncer.syncedPlaylist.Name, "Rebuild Test Playlist")

	// Verify database was updated with new ID
	updatedPl, err := client.Playlist.Get(ctx, pl.ID)
	require.NoError(t, err)
	assert.Equal(t, "nav-playlist-123", updatedPl.NavidromePlaylistID)
	assert.NotNil(t, updatedPl.LastSyncedAt)
	assert.Empty(t, updatedPl.SyncError)

	// Verify notifications were sent (rebuild warning + sync success)
	receivedRebuildNotification := false
	for i := 0; i < 5; i++ {
		select {
		case event := <-notifCh:
			assert.Equal(t, events.EventTypeNotification, event.Type)
			payload := event.Payload.(events.NotificationPayload)
			if payload.Title == "Rebuilding Playlist" {
				receivedRebuildNotification = true
			}
		case <-time.After(100 * time.Millisecond):
			break
		}
	}
	assert.True(t, receivedRebuildNotification, "Expected 'Rebuilding Playlist' notification")
}

func TestPlaylistSyncService_RebuildPlaylistSync_SyncDisabled(t *testing.T) {
	client, svc, _, _ := setupPlaylistSyncService(t)
	ctx := context.Background()

	user := createTestUserWithNavidromeAuth(t, client)

	// Create a playlist with sync disabled
	pl := createTestPlaylistForSync(t, client, user, "spotify", false)

	// Try to rebuild - should fail
	err := svc.RebuildPlaylistSync(ctx, pl.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sync is not enabled")
}

func TestPlaylistSyncService_RebuildPlaylistSync_NavidromePlaylist(t *testing.T) {
	client, svc, _, _ := setupPlaylistSyncService(t)
	ctx := context.Background()

	user := createTestUserWithNavidromeAuth(t, client)

	// Create a Navidrome playlist
	pl := createTestPlaylistForSync(t, client, user, "navidrome", true)

	// Try to rebuild - should fail
	err := svc.RebuildPlaylistSync(ctx, pl.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot rebuild Navidrome playlist")
}

func TestPlaylistSyncService_RebuildPlaylistSync_PlaylistNotFound(t *testing.T) {
	_, svc, _, _ := setupPlaylistSyncService(t)
	ctx := context.Background()

	// Try to rebuild a non-existent playlist
	err := svc.RebuildPlaylistSync(ctx, 99999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load playlist")
}

func TestPlaylistSyncService_RebuildPlaylistSync_NoExistingNavidromeID(t *testing.T) {
	client, svc, _, mockSyncer := setupPlaylistSyncService(t)
	ctx := context.Background()

	user := createTestUserWithNavidromeAuth(t, client)

	// Create a playlist with sync enabled but no Navidrome ID yet
	pl, err := client.Playlist.Create().
		SetUser(user).
		SetRemoteID("remote-rebuild-new").
		SetName("New Rebuild Playlist").
		SetSource("spotify").
		SetSyncToNavidrome(true).
		SetTrackCount(3).
		Save(ctx)
	require.NoError(t, err)

	createTestPlaylistTracksForSync(t, client, pl)

	// Rebuild should work (just creates new playlist)
	err = svc.RebuildPlaylistSync(ctx, pl.ID)
	require.NoError(t, err)

	// Verify delete was NOT called (no existing ID)
	assert.Empty(t, mockSyncer.deletedID)

	// Verify new playlist was created
	assert.NotNil(t, mockSyncer.syncedPlaylist)

	// Verify database was updated
	updatedPl, err := client.Playlist.Get(ctx, pl.ID)
	require.NoError(t, err)
	assert.Equal(t, "nav-playlist-123", updatedPl.NavidromePlaylistID)
}
