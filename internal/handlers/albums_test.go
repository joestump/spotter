package handlers_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"spotter/ent"
	"spotter/ent/schema"
	"spotter/internal/auth"
	"spotter/internal/config"
	"spotter/internal/crypto"
	"spotter/internal/events"
	"spotter/internal/handlers"
	"spotter/internal/services"

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// albumTestUserCounter ensures unique usernames across parallel tests
var albumTestUserCounter int64

func uniqueAlbumTestUsername() string {
	return fmt.Sprintf("albumtestuser_%d", atomic.AddInt64(&albumTestUserCounter, 1))
}

func setupAlbumHandler(t *testing.T) (*ent.Client, *handlers.Handler, *events.Bus) {
	client := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{}
	bus := events.NewBus()
	syncer := services.NewSyncer(client, cfg, logger, bus)
	// Create test encryptor with a dummy key
	encryptor, _ := crypto.NewEncryptor(make([]byte, 32))
	jwtManager := auth.NewJWTManager(testJWTSecret)
	h := handlers.New(client, cfg, logger, encryptor, jwtManager, syncer, nil, nil, nil, nil, nil, bus)
	return client, h, bus
}

func createAlbumTestUser(t *testing.T, client *ent.Client) *ent.User {
	u, err := client.User.Create().
		SetUsername(uniqueAlbumTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)
	return u
}

func createTestAlbum(t *testing.T, client *ent.Client, u *ent.User, name string) *ent.Album {
	artist, err := client.Artist.Create().
		SetName("Test Artist").
		SetUser(u).
		Save(context.Background())
	require.NoError(t, err)

	a, err := client.Album.Create().
		SetName(name).
		SetUser(u).
		SetArtist(artist).
		Save(context.Background())
	require.NoError(t, err)
	return a
}

func createTestDJ(t *testing.T, client *ent.Client, u *ent.User, name string) *ent.DJ {
	dj, err := client.DJ.Create().
		SetName(name).
		SetUser(u).
		Save(context.Background())
	require.NoError(t, err)
	return dj
}

