package status

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/workspace"
)

func TestRenderHTML(t *testing.T) {
	w, _ := workspace.Load(fixture.MapFS(fixture.Workspace()))
	c, _ := catalog.Load(fixture.MapFS(fixture.Catalog()))
	s := Compute(w, c)

	out := string(RenderHTML(s))
	if !strings.HasPrefix(out, "<!DOCTYPE html>") || !strings.Contains(out, "<style>") {
		t.Fatalf("not a self-contained HTML document:\n%s", out)
	}
	if !strings.Contains(out, "Workspace "+s.Workspace) {
		t.Errorf("missing workspace heading:\n%s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Skip("fixture does not currently exercise escaping; see TestRenderHTMLEscapesPlainValues")
	}
}

func TestRenderHTMLEscapesPlainValues(t *testing.T) {
	files := map[string]string{
		"custos.yaml": fixture.Config(fixture.WorkspaceID, fixture.Commit),
		"answers/" + fixture.TaskA + ".md": "---\ntask: " + fixture.TaskA +
			"\ntask_version: 1.0.0\ntype: text\nvalue: <script>alert(1)</script>\n---\n",
	}
	w, _ := workspace.Load(fixture.MapFS(files))
	c, _ := catalog.Load(fixture.MapFS(fixture.Catalog()))
	out := string(RenderHTML(Compute(w, c)))
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Errorf("a plain-text answer value was not escaped:\n%s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("expected the escaped form in the output:\n%s", out)
	}
}

func TestRenderHTMLRendersMarkdownBody(t *testing.T) {
	files := map[string]string{
		"custos.yaml": fixture.Config(fixture.WorkspaceID, fixture.Commit),
		"answers/" + fixture.TaskA + ".md": "---\ntask: " + fixture.TaskA +
			"\ntask_version: 1.0.0\ntype: markdown\n---\n\n**bold** text\n",
	}
	w, _ := workspace.Load(fixture.MapFS(files))
	c, _ := catalog.Load(fixture.MapFS(fixture.Catalog()))
	out := string(RenderHTML(Compute(w, c)))
	if !strings.Contains(out, "<strong>bold</strong>") {
		t.Errorf("markdown body was not converted to HTML:\n%s", out)
	}
}
