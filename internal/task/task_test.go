package task

import (
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/problem"
)

func TestParseFileValid(t *testing.T) {
	path := fixture.TaskPath(fixture.TaskA, "1.1.0")
	v, ps := ParseFile(path, []byte(fixture.TaskFile(fixture.TaskA, "1.1.0", fixture.TaskA+"@1.0.0")))
	fixture.WantNone(t, ps)
	if v.ID != fixture.TaskA || v.Version != "1.1.0" || v.AnswerType != AnswerText || v.Path != path {
		t.Errorf("got %+v", v)
	}
	if want := []Ref{{ID: fixture.TaskA, Version: "1.0.0"}}; !slices.Equal(v.Previous, want) {
		t.Errorf("previous %v, want %v", v.Previous, want)
	}
	if v.Body != "Describe how the task was done.\n" {
		t.Errorf("body %q", v.Body)
	}
}

func TestParseFileWindowsLineEndings(t *testing.T) {
	data := strings.ReplaceAll(fixture.TaskFile(fixture.TaskA, "1.0.0"), "\n", "\r\n")
	v, ps := ParseFile(fixture.TaskPath(fixture.TaskA, "1.0.0"), []byte(data))
	fixture.WantNone(t, ps)
	if strings.Contains(v.Body, "\r") || v.Title != "Task 1.0.0" {
		t.Errorf("got %+v", v)
	}
}

func TestParseFileProblems(t *testing.T) {
	a := fixture.TaskA
	path := fixture.TaskPath(a, "1.0.0")
	base := fixture.TaskFile(a, "1.0.0")
	tests := []struct {
		name, path, data, rule, contains string
	}{
		{"not frontmatter", path, "hello", problem.RuleFormat, "must start"},
		{"previous is not a list", path, "---\nprevious: foo\n---\n", problem.RuleFormat, "frontmatter"},
		{"unknown field", path, strings.Replace(base, "title:", "colour: red\ntitle:", 1), problem.RuleFormat, "colour"},
		{"uppercase id", path, strings.Replace(base, "id: "+a, "id: "+strings.ToUpper(a), 1), problem.RuleFormat, "lowercase UUID v4"},
		{"uuid v1", path, strings.Replace(base, "id: "+a, "id: 6ba7b810-9dad-11d1-80b4-00c04fd430c8", 1), problem.RuleFormat, "lowercase UUID v4"},
		{"leading v", path, strings.Replace(base, "version: 1.0.0", "version: v1.0.0", 1), problem.RuleFormat, "semantic version"},
		{"wrong path", fixture.TaskPath(a, "2.0.0"), base, problem.RulePath, path},
		{"missing title", path, strings.Replace(base, "title: Task 1.0.0", `title: ""`, 1), problem.RuleFormat, "title is missing"},
		{"unknown answer type", path, strings.Replace(base, "answer_type: text", "answer_type: essay", 1), problem.RuleFormat, "essay"},
		{"choice without choices", path, strings.Replace(base, "answer_type: text", "answer_type: choice", 1), problem.RuleFormat, "non-empty choices"},
		{"duplicate choice", path, strings.Replace(base, "answer_type: text", "answer_type: choice\nchoices: [yes, yes]", 1), problem.RuleFormat, "duplicate choice"},
		{"choices on text", path, strings.Replace(base, "answer_type: text", "answer_type: text\nchoices: [a]", 1), problem.RuleFormat, "only allowed"},
		{"previous is itself", path, fixture.TaskFile(a, "1.0.0", a+"@1.0.0"), problem.RulePrevious, "itself"},
		{"previous listed twice", path, fixture.TaskFile(a, "1.0.0", a+"@0.9.0", a+"@0.9.0"), problem.RulePrevious, "twice"},
		{"previous invalid", path, fixture.TaskFile(a, "1.0.0", a+"@latest"), problem.RulePrevious, "not a valid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ps := ParseFile(tt.path, []byte(tt.data))
			fixture.WantProblem(t, ps, tt.path, tt.rule, tt.contains)
		})
	}
}

func TestValidID(t *testing.T) {
	for s, want := range map[string]bool{
		fixture.TaskA:                          true,
		strings.ToUpper(fixture.TaskA):         false,
		"{" + fixture.TaskA + "}":              false,
		"6ba7b810-9dad-11d1-80b4-00c04fd430c8": false,
		"":                                     false,
	} {
		if got := ValidID(s); got != want {
			t.Errorf("ValidID(%q) = %v, want %v", s, got, want)
		}
	}
}
