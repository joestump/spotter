// Governing: ADR-0008 (OpenAI), ADR-0004 (Ent ORM), SPEC metadata-enrichment-pipeline

package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"spotter/ent"
	"spotter/ent/album"
	"spotter/ent/albumimage"
	"spotter/ent/artist"
	"spotter/ent/artistimage"
	"spotter/ent/listen"
	"spotter/ent/playlist"
	"spotter/ent/playlisttrack"
	"spotter/ent/schema"
	"spotter/ent/syncevent"
	"spotter/ent/track"
	"spotter/ent/user"
	"spotter/internal/config"
	"spotter/internal/enrichers"
	"spotter/internal/events"
)

// MetadataService handles catalog building and metadata enrichment.
type MetadataService struct {
	Client     *ent.Client
	Config     *config.Config
	Logger     *slog.Logger
	Bus        *events.Bus
	Registry   *enrichers.Registry
	httpClient *http.Client
}

// NewMetadataService creates a new metadata service.
func NewMetadataService(client *ent.Client, cfg *config.Config, logger *slog.Logger, bus *events.Bus) *MetadataService {
	return &MetadataService{
		Client:   client,
		Config:   cfg,
		Logger:   logger,
		Bus:      bus,
		Registry: enrichers.NewRegistry(),
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Register adds a new enricher factory to the service.
func (s *MetadataService) Register(t enrichers.Type, factory enrichers.Factory) {
	s.Registry.Register(t, factory)
}

// logEvent persists a sync event to the database.
func (s *MetadataService) logEvent(ctx context.Context, u *ent.User, eventType syncevent.EventType, provider string, message string, metadata map[string]interface{}) {
	builder := s.Client.SyncEvent.Create().
		SetUser(u).
		SetEventType(eventType).
		SetProvider(provider).
		SetMessage(message)

	if metadata != nil {
		if metadataJSON, err := json.Marshal(metadata); err == nil {
			builder.SetMetadata(string(metadataJSON))
		}
	}

	if _, err := builder.Save(ctx); err != nil {
		s.Logger.Warn("failed to log sync event", "event_type", eventType, "provider", provider, "error", err)
	}
}

// SyncAll performs a full metadata sync for a user.
// This scans listens/playlists, builds the catalog, and enriches metadata.
func (s *MetadataService) SyncAll(ctx context.Context, u *ent.User) error {
	if !s.Config.Metadata.Enabled {
		s.Logger.Debug("metadata enrichment disabled, skipping")
		return nil
	}

	s.Logger.Info("starting metadata sync", "username", u.Username)

	// Notify user
	s.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Metadata Enrichment",
			Message:  "Starting metadata enrichment...",
			IconType: "info",
		},
	})

	// Log start event
	s.logEvent(ctx, u, syncevent.EventTypeMetadataStarted, "metadata",
		"Started metadata enrichment", nil)

	// Refresh user with all edges
	refreshedUser, err := s.Client.User.Query().
		Where(user.ID(u.ID)).
		WithSpotifyAuth().
		WithNavidromeAuth().
		WithLastfmAuth().
		Only(ctx)
	if err != nil {
		s.logEvent(ctx, u, syncevent.EventTypeMetadataFailed, "metadata",
			fmt.Sprintf("Failed to refresh user: %v", err), nil)
		return fmt.Errorf("failed to refresh user: %w", err)
	}

	stats := map[string]interface{}{
		"artists_enriched":  0,
		"albums_enriched":   0,
		"tracks_enriched":   0,
		"images_downloaded": 0,
	}

	// Step 1: Build catalog from listens and playlists
	if err := s.BuildCatalog(ctx, refreshedUser); err != nil {
		s.Logger.Error("failed to build catalog", "error", err)
		s.logEvent(ctx, u, syncevent.EventTypeMetadataFailed, "metadata",
			fmt.Sprintf("Failed to build catalog: %v", err), nil)
		// Continue with enrichment for existing entries
	}

	// Step 1.5: Match listens to library entities
	matchedCount, err := s.MatchListens(ctx, refreshedUser)
	if err != nil {
		s.Logger.Error("failed to match listens", "error", err)
	} else {
		s.Logger.Info("matched listens to library", "count", matchedCount)
	}

	// Step 2: Enrich artists
	artistCount, err := s.EnrichArtists(ctx, refreshedUser)
	if err != nil {
		s.Logger.Error("failed to enrich artists", "error", err)
	}
	stats["artists_enriched"] = artistCount

	// Step 3: Enrich albums
	albumCount, err := s.EnrichAlbums(ctx, refreshedUser)
	if err != nil {
		s.Logger.Error("failed to enrich albums", "error", err)
	}
	stats["albums_enriched"] = albumCount

	// Step 4: Enrich tracks
	trackCount, err := s.EnrichTracks(ctx, refreshedUser)
	if err != nil {
		s.Logger.Error("failed to enrich tracks", "error", err)
	}
	stats["tracks_enriched"] = trackCount

	// Step 5: Download images
	if s.Config.Metadata.Images.Download {
		imageCount, err := s.DownloadImages(ctx, refreshedUser)
		if err != nil {
			s.Logger.Error("failed to download images", "error", err)
		}
		stats["images_downloaded"] = imageCount
	}

	// Log completion
	s.logEvent(ctx, u, syncevent.EventTypeMetadataCompleted, "metadata",
		fmt.Sprintf("Completed metadata enrichment: %d artists, %d albums, %d tracks enriched",
			stats["artists_enriched"], stats["albums_enriched"], stats["tracks_enriched"]), stats)

	// Notify user
	s.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Metadata Enrichment Complete",
			Message:  fmt.Sprintf("Enriched %d artists, %d albums, %d tracks", stats["artists_enriched"], stats["albums_enriched"], stats["tracks_enriched"]),
			IconType: "success",
		},
	})

	s.Logger.Info("metadata sync completed", "username", u.Username, "stats", stats)
	return nil
}

