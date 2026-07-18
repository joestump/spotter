package navidrome

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/enrichers"
	"spotter/internal/resilience"
	"spotter/internal/tags"
)

const (
	staticSalt = "static"
)

// Enricher implements the Navidrome metadata enricher.
// It uses the Subsonic API to fetch ID3 tags and local metadata.
type Enricher struct {
	logger     *slog.Logger
	config     *config.Config
	user       *ent.User
	auth       *ent.NavidromeAuth
	httpClient *http.Client
}

// Ensure Enricher implements interfaces
var _ enrichers.Enricher = (*Enricher)(nil)
var _ enrichers.ArtistEnricher = (*Enricher)(nil)
var _ enrichers.AlbumEnricher = (*Enricher)(nil)
var _ enrichers.TrackEnricher = (*Enricher)(nil)

// New creates a new Navidrome enricher factory.
func New(logger *slog.Logger, cfg *config.Config) enrichers.Factory {
	return func(ctx context.Context, user *ent.User) (enrichers.Enricher, error) {
		// Check if Navidrome is configured
		if cfg.Navidrome.BaseURL == "" {
			return nil, nil
		}

		// Check if user has Navidrome auth
		if user.Edges.NavidromeAuth == nil {
			return nil, nil
		}

		return &Enricher{
			logger: logger,
			config: cfg,
			user:   user,
			auth:   user.Edges.NavidromeAuth,
			httpClient: &http.Client{
				Timeout: 30 * time.Second,
			},
		}, nil
	}
}

func (e *Enricher) Type() enrichers.Type {
	return enrichers.TypeNavidrome
}

func (e *Enricher) Name() string {
	return "Navidrome"
}

func (e *Enricher) IsAvailable() bool {
	return e.config.Navidrome.BaseURL != "" && e.auth != nil
}

// generateToken creates a Subsonic API token from salt and password.
func generateToken(password, salt string) string {
	hash := md5.Sum([]byte(password + salt))
	return hex.EncodeToString(hash[:])
}

