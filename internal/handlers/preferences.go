package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"spotter/ent"
	"spotter/ent/album"
	"spotter/ent/albumimage"
	"spotter/ent/artist"
	"spotter/ent/artistimage"
	"spotter/ent/listen"
	"spotter/ent/playlist"
	"spotter/ent/syncevent"
	"spotter/ent/track"
	"spotter/ent/user"
	"spotter/internal/events"
	"spotter/internal/providers"
	"spotter/internal/types"
	"spotter/internal/views/preferences"
)

// PreferencesRedirect redirects to the appearance preferences page
func (h *Handler) PreferencesRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/preferences/appearance", http.StatusSeeOther)
}

func (h *Handler) PreferencesAppearance(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	h.Render(w, r, preferences.Appearance(u, h.Config))
}

func (h *Handler) PostPreferencesAppearance(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	theme := r.FormValue("theme")
	paginationSizeStr := r.FormValue("pagination_size")

	// Validate theme is in available themes
	validTheme := false
	for _, t := range h.Config.AvailableThemes() {
		if t == theme {
			validTheme = true
			break
		}
	}
	if !validTheme {
		theme = h.Config.Theme.Default
	}

	updater := h.Client.User.UpdateOneID(u.ID).
		SetTheme(theme)

	// Update pagination size if provided
	if paginationSizeStr != "" {
		if paginationSize, err := strconv.Atoi(paginationSizeStr); err == nil && paginationSize >= 10 && paginationSize <= 100 {
			updater = updater.SetPaginationSize(paginationSize)
		}
	}

	err := updater.Exec(r.Context())

	if err != nil {
		h.Logger.Error("failed to update user preferences", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "preferences-saved")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) PreferencesProviders(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	// Refresh user to get all auth edges
	u, err := h.Client.User.Query().
		Where(user.ID(u.ID)).
		WithSpotifyAuth().
		WithLastfmAuth().
		WithNavidromeAuth().
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to query user preferences", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	h.Render(w, r, preferences.Providers(u, u.Edges.SpotifyAuth, u.Edges.LastfmAuth, u.Edges.NavidromeAuth, h.Config))
}

func (h *Handler) DisconnectSpotify(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	u, err := h.Client.User.Query().
		Where(user.ID(u.ID)).
		WithSpotifyAuth().
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to query user", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if u.Edges.SpotifyAuth != nil {
		if err := h.Client.SpotifyAuth.DeleteOne(u.Edges.SpotifyAuth).Exec(r.Context()); err != nil {
			h.Logger.Error("failed to delete spotify auth", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		h.Logger.Info("disconnected user from Spotify", "username", u.Username)
	}

	w.Header().Set("HX-Redirect", "/preferences/providers")
}

func (h *Handler) DisconnectLastFM(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	u, err := h.Client.User.Query().
		Where(user.ID(u.ID)).
		WithLastfmAuth().
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to query user", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if u.Edges.LastfmAuth != nil {
		if err := h.Client.LastFMAuth.DeleteOne(u.Edges.LastfmAuth).Exec(r.Context()); err != nil {
			h.Logger.Error("failed to delete lastfm auth", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		h.Logger.Info("disconnected user from Last.fm", "username", u.Username)
	}

	w.Header().Set("HX-Redirect", "/preferences/providers")
}

// SyncNavidrome triggers a sync for Navidrome data
// Governing: SPEC graceful-shutdown REQ "background goroutines must not capture *ent.User pointer"
// Governing: SPEC listen-playlist-sync REQ-SYNC-050 (on-demand sync via HTTP), REQ-SYNC-051 (returns immediately, sync in background)
func (h *Handler) SyncNavidrome(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Sync Started",
			Message:  "Syncing Navidrome data in the background...",
			IconType: "info",
		},
	})

	userID := u.ID
	go func() {
		ctx := context.Background()
		freshUser, err := h.Client.User.Get(ctx, userID)
		if err != nil {
			h.Logger.Error("failed to fetch user for navidrome sync", "error", err)
			return
		}
		if err := h.Syncer.SyncProvider(ctx, freshUser, providers.TypeNavidrome); err != nil {
			h.Logger.Error("failed to sync navidrome", "error", err)
		}
	}()

	w.WriteHeader(http.StatusOK)
}

