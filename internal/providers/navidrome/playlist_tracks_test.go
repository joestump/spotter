package navidrome_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/providers"
	"spotter/internal/providers/navidrome"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPlaylistSyncTestProvider starts a Navidrome API stub whose getPlaylist.view answers
// with handler's choice, and reports every updatePlaylist.view call the provider makes.
func newPlaylistSyncTestProvider(t *testing.T, getPlaylistHandler func(w http.ResponseWriter, r *http.Request)) (providers.PlaylistSyncer, *int32) {
	t.Helper()

	var updateCalls int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/rest/getPlaylist.view":
			getPlaylistHandler(w, r)
		case "/rest/updatePlaylist.view":
			atomic.AddInt32(&updateCalls, 1)
			_, _ = io.WriteString(w, `{"subsonic-response":{"status":"ok"}}`)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)

	cfg := &config.Config{}
	cfg.Navidrome.BaseURL = ts.URL

	user := &ent.User{
		Username: "testuser",
		Edges: ent.UserEdges{
			NavidromeAuth: &ent.NavidromeAuth{Password: "password123"},
		},
	}

	provider, err := navidrome.New(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)(context.Background(), user)
	require.NoError(t, err)

	syncer, ok := provider.(providers.PlaylistSyncer)
	require.True(t, ok, "Provider should implement PlaylistSyncer")

	return syncer, &updateCalls
}

// A transient getPlaylist.view failure must not be read as "this playlist is empty": the
// update has to be aborted so the remote playlist keeps the tracks it already has.
// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-031, REQ-PLSYNC-013
func TestUpdatePlaylistTracks_AbortsWhenCurrentTracksUnavailable(t *testing.T) {
	syncer, updateCalls := newPlaylistSyncTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	})

	err := syncer.UpdatePlaylistTracks(context.Background(), "pl-1", []providers.Track{
		{ID: "t1", Name: "New Track", Artist: "Artist A", Album: "Album X"},
	})

	require.Error(t, err, "update must fail when current tracks cannot be read")
	assert.Zero(t, atomic.LoadInt32(updateCalls), "no updatePlaylist.view call may be made")
}

// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-031
func TestUpdatePlaylistTracks_AbortsOnUnreadableSubsonicPayload(t *testing.T) {
	syncer, updateCalls := newPlaylistSyncTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"subsonic-response":{"status":"failed"}}`)
	})

	err := syncer.UpdatePlaylistTracks(context.Background(), "pl-1", []providers.Track{
		{ID: "t1", Name: "New Track", Artist: "Artist A"},
	})

	require.Error(t, err)
	assert.Zero(t, atomic.LoadInt32(updateCalls))
}

// Once the read succeeds the update goes ahead and reports the tracks it wrote.
// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-031
func TestUpdatePlaylistTracks_SucceedsWhenCurrentTracksReadable(t *testing.T) {
	syncer, updateCalls := newPlaylistSyncTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"subsonic-response": map[string]any{
				"status": "ok",
				"playlist": map[string]any{
					"id": "pl-1",
					"entry": []map[string]any{
						{"id": "t1", "title": "Existing", "artist": "Artist A", "album": "Album X", "duration": 200},
					},
				},
			},
		}
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	})

	err := syncer.UpdatePlaylistTracks(context.Background(), "pl-1", []providers.Track{
		{ID: "t1", Name: "Existing", Artist: "Artist A", Album: "Album X"},
		{ID: "t2", Name: "New Track", Artist: "Artist B", Album: "Album Y"},
	})

	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(updateCalls))
}

// Importing playlists must not fail wholesale because one playlist's tracks were unreadable;
// it reports that playlist with no track list, which the importer then leaves untouched.
func TestGetPlaylists_KeepsGoingWhenPlaylistDetailsFail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/rest/getPlaylists.view":
			_, _ = io.WriteString(w, `{"subsonic-response":{"status":"ok","playlists":{"playlist":[
				{"id":"pl-1","name":"Outage Playlist","songCount":2}
			]}}}`)
		case "/rest/getPlaylist.view":
			http.Error(w, "internal error", http.StatusInternalServerError)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer ts.Close()

	cfg := &config.Config{}
	cfg.Navidrome.BaseURL = ts.URL

	user := &ent.User{
		Username: "testuser",
		Edges:    ent.UserEdges{NavidromeAuth: &ent.NavidromeAuth{Password: "password123"}},
	}

	provider, err := navidrome.New(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)(context.Background(), user)
	require.NoError(t, err)

	manager, ok := provider.(providers.PlaylistManager)
	require.True(t, ok)

	playlists, err := manager.GetPlaylists(context.Background())
	require.NoError(t, err, "one unreadable playlist must not fail the whole import")
	require.Len(t, playlists, 1)
	assert.Empty(t, playlists[0].Tracks, "playlist is reported without a track list")
	assert.Equal(t, 2, playlists[0].TrackCount, "the playlist still exists at the provider")
}
