// Package api serves the custos JSON API and the web UI.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/model"
	"github.com/emeland-io/custos/internal/seed"
	"github.com/emeland-io/custos/internal/store"
)

// MaxBodySize limits request bodies. It matches the limit the carabiner
// collector applies to a single attestation source.
const MaxBodySize = 7 << 20

// Server serves the API.
type Server struct {
	Store    *store.Store
	Verifier attest.Verifier
	// Keys lists the trusted keys.
	Keys func() []attest.Key
	// ReloadKeys reads the keys directory again.
	ReloadKeys func() error
	// UI is served for every path outside /api. Paths without a file
	// get index.html, so the SPA can route them.
	UI  fs.FS
	Log *slog.Logger
}

// Handler returns the HTTP handler of the server.
func (s *Server) Handler() http.Handler {
	// Routes are registered on the top router: gorilla/mux answers a
	// method mismatch within a subrouter with 404 instead of 405.
	r := mux.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, MaxBodySize)
			next.ServeHTTP(w, r)
		})
	})
	a := apiRouter{r}

	st := s.Store
	all := func(r *http.Request) bool { return r.URL.Query().Get("all") == "true" }

	a.Handle("/roots", list(func(*http.Request) []model.Root { return st.ListRoots() })).Methods(http.MethodGet)
	a.Handle("/roots", create(s, st.CreateRoot)).Methods(http.MethodPost)
	a.Handle("/roots/{id}", get(s, st.GetRoot)).Methods(http.MethodGet)
	a.Handle("/roots/{id}", update(s, http.StatusOK, st.UpdateRoot)).Methods(http.MethodPut)
	a.Handle("/roots/{id}", remove(s, st.DeleteRoot)).Methods(http.MethodDelete)
	a.Handle("/roots/{id}/tree", get(s, st.Tree)).Methods(http.MethodGet)

	a.Handle("/nodes", list(func(r *http.Request) []model.Node { return st.ListNodes(all(r)) })).Methods(http.MethodGet)
	a.Handle("/nodes", create(s, st.CreateNode)).Methods(http.MethodPost)
	a.Handle("/nodes/{id}", get(s, st.GetNode)).Methods(http.MethodGet)
	a.Handle("/nodes/{id}", update(s, http.StatusOK, st.UpdateNode)).Methods(http.MethodPut)
	a.Handle("/nodes/{id}", remove(s, st.DeleteNode)).Methods(http.MethodDelete)
	a.Handle("/nodes/{id}/tree", get(s, st.Subtree)).Methods(http.MethodGet)
	a.Handle("/nodes/{id}/path", get(s, st.Path)).Methods(http.MethodGet)
	a.Handle("/nodes/{id}/history", get(s, st.NodeHistory)).Methods(http.MethodGet)
	a.Handle("/nodes/{id}/versions", update(s, http.StatusCreated, st.NewNodeVersion)).Methods(http.MethodPost)

	a.Handle("/leaves", list(func(r *http.Request) []model.Leaf { return st.ListLeaves(all(r)) })).Methods(http.MethodGet)
	a.Handle("/leaves", create(s, st.CreateLeaf)).Methods(http.MethodPost)
	a.Handle("/leaves/{id}", get(s, st.GetLeaf)).Methods(http.MethodGet)
	a.Handle("/leaves/{id}", update(s, http.StatusOK, st.UpdateLeaf)).Methods(http.MethodPut)
	a.Handle("/leaves/{id}", remove(s, st.DeleteLeaf)).Methods(http.MethodDelete)
	a.Handle("/leaves/{id}/path", get(s, s.leafPath)).Methods(http.MethodGet)
	a.Handle("/leaves/{id}/history", get(s, st.LeafHistory)).Methods(http.MethodGet)
	a.Handle("/leaves/{id}/versions", update(s, http.StatusCreated, st.NewLeafVersion)).Methods(http.MethodPost)

	a.Handle("/seeds", list(func(*http.Request) []model.Seed { return st.ListSeeds() })).Methods(http.MethodGet)
	a.Handle("/seeds", create(s, st.CreateSeed)).Methods(http.MethodPost)
	a.Handle("/seeds/{id}", get(s, st.GetSeed)).Methods(http.MethodGet)
	a.Handle("/seeds/{id}", update(s, http.StatusOK, st.UpdateSeed)).Methods(http.MethodPut)
	a.Handle("/seeds/{id}", remove(s, st.DeleteSeed)).Methods(http.MethodDelete)
	a.Handle("/seeds/{id}/status", get(s, func(id uuid.UUID) (seed.Status, error) { return seed.Compute(st, id) })).Methods(http.MethodGet)
	a.Handle("/seeds/{id}/shoots", get(s, s.seedShoots)).Methods(http.MethodGet)
	a.Handle("/seeds/{id}/attestations", get(s, s.seedAttestations)).Methods(http.MethodGet)
	a.Handle("/seeds/{id}/nodes/{nodeId}/attestations", http.HandlerFunc(s.uploadAttestation)).Methods(http.MethodPost)

	a.Handle("/shoots", list(s.listShoots)).Methods(http.MethodGet)
	a.Handle("/shoots", create(s, st.CreateShoot)).Methods(http.MethodPost)
	a.Handle("/shoots/{id}", get(s, st.GetShoot)).Methods(http.MethodGet)
	a.Handle("/shoots/{id}", update(s, http.StatusOK, st.UpdateShoot)).Methods(http.MethodPut)
	a.Handle("/shoots/{id}", remove(s, st.DeleteShoot)).Methods(http.MethodDelete)

	a.Handle("/attestations/{id}", get(s, st.GetAttestation)).Methods(http.MethodGet)
	a.Handle("/attestations/{id}", remove(s, st.DeleteAttestation)).Methods(http.MethodDelete)
	a.Handle("/attestations/{id}/verify", get(s, s.reverifyAttestation)).Methods(http.MethodPost)

	a.Handle("/keys", list(s.listKeys)).Methods(http.MethodGet)
	a.Handle("/keys/reload", http.HandlerFunc(s.reloadKeys)).Methods(http.MethodPost)
	a.Handle("/dangling", list(func(*http.Request) []store.Dangling { return st.Dangling() })).Methods(http.MethodGet)

	if s.UI != nil {
		r.MatcherFunc(func(r *http.Request, _ *mux.RouteMatch) bool { return !strings.HasPrefix(r.URL.Path, "/api/") }).
			Methods(http.MethodGet, http.MethodHead).Handler(spaHandler(s.UI))
	}
	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	r.MethodNotAllowedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	})
	return s.logRequests(r)
}

