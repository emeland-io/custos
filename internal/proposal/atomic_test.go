package proposal

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/store"
)

// generatedParent makes TaskB's answer produce parent (accepted) and answers
// parent. With child set, parent's answer also produces child (accepted).
func generatedParent(t *testing.T, st *store.Store, child bool) (parentID, childID string) {
	t.Helper()
	propose(t, st, fixture.TaskB, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("parent", "Parent")}})
	p, _ := Get(st, ws, fixture.TaskB)
	parentID = idOf(t, p.Items, match.KindTask, "parent")
	mustAccept(t, st, fixture.TaskB)
	answer(t, st, parentID, "yes")
	if child {
		propose(t, st, parentID, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("child", "Child")}})
		p, _ = Get(st, ws, parentID)
		childID = idOf(t, p.Items, match.KindTask, "child")
		mustAccept(t, st, parentID)
	}
	return parentID, childID
}

// TestAcceptedRemovalClosesTheRemovedTasksProposal: parent has nothing
// below it on main yet, so its removal opens no cascade — but parent's own
// open output proposal must be closed by the same accept, or accepting it
// later would land tasks below a task that is gone.
func TestAcceptedRemovalClosesTheRemovedTasksProposal(t *testing.T) {
	st := newStore(t)
	parent, _ := generatedParent(t, st, false)
	propose(t, st, parent, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("child", "Child")}})
	propose(t, st, fixture.TaskB, digest2, contract.Output{})
	mustAccept(t, st, fixture.TaskB)
	if got := branches(t, st); len(got) != 0 {
		t.Errorf("branches %v, want none: the removed task's own proposal must be closed", got)
	}
	if _, err := Accept(st, ws, parent, nil, person); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("accepting the removed task's proposal: %v, want ErrNotFound", err)
	}
	if got := mainFiles(t, st); !slices.Equal(got, []string{"answers/" + fixture.TaskB + ".md"}) {
		t.Errorf("main holds %v", got)
	}
}

// TestRemovalRacingAcceptOfTheRemovedTasksProposal: one Accept removes
// parent while another accepts parent's own proposal, which adds child2
// below it. Whatever order they run in, nothing may be left on main below a
// task that is gone without an open cascade proposing its removal: the
// cascade must be opened in the same lock span as the removal, from the tree
// it committed, not later from a snapshot that misses child2.
func TestRemovalRacingAcceptOfTheRemovedTasksProposal(t *testing.T) {
	for range 5 {
		st := newStore(t)
		parent, _ := generatedParent(t, st, true)
		propose(t, st, parent, digest2, contract.Output{Tasks: []contract.OutputTask{textTask("child", "Child"), textTask("child2", "Child 2")}})
		propose(t, st, fixture.TaskB, digest2, contract.Output{})
		var wg sync.WaitGroup
		var removeErr, addErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, removeErr = Accept(st, ws, fixture.TaskB, nil, person)
		}()
		go func() {
			defer wg.Done()
			_, addErr = Accept(st, ws, parent, []string{"task:child2"}, person)
		}()
		wg.Wait()
		if removeErr != nil {
			t.Fatalf("removing parent: %v", removeErr)
		}
		// After the removal, parent's proposal is gone (ErrNotFound) or was
		// replaced by its cascade (ErrConflict, or ErrInvalid when the
		// cascade was read: it has no item task:child2).
		if addErr != nil && !errors.Is(addErr, store.ErrNotFound) && !errors.Is(addErr, store.ErrConflict) && !errors.Is(addErr, ErrInvalid) {
			t.Fatalf("accepting parent's proposal: %v", addErr)
		}
		ps, err := List(st, ws)
		if err != nil {
			t.Fatal(err)
		}
		proposed := map[string]bool{}
		for _, p := range ps {
			if !p.Cascade {
				t.Errorf("open proposal %s of a removed task", p.Branch)
			}
			for _, it := range p.Items {
				if it.Action == match.Removed {
					proposed[it.ID] = true
				}
			}
		}
		for _, id := range orphansOnMain(t, st) {
			if !proposed[id] {
				t.Errorf("%s is left below a removed task without a cascade proposing its removal (open: %v)", id, branches(t, st))
			}
		}
		if t.Failed() {
			return
		}
	}
}

// orphansOnMain returns the ids of the generated tasks and documents on
// main whose producer is neither a catalog task nor a generated task on main.
func orphansOnMain(t *testing.T, st *store.Store) []string {
	t.Helper()
	w, c, err := st.Load(ws, "")
	if err != nil {
		t.Fatal(err)
	}
	live := func(id string) bool { return c.Tasks.Has(id) || w.Graph.Has(id) }
	var out []string
	for _, id := range w.Graph.Tasks() {
		if g := match.Current(w, id); g != nil && !live(g.ProducedBy.Task.ID) {
			out = append(out, id)
		}
	}
	for path, d := range w.Documents {
		if !live(d.ProducedBy.Task.ID) {
			out = append(out, documentID(path))
		}
	}
	return out
}

