// Governing: ADR-0007 (event bus), ADR-0008 (OpenAI), ADR-0004 (Ent ORM), SPEC similar-artists-discovery

package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"text/template"
	"time"

	"spotter/ent"
	"spotter/ent/artist"
	"spotter/ent/similarartist"
	"spotter/ent/user"
	"spotter/internal/config"
	"spotter/internal/events"
	"spotter/internal/llm"
)

const (
	// ProviderOpenAI is the provider name for OpenAI-generated similarities
	ProviderOpenAI = "OpenAI"
	// ProviderLastFM is the provider name for Last.fm-based similarities
	ProviderLastFM = "LastFM"

	// defaultSimilarArtistsTimeout is the default timeout for AI requests
	defaultSimilarArtistsTimeout = 60 * time.Second
)

// SimilarArtistsService handles finding and storing similar artist relationships.
type SimilarArtistsService struct {
	client    *ent.Client
	config    *config.Config
	logger    *slog.Logger
	bus       *events.Bus
	llm       *llm.Client
	templates *template.Template
}

// NewSimilarArtistsService creates a new SimilarArtistsService.
func NewSimilarArtistsService(client *ent.Client, cfg *config.Config, logger *slog.Logger, bus *events.Bus) *SimilarArtistsService {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}

	svc := &SimilarArtistsService{
		client: client,
		config: cfg,
		logger: logger,
		bus:    bus,
		llm: llm.NewClient(llm.ClientConfig{
			APIKey:  cfg.OpenAI.APIKey,
			BaseURL: cfg.OpenAI.BaseURL,
			Timeout: defaultSimilarArtistsTimeout,
		}),
	}

	// Load templates
	if err := svc.loadTemplates(); err != nil {
		// Governing: SPEC similar-artists-discovery REQ-SIM-002 (log warning, continue operating)
		logger.Warn("failed to load similar artists templates", "error", err)
	}

	return svc
}

// loadTemplates loads the prompt template for similar artist detection.
func (s *SimilarArtistsService) loadTemplates() error {
	promptsDir := s.config.GetVibesPromptsDirectory()
	templatePath := fmt.Sprintf("%s/enrich_artist.txt", promptsDir)

	content, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	tmpl, err := template.New("enrich_artist").Parse(string(content))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	s.templates = tmpl
	return nil
}

// AvailableArtist represents an artist in the user's library for the prompt template.
type AvailableArtist struct {
	ID     int
	Name   string
	Genres []string
}

// SimilarArtistTemplateData contains data for rendering the similar artist prompt.
type SimilarArtistTemplateData struct {
	Name             string
	Genres           []string
	Tags             []string
	Bio              string
	AISummary        string
	AvailableArtists []AvailableArtist
}

// SimilarArtistResponse represents the AI response for similar artists.
type SimilarArtistResponse struct {
	SimilarArtists []struct {
		Name       string  `json:"name"`
		ID         int     `json:"id"`
		Confidence float64 `json:"confidence"`
		Reason     string  `json:"reason"`
	} `json:"similar_artists"`
	Analysis string `json:"analysis"`
}

