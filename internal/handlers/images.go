package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"spotter/ent/album"
	"spotter/ent/artist"
	"spotter/ent/playlist"
	"spotter/ent/user"

	"github.com/go-chi/chi/v5"
)

// ArtistImage serves the primary image for an artist
func (h *Handler) ArtistImage(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
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

	// Prefer primary thumbnail, then any primary, then first image
	var localPath string
	for _, img := range a.Edges.Images {
		if img.IsPrimary && string(img.ImageType) == "thumbnail" {
			localPath = img.LocalPath
			break
		}
	}
	if localPath == "" {
		for _, img := range a.Edges.Images {
			if img.IsPrimary {
				localPath = img.LocalPath
				break
			}
		}
	}
	if localPath == "" {
		localPath = a.Edges.Images[0].LocalPath
	}

	h.serveImage(w, r, localPath)
}

// AlbumImage serves the primary image for an album
func (h *Handler) AlbumImage(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
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

	// Prefer primary image, then first image
	var localPath string
	for _, img := range a.Edges.Images {
		if img.IsPrimary {
			localPath = img.LocalPath
			break
		}
	}
	if localPath == "" {
		localPath = a.Edges.Images[0].LocalPath
	}

	h.serveImage(w, r, localPath)
}

// PlaylistImage serves the image for a playlist
func (h *Handler) PlaylistImage(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
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

	// Redirect to the external URL for playlists
	http.Redirect(w, r, p.ImageURL, http.StatusTemporaryRedirect)
}

// serveImage serves an image from a local path
// Governing: filepath.Abs + HasPrefix validates path stays within ./data to prevent traversal
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
