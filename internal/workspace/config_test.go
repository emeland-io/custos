package workspace

import (
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
)

func TestMarshalConfig(t *testing.T) {
	c := Config{Workspace: fixture.WorkspaceID, Catalog: CatalogPin{URL: "https://custos.example.org/git/catalog.git", Commit: fixture.Commit}}
	data, err := MarshalConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	if want := fixture.Config(fixture.WorkspaceID, fixture.Commit); string(data) != want {
		t.Errorf("got:\n%s\nwant:\n%s", data, want)
	}
	c.Frozen = true
	data, err = MarshalConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	f := fixture.Workspace()
	f[ConfigPath] = string(data)
	w, ps := Load(fixture.MapFS(f))
	fixture.WantNone(t, ps)
	if w.Config != c {
		t.Errorf("round trip: %+v", w.Config)
	}
}