// TestWriteForRemovedTaskLeavesItsCascade: a run on parent that was in
// flight when parent's removal was accepted finishes afterwards. Its Write
// must neither drop nor replace parent's cascade.
func TestWriteForRemovedTaskLeavesItsCascade(t *testing.T) {
	st := newStore(t)
	parent, _ := generatedParent(t, st, true)
	late := plan(t, st, parent, contract.Output{Tasks: []contract.OutputTask{textTask("child", "Child"), textTask("late", "Late")}})
	propose(t, st, fixture.TaskB, digest2, contract.Output{})
	mustAccept(t, st, fixture.TaskB)
	cascade := resolve(t, st, headsPrefix+cascadeBranch(parent))
	if cascade == "" {
		t.Fatalf("branches %v, want the cascade of parent", branches(t, st))
	}
	for name, ch := range map[string]*match.Changes{"empty output": {}, "new output": late} {
		branch, err := Write(st, ws, parent, digest1, ch, "late run")
		if !errors.Is(err, store.ErrNotFound) || branch != "" {
			t.Errorf("%s: branch %q, err %v; want ErrNotFound", name, branch, err)
		}
		if got := branches(t, st); !slices.Equal(got, []string{cascadeBranch(parent)}) || resolve(t, st, headsPrefix+cascadeBranch(parent)) != cascade {
			t.Errorf("%s: branches %v, want the untouched cascade of parent", name, got)
		}
	}
}

// TestAcceptOfReplacedProposalIsConflict: the proposal is replaced while an
// Accept of it is in flight — by another digest (another branch) or the same
// digest (the branch moved). Accept must report store.ErrConflict instead of
// applying the replacement under the selection made for the old one.
func TestAcceptOfReplacedProposalIsConflict(t *testing.T) {
	for name, digest := range map[string]string{"other digest": digest2, "same digest": digest1} {
		t.Run(name, func(t *testing.T) {
			st := newStore(t)
			propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
			before := mainOID(t, st)
			beforeAcceptLock = func() {
				propose(t, st, fixture.TaskB, digest, contract.Output{Tasks: []contract.OutputTask{textTask("host:web-01", "Host web-01"), textTask("other", "Other")}})
			}
			defer func() { beforeAcceptLock = nil }()
			commit, err := Accept(st, ws, fixture.TaskB, []string{"task:host:web-01"}, person)
			if !errors.Is(err, store.ErrConflict) || commit != "" {
				t.Errorf("commit %q, err %v; want ErrConflict", commit, err)
			}
			if mainOID(t, st) != before {
				t.Errorf("main moved: the replacement was applied")
			}
			if got := branches(t, st); !slices.Equal(got, []string{Branch(fixture.TaskB, digest)}) {
				t.Errorf("branches %v, want the replacement still open", got)
			}
		})
	}
}

// TestAcceptBuildsOnMainMovedWhileInFlight: main moves (the proposal does
// not) while an Accept is in flight; the Accept builds on the new main.
func TestAcceptBuildsOnMainMovedWhileInFlight(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	var moved string
	beforeAcceptLock = func() {
		answer(t, st, fixture.TaskA, "done")
		moved = mainOID(t, st)
	}
	defer func() { beforeAcceptLock = nil }()
	commit, err := Accept(st, ws, fixture.TaskB, nil, person)
	if err != nil {
		t.Fatal(err)
	}
	if logOf(t, st, "%P", commit) != moved {
		t.Errorf("accept must build on the current main %s", moved)
	}
	if got := mainFiles(t, st); !slices.Contains(got, "answers/"+fixture.TaskA+".md") || len(got) != 5 {
		t.Errorf("main holds %v", got)
	}
}

// lockRef leaves a stale lock file for ref (a short branch name) in the
// workspace repository, so that git cannot update or delete it.
func lockRef(t *testing.T, st *store.Store, branch string) {
	t.Helper()
	path := filepath.Join(repoOf(t, st).Dir, "refs", "heads", filepath.FromSlash(branch)+".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAcceptKeepsCommitWhenClosingFails: the commit lands, but deleting the
// accepted proposal's branch fails. Accept returns the landed commit
// together with the error instead of reporting the accept as not made.
func TestAcceptKeepsCommitWhenClosingFails(t *testing.T) {
	st := newStore(t)
	branch := propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	before := mainOID(t, st)
	lockRef(t, st, branch)
	commit, err := Accept(st, ws, fixture.TaskB, nil, person)
	if err == nil || commit == "" || commit != mainOID(t, st) || commit == before {
		t.Fatalf("commit %q (main %s, before %s), err %v; want the landed commit with an error", commit, mainOID(t, st), before, err)
	}
	if len(mainFiles(t, st)) != 4 {
		t.Errorf("main holds %v", mainFiles(t, st))
	}
}

// TestAcceptKeepsCommitWhenCascadeFails: the removal lands, but parent's
// cascade cannot be written. Accept returns the landed commit together with
// the error.
func TestAcceptKeepsCommitWhenCascadeFails(t *testing.T) {
	st := newStore(t)
	parent, _ := generatedParent(t, st, true)
	propose(t, st, fixture.TaskB, digest2, contract.Output{})
	lockRef(t, st, cascadeBranch(parent))
	commit, err := Accept(st, ws, fixture.TaskB, nil, person)
	if err == nil || commit == "" || commit != mainOID(t, st) {
		t.Fatalf("commit %q (main %s), err %v; want the landed commit with an error", commit, mainOID(t, st), err)
	}
	for _, f := range mainFiles(t, st) {
		if strings.Contains(f, "generated/"+parent+"/") {
			t.Errorf("main still holds %s: the removal did not land", f)
		}
	}
	if got := branches(t, st); len(got) != 0 {
		t.Errorf("branches %v, want none (TaskB's proposal closed, the cascade failed)", got)
	}
}
