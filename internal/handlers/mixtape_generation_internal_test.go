// Governing: issue #340 (one form parser and one generation path for the album,
// artist and vibes entry points), issue #339 (generation failures are recorded
// on the mixtape)
package handlers

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
	"time"

	"spotter/ent"
	"spotter/ent/enttest"
	"spotter/internal/config"
	"spotter/internal/events"
	"spotter/internal/vibes"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMixtapeFormHandler(t *testing.T) (*ent.Client, *Handler, *ent.User) {
	t.Helper()

	dbName := "file:" + t.Name() + "?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dbName)
	t.Cleanup(func() { client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New(client, &config.Config{}, logger, nil, nil, nil, nil, nil, nil, nil, nil, events.NewBus(), nil)

	u, err := client.User.Create().
		SetUsername("mixtape-form-user").
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	return client, h, u
}

func formRequest(t *testing.T, values url.Values) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/vibes/mixtapes", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	require.NoError(t, req.ParseForm())
	return req
}

func TestParseMixtapeForm_RejectsMissingDJ(t *testing.T) {
	_, h, u := setupMixtapeFormHandler(t)

	w := httptest.NewRecorder()
	form, ok := h.parseMixtapeForm(w, formRequest(t, url.Values{"name": {"Mix"}}), u.ID)

	assert.False(t, ok)
	assert.Nil(t, form)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "DJ is required")
}

func TestParseMixtapeForm_RejectsSomeoneElsesDJ(t *testing.T) {
	client, h, u := setupMixtapeFormHandler(t)

	other, err := client.User.Create().
		SetUsername("mixtape-form-other").
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	theirDJ, err := client.DJ.Create().SetName("Their DJ").SetUser(other).Save(context.Background())
	require.NoError(t, err)

	w := httptest.NewRecorder()
	form, ok := h.parseMixtapeForm(w, formRequest(t, url.Values{"dj_id": {strconv.Itoa(theirDJ.ID)}}), u.ID)

	assert.False(t, ok)
	assert.Nil(t, form)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestParseMixtapeForm_MaxTracksAndName(t *testing.T) {
	client, h, u := setupMixtapeFormHandler(t)

	d, err := client.DJ.Create().SetName("DJ Shared").SetUser(u).Save(context.Background())
	require.NoError(t, err)

	cases := []struct {
		name          string
		maxTracks     string
		formName      string
		wantMaxTracks int
		wantName      string
	}{
		{name: "default when absent", maxTracks: "", formName: "Mix", wantMaxTracks: defaultMixtapeMaxTracks, wantName: "Mix"},
		{name: "accepted within bounds", maxTracks: "30", formName: "Mix", wantMaxTracks: 30, wantName: "Mix"},
		{name: "zero falls back to default", maxTracks: "0", formName: "Mix", wantMaxTracks: defaultMixtapeMaxTracks, wantName: "Mix"},
		{name: "above the cap falls back", maxTracks: "101", formName: "Mix", wantMaxTracks: defaultMixtapeMaxTracks, wantName: "Mix"},
		{name: "garbage falls back", maxTracks: "many", formName: "Mix", wantMaxTracks: defaultMixtapeMaxTracks, wantName: "Mix"},
		{name: "name is trimmed", maxTracks: "25", formName: "   Late night   ", wantMaxTracks: 25, wantName: "Late night"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			form, ok := h.parseMixtapeForm(w, formRequest(t, url.Values{
				"dj_id":      {strconv.Itoa(d.ID)},
				"max_tracks": {c.maxTracks},
				"name":       {c.formName},
			}), u.ID)

			require.True(t, ok, w.Body.String())
			assert.Equal(t, http.StatusOK, w.Code)
			require.NotNil(t, form.dj)
			assert.Equal(t, d.ID, form.dj.ID)
			assert.Equal(t, c.wantMaxTracks, form.maxTracks)
			assert.Equal(t, c.wantName, form.name)
		})
	}
}

func TestStartMixtapeGeneration_WithoutGeneratorLeavesMixtapeAlone(t *testing.T) {
	client, h, u := setupMixtapeFormHandler(t)
	ctx := context.Background()

	d, err := client.DJ.Create().SetName("DJ Idle").SetUser(u).Save(ctx)
	require.NoError(t, err)

	m, err := client.Mixtape.Create().
		SetName("Unconfigured").
		SetMaxTracks(10).
		SetSeedType("artist").
		SetDj(d).
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	h.startMixtapeGeneration(u.ID, m, d, nil)

	stored, err := client.Mixtape.Get(ctx, m.ID)
	require.NoError(t, err)
	assert.Empty(t, stored.GenerationError)
	assert.Equal(t, 0, stored.TrackCount)
}

func TestStartMixtapeGeneration_RecordsFailureOnTheMixtape(t *testing.T) {
	client, h, u := setupMixtapeFormHandler(t)
	ctx := context.Background()

	d, err := client.DJ.Create().SetName("DJ Failing").SetUser(u).Save(ctx)
	require.NoError(t, err)

	al, err := client.Album.Create().SetName("Shared Album").SetUser(u).Save(ctx)
	require.NoError(t, err)

	m, err := client.Mixtape.Create().
		SetName("Failing Mix").
		SetMaxTracks(10).
		SetSeedType("album").
		SetSeedID(al.ID).
		SetDj(d).
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	// No OpenAI key configured: generation fails, and the shared path must
	// record the failure the same way whichever page started it.
	cfg := &config.Config{}
	h.MixtapeGenerator = vibes.NewMixtapeGenerator(client, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	h.startMixtapeGeneration(u.ID, m, d, vibes.NewAlbumSeed(al))

	var stored *ent.Mixtape
	require.Eventually(t, func() bool {
		stored, err = client.Mixtape.Get(ctx, m.ID)
		return err == nil && stored.GenerationError != ""
	}, 5*time.Second, 20*time.Millisecond, "generation failure was never recorded on the mixtape")

	assert.Contains(t, stored.GenerationError, "not configured")
	assert.Equal(t, 0, stored.TrackCount)
}
