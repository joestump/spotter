package handlers_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"spotter/ent"
	"spotter/internal/auth"
	"spotter/internal/config"
	"spotter/internal/crypto"
	"spotter/internal/events"
	"spotter/internal/handlers"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sseTestUserCounter int64

const sseEventsURL = "/events"

func uniqueSSETestUsername() string {
	return fmt.Sprintf("sse_user_%d", atomic.AddInt64(&sseTestUserCounter, 1))
}

// setupSSEHandler builds a handler whose streams heartbeat quickly enough for a
// test to observe a couple of keep-alive frames.
func setupSSEHandler(t *testing.T) (*ent.Client, *handlers.Handler, *events.Bus) {
	client := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{}
	bus := events.NewBus()
	encryptor, _ := crypto.NewEncryptor(make([]byte, 32))
	jwtManager := auth.NewJWTManager(testJWTSecret)
	h := handlers.New(client, cfg, logger, encryptor, jwtManager, nil, nil, nil, nil, nil, nil, bus, nil)
	h.SSEHeartbeatInterval = 10 * time.Millisecond
	return client, h, bus
}

func createSSETestUser(t *testing.T, client *ent.Client) *ent.User {
	t.Helper()
	u, err := client.User.Create().
		SetUsername(uniqueSSETestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)
	return u
}

// startSSEServer serves the SSE handler behind a stub of AuthMiddleware that puts
// the user in the request context, with every request parented to rootCtx the way
// http.Server.BaseContext does in cmd/server.
func startSSEServer(t *testing.T, h *handlers.Handler, u *ent.User, rootCtx context.Context, timeouts time.Duration) (string, *http.Server) {
	t.Helper()
	serveMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), handlers.UserContextKey, u)
		h.Events(w, r.WithContext(ctx))
	})
	srv := &http.Server{
		Handler:      serveMux,
		ReadTimeout:  timeouts,
		WriteTimeout: timeouts,
	}
	srv.BaseContext = func(net.Listener) context.Context { return rootCtx }

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		// Serve returns once the test tears the server down.
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String(), srv
}

// streamLines splits a streamed response into lines and stops when the response
// body is closed or the test ends.
func streamLines(t *testing.T, body io.Reader) <-chan string {
	t.Helper()
	lines := make(chan string, 256)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-done:
				return
			}
		}
	}()
	return lines
}

// readFrame returns the data of the next frame whose event field is wantEvent.
func readFrame(t *testing.T, lines <-chan string, wantEvent string, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	var data strings.Builder
	inFrame := false
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed before event %q arrived", wantEvent)
			}
			switch {
			case strings.HasPrefix(line, ":"):
				// keep-alive comment, not part of any frame
			case line == "event: "+wantEvent:
				inFrame = true
			case inFrame && line == "":
				return data.String()
			case inFrame:
				data.WriteString(strings.TrimPrefix(line, "data: "))
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event %q", wantEvent)
		}
	}
}

// TestEvents_StreamSurvivesWriteTimeout is the acceptance criterion for the 60s
// stream death: the handler clears the per-request deadlines, so a stream stays
// usable long past server.write_timeout (exercised with a deliberately short one).
func TestEvents_StreamSurvivesWriteTimeout(t *testing.T) {
	client, h, bus := setupSSEHandler(t)
	u := createSSETestUser(t, client)

	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()

	const writeTimeout = 60 * time.Millisecond
	url, _ := startSSEServer(t, h, u, rootCtx, writeTimeout)

	resp, err := http.Get(url + sseEventsURL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	lines := streamLines(t, resp.Body)
	start := time.Now()

	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-ticker.C:
				bus.Publish(u.ID, events.Event{
					Type:    events.EventTypeNotification,
					Payload: events.NotificationPayload{Title: "ping", Message: fmt.Sprintf("event %d", i), IconType: "info"},
				})
			}
		}
	}()

	var deliveredLate, heartbeats int
	collectUntil := start.Add(600 * time.Millisecond)
	streamOpen := true
	for streamOpen && time.Now().Before(collectUntil) {
		select {
		case line, ok := <-lines:
			if !ok {
				streamOpen = false
				continue
			}
			switch {
			case strings.HasPrefix(line, ": heartbeat"):
				heartbeats++
			case strings.HasPrefix(line, "data: "):
				if time.Since(start) > 2*writeTimeout {
					deliveredLate++
				}
			}
		case <-time.After(time.Until(collectUntil)):
		}
	}
	close(stop)

	require.Greater(t, deliveredLate, 1, "stream must keep delivering events past server.write_timeout")
	require.Greater(t, heartbeats, 1, "expected periodic heartbeat comments")
	assert.Greater(t, time.Since(start), 2*writeTimeout)
}

