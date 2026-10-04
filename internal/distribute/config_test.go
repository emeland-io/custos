package distribute

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/workspace"
)

const baseConfig = "workspace: " + fixture.WorkspaceID + "\ncatalog:\n  url: https://custos.example.org/git/catalog.git\n  commit: " + fixture.Commit + "\n"

func TestSetConfigKeepsOtherFields(t *testing.T) {
	out, changed, err := setConfig([]byte(baseConfig), func(c *workspace.Config) { c.Frozen = true })
	if err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	if want := baseConfig + "frozen: true\n"; string(out) != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	c, err := decodeConfig(out)
	if err != nil {
		t.Fatal(err)
	}
	if c.Workspace != fixture.WorkspaceID || c.Catalog.URL != "https://custos.example.org/git/catalog.git" || c.Catalog.Commit != fixture.Commit || !c.Frozen {
		t.Errorf("decoded %+v", c)
	}
}

func TestSetConfigUnfreezeDropsFlag(t *testing.T) {
	out, changed, err := setConfig([]byte(baseConfig+"frozen: true\n"), func(c *workspace.Config) { c.Frozen = false })
	if err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	if string(out) != baseConfig {
		t.Errorf("got:\n%s\nwant:\n%s", out, baseConfig)
	}
}

func TestSetConfigUnchanged(t *testing.T) {
	out, changed, err := setConfig([]byte(baseConfig), func(c *workspace.Config) { c.Catalog.Commit = fixture.Commit })
	if err != nil || changed || out != nil {
		t.Errorf("out %q, changed %v, err %v", out, changed, err)
	}
}

func TestSetConfigRejectsUnknownFields(t *testing.T) {
	_, _, err := setConfig([]byte(baseConfig+"owner: team-a\n"), func(c *workspace.Config) { c.Frozen = true })
	if err == nil || !strings.Contains(err.Error(), "owner") {
		t.Errorf("err %v, want an error naming the unknown field", err)
	}
}

func TestSetConfigNeedsCommit(t *testing.T) {
	_, _, err := setConfig([]byte("workspace: "+fixture.WorkspaceID+"\n"), func(c *workspace.Config) { c.Frozen = true })
	if err == nil || !strings.Contains(err.Error(), "catalog.commit is missing") {
		t.Errorf("err %v", err)
	}
}

func TestEditConfig(t *testing.T) {
	tree := fixture.MapFS(fixture.Workspace())
	head := strings.Repeat("a", 40)
	changes, err := editConfig(tree, func(c *workspace.Config) { c.Catalog.Commit = head })
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "custos.yaml" || changes[0].Delete || !strings.Contains(string(changes[0].Data), "commit: "+head+"\n") {
		t.Fatalf("changes %+v", changes)
	}
	changes, err = editConfig(tree, func(*workspace.Config) {})
	if err != nil || changes != nil {
		t.Errorf("no-op edit: changes %+v, err %v", changes, err)
	}
	if _, err := editConfig(fixture.MapFS(map[string]string{}), func(*workspace.Config) {}); err == nil {
		t.Error("a tree without custos.yaml must be an error")
	}
}
