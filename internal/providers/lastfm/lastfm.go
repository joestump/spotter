// Governing: SPEC music-provider-integration, SPEC listen-playlist-sync
package lastfm

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/providers"
)

const (
	defaultAPIBaseURL = "http://ws.audioscrobbler.com/2.0/"
)

type Provider struct {
	logger     *slog.Logger
	config     *config.Config
	user       *ent.User
	auth       *ent.LastFMAuth
	baseURL    string
	httpClient *http.Client
}

// Ensure Provider implements interfaces
var _ providers.HistoryFetcher = (*Provider)(nil)
var _ providers.Authenticator = (*Provider)(nil)

// Governing: ADR-0016 (pluggable provider factory), SPEC listen-playlist-sync REQ-SYNC-002 (factory returns nil if unconfigured)
// New returns a factory that creates Last.fm providers for a given user.
func New(logger *slog.Logger, cfg *config.Config) providers.Factory {
	return func(ctx context.Context, user *ent.User) (providers.Provider, error) {
		// Check if the user has Last.fm authentication data.
		if user.Edges.LastfmAuth == nil {
			return nil, nil
		}

		l := logger
		if l == nil {
			l = slog.New(slog.NewTextHandler(io.Discard, nil))
		}
		return &Provider{
			logger:     l,
			config:     cfg,
			user:       user,
			auth:       user.Edges.LastfmAuth,
			baseURL:    defaultAPIBaseURL,
			httpClient: http.DefaultClient,
		}, nil
	}
}

// NewAuthenticator returns an authenticator factory for Last.fm.
// This is used for the OAuth flow before a user is connected.
func NewAuthenticator(logger *slog.Logger, cfg *config.Config) providers.AuthenticatorFactory {
	return func() providers.Authenticator {
		l := logger
		if l == nil {
			l = slog.New(slog.NewTextHandler(io.Discard, nil))
		}
		return &Provider{
			logger:     l,
			config:     cfg,
			baseURL:    defaultAPIBaseURL,
			httpClient: http.DefaultClient,
		}
	}
}

// WithBaseURL sets a custom base URL (used for testing).
func (p *Provider) WithBaseURL(url string) *Provider {
	p.baseURL = url
	return p
}

// WithHTTPClient sets a custom HTTP client (used for testing).
func (p *Provider) WithHTTPClient(client *http.Client) *Provider {
	p.httpClient = client
	return p
}

func (p *Provider) Type() providers.Type {
	return providers.TypeLastFM
}

// SupportsAuth returns true since Last.fm supports authentication from preferences.
func (p *Provider) SupportsAuth() bool {
	return true
}

// GetAuthURL returns the Last.fm authentication URL.
func (p *Provider) GetAuthURL(state string) string {
	if p.config.LastFM.APIKey == "" {
		p.logger.Warn("Last.fm API key not configured")
		return ""
	}

	// Last.fm doesn't support state parameter natively in the same way OAuth2 does,
	// but we can't easily pass it through. The controller needs to handle session management.
	// We just return the standard auth URL.
	return fmt.Sprintf("http://www.last.fm/api/auth/?api_key=%s&cb=%s",
		p.config.LastFM.APIKey,
		p.config.LastFM.RedirectURL)
}

// ExchangeCode exchanges the authorization token for a session key.
func (p *Provider) ExchangeCode(ctx context.Context, code string) (*providers.AuthResult, error) {
	// Governing: SPEC user-authentication REQ "Suppress Last.fm Token from Logs"
	p.logger.Debug("exchanging code for session key")

	params := map[string]string{
		"method":  "auth.getSession",
		"token":   code,
		"api_key": p.config.LastFM.APIKey,
	}

	sig := p.signParams(params)
	params["api_sig"] = sig
	params["format"] = "json"

	var result struct {
		Session struct {
			Name       string `json:"name"`
			Key        string `json:"key"`
			Subscriber int    `json:"subscriber"`
		} `json:"session"`
		Error   int    `json:"error"`
		Message string `json:"message"`
	}

	if err := p.doRequest(ctx, "GET", params, &result); err != nil {
		return nil, err
	}

	if result.Error != 0 {
		return nil, fmt.Errorf("last.fm api error: %d - %s", result.Error, result.Message)
	}

	return &providers.AuthResult{
		AccessToken:  result.Session.Key, // Store session key as access token
		RefreshToken: "",                 // Last.fm doesn't have refresh tokens
		Expiry:       time.Time{},        // Session keys don't expire
		DisplayName:  result.Session.Name,
		UserID:       result.Session.Name,
	}, nil
}

// RefreshToken is not applicable for Last.fm as session keys don't expire.
func (p *Provider) RefreshToken(ctx context.Context, refreshToken string) (*providers.AuthResult, error) {
	return nil, fmt.Errorf("Last.fm session keys do not expire")
}

