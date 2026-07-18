package lastfm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/enrichers"
	"spotter/internal/resilience"
	tagsutil "spotter/internal/tags"
)

const (
	baseURL         = "https://ws.audioscrobbler.com/2.0/"
	imageSizeXLarge = "extralarge"
)

// Enricher implements the Last.fm metadata enricher.
type Enricher struct {
	logger       *slog.Logger
	config       *config.Config
	httpClient   *http.Client
	apiKey       string
	sharedSecret string
}

// Ensure Enricher implements interfaces
var _ enrichers.Enricher = (*Enricher)(nil)
var _ enrichers.ArtistEnricher = (*Enricher)(nil)
var _ enrichers.AlbumEnricher = (*Enricher)(nil)
var _ enrichers.TrackEnricher = (*Enricher)(nil)

// New creates a new Last.fm enricher factory.
func New(logger *slog.Logger, cfg *config.Config) enrichers.Factory {
	return func(ctx context.Context, user *ent.User) (enrichers.Enricher, error) {
		if cfg.LastFM.APIKey == "" {
			return nil, nil
		}

		return &Enricher{
			logger: logger,
			config: cfg,
			httpClient: &http.Client{
				Timeout: 30 * time.Second,
			},
			apiKey:       cfg.LastFM.APIKey,
			sharedSecret: cfg.LastFM.SharedSecret,
		}, nil
	}
}

func (e *Enricher) Type() enrichers.Type {
	return enrichers.TypeLastFM
}

func (e *Enricher) Name() string {
	return "Last.fm"
}

func (e *Enricher) IsAvailable() bool {
	return e.apiKey != ""
}

// doRequest performs a request to the Last.fm API.
func (e *Enricher) doRequest(ctx context.Context, method string, params url.Values) ([]byte, error) {
	params.Set("method", method)
	params.Set("api_key", e.apiKey)
	params.Set("format", "json")

	reqURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Spotter/1.0.0")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			e.logger.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		// Governing: ADR-0020, SPEC error-handling REQ-ERR-002/REQ-ERR-003
		return nil, resilience.NewHTTPStatusError(resp.StatusCode, fmt.Errorf("Last.fm API returned status %d", resp.StatusCode))
	}

	var result []byte
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			result = append(result, buf[:n]...)
		}
		if err != nil {
			break
		}
	}

	// Check for API error response
	var errResp struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(result, &errResp); err == nil && errResp.Error != 0 {
		if errResp.Error == 6 { // Artist/Album/Track not found
			return nil, nil
		}
		return nil, fmt.Errorf("Last.fm API error %d: %s", errResp.Error, errResp.Message)
	}

	return result, nil
}

// Last.fm API response types
type lastfmArtistResponse struct {
	Artist lastfmArtist `json:"artist"`
}

type lastfmArtist struct {
	Name  string      `json:"name"`
	MBID  string      `json:"mbid"`
	URL   string      `json:"url"`
	Stats lastfmStats `json:"stats"`
	Bio   lastfmBio   `json:"bio"`
	Tags  struct {
		Tag []lastfmTag `json:"tag"`
	} `json:"tags"`
	Similar struct {
		Artist []lastfmSimilarArtist `json:"artist"`
	} `json:"similar"`
	Image []lastfmImage `json:"image"`
}

type lastfmStats struct {
	Listeners string `json:"listeners"`
	Playcount string `json:"playcount"`
}

type lastfmBio struct {
	Summary   string `json:"summary"`
	Content   string `json:"content"`
	Published string `json:"published"`
}

type lastfmTag struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type lastfmSimilarArtist struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type lastfmImage struct {
	Text string `json:"#text"`
	Size string `json:"size"`
}

type lastfmAlbumResponse struct {
	Album lastfmAlbum `json:"album"`
}

type lastfmAlbum struct {
	Name   string `json:"name"`
	Artist string `json:"artist"`
	MBID   string `json:"mbid"`
	URL    string `json:"url"`
	Wiki   struct {
		Summary   string `json:"summary"`
		Content   string `json:"content"`
		Published string `json:"published"`
	} `json:"wiki"`
	Tags struct {
		Tag []lastfmTag `json:"tag"`
	} `json:"tags"`
	Image []lastfmImage `json:"image"`
}

