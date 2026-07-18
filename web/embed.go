// Package web embeds the built frontend (web/dist) into the Go binary.
// Run `npm run build` in web/ before `go build` — the Dockerfile does this
// in its frontend stage.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the frontend as a filesystem rooted at the dist directory.
func Dist() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