// BuildCatalog scans listens and playlists to create catalog entries.
func (s *MetadataService) BuildCatalog(ctx context.Context, u *ent.User) error {
	s.Logger.Info("building catalog from listens and playlists", "username", u.Username)

	// Get all listens for the user
	listens, err := s.Client.Listen.Query().
		Where(listen.HasUserWith(user.ID(u.ID))).
		All(ctx)
	if err != nil {
		return fmt.Errorf("failed to query listens: %w", err)
	}

	s.Logger.Debug("processing listens", "count", len(listens))

	artistsAdded := 0
	albumsAdded := 0
	tracksAdded := 0

	// Process listens
	for _, l := range listens {
		added, err := s.processListenEntry(ctx, u, l.ArtistName, l.AlbumName, l.TrackName)
		if err != nil {
			s.Logger.Warn("failed to process listen entry",
				"artist", l.ArtistName,
				"album", l.AlbumName,
				"track", l.TrackName,
				"error", err)
		}
		if added != nil {
			if added["artist"] {
				artistsAdded++
			}
			if added["album"] {
				albumsAdded++
			}
			if added["track"] {
				tracksAdded++
			}
		}
	}

	// Get all playlist tracks for the user
	playlistTracks, err := s.Client.PlaylistTrack.Query().
		Where(playlisttrack.HasPlaylistWith(playlist.HasUserWith(user.ID(u.ID)))).
		All(ctx)
	if err != nil {
		return fmt.Errorf("failed to query playlist tracks: %w", err)
	}

	s.Logger.Debug("processing playlist tracks", "count", len(playlistTracks))

	// Process playlist tracks (similar to listens)
	for _, pt := range playlistTracks {
		added, err := s.processListenEntry(ctx, u, pt.ArtistName, pt.AlbumName, pt.TrackName)
		if err != nil {
			s.Logger.Warn("failed to process playlist track entry",
				"artist", pt.ArtistName,
				"album", pt.AlbumName,
				"track", pt.TrackName,
				"error", err)
		}
		if added != nil {
			if added["artist"] {
				artistsAdded++
			}
			if added["album"] {
				albumsAdded++
			}
			if added["track"] {
				tracksAdded++
			}
		}
	}

	// Link playlist tracks to catalog entries
	linkedCount, err := s.linkPlaylistTracks(ctx, u)
	if err != nil {
		s.Logger.Warn("failed to link playlist tracks", "error", err)
	}

	s.Logger.Debug("linked playlist tracks to catalog", "count", linkedCount)

	// Log catalog build event
	s.logEvent(ctx, u, syncevent.EventTypeCatalogBuilt, "metadata",
		fmt.Sprintf("Built catalog: %d artists, %d albums, %d tracks added", artistsAdded, albumsAdded, tracksAdded),
		map[string]interface{}{
			"artists_added":             artistsAdded,
			"albums_added":              albumsAdded,
			"tracks_added":              tracksAdded,
			"listens_processed":         len(listens),
			"playlist_tracks_processed": len(playlistTracks),
			"playlist_tracks_linked":    linkedCount,
		})

	s.Logger.Info("catalog building completed",
		"username", u.Username,
		"listens_processed", len(listens),
		"playlist_tracks_processed", len(playlistTracks),
		"playlist_tracks_linked", linkedCount,
		"artists_added", artistsAdded,
		"albums_added", albumsAdded,
		"tracks_added", tracksAdded)

	return nil
}

// processListenEntry ensures artist, album, and track entries exist in the catalog.
// Returns a map indicating what was newly added.
func (s *MetadataService) processListenEntry(ctx context.Context, u *ent.User, artistName, albumName, trackName string) (map[string]bool, error) {
	if artistName == "" {
		return nil, nil
	}

	added := map[string]bool{"artist": false, "album": false, "track": false}

	// Get or create artist
	art, isNew, err := s.getOrCreateArtist(ctx, u, artistName)
	if err != nil {
		return nil, fmt.Errorf("failed to get/create artist: %w", err)
	}
	added["artist"] = isNew

	// Get or create album (if we have one)
	var alb *ent.Album
	if albumName != "" {
		alb, isNew, err = s.getOrCreateAlbum(ctx, u, art, albumName)
		if err != nil {
			return nil, fmt.Errorf("failed to get/create album: %w", err)
		}
		added["album"] = isNew
	}

	// Get or create track
	if trackName != "" {
		_, isNew, err = s.getOrCreateTrack(ctx, art, alb, trackName)
		if err != nil {
			return nil, fmt.Errorf("failed to get/create track: %w", err)
		}
		added["track"] = isNew
	}

	return added, nil
}

// linkPlaylistTracks links playlist tracks to their corresponding catalog entries.
func (s *MetadataService) linkPlaylistTracks(ctx context.Context, u *ent.User) (int, error) {
	// Get all unlinked playlist tracks for user's playlists
	playlistTracks, err := s.Client.PlaylistTrack.Query().
		Where(
			playlisttrack.HasPlaylistWith(playlist.HasUserWith(user.ID(u.ID))),
			playlisttrack.Not(playlisttrack.HasTrack()),
		).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query unlinked playlist tracks: %w", err)
	}

	linkedCount := 0
	for _, pt := range playlistTracks {
		// Try to find matching track in catalog
		t, err := s.Client.Track.Query().
			Where(
				track.Name(pt.TrackName),
				track.HasArtistWith(
					artist.HasUserWith(user.ID(u.ID)),
					artist.Name(pt.ArtistName),
				),
			).
			WithArtist().
			WithAlbum().
			First(ctx)
		if err != nil {
			continue // Track not in catalog yet
		}

		// Link the playlist track to the catalog track
		update := s.Client.PlaylistTrack.UpdateOne(pt).SetTrack(t)

		if t.Edges.Artist != nil {
			update.SetArtist(t.Edges.Artist)
		}
		if t.Edges.Album != nil {
			update.SetAlbum(t.Edges.Album)
		}

		if err := update.Exec(ctx); err != nil {
			s.Logger.Debug("failed to link playlist track", "track", pt.TrackName, "error", err)
			continue
		}
		linkedCount++
	}

	return linkedCount, nil
}

// getOrCreateArtist finds or creates an artist in the catalog.
// Governing: SPEC graceful-shutdown REQ-REC-003 (get-or-create ensures idempotent catalog building)
// Governing: SPEC graceful-shutdown REQ-REC-004 (ctx passed to DB ops; cancellation leaves DB consistent)
func (s *MetadataService) getOrCreateArtist(ctx context.Context, u *ent.User, name string) (*ent.Artist, bool, error) {
	// Try to find existing artist
	existing, err := s.Client.Artist.Query().
		Where(
			artist.HasUserWith(user.ID(u.ID)),
			artist.Name(name),
		).
		Only(ctx)
	if err == nil {
		return existing, false, nil
	}
	if !ent.IsNotFound(err) {
		return nil, false, err
	}

	// Create new artist
	newArtist, err := s.Client.Artist.Create().
		SetName(name).
		SetUser(u).
		Save(ctx)
	if err != nil {
		return nil, false, err
	}
	return newArtist, true, nil
}

// getOrCreateAlbum finds or creates an album in the catalog.
func (s *MetadataService) getOrCreateAlbum(ctx context.Context, u *ent.User, art *ent.Artist, name string) (*ent.Album, bool, error) {
	// Try to find existing album
	existing, err := s.Client.Album.Query().
		Where(
			album.HasUserWith(user.ID(u.ID)),
			album.HasArtistWith(artist.ID(art.ID)),
			album.Name(name),
		).
		Only(ctx)
	if err == nil {
		return existing, false, nil
	}
	if !ent.IsNotFound(err) {
		return nil, false, err
	}

	// Create new album
	newAlbum, err := s.Client.Album.Create().
		SetName(name).
		SetUser(u).
		SetArtist(art).
		Save(ctx)
	if err != nil {
		return nil, false, err
	}
	return newAlbum, true, nil
}

// getOrCreateTrack finds or creates a track in the catalog.
func (s *MetadataService) getOrCreateTrack(ctx context.Context, art *ent.Artist, alb *ent.Album, name string) (*ent.Track, bool, error) {
	// Build query
	query := s.Client.Track.Query().
		Where(
			track.Name(name),
			track.HasArtistWith(artist.ID(art.ID)),
		)

	if alb != nil {
		query = query.Where(track.HasAlbumWith(album.ID(alb.ID)))
	}

	existing, err := query.Only(ctx)
	if err == nil {
		return existing, false, nil
	}
	if !ent.IsNotFound(err) {
		return nil, false, err
	}

	// Create new track
	create := s.Client.Track.Create().
		SetName(name).
		SetArtist(art)

	if alb != nil {
		create = create.SetAlbum(alb)
	}

	newTrack, err := create.Save(ctx)
	if err != nil {
		return nil, false, err
	}
	return newTrack, true, nil
}

