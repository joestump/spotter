package services_test

import (
	"context"
	"testing"
	"time"

	"spotter/ent"
	"spotter/ent/playlist"
	"spotter/ent/playlisttrack"
	"spotter/internal/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connectSpotify gives the user the credentials the syncer looks for before it calls a provider.
func connectSpotify(t *testing.T, client *ent.Client, user *ent.User) {
	t.Helper()
	_, err := client.SpotifyAuth.Create().
		SetUser(user).
		SetAccessToken("test_access_token").
		SetRefreshToken("test_refresh_token").
		SetExpiry(time.Now().Add(time.Hour)).
		Save(context.Background())
	require.NoError(t, err)
}

func repeatedTrackPlaylist(tracks ...providers.Track) []providers.Playlist {
	return []providers.Playlist{
		{
			ID:         "playlist-1",
			Name:       "Repeated Track Playlist",
			TrackCount: len(tracks),
			Tracks:     tracks,
		},
	}
}

func playlistTrackRows(ctx context.Context, client *ent.Client) ([]*ent.PlaylistTrack, error) {
	return client.PlaylistTrack.Query().
		Order(ent.Asc(playlisttrack.FieldPosition)).
		All(ctx)
}

// A playlist that legitimately contains the same track twice must keep both copies, in order,
// through repeated syncs: reconciliation matches each occurrence against its own row.
// Governing: SPEC listen-playlist-sync REQ-SYNC-031
func TestSyncer_IntentionalDuplicateTracksSurviveTwoSyncCycles(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	user := createTestUser(t, client)
	connectSpotify(t, client, user)

	sameTrack := providers.Track{ID: "track-1", Name: "Same Track", Artist: "Artist A", Album: "Album X", DurationMs: 180000}
	otherTrack := providers.Track{ID: "track-2", Name: "Other Track", Artist: "Artist B", Album: "Album Y", DurationMs: 240000}

	mock := &mockProvider{
		providerType: providers.TypeSpotify,
		playlists:    repeatedTrackPlaylist(sameTrack, otherTrack, sameTrack),
	}
	syncer.Register(mockFactory(mock))

	for cycle := 1; cycle <= 2; cycle++ {
		require.NoError(t, syncer.Sync(ctx, user))

		rows, err := playlistTrackRows(ctx, client)
		require.NoError(t, err)
		require.Len(t, rows, 3, "cycle %d: both occurrences of the repeated track must survive", cycle)

		assert.Equal(t, []int{0, 1, 2}, []int{rows[0].Position, rows[1].Position, rows[2].Position},
			"cycle %d: positions stay in playlist order", cycle)
		assert.Equal(t, "Same Track", rows[0].TrackName)
		assert.Equal(t, "Other Track", rows[1].TrackName)
		assert.Equal(t, "Same Track", rows[2].TrackName)
		assert.NotEqual(t, rows[0].ID, rows[2].ID, "cycle %d: the two occurrences are separate rows", cycle)
	}
}

// Reordering a playlist that contains a repeated track must not collide the occurrences onto
// each other: the second sync reassigns positions without dropping a copy.
// Governing: SPEC listen-playlist-sync REQ-SYNC-031
func TestSyncer_DuplicateTracksSurviveReorder(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	user := createTestUser(t, client)
	connectSpotify(t, client, user)

	sameTrack := providers.Track{ID: "track-1", Name: "Same Track", Artist: "Artist A", Album: "Album X", DurationMs: 180000}
	otherTrack := providers.Track{ID: "track-2", Name: "Other Track", Artist: "Artist B", Album: "Album Y", DurationMs: 240000}

	mock := &mockProvider{
		providerType: providers.TypeSpotify,
		playlists:    repeatedTrackPlaylist(sameTrack, otherTrack, sameTrack),
	}
	syncer.Register(mockFactory(mock))
	require.NoError(t, syncer.Sync(ctx, user))

	mock.playlists = repeatedTrackPlaylist(otherTrack, sameTrack, sameTrack)
	require.NoError(t, syncer.Sync(ctx, user))

	rows, err := playlistTrackRows(ctx, client)
	require.NoError(t, err)
	require.Len(t, rows, 3, "reordering must not drop either occurrence")
	assert.Equal(t, []string{"Other Track", "Same Track", "Same Track"},
		[]string{rows[0].TrackName, rows[1].TrackName, rows[2].TrackName})
}

// A playlist the provider stopped returning is marked inactive rather than deleted, and becomes
// active again if it reappears. A failed fetch must not deactivate anything.
// Governing: SPEC listen-playlist-sync REQ-SYNC-032
func TestSyncer_MarksDisappearedPlaylistsInactive(t *testing.T) {
	client, syncer, _ := setupTestSyncer(t)
	ctx := context.Background()

	user := createTestUser(t, client)
	connectSpotify(t, client, user)

	kept := providers.Playlist{ID: "playlist-1", Name: "Kept Playlist", TrackCount: 1,
		Tracks: []providers.Track{{ID: "track-1", Name: "First Track", Artist: "Artist A"}}}
	gone := providers.Playlist{ID: "playlist-2", Name: "Gone Playlist", TrackCount: 1,
		Tracks: []providers.Track{{ID: "track-2", Name: "Second Track", Artist: "Artist B"}}}

	mock := &mockProvider{providerType: providers.TypeSpotify, playlists: []providers.Playlist{kept, gone}}
	syncer.Register(mockFactory(mock))

	require.NoError(t, syncer.Sync(ctx, user))

	p1, err := client.Playlist.Query().Where(playlist.RemoteID("playlist-1")).Only(ctx)
	require.NoError(t, err)
	p2, err := client.Playlist.Query().Where(playlist.RemoteID("playlist-2")).Only(ctx)
	require.NoError(t, err)
	assert.True(t, p1.Active && p2.Active, "imported playlists start active")

	mock.playlists = []providers.Playlist{kept}
	require.NoError(t, syncer.Sync(ctx, user))

	p2, err = client.Playlist.Query().Where(playlist.RemoteID("playlist-2")).Only(ctx)
	require.NoError(t, err, "REQ-SYNC-032: the disappeared playlist is kept, not deleted")
	assert.False(t, p2.Active, "REQ-SYNC-032: the disappeared playlist is marked inactive")

	p1, err = client.Playlist.Query().Where(playlist.RemoteID("playlist-1")).Only(ctx)
	require.NoError(t, err)
	assert.True(t, p1.Active, "a playlist the provider still returns stays active")

	// The playlist coming back reactivates it.
	mock.playlists = []providers.Playlist{kept, gone}
	require.NoError(t, syncer.Sync(ctx, user))

	p2, err = client.Playlist.Query().Where(playlist.RemoteID("playlist-2")).Only(ctx)
	require.NoError(t, err)
	assert.True(t, p2.Active, "a playlist the provider returns again is active again")

	// A failed fetch leaves activation state alone: an outage is not a deletion.
	mock.err = assert.AnError
	require.Error(t, syncer.Sync(ctx, user))

	p1, err = client.Playlist.Query().Where(playlist.RemoteID("playlist-1")).Only(ctx)
	require.NoError(t, err)
	p2, err = client.Playlist.Query().Where(playlist.RemoteID("playlist-2")).Only(ctx)
	require.NoError(t, err)
	assert.True(t, p1.Active, "a failed fetch must not deactivate playlists")
	assert.True(t, p2.Active, "a failed fetch must not deactivate playlists")
}
