package vibes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"spotter/ent"
	"spotter/ent/enttest"
	"spotter/internal/config"
	"spotter/internal/events"
	"spotter/internal/llm"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewArtistSeed(t *testing.T) {
	artist := &ent.Artist{Name: "Test Artist"}
	seed := NewArtistSeed(artist)

	assert.Equal(t, SeedTypeArtist, seed.Type)
	assert.Equal(t, artist, seed.Artist)
	assert.Nil(t, seed.Album)
	assert.Nil(t, seed.Tracks)
}

func TestNewAlbumSeed(t *testing.T) {
	album := &ent.Album{Name: "Test Album"}
	seed := NewAlbumSeed(album)

	assert.Equal(t, SeedTypeAlbum, seed.Type)
	assert.Equal(t, album, seed.Album)
	assert.Nil(t, seed.Artist)
	assert.Nil(t, seed.Tracks)
}

func TestNewTracksSeed(t *testing.T) {
	tracks := []*ent.Track{
		{Name: "Track 1"},
		{Name: "Track 2"},
	}
	seed := NewTracksSeed(tracks)

	assert.Equal(t, SeedTypeTracks, seed.Type)
	assert.Equal(t, tracks, seed.Tracks)
	assert.Nil(t, seed.Artist)
	assert.Nil(t, seed.Album)
}

func TestNewTrackIDsSeed(t *testing.T) {
	trackIDs := []int{1, 2, 3}
	seed := NewTrackIDsSeed(trackIDs)

	assert.Equal(t, SeedTypeTracks, seed.Type)
	assert.Equal(t, trackIDs, seed.TrackIDs)
	assert.Nil(t, seed.Tracks)
}