// generateSalt creates a random salt for Subsonic API authentication.
func generateSalt() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to a less random salt if crypto/rand fails
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// doRequest performs an authenticated request to the Subsonic API.
func (e *Enricher) doRequest(ctx context.Context, method string, params url.Values) ([]byte, error) {
	if params == nil {
		params = url.Values{}
	}

	// Subsonic API authentication - use user.Username from User entity
	salt := generateSalt()
	token := generateToken(e.auth.Password, salt)

	params.Set("u", e.user.Username)
	params.Set("t", token)
	params.Set("s", salt)
	params.Set("c", "spotter")
	params.Set("v", "1.16.1")
	params.Set("f", "json")

	reqURL := fmt.Sprintf("%s/rest/%s?%s", e.config.Navidrome.BaseURL, method, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Governing: ADR-0020, SPEC error-handling REQ-ERR-002/REQ-ERR-003
		return nil, resilience.NewHTTPStatusError(resp.StatusCode, fmt.Errorf("Navidrome API returned status %d", resp.StatusCode))
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

	// Check for Subsonic API error
	var wrapper struct {
		SubsonicResponse struct {
			Status string `json:"status"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"subsonic-response"`
	}
	if err := json.Unmarshal(result, &wrapper); err == nil {
		if wrapper.SubsonicResponse.Status == "failed" && wrapper.SubsonicResponse.Error != nil {
			return nil, fmt.Errorf("Subsonic API error %d: %s",
				wrapper.SubsonicResponse.Error.Code,
				wrapper.SubsonicResponse.Error.Message)
		}
	}

	return result, nil
}

// Subsonic API response types
type subsonicArtistResponse struct {
	SubsonicResponse struct {
		Status string `json:"status"`
		Artist struct {
			ID             string          `json:"id"`
			Name           string          `json:"name"`
			AlbumCount     int             `json:"albumCount"`
			CoverArt       string          `json:"coverArt"`
			ArtistImageURL string          `json:"artistImageUrl"`
			MusicBrainzID  string          `json:"musicBrainzId"`
			SortName       string          `json:"sortName"`
			Album          []subsonicAlbum `json:"album"`
		} `json:"artist"`
	} `json:"subsonic-response"`
}

type subsonicArtistInfo struct {
	SubsonicResponse struct {
		Status     string `json:"status"`
		ArtistInfo struct {
			Biography      string `json:"biography"`
			MusicBrainzID  string `json:"musicBrainzId"`
			LastFMURL      string `json:"lastFmUrl"`
			SmallImageURL  string `json:"smallImageUrl"`
			MediumImageURL string `json:"mediumImageUrl"`
			LargeImageURL  string `json:"largeImageUrl"`
		} `json:"artistInfo2"`
	} `json:"subsonic-response"`
}

type subsonicAlbumResponse struct {
	SubsonicResponse struct {
		Status string `json:"status"`
		Album  struct {
			ID            string         `json:"id"`
			Name          string         `json:"name"`
			Artist        string         `json:"artist"`
			ArtistID      string         `json:"artistId"`
			CoverArt      string         `json:"coverArt"`
			SongCount     int            `json:"songCount"`
			Duration      int            `json:"duration"`
			Year          int            `json:"year"`
			Genre         string         `json:"genre"`
			MusicBrainzID string         `json:"musicBrainzId"`
			Song          []subsonicSong `json:"song"`
		} `json:"album"`
	} `json:"subsonic-response"`
}

type subsonicAlbum struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Artist        string `json:"artist"`
	ArtistID      string `json:"artistId"`
	CoverArt      string `json:"coverArt"`
	SongCount     int    `json:"songCount"`
	Duration      int    `json:"duration"`
	Year          int    `json:"year"`
	Genre         string `json:"genre"`
	MusicBrainzID string `json:"musicBrainzId"`
}

type subsonicSongResponse struct {
	SubsonicResponse struct {
		Status string       `json:"status"`
		Song   subsonicSong `json:"song"`
	} `json:"subsonic-response"`
}

type subsonicSong struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Album         string `json:"album"`
	AlbumID       string `json:"albumId"`
	Artist        string `json:"artist"`
	ArtistID      string `json:"artistId"`
	Track         int    `json:"track"`
	DiscNumber    int    `json:"discNumber"`
	Year          int    `json:"year"`
	Genre         string `json:"genre"`
	CoverArt      string `json:"coverArt"`
	Duration      int    `json:"duration"`
	BitRate       int    `json:"bitRate"`
	Path          string `json:"path"`
	MusicBrainzID string `json:"musicBrainzId"`
	BPM           int    `json:"bpm"`
}

type subsonicSearchResponse struct {
	SubsonicResponse struct {
		Status        string `json:"status"`
		SearchResult3 struct {
			Artist []struct {
				ID            string `json:"id"`
				Name          string `json:"name"`
				AlbumCount    int    `json:"albumCount"`
				MusicBrainzID string `json:"musicBrainzId"`
			} `json:"artist"`
			Album []subsonicAlbum `json:"album"`
			Song  []subsonicSong  `json:"song"`
		} `json:"searchResult3"`
	} `json:"subsonic-response"`
}

// searchArtist searches for an artist by name in Navidrome.
func (e *Enricher) searchArtist(ctx context.Context, name string) (string, error) {
	params := url.Values{}
	params.Set("query", name)
	params.Set("artistCount", "5")
	params.Set("albumCount", "0")
	params.Set("songCount", "0")

	data, err := e.doRequest(ctx, "search3", params)
	if err != nil {
		return "", err
	}

	var response subsonicSearchResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(response.SubsonicResponse.SearchResult3.Artist) == 0 {
		return "", nil
	}

	return response.SubsonicResponse.SearchResult3.Artist[0].ID, nil
}

