package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	spmixin "spotter/ent/schema/mixin"
)

// Artist holds the schema definition for the Artist entity.
type Artist struct {
	ent.Schema
}

// Mixin of the Artist.
func (Artist) Mixin() []ent.Mixin {
	return []ent.Mixin{
		spmixin.Timestamps{},
	}
}

// Fields of the Artist.
func (Artist) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			NotEmpty().
			MaxLen(500).
			Comment("Artist name"),
		field.String("sort_name").
			Optional().
			MaxLen(500).
			Comment("Name used for sorting, e.g., 'Beatles, The'"),
		field.String("musicbrainz_id").
			Optional().
			Unique().
			MaxLen(36). // UUID format
			Comment("MusicBrainz artist MBID"),
		field.String("spotify_id").
			Optional().
			Unique().
			MaxLen(50).
			Comment("Spotify artist ID"),
		field.String("lastfm_url").
			Optional().
			MaxLen(2048).
			Comment("Last.fm artist page URL"),
		field.String("navidrome_id").
			Optional().
			MaxLen(255).
			Comment("Navidrome artist ID"),
		field.String("lidarr_id").
			Optional().
			MaxLen(255).
			Comment("Lidarr artist ID"),
		// Governing: SPEC-0017 REQ "Background Submitter Goroutine"
		field.String("lidarr_status").
			Optional().
			Nillable().
			Comment("Status in Lidarr: monitored, unmonitored, etc."),
		field.Text("bio").
			Optional().
			MaxLen(50000). // Bios can be long
			Comment("Artist biography from Last.fm or other sources"),
		// Deprecated: migrated to Tag entity (SPEC-0018, 2026-02-28)
		field.JSON("tags", []string{}).
			Optional().
			Comment("Deprecated: migrated to Tag entity. Genre tags and social tags from various sources"),
		field.Int("popularity").
			Optional().
			Nillable().
			Comment("Popularity score from Spotify (0-100)"),
		field.Int("follower_count").
			Optional().
			Nillable().
			Comment("Follower count from Spotify"),
		// Deprecated: migrated to Tag entity (SPEC-0018, 2026-02-28)
		field.JSON("genres", []string{}).
			Optional().
			Comment("Deprecated: migrated to Tag entity. Genre list from Spotify"),
		field.Time("last_enriched_at").
			Optional().
			Nillable().
			Comment("Last time metadata was enriched from providers"),
		// AI-generated fields
		field.Text("ai_summary").
			Optional().
			MaxLen(10000).
			Comment("AI-generated summary of the artist"),
		field.Text("ai_biography").
			Optional().
			MaxLen(50000).
			Comment("AI-generated biography for the artist"),
		// Deprecated: migrated to Tag entity (SPEC-0018, 2026-02-28)
		field.JSON("ai_tags", []string{}).
			Optional().
			Comment("Deprecated: migrated to Tag entity. AI-generated tags for the artist (max 5)"),
		field.Time("last_ai_enriched_at").
			Optional().
			Nillable().
			Comment("Last time AI enrichment was performed"),
	}
}

// Edges of the Artist.
func (Artist) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("artists").
			Unique().
			Required(),
		edge.To("albums", Album.Type),
		edge.To("tracks", Track.Type),
		edge.To("images", ArtistImage.Type),
		edge.To("listens", Listen.Type),
		edge.To("playlist_tracks", PlaylistTrack.Type),
		edge.From("tag_entities", Tag.Type).Ref("artists"),
	}
}

// Indexes of the Artist.
func (Artist) Indexes() []ent.Index {
	return []ent.Index{
		// Unique artist name per user
		index.Fields("name").
			Edges("user").
			Unique(),
		// Index for MusicBrainz lookups
		index.Fields("musicbrainz_id"),
		// Index for Spotify lookups
		index.Fields("spotify_id"),
		index.Fields("lidarr_id"),
	}
}
