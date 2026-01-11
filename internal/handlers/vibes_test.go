package handlers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"spotter/ent"
	"spotter/ent/enttest"
	"spotter/ent/mixtape"
	"spotter/internal/auth"
	"spotter/internal/config"
	"spotter/internal/crypto"
	"spotter/internal/events"
	"spotter/internal/handlers"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
)

func setupVibesHandler(t *testing.T) (*ent.Client, *handlers.Handler, *events.Bus) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	cfg := &config.Config{
		Vibes: config.VibesConfig{
			DefaultMaxTracks: 25,
		},
	}
	bus := events.NewBus()

	// Create a no-op logger for tests
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	encryptor, _ := crypto.NewEncryptor(make([]byte, 32))
	jwtManager := auth.NewJWTManager(testJWTSecret)
	h := handlers.New(client, cfg, logger, encryptor, jwtManager, nil, nil, nil, nil, nil, nil, bus)
	return client, h, bus
}

var vibesTestUserCounter int

func uniqueVibesTestUsername() string {
	vibesTestUserCounter++
	return "vibesuser_" + strconv.Itoa(vibesTestUserCounter)
}

func createVibesTestUser(t *testing.T, client *ent.Client) *ent.User {
	u, err := client.User.Create().
		SetUsername(uniqueVibesTestUsername()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)
	return u
}

func createVibesTestDJ(t *testing.T, client *ent.Client, u *ent.User, name string) *ent.DJ {
	dj, err := client.DJ.Create().
		SetName(name).
		SetSystemPrompt("Test DJ prompt").
		SetUser(u).
		Save(context.Background())
	require.NoError(t, err)
	return dj
}

func createVibesTestMixtape(t *testing.T, client *ent.Client, u *ent.User, dj *ent.DJ, name string) *ent.Mixtape {
	m, err := client.Mixtape.Create().
		SetName(name).
		SetDescription("Test mixtape description").
		SetMaxTracks(25).
		SetDj(dj).
		SetUser(u).
		Save(context.Background())
	require.NoError(t, err)
	return m
}

func createVibesTestTrack(t *testing.T, client *ent.Client, u *ent.User, name string) *ent.Track {
	// Artist requires a User edge
	artist, err := client.Artist.Create().
		SetName("Test Artist " + name).
		SetUser(u).
		Save(context.Background())
	require.NoError(t, err)

	// Album requires a User edge
	album, err := client.Album.Create().
		SetName("Test Album " + name).
		SetArtist(artist).
		SetUser(u).
		Save(context.Background())
	require.NoError(t, err)

	track, err := client.Track.Create().
		SetName(name).
		SetArtist(artist).
		SetAlbum(album).
		Save(context.Background())
	require.NoError(t, err)
	return track
}

// MixtapeShow Tests

