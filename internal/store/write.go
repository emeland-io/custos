package store

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing/fstest"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/rules"
	"github.com/emeland-io/custos/internal/status"
	"github.com/emeland-io/custos/internal/workspace"
)

// UpdateWorkspace applies edit to the tree at ref ("refs/heads/main" or another
// branch; the ref may not exist yet for branches, then the base is main) under
// the workspace's lock. No changes → no commit, returns the current oid ("" for
// a branch that does not exist). Changes that leave a file as it is do not
// count. Updates of main are validated with rules.CheckWorkspaceUpdate
// (RejectedError). The ref is moved with the expected old value (ErrConflict
// when it moved).
func (s *Store) UpdateWorkspace(id, ref string, author gitrepo.Signature, message string,
	edit func(tree fs.FS) ([]gitrepo.Change, error)) (string, error) {
	return s.UpdateWorkspaceAndThen(id, ref, author, message, edit, nil)
}

// UpdateWorkspaceAndThen is UpdateWorkspace, but when the update to ref
// actually reaches the repository — a new commit written and moved onto ref,
// or, when edit asked for nothing effective, the existing oid kept as is —
// and before the workspace's lock is released, it also calls then with the
// repository and the resulting oid.
//
// then runs inside the very same s.Lock(id) span edit did: nothing else that
// also takes this lock (another UpdateWorkspace/UpdateWorkspaceAndThen call,
// or a caller of Lock directly, such as internal/proposal's Write, Reject and
// Accept) can run between the ref update and then. This is what lets a caller
// combine moving ref with another change to the same repository that must
// happen exactly together with that move — such as Accept deleting the
// accepted proposal's branch only once its content has actually landed on
// main, atomically with the landing, so a concurrent Accept or Reject of the
// same proposal reliably observes either "not landed yet" or "landed and the
// proposal branch is already gone," never a window where the branch still
// looks open after main has already moved past it (which is what let two
// concurrent Accepts, or an Accept racing a Reject, both report success for
// one proposal before this existed).
//
// then must not call s.Lock(id) (or Lock(id)) itself — it is already held,
// and this package's lock is not reentrant, so doing so would deadlock.
// then's error is returned together with the oid, since the ref update
// already happened and must not be reported as if it had not; then is not
// called at all when edit fails or the update never reaches a ref write
// (a RejectedError, or a lost compare-and-swap this call does not retry on
// its own).
func (s *Store) UpdateWorkspaceAndThen(id, ref string, author gitrepo.Signature, message string,
	edit func(tree fs.FS) ([]gitrepo.Change, error), then func(repo *gitrepo.Repo, oid string) error) (string, error) {
	repo, err := s.WorkspaceRepo(id)
	if err != nil {
		return "", err
	}
	if err := checkBranch(ref); err != nil {
		return "", err
	}
	unlock := s.Lock(id)
	defer unlock()
	oid, err := s.updateWorkspaceLocked(repo, id, ref, author, message, edit)
	if err != nil {
		return "", err
	}
	if then != nil {
		if terr := then(repo, oid); terr != nil {
			return oid, terr
		}
	}
	return oid, nil
}

// updateWorkspaceLocked is UpdateWorkspace's body, run by a caller that
// already holds s.Lock(id) for its whole duration (UpdateWorkspaceAndThen).
func (s *Store) updateWorkspaceLocked(repo *gitrepo.Repo, id, ref string, author gitrepo.Signature, message string,
	edit func(tree fs.FS) ([]gitrepo.Change, error)) (string, error) {
	old, _, err := repo.ResolveRef(ref)
	if err != nil {
		return "", err
	}
	base := old
	if base == "" {
		if base, _, err = repo.ResolveRef(mainRef); err != nil {
			return "", err
		}
	}
	var tree fs.FS = fstest.MapFS{}
	if base != "" {
		if tree, err = repo.TreeFS(base); err != nil {
			return "", err
		}
	}
	changes, err := edit(tree)
	if err != nil {
		return "", err
	}
	changes = effective(tree, changes)
	if len(changes) == 0 {
		return old, nil
	}
	req := gitrepo.CommitRequest{Base: base, Changes: changes, Author: author, Message: message}
	if base != "" {
		req.Parents = []string{base}
	}
	commit, err := repo.WriteCommit(req)
	if err != nil {
		return "", err
	}
	if ref == mainRef {
		if err := s.validateMain(repo, id, old, commit); err != nil {
			return "", err
		}
	}
	if err := repo.UpdateRef(ref, commit, old); err != nil {
		return "", conflict(err)
	}
	return commit, nil
}