// RebuildNavidrome deletes all Navidrome data and re-syncs
func (h *Handler) RebuildNavidrome(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()

	// Delete all listens from navidrome
	_, err := h.Client.Listen.Delete().
		Where(
			listen.HasUserWith(user.ID(u.ID)),
			listen.Source("navidrome"),
		).Exec(ctx)
	if err != nil {
		h.Logger.Error("failed to delete navidrome listens", "error", err)
		http.Error(w, "Failed to delete data", http.StatusInternalServerError)
		return
	}

	// Delete all playlists from navidrome
	_, err = h.Client.Playlist.Delete().
		Where(
			playlist.HasUserWith(user.ID(u.ID)),
			playlist.Source("navidrome"),
		).Exec(ctx)
	if err != nil {
		h.Logger.Error("failed to delete navidrome playlists", "error", err)
		http.Error(w, "Failed to delete data", http.StatusInternalServerError)
		return
	}

	// Delete all sync events from navidrome
	_, err = h.Client.SyncEvent.Delete().
		Where(
			syncevent.HasUserWith(user.ID(u.ID)),
			syncevent.Provider("navidrome"),
		).Exec(ctx)
	if err != nil {
		h.Logger.Error("failed to delete navidrome sync events", "error", err)
	}

	h.Logger.Info("deleted all navidrome data for user", "username", u.Username)

	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Rebuild Started",
			Message:  "Deleted Navidrome data. Re-syncing in the background...",
			IconType: "warning",
		},
	})

	userID := u.ID
	go func() {
		ctx := context.Background()
		freshUser, err := h.Client.User.Get(ctx, userID)
		if err != nil {
			h.Logger.Error("failed to fetch user for navidrome rebuild sync", "error", err)
			return
		}
		if err := h.Syncer.SyncProvider(ctx, freshUser, providers.TypeNavidrome); err != nil {
			h.Logger.Error("failed to sync navidrome after rebuild", "error", err)
		}
	}()

	w.WriteHeader(http.StatusOK)
}

// SyncSpotify triggers a sync for Spotify data
// Governing: SPEC graceful-shutdown REQ "background goroutines must not capture *ent.User pointer"
func (h *Handler) SyncSpotify(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Sync Started",
			Message:  "Syncing Spotify data in the background...",
			IconType: "info",
		},
	})

	userID := u.ID
	go func() {
		ctx := context.Background()
		freshUser, err := h.Client.User.Get(ctx, userID)
		if err != nil {
			h.Logger.Error("failed to fetch user for spotify sync", "error", err)
			return
		}
		if err := h.Syncer.SyncProvider(ctx, freshUser, providers.TypeSpotify); err != nil {
			h.Logger.Error("failed to sync spotify", "error", err)
		}
	}()

	w.WriteHeader(http.StatusOK)
}

