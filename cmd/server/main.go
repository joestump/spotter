package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"spotter/ent"
	"spotter/ent/user"
	"spotter/internal/config"
	"spotter/internal/crypto"
	"spotter/internal/database"
	"spotter/internal/enrichers"
	enricherFanart "spotter/internal/enrichers/fanart"
	enricherLastfm "spotter/internal/enrichers/lastfm"
	enricherLidarr "spotter/internal/enrichers/lidarr"
	enricherMusicbrainz "spotter/internal/enrichers/musicbrainz"
	enricherNavidrome "spotter/internal/enrichers/navidrome"
	enricherOpenai "spotter/internal/enrichers/openai"
	enricherSpotify "spotter/internal/enrichers/spotify"
	"spotter/internal/events"
	"spotter/internal/handlers"
	internalMiddleware "spotter/internal/middleware"
	"spotter/internal/providers/lastfm"
	"spotter/internal/providers/navidrome"
	"spotter/internal/providers/spotify"
	"spotter/internal/services"
	"spotter/internal/vibes"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/sync/errgroup"
)

func main() {
	opts := &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, opts))

	// Load Config
	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Initialize encryption for sensitive data
	encryptionKey, err := cfg.GetEncryptionKeyBytes()
	if err != nil {
		logger.Error("failed to get encryption key", "error", err)
		os.Exit(1)
	}
	encryptor, err := crypto.NewEncryptor(encryptionKey)
	if err != nil {
		logger.Error("failed to initialize encryptor", "error", err)
		os.Exit(1)
	}
	logger.Info("encryption initialized for sensitive data")

	// Connect to Database
	client, err := database.NewClient(cfg.Database.Driver, cfg.Database.Source, encryptor)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}

	// Initialize Event Bus
	bus := events.NewBus()

	// Initialize Sync Service (for playlists and listens)
	syncer := services.NewSyncer(client, cfg, logger, bus)
	syncer.Register(navidrome.New(logger, cfg))
	syncer.Register(spotify.New(logger, cfg))
	syncer.Register(lastfm.New(logger, cfg))

	// Initialize Playlist Sync Service (for syncing playlists to Navidrome)
	playlistSyncSvc := services.NewPlaylistSyncService(client, cfg, logger, bus)
	playlistSyncSvc.Register(navidrome.New(logger, cfg))

	// Initialize Metadata Service (for catalog enrichment)
	metadataSvc := services.NewMetadataService(client, cfg, logger, bus)
	metadataSvc.Register(enrichers.TypeLidarr, enricherLidarr.New(logger, cfg, client))
	metadataSvc.Register(enrichers.TypeMusicBrainz, enricherMusicbrainz.New(logger, cfg))
	metadataSvc.Register(enrichers.TypeNavidrome, enricherNavidrome.New(logger, cfg))
	metadataSvc.Register(enrichers.TypeSpotify, enricherSpotify.New(logger, cfg))
	metadataSvc.Register(enrichers.TypeLastFM, enricherLastfm.New(logger, cfg))
	metadataSvc.Register(enrichers.TypeFanart, enricherFanart.New(logger, cfg))
	metadataSvc.Register(enrichers.TypeOpenAI, enricherOpenai.New(logger, cfg))

	// Initialize Mixtape Generator Service (for AI-powered mixtape generation)
	mixtapeGenerator := vibes.NewMixtapeGenerator(client, cfg, logger, bus)
	logger.Info("vibes mixtape generator initialized",
		"default_max_tracks", cfg.Vibes.DefaultMaxTracks,
		"model", cfg.GetVibesModel(),
		"temperature", cfg.Vibes.Temperature)

	// Initialize Playlist Enhancer Service (for AI-powered playlist enhancement)
	playlistEnhancer := vibes.NewPlaylistEnhancer(client, cfg, logger, bus)
	logger.Info("playlist enhancer initialized")

	// Initialize Similar Artists Service (for AI-powered artist similarity detection)
	similarArtistsSvc := services.NewSimilarArtistsService(client, cfg, logger, bus)
	logger.Info("similar artists service initialized")

	// Initialize Handlers
	h := handlers.New(client, cfg, logger, encryptor, syncer, metadataSvc, playlistSyncSvc, mixtapeGenerator, playlistEnhancer, similarArtistsSvc, bus)

	// Create root context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())

	// Create errgroup for managing background goroutines
	g, gctx := errgroup.WithContext(ctx)

	// Background Sync Loop for listens/playlists
	syncInterval, err := time.ParseDuration(cfg.Sync.Interval)
	if err != nil {
		logger.Error("invalid sync interval, using default 5m", "error", err, "value", cfg.Sync.Interval)
		syncInterval = 5 * time.Minute
	}
	logger.Info("background sync configured", "interval", syncInterval)

	g.Go(func() error {
		return runBackgroundSyncLoop(gctx, client, syncer, syncInterval, logger)
	})

	// Background Metadata Enrichment Loop
	if cfg.Metadata.Enabled {
		var metadataInterval time.Duration
		metadataInterval, err = time.ParseDuration(cfg.Metadata.Interval)
		if err != nil {
			logger.Error("invalid metadata interval, using default 1h", "error", err, "value", cfg.Metadata.Interval)
			metadataInterval = 1 * time.Hour
		}
		logger.Info("metadata enrichment configured",
			"interval", metadataInterval,
			"order", cfg.MetadataEnricherOrder())

		g.Go(func() error {
			return runMetadataEnrichmentLoop(gctx, client, metadataSvc, metadataInterval, logger)
		})
	} else {
		logger.Info("metadata enrichment disabled")
	}

	// Background Playlist Sync Loop (for syncing playlists to Navidrome)
	playlistSyncInterval, err := time.ParseDuration(cfg.PlaylistSync.SyncInterval)
	if err != nil {
		logger.Error("invalid playlist sync interval, using default 1h", "error", err, "value", cfg.PlaylistSync.SyncInterval)
		playlistSyncInterval = 1 * time.Hour
	}
	logger.Info("playlist sync configured", "interval", playlistSyncInterval)

	g.Go(func() error {
		return runPlaylistSyncLoop(gctx, client, playlistSyncSvc, playlistSyncInterval, logger)
	})

	// Router setup
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(internalMiddleware.Logger(logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// Static Files
	fileServer := http.FileServer(http.Dir("./static"))
	r.Handle("/static/*", http.StripPrefix("/static", fileServer))

	// Data Files (images, etc.)
	dataFileServer := http.FileServer(http.Dir("./data"))
	r.Handle("/data/*", http.StripPrefix("/data", dataFileServer))

	// Public Routes
	r.Group(func(r chi.Router) {
		r.Get("/auth/login", h.Login)
		r.Post("/login", h.PostLogin)
		r.Get("/logout", h.Logout)
		r.Get("/auth/logout", h.Logout) // Alias for consistency

		// OAuth callbacks must be public (no session required)
		// They will validate session internally
		r.Get("/auth/spotify/callback", h.SpotifyCallback)
		r.Get("/auth/lastfm/callback", h.LastFMCallback)
	})

	// Protected Routes
	r.Group(func(r chi.Router) {
		r.Use(AuthMiddleware(client))
		r.Get("/", h.Home)

		r.Get("/events", h.Events)
		r.Post("/generate", h.GeneratePlaylist)

		r.Get("/preferences", h.PreferencesRedirect)
		r.Get("/preferences/appearance", h.PreferencesAppearance)
		r.Post("/preferences/appearance", h.PostPreferencesAppearance)
		r.Get("/preferences/providers", h.PreferencesProviders)
		r.Get("/preferences/tasks", h.PreferencesTasks)

		// Provider-specific sync/rebuild routes
		r.Post("/preferences/navidrome/sync", h.SyncNavidrome)
		r.Post("/preferences/navidrome/rebuild", h.RebuildNavidrome)
		r.Post("/preferences/spotify/sync", h.SyncSpotify)
		r.Post("/preferences/spotify/rebuild", h.RebuildSpotify)
		r.Post("/preferences/spotify/disconnect", h.DisconnectSpotify)
		r.Post("/preferences/lastfm/sync", h.SyncLastFM)
		r.Post("/preferences/lastfm/rebuild", h.RebuildLastFM)
		r.Post("/preferences/lastfm/disconnect", h.DisconnectLastFM)

		// Task routes
		r.Post("/preferences/tasks/sync-listens", h.TaskSyncListens)
		r.Post("/preferences/tasks/sync-playlists", h.TaskSyncPlaylists)
		r.Post("/preferences/tasks/enrich-metadata", h.TaskEnrichMetadata)
		r.Post("/preferences/tasks/sync-artist-images", h.TaskSyncArtistImages)
		r.Post("/preferences/tasks/sync-album-images", h.TaskSyncAlbumImages)
		r.Post("/preferences/tasks/reset", h.TaskResetData)
		r.Post("/preferences/tasks/cleanup", h.TaskCleanup)

		// OAuth login initiators (require existing session)
		r.Get("/auth/spotify/login", h.SpotifyLogin)
		r.Get("/auth/lastfm/login", h.LastFMLogin)

		r.Get("/recent", h.RecentListens)
		r.Get("/playlists", h.Playlists)
		r.Get("/playlists/{id}", h.PlaylistShow)
		r.Get("/playlists/{id}.png", h.PlaylistImage)
		r.Post("/playlists/{id}/toggle-sync", h.TogglePlaylistSync)
		r.Post("/playlists/{id}/sync", h.SyncPlaylist)
		r.Post("/playlists/{id}/rebuild-sync", h.RebuildPlaylistSync)
		r.Get("/playlists/{id}/sync-status", h.GetPlaylistSyncStatus)
		r.Get("/playlists/{id}/sync-progress", h.GetPlaylistSyncProgress)
		r.Post("/playlists/{id}/debug-sync", h.DebugPlaylistSync)
		r.Post("/playlists/{id}/ai/generate-metadata", h.PlaylistGenerateMetadata)
		r.Post("/playlists/{id}/ai/generate-artwork", h.PlaylistGenerateArtwork)
		r.Get("/playlists/{id}/enhance-vibes-modal", h.EnhanceVibesModal)
		r.Post("/playlists/{id}/enhance-vibes", h.EnhanceVibes)

		// Vibes routes (DJs and Mixtapes)
		r.Get("/vibes", h.VibesRedirect)
		r.Route("/vibes/djs", func(r chi.Router) {
			r.Get("/", h.DJsIndex)
			r.Post("/", h.CreateDJ)
			r.Get("/{id}", h.DJShow)
			r.Put("/{id}", h.UpdateDJ)
			r.Delete("/{id}", h.DeleteDJ)
			r.Get("/suggestions/genres", h.GenreSuggestions)
			r.Get("/suggestions/artists", h.ArtistSuggestions)
		})
		r.Route("/vibes/mixtapes", func(r chi.Router) {
			r.Get("/", h.MixtapesIndex)
			r.Get("/{id}", h.MixtapeShow)
			r.Post("/", h.CreateMixtape)
			r.Put("/{id}", h.UpdateMixtape)
			r.Delete("/{id}", h.DeleteMixtape)
			r.Post("/{id}/toggle-sync", h.ToggleMixtapeSync)
			r.Post("/{id}/generate", h.GenerateMixtape)
		})

		// Library routes (artists, albums, tracks)
		r.Route("/library", func(r chi.Router) {
			// Artist routes
			r.Get("/artists", h.ArtistIndex)
			r.Get("/artist/{id}", h.ArtistShow)
			r.Get("/artist/{id}.png", h.ArtistImage)
			r.Get("/artist/{id}/chart", h.ArtistChart)
			r.Post("/artist/{id}/regenerate-ai", h.ArtistRegenerateAI)
			r.Post("/artist/{id}/find-similar", h.ArtistFindSimilar)
			r.Post("/artist/{id}/create-mixtape", h.ArtistCreateMixtape)
			r.Get("/artist/{id}/mixtape-modal", h.ArtistMixtapeModal)

			// Album routes
			r.Get("/albums", h.AlbumIndex)
			r.Get("/album/{id}", h.AlbumShow)
			r.Get("/album/{id}.png", h.AlbumImage)
			r.Get("/album/{id}/chart", h.AlbumChart)
			r.Post("/album/{id}/regenerate-ai", h.AlbumRegenerateAI)
			r.Post("/album/{id}/create-mixtape", h.AlbumCreateMixtape)
			r.Get("/album/{id}/mixtape-modal", h.AlbumMixtapeModal)

			// Track routes
			r.Get("/tracks", h.TrackIndex)
			r.Get("/track/{id}", h.TrackShow)
			r.Get("/track/{id}/chart", h.TrackChart)
			r.Post("/track/{id}/regenerate-ai", h.TrackRegenerateAI)
		})
	})

	// Create HTTP server
	addr := fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// Channel to listen for shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Start server in goroutine
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting server", "addr", addr)
		serverErrors <- srv.ListenAndServe()
	}()

	// Wait for shutdown signal or server error
	select {
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			cancel() // Cancel background goroutines
			if closeErr := client.Close(); closeErr != nil {
				logger.Error("failed to close database client during error exit", "error", closeErr)
			}
			os.Exit(1)
		}
	case sig := <-sigCh:
		logger.Info("shutdown signal received", "signal", sig)

		// Cancel context for background goroutines
		cancel()

		// Create shutdown context with timeout
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer shutdownCancel()

		// Gracefully shutdown HTTP server
		logger.Info("shutting down HTTP server", "timeout", "25s")
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("error during HTTP server shutdown", "error", err)
		}

		// Wait for background goroutines to finish
		logger.Info("waiting for background goroutines to finish")
		if err := g.Wait(); err != nil && err != context.Canceled {
			logger.Error("error during background goroutine shutdown", "error", err)
		}

		// Shutdown event bus
		logger.Info("shutting down event bus")
		bus.Shutdown()

		// Close database connection
		logger.Info("closing database connection")
		if closeErr := client.Close(); closeErr != nil {
			logger.Error("failed to close database client", "error", closeErr)
		}

		logger.Info("graceful shutdown complete")
	}
}

