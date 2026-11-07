// Governing: issue #344 (a steady-state metadata tick costs O(distinct triples),
// not O(listen rows)), ADR-0004 (Ent ORM), ADR-0019 (structured metrics)
package services_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"spotter/ent"
	"spotter/ent/album"
	"spotter/ent/artist"
	"spotter/ent/playlist"
	"spotter/ent/playlisttrack"
	"spotter/ent/track"
	"spotter/ent/user"
	"spotter/internal/config"
	"spotter/internal/services"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type catalogTripleSpec struct {
	artistName string
	albumName  string
	trackName  string
}

// setupCatalogTick opens an in-memory database behind the counting driver, so a
// test can measure how many reads one catalog tick issues.
func setupCatalogTick(t *testing.T) (*ent.Client, *countingDriver, *services.MetadataService, *ent.User) {
	t.Helper()

	drv, err := entsql.Open("sqlite3",
		fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	require.NoError(t, err)

	counting := &countingDriver{Driver: drv}
	client := ent.NewClient(ent.Driver(counting))
	t.Cleanup(func() { client.Close() })

	require.NoError(t, client.Schema.Create(context.Background()))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := services.NewMetadataService(client, nil, &config.Config{}, logger, nil)

	u, err := client.User.Create().
		SetUsername(t.Name()).
		Save(context.Background())
	require.NoError(t, err)

	return client, counting, svc, u
}

func addCatalogListens(t *testing.T, client *ent.Client, u *ent.User, spec catalogTripleSpec, repeats int) {
	t.Helper()

	for i := 0; i < repeats; i++ {
		_, err := client.Listen.Create().
			SetUser(u).
			SetArtistName(spec.artistName).
			SetAlbumName(spec.albumName).
			SetTrackName(spec.trackName).
			SetSource("navidrome").
			SetPlayedAt(time.Now().Add(-time.Duration(i) * time.Hour)).
			Save(context.Background())
		require.NoError(t, err)
	}
}

func catalogSizes(t *testing.T, client *ent.Client, userID int) (int, int, int) {
	t.Helper()

	ctx := context.Background()
	artists, err := client.Artist.Query().
		Where(artist.HasUserWith(user.ID(userID))).
		Count(ctx)
	require.NoError(t, err)

	albums, err := client.Album.Query().
		Where(album.HasUserWith(user.ID(userID))).
		Count(ctx)
	require.NoError(t, err)

	tracks, err := client.Track.Query().
		Where(track.HasArtistWith(artist.HasUserWith(user.ID(userID)))).
		Count(ctx)
	require.NoError(t, err)

	return artists, albums, tracks
}

// A scrobble stores one listen row every time a track plays, so the same
// (artist, album, track) triple repeats. One run must resolve each entity once,
// whether the row appeared once or twenty times.
func TestBuildCatalog_ResolvesEachTripleOnceRegardlessOfListenCount(t *testing.T) {
	client, counting, svc, u := setupCatalogTick(t)
	ctx := context.Background()

	addCatalogListens(t, client, u, catalogTripleSpec{
		artistName: "Big Thief",
		albumName:  "Dragon New Warm Mountain",
		trackName:  "Ready To Roll",
	}, 20)

	counting.queries.Store(0)
	require.NoError(t, svc.BuildCatalog(ctx, u))
	reads := counting.queries.Load()

	artists, albums, tracks := catalogSizes(t, client, u.ID)
	assert.Equal(t, 1, artists)
	assert.Equal(t, 1, albums)
	assert.Equal(t, 1, tracks)

	// Four bound/aggregate reads, one lookup per distinct entity, its insert, and
	// the event record. Twenty listens used to mean sixty lookups on top of that.
	assert.LessOrEqual(t, reads, int64(12),
		"reading 20 rows of one triple must not cost one lookup per row")
}

// The steady-state tick is what the scheduler runs every interval on an already
// built catalog. Its cost must follow distinct triples, not the size of history.
func TestBuildCatalog_SteadyStateTickIgnoresRepeatedListens(t *testing.T) {
	triples := []catalogTripleSpec{
		{artistName: "Big Thief", albumName: "Dragon New Warm Mountain", trackName: "Ready To Roll"},
		{artistName: "Big Thief", albumName: "Dragon New Warm Mountain", trackName: "Certainty"},
		{artistName: "Big Thief", albumName: "U.F.O.F.", trackName: "Too Late"},
		{artistName: "Fontaines D.C.", albumName: "Skinty Fia", trackName: "I Love You"},
		{artistName: "Fontaines D.C.", albumName: "Romance", trackName: "Love Is the Main"},
		{artistName: "Big Thief", albumName: "Master's House", trackName: "Not"},
	}

	reads := map[string]int64{}
	for _, tc := range []struct {
		name    string
		repeats int
	}{
		{name: "unique", repeats: 1},
		{name: "repeated", repeats: 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, counting, svc, u := setupCatalogTick(t)
			ctx := context.Background()

			for i := 0; i < tc.repeats; i++ {
				for _, spec := range triples {
					addCatalogListens(t, client, u, spec, 1)
				}
			}

			require.NoError(t, svc.BuildCatalog(ctx, u))

			counting.queries.Store(0)
			require.NoError(t, svc.BuildCatalog(ctx, u))
			reads[tc.name] = counting.queries.Load()

			artists, albums, tracks := catalogSizes(t, client, u.ID)
			assert.Equal(t, 2, artists)
			assert.Equal(t, 5, albums)
			assert.Equal(t, 6, tracks)
		})
	}

	assert.Equal(t, reads["unique"], reads["repeated"],
		"72 listens over 6 triples must cost the same tick as 6")
	assert.Less(t, reads["repeated"], int64(len(triples)*12),
		"a steady-state tick must issue fewer queries than there are listens")
}

