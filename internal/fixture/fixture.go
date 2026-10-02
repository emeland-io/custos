// Package fixture provides valid catalog and workspace trees and assertions
// for tests. Only test code imports it.
package fixture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/emeland-io/custos/internal/problem"
)

const (
	TaskA       = "3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f"
	TaskB       = "7d9e0b1c-2f3a-4b5c-8d6e-7f8091a2b3c4"
	TaskC       = "a1b2c3d4-e5f6-4a7b-b8c9-d0e1f2a3b4c5"
	DocID       = "c4d5e6f7-a8b9-4c0d-9e1f-2a3b4c5d6e7f"
	WorkspaceID = "5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9"
	Commit      = "0123456789abcdef0123456789abcdef01234567"
	SHA256      = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// TaskPath returns the catalog path of a task version.
func TaskPath(id, version string) string {
	return "tasks/" + id + "/" + version + ".md"
}

// TaskFile returns a task version file with answer type text. Each previous
// entry is written as "<id>@<version>".
func TaskFile(id, version string, previous ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nid: %s\nversion: %s\ntitle: Task %s\nanswer_type: text\n", id, version, version)
	if len(previous) > 0 {
		b.WriteString("previous:\n")
		for _, p := range previous {
			pid, pv, _ := strings.Cut(p, "@")
			fmt.Fprintf(&b, "  - id: %s\n    version: %s\n", pid, pv)
		}
	}
	b.WriteString("---\n\nDescribe how the task was done.\n")
	return b.String()
}

// Catalog returns a valid catalog: TaskA in versions 1.0.0 and 1.1.0 in group
// "build", TaskB 1.0.0 in group "release" below "build", and the processor
// "host-scanner" bound to TaskB.
func Catalog() map[string]string {
	return map[string]string{
		TaskPath(TaskA, "1.0.0"):    TaskFile(TaskA, "1.0.0"),
		TaskPath(TaskA, "1.1.0"):    TaskFile(TaskA, "1.1.0", TaskA+"@1.0.0"),
		TaskPath(TaskB, "1.0.0"):    TaskFile(TaskB, "1.0.0"),
		"groups/index.yaml":         "groups: [build]\n",
		"groups/build/group.yaml":   "title: Build\nchildren:\n  - task: " + TaskA + "\n  - group: release\n",
		"groups/release/group.yaml": "title: Release\nchildren:\n  - task: " + TaskB + "\n",
		"processors.yaml": "processors:\n  host-scanner:\n    image: registry.example.org/host-scanner@sha256:" + SHA256 +
			"\n    timeout: 30s\nbindings:\n  " + TaskB + ": host-scanner\n",
	}
}

// AnswerFile is the text answer to TaskA 1.0.0 in Workspace.
const AnswerFile = "---\ntask: " + TaskA + "\ntask_version: 1.0.0\ntype: text\nvalue: done with make\n---\n"

// GeneratedFile is the task TaskC that host-scanner generated from the
// answer to TaskB in Workspace.
const GeneratedFile = "---\nid: " + TaskC + "\nversion: 1.0.0\ntitle: Host web-01\nanswer_type: timestamp\n" +
	"origin:\n  id: " + TaskB + "\nmatch_key: host:web-01\nproduced_by:\n  task:\n    id: " + TaskB +
	"\n    version: 1.0.0\n  processor: host-scanner\n  digest: sha256:" + SHA256 +
	"\n  answer_commit: " + Commit + "\n---\n\nWhen was web-01 last patched?\n"

// DocumentFile is an unsigned in-toto statement made by host-scanner.
const DocumentFile = `{"match_key":"slsa:web-01","name":"provenance","media_type":"application/vnd.in-toto+json",` +
	`"produced_by":{"task":{"id":"` + TaskB + `","version":"1.0.0"},"processor":"host-scanner","digest":"sha256:` + SHA256 +
	`","answer_commit":"` + Commit + `"},"verification":{"status":"unsigned"},` +
	`"content":{"_type":"https://in-toto.io/Statement/v1"}}`

// Workspace returns a valid workspace with one answer, one generated task and
// one document.
func Workspace() map[string]string {
	return map[string]string{
		"custos.yaml":                      "workspace: " + WorkspaceID + "\ncatalog:\n  url: https://custos.example.org/git/catalog.git\n  commit: " + Commit + "\n",
		"answers/" + TaskA + ".md":         AnswerFile,
		"generated/" + TaskC + "/1.0.0.md": GeneratedFile,
		"documents/" + DocID + ".json":     DocumentFile,
	}
}

// MapFS turns a file map into an fs.FS.
func MapFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for p, c := range files {
		m[p] = &fstest.MapFile{Data: []byte(c)}
	}
	return m
}

// WriteDir writes files below dir, creating directories as needed.
func WriteDir(t testing.TB, dir string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// WantProblem fails the test unless ps holds a problem for path and rule
// whose message contains the given text.
func WantProblem(t testing.TB, ps []problem.Problem, path, rule, contains string) {
	t.Helper()
	for _, p := range ps {
		if p.Path == path && p.Rule == rule && strings.Contains(p.Message, contains) {
			return
		}
	}
	t.Errorf("want problem %s: %s: ...%s...; got:\n%s", path, rule, contains, list(ps))
}

// WantNone fails the test if there are problems.
func WantNone(t testing.TB, ps []problem.Problem) {
	t.Helper()
	if len(ps) > 0 {
		t.Errorf("unexpected problems:\n%s", list(ps))
	}
}

func list(ps []problem.Problem) string {
	if len(ps) == 0 {
		return "  (none)"
	}
	var b strings.Builder
	for _, p := range ps {
		b.WriteString("  " + p.String() + "\n")
	}
	return b.String()
}
