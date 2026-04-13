package api

import (
	"io/fs"
	"net/http"
	"strings"
)

// AdminUIHandler returns an http.Handler that serves an embedded SPA at the
// given prefix (e.g. "/admin/"). Requests matching a real file in adminFS are
// served directly; all other paths receive index.html for client-side routing.
func AdminUIHandler(prefix string, adminFS fs.FS) http.Handler {
	fileServer := http.StripPrefix(prefix, http.FileServer(http.FS(adminFS)))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip the mount prefix to get the path within the embedded FS.
		path := strings.TrimPrefix(r.URL.Path, prefix)
		if path == "" {
			path = "index.html"
		}

		// If the path maps to a real file, serve it directly.
		f, err := adminFS.Open(path)
		if err == nil {
			defer f.Close()
			info, statErr := f.Stat()
			if statErr == nil && !info.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// SPA fallback: rewrite to prefix root so StripPrefix yields "/"
		// and the file server returns index.html.
		r.URL.Path = prefix
		fileServer.ServeHTTP(w, r)
	})
}
