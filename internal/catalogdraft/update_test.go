package catalogdraft

import (
	"io/fs"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
)

func TestUpdate(t *testing.T) {
	st := setup(t)
	d, err := Create(st, alice, "Edit a task")
	if err != nil {
		t.Fatal(err)
	}
	newTask := fixture.TaskPath(fixture.TaskC, "1.0.0")
	commit, err := Update(st, d.ID, alice, "Add task C", func(fs.FS) ([]gitrepo.Change, error) {
		return []gitrepo.Change{{Path: newTask, Data: []byte(fixture.TaskFile(fixture.TaskC, "1.0.0"))}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit == d.Commit {
		t.Fatal("branch did not move")
	}
	got, err := Get(st, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != commit {
		t.Errorf("Get.Commit = %s, want %s", got.Commit, commit)
	}
	if data, ok, _ := st.CatalogRepo().ReadFile(commit, newTask); !ok || string(data) != fixture.TaskFile(fixture.TaskC, "1.0.0") {
		t.Errorf("task file %q", data)
	}
	// main is untouched.
	if catalogMain(t, st) == commit {
		t.Error("main moved by Update")
	}
	// The draft's metadata marker survives the edit.
	if _, ok, err := st.CatalogRepo().ReadFile(commit, metaPath); err != nil || !ok {
		t.Errorf("metadata marker missing after Update: ok=%v err=%v", ok, err)
	}
}

func TestUpdateUnknownDraft(t *testing.T) {
	st := setup(t)
	_, err := Update(st, "0e1f2a3b-4c5d-4e6f-8a7b-9c0d1e2f3a4b", alice, "x", func(fs.FS) ([]gitrepo.Change, error) { return nil, nil })
	if err == nil {
		t.Error("Update of an unknown draft must fail")
	}
}
