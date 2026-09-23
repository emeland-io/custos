// Package web embeds the built single page application.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

//go:embed fallback
var fallback embed.FS

// UI returns the built application, or a page explaining how to build it
// when the binary was compiled without it.
func UI() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err == nil {
		if _, err := fs.Stat(sub, "index.html"); err == nil {
			return sub
		}
	}
	sub, _ = fs.Sub(fallback, "fallback")
	return sub
}
