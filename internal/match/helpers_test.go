package match

import (
	"fmt"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/workspace"
)

// digest is the image digest of the runs in these tests.
const digest = "sha256:" + fixture.SHA256

// gid returns the n-th test UUID v4.
func gid(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

// genPath returns the path of a generated task version.
func genPath(id, version string) string { return "generated/" + id + "/" + version + ".md" }

// genFile returns a generated text task version with match key key, made
// from the answer to task producer. Each previous entry is "<id>@<version>".
func genFile(id, version, key, producer string, previous ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nid: %s\nversion: %s\ntitle: Generated %s\nanswer_type: text\n", id, version, key)
	if len(previous) > 0 {
		b.WriteString("previous:\n")
		for _, p := range previous {
			pid, pv, _ := strings.Cut(p, "@")
			fmt.Fprintf(&b, "  - id: %s\n    version: %s\n", pid, pv)
		}
	}
	fmt.Fprintf(&b, "match_key: %s\nproduced_by:\n  task:\n    id: %s\n    version: 1.0.0\n  processor: host-scanner\n"+
		"  digest: %s\n  answer_commit: %s\n---\n\nBody of %s.\n", key, producer, digest, fixture.Commit, key)
	return b.String()
}

// answerFile returns a text answer to task id at version 1.0.0.
func answerFile(id string) string {
	return "---\ntask: " + id + "\ntask_version: 1.0.0\ntype: text\nvalue: done\n---\n"
}

// emptyWorkspace returns the files of a workspace without answers or output.
func emptyWorkspace() map[string]string {
	return map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, fixture.Commit)}
}

// with returns files plus more.
func with(files map[string]string, more map[string]string) map[string]string {
	out := map[string]string{}
	for p, c := range files {
		out[p] = c
	}
	for p, c := range more {
		out[p] = c
	}
	return out
}

// load reads a workspace tree and ignores its problems; tests that need a
// valid tree check it with valid.
func load(t *testing.T, files map[string]string) *workspace.Workspace {
	t.Helper()
	w, _ := workspace.Load(fixture.MapFS(files))
	return w
}

// valid fails the test unless the tree passes workspace.Check.
func valid(t *testing.T, files map[string]string) {
	t.Helper()
	fixture.WantNone(t, workspace.Check(fixture.MapFS(files)))
}

// fixtureCatalog loads fixture.Catalog: TaskA, TaskB and the processor
// host-scanner.
func fixtureCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, ps := catalog.Load(fixture.MapFS(fixture.Catalog()))
	fixture.WantNone(t, ps)
	return c
}
