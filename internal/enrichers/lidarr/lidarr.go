package lidarr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"spotter/ent"
	"spotter/ent/lidarrqueue"
	"spotter/ent/syncevent"
	"spotter/ent/user"
	"spotter/internal/config"
	"spotter/internal/enrichers"
	"spotter/internal/resilience"
	"spotter/internal/tags"
	"strings"
	"sync"
	"time"
)

// Governing: ADR-0020 (error handling and resilience)
const (
	rateLimitDelay   = 200 * time.Millisecond // Lidarr is local, lighter throttle than MusicBrainz
	maxRetries       = 3
	retryBaseBackoff = 500 * time.Millisecond
)

type Enricher struct {
	logger *slog.Logger
	config *config.Config
	client *http.Client
	db     *ent.Client
	user   *ent.User

	// Governing: ADR-0020 (error handling and resilience) — rate limiting
	mu          sync.Mutex
	lastRequest time.Time

	// Governing: ADR-0020 (error handling and resilience) — result caching to avoid redundant API calls
	artistCache   map[int]*lidarrArtist
	artistChecked map[int]bool
	albumCache    map[int]*lidarrAlbum
	albumChecked  map[int]bool
}

func New(logger *slog.Logger, cfg *config.Config, db *ent.Client) enrichers.Factory {
	return func(ctx context.Context, user *ent.User) (enrichers.Enricher, error) {
		return &Enricher{
			logger: logger,
			config: cfg,
			client: &http.Client{
				Timeout: 30 * time.Second,
			},
			db:            db,
			user:          user,
			artistCache:   make(map[int]*lidarrArtist),
			artistChecked: make(map[int]bool),
			albumCache:    make(map[int]*lidarrAlbum),
			albumChecked:  make(map[int]bool),
		}, nil
	}
}

func (e *Enricher) Type() enrichers.Type {
	return enrichers.TypeLidarr
}

func (e *Enricher) Name() string {
	return "Lidarr"
}

func (e *Enricher) IsAvailable() bool {
	return e.config.Lidarr.BaseURL != "" && e.config.Lidarr.APIKey != ""
}

// Lidarr API structs

type lidarrArtist struct {
	ID              int           `json:"id"`
	ArtistName      string        `json:"artistName"`
	ForeignArtistID string        `json:"foreignArtistId"` // MusicBrainz ID
	Monitored       bool          `json:"monitored"`
	Overview        string        `json:"overview"`
	Genres          []string      `json:"genres"`
	Images          []lidarrImage `json:"images"`
	Links           []lidarrLink  `json:"links"`
}

type lidarrImage struct {
	URL       string `json:"url"`
	CoverType string `json:"coverType"`
}

type lidarrLink struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

type lidarrAlbumStatistics struct {
	TrackFileCount  int     `json:"trackFileCount"`
	TotalTrackCount int     `json:"totalTrackCount"`
	PercentOfTracks float64 `json:"percentOfTracks"`
}

type lidarrAlbum struct {
	ID             int                   `json:"id"`
	Title          string                `json:"title"`
	ForeignAlbumID string                `json:"foreignAlbumId"` // MusicBrainz ID
	ArtistID       int                   `json:"artistId"`
	Artist         *lidarrArtist         `json:"artist,omitempty"`
	Monitored      bool                  `json:"monitored"`
	ReleaseDate    string                `json:"releaseDate"`
	Genres         []string              `json:"genres"`
	Images         []lidarrImage         `json:"images"`
	AlbumType      string                `json:"albumType"`
	Statistics     lidarrAlbumStatistics `json:"statistics"`
}

