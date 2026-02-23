package events

import (
	"sync"
	"testing"
	"time"
)

// TestBusSubscriberMap verifies REQ-BUS-001: Bus maintains map of userID to subscriber channels.
func TestBusSubscriberMap(t *testing.T) {
	bus := NewBus()

	if bus.subscribers == nil {
		t.Fatal("expected subscribers map to be initialized")
	}

	ch, cleanup := bus.Subscribe(1)
	defer cleanup()

	if ch == nil {
		t.Fatal("expected non-nil channel from Subscribe")
	}

	bus.mu.RLock()
	chans, ok := bus.subscribers[1]
	bus.mu.RUnlock()

	if !ok {
		t.Fatal("expected userID 1 in subscribers map")
	}
	if len(chans) != 1 {
		t.Fatalf("expected 1 subscriber channel, got %d", len(chans))
	}
}

// TestSubscribeReturnsReadOnlyChannelAndCleanup verifies REQ-BUS-002:
// Subscribe returns a buffered read-only channel and cleanup function.
func TestSubscribeReturnsReadOnlyChannelAndCleanup(t *testing.T) {
	bus := NewBus()

	ch, cleanup := bus.Subscribe(42)

	// Verify channel is buffered with capacity 10
	if cap(ch) != 10 {
		t.Fatalf("expected channel capacity 10, got %d", cap(ch))
	}

	// Verify cleanup removes subscriber and closes channel
	cleanup()

	bus.mu.RLock()
	chans := bus.subscribers[42]
	bus.mu.RUnlock()

	if len(chans) != 0 {
		t.Fatalf("expected 0 subscribers after cleanup, got %d", len(chans))
	}

	// Verify the channel was closed by reading from it (should return zero value immediately)
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed after cleanup")
		}
	default:
		t.Fatal("expected closed channel to be readable (returning zero value)")
	}
}

// TestPublishFanOut verifies REQ-BUS-003 and REQ-BUS-004:
// Publish fans out to all subscribers; multiple subscribers each receive the event.
func TestPublishFanOut(t *testing.T) {
	bus := NewBus()

	ch1, cleanup1 := bus.Subscribe(1)
	defer cleanup1()
	ch2, cleanup2 := bus.Subscribe(1)
	defer cleanup2()

	event := Event{Type: EventTypeNotification, Payload: "test"}
	bus.Publish(1, event)

	// Both subscribers should receive the event
	select {
	case e := <-ch1:
		if e.Type != EventTypeNotification {
			t.Fatalf("ch1: expected event type %s, got %s", EventTypeNotification, e.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("ch1: timed out waiting for event")
	}

	select {
	case e := <-ch2:
		if e.Type != EventTypeNotification {
			t.Fatalf("ch2: expected event type %s, got %s", EventTypeNotification, e.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("ch2: timed out waiting for event")
	}
}

// TestPublishZeroSubscribersIsNoOp verifies REQ-BUS-005:
// Publishing to a user with zero subscribers must be a no-op.
func TestPublishZeroSubscribersIsNoOp(t *testing.T) {
	bus := NewBus()

	// Should not panic or block
	bus.Publish(999, Event{Type: EventTypeNotification, Payload: "nobody listening"})
}

// TestPublishDropsWhenBufferFull verifies the non-blocking send behavior from REQ-BUS-003.
func TestPublishDropsWhenBufferFull(t *testing.T) {
	bus := NewBus()

	ch, cleanup := bus.Subscribe(1)
	defer cleanup()

	// Fill the buffer (capacity 10)
	for i := 0; i < 10; i++ {
		bus.Publish(1, Event{Type: EventTypeNotification, Payload: i})
	}

	// This 11th publish should not block; event is dropped
	done := make(chan struct{})
	go func() {
		bus.Publish(1, Event{Type: EventTypeNotification, Payload: "overflow"})
		close(done)
	}()

	select {
	case <-done:
		// Success: publish did not block
	case <-time.After(time.Second):
		t.Fatal("Publish blocked when buffer was full")
	}

	// Drain and verify we got exactly 10 events
	count := 0
	for range 10 {
		select {
		case <-ch:
			count++
		default:
		}
	}
	if count != 10 {
		t.Fatalf("expected 10 buffered events, got %d", count)
	}
}

// TestConcurrentPublishSubscribe verifies thread safety under concurrent access.
func TestConcurrentPublishSubscribe(t *testing.T) {
	bus := NewBus()
	var wg sync.WaitGroup

	// Start multiple subscribers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cleanup := bus.Subscribe(1)
			defer cleanup()

			// Read a few events
			for range 3 {
				select {
				case <-ch:
				case <-time.After(time.Second):
				}
			}
		}()
	}

	// Publish concurrently
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Publish(1, Event{Type: EventTypeNotification, Payload: "concurrent"})
		}()
	}

	wg.Wait()
}

// TestCleanupRemovesUserEntryWhenEmpty verifies that cleanup deletes the map entry
// when the last subscriber for a user is removed.
func TestCleanupRemovesUserEntryWhenEmpty(t *testing.T) {
	bus := NewBus()

	_, cleanup := bus.Subscribe(1)
	cleanup()

	bus.mu.RLock()
	_, exists := bus.subscribers[1]
	bus.mu.RUnlock()

	if exists {
		t.Fatal("expected userID entry to be deleted from map when last subscriber removed")
	}
}
