package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"spotter/ent"
	"spotter/ent/artist"
	"spotter/ent/listen"
	"spotter/ent/playlist"
	"spotter/ent/playlisttrack"
	"spotter/ent/track"
	"spotter/ent/user"
	"spotter/internal/enrichers"
	"spotter/internal/events"
	"spotter/internal/views/components"
	"spotter/internal/views/tracks"

	"github.com/go-chi/chi/v5"
)

// trackSortFields maps URL sort params to ent field selectors
var trackSortFields = map[string]string{
	"track":      track.FieldName,
	"duration":   track.FieldDurationMs,
	"popularity": track.FieldPopularity,
}

func (h *Handler) TrackShow(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	trackID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid track ID", http.StatusBadRequest)
		return
	}

	// Get the track with artist and album
	t, err := h.Client.Track.Query().
		Where(track.ID(trackID)).
		WithArtist(func(q *ent.ArtistQuery) {
			q.WithImages()
			q.Where(artist.HasUserWith(user.ID(u.ID)))
		}).
		WithAlbum(func(q *ent.AlbumQuery) {
			q.WithImages()
		}).
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to get track", "error", err, "id", trackID)
		http.Error(w, "Track not found", http.StatusNotFound)
		return
	}

	// Verify track belongs to user's artist
	if t.Edges.Artist == nil {
		http.Error(w, "Track not found", http.StatusNotFound)
		return
	}

	// Get timeframe from query
	timeframe := r.URL.Query().Get("timeframe")
	if timeframe == "" {
		timeframe = "30d"
	}

	// Get playlists containing this track
	playlists := h.getPlaylistsWithTrack(r.Context(), u.ID, trackID)

	// Get stats
	stats := h.getTrackStats(r.Context(), u.ID, t, timeframe)

	h.Render(w, r, tracks.Show(t, playlists, stats, h.Config, timeframe))
}

func (h *Handler) TrackChart(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	trackID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid track ID", http.StatusBadRequest)
		return
	}

	// Get track with artist
	t, err := h.Client.Track.Query().
		Where(track.ID(trackID)).
		WithArtist(func(q *ent.ArtistQuery) {
			q.Where(artist.HasUserWith(user.ID(u.ID)))
		}).
		WithAlbum().
		Only(r.Context())
	if err != nil {
		http.Error(w, "Track not found", http.StatusNotFound)
		return
	}

	if t.Edges.Artist == nil {
		http.Error(w, "Track not found", http.StatusNotFound)
		return
	}

	timeframe := r.URL.Query().Get("timeframe")
	if timeframe == "" {
		timeframe = "30d"
	}

	artistName := t.Edges.Artist.Name
	albumName := ""
	if t.Edges.Album != nil {
		albumName = t.Edges.Album.Name
	}

	data := h.getProviderHistory(r.Context(), u.ID, artistName, albumName, t.Name, timeframe)
	h.Render(w, r, tracks.ProviderHistoryChartContent(trackID, data))
}

func (h *Handler) getPlaylistsWithTrack(ctx context.Context, userID int, trackID int) []*ent.Playlist {
	playlists, err := h.Client.Playlist.Query().
		Where(
			playlist.HasUserWith(user.ID(userID)),
			playlist.HasTracksWith(
				playlisttrack.HasTrackWith(track.ID(trackID)),
			),
		).
		All(ctx)
	if err != nil {
		h.Logger.Error("failed to get playlists for track", "error", err)
		return nil
	}
	return playlists
}

