// Package server serves the repositories of a store over HTTP.
package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/emeland-io/custos/internal/store"
)

// catalogPushPath is the request that carries a push to the catalog.
const catalogPushPath = "/git/catalog.git/git-receive-pack"

// Server serves the repositories of a store. Repository layout, hooks and
// writes belong to the store (ruling 2.17).
type Server struct {
	st *store.Store

	mu            sync.Mutex
	onCatalogPush []func()
}

// New returns a server for st.
func New(st *store.Store) *Server { return &Server{st: st} }

// OnCatalogPush registers f to be called after each request to
// /git/catalog.git/git-receive-pack completes, whether the push was accepted
// or not. f runs before the response ends, so the pusher waits for it.
func (s *Server) OnCatalogPush(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onCatalogPush = append(s.onCatalogPush, f)
}

// Handler serves the repositories below /git through git http-backend, and
// GET /healthz.
func (s *Server) Handler() (http.Handler, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("custos needs git on PATH: %w", err)
	}
	backend := &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Root: "/git",
		Env:  []string{"GIT_PROJECT_ROOT=" + filepath.Join(s.st.DataDir(), "repos"), "GIT_HTTP_EXPORT_ALL=1"},
	}
	mux := http.NewServeMux()
	mux.Handle("/git/", withContentLength(s.notifyCatalogPush(backend)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	return mux, nil
}

func (s *Server) notifyCatalogPush(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		if r.Method != http.MethodPost || r.URL.Path != catalogPushPath {
			return
		}
		s.mu.Lock()
		fs := append([]func(){}, s.onCatalogPush...)
		s.mu.Unlock()
		for _, f := range fs {
			f()
		}
	})
}

// maxRequestBytes bounds every request body below /git/, chunked or not, so
// that a client cannot exhaust disk by streaming an unbounded push: catalogs
// and workspaces keep attachments outside git, so legitimate pushes stay
// small. It is a var, not a const, so tests can lower it temporarily.
var maxRequestBytes int64 = 256 << 20 // 256 MiB

// withContentLength buffers chunked request bodies in a temporary file.
// git sends pushes above 1 MiB chunked, and net/http/cgi rejects chunked
// bodies because CGI needs CONTENT_LENGTH.
func withContentLength(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxRequestBytes {
			http.Error(w, "push too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		if r.ContentLength >= 0 && len(r.TransferEncoding) == 0 {
			h.ServeHTTP(w, r)
			return
		}
		f, err := os.CreateTemp("", "custos-request-*")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer os.Remove(f.Name())
		defer f.Close()
		n, err := io.Copy(f, r.Body)
		if err == nil {
			_, err = f.Seek(0, io.SeekStart)
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "push too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = f
		r.ContentLength = n
		r.TransferEncoding = nil
		h.ServeHTTP(w, r)
	})
}
