package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/enrichers"
	"spotter/internal/llm"

	"github.com/nfnt/resize"
	_ "golang.org/x/image/webp"
)

// nopHandler is a slog handler that discards all log records.
type nopHandler struct{}

func (nopHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (nopHandler) Handle(context.Context, slog.Record) error { return nil }
func (h nopHandler) WithAttrs([]slog.Attr) slog.Handler      { return h }
func (h nopHandler) WithGroup(string) slog.Handler           { return h }

const (
	defaultTimeout = 120 * time.Second
	defaultModel   = "gpt-4o"
)

// Enricher implements the OpenAI metadata enricher.
type Enricher struct {
	logger    *slog.Logger
	config    *config.Config
	llm       *llm.Client
	templates map[string]*template.Template
}

// Ensure Enricher implements interfaces
var _ enrichers.Enricher = (*Enricher)(nil)
var _ enrichers.ArtistEnricher = (*Enricher)(nil)
var _ enrichers.AlbumEnricher = (*Enricher)(nil)
var _ enrichers.TrackEnricher = (*Enricher)(nil)

// Response types for parsing OpenAI JSON responses
type ArtistResponse struct {
	Biography string   `json:"biography"`
	Summary   string   `json:"summary"`
	Tags      []string `json:"tags"`
}

type DominantColor struct {
	Name string `json:"name"`
	Hex  string `json:"hex"`
}

type AlbumResponse struct {
	Summary            string                `json:"summary"`
	Tags               []string              `json:"tags"`
	DominantColors     []DominantColor       `json:"dominant_colors"`
	CoverArtCommentary string                `json:"cover_art_commentary"`
	Recommendations    []AlbumRecommendation `json:"recommendations"`
}

// AlbumRecommendation mirrors the structure from the AI prompt response.
type AlbumRecommendation struct {
	Name   string `json:"name"`
	Artist string `json:"artist"`
	Year   int    `json:"year"`
	Reason string `json:"reason"`
}

type TrackResponse struct {
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
}

// Template data structures
type ArtistTemplateData struct {
	Name          string
	SortName      string
	Bio           string
	Genres        []string
	Tags          []string
	Popularity    *int
	FollowerCount *int
	MusicBrainzID string
	SpotifyID     string
	LastFMURL     string
	Albums        []AlbumInfo
	Tracks        []TrackInfo
}

type AlbumInfo struct {
	Name  string
	Year  int
	Genre string
}

type TrackInfo struct {
	Name      string
	AlbumName string
}

type AlbumTemplateData struct {
	Name          string
	Artist        string
	Year          int
	ReleaseDate   string
	AlbumType     string
	Label         string
	Genre         string
	Tags          []string
	Popularity    int
	TotalTracks   int
	MusicBrainzID string
	SpotifyID     string
	Tracks        []AlbumTrackInfo
	ArtistBio     string
	ArtistGenres  []string
	HasCoverArt   bool
}

type AlbumTrackInfo struct {
	TrackNumber int
	Name        string
	Duration    string
	Listens     int
}

type TrackTemplateData struct {
	Name             string
	Artist           string
	Album            string
	TrackNumber      *int
	DiscNumber       *int
	Duration         string
	BPM              *float64
	MusicalKey       *string
	Energy           *float64
	Danceability     *float64
	Valence          *float64
	Acousticness     *float64
	Instrumentalness *float64
	Tags             []string
	Genres           []string
	MusicBrainzID    *string
	SpotifyID        *string
	ArtistBio        string
	ArtistGenres     []string
	AlbumYear        int
	AlbumGenre       string
}

// New creates a new OpenAI enricher factory.
func New(logger *slog.Logger, cfg *config.Config) enrichers.Factory {
	return func(ctx context.Context, user *ent.User) (enrichers.Enricher, error) {
		if cfg.OpenAI.APIKey == "" {
			return nil, nil
		}

		// Use a no-op logger if none provided
		if logger == nil {
			logger = slog.New(nopHandler{})
		}

		e := &Enricher{
			logger: logger,
			config: cfg,
			llm: llm.NewClient(llm.ClientConfig{
				APIKey:  cfg.OpenAI.APIKey,
				BaseURL: cfg.OpenAI.BaseURL,
				Timeout: defaultTimeout,
			}),
			templates: make(map[string]*template.Template),
		}

		// Load templates
		if err := e.loadTemplates(); err != nil {
			logger.Warn("failed to load AI prompt templates", "error", err)
			// Continue without templates - we'll use fallback prompts
		}

		return e, nil
	}
}

func (e *Enricher) Type() enrichers.Type {
	return enrichers.TypeOpenAI
}

func (e *Enricher) Name() string {
	return "OpenAI"
}

func (e *Enricher) IsAvailable() bool {
	return e.config.OpenAI.APIKey != ""
}

// loadTemplates loads prompt templates from the prompts directory.
func (e *Enricher) loadTemplates() error {
	promptsDir := e.config.Metadata.AI.PromptsDirectory
	if promptsDir == "" {
		promptsDir = "./data/prompts"
	}

	// Template functions
	funcMap := template.FuncMap{
		"mul": func(a, b float64) float64 {
			return a * b
		},
	}

	// Load each template type
	for _, tmplName := range []string{"artist", "album", "track"} {
		tmplPath := filepath.Join(promptsDir, tmplName+".tmpl")
		content, err := os.ReadFile(tmplPath)
		if err != nil {
			e.logger.Debug("template not found, will use fallback", "template", tmplName, "path", tmplPath)
			continue
		}

		tmpl, err := template.New(tmplName).Funcs(funcMap).Parse(string(content))
		if err != nil {
			return fmt.Errorf("failed to parse template %s: %w", tmplName, err)
		}

		e.templates[tmplName] = tmpl
		e.logger.Debug("loaded prompt template", "template", tmplName)
	}

	return nil
}

// callOpenAI makes a request to the OpenAI API.
func (e *Enricher) callOpenAI(ctx context.Context, prompt string, images []string) (string, error) {
	model := e.config.OpenAI.Model
	if model == "" {
		model = defaultModel
	}

	e.logger.Debug("preparing OpenAI request", "model", model, "prompt_length", len(prompt), "image_count", len(images))

	// Build content parts
	content := []llm.ContentPart{
		{
			Type: "text",
			Text: prompt,
		},
	}

	// Add images if provided
	for _, imgPath := range images {
		imgData, err := e.loadImageAsBase64(imgPath)
		if err != nil {
			e.logger.Debug("failed to load image, skipping", "path", imgPath, "error", err)
			continue
		}

		content = append(content, llm.ContentPart{
			Type: "image_url",
			ImageURL: &llm.ImageURL{
				URL:    fmt.Sprintf("data:image/jpeg;base64,%s", imgData),
				Detail: "low", // Use low detail to save tokens
			},
		})
	}

	req := llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{
				Role:    "user",
				Content: content,
			},
		},
		MaxTokens:   2000,
		Temperature: 0.7,
		ResponseFormat: &llm.ResponseFormat{
			Type: "json_object",
		},
	}

	resp, err := e.llm.Chat(ctx, req)
	if err != nil {
		e.logger.Error("OpenAI request failed", "error", err)
		return "", fmt.Errorf("request failed: %w", err)
	}

	return resp.Choices[0].Message.Content, nil
}