func TestNormalizeForMatch(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "lowercase conversion",
			input:    "Hello World",
			expected: "hello world",
		},
		{
			name:     "remove remastered suffix",
			input:    "Song Name (remastered)",
			expected: "song name",
		},
		{
			name:     "remove deluxe suffix",
			input:    "Album Title (deluxe)",
			expected: "album title",
		},
		{
			name:     "remove live suffix",
			input:    "Track Name (live)",
			expected: "track name",
		},
		{
			name:     "remove brackets version",
			input:    "Song [remastered]",
			expected: "song",
		},
		{
			name:     "remove special characters",
			input:    "Song! @Name#",
			expected: "song name",
		},
		{
			name:     "preserve numbers",
			input:    "Track 42",
			expected: "track 42",
		},
		{
			name:     "trim whitespace",
			input:    "  Song Name  ",
			expected: "song name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeForMatch(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		minScore float64
		maxScore float64
	}{
		{
			name:     "identical strings",
			a:        "hello",
			b:        "hello",
			minScore: 1.0,
			maxScore: 1.0,
		},
		{
			name:     "completely different",
			a:        "abc",
			b:        "xyz",
			minScore: 0.0,
			maxScore: 0.5,
		},
		{
			name:     "one empty string",
			a:        "hello",
			b:        "",
			minScore: 0.0,
			maxScore: 0.0,
		},
		{
			name:     "both empty strings",
			a:        "",
			b:        "",
			minScore: 1.0,
			maxScore: 1.0,
		},
		{
			name:     "similar strings",
			a:        "hello",
			b:        "hallo",
			minScore: 0.7,
			maxScore: 0.9,
		},
		{
			name:     "case matters after normalize",
			a:        "hello",
			b:        "Hello",
			minScore: 0.7,
			maxScore: 0.9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := similarity(tt.a, tt.b)
			assert.GreaterOrEqual(t, score, tt.minScore, "score should be >= minScore")
			assert.LessOrEqual(t, score, tt.maxScore, "score should be <= maxScore")
		})
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected int
	}{
		{
			name:     "identical",
			a:        "hello",
			b:        "hello",
			expected: 0,
		},
		{
			name:     "one insertion",
			a:        "hell",
			b:        "hello",
			expected: 1,
		},
		{
			name:     "one deletion",
			a:        "hello",
			b:        "helo",
			expected: 1,
		},
		{
			name:     "one substitution",
			a:        "hello",
			b:        "hallo",
			expected: 1,
		},
		{
			name:     "empty first string",
			a:        "",
			b:        "hello",
			expected: 5,
		},
		{
			name:     "empty second string",
			a:        "hello",
			b:        "",
			expected: 5,
		},
		{
			name:     "completely different",
			a:        "abc",
			b:        "xyz",
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := levenshtein(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseJSONResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "raw JSON",
			input:    `{"tracks": []}`,
			expected: `{"tracks": []}`,
		},
		{
			name:     "JSON in markdown code block",
			input:    "```json\n{\"tracks\": []}\n```",
			expected: `{"tracks": []}`,
		},
		{
			name:     "JSON in plain code block",
			input:    "```\n{\"tracks\": []}\n```",
			expected: `{"tracks": []}`,
		},
		{
			name:     "JSON with surrounding text",
			input:    "Here is the response:\n{\"tracks\": []}\n\nThat's all!",
			expected: `{"tracks": []}`,
		},
		{
			name:     "nested JSON",
			input:    `{"outer": {"inner": "value"}}`,
			expected: `{"outer": {"inner": "value"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseJSONResponse(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerationResult_GetMatchedTrackIDs(t *testing.T) {
	result := &GenerationResult{
		Tracks: []GeneratedTrack{
			{ID: 1, Matched: true},
			{ID: 2, Matched: false},
			{ID: 3, Matched: true},
			{ID: 4, Matched: false},
			{ID: 5, Matched: true},
		},
		MatchedCount: 3,
	}

	ids := result.GetMatchedTrackIDs()
	assert.Equal(t, []int{1, 3, 5}, ids)
}

func TestGenerationResult_GetMatchedTrackIDsAsStrings(t *testing.T) {
	result := &GenerationResult{
		Tracks: []GeneratedTrack{
			{ID: 10, Matched: true},
			{ID: 20, Matched: false},
			{ID: 30, Matched: true},
		},
		MatchedCount: 2,
	}

	ids := result.GetMatchedTrackIDsAsStrings()
	assert.Equal(t, []string{"10", "30"}, ids)
}

func TestMixtapeGenerator_ValidateRequest(t *testing.T) {
	cfg := &config.Config{}
	cfg.Vibes.DefaultMaxTracks = 25
	cfg.OpenAI.APIKey = "test-key"

	g := NewMixtapeGenerator(nil, cfg, nil, nil)

	tests := []struct {
		name        string
		req         *GenerationRequest
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid request",
			req: &GenerationRequest{
				Mixtape: &ent.Mixtape{},
				DJ:      &ent.DJ{Name: "Test DJ"},
				UserID:  1,
			},
			expectError: false,
		},
		{
			name: "missing mixtape",
			req: &GenerationRequest{
				DJ:     &ent.DJ{Name: "Test DJ"},
				UserID: 1,
			},
			expectError: true,
			errorMsg:    "mixtape is required",
		},
		{
			name: "missing DJ",
			req: &GenerationRequest{
				Mixtape: &ent.Mixtape{},
				UserID:  1,
			},
			expectError: true,
			errorMsg:    "DJ is required",
		},
		{
			name: "invalid user ID",
			req: &GenerationRequest{
				Mixtape: &ent.Mixtape{},
				DJ:      &ent.DJ{Name: "Test DJ"},
				UserID:  0,
			},
			expectError: true,
			errorMsg:    "valid user ID is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := g.validateRequest(tt.req)
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMixtapeGenerator_FallbackPrompt(t *testing.T) {
	cfg := &config.Config{}
	cfg.Vibes.DefaultMaxTracks = 10

	g := NewMixtapeGenerator(nil, cfg, nil, nil)

	data := &TemplateData{
		DJName:         "DJ Test",
		DJSystemPrompt: "You love electronic music",
		MaxTracks:      5,
		AvailableTracks: []AvailableTrack{
			{ID: 1, Name: "Track One", Artist: "Artist A"},
			{ID: 2, Name: "Track Two", Artist: "Artist B"},
		},
	}

	prompt := g.fallbackPrompt(data)

	assert.Contains(t, prompt, "DJ Test")
	assert.Contains(t, prompt, "electronic music")
	assert.Contains(t, prompt, "5 tracks")
	assert.Contains(t, prompt, "Track One")
	assert.Contains(t, prompt, "Artist A")
	assert.Contains(t, prompt, "Track Two")
	assert.Contains(t, prompt, "Artist B")
	assert.Contains(t, prompt, "JSON")
}

func TestMixtapeGenerator_ParseAIResponse(t *testing.T) {
	cfg := &config.Config{}
	g := NewMixtapeGenerator(nil, cfg, nil, nil)

	t.Run("valid response", func(t *testing.T) {
		response := `{
			"tracks": [
				{"id": "1", "name": "Song One", "artist": "Artist A", "reason": "Great opener"},
				{"id": "2", "name": "Song Two", "artist": "Artist B", "reason": "Builds energy"}
			],
			"flow_description": "An energetic journey",
			"opening_thoughts": "Let's get started!",
			"closing_thoughts": "Thanks for listening!"
		}`

		result, err := g.parseAIResponse(response)
		require.NoError(t, err)
		assert.Len(t, result.Tracks, 2)
		assert.Equal(t, "1", result.Tracks[0].ID)
		assert.Equal(t, "Song One", result.Tracks[0].Name)
		assert.Equal(t, "Artist A", result.Tracks[0].Artist)
		assert.Equal(t, "An energetic journey", result.FlowDescription)
	})

	t.Run("response in markdown block", func(t *testing.T) {
		response := "```json\n{\"tracks\": [], \"flow_description\": \"test\", \"opening_thoughts\": \"\", \"closing_thoughts\": \"\"}\n```"

		result, err := g.parseAIResponse(response)
		require.NoError(t, err)
		assert.Equal(t, "test", result.FlowDescription)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		response := "This is not JSON at all"

		_, err := g.parseAIResponse(response)
		assert.Error(t, err)
	})
}

func TestMixtapeGenerator_FindBestFuzzyMatch(t *testing.T) {
	cfg := &config.Config{}
	g := NewMixtapeGenerator(nil, cfg, nil, nil)

	candidates := []AvailableTrack{
		{ID: 1, Name: "Bohemian Rhapsody", Artist: "Queen"},
		{ID: 2, Name: "Stairway to Heaven", Artist: "Led Zeppelin"},
		{ID: 3, Name: "Hotel California", Artist: "Eagles"},
		{ID: 4, Name: "Imagine", Artist: "John Lennon"},
	}

	t.Run("exact match", func(t *testing.T) {
		match, confidence := g.findBestFuzzyMatch("Bohemian Rhapsody", "Queen", candidates)
		require.NotNil(t, match)
		assert.Equal(t, 1, match.ID)
		assert.Equal(t, 1.0, confidence)
	})

	t.Run("close match with typo", func(t *testing.T) {
		match, confidence := g.findBestFuzzyMatch("Bohemian Rapsody", "Queen", candidates)
		require.NotNil(t, match)
		assert.Equal(t, 1, match.ID)
		assert.Greater(t, confidence, 0.8)
	})

	t.Run("match with different casing", func(t *testing.T) {
		match, confidence := g.findBestFuzzyMatch("IMAGINE", "JOHN LENNON", candidates)
		require.NotNil(t, match)
		assert.Equal(t, 4, match.ID)
		assert.Equal(t, 1.0, confidence)
	})

	t.Run("partial artist match", func(t *testing.T) {
		match, confidence := g.findBestFuzzyMatch("Stairway to Heaven", "Led Zep", candidates)
		require.NotNil(t, match)
		assert.Equal(t, 2, match.ID)
		assert.Greater(t, confidence, 0.7)
	})
}

func TestMixtapeGenerator_MatchTracksToLibrary(t *testing.T) {
	cfg := &config.Config{}
	cfg.Vibes.MinMatchConfidence = 0.7

	g := NewMixtapeGenerator(nil, cfg, nil, nil)

	available := []AvailableTrack{
		{ID: 100, Name: "Song Alpha", Artist: "Artist One"},
		{ID: 200, Name: "Song Beta", Artist: "Artist Two"},
		{ID: 300, Name: "Song Gamma", Artist: "Artist Three"},
	}

	t.Run("exact ID match", func(t *testing.T) {
		aiResp := &AIResponse{
			Tracks: []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Artist string `json:"artist"`
				Reason string `json:"reason"`
			}{
				{ID: "100", Name: "Song Alpha", Artist: "Artist One", Reason: "Great track"},
			},
		}

		result, err := g.matchTracksToLibrary(context.Background(), 1, aiResp, available)
		require.NoError(t, err)
		assert.Equal(t, 1, result.MatchedCount)
		assert.Equal(t, 0, result.UnmatchedCount)
		assert.True(t, result.Tracks[0].Matched)
		assert.Equal(t, 100, result.Tracks[0].ID)
		assert.Equal(t, 1.0, result.Tracks[0].MatchConfidence)
	})

	t.Run("exact name match", func(t *testing.T) {
		aiResp := &AIResponse{
			Tracks: []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Artist string `json:"artist"`
				Reason string `json:"reason"`
			}{
				{ID: "unknown", Name: "Song Beta", Artist: "Artist Two", Reason: "Nice"},
			},
		}

		result, err := g.matchTracksToLibrary(context.Background(), 1, aiResp, available)
		require.NoError(t, err)
		assert.Equal(t, 1, result.MatchedCount)
		assert.True(t, result.Tracks[0].Matched)
		assert.Equal(t, 200, result.Tracks[0].ID)
	})

	t.Run("fuzzy match", func(t *testing.T) {
		aiResp := &AIResponse{
			Tracks: []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Artist string `json:"artist"`
				Reason string `json:"reason"`
			}{
				{ID: "x", Name: "Song Gama", Artist: "Artist 3", Reason: "Close"},
			},
		}

		result, err := g.matchTracksToLibrary(context.Background(), 1, aiResp, available)
		require.NoError(t, err)
		// Should match Song Gamma via fuzzy matching
		assert.Equal(t, 1, result.MatchedCount)
		assert.True(t, result.Tracks[0].Matched)
		assert.Equal(t, 300, result.Tracks[0].ID)
	})

	t.Run("no match found", func(t *testing.T) {
		aiResp := &AIResponse{
			Tracks: []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Artist string `json:"artist"`
				Reason string `json:"reason"`
			}{
				{ID: "x", Name: "Completely Different Song", Artist: "Unknown Artist", Reason: "Test"},
			},
		}

		result, err := g.matchTracksToLibrary(context.Background(), 1, aiResp, available)
		require.NoError(t, err)
		assert.Equal(t, 0, result.MatchedCount)
		assert.Equal(t, 1, result.UnmatchedCount)
		assert.False(t, result.Tracks[0].Matched)
	})
}

