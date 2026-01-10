package events

import (
	"sync"
)

type EventType string

const (
	EventTypeRecentListen EventType = "recent-listen"
	EventTypeNotification EventType = "notification"

	// Vibes/Mixtape events
	EventTypeMixtapeCreated    EventType = "mixtape-created"
	EventTypeMixtapeUpdated    EventType = "mixtape-updated"
	EventTypeMixtapeDeleted    EventType = "mixtape-deleted"
	EventTypeMixtapeGenerating EventType = "mixtape-generating"
	EventTypeMixtapeGenerated  EventType = "mixtape-generated"
	EventTypeMixtapeError      EventType = "mixtape-error"

	// Playlist Enhancement events
	EventTypePlaylistEnhancing    EventType = "playlist-enhancing"
	EventTypePlaylistEnhanced     EventType = "playlist-enhanced"
	EventTypePlaylistEnhanceError EventType = "playlist-enhance-error"

	// Similar Artists events
	EventTypeSimilarArtistsSearching EventType = "similar-artists-searching"
	EventTypeSimilarArtistsFound     EventType = "similar-artists-found"
	EventTypeSimilarArtistsError     EventType = "similar-artists-error"
)

type Event struct {
	Type    EventType
	Payload any
}

type NotificationPayload struct {
	Title    string
	Message  string
	IconType string // "success", "error", "warning", "info"
}

// MixtapeCreatedPayload is sent when a new mixtape is created.
type MixtapeCreatedPayload struct {
	MixtapeID   int
	MixtapeName string
	DJName      string
}

// MixtapeUpdatedPayload is sent when a mixtape is updated.
type MixtapeUpdatedPayload struct {
	MixtapeID   int
	MixtapeName string
}

// MixtapeDeletedPayload is sent when a mixtape is deleted.
type MixtapeDeletedPayload struct {
	MixtapeID   int
	MixtapeName string
}

// MixtapeGeneratingPayload is sent when mixtape generation starts.
type MixtapeGeneratingPayload struct {
	MixtapeID   int
	MixtapeName string
	DJName      string
}

// MixtapeGeneratedPayload is sent when mixtape generation completes successfully.
type MixtapeGeneratedPayload struct {
	MixtapeID    int
	MixtapeName  string
	DJName       string
	TracksCount  int
	MatchedCount int
	TokensUsed   int
}

// MixtapeErrorPayload is sent when mixtape generation fails.
type MixtapeErrorPayload struct {
	MixtapeID   int
	MixtapeName string
	Error       string
}

// SimilarArtistsSearchingPayload is sent when similar artist search starts.
type SimilarArtistsSearchingPayload struct {
	ArtistID   int
	ArtistName string
}

// SimilarArtistsFoundPayload is sent when similar artists are found.
type SimilarArtistsFoundPayload struct {
	ArtistID     int
	ArtistName   string
	SimilarCount int
	Provider     string
}

// SimilarArtistsErrorPayload is sent when similar artist search fails.
type SimilarArtistsErrorPayload struct {
	ArtistID   int
	ArtistName string
	Error      string
}

// PlaylistEnhancingPayload is sent when playlist enhancement starts.
type PlaylistEnhancingPayload struct {
	PlaylistID   int
	PlaylistName string
	DJName       string
}

// PlaylistEnhancedPayload is sent when playlist enhancement completes.
type PlaylistEnhancedPayload struct {
	PlaylistID   int
	PlaylistName string
	TracksAdded  int
	TokensUsed   int
}

// PlaylistEnhanceErrorPayload is sent when playlist enhancement fails.
type PlaylistEnhanceErrorPayload struct {
	PlaylistID int
	Error      string
}

type Bus struct {
	mu          sync.RWMutex
	subscribers map[int][]chan Event
}

func NewBus() *Bus {
	return &Bus{
		subscribers: make(map[int][]chan Event),
	}
}

// Subscribe returns a channel that receives events for the given user,
// and a cleanup function that must be called when the subscription is no longer needed.
func (b *Bus) Subscribe(userID int) (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan Event, 10)
	b.subscribers[userID] = append(b.subscribers[userID], ch)

	cleanup := func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		chans := b.subscribers[userID]
		for i, c := range chans {
			if c == ch {
				// Remove from slice
				b.subscribers[userID] = append(chans[:i], chans[i+1:]...)
				close(ch)
				break
			}
		}
		if len(b.subscribers[userID]) == 0 {
			delete(b.subscribers, userID)
		}
	}

	return ch, cleanup
}

func (b *Bus) Publish(userID int, event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if chans, ok := b.subscribers[userID]; ok {
		for _, ch := range chans {
			// Non-blocking send to prevent blocking the publisher if a client is slow
			select {
			case ch <- event:
			default:
				// Channel full, drop event
			}
		}
	}
}

// Shutdown closes all subscriber channels and cleans up the event bus.
// It is safe to call Shutdown multiple times.
func (b *Bus) Shutdown() {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, chans := range b.subscribers {
		for _, ch := range chans {
			close(ch)
		}
	}
	b.subscribers = make(map[int][]chan Event)
}

// PublishNotification is a convenience method to publish a notification event.
func (b *Bus) PublishNotification(userID int, title, message, iconType string) {
	b.Publish(userID, Event{
		Type: EventTypeNotification,
		Payload: NotificationPayload{
			Title:    title,
			Message:  message,
			IconType: iconType,
		},
	})
}

