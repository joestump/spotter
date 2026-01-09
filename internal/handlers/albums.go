package handlers

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"spotter/ent"
	"spotter/ent/album"
	"spotter/ent/artist"
	"spotter/ent/dj"
	"spotter/ent/listen"
	"spotter/ent/track"
	"spotter/ent/user"
	"spotter/internal/enrichers"
	"spotter/internal/events"
	"spotter/internal/vibes"
	"spotter/internal/views/albums"
	"spotter/internal/views/components"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) AlbumShow(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	albumID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid album ID", http.StatusBadRequest)
		return
	}

	// Get the album with artist and images
	a, err := h.Client.Album.Query().
		Where(
			album.ID(albumID),
			album.HasUserWith(user.ID(u.ID)),
		).
		WithArtist(func(q *ent.ArtistQuery) {
			q.WithImages()
		}).
		WithImages().
		WithTracks().
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to get album", "error", err, "id", albumID)
		http.Error(w, "Album not found", http.StatusNotFound)
		return
	}

	// Get DJs for mixtape modal
	djs, err := h.Client.DJ.Query().
		Where(dj.HasUserWith(user.ID(u.ID))).
		Order(ent.Asc(dj.FieldName)).
		All(r.Context())
	if err != nil {
		h.Logger.Warn("failed to get DJs for mixtape modal", "error", err)
		djs = []*ent.DJ{}
	}

	// Get timeframe from query
	timeframe := r.URL.Query().Get("timeframe")
	if timeframe == "" {
		timeframe = "30d"
	}

	// Get tracks with listen counts
	tracks := h.getAlbumTracksWithListens(r.Context(), u.ID, a)

	// Get stats
	stats := h.getAlbumStats(r.Context(), u.ID, a, timeframe)

	h.Render(w, r, albums.Show(a, tracks, stats, djs, h.Config, timeframe))
}

func (h *Handler) AlbumChart(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	albumID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid album ID", http.StatusBadRequest)
		return
	}

	// Get album
	a, err := h.Client.Album.Query().
		Where(
			album.ID(albumID),
			album.HasUserWith(user.ID(u.ID)),
		).
		WithArtist().
		Only(r.Context())
	if err != nil {
		http.Error(w, "Album not found", http.StatusNotFound)
		return
	}

	timeframe := r.URL.Query().Get("timeframe")
	if timeframe == "" {
		timeframe = "30d"
	}

	artistName := ""
	if a.Edges.Artist != nil {
		artistName = a.Edges.Artist.Name
	}

	data := h.getProviderHistory(r.Context(), u.ID, artistName, a.Name, "", timeframe)
	h.Render(w, r, albums.ProviderHistoryChartContent(albumID, data))
}