type lastfmTrackResponse struct {
	Track lastfmTrack `json:"track"`
}

type lastfmTrack struct {
	Name      string `json:"name"`
	MBID      string `json:"mbid"`
	URL       string `json:"url"`
	Duration  string `json:"duration"`
	Listeners string `json:"listeners"`
	Playcount string `json:"playcount"`
	TopTags   struct {
		Tag []lastfmTag `json:"tag"`
	} `json:"toptags"`
	Wiki struct {
		Summary   string `json:"summary"`
		Content   string `json:"content"`
		Published string `json:"published"`
	} `json:"wiki"`
	Album struct {
		Artist string        `json:"artist"`
		Title  string        `json:"title"`
		Image  []lastfmImage `json:"image"`
	} `json:"album"`
}

// EnrichArtist fetches artist bio and tags from Last.fm.
func (e *Enricher) EnrichArtist(ctx context.Context, artist *ent.Artist) (*enrichers.ArtistData, error) {
	e.logger.Debug("enriching artist from Last.fm", "name", artist.Name)

	params := url.Values{}
	params.Set("artist", artist.Name)
	params.Set("autocorrect", "1")

	// Artist has string fields (not *string) - check for empty string
	// If we have a MusicBrainz ID, use it for more accurate matching
	if artist.MusicbrainzID != "" {
		params.Set("mbid", artist.MusicbrainzID)
	}

	data, err := e.doRequest(ctx, "artist.getinfo", params)
	if err != nil {
		return nil, err
	}
	if data == nil {
		e.logger.Debug("no Last.fm data for artist", "artist", artist.Name)
		return nil, nil
	}

	var response lastfmArtistResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("failed to parse Last.fm response: %w", err)
	}

	// Extract tags
	tags := make([]string, 0, len(response.Artist.Tags.Tag))
	for _, t := range response.Artist.Tags.Tag {
		if t.Name != "" {
			tags = append(tags, t.Name)
		}
	}

	// Clean up bio - remove HTML and read more links
	bio := cleanBio(response.Artist.Bio.Content)
	if bio == "" {
		bio = cleanBio(response.Artist.Bio.Summary)
	}

	// Governing: SPEC-0014 REQ "Enricher Integration", ADR-0015 (Pluggable Enricher Registry)
	var typedTags []tagsutil.TypedTag
	for _, t := range tags {
		typedTags = append(typedTags, tagsutil.TypedTag{Name: t, Type: "id3"})
	}

	result := &enrichers.ArtistData{
		LastFMURL: response.Artist.URL,
		Bio:       bio,
		Tags:      tags,
		TypedTags: typedTags,
	}

	// Set MusicBrainz ID if we got one and don't have one yet
	// Artist has string fields (not *string) - check for empty string
	if response.Artist.MBID != "" && artist.MusicbrainzID == "" {
		result.MusicBrainzID = response.Artist.MBID
	}

	e.logger.Debug("enriched artist from Last.fm",
		"artist", artist.Name,
		"tags", len(tags),
		"has_bio", bio != "")

	return result, nil
}

// GetArtistImages returns artist images from Last.fm.
func (e *Enricher) GetArtistImages(ctx context.Context, artist *ent.Artist) ([]enrichers.ImageData, error) {
	params := url.Values{}
	params.Set("artist", artist.Name)
	params.Set("autocorrect", "1")

	// Artist has string fields (not *string) - check for empty string
	if artist.MusicbrainzID != "" {
		params.Set("mbid", artist.MusicbrainzID)
	}

	data, err := e.doRequest(ctx, "artist.getinfo", params)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}

	var response lastfmArtistResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}

	var images []enrichers.ImageData
	for _, img := range response.Artist.Image {
		if img.Text == "" {
			continue
		}

		// Last.fm images come in different sizes
		width, height := imageSizeFromLastFM(img.Size)

		localPath := fmt.Sprintf("data/images/artists/%d_lastfm_%s.png", artist.ID, img.Size)
		_, err := enrichers.DownloadAndSaveImage(img.Text, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download lastfm image", "url", img.Text, "error", err)
			continue
		}

		images = append(images, enrichers.ImageData{
			URL:       img.Text,
			LocalPath: localPath,
			Type:      "thumbnail",
			Source:    "lastfm",
			Width:     width,
			Height:    height,
			IsPrimary: img.Size == imageSizeXLarge,
		})
	}

	// Sort by size (largest first)
	sort.Slice(images, func(i, j int) bool {
		return images[i].Width > images[j].Width
	})

	return images, nil
}

