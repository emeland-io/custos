package catalogdraft

import (
	"io/fs"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
)

// Update applies edit to draft id's current tree and commits the result on
// its branch (author becomes the commit's Git author; the committer is
// always gitrepo.Bot, same as every other store write, through
// store.UpdateCatalog). No validation beyond "does this still parse"
// happens anywhere on this path: a draft may be transiently invalid
// (ruling 4.13), so a Task-Author can save half-finished edits. edit gets
// the draft's tree, exactly like store.UpdateWorkspace's edit parameter.
func Update(st *store.Store, id string, author gitrepo.Signature, message string,
	edit func(tree fs.FS) ([]gitrepo.Change, error)) (string, error) {
	d, err := Get(st, id)
	if err != nil {
		return "", err
	}
	return st.UpdateCatalog(d.ref(), author, message, edit)
}