// getActiveEnrichers returns enrichers in the configured order.
func (s *MetadataService) getActiveEnrichers(ctx context.Context, u *ent.User) ([]enrichers.Enricher, error) {
	order := s.Config.MetadataEnricherOrder()
	var active []enrichers.Enricher

	for _, name := range order {
		t, ok := enrichers.ParseType(name)
		if !ok {
			s.Logger.Warn("unknown enricher type in order", "type", name)
			continue
		}

		factory, ok := s.Registry.Get(t)
		if !ok {
			s.Logger.Debug("no factory registered for enricher", "type", t)
			continue
		}

		enricher, err := factory(ctx, u)
		if err != nil {
			s.Logger.Error("failed to create enricher", "type", t, "error", err)
			continue
		}
		if enricher == nil {
			s.Logger.Debug("enricher not available", "type", t)
			continue
		}
		if !enricher.IsAvailable() {
			s.Logger.Debug("enricher not configured", "type", t)
			continue
		}

		active = append(active, enricher)
		s.Logger.Info("enricher activated", "type", t, "name", enricher.Name())
	}

	// Log summary of active enrichers
	enricherNames := make([]string, len(active))
	for i, e := range active {
		enricherNames[i] = e.Name()
	}
	s.Logger.Info("active enrichers for user", "count", len(active), "enrichers", enricherNames)

	return active, nil
}

// Governing: ADR-0019 (structured metrics), SPEC observability REQ "BG-004"
// Governing: SPEC graceful-shutdown REQ-REC-003 (metadata enrichment tracks per-entity state via LastEnrichedAt)
func (s *MetadataService) EnrichArtists(ctx context.Context, u *ent.User) (int, error) {
	enrichStart := time.Now()
	s.Logger.Info("enriching artists", "username", u.Username)

	enricherList, err := s.getActiveEnrichers(ctx, u)
	if err != nil {
		return 0, err
	}

	// Get artists that need enrichment (not enriched in the last 24 hours)
	// OR that need AI enrichment (never AI enriched or AI enriched more than 7 days ago)
	cutoff := time.Now().Add(-24 * time.Hour)
	aiCutoff := time.Now().Add(-7 * 24 * time.Hour)
	artists, err := s.Client.Artist.Query().
		Where(
			artist.HasUserWith(user.ID(u.ID)),
			artist.Or(
				artist.LastEnrichedAtIsNil(),
				artist.LastEnrichedAtLT(cutoff),
				artist.LastAiEnrichedAtIsNil(),
				artist.LastAiEnrichedAtLT(aiCutoff),
				artist.LidarrIDIsNil(),
			),
		).
		WithAlbums().
		WithTracks(func(q *ent.TrackQuery) {
			q.WithAlbum()
		}).
		WithImages().
		Limit(100). // Process in batches
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query artists: %w", err)
	}

	s.Logger.Info("found artists to enrich", "count", len(artists))
	for _, art := range artists {
		needsRegular := art.LastEnrichedAt == nil || art.LastEnrichedAt.Before(cutoff)
		needsAI := art.LastAiEnrichedAt == nil || art.LastAiEnrichedAt.Before(aiCutoff)
		s.Logger.Debug("artist enrichment status",
			"artist", art.Name,
			"needs_regular", needsRegular,
			"needs_ai", needsAI,
			"last_enriched", art.LastEnrichedAt,
			"last_ai_enriched", art.LastAiEnrichedAt,
			"has_albums", len(art.Edges.Albums),
			"has_tracks", len(art.Edges.Tracks),
			"has_images", len(art.Edges.Images))
	}

	enrichedCount := 0
	for _, art := range artists {
		if err := s.enrichArtist(ctx, u, art, enricherList); err != nil {
			s.Logger.Warn("failed to enrich artist", "artist", art.Name, "error", err)
			continue
		}
		enrichedCount++
	}

	enrichSuccess := true
	var enrichErr string
	if enrichedCount < len(artists) && len(artists) > 0 {
		enrichSuccess = false
		enrichErr = fmt.Sprintf("%d of %d artists failed enrichment", len(artists)-enrichedCount, len(artists))
	}
	s.Logger.Info("metric.enricher",
		"enricher", "artists",
		"entity_type", "artist",
		"entities_processed", len(artists),
		"duration_ms", time.Since(enrichStart).Milliseconds(),
		"success", enrichSuccess,
		"error", enrichErr)

	return enrichedCount, nil
}

// enrichArtist runs all enrichers on a single artist.
func (s *MetadataService) enrichArtist(ctx context.Context, u *ent.User, art *ent.Artist, enricherList []enrichers.Enricher) error {
	s.Logger.Debug("enriching artist", "name", art.Name)

	update := s.Client.Artist.UpdateOne(art)
	var allTags []string
	var allGenres []string
	enrichersUsed := []string{}

	for _, e := range enricherList {
		artistEnricher, ok := e.(enrichers.ArtistEnricher)
		if !ok {
			continue
		}

		data, err := artistEnricher.EnrichArtist(ctx, art)
		if err != nil {
			s.Logger.Warn("enricher failed for artist",
				"enricher", e.Name(),
				"artist", art.Name,
				"error", err)
			continue
		}
		if data == nil {
			continue
		}

		enrichersUsed = append(enrichersUsed, e.Name())

		// Apply enrichment data (Artist has string fields, not *string)
		if data.MusicBrainzID != "" && art.MusicbrainzID == "" {
			update = update.SetMusicbrainzID(data.MusicBrainzID)
		}
		if data.SpotifyID != "" && art.SpotifyID == "" {
			update = update.SetSpotifyID(data.SpotifyID)
		}
		if data.NavidromeID != "" && art.NavidromeID == "" {
			update = update.SetNavidromeID(data.NavidromeID)
		}
		if data.LidarrID != "" && art.LidarrID == "" {
			update = update.SetLidarrID(data.LidarrID)
		}
		if data.LastFMURL != "" && art.LastfmURL == "" {
			update = update.SetLastfmURL(data.LastFMURL)
		}
		if data.SortName != "" && art.SortName == "" {
			update = update.SetSortName(data.SortName)
		}
		if data.Bio != "" && art.Bio == "" {
			update = update.SetBio(data.Bio)
		}
		if data.Popularity != nil && art.Popularity == nil {
			update = update.SetPopularity(*data.Popularity)
		}
		if data.FollowerCount != nil && art.FollowerCount == nil {
			update = update.SetFollowerCount(*data.FollowerCount)
		}

		// Merge tags and genres
		allTags = append(allTags, data.Tags...)
		allGenres = append(allGenres, data.Genres...)

		// Handle AI-specific fields
		if data.AISummary != "" {
			update = update.SetAiSummary(data.AISummary)
		}
		if data.AIBiography != "" {
			update = update.SetAiBiography(data.AIBiography)
		}
		if len(data.AITags) > 0 {
			update = update.SetAiTags(data.AITags)
			update = update.SetLastAiEnrichedAt(time.Now())
		}

		// Get images
		images, err := artistEnricher.GetArtistImages(ctx, art)
		if err != nil {
			s.Logger.Warn("failed to get artist images",
				"enricher", e.Name(),
				"artist", art.Name,
				"error", err)
		} else {
			if err := s.saveArtistImages(ctx, art, images); err != nil {
				s.Logger.Warn("failed to save artist images", "artist", art.Name, "error", err)
			}
		}
	}

	// Deduplicate and set tags/genres
	if len(allTags) > 0 {
		update = update.SetTags(uniqueStrings(allTags))
	}
	if len(allGenres) > 0 {
		update = update.SetGenres(uniqueStrings(allGenres))
	}

	// Update last enriched timestamp
	update = update.SetLastEnrichedAt(time.Now())

	_, err := update.Save(ctx)
	if err != nil {
		return err
	}

	// Log enrichment event
	if len(enrichersUsed) > 0 {
		s.logEvent(ctx, u, syncevent.EventTypeArtistEnriched, "metadata",
			fmt.Sprintf("Enriched artist: %s", art.Name),
			map[string]interface{}{
				"artist":    art.Name,
				"enrichers": enrichersUsed,
			})
	}

	return nil
}

