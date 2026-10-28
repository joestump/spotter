package tags

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"spotter/ent"
	"spotter/ent/enttest"
	"spotter/ent/hook"
	"spotter/ent/tag"
	"spotter/internal/database"

	_ "github.com/mattn/go-sqlite3"
)

// setupUpsertDB opens a file-backed SQLite ent client plus a raw connection with
// the entity_tags table, the same pairing production wiring builds in
// internal/database.NewClient.
func setupUpsertDB(t *testing.T) (*ent.Client, *sql.DB) {
	t.Helper()
	// A file database with WAL, not the shared-cache in-memory form: concurrent
	// writers against one table on a shared cache get SQLITE_LOCKED without
	// consulting the busy timeout, which would test SQLite's locking instead of
	// the tag-create race under test.
	dsn := fmt.Sprintf("file:%s?_fk=1&_journal_mode=WAL&_busy_timeout=15000", filepath.Join(t.TempDir(), "tags.db"))
	client := enttest.Open(t, "sqlite3", dsn)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := database.CreateEntityTagsTable(context.Background(), "sqlite3", db); err != nil {
		t.Fatal(err)
	}
	return client, db
}

func upsertTestUser(t *testing.T, client *ent.Client, name string) *ent.User {
	t.Helper()
	return client.User.Create().SetUsername(name).SetTheme("dark").SaveX(context.Background())
}