// RebuildSpotify deletes all Spotify data and re-syncs
func (h *Handler) RebuildSpotify(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()

	// Delete all listens from spotify
	deleted, err := h.Client.Listen.Delete().
		Where(
			listen.HasUserWith(user.ID(u.ID)),
			listen.Source("spotify"),
		).Exec(ctx)
	if err != nil {
		h.Logger.Error("failed to delete spotify listens", "error", err)
		http.Error(w, "Failed to delete data", http.StatusInternalServerError)
		return
	}
	h.Logger.Info("deleted spotify listens", "count", deleted, "username", u.Username)

	// Delete all playlists from spotify
	deletedPlaylists, err := h.Client.Playlist.Delete().
		Where(
			playlist.HasUserWith(user.ID(u.ID)),
			playlist.Source("spotify"),
		).Exec(ctx)
	if err != nil {
		h.Logger.Error("failed to delete spotify playlists", "error", err)
		http.Error(w, "Failed to delete data", http.StatusInternalServerError)
		return
	}
	h.Logger.Info("deleted spotify playlists", "count", deletedPlaylists, "username", u.Username)

	// Delete all sync events from spotify
	_, err = h.Client.SyncEvent.Delete().
		Where(
			syncevent.HasUserWith(user.ID(u.ID)),
			syncevent.Provider("spotify"),
		).Exec(ctx)
	if err != nil {
		h.Logger.Error("failed to delete spotify sync events", "error", err)
	}

	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Rebuild Started",
			Message:  fmt.Sprintf("Deleted %d listens and %d playlists. Re-syncing...", deleted, deletedPlaylists),
			IconType: "warning",
		},
	})

	userID := u.ID
	go func() {
		ctx := context.Background()
		freshUser, err := h.Client.User.Get(ctx, userID)
		if err != nil {
			h.Logger.Error("failed to fetch user for spotify rebuild sync", "error", err)
			return
		}
		if err := h.Syncer.SyncProvider(ctx, freshUser, providers.TypeSpotify); err != nil {
			h.Logger.Error("failed to sync spotify after rebuild", "error", err)
		}
	}()

	w.WriteHeader(http.StatusOK)
}

// SyncLastFM triggers a sync for Last.fm data
// Governing: SPEC graceful-shutdown REQ "background goroutines must not capture *ent.User pointer"
func (h *Handler) SyncLastFM(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Sync Started",
			Message:  "Syncing Last.fm data in the background...",
			IconType: "info",
		},
	})

	userID := u.ID
	go func() {
		ctx := context.Background()
		freshUser, err := h.Client.User.Get(ctx, userID)
		if err != nil {
			h.Logger.Error("failed to fetch user for lastfm sync", "error", err)
			return
		}
		if err := h.Syncer.SyncProvider(ctx, freshUser, providers.TypeLastFM); err != nil {
			h.Logger.Error("failed to sync lastfm", "error", err)
		}
	}()

	w.WriteHeader(http.StatusOK)
}

// RebuildLastFM deletes all Last.fm data and re-syncs
func (h *Handler) RebuildLastFM(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()

	// Delete all listens from lastfm
	deleted, err := h.Client.Listen.Delete().
		Where(
			listen.HasUserWith(user.ID(u.ID)),
			listen.Source("lastfm"),
		).Exec(ctx)
	if err != nil {
		h.Logger.Error("failed to delete lastfm listens", "error", err)
		http.Error(w, "Failed to delete data", http.StatusInternalServerError)
		return
	}
	h.Logger.Info("deleted lastfm listens", "count", deleted, "username", u.Username)

	// Delete all sync events from lastfm
	_, err = h.Client.SyncEvent.Delete().
		Where(
			syncevent.HasUserWith(user.ID(u.ID)),
			syncevent.Provider("lastfm"),
		).Exec(ctx)
	if err != nil {
		h.Logger.Error("failed to delete lastfm sync events", "error", err)
	}

	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Rebuild Started",
			Message:  fmt.Sprintf("Deleted %d listens. Re-syncing...", deleted),
			IconType: "warning",
		},
	})

	userID := u.ID
	go func() {
		ctx := context.Background()
		freshUser, err := h.Client.User.Get(ctx, userID)
		if err != nil {
			h.Logger.Error("failed to fetch user for lastfm rebuild sync", "error", err)
			return
		}
		if err := h.Syncer.SyncProvider(ctx, freshUser, providers.TypeLastFM); err != nil {
			h.Logger.Error("failed to sync lastfm after rebuild", "error", err)
		}
	}()

	w.WriteHeader(http.StatusOK)
}

