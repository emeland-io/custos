package distribute

import (
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// frozenWithProposal returns a store whose workspace wsA is frozen at c1 and
// has a proposal for c2.
func frozenWithProposal(t *testing.T) (st *store.Store, repo *gitrepo.Repo, c1, c2 string) {
	t.Helper()
	st = newStore(t)
	c1 = commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	markFrozen(t, st, wsA)
	c2 = newTaskAVersion(t, st, "1.2.0", "1.1.0")
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	return st, wsRepo(t, st, wsA), c1, c2
}

func TestProposals(t *testing.T) {
	st, repo, c1, c2 := frozenWithProposal(t)
	ps, err := Proposals(st, wsA)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Fatalf("proposals %+v", ps)
	}
	p := ps[0]
	if p.Branch != "custos/pin/"+c2 || p.Commit != resolve(t, repo, "refs/heads/"+p.Branch) || p.From != c1 || p.To != c2 {
		t.Errorf("proposal %+v", p)
	}
	wantRefs(t, "new versions", p.Changes.NewVersions, task.Ref{ID: fixture.TaskA, Version: "1.2.0"})

	createWorkspace(t, st, wsB) // unfrozen, pinned to catalog main
	ps, err = Proposals(st, wsB)
	if err != nil || ps == nil || len(ps) != 0 {
		t.Errorf("unfrozen workspace: proposals %#v, err %v", ps, err)
	}
	if _, err := Proposals(st, "e5f6a7b8-c9d0-4e1f-a2b3-c4d5e6f7a8b9"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown workspace: err %v", err)
	}
}

func TestAcceptFastForward(t *testing.T) {
	st, repo, _, c2 := frozenWithProposal(t)
	branch := "custos/pin/" + c2
	tip := resolve(t, repo, "refs/heads/"+branch)

	if err := Accept(st, wsA, branch, person); err != nil {
		t.Fatal(err)
	}
	if got := resolve(t, repo, "refs/heads/main"); got != tip {
		t.Errorf("main %s, want the proposal %s", got, tip)
	}
	if c := configAt(t, repo, "refs/heads/main"); c.Catalog.Commit != c2 || !c.Frozen {
		t.Errorf("config %+v", c)
	}
	if b := pinBranches(t, repo); len(b) != 0 {
		t.Errorf("branches left: %v", b)
	}
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	if b := pinBranches(t, repo); len(b) != 0 {
		t.Errorf("reconcile reopened an accepted proposal: %v", b)
	}
}

func TestAcceptDivergedMain(t *testing.T) {
	st, repo, _, c2 := frozenWithProposal(t)
	answerA(t, st, wsA) // main moves past the proposal's base
	branch := "custos/pin/" + c2

	if err := Accept(st, wsA, branch, person); err != nil {
		t.Fatal(err)
	}
	if n := parentCount(t, repo, "refs/heads/main"); n != 2 {
		t.Errorf("main has %d parents, want a merge commit", n)
	}
	if c := configAt(t, repo, "refs/heads/main"); c.Catalog.Commit != c2 || !c.Frozen {
		t.Errorf("config %+v", c)
	}
	if _, ok, err := repo.ReadFile("refs/heads/main", "answers/"+fixture.TaskA+".md"); err != nil || !ok {
		t.Errorf("answer lost in the merge: ok %v, err %v", ok, err)
	}
	if got := authorOf(t, repo, "refs/heads/main"); got != person.String() {
		t.Errorf("merge author %q", got)
	}
	if b := pinBranches(t, repo); len(b) != 0 {
		t.Errorf("branches left: %v", b)
	}
}

func TestAcceptIsValidated(t *testing.T) {
	st, repo, c1, _ := frozenWithProposal(t)
	// A catalog commit that is not on the catalog's main.
	stray, err := st.CatalogRepo().WriteCommit(gitrepo.CommitRequest{
		Base: c1, Parents: []string{c1}, Author: gitrepo.Bot, Message: "Draft",
		Changes: []gitrepo.Change{{Path: "README.md", Data: []byte("draft\n")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	branch := "custos/pin/" + stray
	if _, err := st.UpdateWorkspace(wsA, "refs/heads/"+branch, person, "Pin a draft", func(tree fs.FS) ([]gitrepo.Change, error) {
		return editConfig(tree, func(c *workspace.Config) { c.Catalog.Commit = stray })
	}); err != nil {
		t.Fatal(err)
	}
	main := resolve(t, repo, "refs/heads/main")

	err = Accept(st, wsA, branch, person)
	var rej *store.RejectedError
	if !errors.As(err, &rej) || !slices.ContainsFunc(rej.Problems, func(p problem.Problem) bool { return p.Rule == problem.RulePin }) {
		t.Fatalf("err %v, want a rejection with rule %s", err, problem.RulePin)
	}
	if got := resolve(t, repo, "refs/heads/main"); got != main {
		t.Errorf("main moved to %s", got)
	}
	if resolve(t, repo, "refs/heads/"+branch) == "" {
		t.Error("a rejected accept must keep the proposal")
	}
}

func TestAcceptUnknownBranch(t *testing.T) {
	st, _, _, _ := frozenWithProposal(t)
	for _, branch := range []string{"custos/pin/" + strings.Repeat("a", 40), "draft", "custos/pin/../main"} {
		if err := Accept(st, wsA, branch, person); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("accept %q: err %v, want store.ErrNotFound", branch, err)
		}
		if err := Reject(st, wsA, branch); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("reject %q: err %v, want store.ErrNotFound", branch, err)
		}
	}
}

func TestRejectStaysRejected(t *testing.T) {
	st, repo, _, c2 := frozenWithProposal(t)
	main := resolve(t, repo, "refs/heads/main")
	if err := Reject(st, wsA, "custos/pin/"+c2); err != nil {
		t.Fatal(err)
	}
	if b := pinBranches(t, repo); len(b) != 0 {
		t.Fatalf("branches after reject: %v", b)
	}
	if got := resolve(t, repo, "refs/heads/main"); got != main {
		t.Errorf("reject moved main to %s", got)
	}
	// A restart runs reconcile again; the rejected proposal stays closed.
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	if b := pinBranches(t, repo); len(b) != 0 {
		t.Fatalf("reconcile reopened a rejected proposal: %v", b)
	}
	// The next catalog commit opens a new proposal and forgets the rejection.
	c3 := newTaskAVersion(t, st, "1.3.0", "1.2.0")
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	if b := pinBranches(t, repo); !slices.Equal(b, []string{"custos/pin/" + c3}) {
		t.Errorf("branches %v, want the proposal for %s", b, c3)
	}
	if marks, err := repo.Refs(rejectedRefPrefix); err != nil || len(marks) != 0 {
		t.Errorf("rejection marks left: %v, err %v", marks, err)
	}
}
