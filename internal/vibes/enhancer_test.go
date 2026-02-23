package vibes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"spotter/ent/enttest"
	"spotter/internal/config"
	"spotter/internal/events"
	"spotter/internal/llm"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnhancementModes(t *testing.T) {
	assert.Equal(t, EnhancementMode("one_time"), EnhancementModeOneTime)
	assert.Equal(t, EnhancementMode("convert_to_mixtape"), EnhancementModeConvertToMixtape)
}

func TestNewPlaylistEnhancer(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	cfg := &config.Config{}
	cfg.Vibes.TimeoutSeconds = 30
	cfg.Vibes.PromptsDirectory = "../../data/prompts"

	bus := events.NewBus()

	enhancer := NewPlaylistEnhancer(client, cfg, nil, bus)
	assert.NotNil(t, enhancer)
	assert.NotNil(t, enhancer.llm)
	assert.NotNil(t, enhancer.templates)
}

func TestNewPlaylistEnhancer_NilLogger(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	cfg := &config.Config{}
	enhancer := NewPlaylistEnhancer(client, cfg, nil, nil)

	assert.NotNil(t, enhancer)
	assert.NotNil(t, enhancer.logger)
}

func TestValidateRequest(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	cfg := &config.Config{}
	enhancer := NewPlaylistEnhancer(client, cfg, nil, nil)

	tests := []struct {
		name    string
		req     *EnhancementRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid request - one_time",
			req: &EnhancementRequest{
				PlaylistID: 1,
				DJID:       1,
				Mode:       EnhancementModeOneTime,
				UserID:     1,
			},
			wantErr: false,
		},
		{
			name: "valid request - convert_to_mixtape",
			req: &EnhancementRequest{
				PlaylistID: 1,
				DJID:       1,
				Mode:       EnhancementModeConvertToMixtape,
				UserID:     1,
			},
			wantErr: false,
		},
		{
			name: "missing playlist ID",
			req: &EnhancementRequest{
				PlaylistID: 0,
				DJID:       1,
				Mode:       EnhancementModeOneTime,
				UserID:     1,
			},
			wantErr: true,
			errMsg:  "playlist ID is required",
		},
		{
			name: "missing DJ ID",
			req: &EnhancementRequest{
				PlaylistID: 1,
				DJID:       0,
				Mode:       EnhancementModeOneTime,
				UserID:     1,
			},
			wantErr: true,
			errMsg:  "DJ ID is required",
		},
		{
			name: "missing user ID",
			req: &EnhancementRequest{
				PlaylistID: 1,
				DJID:       1,
				Mode:       EnhancementModeOneTime,
				UserID:     0,
			},
			wantErr: true,
			errMsg:  "user ID is required",
		},
		{
			name: "invalid mode",
			req: &EnhancementRequest{
				PlaylistID: 1,
				DJID:       1,
				Mode:       "invalid_mode",
				UserID:     1,
			},
			wantErr: true,
			errMsg:  "invalid enhancement mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := enhancer.validateRequest(tt.req)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "clean JSON",
			input:    `{"key": "value"}`,
			expected: `{"key": "value"}`,
		},
		{
			name:     "JSON with preamble",
			input:    `Here is the result:\n{"key": "value"}`,
			expected: `{"key": "value"}`,
		},
		{
			name:     "JSON with postamble",
			input:    `{"key": "value"}\nThat's the result!`,
			expected: `{"key": "value"}`,
		},
		{
			name:     "nested JSON",
			input:    `{"outer": {"inner": "value"}}`,
			expected: `{"outer": {"inner": "value"}}`,
		},
		{
			name:     "no JSON",
			input:    `This has no JSON`,
			expected: "",
		},
		{
			name:     "unclosed JSON",
			input:    `{"key": "value"`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractJSON(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseAIResponse(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	cfg := &config.Config{}
	enhancer := NewPlaylistEnhancer(client, cfg, nil, nil)

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name: "valid response",
			input: `{
				"reordered_tracks": [
					{"id": "EXISTING:1", "position": 1, "reason": "opener"},
					{"id": "EXISTING:2", "position": 2, "reason": "buildup"}
				],
				"new_tracks": [
					{"id": "ADD:3", "position": 3, "reason": "fits the vibe"}
				],
				"flow_description": "A smooth journey",
				"enhancement_summary": "Reordered for better flow",
				"opening_thoughts": "Let's vibe"
			}`,
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			input:   `not json at all`,
			wantErr: true,
		},
		{
			name: "response with markdown code block",
			input: "```json\n" + `{
				"reordered_tracks": [],
				"new_tracks": [],
				"flow_description": "test",
				"enhancement_summary": "test",
				"opening_thoughts": "test"
			}` + "\n```",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := enhancer.parseAIResponse(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, resp)
			}
		})
	}
}

