package distribute

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sync"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/workspace"
)

const (
	mainRef      = "refs/heads/main"
	pinBranch    = "custos/pin/" // + catalog commit
	pinRefPrefix = "refs/heads/" + pinBranch
)

// mu serialises the writing operations of this package. Two reconciles
// started by pushes in quick succession would otherwise race, and the one
// that read the older catalog main could move a pin back or delete the
// newer proposal. Pushes to workspaces are not covered by mu; they meet the
// compare-and-swap of the store instead.
var mu sync.Mutex

// Reconcile brings every workspace in line with the catalog's main: an
// unfrozen workspace whose pin differs gets a commit by gitrepo.Bot that
// moves the pin; a frozen one gets (or keeps) exactly one branch
// custos/pin/<catalog-main>, and older custos/pin/* branches are deleted.
// Workspaces without a main branch are skipped. Idempotent; errors of one
// workspace do not stop the others (returned joined).
func Reconcile(st *store.Store) error {
	mu.Lock()
	defer mu.Unlock()
	head, ok, err := st.CatalogRepo().ResolveRef(mainRef)
	if err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	if !ok {
		return nil // nothing published yet
	}
	ids, err := st.WorkspaceIDs()
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if err := reconcile(st, id, head); err != nil {
			errs = append(errs, fmt.Errorf("workspace %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// reconcile brings one workspace in line with catalog commit head. The
// caller holds mu.
func reconcile(st *store.Store, id, head string) error {
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		return err
	}
	main, ok, err := repo.ResolveRef(mainRef)
	if err != nil {
		return err
	}
	if !ok {
		return nil // still being created, for example by a fork
	}
	cfg, err := readConfig(repo, main)
	if err != nil {
		return err
	}
	switch {
	case cfg.Catalog.Commit == head:
		return prune(st, id, repo, "")
	case !cfg.Frozen:
		if err := movePin(st, id, head); err != nil {
			return err
		}
		return prune(st, id, repo, "")
	default:
		if err := propose(st, id, repo, head); err != nil {
			return err
		}
		return prune(st, id, repo, head)
	}
}

// movePin commits the pin head on main, authored by custos-bot.
func movePin(st *store.Store, id, head string) error {
	_, err := st.UpdateWorkspace(id, mainRef, gitrepo.Bot, "Pin catalog commit "+head, func(tree fs.FS) ([]gitrepo.Change, error) {
		return editConfig(tree, func(c *workspace.Config) {
			if !c.Frozen { // a push may have frozen the workspace since main was read
				c.Catalog.Commit = head
			}
		})
	})
	return err
}

// propose makes sure branch custos/pin/<head> exists, unless the proposal
// for head was rejected. A new branch starts at main with one commit by
// custos-bot that changes the pin. An existing branch is kept as it is,
// even when main has moved on; Accept handles that.
func propose(st *store.Store, id string, repo *gitrepo.Repo, head string) error {
	ref := pinRefPrefix + head
	for _, existing := range []string{ref, rejectedRefPrefix + head} {
		if _, ok, err := repo.ResolveRef(existing); err != nil || ok {
			return err
		}
	}
	_, err := st.UpdateWorkspace(id, ref, gitrepo.Bot, "Propose catalog commit "+head, func(tree fs.FS) ([]gitrepo.Change, error) {
		return editConfig(tree, func(c *workspace.Config) { c.Catalog.Commit = head })
	})
	return err
}

// prune deletes the pin proposal branches and rejection marks other than
// those for catalog commit keep; keep "" deletes them all.
func prune(st *store.Store, id string, repo *gitrepo.Repo, keep string) error {
	unlock := st.Lock(id)
	defer unlock()
	if err := pruneRefs(repo, pinRefPrefix, keep); err != nil {
		return err
	}
	return pruneRefs(repo, rejectedRefPrefix, keep)
}

// pruneRefs deletes the refs below prefix except prefix+keep. The caller
// holds the workspace's lock.
func pruneRefs(repo *gitrepo.Repo, prefix, keep string) error {
	refs, err := repo.Refs(prefix)
	if err != nil {
		return err
	}
	for _, ref := range slices.Sorted(maps.Keys(refs)) {
		if keep != "" && ref == prefix+keep {
			continue
		}
		if err := repo.DeleteRef(ref, refs[ref]); err != nil {
			return conflict(err)
		}
	}
	return nil
}

// conflict reports a ref that moved under us as store.ErrConflict.
func conflict(err error) error {
	if errors.Is(err, gitrepo.ErrRefMoved) {
		return fmt.Errorf("%w: %w", store.ErrConflict, err)
	}
	return err
}
