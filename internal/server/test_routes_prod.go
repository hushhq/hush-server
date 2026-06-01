//go:build !e2e_test

package server

import (
	"github.com/go-chi/chi/v5"

	"github.com/hushhq/hush-server/internal/db"
)

// registerTestRoutes is a no-op in production builds. The E2E test session
// endpoint is compiled in ONLY under -tags e2e_test (see test_routes.go), so
// /api/test/* is physically absent from any production binary. The absence is
// proven by test_routes_absent_test.go, which runs in the normal (untagged)
// build and asserts the route returns 404.
func registerTestRoutes(_ chi.Router, _ *db.Pool, _ string) {}
