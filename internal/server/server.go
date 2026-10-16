// Package server serves the repositories of a store over HTTP.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sync"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/store"
)

// catalogPushPaths are the requests that carry a push to the catalog: git
// http-backend serves both the repository's real name and the alias
// without ".git".
var catalogPushPaths = [2]string{
	"/git/catalog.git/git-receive-pack",
	"/git/catalog/git-receive-pack",
}

// workspacePushPath matches the requests that carry a push to a workspace,
// git http-backend serves both the repository's real name and the alias
// without ".git", the same way it does for the catalog. The captured group
// is the workspace id.
var workspacePushPath = regexp.MustCompile(`^/git/workspaces/([^/]+?)(?:\.git)?/git-receive-pack$`)

// Server serves the repositories of a store. Repository layout, hooks and
// writes belong to the store (ruling 2.17).
type Server struct {
	st  *store.Store
	api http.Handler // mounted at /api/ when set
	ui  http.Handler // mounted at / when set

	auth       http.Handler                    // mounted at /auth/ when set
	middleware func(http.Handler) http.Handler // wraps the whole mux when set

	mu              sync.Mutex
	onCatalogPush   []func()
	onWorkspacePush []func(id string)
	onShutdown      []func()
}

// New returns a server for st.
func New(st *store.Store) *Server { return &Server{st: st} }

// WithAPI mounts h at /api/ in the handler that Handler returns. h sees the
// full path, including /api/. It returns s.
func (s *Server) WithAPI(h http.Handler) *Server {
	s.api = h
	return s
}

// WithUI mounts h at "/" in the handler that Handler returns, so that any
// request none of the more specific prefixes (/git/, /api/, /auth/,
// /healthz) claims falls through to the embedded single-page application
// (internal/webui.Handler()), which serves its own index.html for every
// client-side route. It returns s.
func (s *Server) WithUI(h http.Handler) *Server {
	s.ui = h
	return s
}

// WithAuth mounts h at /auth/ in the handler Handler returns, unguarded by
// the sign-in gate WithAPI's and /git/'s handlers get — each /auth/ route
// (Task 5) decides for itself whether it needs a caller. It returns s.
func (s *Server) WithAuth(h http.Handler) *Server {
	s.auth = h
	return s
}

// WithMiddleware wraps the whole handler Handler returns (/git/, /auth/,
// /api/ and /healthz together) in mw, after routing — so mw sees the
// original request path and can special-case it (auth.Manager.Middleware
// does, for its excludedPrefixes). mw is the only thing that ever
// resolves a caller into a request's context; Handler's own gate on
// /api/ and /git/ only ever checks whether one is there (Review Focus 5):
// a Server built without WithMiddleware refuses every /api/ and /git/
// request, because nothing ever puts a caller in context for the gate to
// find. It returns s.
func (s *Server) WithMiddleware(mw func(http.Handler) http.Handler) *Server {
	s.middleware = mw
	return s
}

// OnCatalogPush registers f to be called after each request to push the
// catalog (/git/catalog.git/git-receive-pack, or the /git/catalog alias)
// completes, whether the push was accepted or not. f runs before the
// response ends, so the pusher waits for it.
func (s *Server) OnCatalogPush(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onCatalogPush = append(s.onCatalogPush, f)
}

