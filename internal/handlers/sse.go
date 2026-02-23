// Governing: ADR-0007 (in-memory event bus), ADR-0001 (HTMX+Templ), SPEC event-bus-sse REQ-SSE-001 through REQ-SSE-005
package handlers

import (
	"bytes"
	"fmt"
	"net/http"

	"spotter/ent"
	"spotter/internal/events"
	"spotter/internal/views/components"
)

// Events serves the SSE endpoint for real-time event streaming.
// Governing: SPEC event-bus-sse REQ-SSE-001 (auth-gated SSE endpoint with required headers)
// Governing: SPEC event-bus-sse REQ-SSE-003 (http.Flusher check, 500 if unsupported)
// Governing: SPEC event-bus-sse REQ-SSE-004 (context cancellation for client disconnect)
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	u := h.GetUser(r.Context())
	if u == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}
	flusher.Flush()

	eventChan, cancel := h.Bus.Subscribe(u.ID)
	defer cancel()

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-eventChan:
			var buf bytes.Buffer

			switch event.Type {
			case events.EventTypeRecentListen:
				if listen, ok := event.Payload.(*ent.Listen); ok {
					row := components.TrackTableRow{
						Listen: listen,
					}
					if err := components.TrackTableRowRender(row, []string{"source", "played_at", "track", "artist", "album"}, 0).Render(ctx, &buf); err != nil {
						h.Logger.Error("failed to render recent listen", "error", err)
						continue
					}
				}

			case events.EventTypeNotification:
				if payload, ok := event.Payload.(events.NotificationPayload); ok {
					if err := components.Toast(payload.Title, payload.Message, payload.IconType).Render(ctx, &buf); err != nil {
						h.Logger.Error("failed to render notification", "error", err)
						continue
					}
				}

			// Mixtape CRUD events
			case events.EventTypeMixtapeCreated:
				if payload, ok := event.Payload.(events.MixtapeCreatedPayload); ok {
					msg := fmt.Sprintf("Mixtape '%s' created with DJ %s", payload.MixtapeName, payload.DJName)
					if err := components.Toast("Mixtape Created", msg, "success").Render(ctx, &buf); err != nil {
						h.Logger.Error("failed to render mixtape created toast", "error", err)
						continue
					}
				}

			case events.EventTypeMixtapeUpdated:
				if payload, ok := event.Payload.(events.MixtapeUpdatedPayload); ok {
					msg := fmt.Sprintf("Mixtape '%s' has been updated", payload.MixtapeName)
					if err := components.Toast("Mixtape Updated", msg, "success").Render(ctx, &buf); err != nil {
						h.Logger.Error("failed to render mixtape updated toast", "error", err)
						continue
					}
				}

			case events.EventTypeMixtapeDeleted:
				if payload, ok := event.Payload.(events.MixtapeDeletedPayload); ok {
					msg := fmt.Sprintf("Mixtape '%s' has been deleted", payload.MixtapeName)
					if err := components.Toast("Mixtape Deleted", msg, "info").Render(ctx, &buf); err != nil {
						h.Logger.Error("failed to render mixtape deleted toast", "error", err)
						continue
					}
				}

			// Mixtape generation events
			case events.EventTypeMixtapeGenerating:
				if payload, ok := event.Payload.(events.MixtapeGeneratingPayload); ok {
					msg := fmt.Sprintf("%s is curating '%s'...", payload.DJName, payload.MixtapeName)
					if err := components.Toast("Generating Mixtape", msg, "info").Render(ctx, &buf); err != nil {
						h.Logger.Error("failed to render mixtape generating toast", "error", err)
						continue
					}
				}

			case events.EventTypeMixtapeGenerated:
				if payload, ok := event.Payload.(events.MixtapeGeneratedPayload); ok {
					msg := fmt.Sprintf("'%s' ready with %d tracks (%d matched)", payload.MixtapeName, payload.TracksCount, payload.MatchedCount)
					if err := components.Toast("Mixtape Generated", msg, "success").Render(ctx, &buf); err != nil {
						h.Logger.Error("failed to render mixtape generated toast", "error", err)
						continue
					}
				}

			case events.EventTypeMixtapeError:
				if payload, ok := event.Payload.(events.MixtapeErrorPayload); ok {
					msg := fmt.Sprintf("Failed to generate '%s': %s", payload.MixtapeName, payload.Error)
					if err := components.Toast("Generation Failed", msg, "error").Render(ctx, &buf); err != nil {
						h.Logger.Error("failed to render mixtape error toast", "error", err)
						continue
					}
				}

			// Playlist enhancement events
			case events.EventTypePlaylistEnhancing:
				if payload, ok := event.Payload.(events.PlaylistEnhancingPayload); ok {
					msg := fmt.Sprintf("%s is enhancing '%s'...", payload.DJName, payload.PlaylistName)
					if err := components.Toast("Enhancing Playlist", msg, "info").Render(ctx, &buf); err != nil {
						h.Logger.Error("failed to render playlist enhancing toast", "error", err)
						continue
					}
				}

			case events.EventTypePlaylistEnhanced:
				if payload, ok := event.Payload.(events.PlaylistEnhancedPayload); ok {
					msg := fmt.Sprintf("'%s' enhanced with %d new tracks", payload.PlaylistName, payload.TracksAdded)
					if err := components.Toast("Enhancement Complete", msg, "success").Render(ctx, &buf); err != nil {
						h.Logger.Error("failed to render playlist enhanced toast", "error", err)
						continue
					}
				}

			case events.EventTypePlaylistEnhanceError:
				if payload, ok := event.Payload.(events.PlaylistEnhanceErrorPayload); ok {
					if err := components.Toast("Enhancement Failed", payload.Error, "error").Render(ctx, &buf); err != nil {
						h.Logger.Error("failed to render playlist enhance error toast", "error", err)
						continue
					}
				}

			// Similar artists events
			case events.EventTypeSimilarArtistsSearching:
				if payload, ok := event.Payload.(events.SimilarArtistsSearchingPayload); ok {
					msg := fmt.Sprintf("Finding artists similar to %s...", payload.ArtistName)
					if err := components.Toast("Searching", msg, "info").Render(ctx, &buf); err != nil {
						h.Logger.Error("failed to render similar artists searching toast", "error", err)
						continue
					}
				}

			case events.EventTypeSimilarArtistsFound:
				if payload, ok := event.Payload.(events.SimilarArtistsFoundPayload); ok {
					msg := fmt.Sprintf("Found %d artists similar to %s", payload.SimilarCount, payload.ArtistName)
					if err := components.Toast("Similar Artists Found", msg, "success").Render(ctx, &buf); err != nil {
						h.Logger.Error("failed to render similar artists found toast", "error", err)
						continue
					}
				}

			case events.EventTypeSimilarArtistsError:
				if payload, ok := event.Payload.(events.SimilarArtistsErrorPayload); ok {
					msg := fmt.Sprintf("Could not find similar artists for %s: %s", payload.ArtistName, payload.Error)
					if err := components.Toast("Search Failed", msg, "error").Render(ctx, &buf); err != nil {
						h.Logger.Error("failed to render similar artists error toast", "error", err)
						continue
					}
				}
			}

			// Governing: SPEC event-bus-sse REQ-SSE-002 (render to HTML fragment), REQ-SSE-005 (named event field)
			if buf.Len() > 0 {
				if _, err := fmt.Fprintf(w, "event: %s\n", event.Type); err != nil {
					h.Logger.Error("failed to write SSE event type", "error", err)
					return
				}
				// Write data line by line to adhere to SSE spec
				lines := bytes.Split(buf.Bytes(), []byte("\n"))
				for _, line := range lines {
					if len(line) > 0 {
						if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
							h.Logger.Error("failed to write SSE data line", "error", err)
							return
						}
					}
				}
				if _, err := fmt.Fprintf(w, "\n"); err != nil {
					h.Logger.Error("failed to write SSE separator", "error", err)
					return
				}
				flusher.Flush()
			}
		}
	}
}