// EnrichArtist looks up the artist in Lidarr. If found, it returns enrichment
// data with the Lidarr ID. If not found and the artist has a MusicBrainz ID,
// it inserts a LidarrQueue row instead of calling addArtist() directly.
// Governing: SPEC-0017 REQ "Enricher Decoupling", ADR-0029
func (e *Enricher) EnrichArtist(ctx context.Context, artist *ent.Artist) (*enrichers.ArtistData, error) {
	lArtist, err := e.findArtist(ctx, artist)
	if err != nil {
		return nil, err
	}

	if lArtist == nil || lArtist.ID == 0 {
		mbid := artist.MusicbrainzID
		if mbid == "" && lArtist != nil {
			mbid = lArtist.ForeignArtistID
		}

		if mbid == "" {
			// Governing: SPEC-0017 REQ "Enricher Decoupling" — artists without musicbrainz_id return nil, nil
			return nil, nil
		}

		// Governing: SPEC-0017 REQ "Enricher Decoupling" — enqueue instead of addArtist()
		if err := e.enqueueEntity(ctx, lidarrqueue.EntityTypeArtist, artist.ID, mbid); err != nil {
			e.logger.Error("failed to enqueue artist for lidarr submission", "error", err, "artist", artist.Name)
			return nil, nil
		}

		return &enrichers.ArtistData{
			MusicBrainzID: mbid,
			LidarrStatus:  "queued",
		}, nil
	}

	if artist.LidarrID == "" {
		// Found existing artist in Lidarr, first time match locally
		e.logEvent(ctx, "lidarr_artist_matched", fmt.Sprintf("Matched artist in Lidarr: %s", lArtist.ArtistName), map[string]interface{}{
			"artist_name": lArtist.ArtistName,
			"lidarr_id":   lArtist.ID,
		})
	}

	// Governing: SPEC-0014 REQ "Enricher Integration", ADR-0015 (Pluggable Enricher Registry)
	var typedTags []tags.TypedTag
	for _, g := range lArtist.Genres {
		typedTags = append(typedTags, tags.TypedTag{Name: g, Type: "genre"})
	}

	data := &enrichers.ArtistData{
		MusicBrainzID: lArtist.ForeignArtistID,
		Bio:           lArtist.Overview,
		Genres:        lArtist.Genres,
		TypedTags:     typedTags,
	}

	// Only set LidarrID if we have a valid ID (> 0)
	if lArtist.ID > 0 {
		data.LidarrID = fmt.Sprintf("%d", lArtist.ID)
	}

	return data, nil
}

func (e *Enricher) GetArtistImages(ctx context.Context, artist *ent.Artist) ([]enrichers.ImageData, error) {
	lArtist, err := e.findArtist(ctx, artist)
	if err != nil || lArtist == nil {
		return nil, err
	}

	var images []enrichers.ImageData
	for _, img := range lArtist.Images {
		images = append(images, enrichers.ImageData{
			URL:    img.URL,
			Type:   img.CoverType,
			Source: "lidarr",
		})
	}
	return images, nil
}

// EnrichAlbum looks up the album in Lidarr. If found, it returns enrichment
// data with the Lidarr ID. If not found and the album has a MusicBrainz ID,
// it inserts a LidarrQueue row instead of calling addAlbum() directly.
// Governing: SPEC-0017 REQ "Enricher Decoupling", ADR-0029
func (e *Enricher) EnrichAlbum(ctx context.Context, album *ent.Album) (*enrichers.AlbumData, error) {
	lAlbum, err := e.findAlbum(ctx, album)
	if err != nil {
		return nil, err
	}

	if lAlbum == nil {
		// Governing: SPEC-0017 REQ "Enricher Decoupling" — enqueue instead of addAlbum()
		if album.Edges.Artist != nil && album.Edges.Artist.MusicbrainzID != "" && album.MusicbrainzID != "" {
			// Ensure artist is also enqueued (or already in Lidarr)
			_, err := e.EnrichArtist(ctx, album.Edges.Artist)
			if err != nil {
				e.logger.Warn("could not ensure artist exists in lidarr", "error", err)
			}

			if err := e.enqueueEntity(ctx, lidarrqueue.EntityTypeAlbum, album.ID, album.MusicbrainzID); err != nil {
				e.logger.Error("failed to enqueue album for lidarr submission", "error", err, "album", album.Name)
				return nil, nil
			}

			return &enrichers.AlbumData{
				MusicBrainzID: album.MusicbrainzID,
				LidarrStatus:  "queued",
			}, nil
		}
		return nil, nil
	}

	if album.LidarrID == "" {
		// Found existing album in Lidarr, first time match locally
		e.logEvent(ctx, "lidarr_album_matched", fmt.Sprintf("Matched album in Lidarr: %s", lAlbum.Title), map[string]interface{}{
			"album_name": lAlbum.Title,
			"lidarr_id":  lAlbum.ID,
		})
	}

	// Governing: SPEC-0014 REQ "Enricher Integration", ADR-0015 (Pluggable Enricher Registry)
	var typedTags []tags.TypedTag
	for _, g := range lAlbum.Genres {
		typedTags = append(typedTags, tags.TypedTag{Name: g, Type: "genre"})
	}

	data := &enrichers.AlbumData{
		MusicBrainzID: lAlbum.ForeignAlbumID,
		AlbumType:     lAlbum.AlbumType,
		ReleaseDate:   lAlbum.ReleaseDate,
		Genre:         strings.Join(lAlbum.Genres, ", "),
		TypedTags:     typedTags,
	}

	// Only set LidarrID if we have a valid ID (> 0)
	if lAlbum.ID > 0 {
		data.LidarrID = fmt.Sprintf("%d", lAlbum.ID)
	}

	// Parse year
	if len(lAlbum.ReleaseDate) >= 4 {
		if _, err := fmt.Sscanf(lAlbum.ReleaseDate, "%d", &data.Year); err != nil {
			e.logger.Debug("failed to parse year from release date", "date", lAlbum.ReleaseDate, "error", err)
		}
	}

	return data, nil
}

