package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/enrichers"
	"spotter/internal/resilience"
	tagsutil "spotter/internal/tags"
)

const (
	defaultBaseURL = "https://musicbrainz.org/ws/2"
	rateLimitDelay = 1100 * time.Millisecond // MusicBrainz requires max 1 req/sec
)

// Enricher implements the MusicBrainz metadata enricher.
type Enricher struct {
	logger     *slog.Logger
	config     *config.Config
	httpClient *http.Client
	userAgent  string
	baseURL    string

	// Rate limiting
	mu       sync.Mutex
	lastCall time.Time
}

// Ensure Enricher implements interfaces
var _ enrichers.Enricher = (*Enricher)(nil)
var _ enrichers.ArtistEnricher = (*Enricher)(nil)
var _ enrichers.AlbumEnricher = (*Enricher)(nil)
var _ enrichers.TrackEnricher = (*Enricher)(nil)
var _ enrichers.IDMatcher = (*Enricher)(nil)

// New creates a new MusicBrainz enricher factory.
func New(logger *slog.Logger, cfg *config.Config) enrichers.Factory {
	return func(ctx context.Context, user *ent.User) (enrichers.Enricher, error) {
		userAgent := cfg.Metadata.MusicBrainz.UserAgent
		if userAgent == "" {
			userAgent = "Spotter/1.0.0 (https://github.com/spotter)"
		}

		return &Enricher{
			logger: logger,
			config: cfg,
			httpClient: &http.Client{
				Timeout: 30 * time.Second,
			},
			userAgent: userAgent,
			baseURL:   defaultBaseURL,
		}, nil
	}
}

// WithBaseURL sets a custom base URL for the enricher (used for testing).
func (e *Enricher) WithBaseURL(url string) *Enricher {
	e.baseURL = url
	return e
}

// WithHTTPClient sets a custom HTTP client for the enricher (used for testing).
func (e *Enricher) WithHTTPClient(client *http.Client) *Enricher {
	e.httpClient = client
	return e
}

func (e *Enricher) Type() enrichers.Type {
	return enrichers.TypeMusicBrainz
}

func (e *Enricher) Name() string {
	return "MusicBrainz"
}

func (e *Enricher) IsAvailable() bool {
	// MusicBrainz is a free API, always available
	return true
}

// rateLimit ensures we don't exceed MusicBrainz API rate limits.
func (e *Enricher) rateLimit() {
	e.mu.Lock()
	defer e.mu.Unlock()

	elapsed := time.Since(e.lastCall)
	if elapsed < rateLimitDelay {
		time.Sleep(rateLimitDelay - elapsed)
	}
	e.lastCall = time.Now()
}

// doRequest performs an HTTP request with rate limiting and proper headers.
func (e *Enricher) doRequest(ctx context.Context, endpoint string, params url.Values) ([]byte, error) {
	e.rateLimit()

	params.Set("fmt", "json")
	reqURL := fmt.Sprintf("%s/%s?%s", e.baseURL, endpoint, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", e.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			e.logger.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusTooManyRequests {
		// Governing: ADR-0020, SPEC error-handling REQ-ERR-002 (429/503 retriable)
		return nil, resilience.NewHTTPStatusError(resp.StatusCode, fmt.Errorf("rate limited by MusicBrainz API"))
	}

	if resp.StatusCode != http.StatusOK {
		// Governing: ADR-0020, SPEC error-handling REQ-ERR-002/REQ-ERR-003
		return nil, resilience.NewHTTPStatusError(resp.StatusCode, fmt.Errorf("MusicBrainz API returned status %d", resp.StatusCode))
	}

	var result []byte
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			result = append(result, buf[:n]...)
		}
		if err != nil {
			break
		}
	}

	return result, nil
}

// Artist search response structures
type artistSearchResponse struct {
	Artists []mbArtist `json:"artists"`
}

type mbArtist struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	SortName string    `json:"sort-name"`
	Score    int       `json:"score"`
	Tags     []mbTag   `json:"tags"`
	Type     string    `json:"type"`
	Country  string    `json:"country"`
	Area     *mbArea   `json:"area"`
	Aliases  []mbAlias `json:"aliases"`
}

type mbAlias struct {
	Name     string `json:"name"`
	SortName string `json:"sort-name"`
	Type     string `json:"type"`
	Locale   string `json:"locale"`
	Primary  bool   `json:"primary"`
}