// saveArtistImages saves artist images to the database.
func (s *MetadataService) saveArtistImages(ctx context.Context, art *ent.Artist, images []enrichers.ImageData) error {
	for _, img := range images {
		// Check if image already exists
		exists, err := s.Client.ArtistImage.Query().
			Where(
				artistimage.HasArtistWith(artist.ID(art.ID)),
				artistimage.URL(img.URL),
			).
			Exist(ctx)
		if err != nil {
			return err
		}
		if exists {
			continue
		}

		// Create image record
		create := s.Client.ArtistImage.Create().
			SetArtist(art).
			SetSource(img.Source).
			SetURL(img.URL).
			SetIsPrimary(img.IsPrimary)

		// Map image type
		switch img.Type {
		case "thumbnail":
			create = create.SetImageType(artistimage.ImageTypeThumbnail)
		case "background":
			create = create.SetImageType(artistimage.ImageTypeBackground)
		case "logo":
			create = create.SetImageType(artistimage.ImageTypeLogo)
		case "banner":
			create = create.SetImageType(artistimage.ImageTypeBanner)
		case "fanart":
			create = create.SetImageType(artistimage.ImageTypeFanart)
		default:
			create = create.SetImageType(artistimage.ImageTypeThumbnail)
		}

		if img.Width > 0 {
			create = create.SetWidth(img.Width)
		}
		if img.Height > 0 {
			create = create.SetHeight(img.Height)
		}
		if img.Likes > 0 {
			create = create.SetLikes(img.Likes)
		}

		if _, err := create.Save(ctx); err != nil {
			s.Logger.Warn("failed to save artist image", "url", img.URL, "error", err)
		}
	}

	return nil
}

// Governing: ADR-0019 (structured metrics), SPEC observability REQ "BG-004"
// EnrichAlbums runs enrichment on all albums that need it.
func (s *MetadataService) EnrichAlbums(ctx context.Context, u *ent.User) (int, error) {
	enrichStart := time.Now()
	s.Logger.Info("enriching albums", "username", u.Username)

	enricherList, err := s.getActiveEnrichers(ctx, u)
	if err != nil {
		return 0, err
	}

	// Get albums that need enrichment
	// OR that need AI enrichment (never AI enriched or AI enriched more than 7 days ago)
	cutoff := time.Now().Add(-24 * time.Hour)
	aiCutoff := time.Now().Add(-7 * 24 * time.Hour)
	albums, err := s.Client.Album.Query().
		Where(
			album.HasUserWith(user.ID(u.ID)),
			album.Or(
				album.LastEnrichedAtIsNil(),
				album.LastEnrichedAtLT(cutoff),
				album.LastAiEnrichedAtIsNil(),
				album.LastAiEnrichedAtLT(aiCutoff),
				album.LidarrIDIsNil(),
			),
		).
		WithArtist().
		WithTracks().
		WithImages().
		Limit(100).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query albums: %w", err)
	}

	s.Logger.Info("found albums to enrich", "count", len(albums))
	for _, alb := range albums {
		needsRegular := alb.LastEnrichedAt.IsZero() || alb.LastEnrichedAt.Before(cutoff)
		needsAI := alb.LastAiEnrichedAt == nil || alb.LastAiEnrichedAt.Before(aiCutoff)
		s.Logger.Debug("album enrichment status",
			"album", alb.Name,
			"needs_regular", needsRegular,
			"needs_ai", needsAI,
			"last_enriched", alb.LastEnrichedAt,
			"last_ai_enriched", alb.LastAiEnrichedAt,
			"has_tracks", len(alb.Edges.Tracks),
			"has_images", len(alb.Edges.Images))
	}

	enrichedCount := 0
	for _, alb := range albums {
		if err := s.enrichAlbum(ctx, u, alb, enricherList); err != nil {
			s.Logger.Warn("failed to enrich album", "album", alb.Name, "error", err)
			continue
		}
		enrichedCount++
	}

	enrichSuccess := true
	var enrichErr string
	if enrichedCount < len(albums) && len(albums) > 0 {
		enrichSuccess = false
		enrichErr = fmt.Sprintf("%d of %d albums failed enrichment", len(albums)-enrichedCount, len(albums))
	}
	s.Logger.Info("metric.enricher",
		"enricher", "albums",
		"entity_type", "album",
		"entities_processed", len(albums),
		"duration_ms", time.Since(enrichStart).Milliseconds(),
		"success", enrichSuccess,
		"error", enrichErr)

	return enrichedCount, nil
}

// SyncAllArtistImages re-fetches images for all artists from all enrichers.
// This forces a refresh of artist images regardless of when they were last enriched.
func (s *MetadataService) SyncAllArtistImages(ctx context.Context, u *ent.User) (int, error) {
	s.Logger.Info("syncing all artist images", "username", u.Username)

	enricherList, err := s.getActiveEnrichers(ctx, u)
	if err != nil {
		return 0, err
	}

	// Get all artists for the user
	artists, err := s.Client.Artist.Query().
		Where(artist.HasUserWith(user.ID(u.ID))).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query artists: %w", err)
	}

	s.Logger.Debug("found artists to sync images", "count", len(artists))

	syncedCount := 0
	for _, art := range artists {
		imagesFound := false
		for _, e := range enricherList {
			artistEnricher, ok := e.(enrichers.ArtistEnricher)
			if !ok {
				continue
			}

			images, err := artistEnricher.GetArtistImages(ctx, art)
			if err != nil {
				s.Logger.Warn("failed to get artist images",
					"enricher", e.Name(),
					"artist", art.Name,
					"error", err)
				continue
			}

			if len(images) > 0 {
				if err := s.saveArtistImages(ctx, art, images); err != nil {
					s.Logger.Warn("failed to save artist images", "artist", art.Name, "error", err)
				} else {
					imagesFound = true
				}
			}
		}
		if imagesFound {
			syncedCount++
		}
	}

	s.logEvent(ctx, u, syncevent.EventTypeImageDownloaded, "metadata",
		fmt.Sprintf("Synced images for %d artists", syncedCount),
		map[string]interface{}{"artists_synced": syncedCount})

	return syncedCount, nil
}

