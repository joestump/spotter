// Governing: SPEC music-provider-integration, SPEC listen-playlist-sync
package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/providers"
	"spotter/internal/resilience"

	"golang.org/x/oauth2"
	spotifyOAuth "golang.org/x/oauth2/spotify"
)

// Spotify OAuth scopes needed for our functionality
var scopes = []string{
	"user-read-recently-played",
	"user-read-private",
	"playlist-read-private",
	"playlist-modify-public",
	"playlist-modify-private",
}

type Provider struct {
	logger *slog.Logger
	config *config.Config
	user   *ent.User
	auth   *ent.SpotifyAuth
	oauth  *oauth2.Config
}

// Governing: SPEC music-provider-integration REQ-PROV-030 (Spotify: HistoryFetcher + PlaylistManager + Authenticator)
var _ providers.HistoryFetcher = (*Provider)(nil)
var _ providers.PlaylistManager = (*Provider)(nil)
var _ providers.Authenticator = (*Provider)(nil)

// Governing: ADR-0016 (pluggable provider factory), SPEC music-provider-integration REQ-PROV-011 (nil,nil if unconfigured),
// REQ-PROV-012 (credentials from user.Edges.SpotifyAuth)
// New returns a factory that creates Spotify providers for a given user.
func New(logger *slog.Logger, cfg *config.Config) providers.Factory {
	return func(ctx context.Context, user *ent.User) (providers.Provider, error) {
		// Check if the user has Spotify authentication data.
		// We expect the caller to have loaded the edges (e.g. WithSpotifyAuth()).
		if user.Edges.SpotifyAuth == nil {
			return nil, nil
		}

		return &Provider{
			logger: logger,
			config: cfg,
			user:   user,
			auth:   user.Edges.SpotifyAuth,
			oauth:  newOAuthConfig(cfg),
		}, nil
	}
}

// NewAuthenticator returns an authenticator factory for Spotify.
// This is used for the OAuth flow before a user is connected.
func NewAuthenticator(logger *slog.Logger, cfg *config.Config) providers.AuthenticatorFactory {
	return func() providers.Authenticator {
		return &Provider{
			logger: logger,
			config: cfg,
			oauth:  newOAuthConfig(cfg),
		}
	}
}

// Governing: SPEC music-provider-integration REQ-PROV-031 (OAuth2 via golang.org/x/oauth2, configurable redirect URI)
func newOAuthConfig(cfg *config.Config) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.Spotify.ClientID,
		ClientSecret: cfg.Spotify.ClientSecret,
		RedirectURL:  cfg.Spotify.RedirectURL,
		Scopes:       scopes,
		Endpoint:     spotifyOAuth.Endpoint,
	}
}

func (p *Provider) Type() providers.Type {
	return providers.TypeSpotify
}

// SupportsAuth returns true since Spotify supports OAuth authentication from preferences.
func (p *Provider) SupportsAuth() bool {
	return true
}

// GetAuthURL returns the Spotify OAuth authorization URL.
func (p *Provider) GetAuthURL(state string) string {
	return p.oauth.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("show_dialog", "true"))
}

// ExchangeCode exchanges the authorization code for access and refresh tokens.
func (p *Provider) ExchangeCode(ctx context.Context, code string) (*providers.AuthResult, error) {
	token, err := p.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}

	// Fetch user profile to get display name
	userInfo, err := p.fetchUserProfile(ctx, token.AccessToken)
	if err != nil {
		p.logger.Warn("failed to fetch user profile, continuing without display name", "error", err)
		userInfo = &spotifyUser{}
	}

	return &providers.AuthResult{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		Expiry:       token.Expiry,
		DisplayName:  userInfo.DisplayName,
		UserID:       userInfo.ID,
	}, nil
}