func TestMixtapeShow_Unauthorized(t *testing.T) {
	_, h, _ := setupVibesHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/vibes/mixtapes/1", nil)
	w := httptest.NewRecorder()

	h.MixtapeShow(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
}

func TestMixtapeShow_InvalidID(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)

	req := httptest.NewRequest(http.MethodGet, "/vibes/mixtapes/invalid", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "invalid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.MixtapeShow(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMixtapeShow_NotFound(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)

	req := httptest.NewRequest(http.MethodGet, "/vibes/mixtapes/99999", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "99999")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.MixtapeShow(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMixtapeShow_Success(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u, "Test DJ")
	m := createVibesTestMixtape(t, client, u, dj, "Test Mixtape")

	req := httptest.NewRequest(http.MethodGet, "/vibes/mixtapes/"+strconv.Itoa(m.ID), nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(m.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.MixtapeShow(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Test Mixtape")
}

func TestMixtapeShow_WithTracks(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u, "Test DJ")

	// Create tracks
	track1 := createVibesTestTrack(t, client, u, "Track 1")
	track2 := createVibesTestTrack(t, client, u, "Track 2")

	// Create mixtape with track IDs
	m, err := client.Mixtape.Create().
		SetName("Mixtape With Tracks").
		SetDj(dj).
		SetUser(u).
		SetTrackIds([]string{strconv.Itoa(track1.ID), strconv.Itoa(track2.ID)}).
		SetTrackCount(2).
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/vibes/mixtapes/"+strconv.Itoa(m.ID), nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(m.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.MixtapeShow(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Mixtape With Tracks")
	assert.Contains(t, body, "Track 1")
	assert.Contains(t, body, "Track 2")
}

func TestMixtapeShow_OtherUserMixtape(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u1 := createVibesTestUser(t, client)
	u2 := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u1, "User1 DJ")
	m := createVibesTestMixtape(t, client, u1, dj, "User1 Mixtape")

	req := httptest.NewRequest(http.MethodGet, "/vibes/mixtapes/"+strconv.Itoa(m.ID), nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(m.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	// User 2 tries to view User 1's mixtape
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u2))

	h.MixtapeShow(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// CreateMixtape Tests

func TestCreateMixtape_Unauthorized(t *testing.T) {
	_, h, _ := setupVibesHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes", nil)
	w := httptest.NewRecorder()

	h.CreateMixtape(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCreateMixtape_MissingName(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u, "Test DJ")

	form := url.Values{}
	form.Set("dj_id", strconv.Itoa(dj.ID))

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.CreateMixtape(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateMixtape_MissingDJ(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)

	form := url.Values{}
	form.Set("name", "Test Mixtape")

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.CreateMixtape(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateMixtape_DJNotFound(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)

	form := url.Values{}
	form.Set("name", "Test Mixtape")
	form.Set("dj_id", "99999")

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.CreateMixtape(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCreateMixtape_Success(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u, "Test DJ")

	form := url.Values{}
	form.Set("name", "New Mixtape")
	form.Set("description", "A great mixtape")
	form.Set("dj_id", strconv.Itoa(dj.ID))
	form.Set("max_tracks", "30")
	form.Set("schedule", "weekly")

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.CreateMixtape(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "mixtape-created", w.Header().Get("HX-Trigger"))

	// Verify mixtape was created
	mixtapes, err := client.Mixtape.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, mixtapes, 1)
	assert.Equal(t, "New Mixtape", mixtapes[0].Name)
	assert.Equal(t, "A great mixtape", mixtapes[0].Description)
	assert.Equal(t, 30, mixtapes[0].MaxTracks)
	assert.Equal(t, mixtape.ScheduleWeekly, mixtapes[0].Schedule)
}

func TestCreateMixtape_WithSync(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u, "Test DJ")

	form := url.Values{}
	form.Set("name", "Synced Mixtape")
	form.Set("dj_id", strconv.Itoa(dj.ID))
	form.Set("sync_to_navidrome", "on")

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.CreateMixtape(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	mixtapes, err := client.Mixtape.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, mixtapes, 1)
	assert.True(t, mixtapes[0].SyncToNavidrome)
}

func TestCreateMixtape_OtherUserDJ(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u1 := createVibesTestUser(t, client)
	u2 := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u1, "User1 DJ")

	form := url.Values{}
	form.Set("name", "Test Mixtape")
	form.Set("dj_id", strconv.Itoa(dj.ID))

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	// User 2 tries to use User 1's DJ
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u2))

	h.CreateMixtape(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// UpdateMixtape Tests

func TestUpdateMixtape_Unauthorized(t *testing.T) {
	_, h, _ := setupVibesHandler(t)

	req := httptest.NewRequest(http.MethodPut, "/vibes/mixtapes/1", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.UpdateMixtape(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestUpdateMixtape_InvalidID(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)

	req := httptest.NewRequest(http.MethodPut, "/vibes/mixtapes/invalid", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "invalid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.UpdateMixtape(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateMixtape_NotFound(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)

	form := url.Values{}
	form.Set("name", "Updated Name")

	req := httptest.NewRequest(http.MethodPut, "/vibes/mixtapes/99999",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "99999")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.UpdateMixtape(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateMixtape_Success(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u, "Test DJ")
	m := createVibesTestMixtape(t, client, u, dj, "Original Name")

	form := url.Values{}
	form.Set("name", "Updated Name")
	form.Set("description", "Updated description")
	form.Set("dj_id", strconv.Itoa(dj.ID))
	form.Set("max_tracks", "50")
	form.Set("schedule", "daily")

	req := httptest.NewRequest(http.MethodPut, "/vibes/mixtapes/"+strconv.Itoa(m.ID),
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(m.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.UpdateMixtape(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "mixtape-updated", w.Header().Get("HX-Trigger"))

	// Verify mixtape was updated
	updated, err := client.Mixtape.Get(context.Background(), m.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", updated.Name)
	assert.Equal(t, "Updated description", updated.Description)
	assert.Equal(t, 50, updated.MaxTracks)
	assert.Equal(t, mixtape.ScheduleDaily, updated.Schedule)
}

func TestUpdateMixtape_OtherUserMixtape(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u1 := createVibesTestUser(t, client)
	u2 := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u1, "User1 DJ")
	m := createVibesTestMixtape(t, client, u1, dj, "User1 Mixtape")

	form := url.Values{}
	form.Set("name", "Hacked Name")
	form.Set("dj_id", strconv.Itoa(dj.ID))

	req := httptest.NewRequest(http.MethodPut, "/vibes/mixtapes/"+strconv.Itoa(m.ID),
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(m.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	// User 2 tries to update User 1's mixtape
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u2))

	h.UpdateMixtape(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// DeleteMixtape Tests

func TestDeleteMixtape_Unauthorized(t *testing.T) {
	_, h, _ := setupVibesHandler(t)

	req := httptest.NewRequest(http.MethodDelete, "/vibes/mixtapes/1", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.DeleteMixtape(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestDeleteMixtape_InvalidID(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)

	req := httptest.NewRequest(http.MethodDelete, "/vibes/mixtapes/invalid", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "invalid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.DeleteMixtape(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeleteMixtape_NotFound(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)

	req := httptest.NewRequest(http.MethodDelete, "/vibes/mixtapes/99999", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "99999")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.DeleteMixtape(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeleteMixtape_Success(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u, "Test DJ")
	m := createVibesTestMixtape(t, client, u, dj, "To Delete")

	req := httptest.NewRequest(http.MethodDelete, "/vibes/mixtapes/"+strconv.Itoa(m.ID), nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(m.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.DeleteMixtape(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "mixtape-deleted", w.Header().Get("HX-Trigger"))

	// Verify mixtape was deleted
	_, err := client.Mixtape.Get(context.Background(), m.ID)
	assert.True(t, ent.IsNotFound(err))
}

func TestDeleteMixtape_OtherUserMixtape(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u1 := createVibesTestUser(t, client)
	u2 := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u1, "User1 DJ")
	m := createVibesTestMixtape(t, client, u1, dj, "User1 Mixtape")

	req := httptest.NewRequest(http.MethodDelete, "/vibes/mixtapes/"+strconv.Itoa(m.ID), nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(m.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	// User 2 tries to delete User 1's mixtape
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u2))

	h.DeleteMixtape(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	// Verify mixtape still exists
	_, err := client.Mixtape.Get(context.Background(), m.ID)
	assert.NoError(t, err)
}

// ToggleMixtapeSync Tests

func TestToggleMixtapeSync_Unauthorized(t *testing.T) {
	_, h, _ := setupVibesHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes/1/toggle-sync", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.ToggleMixtapeSync(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestToggleMixtapeSync_Enable(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u, "Test DJ")
	m := createVibesTestMixtape(t, client, u, dj, "Test Mixtape")

	// Ensure sync is disabled initially
	assert.False(t, m.SyncToNavidrome)

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes/"+strconv.Itoa(m.ID)+"/toggle-sync", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(m.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.ToggleMixtapeSync(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "mixtape-updated", w.Header().Get("HX-Trigger"))

	// Verify sync is now enabled
	updated, err := client.Mixtape.Get(context.Background(), m.ID)
	require.NoError(t, err)
	assert.True(t, updated.SyncToNavidrome)
}

func TestToggleMixtapeSync_Disable(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u, "Test DJ")

	// Create mixtape with sync enabled
	m, err := client.Mixtape.Create().
		SetName("Synced Mixtape").
		SetDj(dj).
		SetUser(u).
		SetSyncToNavidrome(true).
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes/"+strconv.Itoa(m.ID)+"/toggle-sync", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(m.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.ToggleMixtapeSync(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify sync is now disabled
	updated, err := client.Mixtape.Get(context.Background(), m.ID)
	require.NoError(t, err)
	assert.False(t, updated.SyncToNavidrome)
}

// GenerateMixtape Tests

func TestGenerateMixtape_Unauthorized(t *testing.T) {
	_, h, _ := setupVibesHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes/1/generate", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.GenerateMixtape(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGenerateMixtape_InvalidID(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes/invalid/generate", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "invalid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.GenerateMixtape(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGenerateMixtape_NotFound(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes/99999/generate", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "99999")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.GenerateMixtape(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGenerateMixtape_NoGenerator(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u, "Test DJ")
	m := createVibesTestMixtape(t, client, u, dj, "Test Mixtape")

	// Handler has nil MixtapeGenerator

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes/"+strconv.Itoa(m.ID)+"/generate", nil)
	w := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(m.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.GenerateMixtape(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// MixtapesIndex Tests

func TestMixtapesIndex_Unauthorized(t *testing.T) {
	_, h, _ := setupVibesHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/vibes/mixtapes", nil)
	w := httptest.NewRecorder()

	h.MixtapesIndex(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
}

func TestMixtapesIndex_EmptyList(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)

	req := httptest.NewRequest(http.MethodGet, "/vibes/mixtapes", nil)
	w := httptest.NewRecorder()

	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.MixtapesIndex(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "No mixtapes yet")
}

func TestMixtapesIndex_WithMixtapes(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	dj := createVibesTestDJ(t, client, u, "Test DJ")
	createVibesTestMixtape(t, client, u, dj, "Mixtape 1")
	createVibesTestMixtape(t, client, u, dj, "Mixtape 2")

	req := httptest.NewRequest(http.MethodGet, "/vibes/mixtapes", nil)
	w := httptest.NewRecorder()

	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))

	h.MixtapesIndex(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Mixtape 1")
	assert.Contains(t, body, "Mixtape 2")
}

func TestMixtapesIndex_OnlyShowsUserMixtapes(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u1 := createVibesTestUser(t, client)
	u2 := createVibesTestUser(t, client)
	dj1 := createVibesTestDJ(t, client, u1, "User1 DJ")
	dj2 := createVibesTestDJ(t, client, u2, "User2 DJ")
	createVibesTestMixtape(t, client, u1, dj1, "User1 Mixtape")
	createVibesTestMixtape(t, client, u2, dj2, "User2 Mixtape")

	req := httptest.NewRequest(http.MethodGet, "/vibes/mixtapes", nil)
	w := httptest.NewRecorder()

	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u1))

	h.MixtapesIndex(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "User1 Mixtape")
	assert.NotContains(t, body, "User2 Mixtape")
}
