// Governing: ADR-0017 (vibes generator abstraction), ADR-0007 (event bus),
// ADR-0013 (background goroutines), issue #339 (panic recovery), issue #340
// (one generation path and one form parser for every mixtape entry point)

package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"spotter/ent"
	"spotter/internal/vibes"
)

const (
	// defaultMixtapeMaxTracks is what a form gets when it does not say; it
	// matches the ent schema default.
	defaultMixtapeMaxTracks = 25
	minMixtapeMaxTracks     = 1
	maxMixtapeMaxTracks     = 100
)

// mixtapeForm is the part of a mixtape form that every entry point asks for:
// the DJ that will generate it, the track-count cap, and the optional name.
type mixtapeForm struct {
	dj        *ent.DJ
	name      string
	maxTracks int
}

// parseMixtapeForm reads those shared fields from an already-parsed request
// form, resolving the DJ against the user's own DJs. It writes the failure
// response itself and reports whether the handler should stop.
//
// A max_tracks outside 1-100 is not an error: the form keeps the default, the
// way every entry point has always treated a stray value.
func (h *Handler) parseMixtapeForm(w http.ResponseWriter, r *http.Request, userID int) (*mixtapeForm, bool) {
	djID, err := strconv.Atoi(r.FormValue("dj_id"))
	if err != nil {
		http.Error(w, "DJ is required", http.StatusBadRequest)
		return nil, false
	}

	// Verify DJ ownership
	d, err := h.GetDJForUser(r.Context(), djID, userID)
	if err != nil {
		http.Error(w, "DJ not found", http.StatusNotFound)
		return nil, false
	}

	maxTracks := defaultMixtapeMaxTracks
	if maxTracksStr := r.FormValue("max_tracks"); maxTracksStr != "" {
		if mt, err := strconv.Atoi(maxTracksStr); err == nil && mt >= minMixtapeMaxTracks && mt <= maxMixtapeMaxTracks {
			maxTracks = mt
		}
	}

	return &mixtapeForm{
		dj:        d,
		name:      strings.TrimSpace(r.FormValue("name")),
		maxTracks: maxTracks,
	}, true
}

// startMixtapeGeneration kicks off generation for an already-persisted mixtape
// and returns immediately: generation is slow and the UI follows the progress
// over SSE. The album page, the artist page and the vibes page all arrive
// here, so a generation result is recorded the same way whichever page asked.
//
// A nil generator means generation is not configured; the mixtape stays as it
// is, ready to be generated once it is, as before.
func (h *Handler) startMixtapeGeneration(userID int, m *ent.Mixtape, d *ent.DJ, seed *vibes.Seed) {
	if h.MixtapeGenerator == nil {
		h.Logger.Error("mixtape generator not initialized", "mixtape_id", m.ID)
		return
	}

	req := &vibes.GenerationRequest{
		Mixtape: m,
		DJ:      d,
		Seed:    seed,
		UserID:  userID,
	}

	// Capture the value types the goroutine needs so it never dereferences a
	// stale *ent pointer after the request has been served.
	// Governing: SPEC graceful-shutdown, SPEC vibes-ai-mixtape-engine
	// ("background goroutines use context.Background()")
	mixtapeID := m.ID
	mixtapeName := m.Name
	djName := d.Name

	go func() {
		defer h.recoverMixtapeGeneration(userID, mixtapeID, mixtapeName)

		ctx := context.Background()

		if h.Bus != nil {
			h.Bus.PublishMixtapeGenerating(userID, mixtapeID, mixtapeName, djName)
		}

		result, err := h.MixtapeGenerator.GenerateMixtape(ctx, req)
		if err != nil {
			h.Logger.Error("mixtape generation failed",
				"mixtape_id", mixtapeID,
				"error", err)

			if _, saveErr := h.Client.Mixtape.UpdateOneID(mixtapeID).
				SetGenerationError(err.Error()).
				Save(ctx); saveErr != nil {
				h.Logger.Error("failed to save mixtape error", "error", saveErr)
			}

			if h.Bus != nil {
				h.Bus.PublishMixtapeError(userID, mixtapeID, mixtapeName, err.Error())
			}
			return
		}

		trackIDs := result.GetMatchedTrackIDsAsStrings()

		updater := h.Client.Mixtape.UpdateOneID(mixtapeID).
			SetTrackIds(trackIDs).
			SetTrackCount(len(trackIDs)).
			SetLastGeneratedAt(time.Now()).
			SetGenerationPrompt(result.PromptUsed).
			SetGenerationModel(result.ModelUsed).
			ClearGenerationError()

		if result.TokensUsed > 0 {
			updater.SetGenerationTokensUsed(result.TokensUsed)
		}

		// Persist what the generation was seeded from, so a mixtape created
		// from an album, an artist or a track list records the same way.
		if seed != nil {
			updater.SetSeedType(string(seed.Type))
			if seed.Type == vibes.SeedTypeArtist && seed.Artist != nil {
				updater.SetSeedID(seed.Artist.ID)
			} else if seed.Type == vibes.SeedTypeAlbum && seed.Album != nil {
				updater.SetSeedID(seed.Album.ID)
			} else if seed.Type == vibes.SeedTypeTracks && len(seed.TrackIDs) > 0 {
				seedTrackIDs := make([]string, len(seed.TrackIDs))
				for i, id := range seed.TrackIDs {
					seedTrackIDs[i] = strconv.Itoa(id)
				}
				updater.SetSeedTrackIds(seedTrackIDs)
			}
		}

		if _, err := updater.Save(ctx); err != nil {
			h.Logger.Error("failed to save mixtape generation results",
				"mixtape_id", mixtapeID,
				"error", err)
			return
		}

		h.Logger.Info("mixtape generation complete",
			"mixtape_id", mixtapeID,
			"tracks_matched", result.MatchedCount,
			"tracks_unmatched", result.UnmatchedCount,
			"tokens_used", result.TokensUsed)

		if h.Bus != nil {
			h.Bus.PublishMixtapeGenerated(userID, mixtapeID, mixtapeName, djName,
				len(result.Tracks), result.MatchedCount, result.TokensUsed)
		}
	}()
}
