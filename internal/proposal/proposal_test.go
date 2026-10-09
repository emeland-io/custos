package proposal

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/store"
)

func TestBranch(t *testing.T) {
	want := "custos/proposal/" + fixture.TaskB + "/" + fixture.SHA256
	if got := Branch(fixture.TaskB, digest1); got != want {
		t.Errorf("Branch = %s, want %s", got, want)
	}
}

func TestWriteOpensProposal(t *testing.T) {
	st := newStore(t)
	before := mainOID(t, st)
	branch := propose(t, st, fixture.TaskB, digest1, contract.Output{
		Tasks:     []contract.OutputTask{textTask("host:web-01", "Host web-01")},
		Documents: []contract.OutputDocument{doc("sbom", "web-01")},
	})
	if branch != Branch(fixture.TaskB, digest1) {
		t.Fatalf("branch %q", branch)
	}
	tip := resolve(t, st, headsPrefix+branch)
	if got := logOf(t, st, "%P", tip); got != before {
		t.Errorf("parent %s, want main %s", got, before)
	}
	if got := logOf(t, st, "%an <%ae>|%cn <%ce>|%s", tip); got != gitrepo.Bot.String()+"|"+gitrepo.Bot.String()+"|Propose output of host-scanner for task "+fixture.TaskB {
		t.Errorf("author|committer|subject = %s", got)
	}
	if mainOID(t, st) != before {
		t.Error("Write moved main")
	}
	p, err := Get(st, ws, fixture.TaskB)
	if err != nil {
		t.Fatal(err)
	}
	if p.Branch != branch || p.Commit != tip || p.Task != fixture.TaskB || p.Digest != digest1 || p.Cascade {
		t.Errorf("proposal %+v", p)
	}
	wantSummary(t, p.Items, "document sbom added →", "task host:web-01 added →1.0.0")
	if v := p.Items[0].Verification; v == nil || v.Status != "unsigned" {
		t.Errorf("document verification %+v", v)
	}
}

func TestWriteReplacesProposalsOfTheTask(t *testing.T) {
	st := newStore(t)
	answer(t, st, fixture.TaskA, "done")
	propose(t, st, fixture.TaskA, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("a", "A")}})
	propose(t, st, fixture.TaskB, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("one", "One")}})
	first := resolve(t, st, headsPrefix+Branch(fixture.TaskB, digest1))
	// Same digest again: the branch is replaced, not stacked on.
	propose(t, st, fixture.TaskB, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("two", "Two")}})
	second := resolve(t, st, headsPrefix+Branch(fixture.TaskB, digest1))
	if second == first || logOf(t, st, "%P", second) != mainOID(t, st) {
		t.Errorf("replaced branch %s (was %s) must be one commit on main", second, first)
	}
	// Another digest: the older proposal of the task is deleted.
	propose(t, st, fixture.TaskB, digest2, contract.Output{Tasks: []contract.OutputTask{textTask("three", "Three")}})
	want := []string{Branch(fixture.TaskA, digest1), Branch(fixture.TaskB, digest2)}
	slices.Sort(want)
	if got := branches(t, st); !slices.Equal(got, want) {
		t.Errorf("branches %v, want %v", got, want)
	}
	p, err := Get(st, ws, fixture.TaskB)
	if err != nil {
		t.Fatal(err)
	}
	wantSummary(t, p.Items, "task three added →1.0.0")
}