// EnrichArtist fetches artist metadata from Navidrome.
func (e *Enricher) EnrichArtist(ctx context.Context, artist *ent.Artist) (*enrichers.ArtistData, error) {
	e.logger.Debug("enriching artist from Navidrome", "name", artist.Name)

	// Artist has string fields (not *string) - check for empty string
	artistID := artist.NavidromeID
	if artistID == "" {
		var err error
		artistID, err = e.searchArtist(ctx, artist.Name)
		if err != nil {
			return nil, err
		}
		if artistID == "" {
			e.logger.Debug("no Navidrome match found for artist", "name", artist.Name)
			return nil, nil
		}
	}

	// Get artist details
	params := url.Values{}
	params.Set("id", artistID)

	data, err := e.doRequest(ctx, "getArtist", params)
	if err != nil {
		return nil, err
	}

	var response subsonicArtistResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("failed to parse artist response: %w", err)
	}

	result := &enrichers.ArtistData{
		NavidromeID: artistID,
	}

	if response.SubsonicResponse.Artist.SortName != "" {
		result.SortName = response.SubsonicResponse.Artist.SortName
	}

	if response.SubsonicResponse.Artist.MusicBrainzID != "" {
		result.MusicBrainzID = response.SubsonicResponse.Artist.MusicBrainzID
	}

	// Get additional artist info (bio, last.fm URL, etc.)
	infoData, err := e.doRequest(ctx, "getArtistInfo2", params)
	if err == nil {
		var info subsonicArtistInfo
		if err := json.Unmarshal(infoData, &info); err == nil {
			if info.SubsonicResponse.ArtistInfo.Biography != "" {
				result.Bio = info.SubsonicResponse.ArtistInfo.Biography
			}
			if info.SubsonicResponse.ArtistInfo.LastFMURL != "" {
				result.LastFMURL = info.SubsonicResponse.ArtistInfo.LastFMURL
			}
			if info.SubsonicResponse.ArtistInfo.MusicBrainzID != "" && result.MusicBrainzID == "" {
				result.MusicBrainzID = info.SubsonicResponse.ArtistInfo.MusicBrainzID
			}
		}
	}

	e.logger.Debug("enriched artist from Navidrome",
		"artist", artist.Name,
		"navidrome_id", artistID)

	return result, nil
}

// GetArtistImages returns artist images from Navidrome.
func (e *Enricher) GetArtistImages(ctx context.Context, artist *ent.Artist) ([]enrichers.ImageData, error) {
	// Artist has string fields (not *string) - check for empty string
	artistID := artist.NavidromeID
	if artistID == "" {
		var err error
		artistID, err = e.searchArtist(ctx, artist.Name)
		if err != nil {
			return nil, err
		}
		if artistID == "" {
			return nil, nil
		}
	}

	params := url.Values{}
	params.Set("id", artistID)

	data, err := e.doRequest(ctx, "getArtistInfo2", params)
	if err != nil {
		return nil, err
	}

	var info subsonicArtistInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}

	var images []enrichers.ImageData

	// Add images from artist info.
	// LargeImageURL, MediumImageURL, and SmallImageURL are direct CDN URLs (e.g. Spotify CDN)
	// forwarded by Navidrome's getArtistInfo2 response. They must be downloaded directly,
	// not wrapped in a getCoverArt call (which expects internal Navidrome entity IDs).
	if directURL := info.SubsonicResponse.ArtistInfo.LargeImageURL; directURL != "" {
		localPath := fmt.Sprintf("data/images/artists/%d_navidrome_large.png", artist.ID)
		_, err := enrichers.DownloadAndSaveImage(directURL, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download navidrome image", "url", directURL, "error", err)
		} else {
			images = append(images, enrichers.ImageData{
				URL:       directURL,
				LocalPath: localPath,
				Type:      "thumbnail",
				Source:    "navidrome",
				IsPrimary: true,
			})
		}
	}

	if directURL := info.SubsonicResponse.ArtistInfo.MediumImageURL; directURL != "" && directURL != info.SubsonicResponse.ArtistInfo.LargeImageURL {
		localPath := fmt.Sprintf("data/images/artists/%d_navidrome_medium.png", artist.ID)
		_, err := enrichers.DownloadAndSaveImage(directURL, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download navidrome image", "url", directURL, "error", err)
		} else {
			images = append(images, enrichers.ImageData{
				URL:       directURL,
				LocalPath: localPath,
				Type:      "thumbnail",
				Source:    "navidrome",
			})
		}
	}

	if directURL := info.SubsonicResponse.ArtistInfo.SmallImageURL; directURL != "" && directURL != info.SubsonicResponse.ArtistInfo.MediumImageURL {
		localPath := fmt.Sprintf("data/images/artists/%d_navidrome_small.png", artist.ID)
		_, err := enrichers.DownloadAndSaveImage(directURL, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download navidrome image", "url", directURL, "error", err)
		} else {
			images = append(images, enrichers.ImageData{
				URL:       directURL,
				LocalPath: localPath,
				Type:      "thumbnail",
				Source:    "navidrome",
			})
		}
	}

	return images, nil
}

