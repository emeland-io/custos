package catalogdraft

import (
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
)

var alice = gitrepo.Signature{Name: "Alice Example", Email: "alice@example.org"}
var bob = gitrepo.Signature{Name: "Bob Example", Email: "bob@example.org"}

// setup returns a store whose catalog main holds fixture.Catalog().
func setup(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir(), "/nonexistent/custos", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	cat := s.CatalogRepo()
	var changes []gitrepo.Change
	for p, c := range fixture.Catalog() {
		changes = append(changes, gitrepo.Change{Path: p, Data: []byte(c)})
	}
	commit, err := cat.WriteCommit(gitrepo.CommitRequest{Changes: changes, Author: alice, Message: "seed catalog"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.UpdateRef(mainRef, commit, ""); err != nil {
		t.Fatal(err)
	}
	return s
}

func catalogMain(t *testing.T, st *store.Store) string {
	t.Helper()
	oid, ok, err := st.CatalogRepo().ResolveRef(mainRef)
	if err != nil || !ok {
		t.Fatalf("catalog main: ok=%v err=%v", ok, err)
	}
	return oid
}
