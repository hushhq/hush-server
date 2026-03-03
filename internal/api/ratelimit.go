package api

import (
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

// ipLimiterStore holds per-IP rate limiters behind a mutex.
// TODO(Phase I): add TTL-based eviction for limiter map
type ipLimiterStore struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	r        rate.Limit
	b        int
}

func newIPLimiterStore(r rate.Limit, b int) *ipLimiterStore {
	return &ipLimiterStore{
		limiters: make(map[string]*rate.Limiter),
		r:        r,
		b:        b,
	}
}

func (s *ipLimiterStore) get(ip string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	lim, ok := s.limiters[ip]
	if !ok {
		lim = rate.NewLimiter(s.r, s.b)
		s.limiters[ip] = lim
	}
	return lim
}

// IPRateLimiter returns middleware that limits requests per remote IP address.
// r is the sustained request rate (tokens per second), b is the burst size.
// On limit breach, responds 429 with Retry-After: 60 and JSON error body.
func IPRateLimiter(r rate.Limit, b int) func(http.Handler) http.Handler {
	store := newIPLimiterStore(r, b)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ip := req.RemoteAddr
			if !store.get(ip).Allow() {
				w.Header().Set("Retry-After", "60")
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

// userLimiterStore holds per-userID rate limiters behind a mutex.
// TODO(Phase I): add TTL-based eviction for limiter map
type userLimiterStore struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	r        rate.Limit
	b        int
}

func newUserLimiterStore(r rate.Limit, b int) *userLimiterStore {
	return &userLimiterStore{
		limiters: make(map[string]*rate.Limiter),
		r:        r,
		b:        b,
	}
}

func (s *userLimiterStore) get(userID string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	lim, ok := s.limiters[userID]
	if !ok {
		lim = rate.NewLimiter(s.r, s.b)
		s.limiters[userID] = lim
	}
	return lim
}

// UserRateLimiter returns middleware that limits requests per authenticated user ID.
// r is the sustained request rate (tokens per second), b is the burst size.
// If no userID is in context (should not happen behind RequireAuth), the request falls through.
// On limit breach, responds 429 with Retry-After: 60 and JSON error body.
func UserRateLimiter(r rate.Limit, b int) func(http.Handler) http.Handler {
	store := newUserLimiterStore(r, b)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			userID := userIDFromContext(req.Context())
			if userID == "" {
				next.ServeHTTP(w, req)
				return
			}
			if !store.get(userID).Allow() {
				w.Header().Set("Retry-After", "60")
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}
