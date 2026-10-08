package proposal

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/store"
)

// twoTasksAndDoc is the output of the first run in most tests below.
var twoTasksAndDoc = contract.Output{
	Tasks:     []contract.OutputTask{textTask("host:web-01", "Host web-01"), textTask("host:db-01", "Host db-01")},
	Documents: []contract.OutputDocument{doc("sbom", "web-01")},
}

func TestAcceptAll(t *testing.T) {
	st := newStore(t)
	before := mainOID(t, st)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	p, _ := Get(st, ws, fixture.TaskB)
	commit, err := Accept(st, ws, fixture.TaskB, nil, person)
	if err != nil {
		t.Fatal(err)
	}
	if commit != mainOID(t, st) || logOf(t, st, "%P", commit) != before {
		t.Errorf("commit %s, main %s, parent %s", commit, mainOID(t, st), logOf(t, st, "%P", commit))
	}
	if got := logOf(t, st, "%an <%ae>|%cn <%ce>|%s", commit); got != person.String()+"|"+gitrepo.Bot.String()+"|Accept output proposal "+p.Branch {
		t.Errorf("author|committer|subject = %s", got)
	}
	want := []string{
		"answers/" + fixture.TaskB + ".md",
		"documents/" + idOf(t, p.Items, match.KindDocument, "sbom") + ".json",
		"generated/" + idOf(t, p.Items, match.KindTask, "host:db-01") + "/1.0.0.md",
		"generated/" + idOf(t, p.Items, match.KindTask, "host:web-01") + "/1.0.0.md",
	}
	slices.Sort(want)
	if got := mainFiles(t, st); !slices.Equal(got, want) {
		t.Errorf("main holds %v, want %v", got, want)
	}
	if len(branches(t, st)) != 0 {
		t.Errorf("branches %v, want none", branches(t, st))
	}
	if _, err := Accept(st, ws, fixture.TaskB, nil, person); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second accept: %v, want ErrNotFound", err)
	}
}

func TestAcceptSome(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	p, _ := Get(st, ws, fixture.TaskB)
	if _, err := Accept(st, ws, fixture.TaskB, []string{"task:host:web-01"}, person); err != nil {
		t.Fatal(err)
	}
	want := []string{"answers/" + fixture.TaskB + ".md", "generated/" + idOf(t, p.Items, match.KindTask, "host:web-01") + "/1.0.0.md"}
	if got := mainFiles(t, st); !slices.Equal(got, want) {
		t.Errorf("main holds %v, want %v", got, want)
	}
	if len(branches(t, st)) != 0 {
		t.Errorf("the rest of the proposal must be dropped: branches %v", branches(t, st))
	}
}

func TestAcceptRejectsBadSelections(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	before := mainOID(t, st)
	for name, sel := range map[string][]string{
		"unknown key":     {"task:host:web-01", "task:nope"},
		"wrong kind":      {"document:host:web-01"},
		"no kind":         {"host:web-01"},
		"empty selection": {},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Accept(st, ws, fixture.TaskB, sel, person)
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("err %v, want ErrInvalid", err)
			}
		})
	}
	if mainOID(t, st) != before || len(branches(t, st)) != 1 {
		t.Errorf("a rejected selection changed main or the branches")
	}
	if _, err := Accept(st, ws, fixture.TaskA, nil, person); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("no proposal: %v, want ErrNotFound", err)
	}
}

func TestAcceptAfterMainMovedOn(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	answer(t, st, fixture.TaskA, "done") // the engineer keeps working
	moved := mainOID(t, st)
	commit, err := Accept(st, ws, fixture.TaskB, nil, person)
	if err != nil {
		t.Fatal(err)
	}
	if logOf(t, st, "%P", commit) != moved {
		t.Errorf("accept must build on the current main %s", moved)
	}
	got := mainFiles(t, st)
	if !slices.Contains(got, "answers/"+fixture.TaskA+".md") || len(got) != 5 {
		t.Errorf("main holds %v", got)
	}
}

