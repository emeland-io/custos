package distribute

import (
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/workspace"
)

const (
	wsA   = fixture.WorkspaceID                    // sorts before wsB
	wsB   = "6c7d8e9f-0a1b-4c2d-b3e4-f5a6b7c8d9e0" // a second workspace
	taskD = "b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e" // a task the fixture catalog lacks
)

var person = gitrepo.Signature{Name: "Jane Doe", Email: "jane@example.org"}

// newStore opens a store in a temporary directory. Its hooks never run: the
// tests move refs with plumbing, not with pushes.
func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir(), filepath.Join(t.TempDir(), "custos"), "http://custos.test")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// commitCatalog commits files (and the removal of the paths in del) on top
// of the catalog's main and moves main to it without validation. It returns
// the new commit.
func commitCatalog(t *testing.T, st *store.Store, files map[string]string, del ...string) string {
	t.Helper()
	cat := st.CatalogRepo()
	old, ok, err := cat.ResolveRef("refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	req := gitrepo.CommitRequest{Author: gitrepo.Bot, Message: "Change the catalog"}
	if ok {
		req.Base, req.Parents = old, []string{old}
	} else {
		old = ""
	}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		req.Changes = append(req.Changes, gitrepo.Change{Path: p, Data: []byte(files[p])})
	}
	for _, p := range del {
		req.Changes = append(req.Changes, gitrepo.Change{Path: p, Delete: true})
	}
	oid, err := cat.WriteCommit(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.UpdateRef("refs/heads/main", oid, old); err != nil {
		t.Fatal(err)
	}
	return oid
}

// newTaskAVersion publishes version of fixture.TaskA after previous.
func newTaskAVersion(t *testing.T, st *store.Store, version, previous string) string {
	t.Helper()
	return commitCatalog(t, st, map[string]string{
		fixture.TaskPath(fixture.TaskA, version): fixture.TaskFile(fixture.TaskA, version, fixture.TaskA+"@"+previous),
	})
}

func createWorkspace(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if err := st.CreateWorkspace(id, person); err != nil {
		t.Fatal(err)
	}
}

func wsRepo(t *testing.T, st *store.Store, id string) *gitrepo.Repo {
	t.Helper()
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// resolve returns the commit ref points to, or "" when it does not exist.
func resolve(t *testing.T, repo *gitrepo.Repo, ref string) string {
	t.Helper()
	oid, ok, err := repo.ResolveRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return ""
	}
	return oid
}

func configAt(t *testing.T, repo *gitrepo.Repo, rev string) workspace.Config {
	t.Helper()
	c, err := readConfig(repo, rev)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// pinBranches returns the short names of the pin proposal branches, sorted.
func pinBranches(t *testing.T, repo *gitrepo.Repo) []string {
	t.Helper()
	refs, err := repo.Refs("refs/heads/custos/pin/")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, ref := range slices.Sorted(maps.Keys(refs)) {
		names = append(names, strings.TrimPrefix(ref, "refs/heads/"))
	}
	return names
}

func allRefs(t *testing.T, repo *gitrepo.Repo) map[string]string {
	t.Helper()
	refs, err := repo.Refs("refs/")
	if err != nil {
		t.Fatal(err)
	}
	return refs
}

func authorOf(t *testing.T, repo *gitrepo.Repo, rev string) string {
	t.Helper()
	return gittest.Run(t, repo.Dir, "log", "-1", "--format=%an <%ae>", rev)
}

func committerOf(t *testing.T, repo *gitrepo.Repo, rev string) string {
	t.Helper()
	return gittest.Run(t, repo.Dir, "log", "-1", "--format=%cn <%ce>", rev)
}

func parentCount(t *testing.T, repo *gitrepo.Repo, rev string) int {
	t.Helper()
	return len(strings.Fields(gittest.Run(t, repo.Dir, "rev-list", "--parents", "-n", "1", rev))) - 1
}

// markFrozen sets frozen: true on main without reconciling, as a push would.
func markFrozen(t *testing.T, st *store.Store, id string) {
	t.Helper()
	_, err := st.UpdateWorkspace(id, "refs/heads/main", person, "Freeze by hand", func(tree fs.FS) ([]gitrepo.Change, error) {
		return editConfig(tree, func(c *workspace.Config) { c.Frozen = true })
	})
	if err != nil {
		t.Fatal(err)
	}
}

// answerA commits fixture.AnswerFile (TaskA 1.0.0) on main.
func answerA(t *testing.T, st *store.Store, id string) {
	t.Helper()
	_, err := st.UpdateWorkspace(id, "refs/heads/main", person, "Answer task A", func(fs.FS) ([]gitrepo.Change, error) {
		return []gitrepo.Change{{Path: "answers/" + fixture.TaskA + ".md", Data: []byte(fixture.AnswerFile)}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// breakConfig commits an unreadable custos.yaml on main, bypassing
// validation, like a manual edit on disk (spec §7).
func breakConfig(t *testing.T, st *store.Store, id string) {
	t.Helper()
	repo := wsRepo(t, st, id)
	main := resolve(t, repo, "refs/heads/main")
	oid, err := repo.WriteCommit(gitrepo.CommitRequest{
		Base: main, Parents: []string{main}, Author: gitrepo.Bot, Message: "Break custos.yaml",
		Changes: []gitrepo.Change{{Path: "custos.yaml", Data: []byte("workspace: [\n")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRef("refs/heads/main", oid, main); err != nil {
		t.Fatal(err)
	}
}
