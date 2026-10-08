package proposal

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/store"
)

// errReplaced marks the store.ErrConflict of an Accept whose proposal was
// replaced (by a Write, or by the cascade opened by an accepted removal of
// its task) while the Accept was in flight. Unlike a compare-and-swap on
// main lost to a push, it is not retried: the caller reviewed and selected
// from the proposal that was replaced, and the new one can hold entirely
// different items.
var errReplaced = errors.New("the proposal was replaced while being accepted")

// beforeAcceptLock, when not nil, is called by Accept after it read the
// proposal and before it first takes the workspace's lock. Tests use it to
// land a concurrent change in exactly that window; it is nil otherwise.
var beforeAcceptLock func()

// Accept applies the selected items ("task:<match_key>" / "document:<match_key>";
// nil = all that are not Unchanged) of the open proposal of taskID to the
// current main in one commit by author (validated, compare-and-swap; when a
// push moves main under it, it retries up to three times), deletes the
// proposal branch, and, for every accepted removal of a generated task,
// closes that task's own open proposals and opens its cascade proposal when
// tasks below it (match.Below) or documents made from its answer or theirs
// remain on main. An unknown selector or an empty, non-nil selection is
// ErrInvalid (400); no open proposal is store.ErrNotFound (404); a proposal
// replaced while this call was in flight is store.ErrConflict (409, not
// retried). Selecting an unchanged item is allowed and changes nothing. When
// main already holds everything selected, no commit is made and main's
// current commit is returned.
//
// When the commit landed but closing the proposal or opening a cascade then
// failed, the commit is returned together with the error, so callers tell
// "landed, with a warning" (commit != "") from "nothing landed" (commit "").
//
// Everything happens under one held workspace lock
// (store.UpdateWorkspaceAndThen): the edit callback requires the proposal
// branch to still name the commit read before the lock was taken and
// recomputes the items against the main it commits on; the then callback,
// still under the same lock and only once the commit has landed, deletes
// the proposal branch and, for every removed task R, deletes everything
// below custos/proposal/<R>/ (R's own open proposal must never land under a
// task that is gone) and writes R's cascade from the tree just committed.
// Nothing else that takes the lock — another Accept, a Reject, a Write — can
// run between the commit and these branch updates, so a concurrent Accept
// of R's own proposal either lands first (and R's cascade then covers what
// it added) or finds its proposal replaced, and no cascade is computed from
// a stale tree.
func Accept(st *store.Store, wsID, taskID string, selected []string, author gitrepo.Signature) (commit string, err error) {
	if selected != nil && len(selected) == 0 {
		return "", fmt.Errorf("%w: select at least one item, or reject the proposal", ErrInvalid)
	}
	repo, err := st.WorkspaceRepo(wsID)
	if err != nil {
		return "", err
	}
	p, err := first(repo, taskID)
	if err != nil {
		return "", err
	}
	tipFS, err := repo.TreeFS(p.Commit)
	if err != nil {
		return "", err
	}
	tip := loadSide(tipFS)
	if beforeAcceptLock != nil {
		beforeAcceptLock()
	}
	var removed []string
	landed := false
	for range maxConflictRetries {
		commit, err = st.UpdateWorkspaceAndThen(wsID, mainRef, author, "Accept output proposal "+p.Branch,
			func(tree fs.FS) ([]gitrepo.Change, error) {
				if err := checkBranchUnchanged(repo, p); err != nil {
					return nil, err
				}
				es, err := diff(loadSide(tree), tip, p.Task, p.Cascade)
				if err != nil {
					return nil, err
				}
				chosen, err := choose(es, selected)
				if err != nil {
					return nil, err
				}
				removed = removed[:0]
				var changes []gitrepo.Change
				for _, e := range chosen {
					changes = append(changes, e.changes...)
					if e.item.Kind == match.KindTask && e.item.Action == match.Removed {
						removed = append(removed, e.item.ID)
					}
				}
				return changes, nil
			},
			func(repo *gitrepo.Repo, oid string) error {
				landed = true
				return errors.Join(deleteBranch(repo, p), openCascades(repo, oid, removed))
			})
		// Retry only a compare-and-swap on main lost to a push: never once
		// the commit landed, and never for a replaced proposal.
		if landed || !errors.Is(err, store.ErrConflict) || errors.Is(err, errReplaced) {
			break
		}
	}
	if err != nil {
		if landed {
			return commit, fmt.Errorf("accepted as %s, but closing the proposal or opening a cascade failed: %w", commit, err)
		}
		return "", err
	}
	return commit, nil
}