// SyncAllAlbumImages re-fetches images for all albums from all enrichers.
// This forces a refresh of album images regardless of when they were last enriched.
func (s *MetadataService) SyncAllAlbumImages(ctx context.Context, u *ent.User) (int, error) {
	s.Logger.Info("syncing all album images", "username", u.Username)

	enricherList, err := s.getActiveEnrichers(ctx, u)
	if err != nil {
		return 0, err
	}

	// Get all albums for the user with their artists
	albums, err := s.Client.Album.Query().
		Where(album.HasUserWith(user.ID(u.ID))).
		WithArtist().
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query albums: %w", err)
	}

	s.Logger.Debug("found albums to sync images", "count", len(albums))

	syncedCount := 0
	for _, alb := range albums {
		imagesFound := false
		for _, e := range enricherList {
			albumEnricher, ok := e.(enrichers.AlbumEnricher)
			if !ok {
				continue
			}

			images, err := albumEnricher.GetAlbumImages(ctx, alb)
			if err != nil {
				s.Logger.Warn("failed to get album images",
					"enricher", e.Name(),
					"album", alb.Name,
					"error", err)
				continue
			}

			if len(images) > 0 {
				if err := s.saveAlbumImages(ctx, alb, images); err != nil {
					s.Logger.Warn("failed to save album images", "album", alb.Name, "error", err)
				} else {
					imagesFound = true
				}
			}
		}
		if imagesFound {
			syncedCount++
		}
	}

	s.logEvent(ctx, u, syncevent.EventTypeImageDownloaded, "metadata",
		fmt.Sprintf("Synced images for %d albums", syncedCount),
		map[string]interface{}{"albums_synced": syncedCount})

	return syncedCount, nil
}

func (s *MetadataService) enrichAlbum(ctx context.Context, u *ent.User, alb *ent.Album, enricherList []enrichers.Enricher) error {
	s.Logger.Debug("enriching album", "name", alb.Name)

	update := s.Client.Album.UpdateOne(alb)
	var allTags []string
	enrichersUsed := []string{}

	for _, e := range enricherList {
		albumEnricher, ok := e.(enrichers.AlbumEnricher)
		if !ok {
			continue
		}

		data, err := albumEnricher.EnrichAlbum(ctx, alb)
		if err != nil {
			s.Logger.Warn("enricher failed for album",
				"enricher", e.Name(),
				"album", alb.Name,
				"error", err)
			continue
		}
		if data == nil {
			continue
		}

		enrichersUsed = append(enrichersUsed, e.Name())

		// Apply enrichment data (Album has string fields, not *string)
		if data.MusicBrainzID != "" && alb.MusicbrainzID == "" {
			update = update.SetMusicbrainzID(data.MusicBrainzID)
		}
		if data.SpotifyID != "" && alb.SpotifyID == "" {
			update = update.SetSpotifyID(data.SpotifyID)
		}
		if data.LidarrID != "" && alb.LidarrID == "" {
			update = update.SetLidarrID(data.LidarrID)
		}
		if data.ReleaseDate != "" && alb.ReleaseDate == "" {
			update = update.SetReleaseDate(data.ReleaseDate)
		}
		if data.Year > 0 && alb.Year == 0 {
			update = update.SetYear(data.Year)
		}
		if data.Genre != "" && alb.Genre == "" {
			update = update.SetGenre(data.Genre)
		}
		if data.AlbumType != "" && alb.AlbumType == "" {
			update = update.SetAlbumType(data.AlbumType)
		}
		if data.Label != "" && alb.Label == "" {
			update = update.SetLabel(data.Label)
		}
		if data.TotalTracks > 0 && alb.TotalTracks == 0 {
			update = update.SetTotalTracks(data.TotalTracks)
		}
		if data.Popularity > 0 && alb.Popularity == 0 {
			update = update.SetPopularity(data.Popularity)
		}

		allTags = append(allTags, data.Tags...)

		// Handle AI-specific fields
		if data.AISummary != "" {
			update = update.SetAiSummary(data.AISummary)
		}
		if len(data.AITags) > 0 {
			update = update.SetAiTags(data.AITags)
		}
		if len(data.DominantColors) > 0 {
			update = update.SetDominantColors(data.DominantColors)
		}
		if data.CoverArtCommentary != "" {
			update = update.SetCoverArtCommentary(data.CoverArtCommentary)
		}
		if len(data.Recommendations) > 0 {
			recs := make([]schema.AlbumRecommendation, len(data.Recommendations))
			for i, r := range data.Recommendations {
				recs[i] = schema.AlbumRecommendation{
					Name:      r.Name,
					Artist:    r.Artist,
					SpotifyID: r.SpotifyID,
					Reason:    r.Reason,
					ImageURL:  r.ImageURL,
					Year:      r.Year,
				}
			}
			update = update.SetRecommendations(recs)
		}
		// If any AI fields were set, update the timestamp
		if data.AISummary != "" || len(data.AITags) > 0 || len(data.DominantColors) > 0 || data.CoverArtCommentary != "" || len(data.Recommendations) > 0 {
			update = update.SetLastAiEnrichedAt(time.Now())
		}

		// Get images
		images, err := albumEnricher.GetAlbumImages(ctx, alb)
		if err != nil {
			s.Logger.Warn("failed to get album images",
				"enricher", e.Name(),
				"album", alb.Name,
				"error", err)
		} else {
			if err := s.saveAlbumImages(ctx, alb, images); err != nil {
				s.Logger.Warn("failed to save album images", "album", alb.Name, "error", err)
			}
		}
	}

	if len(allTags) > 0 {
		update = update.SetTags(uniqueStrings(allTags))
	}

	update = update.SetLastEnrichedAt(time.Now())

	_, err := update.Save(ctx)
	if err != nil {
		return err
	}

	// Log enrichment event
	if len(enrichersUsed) > 0 {
		s.logEvent(ctx, u, syncevent.EventTypeAlbumEnriched, "metadata",
			fmt.Sprintf("Enriched album: %s", alb.Name),
			map[string]interface{}{
				"album":     alb.Name,
				"enrichers": enrichersUsed,
			})
	}

	return nil
}