// loadImageAsBase64 loads an image file and returns it as base64.
// loadImageAsBase64 loads an image, resizes it, and returns it as a base64 encoded string.
func (e *Enricher) loadImageAsBase64(imagePath string) (string, error) {
	file, err := os.Open(imagePath)
	if err != nil {
		return "", fmt.Errorf("failed to open image: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			e.logger.Warn("failed to close file", "error", err)
		}
	}()

	img, _, err := image.Decode(file)
	if err != nil {
		return "", fmt.Errorf("failed to decode image: %w", err)
	}

	// Resize to a max of 512px on the longest side to save tokens
	resizedImg := resize.Thumbnail(512, 512, img, resize.Lanczos3)

	var buf bytes.Buffer
	// Re-encode as JPEG for efficiency
	if err := jpeg.Encode(&buf, resizedImg, &jpeg.Options{Quality: 85}); err != nil {
		return "", fmt.Errorf("failed to encode resized image: %w", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// parseJSONResponse extracts JSON from the response, handling markdown code blocks.
func parseJSONResponse(response string) string {
	var jsonStr string

	// Try to find JSON in markdown code blocks
	if start := strings.Index(response, "```json"); start != -1 {
		start += 7
		if end := strings.Index(response[start:], "```"); end != -1 {
			jsonStr = strings.TrimSpace(response[start : start+end])
		}
	}

	// Try plain code blocks
	if jsonStr == "" {
		if start := strings.Index(response, "```"); start != -1 {
			start += 3
			// Skip any language identifier on the same line
			if newline := strings.Index(response[start:], "\n"); newline != -1 {
				start += newline + 1
			}
			if end := strings.Index(response[start:], "```"); end != -1 {
				jsonStr = strings.TrimSpace(response[start : start+end])
			}
		}
	}

	// Try to find raw JSON object
	if jsonStr == "" {
		if start := strings.Index(response, "{"); start != -1 {
			if end := strings.LastIndex(response, "}"); end != -1 && end > start {
				jsonStr = strings.TrimSpace(response[start : end+1])
			}
		}
	}

	if jsonStr == "" {
		jsonStr = strings.TrimSpace(response)
	}

	return jsonStr
}

// deduplicateTags removes duplicate tags (case-insensitive) and limits to max tags.
func deduplicateTags(newTags, existingTags []string, maxTags int) []string {
	seen := make(map[string]bool)

	// Add existing tags to seen set (lowercase for comparison)
	for _, tag := range existingTags {
		seen[strings.ToLower(strings.TrimSpace(tag))] = true
	}

	var result []string
	for _, tag := range newTags {
		normalizedTag := strings.ToLower(strings.TrimSpace(tag))
		if normalizedTag == "" {
			continue
		}
		if !seen[normalizedTag] {
			seen[normalizedTag] = true
			result = append(result, strings.TrimSpace(tag))
			if len(result) >= maxTags {
				break
			}
		}
	}

	return result
}

// EnrichArtist fetches AI-generated metadata for an artist.
func (e *Enricher) EnrichArtist(ctx context.Context, artist *ent.Artist) (*enrichers.ArtistData, error) {
	// Check if we've enriched this artist recently (within 30 days)
	if artist.LastAiEnrichedAt != nil && time.Since(*artist.LastAiEnrichedAt) < 30*24*time.Hour {
		e.logger.Debug("skipping AI enrichment for artist (recently enriched)", "name", artist.Name, "last_enriched", artist.LastAiEnrichedAt)
		return nil, nil
	}

	e.logger.Info("AI enricher called for artist", "name", artist.Name, "id", artist.ID)

	// Build template data
	data := ArtistTemplateData{
		Name:          artist.Name,
		SortName:      artist.SortName,
		Bio:           artist.Bio,
		Genres:        artist.Genres,
		Tags:          artist.Tags,
		Popularity:    artist.Popularity,
		FollowerCount: artist.FollowerCount,
		MusicBrainzID: artist.MusicbrainzID,
		SpotifyID:     artist.SpotifyID,
		LastFMURL:     artist.LastfmURL,
	}

	// Load albums if available
	if artist.Edges.Albums != nil {
		for _, album := range artist.Edges.Albums {
			data.Albums = append(data.Albums, AlbumInfo{
				Name:  album.Name,
				Year:  album.Year,
				Genre: album.Genre,
			})
		}
	}

	// Load tracks if available
	if artist.Edges.Tracks != nil {
		for _, track := range artist.Edges.Tracks {
			info := TrackInfo{Name: track.Name}
			if track.Edges.Album != nil {
				info.AlbumName = track.Edges.Album.Name
			}
			data.Tracks = append(data.Tracks, info)
		}
	}

	// Generate prompt
	var prompt string
	if tmpl, ok := e.templates["artist"]; ok {
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			e.logger.Warn("failed to execute artist template", "error", err)
			prompt = e.fallbackArtistPrompt(data)
		} else {
			prompt = buf.String()
		}
		e.logger.Debug("using template prompt for artist", "artist", artist.Name, "prompt_length", len(prompt))
	} else {
		prompt = e.fallbackArtistPrompt(data)
		e.logger.Debug("using fallback prompt for artist", "artist", artist.Name, "prompt_length", len(prompt))
	}

	// Collect artist images
	var images []string
	if artist.Edges.Images != nil {
		for _, img := range artist.Edges.Images {
			if img.LocalPath != "" {
				images = append(images, img.LocalPath)
			}
		}
	}
	e.logger.Info("calling OpenAI API for artist", "artist", artist.Name, "image_count", len(images))

	// Call OpenAI
	response, err := e.callOpenAI(ctx, prompt, images)
	if err != nil {
		return nil, fmt.Errorf("OpenAI API call failed: %w", err)
	}

	// Parse response
	jsonStr := parseJSONResponse(response)
	var aiResp ArtistResponse
	if err := json.Unmarshal([]byte(jsonStr), &aiResp); err != nil {
		e.logger.Warn("failed to parse AI response as JSON", "error", err, "response", response)
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}

	// Deduplicate tags
	existingTags := append(artist.Tags, artist.Genres...)
	aiTags := deduplicateTags(aiResp.Tags, existingTags, 5)

	result := &enrichers.ArtistData{
		AISummary:   aiResp.Summary,
		AIBiography: aiResp.Biography,
		AITags:      aiTags,
	}

	e.logger.Debug("enriched artist with AI",
		"artist", artist.Name,
		"has_summary", result.AISummary != "",
		"has_biography", result.AIBiography != "",
		"tags", len(result.AITags))

	return result, nil
}

// fallbackArtistPrompt generates a simple prompt when template is not available.
func (e *Enricher) fallbackArtistPrompt(data ArtistTemplateData) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Analyze the music artist \"%s\".\n\n", data.Name))

	if data.Bio != "" {
		sb.WriteString(fmt.Sprintf("Existing bio: %s\n\n", data.Bio))
	}

	if len(data.Genres) > 0 {
		sb.WriteString(fmt.Sprintf("Genres: %s\n", strings.Join(data.Genres, ", ")))
	}

	sb.WriteString("\nRespond with JSON containing: biography (2-4 paragraphs), summary (1-2 sentences), and tags (up to 5).")
	sb.WriteString("\n\nFormat: {\"biography\": \"...\", \"summary\": \"...\", \"tags\": [\"tag1\", ...]}")

	return sb.String()
}

