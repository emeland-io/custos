package distribute

import (
	"errors"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
)

func TestFreeze(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	repo := wsRepo(t, st, wsA)

	if err := Freeze(st, wsA, person); err != nil {
		t.Fatal(err)
	}
	if c := configAt(t, repo, "refs/heads/main"); !c.Frozen {
		t.Errorf("config %+v, want frozen", c)
	}
	if got := authorOf(t, repo, "refs/heads/main"); got != person.String() {
		t.Errorf("author %q, want %q", got, person.String())
	}
	if got := committerOf(t, repo, "refs/heads/main"); got != gitrepo.Bot.String() {
		t.Errorf("committer %q, want %q", got, gitrepo.Bot.String())
	}
	main := resolve(t, repo, "refs/heads/main")
	if err := Freeze(st, wsA, person); err != nil {
		t.Fatal(err)
	}
	if got := resolve(t, repo, "refs/heads/main"); got != main {
		t.Errorf("freezing a frozen workspace committed %s", got)
	}
}

func TestFreezeUnknownWorkspace(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	if err := Freeze(st, wsB, person); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err %v, want store.ErrNotFound", err)
	}
	if err := Unfreeze(st, wsB, person); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err %v, want store.ErrNotFound", err)
	}
}

func TestFreezeOpensProposalForLaggingPin(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	c2 := newTaskAVersion(t, st, "1.2.0", "1.1.0") // not reconciled yet
	if err := Freeze(st, wsA, person); err != nil {
		t.Fatal(err)
	}
	repo := wsRepo(t, st, wsA)
	if b := pinBranches(t, repo); len(b) != 1 || b[0] != "custos/pin/"+c2 {
		t.Errorf("branches %v, want the proposal for %s", b, c2)
	}
}

func TestUnfreezeMovesPinAndDropsProposal(t *testing.T) {
	st := newStore(t)
	c1 := commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	if err := Freeze(st, wsA, person); err != nil {
		t.Fatal(err)
	}
	c2 := newTaskAVersion(t, st, "1.2.0", "1.1.0")
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	repo := wsRepo(t, st, wsA)
	if len(pinBranches(t, repo)) != 1 {
		t.Fatalf("no proposal before unfreeze: %v", pinBranches(t, repo))
	}

	if err := Unfreeze(st, wsA, person); err != nil {
		t.Fatal(err)
	}
	c := configAt(t, repo, "refs/heads/main")
	if c.Frozen || c.Catalog.Commit != c2 {
		t.Errorf("main config %+v, want unfrozen and pinned to %s", c, c2)
	}
	if got := authorOf(t, repo, "refs/heads/main"); got != gitrepo.Bot.String() {
		t.Errorf("pin move author %q", got)
	}
	prev := configAt(t, repo, "refs/heads/main~1")
	if prev.Frozen || prev.Catalog.Commit != c1 {
		t.Errorf("unfreeze commit config %+v", prev)
	}
	if got := authorOf(t, repo, "refs/heads/main~1"); got != person.String() {
		t.Errorf("unfreeze author %q", got)
	}
	if b := pinBranches(t, repo); len(b) != 0 {
		t.Errorf("proposals left after unfreeze: %v", b)
	}
}