// saveAlbumImages saves album images to the database.
func (s *MetadataService) saveAlbumImages(ctx context.Context, alb *ent.Album, images []enrichers.ImageData) error {
	for _, img := range images {
		// Check URL - for local URLs we use a different identifier
		imgURL := img.URL
		if imgURL == "" {
			continue
		}

		// Check if image already exists
		exists, err := s.Client.AlbumImage.Query().
			Where(
				albumimage.HasAlbumWith(album.ID(alb.ID)),
				albumimage.URL(imgURL),
			).
			Exist(ctx)
		if err != nil {
			return err
		}
		if exists {
			continue
		}

		// Create image record
		create := s.Client.AlbumImage.Create().
			SetAlbum(alb).
			SetSource(img.Source).
			SetURL(imgURL).
			SetIsPrimary(img.IsPrimary)

		// Map image type
		switch img.Type {
		case "cover_front":
			create = create.SetImageType(albumimage.ImageTypeCoverFront)
		case "cover_back":
			create = create.SetImageType(albumimage.ImageTypeCoverBack)
		case "cd_art":
			create = create.SetImageType(albumimage.ImageTypeCdArt)
		case "booklet":
			create = create.SetImageType(albumimage.ImageTypeBooklet)
		case "spine":
			create = create.SetImageType(albumimage.ImageTypeSpine)
		default:
			create = create.SetImageType(albumimage.ImageTypeCoverFront)
		}

		if img.Width > 0 {
			create = create.SetWidth(img.Width)
		}
		if img.Height > 0 {
			create = create.SetHeight(img.Height)
		}

		if _, err := create.Save(ctx); err != nil {
			s.Logger.Warn("failed to save album image", "url", imgURL, "error", err)
		}
	}

	return nil
}

// Governing: ADR-0019 (structured metrics), SPEC observability REQ "BG-004"
// EnrichTracks runs enrichment on all tracks that need it.
func (s *MetadataService) EnrichTracks(ctx context.Context, u *ent.User) (int, error) {
	enrichStart := time.Now()
	s.Logger.Info("enriching tracks", "username", u.Username)

	enricherList, err := s.getActiveEnrichers(ctx, u)
	if err != nil {
		return 0, err
	}

	// Get tracks that need enrichment
	// OR that need AI enrichment (never AI enriched or AI enriched more than 7 days ago)
	cutoff := time.Now().Add(-24 * time.Hour)
	aiCutoff := time.Now().Add(-7 * 24 * time.Hour)
	// Get tracks via their artists (which belong to users)
	tracks, err := s.Client.Track.Query().
		Where(
			track.HasArtistWith(artist.HasUserWith(user.ID(u.ID))),
			track.Or(
				track.LastEnrichedAtIsNil(),
				track.LastEnrichedAtLT(cutoff),
				track.LastAiEnrichedAtIsNil(),
				track.LastAiEnrichedAtLT(aiCutoff),
				track.LidarrIDIsNil(),
				track.LidarrStatusIn("pending", "monitored", "grabbed"),
			),
		).
		WithArtist(func(q *ent.ArtistQuery) {
			q.Where(artist.HasUserWith(user.ID(u.ID)))
		}).
		WithAlbum().
		Limit(200).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query tracks: %w", err)
	}

	// Filter to only tracks that belong to this user's artists
	var userTracks []*ent.Track
	for _, t := range tracks {
		if t.Edges.Artist != nil {
			userTracks = append(userTracks, t)
		}
	}

	s.Logger.Info("found tracks to enrich", "count", len(userTracks))

	enrichedCount := 0
	for _, t := range userTracks {
		if err := s.enrichTrack(ctx, u, t, enricherList); err != nil {
			s.Logger.Warn("failed to enrich track", "track", t.Name, "error", err)
			continue
		}
		enrichedCount++
	}

	enrichSuccess := true
	var enrichErr string
	if enrichedCount < len(userTracks) && len(userTracks) > 0 {
		enrichSuccess = false
		enrichErr = fmt.Sprintf("%d of %d tracks failed enrichment", len(userTracks)-enrichedCount, len(userTracks))
	}
	s.Logger.Info("metric.enricher",
		"enricher", "tracks",
		"entity_type", "track",
		"entities_processed", len(userTracks),
		"duration_ms", time.Since(enrichStart).Milliseconds(),
		"success", enrichSuccess,
		"error", enrichErr)

	return enrichedCount, nil
}

// enrichTrack runs all enrichers on a single track.
func (s *MetadataService) enrichTrack(ctx context.Context, u *ent.User, t *ent.Track, enricherList []enrichers.Enricher) error {
	s.Logger.Debug("enriching track", "name", t.Name)

	update := s.Client.Track.UpdateOne(t)
	var allTags []string
	var allGenres []string
	enrichersUsed := []string{}

	for _, e := range enricherList {
		trackEnricher, ok := e.(enrichers.TrackEnricher)
		if !ok {
			continue
		}

		data, err := trackEnricher.EnrichTrack(ctx, t)
		if err != nil {
			s.Logger.Warn("enricher failed for track",
				"enricher", e.Name(),
				"track", t.Name,
				"error", err)
			continue
		}
		if data == nil {
			continue
		}

		enrichersUsed = append(enrichersUsed, e.Name())

		// Apply enrichment data (Track has *string fields due to Nillable())
		if data.MusicBrainzID != "" && (t.MusicbrainzID == nil || *t.MusicbrainzID == "") {
			update = update.SetMusicbrainzID(data.MusicBrainzID)
		}
		if data.SpotifyID != "" && (t.SpotifyID == nil || *t.SpotifyID == "") {
			update = update.SetSpotifyID(data.SpotifyID)
		}
		if data.NavidromeID != "" && (t.NavidromeID == nil || *t.NavidromeID == "") {
			update = update.SetNavidromeID(data.NavidromeID)
		}
		if data.LidarrID != "" && (t.LidarrID == nil || *t.LidarrID == "") {
			update = update.SetLidarrID(data.LidarrID)
		}
		if data.LidarrStatus != "" {
			update = update.SetLidarrStatus(data.LidarrStatus)
		}
		if data.ISRC != "" && (t.Isrc == nil || *t.Isrc == "") {
			update = update.SetIsrc(data.ISRC)
		}
		if data.DurationMs > 0 && (t.DurationMs == nil || *t.DurationMs == 0) {
			update = update.SetDurationMs(data.DurationMs)
		}
		if data.TrackNumber > 0 && (t.TrackNumber == nil || *t.TrackNumber == 0) {
			update = update.SetTrackNumber(data.TrackNumber)
		}
		if data.DiscNumber > 0 && (t.DiscNumber == nil || *t.DiscNumber == 0) {
			update = update.SetDiscNumber(data.DiscNumber)
		}
		if data.BPM != nil && t.Bpm == nil {
			update = update.SetBpm(*data.BPM)
		}
		if data.MusicalKey != "" && (t.MusicalKey == nil || *t.MusicalKey == "") {
			update = update.SetMusicalKey(data.MusicalKey)
		}
		if data.Energy != nil && t.Energy == nil {
			update = update.SetEnergy(*data.Energy)
		}
		if data.Danceability != nil && t.Danceability == nil {
			update = update.SetDanceability(*data.Danceability)
		}
		if data.Valence != nil && t.Valence == nil {
			update = update.SetValence(*data.Valence)
		}
		if data.Acousticness != nil && t.Acousticness == nil {
			update = update.SetAcousticness(*data.Acousticness)
		}
		if data.Instrumentalness != nil && t.Instrumentalness == nil {
			update = update.SetInstrumentalness(*data.Instrumentalness)
		}
		if data.Popularity != nil && t.Popularity == nil {
			update = update.SetPopularity(*data.Popularity)
		}
		if data.SpotifyURL != "" && (t.SpotifyURL == nil || *t.SpotifyURL == "") {
			update = update.SetSpotifyURL(data.SpotifyURL)
		}
		if data.MusicBrainzURL != "" && (t.MusicbrainzURL == nil || *t.MusicbrainzURL == "") {
			update = update.SetMusicbrainzURL(data.MusicBrainzURL)
		}

		allTags = append(allTags, data.Tags...)
		allGenres = append(allGenres, data.Genres...)

		// Handle AI-specific fields
		if data.AISummary != "" {
			update = update.SetAiSummary(data.AISummary)
		}
		if len(data.AITags) > 0 {
			update = update.SetAiTags(data.AITags)
			update = update.SetLastAiEnrichedAt(time.Now())
		}
	}

	if len(allTags) > 0 {
		update = update.SetTags(uniqueStrings(allTags))
	}
	if len(allGenres) > 0 {
		update = update.SetGenres(uniqueStrings(allGenres))
	}

	update = update.SetLastEnrichedAt(time.Now())

	_, err := update.Save(ctx)
	if err != nil {
		return err
	}

	// Log enrichment event (only for tracks with enrichers used to avoid spam)
	if len(enrichersUsed) > 0 {
		s.logEvent(ctx, u, syncevent.EventTypeTrackEnriched, "metadata",
			fmt.Sprintf("Enriched track: %s", t.Name),
			map[string]interface{}{
				"track":     t.Name,
				"enrichers": enrichersUsed,
			})
	}

	return nil
}