// TestEvents_OpenStreamEndsOnShutdown is the acceptance criterion for clean
// shutdown: cancelling the root context (what SIGTERM does in cmd/server) ends the
// stream, so http.Server.Shutdown drains instead of waiting for the hard-exit timer.
func TestEvents_OpenStreamEndsOnShutdown(t *testing.T) {
	client, h, bus := setupSSEHandler(t)
	u := createSSETestUser(t, client)

	rootCtx, cancelRoot := context.WithCancel(context.Background())
	url, srv := startSSEServer(t, h, u, rootCtx, 0)

	resp, err := http.Get(url + sseEventsURL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	lines := streamLines(t, resp.Body)
	require.True(t, awaitFrameAfterPublishing(t, bus, u, lines, 2*time.Second), "stream never showed a delivered event")

	cancelRoot()

	drained := false
	endDeadline := time.After(2 * time.Second)
	for !drained {
		select {
		case _, ok := <-lines:
			if !ok {
				// The handler returned, so the response terminated cleanly.
				drained = true
			}
		case <-endDeadline:
			t.Fatal("stream stayed open after the root context was cancelled")
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelShutdown()
	require.NoError(t, srv.Shutdown(shutdownCtx), "shutdown should drain once the stream ends")
}

// awaitFrameAfterPublishing publishes notifications until one reaches the stream,
// which proves the handler subscribed on this connection (the bus keeps no replay).
func awaitFrameAfterPublishing(t *testing.T, bus *events.Bus, u *ent.User, lines <-chan string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.After(timeout)
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		bus.Publish(u.ID, events.Event{
			Type:    events.EventTypeNotification,
			Payload: events.NotificationPayload{Title: "ready", Message: "stream is open", IconType: "info"},
		})
	watching:
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					return false
				}
				if line == "event: "+string(events.EventTypeNotification) {
					return true
				}
			case <-poll.C:
				break watching
			case <-deadline:
				return false
			}
		}
	}
}

// TestEvents_RecentListenCarriesTypedPayload covers REQ-BUS-011/REQ-BUS-012 at the
// consumer: the syncer publishes RecentListenPayload and the stream renders the row.
func TestEvents_RecentListenCarriesTypedPayload(t *testing.T) {
	client, h, bus := setupSSEHandler(t)
	u := createSSETestUser(t, client)

	playedAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	listen, err := client.Listen.Create().
		SetUser(u).
		SetTrackName("Second Thought").
		SetArtistName("Big Thief").
		SetAlbumName("Dragon New Warm Mountain").
		SetSource("spotify").
		SetPlayedAt(playedAt).
		Save(context.Background())
	require.NoError(t, err)

	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	url, _ := startSSEServer(t, h, u, rootCtx, 0)

	resp, err := http.Get(url + sseEventsURL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	lines := streamLines(t, resp.Body)
	bus.PublishRecentListen(u.ID, listen)

	frame := readFrame(t, lines, "recent-listen", 2*time.Second)
	assert.Contains(t, frame, "Second Thought")
	assert.Contains(t, frame, "Big Thief")
	assert.Contains(t, frame, "Spotify")
}

// TestEvents_RecentListenDeletedBeforeDelivery keeps a stream alive when the row
// behind a published payload is gone before the handler renders it.
func TestEvents_RecentListenDeletedBeforeDelivery(t *testing.T) {
	client, h, bus := setupSSEHandler(t)
	u := createSSETestUser(t, client)

	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	url, _ := startSSEServer(t, h, u, rootCtx, 0)

	resp, err := http.Get(url + sseEventsURL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	lines := streamLines(t, resp.Body)
	bus.Publish(u.ID, events.Event{
		Type:    events.EventTypeRecentListen,
		Payload: events.RecentListenPayload{ListenID: 424242},
	})
	bus.Publish(u.ID, events.Event{
		Type:    events.EventTypeNotification,
		Payload: events.NotificationPayload{Title: "still here", Message: "stream survived", IconType: "info"},
	})

	frame := readFrame(t, lines, "notification", 2*time.Second)
	assert.Contains(t, frame, "stream survived")
}
