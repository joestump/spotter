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

// oneTrackResponse is an AI response that suggests a single external track.
func oneTrackResponse(name, artist string) *AIResponse {
	return &AIResponse{
		Tracks: []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Artist string `json:"artist"`
			Reason string `json:"reason"`
		}{
			{ID: "external-id", Name: name, Artist: artist, Reason: "test"},
		},
	}
}

// The generator no longer carries its own scorer: every suggestion is scored by
// internal/fuzzy, the same stack the playlist-sync matcher uses.
// Governing: ADR-0014, SPEC vibes-ai-mixtape-engine REQ-VIBES-016, issue #340
func TestMixtapeGenerator_FuzzyMatchUsesSharedStack(t *testing.T) {
	cfg := &config.Config{}
	cfg.Vibes.MinMatchConfidence = 0.7

	g := NewMixtapeGenerator(nil, cfg, nil, nil)

	available := []AvailableTrack{
		{ID: 1, Name: "Bohemian Rhapsody", Artist: "Queen"},
		{ID: 2, Name: "Stairway to Heaven", Artist: "Led Zeppelin"},
		{ID: 3, Name: "Hotel California (Remastered)", Artist: "Eagles"},
		{ID: 4, Name: "Imagine", Artist: "John Lennon"},
		{ID: 5, Name: "夜に駆ける", Artist: "YOASOBI"},
	}

	suggest := oneTrackResponse

	cases := []struct {
		name            string
		suggestedName   string
		suggestedArtist string
		wantID          int
		wantConfidence  float64
	}{
		{
			name:            "close match with typo",
			suggestedName:   "Bohemian Rapsody",
			suggestedArtist: "Queen",
			wantID:          1,
		},
		{
			name:            "match with different casing",
			suggestedName:   "IMAGINE",
			suggestedArtist: "JOHN LENNON",
			wantID:          4,
			wantConfidence:  1.0,
		},
		{
			name:            "partial artist match",
			suggestedName:   "Stairway to Heaven",
			suggestedArtist: "Led Zep",
			wantID:          2,
		},
		{
			name:            "edition suffix is ignored",
			suggestedName:   "Hotel California",
			suggestedArtist: "Eagles",
			wantID:          3,
			wantConfidence:  1.0,
		},
		{
			// The generator's own copy normalized non-ASCII away, so a CJK
			// library title could never be matched from the vibes page.
			name:            "non-ascii library title matches",
			suggestedName:   "夜に駆ける (Live)",
			suggestedArtist: "YOASOBI",
			wantID:          5,
			wantConfidence:  1.0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, err := g.matchTracksToLibrary(context.Background(), 1, suggest(c.suggestedName, c.suggestedArtist), available)
			require.NoError(t, err)
			require.Equal(t, 1, result.MatchedCount, "expected the suggestion to match")
			assert.True(t, result.Tracks[0].Matched)
			assert.Equal(t, c.wantID, result.Tracks[0].ID)
			if c.wantConfidence > 0 {
				assert.InDelta(t, c.wantConfidence, result.Tracks[0].MatchConfidence, 1e-9)
			} else {
				assert.Greater(t, result.Tracks[0].MatchConfidence, 0.7)
			}
		})
	}
}

// The generator's own normalization stripped non-ASCII letters, so two
// different CJK titles both normalized to "" and scored as identical: the
// wrong track could fill the slot. The shared stack compares the runes.
// Governing: ADR-0014, issue #330 (rune units), issue #340 (one stack)
func TestMixtapeGenerator_FuzzyMatchDistinguishesNonASCIITitles(t *testing.T) {
	cfg := &config.Config{}
	cfg.Vibes.MinMatchConfidence = 0.7

	g := NewMixtapeGenerator(nil, cfg, nil, nil)

	available := []AvailableTrack{
		{ID: 5, Name: "夜に駆ける", Artist: "YOASOBI"},
		{ID: 6, Name: "群青", Artist: "YOASOBI"},
	}

	result, err := g.matchTracksToLibrary(context.Background(), 1, oneTrackResponse("群青 (Live)", "YOASOBI"), available)
	require.NoError(t, err)
	require.Equal(t, 1, result.MatchedCount, "expected the suggestion to match")
	assert.Equal(t, 6, result.Tracks[0].ID, "a different CJK title must not match")
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
