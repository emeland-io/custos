package workspace

import (
	"maps"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/problem"
)

var (
	answerPath    = "answers/" + fixture.TaskA + ".md"
	generatedPath = "generated/" + fixture.TaskC + "/1.0.0.md"
	documentPath  = "documents/" + fixture.DocID + ".json"
)

func TestValidWorkspace(t *testing.T) {
	w, ps := Load(fixture.MapFS(fixture.Workspace()))
	fixture.WantNone(t, ps)
	fixture.WantNone(t, Check(fixture.MapFS(fixture.Workspace())))
	if w.Config.Workspace != fixture.WorkspaceID || w.Config.Catalog.Commit != fixture.Commit {
		t.Errorf("config %+v", w.Config)
	}
	if a := w.Answers[answerPath]; a == nil || a.Value != "done with make" || a.TaskVersion != "1.0.0" {
		t.Errorf("answer %+v", a)
	}
	if g := w.Generated[generatedPath]; g == nil || g.MatchKey != "host:web-01" || g.Origin.ID != fixture.TaskB {
		t.Errorf("generated %+v", g)
	}
	if _, ok := w.Graph.Current(fixture.TaskC); !ok {
		t.Error("generated task has no current version")
	}
	if d := w.Documents[documentPath]; d == nil || d.Verification.Status != "unsigned" {
		t.Errorf("document %+v", d)
	}
}

func replace(path, old, new string) func(map[string]string) {
	return func(f map[string]string) { f[path] = strings.Replace(f[path], old, new, 1) }
}

func TestCheckProblems(t *testing.T) {
	tests := []struct {
		name                 string
		change               func(f map[string]string)
		path, rule, contains string
	}{
		{"custos.yaml missing", func(f map[string]string) { delete(f, "custos.yaml") },
			"custos.yaml", problem.RuleFormat, "custos.yaml is missing"},
		{"bad workspace id", replace("custos.yaml", fixture.WorkspaceID, "ws-1"),
			"custos.yaml", problem.RuleFormat, "lowercase UUID v4"},
		{"short commit", replace("custos.yaml", fixture.Commit, "abc123"),
			"custos.yaml", problem.RuleFormat, "full commit hash"},
		{"answer in the wrong file", func(f map[string]string) {
			f["answers/"+fixture.TaskB+".md"] = f[answerPath]
			delete(f, answerPath)
		}, "answers/" + fixture.TaskB + ".md", problem.RulePath, answerPath},
		{"markdown answer with value", replace(answerPath, "type: text", "type: markdown"),
			answerPath, problem.RuleFormat, "value must be empty"},
		{"text answer without value", replace(answerPath, "value: done with make\n", ""),
			answerPath, problem.RuleFormat, "value is missing"},
		{"bad timestamp", replace(answerPath, "type: text\nvalue: done with make", "type: timestamp\nvalue: yesterday"),
			answerPath, problem.RuleFormat, "RFC 3339"},
		{"relative url", replace(answerPath, "type: text\nvalue: done with make", "type: url\nvalue: docs/build"),
			answerPath, problem.RuleFormat, "absolute URL"},
		{"bad attachment hash", replace(answerPath, "value: done with make\n",
			"value: done with make\nattachments:\n  - name: log.txt\n    sha256: abc\n    media_type: text/plain\n"),
			answerPath, problem.RuleFormat, "sha256"},
		{"bad task version", replace(answerPath, "task_version: 1.0.0", "task_version: one"),
			answerPath, problem.RuleFormat, "task_version"},
		{"unexpected file in answers", func(f map[string]string) { f["answers/notes.txt"] = "x" },
			"answers/notes.txt", problem.RulePath, "unexpected file"},
		{"generated without match key", replace(generatedPath, "match_key: host:web-01\n", ""),
			generatedPath, problem.RuleFormat, "match_key is missing"},
		{"generated digest not pinned", replace(generatedPath, "digest: sha256:"+fixture.SHA256, "digest: latest"),
			generatedPath, problem.RuleFormat, "digest"},
		{"generated stored at wrong path", func(f map[string]string) {
			f["generated/"+fixture.TaskC+"/2.0.0.md"] = f[generatedPath]
			delete(f, generatedPath)
		}, "generated/" + fixture.TaskC + "/2.0.0.md", problem.RulePath, generatedPath},
		{"generated with broken previous", replace(generatedPath, "answer_type: timestamp\n",
			"answer_type: timestamp\nprevious:\n  - id: "+fixture.TaskC+"\n    version: 0.9.0\n"),
			generatedPath, problem.RulePrevious, "does not exist"},
		{"document with unknown field", replace(documentPath, `"name":`, `"colour":"red","name":`),
			documentPath, problem.RuleFormat, "colour"},
		{"document with bad status", replace(documentPath, `"status":"unsigned"`, `"status":"maybe"`),
			documentPath, problem.RuleFormat, "verification.status"},
		{"document without content", replace(documentPath, `"content":{"_type":"https://in-toto.io/Statement/v1"}`, `"content":null`),
			documentPath, problem.RuleFormat, "content is missing"},
		{"document with trailing data", func(f map[string]string) { f[documentPath] += " junk" },
			documentPath, problem.RuleFormat, "unexpected data after the JSON object"},
		{"document with a second object", func(f map[string]string) { f[documentPath] += "\n{}\n" },
			documentPath, problem.RuleFormat, "unexpected data after the JSON object"},
		{"document file name not a uuid", func(f map[string]string) {
			f["documents/provenance.json"] = f[documentPath]
			delete(f, documentPath)
		}, "documents/provenance.json", problem.RulePath, "UUID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := maps.Clone(fixture.Workspace())
			tt.change(f)
			fixture.WantProblem(t, Check(fixture.MapFS(f)), tt.path, tt.rule, tt.contains)
		})
	}
}

func TestCheckGarbage(t *testing.T) {
	f := fixture.Workspace()
	f["custos.yaml"] = "- just a list\n"
	f[answerPath] = ""
	f[generatedPath] = "---\nprevious: foo\n---\n"
	f[documentPath] = "[1, 2"
	ps := Check(fixture.MapFS(f)) // must not panic
	fixture.WantProblem(t, ps, "custos.yaml", problem.RuleFormat, "")
	fixture.WantProblem(t, ps, answerPath, problem.RuleFormat, "must start")
	fixture.WantProblem(t, ps, generatedPath, problem.RuleFormat, "frontmatter")
	fixture.WantProblem(t, ps, documentPath, problem.RuleFormat, "")
}