// runMetadataSync runs metadata enrichment for all users.
func runMetadataSync(ctx context.Context, client *ent.Client, metadataSvc *services.MetadataService, logger *slog.Logger) {
	users, err := client.User.Query().All(ctx)
	if err != nil {
		logger.Error("failed to fetch users for metadata sync", "error", err)
		return
	}
	for _, u := range users {
		go func(user *ent.User) {
			if err := metadataSvc.SyncAll(ctx, user); err != nil {
				logger.Error("metadata sync failed", "username", user.Username, "error", err)
			}
		}(u)
	}
}

// runBackgroundSyncLoop runs periodic sync for all users.
func runBackgroundSyncLoop(ctx context.Context, client *ent.Client, syncer *services.Syncer, interval time.Duration, logger *slog.Logger) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("background sync loop shutting down")
			return ctx.Err()
		case <-ticker.C:
			users, err := client.User.Query().All(ctx)
			if err != nil {
				logger.Error("failed to fetch users for background sync", "error", err)
				continue
			}
			for _, u := range users {
				// Check context before spawning goroutine
				if ctx.Err() != nil {
					return ctx.Err()
				}
				go func(user *ent.User) {
					if err := syncer.Sync(ctx, user); err != nil {
						logger.Error("background sync failed", "username", user.Username, "error", err)
					}
				}(u)
			}
		}
	}
}

