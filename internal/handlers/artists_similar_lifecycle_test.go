package handlers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"spotter/ent"
	"spotter/internal/auth"
	"spotter/internal/config"
	"spotter/internal/crypto"
	"spotter/internal/events"
	"spotter/internal/handlers"
	"spotter/internal/lifecycle"
	"spotter/internal/services"

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Governing: ADR-0018 (graceful shutdown), SPEC graceful-shutdown REQ-CTX-003, REQ-WG-003
func setupSimilarArtistsHandler(t *testing.T) (*ent.Client, *handlers.Handler) {
	client := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{}
	bus := events.NewBus()
	encryptor, _ := crypto.NewEncryptor(make([]byte, 32))
	jwtManager := auth.NewJWTManager(testJWTSecret)
	svc := services.NewSimilarArtistsService(client, cfg, logger, bus)
	h := handlers.New(client, cfg, logger, encryptor, jwtManager, nil, nil, nil, nil, nil, svc, bus, nil)
	return client, h
}

func similarArtistsFindSimilarRequest(t *testing.T, client *ent.Client) *http.Request {
	t.Helper()
	u := createAlbumTestUser(t, client)
	a, err := client.Artist.Create().
		SetName("Lifecycle Artist").
		SetUser(u).
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/library/artist/find-similar", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(a.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))
	return req
}

func TestArtistFindSimilarIsTrackedAndDrainable(t *testing.T) {
	client, h := setupSimilarArtistsHandler(t)
	req := similarArtistsFindSimilarRequest(t, client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := lifecycle.NewJobs(ctx)
	h.Jobs = jobs

	w := httptest.NewRecorder()
	h.ArtistFindSimilar(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)

	jobs.Close()
	assert.NoError(t, jobs.Wait(context.Background()),
		"the lookup the handler spawned is tracked, so shutdown drains it instead of racing it")
	assert.EqualValues(t, 0, jobs.Running())
}

func TestArtistFindSimilarRefusesWhileShuttingDown(t *testing.T) {
	client, h := setupSimilarArtistsHandler(t)
	req := similarArtistsFindSimilarRequest(t, client)

	jobs := lifecycle.NewJobs(context.Background())
	jobs.Close()
	h.Jobs = jobs

	w := httptest.NewRecorder()
	h.ArtistFindSimilar(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"a request that lands after shutdown started must not spawn untracked work")
}