// GetArtistImages returns empty for AI enricher (images come from other enrichers).
func (e *Enricher) GetArtistImages(ctx context.Context, artist *ent.Artist) ([]enrichers.ImageData, error) {
	return nil, nil
}

// EnrichAlbum fetches AI-generated metadata for an album.
func (e *Enricher) EnrichAlbum(ctx context.Context, album *ent.Album) (*enrichers.AlbumData, error) {
	e.logger.Info("AI enricher called for album", "name", album.Name, "id", album.ID)

	artistName := ""
	artistBio := ""
	var artistGenres []string
	if album.Edges.Artist != nil {
		artistName = album.Edges.Artist.Name
		artistBio = album.Edges.Artist.Bio
		artistGenres = album.Edges.Artist.Genres
	}

	// Build template data
	data := AlbumTemplateData{
		Name:          album.Name,
		Artist:        artistName,
		Year:          album.Year,
		ReleaseDate:   album.ReleaseDate,
		AlbumType:     album.AlbumType,
		Label:         album.Label,
		Genre:         album.Genre,
		Tags:          album.Tags,
		Popularity:    album.Popularity,
		TotalTracks:   album.TotalTracks,
		MusicBrainzID: album.MusicbrainzID,
		SpotifyID:     album.SpotifyID,
		ArtistBio:     artistBio,
		ArtistGenres:  artistGenres,
	}

	// Load tracks if available
	if album.Edges.Tracks != nil {
		for _, track := range album.Edges.Tracks {
			info := AlbumTrackInfo{
				Name:    track.Name,
				Listens: len(track.Edges.Listens),
			}
			if track.TrackNumber != nil {
				info.TrackNumber = *track.TrackNumber
			}
			if track.DurationMs != nil {
				info.Duration = formatDuration(*track.DurationMs)
			}
			data.Tracks = append(data.Tracks, info)
		}
	}

	// Generate prompt
	var prompt string
	if tmpl, ok := e.templates["album"]; ok {
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			e.logger.Warn("failed to execute album template", "error", err)
			prompt = e.fallbackAlbumPrompt(data)
		} else {
			prompt = buf.String()
		}
	} else {
		prompt = e.fallbackAlbumPrompt(data)
	}

	// Collect album images
	var images []string
	if album.Edges.Images != nil {
		for _, img := range album.Edges.Images {
			if img.LocalPath != "" {
				images = append(images, img.LocalPath)
			}
		}
	}

	if len(images) > 0 {
		data.HasCoverArt = true
	}

	// Call OpenAI
	response, err := e.callOpenAI(ctx, prompt, images)
	if err != nil {
		return nil, fmt.Errorf("OpenAI API call failed: %w", err)
	}

	// Parse response
	jsonStr := parseJSONResponse(response)
	var aiResp AlbumResponse
	if err := json.Unmarshal([]byte(jsonStr), &aiResp); err != nil {
		e.logger.Warn("failed to parse AI response as JSON", "error", err, "response", response)
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}

	// Deduplicate tags
	existingTags := album.Tags
	if album.Genre != "" {
		existingTags = append(existingTags, album.Genre)
	}
	aiTags := deduplicateTags(aiResp.Tags, existingTags, 5)

	// Convert dominant colors to a string slice for storage
	var dominantColors []string
	for _, c := range aiResp.DominantColors {
		// Use a pipe separator as it's unlikely to be in color names
		dominantColors = append(dominantColors, fmt.Sprintf("%s|%s", c.Name, c.Hex))
	}

	// Convert recommendations
	var recommendations []enrichers.RecommendedAlbum
	for _, rec := range aiResp.Recommendations {
		recommendations = append(recommendations, enrichers.RecommendedAlbum{
			Name:   rec.Name,
			Artist: rec.Artist,
			Year:   rec.Year,
			Reason: rec.Reason,
		})
	}

	result := &enrichers.AlbumData{
		AISummary:          aiResp.Summary,
		AITags:             aiTags,
		DominantColors:     dominantColors,
		CoverArtCommentary: aiResp.CoverArtCommentary,
		Recommendations:    recommendations,
	}

	e.logger.Debug("enriched album with AI",
		"album", album.Name,
		"has_summary", result.AISummary != "",
		"tags", len(result.AITags),
		"colors", len(result.DominantColors),
		"commentary", result.CoverArtCommentary != "",
		"recommendations", len(result.Recommendations))

	return result, nil
}