// apiRouter registers routes below /api.
type apiRouter struct{ r *mux.Router }

func (a apiRouter) Handle(path string, h http.Handler) *mux.Route {
	return a.r.Handle("/api"+path, h)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		s.Log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rw.status, "duration", time.Since(start))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// spaHandler serves files from ui and index.html for unknown paths.
func spaHandler(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if st, err := fs.Stat(ui, name); err != nil || st.IsDir() {
			http.ServeFileFS(w, r, ui, "index.html")
			return
		}
		files.ServeHTTP(w, r)
	})
}

// Helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail writes err with the status matching its kind. Invalid input and
// conflicts can wrap ErrNotFound for a referenced element, so they are
// checked first.
func (s *Server) fail(w http.ResponseWriter, err error) {
	var maxBytes *http.MaxBytesError
	switch {
	case errors.Is(err, store.ErrInvalid), errors.Is(err, attest.ErrNotAttestation), errors.Is(err, errBadRequest):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.As(err, &maxBytes):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	default:
		s.Log.Error("internal error", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

var errBadRequest = errors.New("bad request")

func pathID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(mux.Vars(r)[name])
	if err != nil {
		return id, errors.Join(errBadRequest, errors.New(name+" is not a UUID"))
	}
	return id, nil
}

func decode[T any](r *http.Request) (T, error) {
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			return v, err
		}
		return v, errors.Join(errBadRequest, err)
	}
	return v, nil
}

// get handles GET /x/{id}.
func get[T any](s *Server, fn func(uuid.UUID) (T, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			s.fail(w, err)
			return
		}
		v, err := fn(id)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

// create handles POST /x.
func create[T any](s *Server, fn func(T) (T, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, err := decode[T](r)
		if err != nil {
			s.fail(w, err)
			return
		}
		v, err := fn(in)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, v)
	}
}

