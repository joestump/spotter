package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"spotter/ent"
	"spotter/ent/dj"
	"spotter/ent/mixtape"
	"spotter/ent/playlist"
	"spotter/ent/playlisttrack"
	"spotter/ent/user"
	"spotter/internal/providers"
	"spotter/internal/vibes"
	"spotter/internal/views/components"
	"spotter/internal/views/playlists"

	"github.com/a-h/templ"
)

const (
	sourceNavidrome = "navidrome"

	// asyncShortTimeout is used for lightweight background operations.
	asyncShortTimeout = 2 * time.Minute
	// asyncDefaultTimeout is the standard timeout for background sync/generation tasks.
	asyncDefaultTimeout = 5 * time.Minute
)

func (h *Handler) Playlists(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUserRedirect(w, r)
	if u == nil {
		return
	}

	// Refresh user to get pagination settings
	u, err := h.Client.User.Query().
		Where(user.ID(u.ID)).
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to query user", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	pg := h.GetPaginationParams(r, u.PaginationSize)

	// Governing: SPEC-0015 REQ playlist-pairing — hide Navidrome-source playlists that are
	// managed by a paired non-Navidrome playlist (i.e. their remote_id appears as navidrome_playlist_id
	// on another playlist for the same user).
	managedNavidromeIDs, _ := h.Client.Playlist.Query().
		Where(
			playlist.HasUserWith(user.ID(u.ID)),
			playlist.NavidromePlaylistIDNEQ(""),
		).
		Select(playlist.FieldNavidromePlaylistID).
		Strings(r.Context())

	visibilityFilter := playlist.HasUserWith(user.ID(u.ID))
	var baseFilter func(*ent.PlaylistQuery) *ent.PlaylistQuery
	if len(managedNavidromeIDs) > 0 {
		baseFilter = func(q *ent.PlaylistQuery) *ent.PlaylistQuery {
			return q.Where(
				playlist.HasUserWith(user.ID(u.ID)),
				playlist.Not(
					playlist.And(
						playlist.Source(string(providers.TypeNavidrome)),
						playlist.RemoteIDIn(managedNavidromeIDs...),
					),
				),
			)
		}
	} else {
		baseFilter = func(q *ent.PlaylistQuery) *ent.PlaylistQuery {
			return q.Where(visibilityFilter)
		}
	}

	// Query playlists with pagination
	pls, err := baseFilter(h.Client.Playlist.Query()).
		Order(ent.Desc(playlist.FieldUpdatedAt)).
		Limit(pg.PageSize).
		Offset(pg.Offset).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to query playlists", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Get total count for pagination
	total, err := baseFilter(h.Client.Playlist.Query()).
		Count(r.Context())
	if err != nil {
		h.Logger.Error("failed to count playlists", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	pg.WithTotal(total)

	h.Render(w, r, playlists.Index(pls, pg.Page, pg.TotalPages, h.Config))
}

func (h *Handler) PlaylistShow(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUserRedirect(w, r)
	if u == nil {
		return
	}

	playlistID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	// Get the playlist
	pl, err := h.GetPlaylistForUser(r.Context(), playlistID, u.ID)
	if err != nil {
		h.Logger.Error("failed to get playlist", "error", err, "id", playlistID)
		http.Error(w, "Playlist not found", http.StatusNotFound)
		return
	}

	// Get tracks for this playlist from the playlist_tracks table
	playlistTracks, err := h.Client.PlaylistTrack.Query().
		Where(playlisttrack.HasPlaylistWith(playlist.ID(playlistID))).
		WithTrack(func(q *ent.TrackQuery) {
			q.WithArtist()
			q.WithAlbum(func(aq *ent.AlbumQuery) {
				aq.WithImages()
			})
		}).
		WithArtist().
		WithAlbum(func(q *ent.AlbumQuery) {
			q.WithImages()
		}).
		Order(ent.Asc(playlisttrack.FieldPosition)).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to get playlist tracks", "error", err)
		playlistTracks = []*ent.PlaylistTrack{}
	}

	// Convert to TrackTableRow for the component
	rows := h.playlistTracksToRows(playlistTracks)

	h.Render(w, r, playlists.Show(pl, rows, h.Config))
}

// playlistTracksToRows converts playlist tracks to TrackTableRow for the track table component
func (h *Handler) playlistTracksToRows(tracks []*ent.PlaylistTrack) []components.TrackTableRow {
	rows := make([]components.TrackTableRow, len(tracks))
	for i, pt := range tracks {
		row := components.TrackTableRow{
			Index:              i + 1,
			ExplicitTrackName:  pt.TrackName,
			ExplicitArtistName: pt.ArtistName,
			ExplicitAlbumName:  pt.AlbumName,
			ExplicitDurationMs: pt.DurationMs,
			LidarrBaseURL:      h.Config.Lidarr.BaseURL,
		}

		// If linked to catalog track, use enriched data
		if pt.Edges.Track != nil {
			row.Track = pt.Edges.Track
		}
		// If linked to catalog artist, set ID for linking
		if pt.Edges.Artist != nil {
			row.ExplicitArtistID = pt.Edges.Artist.ID
		}
		// If linked to catalog album, set ID for linking
		if pt.Edges.Album != nil {
			row.ExplicitAlbumID = pt.Edges.Album.ID
		}

		rows[i] = row
	}
	return rows
}

// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-050 (toggle-sync endpoint),
// REQ-PLSYNC-051 (async sync/removal in background goroutine)

// TogglePlaylistSync toggles the Navidrome sync status of a playlist
func (h *Handler) TogglePlaylistSync(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	playlistID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	h.Logger.Debug("toggle playlist sync requested",
		"playlist_id", playlistID,
		"user_id", u.ID,
		"username", u.Username)

	// Verify ownership and get current state
	pl, err := h.GetPlaylistForUser(r.Context(), playlistID, u.ID)
	if err != nil {
		h.Logger.Error("failed to get playlist for toggle sync",
			"playlist_id", playlistID,
			"error", err)
		http.Error(w, "Playlist not found", http.StatusNotFound)
		return
	}

	// Only allow toggling sync for non-Navidrome playlists
	if pl.Source == sourceNavidrome {
		h.Logger.Warn("attempted to toggle sync for Navidrome playlist",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		http.Error(w, "Cannot toggle sync for Navidrome playlists", http.StatusBadRequest)
		return
	}

	newSyncState := !pl.SyncToNavidrome

	// Governing: SPEC-0015 REQ playlist-pairing — enabling sync is persisted only after the
	// Navidrome name conflict is resolved. Persisting it first left the scheduler free to
	// create the duplicate playlist this flow exists to prevent, because the conflict card
	// offers only pair/rename: a user who simply closes it would otherwise leave an unpaired
	// playlist marked as syncing.
	if newSyncState && h.PlaylistSyncSvc != nil && pl.NavidromePlaylistID == "" {
		conflict, conflictErr := h.findNavidromeConflict(r.Context(), pl, u.ID)
		if conflictErr != nil {
			h.Logger.Warn("failed to check for Navidrome conflict",
				"playlist_id", playlistID,
				"error", conflictErr)
		} else if conflict != nil {
			h.Logger.Info("Navidrome conflict detected, showing conflict UI without enabling sync",
				"playlist_id", playlistID,
				"conflict_id", conflict.ID,
				"conflict_name", conflict.Name)
			h.Render(w, r, playlists.NavidromeConflict(pl, conflict))
			return
		}
	}

	updatedPlaylist, err := h.Client.Playlist.UpdateOne(pl).
		SetSyncToNavidrome(newSyncState).
		Save(r.Context())
	if err != nil {
		h.Logger.Error("failed to toggle playlist sync",
			"playlist_id", playlistID,
			"error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("toggled playlist sync",
		"playlist_id", playlistID,
		"playlist_name", pl.Name,
		"source", pl.Source,
		"sync_enabled", newSyncState,
		"user", u.Username,
	)

	// Trigger sync or removal based on new state
	// CRITICAL: Use a detached context with timeout since r.Context() is cancelled when response completes
	if h.PlaylistSyncSvc != nil {
		if newSyncState {
			// Sync enabled - trigger immediate sync to Navidrome (async)
			h.Logger.Debug("dispatching async sync to Navidrome",
				"playlist_id", playlistID,
				"playlist_name", pl.Name)

			go func() {
				// Use a new context with timeout since HTTP request context will be cancelled
				ctx, cancel := context.WithTimeout(context.Background(), asyncDefaultTimeout)
				defer cancel()

				h.Logger.Debug("starting async playlist sync",
					"playlist_id", playlistID)

				if err := h.PlaylistSyncSvc.SyncPlaylistToNavidrome(ctx, playlistID); err != nil {
					h.Logger.Error("failed to sync playlist to Navidrome",
						"playlist_id", playlistID,
						"playlist_name", pl.Name,
						"error", err)
				} else {
					h.Logger.Info("async playlist sync completed",
						"playlist_id", playlistID,
						"playlist_name", pl.Name)
				}
			}()
		} else {
			// Sync disabled - optionally remove from Navidrome (async)
			h.Logger.Debug("dispatching async removal from Navidrome",
				"playlist_id", playlistID,
				"playlist_name", pl.Name)

			go func() {
				// Use a new context with timeout since HTTP request context will be cancelled
				ctx, cancel := context.WithTimeout(context.Background(), asyncShortTimeout)
				defer cancel()

				h.Logger.Debug("starting async playlist removal",
					"playlist_id", playlistID)

				if err := h.PlaylistSyncSvc.RemovePlaylistFromNavidrome(ctx, playlistID); err != nil {
					h.Logger.Error("failed to remove playlist from Navidrome",
						"playlist_id", playlistID,
						"playlist_name", pl.Name,
						"error", err)
				} else {
					h.Logger.Info("async playlist removal completed",
						"playlist_id", playlistID,
						"playlist_name", pl.Name)
				}
			}()
		}
	} else {
		h.Logger.Warn("PlaylistSyncSvc is nil, cannot sync playlist",
			"playlist_id", playlistID)
	}

	// Return the updated sync dropdown component
	h.renderPlaylistSyncDropdown(w, r, updatedPlaylist)
}

// DebugPlaylistSync performs a synchronous playlist sync and returns detailed results as JSON
// This is useful for debugging sync issues without relying on async/UI feedback
func (h *Handler) DebugPlaylistSync(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	playlistID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	h.Logger.Info("debug playlist sync requested",
		"playlist_id", playlistID,
		"user_id", u.ID,
		"username", u.Username)

	// Verify ownership
	pl, err := h.GetPlaylistForUser(r.Context(), playlistID, u.ID)
	if err != nil {
		h.Logger.Error("failed to get playlist for debug sync",
			"playlist_id", playlistID,
			"error", err)
		h.RespondJSON(w, http.StatusNotFound, map[string]string{"error": "Playlist not found"})
		return
	}

	// Check if sync service is available
	if h.PlaylistSyncSvc == nil {
		h.Logger.Error("PlaylistSyncSvc is nil")
		h.RespondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Playlist sync service not configured"})
		return
	}

	// Use request context with extended timeout for debug endpoint
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	startTime := time.Now()

	// Perform synchronous sync
	syncErr := h.PlaylistSyncSvc.SyncPlaylistToNavidrome(ctx, playlistID)

	duration := time.Since(startTime)

	// Reload playlist to get updated state
	updatedPl, reloadErr := h.Client.Playlist.Query().
		Where(playlist.ID(playlistID)).
		Only(ctx)
	if reloadErr != nil {
		h.Logger.Warn("failed to reload playlist after sync", "error", reloadErr)
	}

	// Use sync error for response (reload error is just a warning)
	err = syncErr

	response := map[string]interface{}{
		"playlist_id":   playlistID,
		"playlist_name": pl.Name,
		"source":        pl.Source,
		"duration_ms":   duration.Milliseconds(),
		"sync_enabled":  pl.SyncToNavidrome,
	}

	if err != nil {
		response["success"] = false
		response["error"] = err.Error()
		h.Logger.Error("debug playlist sync failed",
			"playlist_id", playlistID,
			"playlist_name", pl.Name,
			"error", err,
			"duration", duration)
	} else {
		response["success"] = true
		if updatedPl != nil {
			response["navidrome_playlist_id"] = updatedPl.NavidromePlaylistID
			response["matched_track_count"] = updatedPl.MatchedTrackCount
			response["total_track_count"] = updatedPl.TrackCount
			response["last_synced_at"] = updatedPl.LastSyncedAt
			if updatedPl.SyncError != "" {
				response["sync_error"] = updatedPl.SyncError
			}
		}
		h.Logger.Info("debug playlist sync completed",
			"playlist_id", playlistID,
			"playlist_name", pl.Name,
			"duration", duration)
	}

	h.RespondJSON(w, http.StatusOK, response)
}

// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-050 (sync-progress endpoint)

// GetPlaylistSyncProgress returns the sync progress bar component for HTMX polling.
// GET /playlists/{id}/sync-progress
func (h *Handler) GetPlaylistSyncProgress(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	playlistID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	// Verify ownership and get current state
	pl, err := h.GetPlaylistForUser(r.Context(), playlistID, u.ID)
	if err != nil {
		http.Error(w, "Playlist not found", http.StatusNotFound)
		return
	}

	// Render the sync progress bar component
	config := components.SyncStatusConfig{
		EntityType:     "playlist",
		EntityID:       pl.ID,
		SyncEnabled:    pl.SyncToNavidrome,
		LastSyncedAt:   pl.LastSyncedAt,
		SyncError:      pl.SyncError,
		TargetProvider: "navidrome",
		MatchedTracks:  pl.MatchedTrackCount,
		TotalTracks:    pl.TrackCount,
		NavidromeID:    pl.NavidromePlaylistID,
	}

	component := components.SyncProgressBar(config)
	templ.Handler(component).ServeHTTP(w, r)
}

// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-050 (sync-status endpoint)

// GetPlaylistSyncStatus returns the current sync status for a playlist as JSON
func (h *Handler) GetPlaylistSyncStatus(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	playlistID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	// Verify ownership and get current state
	pl, err := h.GetPlaylistForUser(r.Context(), playlistID, u.ID)
	if err != nil {
		h.RespondJSON(w, http.StatusNotFound, map[string]string{"error": "Playlist not found"})
		return
	}

	response := map[string]interface{}{
		"playlist_id":           pl.ID,
		"playlist_name":         pl.Name,
		"source":                pl.Source,
		"sync_to_navidrome":     pl.SyncToNavidrome,
		"navidrome_playlist_id": pl.NavidromePlaylistID,
		"matched_track_count":   pl.MatchedTrackCount,
		"total_track_count":     pl.TrackCount,
		"last_synced_at":        pl.LastSyncedAt,
		"sync_error":            pl.SyncError,
	}

	if pl.TrackCount > 0 {
		response["match_percentage"] = float64(pl.MatchedTrackCount) / float64(pl.TrackCount) * 100
	}

	h.RespondJSON(w, http.StatusOK, response)
}

// findNavidromeConflict returns the Navidrome-source playlist with the same name
// as the given playlist, for the same user, if one exists and the playlist has no navidrome_playlist_id yet.
// Governing: SPEC-0015 REQ playlist-pairing
func (h *Handler) findNavidromeConflict(ctx context.Context, pl *ent.Playlist, userID int) (*ent.Playlist, error) {
	if pl.NavidromePlaylistID != "" {
		return nil, nil
	}
	conflict, err := h.Client.Playlist.Query().
		Where(
			playlist.HasUserWith(user.ID(userID)),
			playlist.Source(string(providers.TypeNavidrome)),
			playlist.Name(pl.Name),
		).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return conflict, nil
}

// ResolveNavidromeConflict handles the user's choice when a Navidrome conflict is detected.
// POST /playlists/{id}/resolve-navidrome-conflict
// Governing: SPEC-0015 REQ playlist-pairing
func (h *Handler) ResolveNavidromeConflict(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	playlistID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	action := r.FormValue("action")

	// Verify ownership
	pl, err := h.GetPlaylistForUser(r.Context(), playlistID, u.ID)
	if err != nil {
		h.Logger.Error("failed to get playlist for conflict resolution",
			"playlist_id", playlistID,
			"error", err)
		http.Error(w, "Playlist not found", http.StatusNotFound)
		return
	}

	switch action {
	case "pair":
		existingID := r.FormValue("existing_id")
		if existingID == "" {
			http.Error(w, "existing_id is required", http.StatusBadRequest)
			return
		}

		// Enable sync first so SyncPlaylistToNavidrome proceeds
		if !pl.SyncToNavidrome {
			pl, err = h.Client.Playlist.UpdateOne(pl).
				SetSyncToNavidrome(true).
				Save(r.Context())
			if err != nil {
				h.Logger.Error("failed to enable sync before pairing",
					"playlist_id", playlistID,
					"error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
		}

		if h.PlaylistSyncSvc != nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), asyncDefaultTimeout)
				defer cancel()
				if err := h.PlaylistSyncSvc.PairWithNavidrome(ctx, playlistID, existingID); err != nil {
					h.Logger.Error("failed to pair playlist with Navidrome",
						"playlist_id", playlistID,
						"existing_id", existingID,
						"error", err)
				}
			}()
		}

	case "new-name":
		name := r.FormValue("name")
		if name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}

		// Save custom name and enable sync
		pl, err = h.Client.Playlist.UpdateOne(pl).
			SetNavidromePlaylistName(name).
			SetSyncToNavidrome(true).
			Save(r.Context())
		if err != nil {
			h.Logger.Error("failed to save navidrome_playlist_name",
				"playlist_id", playlistID,
				"error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if h.PlaylistSyncSvc != nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), asyncDefaultTimeout)
				defer cancel()
				if err := h.PlaylistSyncSvc.SyncPlaylistToNavidrome(ctx, playlistID); err != nil {
					h.Logger.Error("failed to sync playlist after name resolution",
						"playlist_id", playlistID,
						"error", err)
				}
			}()
		}

	default:
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}

	// Reload and return the sync dropdown
	updatedPlaylist, err := h.Client.Playlist.Get(r.Context(), playlistID)
	if err != nil {
		updatedPlaylist = pl
	}
	h.renderPlaylistSyncDropdown(w, r, updatedPlaylist)
}

// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-050 (sync endpoint),
// REQ-PLSYNC-051 (async sync in background goroutine)

// SyncPlaylist triggers an immediate sync of a playlist to Navidrome.
// POST /playlists/{id}/sync
func (h *Handler) SyncPlaylist(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	playlistID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	h.Logger.Info("manual playlist sync requested",
		"playlist_id", playlistID,
		"user_id", u.ID,
		"username", u.Username)

	// Verify ownership and get current state
	pl, err := h.GetPlaylistForUser(r.Context(), playlistID, u.ID)
	if err != nil {
		h.Logger.Error("failed to get playlist for sync",
			"playlist_id", playlistID,
			"error", err)
		http.Error(w, "Playlist not found", http.StatusNotFound)
		return
	}

	// Only allow syncing non-Navidrome playlists with sync enabled
	if pl.Source == sourceNavidrome {
		h.Logger.Warn("attempted to sync Navidrome playlist",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		http.Error(w, "Cannot sync Navidrome playlists", http.StatusBadRequest)
		return
	}

	if !pl.SyncToNavidrome {
		h.Logger.Warn("attempted to sync playlist with sync disabled",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		http.Error(w, "Sync is not enabled for this playlist", http.StatusBadRequest)
		return
	}

	// Governing: SPEC-0015 REQ playlist-pairing — check for Navidrome conflict before sync
	if pl.NavidromePlaylistID == "" {
		conflict, conflictErr := h.findNavidromeConflict(r.Context(), pl, u.ID)
		if conflictErr != nil {
			h.Logger.Warn("failed to check for Navidrome conflict",
				"playlist_id", playlistID,
				"error", conflictErr)
		} else if conflict != nil {
			h.Logger.Info("Navidrome conflict detected, showing conflict UI",
				"playlist_id", playlistID,
				"conflict_id", conflict.ID,
				"conflict_name", conflict.Name)
			h.Render(w, r, playlists.NavidromeConflict(pl, conflict))
			return
		}
	}

	// Trigger sync (async)
	if h.PlaylistSyncSvc != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), asyncDefaultTimeout)
			defer cancel()

			h.Logger.Debug("starting async playlist sync",
				"playlist_id", playlistID)

			if err := h.PlaylistSyncSvc.SyncPlaylistToNavidrome(ctx, playlistID); err != nil {
				h.Logger.Error("manual playlist sync failed",
					"playlist_id", playlistID,
					"playlist_name", pl.Name,
					"error", err)
			} else {
				h.Logger.Info("manual playlist sync completed",
					"playlist_id", playlistID,
					"playlist_name", pl.Name,
					"duration", time.Since(startTime))
			}
		}()
	} else {
		h.Logger.Warn("PlaylistSyncSvc is nil, cannot sync playlist",
			"playlist_id", playlistID)
	}

	// Reload playlist to get latest state
	updatedPlaylist, err := h.Client.Playlist.Get(r.Context(), playlistID)
	if err != nil {
		h.Logger.Error("failed to reload playlist after sync trigger",
			"playlist_id", playlistID,
			"error", err)
		// Still return something reasonable
		updatedPlaylist = pl
	}

	h.Logger.Info("manual playlist sync triggered",
		"playlist_id", playlistID,
		"playlist_name", pl.Name,
		"user", u.Username)

	// Return the updated sync dropdown component
	h.renderPlaylistSyncDropdown(w, r, updatedPlaylist)
}

// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-050 (rebuild-sync endpoint),
// REQ-PLSYNC-051 (async rebuild in background goroutine)

// RebuildPlaylistSync clears and rebuilds the Navidrome playlist sync.
// POST /playlists/{id}/rebuild-sync
func (h *Handler) RebuildPlaylistSync(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	playlistID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	h.Logger.Info("playlist rebuild sync requested",
		"playlist_id", playlistID,
		"user_id", u.ID,
		"username", u.Username)

	// Verify ownership and get current state
	pl, err := h.GetPlaylistForUser(r.Context(), playlistID, u.ID)
	if err != nil {
		h.Logger.Error("failed to get playlist for rebuild",
			"playlist_id", playlistID,
			"error", err)
		http.Error(w, "Playlist not found", http.StatusNotFound)
		return
	}

	// Only allow rebuilding non-Navidrome playlists with sync enabled
	if pl.Source == sourceNavidrome {
		h.Logger.Warn("attempted to rebuild Navidrome playlist",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		http.Error(w, "Cannot rebuild Navidrome playlists", http.StatusBadRequest)
		return
	}

	if !pl.SyncToNavidrome {
		h.Logger.Warn("attempted to rebuild playlist with sync disabled",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		http.Error(w, "Sync is not enabled for this playlist", http.StatusBadRequest)
		return
	}

	// Trigger rebuild (async)
	if h.PlaylistSyncSvc != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), asyncDefaultTimeout)
			defer cancel()

			h.Logger.Debug("starting async playlist rebuild",
				"playlist_id", playlistID)

			if err := h.PlaylistSyncSvc.RebuildPlaylistSync(ctx, playlistID); err != nil {
				h.Logger.Error("playlist rebuild failed",
					"playlist_id", playlistID,
					"playlist_name", pl.Name,
					"error", err)
			} else {
				h.Logger.Info("playlist rebuild completed",
					"playlist_id", playlistID,
					"playlist_name", pl.Name,
					"duration", time.Since(startTime))
			}
		}()
	} else {
		h.Logger.Warn("PlaylistSyncSvc is nil, cannot rebuild playlist",
			"playlist_id", playlistID)
	}

	// Reload playlist to get latest state
	updatedPlaylist, err := h.Client.Playlist.Get(r.Context(), playlistID)
	if err != nil {
		h.Logger.Error("failed to reload playlist after rebuild trigger",
			"playlist_id", playlistID,
			"error", err)
		// Still return something reasonable
		updatedPlaylist = pl
	}

	h.Logger.Info("playlist rebuild triggered",
		"playlist_id", playlistID,
		"playlist_name", pl.Name,
		"user", u.Username)

	// Return the updated sync dropdown component
	h.renderPlaylistSyncDropdown(w, r, updatedPlaylist)
}