type mbTag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type mbArea struct {
	Name string `json:"name"`
}

// MatchArtist searches MusicBrainz for an artist by name.
func (e *Enricher) MatchArtist(ctx context.Context, name string) (string, float64, error) {
	e.logger.Debug("matching artist in MusicBrainz", "name", name)

	params := url.Values{}
	params.Set("query", fmt.Sprintf("artist:%s", name))
	params.Set("limit", "5")

	data, err := e.doRequest(ctx, "artist", params)
	if err != nil {
		return "", 0, err
	}

	var result artistSearchResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Artists) == 0 {
		return "", 0, nil
	}

	// Return the best match
	best := result.Artists[0]
	confidence := float64(best.Score) / 100.0

	e.logger.Debug("found MusicBrainz artist match",
		"name", name,
		"mbid", best.ID,
		"matched_name", best.Name,
		"confidence", confidence)

	return best.ID, confidence, nil
}

// Release search response structures
type releaseGroupSearchResponse struct {
	ReleaseGroups []mbReleaseGroup `json:"release-groups"`
}

type mbReleaseGroup struct {
	ID               string        `json:"id"`
	Title            string        `json:"title"`
	Score            int           `json:"score"`
	PrimaryType      string        `json:"primary-type"`
	FirstReleaseDate string        `json:"first-release-date"`
	Tags             []mbTag       `json:"tags"`
	ArtistCredit     []mbArtCredit `json:"artist-credit"`
}

type mbArtCredit struct {
	Artist mbArtist `json:"artist"`
}

// MatchAlbum searches MusicBrainz for an album by name and artist.
func (e *Enricher) MatchAlbum(ctx context.Context, albumName, artistName string) (string, float64, error) {
	e.logger.Debug("matching album in MusicBrainz", "album", albumName, "artist", artistName)

	params := url.Values{}
	query := fmt.Sprintf("releasegroup:%s AND artist:%s", albumName, artistName)
	params.Set("query", query)
	params.Set("limit", "5")

	data, err := e.doRequest(ctx, "release-group", params)
	if err != nil {
		return "", 0, err
	}

	var result releaseGroupSearchResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.ReleaseGroups) == 0 {
		return "", 0, nil
	}

	best := result.ReleaseGroups[0]
	confidence := float64(best.Score) / 100.0

	e.logger.Debug("found MusicBrainz album match",
		"album", albumName,
		"mbid", best.ID,
		"matched_title", best.Title,
		"confidence", confidence)

	return best.ID, confidence, nil
}

// Recording search response structures
type recordingSearchResponse struct {
	Recordings []mbRecording `json:"recordings"`
}

type mbRecording struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	Score        int           `json:"score"`
	Length       int           `json:"length"` // milliseconds
	Tags         []mbTag       `json:"tags"`
	ISRCs        []string      `json:"isrcs"`
	ArtistCredit []mbArtCredit `json:"artist-credit"`
	Releases     []mbRelease   `json:"releases"`
}

type mbRelease struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// MatchTrack searches MusicBrainz for a track by name, artist, and album.
func (e *Enricher) MatchTrack(ctx context.Context, trackName, artistName, albumName string) (string, float64, error) {
	e.logger.Debug("matching track in MusicBrainz", "track", trackName, "artist", artistName, "album", albumName)

	params := url.Values{}
	queryParts := []string{fmt.Sprintf("recording:%s", trackName)}
	if artistName != "" {
		queryParts = append(queryParts, fmt.Sprintf("artist:%s", artistName))
	}
	if albumName != "" {
		queryParts = append(queryParts, fmt.Sprintf("release:%s", albumName))
	}
	params.Set("query", strings.Join(queryParts, " AND "))
	params.Set("limit", "5")

	data, err := e.doRequest(ctx, "recording", params)
	if err != nil {
		return "", 0, err
	}

	var result recordingSearchResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Recordings) == 0 {
		return "", 0, nil
	}

	best := result.Recordings[0]
	confidence := float64(best.Score) / 100.0

	e.logger.Debug("found MusicBrainz track match",
		"track", trackName,
		"mbid", best.ID,
		"matched_title", best.Title,
		"confidence", confidence)

	return best.ID, confidence, nil
}

