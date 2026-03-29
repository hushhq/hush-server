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
	assert.Contains(t, csp, "wss://example.com", "CSP connect-src must include the WS origin")
	assert.Contains(t, csp, "connect-src 'self' https: http: wss: ws:", "CSP connect-src must allow cross-instance HTTP(S) and WS(S) requests")
	assert.Contains(t, csp, "default-src 'self'", "CSP must include default-src 'self'")
	assert.Contains(t, csp, "font-src 'self' https://fonts.gstatic.com", "CSP must allow Google Fonts")
	assert.Contains(t, csp, "https://fonts.googleapis.com", "CSP style-src must allow Google Fonts stylesheets")
}

func TestSecurityHeaders_CSPDeduplicatesWildcardWebSocketOrigin(t *testing.T) {
	rr := applySecurityHeaders(false, "wss:")
	csp := rr.Header().Get("Content-Security-Policy")
	require.NotEmpty(t, csp, "Content-Security-Policy must be set")
	assert.NotContains(t, csp, "wss: wss:", "CSP connect-src must not duplicate the wildcard websocket origin")
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
	assert.Equal(t, "max-age=300", hsts)
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
