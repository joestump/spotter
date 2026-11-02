package lastfm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestProvider creates a Provider with a custom base URL for testing
func createTestProvider(t *testing.T, cfg *config.Config, user *ent.User, serverURL string) *Provider {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := New(logger, cfg)
	provider, err := factory(context.Background(), user)
	require.NoError(t, err)
	require.NotNil(t, provider)

	p := provider.(*Provider)
	p.WithBaseURL(serverURL)
	return p
}

// createTestAuthenticator creates an Authenticator with a custom base URL for testing
func createTestAuthenticator(t *testing.T, cfg *config.Config, serverURL string) *Provider {
	factory := NewAuthenticator(nil, cfg)
	auth := factory()

	p := auth.(*Provider)
	p.WithBaseURL(serverURL)
	return p
}

func TestNew_NoAuth(t *testing.T) {
	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"
	cfg.LastFM.SharedSecret = "test-secret"

	factory := New(nil, cfg)

	user := &ent.User{
		Username: "testuser",
		// No LastfmAuth edge
	}

	provider, err := factory(context.Background(), user)
	assert.NoError(t, err)
	assert.Nil(t, provider)
}

func TestNew_WithAuth(t *testing.T) {
	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"
	cfg.LastFM.SharedSecret = "test-secret"

	factory := New(nil, cfg)

	user := &ent.User{
		Username: "testuser",
		Edges: ent.UserEdges{
			LastfmAuth: &ent.LastFMAuth{
				Username:   "lastfm-user",
				SessionKey: "session-key-123",
			},
		},
	}

	provider, err := factory(context.Background(), user)
	require.NoError(t, err)
	require.NotNil(t, provider)

	assert.Equal(t, providers.TypeLastFM, provider.Type())
}

func TestNewAuthenticator(t *testing.T) {
	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"
	cfg.LastFM.SharedSecret = "test-secret"

	factory := NewAuthenticator(nil, cfg)
	auth := factory()

	require.NotNil(t, auth)
	assert.Equal(t, providers.TypeLastFM, auth.Type())
	assert.True(t, auth.SupportsAuth())
}

func TestGetAuthURL(t *testing.T) {
	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"
	cfg.LastFM.RedirectURL = "http://localhost:8080/callback"

	factory := NewAuthenticator(nil, cfg)
	auth := factory()

	raw := auth.GetAuthURL("random-state")
	assert.Contains(t, raw, "www.last.fm/api/auth/")

	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	assert.Equal(t, "test-api-key", parsed.Query().Get("api_key"))
	assert.Equal(t, "http://localhost:8080/callback", parsed.Query().Get("cb"))
}

func TestGetAuthURL_NoAPIKey(t *testing.T) {
	cfg := &config.Config{}
	// No API key configured

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := NewAuthenticator(logger, cfg)
	auth := factory()

	url := auth.GetAuthURL("random-state")
	assert.Equal(t, "", url)
}

func TestGetAuthURL_EscapesParams(t *testing.T) {
	cfg := &config.Config{}
	cfg.LastFM.APIKey = "key&with=chars"
	cfg.LastFM.RedirectURL = "https://example.com/cb?a=1&b=2"

	factory := NewAuthenticator(nil, cfg)
	auth := factory()

	got := auth.GetAuthURL("state")

	parsed, err := url.Parse(got)
	require.NoError(t, err)
	assert.Equal(t, "www.last.fm", parsed.Host)
	assert.Equal(t, "/api/auth/", parsed.Path)
	assert.Equal(t, "key&with=chars", parsed.Query().Get("api_key"))
	assert.Equal(t, "https://example.com/cb?a=1&b=2", parsed.Query().Get("cb"))
	// The raw query holds exactly one separator: neither value leaks a bare &.
	assert.Equal(t, 1, strings.Count(parsed.RawQuery, "&"))
}

