package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Governing: SPEC-0017 REQ "Queue Entity Schema", ADR-0029

// LidarrQueue holds the schema definition for the LidarrQueue entity.
// It represents items queued for submission to Lidarr.
type LidarrQueue struct {
	ent.Schema
}

// Fields of the LidarrQueue.
func (LidarrQueue) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("entity_type").
			Values("artist", "album").
			Comment("Type of entity to submit to Lidarr"),
		field.Int("entity_id").
			Comment("ID of the entity in the local database"),
		field.String("musicbrainz_id").
			NotEmpty().
			MaxLen(255).
			Comment("MusicBrainz ID required for Lidarr submission"),
		field.Enum("status").
			Values("queued", "submitted", "failed").
			Default("queued").
			Comment("Current status of the queue entry"),
		field.Int("attempts").
			Default(0).
			Comment("Number of submission attempts"),
		field.String("last_error").
			Optional().
			Nillable().
			Comment("Error message from last failed attempt"),
		field.Time("retry_at").
			Optional().
			Nillable().
			Comment("When to retry a failed submission"),
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			Comment("When the entry was queued"),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now).
			Comment("When the entry was last updated"),
	}
}

// Edges of the LidarrQueue.
func (LidarrQueue) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("lidarr_queue").
			Unique().
			Required(),
	}
}

// Indexes of the LidarrQueue.
func (LidarrQueue) Indexes() []ent.Index {
	return []ent.Index{
		// Unique constraint: one queue entry per entity per user
		index.Fields("entity_type", "entity_id").
			Edges("user").
			Unique(),
		// Index for finding entries by status (for processing)
		index.Fields("status"),
		// Index for finding entries ready to retry
		index.Fields("retry_at"),
	}
}