// searchAlbum searches for an album by name and artist in Navidrome.
func (e *Enricher) searchAlbum(ctx context.Context, albumName, artistName string) (string, error) {
	query := albumName
	if artistName != "" {
		query = fmt.Sprintf("%s %s", albumName, artistName)
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("artistCount", "0")
	params.Set("albumCount", "5")
	params.Set("songCount", "0")

	data, err := e.doRequest(ctx, "search3", params)
	if err != nil {
		return "", err
	}

	var response subsonicSearchResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(response.SubsonicResponse.SearchResult3.Album) == 0 {
		return "", nil
	}

	return response.SubsonicResponse.SearchResult3.Album[0].ID, nil
}

// EnrichAlbum fetches album metadata from Navidrome.
func (e *Enricher) EnrichAlbum(ctx context.Context, album *ent.Album) (*enrichers.AlbumData, error) {
	artistName := ""
	if album.Edges.Artist != nil {
		artistName = album.Edges.Artist.Name
	}

	e.logger.Debug("enriching album from Navidrome", "album", album.Name, "artist", artistName)

	albumID, err := e.searchAlbum(ctx, album.Name, artistName)
	if err != nil {
		return nil, err
	}
	if albumID == "" {
		e.logger.Debug("no Navidrome match found for album", "album", album.Name)
		return nil, nil
	}

	params := url.Values{}
	params.Set("id", albumID)

	data, err := e.doRequest(ctx, "getAlbum", params)
	if err != nil {
		return nil, err
	}

	var response subsonicAlbumResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("failed to parse album response: %w", err)
	}

	alb := response.SubsonicResponse.Album

	// Governing: SPEC-0014 REQ "Enricher Integration", ADR-0015 (Pluggable Enricher Registry)
	var typedTags []tags.TypedTag
	if alb.Genre != "" {
		typedTags = append(typedTags, tags.TypedTag{Name: alb.Genre, Type: "genre"})
	}

	result := &enrichers.AlbumData{
		Year:        alb.Year,
		Genre:       alb.Genre,
		TotalTracks: alb.SongCount,
		TypedTags:   typedTags,
	}

	if alb.MusicBrainzID != "" {
		result.MusicBrainzID = alb.MusicBrainzID
	}

	e.logger.Debug("enriched album from Navidrome",
		"album", album.Name,
		"year", result.Year,
		"genre", result.Genre)

	return result, nil
}

// GetAlbumImages returns album artwork from Navidrome.
func (e *Enricher) GetAlbumImages(ctx context.Context, album *ent.Album) ([]enrichers.ImageData, error) {
	artistName := ""
	if album.Edges.Artist != nil {
		artistName = album.Edges.Artist.Name
	}

	albumID, err := e.searchAlbum(ctx, album.Name, artistName)
	if err != nil {
		return nil, err
	}
	if albumID == "" {
		return nil, nil
	}

	params := url.Values{}
	params.Set("id", albumID)

	data, err := e.doRequest(ctx, "getAlbum", params)
	if err != nil {
		return nil, err
	}

	var response subsonicAlbumResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}

	var images []enrichers.ImageData

	if coverArt := response.SubsonicResponse.Album.CoverArt; coverArt != "" {
		// Build URL to fetch cover art - use user.Username from User entity
		salt := staticSalt
		coverURL := fmt.Sprintf("%s/rest/getCoverArt?id=%s&u=%s&t=%s&s=%s&c=spotter&v=1.16.1",
			e.config.Navidrome.BaseURL,
			coverArt,
			e.user.Username,
			generateToken(e.auth.Password, salt),
			salt,
		)

		localPath := fmt.Sprintf("data/images/albums/%d_navidrome.png", album.ID)
		_, err := enrichers.DownloadAndSaveImage(coverURL, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download navidrome image", "url", coverURL, "error", err)
		} else {
			images = append(images, enrichers.ImageData{
				URL:       coverURL,
				LocalPath: localPath,
				Type:      "cover_front",
				Source:    "navidrome",
				IsPrimary: true,
			})
		}
	}

	return images, nil
}

