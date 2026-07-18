// Governing: ADR-0015 (type-keyed enricher registry with factory pattern),
// SPEC metadata-enrichment-pipeline REQ-ENRICH-001 through REQ-ENRICH-005 (enricher interfaces),
// SPEC metadata-enrichment-pipeline REQ-ENRICH-050 (registry with dynamic registration),
// SPEC metadata-enrichment-pipeline REQ-ENRICH-051 (registry supports listing enrichers by type)
package enrichers

import (
	"context"
	"fmt"

	"spotter/ent"
	"spotter/internal/tags"
)

// Type identifies the enricher source.
type Type string

const (
	TypeMusicBrainz Type = "musicbrainz"
	TypeSpotify     Type = "spotify"
	TypeFanart      Type = "fanart"
	TypeLastFM      Type = "lastfm"
	TypeNavidrome   Type = "navidrome"
	TypeOpenAI      Type = "openai"
	TypeLidarr      Type = "lidarr"
)

// Priority defines the order in which enrichers run (lower = earlier).
type Priority int

// ArtistData contains enrichment data for an artist.
// Governing: SPEC-0014 REQ "Enricher Integration"
type ArtistData struct {
	MusicBrainzID string
	SpotifyID     string
	LastFMURL     string
	NavidromeID   string
	LidarrID      string
	LidarrStatus  string
	SortName      string
	Bio           string
	Tags          []string
	Genres        []string
	Popularity    *int
	FollowerCount *int
	// AI-generated fields
	AISummary   string
	AIBiography string
	AITags      []string
	// Typed tags for unified tag taxonomy
	// Governing: SPEC-0014 REQ "Enricher Integration"
	TypedTags []tags.TypedTag
}

// RecommendedAlbum contains data about a recommended album.
type RecommendedAlbum struct {
	Name      string
	Artist    string
	SpotifyID string
	Reason    string
	ImageURL  string
	Year      int
}

// AlbumData contains enrichment data for an album.
// Governing: SPEC-0014 REQ "Enricher Integration"
type AlbumData struct {
	MusicBrainzID string
	SpotifyID     string
	LidarrID      string
	LidarrStatus  string
	ReleaseDate   string
	Year          int
	Genre         string
	Tags          []string
	AlbumType     string // album, single, compilation, ep
	Label         string
	TotalTracks   int
	Popularity    int
	// AI-generated fields
	AISummary          string
	AITags             []string
	DominantColors     []string
	CoverArtCommentary string
	Recommendations    []RecommendedAlbum
	// Typed tags for unified tag taxonomy
	// Governing: SPEC-0014 REQ "Enricher Integration"
	TypedTags []tags.TypedTag
}

// TrackData contains enrichment data for a track.
// Governing: SPEC-0014 REQ "Enricher Integration"
type TrackData struct {
	MusicBrainzID    string
	SpotifyID        string
	NavidromeID      string
	LidarrID         string
	LidarrStatus     string
	ISRC             string
	DurationMs       int
	TrackNumber      int
	DiscNumber       int
	BPM              *float64
	MusicalKey       string
	Energy           *float64
	Danceability     *float64
	Valence          *float64
	Acousticness     *float64
	Instrumentalness *float64
	Popularity       *int
	Tags             []string
	Genres           []string
	SpotifyURL       string
	MusicBrainzURL   string
	// AI-generated fields
	AISummary string
	AITags    []string
	// Typed tags for unified tag taxonomy
	// Governing: SPEC-0014 REQ "Enricher Integration"
	TypedTags []tags.TypedTag
}

// ImageData contains data about an image to download.
type ImageData struct {
	URL       string
	LocalPath string
	Type      string // thumbnail, background, logo, banner, fanart, cover_front, cover_back, cd_art, etc.
	Source    string // provider name
	Width     int
	Height    int
	Likes     int  // popularity from fanart.tv
	IsPrimary bool // whether this should be the primary image
}

// Enricher is the base interface that all metadata enrichers must implement.
// Governing: SPEC metadata-enrichment-pipeline REQ-ENRICH-001 (base enricher interface with Type()).
// Note: Priority() is replaced by DefaultOrder() per ADR-0015 (order-based execution over priority fields).
type Enricher interface {
	// Type returns the identifier for this enricher.
	Type() Type

	// Name returns a human-readable name for this enricher.
	Name() string

	// IsAvailable checks if this enricher is properly configured and can be used.
	IsAvailable() bool
}

// ArtistEnricher is implemented by enrichers that can add metadata to artists.
// Governing: SPEC metadata-enrichment-pipeline REQ-ENRICH-002 (ArtistEnricher specialized interface)
type ArtistEnricher interface {
	Enricher

	// EnrichArtist fetches and returns enrichment data for an artist.
	// The enricher should use available identifiers (name, IDs) to find the artist.
	EnrichArtist(ctx context.Context, artist *ent.Artist) (*ArtistData, error)

	// GetArtistImages returns available images for the artist.
	GetArtistImages(ctx context.Context, artist *ent.Artist) ([]ImageData, error)
}

