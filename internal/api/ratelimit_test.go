package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// okHandler is a minimal handler that always responds 200.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// testAuthMiddlewareFor injects the given userID into the request context.
// It is used in rate limiter tests to simulate a logged-in user without
// running the full RequireAuth middleware stack.
func testAuthMiddlewareFor(userID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), contextKeyUserID, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ---------- IP Rate Limiter ----------

func TestIPRateLimiter_AllowsUnderLimit(t *testing.T) {
	// Burst of 5; make exactly 5 requests — all must succeed.
	limiter := IPRateLimiter(rate.Limit(5.0/60), 5)
	handler := limiter(okHandler)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.168.1.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code, "request %d should be allowed", i+1)
	}
}

func TestIPRateLimiter_BlocksOverLimit(t *testing.T) {
	// Burst of 5; the 6th request must be rate-limited.
	limiter := IPRateLimiter(rate.Limit(5.0/60), 5)
	handler := limiter(okHandler)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.168.1.2:5678"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.2:5678"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusTooManyRequests, rr.Code)
	require.NotEmpty(t, rr.Header().Get("Retry-After"), "429 must include Retry-After header")
}

func TestIPRateLimiter_DifferentIPsIndependent(t *testing.T) {
	// Exhaust IP1's burst; IP2 must still be allowed.
	limiter := IPRateLimiter(rate.Limit(1.0/60), 2)
	handler := limiter(okHandler)

	// Exhaust IP1.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1111"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}

	// IP1 next request must be blocked.
	{
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1111"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		require.Equal(t, http.StatusTooManyRequests, rr.Code)
	}

	// IP2 must be unaffected.
	{
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.2:2222"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	}
}

// ---------- User Rate Limiter ----------

func TestUserRateLimiter_BlocksOverLimit(t *testing.T) {
	// Burst of 3; the 4th request from the same user must be rate-limited.
	userID := "user-rate-test-1"
	limiter := UserRateLimiter(rate.Limit(1.0/60), 3)
	handler := testAuthMiddlewareFor(userID)(limiter(okHandler))

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code, "request %d should be allowed", i+1)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusTooManyRequests, rr.Code)
	require.NotEmpty(t, rr.Header().Get("Retry-After"), "429 must include Retry-After header")
}

func TestUserRateLimiter_NoUserIDFallsThrough(t *testing.T) {
	// With no userID in context (unauthenticated), UserRateLimiter must let the request pass.
	limiter := UserRateLimiter(rate.Limit(0), 0) // rate=0 so any real user would be blocked
	handler := limiter(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "unauthenticated request must not be rate-limited by UserRateLimiter")
}
