package distribute

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/store"
)

func TestReconcileMovesPinOfUnfrozenWorkspace(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	repo := wsRepo(t, st, wsA)
	before := configAt(t, repo, "refs/heads/main")
	c2 := newTaskAVersion(t, st, "1.2.0", "1.1.0")

	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	after := configAt(t, repo, "refs/heads/main")
	if after.Catalog.Commit != c2 {
		t.Errorf("pin %s, want %s", after.Catalog.Commit, c2)
	}
	if after.Workspace != before.Workspace || after.Catalog.URL != before.Catalog.URL || after.Frozen {
		t.Errorf("other fields changed: before %+v, after %+v", before, after)
	}
	if got := authorOf(t, repo, "refs/heads/main"); got != gitrepo.Bot.String() {
		t.Errorf("author %q, want %q", got, gitrepo.Bot.String())
	}
	if b := pinBranches(t, repo); len(b) != 0 {
		t.Errorf("unfrozen workspace got proposals %v", b)
	}
}

func TestReconcileProposesForFrozenWorkspace(t *testing.T) {
	st := newStore(t)
	c1 := commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	markFrozen(t, st, wsA)
	repo := wsRepo(t, st, wsA)
	main := resolve(t, repo, "refs/heads/main")
	c2 := newTaskAVersion(t, st, "1.2.0", "1.1.0")

	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	if got := resolve(t, repo, "refs/heads/main"); got != main {
		t.Errorf("main of a frozen workspace moved from %s to %s", main, got)
	}
	if c := configAt(t, repo, "refs/heads/main"); c.Catalog.Commit != c1 {
		t.Errorf("pin on main %s, want %s", c.Catalog.Commit, c1)
	}
	branch := "custos/pin/" + c2
	if b := pinBranches(t, repo); !slices.Equal(b, []string{branch}) {
		t.Fatalf("branches %v, want [%s]", b, branch)
	}
	c := configAt(t, repo, "refs/heads/"+branch)
	if c.Catalog.Commit != c2 || !c.Frozen {
		t.Errorf("proposal config %+v", c)
	}
	if got := firstParent(t, repo, "refs/heads/"+branch); got != main {
		t.Errorf("proposal parent %s, want main %s", got, main)
	}
	if got := authorOf(t, repo, "refs/heads/"+branch); got != gitrepo.Bot.String() {
		t.Errorf("proposal author %q", got)
	}
}

