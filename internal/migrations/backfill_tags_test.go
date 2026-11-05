package migrations

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"

	"spotter/ent"
	"spotter/ent/enttest"
	"spotter/ent/tag"
	"spotter/internal/database"

	_ "github.com/mattn/go-sqlite3"
)

// setupTestDB creates an in-memory SQLite ent client + raw sql.DB with the entity_tags table.
func setupTestDB(t *testing.T) (*ent.Client, *sql.DB) {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if err := database.CreateEntityTagsTable(context.Background(), "sqlite3", db); err != nil {
		t.Fatal(err)
	}

	return client, db
}

func TestBackfillTags_ArtistGenres(t *testing.T) {
	client, db := setupTestDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create user and artist with genres
	u := client.User.Create().SetUsername("testuser").SetTheme("dark").SaveX(ctx)
	client.Artist.Create().
		SetName("My Bloody Valentine").
		SetUser(u).
		SetGenres([]string{"shoegaze", "dream pop"}).
		SetTags([]string{"noise pop"}).
		SetAiTags([]string{"ethereal"}).
		SaveX(ctx)

	result, err := BackfillTags(ctx, client, db, logger)
	if err != nil {
		t.Fatalf("BackfillTags failed: %v", err)
	}

	if result.ArtistsProcessed != 1 {
		t.Errorf("ArtistsProcessed = %d, want 1", result.ArtistsProcessed)
	}
	if result.Errors != 0 {
		t.Errorf("Errors = %d, want 0", result.Errors)
	}

	// Verify genre tags created
	genreTags, err := client.Tag.Query().Where(tag.TagTypeEQ(tag.TagTypeGenre)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(genreTags) != 2 {
		t.Errorf("genre tags = %d, want 2", len(genreTags))
	}

	// Verify id3 tag
	id3Tags, err := client.Tag.Query().Where(tag.TagTypeEQ(tag.TagTypeId3)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(id3Tags) != 1 {
		t.Errorf("id3 tags = %d, want 1", len(id3Tags))
	}

	// Verify ai tag
	aiTags, err := client.Tag.Query().Where(tag.TagTypeEQ(tag.TagTypeAi)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(aiTags) != 1 {
		t.Errorf("ai tags = %d, want 1", len(aiTags))
	}
}

func TestBackfillTags_AlbumFields(t *testing.T) {
	client, db := setupTestDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	u := client.User.Create().SetUsername("testuser").SetTheme("dark").SaveX(ctx)
	artist := client.Artist.Create().SetName("Radiohead").SetUser(u).SaveX(ctx)
	client.Album.Create().
		SetName("OK Computer").
		SetUser(u).
		SetArtist(artist).
		SetGenre("alternative rock").
		SetTags([]string{"rock", "experimental"}).
		SetAiTags([]string{"complex"}).
		SetLabel("Parlophone").
		SaveX(ctx)

	result, err := BackfillTags(ctx, client, db, logger)
	if err != nil {
		t.Fatalf("BackfillTags failed: %v", err)
	}

	if result.AlbumsProcessed != 1 {
		t.Errorf("AlbumsProcessed = %d, want 1", result.AlbumsProcessed)
	}

	// genre (1) + id3 (2) + ai (1) + label (1) = 5 tags total
	allTags, err := client.Tag.Query().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(allTags) != 5 {
		t.Errorf("total tags = %d, want 5", len(allTags))
	}

	// Verify label tag
	labelTags, err := client.Tag.Query().Where(tag.TagTypeEQ(tag.TagTypeLabel)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(labelTags) != 1 {
		t.Errorf("label tags = %d, want 1", len(labelTags))
	}
	if labelTags[0].NormalizedName != "parlophone" {
		t.Errorf("label tag name = %q, want %q", labelTags[0].NormalizedName, "parlophone")
	}
}

func TestBackfillTags_Idempotent(t *testing.T) {
	client, db := setupTestDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	u := client.User.Create().SetUsername("testuser").SetTheme("dark").SaveX(ctx)
	client.Artist.Create().
		SetName("Slowdive").
		SetUser(u).
		SetGenres([]string{"shoegaze"}).
		SaveX(ctx)

	// Run first time
	result1, err := BackfillTags(ctx, client, db, logger)
	if err != nil {
		t.Fatalf("first BackfillTags failed: %v", err)
	}
	if result1.Errors != 0 {
		t.Errorf("first run errors = %d, want 0", result1.Errors)
	}

	countBefore, _ := client.Tag.Query().Count(ctx)

	// Run second time — should be idempotent
	result2, err := BackfillTags(ctx, client, db, logger)
	if err != nil {
		t.Fatalf("second BackfillTags failed: %v", err)
	}
	if result2.Errors != 0 {
		t.Errorf("second run errors = %d, want 0", result2.Errors)
	}

	countAfter, _ := client.Tag.Query().Count(ctx)
	if countAfter != countBefore {
		t.Errorf("tag count changed: before=%d, after=%d (should be idempotent)", countBefore, countAfter)
	}

	// Verify entity_tags row count didn't change
	var etCountBefore, etCountAfter int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM entity_tags").Scan(&etCountBefore)

	// Third run
	_, err = BackfillTags(ctx, client, db, logger)
	if err != nil {
		t.Fatalf("third BackfillTags failed: %v", err)
	}
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM entity_tags").Scan(&etCountAfter)
	if etCountAfter != etCountBefore {
		t.Errorf("entity_tags count changed: before=%d, after=%d", etCountBefore, etCountAfter)
	}
}

func TestBackfillTags_EmptyFields(t *testing.T) {
	client, db := setupTestDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	u := client.User.Create().SetUsername("testuser").SetTheme("dark").SaveX(ctx)
	// Artist with no tags at all
	client.Artist.Create().SetName("Unknown Artist").SetUser(u).SaveX(ctx)
	// Album with empty genre and no tags
	artist := client.Artist.Create().SetName("Another Artist").SetUser(u).SaveX(ctx)
	client.Album.Create().SetName("Empty Album").SetUser(u).SetArtist(artist).SaveX(ctx)

	result, err := BackfillTags(ctx, client, db, logger)
	if err != nil {
		t.Fatalf("BackfillTags failed: %v", err)
	}

	if result.Errors != 0 {
		t.Errorf("Errors = %d, want 0", result.Errors)
	}

	count, _ := client.Tag.Query().Count(ctx)
	if count != 0 {
		t.Errorf("tag count = %d, want 0 (no legacy data)", count)
	}
}

func TestBackfillTags_TrackFields(t *testing.T) {
	client, db := setupTestDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	u := client.User.Create().SetUsername("testuser").SetTheme("dark").SaveX(ctx)
	artist := client.Artist.Create().SetName("Boards of Canada").SetUser(u).SaveX(ctx)
	album := client.Album.Create().SetName("Music Has the Right to Children").SetUser(u).SetArtist(artist).SaveX(ctx)
	client.Track.Create().
		SetName("Roygbiv").
		SetArtist(artist).
		SetAlbum(album).
		SetGenres([]string{"electronic", "idm"}).
		SetTags([]string{"ambient"}).
		SetAiTags([]string{"nostalgic", "warm"}).
		SaveX(ctx)

	result, err := BackfillTags(ctx, client, db, logger)
	if err != nil {
		t.Fatalf("BackfillTags failed: %v", err)
	}

	if result.TracksProcessed != 1 {
		t.Errorf("TracksProcessed = %d, want 1", result.TracksProcessed)
	}

	// genre (2) + id3 (1) + ai (2) = 5 tags
	allTags, err := client.Tag.Query().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(allTags) != 5 {
		t.Errorf("total tags = %d, want 5", len(allTags))
	}
}

func TestBackfillTags_AllFieldMappings(t *testing.T) {
	client, db := setupTestDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	u := client.User.Create().SetUsername("testuser").SetTheme("dark").SaveX(ctx)
	artist := client.Artist.Create().
		SetName("Test Artist").
		SetUser(u).
		SetGenres([]string{"rock"}).
		SetTags([]string{"classic"}).
		SetAiTags([]string{"energetic"}).
		SaveX(ctx)
	album := client.Album.Create().
		SetName("Test Album").
		SetUser(u).
		SetArtist(artist).
		SetGenre("alternative").
		SetTags([]string{"indie"}).
		SetAiTags([]string{"mellow"}).
		SetLabel("Sub Pop").
		SaveX(ctx)
	client.Track.Create().
		SetName("Test Track").
		SetArtist(artist).
		SetAlbum(album).
		SetGenres([]string{"post-rock"}).
		SetTags([]string{"instrumental"}).
		SetAiTags([]string{"atmospheric"}).
		SaveX(ctx)

	result, err := BackfillTags(ctx, client, db, logger)
	if err != nil {
		t.Fatalf("BackfillTags failed: %v", err)
	}

	// Verify all 10 field mappings produced tags:
	// Artist: genres(1) + tags(1) + ai_tags(1) = 3
	// Album: genre(1) + tags(1) + ai_tags(1) + label(1) = 4
	// Track: genres(1) + tags(1) + ai_tags(1) = 3
	// Total unique tags = 10 (all have different names)
	allTags, err := client.Tag.Query().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(allTags) != 10 {
		t.Errorf("total tags = %d, want 10 (all 10 field mappings)", len(allTags))
	}

	// Verify entity_tags denormalized rows
	var etCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM entity_tags").Scan(&etCount); err != nil {
		t.Fatal(err)
	}
	if etCount != 10 {
		t.Errorf("entity_tags rows = %d, want 10", etCount)
	}

	if result.ArtistsProcessed != 1 {
		t.Errorf("ArtistsProcessed = %d, want 1", result.ArtistsProcessed)
	}
	if result.AlbumsProcessed != 1 {
		t.Errorf("AlbumsProcessed = %d, want 1", result.AlbumsProcessed)
	}
	if result.TracksProcessed != 1 {
		t.Errorf("TracksProcessed = %d, want 1", result.TracksProcessed)
	}
	if result.Errors != 0 {
		t.Errorf("Errors = %d, want 0", result.Errors)
	}
}

// TestBackfillTags_SecondRunReportsUpToDate covers the rerun contract the
// spotter-admin backfill-tags verb reports to operators: a second run migrates
// nothing, says so, and leaves every row untouched.
// Governing: SPEC-0018 REQ "Data Migration"
func TestBackfillTags_SecondRunReportsUpToDate(t *testing.T) {
	client, db := setupTestDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	u := client.User.Create().SetUsername("rerunner").SetTheme("dark").SaveX(ctx)
	client.Artist.Create().
		SetName("Slowdive").
		SetUser(u).
		SetGenres([]string{"shoegaze"}).
		SetTags([]string{"dream pop"}).
		SaveX(ctx)
	client.Artist.Create().
		SetName("Nothing Legacy").
		SetUser(u).
		SaveX(ctx)

	first, err := BackfillTags(ctx, client, db, logger)
	if err != nil {
		t.Fatalf("first BackfillTags failed: %v", err)
	}
	if first.EntitiesMigrated != 1 {
		t.Errorf("first run EntitiesMigrated = %d, want 1", first.EntitiesMigrated)
	}
	if first.EntitiesUpToDate != 1 {
		t.Errorf("first run EntitiesUpToDate = %d, want 1 (the artist with no legacy tags)", first.EntitiesUpToDate)
	}
	if first.Errors != 0 {
		t.Errorf("first run Errors = %d, want 0", first.Errors)
	}

	tagCount := countRows(t, ctx, db, "tags")
	entityTagCount := countRows(t, ctx, db, "entity_tags")

	second, err := BackfillTags(ctx, client, db, logger)
	if err != nil {
		t.Fatalf("second BackfillTags failed: %v", err)
	}
	if second.EntitiesMigrated != 0 {
		t.Errorf("second run EntitiesMigrated = %d, want 0", second.EntitiesMigrated)
	}
	if second.EntitiesUpToDate != 2 {
		t.Errorf("second run EntitiesUpToDate = %d, want 2", second.EntitiesUpToDate)
	}
	if second.Errors != 0 {
		t.Errorf("second run Errors = %d, want 0", second.Errors)
	}

	if got := countRows(t, ctx, db, "tags"); got != tagCount {
		t.Errorf("tags after rerun = %d, want %d (no duplicates)", got, tagCount)
	}
	if got := countRows(t, ctx, db, "entity_tags"); got != entityTagCount {
		t.Errorf("entity_tags after rerun = %d, want %d (no duplicates)", got, entityTagCount)
	}
}

func countRows(t *testing.T, ctx context.Context, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
