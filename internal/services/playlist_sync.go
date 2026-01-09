package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"spotter/ent"
	"spotter/ent/playlist"
	"spotter/ent/playlisttrack"
	"spotter/ent/syncevent"
	"spotter/ent/user"
	"spotter/internal/config"
	"spotter/internal/events"
	"spotter/internal/providers"
)

// PlaylistSyncService handles syncing playlists to Navidrome.
type PlaylistSyncService struct {
	Client       *ent.Client
	Config       *config.Config
	Logger       *slog.Logger
	Bus          *events.Bus
	TrackMatcher *TrackMatcher
	Factories    []providers.Factory
}

// NewPlaylistSyncService creates a new PlaylistSyncService.
func NewPlaylistSyncService(
	client *ent.Client,
	cfg *config.Config,
	logger *slog.Logger,
	bus *events.Bus,
) *PlaylistSyncService {
	trackMatcher := NewTrackMatcher(client, logger, cfg.PlaylistSync.MinMatchConfidence)
	return &PlaylistSyncService{
		Client:       client,
		Config:       cfg,
		Logger:       logger,
		Bus:          bus,
		TrackMatcher: trackMatcher,
		Factories:    make([]providers.Factory, 0),
	}
}

// Register adds a provider factory to the service.
func (s *PlaylistSyncService) Register(factory providers.Factory) {
	s.Factories = append(s.Factories, factory)
	s.Logger.Debug("registered provider factory for playlist sync")
}

