package proposal

import (
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

const (
	ws      = fixture.WorkspaceID
	digest1 = "sha256:" + fixture.SHA256
	digest2 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
)

var person = gitrepo.Signature{Name: "Jane Doe", Email: "jane@example.org"}

// newStore returns a store whose catalog is fixture.Catalog (TaskB is bound
// to host-scanner) and whose workspace ws has a text answer to TaskB.
func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir(), filepath.Join(t.TempDir(), "custos"), "http://custos.test")
	if err != nil {
		t.Fatal(err)
	}
	cat := st.CatalogRepo()
	var changes []gitrepo.Change
	files := fixture.Catalog()
	for _, p := range slices.Sorted(maps.Keys(files)) {
		changes = append(changes, gitrepo.Change{Path: p, Data: []byte(files[p])})
	}
	oid, err := cat.WriteCommit(gitrepo.CommitRequest{Changes: changes, Author: gitrepo.Bot, Message: "Catalog"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.UpdateRef(mainRef, oid, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(ws, person); err != nil {
		t.Fatal(err)
	}
	answer(t, st, fixture.TaskB, "web-01")
	return st
}

// answer commits a text answer to version 1.0.0 of task id on main.
func answer(t *testing.T, st *store.Store, id, value string) {
	t.Helper()
	data := "---\ntask: " + id + "\ntask_version: 1.0.0\ntype: text\nvalue: " + value + "\n---\n"
	commitMain(t, st, map[string]string{"answers/" + id + ".md": data})
}

// commitMain commits files on main through the store, authored by person.
func commitMain(t *testing.T, st *store.Store, files map[string]string) string {
	t.Helper()
	oid, err := st.UpdateWorkspace(ws, mainRef, person, "Change by hand", func(fs.FS) ([]gitrepo.Change, error) {
		var cs []gitrepo.Change
		for p, c := range files {
			cs = append(cs, gitrepo.Change{Path: p, Data: []byte(c)})
		}
		return cs, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

// textTask returns an output task of answer type text.
func textTask(key, title string) contract.OutputTask {
	return contract.OutputTask{MatchKey: key, Title: title, Body: "About " + key + ".", AnswerType: task.AnswerText}
}

// doc returns an output document whose content names subject.
func doc(key, subject string) contract.OutputDocument {
	return contract.OutputDocument{MatchKey: key, Name: key, MediaType: "application/json",
		Content: []byte(`{"subject":"` + subject + `"}`)}
}

// plan matches out for the answer to task id on the current main, as a run
// of host-scanner would.
func plan(t *testing.T, st *store.Store, id string, out contract.Output) *match.Changes {
	t.Helper()
	w, c, err := st.Load(ws, "")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := match.Plan(match.Run{
		Workspace: w, Catalog: c, Task: task.Ref{ID: id, Version: "1.0.0"},
		Processor: "host-scanner", Digest: digest1, AnswerCommit: mainOID(t, st), Verifier: attest.Unverified,
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

// propose plans out for task id and writes the proposal.
func propose(t *testing.T, st *store.Store, id, digest string, out contract.Output) string {
	t.Helper()
	branch, err := Write(st, ws, id, digest, plan(t, st, id, out), "Propose output of host-scanner for task "+id)
	if err != nil {
		t.Fatal(err)
	}
	return branch
}

func repoOf(t *testing.T, st *store.Store) *gitrepo.Repo {
	t.Helper()
	repo, err := st.WorkspaceRepo(ws)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func mainOID(t *testing.T, st *store.Store) string {
	t.Helper()
	return resolve(t, st, mainRef)
}

// resolve returns the commit ref points to, or "".
func resolve(t *testing.T, st *store.Store, ref string) string {
	t.Helper()
	oid, _, err := repoOf(t, st).ResolveRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

// branches returns the short names of the output proposal branches.
func branches(t *testing.T, st *store.Store) []string {
	t.Helper()
	refs, err := repoOf(t, st).Refs(refPrefix)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, ref := range slices.Sorted(maps.Keys(refs)) {
		names = append(names, strings.TrimPrefix(ref, headsPrefix))
	}
	return names
}

// mainFiles returns the paths below generated/, documents/ and answers/ on
// main, sorted.
func mainFiles(t *testing.T, st *store.Store) []string {
	t.Helper()
	out := gittest.Run(t, repoOf(t, st).Dir, "ls-tree", "-r", "--name-only", "main")
	var ps []string
	for _, p := range strings.Split(out, "\n") {
		if p != "custos.yaml" && p != "" {
			ps = append(ps, p)
		}
	}
	return ps
}

func logOf(t *testing.T, st *store.Store, format, rev string) string {
	t.Helper()
	return gittest.Run(t, repoOf(t, st).Dir, "log", "-1", "--format="+format, rev)
}

// summary renders items as "kind key action from→to" lines.
func summary(items []match.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, fmt.Sprintf("%s %s %s %s→%s", it.Kind, it.MatchKey, it.Action, it.From, it.To))
	}
	return out
}

func wantSummary(t *testing.T, items []match.Item, want ...string) {
	t.Helper()
	if got := summary(items); !slices.Equal(got, want) {
		t.Errorf("items:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// idOf returns the id of the item with kind and key.
func idOf(t *testing.T, items []match.Item, kind, key string) string {
	t.Helper()
	for _, it := range items {
		if it.Kind == kind && it.MatchKey == key {
			return it.ID
		}
	}
	t.Fatalf("no item %s:%s in %v", kind, key, summary(items))
	return ""
}