// EnrichAlbum fetches album tags and info from Last.fm.
func (e *Enricher) EnrichAlbum(ctx context.Context, album *ent.Album) (*enrichers.AlbumData, error) {
	artistName := ""
	if album.Edges.Artist != nil {
		artistName = album.Edges.Artist.Name
	}

	if artistName == "" {
		e.logger.Debug("skipping Last.fm album - no artist name", "album", album.Name)
		return nil, nil
	}

	e.logger.Debug("enriching album from Last.fm", "album", album.Name, "artist", artistName)

	params := url.Values{}
	params.Set("album", album.Name)
	params.Set("artist", artistName)
	params.Set("autocorrect", "1")

	// Album has string fields (not *string) - check for empty string
	if album.MusicbrainzID != "" {
		params.Set("mbid", album.MusicbrainzID)
	}

	data, err := e.doRequest(ctx, "album.getinfo", params)
	if err != nil {
		return nil, err
	}
	if data == nil {
		e.logger.Debug("no Last.fm data for album", "album", album.Name)
		return nil, nil
	}

	var response lastfmAlbumResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("failed to parse Last.fm response: %w", err)
	}

	// Extract tags
	tags := make([]string, 0, len(response.Album.Tags.Tag))
	for _, t := range response.Album.Tags.Tag {
		if t.Name != "" {
			tags = append(tags, t.Name)
		}
	}

	// Governing: SPEC-0014 REQ "Enricher Integration", ADR-0015 (Pluggable Enricher Registry)
	var typedTags []tagsutil.TypedTag
	for _, t := range tags {
		typedTags = append(typedTags, tagsutil.TypedTag{Name: t, Type: "id3"})
	}

	result := &enrichers.AlbumData{
		Tags:      tags,
		TypedTags: typedTags,
	}

	// Set MusicBrainz ID if we got one
	// Album has string fields (not *string) - check for empty string
	if response.Album.MBID != "" && album.MusicbrainzID == "" {
		result.MusicBrainzID = response.Album.MBID
	}

	e.logger.Debug("enriched album from Last.fm",
		"album", album.Name,
		"tags", len(tags))

	return result, nil
}

// GetAlbumImages returns album images from Last.fm.
func (e *Enricher) GetAlbumImages(ctx context.Context, album *ent.Album) ([]enrichers.ImageData, error) {
	artistName := ""
	if album.Edges.Artist != nil {
		artistName = album.Edges.Artist.Name
	}

	if artistName == "" {
		return nil, nil
	}

	params := url.Values{}
	params.Set("album", album.Name)
	params.Set("artist", artistName)
	params.Set("autocorrect", "1")

	// Album has string fields (not *string) - check for empty string
	if album.MusicbrainzID != "" {
		params.Set("mbid", album.MusicbrainzID)
	}

	data, err := e.doRequest(ctx, "album.getinfo", params)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}

	var response lastfmAlbumResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}

	var images []enrichers.ImageData
	for _, img := range response.Album.Image {
		if img.Text == "" {
			continue
		}

		width, height := imageSizeFromLastFM(img.Size)

		localPath := fmt.Sprintf("data/images/albums/%d_lastfm_%s.png", album.ID, img.Size)
		_, err := enrichers.DownloadAndSaveImage(img.Text, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download lastfm image", "url", img.Text, "error", err)
			continue
		}

		images = append(images, enrichers.ImageData{
			URL:       img.Text,
			LocalPath: localPath,
			Type:      "cover_front",
			Source:    "lastfm",
			Width:     width,
			Height:    height,
			IsPrimary: img.Size == imageSizeXLarge,
		})
	}

	// Sort by size (largest first)
	sort.Slice(images, func(i, j int) bool {
		return images[i].Width > images[j].Width
	})

	return images, nil
}