// update handles PUT /x/{id} and POST /x/{id}/versions.
func update[T any](s *Server, status int, fn func(uuid.UUID, T) (T, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			s.fail(w, err)
			return
		}
		in, err := decode[T](r)
		if err != nil {
			s.fail(w, err)
			return
		}
		v, err := fn(id, in)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, status, v)
	}
}

// remove handles DELETE /x/{id}.
func remove(s *Server, fn func(uuid.UUID) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			s.fail(w, err)
			return
		}
		if err := fn(id); err != nil {
			s.fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// list handles GET /x.
func list[T any](fn func(r *http.Request) []T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := fn(r)
		if v == nil {
			v = []T{}
		}
		writeJSON(w, http.StatusOK, v)
	}
}

// leafPath returns the Nodes from the top of the tree down to the Leaf's
// parent.
func (s *Server) leafPath(id uuid.UUID) ([]model.Node, error) {
	l, err := s.Store.GetLeaf(id)
	if err != nil {
		return nil, err
	}
	return s.Store.Path(l.ParentID)
}

func (s *Server) seedShoots(id uuid.UUID) ([]model.Shoot, error) {
	if _, err := s.Store.GetSeed(id); err != nil {
		return nil, err
	}
	return s.Store.ListShoots(id), nil
}

// seedAttestations lists the Attestations of a Seed without their raw
// content.
func (s *Server) seedAttestations(id uuid.UUID) ([]model.Attestation, error) {
	if _, err := s.Store.GetSeed(id); err != nil {
		return nil, err
	}
	atts := s.Store.ListAttestations(id)
	for i := range atts {
		atts[i].Raw = nil
	}
	return atts, nil
}

// listShoots lists all Shoots, or those of the Seed given as seedId.
func (s *Server) listShoots(r *http.Request) []model.Shoot {
	seedID, err := uuid.Parse(r.URL.Query().Get("seedId"))
	if err != nil {
		seedID = uuid.Nil
	}
	return s.Store.ListShoots(seedID)
}

// Attestations

// uploadAttestation takes a DSSE envelope, Sigstore bundle or bare
// statement as the request body and stores it with its verification.
func (s *Server) uploadAttestation(w http.ResponseWriter, r *http.Request) {
	seedID, err := pathID(r, "id")
	if err != nil {
		s.fail(w, err)
		return
	}
	nodeID, err := pathID(r, "nodeId")
	if err != nil {
		s.fail(w, err)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !json.Valid(raw) {
		s.fail(w, errors.Join(errBadRequest, errors.New("body is not JSON")))
		return
	}
	n, err := s.Store.GetNode(nodeID)
	if err != nil {
		s.fail(w, err)
		return
	}
	p, v, err := s.Verifier.Verify(raw, n.RequiredAttestation)
	if err != nil {
		s.fail(w, err)
		return
	}
	a, err := s.Store.CreateAttestation(model.Attestation{
		SeedID: seedID, NodeID: nodeID, Raw: raw,
		Format: p.Format, PredicateType: p.PredicateType, Subjects: p.Subjects, Verification: v,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// reverifyAttestation verifies an Attestation again, for example after
// the trusted keys or the Node's requirement changed.
func (s *Server) reverifyAttestation(id uuid.UUID) (model.Attestation, error) {
	a, err := s.Store.GetAttestation(id)
	if err != nil {
		return a, err
	}
	n, err := s.Store.GetNode(a.NodeID)
	if err != nil {
		return a, errors.Join(store.ErrConflict, err)
	}
	_, v, err := s.Verifier.Verify(a.Raw, n.RequiredAttestation)
	if err != nil {
		return a, err
	}
	return s.Store.SetVerification(id, v)
}

// Keys

func (s *Server) listKeys(*http.Request) []attest.Key {
	if s.Keys == nil {
		return nil
	}
	return s.Keys()
}

func (s *Server) reloadKeys(w http.ResponseWriter, r *http.Request) {
	if s.ReloadKeys != nil {
		if err := s.ReloadKeys(); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	list(s.listKeys).ServeHTTP(w, r)
}
