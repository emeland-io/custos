package merge

import (
	"io/fs"
	"maps"
	"slices"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/workspace"
)

var alice = gitrepo.Signature{Name: "Alice Example", Email: "alice@example.org"}

const (
	ws          = fixture.WorkspaceID
	forkID      = "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
	unknownTask = "0e1f2a3b-4c5d-4e6f-8a7b-9c0d1e2f3a4b" // in no catalog
	whatIf      = "refs/heads/what-if"
	answerA     = "answers/" + fixture.TaskA + ".md"
	answerB     = "answers/" + fixture.TaskB + ".md"
)

// env is a store whose catalog main holds fixture.Catalog() and whose
// workspace ws is pinned to that first catalog commit.
type env struct {
	st      *store.Store
	catWork string   // work tree the catalog is pushed from
	cat     []string // catalog main commits, oldest first
}

func setup(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(t.TempDir(), "/nonexistent/custos", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{st: st, catWork: gittest.Init(t)}
	e.advanceCatalog(t, fixture.Catalog())
	if err := st.CreateWorkspace(ws, alice); err != nil {
		t.Fatal(err)
	}
	return e
}

// advanceCatalog commits files on the catalog's main, pushes it and returns
// the commit.
func (e *env) advanceCatalog(t *testing.T, files map[string]string) string {
	t.Helper()
	c := gittest.Commit(t, e.catWork, files)
	gittest.Run(t, e.catWork, "push", "--quiet", e.st.CatalogRepo().Dir, "main")
	e.cat = append(e.cat, c)
	return c
}

// draftCatalog commits files on the catalog branch draft, starting at from.
// The branch never reaches main.
func (e *env) draftCatalog(t *testing.T, from string, files map[string]string) string {
	t.Helper()
	gittest.Run(t, e.catWork, "checkout", "-q", "-b", "draft", from)
	c := gittest.Commit(t, e.catWork, files)
	gittest.Run(t, e.catWork, "push", "--quiet", e.st.CatalogRepo().Dir, "draft")
	gittest.Run(t, e.catWork, "checkout", "-q", "main")
	return c
}

// config is a custos.yaml for workspace id, pinned to pin.
func (e *env) config(id, pin string, frozen bool) string {
	s := "workspace: " + id + "\ncatalog:\n  url: " + e.st.CatalogURL() + "\n  commit: " + pin + "\n"
	if frozen {
		s += "frozen: true\n"
	}
	return s
}

// commitFiles commits files to ref of workspace id through the store, which
// validates commits to main. An empty content deletes the file.
func commitFiles(t *testing.T, st *store.Store, id, ref string, files map[string]string) string {
	t.Helper()
	oid, err := st.UpdateWorkspace(id, ref, alice, "test", func(fs.FS) ([]gitrepo.Change, error) {
		var cs []gitrepo.Change
		for _, p := range slices.Sorted(maps.Keys(files)) {
			if files[p] == "" {
				cs = append(cs, gitrepo.Change{Path: p, Delete: true})
			} else {
				cs = append(cs, gitrepo.Change{Path: p, Data: []byte(files[p])})
			}
		}
		return cs, nil
	})
	if err != nil {
		t.Fatalf("commit to %s of %s: %v", ref, id, err)
	}
	return oid
}

func repoOf(t *testing.T, st *store.Store, id string) *gitrepo.Repo {
	t.Helper()
	r, err := st.WorkspaceRepo(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// ref returns the commit of a ref that must exist.
func ref(t *testing.T, st *store.Store, id, name string) string {
	t.Helper()
	oid, ok, err := repoOf(t, st, id).ResolveRef(name)
	if err != nil || !ok {
		t.Fatalf("ref %s of %s: ok=%v err=%v", name, id, ok, err)
	}
	return oid
}

// file returns a file of workspace id at rev; ok is false when it is missing.
func file(t *testing.T, st *store.Store, id, rev, path string) (string, bool) {
	t.Helper()
	data, ok, err := repoOf(t, st, id).ReadFile(rev, path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), ok
}

// gitOut runs git in the repository of workspace id.
func gitOut(t *testing.T, st *store.Store, id string, args ...string) string {
	t.Helper()
	return gittest.Run(t, repoOf(t, st, id).Dir, args...)
}

func readConfig(t *testing.T, e *env, id, rev string) workspace.Config {
	t.Helper()
	data, ok := file(t, e.st, id, rev, configPath)
	if !ok {
		t.Fatalf("custos.yaml missing at %s", rev)
	}
	cfg, err := decodeConfig([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// textAnswer is an answer of type text.
func textAnswer(taskID, version, value string) string {
	return "---\ntask: " + taskID + "\ntask_version: " + version + "\ntype: text\nvalue: " + value + "\n---\n"
}

func hasRule(ps []problem.Problem, rule string) bool {
	return slices.ContainsFunc(ps, func(p problem.Problem) bool { return p.Rule == rule })
}