func addCatalogPlaylistTrack(t *testing.T, client *ent.Client, pl *ent.Playlist, position int, spec catalogTripleSpec) {
	t.Helper()

	_, err := client.PlaylistTrack.Create().
		SetPlaylist(pl).
		SetPosition(position).
		SetArtistName(spec.artistName).
		SetAlbumName(spec.albumName).
		SetTrackName(spec.trackName).
		SetRemoteID(fmt.Sprintf("remote-%d", position)).
		Save(context.Background())
	require.NoError(t, err)
}

// Linking used to issue one three-way join per unlinked playlist track. Matching
// against one catalog read must keep the tick flat as the playlist grows.
func TestBuildCatalog_LinksPlaylistTracksWithoutPerTrackQueries(t *testing.T) {
	reads := map[string]int64{}

	for _, tc := range []struct {
		name   string
		tracks int
	}{
		{name: "few", tracks: 5},
		{name: "many", tracks: 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, counting, svc, u := setupCatalogTick(t)
			ctx := context.Background()

			pl, err := client.Playlist.Create().
				SetUser(u).
				SetRemoteID("pl-1").
				SetName("Links").
				SetSource("spotify").
				SetTrackCount(tc.tracks).
				Save(ctx)
			require.NoError(t, err)

			spec := catalogTripleSpec{
				artistName: "Big Thief",
				albumName:  "Dragon New Warm Mountain",
				trackName:  "Ready To Roll",
			}
			for i := 0; i < tc.tracks; i++ {
				addCatalogPlaylistTrack(t, client, pl, i, spec)
			}

			counting.queries.Store(0)
			require.NoError(t, svc.BuildCatalog(ctx, u))
			reads[tc.name] = counting.queries.Load()

			linked, err := client.PlaylistTrack.Query().
				Where(
					playlisttrack.HasTrack(),
					playlisttrack.HasPlaylistWith(playlist.HasUserWith(user.ID(u.ID))),
				).
				Count(ctx)
			require.NoError(t, err)
			assert.Equal(t, tc.tracks, linked, "every playlist track must reach its catalog track")

			// The second tick finds nothing left to link, so it costs nothing extra
			// for the rows that are already settled.
			counting.queries.Store(0)
			require.NoError(t, svc.BuildCatalog(ctx, u))
			linkedAgain := counting.queries.Load()
			assert.LessOrEqual(t, linkedAgain, reads[tc.name])
		})
	}

	assert.Equal(t, reads["few"], reads["many"],
		"linking 60 playlist tracks must not cost more reads than linking 5")
}

// Matching happens in memory against the user's own catalog, so a track that
// exists only in another user's catalog must never be linked.
func TestLinkPlaylistTracks_NeverCrossesUsers(t *testing.T) {
	client, _, svc, alice := setupCatalogTick(t)
	ctx := context.Background()

	bob, err := client.User.Create().SetUsername("bob").Save(ctx)
	require.NoError(t, err)

	aliceArtist, err := client.Artist.Create().SetName("Shared Artist").SetUser(alice).Save(ctx)
	require.NoError(t, err)
	aliceAlbum, err := client.Album.Create().
		SetName("Shared Album").
		SetUser(alice).
		SetArtist(aliceArtist).
		Save(ctx)
	require.NoError(t, err)
	aliceTrack, err := client.Track.Create().
		SetName("Shared Song").
		SetArtist(aliceArtist).
		SetAlbum(aliceAlbum).
		Save(ctx)
	require.NoError(t, err)

	bobArtist, err := client.Artist.Create().SetName("Shared Artist").SetUser(bob).Save(ctx)
	require.NoError(t, err)
	_, err = client.Track.Create().SetName("Shared Song").SetArtist(bobArtist).Save(ctx)
	require.NoError(t, err)

	bobPlaylist, err := client.Playlist.Create().
		SetUser(bob).
		SetRemoteID("bob-pl").
		SetName("Bob Links").
		SetSource("spotify").
		Save(ctx)
	require.NoError(t, err)
	addCatalogPlaylistTrack(t, client, bobPlaylist, 0, catalogTripleSpec{
		artistName: "Shared Artist",
		albumName:  "Shared Album",
		trackName:  "Shared Song",
	})

	require.NoError(t, svc.BuildCatalog(ctx, bob))

	linkedTracks, err := client.PlaylistTrack.Query().
		Where(
			playlisttrack.HasPlaylistWith(playlist.HasUserWith(user.ID(bob.ID))),
			playlisttrack.HasTrack(),
		).
		WithTrack().
		All(ctx)
	require.NoError(t, err)
	require.Len(t, linkedTracks, 1)
	assert.NotEqual(t, aliceTrack.ID, linkedTracks[0].Edges.Track.ID,
		"linking must resolve inside the requesting user's catalog")

	linkedArtist, err := linkedTracks[0].Edges.Track.QueryArtist().Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, bobArtist.ID, linkedArtist.ID)
}