func TestExchangeCode_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)

		query := r.URL.Query()
		assert.Equal(t, "auth.getSession", query.Get("method"))
		assert.Equal(t, "test-token", query.Get("token"))
		assert.Equal(t, "test-api-key", query.Get("api_key"))
		assert.Equal(t, "json", query.Get("format"))
		assert.NotEmpty(t, query.Get("api_sig"))

		response := map[string]interface{}{
			"session": map[string]interface{}{
				"name":       "lastfm-username",
				"key":        "session-key-abc123",
				"subscriber": 1,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"
	cfg.LastFM.SharedSecret = "test-secret"

	auth := createTestAuthenticator(t, cfg, server.URL)

	result, err := auth.ExchangeCode(context.Background(), "test-token")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "session-key-abc123", result.AccessToken)
	assert.Equal(t, "", result.RefreshToken)
	assert.Equal(t, "lastfm-username", result.DisplayName)
	assert.Equal(t, "lastfm-username", result.UserID)
	assert.True(t, result.Expiry.IsZero())
}

func TestExchangeCode_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"error":   14,
			"message": "Invalid token",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"
	cfg.LastFM.SharedSecret = "test-secret"

	auth := createTestAuthenticator(t, cfg, server.URL)

	_, err := auth.ExchangeCode(context.Background(), "invalid-token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid token")
}