func (h *Handler) getAlbumStats(ctx context.Context, userID int, a *ent.Album, timeframe string) *albums.AlbumStats {
	stats := &albums.AlbumStats{
		ListensByHour:      make([]components.ChartDataPoint, 24),
		ListensByDayOfWeek: make([]components.ChartDataPoint, 7),
		ListensByMonth:     make([]components.ChartDataPoint, 12),
		TrackListens:       []components.ChartDataPoint{},
	}

	// Initialize hour labels
	for i := 0; i < 24; i++ {
		stats.ListensByHour[i] = components.ChartDataPoint{Label: strconv.Itoa(i), Value: 0}
	}

	// Initialize day labels
	days := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	for i, day := range days {
		stats.ListensByDayOfWeek[i] = components.ChartDataPoint{Label: day, Value: 0}
	}

	// Initialize month labels
	months := []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	for i, month := range months {
		stats.ListensByMonth[i] = components.ChartDataPoint{Label: month, Value: 0}
	}

	// Build the query for this album's listens
	query := h.Client.Listen.Query().
		Where(
			listen.HasUserWith(user.ID(userID)),
			listen.AlbumName(a.Name),
		)

	// If we have an artist, also filter by artist name
	if a.Edges.Artist != nil {
		query = query.Where(listen.ArtistName(a.Edges.Artist.Name))
	}

	listens, err := query.Order(ent.Asc(listen.FieldPlayedAt)).All(ctx)
	if err != nil {
		h.Logger.Error("failed to get album listens", "error", err)
		return stats
	}

	stats.TotalListens = len(listens)

	if len(listens) > 0 {
		stats.FirstListen = listens[0].PlayedAt
		stats.LastListen = listens[len(listens)-1].PlayedAt
	}

	// Count unique tracks and provider stats
	trackSet := make(map[string]bool)
	trackCounts := make(map[string]int)
	providerCounts := make(map[string]int)

	for _, l := range listens {
		trackSet[l.TrackName] = true
		trackCounts[l.TrackName]++
		providerCounts[l.Source]++

		// Hour stats
		hour := l.PlayedAt.Local().Hour()
		stats.ListensByHour[hour].Value++

		// Day of week stats
		day := int(l.PlayedAt.Local().Weekday())
		stats.ListensByDayOfWeek[day].Value++

		// Month stats
		month := int(l.PlayedAt.Local().Month()) - 1
		stats.ListensByMonth[month].Value++
	}

	stats.UniqueTracks = len(trackSet)

	// Provider breakdown
	for provider, count := range providerCounts {
		stats.ListensByProvider = append(stats.ListensByProvider, components.ChartDataPoint{
			Label: provider,
			Value: float64(count),
		})
	}

	// Track listens chart data - sort by track number if available, then by count
	trackListens := make([]components.ChartDataPoint, 0, len(trackCounts))
	for trackName, count := range trackCounts {
		trackListens = append(trackListens, components.ChartDataPoint{
			Label: trackName,
			Value: float64(count),
		})
	}

	// Sort by listen count descending
	sort.Slice(trackListens, func(i, j int) bool {
		return trackListens[i].Value > trackListens[j].Value
	})

	stats.TrackListens = trackListens

	// Get provider history
	artistName := ""
	if a.Edges.Artist != nil {
		artistName = a.Edges.Artist.Name
	}
	stats.ProviderHistory = h.getProviderHistory(ctx, userID, artistName, a.Name, "", timeframe)

	return stats
}

func (h *Handler) getAlbumTracksWithListens(ctx context.Context, userID int, a *ent.Album) []albums.TrackWithListens {
	// Get tracks from the album
	tracks, err := h.Client.Track.Query().
		Where(track.HasAlbumWith(album.ID(a.ID))).
		Order(ent.Asc(track.FieldDiscNumber), ent.Asc(track.FieldTrackNumber)).
		All(ctx)
	if err != nil {
		h.Logger.Error("failed to get album tracks", "error", err)
		return nil
	}

	// Get listen counts for each track
	result := make([]albums.TrackWithListens, 0, len(tracks))

	// Build artist name filter
	artistName := ""
	if a.Edges.Artist != nil {
		artistName = a.Edges.Artist.Name
	}

	for _, t := range tracks {
		query := h.Client.Listen.Query().
			Where(
				listen.HasUserWith(user.ID(userID)),
				listen.TrackName(t.Name),
				listen.AlbumName(a.Name),
			)

		if artistName != "" {
			query = query.Where(listen.ArtistName(artistName))
		}

		count, _ := query.Count(ctx)

		// Set the Album edge on the track so that TrackTableRow.Album() can return it
		// for cover art rendering in the track table
		t.Edges.Album = a
		result = append(result, albums.TrackWithListens{
			Track:       t,
			ListenCount: count,
		})
	}

	// If no tracks found in catalog, try to get from listens
	if len(result) == 0 {
		type trackInfo struct {
			TrackName string `json:"track_name"`
		}

		var trackNames []trackInfo
		query := h.Client.Listen.Query().
			Where(
				listen.HasUserWith(user.ID(userID)),
				listen.AlbumName(a.Name),
			)

		if artistName != "" {
			query = query.Where(listen.ArtistName(artistName))
		}

		err = query.GroupBy(listen.FieldTrackName).Scan(ctx, &trackNames)
		if err == nil {
			for _, tn := range trackNames {
				countQuery := h.Client.Listen.Query().
					Where(
						listen.HasUserWith(user.ID(userID)),
						listen.TrackName(tn.TrackName),
						listen.AlbumName(a.Name),
					)
				if artistName != "" {
					countQuery = countQuery.Where(listen.ArtistName(artistName))
				}
				count, _ := countQuery.Count(ctx)

				result = append(result, albums.TrackWithListens{
					Track:       &ent.Track{Name: tn.TrackName},
					ListenCount: count,
				})
			}
		}
	}

	return result
}

