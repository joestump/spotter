package services_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"spotter/ent"
	"spotter/ent/playlisttrack"
	"spotter/ent/syncevent"
	"spotter/internal/config"
	"spotter/internal/events"
	"spotter/internal/providers"
	"spotter/internal/services"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statementCounter records every statement the client sends, so a test can assert
// what a sync path costs in statements and commits and not only what it stores.
// SQLite is the default backend and turns each autocommit into an fsync
// (ADR-0023), so the statement count is the property this story is about.
// Governing: ADR-0023
type statementCounter struct {
	mu      sync.Mutex
	logged  []string
	records bool
}

func (c *statementCounter) record(query string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.records {
		c.logged = append(c.logged, query)
	}
}

// start records statements from now on, so setup and schema creation stay out of
// the measurement.
func (c *statementCounter) start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logged = nil
	c.records = true
}

// stop ends the measurement and returns the SQL statements collected since start,
// leaving out the transaction bookkeeping.
func (c *statementCounter) stop() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = false

	statements := make([]string, 0, len(c.logged))
	for _, statement := range c.logged {
		trimmed := strings.ToLower(strings.TrimSpace(statement))
		if strings.HasPrefix(trimmed, "insert") || strings.HasPrefix(trimmed, "update") ||
			strings.HasPrefix(trimmed, "delete") || strings.HasPrefix(trimmed, "select") {
			statements = append(statements, statement)
		}
	}
	return statements
}

// statementRecordingDriver records every statement that passes through it,
// including the statements a transaction runs, and is otherwise the driver it
// wraps.
type statementRecordingDriver struct {
	dialect.Driver
	counter *statementCounter
}

func (d *statementRecordingDriver) Query(ctx context.Context, query string, args, v any) error {
	d.counter.record(query)
	return d.Driver.Query(ctx, query, args, v)
}

func (d *statementRecordingDriver) Exec(ctx context.Context, query string, args, v any) error {
	d.counter.record(query)
	return d.Driver.Exec(ctx, query, args, v)
}

func (d *statementRecordingDriver) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	d.counter.record(query)
	return d.Driver.(interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	}).QueryContext(ctx, query, args...)
}

func (d *statementRecordingDriver) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	d.counter.record(query)
	return d.Driver.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}).ExecContext(ctx, query, args...)
}

func (d *statementRecordingDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	tx, err := d.Driver.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return &statementRecordingTx{Tx: tx, counter: d.counter}, nil
}

func (d *statementRecordingDriver) BeginTx(ctx context.Context, opts *sql.TxOptions) (dialect.Tx, error) {
	tx, err := d.Driver.(interface {
		BeginTx(context.Context, *sql.TxOptions) (dialect.Tx, error)
	}).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &statementRecordingTx{Tx: tx, counter: d.counter}, nil
}

type statementRecordingTx struct {
	dialect.Tx
	counter *statementCounter
}

func (t *statementRecordingTx) Query(ctx context.Context, query string, args, v any) error {
	t.counter.record(query)
	return t.Tx.Query(ctx, query, args, v)
}

func (t *statementRecordingTx) Exec(ctx context.Context, query string, args, v any) error {
	t.counter.record(query)
	return t.Tx.Exec(ctx, query, args, v)
}

func (t *statementRecordingTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	t.counter.record(query)
	return t.Tx.(interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	}).QueryContext(ctx, query, args...)
}

func (t *statementRecordingTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	t.counter.record(query)
	return t.Tx.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}).ExecContext(ctx, query, args...)
}

func countWrites(statements []string, table string) int {
	writes := 0
	for _, statement := range statements {
		trimmed := strings.ToLower(strings.TrimSpace(statement))
		if strings.Contains(trimmed, table) &&
			(strings.HasPrefix(trimmed, "insert") || strings.HasPrefix(trimmed, "update") || strings.HasPrefix(trimmed, "delete")) {
			writes++
		}
	}
	return writes
}

func countStatementsOn(statements []string, verb, table string) int {
	matches := 0
	for _, statement := range statements {
		trimmed := strings.ToLower(strings.TrimSpace(statement))
		if strings.Contains(trimmed, table) && strings.HasPrefix(trimmed, verb) {
			matches++
		}
	}
	return matches
}

