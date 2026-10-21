// Governing: ADR-0022 (multi-user correctness on shared instances), issue #338
package handlers

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"spotter/ent"
	"spotter/ent/enttest"
	"spotter/internal/config"
	"spotter/internal/events"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type scopingLibrary struct {
	user   *ent.User
	artist *ent.Artist
	album  *ent.Album
	tracks []*ent.Track
}

func setupScopingHandler(t *testing.T) (*ent.Client, *Handler) {
	t.Helper()
	dbName := "file:" + t.Name() + "?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dbName)
	t.Cleanup(func() { client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New(client, &config.Config{}, logger, nil, nil, nil, nil, nil, nil, nil, nil, events.NewBus(), nil)
	return client, h
}

// newScopingLibrary creates a user that owns one artist, one album and count
// tracks, so two users can be compared on a shared instance.
func newScopingLibrary(t *testing.T, client *ent.Client, name string, count int) *scopingLibrary {
	t.Helper()
	ctx := context.Background()

	u, err := client.User.Create().
		SetUsername(name).
		SetPaginationSize(25).
		Save(ctx)
	require.NoError(t, err)

	a, err := client.Artist.Create().
		SetName(name + " Artist").
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	al, err := client.Album.Create().
		SetName(name + " Album").
		SetUser(u).
		SetArtist(a).
		Save(ctx)
	require.NoError(t, err)

	lib := &scopingLibrary{user: u, artist: a, album: al}
	for i := 1; i <= count; i++ {
		tr, err := client.Track.Create().
			SetName(name + " Track " + strconv.Itoa(i)).
			SetDurationMs(180000).
			SetArtist(a).
			SetAlbum(al).
			Save(ctx)
		require.NoError(t, err)
		lib.tracks = append(lib.tracks, tr)
	}
	return lib
}

func trackIDs(tracks []*ent.Track) []int {
	ids := make([]int, 0, len(tracks))
	for _, t := range tracks {
		ids = append(ids, t.ID)
	}
	return ids
}

// Seed IDs arrive from the request form, so they must resolve against the
// requesting user's library and nothing else.
func TestMixtapeSeedScopedToUser(t *testing.T) {
	client, h := setupScopingHandler(t)
	ctx := context.Background()

	alice := newScopingLibrary(t, client, "scoping_alice", 2)
	bob := newScopingLibrary(t, client, "scoping_bob", 2)

	t.Run("another user's artist is not a seed", func(t *testing.T) {
		form := url.Values{"seed_type": {"artist"}, "seed_id": {strconv.Itoa(bob.artist.ID)}}
		assert.Nil(t, h.mixtapeSeed(ctx, alice.user.ID, form))
	})

	t.Run("own artist is a seed", func(t *testing.T) {
		form := url.Values{"seed_type": {"artist"}, "seed_id": {strconv.Itoa(alice.artist.ID)}}
		seed := h.mixtapeSeed(ctx, alice.user.ID, form)
		require.NotNil(t, seed)
		assert.Equal(t, alice.artist.ID, seed.Artist.ID)
	})

	t.Run("another user's album is not a seed", func(t *testing.T) {
		form := url.Values{"seed_type": {"album"}, "seed_id": {strconv.Itoa(bob.album.ID)}}
		assert.Nil(t, h.mixtapeSeed(ctx, alice.user.ID, form))
	})

	t.Run("own album is a seed", func(t *testing.T) {
		form := url.Values{"seed_type": {"album"}, "seed_id": {strconv.Itoa(alice.album.ID)}}
		seed := h.mixtapeSeed(ctx, alice.user.ID, form)
		require.NotNil(t, seed)
		assert.Equal(t, alice.album.ID, seed.Album.ID)
	})

	t.Run("track seeds drop another user's ids", func(t *testing.T) {
		form := url.Values{
			"seed_type": {"tracks"},
			"track_ids": {joinIDs(append(trackIDs(alice.tracks), trackIDs(bob.tracks)...)...)},
		}
		seed := h.mixtapeSeed(ctx, alice.user.ID, form)
		require.NotNil(t, seed)
		require.Len(t, seed.Tracks, len(alice.tracks))
		for _, tr := range seed.Tracks {
			assert.Contains(t, trackIDs(alice.tracks), tr.ID)
		}
	})

	t.Run("only another user's tracks yield no seed", func(t *testing.T) {
		form := url.Values{"seed_type": {"tracks"}, "track_ids": {joinIDs(trackIDs(bob.tracks)...)}}
		assert.Nil(t, h.mixtapeSeed(ctx, alice.user.ID, form))
	})

	t.Run("malformed ids yield no seed", func(t *testing.T) {
		for _, form := range []url.Values{
			{"seed_type": {"artist"}, "seed_id": {"not-a-number"}},
			{"seed_type": {"artist"}, "seed_id": {"-7"}},
			{"seed_type": {"album"}, "seed_id": {""}},
			{"seed_type": {"tracks"}, "track_ids": {"abc,-3,"}},
		} {
			assert.Nil(t, h.mixtapeSeed(ctx, alice.user.ID, form), "form: %v", form)
		}
	})
}

func joinIDs(ids ...int) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += strconv.Itoa(id)
	}
	return out
}

