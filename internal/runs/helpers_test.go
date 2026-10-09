package runs

import (
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/distribute"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/runner"
	"github.com/emeland-io/custos/internal/store"
)

var alice = gitrepo.Signature{Name: "Alice Example", Email: "alice@example.org"}

const (
	ws       = fixture.WorkspaceID
	otherWS  = "6c7d8e9f-0a1b-4c2d-b3e4-f5a6b7c8d9e0"
	answerA  = "answers/" + fixture.TaskA + ".md"
	answerB  = "answers/" + fixture.TaskB + ".md"
	noSuchID = "0e1f2a3b-4c5d-4e6f-8a7b-9c0d1e2f3a4b"
)

// proc is one entry of processors.yaml.
type proc struct{ name, image, timeout string }

// registry returns processors.yaml with procs and bindings (task → name).
func registry(procs []proc, bindings map[string]string) string {
	var b strings.Builder
	b.WriteString("processors:\n")
	for _, p := range procs {
		b.WriteString("  " + p.name + ":\n    image: " + p.image + "\n")
		if p.timeout != "" {
			b.WriteString("    timeout: " + p.timeout + "\n")
		}
	}
	b.WriteString("bindings:\n")
	for _, id := range slices.Sorted(maps.Keys(bindings)) {
		b.WriteString("  " + id + ": " + bindings[id] + "\n")
	}
	return b.String()
}

// env is a store whose catalog holds fixture.Catalog() with TaskB asking
// for a markdown answer, a workspace ws pinned to it, and a started
// Service wired to OnMainMoved the way serve does.
type env struct {
	st      *store.Store
	bl      *blobs.Store
	svc     *Service
	catWork string
}

// newEnv builds an env whose processors.yaml is reg.
func newEnv(t *testing.T, reg string, cfg Config) *env {
	t.Helper()
	e := newStoppedEnv(t, reg, cfg)
	e.svc.Start(t.Context())
	return e
}

// newStoppedEnv is newEnv with a Service that is not started yet.
func newStoppedEnv(t *testing.T, reg string, cfg Config) *env {
	t.Helper()
	data := t.TempDir()
	st, err := store.Open(data, "/nonexistent/custos", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	bl, err := blobs.Open(filepath.Join(data, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{st: st, bl: bl, catWork: gittest.Init(t)}
	cat := fixture.Catalog()
	path := fixture.TaskPath(fixture.TaskB, "1.0.0")
	cat[path] = strings.Replace(cat[path], "answer_type: text\n", "answer_type: markdown\n", 1)
	cat["processors.yaml"] = reg
	e.advanceCatalog(t, cat)
	if err := st.CreateWorkspace(ws, alice); err != nil {
		t.Fatal(err)
	}
	e.svc = e.open(t, cfg)
	return e
}

// open opens a Service on e's store and wires it to OnMainMoved.
func (e *env) open(t *testing.T, cfg Config) *Service {
	t.Helper()
	svc, err := New(e.st, e.bl, runner.New(runner.Config{}), attest.Unverified, cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.st.OnMainMoved(svc.Scan)
	return svc
}

// advanceCatalog commits files on the catalog's main and pushes it.
func (e *env) advanceCatalog(t *testing.T, files map[string]string) string {
	t.Helper()
	c := gittest.Commit(t, e.catWork, files)
	gittest.Run(t, e.catWork, "push", "--quiet", e.st.CatalogRepo().Dir, "main")
	return c
}

// rebind publishes reg as processors.yaml and moves the pins of all
// workspaces to it, as distribution does after a catalog push.
func (e *env) rebind(t *testing.T, reg string) {
	t.Helper()
	e.advanceCatalog(t, map[string]string{"processors.yaml": reg})
	if err := distribute.Reconcile(e.st); err != nil {
		t.Fatal(err)
	}
}

// commit writes files to workspace id's main through the store (which
// fires OnMainMoved). An empty content deletes the file.
func (e *env) commit(t *testing.T, id string, files map[string]string) string {
	t.Helper()
	oid, err := e.st.UpdateWorkspace(id, "refs/heads/main", alice, "test", func(fs.FS) ([]gitrepo.Change, error) {
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
		t.Fatalf("commit to %s: %v", id, err)
	}
	return oid
}

// runs waits for the service and returns the records of id, newest first.
func (e *env) runs(t *testing.T, id string) []Record {
	t.Helper()
	e.svc.Wait()
	rs, err := e.svc.Runs(id)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// markdownAnswer is an answer to TaskB 1.0.0 with body.
func markdownAnswer(body string) string {
	return "---\ntask: " + fixture.TaskB + "\ntask_version: 1.0.0\ntype: markdown\n---\n" + body
}

// textAnswer is an answer of type text.
func textAnswer(taskID, version, value string) string {
	return "---\ntask: " + taskID + "\ntask_version: " + version + "\ntype: text\nvalue: " + value + "\n---\n"
}

// digestOf returns the "sha256:<hex>" part of a proctest image reference.
func digestOf(image string) string {
	_, d, _ := strings.Cut(image, "@")
	return d
}

// wantRun fails unless r has the given state, outcome and reason.
func wantRun(t *testing.T, r Record, state State, outcome Outcome, reason string) {
	t.Helper()
	if r.State != state || r.Outcome != outcome || r.Reason != reason {
		t.Errorf("run %s: state %s, outcome %q, reason %s, error %q; want %s, %q, %s",
			r.ID, r.State, r.Outcome, r.Reason, r.Error, state, outcome, reason)
	}
}

// treeContains reports whether a file below dir in tree contains s.
func treeContains(t *testing.T, tree fs.FS, dir, s string) bool {
	t.Helper()
	found := false
	fs.WalkDir(tree, dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := fs.ReadFile(tree, path)
			found = found || strings.Contains(string(data), s)
		}
		return nil
	})
	return found
}
