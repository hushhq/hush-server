// Package server builds the Hush HTTP handler and WebSocket hub from a single
// shared constructor used by BOTH the production binary (cmd/hush) and the E2E
// test harness. Keeping one constructor eliminates the prod-vs-test wiring drift
// that let HUSHHQ-104 (missing voice channel WS subscription) ship: the test
// runs the exact middleware order, hub instance, and route mounts as prod.
//
// main() owns lifecycle only (config load, migrate, pool open, background
// goroutines, listen/serve, signals). BuildServer owns wiring only and starts no
// goroutines, so it is safe to construct repeatedly in tests via httptest.
package server

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"time"

	"github.com/hushhq/hush-server/internal/api"
	"github.com/hushhq/hush-server/internal/config"
	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/livekit"
	"github.com/hushhq/hush-server/internal/storage"
	"github.com/hushhq/hush-server/internal/transparency"
	"github.com/hushhq/hush-server/internal/ws"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"golang.org/x/time/rate"
)

// Deps are the already-constructed dependencies BuildServer wires together.
// The caller (main, or the test harness) owns their lifecycle.
type Deps struct {
	Cfg             config.Config
	Pool            *db.Pool                            // may be nil (pre-DB bootstrap)
	TransparencySvc *transparency.TransparencyService   // may be nil
	HandshakeCache  *api.InstanceCache
	HTTPMetrics     *api.HTTPMetrics
	StartedAt       time.Time
	AdminDist       fs.FS // admin SPA sub-filesystem; nil to skip mounting /admin
}

// NewAttachmentBackend returns the lazily-built attachment storage backend
// factory. Exported so main can reuse the exact same closure for its retention
// cleanup goroutine without duplicating the backend-resolution rules.
func NewAttachmentBackend(pool *db.Pool) func() (storage.Backend, error) {
	return func() (storage.Backend, error) {
		cfg, err := storage.LoadAttachmentConfig()
		if err != nil {
			return nil, err
		}
		if cfg.Kind == storage.BackendPostgresBytea {
			return nil, fmt.Errorf("attachments require STORAGE_BACKEND=s3 (current: %s)", cfg.Kind)
		}
		return storage.NewBackend(cfg, pool)
	}
}