// Disconnect performs cleanup when disconnecting from Last.fm.
func (p *Provider) Disconnect(ctx context.Context) error {
	p.logger.Info("disconnecting from Last.fm", "user", p.user.Username)
	return nil
}

// GetRecentListens retrieves tracks played after the given timestamp.
func (p *Provider) GetRecentListens(ctx context.Context, since time.Time, callback func([]providers.Track) error) error {
	p.logger.Info("fetching recent listens from last.fm", "username", p.auth.Username, "since", since)

	page := 1
	limit := 200

	for {
		p.logger.Debug("fetching recent tracks page", "page", page, "limit", limit)

		params := map[string]string{
			"method":  "user.getRecentTracks",
			"user":    p.auth.Username,
			"api_key": p.config.LastFM.APIKey,
			"limit":   strconv.Itoa(limit),
			"page":    strconv.Itoa(page),
			"format":  "json",
		}

		if !since.IsZero() {
			params["from"] = fmt.Sprintf("%d", since.Unix())
		}

		var result struct {
			RecentTracks struct {
				Track []struct {
					Artist struct {
						Name string `json:"#text"`
					} `json:"artist"`
					Name  string `json:"name"`
					Album struct {
						Name string `json:"#text"`
					} `json:"album"`
					URL  string `json:"url"`
					Date struct {
						Uts string `json:"uts"`
					} `json:"date"`
					Attr struct {
						NowPlaying string `json:"nowplaying"`
					} `json:"@attr"`
				} `json:"track"`
				Attr struct {
					TotalPages string `json:"totalPages"`
				} `json:"@attr"`
			} `json:"recenttracks"`
		}

		if err := p.doRequest(ctx, "GET", params, &result); err != nil {
			return err
		}

		p.logger.Debug("fetched page", "page", page, "count", len(result.RecentTracks.Track), "totalPages", result.RecentTracks.Attr.TotalPages)

		if len(result.RecentTracks.Track) == 0 {
			break
		}

		var tracks []providers.Track

		for _, t := range result.RecentTracks.Track {
			// Skip currently playing track as it doesn't have a final timestamp yet
			if t.Attr.NowPlaying == "true" {
				p.logger.Debug("skipping now playing track", "name", t.Name)
				continue
			}

			uts, err := strconv.ParseInt(t.Date.Uts, 10, 64)
			if err != nil {
				p.logger.Warn("failed to parse track date", "uts", t.Date.Uts, "error", err)
				continue
			}

			tracks = append(tracks, providers.Track{
				ID:       t.URL, // Last.fm doesn't provide stable IDs, using URL
				Name:     t.Name,
				Artist:   t.Artist.Name,
				Album:    t.Album.Name,
				PlayedAt: time.Unix(uts, 0).UTC(),
				URL:      t.URL,
			})
		}

		if len(tracks) > 0 {
			if err := callback(tracks); err != nil {
				return err
			}
		}

		totalPages, err := strconv.Atoi(result.RecentTracks.Attr.TotalPages)
		if err != nil {
			p.logger.Warn("failed to parse totalPages", "value", result.RecentTracks.Attr.TotalPages, "error", err)
			break
		}

		if page >= totalPages {
			break
		}
		page++
	}

	return nil
}

// signParams generates the api_sig for Last.fm API calls.
// sort alphabetically, concatenate name+value, append secret, md5 hash
func (p *Provider) signParams(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sigStr strings.Builder
	for _, k := range keys {
		sigStr.WriteString(k)
		sigStr.WriteString(params[k])
	}
	sigStr.WriteString(p.config.LastFM.SharedSecret)

	hasher := md5.New()
	hasher.Write([]byte(sigStr.String()))
	return hex.EncodeToString(hasher.Sum(nil))
}

func (p *Provider) doRequest(ctx context.Context, method string, params map[string]string, result interface{}) error {
	data := url.Values{}
	for k, v := range params {
		data.Set(k, v)
	}

	var req *http.Request
	var err error

	if method == "POST" {
		req, err = http.NewRequestWithContext(ctx, "POST", p.baseURL, strings.NewReader(data.Encode()))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		// GET
		reqURL := p.baseURL + "?" + data.Encode()
		req, err = http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			return err
		}
	}

	// Retry logic for 500 errors
	maxRetries := 3
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			// Exponential backoff: 1s, 2s, 4s
			time.Sleep(time.Duration(1<<uint(i-1)) * time.Second)
			p.logger.Info("retrying last.fm request", "attempt", i+1)
		}

		resp, err := p.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				p.logger.Warn("failed to close response body", "error", err)
			}
		}()

		if resp.StatusCode == http.StatusOK {
			if result != nil {
				if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
					return err
				}
			}
			return nil
		}

		// Try to read body for error details
		body, _ := io.ReadAll(resp.Body)
		lastErr = fmt.Errorf("last.fm api returned status %d: %s", resp.StatusCode, string(body))

		// If not a 500 error, don't retry
		if resp.StatusCode < 500 {
			return lastErr
		}
	}

	return lastErr
}
