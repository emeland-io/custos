package workspace

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/problem"
)

// choiceCatalog is fixture.Catalog with TaskB 1.0.0 asking for a choice.
func choiceCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	f := fixture.Catalog()
	path := fixture.TaskPath(fixture.TaskB, "1.0.0")
	f[path] = strings.Replace(f[path], "answer_type: text\n", "answer_type: choice\nchoices: [yes, no]\n", 1)
	c, ps := catalog.Load(fixture.MapFS(f))
	fixture.WantNone(t, ps)
	return c
}

func answer(id, version, typ, value string) string {
	return "---\ntask: " + id + "\ntask_version: " + version + "\ntype: " + typ + "\nvalue: " + value + "\n---\n"
}

func TestCheckAgainstCatalog(t *testing.T) {
	pathB := "answers/" + fixture.TaskB + ".md"
	pathC := "answers/" + fixture.TaskC + ".md"
	unknown := "0f0e0d0c-0b0a-4908-8706-050403020100"
	tests := []struct {
		name                 string
		files                map[string]string
		path, rule, contains string // empty path: no problems expected
	}{
		{"catalog task", nil, "", "", ""},
		{"generated task", map[string]string{pathC: answer(fixture.TaskC, "1.0.0", "timestamp", "2026-10-02T14:00:00Z")}, "", "", ""},
		{"choice", map[string]string{pathB: answer(fixture.TaskB, "1.0.0", "choice", "yes")}, "", "", ""},
		{"unknown task", map[string]string{"answers/" + unknown + ".md": answer(unknown, "1.0.0", "text", "x")},
			"answers/" + unknown + ".md", problem.RuleAnswer, "neither in the pinned catalog nor a generated task"},
		{"unknown catalog version", map[string]string{answerPath: answer(fixture.TaskA, "9.0.0", "text", "x")},
			answerPath, problem.RuleAnswer, "has no version 9.0.0 in the pinned catalog"},
		{"unknown generated version", map[string]string{pathC: answer(fixture.TaskC, "2.0.0", "timestamp", "2026-10-02T14:00:00Z")},
			pathC, problem.RuleAnswer, "has no version 2.0.0 in the generated tasks"},
		{"wrong type", map[string]string{answerPath: answer(fixture.TaskA, "1.0.0", "path", "docs/build.md")},
			answerPath, problem.RuleAnswer, "type path does not match answer_type text of task " + fixture.TaskA + "@1.0.0"},
		{"value not a choice", map[string]string{pathB: answer(fixture.TaskB, "1.0.0", "choice", "maybe")},
			pathB, problem.RuleAnswer, `value "maybe" is not one of the choices of task ` + fixture.TaskB + "@1.0.0: yes, no"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := fixture.Workspace()
			for p, c := range tt.files {
				f[p] = c
			}
			w, ps := Load(fixture.MapFS(f))
			fixture.WantNone(t, ps)
			got := CheckAgainstCatalog(w, choiceCatalog(t))
			if tt.path == "" {
				fixture.WantNone(t, got)
				return
			}
			fixture.WantProblem(t, got, tt.path, tt.rule, tt.contains)
			if len(got) != 1 {
				t.Errorf("want exactly one problem, got %v", got)
			}
		})
	}
}

// TestCheckAgainstCatalogSkipsMalformedAnswers leaves malformed answers to
// Check, so a push shows each mistake once.
func TestCheckAgainstCatalogSkipsMalformedAnswers(t *testing.T) {
	f := fixture.Workspace()
	f[answerPath] = answer(fixture.TaskA, "one", "text", "x")
	f["answers/not-a-uuid.md"] = answer("not-a-uuid", "1.0.0", "text", "x")
	w, _ := Load(fixture.MapFS(f))
	fixture.WantNone(t, CheckAgainstCatalog(w, choiceCatalog(t)))
}