func TestBuildResult(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	cfg := &config.Config{}
	enhancer := NewPlaylistEnhancer(client, cfg, nil, nil)

	existingTracks := []ExistingTrack{
		{ID: 1, Name: "Track 1", Artist: "Artist 1"},
		{ID: 2, Name: "Track 2", Artist: "Artist 2"},
	}

	availableTracks := []AvailableTrack{
		{ID: 3, Name: "Track 3", Artist: "Artist 3"},
		{ID: 4, Name: "Track 4", Artist: "Artist 4"},
	}

	aiResp := &EnhancementAIResponse{
		ReorderedTracks: []struct {
			ID       string `json:"id"`
			Position int    `json:"position"`
			Reason   string `json:"reason"`
		}{
			{ID: "EXISTING:2", Position: 1, Reason: "energy builder"},
			{ID: "EXISTING:1", Position: 2, Reason: "peak track"},
		},
		NewTracks: []struct {
			ID       string `json:"id"`
			Position int    `json:"position"`
			Reason   string `json:"reason"`
		}{
			{ID: "ADD:3", Position: 3, Reason: "perfect closer"},
		},
		FlowDescription:    "Smooth build to peak",
		EnhancementSummary: "Reordered and added one track",
		OpeningThoughts:    "This is gonna be good",
	}

	result, err := enhancer.buildResult(context.Background(), aiResp, existingTracks, availableTracks)
	require.NoError(t, err)

	assert.Equal(t, "Smooth build to peak", result.FlowDescription)
	assert.Equal(t, "Reordered and added one track", result.EnhancementSummary)
	assert.Equal(t, "This is gonna be good", result.OpeningThoughts)
	assert.Equal(t, 3, result.FinalTrackCount)
	assert.Equal(t, 1, result.TracksAdded)
	assert.Len(t, result.NewTracks, 1)
	assert.Equal(t, 3, result.NewTracks[0].InternalID)
}

func TestBuildResult_PlainIDs(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	cfg := &config.Config{}
	enhancer := NewPlaylistEnhancer(client, cfg, nil, nil)

	existingTracks := []ExistingTrack{
		{ID: 1, Name: "Track 1", Artist: "Artist 1"},
	}

	availableTracks := []AvailableTrack{
		{ID: 2, Name: "Track 2", Artist: "Artist 2"},
	}

	// AI sometimes returns plain numeric IDs instead of prefixed
	aiResp := &EnhancementAIResponse{
		ReorderedTracks: []struct {
			ID       string `json:"id"`
			Position int    `json:"position"`
			Reason   string `json:"reason"`
		}{
			{ID: "1", Position: 1, Reason: "opener"},
		},
		NewTracks: []struct {
			ID       string `json:"id"`
			Position int    `json:"position"`
			Reason   string `json:"reason"`
		}{
			{ID: "2", Position: 2, Reason: "addition"},
		},
		FlowDescription:    "Test",
		EnhancementSummary: "Test",
		OpeningThoughts:    "Test",
	}

	result, err := enhancer.buildResult(context.Background(), aiResp, existingTracks, availableTracks)
	require.NoError(t, err)

	// Should still match tracks even with plain IDs
	assert.Equal(t, 2, result.FinalTrackCount)
	assert.Equal(t, 1, result.TracksAdded)
}

func TestEnhancementResult_GetAllTrackIDs(t *testing.T) {
	result := &EnhancementResult{
		ReorderedTracks: []EnhancedTrack{
			{InternalID: 1, Matched: true},
			{InternalID: 2, Matched: true},
			{InternalID: 0, Matched: false}, // unmatched
			{InternalID: 3, Matched: true},
		},
	}

	ids := result.GetAllTrackIDs()
	assert.Equal(t, []int{1, 2, 3}, ids)
}

func TestEnhancementResult_GetAllTrackIDsAsStrings(t *testing.T) {
	result := &EnhancementResult{
		ReorderedTracks: []EnhancedTrack{
			{InternalID: 10, Matched: true},
			{InternalID: 20, Matched: true},
			{InternalID: 0, Matched: false},
		},
	}

	ids := result.GetAllTrackIDsAsStrings()
	assert.Equal(t, []string{"10", "20"}, ids)
}