// BuildServer wires the full HTTP router (middleware, public endpoints, the
// auth/MLS/instance/server/admin/livekit mounts, the WS upgrade, and the admin
// SPA) and returns the handler plus the WebSocket hub. It starts no goroutines.
func BuildServer(d Deps) (http.Handler, *ws.Hub) {
	cfg := d.Cfg
	pool := d.Pool
	transparencySvc := d.TransparencySvc
	handshakeCache := d.HandshakeCache
	httpMetrics := d.HTTPMetrics

	wsOrigin := api.WSOriginFromCORSOrigin(cfg.CORSOrigin)

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(api.HTTPMetricsMiddleware(httpMetrics))
	// Security headers before CORS so they are always present regardless of origin check outcome.
	r.Use(api.SecurityHeaders(cfg.Production, wsOrigin))
	r.Use(cors.Handler(api.CORSOptions()))
	// General per-IP rate limit: coarse anti-abuse only. Real users can legitimately
	// burst through many lightweight reads (handshake, instance, members, channels,
	// MLS commit polling, transparency checks, reconnect churn), especially behind a
	// desktop/web session that restores multiple active groups at once. Keep the
	// global limiter comfortably above normal app chatter and rely on narrower
	// endpoint-specific limiters for more sensitive surfaces.
	r.Use(api.IPRateLimiter(rate.Limit(600.0/60.0), 300))

	healthHandler := func(w http.ResponseWriter, r *http.Request) {
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
	}
	r.Get("/api/health", healthHandler)
	r.Post("/api/health", healthHandler)

	// Public instance handshake - no auth, no DB query. Clients discover
	// capabilities, version requirements, and registration policy here.
	r.Get("/api/handshake", api.HandshakeHandler(handshakeCache, cfg.LiveKitAPIKey != ""))

	wsHub := ws.NewHub()
	// Wire WS broadcaster into transparency service (nil-safe on both sides).
	if transparencySvc != nil {
		transparencySvc.SetBroadcaster(wsHub)
	}
	if pool != nil && cfg.JWTSecret != "" {
		// Auth endpoints: per-IP limit - 120 requests per minute, burst 60. SEC-01.
		// This remains tighter than the global limiter while allowing legitimate
		// challenge+verify churn during reconnects, multi-instance boot, and recovery
		// from transient client-side auth races.
		r.Route("/api/auth", func(sub chi.Router) {
			sub.Use(api.IPRateLimiter(rate.Limit(120.0/60.0), 60))
			sub.Mount("/", api.AuthRoutes(pool, cfg.JWTSecret, cfg.JWTExpiry, transparencySvc, wsHub))
		})
		// MLS key management: per-user limit. The client legitimately polls
		// commits/count endpoints across multiple active groups during steady
		// state and reconnect recovery, so 10/min self-DOSes normal usage.
		// Keep a limiter here, but move it well above expected app chatter.
		r.Route("/api/mls", func(sub chi.Router) {
			sub.Use(api.UserRateLimiter(rate.Limit(300.0/60.0), 120))
			sub.Mount("/", api.MLSRoutes(pool, wsHub, cfg.JWTSecret, transparencySvc))
		})
		r.Mount("/api/instance", api.InstanceRoutes(pool, wsHub, cfg.JWTSecret, handshakeCache))

		// Transparency log verification endpoint.
		if transparencySvc != nil {
			r.Mount("/api/transparency", api.TransparencyRoutes(transparencySvc, pool, cfg.JWTSecret))
		}

		// Outbound LiveKit room-service client used by ban / kick paths
		// to evict participants from active voice rooms. Falls back to
		// a no-op when LiveKit is not configured.
		roomService := livekit.NewTwirpRoomService(cfg.LiveKitURL, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret)

		// Storage backend for attachment presign URLs. Lazy-built per
		// request via the closure so a transient backend outage does not
		// require a server restart. Returns nil when the attachment
		// storage config resolves to postgres_bytea, since attachments require
		// an S3-compatible backend to produce native presigned URLs.
		attachmentBackend := NewAttachmentBackend(pool)

		// Guild-scoped API: auth and RequireGuildMember applied inside ServerRoutes.
		// Channels, guild invites, and moderation are all mounted under /{serverId}.
		r.Mount("/api/servers", api.ServerRoutes(pool, wsHub, cfg.JWTSecret, roomService, attachmentBackend))

		// Global attachment download/delete (channel-membership checked per row).
		r.Mount("/api/attachments", api.AttachmentRoutes(pool, attachmentBackend, cfg.JWTSecret))

		// Giphy GIF search proxy. Requires auth so anonymous traffic
		// does not burn the upstream key; returns 503 when GIPHY_API_KEY
		// is unset so the rest of the chat surface keeps working.
		r.Group(func(r chi.Router) {
			r.Use(api.RequireAuth(cfg.JWTSecret, pool))
			r.Mount("/api/gif", api.GifRoutes(cfg.GiphyAPIKey))
		})

		// Guild discovery, DM creation, and public user search.
		r.Mount("/api/guilds", api.GuildRoutes(pool, wsHub, cfg.JWTSecret))

		// Public invite info (unauthenticated) + claim (authenticated, not guild-scoped).
		r.Mount("/api/invites", api.PublicInviteRoutes(pool, cfg.JWTSecret, wsHub))

		r.Mount("/api/admin", api.AdminAPIRoutes(
			pool,
			cfg.AdminBootstrapSecret,
			cfg.AdminSessionTTL,
			cfg.Production,
			cfg.ServiceIdentityMasterKey,
			wsHub,
			handshakeCache,
			roomService,
			httpMetrics,
			wsHub,
			d.StartedAt,
		))

		voiceState := api.NewVoiceState()
		r.Get("/ws", ws.Handler(wsHub, cfg.JWTSecret, pool, cfg.CORSOrigin, cfg.WSAllowedOrigins...))
		r.Mount("/api/livekit", api.LiveKitRoutesWithVoiceStateAndPublicURL(
			pool,
			cfg.JWTSecret,
			cfg.LiveKitAPIKey,
			cfg.LiveKitAPISecret,
			cfg.LiveKitPublicURL,
			voiceState,
		))
		r.Post("/api/livekit/webhook", api.LiveKitWebhookHandlerWithState(wsHub, pool, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret, voiceState))

		// Build-tagged E2E test routes. A no-op in production builds (see
		// test_routes_prod.go); only compiled in under -tags e2e_test.
		registerTestRoutes(r, pool, cfg.JWTSecret)
	}

	// Invite landing pages for browser visits to /invite/{code} and
	// /join/{host}/{code}. Mounted unconditionally and DB-free so a
	// self-hosted backend (which serves no web client at its origin)
	// shows join instructions instead of the raw reverse-proxy fallback.
	inviteLanding := api.NewInviteLandingHandler(os.Getenv("HUSH_WEB_CLIENT_URL"))
	r.Get("/invite/{code}", inviteLanding.ServeInvite)
	r.Get("/join/{host}/{code}", inviteLanding.ServeJoin)

	// Admin dashboard SPA: embedded static files served at /admin/.
	// Mounted unconditionally so the bootstrap screen is accessible on
	// a fresh instance before the database is configured.
	if d.AdminDist != nil {
		r.Handle("/admin", http.RedirectHandler("/admin/", http.StatusMovedPermanently))
		r.Handle("/admin/*", api.AdminUIHandler("/admin/", d.AdminDist))
	}

	return r, wsHub
}
