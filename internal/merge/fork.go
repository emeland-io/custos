package merge

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/rules"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

// forkRef holds the source's main in the new repository until the fork
// commit is ready.
const forkRef = "refs/custos/fork-source"

// forkMu serialises forks, so two forks to the same new id cannot both
// pass the existence check and then remove each other's repository.
var forkMu sync.Mutex

// beforeForkUpdate runs just before forkInto moves the new workspace's
// main. Tests replace it to simulate a push landing first.
var beforeForkUpdate = func() {}

// Fork creates workspace newID from the history of srcID's main branch: a
// new repository (store.CreateWorkspaceRepo) whose main is one commit, by
// author, on top of srcID's main that sets the workspace id in custos.yaml.
// Branches are not copied. All workspaces share one blob store (ruling
// 2.7), so no blobs are copied. The new main is validated like any main
// update; when a step fails, the new repository is removed again.
func Fork(st *store.Store, srcID, newID string, author gitrepo.Signature) error {
	if !task.ValidID(newID) {
		return fmt.Errorf("%w: workspace id %q is not a lowercase UUID v4", ErrInvalid, newID)
	}
	src, err := st.WorkspaceRepo(srcID)
	if err != nil {
		return err
	}
	if _, ok, err := src.ResolveRef(mainRef); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("workspace %s has no main branch: %w", srcID, store.ErrNotFound)
	}

	forkMu.Lock()
	defer forkMu.Unlock()
	if _, err := st.WorkspaceRepo(newID); err == nil {
		return fmt.Errorf("workspace %s: %w", newID, store.ErrExists)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	dst, err := st.CreateWorkspaceRepo(newID)
	if err != nil {
		return err
	}
	// Hold the new workspace's lock until main is set, so that in-process
	// writers (catalog distribution) never see the half-made repository's
	// missing main as their base.
	unlock := st.Lock(newID)
	defer unlock()
	// A conflict on the final UpdateRef means a push already landed on the
	// new workspace's main; removing the repository then would erase that
	// accepted push (store.finishCreateWorkspace avoids the same mistake).
	if err := forkInto(st, src, dst, srcID, newID, author); err != nil {
		if !errors.Is(err, store.ErrConflict) {
			if rmErr := os.RemoveAll(dst.Dir); rmErr != nil {
				return errors.Join(err, rmErr)
			}
		}
		return err
	}
	return nil
}

// forkInto copies src's main into the empty repository dst and sets dst's
// main to a commit that names newID in custos.yaml.
func forkInto(st *store.Store, src, dst *gitrepo.Repo, srcID, newID string, author gitrepo.Signature) error {
	base, err := dst.Fetch(src, mainRef, forkRef)
	if err != nil {
		return err
	}
	data, ok, err := dst.ReadFile(base, configPath)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("workspace %s: custos.yaml is missing on main", srcID)
	}
	cfg, err := decodeConfig(data)
	if err != nil {
		return fmt.Errorf("workspace %s: %w", srcID, err)
	}
	cfg.Workspace = newID
	out, err := encodeConfig(cfg)
	if err != nil {
		return err
	}
	commit, err := dst.WriteCommit(gitrepo.CommitRequest{
		Base:    base,
		Parents: []string{base},
		Changes: []gitrepo.Change{{Path: configPath, Data: out}},
		Author:  author,
		Message: fmt.Sprintf("Fork workspace %s as %s", srcID, newID),
	})
	if err != nil {
		return err
	}
	ps, err := rules.CheckWorkspaceUpdate(dst, st.CatalogRepo(), newID, "", commit)
	if err != nil {
		return err
	}
	if len(ps) > 0 {
		return &store.RejectedError{Problems: ps}
	}
	if err := dst.DeleteRef(forkRef, base); err != nil {
		return err
	}
	beforeForkUpdate()
	if err := dst.UpdateRef(mainRef, commit, ""); err != nil {
		if errors.Is(err, gitrepo.ErrRefMoved) {
			return fmt.Errorf("%w: %v", store.ErrConflict, err)
		}
		return err
	}
	return nil
}
