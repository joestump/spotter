package handlers

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"spotter/ent"
	"spotter/ent/album"
	"spotter/ent/artist"
	"spotter/ent/dj"
	"spotter/ent/listen"
	"spotter/ent/playlist"
	"spotter/ent/playlisttrack"
	"spotter/ent/track"
	"spotter/ent/user"
	"spotter/internal/enrichers"
	"spotter/internal/events"
	"spotter/internal/vibes"
	"spotter/internal/views/artists"
	"spotter/internal/views/components"

	"github.com/go-chi/chi/v5"
)

const (
	groupByDay   = "day"
	groupByWeek  = "week"
	groupByMonth = "month"
	timeframe90d = "90d"
	timeframe6m  = "6m"
	timeframe1y  = "1y"
	timeframeAll = "all"
)

func (h *Handler) ArtistShow(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	artistID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid artist ID", http.StatusBadRequest)
		return
	}

	// Get the artist with images
	a, err := h.Client.Artist.Query().
		Where(
			artist.ID(artistID),
			artist.HasUserWith(user.ID(u.ID)),
		).
		WithImages().
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to get artist", "error", err, "id", artistID)
		http.Error(w, "Artist not found", http.StatusNotFound)
		return
	}

	// Get timeframe from query
	timeframe := r.URL.Query().Get("timeframe")
	if timeframe == "" {
		timeframe = timeframe30d
	}

	// Get similar artists
	var similarArtists []artists.SimilarArtistInfo
	if h.SimilarArtistsSvc != nil {
		similar, err := h.SimilarArtistsSvc.GetSimilarArtists(r.Context(), u.ID, artistID)
		if err != nil {
			h.Logger.Warn("failed to get similar artists", "error", err, "artist_id", artistID)
		} else {
			for _, s := range similar {
				if s.Edges.SimilarArtist != nil {
					similarArtists = append(similarArtists, artists.SimilarArtistInfo{
						Artist:     s.Edges.SimilarArtist,
						Provider:   s.Provider,
						Confidence: s.Confidence,
						Reason:     s.Reason,
					})
				}
			}
		}
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

	// Get albums for this artist (with tracks for tag collection)
	albums, err := h.Client.Album.Query().
		Where(
			album.HasArtistWith(artist.ID(artistID)),
			album.HasUserWith(user.ID(u.ID)),
		).
		WithImages().
		WithTracks().
		Unique(true).
		Order(ent.Desc(album.FieldYear)).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to get artist albums", "error", err)
		albums = []*ent.Album{}
	}

	// Get playlists containing this artist
	playlists := h.getPlaylistsWithArtist(r.Context(), u.ID, a.ID)

	// Get stats
	stats := h.getArtistStats(r.Context(), u.ID, a.Name, timeframe)

	h.Render(w, r, artists.Show(a, albums, playlists, stats, similarArtists, djs, h.Config, timeframe))
}

func (h *Handler) ArtistChart(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	artistID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid artist ID", http.StatusBadRequest)
		return
	}

	// Get artist name
	a, err := h.Client.Artist.Query().
		Where(
			artist.ID(artistID),
			artist.HasUserWith(user.ID(u.ID)),
		).
		Only(r.Context())
	if err != nil {
		http.Error(w, "Artist not found", http.StatusNotFound)
		return
	}

	timeframe := r.URL.Query().Get("timeframe")
	if timeframe == "" {
		timeframe = timeframe30d
	}

	data := h.getProviderHistory(r.Context(), u.ID, a.Name, "", "", timeframe)
	h.Render(w, r, artists.ProviderHistoryChartContent(artistID, data))
}

