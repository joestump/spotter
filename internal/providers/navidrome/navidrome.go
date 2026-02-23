// Governing: ADR-0005 (Navidrome primary identity), SPEC music-provider-integration
package navidrome

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/providers"
)

const (
	statusFailed = "failed"
)

type Provider struct {
	logger   *slog.Logger
	config   *config.Config
	user     *ent.User
	auth     *ent.NavidromeAuth
	jwtToken string
}

// Ensure Provider implements interfaces
var _ providers.HistoryFetcher = (*Provider)(nil)
var _ providers.PlaylistManager = (*Provider)(nil)
var _ providers.Authenticator = (*Provider)(nil)
var _ providers.PlaylistSyncer = (*Provider)(nil)

// Governing: ADR-0016 (pluggable provider factory), SPEC listen-playlist-sync REQ-SYNC-002 (factory returns nil if unconfigured)
// New returns a factory that creates Navidrome providers for a given user.
func New(logger *slog.Logger, cfg *config.Config) providers.Factory {
	return func(ctx context.Context, user *ent.User) (providers.Provider, error) {
		// Check if the user has Navidrome authentication data.
		// We expect the caller to have loaded the edges (e.g. WithNavidromeAuth()).
		if user.Edges.NavidromeAuth == nil {
			return nil, nil
		}

		return &Provider{
			logger: logger,
			config: cfg,
			user:   user,
			auth:   user.Edges.NavidromeAuth,
		}, nil
	}
}

func (p *Provider) Type() providers.Type {
	return providers.TypeNavidrome
}

// SupportsAuth returns false for Navidrome.
// Navidrome is the primary authentication mechanism for the application itself,
// not a provider that can be connected/disconnected from preferences.
func (p *Provider) SupportsAuth() bool {
	return false
}

// GetAuthURL is not supported for Navidrome - authentication is handled via app login.
func (p *Provider) GetAuthURL(state string) string {
	p.logger.Warn("GetAuthURL called on Navidrome provider - this should not happen")
	return ""
}

// ExchangeCode is not supported for Navidrome - authentication is handled via app login.
func (p *Provider) ExchangeCode(ctx context.Context, code string) (*providers.AuthResult, error) {
	return nil, fmt.Errorf("Navidrome does not support OAuth authentication from preferences")
}

// RefreshToken is not supported for Navidrome - authentication is handled via app login.
func (p *Provider) RefreshToken(ctx context.Context, refreshToken string) (*providers.AuthResult, error) {
	return nil, fmt.Errorf("Navidrome does not support token refresh from preferences")
}

// Disconnect is not supported for Navidrome - it's the primary app authentication.
func (p *Provider) Disconnect(ctx context.Context) error {
	return fmt.Errorf("Navidrome cannot be disconnected - it is the primary authentication mechanism")
}

// authenticateInternalAPI gets a JWT token from Navidrome's internal API
func (p *Provider) authenticateInternalAPI(ctx context.Context) error {
	if p.jwtToken != "" {
		return nil // Already authenticated
	}

	baseURL := strings.TrimSuffix(p.config.Navidrome.BaseURL, "/")
	loginURL := fmt.Sprintf("%s/auth/login", baseURL)

	loginData := map[string]string{
		"username": p.user.Username,
		"password": p.auth.Password,
	}
	jsonData, err := json.Marshal(loginData)
	if err != nil {
		return fmt.Errorf("failed to marshal login data: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", loginURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute login request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			p.logger.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("navidrome login failed with status: %d", resp.StatusCode)
	}

	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return fmt.Errorf("failed to decode login response: %w", err)
	}

	p.jwtToken = loginResp.Token
	return nil
}