// EnrichTrack fetches track tags from Last.fm.
func (e *Enricher) EnrichTrack(ctx context.Context, track *ent.Track) (*enrichers.TrackData, error) {
	artistName := ""
	if track.Edges.Artist != nil {
		artistName = track.Edges.Artist.Name
	}

	if artistName == "" {
		e.logger.Debug("skipping Last.fm track - no artist name", "track", track.Name)
		return nil, nil
	}

	e.logger.Debug("enriching track from Last.fm", "track", track.Name, "artist", artistName)

	params := url.Values{}
	params.Set("track", track.Name)
	params.Set("artist", artistName)
	params.Set("autocorrect", "1")

	// Track has *string fields (Nillable) - check for nil or empty
	if track.MusicbrainzID != nil && *track.MusicbrainzID != "" {
		params.Set("mbid", *track.MusicbrainzID)
	}

	data, err := e.doRequest(ctx, "track.getinfo", params)
	if err != nil {
		return nil, err
	}
	if data == nil {
		e.logger.Debug("no Last.fm data for track", "track", track.Name)
		return nil, nil
	}

	var response lastfmTrackResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("failed to parse Last.fm response: %w", err)
	}

	// Extract tags
	tags := make([]string, 0, len(response.Track.TopTags.Tag))
	for _, t := range response.Track.TopTags.Tag {
		if t.Name != "" {
			tags = append(tags, t.Name)
		}
	}

	// Parse duration (Last.fm returns milliseconds as string)
	var durationMs int
	if _, err := fmt.Sscanf(response.Track.Duration, "%d", &durationMs); err != nil {
		e.logger.Debug("failed to parse track duration", "duration", response.Track.Duration, "error", err)
	}

	// Governing: SPEC-0014 REQ "Enricher Integration", ADR-0015 (Pluggable Enricher Registry)
	var typedTags []tagsutil.TypedTag
	for _, t := range tags {
		typedTags = append(typedTags, tagsutil.TypedTag{Name: t, Type: "id3"})
	}

	result := &enrichers.TrackData{
		Tags:      tags,
		TypedTags: typedTags,
	}

	if durationMs > 0 {
		result.DurationMs = durationMs
	}

	// Set MusicBrainz ID if we got one
	// Track has *string fields (Nillable) - check for nil or empty
	if response.Track.MBID != "" && (track.MusicbrainzID == nil || *track.MusicbrainzID == "") {
		result.MusicBrainzID = response.Track.MBID
	}

	e.logger.Debug("enriched track from Last.fm",
		"track", track.Name,
		"tags", len(tags))

	return result, nil
}

// cleanBio removes HTML tags and "Read more on Last.fm" links from bio text.
func cleanBio(bio string) string {
	if bio == "" {
		return ""
	}

	// Remove "Read more on Last.fm" link and text
	if idx := strings.Index(bio, "<a href=\"https://www.last.fm/"); idx != -1 {
		bio = strings.TrimSpace(bio[:idx])
	}

	// Remove HTML tags
	bio = stripHTML(bio)

	return strings.TrimSpace(bio)
}

// stripHTML removes HTML tags from a string.
func stripHTML(s string) string {
	var result strings.Builder
	inTag := false

	for _, r := range s {
		if r == '<' {
			inTag = true
		} else if r == '>' {
			inTag = false
		} else if !inTag {
			result.WriteRune(r)
		}
	}

	return result.String()
}

// imageSizeFromLastFM returns approximate dimensions for Last.fm image sizes.
func imageSizeFromLastFM(size string) (width, height int) {
	switch size {
	case "small":
		return 34, 34
	case "medium":
		return 64, 64
	case "large":
		return 174, 174
	case "extralarge":
		return 300, 300
	case "mega":
		return 500, 500
	default:
		return 0, 0
	}
}