func setupCountingSyncer(t *testing.T) (*ent.Client, *services.Syncer, *statementCounter) {
	t.Helper()

	counter := &statementCounter{}
	drv, err := entsql.Open("sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)

	client := ent.NewClient(ent.Driver(&statementRecordingDriver{Driver: drv, counter: counter}))
	t.Cleanup(func() { client.Close() })
	require.NoError(t, client.Schema.Create(context.Background()))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus()
	return client, services.NewSyncer(client, &config.Config{}, logger, bus, nil), counter
}

func onePlaylist(tracks ...providers.Track) []providers.Playlist {
	return []providers.Playlist{
		{
			ID:         "playlist-1",
			Name:       "Batched Playlist",
			TrackCount: len(tracks),
			Tracks:     tracks,
		},
	}
}

func playlistOfLength(size int) []providers.Track {
	tracks := make([]providers.Track, 0, size)
	for i := range size {
		tracks = append(tracks, providers.Track{
			ID:         "track-" + strconv.Itoa(i),
			Name:       "Track " + strconv.Itoa(i),
			Artist:     "Artist " + strconv.Itoa(i%7),
			Album:      "Album " + strconv.Itoa(i%5),
			DurationMs: 180000 + i,
		})
	}
	return tracks
}

// Re-syncing a playlist nothing changed must write nothing at all, and its
// statement count must not grow with the playlist. The old path parked every row
// and re-wrote every row, one autocommit each, even when the provider reported
// exactly what was already stored.
// Governing: ADR-0023, SPEC listen-playlist-sync REQ-SYNC-031
func TestSyncer_UnchangedPlaylistResyncWritesNothingAtAnySize(t *testing.T) {
	for _, size := range []int{8, 32} {
		t.Run("tracks="+strconv.Itoa(size), func(t *testing.T) {
			client, syncer, counter := setupCountingSyncer(t)
			ctx := context.Background()

			user := createTestUser(t, client)
			connectSpotify(t, client, user)

			mock := &mockProvider{
				providerType: providers.TypeSpotify,
				playlists:    onePlaylist(playlistOfLength(size)...),
			}
			syncer.Register(mockFactory(mock))

			require.NoError(t, syncer.Sync(ctx, user), "import must succeed")

			counter.start()
			require.NoError(t, syncer.Sync(ctx, user), "unchanged re-sync must succeed")
			statements := counter.stop()
			require.NotEmpty(t, statements, "the counter must see the re-sync's statements")

			assert.Equal(t, 0, countWrites(statements, "playlist_tracks"),
				"%d-track unchanged re-sync must write no playlist tracks", size)
			assert.Less(t, len(statements), 40,
				"%d-track unchanged re-sync must not pay statements per track", size)

			rows, err := playlistTrackRows(ctx, client)
			require.NoError(t, err)
			require.Len(t, rows, size, "every row survives an unchanged re-sync")
			for i, row := range rows {
				assert.Equal(t, i, row.Position, "positions stay in playlist order")
			}

			parked, err := client.PlaylistTrack.Query().Where(playlisttrack.PositionLT(0)).Count(ctx)
			require.NoError(t, err)
			assert.Equal(t, 0, parked, "no row is left parked at a negative position")
		})
	}
}

// A page of listens is pre-fetched and written per batch instead of per track:
// the de-duplication answers come from a handful of queries, and the listens that
// survive go in as bulk inserts inside one transaction. A 10k-listen import used
// to cost 40-50k round trips; a batched page must cost a flat handful instead.
// Governing: ADR-0023, SPEC listen-playlist-sync REQ-SYNC-021
func TestSyncer_ListenPageIsBatchedNotWrittenPerTrack(t *testing.T) {
	client, syncer, counter := setupCountingSyncer(t)
	ctx := context.Background()

	user := createTestUser(t, client)
	connectSpotify(t, client, user)

	playedAt := time.Now().Add(-time.Hour)
	tracks := make([]providers.Track, 0, 600)
	for i := range 600 {
		tracks = append(tracks, providers.Track{
			ID:       "listen-" + strconv.Itoa(i),
			Name:     "Batched Track " + strconv.Itoa(i),
			Artist:   "Batched Artist",
			Album:    "Batched Album",
			PlayedAt: playedAt.Add(time.Duration(i) * time.Second),
		})
	}

	mock := &mockProvider{providerType: providers.TypeSpotify, tracks: tracks}
	syncer.Register(mockFactory(mock))

	counter.start()
	require.NoError(t, syncer.Sync(ctx, user))
	statements := counter.stop()
	require.NotEmpty(t, statements, "the counter must see the sync's statements")

	saved, err := client.Listen.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 600, saved, "every listen on the page is stored")

	assert.LessOrEqual(t, countStatementsOn(statements, "insert into `listens`", "listens"), 6,
		"listens go in per batch, not one insert per row")
	assert.Less(t, len(statements), 120,
		"a 600-listen page costs statements per batch, not per listen")

	// A repeat of the same page stores nothing twice: the pre-fetched de-dup
	// answers still recognise every listen on it.
	counter.start()
	require.NoError(t, syncer.Sync(ctx, user))
	statements = counter.stop()

	saved, err = client.Listen.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 600, saved, "a repeat sync adds no listens")
	assert.Equal(t, 0, countStatementsOn(statements, "insert into `listens`", "listens"),
		"a repeat sync inserts no listens")
}

