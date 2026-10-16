// Package webui serves the built frontend (web/dist, copied into
// internal/webui/dist by `make build`'s web-build target — go:embed
// cannot reach outside its own module directory with "..") as the custos
// binary's single-page application.
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed dist
var distFS embed.FS

// Handler serves dist/ at the root of the mux it is mounted on
// (server.Server.WithUI, at "/"): a real file as itself, with the
// Content-Type net/http infers from its extension, and dist/index.html
// for any other path, so that a full page load of a client-side route
// such as /w/<id> still gets the single-page application's shell. A path
// whose last segment contains a "." but that dist does not have 404s
// instead of falling back to index.html, so a mistyped or stale asset URL
// is visible as a 404 rather than a blank page trying to parse HTML as
// JavaScript or CSS.
func Handler() (http.Handler, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	return handlerFor(sub), nil
}

// handlerFor builds the handler's serving logic over an arbitrary fs.FS
// rooted at the SPA's dist directory. It is split out from Handler so
// tests can exercise it against a lightweight directory tree (e.g. an
// fstest.MapFS with a real, non-index asset in it) instead of only the
// real embedded dist/, which — per ruling 4.2 — commits nothing but
// index.html and .gitkeep.
func handlerFor(sub fs.FS) http.Handler {
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// path.Clean("/") is "/", and TrimPrefix(..., "/") of that is ""
		// (not "."): an earlier version of this handler checked only for
		// "." and so never matched a bare "/" at all, 404ing the root —
		// found while applying this plan to a scratch copy
		// (TestHandlerServesIndexAtRoot failed). "" and "." both mean
		// "serve index.html".
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || p == "." {
			p = "index.html"
		}
		// A request that resolves to index.html itself — whether because
		// it's the root above, or because the request literally asked for
		// /index.html — must never fall through to files.ServeHTTP:
		// http.FileServer's serveFile 301-redirects any request path
		// ending in "/index.html" to its parent directory ("./"), so a
		// direct link, bookmark, or health check hitting /index.html got a
		// wasted redirect instead of the page. Found in review: the first
		// fix only routed the SPA-fallback branch (an unknown path
		// synthesizing index.html) around FileServer, not this direct-hit
		// case. Every route that should render the SPA shell — root,
		// fallback, and a direct /index.html request — goes through
		// serveIndex; only real, named asset files ever reach FileServer.
		if p == "index.html" {
			serveIndex(w, r, sub)
			return
		}
		if _, err := fs.Stat(sub, p); err != nil {
			if strings.Contains(path.Base(p), ".") {
				http.NotFound(w, r)
				return
			}
			serveIndex(w, r, sub)
			return
		}
		files.ServeHTTP(w, r)
	})
}

// serveIndex writes dist/index.html directly, rather than routing a
// crafted "/index.html" request through http.FileServer: FileServer's own
// serveFile has a built-in redirect for any request whose path ends in
// "/index.html" (a 301 to the canonical directory URL, meant for a real
// browser following links) — found while applying this plan to a scratch
// copy (TestHandlerFallsBackToIndexForAClientRoute got a 301 instead of
// the SPA shell): that redirect fired for every client-side route (e.g.
// /w/<id>) instead of this handler's intended "serve the SPA shell"
// fallback. An earlier version of this function built a shell *http.Request
// with its URL.Path set to "/index.html" and routed it through files
// (http.FileServer); this version reads and serves the bytes directly
// instead.
func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(data))
}
