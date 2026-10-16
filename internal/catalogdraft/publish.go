package catalogdraft

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/merge"
	"github.com/emeland-io/custos/internal/store"
)

// Kinds of a merge.Conflict's Kind field for a catalog publish. Distinct
// from internal/merge's own KindAnswer, KindGenerated etc., which describe
// workspace paths; merge.Conflict.Kind is a plain string, so catalogdraft
// fills it in with these instead.
const (
	kindTask       = "task"
	kindGroup      = "group"
	kindProcessors = "processors"
	kindOther      = "other"
)

// PublishResult is the outcome of Publish.
type PublishResult struct {
	Commit    string
	Conflicts []merge.Conflict // non-empty → nothing changed, same contract as internal/merge.Merge
}

// conflictsErr carries unresolved conflicts out of the edit closure
// store.UpdateCatalog runs, so Publish can turn them into a PublishResult
// instead of a generic error. It never reaches a caller outside this file.
type conflictsErr struct{ conflicts []merge.Conflict }

func (e *conflictsErr) Error() string {
	return fmt.Sprintf("publishing needs a resolution for %d conflicting path(s)", len(e.conflicts))
}

// Publish merges draft id into the catalog's main: a plain fast-forward
// (main's tree becomes the draft's tree) when main has not moved past the
// draft's Base, otherwise a three-way merge computed the same way
// internal/merge.Merge computes a workspace merge (gitrepo.MergeTree, then
// each conflicting path settled by res or left open), over
// tasks/<uuid>/<semver>.md, groups/** and processors.yaml — every path a
// catalog tree holds other than the draft's own metadata marker, which this
// function always strips before anything reaches main. A clean result is
// validated by store.UpdateCatalog's own main-ref check (rules
// .CheckCatalogUpdate) before the compare-and-swap; a validation problem
// comes back as *store.RejectedError, exactly like a rejected push, and the
// draft branch is left untouched so the author can keep editing. Publish
// deletes the draft branch on success.
//
// Both the fast-forward decision and the merge itself are recomputed fresh
// every time the edit closure below actually runs (which may be more than
// once if store.UpdateCatalog's own compare-and-swap loses a race to a
// concurrent git push — it does not retry on its own, callers do), so a
// stale read from before the lock was taken can never cause the wrong
// content to be published; see the task's own notes above this code block.
func Publish(st *store.Store, id string, author gitrepo.Signature, res map[string]merge.Resolution) (*PublishResult, error) {
	d, err := Get(st, id)
	if err != nil {
		return nil, err
	}
	message := fmt.Sprintf("Publish catalog draft %q", d.Title)
	// allowedPaths names exactly the paths the caller explicitly resolved
	// a conflict for (res's own keys): see store.UpdateCatalog's
	// allowImmutableChange doc comment for why this, and only this, is
	// exempt from the immutability check that otherwise applies to every
	// publish (found while applying this plan to a scratch copy, Part 2
	// of cross-plan review: TestPublishConflict's own "resolved with
	// theirs" step cannot succeed without it).
	//
	// This is only safe because every path in res is guaranteed, by the
	// time this value is actually used below, to have been a genuine
	// gitrepo.MergeTree conflict: the fast-forward branch inside the edit
	// closure rejects any call with a non-empty res outright (nothing to
	// resolve on that path), and planPublish itself rejects any res key
	// that doesn't match a real mt.Conflicts entry. So allowedPaths can
	// never smuggle an immutability exemption onto a path that did not
	// actually conflict (code review finding on this task's first
	// version, closed by the fast-forward check below).
	allowedPaths := make([]string, 0, len(res))
	for path := range res {
		allowedPaths = append(allowedPaths, path)
	}
	commit, err := st.UpdateCatalog(mainRef, author, message, func(tree fs.FS) ([]gitrepo.Change, error) {
		cat := st.CatalogRepo()
		ours, ok, err := cat.ResolveRef(mainRef)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("the catalog has no main branch yet: %w", store.ErrConflict)
		}
		draftTip, ok, err := cat.ResolveRef(d.ref())
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: draft %s", store.ErrNotFound, id)
		}
		if merged, err := cat.IsAncestor(draftTip, ours); err != nil {
			return nil, err
		} else if merged {
			return nil, nil // already published; no changes to make
		}

		var desired fs.FS
		if ours == d.Base {
			// A fast-forward has no conflicts to resolve, so a non-empty
			// res here is always wrong — either stale (left over from a
			// previous, now-superseded conflict) or an attempt to smuggle
			// an allowImmutableChange exemption (see allowedPaths below)
			// onto a path nothing actually conflicted on. Rejecting this
			// outright, before diffFS ever runs, is what keeps
			// allowedPaths safe to compute from res's raw keys below:
			// every path it can ever name was a genuine merge conflict,
			// because the only way past this check is to have no res at
			// all. (Found by code review: without this, a caller could
			// rewrite an already-published task version in a draft that
			// is still a clean fast-forward and pass res naming that
			// path to silently defeat CheckImmutable, even though
			// nothing conflicted.)
			if len(res) > 0 {
				return nil, fmt.Errorf("%w: draft %s is a clean fast-forward; res must be empty (there is nothing to resolve)", ErrInvalid, id)
			}
			if desired, err = cat.TreeFS(draftTip); err != nil {
				return nil, err
			}
		} else {
			// cat.MergeTree, unlike internal/merge.Merge's use of it, never
			// needs its own MergeBase/"shares no history" check first:
			// every draft branch is created by Create from the catalog's
			// own main (see draft.go), so ours and draftTip always share
			// history, unlike internal/merge.Merge's arbitrary-branch case.
			mt, err := cat.MergeTree(ours, draftTip)
			if err != nil {
				return nil, err
			}
			changes, conflicts, err := planPublish(mt, res)
			if err != nil {
				return nil, err
			}
			if len(conflicts) > 0 {
				return nil, &conflictsErr{conflicts: conflicts}
			}
			mergedTree, err := cat.EditTree(mt.Tree, changes)
			if err != nil {
				return nil, err
			}
			if desired, err = cat.TreeFS(mergedTree); err != nil {
				return nil, err
			}
		}
		return diffFS(tree, desired)
	}, allowedPaths...)

	var ce *conflictsErr
	if errors.As(err, &ce) {
		return &PublishResult{Conflicts: ce.conflicts}, nil
	}
	if err != nil {
		return nil, err
	}
	// The draft's content has landed on main; discard the branch. A
	// failure here (only a lost compare-and-swap against a concurrent
	// Update of the same draft) is returned as-is: the publish itself
	// already succeeded, and a retried Publish converges safely, because
	// the "already published" check above sees the old content as an
	// ancestor of the new main.
	if err := Delete(st, id, author); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return &PublishResult{Commit: commit}, nil
}

