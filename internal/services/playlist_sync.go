// Governing: ADR-0005 (Navidrome auth), ADR-0007 (event bus), SPEC playlist-sync-navidrome
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
	client       *ent.Client
	config       *config.Config
	logger       *slog.Logger
	bus          *events.Bus
	trackMatcher *TrackMatcher
	factories    []providers.Factory
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
		client:       client,
		config:       cfg,
		logger:       logger,
		bus:          bus,
		trackMatcher: trackMatcher,
		factories:    make([]providers.Factory, 0),
	}
}

// Register adds a provider factory to the service.
func (s *PlaylistSyncService) Register(factory providers.Factory) {
	s.factories = append(s.factories, factory)
	s.logger.Debug("registered provider factory for playlist sync")
}

// SyncPlaylistToNavidrome syncs a single playlist to Navidrome.
// Called when user enables sync or during scheduled sync.
// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-030 (SyncPlaylist creation/update via PlaylistSyncer),
// SPEC playlist-sync-navidrome REQ-PLSYNC-031 (UpdatePlaylistTracks for existing playlists),
// SPEC playlist-sync-navidrome REQ-PLSYNC-032 (remotePlaylistID stored on playlist entity),
// SPEC playlist-sync-navidrome REQ-PLSYNC-060 (SyncEvent audit logging)
func (s *PlaylistSyncService) SyncPlaylistToNavidrome(ctx context.Context, playlistID int) error {
	return s.syncPlaylistToNavidrome(ctx, playlistID, nil)
}

