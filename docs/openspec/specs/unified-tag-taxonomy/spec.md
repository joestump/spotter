---
status: proposed
date: 2026-02-28
decision-makers: joestump
---

# SPEC-0014: Unified Tag Taxonomy

## Overview

This specification defines the unified tag taxonomy system for Spotter, replacing the current
disjoint JSON array fields (`tags`, `genres`, `ai_tags`, `genre`, `label`) on Artist, Album, and
Track entities with a first-class `Tag` Ent entity, a typed five-value `tag_type` enum, a
denormalized `entity_tags` query table for fast multi-type filtering, and UI rendering conventions
that visually differentiate tag sources using DaisyUI badge styles and Heroicons.

Tags exist solely to classify and filter Artist, Album, and Track entities. Artists, Albums, and
Tracks are themselves not tag types.

See ADR-0025 for the architectural decision record.

## Requirements

### Requirement: Tag Entity Schema

The system SHALL define a `Tag` Ent entity in `ent/schema/tag.go` with the following fields:

- `name` (string, non-empty, max 255): display name of the tag as provided by the source
- `normalized_name` (string, non-empty, max 255): lowercase, whitespace-normalized form used for deduplication
- `tag_type` (enum): one of `id3`, `genre`, `ai`, `label`, `source`
- `created_at` (timestamp, immutable): set at creation

The `Tag` entity SHALL have a required `User` edge (scoped per user). The combination of
`(normalized_name, tag_type, user_id)` SHALL be unique — no two tags of the same type with the same
normalized name MAY exist for a single user.

#### Scenario: Tag creation stores normalized name

- **WHEN** a tag "Shoegaze" of type `genre` is created for a user
- **THEN** `name` is stored as `"Shoegaze"` and `normalized_name` is stored as `"shoegaze"`

#### Scenario: Duplicate tag is deduplicated

- **WHEN** a tag "dream pop" of type `genre` is created for a user who already has a `genre` tag with `normalized_name = "dream pop"`
- **THEN** the system returns the existing `Tag` entity rather than inserting a duplicate row

### Requirement: Tag Type Taxonomy

The system SHALL support exactly five tag types via the `tag_type` enum:

| Type | Sources | Meaning |
|------|---------|---------|
| `id3` | Navidrome sync, file metadata, Last.fm | Tags from audio file ID3/Vorbis metadata or social tagging |
| `genre` | Spotify, Last.fm, MusicBrainz, Navidrome | Genre classifications from music databases |
| `ai` | OpenAI enricher | AI-generated descriptive tags |
| `label` | Spotify, MusicBrainz | Record label associations |
| `source` | Navidrome, Spotify, Lidarr | Provider-specific categorization tags |

Artists, Albums, and Tracks are NOT tag types. Tags MUST NOT be used to represent entities —
they exist solely to filter and classify those entities.

#### Scenario: Source tag for provider categorization

- **WHEN** a provider enricher returns a provider-specific categorization string
- **THEN** the tag is stored with `tag_type = "source"`

#### Scenario: AI tag type preserved through pipeline

- **WHEN** the OpenAI enricher returns `AITags` for an album
- **THEN** each tag is stored with `tag_type = "ai"` and associated with the album

### Requirement: Tag-Entity Associations

The `Tag` entity SHALL have many-to-many Ent edges to `Artist`, `Album`, and `Track` entities.
A single `Tag` entity MAY be associated with multiple Artists, Albums, and Tracks. Enrichers
MUST reuse an existing `Tag` entity (matched by `normalized_name + tag_type + user_id`) when
associating tags rather than creating a new entity per association.

#### Scenario: Genre tag shared across artists

- **WHEN** both "My Bloody Valentine" and "Slowdive" are enriched with the `genre` tag "shoegaze"
- **THEN** both artists reference the same `Tag` entity row (same `id`) rather than two separate rows

#### Scenario: Tag association is created once

- **WHEN** an enricher associates a tag with an artist that already has that tag association
- **THEN** the junction table row is not duplicated (upsert semantics)

### Requirement: Denormalized Entity Tags Table

The system SHALL maintain a denormalized `entity_tags` table that provides fast filtered
lookups by tag type and entity type without requiring JOIN traversal through multiple junction
tables. The table SHALL contain:

- `user_id` (bigint, FK → users)
- `tag_id` (bigint, FK → tags, ON DELETE CASCADE)
- `tag_type` (varchar(20), denormalized from Tag)
- `tag_name` (varchar(255), denormalized `normalized_name` from Tag)
- `entity_type` (varchar(20)): `"artist"`, `"album"`, or `"track"`
- `entity_id` (bigint): FK to the respective entity table
- `created_at` (timestamptz, default NOW())

The combination `(tag_id, entity_type, entity_id)` SHALL be unique. The table SHALL have a
composite index on `(user_id, tag_type, tag_name, entity_type)` for filtered library queries.

The `entity_tags` table SHALL be kept in sync with the Ent junction tables. When a tag-entity
association is created via an Ent edge, the corresponding `entity_tags` row SHALL be written
immediately afterwards with `ON CONFLICT DO NOTHING` semantics, and a row found missing SHALL be
repaired by the next upsert for that entity. The edge write and the row write do not share a
transaction (ADR-0032). When an association is removed, the corresponding row SHALL be deleted in
the same change; no removal path exists in the code yet, so that clause is recorded as
unimplemented in ADR-0032.

#### Scenario: Filter artists by AI tag

- **WHEN** the library page requests all artists tagged with the `ai` tag "atmospheric"
- **THEN** the system queries `entity_tags WHERE user_id=? AND tag_type='ai' AND tag_name='atmospheric' AND entity_type='artist'` and returns matching artist IDs without JOIN through junction tables

#### Scenario: Tag deletion cascades to entity_tags

- **WHEN** a `Tag` entity is deleted
- **THEN** all `entity_tags` rows referencing that `tag_id` are automatically deleted via ON DELETE CASCADE

#### Scenario: Uniqueness prevents duplicate rows

- **WHEN** an enricher attempts to insert a duplicate `(tag_id, entity_type, entity_id)` row into `entity_tags`
- **THEN** the insert is a no-op (ON CONFLICT DO NOTHING semantics)

### Requirement: Tag Normalization

The system SHALL normalize tag names at write time before storing. Normalization SHALL:

1. Trim leading and trailing ASCII whitespace
2. Convert to lowercase (Unicode-aware)
3. Collapse internal whitespace runs to a single space

The `name` field SHALL store the trimmed original casing. The `normalized_name` field SHALL store
the fully normalized form. Normalization MUST be applied by the application layer before any Ent
upsert, not by database triggers.

#### Scenario: Mixed-case tag input is normalized

- **WHEN** an enricher provides the tag name `"Dream Pop"`
- **THEN** `name = "Dream Pop"` and `normalized_name = "dream pop"` are stored

#### Scenario: Whitespace-padded input is trimmed

- **WHEN** an enricher provides the tag name `"  shoegaze  "`
- **THEN** `name = "shoegaze"` (trimmed) and `normalized_name = "shoegaze"` are stored

### Requirement: Tag Relationships

The `Tag` entity SHALL support a self-referential many-to-many `related_tags` edge for
expressing semantic similarity between tags (e.g., "shoegaze" is related to "dream pop").

Tag relationships SHALL be undirected — if tag A is related to tag B, adding the relationship
from A SHALL also make B queryable as related to A.

Tag relationships are OPTIONAL and MAY be populated manually or via future automated tooling.
They are not required to be populated during initial migration.

#### Scenario: Related tag suggestions on browse

- **WHEN** a user browses the "shoegaze" genre tag page
- **THEN** the system queries `tag.related_tags` and returns associated tags (e.g., "dream pop", "noise pop") as browsing suggestions

#### Scenario: Relationship symmetry

- **WHEN** tag "shoegaze" is linked as related to tag "dream pop"
- **THEN** querying related tags for "dream pop" also returns "shoegaze" in the results

### Requirement: Enricher Integration

All enrichers in the metadata enrichment pipeline (ADR-0015) SHALL write tag data by creating
or upserting `Tag` entities and associating them with the relevant Artist, Album, or Track.
Enrichers SHALL NOT write to the legacy `tags`, `genres`, `ai_tags`, `genre`, or `label` JSON
fields on entity schemas after the migration phase is complete.

The enricher-to-tag-type mapping SHALL be:

| Enricher | Source Field | `tag_type` |
|----------|-------------|------------|
| Last.fm | `Tags` | `id3` |
| Spotify | `Genres` | `genre` |
| OpenAI | `AITags` | `ai` |
| Navidrome | `Genres` | `genre` |
| MusicBrainz | `Genres` | `genre` |
| Any enricher | `Label` (record label) | `label` |
| Any enricher | provider-specific categorization | `source` |