// fallbackAlbumPrompt generates a simple prompt when template is not available.
func (e *Enricher) fallbackAlbumPrompt(data AlbumTemplateData) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Analyze the album \"%s\"", data.Name))
	if data.Artist != "" {
		sb.WriteString(fmt.Sprintf(" by %s", data.Artist))
	}
	sb.WriteString(".\n\n")

	if data.Year > 0 {
		sb.WriteString(fmt.Sprintf("Year: %d\n", data.Year))
	}
	if data.Genre != "" {
		sb.WriteString(fmt.Sprintf("Genre: %s\n", data.Genre))
	}

	sb.WriteString("\nRespond with JSON containing: summary (2-3 paragraphs about the album) and tags (up to 5).")
	sb.WriteString("\n\nFormat: {\"summary\": \"...\", \"tags\": [\"tag1\", ...]}")

	return sb.String()
}

// GetAlbumImages returns empty for AI enricher (images come from other enrichers).
func (e *Enricher) GetAlbumImages(ctx context.Context, album *ent.Album) ([]enrichers.ImageData, error) {
	return nil, nil
}

// EnrichTrack fetches AI-generated metadata for a track.
func (e *Enricher) EnrichTrack(ctx context.Context, track *ent.Track) (*enrichers.TrackData, error) {
	e.logger.Info("AI enricher called for track", "name", track.Name, "id", track.ID)

	artistName := ""
	artistBio := ""
	var artistGenres []string
	if track.Edges.Artist != nil {
		artistName = track.Edges.Artist.Name
		artistBio = track.Edges.Artist.Bio
		artistGenres = track.Edges.Artist.Genres
	}

	albumName := ""
	albumYear := 0
	albumGenre := ""
	if track.Edges.Album != nil {
		albumName = track.Edges.Album.Name
		albumYear = track.Edges.Album.Year
		albumGenre = track.Edges.Album.Genre
	}

	// Build template data
	data := TrackTemplateData{
		Name:             track.Name,
		Artist:           artistName,
		Album:            albumName,
		TrackNumber:      track.TrackNumber,
		DiscNumber:       track.DiscNumber,
		BPM:              track.Bpm,
		MusicalKey:       track.MusicalKey,
		Energy:           track.Energy,
		Danceability:     track.Danceability,
		Valence:          track.Valence,
		Acousticness:     track.Acousticness,
		Instrumentalness: track.Instrumentalness,
		Tags:             track.Tags,
		Genres:           track.Genres,
		MusicBrainzID:    track.MusicbrainzID,
		SpotifyID:        track.SpotifyID,
		ArtistBio:        artistBio,
		ArtistGenres:     artistGenres,
		AlbumYear:        albumYear,
		AlbumGenre:       albumGenre,
	}

	if track.DurationMs != nil {
		data.Duration = formatDuration(*track.DurationMs)
	}

	// Generate prompt
	var prompt string
	if tmpl, ok := e.templates["track"]; ok {
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			e.logger.Warn("failed to execute track template", "error", err)
			prompt = e.fallbackTrackPrompt(data)
		} else {
			prompt = buf.String()
		}
	} else {
		prompt = e.fallbackTrackPrompt(data)
	}

	// Call OpenAI (no images for tracks)
	response, err := e.callOpenAI(ctx, prompt, nil)
	if err != nil {
		return nil, fmt.Errorf("OpenAI API call failed: %w", err)
	}

	// Parse response
	jsonStr := parseJSONResponse(response)
	var aiResp TrackResponse
	if err := json.Unmarshal([]byte(jsonStr), &aiResp); err != nil {
		e.logger.Warn("failed to parse AI response as JSON", "error", err, "response", response)
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}

	// Deduplicate tags
	existingTags := append(track.Tags, track.Genres...)
	aiTags := deduplicateTags(aiResp.Tags, existingTags, 5)

	result := &enrichers.TrackData{
		AISummary: aiResp.Summary,
		AITags:    aiTags,
	}

	e.logger.Debug("enriched track with AI",
		"track", track.Name,
		"has_summary", result.AISummary != "",
		"tags", len(result.AITags))

	return result, nil
}

