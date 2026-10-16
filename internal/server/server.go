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
	"strings"
	"sync"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

// pushPathPattern recognises any POST request path under /git/ whose final
// segment is git-receive-pack — that is, any attempted push, canonical
// spelling or not — and captures the repository portion between "/git/"
// and "/git-receive-pack" for canonicalPushTarget to classify. Capturing
// broadly here, then requiring an exact match below, is what lets
// pushGate/notifyPush default-deny anything canonicalPushTarget doesn't
// recognise, instead of silently letting it through (see
// canonicalPushTarget's comment).
var pushPathPattern = regexp.MustCompile(`^/git/(.+)/git-receive-pack$`)

// canonicalCatalogPushPaths are the only two byte-for-byte spellings of
// the catalog's repository portion that canonicalPushTarget accepts: the
// repository's real name and the alias git http-backend also serves
// without ".git".
var canonicalCatalogPushPaths = [2]string{"catalog", "catalog.git"}

// pushTarget identifies which repository a canonically-spelled push path
// addresses.
type pushTarget struct {
	catalog     bool
	workspaceID string // set, and catalog false, for a workspace push
}

// canonicalPushTarget reports what repoPath — the portion pushPathPattern
// captured between "/git/" and "/git-receive-pack" — addresses, and
// whether that spelling is the exact canonical one.
//
// This is deliberately NOT a normalize-then-compare check (lower-case
// repoPath and see if it matches a known name): the final whole-branch
// re-review found that shape of fix is fundamentally incomplete, because
// the real mismatch it has to close is between "what this code matches as
// text" and "what the OS filesystem treats as the same path," and those
// two can diverge in more ways than ASCII casing. The prior fix lower-cased
// the path with strings.ToLower before comparing, which closed the ASCII
// case-variant bypass (e.g. "/git/Catalog.git") but missed that
// strings.ToLower does not perform full Unicode case-folding: a workspace
// path spelled with the Unicode "long s" U+017F in place of an ASCII "s"
// in "workspaces" (e.g. "/git/workſpaces/<id>.git") case-folds to the
// exact same directory as "/git/workspaces/<id>.git" on a case-insensitive,
// Unicode-normalizing filesystem (APFS, the macOS default) — but
// strings.ToLower leaves U+017F alone, so the lower-cased path still
// didn't match either known literal, fell through pushGate's switch with
// neither case taken, and reached git http-backend with no role check at
// all (reproduced live: a zero-role token was refused on the canonical
// path but succeeded via the U+017F variant, and the workspace's main
// actually advanced).
//
// Rather than chase that gap (and the next Unicode normalization or
// filesystem quirk after it) with another folding rule, canonicalPushTarget
// requires repoPath to already be written, byte-for-byte, in the one
// canonical form each repository is addressed by. Anything else —
// including a spelling the filesystem would still resolve to the very
// same repository — reports ok=false, and pushGate/notifyPush treat that
// as an unconditional reject: no role check, no fallthrough to git
// http-backend, before either ever sees the request.
func canonicalPushTarget(repoPath string) (target pushTarget, ok bool) {
	if slices.Contains(canonicalCatalogPushPaths[:], repoPath) {
		return pushTarget{catalog: true}, true
	}
	const workspacePrefix = "workspaces/"
	rest, isWorkspace := strings.CutPrefix(repoPath, workspacePrefix)
	if !isWorkspace {
		return pushTarget{}, false
	}
	id := strings.TrimSuffix(rest, ".git")
	if !task.ValidID(id) || (rest != id && rest != id+".git") {
		return pushTarget{}, false
	}
	return pushTarget{workspaceID: id}, true
}

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
//
// Any POST whose path pushPathPattern recognises as an attempted push
// (ends in /git-receive-pack) but whose repository portion
// canonicalPushTarget does not accept as byte-for-byte canonical is
// rejected outright, with no role check and no fallthrough to h — see
// canonicalPushTarget's comment for why this default-deny shape replaced
// the prior normalize-then-compare fix (spec §11.4).
func (s *Server) pushGate(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if m := pushPathPattern.FindStringSubmatch(r.URL.Path); m != nil {
				target, ok := canonicalPushTarget(m[1])
				if !ok {
					http.Error(w, "not found", http.StatusNotFound)
					return
				}
				c, _ := api.CallerOf(r)
				if target.catalog {
					if !c.Has("task-author", "") {
						http.Error(w, "this push needs the task-author role", http.StatusForbidden)
						return
					}
				} else if !c.Has("engineer", target.workspaceID) {
					http.Error(w, "this push needs the engineer role for workspace "+target.workspaceID, http.StatusForbidden)
					return
				}
			}
		}
		h.ServeHTTP(w, r)
	})
}

// notifyPush wraps h so that, once it has served the request, a push to the
// catalog fires every OnCatalogPush callback and a push to a workspace fires
// every OnWorkspacePush callback with that workspace's id. It uses the same
// canonicalPushTarget classification pushGate does, so a path pushGate
// already rejected (not canonical) never reaches here having done
// anything worth reconciling: the request never reached h either.
func (s *Server) notifyPush(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		if r.Method != http.MethodPost {
			return
		}
		m := pushPathPattern.FindStringSubmatch(r.URL.Path)
		if m == nil {
			return
		}
		target, ok := canonicalPushTarget(m[1])
		if !ok {
			return
		}
		if target.catalog {
			s.mu.Lock()
			fs := append([]func(){}, s.onCatalogPush...)
			s.mu.Unlock()
			for _, f := range fs {
				f()
			}
			return
		}
		id := target.workspaceID
		s.mu.Lock()
		fs := append([]func(string){}, s.onWorkspacePush...)
		s.mu.Unlock()
		for _, f := range fs {
			f(id)
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