func TestMixtapeGenerator_Integration(t *testing.T) {
	// Create a mock OpenAI server
	mockResponse := llm.ChatResponse{
		ID:      "test-id",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "gpt-4o",
		Choices: []llm.ChatChoice{
			{
				Index: 0,
				Message: struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				}{
					Role: "assistant",
					Content: `{
						"tracks": [
							{"id": "1", "name": "Test Track", "artist": "Test Artist", "reason": "Perfect fit"}
						],
						"flow_description": "A smooth journey",
						"opening_thoughts": "Welcome to the mix!",
						"closing_thoughts": "Thanks for listening!"
					}`,
				},
				FinishReason: "stop",
			},
		},
		Usage: &llm.ChatUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify authorization header
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-api-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	// Create test database
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	defer client.Close()

	// Create test user
	ctx := context.Background()
	user, err := client.User.Create().
		SetUsername("testuser").
		Save(ctx)
	require.NoError(t, err)

	// Create test artist
	artist, err := client.Artist.Create().
		SetName("Test Artist").
		SetUser(user).
		Save(ctx)
	require.NoError(t, err)

	// Create test track
	_, err = client.Track.Create().
		SetName("Test Track").
		SetArtist(artist).
		Save(ctx)
	require.NoError(t, err)

	// Create test DJ
	dj, err := client.DJ.Create().
		SetName("DJ Test").
		SetSystemPrompt("You are a cool DJ").
		SetUser(user).
		Save(ctx)
	require.NoError(t, err)

	// Create test mixtape
	mixtape, err := client.Mixtape.Create().
		SetName("Test Mixtape").
		SetMaxTracks(10).
		SetUser(user).
		SetDj(dj).
		Save(ctx)
	require.NoError(t, err)

	// Create config pointing to mock server
	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-api-key"
	cfg.OpenAI.BaseURL = server.URL
	cfg.OpenAI.Model = "gpt-4o"
	cfg.Vibes.DefaultMaxTracks = 25
	cfg.Vibes.MinMatchConfidence = 0.7
	cfg.Vibes.Temperature = 0.8
	cfg.Vibes.MaxTokens = 4000
	cfg.Vibes.TimeoutSeconds = 30
	cfg.Vibes.HistoryDays = 30
	cfg.Vibes.MaxHistoryTracks = 50

	bus := events.NewBus()
	g := NewMixtapeGenerator(client, cfg, nil, bus)

	req := &GenerationRequest{
		Mixtape: mixtape,
		DJ:      dj,
		UserID:  user.ID,
	}

	result, err := g.GenerateMixtape(ctx, req)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotEmpty(t, result.PromptUsed)
	assert.Equal(t, "gpt-4o", result.ModelUsed)
	assert.Equal(t, 150, result.TokensUsed)
	assert.Equal(t, "A smooth journey", result.FlowDescription)
	assert.Equal(t, "Welcome to the mix!", result.OpeningThoughts)
	assert.Len(t, result.Tracks, 1)
}