// AlbumEnricher is implemented by enrichers that can add metadata to albums.
// Governing: SPEC metadata-enrichment-pipeline REQ-ENRICH-003 (AlbumEnricher specialized interface)
type AlbumEnricher interface {
	Enricher

	// EnrichAlbum fetches and returns enrichment data for an album.
	EnrichAlbum(ctx context.Context, album *ent.Album) (*AlbumData, error)

	// GetAlbumImages returns available images for the album.
	GetAlbumImages(ctx context.Context, album *ent.Album) ([]ImageData, error)
}

// TrackEnricher is implemented by enrichers that can add metadata to tracks.
// Governing: SPEC metadata-enrichment-pipeline REQ-ENRICH-004 (TrackEnricher specialized interface)
type TrackEnricher interface {
	Enricher

	// EnrichTrack fetches and returns enrichment data for a track.
	EnrichTrack(ctx context.Context, track *ent.Track) (*TrackData, error)
}

// IDMatcher is implemented by enrichers that can match local entities to external IDs.
// This is typically the first step before enrichment - finding the correct external entity.
// Governing: SPEC metadata-enrichment-pipeline REQ-ENRICH-005 (IDMatcher specialized interface)
type IDMatcher interface {
	Enricher

	// MatchArtist attempts to find an external ID for the given artist.
	MatchArtist(ctx context.Context, name string) (externalID string, confidence float64, err error)

	// MatchAlbum attempts to find an external ID for the given album.
	MatchAlbum(ctx context.Context, albumName, artistName string) (externalID string, confidence float64, err error)

	// MatchTrack attempts to find an external ID for the given track.
	MatchTrack(ctx context.Context, trackName, artistName, albumName string) (externalID string, confidence float64, err error)
}

// Factory defines the function signature for creating an enricher instance.
// Returns nil, nil if the enricher is not configured/available.
// Governing: ADR-0015 (factory pattern for per-user instantiation),
// SPEC metadata-enrichment-pipeline REQ-ENRICH-050 (enrichers instantiated per-user with user credentials),
// SPEC metadata-enrichment-pipeline REQ-ENRICH-051 (missing credentials -> graceful nil return)
type Factory func(ctx context.Context, user *ent.User) (Enricher, error)

// Registry holds all registered enricher factories.
// Governing: ADR-0015 (type-keyed registry with map[Type]Factory),
// SPEC metadata-enrichment-pipeline REQ-ENRICH-050 (dynamic registration via Register)
type Registry struct {
	factories map[Type]Factory
}

// NewRegistry creates a new enricher registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[Type]Factory),
	}
}

// Register adds a factory for the given enricher type.
// Registering the same type twice returns an error instead of silently
// overwriting the earlier factory.
// Governing: ADR-0015, SPEC metadata-enrichment-pipeline REQ-ENRICH-050
// (duplicate type registrations MUST return an error)
func (r *Registry) Register(t Type, factory Factory) error {
	if _, exists := r.factories[t]; exists {
		return fmt.Errorf("enricher type %q is already registered", t)
	}
	r.factories[t] = factory
	return nil
}

// Get returns the factory for the given enricher type.
func (r *Registry) Get(t Type) (Factory, bool) {
	f, ok := r.factories[t]
	return f, ok
}

// Types returns all registered enricher types.
func (r *Registry) Types() []Type {
	types := make([]Type, 0, len(r.factories))
	for t := range r.factories {
		types = append(types, t)
	}
	return types
}

// ParseType converts a string to an enricher Type.
func ParseType(s string) (Type, bool) {
	switch Type(s) {
	case TypeMusicBrainz, TypeSpotify, TypeFanart, TypeLastFM, TypeNavidrome, TypeOpenAI, TypeLidarr:
		return Type(s), true
	default:
		return "", false
	}
}

// DefaultOrder returns the default enricher execution order.
// MusicBrainz first for ID matching, then others for metadata, OpenAI last for AI enrichment.
// Governing: SPEC metadata-enrichment-pipeline REQ-ENRICH-010 (deterministic ascending priority order),
// SPEC metadata-enrichment-pipeline REQ-ENRICH-011 (MusicBrainz runs first),
// SPEC metadata-enrichment-pipeline REQ-ENRICH-012 (OpenAI runs last)
func DefaultOrder() []Type {
	return []Type{
		TypeMusicBrainz,
		TypeLidarr,
		TypeNavidrome,
		TypeSpotify,
		TypeLastFM,
		TypeFanart,
		TypeOpenAI,
	}
}
