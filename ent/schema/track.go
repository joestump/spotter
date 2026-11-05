package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Track holds the schema definition for the Track entity.
type Track struct {
	ent.Schema
}

// Fields of the Track.
func (Track) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			NotEmpty(),
		field.String("musicbrainz_id").
			Optional().
			Nillable(),
		field.String("spotify_id").
			Optional().
			Nillable(),
		field.String("navidrome_id").
			Optional().
			Nillable(),
		field.String("lidarr_id").
			Optional().
			Nillable(),
		field.String("lidarr_status").
			Optional().
			Nillable().
			Comment("Status in Lidarr: pending, grabbed, imported, etc."),
		field.Int("duration_ms").
			Optional().
			Nillable().
			Comment("Track duration in milliseconds"),
		field.Int("track_number").
			Optional().
			Nillable(),
		field.Int("disc_number").
			Optional().
			Nillable(),
		field.Float("bpm").
			Optional().
			Nillable().
			Comment("Beats per minute from audio analysis"),
		field.String("musical_key").
			Optional().
			Nillable().
			Comment("Musical key (e.g., C, Am, F#)"),
		field.Float("energy").
			Optional().
			Nillable().
			Comment("Spotify audio feature: energy (0.0 to 1.0)"),
		field.Float("danceability").
			Optional().
			Nillable().
			Comment("Spotify audio feature: danceability (0.0 to 1.0)"),
		field.Float("valence").
			Optional().
			Nillable().
			Comment("Spotify audio feature: musical positiveness (0.0 to 1.0)"),
		field.Float("acousticness").
			Optional().
			Nillable().
			Comment("Spotify audio feature: acousticness (0.0 to 1.0)"),
		field.Float("instrumentalness").
			Optional().
			Nillable().
			Comment("Spotify audio feature: instrumentalness (0.0 to 1.0)"),
		field.Int("popularity").
			Optional().
			Nillable().
			Comment("Popularity score (0-100)"),
		// Deprecated: migrated to Tag entity (SPEC-0018, 2026-02-28)
		field.JSON("tags", []string{}).
			Optional().
			Comment("Deprecated: migrated to Tag entity. Tags from various sources"),
		// Deprecated: migrated to Tag entity (SPEC-0018, 2026-02-28)
		field.JSON("genres", []string{}).
			Optional().
			Comment("Deprecated: migrated to Tag entity. Genres associated with this track"),
		field.String("isrc").
			Optional().
			Nillable().
			Comment("International Standard Recording Code"),
		field.String("spotify_url").
			Optional().
			Nillable(),
		field.String("musicbrainz_url").
			Optional().
			Nillable(),
		field.Time("last_enriched_at").
			Optional().
			Nillable().
			Comment("Last time metadata was enriched"),
		field.Time("created_at").
			Default(time.Now),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
		// AI-generated fields
		field.Text("ai_summary").
			Optional().
			Comment("AI-generated summary of the track"),
		// Deprecated: migrated to Tag entity (SPEC-0018, 2026-02-28)
		field.JSON("ai_tags", []string{}).
			Optional().
			Comment("Deprecated: migrated to Tag entity. AI-generated tags for the track (max 5)"),
		field.Time("last_ai_enriched_at").
			Optional().
			Nillable().
			Comment("Last time AI enrichment was performed"),
	}
}

// Edges of the Track.
func (Track) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("artist", Artist.Type).
			Ref("tracks").
			Unique(),
		edge.From("album", Album.Type).
			Ref("tracks").
			Unique(),
		edge.To("listens", Listen.Type),
		edge.To("playlist_tracks", PlaylistTrack.Type),
		edge.From("tag_entities", Tag.Type).Ref("tracks"),
	}
}

// Indexes of the Track.
func (Track) Indexes() []ent.Index {
	return []ent.Index{
		// Track name + artist should be unique (approximately)
		index.Fields("name").
			Edges("artist"),
		// External IDs for quick lookups
		index.Fields("musicbrainz_id"),
		index.Fields("spotify_id"),
		index.Fields("navidrome_id"),
		index.Fields("lidarr_id"),
		index.Fields("isrc"),
	}
}