func TestFallbackPrompt(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	cfg := &config.Config{}
	enhancer := NewPlaylistEnhancer(client, cfg, nil, nil)

	data := &EnhancementTemplateData{
		DJName:       "DJ Test",
		MaxNewTracks: 5,
		ExistingTracks: []ExistingTrack{
			{ID: 1, Name: "Song 1", Artist: "Artist 1"},
			{ID: 2, Name: "Song 2", Artist: "Artist 2"},
		},
		AvailableTracks: []AvailableTrack{
			{ID: 3, Name: "Song 3", Artist: "Artist 3"},
		},
	}

	prompt := enhancer.fallbackPrompt(data)

	assert.Contains(t, prompt, "DJ Test")
	assert.Contains(t, prompt, "5 new tracks")
	assert.Contains(t, prompt, "[EXISTING:1] Song 1 by Artist 1")
	assert.Contains(t, prompt, "[EXISTING:2] Song 2 by Artist 2")
	assert.Contains(t, prompt, "[ADD:3] Song 3 by Artist 3")
	assert.Contains(t, prompt, "reordered_tracks")
}

func TestEnhancePlaylist_NoOpenAIKey(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "" // No API key

	bus := events.NewBus()
	enhancer := NewPlaylistEnhancer(client, cfg, nil, bus)

	// Create test data
	ctx := context.Background()
	u, err := client.User.Create().
		SetUsername("testuser").
		SetPaginationSize(25).
		Save(ctx)
	require.NoError(t, err)

	d, err := client.DJ.Create().
		SetName("Test DJ").
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	pl, err := client.Playlist.Create().
		SetName("Test Playlist").
		SetRemoteID("remote-1").
		SetSource("navidrome").
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	req := &EnhancementRequest{
		PlaylistID: pl.ID,
		DJID:       d.ID,
		Mode:       EnhancementModeOneTime,
		UserID:     u.ID,
	}

	_, err = enhancer.EnhancePlaylist(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "OpenAI API key not configured")
}

func TestEnhancePlaylist_EmptyPlaylist(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.BaseURL = "http://localhost:8080"

	bus := events.NewBus()
	enhancer := NewPlaylistEnhancer(client, cfg, nil, bus)

	ctx := context.Background()
	u, err := client.User.Create().
		SetUsername("testuser").
		SetPaginationSize(25).
		Save(ctx)
	require.NoError(t, err)

	d, err := client.DJ.Create().
		SetName("Test DJ").
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	pl, err := client.Playlist.Create().
		SetName("Empty Playlist").
		SetRemoteID("remote-1").
		SetSource("navidrome").
		SetTrackCount(0).
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	req := &EnhancementRequest{
		PlaylistID: pl.ID,
		DJID:       d.ID,
		Mode:       EnhancementModeOneTime,
		UserID:     u.ID,
	}

	_, err = enhancer.EnhancePlaylist(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "playlist has no tracks")
}

