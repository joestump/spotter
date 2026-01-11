package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"spotter/ent"
	"spotter/internal/auth"
	"spotter/internal/config"
	"spotter/internal/crypto"
	"spotter/internal/events"
	"spotter/internal/services"
	"spotter/internal/vibes"

	"github.com/a-h/templ"
)

// Input validation constants
const (
	MaxNameLength        = 255
	MaxDescriptionLength = 2000
	MaxPromptLength      = 10000
	MaxURLLength         = 2048
)

// CookieName is the name of the JWT authentication cookie
const CookieName = "spotter_token"

type contextKey string

const UserContextKey contextKey = "user"

type Handler struct {
	Client            *ent.Client
	Config            *config.Config
	Logger            *slog.Logger
	Encryptor         *crypto.Encryptor
	JWTManager        *auth.JWTManager
	Syncer            *services.Syncer
	MetadataSvc       *services.MetadataService
	PlaylistSyncSvc   *services.PlaylistSyncService
	MixtapeGenerator  *vibes.MixtapeGenerator
	PlaylistEnhancer  *vibes.PlaylistEnhancer
	SimilarArtistsSvc *services.SimilarArtistsService
	Bus               *events.Bus
}

func New(client *ent.Client, cfg *config.Config, logger *slog.Logger, encryptor *crypto.Encryptor, jwtManager *auth.JWTManager, syncer *services.Syncer, metadataSvc *services.MetadataService, playlistSyncSvc *services.PlaylistSyncService, mixtapeGen *vibes.MixtapeGenerator, playlistEnhancer *vibes.PlaylistEnhancer, similarArtistsSvc *services.SimilarArtistsService, bus *events.Bus) *Handler {
	return &Handler{
		Client:            client,
		Config:            cfg,
		Logger:            logger,
		Encryptor:         encryptor,
		JWTManager:        jwtManager,
		Syncer:            syncer,
		MetadataSvc:       metadataSvc,
		PlaylistSyncSvc:   playlistSyncSvc,
		MixtapeGenerator:  mixtapeGen,
		PlaylistEnhancer:  playlistEnhancer,
		SimilarArtistsSvc: similarArtistsSvc,
		Bus:               bus,
	}
}

func (h *Handler) Render(w http.ResponseWriter, r *http.Request, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := component.Render(r.Context(), w); err != nil {
		h.Logger.Error("failed to render component", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (h *Handler) GetUser(ctx context.Context) *ent.User {
	u, ok := ctx.Value(UserContextKey).(*ent.User)
	if !ok {
		return nil
	}
	return u
}

// ValidationError represents an input validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidateRequired checks that a string field is not empty
func ValidateRequired(field, value string) *ValidationError {
	if value == "" {
		return &ValidationError{Field: field, Message: "is required"}
	}
	return nil
}

// ValidateMaxLength checks that a string doesn't exceed the maximum length
func ValidateMaxLength(field, value string, maxLen int) *ValidationError {
	if len(value) > maxLen {
		return &ValidationError{Field: field, Message: fmt.Sprintf("exceeds maximum length of %d characters", maxLen)}
	}
	return nil
}

// ValidateRange checks that an integer is within the specified range
func ValidateRange(field string, value, min, max int) *ValidationError {
	if value < min || value > max {
		return &ValidationError{Field: field, Message: fmt.Sprintf("must be between %d and %d", min, max)}
	}
	return nil
}

// BadRequest sends a 400 response with the validation error message
func (h *Handler) BadRequest(w http.ResponseWriter, err *ValidationError) {
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// ValidateTimestamp checks that a string is a valid RFC3339 timestamp
func ValidateTimestamp(field, value string) *ValidationError {
	if value == "" {
		return nil // Empty is ok if field is optional
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return &ValidationError{Field: field, Message: "must be a valid RFC3339 timestamp (e.g., 2006-01-02T15:04:05Z)"}
	}
	return nil
}

// ParseTimestamp parses a string as RFC3339 timestamp, returning an error if invalid
func ParseTimestamp(field, value string) (time.Time, *ValidationError) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, &ValidationError{Field: field, Message: "must be a valid RFC3339 timestamp (e.g., 2006-01-02T15:04:05Z)"}
	}
	return t, nil
}
