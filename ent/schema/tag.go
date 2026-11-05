// Governing: SPEC-0018 REQ "Tag Entity Schema", SPEC-0018 REQ "Tag Type Taxonomy",
// SPEC-0018 REQ "Tag-Entity Associations", SPEC-0018 REQ "Tag Relationships",
// ADR-0004 (Ent ORM), ADR-0025 (Unified Tag Taxonomy)
package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Tag holds the schema definition for the Tag entity.
type Tag struct {
	ent.Schema
}

// Fields of the Tag.
func (Tag) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			NotEmpty().
			MaxLen(255).
			Comment("Display name of the tag"),
		field.String("normalized_name").
			NotEmpty().
			MaxLen(255).
			Comment("Lowercase, whitespace-trimmed canonical form"),
		field.Enum("tag_type").
			Values("id3", "genre", "ai", "label", "source").
			Comment("Tag type taxonomy: id3, genre, ai, label, source"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Edges of the Tag.
func (Tag) Edges() []ent.Edge {
	return []ent.Edge{
		// Each tag belongs to one user
		edge.From("user", User.Type).
			Ref("tags").
			Unique().
			Required(),
		// Many-to-many associations with library entities
		edge.To("artists", Artist.Type),
		edge.To("albums", Album.Type),
		edge.To("tracks", Track.Type),
		// Self-referential many-to-many for related tags
		edge.To("related_tags", Tag.Type).
			From("related_from"),
	}
}

// Indexes of the Tag.
func (Tag) Indexes() []ent.Index {
	return []ent.Index{
		// Unique tag per (normalized_name, tag_type, user)
		index.Fields("normalized_name", "tag_type").
			Edges("user").
			Unique(),
		// Fast lookup by tag_type
		index.Fields("tag_type"),
		// Fast lookup by normalized_name
		index.Fields("normalized_name"),
	}
}