// TestWriteConcurrentDigestsLeaveOneProposal guards against the race where
// two Write calls for the same task but different digests interleave their
// delete-then-write sequences: each writes to a different branch name, so
// there is no compare-and-swap collision to catch two proposals surviving
// at once unless the whole sequence (delete the task's other proposals,
// then write the new one) is atomic per call.
func TestWriteConcurrentDigestsLeaveOneProposal(t *testing.T) {
	st := newStore(t)
	chA := plan(t, st, fixture.TaskB, contract.Output{Tasks: []contract.OutputTask{textTask("a", "A")}})
	chB := plan(t, st, fixture.TaskB, contract.Output{Tasks: []contract.OutputTask{textTask("b", "B")}})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errs[0] = Write(st, ws, fixture.TaskB, digest1, chA, "concurrent A")
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = Write(st, ws, fixture.TaskB, digest2, chB, "concurrent B")
	}()
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	got := branches(t, st)
	if len(got) != 1 {
		t.Fatalf("branches %v, want exactly one open proposal for the task", got)
	}
	wantA, wantB := Branch(fixture.TaskB, digest1), Branch(fixture.TaskB, digest2)
	if got[0] != wantA && got[0] != wantB {
		t.Errorf("branch %q is neither %q nor %q", got[0], wantA, wantB)
	}
}

func TestWriteWithoutChangesClosesProposals(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("one", "One")}})
	branch, err := Write(st, ws, fixture.TaskB, digest2, &match.Changes{}, "nothing")
	if err != nil || branch != "" {
		t.Fatalf("branch %q, err %v", branch, err)
	}
	if got := branches(t, st); len(got) != 0 {
		t.Errorf("branches %v, want none", got)
	}
	if _, err := Get(st, ws, fixture.TaskB); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get: %v, want ErrNotFound", err)
	}
}

func TestWriteChangesMainAlreadyHasOpenNothing(t *testing.T) {
	st := newStore(t)
	out := contract.Output{Tasks: []contract.OutputTask{textTask("one", "One")}}
	ch := plan(t, st, fixture.TaskB, out)
	// main gets the same files meanwhile (an accepted proposal of an
	// identical run).
	files := map[string]string{}
	for _, c := range ch.Files {
		files[c.Path] = string(c.Data)
	}
	commitMain(t, st, files)
	branch, err := Write(st, ws, fixture.TaskB, digest1, ch, "late")
	if err != nil || branch != "" || len(branches(t, st)) != 0 {
		t.Errorf("branch %q, err %v, branches %v", branch, err, branches(t, st))
	}
}

func TestListIgnoresForeignBranches(t *testing.T) {
	st := newStore(t)
	repo := repoOf(t, st)
	main := mainOID(t, st)
	for _, name := range []string{"custos/proposal/not-a-uuid/" + fixture.SHA256, "custos/proposal/" + fixture.TaskB + "/latest"} {
		if err := repo.UpdateRef(headsPrefix+name, main, ""); err != nil {
			t.Fatal(err)
		}
	}
	ps, err := List(st, ws)
	if err != nil || len(ps) != 0 {
		t.Errorf("proposals %+v, err %v", ps, err)
	}
	if _, err := Get(st, ws, fixture.TaskB); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get: %v, want ErrNotFound", err)
	}
}

func TestListEmptyAndUnknownWorkspace(t *testing.T) {
	st := newStore(t)
	ps, err := List(st, ws)
	if err != nil || ps == nil || len(ps) != 0 {
		t.Errorf("proposals %#v, err %v; want an empty, non-nil list", ps, err)
	}
	if _, err := List(st, "6c7d8e9f-0a1b-4c2d-b3e4-f5a6b7c8d9e0"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown workspace: %v", err)
	}
	if _, err := Get(st, ws, "not-a-task"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("malformed task id: %v", err)
	}
}

func TestReject(t *testing.T) {
	st := newStore(t)
	before := mainOID(t, st)
	propose(t, st, fixture.TaskB, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("one", "One")}})
	if err := Reject(st, ws, fixture.TaskB); err != nil {
		t.Fatal(err)
	}
	if len(branches(t, st)) != 0 || mainOID(t, st) != before {
		t.Errorf("branches %v, main moved %v", branches(t, st), mainOID(t, st) != before)
	}
	if err := Reject(st, ws, fixture.TaskB); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second reject: %v, want ErrNotFound", err)
	}
}