func (h *Handler) getArtistStats(ctx context.Context, userID int, artistName string, timeframe string) *artists.ArtistStats {
	stats := &artists.ArtistStats{
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

	// Get all listens for this artist
	listens, err := h.Client.Listen.Query().
		Where(
			listen.HasUserWith(user.ID(userID)),
			listen.ArtistName(artistName),
		).
		Order(ent.Asc(listen.FieldPlayedAt)).
		All(ctx)
	if err != nil {
		h.Logger.Error("failed to get artist listens", "error", err)
		return stats
	}

	stats.TotalListens = len(listens)

	if len(listens) > 0 {
		stats.FirstListen = listens[0].PlayedAt
		stats.LastListen = listens[len(listens)-1].PlayedAt
	}

	// Count unique albums and tracks
	albumSet := make(map[string]bool)
	trackSet := make(map[string]bool)
	providerCounts := make(map[string]int)

	for _, l := range listens {
		albumSet[l.AlbumName] = true
		trackSet[l.TrackName] = true
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

	stats.UniqueAlbums = len(albumSet)
	stats.UniqueTracks = len(trackSet)

	// Provider breakdown
	for provider, count := range providerCounts {
		stats.ListensByProvider = append(stats.ListensByProvider, components.ChartDataPoint{
			Label: provider,
			Value: float64(count),
		})
	}

	// Get provider history chart data
	stats.ProviderHistory = h.getProviderHistory(ctx, userID, artistName, "", "", timeframe)

	// Get top tracks
	stats.TopTracks = h.getTopTracksForArtist(ctx, userID, artistName, 10)

	return stats
}

func (h *Handler) getTopTracksForArtist(ctx context.Context, userID int, artistName string, limit int) []artists.TrackListenCount {
	// Get listen counts grouped by track name
	type trackCount struct {
		TrackName string `json:"track_name"`
		Count     int    `json:"count"`
	}

	var results []trackCount
	err := h.Client.Listen.Query().
		Where(
			listen.HasUserWith(user.ID(userID)),
			listen.ArtistName(artistName),
		).
		GroupBy(listen.FieldTrackName).
		Aggregate(ent.Count()).
		Scan(ctx, &results)
	if err != nil {
		h.Logger.Error("failed to get top tracks", "error", err)
		return nil
	}

	// Sort by count descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Count > results[j].Count
	})

	// Limit results
	if len(results) > limit {
		results = results[:limit]
	}

	// Get actual track entities
	topTracks := make([]artists.TrackListenCount, 0, len(results))
	for _, r := range results {
		t, err := h.Client.Track.Query().
			Where(
				track.Name(r.TrackName),
				track.HasArtistWith(artist.Name(artistName)),
			).
			WithAlbum(func(q *ent.AlbumQuery) {
				q.WithImages()
			}).
			First(ctx)
		if err != nil {
			// Track might not exist in catalog, create a minimal entry
			topTracks = append(topTracks, artists.TrackListenCount{
				Track:       &ent.Track{Name: r.TrackName},
				ListenCount: r.Count,
			})
		} else {
			topTracks = append(topTracks, artists.TrackListenCount{
				Track:       t,
				ListenCount: r.Count,
			})
		}
	}

	return topTracks
}

func (h *Handler) getPlaylistsWithArtist(ctx context.Context, userID int, artistID int) []artists.PlaylistWithArtist {
	// Find playlists that contain tracks by this artist
	playlists, err := h.Client.Playlist.Query().
		Where(
			playlist.HasUserWith(user.ID(userID)),
			playlist.HasTracksWith(
				playlisttrack.HasTrackWith(
					track.HasArtistWith(artist.ID(artistID)),
				),
			),
		).
		WithTracks(func(q *ent.PlaylistTrackQuery) {
			q.Where(
				playlisttrack.HasTrackWith(
					track.HasArtistWith(artist.ID(artistID)),
				),
			)
		}).
		All(ctx)
	if err != nil {
		h.Logger.Error("failed to get playlists", "error", err)
		return nil
	}

	result := make([]artists.PlaylistWithArtist, 0, len(playlists))
	for _, pl := range playlists {
		result = append(result, artists.PlaylistWithArtist{
			Playlist:   pl,
			TrackCount: len(pl.Edges.Tracks),
		})
	}

	return result
}