// renderPlaylistSyncDropdown renders the playlist sync dropdown component
func (h *Handler) renderPlaylistSyncDropdown(w http.ResponseWriter, r *http.Request, pl *ent.Playlist) {
	config := components.PlaylistSyncDropdownConfig{
		PlaylistID:      pl.ID,
		PlaylistName:    pl.Name,
		Source:          pl.Source,
		SyncToNavidrome: pl.SyncToNavidrome,
		NavidromeID:     pl.NavidromePlaylistID,
		LastSyncedAt:    pl.LastSyncedAt,
		MatchedTracks:   pl.MatchedTrackCount,
		TotalTracks:     pl.TrackCount,
		SyncError:       pl.SyncError,
	}

	component := components.PlaylistSyncDropdown(config)
	templ.Handler(component).ServeHTTP(w, r)
}

// PlaylistGenerateMetadata generates AI title and description for a playlist
func (h *Handler) PlaylistGenerateMetadata(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// PlaylistGenerateArtwork generates AI album art for a playlist
func (h *Handler) PlaylistGenerateArtwork(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// EnhanceVibesModal returns the modal content for enhancing a playlist with DJ vibes.
func (h *Handler) EnhanceVibesModal(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	playlistID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	// Verify ownership and get playlist
	pl, err := h.GetPlaylistForUser(r.Context(), playlistID, u.ID)
	if err != nil {
		http.Error(w, "Playlist not found", http.StatusNotFound)
		return
	}

	// Get user's DJs
	djs, err := h.Client.DJ.Query().
		Where(dj.HasUserWith(user.ID(u.ID))).
		Order(ent.Asc(dj.FieldName)).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to query DJs", "error", err)
		djs = []*ent.DJ{}
	}

	params := components.EnhanceVibesModalParams{
		PlaylistID:   pl.ID,
		PlaylistName: pl.Name,
		TrackCount:   pl.TrackCount,
		DJs:          djs,
	}

	h.Render(w, r, components.EnhanceVibesModalContent(params))
}

// EnhanceVibes enhances a playlist using a DJ persona.
func (h *Handler) EnhanceVibes(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	playlistID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	// Verify ownership
	pl, err := h.GetPlaylistForUser(r.Context(), playlistID, u.ID)
	if err != nil {
		http.Error(w, "Playlist not found", http.StatusNotFound)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	djID, err := strconv.Atoi(r.FormValue("dj_id"))
	if err != nil {
		http.Error(w, "DJ is required", http.StatusBadRequest)
		return
	}

	// Verify DJ ownership
	d, err := h.GetDJForUser(r.Context(), djID, u.ID)
	if err != nil {
		http.Error(w, "DJ not found", http.StatusNotFound)
		return
	}

	mode := vibes.EnhancementMode(r.FormValue("mode"))
	if mode != vibes.EnhancementModeOneTime && mode != vibes.EnhancementModeConvertToMixtape {
		mode = vibes.EnhancementModeOneTime
	}

	maxNewTracks := 5
	if maxStr := r.FormValue("max_new_tracks"); maxStr != "" {
		if mt, err := strconv.Atoi(maxStr); err == nil && mt >= 0 && mt <= 20 {
			maxNewTracks = mt
		}
	}

	h.Logger.Info("enhancing playlist vibes",
		"playlist_id", pl.ID,
		"playlist_name", pl.Name,
		"dj_id", d.ID,
		"dj_name", d.Name,
		"mode", mode,
		"max_new_tracks", maxNewTracks,
		"user_id", u.ID)

	// Check if the PlaylistEnhancer is available
	if h.PlaylistEnhancer == nil {
		h.Logger.Error("playlist enhancer not initialized")
		http.Error(w, "Playlist enhancement service is not available", http.StatusServiceUnavailable)
		return
	}

	// Create the enhancement request
	req := &vibes.EnhancementRequest{
		PlaylistID:   pl.ID,
		DJID:         d.ID,
		Mode:         mode,
		MaxNewTracks: maxNewTracks,
		UserID:       u.ID,
	}

	// Run enhancement asynchronously
	go func() {
		ctx := context.Background()

		// Publish enhancing event
		if h.Bus != nil {
			h.Bus.PublishPlaylistEnhancing(u.ID, pl.ID, pl.Name, d.Name)
		}

		result, err := h.PlaylistEnhancer.EnhancePlaylist(ctx, req)
		if err != nil {
			h.Logger.Error("playlist enhancement failed",
				"playlist_id", pl.ID,
				"error", err)

			if h.Bus != nil {
				h.Bus.PublishPlaylistEnhancementError(u.ID, pl.ID, err.Error())
			}
			return
		}

		h.Logger.Info("playlist enhancement complete",
			"playlist_id", pl.ID,
			"original_tracks", result.OriginalTrackCount,
			"final_tracks", result.FinalTrackCount,
			"tracks_added", result.TracksAdded)

		// Handle the result based on mode
		if mode == vibes.EnhancementModeConvertToMixtape {
			// Create a new Mixtape from this playlist
			h.convertPlaylistToMixtape(ctx, u, pl, d, result)
		} else {
			// Apply changes directly to Navidrome
			h.applyEnhancementToNavidrome(ctx, u, pl, result)
		}
	}()

	// Return immediately with "enhancing" status
	h.HTMXEvent(w, "playlist-enhancing", http.StatusAccepted)
}

// convertPlaylistToMixtape converts a playlist into a DJ-managed Mixtape.
func (h *Handler) convertPlaylistToMixtape(ctx context.Context, u *ent.User, pl *ent.Playlist, d *ent.DJ, result *vibes.EnhancementResult) {
	trackIDs := result.GetAllTrackIDsAsStrings()

	// Create the mixtape
	m, err := h.Client.Mixtape.Create().
		SetName(pl.Name + " (Enhanced)").
		SetDescription(result.EnhancementSummary).
		SetSchedule(mixtape.ScheduleNone).
		SetMaxTracks(len(trackIDs)).
		SetTrackIds(trackIDs).
		SetTrackCount(len(trackIDs)).
		SetSyncToNavidrome(pl.SyncToNavidrome || pl.Source == sourceNavidrome).
		SetNavidromePlaylistID(pl.NavidromePlaylistID).
		SetLastGeneratedAt(time.Now()).
		SetGenerationPrompt(result.PromptUsed).
		SetGenerationModel(result.ModelUsed).
		SetGenerationTokensUsed(result.TokensUsed).
		SetSeedType("playlist").
		SetSeedID(pl.ID).
		SetDj(d).
		SetUser(u).
		Save(ctx)

	if err != nil {
		h.Logger.Error("failed to create mixtape from playlist",
			"playlist_id", pl.ID,
			"error", err)

		if h.Bus != nil {
			h.Bus.PublishPlaylistEnhancementError(u.ID, pl.ID, "Failed to create mixtape: "+err.Error())
		}
		return
	}

	h.Logger.Info("converted playlist to mixtape",
		"playlist_id", pl.ID,
		"mixtape_id", m.ID,
		"track_count", len(trackIDs))

	// Publish success
	if h.Bus != nil {
		h.Bus.PublishNotification(u.ID,
			"Playlist Converted to Mixtape",
			pl.Name+" is now managed by "+d.Name,
			"success")
	}
}

// applyEnhancementToNavidrome applies the enhancement directly to Navidrome.
func (h *Handler) applyEnhancementToNavidrome(ctx context.Context, u *ent.User, pl *ent.Playlist, result *vibes.EnhancementResult) {
	// Only apply if the playlist has a Navidrome ID or is from Navidrome
	if pl.NavidromePlaylistID == "" && pl.Source != sourceNavidrome {
		h.Logger.Info("playlist not synced to Navidrome, skipping sync",
			"playlist_id", pl.ID)

		if h.Bus != nil {
			h.Bus.PublishNotification(u.ID,
				"Enhancement Complete",
				pl.Name+" enhanced (not synced to Navidrome)",
				"success")
		}
		return
	}

	// Use the PlaylistSyncService to update the playlist in Navidrome
	if h.PlaylistSyncSvc == nil {
		h.Logger.Warn("playlist sync service not available")
		return
	}

	// Get all track IDs in order
	trackIDs := result.GetAllTrackIDs()

	h.Logger.Info("applying enhancement to Navidrome",
		"playlist_id", pl.ID,
		"navidrome_id", pl.NavidromePlaylistID,
		"track_count", len(trackIDs))

	// Update the playlist tracks in the database
	// First, delete existing playlist tracks
	_, err := h.Client.PlaylistTrack.Delete().
		Where(playlisttrack.HasPlaylistWith(playlist.ID(pl.ID))).
		Exec(ctx)
	if err != nil {
		h.Logger.Error("failed to delete existing playlist tracks",
			"playlist_id", pl.ID,
			"error", err)
	}

	// Add new tracks in order
	for i, trackID := range trackIDs {
		track, err := h.Client.Track.Get(ctx, trackID)
		if err != nil {
			h.Logger.Warn("track not found, skipping",
				"track_id", trackID,
				"error", err)
			continue
		}

		// Get artist and album names
		artistName := ""
		albumName := ""
		if edges, err := track.Edges.ArtistOrErr(); err == nil && edges != nil {
			artistName = edges.Name
		}
		if edges, err := track.Edges.AlbumOrErr(); err == nil && edges != nil {
			albumName = edges.Name
		}

		// Get duration if available
		durationMs := 0
		if track.DurationMs != nil {
			durationMs = *track.DurationMs
		}

		_, err = h.Client.PlaylistTrack.Create().
			SetPlaylist(pl).
			SetTrack(track).
			SetPosition(i + 1).
			SetTrackName(track.Name).
			SetArtistName(artistName).
			SetAlbumName(albumName).
			SetDurationMs(durationMs).
			Save(ctx)
		if err != nil {
			h.Logger.Error("failed to create playlist track",
				"playlist_id", pl.ID,
				"track_id", trackID,
				"error", err)
		}
	}

	// Update playlist track count
	_, err = h.Client.Playlist.UpdateOne(pl).
		SetTrackCount(len(trackIDs)).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		h.Logger.Error("failed to update playlist track count",
			"playlist_id", pl.ID,
			"error", err)
	}

	// Trigger sync to Navidrome
	if pl.SyncToNavidrome || pl.Source == sourceNavidrome {
		go func() {
			if err := h.PlaylistSyncSvc.SyncPlaylistToNavidrome(ctx, pl.ID); err != nil {
				h.Logger.Error("failed to sync enhanced playlist to Navidrome",
					"playlist_id", pl.ID,
					"error", err)

				if h.Bus != nil {
					h.Bus.PublishNotification(u.ID,
						"Sync Failed",
						"Enhanced "+pl.Name+" but failed to sync: "+err.Error(),
						"warning")
				}
				return
			}

			if h.Bus != nil {
				h.Bus.PublishNotification(u.ID,
					"Enhancement Synced",
					pl.Name+" enhanced and synced to Navidrome",
					"success")
			}
		}()
	}
}
