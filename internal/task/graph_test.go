package task

import (
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/problem"
)

const a, b = fixture.TaskA, fixture.TaskB

func ver(id, version string, previous ...Ref) *Version {
	return &Version{
		Meta: Meta{ID: id, Version: version, Title: "t", AnswerType: AnswerText, Previous: previous},
		Path: "tasks/" + id + "/" + version + ".md",
	}
}

func ref(id, version string) Ref { return Ref{ID: id, Version: version} }

func graph(t *testing.T, vs ...*Version) *Graph {
	t.Helper()
	g, ps := NewGraph(vs)
	fixture.WantNone(t, ps)
	return g
}

func TestLinearHistory(t *testing.T) {
	g := graph(t, ver(a, "1.1.0", ref(a, "1.0.0")), ver(a, "1.0.0"))
	cur, ok := g.Current(a)
	if !ok || cur.Version != "1.1.0" {
		t.Fatalf("current %v %v", cur, ok)
	}
	if vs := g.Versions(a); len(vs) != 2 || vs[0].Version != "1.0.0" {
		t.Errorf("versions not sorted: %v", vs)
	}
	if g.Superseded(a) || !g.Has(a) || g.Has(b) {
		t.Error("wrong Superseded or Has")
	}
	fixture.WantNone(t, g.Check())
}

func TestFork(t *testing.T) {
	g := graph(t, ver(a, "1.0.0"), ver(a, "1.1.0", ref(a, "1.0.0")), ver(a, "1.2.0", ref(a, "1.0.0")))
	if _, ok := g.Current(a); ok {
		t.Error("a forked task has no single current version")
	}
	fixture.WantProblem(t, g.Check(), "tasks/"+a+"/1.2.0.md", problem.RuleSingleCurrent, "2 current versions (1.1.0, 1.2.0)")
}

func TestMerge(t *testing.T) {
	g := graph(t, ver(a, "1.0.0"), ver(b, "1.0.0"), ver(a, "2.0.0", ref(a, "1.0.0"), ref(b, "1.0.0")))
	if cur, ok := g.Current(a); !ok || cur.Version != "2.0.0" {
		t.Errorf("current of a: %v %v", cur, ok)
	}
	if !g.Superseded(b) {
		t.Error("b must be superseded")
	}
	if _, ok := g.Current(b); ok {
		t.Error("b must have no current version")
	}
	fixture.WantNone(t, g.Check())
	if got := g.Tasks(); len(got) != 2 || got[0] != a {
		t.Errorf("tasks %v", got)
	}
}

func TestMissingPrevious(t *testing.T) {
	g := graph(t, ver(a, "1.1.0", ref(a, "1.0.0")))
	fixture.WantProblem(t, g.Check(), "tasks/"+a+"/1.1.0.md", problem.RulePrevious, "does not exist")
}

func TestPreviousMustBeLower(t *testing.T) {
	g := graph(t, ver(a, "1.0.0", ref(a, "1.1.0")), ver(a, "1.1.0"))
	fixture.WantProblem(t, g.Check(), "tasks/"+a+"/1.0.0.md", problem.RulePrevious, "lower version")
}

func TestCycle(t *testing.T) {
	g := graph(t,
		ver(a, "1.0.0"), ver(b, "1.0.0"),
		ver(a, "2.0.0", ref(a, "1.0.0"), ref(b, "2.0.0")),
		ver(b, "2.0.0", ref(b, "1.0.0"), ref(a, "2.0.0")),
	)
	fixture.WantProblem(t, g.Check(), "tasks/"+b+"/2.0.0.md", problem.RulePrevious, "reference cycle")
}

func TestDuplicate(t *testing.T) {
	dup := ver(a, "1.0.0")
	dup.Path = "tasks/" + a + "/copy.md"
	_, ps := NewGraph([]*Version{ver(a, "1.0.0"), dup})
	fixture.WantProblem(t, ps, dup.Path, problem.RulePath, "also defined in tasks/"+a+"/1.0.0.md")
}
