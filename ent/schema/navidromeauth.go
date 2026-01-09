package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// NavidromeAuth holds the schema definition for the NavidromeAuth entity.
type NavidromeAuth struct {
	ent.Schema
}

// Fields of the NavidromeAuth.
func (NavidromeAuth) Fields() []ent.Field {
	return []ent.Field{
		// We need the password to sign requests (md5(password + salt))
		// This field is encrypted at rest using AES-256-GCM encryption.
		// Encryption/decryption is handled automatically by database hooks.
		field.String("password"),
		field.Time("last_synced_at").
			Optional(),
	}
}

// Edges of the NavidromeAuth.
func (NavidromeAuth) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("navidrome_auth").
			Unique().
			Required(),
	}
}