func (e *Enricher) GetAlbumImages(ctx context.Context, album *ent.Album) ([]enrichers.ImageData, error) {
	lAlbum, err := e.findAlbum(ctx, album)
	if err != nil || lAlbum == nil {
		return nil, err
	}

	var images []enrichers.ImageData
	for _, img := range lAlbum.Images {
		images = append(images, enrichers.ImageData{
			URL:    img.URL,
			Type:   img.CoverType,
			Source: "lidarr",
		})
	}
	return images, nil
}

// EnrichTrack derives Lidarr status from the album, not the individual track.
// Lidarr only allows requesting at the album/artist level, so per-track status
// must be album-level: "available" if the album is fully downloaded, "monitored"
// if it is tracked but incomplete, "queued" if a queue entry exists for the
// album, or "pending" if it is not yet in Lidarr.
// Governing: SPEC-0017 REQ "Enricher Decoupling", ADR-0029
func (e *Enricher) EnrichTrack(ctx context.Context, track *ent.Track) (*enrichers.TrackData, error) {
	if track.Edges.Album == nil {
		return &enrichers.TrackData{LidarrStatus: "pending"}, nil
	}

	// Governing: SPEC-0017 REQ "Enricher Decoupling" — if the parent album has a
	// pending queue entry, propagate "queued" to the track and skip the Lidarr
	// album statistics lookup. Check via the LidarrQueue table.
	if e.db != nil && e.user != nil && track.Edges.Album.ID != 0 {
		queued, err := e.db.LidarrQueue.Query().
			Where(
				lidarrqueue.EntityTypeEQ(lidarrqueue.EntityTypeAlbum),
				lidarrqueue.EntityIDEQ(track.Edges.Album.ID),
				lidarrqueue.StatusEQ(lidarrqueue.StatusQueued),
				lidarrqueue.HasUserWith(user.ID(e.user.ID)),
			).
			Exist(ctx)
		if err == nil && queued {
			return &enrichers.TrackData{LidarrStatus: "queued"}, nil
		}
	}

	lAlbum, err := e.findAlbum(ctx, track.Edges.Album)
	if err != nil {
		return nil, err
	}

	if lAlbum == nil {
		// Album not in Lidarr — enqueue it if we have an MBID.
		if track.Edges.Album.MusicbrainzID != "" {
			albumData, err := e.EnrichAlbum(ctx, track.Edges.Album)
			if err != nil {
				e.logger.Error("failed to enqueue album for missing track", "error", err, "track", track.Name)
			}
			// If the album was enqueued, report "queued" instead of "pending"
			if albumData != nil && albumData.LidarrStatus == "queued" {
				return &enrichers.TrackData{LidarrStatus: "queued"}, nil
			}
		}
		return &enrichers.TrackData{LidarrStatus: "pending"}, nil
	}

	// Derive status from album-level file statistics.
	// "available" = all tracks downloaded; "monitored" = tracked but incomplete.
	status := "monitored"
	stats := lAlbum.Statistics
	if stats.TotalTrackCount > 0 && stats.TrackFileCount >= stats.TotalTrackCount {
		status = "available"
	}

	return &enrichers.TrackData{LidarrStatus: status}, nil
}

