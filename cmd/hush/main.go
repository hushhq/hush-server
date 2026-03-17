package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"hush.app/server/internal/api"
	"hush.app/server/internal/config"
	"hush.app/server/internal/db"
	"hush.app/server/internal/ws"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"golang.org/x/time/rate"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.Load()
	if cfg.CORSOrigin == "" {
		cfg.CORSOrigin = "*"
	}

	handshakeCache := api.NewInstanceCache()

	var pool *db.Pool
	if cfg.DatabaseURL != "" {
		wd, err := os.Getwd()
		if err != nil {
			slog.Error("getwd failed", "err", err)
			os.Exit(1)
		}
		migrationsPath := "file://" + filepath.ToSlash(filepath.Join(wd, "migrations"))
		m, err := migrate.New(migrationsPath, cfg.DatabaseURL)
		if err != nil {
			slog.Error("migrate new failed", "err", err)
			os.Exit(1)
		}
		defer m.Close()
		if err := m.Up(); err != nil && err != migrate.ErrNoChange {
			slog.Error("migrate up failed", "err", err)
			os.Exit(1)
		}
		slog.Info("migrations applied")

		ctx := context.Background()
		var errOpen error
		pool, errOpen = db.Open(ctx, cfg.DatabaseURL)
		if errOpen != nil {
			slog.Error("db open failed", "err", errOpen)
			os.Exit(1)
		}
		defer pool.Close()

		// Seed handshake cache from instance_config at startup.
		icfg, seedErr := pool.GetInstanceConfig(ctx)
		if seedErr != nil {
			slog.Warn("handshake cache: no instance_config row at startup", "err", seedErr)
			// cache stays zero-valued: bootstrapped=false, name="" — valid for fresh instance
		} else {
			handshakeCache.Set(icfg.Name, icfg.IconURL, icfg.RegistrationMode, icfg.ServerCreationPolicy, icfg.OwnerID != nil)
		}

		// System message cleanup: prune expired messages every 6 hours.
		go func() {
			ticker := time.NewTicker(6 * time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				retention, err := pool.GetSystemMessageRetentionDays(ctx)
				if err != nil {
					slog.Error("system message cleanup: get retention", "err", err)
					cancel()
					continue
				}
				if retention == nil {
					// nil means keep forever — skip purge.
					cancel()
					continue
				}
				n, err := pool.PurgeExpiredSystemMessages(ctx, *retention)
				cancel()
				if err != nil {
					slog.Error("system message cleanup", "err", err)
				} else if n > 0 {
					slog.Info("system messages purged", "count", n)
				}
			}
		}()

		// MLS KeyPackage cleanup: purge consumed rows older than 30 days and
		// unconsumed rows whose expiry has passed. Last-resort packages are never deleted.
		go func() {
			ticker := time.NewTicker(24 * time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				n, err := pool.PurgeExpiredMLSKeyPackages(ctx)
				cancel()
				if err != nil {
					slog.Error("mls key package cleanup", "err", err)
				} else if n > 0 {
					slog.Info("expired mls key packages purged", "count", n)
				}
			}
		}()
	}

	wsOrigin := api.WSOriginFromCORSOrigin(cfg.CORSOrigin)

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	// Security headers before CORS so they are always present regardless of origin check outcome.
	r.Use(api.SecurityHeaders(cfg.Production, wsOrigin))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{cfg.CORSOrigin},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
	}))
	// General per-IP rate limit: 100 requests per minute with a burst of 100. SEC-04.
	r.Use(api.IPRateLimiter(rate.Limit(100.0/60.0), 100))

	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if pool != nil {
			if err := pool.Ping(r.Context()); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"unavailable","error":"db"}`))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Public instance handshake — no auth, no DB query. Clients discover
	// capabilities, version requirements, and registration policy here.
	r.Get("/api/handshake", api.HandshakeHandler(handshakeCache, cfg.LiveKitAPIKey != ""))

	wsHub := ws.NewHub()
	if pool != nil && cfg.JWTSecret != "" {
		// Auth endpoints: stricter per-IP limit — 5 requests per minute. SEC-01.
		r.Route("/api/auth", func(sub chi.Router) {
			sub.Use(api.IPRateLimiter(rate.Limit(5.0/60.0), 5))
			sub.Mount("/", api.AuthRoutes(pool, cfg.JWTSecret, cfg.JWTExpiry))
		})
		// MLS key management: per-user limit — 10 requests per minute.
		r.Route("/api/mls", func(sub chi.Router) {
			sub.Use(api.UserRateLimiter(rate.Limit(10.0/60.0), 10))
			sub.Mount("/", api.MLSRoutes(pool, wsHub, cfg.JWTSecret))
		})
		r.Mount("/api/instance", api.InstanceRoutes(pool, wsHub, cfg.JWTSecret, handshakeCache))

		// Guild-scoped API: auth and RequireGuildMember applied inside ServerRoutes.
		// Channels, guild invites, and moderation are all mounted under /{serverId}.
		r.Mount("/api/servers", api.ServerRoutes(pool, wsHub, cfg.JWTSecret))

		// Public invite info (unauthenticated) + claim (authenticated, not guild-scoped).
		r.Mount("/api/invites", api.PublicInviteRoutes(pool, cfg.JWTSecret, wsHub))

		// Instance-operator admin endpoints.
		r.Mount("/api/admin", api.AdminRoutes(pool, cfg.JWTSecret))

		r.Get("/ws", ws.Handler(wsHub, cfg.JWTSecret, pool, cfg.CORSOrigin))
		r.Mount("/api/livekit", api.LiveKitRoutes(pool, cfg.JWTSecret, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret))
		r.Post("/api/livekit/webhook", api.LiveKitWebhookHandler(wsHub, pool, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret))
	}

	host := os.Getenv("HOST")
	if host == "" {
		host = "127.0.0.1"
	}

	srv := &http.Server{
		Addr:         host + ":" + strconv.Itoa(cfg.Port),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // Disabled: WebSocket connections manage their own write deadlines via writeWait.
		IdleTimeout:  0, // Disabled: WebSocket connections are long-lived; the WS layer handles its own keepalive via ping/pong.
	}

	done := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		slog.Info("shutdown signal received")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("server shutdown error", "err", err)
		}
		close(done)
	}()

	slog.Info("server listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
	<-done
	slog.Info("server stopped")
}