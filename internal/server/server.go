// Package server serves the catalog and workspace repositories over HTTP and
// keeps their pre-receive hooks installed.
package server

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/hook"
	"github.com/emeland-io/custos/internal/task"
)

// Server owns the repositories below <data-dir>/repos:
//
//	catalog.git
//	workspaces/<workspace-uuid>.git
type Server struct {
	reposDir string
	exe      string
}

// New returns a server for the data directory without touching it. exe is
// the custos binary the hooks of repositories it creates run.
func New(dataDir, exe string) *Server {
	return &Server{reposDir: filepath.Join(dataDir, "repos"), exe: exe}
}

// Open prepares the data directory for serving: it creates the catalog
// repository if needed and rewrites the hooks of all repositories to run
// exe, so moving the binary between starts is safe.
func Open(dataDir, exe string) (*Server, error) {
	s := New(dataDir, exe)
	wsDir := filepath.Join(s.reposDir, "workspaces")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		return nil, err
	}
	if err := s.ensure(filepath.Join(s.reposDir, "catalog.git"), hook.Catalog); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(wsDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".git") {
			if err := s.ensure(filepath.Join(wsDir, e.Name()), hook.Workspace); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

// CreateWorkspace creates the empty repository of a new workspace. It
// installs the hook of the new repository only and leaves the others alone.
func (s *Server) CreateWorkspace(id string) error {
	if !task.ValidID(id) {
		return fmt.Errorf("workspace id %q is not a lowercase UUID v4", id)
	}
	wsDir := filepath.Join(s.reposDir, "workspaces")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		return err
	}
	dir := filepath.Join(wsDir, id+".git")
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("workspace %s already exists", id)
	}
	return s.ensure(dir, hook.Workspace)
}

func (s *Server) ensure(dir string, kind hook.Kind) error {
	repo := &gitrepo.Repo{Dir: dir}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		if repo, err = gitrepo.InitBare(dir); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	script, err := hook.Script(s.exe, kind)
	if err != nil {
		return err
	}
	return repo.InstallHook("pre-receive", script)
}

// Handler serves the repositories below /git through git http-backend.
func (s *Server) Handler() (http.Handler, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("custos needs git on PATH: %w", err)
	}
	backend := &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Root: "/git",
		Env:  []string{"GIT_PROJECT_ROOT=" + s.reposDir, "GIT_HTTP_EXPORT_ALL=1"},
	}
	mux := http.NewServeMux()
	mux.Handle("/git/", withContentLength(backend))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	return mux, nil
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
