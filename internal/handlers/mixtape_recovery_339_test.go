// Regression test for issue #339: a panic inside a mixtape generation goroutine
// must surface as a mixtape-error event, not take the server down.

package handlers

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"spotter/ent/enttest"
	"spotter/internal/config"
	"spotter/internal/events"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoverMixtapeGeneration_ReportsPanic(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent_mixtape_panic?mode=memory&_fk=1")
	defer client.Close()

	ctx := context.Background()
	u, err := client.User.Create().SetUsername("panic-user").Save(ctx)
	require.NoError(t, err)

	d, err := client.DJ.Create().
		SetName("Panic DJ").
		SetSystemPrompt("You are a test DJ").
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	m, err := client.Mixtape.Create().
		SetName("Panic Mixtape").
		SetMaxTracks(10).
		SetUser(u).
		SetDj(d).
		Save(ctx)
	require.NoError(t, err)

	bus := events.NewBus()
	eventsCh, cleanup := bus.Subscribe(u.ID)
	t.Cleanup(cleanup)

	h := New(client, &config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil, nil, nil, nil, nil, nil, nil, nil, bus, nil)

	goroutineDone := make(chan struct{})
	go func() {
		defer close(goroutineDone)
		defer h.recoverMixtapeGeneration(u.ID, m.ID, m.Name)
		panic("simulated generator panic")
	}()

	select {
	case <-goroutineDone:
	case <-time.After(5 * time.Second):
		t.Fatal("generation goroutine did not finish")
	}

	stored, err := client.Mixtape.Get(ctx, m.ID)
	require.NoError(t, err)
	assert.Contains(t, stored.GenerationError, "simulated generator panic")

	select {
	case event := <-eventsCh:
		assert.Equal(t, events.EventTypeMixtapeError, event.Type)
		payload, ok := event.Payload.(events.MixtapeErrorPayload)
		require.True(t, ok, "expected a mixtape-error payload")
		assert.Equal(t, m.ID, payload.MixtapeID)
		assert.Equal(t, m.Name, payload.MixtapeName)
		assert.Contains(t, payload.Error, "simulated generator panic")
	case <-time.After(2 * time.Second):
		t.Fatal("no mixtape-error event published")
	}
}

func TestRecoverMixtapeGeneration_IgnoresNormalReturn(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent_no_panic?mode=memory&_fk=1")
	defer client.Close()

	ctx := context.Background()
	u, err := client.User.Create().SetUsername("calm-user").Save(ctx)
	require.NoError(t, err)

	d, err := client.DJ.Create().
		SetName("Calm DJ").
		SetSystemPrompt("You are a test DJ").
		SetUser(u).
		Save(ctx)
	require.NoError(t, err)

	m, err := client.Mixtape.Create().
		SetName("Calm Mixtape").
		SetMaxTracks(10).
		SetUser(u).
		SetDj(d).
		Save(ctx)
	require.NoError(t, err)

	bus := events.NewBus()
	eventsCh, cleanup := bus.Subscribe(u.ID)
	t.Cleanup(cleanup)

	h := New(client, &config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil, nil, nil, nil, nil, nil, nil, nil, bus, nil)

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer h.recoverMixtapeGeneration(u.ID, m.ID, m.Name)
	}()

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("generation goroutine did not finish")
	}

	stored, err := client.Mixtape.Get(ctx, m.ID)
	require.NoError(t, err)
	assert.Empty(t, stored.GenerationError)

	select {
	case event := <-eventsCh:
		t.Fatalf("unexpected event on a clean run: %s", event.Type)
	case <-time.After(100 * time.Millisecond):
	}
}
