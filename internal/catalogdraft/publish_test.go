package catalogdraft

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/merge"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

// newVersionEdit returns an edit closure that adds the next version of
// fixture.TaskA with the given title.
func newVersionEdit(title string) func(fs.FS) ([]gitrepo.Change, error) {
	return func(tree fs.FS) ([]gitrepo.Change, error) {
		return catalog.NewVersionChanges(tree, fixture.TaskA, semver.Minor, title, "Body.\n", task.AnswerText, nil)
	}
}

func TestPublishFastForward(t *testing.T) {
	st := setup(t)
	d, err := Create(st, alice, "Bump task A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Update(st, d.ID, alice, "New version", newVersionEdit("A, 1.2.0")); err != nil {
		t.Fatal(err)
	}

	res, err := Publish(st, d.ID, alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 || res.Commit == "" {
		t.Fatalf("result = %+v, want a clean publish", res)
	}
	if catalogMain(t, st) != res.Commit {
		t.Errorf("main = %s, want %s", catalogMain(t, st), res.Commit)
	}
	// The published tree never carries the draft's metadata marker.
	if _, ok, _ := st.CatalogRepo().ReadFile(res.Commit, metaPath); ok {
		t.Error("published tree carries the draft metadata marker")
	}
	newVersion := fixture.TaskPath(fixture.TaskA, "1.2.0")
	if _, ok, _ := st.CatalogRepo().ReadFile(res.Commit, newVersion); !ok {
		t.Errorf("%s missing from main after publish", newVersion)
	}
	// The draft branch is gone.
	if _, err := Get(st, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get after publish: %v, want ErrNotFound", err)
	}
	fixture.WantNone(t, catalog.Check(mainFS(t, st)))
}

// mainFS returns the catalog's current main tree.
func mainFS(t *testing.T, st *store.Store) fs.FS {
	t.Helper()
	fsys, err := st.CatalogRepo().TreeFS(catalogMain(t, st))
	if err != nil {
		t.Fatal(err)
	}
	return fsys
}

// TestPublishConflict is the architecture note's "two drafts editing the
// same task" scenario: both drafts start from the same main (TaskA current
// at 1.1.0) and each adds its own, differently-titled 1.2.0 of TaskA. The
// first publish is a clean fast-forward. The second then conflicts on
// exactly tasks/<TaskA>/1.2.0.md (an add/add conflict: the merge base has
// no such file, and the two sides added different content at the same
// path), is resolved with side "theirs", and republishes cleanly.
func TestPublishConflict(t *testing.T) {
	st := setup(t)
	first, err := Create(st, alice, "A via first draft")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Create(st, bob, "A via second draft")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Update(st, first.ID, alice, "1.2.0", newVersionEdit("First draft's title")); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(st, second.ID, bob, "1.2.0", newVersionEdit("Second draft's title")); err != nil {
		t.Fatal(err)
	}

	if _, err := Publish(st, first.ID, alice, nil); err != nil {
		t.Fatal(err)
	}

	path := fixture.TaskPath(fixture.TaskA, "1.2.0")
	res, err := Publish(st, second.ID, bob, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != "" || len(res.Conflicts) != 1 {
		t.Fatalf("result = %+v, want exactly one conflict and no commit", res)
	}
	conflict := res.Conflicts[0]
	if conflict.Path != path || conflict.Kind != kindTask || conflict.Ours == nil || conflict.Theirs == nil {
		t.Fatalf("conflict = %+v, want path %s, kind %s", conflict, path, kindTask)
	}
	// The draft is untouched by a conflicting publish.
	if _, err := Get(st, second.ID); err != nil {
		t.Errorf("Get after a conflicting publish: %v", err)
	}

	res, err = Publish(st, second.ID, bob, map[string]merge.Resolution{path: {Side: merge.SideTheirs}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 || res.Commit == "" {
		t.Fatalf("result after resolution = %+v", res)
	}
	data, ok, err := st.CatalogRepo().ReadFile(res.Commit, path)
	if err != nil || !ok || string(data) != string(conflict.Theirs) {
		t.Errorf("published content = %q (ok=%v), want %q", data, ok, conflict.Theirs)
	}
	fixture.WantNone(t, catalog.Check(mainFS(t, st)))
}

// TestPublishImmutabilityViolation: a hand-built edit rewrites the content
// of an already-published task version file. catalog.Check alone would not
// catch this (the file, taken on its own, is still a well-formed task
// version); only catalog.CheckImmutable, which rules.CheckCatalogUpdate also
// runs, compares it against the old main tree.
func TestPublishImmutabilityViolation(t *testing.T) {
	st := setup(t)
	d, err := Create(st, alice, "Rewrite history")
	if err != nil {
		t.Fatal(err)
	}
	existing := fixture.TaskPath(fixture.TaskA, "1.0.0")
	rewritten := "---\nid: " + fixture.TaskA + "\nversion: 1.0.0\ntitle: Rewritten\nanswer_type: text\n---\n\nRewritten body.\n"
	if _, err := Update(st, d.ID, alice, "rewrite", func(fs.FS) ([]gitrepo.Change, error) {
		return []gitrepo.Change{{Path: existing, Data: []byte(rewritten)}}, nil
	}); err != nil {
		t.Fatal(err)
	}

	_, err = Publish(st, d.ID, alice, nil)
	var rejected *store.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want *store.RejectedError", err)
	}
	found := false
	for _, p := range rejected.Problems {
		if p.Rule == problem.RuleImmutable {
			found = true
		}
	}
	if !found {
		t.Errorf("problems %+v, want one with rule %s", rejected.Problems, problem.RuleImmutable)
	}
	// The draft is untouched by a rejected publish.
	if _, err := Get(st, d.ID); err != nil {
		t.Errorf("Get after a rejected publish: %v", err)
	}
}
