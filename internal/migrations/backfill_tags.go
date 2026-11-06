// Governing: SPEC-0018 REQ "Data Migration", ADR-0025 (Unified Tag Taxonomy), ADR-0023 (PostgreSQL)
package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"spotter/ent"
	entartist "spotter/ent/artist"
	"spotter/ent/track"
	"spotter/ent/user"
	"spotter/internal/tags"
)

// BackfillTagsResult holds statistics from the migration run.
type BackfillTagsResult struct {
	ArtistsProcessed int
	AlbumsProcessed  int
	TracksProcessed  int
	Errors           int

	// EntitiesMigrated counts entities whose typed tags this run wrote;
	// EntitiesUpToDate counts entities entity_tags already associated. A rerun
	// after a finished migration therefore reports zero migrated and every
	// entity up to date.
	// Governing: SPEC-0018 REQ "Data Migration"
	EntitiesMigrated int
	EntitiesUpToDate int
}

// BackfillTags reads all legacy JSON tag fields (tags, genres, ai_tags, genre, label)
// from existing Artist, Album, and Track entities and populates the new Tag entity
// and entity_tags denormalized table. Idempotent: safe to run multiple times.
// Governing: SPEC-0018 REQ "Data Migration"
func BackfillTags(ctx context.Context, client *ent.Client, db *sql.DB, logger *slog.Logger) (*BackfillTagsResult, error) {
	result := &BackfillTagsResult{}

	users, err := client.User.Query().All(ctx)
	if err != nil {
		return nil, err
	}

	for _, u := range users {
		logger.Info("backfilling tags for user", "user_id", u.ID, "username", u.Username)

		// One pass over entity_tags per user tells the per-entity loops which
		// entities a previous run already finished, so a rerun neither rewrites
		// them nor claims to have migrated them.
		existing, err := loadEntityTagIndex(ctx, db, u.ID)
		if err != nil {
			logger.Error("error reading existing tag associations", "user_id", u.ID, "error", err)
			result.Errors++
			continue
		}

		if err := backfillArtistTags(ctx, client, db, u.ID, logger, result, existing); err != nil {
			logger.Error("error backfilling artist tags", "user_id", u.ID, "error", err)
			result.Errors++
		}

		if err := backfillAlbumTags(ctx, client, db, u.ID, logger, result, existing); err != nil {
			logger.Error("error backfilling album tags", "user_id", u.ID, "error", err)
			result.Errors++
		}

		if err := backfillTrackTags(ctx, client, db, u.ID, logger, result, existing); err != nil {
			logger.Error("error backfilling track tags", "user_id", u.ID, "error", err)
			result.Errors++
		}
	}

	logger.Info("tag backfill complete",
		"artists", result.ArtistsProcessed,
		"albums", result.AlbumsProcessed,
		"tracks", result.TracksProcessed,
		"migrated", result.EntitiesMigrated,
		"up_to_date", result.EntitiesUpToDate,
		"errors", result.Errors,
	)

	return result, nil
}

// backfillArtistTags migrates legacy artist fields: genres -> genre, tags -> id3, ai_tags -> ai
func backfillArtistTags(ctx context.Context, client *ent.Client, db *sql.DB, userID int, logger *slog.Logger, result *BackfillTagsResult, existing *entityTagIndex) error {
	artists, err := client.Artist.Query().
		Where(entartist.HasUserWith(user.IDEQ(userID))).
		All(ctx)
	if err != nil {
		return err
	}

	for _, a := range artists {
		result.ArtistsProcessed++

		var typed []tags.TypedTag

		// Artist.genres -> tag_type "genre"
		for _, g := range a.Genres {
			typed = append(typed, tags.TypedTag{Name: g, Type: "genre"})
		}

		// Artist.tags -> tag_type "id3"
		for _, t := range a.Tags {
			typed = append(typed, tags.TypedTag{Name: t, Type: "id3"})
		}

		// Artist.ai_tags -> tag_type "ai"
		for _, t := range a.AiTags {
			typed = append(typed, tags.TypedTag{Name: t, Type: "ai"})
		}

		if len(typed) == 0 || existing.hasAll("artist", a.ID, typed) {
			result.EntitiesUpToDate++
			continue
		}

		if err := tags.UpsertTagsForEntity(ctx, client, db, userID, "artist", a.ID, typed); err != nil {
			logger.Error("error upserting artist tags", "artist_id", a.ID, "error", err)
			result.Errors++
			continue
		}
		result.EntitiesMigrated++
	}

	return nil
}

