// Regression tests for issue #339: deleting a DJ that still owns mixtapes must
// be refused with a message, and an unknown schedule must answer 400.

package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"spotter/ent"
	"spotter/internal/events"
	"spotter/internal/handlers"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func deleteDJRequest(t *testing.T, u *ent.User, djID int) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/vibes/djs/"+strconv.Itoa(djID), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(djID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))
}

func TestDeleteDJ_WithMixtapesIsRefused(t *testing.T) {
	client, h, bus := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	d := createVibesTestDJ(t, client, u, "Busy DJ")
	m := createVibesTestMixtape(t, client, u, d, "Owned Mixtape")

	eventsCh, cleanup := bus.Subscribe(u.ID)
	t.Cleanup(cleanup)

	w := httptest.NewRecorder()
	h.DeleteDJ(w, deleteDJRequest(t, u, d.ID))

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "mixtape")

	_, err := client.DJ.Get(context.Background(), d.ID)
	require.NoError(t, err, "the DJ must still exist")
	_, err = client.Mixtape.Get(context.Background(), m.ID)
	require.NoError(t, err, "its mixtape must still exist")

	select {
	case event := <-eventsCh:
		assert.Equal(t, events.EventTypeNotification, event.Type)
		payload, ok := event.Payload.(events.NotificationPayload)
		require.True(t, ok, "expected a notification payload")
		assert.Equal(t, "error", payload.IconType)
	case <-time.After(time.Second):
		t.Fatal("no notification published for the refused delete")
	}
}

func TestDeleteDJ_WithoutMixtapesSucceeds(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	d := createVibesTestDJ(t, client, u, "Idle DJ")

	w := httptest.NewRecorder()
	h.DeleteDJ(w, deleteDJRequest(t, u, d.ID))

	assert.Equal(t, http.StatusOK, w.Code)

	_, err := client.DJ.Get(context.Background(), d.ID)
	require.Error(t, err, "the DJ must be gone")
}

func TestCreateMixtape_InvalidScheduleIsRejected(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	d := createVibesTestDJ(t, client, u, "Schedule DJ")

	form := url.Values{}
	form.Set("name", "Scheduled Mixtape")
	form.Set("dj_id", strconv.Itoa(d.ID))
	form.Set("schedule", "every-other-tuesday")

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))
	w := httptest.NewRecorder()

	h.CreateMixtape(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "schedule")

	count, err := client.Mixtape.Query().Count(context.Background())
	require.NoError(t, err)
	assert.Zero(t, count)
}

func TestUpdateMixtape_InvalidScheduleIsRejected(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	d := createVibesTestDJ(t, client, u, "Schedule DJ")
	m := createVibesTestMixtape(t, client, u, d, "Scheduled Mixtape")

	form := url.Values{}
	form.Set("name", m.Name)
	form.Set("dj_id", strconv.Itoa(d.ID))
	form.Set("schedule", "hourly")

	req := httptest.NewRequest(http.MethodPut, "/vibes/mixtapes/"+strconv.Itoa(m.ID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.Itoa(m.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))
	w := httptest.NewRecorder()

	h.UpdateMixtape(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	stored, err := client.Mixtape.Get(context.Background(), m.ID)
	require.NoError(t, err)
	assert.NotEqual(t, "hourly", string(stored.Schedule))
}

func TestCreateMixtape_ValidScheduleIsAccepted(t *testing.T) {
	client, h, _ := setupVibesHandler(t)
	u := createVibesTestUser(t, client)
	d := createVibesTestDJ(t, client, u, "Schedule DJ")

	form := url.Values{}
	form.Set("name", "Weekly Mixtape")
	form.Set("dj_id", strconv.Itoa(d.ID))
	form.Set("schedule", "weekly")

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, u))
	w := httptest.NewRecorder()

	h.CreateMixtape(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	stored, err := client.Mixtape.Query().Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "weekly", string(stored.Schedule))
}