// SyncPlaylistToNavidrome syncs a single playlist to Navidrome.
// Called when user enables sync or during scheduled sync.
func (s *PlaylistSyncService) SyncPlaylistToNavidrome(ctx context.Context, playlistID int) error {
	startTime := time.Now()

	s.Logger.Info("starting playlist sync to Navidrome",
		"playlist_id", playlistID)

	// Load playlist with user
	pl, err := s.Client.Playlist.Query().
		Where(playlist.ID(playlistID)).
		WithUser().
		Only(ctx)
	if err != nil {
		s.Logger.Error("failed to load playlist",
			"playlist_id", playlistID,
			"error", err)
		return fmt.Errorf("failed to load playlist: %w", err)
	}

	// Verify sync is enabled
	if !pl.SyncToNavidrome {
		s.Logger.Debug("sync not enabled for playlist, skipping",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return nil
	}

	// Ensure playlist is not from Navidrome
	if pl.Source == "navidrome" {
		s.Logger.Warn("attempted to sync Navidrome playlist to Navidrome",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return fmt.Errorf("cannot sync Navidrome playlist to Navidrome")
	}

	u := pl.Edges.User
	if u == nil {
		s.Logger.Error("playlist has no associated user",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return fmt.Errorf("playlist has no associated user")
	}

	s.Logger.Info("syncing playlist to Navidrome",
		"playlist_id", playlistID,
		"playlist_name", pl.Name,
		"source", pl.Source,
		"user_id", u.ID,
		"user", u.Username)

	// Log sync started event
	s.logEvent(ctx, u, syncevent.EventTypePlaylistSyncStarted, "navidrome",
		fmt.Sprintf("Starting sync of playlist '%s' to Navidrome", pl.Name),
		map[string]interface{}{
			"playlist_id":   pl.ID,
			"playlist_name": pl.Name,
			"source":        pl.Source,
		})

	// Publish "sync starting" notification to UI
	s.publishNotification(u.ID, "Syncing Playlist",
		fmt.Sprintf("Syncing '%s' to Navidrome...", pl.Name),
		"info")

	// Get Navidrome provider - this will reload user with NavidromeAuth edge
	navidromeProvider, err := s.getNavidromeProvider(ctx, u)
	if err != nil {
		return s.handleSyncError(ctx, pl, u, fmt.Errorf("failed to get Navidrome provider: %w", err))
	}

	s.Logger.Debug("obtained Navidrome provider",
		"playlist_id", playlistID,
		"provider_type", navidromeProvider.Type())

	syncer, ok := navidromeProvider.(providers.PlaylistSyncer)
	if !ok {
		return s.handleSyncError(ctx, pl, u, fmt.Errorf("Navidrome provider does not implement PlaylistSyncer"))
	}

	// Load playlist tracks
	s.Logger.Debug("loading playlist tracks",
		"playlist_id", playlistID)

	playlistTracks, err := s.Client.PlaylistTrack.Query().
		Where(playlisttrack.HasPlaylistWith(playlist.ID(playlistID))).
		Order(ent.Asc(playlisttrack.FieldPosition)).
		All(ctx)
	if err != nil {
		return s.handleSyncError(ctx, pl, u, fmt.Errorf("failed to load playlist tracks: %w", err))
	}

	s.Logger.Debug("loaded playlist tracks",
		"playlist_id", playlistID,
		"track_count", len(playlistTracks))

	// Convert to provider tracks for matching
	sourceTracks := make([]providers.Track, len(playlistTracks))
	for i, pt := range playlistTracks {
		sourceTracks[i] = providers.Track{
			ID:     pt.RemoteID,
			Name:   pt.TrackName,
			Artist: pt.ArtistName,
			Album:  pt.AlbumName,
		}
		if pt.DurationMs != nil {
			sourceTracks[i].DurationMs = *pt.DurationMs
		}
		// Include ISRC if available (for better matching)
		if pt.Isrc != nil {
			sourceTracks[i].ISRC = *pt.Isrc
		}
	}

	// Match tracks to Navidrome library
	s.Logger.Debug("starting track matching",
		"playlist_id", playlistID,
		"source_track_count", len(sourceTracks))

	matchResults, err := s.TrackMatcher.MatchTracks(ctx, u.ID, sourceTracks)
	if err != nil {
		return s.handleSyncError(ctx, pl, u, fmt.Errorf("failed to match tracks: %w", err))
	}

	// Filter to only matched tracks (we can only add tracks that exist in Navidrome)
	var matchedTracks []providers.Track
	matchedCount := 0
	unmatchedCount := 0
	for _, result := range matchResults {
		if result.NavidromeTrackID != "" {
			matchedTracks = append(matchedTracks, providers.Track{
				ID:     result.NavidromeTrackID,
				Name:   result.SourceTrack.Name,
				Artist: result.SourceTrack.Artist,
				Album:  result.SourceTrack.Album,
			})
			matchedCount++
		} else {
			unmatchedCount++
		}
	}

	s.Logger.Info("track matching complete",
		"playlist_id", playlistID,
		"playlist_name", pl.Name,
		"total_tracks", len(sourceTracks),
		"matched_tracks", matchedCount,
		"unmatched_tracks", unmatchedCount,
		"match_rate", fmt.Sprintf("%.1f%%", float64(matchedCount)/float64(len(sourceTracks))*100))

	// Decide what to do with the playlist
	var navidromePlaylistID string

	if pl.NavidromePlaylistID != "" {
		// Update existing playlist
		s.Logger.Debug("updating existing Navidrome playlist",
			"playlist_id", playlistID,
			"navidrome_playlist_id", pl.NavidromePlaylistID,
			"track_count", len(matchedTracks))

		err = syncer.UpdatePlaylistTracks(ctx, pl.NavidromePlaylistID, matchedTracks)
		if err != nil {
			return s.handleSyncError(ctx, pl, u, fmt.Errorf("failed to update playlist tracks: %w", err))
		}
		navidromePlaylistID = pl.NavidromePlaylistID

		s.Logger.Info("updated existing Navidrome playlist",
			"playlist_id", playlistID,
			"navidrome_playlist_id", navidromePlaylistID,
			"track_count", len(matchedTracks))
	} else {
		// Create new playlist
		s.Logger.Debug("creating new Navidrome playlist",
			"playlist_id", playlistID,
			"playlist_name", pl.Name,
			"track_count", len(matchedTracks))

		navidromePlaylistID, err = syncer.SyncPlaylist(ctx, providers.SyncPlaylistRequest{
			Name:        pl.Name,
			Description: pl.Description,
			ImageURL:    pl.ImageURL,
			Tracks:      matchedTracks,
		})
		if err != nil {
			return s.handleSyncError(ctx, pl, u, fmt.Errorf("failed to create playlist: %w", err))
		}

		s.Logger.Info("created new Navidrome playlist",
			"playlist_id", playlistID,
			"navidrome_playlist_id", navidromePlaylistID,
			"track_count", len(matchedTracks))
	}

	// Update database with sync info
	now := time.Now()
	update := s.Client.Playlist.UpdateOne(pl).
		SetNavidromePlaylistID(navidromePlaylistID).
		SetLastSyncedAt(now).
		SetMatchedTrackCount(matchedCount)

	// Only clear sync error if at least one track matched (SRV-PS-009)
	if matchedCount > 0 {
		update = update.ClearSyncError()
	} else {
		update = update.SetSyncError("No tracks matched - playlist may be empty or library mismatch")
	}

	_, err = update.Save(ctx)
	if err != nil {
		s.Logger.Error("failed to update playlist sync info",
			"playlist_id", playlistID,
			"error", err)
		return err
	}

	duration := time.Since(startTime)

	// Log sync completed event
	s.logEvent(ctx, u, syncevent.EventTypePlaylistSyncCompleted, "navidrome",
		fmt.Sprintf("Synced playlist '%s' to Navidrome (%d/%d tracks matched)", pl.Name, matchedCount, len(sourceTracks)),
		map[string]interface{}{
			"playlist_id":           pl.ID,
			"playlist_name":         pl.Name,
			"navidrome_playlist_id": navidromePlaylistID,
			"total_tracks":          len(sourceTracks),
			"matched_tracks":        matchedCount,
			"unmatched_tracks":      unmatchedCount,
			"duration_ms":           duration.Milliseconds(),
		})

	// Publish success notification
	s.publishNotification(u.ID, "Playlist Synced",
		fmt.Sprintf("'%s' synced to Navidrome (%d/%d tracks matched)",
			pl.Name, matchedCount, len(sourceTracks)),
		"success")

	s.Logger.Info("playlist synced to Navidrome successfully",
		"playlist_id", playlistID,
		"playlist_name", pl.Name,
		"navidrome_playlist_id", navidromePlaylistID,
		"matched_tracks", matchedCount,
		"total_tracks", len(sourceTracks),
		"duration", duration)

	return nil
}

// SyncAllEnabledPlaylists syncs all playlists with sync_to_navidrome=true for a user.
// Called by the scheduler.
func (s *PlaylistSyncService) SyncAllEnabledPlaylists(ctx context.Context, userID int) error {
	s.Logger.Info("syncing all enabled playlists",
		"user_id", userID)

	playlists, err := s.Client.Playlist.Query().
		Where(
			playlist.HasUserWith(user.ID(userID)),
			playlist.SyncToNavidrome(true),
			playlist.SourceNEQ("navidrome"),
		).
		All(ctx)
	if err != nil {
		s.Logger.Error("failed to query enabled playlists",
			"user_id", userID,
			"error", err)
		return fmt.Errorf("failed to query enabled playlists: %w", err)
	}

	s.Logger.Debug("found playlists to sync",
		"user_id", userID,
		"count", len(playlists))

	if len(playlists) == 0 {
		s.Logger.Debug("no playlists to sync",
			"user_id", userID)
		return nil
	}

	var syncErrors []error
	successCount := 0
	for _, pl := range playlists {
		if err := s.SyncPlaylistToNavidrome(ctx, pl.ID); err != nil {
			s.Logger.Error("failed to sync playlist",
				"user_id", userID,
				"playlist_id", pl.ID,
				"playlist_name", pl.Name,
				"error", err)
			syncErrors = append(syncErrors, err)
		} else {
			successCount++
		}
	}

	s.Logger.Info("completed syncing all enabled playlists",
		"user_id", userID,
		"total", len(playlists),
		"success", successCount,
		"failed", len(syncErrors))

	if len(syncErrors) > 0 {
		return fmt.Errorf("failed to sync %d playlists", len(syncErrors))
	}

	return nil
}

// RemovePlaylistFromNavidrome removes a synced playlist from Navidrome.
// Called when user disables sync (if configured to delete on unsync).
func (s *PlaylistSyncService) RemovePlaylistFromNavidrome(ctx context.Context, playlistID int) error {
	s.Logger.Info("remove playlist from Navidrome requested",
		"playlist_id", playlistID,
		"delete_on_unsync", s.Config.PlaylistSync.DeleteOnUnsync)

	// Check if deletion is enabled
	if !s.Config.PlaylistSync.DeleteOnUnsync {
		s.Logger.Debug("delete on unsync is disabled, keeping Navidrome playlist",
			"playlist_id", playlistID)
		return nil
	}

	// Load playlist with user
	pl, err := s.Client.Playlist.Query().
		Where(playlist.ID(playlistID)).
		WithUser().
		Only(ctx)
	if err != nil {
		s.Logger.Error("failed to load playlist for removal",
			"playlist_id", playlistID,
			"error", err)
		return fmt.Errorf("failed to load playlist: %w", err)
	}

	// No Navidrome ID means nothing to delete
	if pl.NavidromePlaylistID == "" {
		s.Logger.Debug("no Navidrome playlist ID, nothing to delete",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return nil
	}

	u := pl.Edges.User
	if u == nil {
		s.Logger.Error("playlist has no associated user",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return fmt.Errorf("playlist has no associated user")
	}

	s.Logger.Info("removing playlist from Navidrome",
		"playlist_id", playlistID,
		"playlist_name", pl.Name,
		"navidrome_playlist_id", pl.NavidromePlaylistID,
		"user", u.Username)

	// Get Navidrome provider
	navidromeProvider, err := s.getNavidromeProvider(ctx, u)
	if err != nil {
		s.Logger.Error("failed to get Navidrome provider for removal",
			"playlist_id", playlistID,
			"error", err)
		return fmt.Errorf("failed to get Navidrome provider: %w", err)
	}

	syncer, ok := navidromeProvider.(providers.PlaylistSyncer)
	if !ok {
		s.Logger.Error("Navidrome provider does not implement PlaylistSyncer",
			"playlist_id", playlistID)
		return fmt.Errorf("Navidrome provider does not implement PlaylistSyncer")
	}

	// Delete from Navidrome
	if err := syncer.DeletePlaylist(ctx, pl.NavidromePlaylistID); err != nil {
		s.Logger.Error("failed to delete playlist from Navidrome",
			"playlist_id", playlistID,
			"navidrome_playlist_id", pl.NavidromePlaylistID,
			"error", err)
		// Don't fail the whole operation - maybe the playlist was already deleted
		s.Logger.Warn("continuing despite Navidrome delete error",
			"playlist_id", playlistID)
	} else {
		s.Logger.Info("deleted playlist from Navidrome",
			"playlist_id", playlistID,
			"navidrome_playlist_id", pl.NavidromePlaylistID)
	}

	// Clear sync info from database
	_, err = s.Client.Playlist.UpdateOne(pl).
		ClearNavidromePlaylistID().
		ClearLastSyncedAt().
		SetMatchedTrackCount(0).
		ClearSyncError().
		Save(ctx)
	if err != nil {
		s.Logger.Error("failed to clear playlist sync info",
			"playlist_id", playlistID,
			"error", err)
		return fmt.Errorf("failed to clear playlist sync info: %w", err)
	}

	// Log removal event
	s.logEvent(ctx, u, syncevent.EventTypePlaylistSyncRemoved, "navidrome",
		fmt.Sprintf("Removed playlist '%s' from Navidrome", pl.Name),
		map[string]interface{}{
			"playlist_id":           pl.ID,
			"playlist_name":         pl.Name,
			"navidrome_playlist_id": pl.NavidromePlaylistID,
		})

	// Publish notification
	s.publishNotification(u.ID, "Playlist Removed",
		fmt.Sprintf("'%s' removed from Navidrome", pl.Name),
		"info")

	s.Logger.Info("playlist removed from Navidrome successfully",
		"playlist_id", playlistID,
		"playlist_name", pl.Name)

	return nil
}

// RebuildPlaylistSync clears the existing Navidrome playlist and re-syncs from scratch.
// This is useful when track matches have changed or to fix sync issues.
func (s *PlaylistSyncService) RebuildPlaylistSync(ctx context.Context, playlistID int) error {
	startTime := time.Now()

	s.Logger.Info("rebuild playlist sync requested",
		"playlist_id", playlistID)

	// Load playlist with user
	pl, err := s.Client.Playlist.Query().
		Where(playlist.ID(playlistID)).
		WithUser().
		Only(ctx)
	if err != nil {
		s.Logger.Error("failed to load playlist for rebuild",
			"playlist_id", playlistID,
			"error", err)
		return fmt.Errorf("failed to load playlist: %w", err)
	}

	// Verify sync is enabled
	if !pl.SyncToNavidrome {
		s.Logger.Warn("attempted to rebuild playlist with sync disabled",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return fmt.Errorf("sync is not enabled for this playlist")
	}

	// Ensure playlist is not from Navidrome
	if pl.Source == "navidrome" {
		s.Logger.Warn("attempted to rebuild Navidrome playlist",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return fmt.Errorf("cannot rebuild Navidrome playlist")
	}

	u := pl.Edges.User
	if u == nil {
		s.Logger.Error("playlist has no associated user",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return fmt.Errorf("playlist has no associated user")
	}

	s.Logger.Info("rebuilding playlist sync",
		"playlist_id", playlistID,
		"playlist_name", pl.Name,
		"source", pl.Source,
		"user", u.Username,
		"navidrome_playlist_id", pl.NavidromePlaylistID)

	// Log rebuild started event
	s.logEvent(ctx, u, syncevent.EventTypePlaylistSyncStarted, "navidrome",
		fmt.Sprintf("Rebuilding sync of playlist '%s' to Navidrome", pl.Name),
		map[string]interface{}{
			"playlist_id":           pl.ID,
			"playlist_name":         pl.Name,
			"source":                pl.Source,
			"action":                "rebuild",
			"navidrome_playlist_id": pl.NavidromePlaylistID,
		})

	// Publish "rebuild starting" notification to UI
	s.publishNotification(u.ID, "Rebuilding Playlist",
		fmt.Sprintf("Rebuilding '%s' sync to Navidrome...", pl.Name),
		"warning")

	// Get Navidrome provider
	navidromeProvider, err := s.getNavidromeProvider(ctx, u)
	if err != nil {
		return s.handleSyncError(ctx, pl, u, fmt.Errorf("failed to get Navidrome provider: %w", err))
	}

	syncer, ok := navidromeProvider.(providers.PlaylistSyncer)
	if !ok {
		return s.handleSyncError(ctx, pl, u, fmt.Errorf("Navidrome provider does not implement PlaylistSyncer"))
	}

	// Delete existing playlist from Navidrome if it exists
	if pl.NavidromePlaylistID != "" {
		s.Logger.Debug("deleting existing Navidrome playlist",
			"playlist_id", playlistID,
			"navidrome_playlist_id", pl.NavidromePlaylistID)

		if err := syncer.DeletePlaylist(ctx, pl.NavidromePlaylistID); err != nil {
			s.Logger.Warn("failed to delete existing Navidrome playlist, continuing with rebuild",
				"playlist_id", playlistID,
				"navidrome_playlist_id", pl.NavidromePlaylistID,
				"error", err)
			// Continue anyway - the playlist might have been deleted manually
		} else {
			s.Logger.Info("deleted existing Navidrome playlist",
				"playlist_id", playlistID,
				"navidrome_playlist_id", pl.NavidromePlaylistID)
		}
	}

	// Clear sync info from database to force fresh sync
	_, err = s.Client.Playlist.UpdateOne(pl).
		ClearNavidromePlaylistID().
		ClearLastSyncedAt().
		SetMatchedTrackCount(0).
		ClearSyncError().
		Save(ctx)
	if err != nil {
		s.Logger.Error("failed to clear playlist sync info for rebuild",
			"playlist_id", playlistID,
			"error", err)
		return s.handleSyncError(ctx, pl, u, fmt.Errorf("failed to clear sync info: %w", err))
	}

	s.Logger.Debug("cleared sync info, starting fresh sync",
		"playlist_id", playlistID)

	// Now perform a fresh sync
	if err := s.SyncPlaylistToNavidrome(ctx, playlistID); err != nil {
		s.Logger.Error("failed to sync playlist after rebuild",
			"playlist_id", playlistID,
			"error", err)
		return err // Error already logged and handled by SyncPlaylistToNavidrome
	}

	duration := time.Since(startTime)

	s.Logger.Info("playlist rebuild completed successfully",
		"playlist_id", playlistID,
		"playlist_name", pl.Name,
		"duration", duration)

	return nil
}

// handleSyncError updates the playlist with the error and publishes a notification.
func (s *PlaylistSyncService) handleSyncError(ctx context.Context, pl *ent.Playlist, u *ent.User, err error) error {
	s.Logger.Error("playlist sync failed",
		"playlist_id", pl.ID,
		"playlist_name", pl.Name,
		"source", pl.Source,
		"error", err)

	// Store error in database
	_, dbErr := s.Client.Playlist.UpdateOne(pl).
		SetSyncError(err.Error()).
		Save(ctx)
	if dbErr != nil {
		s.Logger.Error("failed to save sync error to database",
			"playlist_id", pl.ID,
			"error", dbErr)
	}

	// Log sync failed event
	if u != nil {
		s.logEvent(ctx, u, syncevent.EventTypePlaylistSyncFailed, "navidrome",
			fmt.Sprintf("Failed to sync playlist '%s': %s", pl.Name, err.Error()),
			map[string]interface{}{
				"playlist_id":   pl.ID,
				"playlist_name": pl.Name,
				"error":         err.Error(),
			})

		// Publish error notification
		s.publishNotification(u.ID, "Playlist Sync Failed",
			fmt.Sprintf("Failed to sync '%s': %s", pl.Name, err.Error()),
			"error")
	}

	return err
}

// getNavidromeProvider returns the Navidrome provider for a user.
func (s *PlaylistSyncService) getNavidromeProvider(ctx context.Context, u *ent.User) (providers.Provider, error) {
	s.Logger.Debug("getting Navidrome provider for user",
		"user_id", u.ID,
		"username", u.Username)

	// Load user with Navidrome auth edge - this is critical for the provider to work
	userWithAuth, err := s.Client.User.Query().
		Where(user.ID(u.ID)).
		WithNavidromeAuth().
		Only(ctx)
	if err != nil {
		s.Logger.Error("failed to load user with Navidrome auth",
			"user_id", u.ID,
			"error", err)
		return nil, fmt.Errorf("failed to load user with auth: %w", err)
	}

	// Check if Navidrome auth is configured
	if userWithAuth.Edges.NavidromeAuth == nil {
		s.Logger.Error("user has no Navidrome auth configured",
			"user_id", u.ID,
			"username", u.Username)
		return nil, fmt.Errorf("Navidrome not configured for user %s", u.Username)
	}

	s.Logger.Debug("loaded user with Navidrome auth",
		"user_id", u.ID,
		"username", userWithAuth.Username)

	// Find Navidrome factory and create provider
	for _, factory := range s.Factories {
		provider, err := factory(ctx, userWithAuth)
		if err != nil {
			s.Logger.Debug("factory returned error",
				"error", err)
			continue
		}
		if provider != nil && provider.Type() == providers.TypeNavidrome {
			s.Logger.Debug("found Navidrome provider",
				"user_id", u.ID,
				"provider_type", provider.Type())
			return provider, nil
		}
	}

	s.Logger.Error("no Navidrome provider found for user",
		"user_id", u.ID,
		"factory_count", len(s.Factories))

	return nil, fmt.Errorf("no Navidrome provider configured for user")
}

// logEvent logs a sync event to the database.
func (s *PlaylistSyncService) logEvent(ctx context.Context, u *ent.User, eventType syncevent.EventType, provider string, message string, metadata map[string]interface{}) {
	builder := s.Client.SyncEvent.Create().
		SetUser(u).
		SetEventType(eventType).
		SetProvider(provider).
		SetMessage(message)

	if metadata != nil {
		if metadataJSON, err := json.Marshal(metadata); err == nil {
			builder.SetMetadata(string(metadataJSON))
		} else {
			s.Logger.Warn("failed to marshal event metadata",
				"event_type", eventType,
				"error", err)
		}
	}

	if _, err := builder.Save(ctx); err != nil {
		s.Logger.Warn("failed to log sync event",
			"event_type", eventType,
			"provider", provider,
			"error", err)
	} else {
		s.Logger.Debug("logged sync event",
			"event_type", eventType,
			"provider", provider,
			"message", message)
	}
}

// publishNotification publishes a notification event to the event bus.
func (s *PlaylistSyncService) publishNotification(userID int, title, message, iconType string) {
	if s.Bus == nil {
		s.Logger.Debug("event bus is nil, skipping notification",
			"title", title)
		return
	}

	s.Logger.Debug("publishing notification",
		"user_id", userID,
		"title", title,
		"icon_type", iconType)

	s.Bus.Publish(userID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    title,
			Message:  message,
			IconType: iconType,
		},
	})
}

// GetSyncStatus returns the sync status for a playlist.
type PlaylistSyncStatus struct {
	SyncEnabled     bool
	LastSyncedAt    *time.Time
	SyncError       string
	NavidromeID     string
	MatchedTracks   int
	TotalTracks     int
	MatchPercentage float64
}

// GetPlaylistSyncStatus returns the sync status for a playlist.
func (s *PlaylistSyncService) GetPlaylistSyncStatus(ctx context.Context, playlistID int) (*PlaylistSyncStatus, error) {
	pl, err := s.Client.Playlist.Query().
		Where(playlist.ID(playlistID)).
		Only(ctx)
	if err != nil {
		return nil, err
	}

	status := &PlaylistSyncStatus{
		SyncEnabled:   pl.SyncToNavidrome,
		NavidromeID:   pl.NavidromePlaylistID,
		MatchedTracks: pl.MatchedTrackCount,
		TotalTracks:   pl.TrackCount,
	}

	if pl.LastSyncedAt != nil {
		status.LastSyncedAt = pl.LastSyncedAt
	}

	if pl.SyncError != "" {
		status.SyncError = pl.SyncError
	}

	if status.TotalTracks > 0 {
		status.MatchPercentage = float64(status.MatchedTracks) / float64(status.TotalTracks) * 100
	}

	return status, nil
}
