package distribute

import (
	"io/fs"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/workspace"
)

// Freeze commits frozen: true on the workspace's main, authored by author.
// From then on catalog changes arrive as pin proposals; if the pin already
// lags behind the catalog, the proposal is opened right away.
func Freeze(st *store.Store, id string, author gitrepo.Signature) error {
	return setFrozen(st, id, author, true, "Freeze the workspace")
}

// Unfreeze commits frozen: false on the workspace's main, authored by
// author, then reconciles the workspace: custos-bot moves the pin to the
// catalog's main and the pin proposals are deleted.
func Unfreeze(st *store.Store, id string, author gitrepo.Signature) error {
	return setFrozen(st, id, author, false, "Unfreeze the workspace")
}

func setFrozen(st *store.Store, id string, author gitrepo.Signature, frozen bool, message string) error {
	mu.Lock()
	defer mu.Unlock()
	_, err := st.UpdateWorkspace(id, mainRef, author, message, func(tree fs.FS) ([]gitrepo.Change, error) {
		return editConfig(tree, func(c *workspace.Config) { c.Frozen = frozen })
	})
	if err != nil {
		return err
	}
	head, ok, err := st.CatalogRepo().ResolveRef(mainRef)
	if err != nil || !ok {
		return err
	}
	return reconcile(st, id, head)
}
