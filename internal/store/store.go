// Package store owns the repositories below <data-dir>/repos, keeps their
// pre-receive hooks installed, and writes commits to them (ruling 2.17):
//
//	catalog.git
//	workspaces/<workspace-uuid>.git
//
// Writes to one repository are serialised by a lock in this process; pushes
// do not take it, so every ref update is a compare-and-swap (ruling 2.3).
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/hook"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/rules"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	ErrConflict = errors.New("conflict") // wraps gitrepo.ErrRefMoved and similar
)

// RejectedError reports validation problems of a write.
type RejectedError struct{ Problems []problem.Problem }

func (e *RejectedError) Error() string {
	msgs := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		msgs[i] = p.String()
	}
	return "rejected: " + strings.Join(msgs, "; ")
}

const mainRef = "refs/heads/main"

// Store is the data directory of one server.
type Store struct {
	dataDir   string
	reposDir  string
	exe       string // the custos binary the hooks run
	publicURL string // without trailing slash

	mu     sync.Mutex
	locks  map[string]*sync.Mutex // by "catalog" or workspace id
	onMain []func(id string)      // see OnMainMoved
}

// New returns a store for the data directory without touching it. exe is the
// custos binary that hooks of repositories it creates run; publicURL is the
// base URL clients reach the server at, such as http://127.0.0.1:8080.
func New(dataDir, exe, publicURL string) *Store {
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	return &Store{
		dataDir:   dataDir,
		reposDir:  filepath.Join(dataDir, "repos"),
		exe:       exe,
		publicURL: strings.TrimRight(publicURL, "/"),
		locks:     map[string]*sync.Mutex{},
	}
}

// Open prepares the data directory: it creates the directories and the
// catalog repository if needed. It does not touch hooks; see InstallHooks.
func Open(dataDir, exe, publicURL string) (*Store, error) {
	s := New(dataDir, exe, publicURL)
	if err := os.MkdirAll(s.workspacesDir(), 0o755); err != nil {
		return nil, err
	}
	dir := s.CatalogRepo().Dir
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		if _, err := gitrepo.InitBare(dir); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return s, nil
}

// InstallHooks rewrites the pre-receive hooks of the catalog and of all
// workspaces to run exe, so moving the binary between starts is safe.
func (s *Store) InstallHooks() error {
	if err := s.installHook(s.CatalogRepo(), ""); err != nil {
		return err
	}
	ids, err := s.WorkspaceIDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.installHook(&gitrepo.Repo{Dir: s.workspaceDir(id)}, id); err != nil {
			return err
		}
	}
	return nil
}

// installHook installs the hook of the catalog (id "") or of workspace id.
func (s *Store) installHook(repo *gitrepo.Repo, id string) error {
	opts := hook.ScriptOptions{Kind: hook.Catalog}
	if id != "" {
		opts = hook.ScriptOptions{Kind: hook.Workspace, CatalogDir: s.CatalogRepo().Dir, WorkspaceID: id}
	}
	script, err := hook.Script(s.exe, opts)
	if err != nil {
		return err
	}
	return repo.InstallHook("pre-receive", script)
}

// DataDir returns the absolute data directory.
func (s *Store) DataDir() string { return s.dataDir }

// CatalogRepo returns the catalog repository.
func (s *Store) CatalogRepo() *gitrepo.Repo {
	return &gitrepo.Repo{Dir: filepath.Join(s.reposDir, "catalog.git")}
}

// CatalogURL returns the URL clients clone the catalog from.
func (s *Store) CatalogURL() string { return s.publicURL + "/git/catalog.git" }

// WorkspaceRepo returns the repository of workspace id, or an error wrapping
// ErrNotFound.
func (s *Store) WorkspaceRepo(id string) (*gitrepo.Repo, error) {
	if !task.ValidID(id) {
		return nil, fmt.Errorf("%w: workspace %q", ErrNotFound, id)
	}
	dir := s.workspaceDir(id)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: workspace %s", ErrNotFound, id)
	} else if err != nil {
		return nil, err
	}
	return &gitrepo.Repo{Dir: dir}, nil
}