func TestAcceptNewVersionAndRemoval(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	first, _ := Get(st, ws, fixture.TaskB)
	if _, err := Accept(st, ws, fixture.TaskB, nil, person); err != nil {
		t.Fatal(err)
	}
	db := idOf(t, first.Items, match.KindTask, "host:db-01")
	answer(t, st, db, "patched")
	web := textTask("host:web-01", "Host web-01")
	web.Body = "About host:web-01, now with more detail."
	web.Bump = "major"
	propose(t, st, fixture.TaskB, digest2, contract.Output{Tasks: []contract.OutputTask{web}})
	p, err := Get(st, ws, fixture.TaskB)
	if err != nil {
		t.Fatal(err)
	}
	wantSummary(t, p.Items, "document sbom removed →", "task host:db-01 removed 1.0.0→", "task host:web-01 new-version 1.0.0→2.0.0")
	if _, err := Accept(st, ws, fixture.TaskB, nil, person); err != nil {
		t.Fatal(err)
	}
	webID := idOf(t, first.Items, match.KindTask, "host:web-01")
	want := []string{"answers/" + fixture.TaskB + ".md", "generated/" + webID + "/1.0.0.md", "generated/" + webID + "/2.0.0.md"}
	if got := mainFiles(t, st); !slices.Equal(got, want) {
		t.Errorf("main holds %v, want %v (the removed task's answer goes too)", got, want)
	}
	if len(branches(t, st)) != 0 {
		t.Errorf("branches %v: db-01 had nothing below it, so no cascade", branches(t, st))
	}
}

func TestAcceptedRemovalOpensCascade(t *testing.T) {
	st := newStore(t)
	// TaskB's answer makes parent; parent's answer makes child and a
	// document; child's answer makes grandchild.
	propose(t, st, fixture.TaskB, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("parent", "Parent")}})
	p, _ := Get(st, ws, fixture.TaskB)
	parent := idOf(t, p.Items, match.KindTask, "parent")
	mustAccept(t, st, fixture.TaskB)
	answer(t, st, parent, "yes")
	propose(t, st, parent, digest1, contract.Output{
		Tasks:     []contract.OutputTask{textTask("child", "Child")},
		Documents: []contract.OutputDocument{doc("report", "parent")},
	})
	p, _ = Get(st, ws, parent)
	child := idOf(t, p.Items, match.KindTask, "child")
	mustAccept(t, st, parent)
	answer(t, st, child, "yes")
	propose(t, st, child, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("grandchild", "Grandchild")}})
	p, _ = Get(st, ws, child)
	grandchild := idOf(t, p.Items, match.KindTask, "grandchild")
	mustAccept(t, st, child)

	// TaskB's answer no longer yields parent.
	propose(t, st, fixture.TaskB, digest2, contract.Output{})
	mustAccept(t, st, fixture.TaskB)
	if got := branches(t, st); !slices.Equal(got, []string{cascadeBranch(parent)}) {
		t.Fatalf("branches %v, want the cascade of parent", got)
	}
	c, err := Get(st, ws, parent)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Cascade || c.Digest != "" || c.Task != parent {
		t.Errorf("cascade %+v", c)
	}
	if logOf(t, st, "%an <%ae>", c.Commit) != gitrepo.Bot.String() {
		t.Errorf("cascade author %s", logOf(t, st, "%an <%ae>", c.Commit))
	}
	wantSummary(t, c.Items, "document report removed →", "task child removed 1.0.0→", "task grandchild removed 1.0.0→")

	// Accepting only child's removal opens the cascade of child.
	if _, err := Accept(st, ws, parent, []string{"task:child"}, person); err != nil {
		t.Fatal(err)
	}
	if got := branches(t, st); !slices.Equal(got, []string{cascadeBranch(child)}) {
		t.Fatalf("branches %v, want the cascade of child", got)
	}
	mustAccept(t, st, child)
	for _, f := range mainFiles(t, st) {
		if strings.Contains(f, child) || strings.Contains(f, grandchild) || strings.Contains(f, parent) {
			t.Errorf("main still holds %s", f)
		}
	}
	// The report was not selected and stays; nothing is open any more.
	if len(branches(t, st)) != 0 {
		t.Errorf("branches %v", branches(t, st))
	}
}

func TestListRecomputesAgainstMain(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, contract.Output{
		Tasks:     []contract.OutputTask{textTask("keep", "Keep"), textTask("old", "Old")},
		Documents: []contract.OutputDocument{doc("d", "x")},
	})
	if _, err := Accept(st, ws, fixture.TaskB, nil, person); err != nil {
		t.Fatal(err)
	}
	answer(t, st, fixture.TaskA, "done")
	propose(t, st, fixture.TaskA, digest2, contract.Output{Tasks: []contract.OutputTask{textTask("a", "A")}})
	changed := textTask("keep", "Keep")
	changed.Title = "Keep (new title)"
	propose(t, st, fixture.TaskB, digest1, contract.Output{
		Tasks:     []contract.OutputTask{changed, textTask("new", "New")},
		Documents: []contract.OutputDocument{doc("d", "x")},
	})
	ps, err := List(st, ws)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range ps {
		names = append(names, p.Branch)
	}
	want := []string{Branch(fixture.TaskA, digest2), Branch(fixture.TaskB, digest1)}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("proposals %v, want %v", names, want)
	}
	for _, p := range ps {
		if p.Task == fixture.TaskB {
			wantSummary(t, p.Items,
				"document d unchanged →",
				"task keep new-version 1.0.0→1.1.0",
				"task new added →1.0.0",
				"task old removed 1.0.0→",
			)
		}
	}
}

