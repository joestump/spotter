package services_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"spotter/ent"
	"spotter/internal/config"
	"spotter/internal/enrichers"
	"spotter/internal/events"
	"spotter/internal/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetadataService_GetEnricherFactory_Registered(t *testing.T) {
	client := setupSimilarArtistsTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus()
	svc := services.NewMetadataService(client, nil, &config.Config{}, logger, bus)

	// Register a factory
	called := false
	factory := func(ctx context.Context, user *ent.User) (enrichers.Enricher, error) {
		called = true
		return nil, nil
	}
	require.NoError(t, svc.Register(enrichers.TypeSpotify, factory))

	// Get the factory back
	got, ok := svc.GetEnricherFactory(enrichers.TypeSpotify)
	require.True(t, ok, "registered factory should be found")
	require.NotNil(t, got)

	// Verify it's the same factory by calling it
	_, _ = got(context.Background(), nil)
	assert.True(t, called, "returned factory should be the one we registered")
}

func TestMetadataService_GetEnricherFactory_Unregistered(t *testing.T) {
	client := setupSimilarArtistsTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewBus()
	svc := services.NewMetadataService(client, nil, &config.Config{}, logger, bus)

	// Request an unregistered type — should return (nil, false)
	got, ok := svc.GetEnricherFactory(enrichers.TypeMusicBrainz)
	assert.False(t, ok, "unregistered factory should return false")
	assert.Nil(t, got, "unregistered factory should return nil")
}