func TestRefreshToken_NotSupported(t *testing.T) {
	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"

	factory := NewAuthenticator(nil, cfg)
	auth := factory()

	_, err := auth.RefreshToken(context.Background(), "any-token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "do not expire")
}

func TestDisconnect(t *testing.T) {
	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"

	user := &ent.User{
		Username: "testuser",
		Edges: ent.UserEdges{
			LastfmAuth: &ent.LastFMAuth{
				Username:   "lastfm-user",
				SessionKey: "session-key",
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := New(logger, cfg)
	provider, err := factory(context.Background(), user)
	require.NoError(t, err)

	err = provider.(providers.Authenticator).Disconnect(context.Background())
	assert.NoError(t, err)
}

func TestGetRecentListens_Success(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		assert.Equal(t, "GET", r.Method)

		query := r.URL.Query()
		assert.Equal(t, "user.getRecentTracks", query.Get("method"))
		assert.Equal(t, "test-user", query.Get("user"))
		assert.Equal(t, "test-api-key", query.Get("api_key"))
		assert.Equal(t, "json", query.Get("format"))

		page, _ := strconv.Atoi(query.Get("page"))

		var tracks []map[string]interface{}

		if page == 1 {
			// First page with 2 tracks
			tracks = []map[string]interface{}{
				{
					"name": "Track 1",
					"artist": map[string]string{
						"#text": "Artist 1",
					},
					"album": map[string]string{
						"#text": "Album 1",
					},
					"url": "http://last.fm/track1",
					"date": map[string]string{
						"uts": "1609459200",
					},
				},
				{
					"name": "Track 2",
					"artist": map[string]string{
						"#text": "Artist 2",
					},
					"album": map[string]string{
						"#text": "Album 2",
					},
					"url": "http://last.fm/track2",
					"date": map[string]string{
						"uts": "1609462800",
					},
				},
			}
		} else {
			// Second page empty
			tracks = []map[string]interface{}{}
		}

		response := map[string]interface{}{
			"recenttracks": map[string]interface{}{
				"track": tracks,
				"@attr": map[string]interface{}{
					"totalPages": "1",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"
	cfg.LastFM.SharedSecret = "test-secret"

	user := &ent.User{
		Username: "testuser",
		Edges: ent.UserEdges{
			LastfmAuth: &ent.LastFMAuth{
				Username:   "test-user",
				SessionKey: "session-key",
			},
		},
	}

	provider := createTestProvider(t, cfg, user, server.URL)

	var collectedTracks []providers.Track
	since := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

	err := provider.GetRecentListens(context.Background(), since, func(tracks []providers.Track) error {
		collectedTracks = append(collectedTracks, tracks...)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, len(collectedTracks))
	assert.Equal(t, "Track 1", collectedTracks[0].Name)
	assert.Equal(t, "Artist 1", collectedTracks[0].Artist)
	assert.Equal(t, "Album 1", collectedTracks[0].Album)
}

func TestGetRecentListens_SingleItemResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Last.fm returns XML-derived JSON: a page with exactly one track
		// carries "track" as a bare object, not a single-element array.
		response := map[string]interface{}{
			"recenttracks": map[string]interface{}{
				"track": map[string]interface{}{
					"name":   "Solo Track",
					"artist": map[string]string{"#text": "Solo Artist"},
					"album":  map[string]string{"#text": "Solo Album"},
					"url":    "http://last.fm/solo",
					"date":   map[string]string{"uts": "1609459200"},
				},
				"@attr": map[string]interface{}{"totalPages": "1"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"
	cfg.LastFM.SharedSecret = "test-secret"

	user := &ent.User{
		Username: "testuser",
		Edges: ent.UserEdges{
			LastfmAuth: &ent.LastFMAuth{
				Username:   "test-user",
				SessionKey: "session-key",
			},
		},
	}

	provider := createTestProvider(t, cfg, user, server.URL)

	var collectedTracks []providers.Track
	err := provider.GetRecentListens(context.Background(), time.Time{}, func(tracks []providers.Track) error {
		collectedTracks = append(collectedTracks, tracks...)
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 1, len(collectedTracks))
	assert.Equal(t, "Solo Track", collectedTracks[0].Name)
	assert.Equal(t, "Solo Artist", collectedTracks[0].Artist)
	assert.Equal(t, "Solo Album", collectedTracks[0].Album)
}

func TestTolerantTrackList(t *testing.T) {
	t.Run("array shape", func(t *testing.T) {
		var list tolerantTrackList
		require.NoError(t, json.Unmarshal([]byte(`[{"name":"A"},{"name":"B"}]`), &list))
		require.Equal(t, 2, len(list))
		assert.Equal(t, "A", list[0].Name)
	})

	t.Run("bare object shape", func(t *testing.T) {
		var list tolerantTrackList
		require.NoError(t, json.Unmarshal([]byte(`{"name":"A"}`), &list))
		require.Equal(t, 1, len(list))
		assert.Equal(t, "A", list[0].Name)
	})

	t.Run("null shape", func(t *testing.T) {
		var list tolerantTrackList
		require.NoError(t, json.Unmarshal([]byte(`null`), &list))
		assert.Empty(t, list)
	})

	t.Run("bad shape errors", func(t *testing.T) {
		var list tolerantTrackList
		assert.Error(t, json.Unmarshal([]byte(`"just a string"`), &list))
	})
}

func TestGetRecentListens_SkipsNowPlaying(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"recenttracks": map[string]interface{}{
				"track": []map[string]interface{}{
					{
						"name": "Now Playing Track",
						"artist": map[string]string{
							"#text": "Artist",
						},
						"album": map[string]string{
							"#text": "Album",
						},
						"url": "http://last.fm/track",
						"@attr": map[string]string{
							"nowplaying": "true",
						},
					},
					{
						"name": "Finished Track",
						"artist": map[string]string{
							"#text": "Artist",
						},
						"album": map[string]string{
							"#text": "Album",
						},
						"url": "http://last.fm/track2",
						"date": map[string]string{
							"uts": "1609459200",
						},
					},
				},
				"@attr": map[string]interface{}{
					"totalPages": "1",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"

	user := &ent.User{
		Edges: ent.UserEdges{
			LastfmAuth: &ent.LastFMAuth{
				Username:   "test-user",
				SessionKey: "session-key",
			},
		},
	}

	provider := createTestProvider(t, cfg, user, server.URL)

	var collectedTracks []providers.Track
	err := provider.GetRecentListens(context.Background(), time.Time{}, func(tracks []providers.Track) error {
		collectedTracks = append(collectedTracks, tracks...)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, len(collectedTracks))
	assert.Equal(t, "Finished Track", collectedTracks[0].Name)
}

func TestGetRecentListens_Pagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		page, _ := strconv.Atoi(query.Get("page"))

		var tracks []map[string]interface{}
		totalPages := "2"

		if page == 1 {
			tracks = []map[string]interface{}{
				{
					"name":   "Track Page 1",
					"artist": map[string]string{"#text": "Artist"},
					"album":  map[string]string{"#text": "Album"},
					"url":    "http://last.fm/track1",
					"date":   map[string]string{"uts": "1609459200"},
				},
			}
		} else if page == 2 {
			tracks = []map[string]interface{}{
				{
					"name":   "Track Page 2",
					"artist": map[string]string{"#text": "Artist"},
					"album":  map[string]string{"#text": "Album"},
					"url":    "http://last.fm/track2",
					"date":   map[string]string{"uts": "1609462800"},
				},
			}
		}

		response := map[string]interface{}{
			"recenttracks": map[string]interface{}{
				"track": tracks,
				"@attr": map[string]interface{}{
					"totalPages": totalPages,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"

	user := &ent.User{
		Edges: ent.UserEdges{
			LastfmAuth: &ent.LastFMAuth{
				Username:   "test-user",
				SessionKey: "session-key",
			},
		},
	}

	provider := createTestProvider(t, cfg, user, server.URL)

	var collectedTracks []providers.Track
	err := provider.GetRecentListens(context.Background(), time.Time{}, func(tracks []providers.Track) error {
		collectedTracks = append(collectedTracks, tracks...)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, len(collectedTracks))
	assert.Equal(t, "Track Page 1", collectedTracks[0].Name)
	assert.Equal(t, "Track Page 2", collectedTracks[1].Name)
}

func TestGetRecentListens_InvalidTimestamp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"recenttracks": map[string]interface{}{
				"track": []map[string]interface{}{
					{
						"name":   "Track",
						"artist": map[string]string{"#text": "Artist"},
						"album":  map[string]string{"#text": "Album"},
						"url":    "http://last.fm/track",
						"date": map[string]string{
							"uts": "invalid-timestamp",
						},
					},
				},
				"@attr": map[string]interface{}{
					"totalPages": "1",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"

	user := &ent.User{
		Edges: ent.UserEdges{
			LastfmAuth: &ent.LastFMAuth{
				Username:   "test-user",
				SessionKey: "session-key",
			},
		},
	}

	provider := createTestProvider(t, cfg, user, server.URL)

	var collectedTracks []providers.Track
	err := provider.GetRecentListens(context.Background(), time.Time{}, func(tracks []providers.Track) error {
		collectedTracks = append(collectedTracks, tracks...)
		return nil
	})

	// Should complete without error but skip invalid track
	require.NoError(t, err)
	assert.Equal(t, 0, len(collectedTracks))
}

func TestGetRecentListens_CallbackError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"recenttracks": map[string]interface{}{
				"track": []map[string]interface{}{
					{
						"name":   "Track",
						"artist": map[string]string{"#text": "Artist"},
						"album":  map[string]string{"#text": "Album"},
						"url":    "http://last.fm/track",
						"date":   map[string]string{"uts": "1609459200"},
					},
				},
				"@attr": map[string]interface{}{
					"totalPages": "1",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"

	user := &ent.User{
		Edges: ent.UserEdges{
			LastfmAuth: &ent.LastFMAuth{
				Username:   "test-user",
				SessionKey: "session-key",
			},
		},
	}

	provider := createTestProvider(t, cfg, user, server.URL)

	err := provider.GetRecentListens(context.Background(), time.Time{}, func(tracks []providers.Track) error {
		return fmt.Errorf("callback error")
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "callback error")
}

func TestSignParams(t *testing.T) {
	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"
	cfg.LastFM.SharedSecret = "secret"

	factory := NewAuthenticator(nil, cfg)
	auth := factory()
	p := auth.(*Provider)

	params := map[string]string{
		"method":  "auth.getSession",
		"token":   "test-token",
		"api_key": "test-api-key",
	}

	sig := p.signParams(params)
	assert.NotEmpty(t, sig)
	assert.Equal(t, 32, len(sig)) // MD5 hash is 32 hex characters

	// Verify signature is deterministic
	sig2 := p.signParams(params)
	assert.Equal(t, sig, sig2)
}

func TestSignParams_Ordering(t *testing.T) {
	cfg := &config.Config{}
	cfg.LastFM.SharedSecret = "secret"

	factory := NewAuthenticator(nil, cfg)
	auth := factory()
	p := auth.(*Provider)

	// Test that params are sorted alphabetically before signing
	params1 := map[string]string{
		"zebra":  "z",
		"alpha":  "a",
		"middle": "m",
	}

	params2 := map[string]string{
		"middle": "m",
		"zebra":  "z",
		"alpha":  "a",
	}

	sig1 := p.signParams(params1)
	sig2 := p.signParams(params2)

	// Should produce same signature regardless of insertion order
	assert.Equal(t, sig1, sig2)
}

func TestDoRequest_Retry500(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Server error"))
			return
		}

		// Success on 3rd attempt
		response := map[string]interface{}{
			"session": map[string]interface{}{
				"name": "user",
				"key":  "key",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"
	cfg.LastFM.SharedSecret = "test-secret"

	auth := createTestAuthenticator(t, cfg, server.URL)

	// This will retry on 500 errors, should eventually succeed
	result, err := auth.ExchangeCode(context.Background(), "test-token")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 3, attempts)
}

func TestDoRequest_No400Retry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Bad request"))
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"
	cfg.LastFM.SharedSecret = "test-secret"

	auth := createTestAuthenticator(t, cfg, server.URL)

	_, err := auth.ExchangeCode(context.Background(), "test-token")
	assert.Error(t, err)
	assert.Equal(t, 1, attempts) // Should not retry on 400 errors
}

func TestInterfaceImplementation(t *testing.T) {
	// Verify Provider implements required interfaces
	var _ providers.Provider = (*Provider)(nil)
	var _ providers.HistoryFetcher = (*Provider)(nil)
	var _ providers.Authenticator = (*Provider)(nil)
}

func TestGetRecentListens_WithSinceParameter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		// Verify the "from" parameter is set correctly
		if query.Get("from") != "" {
			fromTimestamp := query.Get("from")
			assert.Equal(t, "1609459200", fromTimestamp)
		}

		response := map[string]interface{}{
			"recenttracks": map[string]interface{}{
				"track": []map[string]interface{}{
					{
						"name":   "Track",
						"artist": map[string]string{"#text": "Artist"},
						"album":  map[string]string{"#text": "Album"},
						"url":    "http://last.fm/track",
						"date":   map[string]string{"uts": "1609462800"},
					},
				},
				"@attr": map[string]interface{}{
					"totalPages": "1",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"

	user := &ent.User{
		Edges: ent.UserEdges{
			LastfmAuth: &ent.LastFMAuth{
				Username:   "test-user",
				SessionKey: "session-key",
			},
		},
	}

	provider := createTestProvider(t, cfg, user, server.URL)

	var collectedTracks []providers.Track
	since := time.Unix(1609459200, 0)

	err := provider.GetRecentListens(context.Background(), since, func(tracks []providers.Track) error {
		collectedTracks = append(collectedTracks, tracks...)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, len(collectedTracks))
}

func TestGetRecentListens_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"recenttracks": map[string]interface{}{
				"track": []map[string]interface{}{},
				"@attr": map[string]interface{}{
					"totalPages": "0",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.LastFM.APIKey = "test-api-key"

	user := &ent.User{
		Edges: ent.UserEdges{
			LastfmAuth: &ent.LastFMAuth{
				Username:   "test-user",
				SessionKey: "session-key",
			},
		},
	}

	provider := createTestProvider(t, cfg, user, server.URL)

	var collectedTracks []providers.Track
	err := provider.GetRecentListens(context.Background(), time.Time{}, func(tracks []providers.Track) error {
		collectedTracks = append(collectedTracks, tracks...)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 0, len(collectedTracks))
}
