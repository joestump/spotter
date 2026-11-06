// Governing: SPEC music-provider-integration, ADR-0016 (pluggable provider factory pattern)
package providers

import (
	"context"
	"time"

	"spotter/ent"
)

// Type identifies the source of the data (e.g., "spotify", "navidrome").
type Type string

const (
	TypeSpotify   Type = "spotify"
	TypeNavidrome Type = "navidrome"
	TypeLastFM    Type = "lastfm"
)

// Governing: SPEC music-provider-integration REQ-PROV-020 (normalized Track struct), REQ-PROV-022 (ISRC for cross-provider matching)
// Track represents a normalized audio track across services.
type Track struct {
	ID         string // Provider specific ID
	Name       string
	Artist     string
	Album      string
	DurationMs int
	PlayedAt   time.Time // When the track was listened to (UTC)
	URL        string    // Deep link to the track
	ISRC       string    // International Standard Recording Code (for matching)
}

// Governing: SPEC music-provider-integration REQ-PROV-021 (normalized Playlist struct)
// Playlist represents a collection of tracks.
type Playlist struct {
	ID            string
	Name          string
	Description   string
	ImageURL      string // URL to playlist cover art
	ExternalURL   string // Deep link to playlist on provider's website
	TrackCount    int    // Number of tracks in the playlist
	UniqueArtists int    // Number of unique artists in the playlist
	UniqueAlbums  int    // Number of unique albums in the playlist
	Tracks        []Track
}

// Governing: SPEC music-provider-integration REQ-PROV-001 (base interface with Type() returning stable identifier)
// Provider is the base interface that all external music services must implement.
type Provider interface {
	// Type returns the identifier for this provider.
	Type() Type
}

// Governing: SPEC music-provider-integration REQ-PROV-002 (history fetcher with batched callback)
// HistoryFetcher is implemented by providers that can retrieve listening history.
type HistoryFetcher interface {
	Provider
	// GetRecentListens retrieves tracks played after the given timestamp.
	// It calls the provided callback for each batch of tracks retrieved.
	GetRecentListens(ctx context.Context, since time.Time, callback func([]Track) error) error
}

// Governing: SPEC music-provider-integration REQ-PROV-003 (playlist reader with GetPlaylists and CreatePlaylist)
// PlaylistManager is implemented by providers that can read/write playlists.
type PlaylistManager interface {
	Provider
	// GetPlaylists retrieves the user's playlists.
	GetPlaylists(ctx context.Context) ([]Playlist, error)
	// CreatePlaylist creates a new playlist with the given tracks.
	CreatePlaylist(ctx context.Context, name, description string, tracks []Track) error
}

// SyncPlaylistRequest contains the data needed to sync a playlist to a provider.
type SyncPlaylistRequest struct {
	Name        string  // Playlist name
	Description string  // Playlist description
	ImageURL    string  // Optional: URL to cover art
	Tracks      []Track // Tracks to include in the playlist
}

// Governing: SPEC music-provider-integration REQ-PROV-004 (playlist write-back: sync, delete, update tracks)
// PlaylistSyncer is implemented by providers that can receive playlists from other sources.
// This is separate from PlaylistManager which reads playlists FROM a provider.
type PlaylistSyncer interface {
	Provider
	// SyncPlaylist creates or updates a playlist on this provider from external data.
	// Returns the remote playlist ID created/updated on this provider.
	SyncPlaylist(ctx context.Context, playlist SyncPlaylistRequest) (string, error)
	// DeletePlaylist removes a playlist from this provider.
	DeletePlaylist(ctx context.Context, remotePlaylistID string) error
	// UpdatePlaylistTracks replaces all tracks in a playlist.
	UpdatePlaylistTracks(ctx context.Context, remotePlaylistID string, tracks []Track) error
}

// AuthConfig contains the configuration needed to start an OAuth flow.
type AuthConfig struct {
	AuthURL string // URL to redirect the user to for authentication
	State   string // State parameter for CSRF protection
}

// AuthResult contains the result of a successful authentication.
type AuthResult struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	DisplayName  string // User's display name from the provider
	UserID       string // User's ID from the provider
}

// Governing: SPEC music-provider-integration REQ-PROV-005 (authenticator: auth URL, code exchange, refresh, disconnect)
// Authenticator is implemented by providers that support OAuth or similar authentication flows.
// This interface should NOT be used for Navidrome as it's the primary app authentication mechanism.
type Authenticator interface {
	Provider
	// SupportsAuth returns true if this provider supports user authentication from preferences.
	// Navidrome should return false as it's used for app login, not as a connected service.
	SupportsAuth() bool
	// GetAuthURL returns the URL to redirect the user to for authentication.
	// The state parameter should be stored in the session for verification on callback.
	GetAuthURL(state string) string
	// ExchangeCode exchanges the authorization code for access and refresh tokens.
	ExchangeCode(ctx context.Context, code string) (*AuthResult, error)
	// RefreshToken refreshes an expired access token.
	RefreshToken(ctx context.Context, refreshToken string) (*AuthResult, error)
	// Disconnect performs any cleanup needed when a user disconnects from the provider.
	// This might include revoking tokens if the provider supports it.
	Disconnect(ctx context.Context) error
}

// Governing: ADR-0016 (pluggable provider factory), SPEC music-provider-integration REQ-PROV-010 (factory signature),
// REQ-PROV-011 (nil,nil if unconfigured), REQ-PROV-012 (credentials read from DB edges, not params)
// Factory defines the function signature for creating a provider instance for a specific user.
// Implementations should return nil, nil if the user is not configured for the provider.
type Factory func(ctx context.Context, user *ent.User) (Provider, error)

// AuthenticatorFactory defines the function signature for creating an authenticator instance.
// Unlike Factory, this doesn't require a user since it's used to initiate the auth flow.
type AuthenticatorFactory func() Authenticator

// Governing: ADR-0016 (pluggable provider factory), SPEC listen-playlist-sync REQ-SYNC-012 (per-provider sync cursor)
// SyncCursor is implemented by providers that persist their sync cursor on the
// per-user auth record they were created from. Registering the provider is then
// enough for cursor persistence: the service layer asks the provider to write
// its own cursor instead of switching on concrete provider types, so a provider
// that cannot persist one yields an error rather than a silent no-op.
type SyncCursor interface {
	Provider
	// SetSyncCursor records the sync cursor for the user this provider was
	// created for. The caller passes its own Ent client, because providers are
	// not constructed with one.
	SetSyncCursor(ctx context.Context, client *ent.Client, at time.Time) error
}

// Governing: ADR-0016 (pluggable provider factory), SPEC music-provider-integration REQ-PROV-012 (credentials read from DB edges, not params)
// AuthLoader adds the eager-load steps a Factory needs on the user query so it
// can read its credentials from the user's auth edges. A provider contributes
// its own loader when it is registered, which keeps the service layer from
// naming auth edges it does not own.
type AuthLoader func(q *ent.UserQuery) *ent.UserQuery
