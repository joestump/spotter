// Governing: ADR-0031 (proposed), issue #367
package services

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"spotter/ent"
	"spotter/ent/enttest"
	"spotter/ent/lidarrqueue"
	"spotter/ent/syncevent"
	"spotter/internal/config"

	_ "github.com/mattn/go-sqlite3"
)

// lidarrStub is a fake Lidarr API that counts submissions, so a test can prove
// nothing reached Lidarr for a queue row whose entity belongs to someone else.
type lidarrStub struct {
	server      *httptest.Server
	artistPosts int64
	albumPosts  int64
}

func newLidarrStub(t *testing.T, artistMBID, albumMBID string) *lidarrStub {
	t.Helper()

	stub := &lidarrStub{}
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, v interface{}) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(v); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}
	mux.HandleFunc("/api/v1/queue", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]int{"totalRecords": 0})
	})
	mux.HandleFunc("/api/v1/artist/lookup", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]interface{}{{"artistName": "Stub Artist", "foreignArtistId": artistMBID}})
	})
	mux.HandleFunc("/api/v1/album/lookup", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]interface{}{{"title": "Stub Album", "foreignAlbumId": albumMBID}})
	})
	mux.HandleFunc("/api/v1/rootfolder", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]string{{"path": "/music"}})
	})
	mux.HandleFunc("/api/v1/qualityprofile", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]interface{}{{"id": 1, "name": "Any"}})
	})
	mux.HandleFunc("/api/v1/metadataprofile", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]interface{}{{"id": 1, "name": "Standard"}})
	})
	mux.HandleFunc("/api/v1/artist", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt64(&stub.artistPosts, 1)
			writeJSON(w, map[string]interface{}{"id": 42, "artistName": "Stub Artist", "foreignArtistId": artistMBID})
			return
		}
		writeJSON(w, []map[string]interface{}{{"id": 42, "artistName": "Stub Artist", "foreignArtistId": artistMBID}})
	})
	mux.HandleFunc("/api/v1/album", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt64(&stub.albumPosts, 1)
			writeJSON(w, map[string]interface{}{"id": 77, "title": "Stub Album", "foreignAlbumId": albumMBID})
			return
		}
		writeJSON(w, []map[string]interface{}{})
	})

	stub.server = httptest.NewServer(mux)
	t.Cleanup(stub.server.Close)
	return stub
}

func lidarrConfig(url string) *config.Config {
	cfg := &config.Config{}
	cfg.Lidarr.BaseURL = url
	cfg.Lidarr.APIKey = "test-key"
	cfg.Lidarr.QueueMax = 50
	return cfg
}

func newOwnerScopingClient(t *testing.T) (*ent.Client, *slog.Logger) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	return client, slog.New(slog.NewTextHandler(io.Discard, nil))
}

func createQueuedItem(ctx context.Context, client *ent.Client, entityType lidarrqueue.EntityType, entityID int, mbid string, owner *ent.User) (*ent.LidarrQueue, error) {
	return client.LidarrQueue.Create().
		SetEntityType(entityType).
		SetEntityID(entityID).
		SetMusicbrainzID(mbid).
		SetStatus(lidarrqueue.StatusQueued).
		SetUser(owner).
		Save(ctx)
}

// TestSubmitItem_ForeignArtistFailsClosed proves that a queue row owned by one
// user cannot submit an artist that belongs to another: nothing is sent to
// Lidarr and the error is classified permanent, so the row stops retrying.
func TestSubmitItem_ForeignArtistFailsClosed(t *testing.T) {
	client, logger := newOwnerScopingClient(t)
	ctx := context.Background()

	ownerA, err := client.User.Create().SetUsername("owner_a_artist").Save(ctx)
	if err != nil {
		t.Fatalf("create user a: %v", err)
	}
	victimB, err := client.User.Create().SetUsername("victim_b_artist").Save(ctx)
	if err != nil {
		t.Fatalf("create user b: %v", err)
	}
	const mbid = "aaaaaaaa-1111-2222-3333-444444444444"
	foreignArtist, err := client.Artist.Create().SetName("B Artist").SetMusicbrainzID(mbid).SetUser(victimB).Save(ctx)
	if err != nil {
		t.Fatalf("create artist: %v", err)
	}
	item, err := createQueuedItem(ctx, client, lidarrqueue.EntityTypeArtist, foreignArtist.ID, mbid, ownerA)
	if err != nil {
		t.Fatalf("create queue item: %v", err)
	}

	stub := newLidarrStub(t, mbid, "")
	s := NewLidarrSubmitter(client, lidarrConfig(stub.server.URL), logger)

	lidarrID, err := s.submitItem(ctx, item, ownerA)
	if err == nil {
		t.Fatalf("expected ownership mismatch to fail, submitted lidarr id %d", lidarrID)
	}
	if !isPermanentLidarrError(err.Error()) {
		t.Errorf("expected permanent classification for %q", err.Error())
	}
	if lidarrID != 0 {
		t.Errorf("expected no lidarr id, got %d", lidarrID)
	}
	if got := atomic.LoadInt64(&stub.artistPosts); got != 0 {
		t.Errorf("expected no artist POSTs for a foreign entity, got %d", got)
	}
}