// OnWorkspacePush registers f to be called, with the workspace's id, after
// each request to push a workspace (/git/workspaces/<id>.git/git-receive-pack,
// or the /git/workspaces/<id> alias) completes, whether the push was
// accepted or not. f runs before the response ends, so the pusher waits
// for it.
func (s *Server) OnWorkspacePush(f func(id string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onWorkspacePush = append(s.onWorkspacePush, f)
}

// OnShutdown registers f to be called by WaitForShutdown. It lets a caller
// of New attach background work it started, such as a run service's
// workers, so whoever drives the process's graceful shutdown (cmd/custos's
// runServe) can wait for that work to actually finish — not just be asked
// to stop — without this package depending on the caller's type. f should
// return once that work has stopped, or ctx (see WaitForShutdown) is done.
func (s *Server) OnShutdown(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onShutdown = append(s.onShutdown, f)
}

// WaitForShutdown calls every OnShutdown callback (concurrently, since
// each is expected to block on its own background work) and returns once
// they have all returned, or ctx is done, whichever comes first. A
// callback still running when ctx is done is abandoned: WaitForShutdown
// returns anyway, so a slow or stuck background worker cannot hang the
// process forever, at the cost of possibly leaving that work unfinished
// (for example, a processor container not yet removed).
func (s *Server) WaitForShutdown(ctx context.Context) {
	s.mu.Lock()
	fs := append([]func(){}, s.onShutdown...)
	s.mu.Unlock()
	if len(fs) == 0 {
		return
	}
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		wg.Add(len(fs))
		for _, f := range fs {
			go func(f func()) {
				defer wg.Done()
				f()
			}(f)
		}
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
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
	mux.Handle("/git/", s.requireSignedIn(true, s.pushGate(withContentLength(s.notifyPush(backend)))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	if s.api != nil {
		mux.Handle("/api/", s.requireSignedIn(false, s.api))
	}
	if s.auth != nil {
		mux.Handle("/auth/", s.auth)
	}
	if s.ui != nil {
		mux.Handle("/", s.ui)
	}
	var h http.Handler = mux
	if s.middleware != nil {
		h = s.middleware(h)
	}
	return h, nil
}

// requireSignedIn wraps h so it 401s before h runs when the request
// carries no caller (api.CallerOf) — unconditionally, not only when
// WithMiddleware happens to be wired (Review Focus 5). gitRealm adds the
// WWW-Authenticate challenge git needs before it will send Basic
// credentials from a URL or a credential helper; /api/ never sends it, so
// a browser running the UI never shows its own password dialog.
func (s *Server) requireSignedIn(gitRealm bool, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := api.CallerOf(r); !ok {
			if gitRealm {
				w.Header().Set("WWW-Authenticate", `Basic realm="custos"`)
			}
			http.Error(w, "not signed in", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// pushGate wraps the /git/ handler so a push to the catalog needs the
// task-author role and a push to a workspace needs the engineer role for
// that workspace (Review Focus 4), checked before h runs — so before
// withContentLength or git http-backend read one byte of the request
// body. Reading (info/refs, upload-pack) is not matched here, so it is
// unaffected: requireSignedIn already covers it. The caller is always
// present: pushGate only ever wraps requireSignedIn's h.
func (s *Server) pushGate(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			c, _ := api.CallerOf(r)
			switch {
			case slices.Contains(catalogPushPaths[:], r.URL.Path):
				if !c.Has("task-author", "") {
					http.Error(w, "this push needs the task-author role", http.StatusForbidden)
					return
				}
			default:
				if m := workspacePushPath.FindStringSubmatch(r.URL.Path); m != nil && !c.Has("engineer", m[1]) {
					http.Error(w, "this push needs the engineer role for workspace "+m[1], http.StatusForbidden)
					return
				}
			}
		}
		h.ServeHTTP(w, r)
	})
}

// notifyPush wraps h so that, once it has served the request, a push to the
// catalog fires every OnCatalogPush callback and a push to a workspace fires
// every OnWorkspacePush callback with that workspace's id.
func (s *Server) notifyPush(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		if r.Method != http.MethodPost {
			return
		}
		if slices.Contains(catalogPushPaths[:], r.URL.Path) {
			s.mu.Lock()
			fs := append([]func(){}, s.onCatalogPush...)
			s.mu.Unlock()
			for _, f := range fs {
				f()
			}
			return
		}
		if m := workspacePushPath.FindStringSubmatch(r.URL.Path); m != nil {
			id := m[1]
			s.mu.Lock()
			fs := append([]func(string){}, s.onWorkspacePush...)
			s.mu.Unlock()
			for _, f := range fs {
				f(id)
			}
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
