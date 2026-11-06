package navidrome

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/enrichers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_NoNavidromeAuth(t *testing.T) {
	cfg := &config.Config{}
	cfg.Navidrome.BaseURL = "http://localhost:4533"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := New(logger, cfg)

	// User has no NavidromeAuth edge
	user := &ent.User{
		ID:       1,
		Username: "testuser",
		Edges: ent.UserEdges{
			NavidromeAuth: nil,
		},
	}

	enricher, err := factory(context.Background(), user)
	assert.NoError(t, err)
	assert.Nil(t, enricher)
}

func TestNew_WithAuth(t *testing.T) {
	cfg := &config.Config{}
	cfg.Navidrome.BaseURL = "http://localhost:4533"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := New(logger, cfg)

	user := &ent.User{
		ID:       1,
		Username: "testuser",
		Edges: ent.UserEdges{
			NavidromeAuth: &ent.NavidromeAuth{
				ID:       1,
				Password: "testpass",
			},
		},
	}

	enricher, err := factory(context.Background(), user)
	require.NoError(t, err)
	require.NotNil(t, enricher)

	assert.Equal(t, enrichers.TypeNavidrome, enricher.Type())
	assert.Equal(t, "Navidrome", enricher.Name())
	assert.True(t, enricher.IsAvailable())
}

// Token derivation now lives in internal/subsonic, shared with the Navidrome
// provider; TestToken in that package covers it.

