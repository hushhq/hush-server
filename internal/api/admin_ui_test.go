package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
)

func TestAdminUIHandler_ServesIndexHTML(t *testing.T) {
	fs := fstest.MapFS{
		"index.html":         {Data: []byte("<html>admin</html>")},
		"assets/main-abc.js": {Data: []byte("console.log('admin')")},
		"assets/style.css":   {Data: []byte("body{}")},
	}
	handler := AdminUIHandler("/admin/", fs)

	req := httptest.NewRequest("GET", "/admin/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "<html>admin</html>")
}

func TestAdminUIHandler_SPAFallback(t *testing.T) {
	fs := fstest.MapFS{
		"index.html": {Data: []byte("<html>spa</html>")},
	}
	handler := AdminUIHandler("/admin/", fs)

	// Client-side route that has no corresponding file.
	req := httptest.NewRequest("GET", "/admin/guilds", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "<html>spa</html>")
}

func TestAdminUIHandler_ServesStaticAsset(t *testing.T) {
	fs := fstest.MapFS{
		"index.html":         {Data: []byte("<html>admin</html>")},
		"assets/main-abc.js": {Data: []byte("console.log('admin')")},
	}
	handler := AdminUIHandler("/admin/", fs)

	req := httptest.NewRequest("GET", "/admin/assets/main-abc.js", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "console.log")
}

func TestAdminUIHandler_NestedSPARoute(t *testing.T) {
	fs := fstest.MapFS{
		"index.html": {Data: []byte("<html>nested</html>")},
	}
	handler := AdminUIHandler("/admin/", fs)

	req := httptest.NewRequest("GET", "/admin/config/templates", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "<html>nested</html>")
}
