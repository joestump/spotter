package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/enrichers"

	"golang.org/x/oauth2"
	spotifyOAuth "golang.org/x/oauth2/spotify"
)

// Enricher implements the Spotify metadata enricher.
type Enricher struct {
	logger     *slog.Logger
	config     *config.Config
	user       *ent.User
	auth       *ent.SpotifyAuth
	oauth      *oauth2.Config
	httpClient *http.Client
}

// Ensure Enricher implements interfaces
var _ enrichers.Enricher = (*Enricher)(nil)
var _ enrichers.ArtistEnricher = (*Enricher)(nil)
var _ enrichers.AlbumEnricher = (*Enricher)(nil)
var _ enrichers.TrackEnricher = (*Enricher)(nil)
var _ enrichers.IDMatcher = (*Enricher)(nil)

// New creates a new Spotify enricher factory.
func New(logger *slog.Logger, cfg *config.Config) enrichers.Factory {
	return func(ctx context.Context, user *ent.User) (enrichers.Enricher, error) {
		// Check if Spotify is configured
		if cfg.Spotify.ClientID == "" || cfg.Spotify.ClientSecret == "" {
			return nil, nil
		}

		// Check if user has Spotify auth
		if user.Edges.SpotifyAuth == nil {
			return nil, nil
		}

		return &Enricher{
			logger: logger,
			config: cfg,
			user:   user,
			auth:   user.Edges.SpotifyAuth,
			oauth: &oauth2.Config{
				ClientID:     cfg.Spotify.ClientID,
				ClientSecret: cfg.Spotify.ClientSecret,
				RedirectURL:  cfg.Spotify.RedirectURL,
				Endpoint:     spotifyOAuth.Endpoint,
			},
			httpClient: &http.Client{
				Timeout: 30 * time.Second,
			},
		}, nil
	}
}

func (e *Enricher) Type() enrichers.Type {
	return enrichers.TypeSpotify
}

func (e *Enricher) Name() string {
	return "Spotify"
}

func (e *Enricher) IsAvailable() bool {
	return e.config.Spotify.ClientID != "" &&
		e.config.Spotify.ClientSecret != "" &&
		e.auth != nil
}

// getValidToken returns a valid access token, refreshing if necessary.
func (e *Enricher) getValidToken(ctx context.Context) (string, error) {
	if e.auth == nil {
		return "", fmt.Errorf("no Spotify auth configured")
	}

	// Check if token is still valid (with 5 minute buffer)
	if time.Now().Add(5 * time.Minute).Before(e.auth.Expiry) {
		return e.auth.AccessToken, nil
	}

	// Token expired, refresh it
	e.logger.Debug("refreshing expired Spotify token")

	token := &oauth2.Token{
		RefreshToken: e.auth.RefreshToken,
	}

	tokenSource := e.oauth.TokenSource(ctx, token)
	newToken, err := tokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("failed to refresh token: %w", err)
	}

	// Update the auth struct (caller should persist)
	e.auth.AccessToken = newToken.AccessToken
	e.auth.Expiry = newToken.Expiry
	if newToken.RefreshToken != "" {
		e.auth.RefreshToken = newToken.RefreshToken
	}

	return newToken.AccessToken, nil
}

// doRequest performs an authenticated request to the Spotify API.
func (e *Enricher) doRequest(ctx context.Context, endpoint string) ([]byte, error) {
	token, err := e.getValidToken(ctx)
	if err != nil {
		return nil, err
	}

	reqURL := fmt.Sprintf("https://api.spotify.com/v1/%s", endpoint)
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
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

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("Spotify API rate limited")
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("Spotify API unauthorized - token may be invalid")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Spotify API returned status %d", resp.StatusCode)
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

// Spotify API response types
type spotifyArtist struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Genres     []string `json:"genres"`
	Popularity int      `json:"popularity"`
	Followers  struct {
		Total int `json:"total"`
	} `json:"followers"`
	Images []spotifyImage `json:"images"`
}

type spotifyImage struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type spotifyAlbum struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	AlbumType   string          `json:"album_type"`
	ReleaseDate string          `json:"release_date"`
	TotalTracks int             `json:"total_tracks"`
	Genres      []string        `json:"genres"`
	Popularity  int             `json:"popularity"`
	Label       string          `json:"label"`
	Images      []spotifyImage  `json:"images"`
	Artists     []spotifyArtist `json:"artists"`
}