// PreferencesTasks shows the tasks management page with event history
func (h *Handler) PreferencesTasks(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()

	// Get filter parameters for event history
	selectedProvider := r.URL.Query().Get("provider")
	selectedEventType := r.URL.Query().Get("event_type")

	// Build query for events
	query := h.Client.SyncEvent.Query().
		Where(syncevent.HasUserWith(user.ID(u.ID))).
		Order(ent.Desc(syncevent.FieldCreatedAt)).
		Limit(500)

	// Apply provider filter
	if selectedProvider != "" {
		query = query.Where(syncevent.Provider(selectedProvider))
	}

	// Apply event type filter
	if selectedEventType != "" {
		query = query.Where(syncevent.EventTypeEQ(syncevent.EventType(selectedEventType)))
	}

	eventsList, err := query.All(ctx)
	if err != nil {
		h.Logger.Error("failed to query events", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Get last run times for each task type
	tasksList := h.getTasksWithLastRun(ctx, u)

	// Get distinct providers for filter dropdown
	providersList := []string{"spotify", "navidrome", "lastfm", "metadata", "system"}

	// Get all event types for filter dropdown
	eventTypes := []syncevent.EventType{
		syncevent.EventTypeSyncStarted,
		syncevent.EventTypeTrackAdded,
		syncevent.EventTypeTrackSkipped,
		syncevent.EventTypePlaylistAdded,
		syncevent.EventTypePlaylistSkipped,
		syncevent.EventTypeSyncCompleted,
		syncevent.EventTypeSyncFailed,
		// Playlist sync events (to Navidrome)
		syncevent.EventTypePlaylistSyncStarted,
		syncevent.EventTypePlaylistSyncCompleted,
		syncevent.EventTypePlaylistSyncFailed,
		syncevent.EventTypePlaylistSyncRemoved,
		// Metadata enrichment events
		syncevent.EventTypeMetadataStarted,
		syncevent.EventTypeMetadataCompleted,
		syncevent.EventTypeMetadataFailed,
		syncevent.EventTypeArtistEnriched,
		syncevent.EventTypeAlbumEnriched,
		syncevent.EventTypeTrackEnriched,
		syncevent.EventTypeImageDownloaded,
		syncevent.EventTypeCatalogBuilt,
		syncevent.EventTypeCleanupStarted,
		syncevent.EventTypeCleanupCompleted,
		syncevent.EventTypeDataReset,
	}

	h.Render(w, r, preferences.Tasks(u, tasksList, eventsList, selectedProvider, selectedEventType, providersList, eventTypes, h.Config))
}

// getTasksWithLastRun returns the list of tasks with their last run times
func (h *Handler) getTasksWithLastRun(ctx context.Context, u *ent.User) []types.Task {
	tasks := []types.Task{
		{
			ID:          "sync-listens",
			Name:        "Sync All Listens",
			Description: "Pull recent listening history from all connected providers",
		},
		{
			ID:          "sync-playlists",
			Name:        "Sync All Playlists",
			Description: "Pull playlist data from all connected providers",
		},
		{
			ID:          "enrich-metadata",
			Name:        "Run Metadata Enricher",
			Description: "Enrich artist, album, and track metadata from external sources",
		},
		{
			ID:          "sync-artist-images",
			Name:        "Sync All Artist Images",
			Description: "Re-fetch artist images from all connected providers",
		},
		{
			ID:          "sync-album-images",
			Name:        "Sync All Album Art",
			Description: "Re-fetch album artwork from all connected providers",
		},
		{
			ID:          "reset-data",
			Name:        "Reset All Data",
			Description: "Delete all listens, playlists, catalog data, and metadata, then re-sync",
		},
		{
			ID:          "cleanup",
			Name:        "Clear Caches & Cleanup",
			Description: "Delete old events and perform maintenance tasks",
		},
	}

	// Get last sync_completed event for listens
	lastListenSync, err := h.Client.SyncEvent.Query().
		Where(
			syncevent.HasUserWith(user.ID(u.ID)),
			syncevent.EventTypeEQ(syncevent.EventTypeSyncCompleted),
			syncevent.MessageContains("listens"),
		).
		Order(ent.Desc(syncevent.FieldCreatedAt)).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		h.Logger.Warn("failed to query last listen sync event", "error", err)
	}
	if lastListenSync != nil {
		tasks[0].LastRanAt = &lastListenSync.CreatedAt
	}

	// Get last sync_completed event for playlists
	lastPlaylistSync, err := h.Client.SyncEvent.Query().
		Where(
			syncevent.HasUserWith(user.ID(u.ID)),
			syncevent.EventTypeEQ(syncevent.EventTypeSyncCompleted),
			syncevent.MessageContains("playlist"),
		).
		Order(ent.Desc(syncevent.FieldCreatedAt)).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		h.Logger.Warn("failed to query last playlist sync event", "error", err)
	}
	if lastPlaylistSync != nil {
		tasks[1].LastRanAt = &lastPlaylistSync.CreatedAt
	}

	// Get last metadata_completed event
	lastMetadata, err := h.Client.SyncEvent.Query().
		Where(
			syncevent.HasUserWith(user.ID(u.ID)),
			syncevent.EventTypeEQ(syncevent.EventTypeMetadataCompleted),
		).
		Order(ent.Desc(syncevent.FieldCreatedAt)).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		h.Logger.Warn("failed to query last metadata event", "error", err)
	}
	if lastMetadata != nil {
		tasks[2].LastRanAt = &lastMetadata.CreatedAt
	}

	// Get last artist image sync event
	lastArtistImages, err := h.Client.SyncEvent.Query().
		Where(
			syncevent.HasUserWith(user.ID(u.ID)),
			syncevent.EventTypeEQ(syncevent.EventTypeImageDownloaded),
			syncevent.MessageContains("artist"),
		).
		Order(ent.Desc(syncevent.FieldCreatedAt)).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		h.Logger.Warn("failed to query last artist images event", "error", err)
	}
	if lastArtistImages != nil {
		tasks[3].LastRanAt = &lastArtistImages.CreatedAt
	}

	// Get last album image sync event
	lastAlbumImages, err := h.Client.SyncEvent.Query().
		Where(
			syncevent.HasUserWith(user.ID(u.ID)),
			syncevent.EventTypeEQ(syncevent.EventTypeImageDownloaded),
			syncevent.MessageContains("album"),
		).
		Order(ent.Desc(syncevent.FieldCreatedAt)).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		h.Logger.Warn("failed to query last album images event", "error", err)
	}
	if lastAlbumImages != nil {
		tasks[4].LastRanAt = &lastAlbumImages.CreatedAt
	}

	// Get last data_reset event
	lastReset, err := h.Client.SyncEvent.Query().
		Where(
			syncevent.HasUserWith(user.ID(u.ID)),
			syncevent.EventTypeEQ(syncevent.EventTypeDataReset),
		).
		Order(ent.Desc(syncevent.FieldCreatedAt)).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		h.Logger.Warn("failed to query last reset event", "error", err)
	}
	if lastReset != nil {
		tasks[5].LastRanAt = &lastReset.CreatedAt
	}

	// Get last cleanup_completed event
	lastCleanup, err := h.Client.SyncEvent.Query().
		Where(
			syncevent.HasUserWith(user.ID(u.ID)),
			syncevent.EventTypeEQ(syncevent.EventTypeCleanupCompleted),
		).
		Order(ent.Desc(syncevent.FieldCreatedAt)).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		h.Logger.Warn("failed to query last cleanup event", "error", err)
	}
	if lastCleanup != nil {
		tasks[6].LastRanAt = &lastCleanup.CreatedAt
	}

	return tasks
}