// searchTrack searches for a track by name and artist in Navidrome.
func (e *Enricher) searchTrack(ctx context.Context, trackName, artistName, albumName string) (string, error) {
	query := trackName
	if artistName != "" {
		query = fmt.Sprintf("%s %s", trackName, artistName)
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("artistCount", "0")
	params.Set("albumCount", "0")
	params.Set("songCount", "10")

	data, err := e.doRequest(ctx, "search3", params)
	if err != nil {
		return "", err
	}

	var response subsonicSearchResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(response.SubsonicResponse.SearchResult3.Song) == 0 {
		return "", nil
	}

	// Try to match by album name if provided
	if albumName != "" {
		for _, song := range response.SubsonicResponse.SearchResult3.Song {
			if song.Album == albumName {
				return song.ID, nil
			}
		}
	}

	return response.SubsonicResponse.SearchResult3.Song[0].ID, nil
}

// EnrichTrack fetches track metadata from Navidrome.
func (e *Enricher) EnrichTrack(ctx context.Context, track *ent.Track) (*enrichers.TrackData, error) {
	artistName := ""
	albumName := ""
	if track.Edges.Artist != nil {
		artistName = track.Edges.Artist.Name
	}
	if track.Edges.Album != nil {
		albumName = track.Edges.Album.Name
	}

	e.logger.Debug("enriching track from Navidrome", "track", track.Name, "artist", artistName)

	// Track has *string fields (Nillable) - check for nil or empty
	trackID := ""
	if track.NavidromeID != nil && *track.NavidromeID != "" {
		trackID = *track.NavidromeID
	} else {
		var err error
		trackID, err = e.searchTrack(ctx, track.Name, artistName, albumName)
		if err != nil {
			return nil, err
		}
		if trackID == "" {
			e.logger.Debug("no Navidrome match found for track", "track", track.Name)
			return nil, nil
		}
	}

	params := url.Values{}
	params.Set("id", trackID)

	data, err := e.doRequest(ctx, "getSong", params)
	if err != nil {
		return nil, err
	}

	var response subsonicSongResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("failed to parse song response: %w", err)
	}

	song := response.SubsonicResponse.Song

	// Governing: SPEC-0014 REQ "Enricher Integration", ADR-0015 (Pluggable Enricher Registry)
	var typedTags []tags.TypedTag
	if song.Genre != "" {
		typedTags = append(typedTags, tags.TypedTag{Name: song.Genre, Type: "genre"})
	}

	result := &enrichers.TrackData{
		NavidromeID: trackID,
		DurationMs:  song.Duration * 1000, // Navidrome returns seconds
		TrackNumber: song.Track,
		DiscNumber:  song.DiscNumber,
		TypedTags:   typedTags,
	}

	if song.Genre != "" {
		result.Genres = []string{song.Genre}
	}

	if song.MusicBrainzID != "" {
		result.MusicBrainzID = song.MusicBrainzID
	}

	if song.BPM > 0 {
		bpm := float64(song.BPM)
		result.BPM = &bpm
	}

	e.logger.Debug("enriched track from Navidrome",
		"track", track.Name,
		"navidrome_id", trackID,
		"duration_ms", result.DurationMs)

	return result, nil
}