// getRecentlyPlayedFromInternalAPI fetches recently played songs from Navidrome's internal API
func (p *Provider) getRecentlyPlayedFromInternalAPI(ctx context.Context, since time.Time) ([]providers.Track, error) {
	if err := p.authenticateInternalAPI(ctx); err != nil {
		p.logger.Debug("failed to authenticate with internal API, falling back to Subsonic", "error", err)
		return nil, err
	}

	baseURL := strings.TrimSuffix(p.config.Navidrome.BaseURL, "/")

	// Query songs sorted by play_date descending, limited to recently played
	// The internal API uses _sort and _order query params
	params := url.Values{}
	params.Set("_sort", "play_date")
	params.Set("_order", "DESC")
	params.Set("_start", "0")
	params.Set("_end", "200") // Get last 200 played songs
	// Only get songs that have been played
	params.Set("play_count_gt", "0")

	apiURL := fmt.Sprintf("%s/api/song?%s", baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("x-nd-authorization", fmt.Sprintf("Bearer %s", p.jwtToken))

	p.logger.Debug("calling navidrome internal api", "url", apiURL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			p.logger.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode == http.StatusUnauthorized {
		// Token might be expired, clear it and return error to fall back
		p.jwtToken = ""
		return nil, fmt.Errorf("unauthorized - token may be expired")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("navidrome internal API returned status: %d", resp.StatusCode)
	}

	var songs []struct {
		ID        string    `json:"id"`
		Title     string    `json:"title"`
		Artist    string    `json:"artist"`
		Album     string    `json:"album"`
		Duration  float64   `json:"duration"` // Duration in seconds
		PlayDate  time.Time `json:"playDate"`
		PlayCount int       `json:"playCount"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&songs); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var tracks []providers.Track
	for _, song := range songs {
		// Filter by 'since' - only include songs played after the since time
		if song.PlayDate.Before(since) || song.PlayDate.IsZero() {
			continue
		}

		tracks = append(tracks, providers.Track{
			ID:         song.ID,
			Name:       song.Title,
			Artist:     song.Artist,
			Album:      song.Album,
			DurationMs: int(song.Duration * 1000),
			PlayedAt:   song.PlayDate,
			URL:        fmt.Sprintf("%s/app/#/song/%s", baseURL, song.ID),
		})
	}

	p.logger.Debug("fetched tracks from navidrome internal API",
		"total_received", len(songs),
		"filtered_by_since", len(tracks))

	return tracks, nil
}

func (p *Provider) GetRecentListens(ctx context.Context, since time.Time, callback func([]providers.Track) error) error {
	p.logger.Info("fetching recent listens from navidrome", "username", p.user.Username, "since", since)

	// Try the internal API first for better history data
	tracks, err := p.getRecentlyPlayedFromInternalAPI(ctx, since)
	if err == nil && len(tracks) > 0 {
		p.logger.Info("fetched recent listens from navidrome internal API", "count", len(tracks))
		return callback(tracks)
	}

	if err != nil {
		p.logger.Debug("internal API failed, falling back to Subsonic getNowPlaying", "error", err)
	}

	// Fall back to Subsonic getNowPlaying for currently playing tracks
	tracks, err = p.getNowPlayingFromSubsonic(ctx, since)
	if err != nil {
		return err
	}
	if len(tracks) > 0 {
		return callback(tracks)
	}
	return nil
}

// getNowPlayingFromSubsonic uses the Subsonic API to get currently playing tracks
func (p *Provider) getNowPlayingFromSubsonic(ctx context.Context, since time.Time) ([]providers.Track, error) {
	// Generate Auth Parameters
	salt := generateSalt()
	token := generateToken(p.auth.Password, salt)

	params := url.Values{}
	params.Set("u", p.user.Username)
	params.Set("t", token)
	params.Set("s", salt)
	params.Set("v", "1.16.1") // Target Subsonic API version
	params.Set("c", "spotter")
	params.Set("f", "json")

	// Construct URL
	// Note: getNowPlaying returns what is currently playing or recently played by users.
	// We will filter by the current user's username.
	baseURL := strings.TrimSuffix(p.config.Navidrome.BaseURL, "/")
	apiURL := fmt.Sprintf("%s/rest/getNowPlaying.view?%s", baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	p.logger.Debug("calling navidrome subsonic api", "url", apiURL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			p.logger.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("navidrome API returned status: %d", resp.StatusCode)
	}

	var result struct {
		SubsonicResponse struct {
			Status string `json:"status"`
			Error  struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
			NowPlaying struct {
				Entry []struct {
					ID         string `json:"id"`
					Title      string `json:"title"`
					Artist     string `json:"artist"`
					Album      string `json:"album"`
					Duration   int    `json:"duration"`   // Seconds
					MinutesAgo int    `json:"minutesAgo"` // Minutes since played
					Username   string `json:"username"`
				} `json:"entry"`
			} `json:"nowPlaying"`
		} `json:"subsonic-response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if result.SubsonicResponse.Status == statusFailed {
		return nil, fmt.Errorf("navidrome API error: %s", result.SubsonicResponse.Error.Message)
	}

	var tracks []providers.Track
	ignoredCount := 0
	for _, entry := range result.SubsonicResponse.NowPlaying.Entry {
		// Filter by username to ensure we only get this user's listens
		if entry.Username != p.user.Username {
			continue
		}

		// Calculate PlayedAt
		// minutesAgo is relative to now
		playedAt := time.Now().Add(-time.Duration(entry.MinutesAgo) * time.Minute)

		// Filter by 'since'
		if playedAt.Before(since) {
			ignoredCount++
			continue
		}

		tracks = append(tracks, providers.Track{
			ID:         entry.ID,
			Name:       entry.Title,
			Artist:     entry.Artist,
			Album:      entry.Album,
			DurationMs: entry.Duration * 1000,
			PlayedAt:   playedAt,
			// Constructing a web player link.
			// Navidrome web UI typical route: /app/#/song/{id}
			URL: fmt.Sprintf("%s/app/#/song/%s", baseURL, entry.ID),
		})
	}

	p.logger.Debug("fetched tracks from navidrome subsonic api",
		"total_received", len(result.SubsonicResponse.NowPlaying.Entry),
		"ignored_too_old", ignoredCount,
		"found_new", len(tracks))

	if len(tracks) == 0 {
		p.logger.Info("no new tracks found in navidrome recent listens")
	}

	return tracks, nil
}

func (p *Provider) GetPlaylists(ctx context.Context) ([]providers.Playlist, error) {
	p.logger.Info("fetching playlists from navidrome", "username", p.user.Username)

	// Generate Auth Parameters
	salt := generateSalt()
	token := generateToken(p.auth.Password, salt)

	params := url.Values{}
	params.Set("u", p.user.Username)
	params.Set("t", token)
	params.Set("s", salt)
	params.Set("v", "1.16.1")
	params.Set("c", "spotter")
	params.Set("f", "json")

	baseURL := strings.TrimSuffix(p.config.Navidrome.BaseURL, "/")
	apiURL := fmt.Sprintf("%s/rest/getPlaylists.view?%s", baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	p.logger.Debug("calling navidrome api", "url", apiURL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			p.logger.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("navidrome API returned status: %d", resp.StatusCode)
	}

	var result struct {
		SubsonicResponse struct {
			Status string `json:"status"`
			Error  struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
			Playlists struct {
				Playlist []struct {
					ID        string `json:"id"`
					Name      string `json:"name"`
					Comment   string `json:"comment"`
					CoverArt  string `json:"coverArt"`
					SongCount int    `json:"songCount"`
					Duration  int    `json:"duration"`
					Public    bool   `json:"public"`
					Owner     string `json:"owner"`
					Created   string `json:"created"`
				} `json:"playlist"`
			} `json:"playlists"`
		} `json:"subsonic-response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if result.SubsonicResponse.Status == statusFailed {
		return nil, fmt.Errorf("navidrome API error: %s", result.SubsonicResponse.Error.Message)
	}

	// Build auth params for cover art URLs
	coverSalt := generateSalt()
	coverToken := generateToken(p.auth.Password, coverSalt)
	coverParams := url.Values{}
	coverParams.Set("u", p.user.Username)
	coverParams.Set("t", coverToken)
	coverParams.Set("s", coverSalt)
	coverParams.Set("v", "1.16.1")
	coverParams.Set("c", "spotter")

	var playlists []providers.Playlist
	for _, pl := range result.SubsonicResponse.Playlists.Playlist {
		// Build cover art URL if available
		imageURL := ""
		if pl.CoverArt != "" {
			imageURL = fmt.Sprintf("%s/rest/getCoverArt.view?id=%s&%s", baseURL, pl.CoverArt, coverParams.Encode())
		}

		// Build external URL to Navidrome web UI
		externalURL := fmt.Sprintf("%s/app/#/playlist/%s", baseURL, pl.ID)

		// Fetch tracks, unique artists and albums for this playlist
		tracks, uniqueArtists, uniqueAlbums := p.getPlaylistTracks(ctx, pl.ID)

		playlists = append(playlists, providers.Playlist{
			ID:            pl.ID,
			Name:          pl.Name,
			Description:   pl.Comment,
			ImageURL:      imageURL,
			ExternalURL:   externalURL,
			TrackCount:    pl.SongCount,
			UniqueArtists: uniqueArtists,
			UniqueAlbums:  uniqueAlbums,
			Tracks:        tracks,
		})
	}

	p.logger.Debug("fetched playlists from navidrome", "count", len(playlists))

	return playlists, nil
}

// getPlaylistTracks fetches all tracks in a playlist along with unique artist/album counts
func (p *Provider) getPlaylistTracks(ctx context.Context, playlistID string) (tracks []providers.Track, uniqueArtists, uniqueAlbums int) {
	// Generate Auth Parameters
	salt := generateSalt()
	token := generateToken(p.auth.Password, salt)

	params := url.Values{}
	params.Set("u", p.user.Username)
	params.Set("t", token)
	params.Set("s", salt)
	params.Set("v", "1.16.1")
	params.Set("c", "spotter")
	params.Set("f", "json")
	params.Set("id", playlistID)

	baseURL := strings.TrimSuffix(p.config.Navidrome.BaseURL, "/")
	apiURL := fmt.Sprintf("%s/rest/getPlaylist.view?%s", baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		p.logger.Debug("failed to create request for playlist details", "error", err)
		return nil, 0, 0
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.logger.Debug("failed to fetch playlist details", "error", err)
		return nil, 0, 0
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			p.logger.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		p.logger.Debug("navidrome API returned non-OK status for playlist details", "status", resp.StatusCode)
		return nil, 0, 0
	}

	var result struct {
		SubsonicResponse struct {
			Status   string `json:"status"`
			Playlist struct {
				Entry []struct {
					ID       string `json:"id"`
					Title    string `json:"title"`
					Artist   string `json:"artist"`
					Album    string `json:"album"`
					Duration int    `json:"duration"`
				} `json:"entry"`
			} `json:"playlist"`
		} `json:"subsonic-response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		p.logger.Debug("failed to decode playlist details response", "error", err)
		return nil, 0, 0
	}

	if result.SubsonicResponse.Status != "ok" {
		return nil, 0, 0
	}

	artists := make(map[string]struct{})
	albums := make(map[string]struct{})

	for _, entry := range result.SubsonicResponse.Playlist.Entry {
		// Track unique artists and albums
		if entry.Artist != "" {
			artists[entry.Artist] = struct{}{}
		}
		if entry.Album != "" {
			albums[entry.Album] = struct{}{}
		}

		// Build track URL
		trackURL := fmt.Sprintf("%s/app/#/album/%s", baseURL, entry.ID)

		// Add track to list (duration from Navidrome is in seconds, convert to ms)
		tracks = append(tracks, providers.Track{
			ID:         entry.ID,
			Name:       entry.Title,
			Artist:     entry.Artist,
			Album:      entry.Album,
			DurationMs: entry.Duration * 1000,
			URL:        trackURL,
		})
	}

	return tracks, len(artists), len(albums)
}

func (p *Provider) CreatePlaylist(ctx context.Context, name, description string, tracks []providers.Track) error {
	p.logger.Info("creating playlist on navidrome", "username", p.user.Username, "name", name, "track_count", len(tracks))

	// Use SyncPlaylist for consistency
	_, err := p.SyncPlaylist(ctx, providers.SyncPlaylistRequest{
		Name:        name,
		Description: description,
		Tracks:      tracks,
	})
	return err
}

// SyncPlaylist creates or updates a playlist on Navidrome from external data.
// Returns the remote playlist ID created/updated on Navidrome.
func (p *Provider) SyncPlaylist(ctx context.Context, playlist providers.SyncPlaylistRequest) (string, error) {
	p.logger.Info("syncing playlist to navidrome",
		"username", p.user.Username,
		"name", playlist.Name,
		"track_count", len(playlist.Tracks))

	// Generate Auth Parameters
	salt := generateSalt()
	token := generateToken(p.auth.Password, salt)

	params := url.Values{}
	params.Set("u", p.user.Username)
	params.Set("t", token)
	params.Set("s", salt)
	params.Set("v", "1.16.1")
	params.Set("c", "spotter")
	params.Set("f", "json")
	params.Set("name", playlist.Name)

	// Add track IDs (songId parameter can be repeated)
	trackIDs := make([]string, 0, len(playlist.Tracks))
	for _, track := range playlist.Tracks {
		if track.ID != "" {
			params.Add("songId", track.ID)
			trackIDs = append(trackIDs, track.ID)
		}
	}

	p.logger.Debug("creating playlist with track IDs",
		"playlist_name", playlist.Name,
		"track_id_count", len(trackIDs))

	baseURL := strings.TrimSuffix(p.config.Navidrome.BaseURL, "/")
	apiURL := fmt.Sprintf("%s/rest/createPlaylist.view?%s", baseURL, params.Encode())

	p.logger.Debug("calling navidrome API",
		"endpoint", "createPlaylist.view",
		"base_url", baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		p.logger.Error("failed to create HTTP request",
			"error", err)
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.logger.Error("failed to execute HTTP request",
			"error", err)
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			p.logger.Warn("failed to close response body", "error", err)
		}
	}()

	p.logger.Debug("received navidrome API response",
		"status_code", resp.StatusCode,
		"status", resp.Status)

	if resp.StatusCode != http.StatusOK {
		p.logger.Error("navidrome API returned non-OK status",
			"status_code", resp.StatusCode,
			"status", resp.Status)
		return "", fmt.Errorf("navidrome API returned status: %d", resp.StatusCode)
	}

	var result struct {
		SubsonicResponse struct {
			Status string `json:"status"`
			Error  struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
			Playlist struct {
				ID string `json:"id"`
			} `json:"playlist"`
		} `json:"subsonic-response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if result.SubsonicResponse.Status == statusFailed {
		p.logger.Error("navidrome API returned error",
			"error_code", result.SubsonicResponse.Error.Code,
			"error_message", result.SubsonicResponse.Error.Message)
		return "", fmt.Errorf("navidrome API error: %s", result.SubsonicResponse.Error.Message)
	}

	playlistID := result.SubsonicResponse.Playlist.ID
	p.logger.Info("created playlist on navidrome",
		"playlist_id", playlistID,
		"name", playlist.Name,
		"track_count", len(trackIDs))

	// Update playlist with description if provided
	if playlist.Description != "" {
		if err := p.updatePlaylistMetadata(ctx, playlistID, playlist.Name, playlist.Description); err != nil {
			p.logger.Warn("failed to update playlist description", "error", err)
		}
	}

	return playlistID, nil
}

// updatePlaylistMetadata updates the name and comment of an existing playlist.
func (p *Provider) updatePlaylistMetadata(ctx context.Context, playlistID, name, comment string) error {
	salt := generateSalt()
	token := generateToken(p.auth.Password, salt)

	params := url.Values{}
	params.Set("u", p.user.Username)
	params.Set("t", token)
	params.Set("s", salt)
	params.Set("v", "1.16.1")
	params.Set("c", "spotter")
	params.Set("f", "json")
	params.Set("playlistId", playlistID)
	params.Set("name", name)
	params.Set("comment", comment)

	baseURL := strings.TrimSuffix(p.config.Navidrome.BaseURL, "/")
	apiURL := fmt.Sprintf("%s/rest/updatePlaylist.view?%s", baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			p.logger.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("navidrome API returned status: %d", resp.StatusCode)
	}

	var result struct {
		SubsonicResponse struct {
			Status string `json:"status"`
			Error  struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"subsonic-response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if result.SubsonicResponse.Status == statusFailed {
		return fmt.Errorf("navidrome API error: %s", result.SubsonicResponse.Error.Message)
	}

	return nil
}

// DeletePlaylist removes a playlist from Navidrome.
func (p *Provider) DeletePlaylist(ctx context.Context, remotePlaylistID string) error {
	p.logger.Info("deleting playlist from navidrome",
		"username", p.user.Username,
		"playlist_id", remotePlaylistID)

	salt := generateSalt()
	token := generateToken(p.auth.Password, salt)

	params := url.Values{}
	params.Set("u", p.user.Username)
	params.Set("t", token)
	params.Set("s", salt)
	params.Set("v", "1.16.1")
	params.Set("c", "spotter")
	params.Set("f", "json")
	params.Set("id", remotePlaylistID)

	baseURL := strings.TrimSuffix(p.config.Navidrome.BaseURL, "/")
	apiURL := fmt.Sprintf("%s/rest/deletePlaylist.view?%s", baseURL, params.Encode())

	p.logger.Debug("calling navidrome API",
		"endpoint", "deletePlaylist.view",
		"playlist_id", remotePlaylistID)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		p.logger.Error("failed to create HTTP request",
			"error", err)
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.logger.Error("failed to execute HTTP request",
			"error", err)
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			p.logger.Warn("failed to close response body", "error", err)
		}
	}()

	p.logger.Debug("received navidrome API response",
		"status_code", resp.StatusCode,
		"status", resp.Status)

	if resp.StatusCode != http.StatusOK {
		p.logger.Error("navidrome API returned non-OK status",
			"status_code", resp.StatusCode)
		return fmt.Errorf("navidrome API returned status: %d", resp.StatusCode)
	}

	var result struct {
		SubsonicResponse struct {
			Status string `json:"status"`
			Error  struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"subsonic-response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if result.SubsonicResponse.Status == statusFailed {
		p.logger.Error("navidrome API returned error",
			"error_code", result.SubsonicResponse.Error.Code,
			"error_message", result.SubsonicResponse.Error.Message)
		return fmt.Errorf("navidrome API error: %s", result.SubsonicResponse.Error.Message)
	}

	p.logger.Info("deleted playlist from navidrome successfully",
		"playlist_id", remotePlaylistID)
	return nil
}

// UpdatePlaylistTracks replaces all tracks in a playlist.
func (p *Provider) UpdatePlaylistTracks(ctx context.Context, remotePlaylistID string, tracks []providers.Track) error {
	p.logger.Info("updating playlist tracks on navidrome",
		"username", p.user.Username,
		"playlist_id", remotePlaylistID,
		"new_track_count", len(tracks))

	// The Subsonic API updatePlaylist can add/remove tracks
	// To replace all tracks, we need to:
	// 1. Get current tracks
	// 2. Remove all existing tracks
	// 3. Add all new tracks

	// First, get current tracks to know their indices
	currentTracks, _, _ := p.getPlaylistTracks(ctx, remotePlaylistID)

	p.logger.Debug("fetched current playlist tracks",
		"playlist_id", remotePlaylistID,
		"current_track_count", len(currentTracks))

	salt := generateSalt()
	token := generateToken(p.auth.Password, salt)

	params := url.Values{}
	params.Set("u", p.user.Username)
	params.Set("t", token)
	params.Set("s", salt)
	params.Set("v", "1.16.1")
	params.Set("c", "spotter")
	params.Set("f", "json")
	params.Set("playlistId", remotePlaylistID)

	// Remove existing tracks by index (0-based, remove from end to start to avoid index shifting)
	for i := len(currentTracks) - 1; i >= 0; i-- {
		params.Add("songIndexToRemove", fmt.Sprintf("%d", i))
	}

	// Add new tracks
	newTrackIDs := make([]string, 0, len(tracks))
	for _, track := range tracks {
		if track.ID != "" {
			params.Add("songIdToAdd", track.ID)
			newTrackIDs = append(newTrackIDs, track.ID)
		}
	}

	p.logger.Debug("prepared playlist update",
		"playlist_id", remotePlaylistID,
		"tracks_to_remove", len(currentTracks),
		"tracks_to_add", len(newTrackIDs))

	baseURL := strings.TrimSuffix(p.config.Navidrome.BaseURL, "/")
	apiURL := fmt.Sprintf("%s/rest/updatePlaylist.view?%s", baseURL, params.Encode())

	p.logger.Debug("calling navidrome API",
		"endpoint", "updatePlaylist.view",
		"playlist_id", remotePlaylistID)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		p.logger.Error("failed to create HTTP request",
			"error", err)
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.logger.Error("failed to execute HTTP request",
			"error", err)
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			p.logger.Warn("failed to close response body", "error", err)
		}
	}()

	p.logger.Debug("received navidrome API response",
		"status_code", resp.StatusCode,
		"status", resp.Status)

	if resp.StatusCode != http.StatusOK {
		p.logger.Error("navidrome API returned non-OK status",
			"status_code", resp.StatusCode)
		return fmt.Errorf("navidrome API returned status: %d", resp.StatusCode)
	}

	var result struct {
		SubsonicResponse struct {
			Status string `json:"status"`
			Error  struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"subsonic-response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if result.SubsonicResponse.Status == statusFailed {
		p.logger.Error("navidrome API returned error",
			"error_code", result.SubsonicResponse.Error.Code,
			"error_message", result.SubsonicResponse.Error.Message)
		return fmt.Errorf("navidrome API error: %s", result.SubsonicResponse.Error.Message)
	}

	p.logger.Info("updated playlist tracks on navidrome",
		"playlist_id", remotePlaylistID,
		"old_track_count", len(currentTracks),
		"new_track_count", len(newTrackIDs))
	return nil
}

func generateSalt() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Fallback if random fails, though unlikely
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func generateToken(password, salt string) string {
	hash := md5.New()
	hash.Write([]byte(password + salt))
	return hex.EncodeToString(hash.Sum(nil))
}