// A batch that cannot be written leaves nothing behind: the rows it planned are
// rolled back whole, instead of a sync storing part of a page and dropping the
// rest.
// Governing: ADR-0023, SPEC graceful-shutdown REQ-REC-001
func TestSyncer_ListenBatchFailurePersistsNothing(t *testing.T) {
	client, syncer, _ := setupCountingSyncer(t)
	ctx := context.Background()

	user := createTestUser(t, client)
	connectSpotify(t, client, user)

	playedAt := time.Now().Add(-time.Hour)
	tracks := []providers.Track{
		{ID: "ok-1", Name: "First Track", Artist: "Artist A", PlayedAt: playedAt},
		{ID: "ok-2", Name: "Second Track", Artist: "Artist B", PlayedAt: playedAt.Add(time.Minute)},
		{ID: "ok-3", Name: "Third Track", Artist: "Artist C", PlayedAt: playedAt.Add(2 * time.Minute)},
		// provider_track_id is capped at 2048 characters, so this row is rejected
		// while the batch is being written.
		{ID: strings.Repeat("x", 3000), Name: "Fourth Track", Artist: "Artist D", PlayedAt: playedAt.Add(3 * time.Minute)},
	}

	mock := &mockProvider{providerType: providers.TypeSpotify, tracks: tracks}
	syncer.Register(mockFactory(mock))

	require.Error(t, syncer.Sync(ctx, user), "a rejected row must surface, not be swallowed")

	saved, err := client.Listen.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, saved, "a failed batch must not leave its good rows behind")

	logged, err := client.SyncEvent.Query().
		Where(syncevent.EventTypeEQ(syncevent.EventTypeTrackAdded)).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, logged, "a rolled back batch must not log listens it did not store")
}

// A changed playlist reconciles exactly as it did before: a kept row keeps its id,
// additions land at their index, removals go, a re-order moves every row without
// colliding on the unique position index, and no row is left parked.
// Governing: SPEC listen-playlist-sync REQ-SYNC-031, REQ-SYNC-032
func TestSyncer_ChangedPlaylistReconciliationIsUnchanged(t *testing.T) {
	client, syncer, _ := setupCountingSyncer(t)
	ctx := context.Background()

	user := createTestUser(t, client)
	connectSpotify(t, client, user)

	first := providers.Track{ID: "track-1", Name: "First Track", Artist: "Artist A", Album: "Album A", DurationMs: 100000}
	second := providers.Track{ID: "track-2", Name: "Second Track", Artist: "Artist B", Album: "Album B", DurationMs: 200000}
	third := providers.Track{ID: "track-3", Name: "Third Track", Artist: "Artist C", Album: "Album C", DurationMs: 300000}

	mock := &mockProvider{providerType: providers.TypeSpotify, playlists: onePlaylist(first, second, third)}
	syncer.Register(mockFactory(mock))
	require.NoError(t, syncer.Sync(ctx, user))

	imported, err := playlistTrackRows(ctx, client)
	require.NoError(t, err)
	require.Len(t, imported, 3)

	relabeled := providers.Track{ID: "track-1", Name: "First Track Renamed", Artist: "Artist A", Album: "Album A2", DurationMs: 111000}
	added := providers.Track{ID: "track-4", Name: "Fourth Track", Artist: "Artist D", Album: "Album D", DurationMs: 400000}

	// third moves from position 2 to 0, first is edited, second is removed, and a
	// new track takes the last position.
	mock.playlists = onePlaylist(third, relabeled, added)
	require.NoError(t, syncer.Sync(ctx, user))

	rows, err := playlistTrackRows(ctx, client)
	require.NoError(t, err)
	require.Len(t, rows, 3, "the removed track is gone and the added one is here")
	assert.Equal(t, []int{0, 1, 2}, []int{rows[0].Position, rows[1].Position, rows[2].Position})
	assert.Equal(t, []string{"Third Track", "First Track Renamed", "Fourth Track"},
		[]string{rows[0].TrackName, rows[1].TrackName, rows[2].TrackName})
	assert.Equal(t, imported[2].ID, rows[0].ID, "a kept row keeps its own row, and its catalog links")
	require.NotNil(t, rows[1].DurationMs)
	assert.Equal(t, 111000, *rows[1].DurationMs, "the edited row carries the new duration")

	parked, err := client.PlaylistTrack.Query().Where(playlisttrack.PositionLT(0)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, parked, "the bulk parking step leaves no row parked")

	// The same list again is a no-op, and it stays a no-op.
	before := rows
	mock.playlists = onePlaylist(third, relabeled, added)
	require.NoError(t, syncer.Sync(ctx, user))

	rows, err = playlistTrackRows(ctx, client)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	assert.Equal(t, []int{0, 1, 2}, []int{rows[0].Position, rows[1].Position, rows[2].Position},
		"an unchanged re-sync leaves positions alone")
	assert.Equal(t, []int{before[0].ID, before[1].ID, before[2].ID},
		[]int{rows[0].ID, rows[1].ID, rows[2].ID}, "an unchanged re-sync rewrites no row")
}
