package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	spmixin "spotter/ent/schema/mixin"
)

// AlbumRecommendation holds metadata about a recommended album.
type AlbumRecommendation struct {
	Name      string `json:"name"`
	Artist    string `json:"artist"`
	SpotifyID string `json:"spotify_id"`
	Reason    string `json:"reason"`
	ImageURL  string `json:"image_url"`
	Year      int    `json:"year"`
}

// Album holds the schema definition for the Album entity.
type Album struct {
	ent.Schema
}

// Mixin of the Album.
func (Album) Mixin() []ent.Mixin {
	return []ent.Mixin{
		spmixin.Timestamps{},
	}
}

// Fields of the Album.
func (Album) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			NotEmpty().
			MaxLen(500),
		field.String("sort_name").
			Optional().
			MaxLen(500).
			Comment("Name used for sorting, e.g., 'White Album, The'"),
		field.String("musicbrainz_id").
			Optional().
			MaxLen(36).
			Comment("MusicBrainz release group ID"),
		field.String("spotify_id").
			Optional().
			MaxLen(50).
			Comment("Spotify album ID"),
		field.String("lidarr_id").
			Optional().
			MaxLen(255).
			Comment("Lidarr album ID"),
		// Governing: SPEC-0017 REQ "Background Submitter Goroutine"
		field.String("lidarr_status").
			Optional().
			Nillable().
			Comment("Status in Lidarr: monitored, unmonitored, etc."),
		field.String("release_date").
			Optional().
			MaxLen(10).
			Comment("Release date in ISO format (YYYY, YYYY-MM, or YYYY-MM-DD)"),
		field.Int("year").
			Optional().
			Comment("Release year extracted from release_date"),
		// Deprecated: migrated to Tag entity (SPEC-0018, 2026-02-28)
		field.String("genre").
			Optional().
			MaxLen(255).
			Comment("Deprecated: migrated to Tag entity. Primary genre of the album"),
		// Deprecated: migrated to Tag entity (SPEC-0018, 2026-02-28)
		field.JSON("tags", []string{}).
			Optional().
			Comment("Deprecated: migrated to Tag entity. Tags/genres from various sources"),
		field.Int("popularity").
			Optional().
			Comment("Popularity score (0-100, from Spotify)"),
		field.Int("total_tracks").
			Optional().
			Comment("Total number of tracks on the album"),
		field.String("album_type").
			Optional().
			MaxLen(50).
			Comment("Type: album, single, compilation, ep"),
		// Deprecated: migrated to Tag entity (SPEC-0018, 2026-02-28)
		field.String("label").
			Optional().
			MaxLen(255).
			Comment("Deprecated: migrated to Tag entity. Record label"),
		field.Time("last_enriched_at").
			Optional().
			Comment("When metadata was last enriched"),
		// AI-generated fields
		field.Text("ai_summary").
			Optional().
			MaxLen(10000).
			Comment("AI-generated summary of the album including artist thoughts and context"),
		// Deprecated: migrated to Tag entity (SPEC-0018, 2026-02-28)
		field.JSON("ai_tags", []string{}).
			Optional().
			Comment("Deprecated: migrated to Tag entity. AI-generated tags for the album (max 5)"),
		field.JSON("dominant_colors", []string{}).
			Optional().
			Comment("AI-generated dominant colors from the cover art"),
		field.Text("cover_art_commentary").
			Optional().
			MaxLen(10000).
			Comment("AI-generated art critic commentary on the cover art"),
		field.Time("last_ai_enriched_at").
			Optional().
			Nillable().
			Comment("Last time AI enrichment was performed"),
		field.JSON("recommendations", []AlbumRecommendation{}).
			Optional().
			Comment("AI-generated album recommendations based on this album"),
	}
}

// Edges of the Album.
func (Album) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("albums").
			Unique().
			Required(),
		edge.From("artist", Artist.Type).
			Ref("albums").
			Unique(),
		edge.To("tracks", Track.Type),
		edge.To("images", AlbumImage.Type),
		edge.To("listens", Listen.Type),
		edge.To("playlist_tracks", PlaylistTrack.Type),
		edge.From("tag_entities", Tag.Type).Ref("albums"),
	}
}

// Indexes of the Album.
func (Album) Indexes() []ent.Index {
	return []ent.Index{
		// Unique album name per artist per user
		index.Fields("name").
			Edges("user", "artist").
			Unique(),
		// Fast lookup by external IDs
		index.Fields("musicbrainz_id"),
		index.Fields("spotify_id"),
		index.Fields("lidarr_id"),
		// Search by year
		index.Fields("year"),
	}
}
