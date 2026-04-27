package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func applySecurityHeaders(productionMode bool, wsOrigin string) *httptest.ResponseRecorder {
	handler := SecurityHeaders(productionMode, wsOrigin)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestSecurityHeaders_CSPPresent(t *testing.T) {
	rr := applySecurityHeaders(false, "wss://example.com")
	csp := rr.Header().Get("Content-Security-Policy")
	require.NotEmpty(t, csp, "Content-Security-Policy must be set")
	assert.Contains(t, csp, "connect-src 'self' wss://example.com", "CSP connect-src must include the configured WS origin")
	assert.NotContains(t, csp, "https: http:", "CSP connect-src must not blanket-allow http(s) origins")
	assert.NotContains(t, csp, "wss: ws:", "CSP connect-src must not blanket-allow ws(s) schemes")
	assert.Contains(t, csp, "default-src 'self'", "CSP must include default-src 'self'")
	assert.Contains(t, csp, "font-src 'self' https://fonts.gstatic.com", "CSP must allow Google Fonts")
	assert.Contains(t, csp, "https://fonts.googleapis.com", "CSP style-src must allow Google Fonts stylesheets")
}

func TestSecurityHeaders_CSPSelfOnlyWhenNoWSOrigin(t *testing.T) {
	rr := applySecurityHeaders(false, "")
	csp := rr.Header().Get("Content-Security-Policy")
	require.NotEmpty(t, csp, "Content-Security-Policy must be set")
	assert.Contains(t, csp, "connect-src 'self';",
		"With no WS origin configured, connect-src must be 'self' only (ans23 / F6)")
}

func TestSecurityHeaders_CSPWildcardKeepsOpenShape(t *testing.T) {
	// Operators that explicitly opt into a wildcard CORS origin still
	// need the broad CSP so federated topologies remain workable.
	rr := applySecurityHeaders(false, "wss:")
	csp := rr.Header().Get("Content-Security-Policy")
	require.NotEmpty(t, csp, "Content-Security-Policy must be set")
	assert.Contains(t, csp, "connect-src 'self' https: http: wss: ws:",
		"wildcard CORS_ORIGIN must keep the broad connect-src shape")
	assert.NotContains(t, csp, "wss: wss:", "must not duplicate the wildcard websocket origin")
}

func TestSecurityHeaders_NoSniff(t *testing.T) {
	rr := applySecurityHeaders(false, "wss://example.com")
	require.Equal(t, "nosniff", rr.Header().Get("X-Content-Type-Options"))
}

func TestSecurityHeaders_FrameDeny(t *testing.T) {
	rr := applySecurityHeaders(false, "wss://example.com")
	require.Equal(t, "DENY", rr.Header().Get("X-Frame-Options"))
}

func TestSecurityHeaders_HSTSInProduction(t *testing.T) {
	rr := applySecurityHeaders(true, "wss://example.com")
	hsts := rr.Header().Get("Strict-Transport-Security")
	require.NotEmpty(t, hsts, "Strict-Transport-Security must be set in production mode")
	// ans23 / F7: production-worthy HSTS, one year + includeSubDomains.
	assert.Equal(t, "max-age=31536000; includeSubDomains", hsts)
}

func TestSecurityHeaders_NoHSTSInDev(t *testing.T) {
	rr := applySecurityHeaders(false, "wss://example.com")
	assert.Empty(t, rr.Header().Get("Strict-Transport-Security"),
		"Strict-Transport-Security must be absent in dev mode")
}

func TestWSOriginFromCORSOrigin_Wildcard(t *testing.T) {
	assert.Equal(t, "wss:", WSOriginFromCORSOrigin("*"))
}

func TestWSOriginFromCORSOrigin_HTTPS(t *testing.T) {
	assert.Equal(t, "wss://example.com", WSOriginFromCORSOrigin("https://example.com"))
}

func TestWSOriginFromCORSOrigin_HTTP(t *testing.T) {
	assert.Equal(t, "ws://example.com", WSOriginFromCORSOrigin("http://example.com"))
}
