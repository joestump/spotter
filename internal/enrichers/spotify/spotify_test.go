package spotify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/enrichers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_NoCredentials(t *testing.T) {
	cfg := &config.Config{}
	// No Spotify credentials configured

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := New(logger, cfg)
	enricher, err := factory(context.Background(), &ent.User{})

	assert.NoError(t, err)
	assert.Nil(t, enricher)
}

func TestNew_NoUserAuth(t *testing.T) {
	cfg := &config.Config{}
	cfg.Spotify.ClientID = "test-client-id"
	cfg.Spotify.ClientSecret = "test-client-secret"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := New(logger, cfg)

	// User has no SpotifyAuth edge
	user := &ent.User{
		ID: 1,
		Edges: ent.UserEdges{
			SpotifyAuth: nil,
		},
	}

	enricher, err := factory(context.Background(), user)
	assert.NoError(t, err)
	assert.Nil(t, enricher)
}

func TestNew_WithAuth(t *testing.T) {
	cfg := &config.Config{}
	cfg.Spotify.ClientID = "test-client-id"
	cfg.Spotify.ClientSecret = "test-client-secret"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := New(logger, cfg)

	user := &ent.User{
		ID: 1,
		Edges: ent.UserEdges{
			SpotifyAuth: &ent.SpotifyAuth{
				ID:           1,
				AccessToken:  "test-access-token",
				RefreshToken: "test-refresh-token",
				Expiry:       time.Now().Add(1 * time.Hour),
			},
		},
	}

	enricher, err := factory(context.Background(), user)
	require.NoError(t, err)
	require.NotNil(t, enricher)

	assert.Equal(t, enrichers.TypeSpotify, enricher.Type())
	assert.Equal(t, "Spotify", enricher.Name())
	assert.True(t, enricher.IsAvailable())
}

func TestGetValidToken_NotExpired(t *testing.T) {
	cfg := &config.Config{}
	cfg.Spotify.ClientID = "test-client-id"
	cfg.Spotify.ClientSecret = "test-client-secret"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := New(logger, cfg)

	futureExpiry := time.Now().Add(1 * time.Hour)
	user := &ent.User{
		ID: 1,
		Edges: ent.UserEdges{
			SpotifyAuth: &ent.SpotifyAuth{
				AccessToken:  "valid-token",
				RefreshToken: "refresh-token",
				Expiry:       futureExpiry,
			},
		},
	}

	enricher, err := factory(context.Background(), user)
	require.NoError(t, err)

	e := enricher.(*Enricher)
	token, err := e.getValidToken(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, "valid-token", token)
}

func TestGetValidToken_Expired(t *testing.T) {
	// This test verifies that tokens within 5min of expiry trigger refresh
	cfg := &config.Config{}
	cfg.Spotify.ClientID = "test-client-id"
	cfg.Spotify.ClientSecret = "test-client-secret"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := New(logger, cfg)

	// Token expires in 3 minutes (within 5min buffer)
	almostExpired := time.Now().Add(3 * time.Minute)
	user := &ent.User{
		ID: 1,
		Edges: ent.UserEdges{
			SpotifyAuth: &ent.SpotifyAuth{
				AccessToken:  "old-token",
				RefreshToken: "refresh-token",
				Expiry:       almostExpired,
			},
		},
	}

	enricher, err := factory(context.Background(), user)
	require.NoError(t, err)

	e := enricher.(*Enricher)
	// Token should trigger refresh (but will fail in test without mock OAuth server)
	// This demonstrates the check happens
	_, err = e.getValidToken(context.Background())
	assert.Error(t, err) // Expected to fail without OAuth mock
}