// planPublish settles every conflict mt reports by its resolution in res,
// or records it as still open. It always appends a deletion of metaPath, so
// the draft's metadata marker never survives into the merged tree (mt.Tree
// carries it as a clean, non-conflicting add from the draft's side, since
// main never has it).
func planPublish(mt *gitrepo.MergeTreeResult, res map[string]merge.Resolution) ([]gitrepo.Change, []merge.Conflict, error) {
	var changes []gitrepo.Change
	var conflicts []merge.Conflict
	used := map[string]bool{}
	for _, c := range mt.Conflicts {
		r, ok := res[c.Path]
		if !ok {
			conflicts = append(conflicts, merge.Conflict{Path: c.Path, Ours: c.Ours, Theirs: c.Theirs, Kind: catalogKind(c.Path)})
			continue
		}
		used[c.Path] = true
		switch r.Side {
		case merge.SideOurs, merge.SideTheirs:
			if r.Content != nil {
				return nil, nil, fmt.Errorf("%w: %s: content is only allowed with side %q", ErrInvalid, c.Path, merge.SideContent)
			}
			data := c.Ours
			if r.Side == merge.SideTheirs {
				data = c.Theirs
			}
			if data == nil {
				changes = append(changes, gitrepo.Change{Path: c.Path, Delete: true})
			} else {
				changes = append(changes, gitrepo.Change{Path: c.Path, Data: data})
			}
		case merge.SideContent:
			if r.Content == nil {
				return nil, nil, fmt.Errorf("%w: %s: side %q needs content", ErrInvalid, c.Path, merge.SideContent)
			}
			changes = append(changes, gitrepo.Change{Path: c.Path, Data: r.Content})
		default:
			return nil, nil, fmt.Errorf("%w: %s: side %q is not %s, %s or %s", ErrInvalid, c.Path, r.Side, merge.SideOurs, merge.SideTheirs, merge.SideContent)
		}
	}
	for path := range res {
		if !used[path] {
			return nil, nil, fmt.Errorf("%w: %s has no conflict to resolve", ErrInvalid, path)
		}
	}
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Path < conflicts[j].Path })
	changes = append(changes, gitrepo.Change{Path: metaPath, Delete: true})
	return changes, conflicts, nil
}

// catalogKind classifies a catalog path for merge.Conflict.Kind.
func catalogKind(path string) string {
	switch {
	case strings.HasPrefix(path, "tasks/"):
		return kindTask
	case strings.HasPrefix(path, "groups/"):
		return kindGroup
	case path == "processors.yaml":
		return kindProcessors
	}
	return kindOther
}

// diffFS returns the gitrepo.Change entries that turn old into newTree, for
// store.UpdateCatalog's edit closure: one entry per path added or changed
// in newTree, one Delete entry per path newTree no longer has. metaPath is
// always skipped on both sides: it exists only on draft branches, never on
// main.
func diffFS(old, newTree fs.FS) ([]gitrepo.Change, error) {
	var changes []gitrepo.Change
	err := fs.WalkDir(newTree, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path == metaPath {
			return err
		}
		data, err := fs.ReadFile(newTree, path)
		if err != nil {
			return err
		}
		if prev, err := fs.ReadFile(old, path); err == nil && bytes.Equal(prev, data) {
			return nil
		}
		changes = append(changes, gitrepo.Change{Path: path, Data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = fs.WalkDir(old, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path == metaPath {
			return err
		}
		if _, statErr := fs.Stat(newTree, path); errors.Is(statErr, fs.ErrNotExist) {
			changes = append(changes, gitrepo.Change{Path: path, Delete: true})
		}
		return nil
	})
	return changes, err
}
