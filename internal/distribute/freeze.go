package distribute

import (
	"fmt"
	"io/fs"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/workspace"
)

// DistributeError reports that a write the caller asked for already
// succeeded, but a follow-up reconcile of the workspace that it triggered
// failed. Cause is the reconcile's error. Callers that only care whether
// their own write landed can treat a *DistributeError as a warning rather
// than a failure; the HTTP handlers do (ruling 2.20: unfreeze must act on
// the flag at once even when the pin move it triggers is rejected).
type DistributeError struct{ Cause error }

func (e *DistributeError) Error() string { return e.Cause.Error() }
func (e *DistributeError) Unwrap() error { return e.Cause }

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

// setFrozen commits the frozen flag, then reconciles the workspace. Once
// the commit has succeeded, a failure of the reconcile that follows it is
// returned wrapped in *DistributeError: the flag is already committed, so
// the caller must not be told the whole operation failed.
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
	if err != nil {
		return &DistributeError{Cause: fmt.Errorf("catalog: %w", err)}
	}
	if !ok {
		return nil // nothing published yet
	}
	if err := reconcile(st, id, head); err != nil {
		return &DistributeError{Cause: err}
	}
	return nil
}