type spotifyTrack struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Popularity  int    `json:"popularity"`
	DurationMs  int    `json:"duration_ms"`
	TrackNumber int    `json:"track_number"`
	DiscNumber  int    `json:"disc_number"`
	ExternalIDs struct {
		ISRC string `json:"isrc"`
	} `json:"external_ids"`
	ExternalURLs struct {
		Spotify string `json:"spotify"`
	} `json:"external_urls"`
	Artists []spotifyArtist `json:"artists"`
	Album   spotifyAlbum    `json:"album"`
}

type spotifyAudioFeatures struct {
	ID               string  `json:"id"`
	Tempo            float64 `json:"tempo"` // BPM
	Key              int     `json:"key"`
	Mode             int     `json:"mode"` // 0 = minor, 1 = major
	Energy           float64 `json:"energy"`
	Danceability     float64 `json:"danceability"`
	Valence          float64 `json:"valence"`
	Acousticness     float64 `json:"acousticness"`
	Instrumentalness float64 `json:"instrumentalness"`
	Speechiness      float64 `json:"speechiness"`
	Liveness         float64 `json:"liveness"`
}

type searchResponse struct {
	Artists struct {
		Items []spotifyArtist `json:"items"`
	} `json:"artists"`
	Albums struct {
		Items []spotifyAlbum `json:"items"`
	} `json:"albums"`
	Tracks struct {
		Items []spotifyTrack `json:"items"`
	} `json:"tracks"`
}

// keyToString converts Spotify's numeric key to a string.
func keyToString(key, mode int) string {
	keys := []string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}
	if key < 0 || key > 11 {
		return ""
	}
	k := keys[key]
	if mode == 0 {
		k += "m" // minor
	}
	return k
}

// MatchArtist searches Spotify for an artist by name.
func (e *Enricher) MatchArtist(ctx context.Context, name string) (string, float64, error) {
	e.logger.Debug("matching artist in Spotify", "name", name)

	endpoint := fmt.Sprintf("search?type=artist&q=%s&limit=5", url.QueryEscape(name))
	data, err := e.doRequest(ctx, endpoint)
	if err != nil {
		return "", 0, err
	}

	var result searchResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Artists.Items) == 0 {
		return "", 0, nil
	}

	best := result.Artists.Items[0]
	// Calculate confidence based on name similarity
	confidence := 0.9
	if !strings.EqualFold(best.Name, name) {
		confidence = 0.7
	}

	e.logger.Debug("found Spotify artist match",
		"name", name,
		"spotify_id", best.ID,
		"matched_name", best.Name,
		"confidence", confidence)

	return best.ID, confidence, nil
}

// MatchAlbum searches Spotify for an album by name and artist.
func (e *Enricher) MatchAlbum(ctx context.Context, albumName, artistName string) (string, float64, error) {
	e.logger.Debug("matching album in Spotify", "album", albumName, "artist", artistName)

	query := fmt.Sprintf("album:%s", albumName)
	if artistName != "" {
		query += fmt.Sprintf(" artist:%s", artistName)
	}

	endpoint := fmt.Sprintf("search?type=album&q=%s&limit=5", url.QueryEscape(query))
	data, err := e.doRequest(ctx, endpoint)
	if err != nil {
		return "", 0, err
	}

	var result searchResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Albums.Items) == 0 {
		return "", 0, nil
	}

	best := result.Albums.Items[0]
	confidence := 0.9
	if !strings.EqualFold(best.Name, albumName) {
		confidence = 0.7
	}

	e.logger.Debug("found Spotify album match",
		"album", albumName,
		"spotify_id", best.ID,
		"matched_name", best.Name,
		"confidence", confidence)

	return best.ID, confidence, nil
}

// MatchTrack searches Spotify for a track by name, artist, and album.
func (e *Enricher) MatchTrack(ctx context.Context, trackName, artistName, albumName string) (string, float64, error) {
	e.logger.Debug("matching track in Spotify", "track", trackName, "artist", artistName, "album", albumName)

	query := fmt.Sprintf("track:%s", trackName)
	if artistName != "" {
		query += fmt.Sprintf(" artist:%s", artistName)
	}
	if albumName != "" {
		query += fmt.Sprintf(" album:%s", albumName)
	}

	endpoint := fmt.Sprintf("search?type=track&q=%s&limit=5", url.QueryEscape(query))
	data, err := e.doRequest(ctx, endpoint)
	if err != nil {
		return "", 0, err
	}

	var result searchResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Tracks.Items) == 0 {
		return "", 0, nil
	}

	best := result.Tracks.Items[0]
	confidence := 0.9
	if !strings.EqualFold(best.Name, trackName) {
		confidence = 0.7
	}

	e.logger.Debug("found Spotify track match",
		"track", trackName,
		"spotify_id", best.ID,
		"matched_name", best.Name,
		"confidence", confidence)

	return best.ID, confidence, nil
}