The `EnrichmentResult` types (`ArtistData`, `AlbumData`, `TrackData`) in
`internal/enrichers/enrichers.go` SHALL be updated to carry tag type information, or a new
`TaggedData` helper type SHALL be introduced so enrichers can express typed tags.

#### Scenario: Last.fm tags become id3 type

- **WHEN** the Last.fm enricher returns `Tags: ["post-punk", "gothic rock"]` for an artist
- **THEN** `Tag` entities with `tag_type = "id3"` and `normalized_name` of `"post-punk"` and `"gothic rock"` are upserted and associated with the artist

#### Scenario: OpenAI tags become ai type

- **WHEN** the OpenAI enricher returns `AITags: ["atmospheric", "melancholic"]` for an album
- **THEN** `Tag` entities with `tag_type = "ai"` are upserted and associated with the album

### Requirement: UI Tag Visual Differentiation

The UI SHALL render each tag type with a distinct badge style. The mapping SHALL be:

| `tag_type` | Icon class | Badge class |
|------------|-----------|-------------|
| `id3` | `icon-[heroicons--musical-note]` | `badge-neutral` |
| `genre` | `icon-[heroicons--tag]` | `badge-primary` |
| `ai` | `icon-[heroicons--sparkles]` | `badge-accent` |
| `label` | `icon-[heroicons--building-office]` | `badge-secondary` |
| `source` | `icon-[heroicons--server]` | `badge-info` |

The `ai` tag type MUST use `badge-accent` and the sparkles icon, consistent with the existing
AI branding used by `AISummaryCard`, AI biography toggle, and AI regeneration buttons.

A shared Templ component `TypedTagBadge` SHALL be defined in
`internal/views/components/ui.templ`. All tag rendering on Artist, Album, Track, and tag browsing
pages SHALL use this component.

Tags of different types that appear together on a page SHOULD be visually grouped by type with
a type label or section header.

#### Scenario: AI tag rendered with accent badge and sparkles

- **WHEN** an artist page renders AI-type tags
- **THEN** each badge uses `class="badge badge-accent gap-1"` with a child `icon-[heroicons--sparkles]` span

#### Scenario: Genre tag rendered with primary badge and tag icon

- **WHEN** an album page renders genre-type tags
- **THEN** each badge uses `class="badge badge-primary gap-1"` with a child `icon-[heroicons--tag]` span

#### Scenario: Mixed tag types are visually grouped

- **WHEN** an artist page displays both `genre` and `ai` tags
- **THEN** tags of each type are grouped together with a visible label or section divider distinguishing "Genres" from "AI Tags"

### Requirement: Data Migration

The system SHALL include a one-time migration that backfills existing JSON array tag data from
Artist, Album, and Track entities into the new `Tag` entity and `entity_tags` table.

The migration SHALL apply the following source-field-to-tag-type mapping:

| Entity | Legacy Field | `tag_type` |
|--------|-------------|------------|
| Artist | `genres` | `genre` |
| Artist | `tags` | `id3` |
| Artist | `ai_tags` | `ai` |
| Album | `genre` | `genre` |
| Album | `tags` | `id3` |
| Album | `ai_tags` | `ai` |
| Album | `label` | `label` |
| Track | `genres` | `genre` |
| Track | `tags` | `id3` |
| Track | `ai_tags` | `ai` |

The migration SHALL be idempotent — running it multiple times MUST NOT create duplicate `Tag`
entities or duplicate junction table associations.

After successful migration, the legacy JSON fields SHALL be marked deprecated in the Ent schema
with a comment indicating the migration date and SHALL NOT be removed until a subsequent major
version.

#### Scenario: Artist genre tags backfilled

- **WHEN** the migration processes an artist with `genres: ["shoegaze", "dream pop"]`
- **THEN** two `Tag` entities with `tag_type = "genre"` are created (or retrieved) and associated with the artist in both the Ent junction table and `entity_tags`

#### Scenario: Migration is idempotent

- **WHEN** the migration is run a second time against the same dataset
- **THEN** no duplicate `Tag` entities or junction rows are created

#### Scenario: Empty legacy fields are skipped

- **WHEN** an artist has `ai_tags: []` or `ai_tags: null`
- **THEN** no `Tag` entities are created for that field and the migration continues without error