// syncPlaylistToNavidrome performs the sync. libraryIndex may be nil (it is
// loaded on demand); callers syncing many playlists in one tick (issue #330)
// pass a shared index so the user's library is loaded once per tick instead
// of once per playlist.
func (s *PlaylistSyncService) syncPlaylistToNavidrome(ctx context.Context, playlistID int, libraryIndex *LibraryIndex) error {
	startTime := time.Now()

	s.logger.Info("starting playlist sync to Navidrome",
		"playlist_id", playlistID)

	// Load playlist with user
	pl, err := s.client.Playlist.Query().
		Where(playlist.ID(playlistID)).
		WithUser().
		Only(ctx)
	if err != nil {
		s.logger.Error("failed to load playlist",
			"playlist_id", playlistID,
			"error", err)
		return fmt.Errorf("failed to load playlist: %w", err)
	}

	// Verify sync is enabled
	if !pl.SyncToNavidrome {
		s.logger.Debug("sync not enabled for playlist, skipping",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return nil
	}

	// Ensure playlist is not from Navidrome
	if pl.Source == "navidrome" {
		s.logger.Warn("attempted to sync Navidrome playlist to Navidrome",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return fmt.Errorf("cannot sync Navidrome playlist to Navidrome")
	}

	u := pl.Edges.User
	if u == nil {
		s.logger.Error("playlist has no associated user",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return fmt.Errorf("playlist has no associated user")
	}

	s.logger.Info("syncing playlist to Navidrome",
		"playlist_id", playlistID,
		"playlist_name", pl.Name,
		"source", pl.Source,
		"user_id", u.ID,
		"user", u.Username)

	// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-014 (syncing state set at start, replaced on completion)
	// Set sync_status to "syncing" at the START of sync so the UI reflects in-progress state.
	_, err = s.client.Playlist.UpdateOne(pl).
		SetSyncStatus(playlist.SyncStatusSyncing).
		Save(ctx)
	if err != nil {
		s.logger.Warn("failed to set sync_status=syncing",
			"playlist_id", playlistID,
			"error", err)
		// Non-fatal: continue with sync even if we can't update status
	}

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

	s.logger.Debug("obtained Navidrome provider",
		"playlist_id", playlistID,
		"provider_type", navidromeProvider.Type())

	syncer, ok := navidromeProvider.(providers.PlaylistSyncer)
	if !ok {
		return s.handleSyncError(ctx, pl, u, fmt.Errorf("Navidrome provider does not implement PlaylistSyncer"))
	}

	// Load playlist tracks
	s.logger.Debug("loading playlist tracks",
		"playlist_id", playlistID)

	playlistTracks, err := s.client.PlaylistTrack.Query().
		Where(playlisttrack.HasPlaylistWith(playlist.ID(playlistID))).
		Order(ent.Asc(playlisttrack.FieldPosition)).
		All(ctx)
	if err != nil {
		return s.handleSyncError(ctx, pl, u, fmt.Errorf("failed to load playlist tracks: %w", err))
	}

	s.logger.Debug("loaded playlist tracks",
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
	s.logger.Debug("starting track matching",
		"playlist_id", playlistID,
		"source_track_count", len(sourceTracks))

	// Load the library index on demand if the caller didn't supply a shared
	// one (or supplied one built for a different user).
	if libraryIndex == nil || libraryIndex.UserID != u.ID {
		libraryIndex, err = s.trackMatcher.LoadLibraryIndex(ctx, u.ID)
		if err != nil {
			return s.handleSyncError(ctx, pl, u, fmt.Errorf("failed to load library index: %w", err))
		}
	}

	matchResults := s.trackMatcher.MatchTracksWithIndex(libraryIndex, sourceTracks)

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

	s.logger.Info("track matching complete",
		"playlist_id", playlistID,
		"playlist_name", pl.Name,
		"total_tracks", len(sourceTracks),
		"matched_tracks", matchedCount,
		"unmatched_tracks", unmatchedCount,
		"match_rate", fmt.Sprintf("%.1f%%", float64(matchedCount)/float64(len(sourceTracks))*100))

	// Governing: SPEC graceful-shutdown REQ-REC-004 (playlist sync compares desired vs current Navidrome state)
	// Decide what to do with the playlist
	var navidromePlaylistID string

	if pl.NavidromePlaylistID != "" {
		// Update existing playlist
		s.logger.Debug("updating existing Navidrome playlist",
			"playlist_id", playlistID,
			"navidrome_playlist_id", pl.NavidromePlaylistID,
			"track_count", len(matchedTracks))

		err = syncer.UpdatePlaylistTracks(ctx, pl.NavidromePlaylistID, matchedTracks)
		if err != nil {
			return s.handleSyncError(ctx, pl, u, fmt.Errorf("failed to update playlist tracks: %w", err))
		}
		navidromePlaylistID = pl.NavidromePlaylistID

		s.logger.Info("updated existing Navidrome playlist",
			"playlist_id", playlistID,
			"navidrome_playlist_id", navidromePlaylistID,
			"track_count", len(matchedTracks))
	} else {
		// Create new playlist
		// Governing: SPEC-0015 REQ playlist-pairing — use navidrome_playlist_name if set
		nameToUse := pl.Name
		if pl.NavidromePlaylistName != "" {
			nameToUse = pl.NavidromePlaylistName
		}

		s.logger.Debug("creating new Navidrome playlist",
			"playlist_id", playlistID,
			"playlist_name", nameToUse,
			"track_count", len(matchedTracks))

		navidromePlaylistID, err = syncer.SyncPlaylist(ctx, providers.SyncPlaylistRequest{
			Name:        nameToUse,
			Description: pl.Description,
			ImageURL:    pl.ImageURL,
			Tracks:      matchedTracks,
		})
		if err != nil {
			return s.handleSyncError(ctx, pl, u, fmt.Errorf("failed to create playlist: %w", err))
		}

		s.logger.Info("created new Navidrome playlist",
			"playlist_id", playlistID,
			"navidrome_playlist_id", navidromePlaylistID,
			"track_count", len(matchedTracks))
	}

	// Update database with sync info
	// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-010 (sync_status state machine)
	// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-014 (syncing state set at start, replaced on completion)
	now := time.Now()
	update := s.client.Playlist.UpdateOne(pl).
		SetNavidromePlaylistID(navidromePlaylistID).
		SetLastSyncedAt(now).
		SetMatchedTrackCount(matchedCount)

	// Only clear sync error if at least one track matched (SRV-PS-009)
	// Set final sync_status: "success" if all tracks matched, "warning" if some unmatched
	if matchedCount > 0 && matchedCount == len(sourceTracks) {
		update = update.ClearSyncError().SetSyncStatus(playlist.SyncStatusSuccess)
	} else if matchedCount > 0 {
		update = update.ClearSyncError().SetSyncStatus(playlist.SyncStatusWarning)
	} else {
		update = update.
			SetSyncError("No tracks matched - playlist may be empty or library mismatch").
			SetSyncStatus(playlist.SyncStatusWarning)
	}

	_, err = update.Save(ctx)
	if err != nil {
		s.logger.Error("failed to update playlist sync info",
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

	s.logger.Info("playlist synced to Navidrome successfully",
		"playlist_id", playlistID,
		"playlist_name", pl.Name,
		"navidrome_playlist_id", navidromePlaylistID,
		"matched_tracks", matchedCount,
		"total_tracks", len(sourceTracks),
		"duration", duration)

	return nil
}

// PairWithNavidrome links an existing Navidrome playlist to a Spotter playlist and
// deletes the Navidrome-source duplicate from Spotter's DB.
// Governing: SPEC-0015 REQ playlist-pairing
func (s *PlaylistSyncService) PairWithNavidrome(ctx context.Context, playlistID int, navidromeRemoteID string) error {
	s.logger.Info("pairing playlist with existing Navidrome playlist",
		"playlist_id", playlistID,
		"navidrome_remote_id", navidromeRemoteID)

	// 1. Update the Spotter playlist: set navidrome_playlist_id
	pl, err := s.client.Playlist.Query().
		Where(playlist.ID(playlistID)).
		WithUser().
		Only(ctx)
	if err != nil {
		return fmt.Errorf("failed to load playlist: %w", err)
	}

	u := pl.Edges.User
	if u == nil {
		return fmt.Errorf("playlist has no associated user")
	}

	_, err = s.client.Playlist.UpdateOne(pl).
		SetNavidromePlaylistID(navidromeRemoteID).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to set navidrome_playlist_id: %w", err)
	}

	// 2. Find the Navidrome-source playlist in Spotter's DB whose remote_id == navidromeRemoteID
	navidromeDuplicate, err := s.client.Playlist.Query().
		Where(
			playlist.HasUserWith(user.ID(u.ID)),
			playlist.Source(string(providers.TypeNavidrome)),
			playlist.RemoteID(navidromeRemoteID),
		).
		First(ctx)
	if err == nil && navidromeDuplicate != nil {
		// 3. Delete the Navidrome-source duplicate from Spotter's DB
		if delErr := s.client.Playlist.DeleteOne(navidromeDuplicate).Exec(ctx); delErr != nil {
			s.logger.Warn("failed to delete Navidrome-source duplicate playlist",
				"duplicate_id", navidromeDuplicate.ID,
				"error", delErr)
			// Non-fatal: continue with sync
		} else {
			s.logger.Info("deleted Navidrome-source duplicate playlist",
				"duplicate_id", navidromeDuplicate.ID,
				"duplicate_name", navidromeDuplicate.Name)
		}
	}

	// 4. Trigger sync (will UPDATE the existing Navidrome playlist)
	return s.SyncPlaylistToNavidrome(ctx, playlistID)
}

// SyncAllEnabledPlaylists syncs all playlists with sync_to_navidrome=true for a user.
// Called by the scheduler.
// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-040 (scheduled sync of all enabled playlists)
func (s *PlaylistSyncService) SyncAllEnabledPlaylists(ctx context.Context, userID int) error {
	s.logger.Info("syncing all enabled playlists",
		"user_id", userID)

	playlists, err := s.client.Playlist.Query().
		Where(
			playlist.HasUserWith(user.ID(userID)),
			playlist.SyncToNavidrome(true),
			playlist.SourceNEQ("navidrome"),
		).
		All(ctx)
	if err != nil {
		s.logger.Error("failed to query enabled playlists",
			"user_id", userID,
			"error", err)
		return fmt.Errorf("failed to query enabled playlists: %w", err)
	}

	s.logger.Debug("found playlists to sync",
		"user_id", userID,
		"count", len(playlists))

	if len(playlists) == 0 {
		s.logger.Debug("no playlists to sync",
			"user_id", userID)
		return nil
	}

	// Governing: ADR-0014; issue #330 — load the user's library once per sync
	// tick and share the index across all playlists, instead of re-running the
	// full library query per playlist.
	//
	// If the tick-level load fails, do NOT abort the tick: fall back to
	// per-playlist loading (libraryIndex == nil) so each playlist still goes
	// through handleSyncError — sync_status=error, SyncEvent audit logging,
	// and UI notification per REQ-PLSYNC-060 — instead of failing invisibly.
	libraryIndex, err := s.trackMatcher.LoadLibraryIndex(ctx, userID)
	if err != nil {
		s.logger.Error("failed to load shared library index for sync tick, falling back to per-playlist loading",
			"user_id", userID,
			"error", err)
		libraryIndex = nil
	}

	var syncErrors []error
	successCount := 0
	for _, pl := range playlists {
		if err := s.syncPlaylistToNavidrome(ctx, pl.ID, libraryIndex); err != nil {
			s.logger.Error("failed to sync playlist",
				"user_id", userID,
				"playlist_id", pl.ID,
				"playlist_name", pl.Name,
				"error", err)
			syncErrors = append(syncErrors, err)
		} else {
			successCount++
		}
	}

	s.logger.Info("completed syncing all enabled playlists",
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
// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-032 (delete-on-unsync option)
func (s *PlaylistSyncService) RemovePlaylistFromNavidrome(ctx context.Context, playlistID int) error {
	s.logger.Info("remove playlist from Navidrome requested",
		"playlist_id", playlistID,
		"delete_on_unsync", s.config.PlaylistSync.DeleteOnUnsync)

	// Check if deletion is enabled
	if !s.config.PlaylistSync.DeleteOnUnsync {
		s.logger.Debug("delete on unsync is disabled, keeping Navidrome playlist",
			"playlist_id", playlistID)
		return nil
	}

	// Load playlist with user
	pl, err := s.client.Playlist.Query().
		Where(playlist.ID(playlistID)).
		WithUser().
		Only(ctx)
	if err != nil {
		s.logger.Error("failed to load playlist for removal",
			"playlist_id", playlistID,
			"error", err)
		return fmt.Errorf("failed to load playlist: %w", err)
	}

	// No Navidrome ID means nothing to delete
	if pl.NavidromePlaylistID == "" {
		s.logger.Debug("no Navidrome playlist ID, nothing to delete",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return nil
	}

	u := pl.Edges.User
	if u == nil {
		s.logger.Error("playlist has no associated user",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return fmt.Errorf("playlist has no associated user")
	}

	s.logger.Info("removing playlist from Navidrome",
		"playlist_id", playlistID,
		"playlist_name", pl.Name,
		"navidrome_playlist_id", pl.NavidromePlaylistID,
		"user", u.Username)

	// Get Navidrome provider
	navidromeProvider, err := s.getNavidromeProvider(ctx, u)
	if err != nil {
		s.logger.Error("failed to get Navidrome provider for removal",
			"playlist_id", playlistID,
			"error", err)
		return fmt.Errorf("failed to get Navidrome provider: %w", err)
	}

	syncer, ok := navidromeProvider.(providers.PlaylistSyncer)
	if !ok {
		s.logger.Error("Navidrome provider does not implement PlaylistSyncer",
			"playlist_id", playlistID)
		return fmt.Errorf("Navidrome provider does not implement PlaylistSyncer")
	}

	// Delete from Navidrome
	if err := syncer.DeletePlaylist(ctx, pl.NavidromePlaylistID); err != nil {
		s.logger.Error("failed to delete playlist from Navidrome",
			"playlist_id", playlistID,
			"navidrome_playlist_id", pl.NavidromePlaylistID,
			"error", err)
		// Don't fail the whole operation - maybe the playlist was already deleted
		s.logger.Warn("continuing despite Navidrome delete error",
			"playlist_id", playlistID)
	} else {
		s.logger.Info("deleted playlist from Navidrome",
			"playlist_id", playlistID,
			"navidrome_playlist_id", pl.NavidromePlaylistID)
	}

	// Clear sync info from database
	_, err = s.client.Playlist.UpdateOne(pl).
		ClearNavidromePlaylistID().
		ClearLastSyncedAt().
		SetMatchedTrackCount(0).
		ClearSyncError().
		Save(ctx)
	if err != nil {
		s.logger.Error("failed to clear playlist sync info",
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

	s.logger.Info("playlist removed from Navidrome successfully",
		"playlist_id", playlistID,
		"playlist_name", pl.Name)

	return nil
}

// RebuildPlaylistSync clears the existing Navidrome playlist and re-syncs from scratch.
// This is useful when track matches have changed or to fix sync issues.
// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-040 (UpdatePlaylistTracks for re-sync)
func (s *PlaylistSyncService) RebuildPlaylistSync(ctx context.Context, playlistID int) error {
	startTime := time.Now()

	s.logger.Info("rebuild playlist sync requested",
		"playlist_id", playlistID)

	// Load playlist with user
	pl, err := s.client.Playlist.Query().
		Where(playlist.ID(playlistID)).
		WithUser().
		Only(ctx)
	if err != nil {
		s.logger.Error("failed to load playlist for rebuild",
			"playlist_id", playlistID,
			"error", err)
		return fmt.Errorf("failed to load playlist: %w", err)
	}

	// Verify sync is enabled
	if !pl.SyncToNavidrome {
		s.logger.Warn("attempted to rebuild playlist with sync disabled",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return fmt.Errorf("sync is not enabled for this playlist")
	}

	// Ensure playlist is not from Navidrome
	if pl.Source == "navidrome" {
		s.logger.Warn("attempted to rebuild Navidrome playlist",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return fmt.Errorf("cannot rebuild Navidrome playlist")
	}

	u := pl.Edges.User
	if u == nil {
		s.logger.Error("playlist has no associated user",
			"playlist_id", playlistID,
			"playlist_name", pl.Name)
		return fmt.Errorf("playlist has no associated user")
	}

	s.logger.Info("rebuilding playlist sync",
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
		s.logger.Debug("deleting existing Navidrome playlist",
			"playlist_id", playlistID,
			"navidrome_playlist_id", pl.NavidromePlaylistID)

		if err := syncer.DeletePlaylist(ctx, pl.NavidromePlaylistID); err != nil {
			s.logger.Warn("failed to delete existing Navidrome playlist, continuing with rebuild",
				"playlist_id", playlistID,
				"navidrome_playlist_id", pl.NavidromePlaylistID,
				"error", err)
			// Continue anyway - the playlist might have been deleted manually
		} else {
			s.logger.Info("deleted existing Navidrome playlist",
				"playlist_id", playlistID,
				"navidrome_playlist_id", pl.NavidromePlaylistID)
		}
	}

	// Clear sync info from database to force fresh sync
	_, err = s.client.Playlist.UpdateOne(pl).
		ClearNavidromePlaylistID().
		ClearLastSyncedAt().
		SetMatchedTrackCount(0).
		ClearSyncError().
		Save(ctx)
	if err != nil {
		s.logger.Error("failed to clear playlist sync info for rebuild",
			"playlist_id", playlistID,
			"error", err)
		return s.handleSyncError(ctx, pl, u, fmt.Errorf("failed to clear sync info: %w", err))
	}

	s.logger.Debug("cleared sync info, starting fresh sync",
		"playlist_id", playlistID)

	// Now perform a fresh sync
	if err := s.SyncPlaylistToNavidrome(ctx, playlistID); err != nil {
		s.logger.Error("failed to sync playlist after rebuild",
			"playlist_id", playlistID,
			"error", err)
		return err // Error already logged and handled by SyncPlaylistToNavidrome
	}

	duration := time.Since(startTime)

	s.logger.Info("playlist rebuild completed successfully",
		"playlist_id", playlistID,
		"playlist_name", pl.Name,
		"duration", duration)

	return nil
}

// handleSyncError updates the playlist with the error and publishes a notification.
// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-013 (error state on Navidrome API failure),
// SPEC playlist-sync-navidrome REQ-PLSYNC-060 (SyncEvent audit logging on failure)
func (s *PlaylistSyncService) handleSyncError(ctx context.Context, pl *ent.Playlist, u *ent.User, err error) error {
	s.logger.Error("playlist sync failed",
		"playlist_id", pl.ID,
		"playlist_name", pl.Name,
		"source", pl.Source,
		"error", err)

	// Store error in database
	// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-010 (sync_status state machine)
	_, dbErr := s.client.Playlist.UpdateOne(pl).
		SetSyncError(err.Error()).
		SetSyncStatus(playlist.SyncStatusError).
		Save(ctx)
	if dbErr != nil {
		s.logger.Error("failed to save sync error to database",
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
	s.logger.Debug("getting Navidrome provider for user",
		"user_id", u.ID,
		"username", u.Username)

	// Load user with Navidrome auth edge - this is critical for the provider to work
	userWithAuth, err := s.client.User.Query().
		Where(user.ID(u.ID)).
		WithNavidromeAuth().
		Only(ctx)
	if err != nil {
		s.logger.Error("failed to load user with Navidrome auth",
			"user_id", u.ID,
			"error", err)
		return nil, fmt.Errorf("failed to load user with auth: %w", err)
	}

	// Check if Navidrome auth is configured
	if userWithAuth.Edges.NavidromeAuth == nil {
		s.logger.Error("user has no Navidrome auth configured",
			"user_id", u.ID,
			"username", u.Username)
		return nil, fmt.Errorf("Navidrome not configured for user %s", u.Username)
	}

	s.logger.Debug("loaded user with Navidrome auth",
		"user_id", u.ID,
		"username", userWithAuth.Username)

	// Find Navidrome factory and create provider
	for _, factory := range s.factories {
		provider, err := factory(ctx, userWithAuth)
		if err != nil {
			s.logger.Debug("factory returned error",
				"error", err)
			continue
		}
		if provider != nil && provider.Type() == providers.TypeNavidrome {
			s.logger.Debug("found Navidrome provider",
				"user_id", u.ID,
				"provider_type", provider.Type())
			return provider, nil
		}
	}

	s.logger.Error("no Navidrome provider found for user",
		"user_id", u.ID,
		"factory_count", len(s.factories))

	return nil, fmt.Errorf("no Navidrome provider configured for user")
}

// logEvent logs a sync event to the database.
func (s *PlaylistSyncService) logEvent(ctx context.Context, u *ent.User, eventType syncevent.EventType, provider string, message string, metadata map[string]interface{}) {
	builder := s.client.SyncEvent.Create().
		SetUser(u).
		SetEventType(eventType).
		SetProvider(provider).
		SetMessage(message)

	if metadata != nil {
		if metadataJSON, err := json.Marshal(metadata); err == nil {
			builder.SetMetadata(string(metadataJSON))
		} else {
			s.logger.Warn("failed to marshal event metadata",
				"event_type", eventType,
				"error", err)
		}
	}

	if _, err := builder.Save(ctx); err != nil {
		s.logger.Warn("failed to log sync event",
			"event_type", eventType,
			"provider", provider,
			"error", err)
	} else {
		s.logger.Debug("logged sync event",
			"event_type", eventType,
			"provider", provider,
			"message", message)
	}
}

// publishNotification publishes a notification event to the event bus.
func (s *PlaylistSyncService) publishNotification(userID int, title, message, iconType string) {
	if s.bus == nil {
		s.logger.Debug("event bus is nil, skipping notification",
			"title", title)
		return
	}

	s.logger.Debug("publishing notification",
		"user_id", userID,
		"title", title,
		"icon_type", iconType)

	s.bus.Publish(userID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    title,
			Message:  message,
			IconType: iconType,
		},
	})
}

// GetSyncStatus returns the sync status for a playlist.
// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-010 (sync_status state machine)
type PlaylistSyncStatus struct {
	SyncEnabled     bool
	LastSyncedAt    *time.Time
	SyncError       string
	NavidromeID     string
	MatchedTracks   int
	TotalTracks     int
	MatchPercentage float64
	SyncStatus      string // State of sync: pending, syncing, success, warning, error
}

// GetPlaylistSyncStatus returns the sync status for a playlist.
func (s *PlaylistSyncService) GetPlaylistSyncStatus(ctx context.Context, playlistID int) (*PlaylistSyncStatus, error) {
	pl, err := s.client.Playlist.Query().
		Where(playlist.ID(playlistID)).
		Only(ctx)
	if err != nil {
		return nil, err
	}

	// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-010 (sync_status state machine)
	status := &PlaylistSyncStatus{
		SyncEnabled:   pl.SyncToNavidrome,
		NavidromeID:   pl.NavidromePlaylistID,
		MatchedTracks: pl.MatchedTrackCount,
		TotalTracks:   pl.TrackCount,
		SyncStatus:    string(pl.SyncStatus),
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