// TaskSyncListens triggers a sync of all listens
// Governing: SPEC graceful-shutdown REQ "background goroutines must not capture *ent.User pointer"
// Governing: SPEC listen-playlist-sync REQ-SYNC-050 (on-demand sync via HTTP), REQ-SYNC-051 (returns immediately, sync in background)
func (h *Handler) TaskSyncListens(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Task Started",
			Message:  "Syncing all listens in the background...",
			IconType: "info",
		},
	})

	userID := u.ID
	go func() {
		ctx := context.Background()
		freshUser, err := h.Client.User.Get(ctx, userID)
		if err != nil {
			h.Logger.Error("failed to fetch user for listen sync", "error", err)
			return
		}
		if err := h.Syncer.SyncRecentListens(ctx, freshUser); err != nil {
			h.Logger.Error("failed to sync listens", "error", err)
		}
	}()

	w.WriteHeader(http.StatusOK)
}

// Governing: SPEC listen-playlist-sync REQ-SYNC-050 (on-demand sync via HTTP), REQ-SYNC-051 (returns immediately, sync in background)
// TaskSyncPlaylists triggers a sync of all playlists
// Governing: SPEC graceful-shutdown REQ "background goroutines must not capture *ent.User pointer"
func (h *Handler) TaskSyncPlaylists(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Task Started",
			Message:  "Syncing all playlists in the background...",
			IconType: "info",
		},
	})

	userID := u.ID
	go func() {
		ctx := context.Background()
		freshUser, err := h.Client.User.Get(ctx, userID)
		if err != nil {
			h.Logger.Error("failed to fetch user for playlist sync", "error", err)
			return
		}
		if err := h.Syncer.SyncPlaylists(ctx, freshUser); err != nil {
			h.Logger.Error("failed to sync playlists", "error", err)
		}
	}()

	w.WriteHeader(http.StatusOK)
}

