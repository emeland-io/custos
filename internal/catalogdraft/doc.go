// Package catalogdraft lets a Task-Author draft catalog changes on a branch
// of the catalog repository and publish them into main, with custos-aware
// conflict handling when main has moved on (spec §6.1, architecture note
// Plan 4b). It is the catalog's analogue of internal/merge's workspace fork
// and merge, but for the single catalog repository: drafts are created and
// listed explicitly, not forked implicitly per user.
package catalogdraft

import "errors"

// ErrInvalid reports a malformed request: an empty title, an unknown kind
// of resolution, or a resolution for a path that has no conflict. The REST
// API answers it with 400.
var ErrInvalid = errors.New("invalid request")

const (
	mainRef = "refs/heads/main"
	// BranchPrefix names a draft's branch: custos/draft/<draft-id>.
	BranchPrefix = "custos/draft/"
	// metaPath is a draft branch's own metadata marker (title, author,
	// base), committed at the catalog tree's root. catalog.Load never
	// reads it (only tasks/, groups/ and processors.yaml), so it never
	// affects validation; Publish (Task 5) strips it before anything
	// reaches main.
	metaPath = "custos-draft.json"
	// catalogLock is the Store.Lock key for the catalog repository (see
	// store.go's doc comment on Lock); Delete uses it directly, the same
	// way internal/merge.Fork calls st.Lock(newID) directly for a
	// workspace.
	catalogLock = "catalog"
)
