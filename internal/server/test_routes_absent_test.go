//go:build !e2e_test

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/hushhq/hush-server/internal/api"
	"github.com/hushhq/hush-server/internal/config"
)

// TestRegisterTestRoutes_NoOpInProdBuild proves the E2E session endpoint is
// physically absent from a production (untagged) build: registerTestRoutes is
// the no-op from test_routes_prod.go, so it mounts no /api/test routes even when
// called with a non-nil pool stand-in. DB-free; runs on every PR.
func TestRegisterTestRoutes_NoOpInProdBuild(t *testing.T) {
	r := chi.NewRouter()
	registerTestRoutes(r, nil, "test-secret")

	walkErr := chi.Walk(r, func(_ string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/api/test") {
			t.Fatalf("production build registered a test route: %s", route)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk routes: %v", walkErr)
	}
}

// TestBuildServer_TestSessionReturns404InProdBuild asserts the observable: in a
// prod build, POST /api/test/session is 404 through the real BuildServer router.
func TestBuildServer_TestSessionReturns404InProdBuild(t *testing.T) {
	handler, _ := BuildServer(Deps{
		Cfg:            config.Config{JWTSecret: "test-secret"},
		HandshakeCache: api.NewInstanceCache(),
		HTTPMetrics:    api.NewHTTPMetrics(),
		StartedAt:      time.Now(),
	})

	req := httptest.NewRequest(http.MethodPost, "/api/test/session", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/test/session = %d, want 404 (test routes must be absent in prod build)", rec.Code)
	}
}