// TestSubmitItem_ForeignAlbumFailsClosed covers the album path, where the album
// entity supplies the artist MBID used for the submission.
func TestSubmitItem_ForeignAlbumFailsClosed(t *testing.T) {
	client, logger := newOwnerScopingClient(t)
	ctx := context.Background()

	ownerA, err := client.User.Create().SetUsername("owner_a_album").Save(ctx)
	if err != nil {
		t.Fatalf("create user a: %v", err)
	}
	victimB, err := client.User.Create().SetUsername("victim_b_album").Save(ctx)
	if err != nil {
		t.Fatalf("create user b: %v", err)
	}
	const artistMBID = "bbbbbbbb-1111-2222-3333-444444444444"
	const albumMBID = "cccccccc-1111-2222-3333-444444444444"
	bArtist, err := client.Artist.Create().SetName("B Artist").SetMusicbrainzID(artistMBID).SetUser(victimB).Save(ctx)
	if err != nil {
		t.Fatalf("create artist: %v", err)
	}
	bAlbum, err := client.Album.Create().SetName("B Album").SetMusicbrainzID(albumMBID).SetUser(victimB).SetArtist(bArtist).Save(ctx)
	if err != nil {
		t.Fatalf("create album: %v", err)
	}
	item, err := createQueuedItem(ctx, client, lidarrqueue.EntityTypeAlbum, bAlbum.ID, albumMBID, ownerA)
	if err != nil {
		t.Fatalf("create queue item: %v", err)
	}

	stub := newLidarrStub(t, artistMBID, albumMBID)
	s := NewLidarrSubmitter(client, lidarrConfig(stub.server.URL), logger)

	lidarrID, err := s.submitItem(ctx, item, ownerA)
	if err == nil {
		t.Fatalf("expected ownership mismatch to fail, submitted lidarr id %d", lidarrID)
	}
	if !isPermanentLidarrError(err.Error()) {
		t.Errorf("expected permanent classification for %q", err.Error())
	}
	if got := atomic.LoadInt64(&stub.albumPosts); got != 0 {
		t.Errorf("expected no album POSTs for a foreign entity, got %d", got)
	}
}

// TestSubmitAlbum_ArtistOfAnotherOwnerIsNotUsed proves the album path cannot be
// steered by an album whose artist edge points at another user's artist: the
// artist load carries the owner predicate, so the submission fails instead of
// borrowing the other user's MusicBrainz ID.
func TestSubmitAlbum_ArtistOfAnotherOwnerIsNotUsed(t *testing.T) {
	client, logger := newOwnerScopingClient(t)
	ctx := context.Background()

	ownerA, err := client.User.Create().SetUsername("owner_a_mixed").Save(ctx)
	if err != nil {
		t.Fatalf("create user a: %v", err)
	}
	victimB, err := client.User.Create().SetUsername("victim_b_mixed").Save(ctx)
	if err != nil {
		t.Fatalf("create user b: %v", err)
	}
	const bArtistMBID = "eeeeeeee-1111-2222-3333-444444444444"
	const albumMBID = "ffffffff-1111-2222-3333-444444444444"

	bArtist, err := client.Artist.Create().SetName("B Artist").SetMusicbrainzID(bArtistMBID).SetUser(victimB).Save(ctx)
	if err != nil {
		t.Fatalf("create b artist: %v", err)
	}
	// A's album, but its artist edge points at B's artist.
	aAlbum, err := client.Album.Create().SetName("A Album").SetMusicbrainzID(albumMBID).SetUser(ownerA).SetArtist(bArtist).Save(ctx)
	if err != nil {
		t.Fatalf("create album: %v", err)
	}
	item, err := createQueuedItem(ctx, client, lidarrqueue.EntityTypeAlbum, aAlbum.ID, albumMBID, ownerA)
	if err != nil {
		t.Fatalf("create queue item: %v", err)
	}

	stub := newLidarrStub(t, bArtistMBID, albumMBID)
	s := NewLidarrSubmitter(client, lidarrConfig(stub.server.URL), logger)

	if _, err := s.submitAlbum(ctx, item, ownerA); err == nil {
		t.Fatal("expected the owner-scoped artist load to reject another user's artist")
	}
	if got := atomic.LoadInt64(&stub.albumPosts); got != 0 {
		t.Errorf("expected no album POSTs when the artist is not the owner's, got %d", got)
	}
}

