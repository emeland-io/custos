package store

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
)

func mainOfCatalog(t *testing.T, s *Store) string {
	t.Helper()
	oid, ok, err := s.CatalogRepo().ResolveRef(mainRef)
	if err != nil || !ok {
		t.Fatalf("catalog main: ok=%v err=%v", ok, err)
	}
	return oid
}

func TestUpdateCatalogMain(t *testing.T) {
	s, first := open(t)
	newTask := fixture.TaskPath(fixture.TaskC, "1.0.0")
	commit, err := s.UpdateCatalog(mainRef, jane, "Add task C",
		put(map[string]string{newTask: fixture.TaskFile(fixture.TaskC, "1.0.0")}))
	if err != nil {
		t.Fatal(err)
	}
	if commit == first || mainOfCatalog(t, s) != commit {
		t.Fatalf("main %s, commit %s, first %s", mainOfCatalog(t, s), commit, first)
	}
	if data, ok, _ := s.CatalogRepo().ReadFile(commit, newTask); !ok || string(data) != fixture.TaskFile(fixture.TaskC, "1.0.0") {
		t.Errorf("task file %q", data)
	}
	// The same content again makes no commit.
	again, err := s.UpdateCatalog(mainRef, jane, "again",
		put(map[string]string{newTask: fixture.TaskFile(fixture.TaskC, "1.0.0")}))
	if err != nil || again != commit {
		t.Errorf("no-op update: %s, %v, want %s", again, err, commit)
	}
}

func TestUpdateCatalogMainRejectsInvalid(t *testing.T) {
	s, _ := open(t)
	// id in the frontmatter does not match the path (rule 4).
	bad := "---\nid: " + fixture.TaskA + "\nversion: 1.0.0\ntitle: Wrong path\nanswer_type: text\n---\n\nBody.\n"
	_, err := s.UpdateCatalog(mainRef, jane, "bad",
		put(map[string]string{fixture.TaskPath(fixture.TaskC, "1.0.0"): bad}))
	var rejected *RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want *RejectedError", err)
	}
	found := false
	for _, p := range rejected.Problems {
		if p.Rule == problem.RulePath {
			found = true
		}
	}
	if !found {
		t.Errorf("problems %+v, want one with rule %s", rejected.Problems, problem.RulePath)
	}
	if mainOfCatalog(t, s) == "" {
		t.Error("main moved")
	}
}

func TestUpdateCatalogBranchSkipsValidation(t *testing.T) {
	s, first := open(t)
	branch := "refs/heads/draft-x"
	// Missing title: catalog.Check would reject this on main, but a
	// non-main ref is never validated here (ruling 4.13).
	invalid := "---\nid: " + fixture.TaskC + "\nversion: 1.0.0\nanswer_type: text\n---\n\nBody.\n"
	commit, err := s.UpdateCatalog(branch, jane, "draft edit",
		put(map[string]string{fixture.TaskPath(fixture.TaskC, "1.0.0"): invalid}))
	if err != nil {
		t.Fatal(err)
	}
	if data, ok, _ := s.CatalogRepo().ReadFile(commit, fixture.TaskPath(fixture.TaskC, "1.0.0")); !ok || string(data) != invalid {
		t.Errorf("branch file %q", data)
	}
	if mainOfCatalog(t, s) != first {
		t.Error("main moved by a branch update")
	}
	// The branch starts from main's current tree: a file only main has is
	// visible to the edit closure.
	_, err = s.UpdateCatalog(branch, jane, "check base", func(tree fs.FS) ([]gitrepo.Change, error) {
		if _, err := fs.Stat(tree, fixture.TaskPath(fixture.TaskA, "1.0.0")); err != nil {
			t.Errorf("branch tree lacks main's existing task: %v", err)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(branch, "refs/heads/") {
		t.Fatal("test precondition")
	}
}
