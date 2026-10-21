package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"spotter/ent"
	"spotter/ent/album"
	"spotter/ent/artist"
	"spotter/ent/dj"
	"spotter/ent/listen"
	"spotter/ent/mixtape"
	"spotter/ent/track"
	"spotter/ent/user"
	"spotter/internal/vibes"
	vibesViews "spotter/internal/views/vibes"
)

// djFilterFields holds the parsed tag/filter fields from a DJ create/update form.
type djFilterFields struct {
	GenresInclude  []string
	GenresExclude  []string
	Vibes          []string
	ArtistsInclude []string
	ArtistsExclude []string
}

// parseDJFilterFields extracts the comma-separated DJ filter fields from a request form.
func parseDJFilterFields(r *http.Request) djFilterFields {
	return djFilterFields{
		GenresInclude:  parseCommaSeparated(r.FormValue("genres_include")),
		GenresExclude:  parseCommaSeparated(r.FormValue("genres_exclude")),
		Vibes:          parseCommaSeparated(r.FormValue("vibes")),
		ArtistsInclude: parseCommaSeparated(r.FormValue("artists_include")),
		ArtistsExclude: parseCommaSeparated(r.FormValue("artists_exclude")),
	}
}

// VibesRedirect redirects to the DJs page
func (h *Handler) VibesRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/vibes/djs", http.StatusSeeOther)
}