// DownloadImages downloads all pending images to local storage.
func (s *MetadataService) DownloadImages(ctx context.Context, u *ent.User) (int, error) {
	s.Logger.Info("downloading images", "username", u.Username)

	baseDir := s.Config.Metadata.Images.Directory
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create images directory: %w", err)
	}

	// Repair stale paths where local_path is set but the file no longer exists on disk.
	// This handles container recreation where the data directory is lost.
	s.repairStaleImagePaths(ctx, u)

	downloadedCount := 0

	// Download artist images (null or empty local_path)
	artistImages, err := s.Client.ArtistImage.Query().
		Where(
			artistimage.Or(artistimage.LocalPathIsNil(), artistimage.LocalPathEQ("")),
			artistimage.HasArtistWith(artist.HasUserWith(user.ID(u.ID))),
		).
		WithArtist().
		Limit(50).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query artist images: %w", err)
	}

	for _, img := range artistImages {
		if err := s.downloadArtistImage(ctx, u, img, baseDir); err != nil {
			s.Logger.Warn("failed to download artist image", "url", img.URL, "error", err)
		} else {
			downloadedCount++
		}
	}

	// Download album images (null or empty local_path)
	albumImages, err := s.Client.AlbumImage.Query().
		Where(
			albumimage.Or(albumimage.LocalPathIsNil(), albumimage.LocalPathEQ("")),
			albumimage.HasAlbumWith(album.HasUserWith(user.ID(u.ID))),
		).
		WithAlbum().
		Limit(50).
		All(ctx)
	if err != nil {
		return downloadedCount, fmt.Errorf("failed to query album images: %w", err)
	}

	for _, img := range albumImages {
		if err := s.downloadAlbumImage(ctx, u, img, baseDir); err != nil {
			s.Logger.Warn("failed to download album image", "error", err)
		} else {
			downloadedCount++
		}
	}

	return downloadedCount, nil
}

// repairStaleImagePaths clears local_path for image records where the file no longer exists on disk.
// This self-heals after container recreation where the data directory is lost.
func (s *MetadataService) repairStaleImagePaths(ctx context.Context, u *ent.User) {
	artistImages, err := s.Client.ArtistImage.Query().
		Where(
			artistimage.LocalPathNotNil(),
			artistimage.LocalPathNEQ(""),
			artistimage.HasArtistWith(artist.HasUserWith(user.ID(u.ID))),
		).
		All(ctx)
	if err != nil {
		s.Logger.Warn("failed to query artist images for repair", "error", err)
	} else {
		for _, img := range artistImages {
			if _, err := os.Stat(img.LocalPath); os.IsNotExist(err) {
				if _, err := s.Client.ArtistImage.UpdateOne(img).ClearLocalPath().Save(ctx); err != nil {
					s.Logger.Warn("failed to clear stale artist image path", "id", img.ID, "path", img.LocalPath, "error", err)
				} else {
					s.Logger.Info("cleared stale artist image path", "id", img.ID, "path", img.LocalPath)
				}
			}
		}
	}

	albumImages, err := s.Client.AlbumImage.Query().
		Where(
			albumimage.LocalPathNotNil(),
			albumimage.LocalPathNEQ(""),
			albumimage.HasAlbumWith(album.HasUserWith(user.ID(u.ID))),
		).
		All(ctx)
	if err != nil {
		s.Logger.Warn("failed to query album images for repair", "error", err)
	} else {
		for _, img := range albumImages {
			if _, err := os.Stat(img.LocalPath); os.IsNotExist(err) {
				if _, err := s.Client.AlbumImage.UpdateOne(img).ClearLocalPath().Save(ctx); err != nil {
					s.Logger.Warn("failed to clear stale album image path", "id", img.ID, "path", img.LocalPath, "error", err)
				} else {
					s.Logger.Info("cleared stale album image path", "id", img.ID, "path", img.LocalPath)
				}
			}
		}
	}
}

// downloadArtistImage downloads a single artist image.
func (s *MetadataService) downloadArtistImage(ctx context.Context, u *ent.User, img *ent.ArtistImage, baseDir string) error {
	if img.URL == "" {
		return nil
	}

	// Create directory for artists
	artistDir := filepath.Join(baseDir, "artists")
	if err := os.MkdirAll(artistDir, 0755); err != nil {
		return err
	}

	// Determine filename using artist ID and image type (e.g., 123-hero.png)
	ext := getImageExtension(img.URL)
	filename := fmt.Sprintf("%d-%s%s", img.Edges.Artist.ID, img.ImageType.String(), ext)
	localPath := filepath.Join(artistDir, filename)

	// Check if file already exists on disk
	if _, err := os.Stat(localPath); err == nil {
		// File exists, just update database without downloading again
		_, err := s.Client.ArtistImage.UpdateOne(img).
			SetLocalPath(localPath).
			Save(ctx)
		return err
	}

	// Download image
	if err := s.downloadFile(ctx, img.URL, localPath); err != nil {
		return err
	}

	// Update database
	_, err := s.Client.ArtistImage.UpdateOne(img).
		SetLocalPath(localPath).
		Save(ctx)
	if err != nil {
		return err
	}

	// Log image download event
	s.logEvent(ctx, u, syncevent.EventTypeImageDownloaded, "metadata",
		fmt.Sprintf("Downloaded artist image: %s", img.Edges.Artist.Name),
		map[string]interface{}{
			"artist":     img.Edges.Artist.Name,
			"image_type": img.ImageType.String(),
			"source":     img.Source,
		})

	return nil
}

