package handlers

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"spotter/ent"
	"spotter/ent/album"
	"spotter/ent/artist"
	"spotter/ent/playlist"
	"spotter/ent/user"

	"github.com/go-chi/chi/v5"
)

// ArtistImage serves the primary image for an artist
func (h *Handler) ArtistImage(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	// Parse ID from URL (remove .png extension if present)
	idStr := chi.URLParam(r, "id")
	idStr = strings.TrimSuffix(idStr, ".png")
	idStr = strings.TrimSuffix(idStr, ".jpg")
	idStr = strings.TrimSuffix(idStr, ".jpeg")
	idStr = strings.TrimSuffix(idStr, ".webp")

	artistID, err := strconv.Atoi(idStr)
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
		http.Error(w, "Artist not found", http.StatusNotFound)
		return
	}

	// Find the best image to serve
	if len(a.Edges.Images) == 0 {
		http.Error(w, "No image available", http.StatusNotFound)
		return
	}

	h.serveImage(w, r, bestArtistImagePath(a.Edges.Images))
}

// imageCandidate normalizes artist and album image records for best-image ranking.
type imageCandidate struct {
	localPath string
	isPrimary bool
	likes     int
	area      int64
	id        int
}

// betterImage reports whether a should be served over b.
// Governing: SPEC metadata-enrichment-pipeline REQ-ENRICH-022 — rank by
// IsPrimary, then highest Likes, then largest dimensions (Width × Height).
// ID is a deterministic final tie-breaker.
func betterImage(a, b imageCandidate) bool {
	if a.isPrimary != b.isPrimary {
		return a.isPrimary
	}
	if a.likes != b.likes {
		return a.likes > b.likes
	}
	if a.area != b.area {
		return a.area > b.area
	}
	return a.id < b.id
}

// bestCandidatePath returns the local path of the best-ranked candidate, or ""
// if there are no candidates.
func bestCandidatePath(candidates []imageCandidate) string {
	var best *imageCandidate
	for i := range candidates {
		if best == nil || betterImage(candidates[i], *best) {
			best = &candidates[i]
		}
	}
	if best == nil {
		return ""
	}
	return best.localPath
}

// bestArtistImagePath selects the best downloaded artist image per
// REQ-ENRICH-022 (IsPrimary → Likes → dimensions).
// Governing: SPEC metadata-enrichment-pipeline REQ-ENRICH-022;
// issue #127 — skip records with empty local_path to avoid serving 404 when the image wasn't downloaded
func bestArtistImagePath(images []*ent.ArtistImage) string {
	candidates := make([]imageCandidate, 0, len(images))
	for _, img := range images {
		if img.LocalPath == "" {
			continue
		}
		c := imageCandidate{
			localPath: img.LocalPath,
			isPrimary: img.IsPrimary,
			id:        img.ID,
		}
		if img.Likes != nil {
			c.likes = *img.Likes
		}
		if img.Width != nil && img.Height != nil {
			c.area = int64(*img.Width) * int64(*img.Height)
		}
		candidates = append(candidates, c)
	}
	return bestCandidatePath(candidates)
}

// bestAlbumImagePath selects the best downloaded album image per
// REQ-ENRICH-022 (IsPrimary → dimensions; album images carry no Likes field).
// Governing: SPEC metadata-enrichment-pipeline REQ-ENRICH-022;
// issue #127 — skip records with empty local_path to avoid serving 404 when the image wasn't downloaded
func bestAlbumImagePath(images []*ent.AlbumImage) string {
	candidates := make([]imageCandidate, 0, len(images))
	for _, img := range images {
		if img.LocalPath == "" {
			continue
		}
		c := imageCandidate{
			localPath: img.LocalPath,
			isPrimary: img.IsPrimary,
			id:        img.ID,
		}
		if img.Width != nil && img.Height != nil {
			c.area = int64(*img.Width) * int64(*img.Height)
		}
		candidates = append(candidates, c)
	}
	return bestCandidatePath(candidates)
}