// checkBranchUnchanged requires that p's branch still names p.Commit, as
// seen under the workspace's lock (the caller runs this from inside
// store.UpdateWorkspaceAndThen's edit callback, which already holds it).
// Otherwise: when the task has no open proposal any more, it was accepted or
// rejected by someone else — store.ErrNotFound, as a fresh Get of the task
// would now report; when the branch names another commit, or another
// proposal of the task took its place (a Write with another digest, or the
// cascade opened by an accepted removal of the task), the proposal was
// replaced — store.ErrConflict marked errReplaced, which Accept returns to
// its caller instead of retrying.
func checkBranchUnchanged(repo *gitrepo.Repo, p *Proposal) error {
	cur, ok, err := repo.ResolveRef(headsPrefix + p.Branch)
	if err != nil {
		return err
	}
	if ok && cur == p.Commit {
		return nil
	}
	now, err := first(repo, p.Task)
	if err != nil {
		return err // store.ErrNotFound when the task has no open proposal
	}
	return fmt.Errorf("%w: %w: %s is now %s at %s", store.ErrConflict, errReplaced, p.Branch, now.Branch, now.Commit)
}

// choose returns the entries selected (nil = all that are not unchanged).
func choose(es []entry, selected []string) ([]entry, error) {
	if selected == nil {
		var out []entry
		for _, e := range es {
			if e.item.Action != match.Unchanged {
				out = append(out, e)
			}
		}
		return out, nil
	}
	known := map[string]bool{}
	for _, e := range es {
		known[selector(e.item)] = true
	}
	for _, s := range selected {
		if !known[s] {
			return nil, fmt.Errorf("%w: the proposal has no item %q (use task:<match_key> or document:<match_key>)", ErrInvalid, s)
		}
	}
	var out []entry
	for _, e := range es {
		if slices.Contains(selected, selector(e.item)) {
			out = append(out, e)
		}
	}
	return out, nil
}

// deleteBranch deletes the accepted proposal's branch. It runs as
// store.UpdateWorkspaceAndThen's then callback (see Accept), already under
// the workspace's lock, so it must not (and does not) take the lock itself.
// A branch already gone — deleted by this very call on a successful CAS, or,
// in principle, moved under us, which checkBranchUnchanged already ruled
// out earlier in the very same lock span — is not an error.
func deleteBranch(repo *gitrepo.Repo, p *Proposal) error {
	err := repo.DeleteRef(headsPrefix+p.Branch, p.Commit)
	if errors.Is(err, gitrepo.ErrRefMoved) {
		return nil
	}
	return err
}

// openCascades handles every removed generated task id as part of the
// accept that removed it: it runs as store.UpdateWorkspaceAndThen's then
// callback, under the lock already held, with oid the commit main now names.
// For each id it deletes every open proposal of id (the task is gone, so
// none may land any more) and then writes id's cascade, computed from the
// tree of oid, when tasks below id or documents made from its answer or
// theirs remain there. A failure for one id does not stop the others.
func openCascades(repo *gitrepo.Repo, oid string, removed []string) error {
	if len(removed) == 0 {
		return nil
	}
	tree, err := repo.TreeFS(oid)
	if err != nil {
		return err
	}
	main := loadSide(tree)
	var errs []error
	for _, id := range removed {
		msg := "Propose removing what was generated below task " + id
		if _, err := replaceProposals(repo, id, cascadeBranch(id), oid, tree, cascadeFiles(main, id), msg); err != nil {
			errs = append(errs, fmt.Errorf("task %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// cascadeFiles returns the deletions a cascade of removed task id proposes
// on main: every version file and answer of the generated tasks below id,
// and the documents made from the answers of id or of those tasks.
func cascadeFiles(main side, id string) []gitrepo.Change {
	tasks, docs := scope(main.ws, id, true)
	var files []gitrepo.Change
	for _, t := range tasks {
		for _, v := range main.ws.Graph.Versions(t) {
			files = append(files, gitrepo.Change{Path: v.Path, Delete: true})
		}
		if answer := "answers/" + t + ".md"; main.ws.Answers[answer] != nil {
			files = append(files, gitrepo.Change{Path: answer, Delete: true})
		}
	}
	for _, d := range docs {
		files = append(files, gitrepo.Change{Path: d, Delete: true})
	}
	return files
}
