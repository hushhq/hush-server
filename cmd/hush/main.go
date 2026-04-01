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

	"encoding/hex"

	"github.com/hushhq/hush-server/internal/api"
	"github.com/hushhq/hush-server/internal/config"
	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/models"
	"github.com/hushhq/hush-server/internal/transparency"
	"github.com/hushhq/hush-server/internal/ws"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"golang.org/x/time/rate"
)

// poolTransparencyStore adapts db.Pool to satisfy transparency.TransparencyStore.
// The interface uses clean method names and takes *LogEntry; the pool uses
// verbose names and individual fields. This adapter bridges the two without
// creating a circular import between db and transparency packages.
type poolTransparencyStore struct {
	pool *db.Pool
}

func (a *poolTransparencyStore) InsertLogEntry(
	ctx context.Context, leafIndex uint64, entry *transparency.LogEntry,
	cborBytes, leafHash, logSig []byte,
) error {
	// Coalesce nil slices to empty - the DB columns are NOT NULL but MVP mode
	// does not yet populate user signatures or public keys for all operations.
	userPub := entry.UserPublicKey
	if userPub == nil {
		userPub = []byte{}
	}
	userSig := entry.UserSignature
	if userSig == nil {
		userSig = []byte{}
	}
	return a.pool.InsertTransparencyLogEntry(
		ctx, leafIndex, string(entry.OperationType),
		userPub, entry.SubjectKey,
		cborBytes, leafHash, userSig, logSig,
	)
}

func (a *poolTransparencyStore) GetLogEntriesByPubKey(ctx context.Context, pubKey []byte) ([]models.TransparencyLogEntry, error) {
	return a.pool.GetTransparencyLogEntriesByPubKey(ctx, pubKey)
}

func (a *poolTransparencyStore) GetLatestTreeHead(ctx context.Context) (*models.TransparencyTreeHead, error) {
	return a.pool.GetLatestTransparencyTreeHead(ctx)
}

func (a *poolTransparencyStore) GetAllLeafHashes(ctx context.Context) ([][32]byte, error) {
	return a.pool.GetAllLeafHashes(ctx)
}

func (a *poolTransparencyStore) InsertTreeHead(ctx context.Context, treeSize uint64, rootHash, fringe, headSig []byte) error {
	return a.pool.InsertTransparencyTreeHead(ctx, treeSize, rootHash, fringe, headSig)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.Load()
	if cfg.CORSOrigin == "" {
		cfg.CORSOrigin = "*"
	}

	handshakeCache := api.NewInstanceCache()

	var pool *db.Pool
	var transparencySvc *transparency.TransparencyService
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
			// cache stays zero-valued: bootstrapped=false, name="" - valid for fresh instance
		} else {
			voiceKeyRotationHours, vkrhErr := pool.GetVoiceKeyRotationHours(ctx)
			if vkrhErr != nil {
				slog.Warn("handshake cache: failed to read voice_key_rotation_hours, using default", "err", vkrhErr)
				voiceKeyRotationHours = 2
			}
			handshakeCache.Set(icfg.Name, icfg.IconURL, icfg.RegistrationMode, icfg.GuildDiscovery, voiceKeyRotationHours, icfg.ServerCreationPolicy)
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
					// nil means keep forever - skip purge.
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

		// Transparency log: load signing key, create service, recover tree state.
		// If no key is configured, transparency is disabled for this instance.
		logSigner, signerErr := transparency.LoadLogSignerFromEnv()
		if signerErr != nil {
			slog.Info("transparency: log signer not configured, transparency disabled", "err", signerErr)
		} else {
			tStore := &poolTransparencyStore{pool: pool}
			var tErr error
			transparencySvc, tErr = transparency.NewTransparencyService(tStore, logSigner)
			if tErr != nil {
				slog.Error("transparency: service init failed, transparency disabled", "err", tErr)
				transparencySvc = nil
			} else {
				slog.Info("transparency: service initialized", "tree_size", transparencySvc.TreeSize())
				// Set transparency info in handshake cache.
				pubKeyHex := hex.EncodeToString(logSigner.PublicKey())
				// transparency_url is self (this instance serves its own log).
				selfURL := "/api/transparency"
				handshakeCache.SetTransparencyInfo(&selfURL, &pubKeyHex)
			}
		}

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
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Admin-Key"},
		AllowCredentials: false,
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

	// Public instance handshake - no auth, no DB query. Clients discover
	// capabilities, version requirements, and registration policy here.
	r.Get("/api/handshake", api.HandshakeHandler(handshakeCache, cfg.LiveKitAPIKey != ""))

	wsHub := ws.NewHub()
	// Wire WS broadcaster into transparency service (nil-safe on both sides).
	if transparencySvc != nil {
		transparencySvc.SetBroadcaster(wsHub)
	}
	if pool != nil && cfg.JWTSecret != "" {
		// Auth endpoints: per-IP limit - 60 requests per minute, burst 30. SEC-01.
		// Generous to accommodate React StrictMode (double-fires effects),
		// multi-instance boot (challenge+verify per instance), and /me polling.
		r.Route("/api/auth", func(sub chi.Router) {
			sub.Use(api.IPRateLimiter(rate.Limit(60.0/60.0), 30))
			sub.Mount("/", api.AuthRoutes(pool, cfg.JWTSecret, cfg.JWTExpiry, transparencySvc, wsHub))
		})
		// MLS key management: per-user limit - 10 requests per minute.
		r.Route("/api/mls", func(sub chi.Router) {
			sub.Use(api.UserRateLimiter(rate.Limit(10.0/60.0), 10))
			sub.Mount("/", api.MLSRoutes(pool, wsHub, cfg.JWTSecret, transparencySvc))
		})
		r.Mount("/api/instance", api.InstanceRoutes(pool, wsHub, cfg.JWTSecret, handshakeCache))

		// Transparency log verification endpoint.
		if transparencySvc != nil {
			r.Mount("/api/transparency", api.TransparencyRoutes(transparencySvc, pool, cfg.JWTSecret))
		}

		// Guild-scoped API: auth and RequireGuildMember applied inside ServerRoutes.
		// Channels, guild invites, and moderation are all mounted under /{serverId}.
		r.Mount("/api/servers", api.ServerRoutes(pool, wsHub, cfg.JWTSecret))

		// Guild discovery, DM creation, and public user search.
		r.Mount("/api/guilds", api.GuildRoutes(pool, wsHub, cfg.JWTSecret))

		// Public invite info (unauthenticated) + claim (authenticated, not guild-scoped).
		r.Mount("/api/invites", api.PublicInviteRoutes(pool, cfg.JWTSecret, wsHub))

		// Instance-operator admin endpoints - authenticated by X-Admin-Key header, not JWT.
		// AdminAPIKey empty means no admin key is configured; the middleware rejects all requests.
		r.Mount("/api/admin", api.AdminAPIRoutes(pool, cfg.AdminAPIKey, wsHub, handshakeCache))

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