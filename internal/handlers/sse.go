// Governing: ADR-0007 (in-memory event bus), ADR-0018 (graceful shutdown), ADR-0001 (HTMX+Templ), SPEC event-bus-sse REQ-SSE-001 through REQ-SSE-007
package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

	"spotter/internal/events"
	"spotter/internal/views/components"
)

// defaultSSEHeartbeatInterval is how often a keep-alive comment frame is written
// when the handler does not override it. Governing: SPEC event-bus-sse REQ-SSE-006
const defaultSSEHeartbeatInterval = 15 * time.Second

// Events serves the SSE endpoint for real-time event streaming.
// Governing: SPEC event-bus-sse REQ-SSE-001 (auth-gated SSE endpoint with required headers)
// Governing: SPEC event-bus-sse REQ-SSE-003 (http.Flusher check, 500 if unsupported)
// Governing: SPEC event-bus-sse REQ-SSE-004 (context cancellation for client disconnect)
// Governing: SPEC event-bus-sse REQ-SSE-006 (heartbeat comments), REQ-SSE-007 (stream timeouts)
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	u := h.RequireUser(w, r)
	if u == nil {
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

	// A stream is open for as long the client keeps it, so the server's
	// read/write timeouts must not apply here: a write deadline would kill every
	// connection at server.write_timeout (60s by default) and drop the events
	// published while the client reconnects, because the Bus keeps no replay
	// buffer. ReadHeaderTimeout still bounds the request phase, which is the part
	// a slow client can abuse. Governing: SPEC event-bus-sse REQ-SSE-007
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Time{}); err != nil {
		h.Logger.Debug("could not clear SSE write deadline", "error", err)
	}
	if err := controller.SetReadDeadline(time.Time{}); err != nil {
		h.Logger.Debug("could not clear SSE read deadline", "error", err)
	}
	flusher.Flush()

	eventChan, cancel := h.Bus.Subscribe(u.ID)
	defer cancel()

	ctx := r.Context()

	heartbeatInterval := h.SSEHeartbeatInterval
	if heartbeatInterval <= 0 {
		heartbeatInterval = defaultSSEHeartbeatInterval
	}
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			// Covers both a closed browser tab and the root context being
			// cancelled at shutdown, since the server hands requests the root
			// context via BaseContext. Governing: ADR-0018, SPEC graceful-shutdown
			// REQ-CTX-004, SPEC event-bus-sse REQ-SSE-004.
			return
		case <-heartbeat.C:
			// Comment frames keep intermediates willing to hold the stream open and
			// notice clients that vanished without closing the connection; that
			// failed write ends the handler, which releases the subscriber.
			// Governing: SPEC event-bus-sse REQ-SSE-006
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				h.Logger.Debug("ending SSE stream, client gone", "user_id", u.ID, "error", err)
				return
			}
			flusher.Flush()
		case event := <-eventChan:
			var buf bytes.Buffer

			switch event.Type {
			case events.EventTypeRecentListen:
				if payload, ok := event.Payload.(events.RecentListenPayload); ok {
					// The bus carries the typed payload, so the row rendered into the
					// fragment is read back here. Governing: SPEC event-bus-sse
					// REQ-BUS-011, REQ-BUS-012
					listen, err := h.Client.Listen.Get(ctx, payload.ListenID)
					if err != nil {
						h.Logger.Warn("recent listen is no longer readable", "listen_id", payload.ListenID, "error", err)
						continue
					}
					row := components.TrackTableRow{
						Listen:        listen,
						LidarrBaseURL: h.Config.Lidarr.BaseURL,
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