// RefreshToken refreshes an expired access token.
func (p *Provider) RefreshToken(ctx context.Context, refreshToken string) (*providers.AuthResult, error) {
	token := &oauth2.Token{
		RefreshToken: refreshToken,
	}

	tokenSource := p.oauth.TokenSource(ctx, token)
	newToken, err := tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}

	// Fetch user profile to update display name if needed
	userInfo, err := p.fetchUserProfile(ctx, newToken.AccessToken)
	if err != nil {
		p.logger.Warn("failed to fetch user profile during refresh", "error", err)
		userInfo = &spotifyUser{}
	}

	return &providers.AuthResult{
		AccessToken:  newToken.AccessToken,
		RefreshToken: newToken.RefreshToken,
		Expiry:       newToken.Expiry,
		DisplayName:  userInfo.DisplayName,
		UserID:       userInfo.ID,
	}, nil
}

// Disconnect performs cleanup when disconnecting from Spotify.
// Spotify doesn't support token revocation via API, so we just return nil.
func (p *Provider) Disconnect(ctx context.Context) error {
	p.logger.Info("disconnecting from Spotify", "user", p.user.Username)
	// Spotify doesn't have a token revocation endpoint
	// The tokens will be deleted from the database by the handler
	return nil
}

// Governing: SPEC music-provider-integration REQ-PROV-013 (transparent token refresh), REQ-PROV-032 (auto refresh before API calls)
// getValidToken returns a valid access token, refreshing if necessary.
func (p *Provider) getValidToken(ctx context.Context) (string, error) {
	if p.auth == nil {
		return "", fmt.Errorf("no spotify auth configured")
	}

	// Check if token is still valid (with 5 minute buffer)
	if time.Now().Add(5 * time.Minute).Before(p.auth.Expiry) {
		return p.auth.AccessToken, nil
	}

	// Token expired or about to expire, refresh it
	p.logger.Debug("refreshing expired Spotify token")
	result, err := p.RefreshToken(ctx, p.auth.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("failed to refresh token: %w", err)
	}

	// Update the auth struct with new token (caller should persist this)
	p.auth.AccessToken = result.AccessToken
	p.auth.Expiry = result.Expiry
	if result.RefreshToken != "" {
		p.auth.RefreshToken = result.RefreshToken
	}

	return result.AccessToken, nil
}

type spotifyUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

func (p *Provider) fetchUserProfile(ctx context.Context, accessToken string) (*spotifyUser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.spotify.com/v1/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			p.logger.Warn("failed to close response body", "error", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		// Governing: ADR-0020, SPEC error-handling REQ-ERR-002/REQ-ERR-003
		return nil, resilience.NewHTTPStatusError(resp.StatusCode, fmt.Errorf("spotify API returned status %d", resp.StatusCode))
	}

	var user spotifyUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}

	return &user, nil
}

type recentlyPlayedResponse struct {
	Items []struct {
		Track struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Duration int    `json:"duration_ms"`
			Album    struct {
				Name string `json:"name"`
			} `json:"album"`
			Artists []struct {
				Name string `json:"name"`
			} `json:"artists"`
			ExternalURLs struct {
				Spotify string `json:"spotify"`
			} `json:"external_urls"`
		} `json:"track"`
		PlayedAt time.Time `json:"played_at"`
	} `json:"items"`
}

