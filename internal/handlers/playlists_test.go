package handlers_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"spotter/ent"
	"spotter/ent/playlist"
	"spotter/ent/user"
	"spotter/internal/auth"
	"spotter/internal/config"
	"spotter/internal/crypto"
	"spotter/internal/events"
	"spotter/internal/handlers"
	"spotter/internal/services"

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// playlistTestUserCounter ensures unique usernames across parallel tests
var playlistTestUserCounter int64

func uniquePlaylistTestUsername() string {
	return fmt.Sprintf("testuser_%d", atomic.AddInt64(&playlistTestUserCounter, 1))
}

func setupPlaylistHandler(t *testing.T) (*ent.Client, *handlers.Handler, *events.Bus) {
	client := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{}
	cfg.PlaylistSync.MinMatchConfidence = 0.8
	cfg.PlaylistSync.DeleteOnUnsync = false
	bus := events.NewBus()
	syncer := services.NewSyncer(client, cfg, logger, bus)
	playlistSyncSvc := services.NewPlaylistSyncService(client, cfg, logger, bus)
	encryptor, _ := crypto.NewEncryptor(make([]byte, 32))
	jwtManager := auth.NewJWTManager(testJWTSecret)
	h := handlers.New(client, cfg, logger, encryptor, jwtManager, syncer, nil, playlistSyncSvc, nil, nil, nil, bus)
	return client, h, bus
}

func TestPlaylists_EmptyState(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/playlists", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))
	w := httptest.NewRecorder()

	h.Playlists(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "No playlists found")
}