// backfillAlbumTags migrates legacy album fields: genre -> genre, tags -> id3, ai_tags -> ai, label -> label
func backfillAlbumTags(ctx context.Context, client *ent.Client, db *sql.DB, userID int, logger *slog.Logger, result *BackfillTagsResult, existing *entityTagIndex) error {
	albums, err := client.User.QueryAlbums(client.User.GetX(ctx, userID)).All(ctx)
	if err != nil {
		return err
	}

	for _, a := range albums {
		result.AlbumsProcessed++

		var typed []tags.TypedTag

		// Album.genre (single string) -> tag_type "genre"
		if a.Genre != "" {
			typed = append(typed, tags.TypedTag{Name: a.Genre, Type: "genre"})
		}

		// Album.tags -> tag_type "id3"
		for _, t := range a.Tags {
			typed = append(typed, tags.TypedTag{Name: t, Type: "id3"})
		}

		// Album.ai_tags -> tag_type "ai"
		for _, t := range a.AiTags {
			typed = append(typed, tags.TypedTag{Name: t, Type: "ai"})
		}

		// Album.label -> tag_type "label"
		if a.Label != "" {
			typed = append(typed, tags.TypedTag{Name: a.Label, Type: "label"})
		}

		if len(typed) == 0 || existing.hasAll("album", a.ID, typed) {
			result.EntitiesUpToDate++
			continue
		}

		if err := tags.UpsertTagsForEntity(ctx, client, db, userID, "album", a.ID, typed); err != nil {
			logger.Error("error upserting album tags", "album_id", a.ID, "error", err)
			result.Errors++
			continue
		}
		result.EntitiesMigrated++
	}

	return nil
}

// backfillTrackTags migrates legacy track fields: genres -> genre, tags -> id3, ai_tags -> ai
func backfillTrackTags(ctx context.Context, client *ent.Client, db *sql.DB, userID int, logger *slog.Logger, result *BackfillTagsResult, existing *entityTagIndex) error {
	tracks, err := client.Track.Query().
		Where(track.HasArtistWith(entartist.HasUserWith(user.IDEQ(userID)))).
		All(ctx)
	if err != nil {
		return err
	}

	for _, t := range tracks {
		result.TracksProcessed++

		var typed []tags.TypedTag

		// Track.genres -> tag_type "genre"
		for _, g := range t.Genres {
			typed = append(typed, tags.TypedTag{Name: g, Type: "genre"})
		}

		// Track.tags -> tag_type "id3"
		for _, tg := range t.Tags {
			typed = append(typed, tags.TypedTag{Name: tg, Type: "id3"})
		}

		// Track.ai_tags -> tag_type "ai"
		for _, tg := range t.AiTags {
			typed = append(typed, tags.TypedTag{Name: tg, Type: "ai"})
		}

		if len(typed) == 0 || existing.hasAll("track", t.ID, typed) {
			result.EntitiesUpToDate++
			continue
		}

		if err := tags.UpsertTagsForEntity(ctx, client, db, userID, "track", t.ID, typed); err != nil {
			logger.Error("error upserting track tags", "track_id", t.ID, "error", err)
			result.Errors++
			continue
		}
		result.EntitiesMigrated++
	}

	return nil
}

// entityTagRef is one entity-to-tag association as the entity_tags table
// stores it: tag_name holds the normalized name UpsertTagsForEntity wrote.
type entityTagRef struct {
	entityType string
	entityID   int
	tagType    string
	tagName    string
}

// entityTagIndex is the set of associations entity_tags already holds for one
// user, which is what lets the backfill tell an entity it still has to migrate
// from one an earlier run finished.
// Governing: SPEC-0018 REQ "Data Migration"
type entityTagIndex struct {
	refs map[entityTagRef]bool
}

// loadEntityTagIndex reads every entity_tags row for the user. The user id
// comes from a row this function already read rather than from operator input,
// and it is interpolated so the statement runs unchanged on every driver
// (ADR-0023): entity_tags is a raw table, so there is no Ent query builder to
// lean on, and placeholder syntax differs between drivers.
func loadEntityTagIndex(ctx context.Context, db *sql.DB, userID int) (*entityTagIndex, error) {
	rows, err := db.QueryContext(ctx,
		fmt.Sprintf("SELECT entity_type, entity_id, tag_type, tag_name FROM entity_tags WHERE user_id = %d", userID))
	if err != nil {
		return nil, fmt.Errorf("query entity_tags for user %d: %w", userID, err)
	}
	defer func() { _ = rows.Close() }()

	index := &entityTagIndex{refs: make(map[entityTagRef]bool)}
	for rows.Next() {
		var ref entityTagRef
		if err := rows.Scan(&ref.entityType, &ref.entityID, &ref.tagType, &ref.tagName); err != nil {
			return nil, fmt.Errorf("scan entity_tags for user %d: %w", userID, err)
		}
		index.refs[ref] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entity_tags for user %d: %w", userID, err)
	}
	return index, nil
}

// hasAll reports whether every typed tag is already associated with the entity.
func (i *entityTagIndex) hasAll(entityType string, entityID int, typed []tags.TypedTag) bool {
	for _, tt := range typed {
		normalized := tags.Normalize(tt.Name)
		if normalized == "" {
			continue
		}
		if !i.refs[entityTagRef{entityType: entityType, entityID: entityID, tagType: tt.Type, tagName: normalized}] {
			return false
		}
	}
	return true
}
