// Governing: ADR-0022 (multi-user correctness on shared instances), issue #338
package vibes

import (
	"context"
	"strconv"
	"testing"

	"spotter/ent"
	"spotter/ent/enttest"
	"spotter/internal/config"
	"spotter/internal/events"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// libraryUser creates a user owning one artist with count tracks, and returns
// the user together with the IDs of the tracks it owns.
func libraryUser(t *testing.T, client *ent.Client, username string, count int) (*ent.User, map[int]bool) {
	t.Helper()
	ctx := context.Background()

	u, err := client.User.Create().
		SetUsername(username).
		SetPaginationSize(25).
		Save(ctx)
	require.NoError(t, err)

	a, err := client.Artist.Create().
		SetName("Artist " + username).
		SetNavidromeID("nd-artist-" + username).
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	owned := make(map[int]bool, count)
	for i := 1; i <= count; i++ {
		suffix := username + "-" + strconv.Itoa(i)
		tr, err := client.Track.Create().
			SetName("Track " + suffix).
			SetNavidromeID("nd-track-" + suffix).
			SetDurationMs(180000).
			SetArtist(a).
			Save(ctx)
		require.NoError(t, err)
		owned[tr.ID] = true
	}

	return u, owned
}

func testVibesClient(t *testing.T) *ent.Client {
	t.Helper()
	dbName := "file:" + t.Name() + "?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dbName)
	t.Cleanup(func() { client.Close() })
	return client
}

func testVibesConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Vibes.TimeoutSeconds = 30
	return cfg
}

// Enhancement candidates must come only from the requesting user's library: an
// unscoped query leaks another user's tracks into the prompt and then into the
// playlist, which breaks the later Navidrome sync.
func TestPlaylistEnhancerGetAvailableTracksScopedToUser(t *testing.T) {
	client := testVibesClient(t)
	ctx := context.Background()

	alice, aliceTracks := libraryUser(t, client, "alice_scoping", 3)
	_, bobTracks := libraryUser(t, client, "bob_scoping", 3)

	enhancer := NewPlaylistEnhancer(client, testVibesConfig(), nil, events.NewBus())

	available, err := enhancer.getAvailableTracks(ctx, alice.ID, nil)
	require.NoError(t, err)
	require.Len(t, available, len(aliceTracks))

	for _, candidate := range available {
		assert.Truef(t, aliceTracks[candidate.ID],
			"enhancement candidate %d belongs to another user", candidate.ID)
		assert.Falsef(t, bobTracks[candidate.ID],
			"enhancement candidate %d is Bob's track", candidate.ID)
	}
}

// Seed entities are resolved from request-supplied IDs, so the generator must
// load them against the requesting user's library rather than by bare ID.
func TestLoadSeedDataScopedToRequestingUser(t *testing.T) {
	client := testVibesClient(t)
	ctx := context.Background()

	alice, aliceTracks := libraryUser(t, client, "alice_seed", 2)
	bob, bobTracks := libraryUser(t, client, "bob_seed", 2)

	djA, err := client.DJ.Create().
		SetName("Alice DJ").
		SetSystemPrompt("You are Alice's DJ").
		SetUser(alice).
		Save(ctx)
	require.NoError(t, err)

	mixtapeUserA, err := client.Mixtape.Create().
		SetName("Alice Mixtape").
		SetUser(alice).
		SetDj(djA).
		Save(ctx)
	require.NoError(t, err)

	gen := NewMixtapeGenerator(client, testVibesConfig(), nil, events.NewBus())

	bobArtist, err := bob.QueryArtists().First(ctx)
	require.NoError(t, err)
	bobAlbum, err := client.Album.Create().
		SetName("Bob Album").
		SetUser(bob).
		SetArtist(bobArtist).
		Save(ctx)
	require.NoError(t, err)
	bobTrackIDs := keysOf(bobTracks)

	aliceArtist, err := alice.QueryArtists().First(ctx)
	require.NoError(t, err)

	t.Run("artist seed owned by another user is rejected", func(t *testing.T) {
		req := &GenerationRequest{
			Mixtape: mixtapeUserA,
			DJ:      djA,
			UserID:  alice.ID,
			Seed:    NewArtistSeed(&ent.Artist{ID: bobArtist.ID}),
		}
		err := gen.loadSeedData(ctx, req)
		assert.Error(t, err, "another user's artist must not load")
	})

	t.Run("album seed owned by another user is rejected", func(t *testing.T) {
		req := &GenerationRequest{
			Mixtape: mixtapeUserA,
			DJ:      djA,
			UserID:  alice.ID,
			Seed:    NewAlbumSeed(&ent.Album{ID: bobAlbum.ID}),
		}
		err := gen.loadSeedData(ctx, req)
		assert.Error(t, err, "another user's album must not load")
	})

	t.Run("track seed ids owned by another user resolve to nothing", func(t *testing.T) {
		req := &GenerationRequest{
			Mixtape: mixtapeUserA,
			DJ:      djA,
			UserID:  alice.ID,
			Seed:    NewTrackIDsSeed(bobTrackIDs),
		}
		require.NoError(t, gen.loadSeedData(ctx, req))
		assert.Empty(t, req.Seed.Tracks, "another user's tracks must not become seed metadata")
	})

	t.Run("own seed data still loads", func(t *testing.T) {
		req := &GenerationRequest{
			Mixtape: mixtapeUserA,
			DJ:      djA,
			UserID:  alice.ID,
			Seed:    NewArtistSeed(aliceArtist),
		}
		require.NoError(t, gen.loadSeedData(ctx, req))
		require.NotNil(t, req.Seed.Artist)
		assert.Equal(t, aliceArtist.ID, req.Seed.Artist.ID)

		trackReq := &GenerationRequest{
			Mixtape: mixtapeUserA,
			DJ:      djA,
			UserID:  alice.ID,
			Seed:    NewTrackIDsSeed(keysOf(aliceTracks)),
		}
		require.NoError(t, gen.loadSeedData(ctx, trackReq))
		assert.Len(t, trackReq.Seed.Tracks, len(aliceTracks))
	})
}

func keysOf(set map[int]bool) []int {
	ids := make([]int, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return ids
}
