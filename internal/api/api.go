// Package api serves the REST API below /api (spec §6.3): workspaces,
// status, the book, answers, attachments and the read-only catalog. It
// writes only through the store, so every write is serialised per
// repository and validated like a push (ruling 2.3).
package api

import (
	"net/http"

	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/store"
)

const mainRef = "refs/heads/main"

// API holds the endpoints. Later plans add theirs with Handle.
type API struct {
	st  *store.Store
	bl  *blobs.Store
	mux *http.ServeMux
}

// New returns the API over a store and a blob store.
func New(st *store.Store, bl *blobs.Store) *API {
	a := &API{st: st, bl: bl, mux: http.NewServeMux()}
	a.routes()
	return a
}

// Handle registers h for a pattern such as "POST /api/workspaces/{id}/freeze".
func (a *API) Handle(pattern string, h http.HandlerFunc) { a.mux.HandleFunc(pattern, h) }

// Handler serves every registered endpoint. Patterns carry the full path,
// so the handler is mounted at /api/ without stripping a prefix.
func (a *API) Handler() http.Handler { return a.mux }

// routes registers the endpoints of this package.
func (a *API) routes() {
	a.Handle("POST /api/blobs", a.postBlob)
	a.Handle("GET /api/blobs/{sha256}", a.getBlob) // GET patterns also match HEAD
}