// TestTick_ForeignEntityNeitherSubmitsNorNotifies is the end-to-end statement of
// issue #367: a queue row that names another user's entity must not submit that
// user's catalog item and must not notify them.
func TestTick_ForeignEntityNeitherSubmitsNorNotifies(t *testing.T) {
	client, logger := newOwnerScopingClient(t)
	ctx := context.Background()

	ownerA, err := client.User.Create().SetUsername("owner_a_tick").Save(ctx)
	if err != nil {
		t.Fatalf("create user a: %v", err)
	}
	victimB, err := client.User.Create().SetUsername("victim_b_tick").Save(ctx)
	if err != nil {
		t.Fatalf("create user b: %v", err)
	}
	const mbid = "99999999-1111-2222-3333-444444444444"
	bArtist, err := client.Artist.Create().SetName("B Tick Artist").SetMusicbrainzID(mbid).SetUser(victimB).Save(ctx)
	if err != nil {
		t.Fatalf("create artist: %v", err)
	}
	item, err := createQueuedItem(ctx, client, lidarrqueue.EntityTypeArtist, bArtist.ID, mbid, ownerA)
	if err != nil {
		t.Fatalf("create queue item: %v", err)
	}

	stub := newLidarrStub(t, mbid, "")
	s := NewLidarrSubmitter(client, lidarrConfig(stub.server.URL), logger)

	tickCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s.tick(tickCtx)

	if got := atomic.LoadInt64(&stub.artistPosts); got != 0 {
		t.Errorf("expected no submission to Lidarr, got %d artist POSTs", got)
	}
	events, err := client.SyncEvent.Query().All(ctx)
	if err != nil {
		t.Fatalf("query sync events: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected no sync event for a refused submission, got %d", len(events))
	}
	storedArtist, err := client.Artist.Get(ctx, bArtist.ID)
	if err != nil {
		t.Fatalf("get artist: %v", err)
	}
	if storedArtist.LidarrID != "" {
		t.Errorf("expected victim artist untouched, got lidarr_id %q", storedArtist.LidarrID)
	}
	storedItem, err := client.LidarrQueue.Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("get queue item: %v", err)
	}
	if storedItem.Status != lidarrqueue.StatusFailed {
		t.Errorf("expected status failed, got %s", storedItem.Status)
	}
	if storedItem.Attempts != maxAttempts {
		t.Errorf("expected attempts capped at %d, got %d", maxAttempts, storedItem.Attempts)
	}
	if storedItem.LastError == nil || *storedItem.LastError == "" {
		t.Error("expected last_error to name the ownership mismatch")
	}
}

// TestTick_SubmitsAndNotifiesQueueOwner is the regression guard: the normal path
// still submits the owner's own entity, writes back the owner's row, and notifies
// the queue row's owner.
func TestTick_SubmitsAndNotifiesQueueOwner(t *testing.T) {
	client, logger := newOwnerScopingClient(t)
	ctx := context.Background()

	ownerA, err := client.User.Create().SetUsername("owner_a_ok").Save(ctx)
	if err != nil {
		t.Fatalf("create user a: %v", err)
	}
	const mbid = "88888888-1111-2222-3333-444444444444"
	aArtist, err := client.Artist.Create().SetName("A Tick Artist").SetMusicbrainzID(mbid).SetUser(ownerA).Save(ctx)
	if err != nil {
		t.Fatalf("create artist: %v", err)
	}
	item, err := createQueuedItem(ctx, client, lidarrqueue.EntityTypeArtist, aArtist.ID, mbid, ownerA)
	if err != nil {
		t.Fatalf("create queue item: %v", err)
	}

	stub := newLidarrStub(t, mbid, "")
	s := NewLidarrSubmitter(client, lidarrConfig(stub.server.URL), logger)

	tickCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s.tick(tickCtx)

	if got := atomic.LoadInt64(&stub.artistPosts); got != 1 {
		t.Fatalf("expected exactly 1 artist submission, got %d", got)
	}
	storedArtist, err := client.Artist.Get(ctx, aArtist.ID)
	if err != nil {
		t.Fatalf("get artist: %v", err)
	}
	if storedArtist.LidarrID != "42" {
		t.Errorf("expected owner's artist to carry lidarr_id 42, got %q", storedArtist.LidarrID)
	}
	events, err := client.SyncEvent.Query().All(ctx)
	if err != nil {
		t.Fatalf("query sync events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 sync event, got %d", len(events))
	}
	notifiedUser, err := events[0].QueryUser().Only(ctx)
	if err != nil {
		t.Fatalf("load sync event user: %v", err)
	}
	if notifiedUser.ID != ownerA.ID {
		t.Errorf("expected the queue owner to be notified, got user %d", notifiedUser.ID)
	}
	if events[0].EventType != syncevent.EventTypeLidarrArtistSubmitted {
		t.Errorf("unexpected event type %s", events[0].EventType)
	}
	storedItem, err := client.LidarrQueue.Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("get queue item: %v", err)
	}
	if storedItem.Status != lidarrqueue.StatusSubmitted {
		t.Errorf("expected status submitted, got %s", storedItem.Status)
	}
}

// TestIsPermanentLidarrError_Ownership covers the classification added for the
// fail-closed paths: retrying an ownership mismatch can never succeed.
func TestIsPermanentLidarrError_Ownership(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"album 7 does not belong to the queue owner (user 3)", true},
		{"queue row has no owner (item 12): no user", true},
		{"context deadline exceeded", false},
	}
	for _, tc := range cases {
		if got := isPermanentLidarrError(tc.msg); got != tc.want {
			t.Errorf("isPermanentLidarrError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}
