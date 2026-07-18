package fanart

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/enrichers"
	"spotter/internal/resilience"
)

const (
	baseURL = "https://webservice.fanart.tv/v3"
)

// Enricher implements the Fanart.tv metadata enricher.
type Enricher struct {
	logger     *slog.Logger
	config     *config.Config
	httpClient *http.Client
	apiKey     string
	baseURL    string
}

// Ensure Enricher implements interfaces
var _ enrichers.Enricher = (*Enricher)(nil)
var _ enrichers.ArtistEnricher = (*Enricher)(nil)
var _ enrichers.AlbumEnricher = (*Enricher)(nil)

// New creates a new Fanart.tv enricher factory.
func New(logger *slog.Logger, cfg *config.Config) enrichers.Factory {
	return func(ctx context.Context, user *ent.User) (enrichers.Enricher, error) {
		if cfg.Metadata.Fanart.APIKey == "" {
			return nil, nil
		}

		return &Enricher{
			logger: logger,
			config: cfg,
			httpClient: &http.Client{
				Timeout: 30 * time.Second,
			},
			apiKey:  cfg.Metadata.Fanart.APIKey,
			baseURL: baseURL,
		}, nil
	}
}

func (e *Enricher) Type() enrichers.Type {
	return enrichers.TypeFanart
}

func (e *Enricher) Name() string {
	return "Fanart.tv"
}

func (e *Enricher) IsAvailable() bool {
	return e.apiKey != ""
}

// doRequest performs an authenticated request to the Fanart.tv API.
func (e *Enricher) doRequest(ctx context.Context, endpoint string) ([]byte, error) {
	reqURL := fmt.Sprintf("%s/%s?api_key=%s", e.baseURL, endpoint, e.apiKey)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

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

	if resp.StatusCode == http.StatusNotFound {
		// No data available for this entity
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		// Governing: ADR-0020, SPEC error-handling REQ-ERR-002/REQ-ERR-003
		return nil, resilience.NewHTTPStatusError(resp.StatusCode, fmt.Errorf("Fanart.tv API returned status %d", resp.StatusCode))
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

	return result, nil
}

// Fanart.tv API response types for music artists
type fanartArtistResponse struct {
	Name             string        `json:"name"`
	MBID             string        `json:"mbid_id"`
	ArtistBackground []fanartImage `json:"artistbackground"`
	ArtistThumb      []fanartImage `json:"artistthumb"`
	MusicLogo        []fanartImage `json:"musiclogo"`
	HDMusicLogo      []fanartImage `json:"hdmusiclogo"`
	MusicBanner      []fanartImage `json:"musicbanner"`
	ArtistFanart     []fanartImage `json:"artistfanart"`
}

type fanartImage struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Likes string `json:"likes"`
}

// Fanart.tv API response types for albums
type fanartAlbumResponse struct {
	Name   string                 `json:"name"`
	MBID   string                 `json:"mbid_id"`
	Albums map[string]fanartAlbum `json:"albums"`
}

type fanartAlbum struct {
	CDart      []fanartImage `json:"cdart"`
	AlbumCover []fanartImage `json:"albumcover"`
}

// EnrichArtist is not implemented for Fanart.tv as it only provides images.
func (e *Enricher) EnrichArtist(ctx context.Context, artist *ent.Artist) (*enrichers.ArtistData, error) {
	// Fanart.tv only provides images, not metadata
	return nil, nil
}

