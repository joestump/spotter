package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"spotter/ent"
	"spotter/internal/auth"
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
	"golang.org/x/time/rate"
)

func main() {
	// Governing: ADR-0019 (structured metrics), ADR-0010 (slog), SPEC observability REQ "FMT-001", REQ "FMT-002", REQ "FMT-003", REQ "FMT-004", REQ "FMT-005"
	// Bootstrap logger with text handler; will be replaced after config load
	bootstrapOpts := &slog.HandlerOptions{Level: slog.LevelDebug}
	logger := slog.New(slog.NewTextHandler(os.Stdout, bootstrapOpts))

	// Load Config
	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Re-initialize logger with configured format
	opts := &slog.HandlerOptions{Level: slog.LevelDebug}
	format := strings.ToLower(cfg.Log.Format)
	if format != "json" {
		format = "text" // REQ "FMT-002": invalid values default to text
	}
	if format == "json" {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, opts))
	} else {
		logger = slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	logger.Info("logger initialized", "format", format, "level", "debug")

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

	// Initialize JWT Manager
	jwtManager := auth.NewJWTManager(cfg.Security.JWTSecret)
	logger.Info("JWT manager initialized")

	// Connect to Database
	client, err := database.NewClient(cfg.Database.Driver, cfg.Database.Source, encryptor)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := client.Close(); err != nil {
			logger.Error("failed to close database client", "error", err)
		}
	}()
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
	h := handlers.New(client, cfg, logger, encryptor, jwtManager, syncer, metadataSvc, playlistSyncSvc, mixtapeGenerator, playlistEnhancer, similarArtistsSvc, bus)

	// Governing: ADR-0007 (graceful shutdown), ADR-0018 (graceful shutdown), SPEC graceful-shutdown REQ "SHUTDOWN-001"
	// Governing: SPEC graceful-shutdown REQ-SIG-001 (signal.NotifyContext for SIGTERM/SIGINT)
	// Governing: SPEC graceful-shutdown REQ-SIG-002 (root context is parent for all background loops)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	// Governing: SPEC graceful-shutdown REQ-SIG-003 (defer stop to release signal resources)
	defer stop()

	// Governing: SPEC graceful-shutdown REQ-TMO-001 (30s default shutdown budget)
	// Governing: SPEC graceful-shutdown REQ-TMO-005 (configurable shutdown timeout)
	shutdownTimeout := 30 * time.Second
	if s := os.Getenv("SPOTTER_SHUTDOWN_TIMEOUT"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			shutdownTimeout = d
		}
	}

	// Governing: SPEC graceful-shutdown REQ-SEM-001 through REQ-SEM-004 (bounded concurrency semaphore)
	// Governing: SPEC graceful-shutdown REQ-SEM-002 (configurable semaphore capacity, default 10)
	maxJobs := 10
	if s := os.Getenv("SPOTTER_MAX_CONCURRENT_JOBS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			maxJobs = n
		}
	}
	sem := make(chan struct{}, maxJobs)

	// Governing: SPEC graceful-shutdown REQ-WG-001 (single shared WaitGroup for all per-user goroutines)
	var wg sync.WaitGroup

	// Background Sync Loop for listens/playlists
	syncInterval, err := time.ParseDuration(cfg.Sync.Interval)
	if err != nil {
		logger.Error("invalid sync interval, using default 5m", "error", err, "value", cfg.Sync.Interval)
		syncInterval = 5 * time.Minute
	}
	logger.Info("background sync configured", "interval", syncInterval)

	// Governing: ADR-0019 (structured metrics), SPEC observability REQ "BG-001", REQ "BG-002"
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(syncInterval)
		defer ticker.Stop()
		// Governing: SPEC graceful-shutdown REQ-CTX-001 (select on ctx.Done vs ticker.C)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tickStart := time.Now()
				// Governing: SPEC graceful-shutdown REQ-CTX-003 (per-user goroutines use root ctx)
				users, err := client.User.Query().All(ctx)
				if err != nil {
					logger.Error("failed to fetch users for background sync", "error", err)
					continue
				}
				syncErrors := 0
				for _, u := range users {
					// Governing: SPEC graceful-shutdown REQ-WG-002 (wg.Add before spawn, defer wg.Done first)
					wg.Add(1)
					go func(user *ent.User) {
						defer wg.Done()
						// Governing: SPEC graceful-shutdown REQ-SEM-003, REQ-SEM-004 (semaphore acquire with ctx)
						select {
						case sem <- struct{}{}:
							defer func() { <-sem }()
						case <-ctx.Done():
							return
						}
						// Governing: SPEC graceful-shutdown REQ-CTX-002 (cancelled ctx passed to service methods)
						if err := syncer.Sync(ctx, user); err != nil {
							logger.Error("background sync failed", "username", user.Username, "error", err)
							syncErrors++
						}
					}(u)
				}
				logger.Info("metric.background_tick",
					"loop", "sync",
					"users_processed", len(users),
					"duration_ms", time.Since(tickStart).Milliseconds(),
					"errors", syncErrors)
			}
		}
	}()

	// Background Metadata Enrichment Loop
	if cfg.Metadata.Enabled {
		metadataInterval, err := time.ParseDuration(cfg.Metadata.Interval)
		if err != nil {
			logger.Error("invalid metadata interval, using default 1h", "error", err, "value", cfg.Metadata.Interval)
			metadataInterval = 1 * time.Hour
		}
		logger.Info("metadata enrichment configured",
			"interval", metadataInterval,
			"order", cfg.MetadataEnricherOrder())

		wg.Add(1)
		go func() {
			defer wg.Done()

			// Governing: ADR-0019 (structured metrics), SPEC observability REQ "BG-001", REQ "BG-002"
			syncMetadataForUsers := func() {
				tickStart := time.Now()
				// Governing: SPEC graceful-shutdown REQ-CTX-003 (per-user goroutines use root ctx)
				users, err := client.User.Query().All(ctx)
				if err != nil {
					logger.Error("failed to fetch users for metadata sync", "error", err)
					return
				}
				metadataErrors := 0
				for _, u := range users {
					// Governing: SPEC graceful-shutdown REQ-WG-002 (wg.Add before spawn, defer wg.Done first)
					wg.Add(1)
					go func(user *ent.User) {
						defer wg.Done()
						// Governing: SPEC graceful-shutdown REQ-SEM-003, REQ-SEM-004 (semaphore acquire with ctx)
						select {
						case sem <- struct{}{}:
							defer func() { <-sem }()
						case <-ctx.Done():
							return
						}
						// Governing: SPEC graceful-shutdown REQ-CTX-002 (cancelled ctx passed to service methods)
						if err := metadataSvc.SyncAll(ctx, user); err != nil {
							logger.Error("metadata sync failed", "username", user.Username, "error", err)
							metadataErrors++
						}
					}(u)
				}
				logger.Info("metric.background_tick",
					"loop", "metadata",
					"users_processed", len(users),
					"duration_ms", time.Since(tickStart).Milliseconds(),
					"errors", metadataErrors)
			}

			// Initial delay to let the app start up
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}

			// Run immediately on startup
			syncMetadataForUsers()

			ticker := time.NewTicker(metadataInterval)
			defer ticker.Stop()
			// Governing: SPEC graceful-shutdown REQ-CTX-001 (select on ctx.Done vs ticker.C)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					syncMetadataForUsers()
				}
			}
		}()
	} else {
		logger.Info("metadata enrichment disabled")
	}

	// Governing: ADR-0019 (structured metrics), SPEC observability REQ "BG-001", REQ "BG-002"
	// Background Playlist Sync Loop (for syncing playlists to Navidrome)
	playlistSyncInterval, err := time.ParseDuration(cfg.PlaylistSync.SyncInterval)
	if err != nil {
		logger.Error("invalid playlist sync interval, using default 1h", "error", err, "value", cfg.PlaylistSync.SyncInterval)
		playlistSyncInterval = 1 * time.Hour
	}
	logger.Info("playlist sync configured", "interval", playlistSyncInterval)

	wg.Add(1)
	go func() {
		defer wg.Done()
		// Initial delay to let the app start up
		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Minute):
		}

		ticker := time.NewTicker(playlistSyncInterval)
		defer ticker.Stop()
		// Governing: SPEC graceful-shutdown REQ-CTX-001 (select on ctx.Done vs ticker.C)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tickStart := time.Now()
				// Governing: SPEC graceful-shutdown REQ-CTX-003 (per-user goroutines use root ctx)
				users, err := client.User.Query().All(ctx)
				if err != nil {
					logger.Error("failed to fetch users for playlist sync", "error", err)
					continue
				}
				plSyncErrors := 0
				for _, u := range users {
					// Governing: SPEC graceful-shutdown REQ-WG-002 (wg.Add before spawn, defer wg.Done first)
					wg.Add(1)
					go func(user *ent.User) {
						defer wg.Done()
						// Governing: SPEC graceful-shutdown REQ-SEM-003, REQ-SEM-004 (semaphore acquire with ctx)
						select {
						case sem <- struct{}{}:
							defer func() { <-sem }()
						case <-ctx.Done():
							return
						}
						// Governing: SPEC graceful-shutdown REQ-CTX-002 (cancelled ctx passed to service methods)
						if err := playlistSyncSvc.SyncAllEnabledPlaylists(ctx, user.ID); err != nil {
							logger.Error("playlist sync failed", "username", user.Username, "error", err)
							plSyncErrors++
						}
					}(u)
				}
				logger.Info("metric.background_tick",
					"loop", "playlist_sync",
					"users_processed", len(users),
					"duration_ms", time.Since(tickStart).Milliseconds(),
					"errors", plSyncErrors)
			}
		}
	}()

	// Router setup
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	// Governing: SPEC-0014 REQ "HTTP Server Timeouts", SPEC user-authentication REQ "Security Headers"
	r.Use(internalMiddleware.SecurityHeaders)
	r.Use(internalMiddleware.Logger(logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// Static Files
	fileServer := http.FileServer(http.Dir("./static"))
	r.Handle("/static/*", http.StripPrefix("/static", fileServer))

	// Governing: issue #155 — rate limiting on login endpoint
	// Configure per-IP rate limiter for auth endpoints
	authRateLimit := cfg.Security.AuthRateLimit
	if authRateLimit <= 0 {
		authRateLimit = 10
	}
	loginLimiter := internalMiddleware.NewIPRateLimiter(rate.Every(time.Minute/time.Duration(authRateLimit)), authRateLimit)

	// Public Routes
	r.Group(func(r chi.Router) {
		r.Get("/auth/login", h.Login)
		// Apply rate limiting only to POST /login
		r.With(internalMiddleware.RateLimit(loginLimiter, logger)).Post("/login", h.PostLogin)
		r.Get("/logout", h.Logout)
		r.Get("/auth/logout", h.Logout) // Alias for consistency

		// OAuth callbacks must be public (no session required)
		// They will validate session internally
		r.Get("/auth/spotify/callback", h.SpotifyCallback)
		r.Get("/auth/lastfm/callback", h.LastFMCallback)
	})

	// Protected Routes
	r.Group(func(r chi.Router) {
		r.Use(AuthMiddleware(client, jwtManager, logger))

		// Governing: issue #155 — /data/* moved behind auth to prevent unauthenticated access
		dataFileServer := http.FileServer(http.Dir("./data"))
		r.Handle("/data/*", http.StripPrefix("/data", dataFileServer))

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

	addr := fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port)

	// Governing: SPEC-0014 REQ "HTTP Server Timeouts", SPEC user-authentication REQ "Security Headers"
	// Parse server timeouts from config with sensible defaults to protect against slowloris DoS
	readHeaderTimeout, err := time.ParseDuration(cfg.Server.ReadHeaderTimeout)
	if err != nil {
		logger.Error("invalid server.read_header_timeout, using default 10s", "error", err, "value", cfg.Server.ReadHeaderTimeout)
		readHeaderTimeout = 10 * time.Second
	}
	readTimeout, err := time.ParseDuration(cfg.Server.ReadTimeout)
	if err != nil {
		logger.Error("invalid server.read_timeout, using default 30s", "error", err, "value", cfg.Server.ReadTimeout)
		readTimeout = 30 * time.Second
	}
	writeTimeout, err := time.ParseDuration(cfg.Server.WriteTimeout)
	if err != nil {
		logger.Error("invalid server.write_timeout, using default 60s", "error", err, "value", cfg.Server.WriteTimeout)
		writeTimeout = 60 * time.Second
	}
	idleTimeout, err := time.ParseDuration(cfg.Server.IdleTimeout)
	if err != nil {
		logger.Error("invalid server.idle_timeout, using default 120s", "error", err, "value", cfg.Server.IdleTimeout)
		idleTimeout = 120 * time.Second
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	// Start the HTTP server in a goroutine
	go func() {
		logger.Info("starting server", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	logger.Info("shutdown initiated, waiting for background jobs to finish")

	// Governing: SPEC graceful-shutdown REQ-SIG-004 (second signal -> hard exit)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		if _, ok := <-sigCh; ok {
			logger.Error("second signal received, forcing exit")
			os.Exit(1)
		}
	}()

	// Governing: SPEC graceful-shutdown REQ-TMO-002 (hard exit timer)
	// Governing: SPEC graceful-shutdown REQ-TMO-004 (non-zero exit code on timeout)
	timer := time.AfterFunc(shutdownTimeout, func() {
		logger.Error("shutdown timeout exceeded, forcing exit")
		os.Exit(1)
	})
	defer timer.Stop()

	// Governing: SPEC graceful-shutdown REQ-CTX-004 (HTTP server uses Shutdown(ctx) for graceful drain)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}

	// Governing: SPEC graceful-shutdown REQ-WG-003 (wait for all per-user goroutines to drain)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// Governing: SPEC graceful-shutdown REQ-WG-004 (wg.Wait combined with shutdown timeout to avoid indefinite blocking)
	select {
	case <-done:
		// Governing: SPEC graceful-shutdown REQ-TMO-005 (clean shutdown exits with code 0)
		logger.Info("all background jobs finished cleanly")
	case <-shutdownCtx.Done():
		logger.Warn("shutdown timeout exceeded")
	}
}

// Governing: ADR-0005 (Navidrome primary identity), ADR-0002 (Chi router), SPEC user-authentication REQ "MIDDLEWARE-001", REQ "MIDDLEWARE-002", REQ "MIDDLEWARE-003", REQ "MIDDLEWARE-004"
func AuthMiddleware(client *ent.Client, jwtManager *auth.JWTManager, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Helper to redirect to login, handling HTMX requests
			redirectToLogin := func() {
				// Clear any invalid cookie
				http.SetCookie(w, &http.Cookie{
					Name:     handlers.CookieName,
					Value:    "",
					Path:     "/",
					HttpOnly: true,
					MaxAge:   -1,
				})

				if r.Header.Get("HX-Request") == "true" {
					w.Header().Set("HX-Redirect", "/auth/login")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			}

			cookie, err := r.Cookie(handlers.CookieName)
			if err != nil {
				redirectToLogin()
				return
			}

			claims, err := jwtManager.ValidateToken(cookie.Value)
			if err != nil {
				logger.Debug("invalid JWT token", "error", err)
				redirectToLogin()
				return
			}

			u, err := client.User.Get(r.Context(), claims.UserID)
			if err != nil {
				logger.Debug("user not found for JWT claims", "user_id", claims.UserID, "error", err)
				redirectToLogin()
				return
			}

			ctx := context.WithValue(r.Context(), handlers.UserContextKey, u)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
