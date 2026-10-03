package catalog

import (
	"maps"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/problem"
)

const (
	a = fixture.TaskA
	b = fixture.TaskB
	c = fixture.TaskC
)

// merge adds TaskA 2.0.0, which merges TaskA 1.1.0 and TaskB 1.0.0.
func merge(f map[string]string) {
	f[fixture.TaskPath(a, "2.0.0")] = fixture.TaskFile(a, "2.0.0", a+"@1.1.0", b+"@1.0.0")
}

func TestValidCatalog(t *testing.T) {
	fixture.WantNone(t, Check(fixture.MapFS(fixture.Catalog())))
}

func TestEmptyCatalog(t *testing.T) {
	fixture.WantNone(t, Check(fixture.MapFS(nil)))
}

func TestLoad(t *testing.T) {
	cat, ps := Load(fixture.MapFS(fixture.Catalog()))
	fixture.WantNone(t, ps)
	if cur, ok := cat.Tasks.Current(a); !ok || cur.Version != "1.1.0" {
		t.Errorf("current of TaskA: %v %v", cur, ok)
	}
	if len(cat.Index) != 1 || cat.Groups["build"].Title != "Build" || cat.Groups["release"].Children[0].Task != b {
		t.Errorf("groups %+v index %v", cat.Groups, cat.Index)
	}
	if cat.Registry.Bindings[b] != "host-scanner" {
		t.Errorf("registry %+v", cat.Registry)
	}
}

func TestValidMerge(t *testing.T) {
	f := fixture.Catalog()
	merge(f)
	f["groups/release/group.yaml"] = "title: Release\nchildren: []\n"
	f["processors.yaml"] = "processors: {}\nbindings: {}\n"
	fixture.WantNone(t, Check(fixture.MapFS(f)))
}

func TestCheckProblems(t *testing.T) {
	tests := []struct {
		name                 string
		change               func(f map[string]string)
		path, rule, contains string
	}{
		{"version stored at wrong path",
			func(f map[string]string) { f[fixture.TaskPath(a, "2.0.0")] = fixture.TaskFile(a, "1.5.0", a+"@1.1.0") },
			fixture.TaskPath(a, "2.0.0"), problem.RulePath, fixture.TaskPath(a, "1.5.0")},
		{"unexpected file below tasks",
			func(f map[string]string) { f["tasks/"+a+"/notes.txt"] = "x" },
			"tasks/" + a + "/notes.txt", problem.RulePath, "unexpected file"},
		{"previous does not exist",
			func(f map[string]string) { f[fixture.TaskPath(b, "1.1.0")] = fixture.TaskFile(b, "1.1.0", b+"@0.9.0") },
			fixture.TaskPath(b, "1.1.0"), problem.RulePrevious, "does not exist"},
		{"two current versions",
			func(f map[string]string) { f[fixture.TaskPath(a, "1.2.0")] = fixture.TaskFile(a, "1.2.0", a+"@1.0.0") },
			fixture.TaskPath(a, "1.2.0"), problem.RuleSingleCurrent, "2 current versions"},
		{"merged task still in a group",
			merge,
			"groups/release/group.yaml", problem.RuleGroups, "merged into another task"},
		{"index lists unknown group",
			func(f map[string]string) { f["groups/index.yaml"] = "groups: [build, missing]\n" },
			"groups/index.yaml", problem.RuleGroups, `unknown group "missing"`},
		{"group lists unknown task",
			func(f map[string]string) {
				f["groups/release/group.yaml"] = "title: Release\nchildren:\n  - task: " + b + "\n  - task: " + c + "\n"
			},
			"groups/release/group.yaml", problem.RuleGroups, "unknown task " + c},
		{"task listed in two groups",
			func(f map[string]string) {
				f["groups/release/group.yaml"] = "title: Release\nchildren:\n  - task: " + b + "\n  - task: " + a + "\n"
			},
			"groups/release/group.yaml", problem.RuleGroups, "already listed in groups/build/group.yaml"},
		{"group not reachable",
			func(f map[string]string) { f["groups/orphan/group.yaml"] = "title: Orphan\nchildren: []\n" },
			"groups/orphan/group.yaml", problem.RuleGroups, "not reachable"},
		{"group listed twice",
			func(f map[string]string) { f["groups/index.yaml"] = "groups: [build, release]\n" },
			"groups/release/group.yaml", problem.RuleGroups, "listed 2 times"},
		{"child sets group and task",
			func(f map[string]string) {
				f["groups/release/group.yaml"] = "title: Release\nchildren:\n  - task: " + b + "\n    group: build\n"
			},
			"groups/release/group.yaml", problem.RuleGroups, "exactly one of group or task"},
		{"group without title",
			func(f map[string]string) { f["groups/release/group.yaml"] = "children:\n  - task: " + b + "\n" },
			"groups/release/group.yaml", problem.RuleGroups, "title is missing"},
		{"group directory without group.yaml",
			func(f map[string]string) { f["groups/empty/README.md"] = "x" },
			"groups/empty", problem.RuleGroups, "group.yaml is missing"},
		{"bad group directory name",
			func(f map[string]string) { f["groups/Build Tools/group.yaml"] = "title: X\n" },
			"groups/Build Tools", problem.RuleGroups, "must match"},
		{"unexpected file in groups",
			func(f map[string]string) { f["groups/notes.txt"] = "x" },
			"groups/notes.txt", problem.RulePath, "unexpected file"},
		{"unknown field in index",
			func(f map[string]string) { f["groups/index.yaml"] = "groups: [build]\nextra: 1\n" },
			"groups/index.yaml", problem.RuleFormat, "extra"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := maps.Clone(fixture.Catalog())
			tt.change(f)
			fixture.WantProblem(t, Check(fixture.MapFS(f)), tt.path, tt.rule, tt.contains)
		})
	}
}

func TestCheckGarbage(t *testing.T) {
	f := fixture.Catalog()
	f[fixture.TaskPath(c, "1.0.0")] = ""
	f[fixture.TaskPath(c, "1.1.0")] = "---\nprevious: foo\n---\n"
	f["groups/index.yaml"] = "groups: {a: 1}\n"
	f["groups/build/group.yaml"] = "- not a map\n"
	f["processors.yaml"] = "processors: [1, 2]\n"
	ps := Check(fixture.MapFS(f)) // must not panic
	fixture.WantProblem(t, ps, fixture.TaskPath(c, "1.0.0"), problem.RuleFormat, "must start")
	fixture.WantProblem(t, ps, fixture.TaskPath(c, "1.1.0"), problem.RuleFormat, "frontmatter")
	fixture.WantProblem(t, ps, "groups/index.yaml", problem.RuleFormat, "")
	fixture.WantProblem(t, ps, "groups/build/group.yaml", problem.RuleFormat, "")
	fixture.WantProblem(t, ps, "processors.yaml", problem.RuleFormat, "")
}
