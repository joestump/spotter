package services

import (
	"context"

	"spotter/internal/tags"
)

// PersistTypedTags writes one entity's typed tags into the unified tag taxonomy:
// the Tag rows, their entity edges, and the denormalized entity_tags rows.
//
// This is the one write path for tags. Both the background enrichment cycle and
// the per-entity "regenerate AI" handlers call it, so a tag is visible to tag
// browsing and filtering as soon as it is produced, instead of waiting for the
// next enrichment cycle. It is idempotent, and safe to call concurrently for
// the same tag.
//
// Governing: SPEC-0014 REQ "Enricher Integration",
// SPEC-0014 REQ "Denormalized Entity Tags Table", ADR-0025, issue #349
func (s *MetadataService) PersistTypedTags(ctx context.Context, userID int, entityType string, entityID int, typed []tags.TypedTag) error {
	if len(typed) == 0 {
		return nil
	}
	return tags.UpsertTagsForEntity(ctx, s.client, s.db, userID, entityType, entityID, typed)
}