// EnrichArtist fetches detailed artist data from MusicBrainz.
func (e *Enricher) EnrichArtist(ctx context.Context, artist *ent.Artist) (*enrichers.ArtistData, error) {
	// Artist has string fields (not *string) - check for empty string
	mbid := artist.MusicbrainzID
	if mbid == "" {
		// Try to find the artist first
		var err error
		mbid, _, err = e.MatchArtist(ctx, artist.Name)
		if err != nil {
			return nil, err
		}
		if mbid == "" {
			e.logger.Debug("no MusicBrainz match found for artist", "name", artist.Name)
			return nil, nil
		}
	}

	e.logger.Debug("enriching artist from MusicBrainz", "name", artist.Name, "mbid", mbid)

	// Fetch full artist details with tags
	params := url.Values{}
	params.Set("inc", "tags+ratings")

	data, err := e.doRequest(ctx, fmt.Sprintf("artist/%s", mbid), params)
	if err != nil {
		return nil, err
	}

	var mb mbArtist
	if err := json.Unmarshal(data, &mb); err != nil {
		return nil, fmt.Errorf("failed to parse artist response: %w", err)
	}

	// Extract tags sorted by count
	tags := make([]string, 0, len(mb.Tags))
	for _, t := range mb.Tags {
		if t.Count > 0 {
			tags = append(tags, t.Name)
		}
	}

	// Governing: SPEC-0014 REQ "Enricher Integration", ADR-0015 (Pluggable Enricher Registry)
	var typedTags []tagsutil.TypedTag
	for _, t := range tags {
		typedTags = append(typedTags, tagsutil.TypedTag{Name: t, Type: "genre"})
	}

	return &enrichers.ArtistData{
		MusicBrainzID: mbid,
		SortName:      mb.SortName,
		Tags:          tags,
		TypedTags:     typedTags,
	}, nil
}

// GetArtistImages returns images for an artist from MusicBrainz.
// Note: MusicBrainz doesn't host images directly; images come from Cover Art Archive
// and are typically for releases, not artists. Fanart.tv is better for artist images.
func (e *Enricher) GetArtistImages(ctx context.Context, artist *ent.Artist) ([]enrichers.ImageData, error) {
	// MusicBrainz doesn't provide artist images directly
	// Artist images should be fetched from Fanart.tv
	return nil, nil
}

// EnrichAlbum fetches detailed album data from MusicBrainz.
func (e *Enricher) EnrichAlbum(ctx context.Context, album *ent.Album) (*enrichers.AlbumData, error) {
	// Album has string fields (not *string) - check for empty string
	mbid := album.MusicbrainzID
	if mbid == "" {
		// Try to find the album first
		artistName := ""
		if album.Edges.Artist != nil {
			artistName = album.Edges.Artist.Name
		}
		var err error
		mbid, _, err = e.MatchAlbum(ctx, album.Name, artistName)
		if err != nil {
			return nil, err
		}
		if mbid == "" {
			e.logger.Debug("no MusicBrainz match found for album", "name", album.Name)
			return nil, nil
		}
	}

	e.logger.Debug("enriching album from MusicBrainz", "name", album.Name, "mbid", mbid)

	params := url.Values{}
	params.Set("inc", "tags+releases")

	data, err := e.doRequest(ctx, fmt.Sprintf("release-group/%s", mbid), params)
	if err != nil {
		return nil, err
	}

	var rg mbReleaseGroup
	if err := json.Unmarshal(data, &rg); err != nil {
		return nil, fmt.Errorf("failed to parse release-group response: %w", err)
	}

	tags := make([]string, 0, len(rg.Tags))
	for _, t := range rg.Tags {
		tags = append(tags, t.Name)
	}

	// Parse year from first release date
	year := 0
	if len(rg.FirstReleaseDate) >= 4 {
		if _, err := fmt.Sscanf(rg.FirstReleaseDate, "%d", &year); err != nil {
			e.logger.Debug("failed to parse year from release date", "date", rg.FirstReleaseDate, "error", err)
		}
	}

	// Governing: SPEC-0014 REQ "Enricher Integration", ADR-0015 (Pluggable Enricher Registry)
	var typedTags []tagsutil.TypedTag
	for _, t := range tags {
		typedTags = append(typedTags, tagsutil.TypedTag{Name: t, Type: "genre"})
	}

	return &enrichers.AlbumData{
		MusicBrainzID: mbid,
		ReleaseDate:   rg.FirstReleaseDate,
		Year:          year,
		Tags:          tags,
		AlbumType:     strings.ToLower(rg.PrimaryType),
		TypedTags:     typedTags,
	}, nil
}