func TestConcurrentAccepts(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	before := mainOID(t, st)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = Accept(st, ws, fixture.TaskB, nil, person)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			t.Errorf("accept: %v", err)
		}
	}
	if n := gitCount(t, st, before+"..main"); n != 1 {
		t.Errorf("%d commits on main, want exactly one", n)
	}
	if len(mainFiles(t, st)) != 4 || len(branches(t, st)) != 0 {
		t.Errorf("main %v, branches %v", mainFiles(t, st), branches(t, st))
	}
}

// TestConcurrentAcceptsWithDifferentSelections guards against the compound
// bug where the proposal branch's deletion happened in a lock span separate
// from (and after) the main-branch commit's: a second Accept, racing in
// between, could recompute its own (different) selection against the branch
// while it still looked open and land a second, independent commit. With
// disjoint selections there is no shared-path collision to hide the bug
// behind, unlike TestConcurrentAccepts above (identical, nil selections),
// which the fix must also keep passing.
func TestConcurrentAcceptsWithDifferentSelections(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	before := mainOID(t, st)
	sels := [][]string{{"task:host:web-01"}, {"task:host:db-01"}}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = Accept(st, ws, fixture.TaskB, sels[i], person)
		}(i)
	}
	wg.Wait()
	var oks, notFound int
	for _, err := range errs {
		switch {
		case err == nil:
			oks++
		case errors.Is(err, store.ErrNotFound):
			notFound++
		default:
			t.Errorf("accept: %v", err)
		}
	}
	if oks != 1 || notFound != 1 {
		t.Errorf("errs %v, want exactly one nil and one ErrNotFound", errs)
	}
	if n := gitCount(t, st, before+"..main"); n != 1 {
		t.Errorf("%d commits on main, want exactly one", n)
	}
	if len(branches(t, st)) != 0 {
		t.Errorf("branches %v, want none", branches(t, st))
	}
}

// TestAcceptRacingReject guards against the other half of the same
// compound bug: Accept and Reject of the same proposal, run concurrently,
// each do their own read-then-act sequence, and without both acting under
// one held lock each could observe the proposal as still theirs to finish
// and report success — Accept landing its commit after Reject already
// deleted the branch it thought it was still closing, or Reject deleting a
// branch Accept had already consumed and was about to (or had just)
// commit(ted) from. Exactly one of the two must win.
func TestAcceptRacingReject(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	before := mainOID(t, st)
	var wg sync.WaitGroup
	var acceptErr, rejectErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, acceptErr = Accept(st, ws, fixture.TaskB, nil, person)
	}()
	go func() {
		defer wg.Done()
		rejectErr = Reject(st, ws, fixture.TaskB)
	}()
	wg.Wait()
	acceptOK, rejectOK := acceptErr == nil, rejectErr == nil
	if acceptOK == rejectOK {
		t.Fatalf("accept err=%v, reject err=%v: exactly one must succeed", acceptErr, rejectErr)
	}
	if acceptErr != nil && !errors.Is(acceptErr, store.ErrNotFound) {
		t.Errorf("accept error %v, want nil or ErrNotFound", acceptErr)
	}
	if rejectErr != nil && !errors.Is(rejectErr, store.ErrNotFound) {
		t.Errorf("reject error %v, want nil or ErrNotFound", rejectErr)
	}
	landed := mainOID(t, st) != before
	if acceptOK != landed {
		t.Errorf("accept ok=%v but landed=%v: accept succeeding must mean, and only mean, it landed", acceptOK, landed)
	}
	if rejectOK && landed {
		t.Errorf("reject succeeded but the proposal landed on main anyway")
	}
	if len(branches(t, st)) != 0 {
		t.Errorf("branches %v, want none: one of accept/reject must have closed the proposal", branches(t, st))
	}
}

func mustAccept(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if _, err := Accept(st, ws, id, nil, person); err != nil {
		t.Fatal(err)
	}
}

// gitCount returns the number of commits in the range rng.
func gitCount(t *testing.T, st *store.Store, rng string) int {
	t.Helper()
	return len(strings.Fields(gittest.Run(t, repoOf(t, st).Dir, "rev-list", rng)))
}