// EnrichArtist fetches detailed artist data from Spotify.
func (e *Enricher) EnrichArtist(ctx context.Context, artist *ent.Artist) (*enrichers.ArtistData, error) {
	// Artist has string fields (not *string) - check for empty string
	spotifyID := artist.SpotifyID
	if spotifyID == "" {
		// Try to find the artist
		var err error
		spotifyID, _, err = e.MatchArtist(ctx, artist.Name)
		if err != nil {
			return nil, err
		}
		if spotifyID == "" {
			e.logger.Debug("no Spotify match found for artist", "name", artist.Name)
			return nil, nil
		}
	}

	e.logger.Debug("enriching artist from Spotify", "name", artist.Name, "spotify_id", spotifyID)

	data, err := e.doRequest(ctx, fmt.Sprintf("artists/%s", spotifyID))
	if err != nil {
		return nil, err
	}

	var sp spotifyArtist
	if err := json.Unmarshal(data, &sp); err != nil {
		return nil, fmt.Errorf("failed to parse artist response: %w", err)
	}

	popularity := sp.Popularity
	followers := sp.Followers.Total

	return &enrichers.ArtistData{
		SpotifyID:     spotifyID,
		Genres:        sp.Genres,
		Popularity:    &popularity,
		FollowerCount: &followers,
	}, nil
}

// GetArtistImages returns artist images from Spotify.
func (e *Enricher) GetArtistImages(ctx context.Context, artist *ent.Artist) ([]enrichers.ImageData, error) {
	// Artist has string fields (not *string) - check for empty string
	spotifyID := artist.SpotifyID
	if spotifyID == "" {
		return nil, nil
	}

	data, err := e.doRequest(ctx, fmt.Sprintf("artists/%s", spotifyID))
	if err != nil {
		return nil, err
	}

	var sp spotifyArtist
	if err := json.Unmarshal(data, &sp); err != nil {
		return nil, err
	}

	var images []enrichers.ImageData
	for i, img := range sp.Images {
		localPath := fmt.Sprintf("data/images/artists/%d_spotify_%d.png", artist.ID, i)
		_, err := enrichers.DownloadAndSaveImage(img.URL, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download spotify image", "url", img.URL, "error", err)
			continue
		}
		images = append(images, enrichers.ImageData{
			URL:       img.URL,
			LocalPath: localPath,
			Type:      "thumbnail",
			Source:    "spotify",
			Width:     img.Width,
			Height:    img.Height,
			IsPrimary: i == 0, // First image is usually the best
		})
	}

	return images, nil
}

// EnrichAlbum fetches detailed album data from Spotify.
func (e *Enricher) EnrichAlbum(ctx context.Context, album *ent.Album) (*enrichers.AlbumData, error) {
	// Album has string fields (not *string) - check for empty string
	spotifyID := album.SpotifyID
	if spotifyID == "" {
		artistName := ""
		if album.Edges.Artist != nil {
			artistName = album.Edges.Artist.Name
		}
		var err error
		spotifyID, _, err = e.MatchAlbum(ctx, album.Name, artistName)
		if err != nil {
			return nil, err
		}
		if spotifyID == "" {
			e.logger.Debug("no Spotify match found for album", "name", album.Name)
			return nil, nil
		}
	}

	e.logger.Debug("enriching album from Spotify", "name", album.Name, "spotify_id", spotifyID)

	data, err := e.doRequest(ctx, fmt.Sprintf("albums/%s", spotifyID))
	if err != nil {
		return nil, err
	}

	var sp spotifyAlbum
	if err := json.Unmarshal(data, &sp); err != nil {
		return nil, fmt.Errorf("failed to parse album response: %w", err)
	}

	// Parse year from release date
	year := 0
	if len(sp.ReleaseDate) >= 4 {
		if _, err := fmt.Sscanf(sp.ReleaseDate, "%d", &year); err != nil {
			e.logger.Debug("failed to parse year from release date", "date", sp.ReleaseDate, "error", err)
		}
	}

	return &enrichers.AlbumData{
		SpotifyID:   spotifyID,
		ReleaseDate: sp.ReleaseDate,
		Year:        year,
		Tags:        sp.Genres,
		AlbumType:   sp.AlbumType,
		Label:       sp.Label,
		TotalTracks: sp.TotalTracks,
		Popularity:  sp.Popularity,
	}, nil
}