// FindSimilarArtists finds artists similar to the given artist from the user's library.
func (s *SimilarArtistsService) FindSimilarArtists(ctx context.Context, userID int, artistID int) error {
	// Get the target artist
	targetArtist, err := s.client.Artist.Query().
		Where(
			artist.ID(artistID),
			artist.HasUserWith(user.ID(userID)),
		).
		Only(ctx)
	if err != nil {
		return fmt.Errorf("failed to get target artist: %w", err)
	}

	s.logger.Info("finding similar artists",
		"artist_id", artistID,
		"artist_name", targetArtist.Name,
		"user_id", userID)

	// Get all artists in the user's library (excluding the target artist)
	allArtists, err := s.client.Artist.Query().
		Where(
			artist.HasUserWith(user.ID(userID)),
			artist.IDNEQ(artistID),
		).
		All(ctx)
	if err != nil {
		return fmt.Errorf("failed to get available artists: %w", err)
	}

	if len(allArtists) == 0 {
		s.logger.Info("no other artists in library to compare",
			"artist_id", artistID)
		return nil
	}

	// Build template data
	availableArtists := make([]AvailableArtist, len(allArtists))
	for i, a := range allArtists {
		availableArtists[i] = AvailableArtist{
			ID:     a.ID,
			Name:   a.Name,
			Genres: a.Genres,
		}
	}

	templateData := SimilarArtistTemplateData{
		Name:             targetArtist.Name,
		Genres:           targetArtist.Genres,
		Tags:             targetArtist.Tags,
		Bio:              targetArtist.Bio,
		AISummary:        targetArtist.AiSummary,
		AvailableArtists: availableArtists,
	}

	// Render prompt
	prompt, err := s.renderPrompt(templateData)
	if err != nil {
		return fmt.Errorf("failed to render prompt: %w", err)
	}

	// Governing: ADR-0019 (structured metrics), SPEC observability REQ "LLM-001", REQ "LLM-002", REQ "LLM-003"
	// Call OpenAI
	model := s.config.GetVibesModel()
	llmStart := time.Now()
	response, err := s.callOpenAI(ctx, prompt)
	llmDuration := time.Since(llmStart).Milliseconds()
	if err != nil {
		s.logger.Info("metric.llm",
			"model", model,
			"operation", "similar_artists",
			"tokens_used", 0,
			"duration_ms", llmDuration,
			"success", false,
			"error", err.Error())
		return fmt.Errorf("failed to call OpenAI: %w", err)
	}

	s.logger.Info("metric.llm",
		"model", model,
		"operation", "similar_artists",
		"tokens_used", 0,
		"duration_ms", llmDuration,
		"success", true,
		"error", "")

	// Parse response
	var aiResponse SimilarArtistResponse
	if err := parseJSONFromResponse(response, &aiResponse); err != nil {
		return fmt.Errorf("failed to parse AI response: %w", err)
	}

	// Store the results
	if err := s.storeSimilarArtists(ctx, userID, artistID, aiResponse.SimilarArtists); err != nil {
		return fmt.Errorf("failed to store similar artists: %w", err)
	}

	s.logger.Info("found similar artists",
		"artist_id", artistID,
		"artist_name", targetArtist.Name,
		"similar_count", len(aiResponse.SimilarArtists))

	// Publish notification
	if s.bus != nil {
		s.bus.PublishNotification(userID,
			"Similar Artists Found",
			fmt.Sprintf("Found %d artists similar to %s", len(aiResponse.SimilarArtists), targetArtist.Name),
			"success")
	}

	return nil
}

// renderPrompt renders the similar artist prompt template.
func (s *SimilarArtistsService) renderPrompt(data SimilarArtistTemplateData) (string, error) {
	if s.templates == nil {
		return s.fallbackPrompt(data), nil
	}

	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, "enrich_artist", data); err != nil {
		s.logger.Warn("failed to execute template, using fallback", "error", err)
		return s.fallbackPrompt(data), nil
	}

	return buf.String(), nil
}

// fallbackPrompt provides a simple prompt if the template fails to load.
func (s *SimilarArtistsService) fallbackPrompt(data SimilarArtistTemplateData) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Find artists similar to %s", data.Name))
	if len(data.Genres) > 0 {
		sb.WriteString(fmt.Sprintf(" (genres: %s)", strings.Join(data.Genres, ", ")))
	}
	sb.WriteString("\n\nAvailable artists in library:\n")
	for _, a := range data.AvailableArtists {
		sb.WriteString(fmt.Sprintf("- %s [ID: %d]\n", a.Name, a.ID))
	}
	sb.WriteString("\nRespond with JSON: {\"similar_artists\": [{\"name\": \"...\", \"id\": 123, \"confidence\": 0.9, \"reason\": \"...\"}]}")
	return sb.String()
}

// callOpenAI sends the prompt to OpenAI and returns the response.
func (s *SimilarArtistsService) callOpenAI(ctx context.Context, prompt string) (string, error) {
	if s.config.OpenAI.APIKey == "" {
		return "", fmt.Errorf("OpenAI API key not configured")
	}

	model := s.config.GetVibesModel()
	req := llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: "user", Content: prompt},
		},
		MaxTokens:   2000,
		Temperature: 0.7,
		ResponseFormat: &llm.ResponseFormat{
			Type: "json_object",
		},
	}

	resp, err := s.llm.Chat(ctx, req)
	if err != nil {
		return "", err
	}

	return resp.Choices[0].Message.Content, nil
}

// parseJSONFromResponse extracts and parses JSON from the AI response.
func parseJSONFromResponse(response string, v interface{}) error {
	// Try to find JSON in the response
	content := strings.TrimSpace(response)

	// Look for JSON block markers
	if idx := strings.Index(content, "```json"); idx != -1 {
		content = content[idx+7:]
		if endIdx := strings.Index(content, "```"); endIdx != -1 {
			content = content[:endIdx]
		}
	} else if idx := strings.Index(content, "```"); idx != -1 {
		content = content[idx+3:]
		if endIdx := strings.Index(content, "```"); endIdx != -1 {
			content = content[:endIdx]
		}
	}

	// Try to find JSON object
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start != -1 && end != -1 && end > start {
		content = content[start : end+1]
	}

	content = strings.TrimSpace(content)

	if err := json.Unmarshal([]byte(content), v); err != nil {
		return fmt.Errorf("failed to parse JSON: %w (content: %s)", err, content[:min(200, len(content))])
	}

	return nil
}

