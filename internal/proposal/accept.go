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
		commit, err = st.UpdateWorkspace(wsID, mainRef, author, "Accept output proposal "+p.Branch,
			func(tree fs.FS) ([]gitrepo.Change, error) {
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
			})
		if !errors.Is(err, store.ErrConflict) {
			break
		}
	}
	if err != nil {
		return "", err
	}
	if err := deleteBranch(st, wsID, repo, p); err != nil {
		return commit, fmt.Errorf("accepted as %s, but the proposal branch was not deleted: %w", commit, err)
	}
	if err := openCascades(st, wsID, repo, commit, removed); err != nil {
		return commit, fmt.Errorf("accepted as %s, but a cascade proposal could not be opened: %w", commit, err)
	}
	return commit, nil
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

// deleteBranch deletes the accepted proposal's branch. When a newer run
// replaced the branch meanwhile, the newer proposal is kept.
func deleteBranch(st *store.Store, wsID string, repo *gitrepo.Repo, p *Proposal) error {
	unlock := st.Lock(wsID)
	defer unlock()
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