func (h *Handler) getProviderHistory(ctx context.Context, userID int, artistName, albumName, trackName string, timeframe string) components.StackedChartData {
	data := components.StackedChartData{
		Labels:   []string{},
		Datasets: []components.ChartDataset{},
	}

	// Calculate date range
	now := time.Now()
	var startDate time.Time
	var groupBy string

	switch timeframe {
	case timeframe30d:
		startDate = now.AddDate(0, 0, -30)
		groupBy = groupByDay
	case timeframe90d:
		startDate = now.AddDate(0, 0, -90)
		groupBy = groupByDay
	case timeframe6m:
		startDate = now.AddDate(0, -6, 0)
		groupBy = groupByWeek
	case timeframe1y:
		startDate = now.AddDate(-1, 0, 0)
		groupBy = groupByMonth
	case timeframeAll:
		startDate = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		groupBy = groupByMonth
	default:
		startDate = now.AddDate(0, 0, -30)
		groupBy = groupByDay
	}

	// Build query
	query := h.Client.Listen.Query().
		Where(
			listen.HasUserWith(user.ID(userID)),
			listen.PlayedAtGTE(startDate),
		)

	if artistName != "" {
		query = query.Where(listen.ArtistName(artistName))
	}
	if albumName != "" {
		query = query.Where(listen.AlbumName(albumName))
	}
	if trackName != "" {
		query = query.Where(listen.TrackName(trackName))
	}

	listens, err := query.All(ctx)
	if err != nil {
		h.Logger.Error("failed to get listens for history", "error", err)
		return data
	}

	// Group listens by date and provider
	providerData := make(map[string]map[string]int) // date -> provider -> count
	providers := make(map[string]bool)

	for _, l := range listens {
		var dateKey string
		switch groupBy {
		case groupByDay:
			dateKey = l.PlayedAt.Local().Format("Jan 2")
		case groupByWeek:
			year, week := l.PlayedAt.Local().ISOWeek()
			dateKey = time.Date(year, 1, 1, 0, 0, 0, 0, time.Local).AddDate(0, 0, (week-1)*7).Format("Jan 2")
		case groupByMonth:
			dateKey = l.PlayedAt.Local().Format("Jan 2006")
		}

		if providerData[dateKey] == nil {
			providerData[dateKey] = make(map[string]int)
		}
		providerData[dateKey][l.Source]++
		providers[l.Source] = true
	}

	// Generate labels (dates)
	labels := make([]string, 0)
	current := startDate
	for current.Before(now) || current.Equal(now) {
		var dateKey string
		var nextDate time.Time
		switch groupBy {
		case groupByDay:
			dateKey = current.Format("Jan 2")
			nextDate = current.AddDate(0, 0, 1)
		case groupByWeek:
			dateKey = current.Format("Jan 2")
			nextDate = current.AddDate(0, 0, 7)
		case groupByMonth:
			dateKey = current.Format("Jan 2006")
			nextDate = current.AddDate(0, 1, 0)
		}
		labels = append(labels, dateKey)
		current = nextDate
	}

	data.Labels = labels

	// Create datasets for each provider
	providerColors := map[string]string{
		"spotify":   "#1DB954",
		"navidrome": "#4285F4",
		"lastfm":    "#D51007",
	}

	for provider := range providers {
		dataset := components.ChartDataset{
			Label:           capitalizeFirst(provider),
			Data:            make([]float64, len(labels)),
			BackgroundColor: providerColors[provider],
			BorderColor:     providerColors[provider],
		}
		if dataset.BackgroundColor == "" {
			dataset.BackgroundColor = "#6B7280"
			dataset.BorderColor = "#6B7280"
		}

		for i, label := range labels {
			if providerData[label] != nil {
				dataset.Data[i] = float64(providerData[label][provider])
			}
		}

		data.Datasets = append(data.Datasets, dataset)
	}

	return data
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	if s[0] >= 'a' && s[0] <= 'z' {
		return string(s[0]-32) + s[1:]
	}
	return s
}

