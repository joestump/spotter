package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"spotter/ent"
	"spotter/ent/artist"
	"spotter/ent/dj"
	"spotter/ent/listen"
	"spotter/ent/mixtape"
	"spotter/ent/track"
	"spotter/ent/user"
	"spotter/internal/vibes"
	vibesViews "spotter/internal/views/vibes"

	"github.com/go-chi/chi/v5"
)

// VibesRedirect redirects to the DJs page
func (h *Handler) VibesRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/vibes/djs", http.StatusSeeOther)
}

// DJsIndex shows the list of DJs
func (h *Handler) DJsIndex(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	djs, err := h.Client.DJ.Query().
		Where(dj.HasUserWith(user.ID(u.ID))).
		WithMixtapes().
		Order(ent.Desc(dj.FieldCreatedAt)).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to query DJs", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	h.Render(w, r, vibesViews.DJsIndex(djs, h.Config))
}

// DJShow shows a single DJ
func (h *Handler) DJShow(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	djID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid DJ ID", http.StatusBadRequest)
		return
	}

	// Get the DJ with mixtapes
	d, err := h.Client.DJ.Query().
		Where(
			dj.ID(djID),
			dj.HasUserWith(user.ID(u.ID)),
		).
		WithMixtapes().
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to get DJ", "error", err, "id", djID)
		http.Error(w, "DJ not found", http.StatusNotFound)
		return
	}

	h.Render(w, r, vibesViews.DJShow(d, h.Config))
}

