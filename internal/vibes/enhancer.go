// Governing: ADR-0008 (OpenAI API / LiteLLM backend), ADR-0007 (event bus), SPEC vibes-ai-mixtape-engine
package vibes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"spotter/ent"
	"spotter/ent/listen"
	"spotter/ent/playlist"
	"spotter/ent/playlisttrack"
	"spotter/ent/track"
	"spotter/ent/user"
	"spotter/internal/config"
	"spotter/internal/events"
	"spotter/internal/llm"
)

// PlaylistEnhancer implements playlist enhancement using AI.
type PlaylistEnhancer struct {
	client    *ent.Client
	config    *config.Config
	logger    *slog.Logger
	bus       *events.Bus
	llm       *llm.Client
	templates map[string]*template.Template
}

// NewPlaylistEnhancer creates a new PlaylistEnhancer service.
func NewPlaylistEnhancer(client *ent.Client, cfg *config.Config, logger *slog.Logger, bus *events.Bus) *PlaylistEnhancer {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	timeout := time.Duration(cfg.Vibes.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	e := &PlaylistEnhancer{
		client: client,
		config: cfg,
		logger: logger,
		bus:    bus,
		llm: llm.NewClient(llm.ClientConfig{
			APIKey:  cfg.OpenAI.APIKey,
			BaseURL: cfg.OpenAI.BaseURL,
			Timeout: timeout,
		}),
		templates: make(map[string]*template.Template),
	}

	if err := e.loadTemplates(); err != nil {
		logger.Warn("failed to load enhancement prompt templates", "error", err)
	}

	return e
}

// loadTemplates loads prompt templates from the prompts directory.
func (e *PlaylistEnhancer) loadTemplates() error {
	promptsDir := e.config.GetVibesPromptsDirectory()

	funcMap := template.FuncMap{
		"mul": func(a, b float64) float64 {
			return a * b
		},
	}

	tmplPath := filepath.Join(promptsDir, "enhance_playlist.tmpl")
	content, err := os.ReadFile(tmplPath)
	if err != nil {
		e.logger.Debug("enhancement template not found, will use fallback", "path", tmplPath)
		return nil
	}

	tmpl, err := template.New("enhance_playlist").Funcs(funcMap).Parse(string(content))
	if err != nil {
		return fmt.Errorf("failed to parse enhancement template: %w", err)
	}

	e.templates["enhance_playlist"] = tmpl
	e.logger.Debug("loaded enhancement prompt template", "path", tmplPath)

	return nil
}

// EnhancePlaylist enhances a playlist using AI based on the DJ persona.
func (e *PlaylistEnhancer) EnhancePlaylist(ctx context.Context, req *EnhancementRequest) (*EnhancementResult, error) {
	startTime := time.Now()

	e.logger.Info("starting playlist enhancement",
		"playlist_id", req.PlaylistID,
		"dj_id", req.DJID,
		"mode", req.Mode,
		"user_id", req.UserID)

	// Validate request
	if err := e.validateRequest(req); err != nil {
		e.publishError(req.UserID, req.PlaylistID, "Invalid request: "+err.Error())
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Check if OpenAI is configured
	if !e.config.IsOpenAIEnabled() {
		e.publishError(req.UserID, req.PlaylistID, "AI service is not configured")
		return nil, fmt.Errorf("OpenAI API key not configured")
	}

	// Load playlist with tracks
	playlist, err := e.client.Playlist.Get(ctx, req.PlaylistID)
	if err != nil {
		e.publishError(req.UserID, req.PlaylistID, "Playlist not found")
		return nil, fmt.Errorf("failed to load playlist: %w", err)
	}

	// Load DJ
	dj, err := e.client.DJ.Get(ctx, req.DJID)
	if err != nil {
		e.publishError(req.UserID, req.PlaylistID, "DJ not found")
		return nil, fmt.Errorf("failed to load DJ: %w", err)
	}

	// Get existing tracks in the playlist
	existingTracks, err := e.getExistingTracks(ctx, req.PlaylistID)
	if err != nil {
		e.publishError(req.UserID, req.PlaylistID, "Failed to load playlist tracks")
		return nil, fmt.Errorf("failed to load playlist tracks: %w", err)
	}

	if len(existingTracks) == 0 {
		e.publishError(req.UserID, req.PlaylistID, "Playlist has no tracks to enhance")
		return nil, fmt.Errorf("playlist has no tracks")
	}

	e.logger.Debug("loaded existing tracks", "count", len(existingTracks))

	// Set default max new tracks
	maxNewTracks := req.MaxNewTracks
	if maxNewTracks <= 0 {
		maxNewTracks = 5
	}
	if maxNewTracks > 20 {
		maxNewTracks = 20
	}

	// Build template data
	templateData, err := e.buildTemplateData(ctx, playlist, dj, existingTracks, req.UserID, maxNewTracks)
	if err != nil {
		e.publishError(req.UserID, req.PlaylistID, "Failed to prepare enhancement data")
		return nil, fmt.Errorf("failed to build template data: %w", err)
	}

	// Generate the prompt
	prompt, err := e.renderPrompt(templateData)
	if err != nil {
		e.publishError(req.UserID, req.PlaylistID, "Failed to generate prompt")
		return nil, fmt.Errorf("failed to render prompt: %w", err)
	}

	e.logger.Debug("generated prompt", "prompt_length", len(prompt))

	// Governing: ADR-0019 (structured metrics), SPEC observability REQ "LLM-001", REQ "LLM-002", REQ "LLM-003"
	// Call the AI
	model := e.config.GetVibesModel()
	llmStart := time.Now()
	response, tokensUsed, err := e.callOpenAI(ctx, prompt)
	llmDuration := time.Since(llmStart).Milliseconds()
	if err != nil {
		e.logger.Info("metric.llm",
			"model", model,
			"operation", "playlist_enhance",
			"tokens_used", 0,
			"duration_ms", llmDuration,
			"success", false,
			"error", err.Error())
		e.publishError(req.UserID, req.PlaylistID, "AI service error: "+err.Error())
		return nil, fmt.Errorf("AI call failed: %w", err)
	}

	e.logger.Info("metric.llm",
		"model", model,
		"operation", "playlist_enhance",
		"tokens_used", tokensUsed,
		"duration_ms", llmDuration,
		"success", true,
		"error", "")

	e.logger.Info("AI enhancement complete",
		"model", model,
		"tokens_used", tokensUsed,
		"response_length", len(response))

	// Parse the response
	aiResp, err := e.parseAIResponse(response)
	if err != nil {
		e.publishError(req.UserID, req.PlaylistID, "Failed to parse AI response")
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}

	// Build the result
	result, err := e.buildResult(ctx, aiResp, existingTracks, templateData.AvailableTracks)
	if err != nil {
		e.publishError(req.UserID, req.PlaylistID, "Failed to process enhancement")
		return nil, fmt.Errorf("failed to build result: %w", err)
	}

	result.PromptUsed = prompt
	result.ModelUsed = model
	result.TokensUsed = tokensUsed
	result.OriginalTrackCount = len(existingTracks)

	duration := time.Since(startTime)
	e.logger.Info("playlist enhancement complete",
		"playlist_id", req.PlaylistID,
		"original_tracks", len(existingTracks),
		"final_tracks", result.FinalTrackCount,
		"tracks_added", result.TracksAdded,
		"tokens_used", tokensUsed,
		"duration_ms", duration.Milliseconds())

	// Publish success notification
	e.publishSuccess(req.UserID, req.PlaylistID, playlist.Name, result.TracksAdded, tokensUsed)

	return result, nil
}

// validateRequest validates the enhancement request.
func (e *PlaylistEnhancer) validateRequest(req *EnhancementRequest) error {
	if req.PlaylistID <= 0 {
		return fmt.Errorf("playlist ID is required")
	}
	if req.DJID <= 0 {
		return fmt.Errorf("DJ ID is required")
	}
	if req.UserID <= 0 {
		return fmt.Errorf("user ID is required")
	}
	if req.Mode != EnhancementModeOneTime && req.Mode != EnhancementModeConvertToMixtape {
		return fmt.Errorf("invalid enhancement mode: %s", req.Mode)
	}
	return nil
}

// getExistingTracks retrieves the current tracks in the playlist.
func (e *PlaylistEnhancer) getExistingTracks(ctx context.Context, playlistID int) ([]ExistingTrack, error) {
	playlistTracks, err := e.client.PlaylistTrack.Query().
		Where(playlisttrack.HasPlaylistWith(playlist.ID(playlistID))).
		WithTrack(func(q *ent.TrackQuery) {
			q.WithArtist()
			q.WithAlbum()
		}).
		WithArtist().
		WithAlbum().
		Order(ent.Asc(playlisttrack.FieldPosition)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	tracks := make([]ExistingTrack, 0, len(playlistTracks))
	for i, pt := range playlistTracks {
		track := ExistingTrack{
			Position: i + 1,
		}

		// Try to get data from linked catalog track first
		if pt.Edges.Track != nil {
			t := pt.Edges.Track
			track.ID = t.ID
			track.Name = t.Name
			if t.Edges.Artist != nil {
				track.Artist = t.Edges.Artist.Name
			}
			if t.Edges.Album != nil {
				track.Album = t.Edges.Album.Name
			}
			track.Genres = t.Tags
			if t.Energy != nil {
				track.Energy = t.Energy
			}
			if t.Bpm != nil {
				track.BPM = t.Bpm
			}
		} else {
			// Fall back to explicit data from playlist track
			track.ID = pt.ID // Use playlist track ID as fallback
			track.Name = pt.TrackName
			track.Artist = pt.ArtistName
			track.Album = pt.AlbumName
		}

		tracks = append(tracks, track)
	}

	return tracks, nil
}

// buildTemplateData prepares the data for the prompt template.
func (e *PlaylistEnhancer) buildTemplateData(ctx context.Context, playlist *ent.Playlist, dj *ent.DJ, existingTracks []ExistingTrack, userID int, maxNewTracks int) (*EnhancementTemplateData, error) {
	data := &EnhancementTemplateData{
		DJName:              dj.Name,
		DJSystemPrompt:      dj.SystemPrompt,
		GenresInclude:       dj.GenresInclude,
		GenresExclude:       dj.GenresExclude,
		Vibes:               dj.Vibes,
		ArtistsInclude:      dj.ArtistsInclude,
		ArtistsExclude:      dj.ArtistsExclude,
		PlaylistName:        playlist.Name,
		PlaylistDescription: playlist.Description,
		ExistingTracks:      existingTracks,
		MaxNewTracks:        maxNewTracks,
	}

	// Get user's listening history
	history, err := e.getListeningHistory(ctx, userID)
	if err != nil {
		e.logger.Warn("failed to get listening history", "error", err)
	} else {
		data.ListeningHistory = history
	}

	// Get available tracks for addition (excluding existing tracks)
	existingIDs := make(map[int]bool)
	for _, t := range existingTracks {
		existingIDs[t.ID] = true
	}

	availableTracks, err := e.getAvailableTracks(ctx, userID, existingIDs)
	if err != nil {
		e.logger.Warn("failed to get available tracks", "error", err)
	} else {
		data.AvailableTracks = availableTracks
	}

	return data, nil
}

// getListeningHistory retrieves the user's recent listening history.
func (e *PlaylistEnhancer) getListeningHistory(ctx context.Context, userID int) ([]HistoryEntry, error) {
	historyDays := e.config.Vibes.HistoryDays
	if historyDays <= 0 {
		historyDays = 30
	}
	since := time.Now().AddDate(0, 0, -historyDays)

	maxHistory := e.config.Vibes.MaxHistoryTracks
	if maxHistory <= 0 {
		maxHistory = 50
	}

	listens, err := e.client.Listen.Query().
		Where(
			listen.HasUserWith(user.ID(userID)),
			listen.PlayedAtGTE(since),
		).
		WithTrack(func(q *ent.TrackQuery) {
			q.WithArtist()
			q.WithAlbum()
		}).
		Order(ent.Desc(listen.FieldPlayedAt)).
		Limit(maxHistory * 2).
		All(ctx)
	if err != nil {
		return nil, err
	}

	// Aggregate by track
	trackCounts := make(map[string]*HistoryEntry)
	for _, l := range listens {
		if l.Edges.Track == nil {
			continue
		}
		t := l.Edges.Track
		key := fmt.Sprintf("%s-%s", t.Name, t.Edges.Artist.Name)
		if entry, ok := trackCounts[key]; ok {
			entry.PlayCount++
		} else {
			entry := &HistoryEntry{
				TrackName:  t.Name,
				ArtistName: t.Edges.Artist.Name,
				PlayCount:  1,
			}
			if t.Edges.Album != nil {
				entry.AlbumName = t.Edges.Album.Name
			}
			trackCounts[key] = entry
		}
	}

	// Convert to slice and limit
	history := make([]HistoryEntry, 0, len(trackCounts))
	for _, entry := range trackCounts {
		history = append(history, *entry)
		if len(history) >= maxHistory {
			break
		}
	}

	return history, nil
}

// getAvailableTracks retrieves tracks available for addition.
func (e *PlaylistEnhancer) getAvailableTracks(ctx context.Context, userID int, excludeIDs map[int]bool) ([]AvailableTrack, error) {
	// Get tracks from user's library (limit for prompt size)
	tracks, err := e.client.Track.Query().
		Where(track.HasArtist()).
		WithArtist().
		WithAlbum().
		Limit(500).
		All(ctx)
	if err != nil {
		return nil, err
	}

	available := make([]AvailableTrack, 0, len(tracks))
	for _, t := range tracks {
		if excludeIDs[t.ID] {
			continue
		}

		at := AvailableTrack{
			ID:     t.ID,
			Name:   t.Name,
			Genres: t.Tags,
			Energy: t.Energy,
			BPM:    t.Bpm,
		}

		if t.Edges.Artist != nil {
			at.Artist = t.Edges.Artist.Name
		}
		if t.Edges.Album != nil {
			at.Album = t.Edges.Album.Name
		}

		available = append(available, at)

		// Limit available tracks for prompt size
		if len(available) >= 200 {
			break
		}
	}

	return available, nil
}

// renderPrompt renders the enhancement prompt using the template.
func (e *PlaylistEnhancer) renderPrompt(data *EnhancementTemplateData) (string, error) {
	tmpl, ok := e.templates["enhance_playlist"]
	if !ok {
		return e.fallbackPrompt(data), nil
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// fallbackPrompt generates a basic prompt when template is not available.
func (e *PlaylistEnhancer) fallbackPrompt(data *EnhancementTemplateData) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("You are %s, a music curator. Enhance this playlist by reordering tracks for better flow and adding %d new tracks.\n\n", data.DJName, data.MaxNewTracks))

	sb.WriteString("Current playlist tracks (MUST ALL be included in output):\n")
	for _, t := range data.ExistingTracks {
		sb.WriteString(fmt.Sprintf("- [EXISTING:%d] %s by %s\n", t.ID, t.Name, t.Artist))
	}

	sb.WriteString("\nAvailable tracks to add:\n")
	for _, t := range data.AvailableTracks {
		sb.WriteString(fmt.Sprintf("- [ADD:%d] %s by %s\n", t.ID, t.Name, t.Artist))
	}

	sb.WriteString("\nRespond with JSON containing reordered_tracks, new_tracks, flow_description, enhancement_summary, and opening_thoughts.")

	return sb.String()
}

// callOpenAI calls the OpenAI API with the prompt.
func (e *PlaylistEnhancer) callOpenAI(ctx context.Context, prompt string) (string, int, error) {
	req := llm.ChatRequest{
		Model: e.config.GetVibesModel(),
		Messages: []llm.ChatMessage{
			{Role: "user", Content: prompt},
		},
		MaxTokens:   e.config.Vibes.MaxTokens,
		Temperature: e.config.Vibes.Temperature,
		ResponseFormat: &llm.ResponseFormat{
			Type: "json_object",
		},
	}

	resp, err := e.llm.Chat(ctx, req)
	if err != nil {
		return "", 0, fmt.Errorf("failed to execute request: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", 0, fmt.Errorf("no response from AI")
	}

	content := resp.Choices[0].Message.Content
	tokensUsed := 0
	if resp.Usage != nil {
		tokensUsed = resp.Usage.TotalTokens
	}

	return content, tokensUsed, nil
}

// parseAIResponse parses the AI response into structured data.
func (e *PlaylistEnhancer) parseAIResponse(response string) (*EnhancementAIResponse, error) {
	// Try to extract JSON from the response and sanitize trailing commas
	jsonStr := ExtractJSONObject(response)
	if jsonStr == "" {
		jsonStr = SanitizeJSON(response)
	}

	var aiResp EnhancementAIResponse
	if err := json.Unmarshal([]byte(jsonStr), &aiResp); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w (response: %s)", err, response[:min(200, len(response))])
	}

	return &aiResp, nil
}

// extractJSON attempts to extract a JSON object from text.
// Deprecated: Use ExtractJSONObject instead which also handles trailing commas and string escaping.
func extractJSON(text string) string {
	return ExtractJSONObject(text)
}

// buildResult processes the AI response into an EnhancementResult.
func (e *PlaylistEnhancer) buildResult(ctx context.Context, aiResp *EnhancementAIResponse, existingTracks []ExistingTrack, availableTracks []AvailableTrack) (*EnhancementResult, error) {
	result := &EnhancementResult{
		FlowDescription:    aiResp.FlowDescription,
		EnhancementSummary: aiResp.EnhancementSummary,
		OpeningThoughts:    aiResp.OpeningThoughts,
	}

	// Build lookup maps
	existingByID := make(map[int]ExistingTrack)
	for _, t := range existingTracks {
		existingByID[t.ID] = t
	}

	availableByID := make(map[int]AvailableTrack)
	for _, t := range availableTracks {
		availableByID[t.ID] = t
	}

	// Regex to parse track IDs like "EXISTING:123" or "ADD:456"
	idRegex := regexp.MustCompile(`^(EXISTING|ADD):(\d+)$`)

	// Process reordered tracks
	result.ReorderedTracks = make([]EnhancedTrack, 0, len(aiResp.ReorderedTracks)+len(aiResp.NewTracks))

	for _, rt := range aiResp.ReorderedTracks {
		matches := idRegex.FindStringSubmatch(rt.ID)
		if matches == nil {
			// Try parsing as plain number (existing track)
			if id, err := strconv.Atoi(rt.ID); err == nil {
				if t, ok := existingByID[id]; ok {
					result.ReorderedTracks = append(result.ReorderedTracks, EnhancedTrack{
						ID:         rt.ID,
						InternalID: id,
						Name:       t.Name,
						Artist:     t.Artist,
						Position:   rt.Position,
						Reason:     rt.Reason,
						IsNew:      false,
						Matched:    true,
					})
				}
			}
			continue
		}

		prefix := matches[1]
		id, err := strconv.Atoi(matches[2])
		if err != nil {
			e.logger.Debug("failed to parse track ID", "id_string", matches[2], "error", err)
			continue
		}

		if prefix == "EXISTING" {
			if t, ok := existingByID[id]; ok {
				result.ReorderedTracks = append(result.ReorderedTracks, EnhancedTrack{
					ID:         rt.ID,
					InternalID: id,
					Name:       t.Name,
					Artist:     t.Artist,
					Position:   rt.Position,
					Reason:     rt.Reason,
					IsNew:      false,
					Matched:    true,
				})
			}
		}
	}

	// Process new tracks
	result.NewTracks = make([]EnhancedTrack, 0, len(aiResp.NewTracks))

	for _, nt := range aiResp.NewTracks {
		matches := idRegex.FindStringSubmatch(nt.ID)
		var id int
		if matches != nil && matches[1] == "ADD" {
			var err error
			id, err = strconv.Atoi(matches[2])
			if err != nil {
				e.logger.Debug("failed to parse ADD track ID", "id", nt.ID, "error", err)
				continue
			}
		} else if numID, err := strconv.Atoi(nt.ID); err == nil {
			id = numID
		}

		if t, ok := availableByID[id]; ok {
			track := EnhancedTrack{
				ID:         nt.ID,
				InternalID: id,
				Name:       t.Name,
				Artist:     t.Artist,
				Position:   nt.Position,
				Reason:     nt.Reason,
				IsNew:      true,
				Matched:    true,
			}
			result.NewTracks = append(result.NewTracks, track)
			result.ReorderedTracks = append(result.ReorderedTracks, track)
		}
	}

	result.TracksAdded = len(result.NewTracks)
	result.FinalTrackCount = len(result.ReorderedTracks)

	return result, nil
}

// publishError publishes an error event.
func (e *PlaylistEnhancer) publishError(userID int, playlistID int, message string) {
	if e.bus == nil {
		return
	}
	e.bus.PublishPlaylistEnhancementError(userID, playlistID, message)
}

// publishSuccess publishes a success event.
func (e *PlaylistEnhancer) publishSuccess(userID int, playlistID int, playlistName string, tracksAdded int, tokensUsed int) {
	if e.bus == nil {
		return
	}
	e.bus.PublishPlaylistEnhanced(userID, playlistID, playlistName, tracksAdded, tokensUsed)
}

// GetAllTrackIDs returns all track IDs from the enhancement result in order.
func (r *EnhancementResult) GetAllTrackIDs() []int {
	ids := make([]int, 0, len(r.ReorderedTracks))
	for _, t := range r.ReorderedTracks {
		if t.Matched && t.InternalID > 0 {
			ids = append(ids, t.InternalID)
		}
	}
	return ids
}

// GetAllTrackIDsAsStrings returns all track IDs as strings.
func (r *EnhancementResult) GetAllTrackIDsAsStrings() []string {
	ids := make([]string, 0, len(r.ReorderedTracks))
	for _, t := range r.ReorderedTracks {
		if t.Matched && t.InternalID > 0 {
			ids = append(ids, strconv.Itoa(t.InternalID))
		}
	}
	return ids
}