// DJsIndex shows the list of DJs
func (h *Handler) DJsIndex(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUserRedirect(w, r)
	if u == nil {
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
	u := h.RequireUserRedirect(w, r)
	if u == nil {
		return
	}

	djID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
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
	u := h.RequireUser(w, r)
	if u == nil {
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

	f := parseDJFilterFields(r)

	_, err := h.Client.DJ.Create().
		SetName(name).
		SetSystemPrompt(systemPrompt).
		SetGenresInclude(f.GenresInclude).
		SetGenresExclude(f.GenresExclude).
		SetVibes(f.Vibes).
		SetArtistsInclude(f.ArtistsInclude).
		SetArtistsExclude(f.ArtistsExclude).
		SetUser(u).
		Save(r.Context())

	if err != nil {
		h.Logger.Error("failed to create DJ", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	h.HTMXEvent(w, "dj-created", http.StatusOK)
}

// UpdateDJ updates an existing DJ
func (h *Handler) UpdateDJ(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	djID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	// Verify ownership
	d, err := h.GetDJForUser(r.Context(), djID, u.ID)
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

	f := parseDJFilterFields(r)

	_, err = h.Client.DJ.UpdateOne(d).
		SetName(name).
		SetSystemPrompt(systemPrompt).
		SetGenresInclude(f.GenresInclude).
		SetGenresExclude(f.GenresExclude).
		SetVibes(f.Vibes).
		SetArtistsInclude(f.ArtistsInclude).
		SetArtistsExclude(f.ArtistsExclude).
		Save(r.Context())

	if err != nil {
		h.Logger.Error("failed to update DJ", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	h.HTMXEvent(w, "dj-updated", http.StatusOK)
}

// DeleteDJ deletes a DJ
func (h *Handler) DeleteDJ(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	djID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	// Verify ownership
	_, err := h.GetDJForUser(r.Context(), djID, u.ID)
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

	h.HTMXEvent(w, "dj-deleted", http.StatusOK)
}

// MixtapesIndex shows the list of Mixtapes
func (h *Handler) MixtapesIndex(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUserRedirect(w, r)
	if u == nil {
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

	pg := h.GetPaginationParams(r, u.PaginationSize)

	// Query mixtapes with pagination
	mixtapes, err := h.Client.Mixtape.Query().
		Where(mixtape.HasUserWith(user.ID(u.ID))).
		WithDj().
		Order(ent.Desc(mixtape.FieldUpdatedAt)).
		Limit(pg.PageSize).
		Offset(pg.Offset).
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

	pg.WithTotal(total)

	// Get all DJs for the create modal
	djs, err := h.Client.DJ.Query().
		Where(dj.HasUserWith(user.ID(u.ID))).
		Order(ent.Asc(dj.FieldName)).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to query DJs", "error", err)
		djs = []*ent.DJ{}
	}

	h.Render(w, r, vibesViews.MixtapesIndex(mixtapes, djs, pg.Page, pg.TotalPages, h.Config))
}

// CreateMixtape creates a new Mixtape
func (h *Handler) CreateMixtape(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
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
	d, err := h.GetDJForUser(r.Context(), djID, u.ID)
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

	h.HTMXEvent(w, "mixtape-created", http.StatusOK)
}

// UpdateMixtape updates an existing Mixtape
func (h *Handler) UpdateMixtape(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	mixtapeID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	// Verify ownership
	m, err := h.GetMixtapeForUser(r.Context(), mixtapeID, u.ID)
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
	d, err := h.GetDJForUser(r.Context(), djID, u.ID)
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

	h.HTMXEvent(w, "mixtape-updated", http.StatusOK)
}

// DeleteMixtape deletes a Mixtape
func (h *Handler) DeleteMixtape(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	mixtapeID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	// Verify ownership
	m, err := h.GetMixtapeForUser(r.Context(), mixtapeID, u.ID)
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

	h.HTMXEvent(w, "mixtape-deleted", http.StatusOK)
}

// ToggleMixtapeSync toggles the sync_to_navidrome flag for a Mixtape
func (h *Handler) ToggleMixtapeSync(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	mixtapeID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
		return
	}

	// Verify ownership
	m, err := h.GetMixtapeForUser(r.Context(), mixtapeID, u.ID)
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

	h.HTMXEvent(w, "mixtape-updated", http.StatusOK)
}

// maxSeedTrackIDs bounds how many request-supplied track IDs one generation may be seeded with.
const maxSeedTrackIDs = 100

// mixtapeSeed resolves a generation request's seed parameters against the
// requesting user's library, so another user's artist, album or track metadata
// can never be embedded in a stored generation prompt (ADR-0022). IDs the user
// does not own are dropped instead of resolved.
func (h *Handler) mixtapeSeed(ctx context.Context, userID int, form url.Values) *vibes.Seed {
	switch form.Get("seed_type") {
	case "artist":
		artistID, err := strconv.Atoi(form.Get("seed_id"))
		if err != nil || artistID <= 0 {
			return nil
		}
		a, err := h.Client.Artist.Query().
			Where(artist.ID(artistID), artist.HasUserWith(user.ID(userID))).
			Only(ctx)
		if err != nil {
			h.Logger.Debug("artist seed is not in this user's library", "artist_id", artistID, "user_id", userID)
			return nil
		}
		return vibes.NewArtistSeed(a)
	case "album":
		albumID, err := strconv.Atoi(form.Get("seed_id"))
		if err != nil || albumID <= 0 {
			return nil
		}
		al, err := h.Client.Album.Query().
			Where(album.ID(albumID), album.HasUserWith(user.ID(userID))).
			Only(ctx)
		if err != nil {
			h.Logger.Debug("album seed is not in this user's library", "album_id", albumID, "user_id", userID)
			return nil
		}
		return vibes.NewAlbumSeed(al)
	case "tracks":
		tracks := h.tracksOwnedBy(ctx, userID, parseCommaSeparatedIDs(form.Get("track_ids")))
		if len(tracks) == 0 {
			return nil
		}
		return vibes.NewTracksSeed(tracks)
	}
	return nil
}

// tracksOwnedBy returns the named tracks that belong to userID, dropping any ID
// that names another user's track (ADR-0022).
func (h *Handler) tracksOwnedBy(ctx context.Context, userID int, trackIDs []int) []*ent.Track {
	if len(trackIDs) == 0 {
		return nil
	}
	tracks, err := h.Client.Track.Query().
		Where(
			track.IDIn(trackIDs...),
			track.HasArtistWith(artist.HasUserWith(user.ID(userID))),
		).
		WithArtist().
		WithAlbum().
		All(ctx)
	if err != nil {
		h.Logger.Error("failed to load tracks for user", "error", err, "user_id", userID)
		return nil
	}
	return tracks
}

// parseCommaSeparatedIDs parses a comma-separated list of positive IDs and
// caps the list so a request cannot widen a query without bound.
func parseCommaSeparatedIDs(s string) []int {
	var ids []int
	for _, part := range strings.Split(s, ",") {
		if id, err := strconv.Atoi(strings.TrimSpace(part)); err == nil && id > 0 {
			ids = append(ids, id)
		}
		if len(ids) >= maxSeedTrackIDs {
			break
		}
	}
	return ids
}

// GenerateMixtape generates tracks for a mixtape using the AI-powered vibes engine
func (h *Handler) GenerateMixtape(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	mixtapeID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
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
		seed = h.mixtapeSeed(r.Context(), u.ID, r.Form)
	}

	// Create the generation request
	req := &vibes.GenerationRequest{
		Mixtape: m,
		DJ:      m.Edges.Dj,
		Seed:    seed,
		UserID:  u.ID,
	}

	// Governing: SPEC graceful-shutdown, SPEC vibes-ai-mixtape-engine REQ "background goroutines use context.Background()"
	// Capture value types to avoid stale *ent.User pointer in goroutine
	userID := u.ID
	mixtapeName := m.Name
	djName := m.Edges.Dj.Name

	// Run generation (this can take a while, so we do it in a goroutine for async UX)
	go func() {
		ctx := context.Background()

		// Publish generating event
		if h.Bus != nil {
			h.Bus.PublishMixtapeGenerating(userID, mixtapeID, mixtapeName, djName)
		}

		result, err := h.MixtapeGenerator.GenerateMixtape(ctx, req)
		if err != nil {
			h.Logger.Error("mixtape generation failed",
				"mixtape_id", mixtapeID,
				"error", err)

			// Update mixtape with error
			if _, saveErr := h.Client.Mixtape.UpdateOneID(mixtapeID).
				SetGenerationError(err.Error()).
				Save(ctx); saveErr != nil {
				h.Logger.Error("failed to save mixtape error", "error", saveErr)
			}

			// Publish error event
			if h.Bus != nil {
				h.Bus.PublishMixtapeError(userID, mixtapeID, mixtapeName, err.Error())
			}
			return
		}

		// Get matched track IDs
		trackIDs := result.GetMatchedTrackIDsAsStrings()

		// Update the mixtape with the results
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
				"mixtape_id", mixtapeID,
				"error", err)
			return
		}

		h.Logger.Info("mixtape generation complete",
			"mixtape_id", mixtapeID,
			"tracks_matched", result.MatchedCount,
			"tracks_unmatched", result.UnmatchedCount,
			"tokens_used", result.TokensUsed)

		// Publish success event
		if h.Bus != nil {
			h.Bus.PublishMixtapeGenerated(userID, mixtapeID, mixtapeName, djName,
				len(result.Tracks), result.MatchedCount, result.TokensUsed)
		}
	}()

	// Return immediately with a "generating" status
	h.HTMXEvent(w, "mixtape-generating", http.StatusAccepted)
}

// MixtapeShow shows a single Mixtape
func (h *Handler) MixtapeShow(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUserRedirect(w, r)
	if u == nil {
		return
	}

	mixtapeID, ok := h.ParseIntParam(w, r, "id")
	if !ok {
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
			// Resolve the stored IDs against the viewer's library, so an ID that
			// names another user's track renders nothing rather than another
			// user's row and art (ADR-0022)
			tracks = h.tracksOwnedBy(r.Context(), u.ID, trackIDs)
			if tracks == nil {
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
	u := h.RequireUser(w, r)
	if u == nil {
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
	u := h.RequireUser(w, r)
	if u == nil {
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