func TestMixtapeGenerator_OpenAIError(t *testing.T) {
	// Create a mock server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": {"message": "Internal server error", "type": "server_error"}}`))
	}))
	defer server.Close()

	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	defer client.Close()

	ctx := context.Background()
	user, _ := client.User.Create().SetUsername("testuser").Save(ctx)
	artist, _ := client.Artist.Create().SetName("Artist").SetUser(user).Save(ctx)
	_, _ = client.Track.Create().SetName("Track").SetArtist(artist).Save(ctx)
	dj, _ := client.DJ.Create().SetName("DJ").SetUser(user).Save(ctx)
	mixtape, _ := client.Mixtape.Create().SetName("Mix").SetMaxTracks(10).SetUser(user).SetDj(dj).Save(ctx)

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.BaseURL = server.URL
	cfg.Vibes.DefaultMaxTracks = 25
	cfg.Vibes.TimeoutSeconds = 5

	g := NewMixtapeGenerator(client, cfg, nil, nil)

	req := &GenerationRequest{
		Mixtape: mixtape,
		DJ:      dj,
		UserID:  user.ID,
	}

	_, err := g.GenerateMixtape(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestMixtapeGenerator_NoAPIKey(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	defer client.Close()

	ctx := context.Background()
	user, _ := client.User.Create().SetUsername("testuser").Save(ctx)
	dj, _ := client.DJ.Create().SetName("DJ").SetUser(user).Save(ctx)
	mixtape, _ := client.Mixtape.Create().SetName("Mix").SetMaxTracks(10).SetUser(user).SetDj(dj).Save(ctx)

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "" // No API key

	g := NewMixtapeGenerator(client, cfg, nil, nil)

	req := &GenerationRequest{
		Mixtape: mixtape,
		DJ:      dj,
		UserID:  user.ID,
	}

	_, err := g.GenerateMixtape(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestMixtapeGenerator_NoTracks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This shouldn't be called since we should fail before API call
		t.Error("API should not be called when no tracks available")
	}))
	defer server.Close()

	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	defer client.Close()

	ctx := context.Background()
	user, _ := client.User.Create().SetUsername("testuser").Save(ctx)
	// No artists or tracks created
	dj, _ := client.DJ.Create().SetName("DJ").SetUser(user).Save(ctx)
	mixtape, _ := client.Mixtape.Create().SetName("Mix").SetMaxTracks(10).SetUser(user).SetDj(dj).Save(ctx)

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.BaseURL = server.URL
	cfg.Vibes.DefaultMaxTracks = 25

	g := NewMixtapeGenerator(client, cfg, nil, nil)

	req := &GenerationRequest{
		Mixtape: mixtape,
		DJ:      dj,
		UserID:  user.ID,
	}

	_, err := g.GenerateMixtape(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no tracks available")
}

