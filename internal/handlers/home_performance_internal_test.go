// Governing: issue #344 (home counts come from aggregates; the render is not
// blocked by Navidrome reachability), ADR-0004 (Ent ORM), ADR-0018 (jobs)
package handlers

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"spotter/ent"
	"spotter/ent/playlist"
	"spotter/ent/playlisttrack"
	"spotter/ent/user"
	"spotter/internal/config"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingReadDriver counts database reads and records their SQL, so a test can
// prove the home page stopped walking the user's history.
type countingReadDriver struct {
	dialect.Driver
	reads atomic.Int64
	mu    sync.Mutex
	sql   []string
}

func (d *countingReadDriver) Query(ctx context.Context, query string, args, v any) error {
	d.reads.Add(1)
	d.mu.Lock()
	d.sql = append(d.sql, query)
	d.mu.Unlock()
	return d.Driver.Query(ctx, query, args, v)
}

// listenQueries returns every recorded read that touches the listens table.
func (d *countingReadDriver) listenQueries() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	var out []string
	for _, q := range d.sql {
		if strings.Contains(q, "`listens`") {
			out = append(out, q)
		}
	}
	return out
}

func setupHomeStats(t *testing.T) (*ent.Client, *countingReadDriver, *Handler, *ent.User) {
	t.Helper()

	drv, err := entsql.Open("sqlite3",
		fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	require.NoError(t, err)

	counting := &countingReadDriver{Driver: drv}
	client := ent.NewClient(ent.Driver(counting))
	t.Cleanup(func() { client.Close() })

	require.NoError(t, client.Schema.Create(context.Background()))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New(client, &config.Config{}, logger, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	u, err := client.User.Create().
		SetUsername(t.Name()).
		SetPaginationSize(25).
		Save(context.Background())
	require.NoError(t, err)

	return client, counting, h, u
}

func seedHomeHistory(t *testing.T, client *ent.Client, u *ent.User, source string, repeats int) {
	t.Helper()

	ctx := context.Background()
	triples := []struct{ artist, album, track string }{
		{"Big Thief", "Dragon New Warm Mountain", "Ready To Roll"},
		{"Big Thief", "U.F.O.F.", "Too Late"},
		{"Fontaines D.C.", "Skinty Fia", "I Love You"},
	}

	for _, artist := range []string{"Big Thief", "Fontaines D.C."} {
		_, err := client.Artist.Create().SetName(artist).SetUser(u).Save(ctx)
		require.NoError(t, err)
	}

	for i := 0; i < repeats; i++ {
		for _, spec := range triples {
			_, err := client.Listen.Create().
				SetUser(u).
				SetArtistName(spec.artist).
				SetAlbumName(spec.album).
				SetTrackName(spec.track).
				SetSource(source).
				SetPlayedAt(time.Now().Add(-time.Duration(i) * time.Hour)).
				Save(ctx)
			require.NoError(t, err)
		}
	}

	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("pl-" + source).
		SetName("Counts").
		SetSource(source).
		SetTrackCount(1).
		Save(ctx)
	require.NoError(t, err)

	// A playlist-only album and track widen the unique counts without adding a
	// listen, which is why playlist names still feed the sets.
	_, err = client.PlaylistTrack.Create().
		SetPlaylist(pl).
		SetPosition(0).
		SetArtistName("Fontaines D.C.").
		SetAlbumName("Dogrel").
		SetTrackName("Big").
		SetRemoteID("pt-0").
		Save(ctx)
	require.NoError(t, err)
}

// The home page used to read every listen row to count artists, albums and
// tracks. The same numbers must come out of aggregates, at the same cost no
// matter how much history a user has.
func TestGetHomeStats_CountsAreAggregateAndHistoryIndependent(t *testing.T) {
	reads := map[string]int64{}

	for _, tc := range []struct {
		name    string
		source  string
		repeats int
	}{
		{name: "light", source: "spotify", repeats: 2},
		{name: "heavy", source: "lastfm", repeats: 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, counting, h, u := setupHomeStats(t)
			seedHomeHistory(t, client, u, tc.source, tc.repeats)

			counting.reads.Store(0)
			counting.sql = nil
			stats, err := h.getHomeStats(context.Background(), u)
			require.NoError(t, err)
			reads[tc.name] = counting.reads.Load()

			// Acceptance: the render must not materialize listen rows. Every read
			// that touches listens has to be a COUNT or a GROUP BY.
			for _, q := range counting.listenQueries() {
				assert.True(t,
					strings.Contains(q, "COUNT(") || strings.Contains(q, "GROUP BY"),
					"the home render must aggregate listens in the database, got: %s", q)
			}
			assert.NotEmpty(t, counting.listenQueries())

			assert.Equal(t, 3*tc.repeats, stats.TotalListens)
			assert.Equal(t, 2, stats.UniqueArtists)
			assert.Equal(t, 4, stats.UniqueAlbums)
			assert.Equal(t, 4, stats.UniqueTracks)
			assert.Len(t, stats.TopArtists, 2)
			assert.Equal(t, "Big Thief", stats.TopArtists[0].Artist.Name)
			assert.Equal(t, 2*tc.repeats, stats.TopArtists[0].Listens)
		})
	}

	assert.Equal(t, reads["light"], reads["heavy"],
		"a render over 120 listens must not read more than one over 6")
}

// Top artists must stay sorted by listen count even though the counts now come
// straight from a grouped query.
func TestGetHomeStats_TopArtistsSortedByListenCount(t *testing.T) {
	client, _, h, u := setupHomeStats(t)
	ctx := context.Background()

	counts := map[string]int{"Frequent": 5, "Occasional": 2, "Rare": 1}
	for artist, count := range counts {
		_, err := client.Artist.Create().SetName(artist).SetUser(u).Save(ctx)
		require.NoError(t, err)
		for i := 0; i < count; i++ {
			_, err := client.Listen.Create().
				SetUser(u).
				SetArtistName(artist).
				SetAlbumName("Album").
				SetTrackName("Track " + artist).
				SetSource("navidrome").
				SetPlayedAt(time.Now()).
				Save(ctx)
			require.NoError(t, err)
		}
	}

	stats, err := h.getHomeStats(ctx, u)
	require.NoError(t, err)
	require.Len(t, stats.TopArtists, 3)
	assert.Equal(t, "Frequent", stats.TopArtists[0].Artist.Name)
	assert.Equal(t, 5, stats.TopArtists[0].Listens)
	assert.Equal(t, "Occasional", stats.TopArtists[1].Artist.Name)
	assert.Equal(t, "Rare", stats.TopArtists[2].Artist.Name)
}

func TestNavidromeOnline_ServesLastKnownResultWithoutWaiting(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{Logger: logger, Config: &config.Config{}}

	probeCalls := atomic.Int64{}
	unblock := make(chan struct{})
	t.Cleanup(func() { close(unblock) })
	probeStarted := make(chan struct{}, 1)

	h.navidromeHealth.probe = func(ctx context.Context, username, password string) bool {
		probeCalls.Add(1)
		select {
		case probeStarted <- struct{}{}:
		default:
		}
		select {
		case <-unblock:
		case <-time.After(10 * time.Second):
		}
		return true
	}
	h.navidromeHealth.start = func(fn func(context.Context)) bool {
		go fn(context.Background())
		return true
	}
	h.navidromeHealth.ttl = time.Minute
	h.navidromeHealth.now = time.Now

	started := time.Now()
	online := h.navidromeOnline(1, "joe", "secret")
	assert.False(t, online, "a user who has never been probed reads offline instead of waiting")

	select {
	case <-probeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("the render never handed the probe to the background")
	}
	assert.Less(t, time.Since(started), 2*time.Second,
		"the render must not wait for a Navidrome server that is not answering")

	// The probe is still blocked when the render returned, so the second render
	// reports the same cached value and does not start a second probe.
	assert.Equal(t, int64(1), probeCalls.Load())
	assert.False(t, h.navidromeOnline(1, "joe", "secret"))
}

func TestNavidromeOnline_CachesWithinTTLAndRefreshesAfterIt(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{Logger: logger, Config: &config.Config{}}

	now := time.Now()
	probeCalls := 0
	serverOnline := false
	h.navidromeHealth.probe = func(ctx context.Context, username, password string) bool {
		probeCalls++
		return serverOnline
	}
	h.navidromeHealth.start = func(fn func(context.Context)) bool {
		fn(context.Background())
		return true
	}
	h.navidromeHealth.ttl = time.Minute
	h.navidromeHealth.now = func() time.Time { return now }

	// First render starts the probe and still reports what was known: offline.
	assert.False(t, h.navidromeOnline(7, "joe", "secret"))
	assert.Equal(t, 1, probeCalls)

	// Within the TTL the cached answer is served with no further probe.
	now = now.Add(30 * time.Second)
	assert.False(t, h.navidromeOnline(7, "joe", "secret"))
	assert.False(t, h.navidromeOnline(7, "joe", "secret"))
	assert.Equal(t, 1, probeCalls)

	// Once it goes stale, one render reports the old answer while it refreshes,
	// and the next one reports the new one without probing again.
	serverOnline = true
	now = now.Add(time.Minute)
	assert.False(t, h.navidromeOnline(7, "joe", "secret"))
	assert.Equal(t, 2, probeCalls)
	assert.True(t, h.navidromeOnline(7, "joe", "secret"))
	assert.Equal(t, 2, probeCalls)
}

// Shutdown closes the job runner: a stale entry must not spawn an untracked
// goroutine, and the next render gets another chance.
func TestNavidromeOnline_WhenRunnerIsClosedItRetriesLater(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{Logger: logger, Config: &config.Config{}}

	probeCalls := 0
	h.navidromeHealth.probe = func(ctx context.Context, username, password string) bool {
		probeCalls++
		return true
	}
	runnerClosed := true
	h.navidromeHealth.start = func(fn func(context.Context)) bool {
		if runnerClosed {
			return false
		}
		fn(context.Background())
		return true
	}
	h.navidromeHealth.ttl = time.Minute
	h.navidromeHealth.now = time.Now

	assert.False(t, h.navidromeOnline(9, "joe", "secret"))
	assert.Equal(t, 0, probeCalls)

	runnerClosed = false
	assert.False(t, h.navidromeOnline(9, "joe", "secret"))
	assert.Equal(t, 1, probeCalls)
	assert.True(t, h.navidromeOnline(9, "joe", "secret"))
}

// The cached result is per user, so one user's healthy server cannot mark
// another user's as reachable.
func TestNavidromeOnline_IsPerUser(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{Logger: logger, Config: &config.Config{}}

	onlineFor := map[string]bool{"healthy": true}
	h.navidromeHealth.probe = func(ctx context.Context, username, password string) bool {
		return onlineFor[username]
	}
	h.navidromeHealth.start = func(fn func(context.Context)) bool {
		fn(context.Background())
		return true
	}
	h.navidromeHealth.ttl = time.Minute
	h.navidromeHealth.now = time.Now

	require.False(t, h.navidromeOnline(1, "healthy", "pw"))
	require.False(t, h.navidromeOnline(2, "unreachable", "pw"))
	assert.True(t, h.navidromeOnline(1, "healthy", "pw"))
	assert.False(t, h.navidromeOnline(2, "unreachable", "pw"))
}

func TestGetHomeStats_PlaylistOnlyCountsAreIncluded(t *testing.T) {
	client, _, h, u := setupHomeStats(t)
	ctx := context.Background()

	pl, err := client.Playlist.Create().
		SetUser(u).
		SetRemoteID("pl-only").
		SetName("No History").
		SetSource("spotify").
		Save(ctx)
	require.NoError(t, err)

	for i, spec := range []struct{ artist, album, track string }{
		{"Cigarettes After Sex", "Cry", "Kiss It Better"},
		{"Cigarettes After Sex", "X's", "Heavenly"},
	} {
		_, err := client.PlaylistTrack.Create().
			SetPlaylist(pl).
			SetPosition(i).
			SetArtistName(spec.artist).
			SetAlbumName(spec.album).
			SetTrackName(spec.track).
			SetRemoteID(fmt.Sprintf("pt-%d", i)).
			Save(ctx)
		require.NoError(t, err)
	}

	stats, err := h.getHomeStats(ctx, u)
	require.NoError(t, err)

	assert.Equal(t, 0, stats.TotalListens)
	assert.Equal(t, 1, stats.UniqueArtists)
	assert.Equal(t, 2, stats.UniqueAlbums)
	assert.Equal(t, 2, stats.UniqueTracks)
}

// The grouped playlist scan must stay inside the requesting user's playlists.
func TestGetHomeStats_IgnoresOtherUsersPlaylistTracks(t *testing.T) {
	client, _, h, alice := setupHomeStats(t)
	ctx := context.Background()

	bob, err := client.User.Create().SetUsername(t.Name() + "-bob").Save(ctx)
	require.NoError(t, err)

	bobPlaylist, err := client.Playlist.Create().
		SetUser(bob).
		SetRemoteID("bob-pl").
		SetName("Bob's").
		SetSource("spotify").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.PlaylistTrack.Create().
		SetPlaylist(bobPlaylist).
		SetPosition(0).
		SetArtistName("Someone Else").
		SetAlbumName("Their Album").
		SetTrackName("Their Track").
		SetRemoteID("bob-pt").
		Save(ctx)
	require.NoError(t, err)

	stats, err := h.getHomeStats(ctx, alice)
	require.NoError(t, err)

	assert.Equal(t, 0, stats.UniqueArtists)
	assert.Equal(t, 0, stats.UniqueAlbums)
	assert.Equal(t, 0, stats.UniqueTracks)

	all, err := client.PlaylistTrack.Query().
		Where(playlisttrack.HasPlaylistWith(playlist.HasUserWith(user.ID(alice.ID)))).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, all)
}
