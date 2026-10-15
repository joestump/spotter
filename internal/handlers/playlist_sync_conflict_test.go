package handlers_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"spotter/ent"
	"spotter/internal/handlers"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// togglePlaylistSync calls the toggle endpoint for a playlist owned by u.
func togglePlaylistSync(t *testing.T, h *handlers.Handler, u *ent.User, pl *ent.Playlist) *http.Response {
	t.Helper()

	req := httptest.NewRequest("POST", "/playlists/"+strconv.Itoa(pl.ID)+"/toggle-sync", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(pl.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.TogglePlaylistSync(w, req)
	return w.Result()
}

// Enabling sync on a playlist whose name already exists as a Navidrome playlist must show the
// conflict card without persisting the flag: the card offers only pair or rename, so a user who
// closes it instead must not leave an unpaired playlist that the scheduler then duplicates on
// Navidrome.
// Governing: SPEC-0015 REQ playlist-pairing
func TestTogglePlaylistSync_ConflictKeepsSyncDisabledUntilResolved(t *testing.T) {
	client, h, _ := setupPlaylistHandler(t)
	ctx := context.Background()

	u, err := client.User.Create().
		SetUsername(uniquePlaylistTestUsername()).
		SetPaginationSize(25).
		Save(ctx)
	require.NoError(t, err)

	spotterPL, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("spotify-conflict-1").
		SetName("Road Trip").
		SetSource("spotify").
		SetSyncToNavidrome(false).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.Playlist.Create().
		SetUser(u).
		SetRemoteID("navidrome-conflict-1").
		SetName("Road Trip").
		SetSource("navidrome").
		Save(ctx)
	require.NoError(t, err)

	resp := togglePlaylistSync(t, h, u, spotterPL)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body := new(strings.Builder)
	_, err = io.Copy(body, resp.Body)
	require.NoError(t, err)
	assert.Contains(t, body.String(), "Navidrome conflict detected")

	stored, err := client.Playlist.Get(ctx, spotterPL.ID)
	require.NoError(t, err)
	assert.False(t, stored.SyncToNavidrome,
		"dismissed conflict must leave sync disabled; only resolving it enables sync")
	assert.Empty(t, stored.NavidromePlaylistID)
}
