package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"encoding/hex"
	"io/fs"

	adminui "github.com/hushhq/hush-server/admin"
	"github.com/hushhq/hush-server/internal/api"
	"github.com/hushhq/hush-server/internal/config"
	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/dbcompat"
	"github.com/hushhq/hush-server/internal/models"
	"github.com/hushhq/hush-server/internal/server"
	"github.com/hushhq/hush-server/internal/storage"
	"github.com/hushhq/hush-server/internal/transparency"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
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
		// HUSHHQ-83 phase 2: refuse to start when the live DB has been
		// migrated past this binary's compiled-in schema ceiling. The
		// check runs after migrate.New() (so we have a handle to read
		// schema_migrations) and before m.Up() (so we never silently
		// no-op on a rollback scenario and continue running the older
		// code against the newer schema).
		if err := dbcompat.CheckSchemaCompatibility(context.Background(), m); err != nil {
			slog.Error("db schema compatibility check failed", "err", err)
			os.Exit(1)
		}
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
			handshakeCache.Set(icfg.Name, icfg.IconURL, icfg.RegistrationMode, icfg.GuildDiscovery, voiceKeyRotationHours, icfg.ServerCreationPolicy, icfg.ScreenShareResolutionCap)
			handshakeCache.SetAttachmentPolicy(icfg.MaxAttachmentBytes)
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

		// Session cleanup: drop expired user sessions and stale admin sessions
		// once a day. Expired rows are already rejected at the read path; this
		// exists to keep the tables bounded over time.
		go func() {
			const adminRevokedRetention = 30 * 24 * time.Hour
			ticker := time.NewTicker(24 * time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if n, err := pool.PurgeExpiredSessions(ctx); err != nil {
					slog.Error("session cleanup: user sessions", "err", err)
				} else if n > 0 {
					slog.Info("expired user sessions purged", "count", n)
				}
				if n, err := pool.PurgeStaleAdminSessions(ctx, adminRevokedRetention); err != nil {
					slog.Error("session cleanup: admin sessions", "err", err)
				} else if n > 0 {
					slog.Info("stale admin sessions purged", "count", n)
				}
				cancel()
			}
		}()
	}

	startedAt := time.Now()
	httpMetrics := api.NewHTTPMetrics()

	// Admin dashboard SPA: embedded static files served at /admin/. Resolved
	// here (main owns the embed.FS) and passed into BuildServer, which mounts it.
	var adminDist fs.FS
	if sub, subErr := fs.Sub(adminui.DistFS, "dist"); subErr != nil {
		slog.Error("admin ui: failed to create sub filesystem", "err", subErr)
	} else {
		adminDist = sub
	}

	// Message retention cleanup goroutine. Lifecycle (not wiring), so it stays in
	// main; it reuses the same attachment backend factory BuildServer wires into
	// the attachment routes.
	if pool != nil && cfg.JWTSecret != "" {
		attachmentBackend := server.NewAttachmentBackend(pool)
		go func() {
			ticker := time.NewTicker(6 * time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				if err := runMessageRetentionCleanup(ctx, pool, attachmentBackend); err != nil {
					slog.Error("message retention cleanup", "err", err)
				}
				cancel()
			}
		}()
	}

	// Single shared constructor for the HTTP handler + WS hub, used identically
	// by production (here) and the E2E harness, so wiring cannot drift (HUSHHQ-106).
	handler, _ := server.BuildServer(server.Deps{
		Cfg:             cfg,
		Pool:            pool,
		TransparencySvc: transparencySvc,
		HandshakeCache:  handshakeCache,
		HTTPMetrics:     httpMetrics,
		StartedAt:       startedAt,
		AdminDist:       adminDist,
	})

	host := os.Getenv("HOST")
	if host == "" {
		host = "127.0.0.1"
	}

	srv := &http.Server{
		Addr:         host + ":" + strconv.Itoa(cfg.Port),
		Handler:      handler,
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

func runMessageRetentionCleanup(ctx context.Context, pool *db.Pool, attachmentBackend func() (storage.Backend, error)) error {
	cfg, err := pool.GetInstanceConfig(ctx)
	if err != nil {
		return fmt.Errorf("get instance config: %w", err)
	}
	if cfg.MessageRetentionDays <= 0 {
		return nil
	}
	expired, err := pool.ListExpiredAttachments(ctx, cfg.MessageRetentionDays, 500)
	if err != nil {
		return fmt.Errorf("list expired attachments: %w", err)
	}
	if len(expired) > 0 {
		ids := make([]string, 0, len(expired))
		for _, attachment := range expired {
			ids = append(ids, attachment.ID)
		}
		deleted, err := pool.SoftDeleteAttachmentsByID(ctx, ids)
		if err != nil {
			return fmt.Errorf("soft delete expired attachments: %w", err)
		}
		backend, err := attachmentBackend()
		if err != nil {
			slog.Warn("message retention cleanup: attachment backend unavailable", "err", err)
		} else {
			for _, attachment := range deleted {
				if err := backend.Delete(context.Background(), attachment.StorageKey); err != nil {
					slog.Warn("message retention cleanup: delete attachment object", "id", attachment.ID, "err", err)
				}
			}
		}
	}
	n, err := pool.PurgeExpiredMessages(ctx, cfg.MessageRetentionDays)
	if err != nil {
		return fmt.Errorf("purge expired messages: %w", err)
	}
	if n > 0 {
		slog.Info("expired chat messages purged", "count", n)
	}
	if len(expired) > 0 {
		slog.Info("expired chat attachments tombstoned", "count", len(expired))
	}
	return nil
}