func countTags(t *testing.T, client *ent.Client) int {
	t.Helper()
	n, err := client.Tag.Query().Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func countEntityTagRows(t *testing.T, db *sql.DB, tagID int, entityType string, entityID int) int {
	t.Helper()
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM entity_tags WHERE tag_id = ? AND entity_type = ? AND entity_id = ?`,
		tagID, entityType, entityID,
	).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUpsertTagsForEntityTrimsDisplayName(t *testing.T) {
	client, db := setupUpsertDB(t)
	ctx := context.Background()
	u := upsertTestUser(t, client, "trim@example.invalid")
	a := client.Artist.Create().SetName("My Bloody Valentine").SetUser(u).SaveX(ctx)

	if err := UpsertTagsForEntity(ctx, client, db, u.ID, "artist", a.ID,
		[]TypedTag{{Name: "  shoegaze  ", Type: "genre"}}); err != nil {
		t.Fatalf("UpsertTagsForEntity: %v", err)
	}

	tagRow := client.Tag.Query().OnlyX(ctx)
	if got, want := tagRow.Name, "shoegaze"; got != want {
		t.Errorf("display name = %q, want %q", got, want)
	}
	if got, want := tagRow.NormalizedName, "shoegaze"; got != want {
		t.Errorf("normalized name = %q, want %q", got, want)
	}

	var projectedName string
	if err := db.QueryRow(`SELECT tag_name FROM entity_tags WHERE tag_id = ?`, tagRow.ID).Scan(&projectedName); err != nil {
		t.Fatal(err)
	}
	if got, want := projectedName, "shoegaze"; got != want {
		t.Errorf("entity_tags.tag_name = %q, want %q", got, want)
	}
}

func TestUpsertTagsForEntityKeepsOriginalCasing(t *testing.T) {
	client, db := setupUpsertDB(t)
	ctx := context.Background()
	u := upsertTestUser(t, client, "case@example.invalid")
	a := client.Artist.Create().SetName("Slowdive").SetUser(u).SaveX(ctx)

	if err := UpsertTagsForEntity(ctx, client, db, u.ID, "artist", a.ID,
		[]TypedTag{{Name: " Dream Pop ", Type: "genre"}}); err != nil {
		t.Fatalf("UpsertTagsForEntity: %v", err)
	}

	tagRow := client.Tag.Query().OnlyX(ctx)
	if got, want := tagRow.Name, "Dream Pop"; got != want {
		t.Errorf("display name = %q, want %q", got, want)
	}
	if got, want := tagRow.NormalizedName, "dream pop"; got != want {
		t.Errorf("normalized name = %q, want %q", got, want)
	}
}

func TestUpsertTagsForEntitySkipsBlankNames(t *testing.T) {
	client, db := setupUpsertDB(t)
	ctx := context.Background()
	u := upsertTestUser(t, client, "blank@example.invalid")
	a := client.Artist.Create().SetName("Ride").SetUser(u).SaveX(ctx)

	if err := UpsertTagsForEntity(ctx, client, db, u.ID, "artist", a.ID,
		[]TypedTag{{Name: "", Type: "genre"}, {Name: "   ", Type: "genre"}, {Name: "\t\n", Type: "ai"}}); err != nil {
		t.Fatalf("UpsertTagsForEntity: %v", err)
	}

	if n := countTags(t, client); n != 0 {
		t.Errorf("tag count = %d, want 0 for blank names", n)
	}
}

func TestUpsertTagsForEntityIsIdempotent(t *testing.T) {
	client, db := setupUpsertDB(t)
	ctx := context.Background()
	u := upsertTestUser(t, client, "idem@example.invalid")
	a := client.Artist.Create().SetName("Lush").SetUser(u).SaveX(ctx)

	typed := []TypedTag{{Name: "shoegaze", Type: "genre"}, {Name: "ethereal", Type: "ai"}}
	for i := 0; i < 3; i++ {
		if err := UpsertTagsForEntity(ctx, client, db, u.ID, "artist", a.ID, typed); err != nil {
			t.Fatalf("UpsertTagsForEntity call %d: %v", i+1, err)
		}
	}

	if n := countTags(t, client); n != 2 {
		t.Fatalf("tag count = %d, want 2", n)
	}
	for _, tt := range typed {
		row := client.Tag.Query().Where(tag.NormalizedNameEQ(tt.Name)).OnlyX(ctx)
		if n := countEntityTagRows(t, db, row.ID, "artist", a.ID); n != 1 {
			t.Errorf("entity_tags rows for %q = %d, want 1", tt.Name, n)
		}
	}
}

// TestUpsertTagsForEntityConcurrentSameTag covers the query-then-create race:
// every writer must miss the lookup and then contend for the unique-index
// create, so all but one writer takes the conflict path. The hook is the
// barrier that makes the interleaving deterministic: writers queue at their
// Tag create and none may insert until all of them are queued, which means
// every lookup has already run against an empty table.
func TestUpsertTagsForEntityConcurrentSameTag(t *testing.T) {
	client, db := setupUpsertDB(t)
	ctx := context.Background()
	u := upsertTestUser(t, client, "race@example.invalid")
	a := client.Artist.Create().SetName("Chapterhouse").SetUser(u).SaveX(ctx)

	const writers = 4
	var arrived int64
	var once sync.Once
	release := make(chan struct{})
	client.Use(hook.On(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Type() == "Tag" {
				if atomic.AddInt64(&arrived, 1) >= writers {
					once.Do(func() { close(release) })
				} else {
					select {
					case <-release:
					case <-time.After(10 * time.Second):
						t.Error("tag create barrier never released")
					}
				}
			}
			return next.Mutate(ctx, m)
		})
	}, ent.OpCreate))

	var wg sync.WaitGroup
	errs := make([]error, writers)
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			defer wg.Done()
			errs[i] = UpsertTagsForEntity(ctx, client, db, u.ID, "artist", a.ID,
				[]TypedTag{{Name: "Shoegaze", Type: "genre"}})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("writer %d failed: %v", i, err)
		}
	}
	if n := countTags(t, client); n != 1 {
		t.Fatalf("tag count = %d, want 1", n)
	}
	row := client.Tag.Query().OnlyX(ctx)
	if n := countEntityTagRows(t, db, row.ID, "artist", a.ID); n != 1 {
		t.Errorf("entity_tags rows = %d, want 1", n)
	}
}

func TestUpsertTagsForEntityCoversEveryEntityType(t *testing.T) {
	client, db := setupUpsertDB(t)
	ctx := context.Background()
	u := upsertTestUser(t, client, "types@example.invalid")
	a := client.Artist.Create().SetName("Curve").SetUser(u).SaveX(ctx)
	al := client.Album.Create().SetName("Dunnowhere").SetArtist(a).SetUser(u).SaveX(ctx)
	tr := client.Track.Create().SetName("Faults").SetArtist(a).SetAlbum(al).SaveX(ctx)

	cases := []struct {
		entityType string
		entityID   int
	}{
		{entityType: "artist", entityID: a.ID},
		{entityType: "album", entityID: al.ID},
		{entityType: "track", entityID: tr.ID},
	}
	for _, tc := range cases {
		if err := UpsertTagsForEntity(ctx, client, db, u.ID, tc.entityType, tc.entityID,
			[]TypedTag{{Name: "ambient", Type: "ai"}}); err != nil {
			t.Fatalf("UpsertTagsForEntity(%s): %v", tc.entityType, err)
		}
	}

	row := client.Tag.Query().Where(tag.NormalizedNameEQ("ambient")).OnlyX(ctx)
	artists, err := row.QueryArtists().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	albums, err := row.QueryAlbums().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tracks, err := row.QueryTracks().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for kind, n := range map[string]int{"artist": artists, "album": albums, "track": tracks} {
		if n != 1 {
			t.Errorf("%s edges = %d, want 1", kind, n)
		}
	}
	for _, tc := range cases {
		if n := countEntityTagRows(t, db, row.ID, tc.entityType, tc.entityID); n != 1 {
			t.Errorf("entity_tags rows for %s = %d, want 1", tc.entityType, n)
		}
	}
}

func TestUpsertTagsForEntityRejectsUnknownEntityType(t *testing.T) {
	client, db := setupUpsertDB(t)
	ctx := context.Background()
	u := upsertTestUser(t, client, "unknown@example.invalid")

	err := UpsertTagsForEntity(ctx, client, db, u.ID, "playlist", 1,
		[]TypedTag{{Name: "misfiled", Type: "ai"}})
	if err == nil {
		t.Fatal("expected error for unknown entity type, got nil")
	}
}
