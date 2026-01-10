package vibes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"spotter/ent"
	"spotter/ent/artist"
	"spotter/ent/listen"
	"spotter/ent/track"
	"spotter/ent/user"
	"spotter/internal/config"
	"spotter/internal/events"
)

// nopHandler is a slog handler that discards all log records.
type nopHandler struct{}

func (nopHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (nopHandler) Handle(context.Context, slog.Record) error { return nil }
func (h nopHandler) WithAttrs([]slog.Attr) slog.Handler      { return h }
func (h nopHandler) WithGroup(string) slog.Handler           { return h }

// MixtapeGenerator implements the Generator interface for creating AI-powered mixtapes.
type MixtapeGenerator struct {
	client     *ent.Client
	config     *config.Config
	logger     *slog.Logger
	bus        *events.Bus
	httpClient *http.Client
	templates  map[string]*template.Template
}

// NewMixtapeGenerator creates a new MixtapeGenerator service.
func NewMixtapeGenerator(client *ent.Client, cfg *config.Config, logger *slog.Logger, bus *events.Bus) *MixtapeGenerator {
	// Use a no-op logger if none provided
	if logger == nil {
		logger = slog.New(nopHandler{})
	}

	timeout := time.Duration(cfg.Vibes.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	g := &MixtapeGenerator{
		client: client,
		config: cfg,
		logger: logger,
		bus:    bus,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		templates: make(map[string]*template.Template),
	}

	// Load templates on initialization
	if err := g.loadTemplates(); err != nil {
		logger.Warn("failed to load vibes prompt templates", "error", err)
	}

	return g
}

// loadTemplates loads prompt templates from the prompts directory.
func (g *MixtapeGenerator) loadTemplates() error {
	promptsDir := g.config.GetVibesPromptsDirectory()

	// Template functions
	funcMap := template.FuncMap{
		"mul": func(a, b float64) float64 {
			return a * b
		},
	}

	// Load the mixtape generation template
	tmplPath := filepath.Join(promptsDir, "generate_mixtape.tmpl")
	content, err := os.ReadFile(tmplPath)
	if err != nil {
		g.logger.Debug("mixtape template not found, will use fallback", "path", tmplPath)
		return nil
	}

	tmpl, err := template.New("generate_mixtape").Funcs(funcMap).Parse(string(content))
	if err != nil {
		return fmt.Errorf("failed to parse mixtape template: %w", err)
	}

	g.templates["generate_mixtape"] = tmpl
	g.logger.Debug("loaded mixtape prompt template", "path", tmplPath)

	return nil
}

// GenerateMixtape generates tracks for a mixtape based on the DJ persona and optional seed.
func (g *MixtapeGenerator) GenerateMixtape(ctx context.Context, req *GenerationRequest) (*GenerationResult, error) {
	startTime := time.Now()

	g.logger.Info("starting mixtape generation",
		"mixtape_id", req.Mixtape.ID,
		"mixtape_name", req.Mixtape.Name,
		"dj_name", req.DJ.Name,
		"user_id", req.UserID)

	// Validate the request
	if err := g.validateRequest(req); err != nil {
		g.publishError(req.UserID, "Invalid request: "+err.Error())
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Check if OpenAI is configured
	if !g.config.IsOpenAIEnabled() {
		g.publishError(req.UserID, "AI service is not configured. Please configure OpenAI API key.")
		return nil, fmt.Errorf("OpenAI API key not configured")
	}

	// Determine max tracks
	maxTracks := req.Mixtape.MaxTracks
	if req.MaxTracks > 0 {
		maxTracks = req.MaxTracks
	}
	if maxTracks <= 0 {
		maxTracks = g.config.Vibes.DefaultMaxTracks
	}
	if maxTracks > g.config.Vibes.MaxTracks {
		maxTracks = g.config.Vibes.MaxTracks
	}

	g.logger.Debug("determined max tracks", "max_tracks", maxTracks)

	// Load seed data if provided
	if err := g.loadSeedData(ctx, req); err != nil {
		g.logger.Warn("failed to load seed data", "error", err)
		// Continue without seed data
	}

	// Build the template data
	templateData, err := g.buildTemplateData(ctx, req, maxTracks)
	if err != nil {
		g.publishError(req.UserID, "Failed to prepare generation data: "+err.Error())
		return nil, fmt.Errorf("failed to build template data: %w", err)
	}

	// Generate the prompt
	prompt, err := g.renderPrompt(templateData)
	if err != nil {
		g.publishError(req.UserID, "Failed to generate prompt: "+err.Error())
		return nil, fmt.Errorf("failed to render prompt: %w", err)
	}

	g.logger.Debug("generated prompt", "prompt_length", len(prompt))

	// Call the AI
	model := g.config.GetVibesModel()
	response, tokensUsed, err := g.callOpenAI(ctx, prompt)
	if err != nil {
		g.publishError(req.UserID, "AI service error: "+err.Error())
		return nil, fmt.Errorf("AI call failed: %w", err)
	}

	g.logger.Info("AI generation complete",
		"model", model,
		"tokens_used", tokensUsed,
		"response_length", len(response))

	// Parse the response
	aiResp, err := g.parseAIResponse(response)
	if err != nil {
		g.publishError(req.UserID, "Failed to parse AI response")
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}

	// Match tracks to library
	result, err := g.matchTracksToLibrary(ctx, req.UserID, aiResp, templateData.AvailableTracks)
	if err != nil {
		g.publishError(req.UserID, "Failed to match tracks to library")
		return nil, fmt.Errorf("failed to match tracks: %w", err)
	}

	// Complete the result
	result.PromptUsed = prompt
	result.ModelUsed = model
	result.TokensUsed = tokensUsed
	result.FlowDescription = aiResp.FlowDescription
	result.OpeningThoughts = aiResp.OpeningThoughts
	result.ClosingThoughts = aiResp.ClosingThoughts

	duration := time.Since(startTime)
	g.logger.Info("mixtape generation complete",
		"mixtape_id", req.Mixtape.ID,
		"tracks_suggested", len(aiResp.Tracks),
		"tracks_matched", result.MatchedCount,
		"tracks_unmatched", result.UnmatchedCount,
		"tokens_used", tokensUsed,
		"duration_ms", duration.Milliseconds())

	// Publish success notification
	g.publishSuccess(req.UserID, req.Mixtape.Name, result.MatchedCount, tokensUsed)

	return result, nil
}

// validateRequest validates the generation request.
func (g *MixtapeGenerator) validateRequest(req *GenerationRequest) error {
	if req.Mixtape == nil {
		return fmt.Errorf("mixtape is required")
	}
	if req.DJ == nil {
		return fmt.Errorf("DJ is required")
	}
	if req.UserID <= 0 {
		return fmt.Errorf("valid user ID is required")
	}
	return nil
}

// loadSeedData loads the full entities for seed data.
func (g *MixtapeGenerator) loadSeedData(ctx context.Context, req *GenerationRequest) error {
	if req.Seed == nil {
		return nil
	}

	switch req.Seed.Type {
	case SeedTypeArtist:
		if req.Seed.Artist == nil {
			return nil
		}
		// Load full artist with edges
		a, err := g.client.Artist.Query().
			Where(artist.ID(req.Seed.Artist.ID)).
			WithAlbums().
			WithTracks().
			Only(ctx)
		if err == nil {
			req.Seed.Artist = a
		}
		return err

	case SeedTypeAlbum:
		if req.Seed.Album == nil {
			return nil
		}
		// Load album with artist edge
		album, err := g.client.Album.Get(ctx, req.Seed.Album.ID)
		if err == nil {
			req.Seed.Album = album
			// Load artist
			albumArtist, artistErr := album.QueryArtist().Only(ctx)
			if artistErr != nil {
				g.logger.Debug("failed to load album artist", "album_id", album.ID, "error", artistErr)
			} else if albumArtist != nil {
				_ = albumArtist // Album already has artist info we need
			}
		}
		return err

	case SeedTypeTracks:
		if len(req.Seed.TrackIDs) > 0 && len(req.Seed.Tracks) == 0 {
			tracks, err := g.client.Track.Query().
				Where(track.IDIn(req.Seed.TrackIDs...)).
				WithArtist().
				WithAlbum().
				All(ctx)
			if err == nil {
				req.Seed.Tracks = tracks
			}
			return err
		}
	}

	return nil
}

// buildTemplateData builds the template data for prompt generation.
func (g *MixtapeGenerator) buildTemplateData(ctx context.Context, req *GenerationRequest, maxTracks int) (*TemplateData, error) {
	data := &TemplateData{
		DJName:         req.DJ.Name,
		DJSystemPrompt: req.DJ.SystemPrompt,
		GenresInclude:  req.DJ.GenresInclude,
		GenresExclude:  req.DJ.GenresExclude,
		Vibes:          req.DJ.Vibes,
		ArtistsInclude: req.DJ.ArtistsInclude,
		ArtistsExclude: req.DJ.ArtistsExclude,

		MixtapeName:        req.Mixtape.Name,
		MixtapeDescription: req.Mixtape.Description,
		MaxTracks:          maxTracks,
	}

	// Add seed data
	if req.Seed != nil {
		data.SeedType = req.Seed.Type

		switch req.Seed.Type {
		case SeedTypeArtist:
			if req.Seed.Artist != nil {
				data.SeedArtist = &SeedArtistData{
					Name:      req.Seed.Artist.Name,
					Genres:    req.Seed.Artist.Genres,
					Bio:       req.Seed.Artist.Bio,
					AISummary: req.Seed.Artist.AiSummary,
				}
			}
		case SeedTypeAlbum:
			if req.Seed.Album != nil {
				albumArtist := ""
				if a, err := req.Seed.Album.QueryArtist().Only(ctx); err == nil && a != nil {
					albumArtist = a.Name
				}
				data.SeedAlbum = &SeedAlbumData{
					Name:      req.Seed.Album.Name,
					Artist:    albumArtist,
					Year:      req.Seed.Album.Year,
					Genre:     req.Seed.Album.Genre,
					AISummary: req.Seed.Album.AiSummary,
				}
			}
		case SeedTypeTracks:
			for _, t := range req.Seed.Tracks {
				artistName := ""
				albumName := ""
				if a := t.Edges.Artist; a != nil {
					artistName = a.Name
				}
				if a := t.Edges.Album; a != nil {
					albumName = a.Name
				}
				data.SeedTracks = append(data.SeedTracks, SeedTrackData{
					Name:   t.Name,
					Artist: artistName,
					Album:  albumName,
				})
			}
		}
	}

	// Get user's listening history
	history, err := g.getListeningHistory(ctx, req.UserID)
	if err != nil {
		g.logger.Warn("failed to get listening history", "error", err)
	} else {
		data.ListeningHistory = history
	}

	// Get available tracks from user's library
	availableTracks, err := g.getAvailableTracks(ctx, req.UserID, req.DJ)
	if err != nil {
		return nil, fmt.Errorf("failed to get available tracks: %w", err)
	}
	data.AvailableTracks = availableTracks

	if len(availableTracks) == 0 {
		return nil, fmt.Errorf("no tracks available in library")
	}

	g.logger.Debug("built template data",
		"available_tracks", len(availableTracks),
		"history_entries", len(data.ListeningHistory))

	return data, nil
}

// getListeningHistory retrieves the user's recent listening history.
func (g *MixtapeGenerator) getListeningHistory(ctx context.Context, userID int) ([]HistoryEntry, error) {
	historyDays := g.config.Vibes.HistoryDays
	if historyDays <= 0 {
		historyDays = 30
	}
	since := time.Now().AddDate(0, 0, -historyDays)

	maxHistory := g.config.Vibes.MaxHistoryTracks
	if maxHistory <= 0 {
		maxHistory = 50
	}

	listens, err := g.client.Listen.Query().
		Where(
			listen.HasUserWith(user.ID(userID)),
			listen.PlayedAtGTE(since),
		).
		Order(ent.Desc(listen.FieldPlayedAt)).
		Limit(maxHistory * 3). // Get more to aggregate
		All(ctx)

	if err != nil {
		return nil, err
	}

	// Aggregate by track/artist to get play counts
	trackCounts := make(map[string]*HistoryEntry)
	for _, l := range listens {
		key := l.TrackName + "|" + l.ArtistName
		if entry, exists := trackCounts[key]; exists {
			entry.PlayCount++
		} else {
			trackCounts[key] = &HistoryEntry{
				TrackName:  l.TrackName,
				ArtistName: l.ArtistName,
				AlbumName:  l.AlbumName,
				PlayCount:  1,
			}
		}
	}

	// Convert to slice and limit
	result := make([]HistoryEntry, 0, len(trackCounts))
	for _, entry := range trackCounts {
		result = append(result, *entry)
		if len(result) >= maxHistory {
			break
		}
	}

	return result, nil
}

// getAvailableTracks retrieves tracks from the user's library, applying DJ filters.
func (g *MixtapeGenerator) getAvailableTracks(ctx context.Context, userID int, dj *ent.DJ) ([]AvailableTrack, error) {
	// Query all tracks in user's library
	query := g.client.Track.Query().
		Where(track.HasArtistWith(artist.HasUserWith(user.ID(userID)))).
		WithArtist().
		WithAlbum().
		Limit(500) // Reasonable limit for context

	tracks, err := query.All(ctx)
	if err != nil {
		return nil, err
	}

	available := make([]AvailableTrack, 0, len(tracks))
	excludeArtists := make(map[string]bool)
	excludeGenres := make(map[string]bool)

	// Build exclude maps
	for _, a := range dj.ArtistsExclude {
		excludeArtists[strings.ToLower(a)] = true
	}
	for _, g := range dj.GenresExclude {
		excludeGenres[strings.ToLower(g)] = true
	}

	for _, t := range tracks {
		// Get artist name
		artistName := ""
		if t.Edges.Artist != nil {
			artistName = t.Edges.Artist.Name
			// Check artist exclusion
			if excludeArtists[strings.ToLower(artistName)] {
				continue
			}
		}

		// Check genre exclusion
		excluded := false
		for _, genre := range t.Genres {
			if excludeGenres[strings.ToLower(genre)] {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}

		albumName := ""
		if t.Edges.Album != nil {
			albumName = t.Edges.Album.Name
		}

		at := AvailableTrack{
			ID:     t.ID,
			Name:   t.Name,
			Artist: artistName,
			Album:  albumName,
			Genres: t.Genres,
			Tags:   t.Tags,
		}

		if t.Energy != nil {
			at.Energy = t.Energy
		}
		if t.Valence != nil {
			at.Valence = t.Valence
		}
		if t.Bpm != nil {
			at.BPM = t.Bpm
		}

		available = append(available, at)
	}

	return available, nil
}

// renderPrompt renders the prompt template with the given data.
func (g *MixtapeGenerator) renderPrompt(data *TemplateData) (string, error) {
	tmpl, ok := g.templates["generate_mixtape"]
	if !ok {
		// Use fallback prompt
		return g.fallbackPrompt(data), nil
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template execution failed: %w", err)
	}

	return buf.String(), nil
}

// fallbackPrompt generates a basic prompt when templates are not available.
func (g *MixtapeGenerator) fallbackPrompt(data *TemplateData) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("You are %s, a music curator.\n\n", data.DJName))

	if data.DJSystemPrompt != "" {
		sb.WriteString(fmt.Sprintf("Your personality: %s\n\n", data.DJSystemPrompt))
	}

	sb.WriteString(fmt.Sprintf("Create a playlist of %d tracks from this library:\n\n", data.MaxTracks))

	for _, t := range data.AvailableTracks {
		sb.WriteString(fmt.Sprintf("- %d: %s by %s\n", t.ID, t.Name, t.Artist))
	}

	sb.WriteString("\nRespond with JSON: {\"tracks\": [{\"id\": \"track_id\", \"name\": \"name\", \"artist\": \"artist\", \"reason\": \"why\"}], \"flow_description\": \"description\", \"opening_thoughts\": \"intro\", \"closing_thoughts\": \"outro\"}")

	return sb.String()
}

// ChatMessage represents a message in the OpenAI chat format.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ResponseFormat specifies the format of the response from OpenAI.
type ResponseFormat struct {
	Type string `json:"type"`
}

// ChatRequest represents an OpenAI chat completion request.
type ChatRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Temperature    float64         `json:"temperature,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

// ChatResponse represents an OpenAI chat completion response.
type ChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// callOpenAI makes a request to the OpenAI API.
func (g *MixtapeGenerator) callOpenAI(ctx context.Context, prompt string) (string, int, error) {
	baseURL := g.config.OpenAI.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	model := g.config.GetVibesModel()
	temperature := g.config.Vibes.Temperature
	if temperature <= 0 {
		temperature = 0.8
	}
	maxTokens := g.config.Vibes.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4000
	}

	g.logger.Debug("preparing OpenAI request",
		"base_url", baseURL,
		"model", model,
		"temperature", temperature,
		"max_tokens", maxTokens,
		"prompt_length", len(prompt))

	req := ChatRequest{
		Model: model,
		Messages: []ChatMessage{
			{
				Role:    "user",
				Content: prompt,
			},
		},
		MaxTokens:   maxTokens,
		Temperature: temperature,
		ResponseFormat: &ResponseFormat{
			Type: "json_object",
		},
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return "", 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+g.config.OpenAI.APIKey)

	g.logger.Debug("sending OpenAI request", "url", baseURL+"/chat/completions")

	resp, err := g.httpClient.Do(httpReq)
	if err != nil {
		g.logger.Error("OpenAI request failed", "error", err)
		return "", 0, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			g.logger.Warn("failed to close response body", "error", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("failed to read response: %w", err)
	}

	g.logger.Debug("received OpenAI response",
		"status", resp.StatusCode,
		"body_length", len(body))

	if resp.StatusCode != http.StatusOK {
		g.logger.Error("OpenAI API error",
			"status", resp.StatusCode,
			"body", string(body))
		return "", 0, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return "", 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if chatResp.Error != nil {
		return "", 0, fmt.Errorf("API error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", 0, fmt.Errorf("no choices in response")
	}

	tokensUsed := 0
	if chatResp.Usage != nil {
		tokensUsed = chatResp.Usage.TotalTokens
		g.logger.Info("OpenAI token usage",
			"prompt_tokens", chatResp.Usage.PromptTokens,
			"completion_tokens", chatResp.Usage.CompletionTokens,
			"total_tokens", chatResp.Usage.TotalTokens)
	}

	return chatResp.Choices[0].Message.Content, tokensUsed, nil
}

// parseAIResponse parses the AI response JSON.
func (g *MixtapeGenerator) parseAIResponse(response string) (*AIResponse, error) {
	// Extract JSON from potential markdown code blocks and sanitize trailing commas
	jsonStr := ExtractAndSanitizeJSON(response)

	var aiResp AIResponse
	if err := json.Unmarshal([]byte(jsonStr), &aiResp); err != nil {
		g.logger.Error("failed to parse AI response JSON",
			"error", err,
			"response", response[:min(500, len(response))])
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	return &aiResp, nil
}

// parseJSONResponse extracts JSON from the response, handling markdown code blocks.
// Deprecated: Use ExtractAndSanitizeJSON instead which also handles trailing commas.
func parseJSONResponse(response string) string {
	return ExtractAndSanitizeJSON(response)
}

// matchTracksToLibrary matches AI-suggested tracks to the user's library.
func (g *MixtapeGenerator) matchTracksToLibrary(ctx context.Context, userID int, aiResp *AIResponse, available []AvailableTrack) (*GenerationResult, error) {
	result := &GenerationResult{
		Tracks: make([]GeneratedTrack, 0, len(aiResp.Tracks)),
	}

	// Build lookup map by ID
	trackByID := make(map[int]AvailableTrack)
	for _, t := range available {
		trackByID[t.ID] = t
	}

	// Build fuzzy match maps
	trackByName := make(map[string]AvailableTrack)
	for _, t := range available {
		key := strings.ToLower(t.Artist + "|" + t.Name)
		trackByName[key] = t
	}

	minConfidence := g.config.Vibes.MinMatchConfidence
	if minConfidence <= 0 {
		minConfidence = 0.7
	}

	for _, suggested := range aiResp.Tracks {
		gt := GeneratedTrack{
			ExternalID: suggested.ID,
			Name:       suggested.Name,
			Artist:     suggested.Artist,
			Reason:     suggested.Reason,
		}

		// Try exact ID match first
		if id, err := strconv.Atoi(suggested.ID); err == nil {
			if t, ok := trackByID[id]; ok {
				gt.ID = t.ID
				gt.Matched = true
				gt.MatchConfidence = 1.0
				gt.Name = t.Name
				gt.Artist = t.Artist
				result.MatchedCount++
				result.Tracks = append(result.Tracks, gt)
				continue
			}
		}

		// Try exact name match
		key := strings.ToLower(suggested.Artist + "|" + suggested.Name)
		if t, ok := trackByName[key]; ok {
			gt.ID = t.ID
			gt.Matched = true
			gt.MatchConfidence = 1.0
			result.MatchedCount++
			result.Tracks = append(result.Tracks, gt)
			continue
		}

		// Try fuzzy matching
		bestMatch, confidence := g.findBestFuzzyMatch(suggested.Name, suggested.Artist, available)
		if bestMatch != nil && confidence >= minConfidence {
			gt.ID = bestMatch.ID
			gt.Matched = true
			gt.MatchConfidence = confidence
			gt.Name = bestMatch.Name
			gt.Artist = bestMatch.Artist
			result.MatchedCount++
			g.logger.Debug("fuzzy matched track",
				"suggested_name", suggested.Name,
				"suggested_artist", suggested.Artist,
				"matched_name", bestMatch.Name,
				"matched_artist", bestMatch.Artist,
				"confidence", confidence)
		} else {
			result.UnmatchedCount++
			g.logger.Debug("failed to match track",
				"name", suggested.Name,
				"artist", suggested.Artist,
				"best_confidence", confidence)
		}

		result.Tracks = append(result.Tracks, gt)
	}

	return result, nil
}

// findBestFuzzyMatch finds the best fuzzy match for a track.
func (g *MixtapeGenerator) findBestFuzzyMatch(name, artist string, candidates []AvailableTrack) (*AvailableTrack, float64) {
	var bestMatch *AvailableTrack
	bestScore := 0.0

	normalizedName := normalizeForMatch(name)
	normalizedArtist := normalizeForMatch(artist)

	for i := range candidates {
		candidate := &candidates[i]

		candidateName := normalizeForMatch(candidate.Name)
		candidateArtist := normalizeForMatch(candidate.Artist)

		nameScore := similarity(normalizedName, candidateName)
		artistScore := similarity(normalizedArtist, candidateArtist)

		// Weighted average: name is more important
		score := (nameScore * 0.6) + (artistScore * 0.4)

		// Bonus for both being high confidence
		if nameScore > 0.8 && artistScore > 0.8 {
			score += 0.1
			if score > 1.0 {
				score = 1.0
			}
		}

		if score > bestScore {
			bestScore = score
			bestMatch = candidate
		}
	}

	return bestMatch, bestScore
}

// normalizeForMatch normalizes a string for fuzzy matching.
func normalizeForMatch(s string) string {
	s = strings.ToLower(s)

	// Remove common suffixes
	suffixes := []string{
		"(remastered)", "(remaster)", "(deluxe)", "(live)",
		"[remastered]", "[remaster]", "[deluxe]", "[live]",
		" - remastered", " - live",
	}
	for _, suffix := range suffixes {
		s = strings.TrimSuffix(s, suffix)
	}

	// Remove non-alphanumeric characters
	var result strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
			result.WriteRune(r)
		}
	}

	return strings.TrimSpace(result.String())
}

// similarity calculates string similarity using Levenshtein distance.
func similarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	distance := levenshtein(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}

	return 1.0 - float64(distance)/float64(maxLen)
}

// levenshtein calculates Levenshtein distance between two strings.
func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	aRunes := []rune(a)
	bRunes := []rune(b)

	d := make([][]int, len(aRunes)+1)
	for i := range d {
		d[i] = make([]int, len(bRunes)+1)
	}

	for i := 0; i <= len(aRunes); i++ {
		d[i][0] = i
	}
	for j := 0; j <= len(bRunes); j++ {
		d[0][j] = j
	}

	for i := 1; i <= len(aRunes); i++ {
		for j := 1; j <= len(bRunes); j++ {
			cost := 1
			if aRunes[i-1] == bRunes[j-1] {
				cost = 0
			}

			d[i][j] = minInt(
				d[i-1][j]+1,
				minInt(d[i][j-1]+1, d[i-1][j-1]+cost),
			)
		}
	}

	return d[len(aRunes)][len(bRunes)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// publishError publishes an error notification to the user.
func (g *MixtapeGenerator) publishError(userID int, message string) {
	if g.bus == nil {
		return
	}

	g.bus.Publish(userID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Mixtape Generation Failed",
			Message:  message,
			IconType: "error",
		},
	})
}

// publishSuccess publishes a success notification to the user.
func (g *MixtapeGenerator) publishSuccess(userID int, mixtapeName string, trackCount int, tokensUsed int) {
	if g.bus == nil {
		return
	}

	message := fmt.Sprintf("Generated %d tracks for '%s' (tokens: %d)", trackCount, mixtapeName, tokensUsed)

	g.bus.Publish(userID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "Mixtape Generated",
			Message:  message,
			IconType: "success",
		},
	})
}

// GetMatchedTrackIDs returns just the IDs of matched tracks from a generation result.
func (r *GenerationResult) GetMatchedTrackIDs() []int {
	ids := make([]int, 0, r.MatchedCount)
	for _, t := range r.Tracks {
		if t.Matched {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// GetMatchedTrackIDsAsStrings returns matched track IDs as strings.
func (r *GenerationResult) GetMatchedTrackIDsAsStrings() []string {
	ids := make([]string, 0, r.MatchedCount)
	for _, t := range r.Tracks {
		if t.Matched {
			ids = append(ids, strconv.Itoa(t.ID))
		}
	}
	return ids
}