// runMetadataEnrichmentLoop runs periodic metadata enrichment.
func runMetadataEnrichmentLoop(ctx context.Context, client *ent.Client, metadataSvc *services.MetadataService, interval time.Duration, logger *slog.Logger) error {
	// Initial delay to let the app start up
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(30 * time.Second):
	}

	// Run immediately on startup
	runMetadataSync(ctx, client, metadataSvc, logger)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("metadata enrichment loop shutting down")
			return ctx.Err()
		case <-ticker.C:
			runMetadataSync(ctx, client, metadataSvc, logger)
		}
	}
}

// runPlaylistSyncLoop runs periodic playlist sync to external services.
func runPlaylistSyncLoop(ctx context.Context, client *ent.Client, playlistSyncSvc *services.PlaylistSyncService, interval time.Duration, logger *slog.Logger) error {
	// Initial delay to let the app start up
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(1 * time.Minute):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("playlist sync loop shutting down")
			return ctx.Err()
		case <-ticker.C:
			users, err := client.User.Query().All(ctx)
			if err != nil {
				logger.Error("failed to fetch users for playlist sync", "error", err)
				continue
			}
			for _, u := range users {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				go func(user *ent.User) {
					if err := playlistSyncSvc.SyncAllEnabledPlaylists(ctx, user.ID); err != nil {
						logger.Error("playlist sync failed", "username", user.Username, "error", err)
					}
				}(u)
			}
		}
	}
}

func AuthMiddleware(client *ent.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("spotter_user")
			if err != nil {
				http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
				return
			}

			username := cookie.Value
			if username == "" {
				http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
				return
			}

			u, err := client.User.Query().
				Where(user.Username(username)).
				Only(r.Context())

			if err != nil {
				// User not found or db error, redirect to login
				http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
				return
			}

			ctx := context.WithValue(r.Context(), handlers.UserContextKey, u)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
