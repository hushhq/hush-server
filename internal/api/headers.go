package api

import (
	"fmt"
	"net/http"
	"strings"
)

// SecurityHeaders returns middleware that sets security-related HTTP response
// headers on every response. productionMode enables HSTS. wsOrigin is the
// WebSocket origin to allow in the Content-Security-Policy connect-src directive;
// derive it from CORSOrigin by replacing https:// with wss:// (or http:// with ws://).
// If CORSOrigin is "*", pass "wss:" to allow all WebSocket origins.
func SecurityHeaders(productionMode bool, wsOrigin string) func(http.Handler) http.Handler {
	csp := buildCSP(wsOrigin)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			if productionMode {
				h.Set("Strict-Transport-Security", "max-age=300")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// buildCSP constructs the Content-Security-Policy header value.
// wsOrigin is included verbatim in the connect-src directive.
func buildCSP(wsOrigin string) string {
	connectSrc := []string{"'self'", "https:", "http:", "wss:", "ws:"}
	trimmedOrigin := strings.TrimSpace(wsOrigin)
	if trimmedOrigin != "" && !containsString(connectSrc, trimmedOrigin) {
		connectSrc = append(connectSrc, trimmedOrigin)
	}

	return fmt.Sprintf(
		"default-src 'self'; script-src 'self' 'wasm-unsafe-eval' blob: data:; connect-src %s; img-src 'self' data:; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; worker-src 'self' blob: data:",
		strings.Join(connectSrc, " "),
	)
}

// WSOriginFromCORSOrigin derives the WebSocket origin string for the CSP
// connect-src directive from the configured CORS origin.
// "https://example.com" -> "wss://example.com"
// "http://example.com"  -> "ws://example.com"
// "*"                   -> "wss:"
func WSOriginFromCORSOrigin(corsOrigin string) string {
	switch {
	case corsOrigin == "*":
		return "wss:"
	case strings.HasPrefix(corsOrigin, "https://"):
		return "wss://" + strings.TrimPrefix(corsOrigin, "https://")
	case strings.HasPrefix(corsOrigin, "http://"):
		return "ws://" + strings.TrimPrefix(corsOrigin, "http://")
	default:
		return corsOrigin
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