// downloadAlbumImage downloads a single album image.
func (s *MetadataService) downloadAlbumImage(ctx context.Context, u *ent.User, img *ent.AlbumImage, baseDir string) error {
	if img.URL == "" {
		return nil
	}

	// Create directory for albums
	albumDir := filepath.Join(baseDir, "albums")
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		return err
	}

	// Determine filename using album ID and image type (e.g., 456-cover.png)
	ext := getImageExtension(img.URL)
	filename := fmt.Sprintf("%d-%s%s", img.Edges.Album.ID, img.ImageType.String(), ext)
	localPath := filepath.Join(albumDir, filename)

	// Check if file already exists on disk
	if _, err := os.Stat(localPath); err == nil {
		// File exists, just update database without downloading again
		_, err := s.Client.AlbumImage.UpdateOne(img).
			SetLocalPath(localPath).
			Save(ctx)
		return err
	}

	// Download image
	if err := s.downloadFile(ctx, img.URL, localPath); err != nil {
		return err
	}

	// Update database
	_, err := s.Client.AlbumImage.UpdateOne(img).
		SetLocalPath(localPath).
		Save(ctx)
	if err != nil {
		return err
	}

	// Log image download event
	s.logEvent(ctx, u, syncevent.EventTypeImageDownloaded, "metadata",
		fmt.Sprintf("Downloaded album image: %s", img.Edges.Album.Name),
		map[string]interface{}{
			"album":      img.Edges.Album.Name,
			"image_type": img.ImageType.String(),
			"source":     img.Source,
		})

	return nil
}

// downloadFile downloads a file from a URL to a local path.
func (s *MetadataService) downloadFile(ctx context.Context, url, localPath string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	file, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	_, err = io.Copy(file, resp.Body)
	return err
}

// EnrichNewListens enriches catalog entries for newly synced listens.
// This is called by the sync service when new tracks are added.
func (s *MetadataService) EnrichNewListens(ctx context.Context, u *ent.User, artistName, albumName, trackName string) {
	if !s.Config.Metadata.Enabled {
		return
	}

	// Process the listen entry to ensure catalog entries exist
	added, err := s.processListenEntry(ctx, u, artistName, albumName, trackName)
	if err != nil {
		s.Logger.Warn("failed to process listen entry for enrichment",
			"artist", artistName,
			"album", albumName,
			"track", trackName,
			"error", err)
		return
	}

	// Only enrich if something new was added
	if added == nil || (!added["artist"] && !added["album"] && !added["track"]) {
		return
	}

	s.Logger.Debug("new catalog entries added, will be enriched in next sync",
		"artist", artistName,
		"album", albumName,
		"track", trackName,
		"added", added)
}

// MatchListens links listens to their corresponding artist, album, and track entities.
// This should be called after BuildCatalog to establish the relationships.
func (s *MetadataService) MatchListens(ctx context.Context, u *ent.User) (int, error) {
	s.Logger.Info("matching listens to library entities", "username", u.Username)

	// Get all listens for the user that don't have linked entities
	listens, err := s.Client.Listen.Query().
		Where(listen.HasUserWith(user.ID(u.ID))).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query listens: %w", err)
	}

	matchedCount := 0

	for _, l := range listens {
		updated := false

		// Match artist
		if l.ArtistName != "" {
			art, err := s.Client.Artist.Query().
				Where(
					artist.HasUserWith(user.ID(u.ID)),
					artist.Name(l.ArtistName),
				).
				Only(ctx)
			if err == nil {
				// Check if already linked by querying the edge
				hasArtist, err := s.Client.Listen.Query().
					Where(listen.ID(l.ID)).
					QueryArtist().
					Exist(ctx)
				if err != nil {
					s.Logger.Warn("failed to check artist link", "listen_id", l.ID, "error", err)
				} else if !hasArtist {
					_, err = l.Update().SetArtist(art).Save(ctx)
					if err != nil {
						s.Logger.Warn("failed to link listen to artist", "listen_id", l.ID, "artist", l.ArtistName, "error", err)
					} else {
						updated = true
					}
				}
			}
		}

		// Match album
		if l.AlbumName != "" && l.ArtistName != "" {
			// Find the artist first
			art, err := s.Client.Artist.Query().
				Where(
					artist.HasUserWith(user.ID(u.ID)),
					artist.Name(l.ArtistName),
				).
				Only(ctx)
			if err == nil {
				alb, err := s.Client.Album.Query().
					Where(
						album.HasUserWith(user.ID(u.ID)),
						album.HasArtistWith(artist.ID(art.ID)),
						album.Name(l.AlbumName),
					).
					Only(ctx)
				if err == nil {
					// Check if already linked
					hasAlbum, err := s.Client.Listen.Query().
						Where(listen.ID(l.ID)).
						QueryAlbum().
						Exist(ctx)
					if err != nil {
						s.Logger.Warn("failed to check album link", "listen_id", l.ID, "error", err)
					} else if !hasAlbum {
						_, err = l.Update().SetAlbum(alb).Save(ctx)
						if err != nil {
							s.Logger.Warn("failed to link listen to album", "listen_id", l.ID, "album", l.AlbumName, "error", err)
						} else {
							updated = true
						}
					}
				}
			}
		}

		// Match track
		if l.TrackName != "" && l.ArtistName != "" {
			art, err := s.Client.Artist.Query().
				Where(
					artist.HasUserWith(user.ID(u.ID)),
					artist.Name(l.ArtistName),
				).
				Only(ctx)
			if err == nil {
				query := s.Client.Track.Query().
					Where(
						track.Name(l.TrackName),
						track.HasArtistWith(artist.ID(art.ID)),
					)

				// If we have an album name, also match on that for more precision
				if l.AlbumName != "" {
					alb, albErr := s.Client.Album.Query().
						Where(
							album.HasUserWith(user.ID(u.ID)),
							album.HasArtistWith(artist.ID(art.ID)),
							album.Name(l.AlbumName),
						).
						Only(ctx)
					if albErr == nil {
						query = query.Where(track.HasAlbumWith(album.ID(alb.ID)))
					}
				}

				trk, err := query.Only(ctx)
				if err == nil {
					// Check if already linked
					hasTrack, err := s.Client.Listen.Query().
						Where(listen.ID(l.ID)).
						QueryTrack().
						Exist(ctx)
					if err != nil {
						s.Logger.Warn("failed to check track link", "listen_id", l.ID, "error", err)
					} else if !hasTrack {
						_, err = l.Update().SetTrack(trk).Save(ctx)
						if err != nil {
							s.Logger.Warn("failed to link listen to track", "listen_id", l.ID, "track", l.TrackName, "error", err)
						} else {
							updated = true
						}
					}
				}
			}
		}

		if updated {
			matchedCount++
		}
	}

	s.Logger.Info("listen matching completed",
		"username", u.Username,
		"total_listens", len(listens),
		"matched", matchedCount)

	return matchedCount, nil
}

// Helper functions

// uniqueStrings returns a deduplicated slice of strings.
func uniqueStrings(s []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(s))
	for _, v := range s {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		lower := strings.ToLower(v)
		if _, ok := seen[lower]; !ok {
			seen[lower] = struct{}{}
			result = append(result, v)
		}
	}
	return result
}

// getImageExtension extracts the file extension from a URL.
func getImageExtension(url string) string {
	// Try to get extension from URL
	if idx := strings.LastIndex(url, "."); idx != -1 {
		ext := strings.ToLower(url[idx:])
		if idx := strings.Index(ext, "?"); idx != -1 {
			ext = ext[:idx]
		}
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" || ext == ".webp" {
			return ext
		}
	}
	return ".jpg" // Default to jpg
}