// fallbackTrackPrompt generates a simple prompt when template is not available.
func (e *Enricher) fallbackTrackPrompt(data TrackTemplateData) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Analyze the track \"%s\"", data.Name))
	if data.Artist != "" {
		sb.WriteString(fmt.Sprintf(" by %s", data.Artist))
	}
	if data.Album != "" {
		sb.WriteString(fmt.Sprintf(" from the album \"%s\"", data.Album))
	}
	sb.WriteString(".\n\n")

	if data.Duration != "" {
		sb.WriteString(fmt.Sprintf("Duration: %s\n", data.Duration))
	}

	sb.WriteString("\nRespond with JSON containing: summary (1-2 paragraphs) and tags (up to 5).")
	sb.WriteString("\n\nFormat: {\"summary\": \"...\", \"tags\": [\"tag1\", ...]}")

	return sb.String()
}

// formatDuration converts milliseconds to a human-readable duration.
func formatDuration(ms int) string {
	seconds := ms / 1000
	minutes := seconds / 60
	secs := seconds % 60
	if minutes >= 60 {
		hours := minutes / 60
		mins := minutes % 60
		return fmt.Sprintf("%d:%02d:%02d", hours, mins, secs)
	}
	return fmt.Sprintf("%d:%02d", minutes, secs)
}
