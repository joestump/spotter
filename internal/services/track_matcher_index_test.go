package services_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"spotter/ent"
	"spotter/internal/providers"
	"spotter/internal/services"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingDriver wraps an Ent dialect.Driver and counts read queries so tests
// can prove how many times the library is loaded from the database.
type countingDriver struct {
	dialect.Driver
	queries atomic.Int64
}

func (d *countingDriver) Query(ctx context.Context, query string, args, v any) error {
	d.queries.Add(1)
	return d.Driver.Query(ctx, query, args, v)
}

func setupCountingMatcher(t *testing.T, minConfidence float64) (*ent.Client, *countingDriver, *services.TrackMatcher, *ent.User) {
	t.Helper()

	drv, err := entsql.Open("sqlite3",
		fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	require.NoError(t, err)

	counting := &countingDriver{Driver: drv}
	client := ent.NewClient(ent.Driver(counting))
	t.Cleanup(func() { client.Close() })

	require.NoError(t, client.Schema.Create(context.Background()))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	matcher := services.NewTrackMatcher(client, logger, minConfidence)

	user, err := client.User.Create().
		SetUsername("indexuser").
		Save(context.Background())
	require.NoError(t, err)

	return client, counting, matcher, user
}

// Issue #330: per-tick matching cost must not scale with playlist count x
// library size. A shared LibraryIndex is loaded with exactly ONE library
// query and reused across an arbitrary number of MatchTracksWithIndex calls,
// whereas legacy MatchTracks re-queries the library on every call.
func TestLibraryIndex_LoadedOncePerTick(t *testing.T) {
	client, counting, matcher, user := setupCountingMatcher(t, 0.7)
	ctx := context.Background()

	// Seed a small library.
	for i := 0; i < 10; i++ {
		createTestTrackWithNavidromeID(t, client, user,
			fmt.Sprintf("Library Song %d", i),
			fmt.Sprintf("Library Artist %d", i),
			fmt.Sprintf("nav-%d", i))
	}

	sourceTracks := []providers.Track{
		{ID: "s-1", Name: "Library Song 1", Artist: "Library Artist 1"},
		{ID: "s-2", Name: "Library Song 2 (Remastered)", Artist: "Library Artist 2"},
		{ID: "s-3", Name: "Nonexistent Song", Artist: "Nobody"},
	}

	const playlists = 5

	// Measure the cost of a single library load (the track query plus its
	// eager-loaded artist edge — 2 SQL queries with the current schema).
	counting.queries.Store(0)
	idx, err := matcher.LoadLibraryIndex(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, 10, idx.Size())
	loadCost := counting.queries.Load()
	require.Positive(t, loadCost)

	// Shared-index path: matching N playlists issues ZERO additional queries.
	for i := 0; i < playlists; i++ {
		results := matcher.MatchTracksWithIndex(idx, sourceTracks)
		require.Len(t, results, 3)
		assert.Equal(t, "nav-1", results[0].NavidromeTrackID)
		assert.Equal(t, "nav-2", results[1].NavidromeTrackID)
		assert.Empty(t, results[2].NavidromeTrackID)
	}
	sharedQueries := counting.queries.Load()
	assert.Equal(t, loadCost, sharedQueries,
		"shared index should load the library exactly once per tick, with no per-playlist queries")

	// Legacy path: the library is reloaded for every playlist.
	counting.queries.Store(0)
	for i := 0; i < playlists; i++ {
		_, err := matcher.MatchTracks(ctx, user.ID, sourceTracks)
		require.NoError(t, err)
	}
	legacyQueries := counting.queries.Load()
	assert.Equal(t, playlists*loadCost, legacyQueries,
		"MatchTracks reloads the library on every call")

	assert.Less(t, sharedQueries, legacyQueries,
		"per-tick query count must not scale with playlist count")
}

// Issue #330 regression: with the old byte-based max length, a dissimilar CJK
// title by the same artist scored ~0.8 (0.6*0.67 + 0.4*1.0) and wrongly
// cleared the 0.7 threshold. With rune-based similarity it scores 0.4 and is
// reported unmatched.
func TestTrackMatcher_CJK_DissimilarTitlesDoNotMatch(t *testing.T) {
	client, _, matcher, user := setupCountingMatcher(t, 0.7)
	ctx := context.Background()

	createTestTrackWithNavidromeID(t, client, user, "夜に駆ける", "YOASOBI", "nav-yoasobi-1")

	sourceTracks := []providers.Track{
		{ID: "s-1", Name: "群青", Artist: "YOASOBI"},
	}

	results, err := matcher.MatchTracks(ctx, user.ID, sourceTracks)
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Empty(t, results[0].NavidromeTrackID,
		"dissimilar CJK title must not fuzzy-match, got confidence %f", results[0].MatchConfidence)
	assert.Equal(t, services.MatchMethodNone, results[0].MatchMethod)
}

func TestTrackMatcher_Cyrillic_DissimilarTitlesDoNotMatch(t *testing.T) {
	client, _, matcher, user := setupCountingMatcher(t, 0.7)
	ctx := context.Background()

	createTestTrackWithNavidromeID(t, client, user, "Калинка", "Русский Хор", "nav-ru-1")

	sourceTracks := []providers.Track{
		{ID: "s-1", Name: "Катюша", Artist: "Русский Хор"},
	}

	results, err := matcher.MatchTracks(ctx, user.ID, sourceTracks)
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Empty(t, results[0].NavidromeTrackID,
		"dissimilar Cyrillic title must not fuzzy-match, got confidence %f", results[0].MatchConfidence)
	assert.Equal(t, services.MatchMethodNone, results[0].MatchMethod)
}

// Identical non-ASCII titles must still match exactly.
func TestTrackMatcher_CJK_IdenticalTitlesMatch(t *testing.T) {
	client, _, matcher, user := setupCountingMatcher(t, 0.7)
	ctx := context.Background()

	createTestTrackWithNavidromeID(t, client, user, "夜に駆ける", "YOASOBI", "nav-yoasobi-1")

	sourceTracks := []providers.Track{
		{ID: "s-1", Name: "夜に駆ける", Artist: "YOASOBI"},
	}

	results, err := matcher.MatchTracks(ctx, user.ID, sourceTracks)
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, "nav-yoasobi-1", results[0].NavidromeTrackID)
	assert.Equal(t, 1.0, results[0].MatchConfidence)
}