// AlbumIndex shows all albums for the user
func (h *Handler) AlbumIndex(w http.ResponseWriter, r *http.Request) {
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

	// Get artist filter from query
	artistFilter := r.URL.Query().Get("artist")

	pageSize := u.PaginationSize
	offset := (page - 1) * pageSize

	// Build query with optional artist filter
	query := h.Client.Album.Query().
		Where(album.HasUserWith(user.ID(u.ID)))

	if artistFilter != "" {
		query = query.Where(album.HasArtistWith(artist.Name(artistFilter)))
	}

	// Query albums with pagination
	albumList, err := query.
		WithArtist().
		WithImages().
		Unique(true).
		Order(ent.Desc(album.FieldUpdatedAt)).
		Limit(pageSize).
		Offset(offset).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to query albums", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Build count query with same filter
	countQuery := h.Client.Album.Query().
		Where(album.HasUserWith(user.ID(u.ID)))

	if artistFilter != "" {
		countQuery = countQuery.Where(album.HasArtistWith(artist.Name(artistFilter)))
	}

	// Get total count for pagination
	total, err := countQuery.Count(r.Context())
	if err != nil {
		h.Logger.Error("failed to count albums", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	totalPages := (total + pageSize - 1) / pageSize

	h.Render(w, r, albums.Index(albumList, page, totalPages, h.Config, artistFilter))
}

// AlbumRegenerateAI regenerates AI content for a specific album
func (h *Handler) AlbumRegenerateAI(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	albumID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid album ID", http.StatusBadRequest)
		return
	}

	// Get the album with all edges needed for AI enrichment
	a, err := h.Client.Album.Query().
		Where(
			album.ID(albumID),
			album.HasUserWith(user.ID(u.ID)),
		).
		WithArtist().
		WithTracks().
		WithImages().
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to get album for AI regeneration", "error", err, "id", albumID)
		http.Error(w, "Album not found", http.StatusNotFound)
		return
	}

	// Get the OpenAI enricher
	enricherList, err := h.getAIEnricher(r.Context(), u)
	if err != nil || len(enricherList) == 0 {
		h.Logger.Error("AI enricher not available", "error", err)
		http.Error(w, "AI enrichment not available", http.StatusServiceUnavailable)
		return
	}

	// Run AI enrichment
	for _, e := range enricherList {
		albumEnricher, ok := e.(enrichers.AlbumEnricher)
		if !ok {
			continue
		}

		data, err := albumEnricher.EnrichAlbum(r.Context(), a)
		if err != nil {
			h.Logger.Error("AI enrichment failed", "error", err, "album", a.Name)
			http.Error(w, "AI enrichment failed", http.StatusInternalServerError)
			return
		}

		if data != nil {
			// Update the album with AI data
			update := h.Client.Album.UpdateOne(a)
			if data.AISummary != "" {
				update = update.SetAiSummary(data.AISummary)
			}
			if len(data.AITags) > 0 {
				update = update.SetAiTags(data.AITags)
			}
			update = update.SetLastAiEnrichedAt(time.Now())

			if _, err := update.Save(r.Context()); err != nil {
				h.Logger.Error("failed to save AI enrichment", "error", err, "album", a.Name)
				http.Error(w, "Failed to save AI enrichment", http.StatusInternalServerError)
				return
			}

			h.Logger.Info("regenerated AI content for album", "album", a.Name)
		}
	}

	// Send success notification
	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "AI Regenerated",
			Message:  "AI insights for " + a.Name + " have been regenerated.",
			IconType: "success",
		},
	})

	// Return HX-Refresh header to reload the page
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
}