// Seed track IDs come straight from a form field, so parsing must ignore junk
// and refuse to let one request widen the query without bound.
func TestParseCommaSeparatedIDs(t *testing.T) {
	assert.Equal(t, []int{7, 8}, parseCommaSeparatedIDs(" 7 , 8 "))
	assert.Empty(t, parseCommaSeparatedIDs("abc,-3,,0"))
	assert.Empty(t, parseCommaSeparatedIDs(""))

	var many []string
	for i := 1; i <= maxSeedTrackIDs+25; i++ {
		many = append(many, strconv.Itoa(i))
	}
	assert.Len(t, parseCommaSeparatedIDs(strings.Join(many, ",")), maxSeedTrackIDs)
}

func TestTracksOwnedByDropsForeignTracks(t *testing.T) {
	client, h := setupScopingHandler(t)
	ctx := context.Background()

	alice := newScopingLibrary(t, client, "owned_alice", 2)
	bob := newScopingLibrary(t, client, "owned_bob", 2)

	tracks := h.tracksOwnedBy(ctx, alice.user.ID, append(trackIDs(alice.tracks), trackIDs(bob.tracks)...))
	require.Len(t, tracks, len(alice.tracks))
	for _, tr := range tracks {
		assert.Contains(t, trackIDs(alice.tracks), tr.ID)
	}

	assert.Empty(t, h.tracksOwnedBy(ctx, alice.user.ID, trackIDs(bob.tracks)))
	assert.Nil(t, h.tracksOwnedBy(ctx, alice.user.ID, nil))
}

// The artist top-tracks view resolves listens to a catalog row; an unscoped
// lookup renders another user's row and art, with links that 404 for the viewer.
func TestGetTopTracksForArtistScopedToUser(t *testing.T) {
	client, h := setupScopingHandler(t)
	ctx := context.Background()

	// Bob is created first so his catalog row wins an unscoped First().
	bobUser, err := client.User.Create().
		SetUsername("topttracks_bob").
		SetPaginationSize(25).
		Save(ctx)
	require.NoError(t, err)
	bobArtist, err := client.Artist.Create().
		SetName("Shared Artist").
		SetUser(bobUser).
		Save(ctx)
	require.NoError(t, err)
	bobTrack, err := client.Track.Create().
		SetName("Shared Song").
		SetArtist(bobArtist).
		Save(ctx)
	require.NoError(t, err)

	aliceUser, err := client.User.Create().
		SetUsername("topttracks_alice").
		SetPaginationSize(25).
		Save(ctx)
	require.NoError(t, err)
	aliceArtist, err := client.Artist.Create().
		SetName("Shared Artist").
		SetUser(aliceUser).
		Save(ctx)
	require.NoError(t, err)
	aliceTrack, err := client.Track.Create().
		SetName("Shared Song").
		SetArtist(aliceArtist).
		Save(ctx)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		_, err = client.Listen.Create().
			SetUser(aliceUser).
			SetTrackName("Shared Song").
			SetArtistName("Shared Artist").
			SetAlbumName("Shared Album").
			SetSource("navidrome").
			SetPlayedAt(time.Now().Add(-time.Duration(i) * time.Hour)).
			Save(ctx)
		require.NoError(t, err)
	}

	top := h.getTopTracksForArtist(ctx, aliceUser.ID, "Shared Artist", 10)
	require.Len(t, top, 1)
	assert.Equal(t, aliceTrack.ID, top[0].Track.ID, "top track resolved to another user's row")
	assert.NotEqual(t, bobTrack.ID, top[0].Track.ID)
}

// The album page lists the album's rows; tracks owned by someone else must not
// appear with their art and links.
func TestGetAlbumTracksWithListensScopedToUser(t *testing.T) {
	client, h := setupScopingHandler(t)
	ctx := context.Background()

	alice := newScopingLibrary(t, client, "albumtracks_alice", 2)
	bobUser, err := client.User.Create().
		SetUsername("albumtracks_bob").
		SetPaginationSize(25).
		Save(ctx)
	require.NoError(t, err)
	intruderArtist, err := client.Artist.Create().
		SetName("Intruder Artist").
		SetUser(bobUser).
		Save(ctx)
	require.NoError(t, err)

	// A track owned by another user that points at Alice's album row.
	intruder, err := client.Track.Create().
		SetName("Intruder Track").
		SetArtist(intruderArtist).
		SetAlbum(alice.album).
		Save(ctx)
	require.NoError(t, err)

	rows := h.getAlbumTracksWithListens(ctx, alice.user.ID, alice.album)
	require.Len(t, rows, len(alice.tracks))
	for _, row := range rows {
		assert.NotEqual(t, intruder.ID, row.Track.ID, "another user's track appeared on this album page")
		assert.Contains(t, trackIDs(alice.tracks), row.Track.ID)
	}
}