// WorkspaceIDs returns the ids of all workspaces, sorted. Directories whose
// name is not "<uuid>.git" are ignored.
func (s *Store) WorkspaceIDs() ([]string, error) {
	entries, err := os.ReadDir(s.workspacesDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".git"); ok && e.IsDir() && task.ValidID(id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// CreateWorkspace creates the bare repository with its hook and an initial
// commit on main whose custos.yaml pins the current catalog main (ruling 2.13).
// ErrExists if it exists; error if the catalog has no main. Only the new
// repository's hook is installed (ruling 1.8). A successful call moves main
// from nothing to the initial commit and calls the OnMainMoved callbacks
// after the workspace's lock is released.
func (s *Store) CreateWorkspace(id string, author gitrepo.Signature) (err error) {
	if !task.ValidID(id) {
		return &RejectedError{Problems: []problem.Problem{{
			Rule: problem.RuleWorkspaceID, Message: fmt.Sprintf("workspace id %q is not a lowercase UUID v4", id),
		}}}
	}
	unlock := s.Lock(id)
	// Registered before the unlock defer, so by LIFO ordering it runs after
	// unlock has already released the lock.
	defer func() {
		if err == nil {
			s.mainMoved(id)
		}
	}()
	defer unlock()
	pin, err := s.catalogMain()
	if err != nil {
		return err
	}
	repo, err := s.CreateWorkspaceRepo(id)
	if err != nil {
		return err
	}
	return s.finishCreateWorkspace(repo, id, author, pin)
}

// finishCreateWorkspace commits custos.yaml pinned to pin on repo's empty
// main. The repository is removed on failure, except when the final
// UpdateRef loses its compare-and-swap to a push that landed in the window
// between CreateWorkspaceRepo and here: removing it then would erase a push
// that was already accepted.
func (s *Store) finishCreateWorkspace(repo *gitrepo.Repo, id string, author gitrepo.Signature, pin string) error {
	config, err := workspace.MarshalConfig(workspace.Config{
		Workspace: id,
		Catalog:   workspace.CatalogPin{URL: s.CatalogURL(), Commit: pin},
	})
	if err != nil {
		os.RemoveAll(repo.Dir)
		return err
	}
	commit, err := repo.WriteCommit(gitrepo.CommitRequest{
		Changes: []gitrepo.Change{{Path: workspace.ConfigPath, Data: config}},
		Author:  author,
		Message: "Create workspace " + id,
	})
	if err != nil {
		os.RemoveAll(repo.Dir)
		return err
	}
	if err := s.validateMain(repo, id, "", commit); err != nil {
		os.RemoveAll(repo.Dir)
		return err
	}
	err = conflict(repo.UpdateRef(mainRef, commit, ""))
	if err != nil && !errors.Is(err, ErrConflict) {
		os.RemoveAll(repo.Dir)
	}
	return err
}

// CreateWorkspaceRepo creates an empty bare repository with its hook (used by
// fork in 2d). It does not take the workspace's lock.
func (s *Store) CreateWorkspaceRepo(id string) (*gitrepo.Repo, error) {
	if !task.ValidID(id) {
		return nil, &RejectedError{Problems: []problem.Problem{{
			Rule: problem.RuleWorkspaceID, Message: fmt.Sprintf("workspace id %q is not a lowercase UUID v4", id),
		}}}
	}
	if err := os.MkdirAll(s.workspacesDir(), 0o755); err != nil {
		return nil, err
	}
	dir := s.workspaceDir(id)
	if err := os.Mkdir(dir, 0o755); errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("%w: workspace %s", ErrExists, id)
	} else if err != nil {
		return nil, err
	}
	repo, err := gitrepo.InitBare(dir)
	if err == nil {
		err = s.installHook(repo, id)
	}
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return repo, nil
}

// Lock serialises writes to one repository ("catalog" or a workspace id). It
// returns the unlock function.
func (s *Store) Lock(repo string) func() {
	s.mu.Lock()
	m := s.locks[repo]
	if m == nil {
		m = &sync.Mutex{}
		s.locks[repo] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// OnMainMoved registers f to be called with a workspace's id after
// UpdateWorkspace, SetRef or CreateWorkspace moved that workspace's main to
// a genuinely different commit. f runs synchronously in the writer's
// goroutine, after the workspace's lock has already been released, so it
// may itself write to the store (including calling UpdateWorkspace) without
// deadlocking; it should return quickly. It is not called for writes to
// other branches, for writes that change nothing, or for writes that fail
// (validation, lost compare-and-swap). Pushes do not go through the store;
// see server.OnWorkspacePush.
func (s *Store) OnMainMoved(f func(id string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onMain = append(s.onMain, f)
}

// mainMoved calls the OnMainMoved callbacks. The caller holds no lock on the
// workspace.
func (s *Store) mainMoved(id string) {
	s.mu.Lock()
	fs := slices.Clone(s.onMain)
	s.mu.Unlock()
	for _, f := range fs {
		f(id)
	}
}

// catalogMain returns the commit of the catalog's main.
func (s *Store) catalogMain() (string, error) {
	errEmpty := fmt.Errorf("%w: the catalog has no main branch yet; push the catalog first", ErrConflict)
	cat := s.CatalogRepo()
	if _, err := os.Stat(cat.Dir); errors.Is(err, fs.ErrNotExist) {
		return "", errEmpty
	}
	oid, ok, err := cat.ResolveRef(mainRef)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errEmpty
	}
	return oid, nil
}

// validateMain applies the push rules to moving workspace id's main.
func (s *Store) validateMain(repo *gitrepo.Repo, id, oldOID, newOID string) error {
	ps, err := rules.CheckWorkspaceUpdate(repo, s.CatalogRepo(), id, oldOID, newOID)
	if err != nil {
		return err
	}
	if len(ps) > 0 {
		return &RejectedError{Problems: ps}
	}
	return nil
}

// conflict marks a lost compare-and-swap as ErrConflict.
func conflict(err error) error {
	if errors.Is(err, gitrepo.ErrRefMoved) {
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	return err
}

func (s *Store) workspacesDir() string { return filepath.Join(s.reposDir, "workspaces") }

func (s *Store) workspaceDir(id string) string {
	return filepath.Join(s.workspacesDir(), id+".git")
}
