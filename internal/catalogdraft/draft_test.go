package catalogdraft

import (
	"errors"
	"testing"

	"github.com/emeland-io/custos/internal/store"
)

func TestCreateGetList(t *testing.T) {
	st := setup(t)
	base := catalogMain(t, st)

	d, err := Create(st, alice, "Rename the release group")
	if err != nil {
		t.Fatal(err)
	}
	if d.ID == "" || d.Branch != BranchPrefix+d.ID || d.Base != base || d.Commit == "" {
		t.Fatalf("draft = %+v, want Base %s", d, base)
	}
	if d.Title != "Rename the release group" || d.Author != alice {
		t.Errorf("title/author = %q/%v", d.Title, d.Author)
	}

	got, err := Get(st, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if *got != *d {
		t.Errorf("Get = %+v, want %+v", got, d)
	}

	second, err := Create(st, bob, "Add a processor binding")
	if err != nil {
		t.Fatal(err)
	}
	all, err := List(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("List = %+v, want 2 drafts", all)
	}
	ids := map[string]bool{all[0].ID: true, all[1].ID: true}
	if !ids[d.ID] || !ids[second.ID] {
		t.Errorf("List ids %v, want %s and %s", ids, d.ID, second.ID)
	}
}

func TestGetUnknownDraft(t *testing.T) {
	st := setup(t)
	if _, err := Get(st, "0e1f2a3b-4c5d-4e6f-8a7b-9c0d1e2f3a4b"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get of an unknown draft: %v, want ErrNotFound", err)
	}
}

func TestDelete(t *testing.T) {
	st := setup(t)
	d, err := Create(st, alice, "Throwaway")
	if err != nil {
		t.Fatal(err)
	}
	if err := Delete(st, d.ID, alice); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(st, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get after Delete: %v, want ErrNotFound", err)
	}
	if err := Delete(st, d.ID, alice); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second Delete: %v, want ErrNotFound", err)
	}
}

func TestCreateEmptyTitle(t *testing.T) {
	st := setup(t)
	if _, err := Create(st, alice, "   "); !errors.Is(err, ErrInvalid) {
		t.Errorf("Create with an empty title: %v, want ErrInvalid", err)
	}
}

func TestListIgnoresForeignBranches(t *testing.T) {
	st := setup(t)
	base := catalogMain(t, st)
	cat := st.CatalogRepo()
	// A branch under the draft prefix that custos never wrote.
	if err := cat.UpdateRef("refs/heads/"+BranchPrefix+"not-a-uuid", base, ""); err != nil {
		t.Fatal(err)
	}
	all, err := List(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("List = %+v, want none", all)
	}
}