// Governing: SPEC music-provider-integration REQ-PROV-033 (paginated recently-played with since filter)
// GetRecentListens fetches recently played tracks from Spotify.
//
// IMPORTANT LIMITATION: Spotify's API only returns the last 50 recently played tracks.
// There is NO way to paginate further back in history - this is a hard limitation of
// Spotify's API. For full listening history, users must request a data export from
// Spotify's privacy settings at https://www.spotify.com/account/privacy/
//
// To capture the most history, sync should run frequently (at least every few hours)
// to catch new plays before they fall out of the 50-track window.
func (p *Provider) GetRecentListens(ctx context.Context, since time.Time, callback func([]providers.Track) error) error {
	p.logger.Info("fetching recent listens from spotify", "username", p.user.Username, "since", since)

	accessToken, err := p.getValidToken(ctx)
	if err != nil {
		return err
	}

	// Spotify API uses milliseconds timestamp for the 'after' parameter
	// Note: Spotify only returns up to 50 tracks regardless of the 'after' value
	// There is no pagination cursor to get older history - this is a Spotify API limitation
	afterMs := since.UnixMilli()
	if afterMs < 0 {
		afterMs = 0
	}
	url := fmt.Sprintf("https://api.spotify.com/v1/me/player/recently-played?limit=50&after=%d", afterMs)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			p.logger.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		// Governing: ADR-0020, SPEC error-handling REQ-ERR-002/REQ-ERR-003
		return resilience.NewHTTPStatusError(resp.StatusCode, fmt.Errorf("spotify API returned status %d", resp.StatusCode))
	}

	var result recentlyPlayedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	tracks := make([]providers.Track, 0, len(result.Items))
	for _, item := range result.Items {
		artist := ""
		if len(item.Track.Artists) > 0 {
			artist = item.Track.Artists[0].Name
		}

		tracks = append(tracks, providers.Track{
			ID:         item.Track.ID,
			Name:       item.Track.Name,
			Artist:     artist,
			Album:      item.Track.Album.Name,
			DurationMs: item.Track.Duration,
			PlayedAt:   item.PlayedAt,
			URL:        item.Track.ExternalURLs.Spotify,
		})
	}

	p.logger.Info("fetched recent listens from spotify",
		"count", len(tracks),
		"note", "Spotify API limited to last 50 tracks - older history unavailable")

	if len(tracks) > 0 {
		return callback(tracks)
	}
	return nil
}

type playlistsResponse struct {
	Items []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Images      []struct {
			URL    string `json:"url"`
			Height int    `json:"height"`
			Width  int    `json:"width"`
		} `json:"images"`
		ExternalURLs struct {
			Spotify string `json:"spotify"`
		} `json:"external_urls"`
		Tracks struct {
			Total int `json:"total"`
		} `json:"tracks"`
	} `json:"items"`
	Next  string `json:"next"`
	Total int    `json:"total"`
}

type playlistTracksResponse struct {
	Items []struct {
		Track struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			DurationMs int    `json:"duration_ms"`
			Artists    []struct {
				Name string `json:"name"`
			} `json:"artists"`
			Album struct {
				Name string `json:"name"`
			} `json:"album"`
			ExternalURLs struct {
				Spotify string `json:"spotify"`
			} `json:"external_urls"`
		} `json:"track"`
	} `json:"items"`
	Next  string `json:"next"`
	Total int    `json:"total"`
}

func (p *Provider) GetPlaylists(ctx context.Context) ([]providers.Playlist, error) {
	p.logger.Info("fetching playlists from spotify", "username", p.user.Username)

	accessToken, err := p.getValidToken(ctx)
	if err != nil {
		return nil, err
	}

	var allPlaylists []providers.Playlist
	nextURL := "https://api.spotify.com/v1/me/playlists?limit=50"

	// Paginate through all playlists
	for nextURL != "" {
		req, err := http.NewRequestWithContext(ctx, "GET", nextURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			if err := resp.Body.Close(); err != nil {
				p.logger.Warn("failed to close response body", "error", err)
			}
			// Governing: ADR-0020, SPEC error-handling REQ-ERR-002/REQ-ERR-003
			return nil, resilience.NewHTTPStatusError(resp.StatusCode, fmt.Errorf("spotify API returned status %d", resp.StatusCode))
		}

		var result playlistsResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			if err := resp.Body.Close(); err != nil {
				p.logger.Warn("failed to close response body", "error", err)
			}
			return nil, err
		}
		_ = resp.Body.Close()

		for _, item := range result.Items {
			// Get the best image (first one is usually the largest)
			imageURL := ""
			if len(item.Images) > 0 {
				imageURL = item.Images[0].URL
			}

			// Fetch tracks, unique artists and albums for this playlist
			tracks, uniqueArtists, uniqueAlbums := p.getPlaylistTracks(ctx, accessToken, item.ID)

			allPlaylists = append(allPlaylists, providers.Playlist{
				ID:            item.ID,
				Name:          item.Name,
				Description:   item.Description,
				ImageURL:      imageURL,
				ExternalURL:   item.ExternalURLs.Spotify,
				TrackCount:    item.Tracks.Total,
				UniqueArtists: uniqueArtists,
				UniqueAlbums:  uniqueAlbums,
				Tracks:        tracks,
			})
		}

		nextURL = result.Next
	}

	p.logger.Info("fetched playlists from spotify", "count", len(allPlaylists))
	return allPlaylists, nil
}