// GetArtistImages fetches artist images from Fanart.tv.
// Requires a MusicBrainz ID to be set on the artist.
func (e *Enricher) GetArtistImages(ctx context.Context, artist *ent.Artist) ([]enrichers.ImageData, error) {
	// Artist has string fields (not *string) - check for empty string
	if artist.MusicbrainzID == "" {
		e.logger.Debug("skipping Fanart.tv - no MusicBrainz ID", "artist", artist.Name)
		return nil, nil
	}

	mbid := artist.MusicbrainzID
	e.logger.Debug("fetching artist images from Fanart.tv", "artist", artist.Name, "mbid", mbid)

	data, err := e.doRequest(ctx, fmt.Sprintf("music/%s", mbid))
	if err != nil {
		return nil, err
	}
	if data == nil {
		e.logger.Debug("no Fanart.tv data for artist", "artist", artist.Name, "mbid", mbid)
		return nil, nil
	}

	var response fanartArtistResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("failed to parse Fanart.tv response: %w", err)
	}

	var images []enrichers.ImageData

	// Add HD music logos (preferred over regular logos)
	for i, img := range response.HDMusicLogo {
		likes := parseLikes(img.Likes)
		localPath := fmt.Sprintf("data/images/artists/%d_fanart_%s.png", artist.ID, img.ID)
		_, err := enrichers.DownloadAndSaveImage(img.URL, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download fanart image", "url", img.URL, "error", err)
			continue
		}
		images = append(images, enrichers.ImageData{
			URL:       img.URL,
			LocalPath: localPath,
			Type:      "logo",
			Source:    "fanart",
			Likes:     likes,
			IsPrimary: i == 0 && len(response.HDMusicLogo) > 0,
		})
	}

	// Add regular music logos
	for _, img := range response.MusicLogo {
		likes := parseLikes(img.Likes)
		localPath := fmt.Sprintf("data/images/artists/%d_fanart_%s.png", artist.ID, img.ID)
		_, err := enrichers.DownloadAndSaveImage(img.URL, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download fanart image", "url", img.URL, "error", err)
			continue
		}
		images = append(images, enrichers.ImageData{
			URL:       img.URL,
			LocalPath: localPath,
			Type:      "logo",
			Source:    "fanart",
			Likes:     likes,
			IsPrimary: false, // HD logos take priority
		})
	}

	// Add artist backgrounds
	for i, img := range response.ArtistBackground {
		likes := parseLikes(img.Likes)
		localPath := fmt.Sprintf("data/images/artists/%d_fanart_%s.png", artist.ID, img.ID)
		_, err := enrichers.DownloadAndSaveImage(img.URL, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download fanart image", "url", img.URL, "error", err)
			continue
		}
		images = append(images, enrichers.ImageData{
			URL:       img.URL,
			LocalPath: localPath,
			Type:      "background",
			Source:    "fanart",
			Likes:     likes,
			IsPrimary: i == 0,
		})
	}

	// Add artist fanart
	for i, img := range response.ArtistFanart {
		likes := parseLikes(img.Likes)
		localPath := fmt.Sprintf("data/images/artists/%d_fanart_%s.png", artist.ID, img.ID)
		_, err := enrichers.DownloadAndSaveImage(img.URL, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download fanart image", "url", img.URL, "error", err)
			continue
		}
		images = append(images, enrichers.ImageData{
			URL:       img.URL,
			LocalPath: localPath,
			Type:      "fanart",
			Source:    "fanart",
			Likes:     likes,
			IsPrimary: i == 0,
		})
	}

	// Add artist thumbnails
	for i, img := range response.ArtistThumb {
		likes := parseLikes(img.Likes)
		localPath := fmt.Sprintf("data/images/artists/%d_fanart_%s.png", artist.ID, img.ID)
		_, err := enrichers.DownloadAndSaveImage(img.URL, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download fanart image", "url", img.URL, "error", err)
			continue
		}
		images = append(images, enrichers.ImageData{
			URL:       img.URL,
			LocalPath: localPath,
			Type:      "thumbnail",
			Source:    "fanart",
			Likes:     likes,
			IsPrimary: i == 0,
		})
	}

	// Add music banners
	for i, img := range response.MusicBanner {
		likes := parseLikes(img.Likes)
		localPath := fmt.Sprintf("data/images/artists/%d_fanart_%s.png", artist.ID, img.ID)
		_, err := enrichers.DownloadAndSaveImage(img.URL, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download fanart image", "url", img.URL, "error", err)
			continue
		}
		images = append(images, enrichers.ImageData{
			URL:       img.URL,
			LocalPath: localPath,
			Type:      "banner",
			Source:    "fanart",
			Likes:     likes,
			IsPrimary: i == 0,
		})
	}

	e.logger.Debug("fetched artist images from Fanart.tv",
		"artist", artist.Name,
		"total_images", len(images))

	return images, nil
}

