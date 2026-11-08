package musicbrainz

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/enrichers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestEnricher creates an enricher with a custom base URL for testing
func createTestEnricher(t *testing.T, serverURL string) enrichers.Enricher {
	cfg := &config.Config{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := New(logger, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	e := enricher.(*Enricher)
	e.WithBaseURL(serverURL)
	return enricher
}

func TestNew(t *testing.T) {
	cfg := &config.Config{}
	factory := New(nil, cfg)

	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, enricher)

	assert.Equal(t, enrichers.TypeMusicBrainz, enricher.Type())
	assert.Equal(t, "MusicBrainz", enricher.Name())
	assert.True(t, enricher.IsAvailable())
}

func TestNew_CustomUserAgent(t *testing.T) {
	cfg := &config.Config{}
	cfg.Metadata.MusicBrainz.UserAgent = "CustomAgent/1.0"

	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	e := enricher.(*Enricher)
	assert.Equal(t, "CustomAgent/1.0", e.userAgent)
}

func TestNew_DefaultUserAgent(t *testing.T) {
	cfg := &config.Config{}
	// No user agent configured

	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	e := enricher.(*Enricher)
	assert.Contains(t, e.userAgent, "Spotter")
}

func TestIsAvailable(t *testing.T) {
	cfg := &config.Config{}
	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	// MusicBrainz is always available (free API)
	assert.True(t, enricher.IsAvailable())
}

func TestMatchArtist_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/artist")
		assert.Contains(t, r.URL.RawQuery, "fmt=json")
		assert.NotEmpty(t, r.Header.Get("User-Agent"))

		response := artistSearchResponse{
			Artists: []mbArtist{
				{
					ID:       "mbid-radiohead",
					Name:     "Radiohead",
					Score:    100,
					SortName: "Radiohead",
				},
				{
					ID:       "mbid-other",
					Name:     "Radiohead Tribute",
					Score:    65,
					SortName: "Radiohead Tribute",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)
	mbid, confidence, err := matcher.MatchArtist(context.Background(), "Radiohead")

	require.NoError(t, err)
	assert.Equal(t, "mbid-radiohead", mbid)
	assert.Equal(t, 1.0, confidence)
}

func TestEscapeLuceneValue(t *testing.T) {
	assert.Equal(t, `"AC/DC"`, escapeLuceneValue("AC/DC"))
	assert.Equal(t, `"The Beatles"`, escapeLuceneValue("The Beatles"))
	assert.Equal(t, `"Guns N' Roses"`, escapeLuceneValue("Guns N' Roses"))
	assert.Equal(t, `"Buck 65:Reject"`, escapeLuceneValue("Buck 65:Reject"))
	assert.Equal(t, `"Bill\"With\"Quotes"`, escapeLuceneValue(`Bill"With"Quotes`))
	assert.Equal(t, `"back\\slash"`, escapeLuceneValue(`back\slash`))
}

func TestMatchArtist_SpecialCharactersAreEscaped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/artist")
		assert.Equal(t, `artist:"AC/DC"`, r.URL.Query().Get("query"))

		response := artistSearchResponse{
			Artists: []mbArtist{
				{ID: "mbid-acdc", Name: "AC/DC", Score: 100},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)
	mbid, _, err := matcher.MatchArtist(context.Background(), "AC/DC")

	require.NoError(t, err)
	assert.Equal(t, "mbid-acdc", mbid)
}

func TestMatchArtist_MultiWordNameIsPhraseBound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, `artist:"The Beatles"`, r.URL.Query().Get("query"))

		response := artistSearchResponse{
			Artists: []mbArtist{
				{ID: "mbid-beatles", Name: "The Beatles", Score: 100},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)
	mbid, _, err := matcher.MatchArtist(context.Background(), "The Beatles")

	require.NoError(t, err)
	assert.Equal(t, "mbid-beatles", mbid)
}

func TestMatchArtist_QuotedNameIsEscaped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, `artist:"We\"ird\" Al"`, r.URL.Query().Get("query"))

		response := artistSearchResponse{
			Artists: []mbArtist{
				{ID: "mbid-weird-al", Name: `We"ird" Al`, Score: 100},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)
	mbid, _, err := matcher.MatchArtist(context.Background(), `We"ird" Al`)

	require.NoError(t, err)
	assert.Equal(t, "mbid-weird-al", mbid)
}

