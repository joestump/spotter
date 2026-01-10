package main

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"spotter/ent/enttest"
	"spotter/internal/config"
	"spotter/internal/services"

	_ "github.com/mattn/go-sqlite3"
)

func TestRunBackgroundSyncLoop_ExitsOnContextCancellation(t *testing.T) {
	// Create test database
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Create logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create test config and syncer
	cfg := &config.Config{}
	syncer := services.NewSyncer(client, cfg, logger, nil)

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Start the loop in a goroutine
	done := make(chan error, 1)
	go func() {
		done <- runBackgroundSyncLoop(ctx, client, syncer, 10*time.Millisecond, logger)
	}()

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	// Cancel the context
	cancel()

	// Verify the function exits within reasonable time
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Expected context.Canceled error, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("runBackgroundSyncLoop did not exit within timeout after context cancellation")
	}
}

func TestRunMetadataEnrichmentLoop_ExitsOnContextCancellation(t *testing.T) {
	// Create test database
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Create logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create test config and metadata service
	cfg := &config.Config{}
	metadataSvc := services.NewMetadataService(client, cfg, logger, nil)

	// Create cancellable context that is cancelled immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before starting

	// Start the loop - should exit during initial delay
	err := runMetadataEnrichmentLoop(ctx, client, metadataSvc, 1*time.Hour, logger)

	if err != context.Canceled {
		t.Errorf("Expected context.Canceled error, got %v", err)
	}
}

func TestRunMetadataEnrichmentLoop_ExitsAfterInitialDelay(t *testing.T) {
	// Create test database
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Create logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create test config and metadata service
	cfg := &config.Config{}
	metadataSvc := services.NewMetadataService(client, cfg, logger, nil)

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Start the loop in a goroutine with very short interval
	done := make(chan error, 1)
	go func() {
		done <- runMetadataEnrichmentLoop(ctx, client, metadataSvc, 10*time.Millisecond, logger)
	}()

	// Let it run through initial delay and at least one tick
	time.Sleep(100 * time.Millisecond)

	// Cancel the context
	cancel()

	// Verify the function exits within reasonable time
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Expected context.Canceled error, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("runMetadataEnrichmentLoop did not exit within timeout after context cancellation")
	}
}

func TestRunPlaylistSyncLoop_ExitsOnContextCancellation(t *testing.T) {
	// Create test database
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Create logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create test config and playlist sync service
	cfg := &config.Config{}
	playlistSyncSvc := services.NewPlaylistSyncService(client, cfg, logger, nil)

	// Create cancellable context that is cancelled immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before starting

	// Start the loop - should exit during initial delay
	err := runPlaylistSyncLoop(ctx, client, playlistSyncSvc, 1*time.Hour, logger)

	if err != context.Canceled {
		t.Errorf("Expected context.Canceled error, got %v", err)
	}
}

func TestRunPlaylistSyncLoop_ExitsAfterInitialDelay(t *testing.T) {
	// Create test database
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Create logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create test config and playlist sync service
	cfg := &config.Config{}
	playlistSyncSvc := services.NewPlaylistSyncService(client, cfg, logger, nil)

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Start the loop in a goroutine with very short interval
	done := make(chan error, 1)
	go func() {
		done <- runPlaylistSyncLoop(ctx, client, playlistSyncSvc, 10*time.Millisecond, logger)
	}()

	// Let it run through initial delay and at least one tick
	time.Sleep(150 * time.Millisecond)

	// Cancel the context
	cancel()

	// Verify the function exits within reasonable time
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Expected context.Canceled error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runPlaylistSyncLoop did not exit within timeout after context cancellation")
	}
}

func TestRunMetadataSync_RespectsContext(t *testing.T) {
	// Create test database
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Create logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create test config and metadata service
	cfg := &config.Config{}
	metadataSvc := services.NewMetadataService(client, cfg, logger, nil)

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Run metadata sync with cancelled context - should not panic
	runMetadataSync(ctx, client, metadataSvc, logger)

	// If we get here without panic, test passes
}

func TestBackgroundLoops_HandleEmptyDatabase(t *testing.T) {
	// Create test database with no users
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Create logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create test config
	cfg := &config.Config{}

	tests := []struct {
		name string
		fn   func(context.Context) error
	}{
		{
			name: "runBackgroundSyncLoop",
			fn: func(ctx context.Context) error {
				syncer := services.NewSyncer(client, cfg, logger, nil)
				return runBackgroundSyncLoop(ctx, client, syncer, 10*time.Millisecond, logger)
			},
		},
		{
			name: "runMetadataEnrichmentLoop",
			fn: func(ctx context.Context) error {
				metadataSvc := services.NewMetadataService(client, cfg, logger, nil)
				return runMetadataEnrichmentLoop(ctx, client, metadataSvc, 10*time.Millisecond, logger)
			},
		},
		{
			name: "runPlaylistSyncLoop",
			fn: func(ctx context.Context) error {
				playlistSyncSvc := services.NewPlaylistSyncService(client, cfg, logger, nil)
				return runPlaylistSyncLoop(ctx, client, playlistSyncSvc, 10*time.Millisecond, logger)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())

			done := make(chan error, 1)
			go func() {
				done <- tt.fn(ctx)
			}()

			// Let it run a couple iterations
			time.Sleep(150 * time.Millisecond)

			// Cancel and verify clean exit
			cancel()

			select {
			case err := <-done:
				if err != context.Canceled {
					t.Errorf("Expected context.Canceled error, got %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("%s did not exit within timeout", tt.name)
			}
		})
	}
}