// TaskEnrichMetadata triggers metadata enrichment
func (h *Handler) TaskEnrichMetadata(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	if h.MetadataSvc == nil {
		h.Bus.Publish(u.ID, events.Event{
			Type: events.EventTypeNotification,
			Payload: events.NotificationPayload{
				Title:    "Error",
				Message:  "Metadata service is not configured",
				IconType: "error",
			},
		})
		w.WriteHeader(http.StatusOK)
		return
	}

	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Task Started",
			Message:  "Running metadata enrichment in the background...",
			IconType: "info",
		},
	})

	userID := u.ID
	go func() {
		ctx := context.Background()
		freshUser, err := h.Client.User.Get(ctx, userID)
		if err != nil {
			h.Logger.Error("failed to fetch user for metadata enrichment", "error", err)
			return
		}
		if err := h.MetadataSvc.SyncAll(ctx, freshUser); err != nil {
			h.Logger.Error("failed to run metadata enrichment", "error", err)
		}
	}()

	w.WriteHeader(http.StatusOK)
}

// TaskSyncArtistImages triggers a sync of all artist images
func (h *Handler) TaskSyncArtistImages(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	if h.MetadataSvc == nil {
		h.Bus.Publish(u.ID, events.Event{
			Type: events.EventTypeNotification,
			Payload: events.NotificationPayload{
				Title:    "Error",
				Message:  "Metadata service is not configured",
				IconType: "error",
			},
		})
		w.WriteHeader(http.StatusOK)
		return
	}

	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Task Started",
			Message:  "Syncing all artist images in the background...",
			IconType: "info",
		},
	})

	userID := u.ID
	go func() {
		ctx := context.Background()
		freshUser, err := h.Client.User.Get(ctx, userID)
		if err != nil {
			h.Logger.Error("failed to fetch user for artist image sync", "error", err)
			return
		}
		count, err := h.MetadataSvc.SyncAllArtistImages(ctx, freshUser)
		if err != nil {
			h.Logger.Error("failed to sync artist images", "error", err)
			h.Bus.Publish(userID, events.Event{
				Type: events.EventTypeNotification,
				Payload: events.NotificationPayload{
					Title:    "Sync Failed",
					Message:  "Failed to sync artist images",
					IconType: "error",
				},
			})
			return
		}
		h.Bus.Publish(userID, events.Event{
			Type: events.EventTypeNotification,
			Payload: events.NotificationPayload{
				Title:    "Sync Complete",
				Message:  fmt.Sprintf("Synced images for %d artists", count),
				IconType: "success",
			},
		})
	}()

	w.WriteHeader(http.StatusOK)
}