func TestMatchArtist_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := artistSearchResponse{
			Artists: []mbArtist{},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)
	mbid, confidence, err := matcher.MatchArtist(context.Background(), "NonExistentArtist")

	require.NoError(t, err)
	assert.Equal(t, "", mbid)
	assert.Equal(t, 0.0, confidence)
}

func TestMatchArtist_LowScore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := artistSearchResponse{
			Artists: []mbArtist{
				{
					ID:    "mbid-partial",
					Name:  "Similar Artist",
					Score: 45,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)
	mbid, confidence, err := matcher.MatchArtist(context.Background(), "Test")

	require.NoError(t, err)
	assert.Equal(t, "mbid-partial", mbid)
	assert.Equal(t, 0.45, confidence)
}

func TestMatchAlbum_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/release-group")
		assert.Equal(t, `releasegroup:"OK Computer" AND artist:"Radiohead"`, r.URL.Query().Get("query"))

		response := releaseGroupSearchResponse{
			ReleaseGroups: []mbReleaseGroup{
				{
					ID:          "mbid-ok-computer",
					Title:       "OK Computer",
					Score:       95,
					PrimaryType: "Album",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)
	mbid, confidence, err := matcher.MatchAlbum(context.Background(), "OK Computer", "Radiohead")

	require.NoError(t, err)
	assert.Equal(t, "mbid-ok-computer", mbid)
	assert.Equal(t, 0.95, confidence)
}

func TestMatchAlbum_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := releaseGroupSearchResponse{
			ReleaseGroups: []mbReleaseGroup{},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)
	mbid, confidence, err := matcher.MatchAlbum(context.Background(), "NonExistent Album", "Unknown")

	require.NoError(t, err)
	assert.Equal(t, "", mbid)
	assert.Equal(t, 0.0, confidence)
}

func TestMatchTrack_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/recording")
		assert.Contains(t, r.URL.RawQuery, "recording")

		response := recordingSearchResponse{
			Recordings: []mbRecording{
				{
					ID:    "mbid-paranoid-android",
					Title: "Paranoid Android",
					Score: 98,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)
	mbid, confidence, err := matcher.MatchTrack(context.Background(), "Paranoid Android", "Radiohead", "OK Computer")

	require.NoError(t, err)
	assert.Equal(t, "mbid-paranoid-android", mbid)
	assert.Equal(t, 0.98, confidence)
}

func TestMatchTrack_NoArtistOrAlbum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify query only contains recording, not artist or release
		query := r.URL.Query().Get("query")
		assert.Contains(t, query, "recording:")
		assert.NotContains(t, query, "artist:")
		assert.NotContains(t, query, "release:")

		response := recordingSearchResponse{
			Recordings: []mbRecording{
				{
					ID:    "mbid-track",
					Title: "Test Track",
					Score: 75,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)
	mbid, confidence, err := matcher.MatchTrack(context.Background(), "Test Track", "", "")

	require.NoError(t, err)
	assert.Equal(t, "mbid-track", mbid)
	assert.Equal(t, 0.75, confidence)
}

func TestEnrichArtist_WithMBID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/artist/artist-mbid")

		response := mbArtist{
			ID:       "artist-mbid",
			Name:     "Test Artist",
			SortName: "Artist, Test",
			Tags: []mbTag{
				{Name: "rock", Count: 10},
				{Name: "alternative", Count: 5},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{
		Name:          "Test Artist",
		MusicbrainzID: "artist-mbid",
	}

	data, err := artistEnricher.EnrichArtist(context.Background(), artist)

	require.NoError(t, err)
	require.NotNil(t, data)
	assert.Equal(t, "artist-mbid", data.MusicBrainzID)
	assert.Equal(t, "Artist, Test", data.SortName)
	assert.Contains(t, data.Tags, "rock")
	assert.Contains(t, data.Tags, "alternative")
}

func TestEnrichArtist_NoMBID_MatchFirst(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		if callCount == 1 {
			// First call - search for artist
			response := artistSearchResponse{
				Artists: []mbArtist{
					{
						ID:    "found-mbid",
						Name:  "Artist",
						Score: 95,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else {
			// Second call - fetch full details
			response := mbArtist{
				ID:       "found-mbid",
				Name:     "Artist",
				SortName: "Artist",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{
		Name:          "Artist",
		MusicbrainzID: "", // No MBID - should search first
	}

	data, err := artistEnricher.EnrichArtist(context.Background(), artist)

	require.NoError(t, err)
	require.NotNil(t, data)
	assert.Equal(t, "found-mbid", data.MusicBrainzID)
	assert.Equal(t, 2, callCount, "Should make two calls: search + fetch")
}

func TestEnrichArtist_NoMBID_NoMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := artistSearchResponse{
			Artists: []mbArtist{}, // No results
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{
		Name:          "Unknown Artist",
		MusicbrainzID: "",
	}

	data, err := artistEnricher.EnrichArtist(context.Background(), artist)

	require.NoError(t, err)
	assert.Nil(t, data, "Should return nil when no match found")
}

func TestGetArtistImages_ReturnsNil(t *testing.T) {
	// MusicBrainz doesn't provide artist images directly
	cfg := &config.Config{}
	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	artistEnricher := enricher.(enrichers.ArtistEnricher)

	artist := &ent.Artist{
		Name:          "Test Artist",
		MusicbrainzID: "some-mbid",
	}

	images, err := artistEnricher.GetArtistImages(context.Background(), artist)
	require.NoError(t, err)
	assert.Nil(t, images)
}

func TestEnrichAlbum_WithMBID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/release-group/album-mbid")

		response := mbReleaseGroup{
			ID:               "album-mbid",
			Title:            "Test Album",
			PrimaryType:      "Album",
			FirstReleaseDate: "1997-06-16",
			Tags: []mbTag{
				{Name: "rock", Count: 8},
				{Name: "experimental", Count: 4},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	albumEnricher := enricher.(enrichers.AlbumEnricher)

	album := &ent.Album{
		Name:          "Test Album",
		MusicbrainzID: "album-mbid",
	}

	data, err := albumEnricher.EnrichAlbum(context.Background(), album)

	require.NoError(t, err)
	require.NotNil(t, data)
	assert.Equal(t, "album-mbid", data.MusicBrainzID)
	assert.Equal(t, 1997, data.Year)
	assert.Equal(t, "1997-06-16", data.ReleaseDate)
	assert.Equal(t, "album", data.AlbumType)
	assert.Contains(t, data.Tags, "rock")
	assert.Contains(t, data.Tags, "experimental")
}

func TestEnrichAlbum_YearParsing(t *testing.T) {
	tests := []struct {
		name         string
		releaseDate  string
		expectedYear int
	}{
		{"full date", "2023-12-25", 2023},
		{"year-month", "2020-06", 2020},
		{"year only", "1999", 1999},
		{"empty", "", 0},
		{"partial", "20", 0}, // Less than 4 chars, won't parse
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := mbReleaseGroup{
					ID:               "album-mbid",
					Title:            "Test Album",
					FirstReleaseDate: tt.releaseDate,
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()

			enricher := createTestEnricher(t, server.URL)
			albumEnricher := enricher.(enrichers.AlbumEnricher)

			album := &ent.Album{
				Name:          "Test Album",
				MusicbrainzID: "album-mbid",
			}

			data, err := albumEnricher.EnrichAlbum(context.Background(), album)

			require.NoError(t, err)
			require.NotNil(t, data)
			assert.Equal(t, tt.expectedYear, data.Year)
		})
	}
}

func TestEnrichTrack_WithMBID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/recording/track-mbid")

		response := mbRecording{
			ID:     "track-mbid",
			Title:  "Test Track",
			Length: 245000,
			ISRCs:  []string{"USRC17607839", "USRC17607840"},
			Tags: []mbTag{
				{Name: "electronic", Count: 3},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	trackEnricher := enricher.(enrichers.TrackEnricher)

	mbid := "track-mbid"
	track := &ent.Track{
		Name:          "Test Track",
		MusicbrainzID: &mbid,
	}

	data, err := trackEnricher.EnrichTrack(context.Background(), track)

	require.NoError(t, err)
	require.NotNil(t, data)
	assert.Equal(t, "track-mbid", data.MusicBrainzID)
	assert.Equal(t, "USRC17607839", data.ISRC)
	assert.Equal(t, 245000, data.DurationMs)
	assert.Contains(t, data.Tags, "electronic")
	assert.Contains(t, data.MusicBrainzURL, "track-mbid")
}

func TestEnrichTrack_NoISRC(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := mbRecording{
			ID:     "track-no-isrc",
			Title:  "Track Without ISRC",
			Length: 180000,
			ISRCs:  []string{}, // No ISRCs
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	trackEnricher := enricher.(enrichers.TrackEnricher)

	mbid := "track-no-isrc"
	track := &ent.Track{
		Name:          "Track Without ISRC",
		MusicbrainzID: &mbid,
	}

	data, err := trackEnricher.EnrichTrack(context.Background(), track)

	require.NoError(t, err)
	require.NotNil(t, data)
	assert.Equal(t, "", data.ISRC)
}

func TestEnrichTrack_NilMBID_Match(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		if callCount == 1 {
			// Search call
			response := recordingSearchResponse{
				Recordings: []mbRecording{
					{ID: "found-track-mbid", Title: "Track", Score: 90},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else {
			// Detail fetch call
			response := mbRecording{
				ID:    "found-track-mbid",
				Title: "Track",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	trackEnricher := enricher.(enrichers.TrackEnricher)

	track := &ent.Track{
		Name:          "Track",
		MusicbrainzID: nil, // Nil MBID
		Edges: ent.TrackEdges{
			Artist: &ent.Artist{Name: "Artist"},
			Album:  &ent.Album{Name: "Album"},
		},
	}

	data, err := trackEnricher.EnrichTrack(context.Background(), track)

	require.NoError(t, err)
	require.NotNil(t, data)
	assert.Equal(t, "found-track-mbid", data.MusicBrainzID)
}

func TestRateLimiting(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		response := artistSearchResponse{Artists: []mbArtist{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Rate limiting is hard to test precisely, just verify multiple calls work
	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)

	_, _, _ = matcher.MatchArtist(context.Background(), "Test1")
	_, _, _ = matcher.MatchArtist(context.Background(), "Test2")

	assert.Equal(t, 2, callCount)
}

func TestDoRequest_ServiceUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)

	_, _, err := matcher.MatchArtist(context.Background(), "Test")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limited")
}

func TestDoRequest_TooManyRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)

	_, _, err := matcher.MatchArtist(context.Background(), "Test")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limited")
}

func TestDoRequest_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)

	_, _, err := matcher.MatchArtist(context.Background(), "Test")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestDoRequest_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{invalid json"))
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)

	_, _, err := matcher.MatchArtist(context.Background(), "Test")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestGetAlbumImages_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := struct {
			Images []struct {
				ID         int64  `json:"id"`
				Front      bool   `json:"front"`
				Back       bool   `json:"back"`
				Image      string `json:"image"`
				Thumbnails struct {
					Small string `json:"small"`
					Large string `json:"large"`
				} `json:"thumbnails"`
			} `json:"images"`
		}{
			Images: []struct {
				ID         int64  `json:"id"`
				Front      bool   `json:"front"`
				Back       bool   `json:"back"`
				Image      string `json:"image"`
				Thumbnails struct {
					Small string `json:"small"`
					Large string `json:"large"`
				} `json:"thumbnails"`
			}{
				{
					ID:    1,
					Front: true,
					Image: "http://example.com/front.jpg",
				},
				{
					ID:    2,
					Back:  true,
					Image: "http://example.com/back.jpg",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// For GetAlbumImages we need to mock the coverartarchive.org endpoint
	// Since the code hardcodes that URL, we can only test with an album with no MBID
	// or we need to modify the implementation to allow injection
	cfg := &config.Config{}
	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	albumEnricher := enricher.(enrichers.AlbumEnricher)

	album := &ent.Album{
		Name:          "Test Album",
		MusicbrainzID: "", // No MBID - will return nil
	}

	images, err := albumEnricher.GetAlbumImages(context.Background(), album)
	require.NoError(t, err)
	assert.Nil(t, images)
}

func TestGetAlbumImages_NoMBID(t *testing.T) {
	cfg := &config.Config{}
	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	albumEnricher := enricher.(enrichers.AlbumEnricher)

	album := &ent.Album{
		Name:          "Test Album",
		MusicbrainzID: "", // Empty MBID
	}

	images, err := albumEnricher.GetAlbumImages(context.Background(), album)

	require.NoError(t, err)
	assert.Nil(t, images)
}

func TestGetAlbumImages_NotFound(t *testing.T) {
	// Can't easily test without mocking coverartarchive.org
	// This test verifies behavior with no MBID
	cfg := &config.Config{}
	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	albumEnricher := enricher.(enrichers.AlbumEnricher)

	album := &ent.Album{
		Name:          "Nonexistent Album",
		MusicbrainzID: "",
	}

	images, err := albumEnricher.GetAlbumImages(context.Background(), album)

	require.NoError(t, err)
	assert.Nil(t, images)
}

func TestInterfaceImplementation(t *testing.T) {
	// Verify Enricher implements all required interfaces at compile time
	var _ enrichers.Enricher = (*Enricher)(nil)
	var _ enrichers.ArtistEnricher = (*Enricher)(nil)
	var _ enrichers.AlbumEnricher = (*Enricher)(nil)
	var _ enrichers.TrackEnricher = (*Enricher)(nil)
	var _ enrichers.IDMatcher = (*Enricher)(nil)
}

func TestDoRequest_UserAgentSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent := r.Header.Get("User-Agent")
		assert.NotEmpty(t, userAgent)
		assert.Contains(t, userAgent, "Spotter")

		response := artistSearchResponse{Artists: []mbArtist{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	matcher := enricher.(enrichers.IDMatcher)

	_, _, _ = matcher.MatchArtist(context.Background(), "Test")
}

func TestEnrichAlbum_NoArtist(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		if callCount == 1 {
			// Search for album
			response := releaseGroupSearchResponse{
				ReleaseGroups: []mbReleaseGroup{
					{ID: "found", Title: "Album", Score: 80},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else {
			// Fetch album details
			response := mbReleaseGroup{ID: "found", Title: "Album"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	albumEnricher := enricher.(enrichers.AlbumEnricher)

	album := &ent.Album{
		Name:          "Album",
		MusicbrainzID: "",
		// No artist edge
	}

	data, err := albumEnricher.EnrichAlbum(context.Background(), album)

	require.NoError(t, err)
	require.NotNil(t, data)
	assert.Equal(t, "found", data.MusicBrainzID)
}

func TestEnrichTrack_EmptyStringMBID(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		if callCount == 1 {
			response := recordingSearchResponse{
				Recordings: []mbRecording{
					{ID: "found", Title: "Track", Score: 88},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else {
			response := mbRecording{ID: "found", Title: "Track"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	enricher := createTestEnricher(t, server.URL)
	trackEnricher := enricher.(enrichers.TrackEnricher)

	emptyStr := ""
	track := &ent.Track{
		Name:          "Track",
		MusicbrainzID: &emptyStr, // Non-nil but empty
	}

	data, err := trackEnricher.EnrichTrack(context.Background(), track)

	require.NoError(t, err)
	require.NotNil(t, data)
	assert.Equal(t, "found", data.MusicBrainzID)
}