func TestAlbumCreateMixtape_Unauthorized(t *testing.T) {
	_, h, _ := setupAlbumHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/library/album/1/create-mixtape", nil)
	w := httptest.NewRecorder()

	h.AlbumCreateMixtape(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAlbumCreateMixtape_InvalidAlbumID(t *testing.T) {
	client, h, _ := setupAlbumHandler(t)
	u := createAlbumTestUser(t, client)

	req := httptest.NewRequest(http.MethodPost, "/library/album/invalid/create-mixtape", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "invalid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.AlbumCreateMixtape(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAlbumCreateMixtape_AlbumNotFound(t *testing.T) {
	client, h, _ := setupAlbumHandler(t)
	u := createAlbumTestUser(t, client)

	req := httptest.NewRequest(http.MethodPost, "/library/album/99999/create-mixtape", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "99999")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.AlbumCreateMixtape(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAlbumCreateMixtape_MissingDJ(t *testing.T) {
	client, h, _ := setupAlbumHandler(t)
	u := createAlbumTestUser(t, client)
	a := createTestAlbum(t, client, u, "Test Album")

	form := url.Values{}
	form.Set("name", "Test Mixtape")

	req := httptest.NewRequest(http.MethodPost, "/library/album/"+strconv.Itoa(a.ID)+"/create-mixtape",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(a.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.AlbumCreateMixtape(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAlbumCreateMixtape_DJNotFound(t *testing.T) {
	client, h, _ := setupAlbumHandler(t)
	u := createAlbumTestUser(t, client)
	a := createTestAlbum(t, client, u, "Test Album")

	form := url.Values{}
	form.Set("name", "Test Mixtape")
	form.Set("dj_id", "99999")

	req := httptest.NewRequest(http.MethodPost, "/library/album/"+strconv.Itoa(a.ID)+"/create-mixtape",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(a.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.AlbumCreateMixtape(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAlbumCreateMixtape_Success(t *testing.T) {
	client, h, _ := setupAlbumHandler(t)
	u := createAlbumTestUser(t, client)
	a := createTestAlbum(t, client, u, "Test Album")
	dj := createTestDJ(t, client, u, "Test DJ")

	form := url.Values{}
	form.Set("name", "Test Mixtape")
	form.Set("dj_id", strconv.Itoa(dj.ID))
	form.Set("max_tracks", "30")

	req := httptest.NewRequest(http.MethodPost, "/library/album/"+strconv.Itoa(a.ID)+"/create-mixtape",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(a.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.AlbumCreateMixtape(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify mixtape was created
	mixtapes, err := client.Mixtape.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, mixtapes, 1)
	assert.Equal(t, "Test Mixtape", mixtapes[0].Name)
	assert.Equal(t, "album", mixtapes[0].SeedType)
	assert.Equal(t, a.ID, *mixtapes[0].SeedID)
	assert.Equal(t, 30, mixtapes[0].MaxTracks)
}

func TestAlbumCreateMixtape_DefaultName(t *testing.T) {
	client, h, _ := setupAlbumHandler(t)
	u := createAlbumTestUser(t, client)
	a := createTestAlbum(t, client, u, "My Favorite Album")
	dj := createTestDJ(t, client, u, "Test DJ")

	form := url.Values{}
	form.Set("dj_id", strconv.Itoa(dj.ID))
	// No name provided - should default to album name + " Mix"

	req := httptest.NewRequest(http.MethodPost, "/library/album/"+strconv.Itoa(a.ID)+"/create-mixtape",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(a.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.AlbumCreateMixtape(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	mixtapes, err := client.Mixtape.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, mixtapes, 1)
	assert.Equal(t, "My Favorite Album Mix", mixtapes[0].Name)
}

func TestAlbumCreateMixtape_OtherUserAlbum(t *testing.T) {
	client, h, _ := setupAlbumHandler(t)
	u1 := createAlbumTestUser(t, client)
	u2 := createAlbumTestUser(t, client)

	// Create album owned by u1
	a := createTestAlbum(t, client, u1, "User1 Album")
	dj := createTestDJ(t, client, u2, "User2 DJ")

	form := url.Values{}
	form.Set("name", "Test Mixtape")
	form.Set("dj_id", strconv.Itoa(dj.ID))

	req := httptest.NewRequest(http.MethodPost, "/library/album/"+strconv.Itoa(a.ID)+"/create-mixtape",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(a.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	// User 2 tries to create mixtape from user 1's album
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u2))

	h.AlbumCreateMixtape(w, req)

	// Should fail because u2 doesn't own the album
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAlbumMixtapeModal_Unauthorized(t *testing.T) {
	_, h, _ := setupAlbumHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/library/album/1/mixtape-modal", nil)
	w := httptest.NewRecorder()

	h.AlbumMixtapeModal(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAlbumMixtapeModal_InvalidAlbumID(t *testing.T) {
	client, h, _ := setupAlbumHandler(t)
	u := createAlbumTestUser(t, client)

	req := httptest.NewRequest(http.MethodGet, "/library/album/invalid/mixtape-modal", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "invalid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.AlbumMixtapeModal(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAlbumMixtapeModal_AlbumNotFound(t *testing.T) {
	client, h, _ := setupAlbumHandler(t)
	u := createAlbumTestUser(t, client)

	req := httptest.NewRequest(http.MethodGet, "/library/album/99999/mixtape-modal", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "99999")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.AlbumMixtapeModal(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAlbumMixtapeModal_Success(t *testing.T) {
	client, h, _ := setupAlbumHandler(t)
	u := createAlbumTestUser(t, client)
	a := createTestAlbum(t, client, u, "Test Album")
	createTestDJ(t, client, u, "DJ One")
	createTestDJ(t, client, u, "DJ Two")

	req := httptest.NewRequest(http.MethodGet, "/library/album/"+strconv.Itoa(a.ID)+"/mixtape-modal", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(a.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.AlbumMixtapeModal(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// The response should contain HTML with the album name and DJ options
	body := w.Body.String()
	assert.Contains(t, body, "Test Album")
	assert.Contains(t, body, "DJ One")
	assert.Contains(t, body, "DJ Two")
}

func TestAlbumShow_IncludesDJsForMixtapeModal(t *testing.T) {
	client, h, _ := setupAlbumHandler(t)
	u := createAlbumTestUser(t, client)
	a := createTestAlbum(t, client, u, "Test Album")
	createTestDJ(t, client, u, "Test DJ")

	req := httptest.NewRequest(http.MethodGet, "/library/album/"+strconv.Itoa(a.ID), nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(a.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.AlbumShow(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	// Should contain the mixtape modal trigger
	assert.Contains(t, body, "create-mixtape-modal")
}

func TestAlbumShow_RecommendationsSection(t *testing.T) {
	client, h, _ := setupAlbumHandler(t)
	u := createAlbumTestUser(t, client)

	// Create artist first
	artist, err := client.Artist.Create().
		SetName("Test Artist").
		SetUser(u).
		Save(context.Background())
	require.NoError(t, err)

	// Create album with recommendations
	a, err := client.Album.Create().
		SetName("Test Album").
		SetUser(u).
		SetArtist(artist).
		SetRecommendations([]schema.AlbumRecommendation{
			{Name: "Similar Album 1", Artist: "Artist 1", Year: 2020, Reason: "Similar vibe"},
			{Name: "Similar Album 2", Artist: "Artist 2", Year: 2019, Reason: "Same genre"},
		}).
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/library/album/"+strconv.Itoa(a.ID), nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(a.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.AlbumShow(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	// Should contain recommended albums section
	assert.Contains(t, body, "Recommended Albums")
	assert.Contains(t, body, "Similar Album 1")
	assert.Contains(t, body, "Similar Album 2")
	assert.Contains(t, body, "AI") // AI attribution badge
}

func TestAlbumShow_NoRecommendationsWhenEmpty(t *testing.T) {
	client, h, _ := setupAlbumHandler(t)
	u := createAlbumTestUser(t, client)
	a := createTestAlbum(t, client, u, "Album Without Recommendations")

	req := httptest.NewRequest(http.MethodGet, "/library/album/"+strconv.Itoa(a.ID), nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(a.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.AlbumShow(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	// Should NOT contain the recommendations section when there are no recommendations
	assert.NotContains(t, body, "Recommended Albums")
}

func TestAlbumShow_UserIsolation(t *testing.T) {
	client, h, _ := setupAlbumHandler(t)
	u1 := createAlbumTestUser(t, client)
	u2 := createAlbumTestUser(t, client)

	// Create album owned by u1
	a := createTestAlbum(t, client, u1, "User1 Album")

	req := httptest.NewRequest(http.MethodGet, "/library/album/"+strconv.Itoa(a.ID), nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(a.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	// User 2 tries to view user 1's album
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u2))

	h.AlbumShow(w, req)

	// Should not be found because u2 doesn't own this album
	assert.Equal(t, http.StatusNotFound, w.Code)
}