func TestTemplateData_Structure(t *testing.T) {
	data := &TemplateData{
		DJName:         "Test DJ",
		DJSystemPrompt: "You are energetic",
		GenresInclude:  []string{"rock", "pop"},
		GenresExclude:  []string{"country"},
		Vibes:          []string{"upbeat", "party"},
		ArtistsInclude: []string{"Queen"},
		ArtistsExclude: []string{"Nickelback"},
		SeedType:       SeedTypeArtist,
		SeedArtist: &SeedArtistData{
			Name:      "The Beatles",
			Genres:    []string{"rock", "pop"},
			Bio:       "Legendary band",
			AISummary: "One of the most influential bands",
		},
		ListeningHistory: []HistoryEntry{
			{TrackName: "Yesterday", ArtistName: "The Beatles", PlayCount: 5},
		},
		AvailableTracks: []AvailableTrack{
			{ID: 1, Name: "Help!", Artist: "The Beatles"},
		},
		MixtapeName:        "Beatles Vibes",
		MixtapeDescription: "A tribute to the Fab Four",
		MaxTracks:          20,
	}

	assert.Equal(t, "Test DJ", data.DJName)
	assert.Len(t, data.GenresInclude, 2)
	assert.Equal(t, SeedTypeArtist, data.SeedType)
	assert.NotNil(t, data.SeedArtist)
	assert.Len(t, data.ListeningHistory, 1)
	assert.Len(t, data.AvailableTracks, 1)
}

func TestMinInt(t *testing.T) {
	assert.Equal(t, 1, minInt(1, 2))
	assert.Equal(t, 1, minInt(2, 1))
	assert.Equal(t, 5, minInt(5, 5))
	assert.Equal(t, -1, minInt(-1, 0))
	assert.Equal(t, -2, minInt(-1, -2))
}
