package services

// Batch-starvation regression tests for issue #343.
//
// The enrichment queries use Limit(100) (tracks: 200) with Or-predicates such as
// LidarrIDIsNil that still match rows immediately after they are enriched. Without
// an explicit order, the same first-limit rows re-filled every batch and rows
// beyond the limit were never enriched. The queries now order by last_enriched_at
// (NULLs first) so successive ticks rotate through the entire library.
//
// Governing: SPEC metadata-enrichment-pipeline REQ-ENRICH-040 (enrich ALL
// un-enriched or stale entities), issue #343 (batch starvation)

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"spotter/ent/album"
	"spotter/ent/artist"
	"spotter/ent/enttest"
	"spotter/ent/track"
	"spotter/internal/config"
	"spotter/internal/enrichers"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStarvationTestService(t *testing.T) *MetadataService {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:starvation_%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { client.Close() })

	return &MetadataService{
		client:   client,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		config:   &config.Config{},
		registry: enrichers.NewRegistry(),
	}
}

// TestEnrichArtists_NoBatchStarvation creates 250 never-matchable artists
// (no Lidarr ID, so they keep matching the LidarrIDIsNil Or-predicate even
// after being enriched) and verifies that all of them receive enrichment
// across successive ticks with a batch limit of 100.
func TestEnrichArtists_NoBatchStarvation(t *testing.T) {
	svc := newStarvationTestService(t)
	ctx := context.Background()

	u, err := svc.client.User.Create().SetUsername("starvation-artists").Save(ctx)
	require.NoError(t, err)

	const total = 250
	for i := 0; i < total; i++ {
		_, err := svc.client.Artist.Create().
			SetName(fmt.Sprintf("Artist %03d", i)).
			SetUser(u).
			Save(ctx)
		require.NoError(t, err)
	}

	// 250 artists at Limit(100) per tick: 3 ticks must cover everyone.
	for tick := 1; tick <= 3; tick++ {
		_, err := svc.EnrichArtists(ctx, u)
		require.NoError(t, err, "tick %d", tick)
	}

	unenriched, err := svc.client.Artist.Query().
		Where(artist.LastEnrichedAtIsNil()).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, unenriched, "all %d artists must be enriched after 3 ticks (no starvation)", total)
}

// TestEnrichArtists_BatchesRotate verifies the rotation mechanics directly:
// the second tick must select the rows the first tick skipped, not re-select
// the just-enriched rows that still match the Or-predicates.
func TestEnrichArtists_BatchesRotate(t *testing.T) {
	svc := newStarvationTestService(t)
	ctx := context.Background()

	u, err := svc.client.User.Create().SetUsername("rotation-artists").Save(ctx)
	require.NoError(t, err)

	const total = 150
	for i := 0; i < total; i++ {
		_, err := svc.client.Artist.Create().
			SetName(fmt.Sprintf("Artist %03d", i)).
			SetUser(u).
			Save(ctx)
		require.NoError(t, err)
	}

	_, err = svc.EnrichArtists(ctx, u)
	require.NoError(t, err)

	afterFirst, err := svc.client.Artist.Query().
		Where(artist.LastEnrichedAtNotNil()).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 100, afterFirst, "first tick enriches one full batch")

	_, err = svc.EnrichArtists(ctx, u)
	require.NoError(t, err)

	afterSecond, err := svc.client.Artist.Query().
		Where(artist.LastEnrichedAtNotNil()).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, total, afterSecond,
		"second tick must pick up the remaining %d never-enriched artists instead of re-selecting the first batch", total-100)
}

// TestEnrichAlbums_NoBatchStarvation mirrors the artist test for the album
// query (Limit(100)).
func TestEnrichAlbums_NoBatchStarvation(t *testing.T) {
	svc := newStarvationTestService(t)
	ctx := context.Background()

	u, err := svc.client.User.Create().SetUsername("starvation-albums").Save(ctx)
	require.NoError(t, err)
	art, err := svc.client.Artist.Create().SetName("Album Artist").SetUser(u).Save(ctx)
	require.NoError(t, err)

	const total = 120
	for i := 0; i < total; i++ {
		_, err := svc.client.Album.Create().
			SetName(fmt.Sprintf("Album %03d", i)).
			SetUser(u).
			SetArtist(art).
			Save(ctx)
		require.NoError(t, err)
	}

	for tick := 1; tick <= 2; tick++ {
		_, err := svc.EnrichAlbums(ctx, u)
		require.NoError(t, err, "tick %d", tick)
	}

	unenriched, err := svc.client.Album.Query().
		Where(album.LastEnrichedAtIsNil()).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, unenriched, "all %d albums must be enriched after 2 ticks (no starvation)", total)
}

// TestEnrichTracks_NoBatchStarvation mirrors the artist test for the track
// query (Limit(200)).
func TestEnrichTracks_NoBatchStarvation(t *testing.T) {
	svc := newStarvationTestService(t)
	ctx := context.Background()

	u, err := svc.client.User.Create().SetUsername("starvation-tracks").Save(ctx)
	require.NoError(t, err)
	art, err := svc.client.Artist.Create().SetName("Track Artist").SetUser(u).Save(ctx)
	require.NoError(t, err)

	const total = 250
	for i := 0; i < total; i++ {
		_, err := svc.client.Track.Create().
			SetName(fmt.Sprintf("Track %03d", i)).
			SetArtist(art).
			Save(ctx)
		require.NoError(t, err)
	}

	for tick := 1; tick <= 2; tick++ {
		_, err := svc.EnrichTracks(ctx, u)
		require.NoError(t, err, "tick %d", tick)
	}

	unenriched, err := svc.client.Track.Query().
		Where(track.LastEnrichedAtIsNil()).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, unenriched, "all %d tracks must be enriched after 2 ticks (no starvation)", total)
}