// getPlaylistTracks fetches all tracks in a playlist along with unique artist/album counts
func (p *Provider) getPlaylistTracks(ctx context.Context, accessToken, playlistID string) (tracks []providers.Track, uniqueArtists, uniqueAlbums int) {
	artists := make(map[string]struct{})
	albums := make(map[string]struct{})

	nextURL := fmt.Sprintf("https://api.spotify.com/v1/playlists/%s/tracks?limit=100&fields=items(track(id,name,duration_ms,artists(name),album(name),external_urls)),next,total", playlistID)

	for nextURL != "" {
		req, err := http.NewRequestWithContext(ctx, "GET", nextURL, nil)
		if err != nil {
			p.logger.Debug("failed to create request for playlist tracks", "error", err)
			break
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			p.logger.Debug("failed to fetch playlist tracks", "error", err)
			break
		}

		if resp.StatusCode != http.StatusOK {
			if err := resp.Body.Close(); err != nil {
				p.logger.Warn("failed to close response body", "error", err)
			}
			p.logger.Debug("spotify API returned non-OK status for playlist tracks", "status", resp.StatusCode)
			break
		}

		var result playlistTracksResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			if err := resp.Body.Close(); err != nil {
				p.logger.Warn("failed to close response body", "error", err)
			}
			p.logger.Debug("failed to decode playlist tracks response", "error", err)
			break
		}
		_ = resp.Body.Close()

		for _, item := range result.Items {
			if item.Track.ID == "" {
				continue // Skip local files or unavailable tracks
			}

			// Get artist name (use first artist)
			artistName := ""
			if len(item.Track.Artists) > 0 {
				artistName = item.Track.Artists[0].Name
			}

			// Track unique artists and albums
			for _, artist := range item.Track.Artists {
				if artist.Name != "" {
					artists[artist.Name] = struct{}{}
				}
			}
			if item.Track.Album.Name != "" {
				albums[item.Track.Album.Name] = struct{}{}
			}

			// Add track to list
			tracks = append(tracks, providers.Track{
				ID:         item.Track.ID,
				Name:       item.Track.Name,
				Artist:     artistName,
				Album:      item.Track.Album.Name,
				DurationMs: item.Track.DurationMs,
				URL:        item.Track.ExternalURLs.Spotify,
			})
		}

		nextURL = result.Next
	}

	return tracks, len(artists), len(albums)
}

func (p *Provider) CreatePlaylist(ctx context.Context, name, description string, tracks []providers.Track) error {
	p.logger.Info("creating playlist on spotify", "username", p.user.Username, "name", name, "track_count", len(tracks))

	if len(tracks) == 0 {
		return fmt.Errorf("cannot create empty playlist")
	}

	accessToken, err := p.getValidToken(ctx)
	if err != nil {
		return err
	}

	// Get user ID first
	userInfo, err := p.fetchUserProfile(ctx, accessToken)
	if err != nil {
		return fmt.Errorf("failed to get user ID: %w", err)
	}

	// TODO: Implement actual playlist creation
	// 1. POST /users/{user_id}/playlists to create playlist
	// 2. POST /playlists/{playlist_id}/tracks to add tracks
	p.logger.Info("would create playlist", "user_id", userInfo.ID, "name", name, "track_count", len(tracks))

	return nil
}
