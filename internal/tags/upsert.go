// Governing: SPEC-0014 REQ "Enricher Integration", SPEC-0014 REQ "Denormalized Entity Tags Table",
// SPEC-0014 REQ "Tag Normalization"
package tags

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"spotter/ent"
	"spotter/ent/tag"
	"spotter/ent/user"
)

// TypedTag represents a tag with a specific type classification.
// Governing: SPEC-0014 REQ "Enricher Integration"
type TypedTag struct {
	Name string
	Type string // "id3", "genre", "ai", "label", "source"
}

// UpsertTagsForEntity creates or retrieves Tag entities for the given typed tags,
// associates them with the specified entity via Ent edges, and maintains the
// denormalized entity_tags table. Idempotent: safe to call multiple times, and
// safe to call concurrently for the same tag.
// Governing: SPEC-0014 REQ "Enricher Integration", SPEC-0014 REQ "Denormalized Entity Tags Table"
func UpsertTagsForEntity(ctx context.Context, client *ent.Client, db *sql.DB, userID int, entityType string, entityID int, typed []TypedTag) error {
	for _, tt := range typed {
		normalized := Normalize(tt.Name)
		if normalized == "" {
			continue
		}
		displayName := strings.TrimSpace(tt.Name)

		tagType := tag.TagType(tt.Type)

		t, err := findOrCreateTag(ctx, client, userID, displayName, normalized, tagType)
		if err != nil {
			return err
		}

		// Add entity to the tag's edge (idempotent — Ent ignores duplicate edges)
		if err := addEntityEdge(ctx, t, entityType, entityID, normalized); err != nil {
			return err
		}

		// Upsert into denormalized entity_tags table (ON CONFLICT DO NOTHING for idempotency)
		if err := upsertEntityTag(ctx, db, userID, t.ID, tt.Type, normalized, entityType, entityID); err != nil {
			return fmt.Errorf("upsert entity_tag for tag %q: %w", normalized, err)
		}
	}
	return nil
}

// findOrCreateTag resolves the Tag row for (normalized, tagType, userID),
// creating it when absent.
//
// The lookup and the create are separate statements against the unique
// (normalized_name, tag_type, user_id) index, so two concurrent enrichment
// goroutines can both miss the lookup and one of them loses the create. That
// loser treats the constraint violation as "someone else made this tag" and
// re-reads the winning row; only a create failure that is not a constraint
// violation, or a re-read that still finds nothing, fails the upsert.
// Governing: SPEC-0014 REQ "Tag Entity Schema" (unique index), issue #349
func findOrCreateTag(ctx context.Context, client *ent.Client, userID int, displayName, normalized string, tagType tag.TagType) (*ent.Tag, error) {
	t, err := findTag(ctx, client, userID, normalized, tagType)
	if err == nil {
		return t, nil
	}
	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("query tag %q (type %s): %w", normalized, tagType, err)
	}

	t, err = client.Tag.Create().
		// The display name keeps its original casing but is trimmed, so
		// "  shoegaze  " is stored as "shoegaze" and never as a second
		// display variant of the tag Normalize already folded onto.
		// Governing: SPEC-0014 REQ "Tag Normalization"
		SetName(displayName).
		SetNormalizedName(normalized).
		SetTagType(tagType).
		SetUserID(userID).
		Save(ctx)
	if err == nil {
		return t, nil
	}
	if !ent.IsConstraintError(err) {
		return nil, fmt.Errorf("create tag %q (type %s): %w", displayName, tagType, err)
	}

	t, err = findTag(ctx, client, userID, normalized, tagType)
	if err != nil {
		return nil, fmt.Errorf("re-read tag %q (type %s) after create conflict: %w", normalized, tagType, err)
	}
	return t, nil
}

// findTag looks up the Tag row for (normalized, tagType, userID).
func findTag(ctx context.Context, client *ent.Client, userID int, normalized string, tagType tag.TagType) (*ent.Tag, error) {
	return client.Tag.Query().
		Where(
			tag.NormalizedNameEQ(normalized),
			tag.TagTypeEQ(tagType),
			tag.HasUserWith(user.IDEQ(userID)),
		).
		Only(ctx)
}

// addEntityEdge attaches the entity to the tag's edge for the given entity type.
func addEntityEdge(ctx context.Context, t *ent.Tag, entityType string, entityID int, normalized string) error {
	var err error
	switch entityType {
	case "artist":
		err = t.Update().AddArtistIDs(entityID).Exec(ctx)
	case "album":
		err = t.Update().AddAlbumIDs(entityID).Exec(ctx)
	case "track":
		err = t.Update().AddTrackIDs(entityID).Exec(ctx)
	default:
		return fmt.Errorf("unknown entity type: %s", entityType)
	}
	if err != nil {
		return fmt.Errorf("add %s edge for tag %q: %w", entityType, normalized, err)
	}
	return nil
}

// upsertEntityTag inserts a row into entity_tags, ignoring conflicts for idempotency.
func upsertEntityTag(ctx context.Context, db *sql.DB, userID, tagID int, tagType, tagName, entityType string, entityID int) error {
	// Use INSERT ... ON CONFLICT DO NOTHING for idempotency.
	// This works for both PostgreSQL and SQLite.
	_, err := db.ExecContext(ctx,
		`INSERT INTO entity_tags (user_id, tag_id, tag_type, tag_name, entity_type, entity_id)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (tag_id, entity_type, entity_id) DO NOTHING`,
		userID, tagID, tagType, tagName, entityType, entityID,
	)
	return err
}