func (h *Handler) getTrackStats(ctx context.Context, userID int, t *ent.Track, timeframe string) *tracks.TrackStats {
	stats := &tracks.TrackStats{
		ListensByHour:      make([]components.ChartDataPoint, 24),
		ListensByDayOfWeek: make([]components.ChartDataPoint, 7),
		ListensByMonth:     make([]components.ChartDataPoint, 12),
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

	// Build query for this track's listens
	query := h.Client.Listen.Query().
		Where(
			listen.HasUserWith(user.ID(userID)),
			listen.TrackName(t.Name),
		)

	// Add artist filter if available
	if t.Edges.Artist != nil {
		query = query.Where(listen.ArtistName(t.Edges.Artist.Name))
	}

	// Add album filter if available
	if t.Edges.Album != nil {
		query = query.Where(listen.AlbumName(t.Edges.Album.Name))
	}

	listens, err := query.Order(ent.Asc(listen.FieldPlayedAt)).All(ctx)
	if err != nil {
		h.Logger.Error("failed to get track listens", "error", err)
		return stats
	}

	stats.TotalListens = len(listens)

	if len(listens) > 0 {
		stats.FirstListen = listens[0].PlayedAt
		stats.LastListen = listens[len(listens)-1].PlayedAt
	}

	// Count provider stats and time distributions
	providerCounts := make(map[string]int)

	for _, l := range listens {
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

	// Provider breakdown
	for provider, count := range providerCounts {
		stats.ListensByProvider = append(stats.ListensByProvider, components.ChartDataPoint{
			Label: provider,
			Value: float64(count),
		})
	}

	// Get provider history
	artistName := ""
	if t.Edges.Artist != nil {
		artistName = t.Edges.Artist.Name
	}
	albumName := ""
	if t.Edges.Album != nil {
		albumName = t.Edges.Album.Name
	}
	stats.ProviderHistory = h.getProviderHistory(ctx, userID, artistName, albumName, t.Name, timeframe)

	return stats
}

// TrackIndex shows all tracks for the user
func (h *Handler) TrackIndex(w http.ResponseWriter, r *http.Request) {
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
		if p, parseErr := strconv.Atoi(pageStr); parseErr == nil && p > 0 {
			page = p
		}
	}

	// Get artist filter from query
	artistFilter := r.URL.Query().Get("artist")

	// Get sort parameters
	sortCol := r.URL.Query().Get("sort")
	sortDir := r.URL.Query().Get("dir")
	if sortCol == "" {
		sortCol = "track"
	}
	if sortDir == "" {
		sortDir = "asc"
	}

	pageSize := u.PaginationSize
	offset := (page - 1) * pageSize

	// Build query with optional artist filter
	query := h.Client.Track.Query().
		Where(track.HasArtistWith(artist.HasUserWith(user.ID(u.ID))))

	if artistFilter != "" {
		query = query.Where(track.HasArtistWith(artist.Name(artistFilter)))
	}

	// Apply sorting
	if field, ok := trackSortFields[sortCol]; ok {
		if sortDir == "asc" {
			query = query.Order(ent.Asc(field))
		} else {
			query = query.Order(ent.Desc(field))
		}
	} else {
		query = query.Order(ent.Asc(track.FieldName))
	}

	// Query tracks with pagination
	trackList, err := query.
		WithArtist().
		WithAlbum(func(q *ent.AlbumQuery) {
			q.WithImages()
		}).
		Limit(pageSize).
		Offset(offset).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to query tracks", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Build count query with same filter
	countQuery := h.Client.Track.Query().
		Where(track.HasArtistWith(artist.HasUserWith(user.ID(u.ID))))

	if artistFilter != "" {
		countQuery = countQuery.Where(track.HasArtistWith(artist.Name(artistFilter)))
	}

	// Get total count for pagination
	total, err := countQuery.Count(r.Context())
	if err != nil {
		h.Logger.Error("failed to count tracks", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	totalPages := (total + pageSize - 1) / pageSize

	h.Render(w, r, tracks.Index(trackList, page, totalPages, h.Config, artistFilter, sortCol, sortDir))
}

// TrackRegenerateAI regenerates AI content for a specific track
func (h *Handler) TrackRegenerateAI(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	trackID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid track ID", http.StatusBadRequest)
		return
	}

	// Get the track with all edges needed for AI enrichment
	t, err := h.Client.Track.Query().
		Where(track.ID(trackID)).
		WithArtist(func(q *ent.ArtistQuery) {
			q.Where(artist.HasUserWith(user.ID(u.ID)))
		}).
		WithAlbum().
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to get track for AI regeneration", "error", err, "id", trackID)
		http.Error(w, "Track not found", http.StatusNotFound)
		return
	}

	// Verify track belongs to user's artist
	if t.Edges.Artist == nil {
		http.Error(w, "Track not found", http.StatusNotFound)
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
		trackEnricher, ok := e.(enrichers.TrackEnricher)
		if !ok {
			continue
		}

		data, err := trackEnricher.EnrichTrack(r.Context(), t)
		if err != nil {
			h.Logger.Error("AI enrichment failed", "error", err, "track", t.Name)
			http.Error(w, "AI enrichment failed", http.StatusInternalServerError)
			return
		}

		if data != nil {
			// Update the track with AI data
			update := h.Client.Track.UpdateOne(t)
			if data.AISummary != "" {
				update = update.SetAiSummary(data.AISummary)
			}
			if len(data.AITags) > 0 {
				update = update.SetAiTags(data.AITags)
			}
			update = update.SetLastAiEnrichedAt(time.Now())

			if _, err := update.Save(r.Context()); err != nil {
				h.Logger.Error("failed to save AI enrichment", "error", err, "track", t.Name)
				http.Error(w, "Failed to save AI enrichment", http.StatusInternalServerError)
				return
			}

			h.Logger.Info("regenerated AI content for track", "track", t.Name)
		}
	}

	// Send success notification
	h.Bus.Publish(u.ID, events.Event{
		Type: events.EventTypeNotification,
		Payload: events.NotificationPayload{
			Title:    "AI Regenerated",
			Message:  "AI insights for " + t.Name + " have been regenerated.",
			IconType: "success",
		},
	})

	// Return HX-Refresh header to reload the page
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
}
