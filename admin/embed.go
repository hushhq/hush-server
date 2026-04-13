// Package admin embeds the built admin dashboard SPA.
//
// Build the SPA before compiling Go (CI and Docker do this automatically):
//
//	cd admin && npm ci && npm run build
//
// A committed placeholder admin/dist/index.html ensures go build succeeds
// without Node for local development. CI and Docker always build real assets.
package admin

import "embed"

// DistFS contains the built admin SPA assets rooted at dist/.
// Callers should use fs.Sub(DistFS, "dist") to get a filesystem
// rooted at the SPA output directory.
//
//go:embed all:dist
var DistFS embed.FS