// ArtistIndex shows all artists for the user
func (h *Handler) ArtistIndex(w http.ResponseWriter, r *http.Request) {
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

	// Query artists with pagination
	artistList, err := h.Client.Artist.Query().
		Where(artist.HasUserWith(user.ID(u.ID))).
		WithImages().
		Order(ent.Asc(artist.FieldName)).
		Limit(pageSize).
		Offset(offset).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to query artists", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Get total count for pagination
	total, err := h.Client.Artist.Query().
		Where(artist.HasUserWith(user.ID(u.ID))).
		Count(r.Context())
	if err != nil {
		h.Logger.Error("failed to count artists", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	totalPages := (total + pageSize - 1) / pageSize

	h.Render(w, r, artists.Index(artistList, page, totalPages, h.Config))
}

// ArtistRegenerateAI regenerates AI content for a specific artist
func (h *Handler) ArtistRegenerateAI(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	artistID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid artist ID", http.StatusBadRequest)
		return
	}

	// Get the artist with all edges needed for AI enrichment
	a, err := h.Client.Artist.Query().
		Where(
			artist.ID(artistID),
			artist.HasUserWith(user.ID(u.ID)),
		).
		WithAlbums().
		WithTracks(func(q *ent.TrackQuery) {
			q.WithAlbum()
		}).
		WithImages().
		Only(r.Context())
	if err != nil {
		h.Logger.Error("failed to get artist for AI regeneration", "error", err, "id", artistID)
		http.Error(w, "Artist not found", http.StatusNotFound)
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
		artistEnricher, ok := e.(enrichers.ArtistEnricher)
		if !ok {
			continue
		}

		data, err := artistEnricher.EnrichArtist(r.Context(), a)
		if err != nil {
			h.Logger.Error("AI enrichment failed", "error", err, "artist", a.Name)
			http.Error(w, "AI enrichment failed", http.StatusInternalServerError)
			return
		}

		if data != nil {
			// Update the artist with AI data
			update := h.Client.Artist.UpdateOne(a)
			if data.AISummary != "" {
				update = update.SetAiSummary(data.AISummary)
			}
			if data.AIBiography != "" {
				update = update.SetAiBiography(data.AIBiography)
			}
			if len(data.AITags) > 0 {
				update = update.SetAiTags(data.AITags)
			}
			update = update.SetLastAiEnrichedAt(time.Now())

			if _, err := update.Save(r.Context()); err != nil {
				h.Logger.Error("failed to save AI enrichment", "error", err, "artist", a.Name)
				http.Error(w, "Failed to save AI enrichment", http.StatusInternalServerError)
				return
			}

			h.Logger.Info("regenerated AI content for artist", "artist", a.Name)
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

// getAIEnricher returns the OpenAI enricher if available
func (h *Handler) getAIEnricher(ctx context.Context, u *ent.User) ([]enrichers.Enricher, error) {
	factory, ok := h.MetadataSvc.Registry.Get(enrichers.TypeOpenAI)
	if !ok {
		return nil, nil
	}

	enricher, err := factory(ctx, u)
	if err != nil {
		return nil, err
	}
	if enricher == nil || !enricher.IsAvailable() {
		return nil, nil
	}

	return []enrichers.Enricher{enricher}, nil
}

// ArtistFindSimilar finds similar artists for the given artist using AI.
func (h *Handler) ArtistFindSimilar(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	artistID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid artist ID", http.StatusBadRequest)
		return
	}

	// Verify artist exists and belongs to user
	a, err := h.Client.Artist.Query().
		Where(
			artist.ID(artistID),
			artist.HasUserWith(user.ID(u.ID)),
		).
		Only(r.Context())
	if err != nil {
		http.Error(w, "Artist not found", http.StatusNotFound)
		return
	}

	if h.SimilarArtistsSvc == nil {
		http.Error(w, "Similar artists service not available", http.StatusServiceUnavailable)
		return
	}

	// Run in background
	go func() {
		ctx := context.Background()
		if err := h.SimilarArtistsSvc.FindSimilarArtists(ctx, u.ID, artistID); err != nil {
			h.Logger.Error("failed to find similar artists",
				"artist_id", artistID,
				"artist_name", a.Name,
				"error", err)
			if h.Bus != nil {
				h.Bus.PublishNotification(u.ID,
					"Similar Artists Failed",
					"Failed to find similar artists for "+a.Name+": "+err.Error(),
					"error")
			}
		}
	}()

	// Send notification that search started
	if h.Bus != nil {
		h.Bus.PublishNotification(u.ID,
			"Finding Similar Artists",
			"Searching for artists similar to "+a.Name+"...",
			"info")
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusAccepted)
}

// ArtistCreateMixtape creates a mixtape seeded with the given artist.
func (h *Handler) ArtistCreateMixtape(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	artistID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid artist ID", http.StatusBadRequest)
		return
	}

	// Verify artist exists and belongs to user
	a, err := h.Client.Artist.Query().
		Where(
			artist.ID(artistID),
			artist.HasUserWith(user.ID(u.ID)),
		).
		Only(r.Context())
	if err != nil {
		http.Error(w, "Artist not found", http.StatusNotFound)
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
	mixtapeName := strings.TrimSpace(r.FormValue("name"))
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
		SetSeedType("artist").
		SetSeedID(artistID).
		SetDj(d).
		SetUser(u).
		Save(r.Context())
	if err != nil {
		h.Logger.Error("failed to create mixtape", "error", err)
		http.Error(w, "Failed to create mixtape", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("created artist-seeded mixtape",
		"mixtape_id", m.ID,
		"mixtape_name", m.Name,
		"artist_id", artistID,
		"artist_name", a.Name,
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

			seed := vibes.NewArtistSeed(a)
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

				if _, saveErr := h.Client.Mixtape.UpdateOneID(m.ID).
					SetGenerationError(err.Error()).
					Save(ctx); saveErr != nil {
					h.Logger.Error("failed to save mixtape error", "error", saveErr)
				}

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

// ArtistMixtapeModal returns the HTML for the create mixtape modal (for HTMX).
func (h *Handler) ArtistMixtapeModal(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	artistID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid artist ID", http.StatusBadRequest)
		return
	}

	// Get artist
	a, err := h.Client.Artist.Query().
		Where(
			artist.ID(artistID),
			artist.HasUserWith(user.ID(u.ID)),
		).
		Only(r.Context())
	if err != nil {
		http.Error(w, "Artist not found", http.StatusNotFound)
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

	h.Render(w, r, artists.CreateMixtapeModalContent(a, djs))
}
