package events

import (
	"context"
	"testing"
	"time"

	"spotter/ent"

	_ "github.com/mattn/go-sqlite3"
)

// TestPublishRecentListenCarriesTypedPayload verifies REQ-BUS-011 and REQ-BUS-012:
// the syncer-facing publisher sends the payload struct the spec names, not the
// entity it was handed.
func TestPublishRecentListenCarriesTypedPayload(t *testing.T) {
	client, err := ent.Open("sqlite3", "file:bus_publish_recent_listen?mode=memory&cache=shared&_fk=1")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	ctx := context.Background()
	u, err := client.User.Create().
		SetUsername("bus-publisher").
		SetPaginationSize(25).
		Save(ctx)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	playedAt := time.Date(2026, 10, 30, 9, 15, 0, 0, time.UTC)
	listen, err := client.Listen.Create().
		SetUser(u).
		SetTrackName("Second Thought").
		SetArtistName("Big Thief").
		SetAlbumName("Dragon New Warm Mountain").
		SetSource("spotify").
		SetPlayedAt(playedAt).
		Save(ctx)
	if err != nil {
		t.Fatalf("create listen: %v", err)
	}

	bus := NewBus()
	ch, cleanup := bus.Subscribe(u.ID)
	defer cleanup()

	bus.PublishRecentListen(u.ID, listen)

	event := <-ch
	if event.Type != EventTypeRecentListen {
		t.Fatalf("event type = %q, want %q", event.Type, EventTypeRecentListen)
	}

	payload, ok := event.Payload.(RecentListenPayload)
	if !ok {
		t.Fatalf("payload type = %T, want RecentListenPayload", event.Payload)
	}
	if payload.ListenID != listen.ID {
		t.Errorf("ListenID = %d, want %d", payload.ListenID, listen.ID)
	}
	if payload.TrackName != "Second Thought" || payload.ArtistName != "Big Thief" || payload.AlbumName != "Dragon New Warm Mountain" {
		t.Errorf("payload names = %+v", payload)
	}
	if payload.Source != "spotify" {
		t.Errorf("Source = %q, want spotify", payload.Source)
	}
	if diff := payload.PlayedAt.Sub(playedAt); diff > time.Second || diff < -time.Second {
		t.Errorf("PlayedAt = %v, want %v", payload.PlayedAt, playedAt)
	}

	bus.PublishRecentListen(u.ID, nil)
	select {
	case unexpected := <-ch:
		t.Fatalf("a nil listen published %+v", unexpected)
	default:
	}
}
