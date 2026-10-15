package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Playlist holds the schema definition for the Playlist entity.
type Playlist struct {
	ent.Schema
}

// Fields of the Playlist.
func (Playlist) Fields() []ent.Field {
	return []ent.Field{
		field.String("remote_id").
			MaxLen(255),
		field.String("name").
			NotEmpty().
			MaxLen(500),
		field.String("description").
			Optional().
			MaxLen(2000),
		field.String("source").
			NotEmpty().
			MaxLen(50), // e.g. "spotify", "navidrome"
		field.String("image_url").
			Optional().
			MaxLen(2048).
			Comment("URL to the playlist cover art"),
		field.String("external_url").
			Optional().
			MaxLen(2048).
			Comment("Deep link to the playlist on the provider's website"),
		field.Int("track_count").
			Default(0).
			Comment("Number of tracks in the playlist"),
		field.Int("unique_artists").
			Default(0).
			Comment("Number of unique artists in the playlist"),
		field.Int("unique_albums").
			Default(0).
			Comment("Number of unique albums in the playlist"),
		field.Bool("sync_to_navidrome").
			Default(false).
			Comment("Whether to sync this playlist to Navidrome (only for non-Navidrome sources)"),
		field.String("navidrome_playlist_id").
			Optional().
			MaxLen(255).
			Comment("The remote playlist ID in Navidrome if synced from another source"),
		// Governing: SPEC-0015 REQ playlist-pairing
		field.String("navidrome_playlist_name").
			Optional().
			MaxLen(255).
			Comment("Custom name to use for the Navidrome playlist (overrides source name). Governing: SPEC-0015 playlist-pairing"),
		field.Time("last_synced_at").
			Optional().
			Nillable().
			Comment("When the playlist was last synced to Navidrome"),
		field.String("sync_error").
			Optional().
			MaxLen(1000).
			Comment("Last sync error message, empty if successful"),
		// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-010 (sync_status state machine)
		field.Enum("sync_status").
			Values("pending", "syncing", "success", "warning", "error").
			Optional().
			Default("pending").
			Comment("State of sync to Navidrome: pending, syncing, success, warning, error"),
		field.Bool("active").
			Default(true).
			Comment("False once the source provider stopped returning this playlist. Governing: SPEC listen-playlist-sync REQ-SYNC-032"),
		field.Int("matched_track_count").
			Default(0).
			Comment("Number of tracks successfully matched in Navidrome"),
		field.Time("created_at").
			Default(time.Now),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the Playlist.
func (Playlist) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("playlists").
			Unique().
			Required(),
		edge.To("tracks", PlaylistTrack.Type),
	}
}

// Indexes of the Playlist.
func (Playlist) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("remote_id", "source").
			Edges("user").
			Unique(),
	}
}