// TaskSyncAlbumImages triggers a sync of all album images
func (h *Handler) TaskSyncAlbumImages(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	if h.MetadataSvc == nil {
		h.Bus.Publish(u.ID, events.Event{
			Type: events.EventTypeNotification,
			Payload: events.NotificationPayload{
				Title:    "Error",
				Message:  "Metadata service is not configured",
				IconType: "error",
			},
		})
		w.WriteHeader(http.StatusOK)
		return
	}

	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Task Started",
			Message:  "Syncing all album artwork in the background...",
			IconType: "info",
		},
	})

	userID := u.ID
	go func() {
		ctx := context.Background()
		freshUser, err := h.Client.User.Get(ctx, userID)
		if err != nil {
			h.Logger.Error("failed to fetch user for album image sync", "error", err)
			return
		}
		count, err := h.MetadataSvc.SyncAllAlbumImages(ctx, freshUser)
		if err != nil {
			h.Logger.Error("failed to sync album images", "error", err)
			h.Bus.Publish(userID, events.Event{
				Type: events.EventTypeNotification,
				Payload: events.NotificationPayload{
					Title:    "Sync Failed",
					Message:  "Failed to sync album artwork",
					IconType: "error",
				},
			})
			return
		}
		h.Bus.Publish(userID, events.Event{
			Type: events.EventTypeNotification,
			Payload: events.NotificationPayload{
				Title:    "Sync Complete",
				Message:  fmt.Sprintf("Synced artwork for %d albums", count),
				IconType: "success",
			},
		})
	}()

	w.WriteHeader(http.StatusOK)
}

// TaskResetData deletes all user data and re-syncs
func (h *Handler) TaskResetData(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()

	// Log the start
	h.logEvent(ctx, u, syncevent.EventTypeDataReset, "system", "Starting full data reset", nil)

	// Delete all listens
	deletedListens, err := h.Client.Listen.Delete().
		Where(listen.HasUserWith(user.ID(u.ID))).
		Exec(ctx)
	if err != nil {
		h.Logger.Error("failed to delete listens", "error", err)
		http.Error(w, "Failed to delete data", http.StatusInternalServerError)
		return
	}

	// Delete all playlists
	deletedPlaylists, err := h.Client.Playlist.Delete().
		Where(playlist.HasUserWith(user.ID(u.ID))).
		Exec(ctx)
	if err != nil {
		h.Logger.Error("failed to delete playlists", "error", err)
		http.Error(w, "Failed to delete data", http.StatusInternalServerError)
		return
	}

	// Delete all tracks (cascade from artists/albums handled by foreign keys)
	deletedTracks, err := h.Client.Track.Delete().
		Where(track.HasArtistWith(artist.HasUserWith(user.ID(u.ID)))).
		Exec(ctx)
	if err != nil {
		h.Logger.Error("failed to delete tracks", "error", err)
	}

	// Delete all album images
	if _, err := h.Client.AlbumImage.Delete().
		Where(albumimage.HasAlbumWith(album.HasUserWith(user.ID(u.ID)))).
		Exec(ctx); err != nil {
		h.Logger.Error("failed to delete album images", "error", err)
	}

	// Delete all albums
	deletedAlbums, err := h.Client.Album.Delete().
		Where(album.HasUserWith(user.ID(u.ID))).
		Exec(ctx)
	if err != nil {
		h.Logger.Error("failed to delete albums", "error", err)
	}

	// Delete all artist images
	if _, err := h.Client.ArtistImage.Delete().
		Where(artistimage.HasArtistWith(artist.HasUserWith(user.ID(u.ID)))).
		Exec(ctx); err != nil {
		h.Logger.Error("failed to delete artist images", "error", err)
	}

	// Delete all artists
	deletedArtists, err := h.Client.Artist.Delete().
		Where(artist.HasUserWith(user.ID(u.ID))).
		Exec(ctx)
	if err != nil {
		h.Logger.Error("failed to delete artists", "error", err)
	}

	h.Logger.Info("reset all data for user",
		"username", u.Username,
		"listens", deletedListens,
		"playlists", deletedPlaylists,
		"tracks", deletedTracks,
		"albums", deletedAlbums,
		"artists", deletedArtists)

	h.logEvent(ctx, u, syncevent.EventTypeDataReset, "system",
		fmt.Sprintf("Data reset complete: %d listens, %d playlists, %d tracks, %d albums, %d artists deleted",
			deletedListens, deletedPlaylists, deletedTracks, deletedAlbums, deletedArtists),
		map[string]interface{}{
			"listens":   deletedListens,
			"playlists": deletedPlaylists,
			"tracks":    deletedTracks,
			"albums":    deletedAlbums,
			"artists":   deletedArtists,
		})

	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Data Reset Complete",
			Message:  fmt.Sprintf("Deleted %d listens, %d playlists. Re-syncing...", deletedListens, deletedPlaylists),
			IconType: "warning",
		},
	})

	// Re-sync everything in the background
	// Governing: SPEC graceful-shutdown REQ "background goroutines must not capture *ent.User pointer"
	userID := u.ID
	go func() {
		bgCtx := context.Background()
		freshUser, err := h.Client.User.Get(bgCtx, userID)
		if err != nil {
			h.Logger.Error("failed to fetch user for reset sync", "error", err)
			return
		}
		if err := h.Syncer.Sync(bgCtx, freshUser); err != nil {
			h.Logger.Error("failed to sync after reset", "error", err)
		}
		// Also run metadata enrichment if available
		if h.MetadataSvc != nil {
			if err := h.MetadataSvc.SyncAll(bgCtx, freshUser); err != nil {
				h.Logger.Error("failed to run metadata after reset", "error", err)
			}
		}
	}()

	w.WriteHeader(http.StatusOK)
}

