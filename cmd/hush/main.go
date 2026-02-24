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
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.Load()
	if cfg.CORSOrigin == "" {
		cfg.CORSOrigin = "*"
	}

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
	}

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{cfg.CORSOrigin},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
	}))

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

	wsHub := ws.NewHub()
	if pool != nil && cfg.JWTSecret != "" {
		r.Mount("/api/auth", api.AuthRoutes(pool, cfg.JWTSecret, cfg.JWTExpiry))
		r.Mount("/api/keys", api.KeysRoutes(pool, wsHub, cfg.JWTSecret))
		r.Get("/ws", ws.Handler(wsHub, cfg.JWTSecret, pool, cfg.CORSOrigin))
		r.Mount("/api/livekit", api.LiveKitRoutes(pool, cfg.JWTSecret, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret))
	}

	srv := &http.Server{
		Addr:         ":" + strconv.Itoa(cfg.Port),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // Disabled: WebSocket connections manage their own write deadlines via writeWait.
		IdleTimeout:  60 * time.Second,
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