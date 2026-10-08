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

// Accept applies the selected items ("task:<match_key>" / "document:<match_key>";
// nil = all that are not Unchanged) of the open proposal of taskID to the
// current main in one commit by author (validated, compare-and-swap; on
// store.ErrConflict it re-reads and retries up to three times), deletes the
// proposal branch, and opens a cascade proposal for every accepted removal
// of a generated task that has tasks below it (match.Below) or documents
// made from its answer. An unknown selector or an empty, non-nil selection
// is ErrInvalid (400); no open proposal is store.ErrNotFound (404).
// Selecting an unchanged item is allowed and changes nothing. When main
// already holds everything selected, no commit is made and main's current
// commit is returned.
//
// The commit onto main and the deletion of the proposal branch happen as
// one atomic step, under one held workspace lock (store.UpdateWorkspaceAndThen):
// the edit callback first re-resolves the proposal branch and requires it
// still names the commit read before the lock was taken (gone → ErrNotFound,
// since there is then no open proposal any more; moved to another commit,
// by a concurrent Write replacing it → store.ErrConflict, retried with the
// fresh branch), and only once the main commit has actually landed does the
// then callback delete the branch, still under the same lock. Without this,
// two concurrent Accepts of one proposal (even with disjoint selections), or
// an Accept racing a Reject, could each observe the branch as still open and
// both land their own commit onto main — the lock span that used to cover
// only the main-branch compare-and-swap left a window, after it released but
// before a separate, later-acquired lock span deleted the branch, for a
// second accept to run its own full read-compute-commit sequence against the
// same, now-stale, proposal.
func Accept(st *store.Store, wsID, taskID string, selected []string, author gitrepo.Signature) (commit string, err error) {
	if selected != nil && len(selected) == 0 {
		return "", fmt.Errorf("%w: select at least one item, or reject the proposal", ErrInvalid)
	}
	repo, err := st.WorkspaceRepo(wsID)
	if err != nil {
		return "", err
	}
	var p *Proposal
	var removed []string
	for range maxConflictRetries {
		if p, err = first(repo, taskID); err != nil {
			return "", err
		}
		var tipFS fs.FS
		if tipFS, err = repo.TreeFS(p.Commit); err != nil {
			return "", err
		}
		tip := loadSide(tipFS)
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
			func(repo *gitrepo.Repo, _ string) error {
				return deleteBranch(repo, p)
			})
		if !errors.Is(err, store.ErrConflict) {
			break
		}
	}
	if err != nil {
		return "", err
	}
	if err := openCascades(st, wsID, repo, commit, removed); err != nil {
		return commit, fmt.Errorf("accepted as %s, but a cascade proposal could not be opened: %w", commit, err)
	}
	return commit, nil
}

// checkBranchUnchanged requires that p's branch still names p.Commit, as
// seen under the workspace's lock (the caller runs this from inside
// store.UpdateWorkspaceAndThen's edit callback, which already holds it). A
// branch that is gone means the proposal was already accepted or rejected
// by someone else: store.ErrNotFound, matching what a fresh Get/Accept of
// the same task would now report. A branch that moved to a different commit
// means a concurrent Write replaced the proposal: store.ErrConflict, which
// Accept's retry loop treats as "re-read the proposal and try again."
func checkBranchUnchanged(repo *gitrepo.Repo, p *Proposal) error {
	cur, ok, err := repo.ResolveRef(headsPrefix + p.Branch)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: no open output proposal for task %s", store.ErrNotFound, p.Task)
	}
	if cur != p.Commit {
		return fmt.Errorf("%w: proposal %s moved to %s while being accepted", store.ErrConflict, p.Branch, cur)
	}
	return nil
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

// openCascades opens a cascade proposal, from main at commit, for each
// removed generated task that has generated tasks below it or documents made
// from its answer or theirs.
func openCascades(st *store.Store, wsID string, repo *gitrepo.Repo, commit string, removed []string) error {
	if len(removed) == 0 {
		return nil
	}
	fsys, err := repo.TreeFS(commit)
	if err != nil {
		return err
	}
	main := loadSide(fsys)
	var errs []error
	for _, id := range removed {
		files := cascadeFiles(main, id)
		if len(files) == 0 {
			continue
		}
		msg := "Propose removing what was generated below task " + id
		if _, err := writeBranch(st, wsID, id, cascadeBranch(id), files, msg); err != nil {
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
