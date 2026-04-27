package api

import (
	"fmt"
	"net/http"
	"strings"
)

// hstsMaxAgeSeconds is the Strict-Transport-Security max-age applied when
// productionMode is true. One year is the lower bound for HSTS preload and
// is the value the hosted Cloudflare edge already uses; bringing the
// server's own header up to that line keeps the self-hoster posture
// coherent with hosted (ans23 / F7).
const hstsMaxAgeSeconds = 31536000

// SecurityHeaders returns middleware that sets security-related HTTP response
// headers on every response. productionMode enables HSTS. wsOrigin is the
// WebSocket origin to allow in the Content-Security-Policy connect-src directive;
// derive it from CORSOrigin by replacing https:// with wss:// (or http:// with ws://).
// If CORSOrigin is "*", pass "wss:" to allow all WebSocket origins.
func SecurityHeaders(productionMode bool, wsOrigin string) func(http.Handler) http.Handler {
	csp := buildCSP(wsOrigin)
	hsts := fmt.Sprintf("max-age=%d; includeSubDomains", hstsMaxAgeSeconds)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			if productionMode {
				h.Set("Strict-Transport-Security", hsts)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// buildCSP constructs the Content-Security-Policy header value.
//
// ans23 / F6: the server-side default is now narrow. `connect-src` is
// `'self'` plus the same-origin WebSocket scheme; the prior `https: http:
// wss: ws:` blanket allowed any origin and made same-origin XSS exfiltrate
// trivially. The hosted Cloudflare edge replaces this header with its own
// nonce-based CSP, so the change matters mostly for self-hoster deploys.
//
// wsOrigin is included verbatim in `connect-src`. When the operator passes
// the wildcard "wss:" (i.e. CORS_ORIGIN=*), the CSP keeps the prior open
// shape for that operator's deployment so federated topologies still work.
func buildCSP(wsOrigin string) string {
	connectSrc := []string{"'self'"}
	trimmedOrigin := strings.TrimSpace(wsOrigin)
	switch trimmedOrigin {
	case "":
		// No CORS origin configured — default to same-origin only.
	case "wss:", "ws:":
		// Operator opted into wildcard CORS; preserve the open shape.
		connectSrc = append(connectSrc, "https:", "http:", "wss:", "ws:")
	default:
		if !containsString(connectSrc, trimmedOrigin) {
			connectSrc = append(connectSrc, trimmedOrigin)
		}
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