// CreateDJ creates a new DJ
func (h *Handler) CreateDJ(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if err := ValidateRequired("name", name); err != nil {
		h.BadRequest(w, err)
		return
	}
	if err := ValidateMaxLength("name", name, MaxNameLength); err != nil {
		h.BadRequest(w, err)
		return
	}

	systemPrompt := strings.TrimSpace(r.FormValue("system_prompt"))
	if err := ValidateMaxLength("system_prompt", systemPrompt, MaxPromptLength); err != nil {
		h.BadRequest(w, err)
		return
	}

	genresInclude := parseCommaSeparated(r.FormValue("genres_include"))
	genresExclude := parseCommaSeparated(r.FormValue("genres_exclude"))
	vibesTags := parseCommaSeparated(r.FormValue("vibes"))
	artistsInclude := parseCommaSeparated(r.FormValue("artists_include"))
	artistsExclude := parseCommaSeparated(r.FormValue("artists_exclude"))

	_, err := h.Client.DJ.Create().
		SetName(name).
		SetSystemPrompt(systemPrompt).
		SetGenresInclude(genresInclude).
		SetGenresExclude(genresExclude).
		SetVibes(vibesTags).
		SetArtistsInclude(artistsInclude).
		SetArtistsExclude(artistsExclude).
		SetUser(u).
		Save(r.Context())

	if err != nil {
		h.Logger.Error("failed to create DJ", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "dj-created")
	w.WriteHeader(http.StatusOK)
}

// UpdateDJ updates an existing DJ
func (h *Handler) UpdateDJ(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	djID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid DJ ID", http.StatusBadRequest)
		return
	}

	// Verify ownership
	d, err := h.Client.DJ.Query().
		Where(dj.ID(djID), dj.HasUserWith(user.ID(u.ID))).
		Only(r.Context())
	if err != nil {
		http.Error(w, "DJ not found", http.StatusNotFound)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if err := ValidateRequired("name", name); err != nil {
		h.BadRequest(w, err)
		return
	}
	if err := ValidateMaxLength("name", name, MaxNameLength); err != nil {
		h.BadRequest(w, err)
		return
	}

	systemPrompt := strings.TrimSpace(r.FormValue("system_prompt"))
	if err := ValidateMaxLength("system_prompt", systemPrompt, MaxPromptLength); err != nil {
		h.BadRequest(w, err)
		return
	}

	genresInclude := parseCommaSeparated(r.FormValue("genres_include"))
	genresExclude := parseCommaSeparated(r.FormValue("genres_exclude"))
	vibesTags := parseCommaSeparated(r.FormValue("vibes"))
	artistsInclude := parseCommaSeparated(r.FormValue("artists_include"))
	artistsExclude := parseCommaSeparated(r.FormValue("artists_exclude"))

	_, err = h.Client.DJ.UpdateOne(d).
		SetName(name).
		SetSystemPrompt(systemPrompt).
		SetGenresInclude(genresInclude).
		SetGenresExclude(genresExclude).
		SetVibes(vibesTags).
		SetArtistsInclude(artistsInclude).
		SetArtistsExclude(artistsExclude).
		Save(r.Context())

	if err != nil {
		h.Logger.Error("failed to update DJ", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "dj-updated")
	w.WriteHeader(http.StatusOK)
}

// DeleteDJ deletes a DJ
func (h *Handler) DeleteDJ(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	djID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid DJ ID", http.StatusBadRequest)
		return
	}

	// Verify ownership
	_, err = h.Client.DJ.Query().
		Where(dj.ID(djID), dj.HasUserWith(user.ID(u.ID))).
		Only(r.Context())
	if err != nil {
		http.Error(w, "DJ not found", http.StatusNotFound)
		return
	}

	err = h.Client.DJ.DeleteOneID(djID).Exec(r.Context())
	if err != nil {
		h.Logger.Error("failed to delete DJ", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "dj-deleted")
	w.WriteHeader(http.StatusOK)
}

// MixtapesIndex shows the list of Mixtapes
func (h *Handler) MixtapesIndex(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	// Refresh user to get pagination settings
	u, err := h.Client.User.Query().
		Where(user.ID(u.ID)).
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to query user", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Get page number from query
	page := 1
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	pageSize := u.PaginationSize
	offset := (page - 1) * pageSize

	// Query mixtapes with pagination
	mixtapes, err := h.Client.Mixtape.Query().
		Where(mixtape.HasUserWith(user.ID(u.ID))).
		WithDj().
		Order(ent.Desc(mixtape.FieldUpdatedAt)).
		Limit(pageSize).
		Offset(offset).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to query mixtapes", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Get total count for pagination
	total, err := h.Client.Mixtape.Query().
		Where(mixtape.HasUserWith(user.ID(u.ID))).
		Count(r.Context())
	if err != nil {
		h.Logger.Error("failed to count mixtapes", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	totalPages := (total + pageSize - 1) / pageSize

	// Get all DJs for the create modal
	djs, err := h.Client.DJ.Query().
		Where(dj.HasUserWith(user.ID(u.ID))).
		Order(ent.Asc(dj.FieldName)).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to query DJs", "error", err)
		djs = []*ent.DJ{}
	}

	h.Render(w, r, vibesViews.MixtapesIndex(mixtapes, djs, page, totalPages, h.Config))
}

// CreateMixtape creates a new Mixtape
func (h *Handler) CreateMixtape(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if err := ValidateRequired("name", name); err != nil {
		h.BadRequest(w, err)
		return
	}
	if err := ValidateMaxLength("name", name, MaxNameLength); err != nil {
		h.BadRequest(w, err)
		return
	}

	description := strings.TrimSpace(r.FormValue("description"))
	if err := ValidateMaxLength("description", description, MaxDescriptionLength); err != nil {
		h.BadRequest(w, err)
		return
	}

	schedule := r.FormValue("schedule")
	if schedule == "" {
		schedule = "none"
	}

	djID, err := strconv.Atoi(r.FormValue("dj_id"))
	if err != nil {
		http.Error(w, "DJ is required", http.StatusBadRequest)
		return
	}

	// Verify DJ ownership
	d, err := h.Client.DJ.Query().
		Where(dj.ID(djID), dj.HasUserWith(user.ID(u.ID))).
		Only(r.Context())
	if err != nil {
		http.Error(w, "DJ not found", http.StatusNotFound)
		return
	}

	syncToNavidrome := r.FormValue("sync_to_navidrome") == "on"

	maxTracks := 25 // default
	if maxTracksStr := r.FormValue("max_tracks"); maxTracksStr != "" {
		if mt, err := strconv.Atoi(maxTracksStr); err == nil && mt >= 1 && mt <= 100 {
			maxTracks = mt
		}
	}

	m, err := h.Client.Mixtape.Create().
		SetName(name).
		SetDescription(description).
		SetSchedule(mixtape.Schedule(schedule)).
		SetMaxTracks(maxTracks).
		SetSyncToNavidrome(syncToNavidrome).
		SetDj(d).
		SetUser(u).
		Save(r.Context())

	if err != nil {
		h.Logger.Error("failed to create mixtape", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("mixtape created", "mixtape_id", m.ID, "name", m.Name, "dj", d.Name, "user_id", u.ID)

	// Publish event
	if h.Bus != nil {
		h.Bus.PublishMixtapeCreated(u.ID, m.ID, m.Name, d.Name)
	}

	w.Header().Set("HX-Trigger", "mixtape-created")
	w.WriteHeader(http.StatusOK)
}

// UpdateMixtape updates an existing Mixtape
func (h *Handler) UpdateMixtape(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	mixtapeID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid Mixtape ID", http.StatusBadRequest)
		return
	}

	// Verify ownership
	m, err := h.Client.Mixtape.Query().
		Where(mixtape.ID(mixtapeID), mixtape.HasUserWith(user.ID(u.ID))).
		Only(r.Context())
	if err != nil {
		http.Error(w, "Mixtape not found", http.StatusNotFound)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if err := ValidateRequired("name", name); err != nil {
		h.BadRequest(w, err)
		return
	}
	if err := ValidateMaxLength("name", name, MaxNameLength); err != nil {
		h.BadRequest(w, err)
		return
	}

	description := strings.TrimSpace(r.FormValue("description"))
	if err := ValidateMaxLength("description", description, MaxDescriptionLength); err != nil {
		h.BadRequest(w, err)
		return
	}

	schedule := r.FormValue("schedule")
	if schedule == "" {
		schedule = "none"
	}

	djID, err := strconv.Atoi(r.FormValue("dj_id"))
	if err != nil {
		http.Error(w, "DJ is required", http.StatusBadRequest)
		return
	}

	// Verify DJ ownership
	d, err := h.Client.DJ.Query().
		Where(dj.ID(djID), dj.HasUserWith(user.ID(u.ID))).
		Only(r.Context())
	if err != nil {
		http.Error(w, "DJ not found", http.StatusNotFound)
		return
	}

	syncToNavidrome := r.FormValue("sync_to_navidrome") == "on"

	maxTracks := 25 // default
	if maxTracksStr := r.FormValue("max_tracks"); maxTracksStr != "" {
		if mt, err := strconv.Atoi(maxTracksStr); err == nil && mt >= 1 && mt <= 100 {
			maxTracks = mt
		}
	}

	updated, err := h.Client.Mixtape.UpdateOne(m).
		SetName(name).
		SetDescription(description).
		SetSchedule(mixtape.Schedule(schedule)).
		SetMaxTracks(maxTracks).
		SetSyncToNavidrome(syncToNavidrome).
		SetDj(d).
		Save(r.Context())

	if err != nil {
		h.Logger.Error("failed to update mixtape", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("mixtape updated", "mixtape_id", updated.ID, "name", updated.Name, "user_id", u.ID)

	// Publish event
	if h.Bus != nil {
		h.Bus.PublishMixtapeUpdated(u.ID, updated.ID, updated.Name)
	}

	w.Header().Set("HX-Trigger", "mixtape-updated")
	w.WriteHeader(http.StatusOK)
}

// DeleteMixtape deletes a Mixtape
func (h *Handler) DeleteMixtape(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	mixtapeID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid Mixtape ID", http.StatusBadRequest)
		return
	}

	// Verify ownership
	m, err := h.Client.Mixtape.Query().
		Where(mixtape.ID(mixtapeID), mixtape.HasUserWith(user.ID(u.ID))).
		Only(r.Context())
	if err != nil {
		http.Error(w, "Mixtape not found", http.StatusNotFound)
		return
	}

	mixtapeName := m.Name // Save name before deletion

	err = h.Client.Mixtape.DeleteOneID(mixtapeID).Exec(r.Context())
	if err != nil {
		h.Logger.Error("failed to delete mixtape", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("mixtape deleted", "mixtape_id", mixtapeID, "name", mixtapeName, "user_id", u.ID)

	// Publish event
	if h.Bus != nil {
		h.Bus.PublishMixtapeDeleted(u.ID, mixtapeID, mixtapeName)
	}

	w.Header().Set("HX-Trigger", "mixtape-deleted")
	w.WriteHeader(http.StatusOK)
}

// ToggleMixtapeSync toggles the sync_to_navidrome flag for a Mixtape
func (h *Handler) ToggleMixtapeSync(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	mixtapeID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid Mixtape ID", http.StatusBadRequest)
		return
	}

	// Verify ownership
	m, err := h.Client.Mixtape.Query().
		Where(mixtape.ID(mixtapeID), mixtape.HasUserWith(user.ID(u.ID))).
		Only(r.Context())
	if err != nil {
		http.Error(w, "Mixtape not found", http.StatusNotFound)
		return
	}

	// Toggle the sync flag
	_, err = h.Client.Mixtape.UpdateOne(m).
		SetSyncToNavidrome(!m.SyncToNavidrome).
		Save(r.Context())
	if err != nil {
		h.Logger.Error("failed to toggle mixtape sync", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "mixtape-updated")
	w.WriteHeader(http.StatusOK)
}

// GenerateMixtape generates tracks for a mixtape using the AI-powered vibes engine
func (h *Handler) GenerateMixtape(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	mixtapeID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid Mixtape ID", http.StatusBadRequest)
		return
	}

	// Verify ownership and get mixtape with DJ
	m, err := h.Client.Mixtape.Query().
		Where(mixtape.ID(mixtapeID), mixtape.HasUserWith(user.ID(u.ID))).
		WithDj().
		Only(r.Context())
	if err != nil {
		http.Error(w, "Mixtape not found", http.StatusNotFound)
		return
	}

	h.Logger.Info("generating mixtape",
		"mixtape_id", m.ID,
		"name", m.Name,
		"dj", m.Edges.Dj.Name,
		"user_id", u.ID)

	// Check if the MixtapeGenerator is available
	if h.MixtapeGenerator == nil {
		h.Logger.Error("mixtape generator not initialized")
		http.Error(w, "Mixtape generation service is not available", http.StatusServiceUnavailable)
		return
	}

	// Parse optional seed data from the request
	var seed *vibes.Seed
	if err := r.ParseForm(); err == nil {
		seedType := r.FormValue("seed_type")
		seedID := r.FormValue("seed_id")

		switch seedType {
		case "artist":
			if artistID, err := strconv.Atoi(seedID); err == nil {
				a, err := h.Client.Artist.Get(r.Context(), artistID)
				if err == nil {
					seed = vibes.NewArtistSeed(a)
					h.Logger.Debug("using artist seed", "artist_id", artistID, "artist_name", a.Name)
				}
			}
		case "album":
			if albumID, err := strconv.Atoi(seedID); err == nil {
				album, err := h.Client.Album.Get(r.Context(), albumID)
				if err == nil {
					seed = vibes.NewAlbumSeed(album)
					h.Logger.Debug("using album seed", "album_id", albumID, "album_name", album.Name)
				}
			}
		case "tracks":
			if trackIDsStr := r.FormValue("track_ids"); trackIDsStr != "" {
				var trackIDs []int
				for _, idStr := range strings.Split(trackIDsStr, ",") {
					if id, err := strconv.Atoi(strings.TrimSpace(idStr)); err == nil {
						trackIDs = append(trackIDs, id)
					}
				}
				if len(trackIDs) > 0 {
					seed = vibes.NewTrackIDsSeed(trackIDs)
					h.Logger.Debug("using tracks seed", "track_ids", trackIDs)
				}
			}
		}
	}

	// Create the generation request
	req := &vibes.GenerationRequest{
		Mixtape: m,
		DJ:      m.Edges.Dj,
		Seed:    seed,
		UserID:  u.ID,
	}

	// Run generation (this can take a while, so we do it in a goroutine for async UX)
	go func() {
		// Governing: context.Background() prevents premature cancellation when HTTP handler returns
		ctx := context.Background()

		// Publish generating event
		if h.Bus != nil {
			h.Bus.PublishMixtapeGenerating(u.ID, m.ID, m.Name, m.Edges.Dj.Name)
		}

		result, err := h.MixtapeGenerator.GenerateMixtape(ctx, req)
		if err != nil {
			h.Logger.Error("mixtape generation failed",
				"mixtape_id", m.ID,
				"error", err)

			// Update mixtape with error
			if _, saveErr := h.Client.Mixtape.UpdateOneID(m.ID).
				SetGenerationError(err.Error()).
				Save(ctx); saveErr != nil {
				h.Logger.Error("failed to save mixtape error", "error", saveErr)
			}

			// Publish error event
			if h.Bus != nil {
				h.Bus.PublishMixtapeError(u.ID, m.ID, m.Name, err.Error())
			}
			return
		}

		// Get matched track IDs
		trackIDs := result.GetMatchedTrackIDsAsStrings()

		// Update the mixtape with the results
		updater := h.Client.Mixtape.UpdateOneID(m.ID).
			SetTrackIds(trackIDs).
			SetTrackCount(len(trackIDs)).
			SetLastGeneratedAt(time.Now()).
			SetGenerationPrompt(result.PromptUsed).
			SetGenerationModel(result.ModelUsed).
			ClearGenerationError()

		if result.TokensUsed > 0 {
			updater.SetGenerationTokensUsed(result.TokensUsed)
		}

		// Store seed information if provided
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

		_, err = updater.Save(ctx)
		if err != nil {
			h.Logger.Error("failed to save mixtape generation results",
				"mixtape_id", m.ID,
				"error", err)
			return
		}

		h.Logger.Info("mixtape generation complete",
			"mixtape_id", m.ID,
			"tracks_matched", result.MatchedCount,
			"tracks_unmatched", result.UnmatchedCount,
			"tokens_used", result.TokensUsed)

		// Publish success event
		if h.Bus != nil {
			h.Bus.PublishMixtapeGenerated(u.ID, m.ID, m.Name, m.Edges.Dj.Name,
				len(result.Tracks), result.MatchedCount, result.TokensUsed)
		}
	}()

	// Return immediately with a "generating" status
	w.Header().Set("HX-Trigger", "mixtape-generating")
	w.WriteHeader(http.StatusAccepted)
}

// MixtapeShow shows a single Mixtape
func (h *Handler) MixtapeShow(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	mixtapeID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid Mixtape ID", http.StatusBadRequest)
		return
	}

	// Get the mixtape with DJ
	m, err := h.Client.Mixtape.Query().
		Where(
			mixtape.ID(mixtapeID),
			mixtape.HasUserWith(user.ID(u.ID)),
		).
		WithDj().
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to get mixtape", "error", err, "id", mixtapeID)
		http.Error(w, "Mixtape not found", http.StatusNotFound)
		return
	}

	// Get all DJs for the edit modal
	djs, err := h.Client.DJ.Query().
		Where(dj.HasUserWith(user.ID(u.ID))).
		Order(ent.Asc(dj.FieldName)).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to query DJs", "error", err)
		djs = []*ent.DJ{}
	}

	// Load tracks if the mixtape has track IDs
	var tracks []*ent.Track
	if len(m.TrackIds) > 0 {
		// Parse track IDs from strings to ints
		trackIDs := make([]int, 0, len(m.TrackIds))
		for _, idStr := range m.TrackIds {
			if id, err := strconv.Atoi(idStr); err == nil {
				trackIDs = append(trackIDs, id)
			}
		}

		if len(trackIDs) > 0 {
			// Query tracks with artist and album
			tracks, err = h.Client.Track.Query().
				Where(track.IDIn(trackIDs...)).
				WithArtist().
				WithAlbum().
				All(r.Context())
			if err != nil {
				h.Logger.Error("failed to query mixtape tracks", "error", err, "mixtape_id", m.ID)
				tracks = []*ent.Track{}
			}

			// Reorder tracks to match the original track_ids order
			trackMap := make(map[int]*ent.Track)
			for _, t := range tracks {
				trackMap[t.ID] = t
			}
			orderedTracks := make([]*ent.Track, 0, len(trackIDs))
			for _, id := range trackIDs {
				if t, ok := trackMap[id]; ok {
					orderedTracks = append(orderedTracks, t)
				}
			}
			tracks = orderedTracks
		}
	}

	h.Logger.Debug("showing mixtape",
		"mixtape_id", m.ID,
		"name", m.Name,
		"track_count", len(tracks),
		"track_ids_count", len(m.TrackIds))

	h.Render(w, r, vibesViews.MixtapeShow(m, tracks, djs, h.Config))
}

// GenreSuggestions returns genre suggestions based on user's library
func (h *Handler) GenreSuggestions(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	query := strings.ToLower(r.URL.Query().Get("q"))

	// Get unique genres from user's listens
	listens, err := h.Client.Listen.Query().
		Where(listen.HasUserWith(user.ID(u.ID))).
		Limit(1000).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to query listens for genres", "error", err)
		w.Header().Set("Content-Type", "application/json")
		if encErr := json.NewEncoder(w).Encode([]string{}); encErr != nil {
			h.Logger.Error("failed to encode empty genres response", "error", encErr)
		}
		return
	}

	// Extract and count genres from album names and artist names
	genreCounts := make(map[string]int)
	for _, l := range listens {
		// This is a simplified approach - in production you'd have actual genre data
		// For now, we'll suggest based on artist/album patterns
		genreCounts[l.ArtistName]++
	}

	// Get actual genres from artists in the database
	artists, err := h.Client.Artist.Query().
		Where(artist.HasUserWith(user.ID(u.ID))).
		All(r.Context())
	if err == nil {
		for _, a := range artists {
			for _, g := range a.Genres {
				genreCounts[g]++
			}
		}
	}

	// Filter by query and sort by count
	var suggestions []string
	for genre := range genreCounts {
		if query == "" || strings.Contains(strings.ToLower(genre), query) {
			suggestions = append(suggestions, genre)
		}
	}

	// Sort alphabetically, limit to 20
	sort.Strings(suggestions)
	if len(suggestions) > 20 {
		suggestions = suggestions[:20]
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(suggestions); err != nil {
		h.Logger.Error("failed to encode genre suggestions", "error", err)
	}
}

// ArtistSuggestions returns artist suggestions based on user's library
func (h *Handler) ArtistSuggestions(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	query := strings.ToLower(r.URL.Query().Get("q"))

	// Get artists from user's catalog
	artists, err := h.Client.Artist.Query().
		Where(artist.HasUserWith(user.ID(u.ID))).
		Order(ent.Asc(artist.FieldName)).
		Limit(100).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to query artists", "error", err)
		w.Header().Set("Content-Type", "application/json")
		if encErr := json.NewEncoder(w).Encode([]string{}); encErr != nil {
			h.Logger.Error("failed to encode empty artists response", "error", encErr)
		}
		return
	}

	// Filter by query
	var suggestions []string
	for _, a := range artists {
		if query == "" || strings.Contains(strings.ToLower(a.Name), query) {
			suggestions = append(suggestions, a.Name)
		}
	}

	// Limit to 20
	if len(suggestions) > 20 {
		suggestions = suggestions[:20]
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(suggestions); err != nil {
		h.Logger.Error("failed to encode artist suggestions", "error", err)
	}
}

// parseCommaSeparated splits a comma-separated string into a slice of trimmed strings
func parseCommaSeparated(s string) []string {
	if s == "" {
		return nil
	}

	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}
