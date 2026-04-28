package api

import "github.com/go-chi/cors"

// CORSOptions returns the application-wide CORS configuration applied to
// every API route by cmd/hush/main.go.
//
// AllowedHeaders must enumerate every custom request header the HTTP API
// actually consumes, because chi/cors echoes only the listed names back in
// the preflight Access-Control-Allow-Headers response. Anything missing
// here causes browsers (and Electron renderers running under a non-hosted
// origin such as `app://localhost`) to fail the preflight with a generic
// "TypeError: Failed to fetch" before the real request leaves the client.
//
// The link-archive bulk transfer plane carries three custom request
// headers — X-Upload-Token, X-Download-Token, and X-Chunk-Sha256 — which
// the OLD-device upload path and the NEW-device download path both rely
// on. Same-origin browser sessions never trigger a preflight, so these
// headers worked in the hosted web app, but the packaged desktop
// renderer's cross-origin fetch from `app://localhost` to the hosted
// instance does. Excluding them here is what surfaces as the
// `Failed to fetch` during the LinkDevice flow on desktop.
func CORSOptions() cors.Options {
	return cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{
			"Accept",
			"Authorization",
			"Content-Type",
			linkArchiveUploadTokenHeader,
			linkArchiveDownloadHeader,
			linkArchiveChunkHashHeader,
		},
		AllowCredentials: false,
	}
}