func TestEnhancePlaylist_Success(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Mock OpenAI server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := llm.ChatResponse{
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
							"reordered_tracks": [
								{"id": "EXISTING:1", "position": 1, "reason": "opener"}
							],
							"new_tracks": [],
							"flow_description": "A smooth journey",
							"enhancement_summary": "Optimized the flow",
							"opening_thoughts": "This playlist rocks"
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
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.BaseURL = mockServer.URL
	cfg.OpenAI.Model = "gpt-4o"
	cfg.Vibes.MaxTokens = 4000
	cfg.Vibes.Temperature = 0.8
	cfg.Vibes.HistoryDays = 30
	cfg.Vibes.MaxHistoryTracks = 50

	bus := events.NewBus()
	enhancer := NewPlaylistEnhancer(client, cfg, nil, bus)

	ctx := context.Background()

	// Create test user
	u, err := client.User.Create().
		SetUsername("testuser").
		SetPaginationSize(25).
		Save(ctx)
	require.NoError(t, err)

	// Create test DJ
	d, err := client.DJ.Create().
		SetName("Test DJ").
		SetSystemPrompt("You are a chill DJ").
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	// Create test artist and track
	artist, err := client.Artist.Create().
		SetName("Test Artist").
		SetNavidromeID("nd-artist-1").
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	track, err := client.Track.Create().
		SetName("Test Track").
		SetNavidromeID("nd-track-1").
		SetDurationMs(180000).
		SetArtist(artist).
		Save(ctx)
	require.NoError(t, err)

	// Create test playlist with track
	pl, err := client.Playlist.Create().
		SetName("Test Playlist").
		SetRemoteID("remote-1").
		SetSource("navidrome").
		SetTrackCount(1).
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	// Add track to playlist
	_, err = client.PlaylistTrack.Create().
		SetPlaylist(pl).
		SetTrack(track).
		SetPosition(1).
		SetTrackName("Test Track").
		SetArtistName("Test Artist").
		SetDurationMs(180000).
		Save(ctx)
	require.NoError(t, err)

	req := &EnhancementRequest{
		PlaylistID:   pl.ID,
		DJID:         d.ID,
		Mode:         EnhancementModeOneTime,
		MaxNewTracks: 5,
		UserID:       u.ID,
	}

	result, err := enhancer.EnhancePlaylist(ctx, req)
	require.NoError(t, err)

	assert.NotNil(t, result)
	assert.Equal(t, "A smooth journey", result.FlowDescription)
	assert.Equal(t, "Optimized the flow", result.EnhancementSummary)
	assert.Equal(t, "This playlist rocks", result.OpeningThoughts)
	assert.Equal(t, "gpt-4o", result.ModelUsed)
	assert.Equal(t, 150, result.TokensUsed)
	assert.Equal(t, 1, result.OriginalTrackCount)
}

func TestEnhancePlaylist_APIError(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Mock server that returns an error
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{
				"message": "Internal server error",
			},
		})
	}))
	defer mockServer.Close()

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.BaseURL = mockServer.URL
	cfg.OpenAI.Model = "gpt-4o"
	cfg.Vibes.MaxTokens = 4000
	cfg.Vibes.Temperature = 0.8

	bus := events.NewBus()
	enhancer := NewPlaylistEnhancer(client, cfg, nil, bus)

	ctx := context.Background()

	u, err := client.User.Create().
		SetUsername("testuser").
		SetPaginationSize(25).
		Save(ctx)
	require.NoError(t, err)

	d, err := client.DJ.Create().
		SetName("Test DJ").
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	artist, err := client.Artist.Create().
		SetName("Test Artist").
		SetNavidromeID("nd-artist-1").
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	track, err := client.Track.Create().
		SetName("Test Track").
		SetNavidromeID("nd-track-1").
		SetDurationMs(180000).
		SetArtist(artist).
		Save(ctx)
	require.NoError(t, err)

	pl, err := client.Playlist.Create().
		SetName("Test Playlist").
		SetRemoteID("remote-1").
		SetSource("navidrome").
		SetTrackCount(1).
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.PlaylistTrack.Create().
		SetPlaylist(pl).
		SetTrack(track).
		SetPosition(1).
		SetTrackName("Test Track").
		SetArtistName("Test Artist").
		SetDurationMs(180000).
		Save(ctx)
	require.NoError(t, err)

	req := &EnhancementRequest{
		PlaylistID:   pl.ID,
		DJID:         d.ID,
		Mode:         EnhancementModeOneTime,
		MaxNewTracks: 5,
		UserID:       u.ID,
	}

	_, err = enhancer.EnhancePlaylist(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "AI call failed")
}

func TestEnhancePlaylist_RateLimitError(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Mock server that returns 429
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{
				"message": "Rate limit exceeded",
				"type":    "rate_limit_error",
			},
		})
	}))
	defer mockServer.Close()

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.BaseURL = mockServer.URL
	cfg.OpenAI.Model = "gpt-4o"

	enhancer := NewPlaylistEnhancer(client, cfg, nil, nil)

	ctx := context.Background()

	u, err := client.User.Create().
		SetUsername("testuser").
		SetPaginationSize(25).
		Save(ctx)
	require.NoError(t, err)

	d, err := client.DJ.Create().
		SetName("Test DJ").
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	artist, err := client.Artist.Create().
		SetName("Test Artist").
		SetNavidromeID("nd-artist-1").
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	track, err := client.Track.Create().
		SetName("Test Track").
		SetNavidromeID("nd-track-1").
		SetDurationMs(180000).
		SetArtist(artist).
		Save(ctx)
	require.NoError(t, err)

	pl, err := client.Playlist.Create().
		SetName("Test Playlist").
		SetRemoteID("remote-1").
		SetSource("navidrome").
		SetTrackCount(1).
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.PlaylistTrack.Create().
		SetPlaylist(pl).
		SetTrack(track).
		SetPosition(1).
		SetTrackName("Test Track").
		SetArtistName("Test Artist").
		SetDurationMs(180000).
		Save(ctx)
	require.NoError(t, err)

	req := &EnhancementRequest{
		PlaylistID: pl.ID,
		DJID:       d.ID,
		Mode:       EnhancementModeOneTime,
		UserID:     u.ID,
	}

	_, err = enhancer.EnhancePlaylist(ctx, req)
	assert.Error(t, err)
}