// GetAlbumImages fetches cover art from the Cover Art Archive.
func (e *Enricher) GetAlbumImages(ctx context.Context, album *ent.Album) ([]enrichers.ImageData, error) {
	// Album has string fields (not *string) - check for empty string
	mbid := album.MusicbrainzID
	if mbid == "" {
		return nil, nil
	}

	e.rateLimit()

	// Cover Art Archive uses release group ID
	caaURL := fmt.Sprintf("https://coverartarchive.org/release-group/%s", mbid)

	req, err := http.NewRequestWithContext(ctx, "GET", caaURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", e.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			e.logger.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		// No cover art available
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		// Governing: ADR-0020, SPEC error-handling REQ-ERR-002/REQ-ERR-003
		return nil, resilience.NewHTTPStatusError(resp.StatusCode, fmt.Errorf("Cover Art Archive returned status %d", resp.StatusCode))
	}

	var caaResponse struct {
		Images []struct {
			ID         int64  `json:"id"`
			Front      bool   `json:"front"`
			Back       bool   `json:"back"`
			Image      string `json:"image"`
			Thumbnails struct {
				Small string `json:"small"`
				Large string `json:"large"`
			} `json:"thumbnails"`
		} `json:"images"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&caaResponse); err != nil {
		return nil, fmt.Errorf("failed to parse CAA response: %w", err)
	}

	var images []enrichers.ImageData
	for _, img := range caaResponse.Images {
		imgType := "other"
		if img.Front {
			imgType = "cover_front"
		} else if img.Back {
			imgType = "cover_back"
		}

		images = append(images, enrichers.ImageData{
			URL:       img.Image,
			Type:      imgType,
			Source:    "musicbrainz",
			IsPrimary: img.Front,
		})
	}

	return images, nil
}

// EnrichTrack fetches detailed track data from MusicBrainz.
func (e *Enricher) EnrichTrack(ctx context.Context, track *ent.Track) (*enrichers.TrackData, error) {
	// Track has *string fields (Nillable) - check for nil or empty
	mbid := ""
	if track.MusicbrainzID != nil && *track.MusicbrainzID != "" {
		mbid = *track.MusicbrainzID
	} else {
		artistName := ""
		albumName := ""
		if track.Edges.Artist != nil {
			artistName = track.Edges.Artist.Name
		}
		if track.Edges.Album != nil {
			albumName = track.Edges.Album.Name
		}
		var err error
		mbid, _, err = e.MatchTrack(ctx, track.Name, artistName, albumName)
		if err != nil {
			return nil, err
		}
		if mbid == "" {
			e.logger.Debug("no MusicBrainz match found for track", "name", track.Name)
			return nil, nil
		}
	}

	e.logger.Debug("enriching track from MusicBrainz", "name", track.Name, "mbid", mbid)

	params := url.Values{}
	params.Set("inc", "tags+isrcs")

	data, err := e.doRequest(ctx, fmt.Sprintf("recording/%s", mbid), params)
	if err != nil {
		return nil, err
	}

	var rec mbRecording
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("failed to parse recording response: %w", err)
	}

	tags := make([]string, 0, len(rec.Tags))
	for _, t := range rec.Tags {
		tags = append(tags, t.Name)
	}

	isrc := ""
	if len(rec.ISRCs) > 0 {
		isrc = rec.ISRCs[0]
	}

	// Governing: SPEC-0014 REQ "Enricher Integration", ADR-0015 (Pluggable Enricher Registry)
	var typedTags []tagsutil.TypedTag
	for _, t := range tags {
		typedTags = append(typedTags, tagsutil.TypedTag{Name: t, Type: "genre"})
	}

	return &enrichers.TrackData{
		MusicBrainzID:  mbid,
		ISRC:           isrc,
		DurationMs:     rec.Length,
		Tags:           tags,
		MusicBrainzURL: fmt.Sprintf("https://musicbrainz.org/recording/%s", mbid),
		TypedTags:      typedTags,
	}, nil
}