// storeSimilarArtists stores the similar artist relationships in the database.
func (s *SimilarArtistsService) storeSimilarArtists(ctx context.Context, userID int, sourceArtistID int, similarArtists []struct {
	Name       string  `json:"name"`
	ID         int     `json:"id"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}) error {
	// Get user
	u, err := s.client.User.Get(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	// Get source artist
	sourceArtist, err := s.client.Artist.Get(ctx, sourceArtistID)
	if err != nil {
		return fmt.Errorf("failed to get source artist: %w", err)
	}

	// Delete existing similar artist entries for this source artist and provider
	_, err = s.client.SimilarArtist.Delete().
		Where(
			similarartist.SourceArtistID(sourceArtistID),
			similarartist.Provider(ProviderOpenAI),
			similarartist.HasUserWith(user.ID(userID)),
		).
		Exec(ctx)
	if err != nil {
		s.logger.Warn("failed to delete existing similar artists", "error", err)
	}

	// Create new entries
	for rank, sa := range similarArtists {
		// Verify the similar artist exists and belongs to the user
		similarArtistEntity, err := s.client.Artist.Query().
			Where(
				artist.ID(sa.ID),
				artist.HasUserWith(user.ID(userID)),
			).
			Only(ctx)
		if err != nil {
			// Try fuzzy matching by name if ID doesn't work
			similarArtistEntity, err = s.client.Artist.Query().
				Where(
					artist.NameContainsFold(sa.Name),
					artist.HasUserWith(user.ID(userID)),
				).
				First(ctx)
			if err != nil {
				s.logger.Warn("similar artist not found in library",
					"name", sa.Name,
					"id", sa.ID,
					"error", err)
				continue
			}
		}

		// Create the similar artist relationship
		_, err = s.client.SimilarArtist.Create().
			SetSourceArtist(sourceArtist).
			SetSimilarArtist(similarArtistEntity).
			SetUser(u).
			SetProvider(ProviderOpenAI).
			SetConfidence(sa.Confidence).
			SetRank(rank + 1).
			SetReason(sa.Reason).
			Save(ctx)
		if err != nil {
			s.logger.Warn("failed to create similar artist entry",
				"source_artist", sourceArtistID,
				"similar_artist", similarArtistEntity.ID,
				"error", err)
			continue
		}
	}

	return nil
}

// GetSimilarArtists retrieves similar artists for a given artist.
func (s *SimilarArtistsService) GetSimilarArtists(ctx context.Context, userID int, artistID int) ([]*ent.SimilarArtist, error) {
	return s.client.SimilarArtist.Query().
		Where(
			similarartist.SourceArtistID(artistID),
			similarartist.HasUserWith(user.ID(userID)),
		).
		WithSimilarArtist(func(q *ent.ArtistQuery) {
			q.WithImages()
		}).
		Order(ent.Asc(similarartist.FieldRank)).
		All(ctx)
}

// FindSimilarArtistsForAll finds similar artists for all artists in a user's library.
// This is meant to be run as a background job.
func (s *SimilarArtistsService) FindSimilarArtistsForAll(ctx context.Context, userID int) error {
	// Get all artists that haven't been processed recently
	cutoff := time.Now().Add(-24 * time.Hour * 7) // Reprocess weekly

	artists, err := s.client.Artist.Query().
		Where(artist.HasUserWith(user.ID(userID))).
		All(ctx)
	if err != nil {
		return fmt.Errorf("failed to get artists: %w", err)
	}

	s.logger.Info("starting similar artist enrichment",
		"user_id", userID,
		"artist_count", len(artists))

	processed := 0
	for _, a := range artists {
		// Check if we already have recent similar artists for this artist
		existing, err := s.client.SimilarArtist.Query().
			Where(
				similarartist.SourceArtistID(a.ID),
				similarartist.HasUserWith(user.ID(userID)),
				similarartist.CreatedAtGT(cutoff),
			).
			First(ctx)
		if err == nil && existing != nil {
			continue // Skip if recently processed
		}

		// Find similar artists
		if err := s.FindSimilarArtists(ctx, userID, a.ID); err != nil {
			s.logger.Warn("failed to find similar artists",
				"artist_id", a.ID,
				"artist_name", a.Name,
				"error", err)
			continue
		}

		processed++

		// Add a small delay to avoid rate limiting
		time.Sleep(500 * time.Millisecond)
	}

	s.logger.Info("completed similar artist enrichment",
		"user_id", userID,
		"processed", processed,
		"total", len(artists))

	return nil
}

// ClearSimilarArtists removes all similar artist entries for a given artist.
func (s *SimilarArtistsService) ClearSimilarArtists(ctx context.Context, userID int, artistID int) error {
	_, err := s.client.SimilarArtist.Delete().
		Where(
			similarartist.SourceArtistID(artistID),
			similarartist.HasUserWith(user.ID(userID)),
		).
		Exec(ctx)
	return err
}
