package hook

import (
	"io"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/problem"
)

var zero = strings.Repeat("0", 40)

func update(old, new, ref string) io.Reader {
	return strings.NewReader(old + " " + new + " " + ref + "\n")
}

func check(t *testing.T, dir string, kind Kind, input io.Reader) []problem.Problem {
	t.Helper()
	ps, err := PreReceive(&gitrepo.Repo{Dir: dir}, kind, input)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestFirstPushOfValidCatalog(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Catalog())
	fixture.WantNone(t, check(t, dir, Catalog, update(zero, c1, "refs/heads/main")))
}

func TestInvalidCatalog(t *testing.T) {
	dir := gittest.Init(t)
	f := fixture.Catalog()
	f["groups/index.yaml"] = "groups: [missing]\n"
	c1 := gittest.Commit(t, dir, f)
	fixture.WantProblem(t, check(t, dir, Catalog, update(zero, c1, "refs/heads/main")), "groups/index.yaml", problem.RuleGroups, "unknown group")
}

func TestChangedPublishedVersion(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Catalog())
	path := fixture.TaskPath(fixture.TaskA, "1.0.0")
	c2 := gittest.Commit(t, dir, map[string]string{path: fixture.TaskFile(fixture.TaskA, "1.0.0") + "Changed.\n"})
	fixture.WantProblem(t, check(t, dir, Catalog, update(c1, c2, "refs/heads/main")), path, problem.RuleImmutable, "was changed")
}

// TestGitattributesCannotHideFiles guards against .gitattributes hiding a
// file from validation, for example with export-ignore.
func TestGitattributesCannotHideFiles(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Catalog())
	path := fixture.TaskPath(fixture.TaskA, "2.0.0")
	c2 := gittest.Commit(t, dir, map[string]string{
		path:             "not a task\n",
		".gitattributes": path + " export-ignore\n",
	})
	fixture.WantProblem(t, check(t, dir, Catalog, update(c1, c2, "refs/heads/main")), path, problem.RuleFormat, "")
}

func TestDraftBranchesAreNotChecked(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, map[string]string{"groups/index.yaml": "groups: [missing]\n"})
	fixture.WantNone(t, check(t, dir, Catalog, update(zero, c1, "refs/heads/draft")))
}

func TestDeletingMain(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Catalog())
	fixture.WantProblem(t, check(t, dir, Catalog, update(c1, zero, "refs/heads/main")), "", problem.RuleHistory, "cannot be deleted")
}

func TestRewritingMain(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Catalog())
	c2 := gittest.Commit(t, dir, map[string]string{"README.md": "hello"})
	fixture.WantProblem(t, check(t, dir, Catalog, update(c2, c1, "refs/heads/main")), "", problem.RuleHistory, "cannot be rewritten")
}

func TestWorkspace(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Workspace())
	fixture.WantNone(t, check(t, dir, Workspace, update(zero, c1, "refs/heads/main")))

	other := gittest.Init(t)
	c2 := gittest.Commit(t, other, fixture.Catalog())
	fixture.WantProblem(t, check(t, other, Workspace, update(zero, c2, "refs/heads/main")), "custos.yaml", problem.RuleFormat, "missing")
}

func TestMalformedInput(t *testing.T) {
	if _, err := PreReceive(&gitrepo.Repo{Dir: t.TempDir()}, Catalog, strings.NewReader("garbage\n")); err == nil {
		t.Error("want error")
	}
}

func TestScript(t *testing.T) {
	s, err := Script("/usr/local/bin/custos", Workspace)
	if err != nil {
		t.Fatal(err)
	}
	if want := "#!/bin/sh\nexec '/usr/local/bin/custos' hook pre-receive --kind workspace\n"; s != want {
		t.Errorf("got %q, want %q", s, want)
	}
	if _, err := Script("/tmp/it's/custos", Catalog); err == nil {
		t.Error("a quote in the path must be rejected")
	}
}

func TestParseKind(t *testing.T) {
	if k, err := ParseKind("catalog"); k != Catalog || err != nil {
		t.Errorf("%v %v", k, err)
	}
	if _, err := ParseKind("other"); err == nil {
		t.Error("want error")
	}
}
