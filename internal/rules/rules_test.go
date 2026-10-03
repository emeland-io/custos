package rules

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/problem"
)

var zero = strings.Repeat("0", 40)

func checkCatalog(t *testing.T, dir, oldOID, newOID string) []problem.Problem {
	t.Helper()
	ps, err := CheckCatalogUpdate(&gitrepo.Repo{Dir: dir}, oldOID, newOID)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestCatalogUpdate(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Catalog())
	fixture.WantNone(t, checkCatalog(t, dir, "", c1))
	fixture.WantNone(t, checkCatalog(t, dir, zero, c1))

	path := fixture.TaskPath(fixture.TaskA, "1.0.0")
	c2 := gittest.Commit(t, dir, map[string]string{path: fixture.TaskFile(fixture.TaskA, "1.0.0") + "Changed.\n"})
	fixture.WantProblem(t, checkCatalog(t, dir, c1, c2), path, problem.RuleImmutable, "was changed")
	fixture.WantProblem(t, checkCatalog(t, dir, c2, c1), "", problem.RuleHistory, "cannot be rewritten")
	fixture.WantProblem(t, checkCatalog(t, dir, c1, zero), "", problem.RuleHistory, "cannot be deleted")

	c3 := gittest.Commit(t, dir, map[string]string{"groups/index.yaml": "groups: [missing]\n"})
	fixture.WantProblem(t, checkCatalog(t, dir, c2, c3), "groups/index.yaml", problem.RuleGroups, "unknown group")
}

// world is a catalog and a workspace repository, the workspace pinned to
// the catalog's first commit.
type world struct {
	cat, ws  *gitrepo.Repo
	catMain  string // first commit on the catalog's main
	wsCommit string // first commit on the workspace's main
}

func newWorld(t *testing.T) world {
	t.Helper()
	catDir := gittest.Init(t)
	c1 := gittest.Commit(t, catDir, fixture.Catalog())
	wsDir := gittest.Init(t)
	w1 := gittest.Commit(t, wsDir, fixture.PinnedWorkspace(c1))
	return world{cat: &gitrepo.Repo{Dir: catDir}, ws: &gitrepo.Repo{Dir: wsDir}, catMain: c1, wsCommit: w1}
}

func (w world) check(t *testing.T, oldOID, newOID string) []problem.Problem {
	t.Helper()
	ps, err := CheckWorkspaceUpdate(w.ws, w.cat, fixture.WorkspaceID, oldOID, newOID)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

// commit adds files to the workspace and returns the new commit.
func (w world) commit(t *testing.T, files map[string]string) string {
	t.Helper()
	return gittest.Commit(t, w.ws.Dir, files)
}

func TestWorkspaceUpdateValid(t *testing.T) {
	w := newWorld(t)
	fixture.WantNone(t, w.check(t, "", w.wsCommit))
	fixture.WantNone(t, w.check(t, zero, w.wsCommit))
}

func TestWorkspaceUpdateHistory(t *testing.T) {
	w := newWorld(t)
	w2 := w.commit(t, map[string]string{"README.md": "x"})
	fixture.WantNone(t, w.check(t, w.wsCommit, w2))
	fixture.WantProblem(t, w.check(t, w2, w.wsCommit), "", problem.RuleHistory, "cannot be rewritten")
	fixture.WantProblem(t, w.check(t, w2, zero), "", problem.RuleHistory, "cannot be deleted")
}

func TestWorkspaceUpdateID(t *testing.T) {
	w := newWorld(t)
	other := "0f0e0d0c-0b0a-4908-8706-050403020100"
	w2 := w.commit(t, map[string]string{"custos.yaml": fixture.Config(other, w.catMain)})
	fixture.WantProblem(t, w.check(t, w.wsCommit, w2), "custos.yaml", problem.RuleWorkspaceID,
		"workspace "+other+" does not match this repository, which belongs to workspace "+fixture.WorkspaceID)
}

func TestWorkspaceUpdatePin(t *testing.T) {
	w := newWorld(t)
	// A newer catalog main, and a commit on a catalog draft branch.
	newer := gittest.Commit(t, w.cat.Dir, map[string]string{"README.md": "catalog"})
	gittest.Run(t, w.cat.Dir, "checkout", "--quiet", "-b", "draft")
	draft := gittest.Commit(t, w.cat.Dir, map[string]string{"README.md": "draft"})
	gittest.Run(t, w.cat.Dir, "checkout", "--quiet", "main")

	toNewer := w.commit(t, map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, newer)})
	fixture.WantNone(t, w.check(t, w.wsCommit, toNewer))
	back := w.commit(t, map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, w.catMain)})
	fixture.WantNone(t, w.check(t, toNewer, back)) // ruling 2.16: older pins are fine

	toDraft := w.commit(t, map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, draft)})
	fixture.WantProblem(t, w.check(t, back, toDraft), "custos.yaml", problem.RulePin, "is not on the catalog's main branch")

	unknown := w.commit(t, map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, fixture.Commit)})
	fixture.WantProblem(t, w.check(t, toDraft, unknown), "custos.yaml", problem.RulePin, "does not exist in this server's catalog")

	tree := gittest.Run(t, w.cat.Dir, "rev-parse", "main^{tree}")
	toTree := w.commit(t, map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, tree)})
	fixture.WantProblem(t, w.check(t, unknown, toTree), "custos.yaml", problem.RulePin, "does not exist in this server's catalog")
}

func TestWorkspaceUpdateEmptyCatalog(t *testing.T) {
	w := newWorld(t)
	empty := &gitrepo.Repo{Dir: gittest.Init(t)}
	ps, err := CheckWorkspaceUpdate(w.ws, empty, fixture.WorkspaceID, "", w.wsCommit)
	if err != nil {
		t.Fatal(err)
	}
	fixture.WantProblem(t, ps, "custos.yaml", problem.RulePin, "the catalog has no main branch yet")
}

func TestWorkspaceUpdateAnswers(t *testing.T) {
	w := newWorld(t)
	path := "answers/" + fixture.TaskA + ".md"
	w2 := w.commit(t, map[string]string{path: strings.Replace(fixture.AnswerFile, "task_version: 1.0.0", "task_version: 1.2.0", 1)})
	ps := w.check(t, w.wsCommit, w2)
	fixture.WantProblem(t, ps, path, problem.RuleAnswer, "has no version 1.2.0 in the pinned catalog")
	if len(ps) != 1 {
		t.Errorf("want one problem, got %v", ps)
	}
}

// TestWorkspaceUpdateBrokenConfig reports a broken custos.yaml once, without
// follow-up problems about the id, the pin or the answers.
func TestWorkspaceUpdateBrokenConfig(t *testing.T) {
	w := newWorld(t)
	w2 := w.commit(t, map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, "abc123")})
	ps := w.check(t, w.wsCommit, w2)
	fixture.WantProblem(t, ps, "custos.yaml", problem.RuleFormat, "full commit hash")
	if len(ps) != 1 {
		t.Errorf("want one problem, got %v", ps)
	}
}

func TestWorkspaceUpdateFormat(t *testing.T) {
	w := newWorld(t)
	w2 := w.commit(t, map[string]string{"answers/notes.txt": "x"})
	fixture.WantProblem(t, w.check(t, w.wsCommit, w2), "answers/notes.txt", problem.RulePath, "unexpected file")
}