func TestPlaylists_WithData(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create test playlists
	_, err = client.Playlist.Create().
		SetUser(u).
		SetRemoteID("playlist1").
		SetName("My Favorites").
		SetDescription("Best tracks").
		SetSource("spotify").
		Save(context.Background())
	require.NoError(t, err)

	_, err = client.Playlist.Create().
		SetUser(u).
		SetRemoteID("playlist2").
		SetName("Chill Vibes").
		SetSource("navidrome").
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/playlists", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))
	w := httptest.NewRecorder()

	h.Playlists(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	assert.Contains(t, bodyStr, "My Favorites")
	assert.Contains(t, bodyStr, "Chill Vibes")
	assert.Contains(t, bodyStr, "Spotify")
	assert.Contains(t, bodyStr, "Navidrome")
}

func TestPlaylists_Pagination(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user with small page size
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(2).
		Save(context.Background())
	require.NoError(t, err)

	// Create 5 test playlists
	for i := 1; i <= 5; i++ {
		_, err = client.Playlist.Create().
			SetUser(u).
			SetRemoteID("playlist" + string(rune('0'+i))).
			SetName("Playlist " + string(rune('0'+i))).
			SetSource("navidrome").
			Save(context.Background())
		require.NoError(t, err)
	}

	// Test page 1
	req := httptest.NewRequest("GET", "/playlists?page=1", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))
	w := httptest.NewRecorder()

	h.Playlists(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Count playlists displayed (should only show 2 on page 1)
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// Should have pagination controls
	assert.Contains(t, bodyStr, "join")

	// Test page 2
	req = httptest.NewRequest("GET", "/playlists?page=2", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))
	w = httptest.NewRecorder()

	h.Playlists(w, req)

	resp = w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestPlaylists_UserIsolation(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create two test users
	u1, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	u2, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create playlist for user1
	_, err = client.Playlist.Create().
		SetUser(u1).
		SetRemoteID("playlist1").
		SetName("User1 Playlist").
		SetSource("spotify").
		Save(context.Background())
	require.NoError(t, err)

	// Create playlist for user2
	_, err = client.Playlist.Create().
		SetUser(u2).
		SetRemoteID("playlist2").
		SetName("User2 Playlist").
		SetSource("navidrome").
		Save(context.Background())
	require.NoError(t, err)

	// User1 should only see their playlist
	req := httptest.NewRequest("GET", "/playlists", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u1))
	w := httptest.NewRecorder()

	h.Playlists(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	assert.Contains(t, bodyStr, "User1 Playlist")
	assert.NotContains(t, bodyStr, "User2 Playlist")

	// Verify count
	count, err := client.Playlist.Query().
		Where(playlist.HasUserWith(user.ID(u1.ID))).
		Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestTogglePlaylistSync_EnableSync(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create a Spotify playlist with sync disabled
	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("spotify-123").
		SetName("My Spotify Playlist").
		SetSource("spotify").
		SetSyncToNavidrome(false).
		Save(context.Background())
	require.NoError(t, err)

	// Create request with chi URL params
	req := httptest.NewRequest("POST", "/playlists/"+string(rune(pl.ID+'0'))+"/toggle-sync", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	// Add chi URL params
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", string(rune(pl.ID+'0')))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.TogglePlaylistSync(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify sync was enabled
	updatedPl, err := client.Playlist.Get(context.Background(), pl.ID)
	require.NoError(t, err)
	assert.True(t, updatedPl.SyncToNavidrome)
}

func TestTogglePlaylistSync_DisableSync(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create a Spotify playlist with sync enabled
	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("spotify-456").
		SetName("Synced Playlist").
		SetSource("spotify").
		SetSyncToNavidrome(true).
		SetNavidromePlaylistID("nav-123").
		Save(context.Background())
	require.NoError(t, err)

	// Create request with chi URL params
	req := httptest.NewRequest("POST", "/playlists/"+string(rune(pl.ID+'0'))+"/toggle-sync", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", string(rune(pl.ID+'0')))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.TogglePlaylistSync(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify sync was disabled
	updatedPl, err := client.Playlist.Get(context.Background(), pl.ID)
	require.NoError(t, err)
	assert.False(t, updatedPl.SyncToNavidrome)
}

func TestTogglePlaylistSync_NavidromePlaylist(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create a Navidrome playlist
	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("nav-789").
		SetName("Native Navidrome Playlist").
		SetSource("navidrome").
		Save(context.Background())
	require.NoError(t, err)

	// Create request with chi URL params
	req := httptest.NewRequest("POST", "/playlists/"+string(rune(pl.ID+'0'))+"/toggle-sync", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", string(rune(pl.ID+'0')))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.TogglePlaylistSync(w, req)

	// Should return 400 Bad Request for Navidrome playlists
	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestTogglePlaylistSync_Unauthorized(t *testing.T) {
	_, h, _ := setupPlaylistHandler(t)

	// Create request without user context
	req := httptest.NewRequest("POST", "/playlists/1/toggle-sync", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.TogglePlaylistSync(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestTogglePlaylistSync_NotFound(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create request for non-existent playlist
	req := httptest.NewRequest("POST", "/playlists/99999/toggle-sync", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "99999")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.TogglePlaylistSync(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestSyncPlaylist_Success(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create a Spotify playlist with sync enabled
	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("spotify-sync-test").
		SetName("Sync Test Playlist").
		SetSource("spotify").
		SetSyncToNavidrome(true).
		Save(context.Background())
	require.NoError(t, err)

	// Create request
	req := httptest.NewRequest("POST", "/playlists/"+strconv.Itoa(pl.ID)+"/sync", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(pl.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.SyncPlaylist(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestSyncPlaylist_Unauthorized(t *testing.T) {
	_, h, _ := setupPlaylistHandler(t)

	// Create request without user context
	req := httptest.NewRequest("POST", "/playlists/1/sync", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.SyncPlaylist(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestSyncPlaylist_NotFound(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create request for non-existent playlist
	req := httptest.NewRequest("POST", "/playlists/99999/sync", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "99999")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.SyncPlaylist(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestSyncPlaylist_NavidromePlaylist(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create a Navidrome playlist
	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("nav-sync-test").
		SetName("Navidrome Playlist").
		SetSource("navidrome").
		Save(context.Background())
	require.NoError(t, err)

	// Create request
	req := httptest.NewRequest("POST", "/playlists/"+strconv.Itoa(pl.ID)+"/sync", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(pl.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.SyncPlaylist(w, req)

	// Should return 400 Bad Request for Navidrome playlists
	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestSyncPlaylist_SyncDisabled(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create a Spotify playlist with sync disabled
	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("spotify-no-sync").
		SetName("No Sync Playlist").
		SetSource("spotify").
		SetSyncToNavidrome(false).
		Save(context.Background())
	require.NoError(t, err)

	// Create request
	req := httptest.NewRequest("POST", "/playlists/"+strconv.Itoa(pl.ID)+"/sync", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(pl.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.SyncPlaylist(w, req)

	// Should return 400 Bad Request when sync is disabled
	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRebuildPlaylistSync_Success(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create a Spotify playlist with sync enabled
	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("spotify-rebuild-test").
		SetName("Rebuild Test Playlist").
		SetSource("spotify").
		SetSyncToNavidrome(true).
		SetNavidromePlaylistID("nav-123").
		Save(context.Background())
	require.NoError(t, err)

	// Create request
	req := httptest.NewRequest("POST", "/playlists/"+strconv.Itoa(pl.ID)+"/rebuild-sync", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(pl.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.RebuildPlaylistSync(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRebuildPlaylistSync_Unauthorized(t *testing.T) {
	_, h, _ := setupPlaylistHandler(t)

	// Create request without user context
	req := httptest.NewRequest("POST", "/playlists/1/rebuild-sync", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.RebuildPlaylistSync(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestRebuildPlaylistSync_NotFound(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create request for non-existent playlist
	req := httptest.NewRequest("POST", "/playlists/99999/rebuild-sync", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "99999")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.RebuildPlaylistSync(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestRebuildPlaylistSync_NavidromePlaylist(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create a Navidrome playlist
	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("nav-rebuild-test").
		SetName("Navidrome Playlist").
		SetSource("navidrome").
		Save(context.Background())
	require.NoError(t, err)

	// Create request
	req := httptest.NewRequest("POST", "/playlists/"+strconv.Itoa(pl.ID)+"/rebuild-sync", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(pl.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.RebuildPlaylistSync(w, req)

	// Should return 400 Bad Request for Navidrome playlists
	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRebuildPlaylistSync_SyncDisabled(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create a Spotify playlist with sync disabled
	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("spotify-rebuild-no-sync").
		SetName("No Sync Playlist").
		SetSource("spotify").
		SetSyncToNavidrome(false).
		Save(context.Background())
	require.NoError(t, err)

	// Create request
	req := httptest.NewRequest("POST", "/playlists/"+strconv.Itoa(pl.ID)+"/rebuild-sync", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(pl.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.RebuildPlaylistSync(w, req)

	// Should return 400 Bad Request when sync is disabled
	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestSyncState_ZeroMatchesReturnsWarning is a regression test for the "False Success" bug
// where playlists with 0 matched tracks incorrectly showed as Success (green) instead of Warning (orange).
// This test verifies that the sync state logic correctly identifies 0 matches as a Warning state.
func TestSyncState_ZeroMatchesReturnsWarning(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create a Spotify playlist with sync enabled and a NavidromePlaylistID (sync completed)
	// but with 0 matched tracks - this was the bug scenario
	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("spotify-zero-matches").
		SetName("Zero Matches Playlist").
		SetSource("spotify").
		SetSyncToNavidrome(true).
		SetNavidromePlaylistID("navidrome-playlist-id"). // Sync has completed
		SetTrackCount(30).                               // Has tracks
		SetMatchedTrackCount(0).                         // But none matched!
		Save(context.Background())
	require.NoError(t, err)

	// Verify the playlist was created with the expected state
	savedPl, err := client.Playlist.Query().
		Where(playlist.ID(pl.ID)).
		Only(context.Background())
	require.NoError(t, err)

	// Assert the database state that would trigger the bug
	assert.True(t, savedPl.SyncToNavidrome, "Sync should be enabled")
	assert.NotEmpty(t, savedPl.NavidromePlaylistID, "NavidromePlaylistID should be set (sync completed)")
	assert.Equal(t, 30, savedPl.TrackCount, "TrackCount should be 30")
	assert.Equal(t, 0, savedPl.MatchedTrackCount, "MatchedTrackCount should be 0")

	// Request the sync progress endpoint to verify the UI state
	req := httptest.NewRequest("GET", "/playlists/"+strconv.Itoa(pl.ID)+"/sync-progress", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(pl.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.GetPlaylistSyncProgress(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Read the response body to verify the UI contains warning indicators
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	bodyStr := string(body)

	// The response should contain warning styling, not success styling
	// The progress bar should use progress-warning class (orange) not progress-success (green)
	assert.Contains(t, bodyStr, "progress-warning",
		"Zero matches should render with progress-warning class, not progress-success (the False Success bug)")
	assert.NotContains(t, bodyStr, "progress-success",
		"Zero matches must NOT render with progress-success class")
}

// TestEnhanceVibesModal_Success tests that the enhance vibes modal is rendered correctly
func TestEnhanceVibesModal_Success(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	// Create a test user
	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	// Create a DJ for the user
	_, err = client.DJ.Create().
		SetName("Test DJ").
		SetUser(u).
		Save(context.Background())
	require.NoError(t, err)

	// Create a Navidrome playlist
	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("navidrome-playlist-1").
		SetName("Test Playlist").
		SetSource("navidrome").
		SetTrackCount(10).
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/playlists/"+strconv.Itoa(pl.ID)+"/enhance-vibes-modal", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(pl.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.EnhanceVibesModal(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// Verify modal content
	assert.Contains(t, bodyStr, "Enhance Vibes")
	assert.Contains(t, bodyStr, "Test Playlist")
	assert.Contains(t, bodyStr, "Test DJ")
	assert.Contains(t, bodyStr, "one_time")
	assert.Contains(t, bodyStr, "convert_to_mixtape")
}

// TestEnhanceVibesModal_Unauthorized tests that unauthorized users cannot access the modal
func TestEnhanceVibesModal_Unauthorized(t *testing.T) {
	_, h, _ := setupPlaylistHandler(t)

	req := httptest.NewRequest("GET", "/playlists/1/enhance-vibes-modal", nil)
	// No user context set

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.EnhanceVibesModal(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// TestEnhanceVibesModal_NotFound tests that the modal returns 404 for non-existent playlists
func TestEnhanceVibesModal_NotFound(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/playlists/99999/enhance-vibes-modal", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "99999")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.EnhanceVibesModal(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestEnhanceVibes_Unauthorized tests that unauthorized users cannot enhance playlists
func TestEnhanceVibes_Unauthorized(t *testing.T) {
	_, h, _ := setupPlaylistHandler(t)

	req := httptest.NewRequest("POST", "/playlists/1/enhance-vibes", nil)
	// No user context set

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.EnhanceVibes(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// TestEnhanceVibes_NotFound tests that enhancement returns 404 for non-existent playlists
func TestEnhanceVibes_NotFound(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/playlists/99999/enhance-vibes", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "99999")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.EnhanceVibes(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestEnhanceVibes_MissingDJ tests that enhancement fails when no DJ is provided
func TestEnhanceVibes_MissingDJ(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)

	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("navidrome-playlist-1").
		SetName("Test Playlist").
		SetSource("navidrome").
		SetTrackCount(10).
		Save(context.Background())
	require.NoError(t, err)

	form := "mode=one_time&max_new_tracks=5"
	req := httptest.NewRequest("POST", "/playlists/"+strconv.Itoa(pl.ID)+"/enhance-vibes", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(pl.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.EnhanceVibes(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestEnhanceVibes_ServiceUnavailable tests that enhancement fails gracefully when enhancer is nil
func TestEnhanceVibes_ServiceUnavailable(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)
	// Note: h.PlaylistEnhancer is nil by default in test setup

	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	dj, err := client.DJ.Create().
		SetName("Test DJ").
		SetUser(u).
		Save(context.Background())
	require.NoError(t, err)

	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("navidrome-playlist-1").
		SetName("Test Playlist").
		SetSource("navidrome").
		SetTrackCount(10).
		Save(context.Background())
	require.NoError(t, err)

	form := fmt.Sprintf("dj_id=%d&mode=one_time&max_new_tracks=5", dj.ID)
	req := httptest.NewRequest("POST", "/playlists/"+strconv.Itoa(pl.ID)+"/enhance-vibes", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(pl.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	h.EnhanceVibes(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}