// GetAlbumImages returns album artwork from Spotify.
func (e *Enricher) GetAlbumImages(ctx context.Context, album *ent.Album) ([]enrichers.ImageData, error) {
	// Album has string fields (not *string) - check for empty string
	spotifyID := album.SpotifyID
	if spotifyID == "" {
		return nil, nil
	}

	data, err := e.doRequest(ctx, fmt.Sprintf("albums/%s", spotifyID))
	if err != nil {
		return nil, err
	}

	var sp spotifyAlbum
	if err := json.Unmarshal(data, &sp); err != nil {
		return nil, err
	}

	var images []enrichers.ImageData
	for i, img := range sp.Images {
		localPath := fmt.Sprintf("data/images/albums/%d_spotify_%d.png", album.ID, i)
		_, err := enrichers.DownloadAndSaveImage(img.URL, localPath, e.logger)
		if err != nil {
			e.logger.Warn("failed to download spotify image", "url", img.URL, "error", err)
			continue
		}
		images = append(images, enrichers.ImageData{
			URL:       img.URL,
			LocalPath: localPath,
			Type:      "cover_front",
			Source:    "spotify",
			Width:     img.Width,
			Height:    img.Height,
			IsPrimary: i == 0,
		})
	}

	return images, nil
}

// EnrichTrack fetches detailed track data including audio features from Spotify.
func (e *Enricher) EnrichTrack(ctx context.Context, track *ent.Track) (*enrichers.TrackData, error) {
	// Track has *string fields (Nillable) - check for nil or empty
	spotifyID := ""
	if track.SpotifyID != nil && *track.SpotifyID != "" {
		spotifyID = *track.SpotifyID
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
		spotifyID, _, err = e.MatchTrack(ctx, track.Name, artistName, albumName)
		if err != nil {
			return nil, err
		}
		if spotifyID == "" {
			e.logger.Debug("no Spotify match found for track", "name", track.Name)
			return nil, nil
		}
	}

	e.logger.Debug("enriching track from Spotify", "name", track.Name, "spotify_id", spotifyID)

	// Fetch track details
	trackData, err := e.doRequest(ctx, fmt.Sprintf("tracks/%s", spotifyID))
	if err != nil {
		return nil, err
	}

	var sp spotifyTrack
	if unmarshalErr := json.Unmarshal(trackData, &sp); unmarshalErr != nil {
		return nil, fmt.Errorf("failed to parse track response: %w", unmarshalErr)
	}

	// Fetch audio features
	// Note: Spotify deprecated the Audio Features API for standard apps in late 2024.
	// Apps now need "Extended Quota Mode" to access this endpoint.
	// A 403 error is expected for apps without extended access.
	featuresData, err := e.doRequest(ctx, fmt.Sprintf("audio-features/%s", spotifyID))
	if err != nil {
		// Only log at debug level since 403s are expected for most apps now
		e.logger.Debug("audio features unavailable (likely requires Extended Quota Mode)", "track", track.Name)
		// Continue without audio features - we still have other useful track data
	}

	var features spotifyAudioFeatures
	if featuresData != nil {
		if err := json.Unmarshal(featuresData, &features); err != nil {
			e.logger.Warn("failed to parse audio features", "error", err)
		}
	}

	popularity := sp.Popularity
	bpm := features.Tempo
	energy := features.Energy
	danceability := features.Danceability
	valence := features.Valence
	acousticness := features.Acousticness
	instrumentalness := features.Instrumentalness

	result := &enrichers.TrackData{
		SpotifyID:   spotifyID,
		ISRC:        sp.ExternalIDs.ISRC,
		DurationMs:  sp.DurationMs,
		TrackNumber: sp.TrackNumber,
		DiscNumber:  sp.DiscNumber,
		Popularity:  &popularity,
		SpotifyURL:  sp.ExternalURLs.Spotify,
	}

	// Only set audio features if we got them
	if features.ID != "" {
		result.BPM = &bpm
		result.MusicalKey = keyToString(features.Key, features.Mode)
		result.Energy = &energy
		result.Danceability = &danceability
		result.Valence = &valence
		result.Acousticness = &acousticness
		result.Instrumentalness = &instrumentalness
	}

	return result, nil
}