// AlbumImage serves the primary image for an album
func (h *Handler) AlbumImage(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	// Parse ID from URL (remove extension if present)
	idStr := chi.URLParam(r, "id")
	idStr = strings.TrimSuffix(idStr, ".png")
	idStr = strings.TrimSuffix(idStr, ".jpg")
	idStr = strings.TrimSuffix(idStr, ".jpeg")
	idStr = strings.TrimSuffix(idStr, ".webp")

	albumID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Invalid album ID", http.StatusBadRequest)
		return
	}

	// Get the album with images
	a, err := h.Client.Album.Query().
		Where(
			album.ID(albumID),
			album.HasUserWith(user.ID(u.ID)),
		).
		WithImages().
		Only(r.Context())
	if err != nil {
		http.Error(w, "Album not found", http.StatusNotFound)
		return
	}

	// Find the best image to serve
	if len(a.Edges.Images) == 0 {
		http.Error(w, "No image available", http.StatusNotFound)
		return
	}

	h.serveImage(w, r, bestAlbumImagePath(a.Edges.Images))
}

// PlaylistImage serves the image for a playlist
func (h *Handler) PlaylistImage(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
		return
	}

	// Parse ID from URL (remove extension if present)
	idStr := chi.URLParam(r, "id")
	idStr = strings.TrimSuffix(idStr, ".png")
	idStr = strings.TrimSuffix(idStr, ".jpg")
	idStr = strings.TrimSuffix(idStr, ".jpeg")
	idStr = strings.TrimSuffix(idStr, ".webp")

	playlistID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Invalid playlist ID", http.StatusBadRequest)
		return
	}

	// Get the playlist
	p, err := h.Client.Playlist.Query().
		Where(
			playlist.ID(playlistID),
			playlist.HasUserWith(user.ID(u.ID)),
		).
		Only(r.Context())
	if err != nil {
		http.Error(w, "Playlist not found", http.StatusNotFound)
		return
	}

	// Playlists store image URL directly
	if p.ImageURL == "" {
		http.Error(w, "No image available", http.StatusNotFound)
		return
	}

	// Governing: SPEC user-authentication REQ "Input Validation"
	// Validate URL scheme before redirect to prevent open redirect attacks (javascript:, data:, etc.)
	parsed, err := url.Parse(p.ImageURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		h.Logger.Warn("blocked redirect to invalid image URL", "url", p.ImageURL)
		http.Error(w, "invalid image URL", http.StatusBadRequest)
		return
	}

	// Redirect to the external URL for playlists
	http.Redirect(w, r, p.ImageURL, http.StatusTemporaryRedirect)
}

// serveImage serves an image from a local path
// Governing: SPEC user-authentication REQ "Input Validation"
// filepath.Abs + HasPrefix validates path stays within ./data to prevent path traversal
func (h *Handler) serveImage(w http.ResponseWriter, r *http.Request, localPath string) {
	// Try to serve local file first
	if localPath != "" {
		// Clean and validate the path
		cleanPath := filepath.Clean(localPath)

		// Get the absolute path of the images directory for validation
		baseDir, err := filepath.Abs("./data")
		if err != nil {
			http.Error(w, "No image available", http.StatusNotFound)
			return
		}
		absPath, err := filepath.Abs(cleanPath)
		if err != nil || !strings.HasPrefix(absPath, baseDir+string(filepath.Separator)) {
			h.Logger.Warn("path traversal attempt blocked", "path", localPath, "cleaned", cleanPath)
			http.Error(w, "No image available", http.StatusNotFound)
			return
		}

		// Check if file exists
		if _, err := os.Stat(absPath); err == nil {
			// All images are converted to PNG on download
			w.Header().Set("Content-Type", "image/png")

			// Set cache headers for images
			w.Header().Set("Cache-Control", "public, max-age=86400") // 24 hours

			http.ServeFile(w, r, absPath)
			return
		}
	}

	http.Error(w, "No image available", http.StatusNotFound)
}