// Helper methods

// enqueueEntity inserts a LidarrQueue row for deferred submission to Lidarr.
// If a row already exists for the same entity_type+entity_id+user combination,
// the insert is silently skipped (idempotent).
// Governing: SPEC-0017 REQ "Enricher Decoupling", ADR-0029
func (e *Enricher) enqueueEntity(ctx context.Context, entityType lidarrqueue.EntityType, entityID int, mbid string) error {
	if e.db == nil || e.user == nil {
		return nil
	}

	// Attempt insert directly; rely on the unique constraint for idempotency.
	// NOTE: SPEC-0017 recommends OnConflictColumns().DoNothing() upsert, but
	// Ent's OnConflict feature is not enabled in this project's code generation.
	// The unique constraint catch below provides equivalent idempotent behavior.
	// Governing: SPEC-0017 REQ "Enricher Decoupling", ADR-0029
	_, err := e.db.LidarrQueue.Create().
		SetEntityType(entityType).
		SetEntityID(entityID).
		SetMusicbrainzID(mbid).
		SetStatus(lidarrqueue.StatusQueued).
		SetUser(e.user).
		Save(ctx)

	// Unique constraint violation means the row already exists — idempotent
	if err != nil && strings.Contains(err.Error(), "unique") {
		return nil
	}

	return err
}

func (e *Enricher) logEvent(ctx context.Context, eventType string, message string, meta map[string]interface{}) {
	if e.db == nil || e.user == nil {
		return
	}

	builder := e.db.SyncEvent.Create().
		SetUser(e.user).
		SetEventType(syncevent.EventType(eventType)).
		SetProvider("lidarr").
		SetMessage(message)

	if meta != nil {
		if b, err := json.Marshal(meta); err == nil {
			builder.SetMetadata(string(b))
		}
	}

	if err := builder.Exec(ctx); err != nil {
		e.logger.Error("failed to create sync event", "error", err)
	}
}

// rateLimit ensures we don't overwhelm the local Lidarr instance.
// Governing: ADR-0020 (error handling and resilience)
func (e *Enricher) rateLimit() {
	e.mu.Lock()
	defer e.mu.Unlock()

	elapsed := time.Since(e.lastRequest)
	if elapsed < rateLimitDelay {
		time.Sleep(rateLimitDelay - elapsed)
	}
	e.lastRequest = time.Now()
}

// Governing: ADR-0020 (error handling and resilience) — retry on 500 with exponential backoff
func (e *Enricher) doRequest(ctx context.Context, method, endpoint string, body interface{}, result interface{}) error {
	e.rateLimit()

	u, err := url.Parse(e.config.Lidarr.BaseURL)
	if err != nil {
		return err
	}

	var query string
	if idx := strings.Index(endpoint, "?"); idx != -1 {
		query = endpoint[idx+1:]
		endpoint = endpoint[:idx]
	}

	pathJoin, err := url.JoinPath(u.Path, "api/v1", endpoint)
	if err != nil {
		return err
	}
	u.Path = pathJoin
	u.RawQuery = query

	var bodyBytes []byte
	if body != nil {
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1)) * retryBaseBackoff // 500ms, 1s, 2s
			e.logger.Debug("retrying lidarr request after server error",
				"attempt", attempt,
				"backoff", backoff,
				"endpoint", endpoint,
			)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			e.rateLimit()
		}

		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, u.String(), bodyReader)
		if err != nil {
			return err
		}

		req.Header.Set("X-Api-Key", e.config.Lidarr.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := e.client.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode == http.StatusInternalServerError {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			// Governing: ADR-0020, SPEC error-handling REQ-ERR-002 (5xx retriable)
			lastErr = resilience.NewHTTPStatusError(resp.StatusCode, fmt.Errorf("lidarr api error: %d - %s", resp.StatusCode, string(b)))
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			// Governing: ADR-0020, SPEC error-handling REQ-ERR-002/REQ-ERR-003
			return resilience.NewHTTPStatusError(resp.StatusCode, fmt.Errorf("lidarr api error: %d - %s", resp.StatusCode, string(b)))
		}

		if result != nil {
			if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
				resp.Body.Close()
				return err
			}
		}
		resp.Body.Close()
		return nil
	}

	return lastErr
}