// EnrichAlbum is not implemented for Fanart.tv as it only provides images.
func (e *Enricher) EnrichAlbum(ctx context.Context, album *ent.Album) (*enrichers.AlbumData, error) {
	// Fanart.tv only provides images, not metadata
	return nil, nil
}

// GetAlbumImages fetches album images from Fanart.tv.
// Requires a MusicBrainz ID on the album's artist.
func (e *Enricher) GetAlbumImages(ctx context.Context, album *ent.Album) ([]enrichers.ImageData, error) {
	// Fanart.tv uses the artist's MBID to get album art, keyed by album MBID
	// Artist has string fields (not *string) - check for empty string
	artistMBID := ""
	if album.Edges.Artist != nil && album.Edges.Artist.MusicbrainzID != "" {
		artistMBID = album.Edges.Artist.MusicbrainzID
	}

	if artistMBID == "" {
		e.logger.Debug("skipping Fanart.tv album - no artist MusicBrainz ID", "album", album.Name)
		return nil, nil
	}

	// Album has string fields (not *string) - check for empty string
	albumMBID := album.MusicbrainzID
	if albumMBID == "" {
		e.logger.Debug("skipping Fanart.tv album - no album MusicBrainz ID", "album", album.Name)
		return nil, nil
	}

	e.logger.Debug("fetching album images from Fanart.tv",
		"album", album.Name,
		"artist_mbid", artistMBID,
		"album_mbid", albumMBID)

	data, err := e.doRequest(ctx, fmt.Sprintf("music/%s", artistMBID))
	if err != nil {
		return nil, err
	}
	if data == nil {
		e.logger.Debug("no Fanart.tv data for artist", "artist_mbid", artistMBID)
		return nil, nil
	}

	var response fanartAlbumResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("failed to parse Fanart.tv response: %w", err)
	}

	// Find the specific album by MBID
	albumData, ok := response.Albums[albumMBID]
	if !ok {
		e.logger.Debug("no Fanart.tv data for album", "album", album.Name, "mbid", albumMBID)
		return nil, nil
	}

	var images []enrichers.ImageData

	// Add CD art
	for i, img := range albumData.CDart {
		likes := parseLikes(img.Likes)
		localPath := fmt.Sprintf("data/images/albums/%d_fanart_%s.png", album.ID, img.ID)
		_, err := enrichers.DownloadAndSaveImage(img.URL, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download fanart image", "url", img.URL, "error", err)
			continue
		}
		images = append(images, enrichers.ImageData{
			URL:       img.URL,
			LocalPath: localPath,
			Type:      "cd_art",
			Source:    "fanart",
			Likes:     likes,
			IsPrimary: i == 0,
		})
	}

	// Add album covers
	for i, img := range albumData.AlbumCover {
		likes := parseLikes(img.Likes)
		localPath := fmt.Sprintf("data/images/albums/%d_fanart_%s.png", album.ID, img.ID)
		_, err := enrichers.DownloadAndSaveImage(img.URL, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download fanart image", "url", img.URL, "error", err)
			continue
		}
		images = append(images, enrichers.ImageData{
			URL:       img.URL,
			LocalPath: localPath,
			Type:      "cover_front",
			Source:    "fanart",
			Likes:     likes,
			IsPrimary: i == 0,
		})
	}

	e.logger.Debug("fetched album images from Fanart.tv",
		"album", album.Name,
		"total_images", len(images))

	return images, nil
}

// parseLikes converts the likes string to an integer.
func parseLikes(s string) int {
	var likes int
	if _, err := fmt.Sscanf(s, "%d", &likes); err != nil {
		return 0
	}
	return likes
}
