package handlers

import (
	"context"
	"net/http"
	"strconv"

	"spotter/ent"
	"spotter/ent/listen"
	"spotter/ent/user"
	"spotter/internal/views/components"
	"spotter/internal/views/recent"
)

const (
	sortDirAsc = "asc"
)

// listenSortFields maps URL sort params to ent field selectors
var listenSortFields = map[string]string{
	"played_at": listen.FieldPlayedAt,
	"track":     listen.FieldTrackName,
	"artist":    listen.FieldArtistName,
	"album":     listen.FieldAlbumName,
	"source":    listen.FieldSource,
}

func (h *Handler) RecentListens(w http.ResponseWriter, r *http.Request) {
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
		sortCol = "played_at"
	}
	if sortDir == "" {
		sortDir = "desc"
	}

	pageSize := u.PaginationSize
	offset := (page - 1) * pageSize

	// Build query with optional artist filter
	query := h.Client.Listen.Query().
		Where(listen.HasUserWith(user.ID(u.ID)))

	if artistFilter != "" {
		query = query.Where(listen.ArtistName(artistFilter))
	}

	// Apply sorting
	if field, ok := listenSortFields[sortCol]; ok {
		if sortDir == sortDirAsc {
			query = query.Order(ent.Asc(field))
		} else {
			query = query.Order(ent.Desc(field))
		}
	} else {
		query = query.Order(ent.Desc(listen.FieldPlayedAt))
	}

	listens, err := query.
		WithArtist().
		WithAlbum(func(q *ent.AlbumQuery) {
			q.WithImages()
		}).
		WithTrack().
		Limit(pageSize).
		Offset(offset).
		All(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch recent listens", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Build count query with same filter
	countQuery := h.Client.Listen.Query().
		Where(listen.HasUserWith(user.ID(u.ID)))

	if artistFilter != "" {
		countQuery = countQuery.Where(listen.ArtistName(artistFilter))
	}

	// Get total count for pagination
	total, err := countQuery.Count(r.Context())
	if err != nil {
		h.Logger.Error("failed to count listens", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	totalPages := (total + pageSize - 1) / pageSize

	// Convert listens to rows
	rows := make([]components.TrackTableRow, len(listens))
	for i, l := range listens {
		rows[i] = components.TrackTableRow{
			Listen: l,
		}
	}

	h.Render(w, r, recent.Index(rows, page, totalPages, h.Config, artistFilter, sortCol, sortDir))
}

func (h *Handler) RefreshRecentListens(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	// Trigger background sync
	go func() {
		if err := h.Syncer.SyncRecentListens(context.Background(), u); err != nil {
			h.Logger.Error("Background sync failed", "error", err, "username", u.Username)
		}
	}()

	// Reload the page
	w.Header().Set("HX-Redirect", "/recent")
}