// SetRef moves ref of a workspace to an existing commit (fast-forward or merge
// result computed by the caller) with the same lock, validation (main only) and
// compare-and-swap. oldOID "" = ref must not exist.
func (s *Store) SetRef(id, ref, newOID, oldOID string) error {
	repo, err := s.WorkspaceRepo(id)
	if err != nil {
		return err
	}
	if err := checkBranch(ref); err != nil {
		return err
	}
	unlock := s.Lock(id)
	defer unlock()
	if ref == mainRef {
		if err := s.validateMain(repo, id, oldOID, newOID); err != nil {
			return err
		}
	}
	return conflict(repo.UpdateRef(ref, newOID, oldOID))
}

// Load reads a workspace at rev ("" = main) and the catalog at its pin.
// Problems in the trees are not reported here; see CheckWorkspace.
func (s *Store) Load(id, rev string) (*workspace.Workspace, *catalog.Catalog, error) {
	repo, err := s.WorkspaceRepo(id)
	if err != nil {
		return nil, nil, err
	}
	if rev == "" {
		rev = mainRef
	}
	oid, ok, err := repo.ResolveRef(rev + "^{commit}")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("%w: revision %s of workspace %s", ErrNotFound, rev, id)
	}
	fsys, err := repo.TreeFS(oid)
	if err != nil {
		return nil, nil, err
	}
	w, _ := workspace.Load(fsys)
	pin := w.Config.Catalog.Commit
	cat := s.CatalogRepo()
	if pin == "" || strings.HasPrefix(pin, "-") {
		return nil, nil, fmt.Errorf("workspace %s at %s pins no catalog commit", id, rev)
	}
	pinOID, ok, err := cat.ResolveRef(pin + "^{commit}")
	if err != nil {
		return nil, nil, err
	}
	if !ok || pinOID != pin {
		return nil, nil, fmt.Errorf("workspace %s pins catalog commit %s, which this server's catalog does not have", id, pin)
	}
	cfs, err := cat.TreeFS(pin)
	if err != nil {
		return nil, nil, err
	}
	c, _ := catalog.Load(cfs)
	return w, c, nil
}

// Status computes status.Compute for the workspace's main.
func (s *Store) Status(id string) (*status.Status, error) {
	w, c, err := s.Load(id, "")
	if err != nil {
		return nil, err
	}
	return status.Compute(w, c), nil
}

// CheckWorkspace applies the push rules to the workspace's main as it is, so
// that serve can report a repository edited on disk (spec section 7). An
// empty repository has no problems.
func (s *Store) CheckWorkspace(id string) ([]problem.Problem, error) {
	repo, err := s.WorkspaceRepo(id)
	if err != nil {
		return nil, err
	}
	main, ok, err := repo.ResolveRef(mainRef)
	if err != nil || !ok {
		return nil, err
	}
	return rules.CheckWorkspaceUpdate(repo, s.CatalogRepo(), id, "", main)
}

// effective drops changes that leave tree as it is.
func effective(tree fs.FS, changes []gitrepo.Change) []gitrepo.Change {
	changes = lastPerPath(changes)
	var out []gitrepo.Change
	for _, c := range changes {
		if c.Delete {
			if info, err := fs.Stat(tree, c.Path); err == nil && info.IsDir() {
				continue // nothing to delete: the path is a directory, not a file
			}
		}
		cur, err := fs.ReadFile(tree, c.Path)
		missing := errors.Is(err, fs.ErrNotExist)
		if c.Delete && missing || !c.Delete && err == nil && bytes.Equal(cur, c.Data) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// lastPerPath keeps only the last change to each path, in the order its
// first occurrence appeared: WriteCommit applies changes in order, so an
// earlier change to a path that a later one in the same edit overrides
// never reaches the tree and must not be judged against the base on its
// own (it would otherwise be let through, or wrongly filtered out, by
// comparing it to the base instead of to the change that actually wins).
func lastPerPath(changes []gitrepo.Change) []gitrepo.Change {
	latest := make(map[string]gitrepo.Change, len(changes))
	for _, c := range changes {
		latest[c.Path] = c
	}
	seen := make(map[string]bool, len(changes))
	var out []gitrepo.Change
	for _, c := range changes {
		if seen[c.Path] {
			continue
		}
		seen[c.Path] = true
		out = append(out, latest[c.Path])
	}
	return out
}

func checkBranch(ref string) error {
	if !strings.HasPrefix(ref, "refs/heads/") {
		return fmt.Errorf("invalid branch %q: must start with refs/heads/", ref)
	}
	return nil
}