// Governing: ADR-0020 (error handling and resilience) — cache findArtist results to avoid redundant API calls
func (e *Enricher) findArtist(ctx context.Context, artist *ent.Artist) (*lidarrArtist, error) {
	if e.artistChecked[artist.ID] {
		return e.artistCache[artist.ID], nil
	}

	result, err := e.findArtistUncached(ctx, artist)
	if err != nil {
		return nil, err
	}

	e.artistChecked[artist.ID] = true
	e.artistCache[artist.ID] = result
	return result, nil
}

func (e *Enricher) findArtistUncached(ctx context.Context, artist *ent.Artist) (*lidarrArtist, error) {
	// Try MBID search
	if artist.MusicbrainzID != "" {
		var artists []lidarrArtist
		err := e.doRequest(ctx, "GET", "artist", nil, &artists)
		if err != nil {
			return nil, err
		}

		for _, a := range artists {
			if a.ForeignArtistID == artist.MusicbrainzID {
				return &a, nil
			}
		}
	}

	// Try search by name or MBID
	term := artist.Name
	if artist.MusicbrainzID != "" {
		term = fmt.Sprintf("lidarr:%s", artist.MusicbrainzID)
	}

	u := fmt.Sprintf("artist/lookup?term=%s", url.QueryEscape(term))
	var results []lidarrArtist
	err := e.doRequest(ctx, "GET", u, nil, &results)
	if err != nil {
		return nil, err
	}

	for _, a := range results {
		if artist.MusicbrainzID != "" && a.ForeignArtistID == artist.MusicbrainzID {
			return &a, nil
		}
		// Only fall back to name matching when not doing an MBID-based search,
		// to avoid mis-matching artists that share a similar name.
		if artist.MusicbrainzID == "" && strings.EqualFold(a.ArtistName, artist.Name) {
			return &a, nil
		}
	}

	return nil, nil
}

// Governing: ADR-0020 (error handling and resilience) — cache findAlbum results to avoid redundant API calls
func (e *Enricher) findAlbum(ctx context.Context, album *ent.Album) (*lidarrAlbum, error) {
	if e.albumChecked[album.ID] {
		return e.albumCache[album.ID], nil
	}

	result, err := e.findAlbumUncached(ctx, album)
	if err != nil {
		return nil, err
	}

	e.albumChecked[album.ID] = true
	e.albumCache[album.ID] = result
	return result, nil
}

func (e *Enricher) findAlbumUncached(ctx context.Context, album *ent.Album) (*lidarrAlbum, error) {
	// Artist edge is required for album lookup (we need an artist to query albums).
	// Note: artist MBID is NOT required — findArtist handles name-based lookup too.
	if album.Edges.Artist == nil {
		return nil, nil
	}

	artist, err := e.findArtist(ctx, album.Edges.Artist)
	if err != nil || artist == nil {
		return nil, err
	}

	if artist.ID == 0 {
		return nil, nil
	}

	u := fmt.Sprintf("album?artistId=%d", artist.ID)
	var albums []lidarrAlbum
	err = e.doRequest(ctx, "GET", u, nil, &albums)
	if err != nil {
		return nil, err
	}

	for _, a := range albums {
		if album.MusicbrainzID != "" && a.ForeignAlbumID == album.MusicbrainzID {
			return &a, nil
		}
		if strings.EqualFold(a.Title, album.Name) {
			return &a, nil
		}
	}

	return nil, nil
}
