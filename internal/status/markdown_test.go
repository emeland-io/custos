package status

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
)

func TestRenderMarkdown(t *testing.T) {
	ws := fixture.Workspace()
	ws[answerPath(b)] = "---\ntask: " + b + "\ntask_version: 1.0.0\ntype: text\nvalue: shipped\n" +
		"attachments:\n  - name: log.txt\n    sha256: " + fixture.SHA256 + "\n    media_type: text/plain\n---\n"
	got := string(RenderMarkdown(compute(t, fixture.Catalog(), ws)))
	want := "# Workspace " + fixture.WorkspaceID + "\n\n" +
		"Catalog commit `" + fixture.Commit + "`.\n\n" +
		"## Build\n\n" +
		"### Task 1.1.0\n\n" +
		"*Pending update: answered for version 1.0.0; the current version is 1.1.0.*\n\n" +
		"done with make\n\n" +
		"### Release\n\n" +
		"#### Task 1.0.0\n\n" +
		"shipped\n\n" +
		"- Attachment log.txt (text/plain, sha256 " + fixture.SHA256 + ")\n\n" +
		"##### Host web-01\n\n" +
		"*Unanswered.*\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderMarkdownMerged(t *testing.T) {
	ws := fixture.Workspace()
	delete(ws, answerPath(a))
	ws[answerPath(b)] = answerFile(b, "1.0.0")
	got := string(RenderMarkdown(compute(t, mergedCatalog(), ws)))
	want := "### Task 2.0.0\n\n" +
		"*Pending update: version 2.0.0 is not answered yet; the answers of the tasks it merged stay in effect.*\n\n" +
		"*Answer to merged task " + b + ", version 1.0.0:*\n\n" +
		"answer to 7d9e@1.0.0\n\n"
	if !strings.Contains(got, want) {
		t.Errorf("got:\n%s\nwant it to contain:\n%s", got, want)
	}
}

func TestRenderMarkdownBody(t *testing.T) {
	cat := fixture.Catalog()
	path := fixture.TaskPath(a, "1.1.0")
	cat[path] = strings.Replace(cat[path], "answer_type: text", "answer_type: markdown", 1)
	ws := fixture.Workspace()
	ws[answerPath(a)] = "---\ntask: " + a + "\ntask_version: 1.1.0\ntype: markdown\n---\n\nBuilt with `make release`.\n\nSee the pipeline.\n"
	got := string(RenderMarkdown(compute(t, cat, ws)))
	if !strings.Contains(got, "### Task 1.1.0\n\nBuilt with `make release`.\n\nSee the pipeline.\n\n### Release") {
		t.Errorf("got:\n%s", got)
	}
}

func TestRenderMarkdownDeepNesting(t *testing.T) {
	cat := fixture.Catalog()
	cat["groups/index.yaml"] = "groups: [g1]\n"
	cat["groups/g1/group.yaml"] = "title: G1\nchildren:\n  - group: g2\n"
	cat["groups/g2/group.yaml"] = "title: G2\nchildren:\n  - group: g3\n"
	cat["groups/g3/group.yaml"] = "title: G3\nchildren:\n  - group: g4\n"
	cat["groups/g4/group.yaml"] = "title: G4\nchildren:\n  - group: g5\n"
	cat["groups/g5/group.yaml"] = "title: G5\nchildren:\n  - group: build\n"
	got := string(RenderMarkdown(compute(t, cat, fixture.Workspace())))
	if !strings.Contains(got, "\n###### G5\n") || !strings.Contains(got, "\n###### Build\n") || strings.Contains(got, "#######") {
		t.Errorf("got:\n%s", got)
	}
}