func TestNewerCatalogCommitReplacesProposal(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	markFrozen(t, st, wsA)
	repo := wsRepo(t, st, wsA)
	newTaskAVersion(t, st, "1.2.0", "1.1.0")
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	c3 := newTaskAVersion(t, st, "1.3.0", "1.2.0")
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	if b := pinBranches(t, repo); !slices.Equal(b, []string{"custos/pin/" + c3}) {
		t.Fatalf("branches %v, want only the proposal for %s", b, c3)
	}
	if c := configAt(t, repo, "refs/heads/custos/pin/"+c3); c.Catalog.Commit != c3 {
		t.Errorf("proposal pins %s", c.Catalog.Commit)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	createWorkspace(t, st, wsB)
	markFrozen(t, st, wsB)
	newTaskAVersion(t, st, "1.2.0", "1.1.0")
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	a, b := wsRepo(t, st, wsA), wsRepo(t, st, wsB)
	refsA, refsB := allRefs(t, a), allRefs(t, b)
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	if got := allRefs(t, a); !maps.Equal(got, refsA) {
		t.Errorf("unfrozen workspace changed: %v → %v", refsA, got)
	}
	if got := allRefs(t, b); !maps.Equal(got, refsB) {
		t.Errorf("frozen workspace changed: %v → %v", refsB, got)
	}
}

func TestReconcileContinuesAfterFailingWorkspace(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	createWorkspace(t, st, wsB)
	breakConfig(t, st, wsA) // wsA sorts first, so its failure comes before wsB
	c2 := newTaskAVersion(t, st, "1.2.0", "1.1.0")

	err := Reconcile(st)
	if err == nil || !strings.Contains(err.Error(), "workspace "+wsA) {
		t.Fatalf("err %v, want an error naming %s", err, wsA)
	}
	if strings.Contains(err.Error(), wsB) {
		t.Errorf("err names the healthy workspace: %v", err)
	}
	if c := configAt(t, wsRepo(t, st, wsB), "refs/heads/main"); c.Catalog.Commit != c2 {
		t.Errorf("healthy workspace pins %s, want %s", c.Catalog.Commit, c2)
	}
}

func TestReconcileSkipsEmptyRepositories(t *testing.T) {
	st := newStore(t)
	if err := Reconcile(st); err != nil {
		t.Fatalf("empty catalog: %v", err)
	}
	if _, err := st.CreateWorkspaceRepo(wsB); err != nil { // no main yet, as during a fork
		t.Fatal(err)
	}
	commitCatalog(t, st, fixture.Catalog())
	if err := Reconcile(st); err != nil {
		t.Fatalf("workspace without main: %v", err)
	}
}

func TestConcurrentReconciles(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	createWorkspace(t, st, wsB)
	markFrozen(t, st, wsB)
	newTaskAVersion(t, st, "1.2.0", "1.1.0")
	c3 := newTaskAVersion(t, st, "1.3.0", "1.2.0")

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() { errs <- Reconcile(st) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if c := configAt(t, wsRepo(t, st, wsA), "refs/heads/main"); c.Catalog.Commit != c3 {
		t.Errorf("pin %s, want %s", c.Catalog.Commit, c3)
	}
	if b := pinBranches(t, wsRepo(t, st, wsB)); !slices.Equal(b, []string{"custos/pin/" + c3}) {
		t.Errorf("branches %v", b)
	}
}

// TestReconcileWorkspaceMovesOnlyOneWorkspace checks that ReconcileWorkspace
// brings one workspace in line with the catalog's main without touching the
// others, so a workspace push can reconcile itself without waiting for the
// next catalog push.
func TestReconcileWorkspaceMovesOnlyOneWorkspace(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	createWorkspace(t, st, wsB)
	c2 := newTaskAVersion(t, st, "1.2.0", "1.1.0")

	if err := ReconcileWorkspace(st, wsA); err != nil {
		t.Fatal(err)
	}
	if c := configAt(t, wsRepo(t, st, wsA), "refs/heads/main"); c.Catalog.Commit != c2 {
		t.Errorf("wsA pin %s, want %s", c.Catalog.Commit, c2)
	}
	if c := configAt(t, wsRepo(t, st, wsB), "refs/heads/main"); c.Catalog.Commit == c2 {
		t.Errorf("ReconcileWorkspace(wsA) also moved wsB's pin")
	}
}

// TestReconcileWorkspaceNoCatalogYet checks that ReconcileWorkspace does
// nothing, without error, when the catalog has no main branch yet.
func TestReconcileWorkspaceNoCatalogYet(t *testing.T) {
	st := newStore(t)
	if err := ReconcileWorkspace(st, wsA); err != nil {
		t.Fatalf("empty catalog: %v", err)
	}
}

// TestRetryConflictRetries checks that retryConflict calls fn again while it
// fails with store.ErrConflict, so the loser of a pin move's compare-and-swap
// against a concurrent workspace push gets another attempt (ruling 2.3)
// instead of being dropped.
func TestRetryConflictRetries(t *testing.T) {
	calls := 0
	err := retryConflict(func() error {
		calls++
		if calls < maxConflictRetries {
			return fmt.Errorf("lost the race: %w", store.ErrConflict)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != maxConflictRetries {
		t.Errorf("calls = %d, want %d", calls, maxConflictRetries)
	}
}

// TestRetryConflictGivesUpAfterMaxAttempts checks that retryConflict stops
// retrying and returns the last error once the bounded number of attempts is
// used up.
func TestRetryConflictGivesUpAfterMaxAttempts(t *testing.T) {
	calls := 0
	err := retryConflict(func() error {
		calls++
		return store.ErrConflict
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("err %v, want store.ErrConflict", err)
	}
	if calls != maxConflictRetries {
		t.Errorf("calls = %d, want %d", calls, maxConflictRetries)
	}
}

// TestRetryConflictStopsOnOtherErrors checks that retryConflict does not
// retry an error that is not store.ErrConflict.
func TestRetryConflictStopsOnOtherErrors(t *testing.T) {
	calls := 0
	want := errors.New("boom")
	err := retryConflict(func() error {
		calls++
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("err %v, want %v", err, want)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

// firstParent returns the first parent of rev.
func firstParent(t *testing.T, repo *gitrepo.Repo, rev string) string {
	t.Helper()
	return gittest.Run(t, repo.Dir, "rev-parse", rev+"^")
}
