package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/enrichers"
	"spotter/internal/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_NoAPIKey(t *testing.T) {
	cfg := &config.Config{}
	factory := New(nil, cfg)

	enricher, err := factory(context.Background(), nil)
	assert.NoError(t, err)
	assert.Nil(t, enricher)
}

func TestNew_WithAPIKey(t *testing.T) {
	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.BaseURL = "https://api.openai.com/v1"
	cfg.OpenAI.Model = "gpt-4o"
	cfg.Metadata.AI.PromptsDirectory = "./nonexistent"

	factory := New(nil, cfg)

	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, enricher)

	assert.Equal(t, enrichers.TypeOpenAI, enricher.Type())
	assert.Equal(t, "OpenAI", enricher.Name())
	assert.True(t, enricher.IsAvailable())
}

func TestParseJSONResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain JSON",
			input:    `{"summary": "test", "tags": ["a", "b"]}`,
			expected: `{"summary": "test", "tags": ["a", "b"]}`,
		},
		{
			name:     "JSON in markdown code block",
			input:    "Here's the response:\n```json\n{\"summary\": \"test\", \"tags\": []}\n```",
			expected: `{"summary": "test", "tags": []}`,
		},
		{
			name:     "JSON in plain code block",
			input:    "Response:\n```\n{\"summary\": \"test\", \"tags\": []}\n```\nDone.",
			expected: `{"summary": "test", "tags": []}`,
		},
		{
			name:     "JSON with surrounding text",
			input:    "The result is {\"summary\": \"test\"} as requested",
			expected: `{"summary": "test"}`,
		},
		{
			name:     "whitespace around JSON",
			input:    "   {\"summary\": \"test\"}   ",
			expected: `{"summary": "test"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseJSONResponse(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDeduplicateTags(t *testing.T) {
	tests := []struct {
		name         string
		newTags      []string
		existingTags []string
		maxTags      int
		expected     []string
	}{
		{
			name:         "no duplicates",
			newTags:      []string{"rock", "indie", "alternative"},
			existingTags: []string{"pop"},
			maxTags:      5,
			expected:     []string{"rock", "indie", "alternative"},
		},
		{
			name:         "with duplicates case-insensitive",
			newTags:      []string{"Rock", "Indie", "Pop"},
			existingTags: []string{"rock", "pop"},
			maxTags:      5,
			expected:     []string{"Indie"},
		},
		{
			name:         "respects max tags",
			newTags:      []string{"a", "b", "c", "d", "e", "f"},
			existingTags: []string{},
			maxTags:      3,
			expected:     []string{"a", "b", "c"},
		},
		{
			name:         "empty new tags",
			newTags:      []string{},
			existingTags: []string{"rock"},
			maxTags:      5,
			expected:     nil,
		},
		{
			name:         "whitespace handling",
			newTags:      []string{"  rock  ", "indie", "  "},
			existingTags: []string{},
			maxTags:      5,
			expected:     []string{"rock", "indie"},
		},
		{
			name:         "all duplicates",
			newTags:      []string{"Rock", "Pop"},
			existingTags: []string{"rock", "pop"},
			maxTags:      5,
			expected:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deduplicateTags(tt.newTags, tt.existingTags, tt.maxTags)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		ms       int
		expected string
	}{
		{
			name:     "short song",
			ms:       180000, // 3 minutes
			expected: "3:00",
		},
		{
			name:     "with seconds",
			ms:       245000, // 4:05
			expected: "4:05",
		},
		{
			name:     "over an hour",
			ms:       3723000, // 1:02:03
			expected: "1:02:03",
		},
		{
			name:     "zero",
			ms:       0,
			expected: "0:00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.ms)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLoadTemplates(t *testing.T) {
	// Create a temporary directory with test templates
	tmpDir, err := os.MkdirTemp("", "prompts")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create a test template
	artistTmpl := `Artist: {{ .Name }}
{{- if .Bio }}
Bio: {{ .Bio }}
{{- end }}`
	err = os.WriteFile(filepath.Join(tmpDir, "artist.tmpl"), []byte(artistTmpl), 0644)
	require.NoError(t, err)

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.Metadata.AI.PromptsDirectory = tmpDir

	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	e := enricher.(*Enricher)
	assert.Contains(t, e.templates, "artist")
}

func TestEnrichArtist_MockAPI(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		response := llm.ChatResponse{
			Choices: []llm.ChatChoice{
				{
					Index: 0,
					Message: struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					}{
						Role:    "assistant",
						Content: `{"biography": "Test bio", "summary": "Test summary", "tags": ["tag1", "tag2"]}`,
					},
					FinishReason: "stop",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.BaseURL = server.URL
	cfg.OpenAI.Model = "gpt-4o"
	cfg.Metadata.AI.PromptsDirectory = "./nonexistent"

	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	artist := &ent.Artist{
		Name:   "Test Artist",
		Bio:    "Original bio",
		Genres: []string{"rock"},
		Tags:   []string{"indie"},
	}

	artistEnricher := enricher.(enrichers.ArtistEnricher)
	data, err := artistEnricher.EnrichArtist(context.Background(), artist)
	require.NoError(t, err)
	require.NotNil(t, data)

	assert.Equal(t, "Test summary", data.AISummary)
	assert.Equal(t, "Test bio", data.AIBiography)
	assert.Contains(t, data.AITags, "tag1")
	assert.Contains(t, data.AITags, "tag2")
}

func TestEnrichArtist_SkipRecentlyEnriched(t *testing.T) {
	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.Metadata.AI.PromptsDirectory = "./nonexistent"

	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	recent := time.Now().Add(-24 * time.Hour)
	artist := &ent.Artist{
		Name:             "Test Artist",
		LastAiEnrichedAt: &recent,
	}

	artistEnricher := enricher.(enrichers.ArtistEnricher)
	data, err := artistEnricher.EnrichArtist(context.Background(), artist)
	assert.NoError(t, err)
	assert.Nil(t, data)
}

func TestEnrichAlbum_MockAPI(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := llm.ChatResponse{
			Choices: []llm.ChatChoice{
				{
					Index: 0,
					Message: struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					}{
						Role:    "assistant",
						Content: `{"summary": "Great album from 1975", "tags": ["classic rock", "70s"]}`,
					},
					FinishReason: "stop",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.BaseURL = server.URL
	cfg.OpenAI.Model = "gpt-4o"
	cfg.Metadata.AI.PromptsDirectory = "./nonexistent"

	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	album := &ent.Album{
		Name:  "Test Album",
		Year:  1975,
		Genre: "Rock",
		Tags:  []string{"progressive"},
	}

	albumEnricher := enricher.(enrichers.AlbumEnricher)
	data, err := albumEnricher.EnrichAlbum(context.Background(), album)
	require.NoError(t, err)
	require.NotNil(t, data)

	assert.Equal(t, "Great album from 1975", data.AISummary)
	assert.Contains(t, data.AITags, "classic rock")
	assert.Contains(t, data.AITags, "70s")
}

func TestEnrichTrack_MockAPI(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := llm.ChatResponse{
			Choices: []llm.ChatChoice{
				{
					Index: 0,
					Message: struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					}{
						Role:    "assistant",
						Content: `{"summary": "An energetic track", "tags": ["upbeat", "energetic"]}`,
					},
					FinishReason: "stop",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.BaseURL = server.URL
	cfg.OpenAI.Model = "gpt-4o"
	cfg.Metadata.AI.PromptsDirectory = "./nonexistent"

	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	track := &ent.Track{
		Name:   "Test Track",
		Tags:   []string{"rock"},
		Genres: []string{"alternative"},
	}

	trackEnricher := enricher.(enrichers.TrackEnricher)
	data, err := trackEnricher.EnrichTrack(context.Background(), track)
	require.NoError(t, err)
	require.NotNil(t, data)

	assert.Equal(t, "An energetic track", data.AISummary)
	assert.Contains(t, data.AITags, "upbeat")
	assert.Contains(t, data.AITags, "energetic")
}

func TestEnrichArtist_APIError(t *testing.T) {
	// Create mock server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.BaseURL = server.URL
	cfg.OpenAI.Model = "gpt-4o"
	cfg.Metadata.AI.PromptsDirectory = "./nonexistent"

	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	artist := &ent.Artist{
		Name: "Test Artist",
	}

	artistEnricher := enricher.(enrichers.ArtistEnricher)
	_, err = artistEnricher.EnrichArtist(context.Background(), artist)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestGetArtistImages_ReturnsNil(t *testing.T) {
	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.Metadata.AI.PromptsDirectory = "./nonexistent"

	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	artist := &ent.Artist{
		Name: "Test Artist",
	}

	artistEnricher := enricher.(enrichers.ArtistEnricher)
	images, err := artistEnricher.GetArtistImages(context.Background(), artist)
	assert.NoError(t, err)
	assert.Nil(t, images)
}

func TestGetAlbumImages_ReturnsNil(t *testing.T) {
	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.Metadata.AI.PromptsDirectory = "./nonexistent"

	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	album := &ent.Album{
		Name: "Test Album",
	}

	albumEnricher := enricher.(enrichers.AlbumEnricher)
	images, err := albumEnricher.GetAlbumImages(context.Background(), album)
	assert.NoError(t, err)
	assert.Nil(t, images)
}

func TestFallbackPrompts(t *testing.T) {
	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.Metadata.AI.PromptsDirectory = "./nonexistent"

	factory := New(nil, cfg)
	enricher, err := factory(context.Background(), nil)
	require.NoError(t, err)

	e := enricher.(*Enricher)

	t.Run("artist fallback prompt", func(t *testing.T) {
		data := ArtistTemplateData{
			Name:   "The Beatles",
			Bio:    "A famous band",
			Genres: []string{"rock", "pop"},
		}
		prompt := e.fallbackArtistPrompt(data)
		assert.Contains(t, prompt, "The Beatles")
		assert.Contains(t, prompt, "A famous band")
		assert.Contains(t, prompt, "rock, pop")
	})

	t.Run("album fallback prompt", func(t *testing.T) {
		data := AlbumTemplateData{
			Name:   "Abbey Road",
			Artist: "The Beatles",
			Year:   1969,
			Genre:  "Rock",
		}
		prompt := e.fallbackAlbumPrompt(data)
		assert.Contains(t, prompt, "Abbey Road")
		assert.Contains(t, prompt, "The Beatles")
		assert.Contains(t, prompt, "1969")
		assert.Contains(t, prompt, "Rock")
	})

	t.Run("track fallback prompt", func(t *testing.T) {
		data := TrackTemplateData{
			Name:     "Come Together",
			Artist:   "The Beatles",
			Album:    "Abbey Road",
			Duration: "4:20",
		}
		prompt := e.fallbackTrackPrompt(data)
		assert.Contains(t, prompt, "Come Together")
		assert.Contains(t, prompt, "The Beatles")
		assert.Contains(t, prompt, "Abbey Road")
		assert.Contains(t, prompt, "4:20")
	})
}