// TaskCleanup runs cleanup tasks (delete old events, etc.)
func (h *Handler) TaskCleanup(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()

	h.logEvent(ctx, u, syncevent.EventTypeCleanupStarted, "system", "Starting cleanup tasks", nil)

	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Cleanup Started",
			Message:  "Running cleanup tasks...",
			IconType: "info",
		},
	})

	// Governing: SPEC graceful-shutdown REQ "background goroutines must not capture *ent.User pointer"
	userID := u.ID
	go func() {
		bgCtx := context.Background()
		freshUser, err := h.Client.User.Get(bgCtx, userID)
		if err != nil {
			h.Logger.Error("failed to fetch user for cleanup", "error", err)
			return
		}

		// Delete events older than 30 days
		cutoff := time.Now().AddDate(0, 0, -30)
		deleted, err := h.Client.SyncEvent.Delete().
			Where(
				syncevent.HasUserWith(user.ID(userID)),
				syncevent.CreatedAtLT(cutoff),
			).
			Exec(bgCtx)

		if err != nil {
			h.Logger.Error("failed to delete old events", "error", err)
			h.Bus.Publish(userID, events.Event{
				Type: events.EventTypeNotification,
				Payload: events.NotificationPayload{
					Title:    "Cleanup Failed",
					Message:  "Failed to delete old events",
					IconType: "error",
				},
			})
			return
		}

		h.Logger.Info("cleanup completed", "username", freshUser.Username, "events_deleted", deleted)

		h.logEvent(bgCtx, freshUser, syncevent.EventTypeCleanupCompleted, "system",
			fmt.Sprintf("Cleanup completed: deleted %d events older than 30 days", deleted),
			map[string]interface{}{"events_deleted": deleted})

		h.Bus.Publish(userID, events.Event{
			Type: events.EventTypeNotification,
			Payload: events.NotificationPayload{
				Title:    "Cleanup Complete",
				Message:  fmt.Sprintf("Deleted %d old events", deleted),
				IconType: "success",
			},
		})
	}()

	w.WriteHeader(http.StatusOK)
}

// logEvent persists a sync event to the database
func (h *Handler) logEvent(ctx context.Context, u *ent.User, eventType syncevent.EventType, provider string, message string, metadata map[string]interface{}) {
	builder := h.Client.SyncEvent.Create().
		SetUser(u).
		SetEventType(eventType).
		SetProvider(provider).
		SetMessage(message)

	if metadata != nil {
		if metadataJSON, err := encodeMetadata(metadata); err == nil {
			builder.SetMetadata(metadataJSON)
		}
	}

	if _, err := builder.Save(ctx); err != nil {
		h.Logger.Warn("failed to log sync event", "event_type", eventType, "provider", provider, "error", err)
	}
}

func encodeMetadata(metadata map[string]interface{}) (string, error) {
	data, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