// BenchmarkMatchTracksSharedIndex measures matching with a precomputed
// LibraryIndex (normalization done once per candidate, no per-call DB query).
func BenchmarkMatchTracksSharedIndex(b *testing.B) {
	client, matcher, user, sourceTracks := setupBenchmarkLibrary(b)
	defer client.Close()
	ctx := context.Background()

	idx, err := matcher.LoadLibraryIndex(ctx, user.ID)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		matcher.MatchTracksWithIndex(idx, sourceTracks)
	}
}

// BenchmarkMatchTracksLegacy measures the legacy per-playlist path that
// reloads the library and re-normalizes every candidate on every call.
func BenchmarkMatchTracksLegacy(b *testing.B) {
	client, matcher, user, sourceTracks := setupBenchmarkLibrary(b)
	defer client.Close()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := matcher.MatchTracks(ctx, user.ID, sourceTracks); err != nil {
			b.Fatal(err)
		}
	}
}

func setupBenchmarkLibrary(b *testing.B) (*ent.Client, *services.TrackMatcher, *ent.User, []providers.Track) {
	b.Helper()

	drv, err := entsql.Open("sqlite3",
		fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", b.Name()))
	if err != nil {
		b.Fatal(err)
	}
	client := ent.NewClient(ent.Driver(drv))
	ctx := context.Background()
	if err := client.Schema.Create(ctx); err != nil {
		b.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	matcher := services.NewTrackMatcher(client, logger, 0.7)

	user, err := client.User.Create().SetUsername("benchuser").Save(ctx)
	if err != nil {
		b.Fatal(err)
	}

	const librarySize = 500
	for i := 0; i < librarySize; i++ {
		artistEnt, err := client.Artist.Create().
			SetName(fmt.Sprintf("Bench Artist %d (Deluxe Edition)", i)).
			SetUser(user).
			Save(ctx)
		if err != nil {
			b.Fatal(err)
		}
		navID := fmt.Sprintf("nav-bench-%d", i)
		if _, err := client.Track.Create().
			SetName(fmt.Sprintf("Bench Song %d (Remastered)", i)).
			SetArtist(artistEnt).
			SetNillableNavidromeID(&navID).
			Save(ctx); err != nil {
			b.Fatal(err)
		}
	}

	// Source tracks that miss the exact map and exercise the fuzzy path.
	sourceTracks := make([]providers.Track, 25)
	for i := range sourceTracks {
		sourceTracks[i] = providers.Track{
			ID:     fmt.Sprintf("bench-src-%d", i),
			Name:   fmt.Sprintf("Bench Songg %d", i),
			Artist: fmt.Sprintf("Bench Artistt %d", i),
		}
	}

	return client, matcher, user, sourceTracks
}