func TestMatchArtist_ExactMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/v1/search")
		assert.Contains(t, r.URL.Query().Get("q"), "Radiohead")
		assert.Equal(t, "artist", r.URL.Query().Get("type"))
		assert.Contains(t, r.Header.Get("Authorization"), "Bearer")

		response := searchResponse{
			Artists: struct {
				Items []spotifyArtist `json:"items"`
			}{
				Items: []spotifyArtist{
					{
						ID:   "artist-123",
						Name: "Radiohead",
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)

	id, confidence, err := matcher.MatchArtist(context.Background(), "Radiohead")
	require.NoError(t, err)
	assert.Equal(t, "artist-123", id)
	assert.Equal(t, 0.9, confidence) // Exact match
}

func TestMatchArtist_PartialMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := searchResponse{
			Artists: struct {
				Items []spotifyArtist `json:"items"`
			}{
				Items: []spotifyArtist{
					{
						ID:   "artist-456",
						Name: "The Beatles", // Different case/format
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)

	id, confidence, err := matcher.MatchArtist(context.Background(), "Beatles")
	require.NoError(t, err)
	assert.Equal(t, "artist-456", id)
	assert.Equal(t, 0.7, confidence) // Partial match
}

func TestMatchArtist_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := searchResponse{
			Artists: struct {
				Items []spotifyArtist `json:"items"`
			}{
				Items: []spotifyArtist{}, // Empty results
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)

	id, confidence, err := matcher.MatchArtist(context.Background(), "UnknownArtist")
	require.NoError(t, err)
	assert.Equal(t, "", id)
	assert.Equal(t, 0.0, confidence)
}

func TestMatchAlbum_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Query().Get("q"), "OK Computer")
		assert.Contains(t, r.URL.Query().Get("q"), "Radiohead")
		assert.Equal(t, "album", r.URL.Query().Get("type"))

		response := searchResponse{
			Albums: struct {
				Items []spotifyAlbum `json:"items"`
			}{
				Items: []spotifyAlbum{
					{
						ID:   "album-789",
						Name: "OK Computer",
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)

	id, confidence, err := matcher.MatchAlbum(context.Background(), "OK Computer", "Radiohead")
	require.NoError(t, err)
	assert.Equal(t, "album-789", id)
	assert.Equal(t, 0.9, confidence)
}

func TestMatchAlbum_WithoutArtist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		assert.Contains(t, query, "Abbey Road")
		assert.NotContains(t, query, "artist:")

		response := searchResponse{
			Albums: struct {
				Items []spotifyAlbum `json:"items"`
			}{
				Items: []spotifyAlbum{
					{
						ID:   "album-999",
						Name: "Abbey Road",
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)

	id, confidence, err := matcher.MatchAlbum(context.Background(), "Abbey Road", "")
	require.NoError(t, err)
	assert.Equal(t, "album-999", id)
	assert.Equal(t, 0.9, confidence)
}

func TestMatchTrack_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		assert.Contains(t, query, "Paranoid Android")
		assert.Contains(t, query, "Radiohead")
		assert.Contains(t, query, "OK Computer")
		assert.Equal(t, "track", r.URL.Query().Get("type"))

		response := searchResponse{
			Tracks: struct {
				Items []spotifyTrack `json:"items"`
			}{
				Items: []spotifyTrack{
					{
						ID:   "track-555",
						Name: "Paranoid Android",
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)

	id, confidence, err := matcher.MatchTrack(context.Background(), "Paranoid Android", "Radiohead", "OK Computer")
	require.NoError(t, err)
	assert.Equal(t, "track-555", id)
	assert.Equal(t, 0.9, confidence)
}

func TestMatchTrack_NoAlbum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		assert.Contains(t, query, "Creep")
		assert.Contains(t, query, "Radiohead")
		assert.NotContains(t, query, "album:")

		response := searchResponse{
			Tracks: struct {
				Items []spotifyTrack `json:"items"`
			}{
				Items: []spotifyTrack{
					{
						ID:   "track-666",
						Name: "Creep",
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)

	id, confidence, err := matcher.MatchTrack(context.Background(), "Creep", "Radiohead", "")
	require.NoError(t, err)
	assert.Equal(t, "track-666", id)
	assert.Equal(t, 0.9, confidence)
}

func TestEnrichArtist_WithSpotifyID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/artists/artist-123", r.URL.Path)

		response := spotifyArtist{
			ID:     "artist-123",
			Name:   "Radiohead",
			Genres: []string{"alternative rock", "art rock", "permanent wave"},
			Followers: struct {
				Total int `json:"total"`
			}{
				Total: 5000000,
			},
			Popularity: 85,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{
		ID:        1,
		Name:      "Radiohead",
		SpotifyID: "artist-123",
	}

	data, err := artistEnricher.EnrichArtist(context.Background(), artist)
	require.NoError(t, err)
	require.NotNil(t, data)

	assert.Equal(t, "artist-123", data.SpotifyID)
	assert.Contains(t, data.Genres, "alternative rock")
	assert.Contains(t, data.Genres, "art rock")
	assert.NotNil(t, data.Popularity)
	assert.Equal(t, 85, *data.Popularity)
	assert.NotNil(t, data.FollowerCount)
	assert.Equal(t, 5000000, *data.FollowerCount)
}

func TestEnrichArtist_WithoutID_Match(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/search" {
			// Search endpoint
			response := searchResponse{
				Artists: struct {
					Items []spotifyArtist `json:"items"`
				}{
					Items: []spotifyArtist{
						{ID: "artist-found", Name: "The Beatles"},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else if r.URL.Path == "/v1/artists/artist-found" {
			// Get artist endpoint
			response := spotifyArtist{
				ID:         "artist-found",
				Name:       "The Beatles",
				Genres:     []string{"rock", "pop"},
				Popularity: 95,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{
		ID:        2,
		Name:      "The Beatles",
		SpotifyID: "", // No Spotify ID
	}

	data, err := artistEnricher.EnrichArtist(context.Background(), artist)
	require.NoError(t, err)
	require.NotNil(t, data)

	assert.Equal(t, "artist-found", data.SpotifyID)
	assert.Contains(t, data.Genres, "rock")
}

func TestEnrichArtist_WithoutID_NoMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Empty search results
		response := searchResponse{
			Artists: struct {
				Items []spotifyArtist `json:"items"`
			}{
				Items: []spotifyArtist{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{
		ID:        3,
		Name:      "Unknown Artist",
		SpotifyID: "",
	}

	data, err := artistEnricher.EnrichArtist(context.Background(), artist)
	assert.NoError(t, err)
	assert.Nil(t, data)
}

func TestEnrichAlbum_WithSpotifyID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/albums/album-456", r.URL.Path)

		response := spotifyAlbum{
			ID:          "album-456",
			Name:        "OK Computer",
			AlbumType:   "album",
			ReleaseDate: "1997-05-21",
			Genres:      []string{"alternative rock"},
			Popularity:  88,
			Label:       "Parlophone",
			TotalTracks: 12,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	albumEnricher := enricher.(enrichers.AlbumEnricher)

	album := &ent.Album{
		ID:        1,
		Name:      "OK Computer",
		SpotifyID: "album-456",
	}

	data, err := albumEnricher.EnrichAlbum(context.Background(), album)
	require.NoError(t, err)
	require.NotNil(t, data)

	assert.Equal(t, "album-456", data.SpotifyID)
	assert.Equal(t, "1997-05-21", data.ReleaseDate)
	assert.Equal(t, 1997, data.Year)
	assert.Equal(t, "album", data.AlbumType)
	assert.Equal(t, "Parlophone", data.Label)
	assert.Equal(t, 12, data.TotalTracks)
	assert.Equal(t, 88, data.Popularity)
}

func TestEnrichAlbum_ParsesReleaseDate(t *testing.T) {
	tests := []struct {
		name         string
		releaseDate  string
		expectedYear int
	}{
		{"full date", "2023-05-15", 2023},
		{"year-month", "2023-05", 2023},
		{"year only", "2023", 2023},
		{"invalid", "unknown", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := spotifyAlbum{
					ID:          "album-test",
					Name:        "Test Album",
					ReleaseDate: tt.releaseDate,
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()

			enricher := createTestEnricher(t, server.URL)
			albumEnricher := enricher.(enrichers.AlbumEnricher)

			album := &ent.Album{
				ID:        1,
				Name:      "Test Album",
				SpotifyID: "album-test",
			}

			data, err := albumEnricher.EnrichAlbum(context.Background(), album)
			require.NoError(t, err)
			require.NotNil(t, data)

			assert.Equal(t, tt.expectedYear, data.Year)
		})
	}
}

func TestEnrichTrack_WithSpotifyID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tracks/track-789" {
			response := spotifyTrack{
				ID:         "track-789",
				Name:       "Paranoid Android",
				Popularity: 80,
				DurationMs: 383000,
				ExternalIDs: struct {
					ISRC string `json:"isrc"`
				}{
					ISRC: "GBAYE9601210",
				},
				ExternalURLs: struct {
					Spotify string `json:"spotify"`
				}{
					Spotify: "https://open.spotify.com/track/xyz",
				},
				TrackNumber: 2,
				DiscNumber:  1,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else if r.URL.Path == "/v1/audio-features/track-789" {
			response := spotifyAudioFeatures{
				ID:               "track-789",
				Tempo:            144.5,
				Key:              2, // D
				Mode:             1, // Major
				Energy:           0.75,
				Danceability:     0.42,
				Valence:          0.35,
				Acousticness:     0.01,
				Instrumentalness: 0.12,
				Speechiness:      0.04,
				Liveness:         0.08,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	trackEnricher := enricher.(enrichers.TrackEnricher)

	spotifyID := "track-789"
	track := &ent.Track{
		ID:        1,
		Name:      "Paranoid Android",
		SpotifyID: &spotifyID,
	}

	data, err := trackEnricher.EnrichTrack(context.Background(), track)
	require.NoError(t, err)
	require.NotNil(t, data)

	assert.Equal(t, "track-789", data.SpotifyID)
	assert.Equal(t, "GBAYE9601210", data.ISRC)
	assert.Equal(t, 383000, data.DurationMs)
	assert.Equal(t, 2, data.TrackNumber)
	assert.Equal(t, 1, data.DiscNumber)
	assert.NotNil(t, data.Popularity)
	assert.Equal(t, 80, *data.Popularity)
	assert.Equal(t, "https://open.spotify.com/track/xyz", data.SpotifyURL)

	// Audio features
	assert.NotNil(t, data.BPM)
	assert.Equal(t, 144.5, *data.BPM)
	assert.Equal(t, "D", data.MusicalKey)
	assert.NotNil(t, data.Energy)
	assert.Equal(t, 0.75, *data.Energy)
	assert.NotNil(t, data.Danceability)
	assert.Equal(t, 0.42, *data.Danceability)
}

func TestEnrichTrack_NoAudioFeatures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tracks/track-999" {
			response := spotifyTrack{
				ID:         "track-999",
				Name:       "Test Track",
				DurationMs: 180000,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else if r.URL.Path == "/v1/audio-features/track-999" {
			// Return 403 (Extended Quota Mode required)
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	trackEnricher := enricher.(enrichers.TrackEnricher)

	spotifyID := "track-999"
	track := &ent.Track{
		ID:        2,
		Name:      "Test Track",
		SpotifyID: &spotifyID,
	}

	data, err := trackEnricher.EnrichTrack(context.Background(), track)
	require.NoError(t, err)
	require.NotNil(t, data)

	// Should have basic track data
	assert.Equal(t, "track-999", data.SpotifyID)
	assert.Equal(t, 180000, data.DurationMs)

	// But no audio features
	assert.Nil(t, data.BPM)
	assert.Equal(t, "", data.MusicalKey)
	assert.Nil(t, data.Energy)
}

func TestKeyToString_AllKeys(t *testing.T) {
	tests := []struct {
		key      int
		mode     int
		expected string
	}{
		{0, 1, "C"},
		{1, 1, "C#"},
		{2, 1, "D"},
		{3, 1, "D#"},
		{4, 1, "E"},
		{5, 1, "F"},
		{6, 1, "F#"},
		{7, 1, "G"},
		{8, 1, "G#"},
		{9, 1, "A"},
		{10, 1, "A#"},
		{11, 1, "B"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := keyToString(tt.key, tt.mode)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestKeyToString_MajorMinor(t *testing.T) {
	tests := []struct {
		name     string
		key      int
		mode     int
		expected string
	}{
		{"C major", 0, 1, "C"},
		{"C minor", 0, 0, "Cm"},
		{"D major", 2, 1, "D"},
		{"D minor", 2, 0, "Dm"},
		{"G major", 7, 1, "G"},
		{"G minor", 7, 0, "Gm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := keyToString(tt.key, tt.mode)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestKeyToString_InvalidKey(t *testing.T) {
	tests := []struct {
		name string
		key  int
		mode int
	}{
		{"negative", -1, 1},
		{"too high", 12, 1},
		{"way too high", 100, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := keyToString(tt.key, tt.mode)
			assert.Equal(t, "", result)
		})
	}
}

func TestGetArtistImages_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := spotifyArtist{
			ID:   "artist-123",
			Name: "Test Artist",
			Images: []spotifyImage{
				{URL: "http://example.com/large.jpg", Width: 640, Height: 640},
				{URL: "http://example.com/medium.jpg", Width: 320, Height: 320},
				{URL: "http://example.com/small.jpg", Width: 160, Height: 160},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{
		ID:        1,
		Name:      "Test Artist",
		SpotifyID: "artist-123",
	}

	// Note: GetArtistImages will try to download images which will fail in tests
	// since example.com isn't reachable. The function logs warnings but continues,
	// returning nil when all downloads fail. This is expected behavior.
	images, err := artistEnricher.GetArtistImages(context.Background(), artist)
	require.NoError(t, err)
	// Images will be nil since downloads fail, but no error is returned
	// This test verifies the API call succeeds; actual image download is tested elsewhere
	assert.Nil(t, images)
}

func TestGetArtistImages_NullSpotifyID(t *testing.T) {
	cfg := &config.Config{}
	cfg.Spotify.ClientID = "test-client-id"
	cfg.Spotify.ClientSecret = "test-client-secret"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := New(logger, cfg)

	user := &ent.User{
		Edges: ent.UserEdges{
			SpotifyAuth: &ent.SpotifyAuth{
				AccessToken: "token",
				Expiry:      time.Now().Add(1 * time.Hour),
			},
		},
	}

	enricher, _ := factory(context.Background(), user)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{
		ID:        1,
		Name:      "Test Artist",
		SpotifyID: "", // No Spotify ID
	}

	images, err := artistEnricher.GetArtistImages(context.Background(), artist)
	assert.NoError(t, err)
	assert.Nil(t, images)
}

func TestGetAlbumImages_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := spotifyAlbum{
			ID:   "album-456",
			Name: "Test Album",
			Images: []spotifyImage{
				{URL: "http://example.com/cover-large.jpg", Width: 640, Height: 640},
				{URL: "http://example.com/cover-small.jpg", Width: 300, Height: 300},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	albumEnricher := enricher.(enrichers.AlbumEnricher)

	album := &ent.Album{
		ID:        1,
		Name:      "Test Album",
		SpotifyID: "album-456",
	}

	// Note: GetAlbumImages will try to download images which will fail in tests
	// since example.com isn't reachable. The function logs warnings but continues,
	// returning nil when all downloads fail. This is expected behavior.
	images, err := albumEnricher.GetAlbumImages(context.Background(), album)
	require.NoError(t, err)
	// Images will be nil since downloads fail, but no error is returned
	// This test verifies the API call succeeds; actual image download is tested elsewhere
	assert.Nil(t, images)
}

func TestDoRequest_401Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"status": 401, "message": "The access token expired"}}`))
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	e := enricher.(*Enricher)

	_, err := e.doRequest(context.Background(), "artists/test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unauthorized")
}

func TestDoRequest_404NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": {"status": 404, "message": "Not found"}}`))
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	e := enricher.(*Enricher)

	_, err := e.doRequest(context.Background(), "artists/nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestDoRequest_429RateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": {"status": 429, "message": "Rate limit exceeded"}}`))
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	e := enricher.(*Enricher)

	_, err := e.doRequest(context.Background(), "artists/test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rate limited")
}

func TestDoRequest_500ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	e := enricher.(*Enricher)

	_, err := e.doRequest(context.Background(), "artists/test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestDoRequest_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"invalid json`))
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{
		ID:        1,
		Name:      "Test",
		SpotifyID: "test",
	}

	_, err := artistEnricher.EnrichArtist(context.Background(), artist)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestInterfaceImplementation(t *testing.T) {
	// Verify Enricher implements all required interfaces
	var _ enrichers.Enricher = (*Enricher)(nil)
	var _ enrichers.ArtistEnricher = (*Enricher)(nil)
	var _ enrichers.AlbumEnricher = (*Enricher)(nil)
	var _ enrichers.TrackEnricher = (*Enricher)(nil)
	var _ enrichers.IDMatcher = (*Enricher)(nil)
}

// Helper function to create a test enricher with custom API URL
func createTestEnricher(t *testing.T, apiURL string) enrichers.Enricher {
	cfg := &config.Config{}
	cfg.Spotify.ClientID = "test-client-id"
	cfg.Spotify.ClientSecret = "test-client-secret"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	user := &ent.User{
		ID: 1,
		Edges: ent.UserEdges{
			SpotifyAuth: &ent.SpotifyAuth{
				ID:           1,
				AccessToken:  "test-access-token",
				RefreshToken: "test-refresh-token",
				Expiry:       time.Now().Add(1 * time.Hour),
			},
		},
	}

	enricher := &Enricher{
		logger: logger,
		config: cfg,
		user:   user,
		auth:   user.Edges.SpotifyAuth,
		httpClient: &http.Client{
			Transport: &testTransport{baseURL: apiURL},
		},
	}

	return enricher
}

// testTransport is a custom http.RoundTripper that rewrites URLs to use the test server
type testTransport struct {
	baseURL string
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite the URL to use the test server
	req.URL.Scheme = "http"
	if t.baseURL != "" {
		// Parse test server URL
		testURL := t.baseURL
		if len(testURL) > 7 && testURL[:7] == "http://" {
			testURL = testURL[7:]
		}
		req.URL.Host = testURL
	}

	return http.DefaultTransport.RoundTrip(req)
}
