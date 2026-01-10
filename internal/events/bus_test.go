package events

import (
	"sync"
	"testing"
	"time"
)

func TestBus_Subscribe(t *testing.T) {
	bus := NewBus()
	userID := 1

	ch, cleanup := bus.Subscribe(userID)
	defer cleanup()

	if ch == nil {
		t.Fatal("Subscribe returned nil channel")
	}

	// Verify we can receive events
	testEvent := Event{
		Type:    EventTypeNotification,
		Payload: NotificationPayload{Title: "Test", Message: "Test message", IconType: "info"},
	}

	bus.Publish(userID, testEvent)

	select {
	case received := <-ch:
		if received.Type != testEvent.Type {
			t.Errorf("Expected event type %v, got %v", testEvent.Type, received.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for event")
	}
}

func TestBus_Publish_NonBlockingWhenChannelFull(t *testing.T) {
	bus := NewBus()
	userID := 1

	ch, cleanup := bus.Subscribe(userID)
	defer cleanup()

	// Fill the channel (buffer size is 10)
	for i := 0; i < 10; i++ {
		bus.Publish(userID, Event{Type: EventTypeNotification})
	}

	// This should not block even though channel is full
	done := make(chan bool)
	go func() {
		bus.Publish(userID, Event{Type: EventTypeNotification})
		done <- true
	}()

	select {
	case <-done:
		// Success - publish didn't block
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Publish blocked when channel was full")
	}

	// Drain the channel
	for i := 0; i < 10; i++ {
		<-ch
	}
}

func TestBus_Shutdown_ClosesAllChannels(t *testing.T) {
	bus := NewBus()

	// Create multiple subscribers for different users
	userIDs := []int{1, 2, 3}
	channels := make([]<-chan Event, 0)

	for _, userID := range userIDs {
		ch, _ := bus.Subscribe(userID)
		channels = append(channels, ch)

		// Subscribe multiple times for the same user
		ch2, _ := bus.Subscribe(userID)
		channels = append(channels, ch2)
	}

	// Shutdown the bus
	bus.Shutdown()

	// Verify all channels are closed
	for i, ch := range channels {
		select {
		case _, ok := <-ch:
			if ok {
				t.Errorf("Channel %d is still open after Shutdown", i)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("Channel %d did not close within timeout", i)
		}
	}

	// Verify subscribers map is empty
	bus.mu.RLock()
	if len(bus.subscribers) != 0 {
		t.Errorf("Expected subscribers map to be empty, got %d entries", len(bus.subscribers))
	}
	bus.mu.RUnlock()
}

func TestBus_Shutdown_CanBeCalledMultipleTimes(t *testing.T) {
	bus := NewBus()

	ch1, _ := bus.Subscribe(1)
	ch2, _ := bus.Subscribe(2)

	// First shutdown
	bus.Shutdown()

	// Verify channels are closed
	if _, ok := <-ch1; ok {
		t.Error("Channel 1 should be closed after first Shutdown")
	}
	if _, ok := <-ch2; ok {
		t.Error("Channel 2 should be closed after first Shutdown")
	}

	// Second shutdown should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Second Shutdown() panicked: %v", r)
		}
	}()
	bus.Shutdown()

	// Third shutdown should also not panic
	bus.Shutdown()
}

func TestBus_PublishAfterShutdown_DoesNotPanic(t *testing.T) {
	bus := NewBus()
	userID := 1

	ch, _ := bus.Subscribe(userID)

	bus.Shutdown()

	// Verify channel is closed
	if _, ok := <-ch; ok {
		t.Error("Channel should be closed after Shutdown")
	}

	// Publishing after shutdown should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Publish after Shutdown panicked: %v", r)
		}
	}()

	bus.Publish(userID, Event{Type: EventTypeNotification})
}

func TestBus_SubscribeAfterShutdown(t *testing.T) {
	bus := NewBus()

	bus.Shutdown()

	// Subscribe after shutdown should work (creates new entry in empty map)
	ch, cleanup := bus.Subscribe(1)
	defer cleanup()

	if ch == nil {
		t.Fatal("Subscribe after Shutdown returned nil channel")
	}

	// Should be able to receive events
	testEvent := Event{Type: EventTypeNotification}
	bus.Publish(1, testEvent)

	select {
	case received := <-ch:
		if received.Type != testEvent.Type {
			t.Errorf("Expected event type %v, got %v", testEvent.Type, received.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for event after Shutdown and re-subscribe")
	}
}

func TestBus_ConcurrentPublish(t *testing.T) {
	bus := NewBus()
	userID := 1

	ch, cleanup := bus.Subscribe(userID)
	defer cleanup()

	// Start goroutine to consume events
	received := make(chan int, 100)
	go func() {
		for range ch {
			received <- 1
		}
	}()

	// Publish from multiple goroutines concurrently
	var wg sync.WaitGroup
	numGoroutines := 10
	eventsPerGoroutine := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				bus.Publish(userID, Event{Type: EventTypeNotification})
			}
		}()
	}

	wg.Wait()

	// Give some time for events to be consumed
	time.Sleep(50 * time.Millisecond)

	close(received)

	// Count received events (may be less than total due to non-blocking send)
	count := 0
	for range received {
		count++
	}

	if count == 0 {
		t.Error("Expected to receive at least some events")
	}

	// Should receive at most all events (buffer size might drop some)
	maxExpected := numGoroutines * eventsPerGoroutine
	if count > maxExpected {
		t.Errorf("Received more events (%d) than published (%d)", count, maxExpected)
	}
}

func TestBus_CleanupFunction(t *testing.T) {
	bus := NewBus()
	userID := 1

	ch1, cleanup1 := bus.Subscribe(userID)
	ch2, cleanup2 := bus.Subscribe(userID)

	// Verify both channels are registered
	bus.mu.RLock()
	if len(bus.subscribers[userID]) != 2 {
		t.Errorf("Expected 2 subscribers for user %d, got %d", userID, len(bus.subscribers[userID]))
	}
	bus.mu.RUnlock()

	// Cleanup first subscription
	cleanup1()

	// Verify first channel is closed
	if _, ok := <-ch1; ok {
		t.Error("Channel 1 should be closed after cleanup")
	}

	// Verify second channel still works
	bus.Publish(userID, Event{Type: EventTypeNotification})
	select {
	case <-ch2:
		// Success
	case <-time.After(100 * time.Millisecond):
		t.Error("Channel 2 should still receive events after channel 1 cleanup")
	}

	// Cleanup second subscription
	cleanup2()

	// Verify user entry is removed from map when last subscriber is removed
	bus.mu.RLock()
	if _, exists := bus.subscribers[userID]; exists {
		t.Errorf("User %d should be removed from subscribers after all cleanups", userID)
	}
	bus.mu.RUnlock()
}
