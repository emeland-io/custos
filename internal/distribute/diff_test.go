package distribute

import (
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/task"
)

func wantRefs(t *testing.T, what string, got []task.Ref, want ...task.Ref) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func TestCatalogDiff(t *testing.T) {
	st := newStore(t)
	cat := st.CatalogRepo()
	c1 := commitCatalog(t, st, fixture.Catalog())
	// TaskA 2.0.0 merges TaskB; taskD is new and takes TaskB's place in the
	// release group; the binding moves from TaskB to TaskA.
	c2 := commitCatalog(t, st, map[string]string{
		fixture.TaskPath(fixture.TaskA, "2.0.0"): fixture.TaskFile(fixture.TaskA, "2.0.0", fixture.TaskA+"@1.1.0", fixture.TaskB+"@1.0.0"),
		fixture.TaskPath(taskD, "1.0.0"):         fixture.TaskFile(taskD, "1.0.0"),
		"groups/release/group.yaml":              "title: Release\nchildren:\n  - task: " + taskD + "\n",
		"processors.yaml": strings.Replace(fixture.Catalog()["processors.yaml"],
			fixture.TaskB+": host-scanner", fixture.TaskA+": host-scanner", 1),
	})

	d, err := CatalogDiff(cat, c1, c2)
	if err != nil {
		t.Fatal(err)
	}
	wantRefs(t, "new versions", d.NewVersions, task.Ref{ID: fixture.TaskA, Version: "2.0.0"})
	wantRefs(t, "added", d.Added, task.Ref{ID: taskD, Version: "1.0.0"})
	wantRefs(t, "superseded", d.Superseded, task.Ref{ID: fixture.TaskB, Version: "1.0.0"})
	if !d.GroupsChanged || !d.BindingsChanged {
		t.Errorf("groups changed %v, bindings changed %v", d.GroupsChanged, d.BindingsChanged)
	}

	// Moving the pin back (ruling 2.16) mirrors the diff.
	back, err := CatalogDiff(cat, c2, c1)
	if err != nil {
		t.Fatal(err)
	}
	wantRefs(t, "new versions back", back.NewVersions, task.Ref{ID: fixture.TaskA, Version: "1.1.0"})
	wantRefs(t, "added back", back.Added, task.Ref{ID: fixture.TaskB, Version: "1.0.0"})
	wantRefs(t, "superseded back", back.Superseded, task.Ref{ID: taskD, Version: "1.0.0"})
}

func TestCatalogDiffUnchanged(t *testing.T) {
	st := newStore(t)
	c1 := commitCatalog(t, st, fixture.Catalog())
	c2 := commitCatalog(t, st, map[string]string{"README.md": "About this catalog.\n"})
	for _, pair := range [][2]string{{c1, c1}, {c1, c2}} {
		d, err := CatalogDiff(st.CatalogRepo(), pair[0], pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if len(d.NewVersions)+len(d.Added)+len(d.Superseded) != 0 || d.GroupsChanged || d.BindingsChanged {
			t.Errorf("%s..%s: %+v", pair[0], pair[1], d)
		}
		if d.NewVersions == nil || d.Added == nil || d.Superseded == nil {
			t.Errorf("slices must be empty, not nil, so JSON shows []: %+v", d)
		}
	}
}

func TestCatalogDiffDigestChange(t *testing.T) {
	st := newStore(t)
	c1 := commitCatalog(t, st, fixture.Catalog())
	c2 := commitCatalog(t, st, map[string]string{
		"processors.yaml": strings.Replace(fixture.Catalog()["processors.yaml"], fixture.SHA256, strings.Repeat("b", 64), 1),
	})
	d, err := CatalogDiff(st.CatalogRepo(), c1, c2)
	if err != nil {
		t.Fatal(err)
	}
	if !d.BindingsChanged || d.GroupsChanged || len(d.NewVersions)+len(d.Added)+len(d.Superseded) != 0 {
		t.Errorf("%+v", d)
	}
}

func TestCatalogDiffFromEmpty(t *testing.T) {
	st := newStore(t)
	c1 := commitCatalog(t, st, fixture.Catalog())
	d, err := CatalogDiff(st.CatalogRepo(), "", c1)
	if err != nil {
		t.Fatal(err)
	}
	wantRefs(t, "added", d.Added,
		task.Ref{ID: fixture.TaskA, Version: "1.1.0"},
		task.Ref{ID: fixture.TaskB, Version: "1.0.0"})
	if !d.GroupsChanged || !d.BindingsChanged {
		t.Errorf("%+v", d)
	}
}

func TestCatalogDiffUnknownCommit(t *testing.T) {
	st := newStore(t)
	c1 := commitCatalog(t, st, fixture.Catalog())
	if _, err := CatalogDiff(st.CatalogRepo(), c1, strings.Repeat("f", 40)); err == nil {
		t.Error("an unknown commit must be an error")
	}
}