// PublishMixtapeCreated publishes an event when a mixtape is created.
func (b *Bus) PublishMixtapeCreated(userID int, mixtapeID int, mixtapeName, djName string) {
	b.Publish(userID, Event{
		Type: EventTypeMixtapeCreated,
		Payload: MixtapeCreatedPayload{
			MixtapeID:   mixtapeID,
			MixtapeName: mixtapeName,
			DJName:      djName,
		},
	})
}

// PublishMixtapeUpdated publishes an event when a mixtape is updated.
func (b *Bus) PublishMixtapeUpdated(userID int, mixtapeID int, mixtapeName string) {
	b.Publish(userID, Event{
		Type: EventTypeMixtapeUpdated,
		Payload: MixtapeUpdatedPayload{
			MixtapeID:   mixtapeID,
			MixtapeName: mixtapeName,
		},
	})
}

// PublishMixtapeDeleted publishes an event when a mixtape is deleted.
func (b *Bus) PublishMixtapeDeleted(userID int, mixtapeID int, mixtapeName string) {
	b.Publish(userID, Event{
		Type: EventTypeMixtapeDeleted,
		Payload: MixtapeDeletedPayload{
			MixtapeID:   mixtapeID,
			MixtapeName: mixtapeName,
		},
	})
}

// PublishMixtapeGenerating publishes an event when mixtape generation starts.
func (b *Bus) PublishMixtapeGenerating(userID int, mixtapeID int, mixtapeName, djName string) {
	b.Publish(userID, Event{
		Type: EventTypeMixtapeGenerating,
		Payload: MixtapeGeneratingPayload{
			MixtapeID:   mixtapeID,
			MixtapeName: mixtapeName,
			DJName:      djName,
		},
	})
}

// PublishMixtapeGenerated publishes an event when mixtape generation completes.
func (b *Bus) PublishMixtapeGenerated(userID int, mixtapeID int, mixtapeName, djName string, tracksCount, matchedCount, tokensUsed int) {
	b.Publish(userID, Event{
		Type: EventTypeMixtapeGenerated,
		Payload: MixtapeGeneratedPayload{
			MixtapeID:    mixtapeID,
			MixtapeName:  mixtapeName,
			DJName:       djName,
			TracksCount:  tracksCount,
			MatchedCount: matchedCount,
			TokensUsed:   tokensUsed,
		},
	})
}

// PublishMixtapeError publishes an event when mixtape generation fails.
func (b *Bus) PublishMixtapeError(userID int, mixtapeID int, mixtapeName, errorMsg string) {
	b.Publish(userID, Event{
		Type: EventTypeMixtapeError,
		Payload: MixtapeErrorPayload{
			MixtapeID:   mixtapeID,
			MixtapeName: mixtapeName,
			Error:       errorMsg,
		},
	})
}

// PublishSimilarArtistsSearching publishes an event when similar artist search starts.
func (b *Bus) PublishSimilarArtistsSearching(userID int, artistID int, artistName string) {
	b.Publish(userID, Event{
		Type: EventTypeSimilarArtistsSearching,
		Payload: SimilarArtistsSearchingPayload{
			ArtistID:   artistID,
			ArtistName: artistName,
		},
	})
}

// PublishSimilarArtistsFound publishes an event when similar artists are found.
func (b *Bus) PublishSimilarArtistsFound(userID int, artistID int, artistName string, similarCount int, provider string) {
	b.Publish(userID, Event{
		Type: EventTypeSimilarArtistsFound,
		Payload: SimilarArtistsFoundPayload{
			ArtistID:     artistID,
			ArtistName:   artistName,
			SimilarCount: similarCount,
			Provider:     provider,
		},
	})
}

// PublishSimilarArtistsError publishes an event when similar artist search fails.
func (b *Bus) PublishSimilarArtistsError(userID int, artistID int, artistName, errorMsg string) {
	b.Publish(userID, Event{
		Type: EventTypeSimilarArtistsError,
		Payload: SimilarArtistsErrorPayload{
			ArtistID:   artistID,
			ArtistName: artistName,
			Error:      errorMsg,
		},
	})
}

// PublishPlaylistEnhancing publishes an event when playlist enhancement starts.
func (b *Bus) PublishPlaylistEnhancing(userID int, playlistID int, playlistName, djName string) {
	b.Publish(userID, Event{
		Type: EventTypePlaylistEnhancing,
		Payload: PlaylistEnhancingPayload{
			PlaylistID:   playlistID,
			PlaylistName: playlistName,
			DJName:       djName,
		},
	})
}

// PublishPlaylistEnhanced publishes an event when playlist enhancement completes.
func (b *Bus) PublishPlaylistEnhanced(userID int, playlistID int, playlistName string, tracksAdded, tokensUsed int) {
	b.Publish(userID, Event{
		Type: EventTypePlaylistEnhanced,
		Payload: PlaylistEnhancedPayload{
			PlaylistID:   playlistID,
			PlaylistName: playlistName,
			TracksAdded:  tracksAdded,
			TokensUsed:   tokensUsed,
		},
	})
}

// PublishPlaylistEnhancementError publishes an event when playlist enhancement fails.
func (b *Bus) PublishPlaylistEnhancementError(userID int, playlistID int, errorMsg string) {
	b.Publish(userID, Event{
		Type: EventTypePlaylistEnhanceError,
		Payload: PlaylistEnhanceErrorPayload{
			PlaylistID: playlistID,
			Error:      errorMsg,
		},
	})
}
