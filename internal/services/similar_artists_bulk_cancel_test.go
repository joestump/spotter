package services_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"spotter/internal/config"
	"spotter/internal/events"
	"spotter/internal/services"

	"github.com/stretchr/testify/assert"
)

func similarArtistsBulkLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func slowSimilarArtistsServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
}

// Governing: SPEC graceful-shutdown REQ-CTX-001 (background work stops on ctx.Done),
// SPEC similar-artists-discovery REQ-SIM-061 (the per-artist pause is cancellable, not a blind sleep)
func TestFindSimilarArtistsForAll_CancellationStopsThePassPromptly(t *testing.T) {
	client := setupSimilarArtistsTestDB(t)
	srv := slowSimilarArtistsServer()
	defer srv.Close()

	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.BaseURL = srv.URL + "/v1"
	svc := services.NewSimilarArtistsService(client, cfg, similarArtistsBulkLogger(), events.NewBus())

	u := similarArtistsCreateTestUser(t, client, "bulkcanceluser")
	for i := 0; i < 5; i++ {
		similarArtistsCreateTestArtist(t, client, u, fmt.Sprintf("Bulk Artist %d", i), []string{"Rock"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := svc.FindSimilarArtistsForAll(ctx, u.ID)
	elapsed := time.Since(start)

	assert.ErrorIs(t, err, context.DeadlineExceeded, "the pass must report why it stopped")
	assert.Less(t, elapsed, 1500*time.Millisecond,
		"a cancelled pass must stop at once, not work through the library at 500ms per artist")
}

func TestFindSimilarArtistsForAll_AlreadyCancelledContextReturnsAtOnce(t *testing.T) {
	client := setupSimilarArtistsTestDB(t)
	cfg := &config.Config{}
	cfg.OpenAI.APIKey = "test-key"
	svc := services.NewSimilarArtistsService(client, cfg, similarArtistsBulkLogger(), events.NewBus())

	u := similarArtistsCreateTestUser(t, client, "bulkprecanceluser")
	similarArtistsCreateTestArtist(t, client, u, "Pre Cancelled Artist", []string{"Rock"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := svc.FindSimilarArtistsForAll(ctx, u.ID)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), time.Second)
}
