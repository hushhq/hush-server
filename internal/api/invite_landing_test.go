package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

const testWebClientURL = "https://app.example.test"

// router wires the landing handler the same way main.go mounts it, so the
// tests exercise real chi URL-param extraction rather than a stub.
func newInviteLandingRouter() chi.Router {
	h := NewInviteLandingHandler(testWebClientURL)
	r := chi.NewRouter()
	r.Get("/invite/{code}", h.ServeInvite)
	r.Get("/join/{host}/{code}", h.ServeJoin)
	return r
}

func TestInviteLanding_SameInstance_RendersCodeHostAndInstructions(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/invite/GAyAGHFjy", nil)
	req.Host = "chat.example.tld"
	w := httptest.NewRecorder()

	newInviteLandingRouter().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")

	body := w.Body.String()
	assert.Contains(t, body, "GAyAGHFjy", "invite code must be shown for manual entry")
	assert.Contains(t, body, "chat.example.tld", "instance host must be shown")
	assert.Contains(t, body, "hush://invite/GAyAGHFjy", "desktop deep-link must target the same-instance invite")
	assert.Contains(t, body, testWebClientURL, "browser web-client entry point must be offered")
}

func TestInviteLanding_DoesNotShowRawBackendFallback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/invite/GAyAGHFjy", nil)
	req.Host = "chat.example.tld"
	w := httptest.NewRecorder()

	newInviteLandingRouter().ServeHTTP(w, req)

	assert.NotContains(t, w.Body.String(), "Hush instance backend is live",
		"landing page must replace, not echo, the backend fallback text")
}

func TestInviteLanding_EscapesCode_PreventsHTMLInjection(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/invite/%3Cscript%3Ealert(1)%3C%2Fscript%3E", nil)
	req.Host = "chat.example.tld"
	w := httptest.NewRecorder()

	newInviteLandingRouter().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "<script>alert(1)</script>",
		"user-controlled code must be HTML-escaped to prevent injection")
}

func TestJoinLanding_CrossInstance_RendersExplicitHostFromPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/join/other.example.tld/XYZ12345", nil)
	// The serving origin differs from the invite's instance host on /join links.
	req.Host = "chat.example.tld"
	w := httptest.NewRecorder()

	newInviteLandingRouter().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "other.example.tld", "join page must show the invite's explicit instance host, not the serving origin")
	assert.Contains(t, body, "XYZ12345")
	assert.Contains(t, body, "hush://join/other.example.tld/XYZ12345", "desktop deep-link must carry the explicit instance host")
}

func TestInviteLanding_DefaultsWebClientURL_WhenUnset(t *testing.T) {
	h := NewInviteLandingHandler("")
	r := chi.NewRouter()
	r.Get("/invite/{code}", h.ServeInvite)

	req := httptest.NewRequest(http.MethodGet, "/invite/ABC", nil)
	req.Host = "chat.example.tld"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Contains(t, w.Body.String(), defaultWebClientURL,
		"empty config must fall back to the documented public convenience client")
	assert.False(t, strings.Contains(w.Body.String(), "href=\"\""),
		"web-client link must never render empty")
}