// AlbumCreateMixtape handles the creation of a mixtape from an album.
func (h *Handler) AlbumCreateMixtape(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	albumID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid album ID", http.StatusBadRequest)
		return
	}

	// Verify album exists and belongs to user
	a, err := h.Client.Album.Query().
		Where(
			album.ID(albumID),
			album.HasUserWith(user.ID(u.ID)),
		).
		Only(r.Context())
	if err != nil {
		http.Error(w, "Album not found", http.StatusNotFound)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
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

	// Generate mixtape name
	mixtapeName := r.FormValue("name")
	if mixtapeName == "" {
		mixtapeName = a.Name + " Mix"
	}

	// Get max tracks (default 25)
	maxTracks := 25
	if maxTracksStr := r.FormValue("max_tracks"); maxTracksStr != "" {
		if mt, err := strconv.Atoi(maxTracksStr); err == nil && mt >= 1 && mt <= 100 {
			maxTracks = mt
		}
	}

	// Create the mixtape
	m, err := h.Client.Mixtape.Create().
		SetName(mixtapeName).
		SetDescription("Inspired by " + a.Name).
		SetMaxTracks(maxTracks).
		SetSeedType("album").
		SetSeedID(albumID).
		SetDj(d).
		SetUser(u).
		Save(r.Context())
	if err != nil {
		h.Logger.Error("failed to create mixtape", "error", err)
		http.Error(w, "Failed to create mixtape", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("created album-seeded mixtape",
		"mixtape_id", m.ID,
		"mixtape_name", m.Name,
		"album_id", albumID,
		"album_name", a.Name,
		"dj_id", d.ID,
		"dj_name", d.Name)

	// Generate the mixtape in background
	if h.MixtapeGenerator != nil {
		go func() {
			ctx := context.Background()

			// Publish generating event
			if h.Bus != nil {
				h.Bus.PublishMixtapeGenerating(u.ID, m.ID, m.Name, d.Name)
			}

			seed := vibes.NewAlbumSeed(a)
			req := &vibes.GenerationRequest{
				Mixtape: m,
				DJ:      d,
				Seed:    seed,
				UserID:  u.ID,
			}

			result, err := h.MixtapeGenerator.GenerateMixtape(ctx, req)
			if err != nil {
				h.Logger.Error("mixtape generation failed",
					"mixtape_id", m.ID,
					"error", err)

				h.Client.Mixtape.UpdateOneID(m.ID).
					SetGenerationError(err.Error()).
					Save(ctx)

				if h.Bus != nil {
					h.Bus.PublishMixtapeError(u.ID, m.ID, m.Name, err.Error())
				}
				return
			}

			// Get matched track IDs
			trackIDs := result.GetMatchedTrackIDsAsStrings()

			// Update the mixtape with results
			_, err = h.Client.Mixtape.UpdateOneID(m.ID).
				SetTrackIds(trackIDs).
				SetTrackCount(len(trackIDs)).
				SetLastGeneratedAt(time.Now()).
				SetGenerationPrompt(result.PromptUsed).
				SetGenerationModel(result.ModelUsed).
				SetNillableGenerationTokensUsed(&result.TokensUsed).
				ClearGenerationError().
				Save(ctx)
			if err != nil {
				h.Logger.Error("failed to save mixtape generation results",
					"mixtape_id", m.ID,
					"error", err)
				return
			}

			h.Logger.Info("mixtape generation complete",
				"mixtape_id", m.ID,
				"tracks_matched", result.MatchedCount)

			if h.Bus != nil {
				h.Bus.PublishMixtapeGenerated(u.ID, m.ID, m.Name, d.Name,
					len(result.Tracks), result.MatchedCount, result.TokensUsed)
			}
		}()
	}

	// Send success notification
	if h.Bus != nil {
		h.Bus.PublishNotification(u.ID,
			"Mixtape Created",
			"Creating mixtape \""+m.Name+"\" inspired by "+a.Name+"...",
			"success")
	}

	w.Header().Set("HX-Redirect", "/vibes/mixtapes")
	w.WriteHeader(http.StatusOK)
}

// AlbumMixtapeModal returns the content for the create mixtape modal.
func (h *Handler) AlbumMixtapeModal(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	albumID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid Album ID", http.StatusBadRequest)
		return
	}

	a, err := h.Client.Album.Query().
		Where(
			album.ID(albumID),
			album.HasUserWith(user.ID(u.ID)),
		).
		Only(r.Context())
	if err != nil {
		http.Error(w, "Album not found", http.StatusNotFound)
		return
	}

	// Get DJs
	djs, err := h.Client.DJ.Query().
		Where(dj.HasUserWith(user.ID(u.ID))).
		Order(ent.Asc(dj.FieldName)).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to get DJs", "error", err)
		djs = []*ent.DJ{}
	}

	h.Render(w, r, albums.CreateMixtapeModalContent(a, djs))
}