func TestAuthParams_IncludesSaltAndToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify authentication parameters
		assert.NotEmpty(t, r.URL.Query().Get("u"), "username should be present")
		assert.NotEmpty(t, r.URL.Query().Get("t"), "token should be present")
		assert.NotEmpty(t, r.URL.Query().Get("s"), "salt should be present")
		assert.Equal(t, "spotter", r.URL.Query().Get("c"))
		assert.Equal(t, "1.16.1", r.URL.Query().Get("v"))
		assert.Equal(t, "json", r.URL.Query().Get("f"))

		response := subsonicSearchResponse{
			SubsonicResponse: struct {
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
			}{
				Status: "ok",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	e := enricher.(*Enricher)

	_, _ = e.searchArtist(context.Background(), "Test")
}

func TestAuthParams_Username(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "testuser", r.URL.Query().Get("u"))

		response := subsonicSearchResponse{
			SubsonicResponse: struct {
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
			}{
				Status: "ok",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	e := enricher.(*Enricher)

	_, _ = e.searchArtist(context.Background(), "Test")
}

func TestEnrichArtist_ByID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/getArtist" {
			assert.Equal(t, "artist-123", r.URL.Query().Get("id"))

			response := subsonicArtistResponse{
				SubsonicResponse: struct {
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
				}{
					Status: "ok",
					Artist: struct {
						ID             string          `json:"id"`
						Name           string          `json:"name"`
						AlbumCount     int             `json:"albumCount"`
						CoverArt       string          `json:"coverArt"`
						ArtistImageURL string          `json:"artistImageUrl"`
						MusicBrainzID  string          `json:"musicBrainzId"`
						SortName       string          `json:"sortName"`
						Album          []subsonicAlbum `json:"album"`
					}{
						ID:            "artist-123",
						Name:          "Radiohead",
						MusicBrainzID: "mbid-radiohead",
						AlbumCount:    10,
					},
				},
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else if r.URL.Path == "/rest/getArtistInfo2" {
			response := subsonicArtistInfo{
				SubsonicResponse: struct {
					Status     string `json:"status"`
					ArtistInfo struct {
						Biography      string `json:"biography"`
						MusicBrainzID  string `json:"musicBrainzId"`
						LastFMURL      string `json:"lastFmUrl"`
						SmallImageURL  string `json:"smallImageUrl"`
						MediumImageURL string `json:"mediumImageUrl"`
						LargeImageURL  string `json:"largeImageUrl"`
					} `json:"artistInfo2"`
				}{
					Status: "ok",
					ArtistInfo: struct {
						Biography      string `json:"biography"`
						MusicBrainzID  string `json:"musicBrainzId"`
						LastFMURL      string `json:"lastFmUrl"`
						SmallImageURL  string `json:"smallImageUrl"`
						MediumImageURL string `json:"mediumImageUrl"`
						LargeImageURL  string `json:"largeImageUrl"`
					}{
						Biography:     "English rock band",
						MusicBrainzID: "mbid-radiohead",
					},
				},
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{
		ID:          1,
		Name:        "Radiohead",
		NavidromeID: "artist-123",
	}

	data, err := artistEnricher.EnrichArtist(context.Background(), artist)
	require.NoError(t, err)
	require.NotNil(t, data)

	assert.Equal(t, "artist-123", data.NavidromeID)
	assert.Equal(t, "mbid-radiohead", data.MusicBrainzID)
	assert.Contains(t, data.Bio, "English rock band")
}

func TestEnrichArtist_BySearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/search3" {
			response := subsonicSearchResponse{
				SubsonicResponse: struct {
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
				}{
					Status: "ok",
					SearchResult3: struct {
						Artist []struct {
							ID            string `json:"id"`
							Name          string `json:"name"`
							AlbumCount    int    `json:"albumCount"`
							MusicBrainzID string `json:"musicBrainzId"`
						} `json:"artist"`
						Album []subsonicAlbum `json:"album"`
						Song  []subsonicSong  `json:"song"`
					}{
						Artist: []struct {
							ID            string `json:"id"`
							Name          string `json:"name"`
							AlbumCount    int    `json:"albumCount"`
							MusicBrainzID string `json:"musicBrainzId"`
						}{
							{
								ID:            "found-artist-id",
								Name:          "The Beatles",
								MusicBrainzID: "mbid-beatles",
							},
						},
					},
				},
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else if r.URL.Path == "/rest/getArtist" {
			response := subsonicArtistResponse{}
			response.SubsonicResponse.Status = "ok"
			response.SubsonicResponse.Artist.ID = "found-artist-id"
			response.SubsonicResponse.Artist.Name = "The Beatles"
			response.SubsonicResponse.Artist.MusicBrainzID = "mbid-beatles"

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else if r.URL.Path == "/rest/getArtistInfo2" {
			response := subsonicArtistInfo{}
			response.SubsonicResponse.Status = "ok"

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{
		ID:          2,
		Name:        "The Beatles",
		NavidromeID: "", // No Navidrome ID
	}

	data, err := artistEnricher.EnrichArtist(context.Background(), artist)
	require.NoError(t, err)
	require.NotNil(t, data)

	assert.Equal(t, "found-artist-id", data.NavidromeID)
	assert.Equal(t, "mbid-beatles", data.MusicBrainzID)
}

func TestEnrichArtist_WithMBID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/search3" {
			// Search uses artist name, not MBID
			query := r.URL.Query().Get("query")
			assert.Contains(t, query, "Test Artist")

			response := subsonicSearchResponse{}
			response.SubsonicResponse.Status = "ok"
			response.SubsonicResponse.SearchResult3.Artist = []struct {
				ID            string `json:"id"`
				Name          string `json:"name"`
				AlbumCount    int    `json:"albumCount"`
				MusicBrainzID string `json:"musicBrainzId"`
			}{
				{
					ID:            "artist-by-mbid",
					Name:          "Test Artist",
					MusicBrainzID: "mbid-test",
				},
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else if r.URL.Path == "/rest/getArtist" {
			response := subsonicArtistResponse{}
			response.SubsonicResponse.Status = "ok"
			response.SubsonicResponse.Artist.ID = "artist-by-mbid"
			response.SubsonicResponse.Artist.Name = "Test Artist"
			response.SubsonicResponse.Artist.MusicBrainzID = "mbid-test"

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else if r.URL.Path == "/rest/getArtistInfo2" {
			response := subsonicArtistInfo{}
			response.SubsonicResponse.Status = "ok"

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{
		Name:          "Test Artist",
		MusicbrainzID: "mbid-test",
	}

	data, err := artistEnricher.EnrichArtist(context.Background(), artist)
	require.NoError(t, err)
	require.NotNil(t, data)
}

func TestEnrichAlbum_ByID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/search3" {
			// First call: search for album
			response := subsonicSearchResponse{}
			response.SubsonicResponse.Status = "ok"
			response.SubsonicResponse.SearchResult3.Album = []subsonicAlbum{
				{
					ID:            "album-456",
					Name:          "OK Computer",
					Artist:        "Radiohead",
					Year:          1997,
					MusicBrainzID: "album-mbid-456",
				},
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else if r.URL.Path == "/rest/getAlbum" {
			// Second call: get album details
			assert.Equal(t, "album-456", r.URL.Query().Get("id"))

			response := subsonicAlbumResponse{}
			response.SubsonicResponse.Status = "ok"
			response.SubsonicResponse.Album.ID = "album-456"
			response.SubsonicResponse.Album.Name = "OK Computer"
			response.SubsonicResponse.Album.Artist = "Radiohead"
			response.SubsonicResponse.Album.Year = 1997
			response.SubsonicResponse.Album.Genre = "Alternative Rock"
			response.SubsonicResponse.Album.MusicBrainzID = "album-mbid-456"

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	albumEnricher := enricher.(enrichers.AlbumEnricher)

	album := &ent.Album{
		ID:   1,
		Name: "OK Computer",
		Edges: ent.AlbumEdges{
			Artist: &ent.Artist{Name: "Radiohead"},
		},
	}

	data, err := albumEnricher.EnrichAlbum(context.Background(), album)
	require.NoError(t, err)
	require.NotNil(t, data)

	assert.Equal(t, "album-mbid-456", data.MusicBrainzID)
	assert.Equal(t, 1997, data.Year)
	assert.Equal(t, "Alternative Rock", data.Genre)
}

func TestEnrichAlbum_BySearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/search3" {
			response := subsonicSearchResponse{}
			response.SubsonicResponse.Status = "ok"
			response.SubsonicResponse.SearchResult3.Album = []subsonicAlbum{
				{
					ID:            "album-found",
					Name:          "Abbey Road",
					Artist:        "The Beatles",
					Year:          1969,
					MusicBrainzID: "album-mbid-abbey",
				},
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else if r.URL.Path == "/rest/getAlbum" {
			response := subsonicAlbumResponse{}
			response.SubsonicResponse.Status = "ok"
			response.SubsonicResponse.Album.ID = "album-found"
			response.SubsonicResponse.Album.Name = "Abbey Road"
			response.SubsonicResponse.Album.Year = 1969

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	albumEnricher := enricher.(enrichers.AlbumEnricher)

	album := &ent.Album{
		ID:   1,
		Name: "Abbey Road",
		Edges: ent.AlbumEdges{
			Artist: &ent.Artist{Name: "The Beatles"},
		},
	}

	data, err := albumEnricher.EnrichAlbum(context.Background(), album)
	require.NoError(t, err)
	require.NotNil(t, data)

	assert.Equal(t, 1969, data.Year)
}

func TestEnrichAlbum_ParsesYear(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/search3" {
			// First call: search for album
			response := subsonicSearchResponse{}
			response.SubsonicResponse.Status = "ok"
			response.SubsonicResponse.SearchResult3.Album = []subsonicAlbum{
				{
					ID:   "album-year",
					Name: "Test Album",
					Year: 2023,
				},
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else if r.URL.Path == "/rest/getAlbum" {
			// Second call: get album details
			response := subsonicAlbumResponse{}
			response.SubsonicResponse.Status = "ok"
			response.SubsonicResponse.Album.ID = "album-year"
			response.SubsonicResponse.Album.Name = "Test Album"
			response.SubsonicResponse.Album.Year = 2023

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	albumEnricher := enricher.(enrichers.AlbumEnricher)

	album := &ent.Album{
		ID:   1,
		Name: "Test Album",
		Edges: ent.AlbumEdges{
			Artist: &ent.Artist{Name: "Test Artist"},
		},
	}

	data, err := albumEnricher.EnrichAlbum(context.Background(), album)
	require.NoError(t, err)
	require.NotNil(t, data)

	assert.Equal(t, 2023, data.Year)
}

func TestSearch_MultipleResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := subsonicSearchResponse{}
		response.SubsonicResponse.Status = "ok"
		response.SubsonicResponse.SearchResult3.Artist = []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			AlbumCount    int    `json:"albumCount"`
			MusicBrainzID string `json:"musicBrainzId"`
		}{
			{ID: "artist-1", Name: "First Artist"},
			{ID: "artist-2", Name: "Second Artist"},
			{ID: "artist-3", Name: "Third Artist"},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	e := enricher.(*Enricher)

	artistID, err := e.searchArtist(context.Background(), "Test")
	require.NoError(t, err)
	assert.Equal(t, "artist-1", artistID) // Should return first result
}

func TestSearch_NoResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := subsonicSearchResponse{}
		response.SubsonicResponse.Status = "ok"
		response.SubsonicResponse.SearchResult3.Artist = []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			AlbumCount    int    `json:"albumCount"`
			MusicBrainzID string `json:"musicBrainzId"`
		}{}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	e := enricher.(*Enricher)

	artistID, err := e.searchArtist(context.Background(), "NonexistentArtist")
	assert.NoError(t, err)
	assert.Equal(t, "", artistID)
}

func TestSubsonicError_Code10(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"subsonic-response": map[string]interface{}{
				"status": "failed",
				"error": map[string]interface{}{
					"code":    10,
					"message": "Required parameter is missing",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{Name: "Test"}

	_, err := artistEnricher.EnrichArtist(context.Background(), artist)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error 10")
}

func TestSubsonicError_Code40(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"subsonic-response": map[string]interface{}{
				"status": "failed",
				"error": map[string]interface{}{
					"code":    40,
					"message": "Wrong username or password",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{Name: "Test"}

	_, err := artistEnricher.EnrichArtist(context.Background(), artist)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error 40")
}

func TestSubsonicError_Code70(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"subsonic-response": map[string]interface{}{
				"status": "failed",
				"error": map[string]interface{}{
					"code":    70,
					"message": "The requested data was not found",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{Name: "NotFound"}

	_, err := artistEnricher.EnrichArtist(context.Background(), artist)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error 70")
}

func TestEnrichTrack_BySearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/search3" {
			response := subsonicSearchResponse{}
			response.SubsonicResponse.Status = "ok"
			response.SubsonicResponse.SearchResult3.Song = []subsonicSong{
				{
					ID:       "track-123",
					Title:    "Paranoid Android",
					Artist:   "Radiohead",
					Album:    "OK Computer",
					Duration: 383,
					BPM:      144,
					Track:    2,
				},
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else if r.URL.Path == "/rest/getSong" {
			// Second call: get song details
			response := subsonicSongResponse{}
			response.SubsonicResponse.Status = "ok"
			response.SubsonicResponse.Song = subsonicSong{
				ID:       "track-123",
				Title:    "Paranoid Android",
				Artist:   "Radiohead",
				Album:    "OK Computer",
				Duration: 383,
				BPM:      144,
				Track:    2,
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	trackEnricher := enricher.(enrichers.TrackEnricher)

	trackNum := 2
	track := &ent.Track{
		ID:          1,
		Name:        "Paranoid Android",
		TrackNumber: &trackNum,
		Edges: ent.TrackEdges{
			Artist: &ent.Artist{Name: "Radiohead", NavidromeID: "artist-radiohead"},
			Album:  &ent.Album{Name: "OK Computer"},
		},
	}

	data, err := trackEnricher.EnrichTrack(context.Background(), track)
	require.NoError(t, err)
	require.NotNil(t, data)

	assert.Equal(t, "track-123", data.NavidromeID)
	assert.Equal(t, 383000, data.DurationMs) // Converted to milliseconds
}

func TestInterfaceImplementation(t *testing.T) {
	// Verify Enricher implements all required interfaces
	var _ enrichers.Enricher = (*Enricher)(nil)
	var _ enrichers.ArtistEnricher = (*Enricher)(nil)
	var _ enrichers.AlbumEnricher = (*Enricher)(nil)
	var _ enrichers.TrackEnricher = (*Enricher)(nil)
}

// TestGetArtistImages_UsesDirectURLNotCoverArt is a regression test verifying that
// GetArtistImages downloads artist images from the direct CDN URL returned by
// getArtistInfo2 (largeImageUrl, mediumImageUrl, smallImageUrl), NOT from a
// getCoverArt endpoint constructed using those URLs as IDs.
//
// Bug: getArtistInfo2 returns external CDN URLs (e.g. Spotify CDN) in the image
// URL fields. The old code incorrectly used these as getCoverArt?id= parameters,
// causing "image: unknown format" errors because Navidrome cannot interpret a CDN
// URL as an internal cover art ID.
func TestGetArtistImages_UsesDirectURLNotCoverArt(t *testing.T) {
	// Track whether the image URL was requested directly.
	var requestedURLs []string
	// This image server serves valid PNG image data.
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedURLs = append(requestedURLs, r.URL.Path)
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		w.Header().Set("Content-Type", "image/png")
		_ = png.Encode(w, img)
	}))
	defer imageServer.Close()

	// The direct CDN URL for the large artist image.
	directImageURL := imageServer.URL + "/artist-image-large.png"

	// The Navidrome API server returns the direct CDN URL in largeImageUrl.
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/getArtistInfo2" {
			response := subsonicArtistInfo{}
			response.SubsonicResponse.Status = "ok"
			response.SubsonicResponse.ArtistInfo.LargeImageURL = directImageURL

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else {
			// Any call to getCoverArt would be a bug — fail the test.
			t.Errorf("unexpected request to %s — GetArtistImages should not call getCoverArt", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()

	enricher := createTestEnricher(t, apiServer.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	tmpDir := t.TempDir()
	// Override the image save path to use a temp dir.
	// We do this by creating the expected directory structure.
	oldWd, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(oldWd)

	artist := &ent.Artist{
		ID:          42,
		Name:        "Test Artist",
		NavidromeID: "artist-42",
	}

	images, err := artistEnricher.GetArtistImages(context.Background(), artist)
	require.NoError(t, err)

	// The direct image URL should have been hit on the image server.
	assert.Contains(t, requestedURLs, "/artist-image-large.png",
		"GetArtistImages must download from the direct CDN URL, not via getCoverArt")

	// The returned ImageData should reference the local file.
	require.Len(t, images, 1)
	assert.Equal(t, directImageURL, images[0].URL,
		"ImageData.URL should be the direct CDN URL, not a getCoverArt URL")
	assert.NotEmpty(t, images[0].LocalPath,
		"ImageData.LocalPath should be set after successful download")
	assert.Equal(t, "navidrome", images[0].Source)
	assert.True(t, images[0].IsPrimary)
}

// TestGetArtistImages_EmptyImageURLsReturnsNoImages verifies that when Navidrome
// returns no image URLs for an artist, GetArtistImages returns an empty slice.
func TestGetArtistImages_EmptyImageURLsReturnsNoImages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/getArtistInfo2" {
			response := subsonicArtistInfo{}
			response.SubsonicResponse.Status = "ok"
			// No image URLs set.
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{
		ID:          1,
		Name:        "No Image Artist",
		NavidromeID: "artist-1",
	}

	images, err := artistEnricher.GetArtistImages(context.Background(), artist)
	require.NoError(t, err)
	assert.Empty(t, images, "should return no images when Navidrome returns no image URLs")
}

// Helper function to create a test enricher with custom base URL
func createTestEnricher(t *testing.T, serverURL string) enrichers.Enricher {
	cfg := &config.Config{}
	cfg.Navidrome.BaseURL = serverURL

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	user := &ent.User{
		ID:       1,
		Username: "testuser",
		Edges: ent.UserEdges{
			NavidromeAuth: &ent.NavidromeAuth{
				ID:       1,
				Password: "testpass",
			},
		},
	}

	enricher := &Enricher{
		logger:     logger,
		config:     cfg,
		user:       user,
		auth:       user.Edges.NavidromeAuth,
		httpClient: &http.Client{},
	}

	return enricher
}
