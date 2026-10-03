# Phase 2c (Catalog distribution, freeze, pin proposals) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When the catalog's `main` advances, every workspace follows it: unfrozen workspaces get a `custos-bot` commit that moves their pin, frozen workspaces get exactly one pin proposal branch `custos/pin/<catalog-commit>` that an engineer accepts or rejects; freeze, unfreeze and the proposals are available over the REST API.

**Architecture:** A new package `internal/distribute` does all of it on top of `internal/store` (plan 2a): it rewrites `custos.yaml` through `store.UpdateWorkspace`, moves `main` with `store.SetRef` (both validate and compare-and-swap), and deletes branches with `gitrepo.Repo.DeleteRef` under `store.Lock`. `Reconcile` is idempotent and is called by `custos serve` once at start and after every push to `catalog.git` through `server.OnCatalogPush` (ruling 2.5). `distribute.Register` adds the endpoints to the API of plan 2b.

**Tech Stack:** Go 1.26, the `git` binary (≥ 2.38), `go.yaml.in/yaml/v3`; standard library otherwise.

**Spec:** [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md), §4.1 (catalog change → workspaces), §4.5 (concurrency), §7 (error handling) and §11 (rulings, which override earlier sections; 2.3, 2.5, 2.16 matter most here). Shared names and conventions: [docs/superpowers/plans/2026-10-03-phase-2-architecture.md](2026-10-03-phase-2-architecture.md).

**Requires:** plans 2a and 2b executed first. This plan uses `gitrepo.Signature`, `gitrepo.Bot`, `gitrepo.ErrRefMoved`, `gitrepo.Change`, `gitrepo.CommitRequest`, `Repo.ResolveRef/ReadFile/WriteCommit/UpdateRef/DeleteRef/Refs`, `problem.RulePin`, the whole `store` API, `server.New`, `Server.OnCatalogPush`, `Server.Handler`, `blobs.Open`, `api.New`, `API.Handle`, `API.Handler`, `api.Author`, `api.WriteJSON` and `api.WriteError` exactly as the architecture note declares them. None of them exist on `main` before 2a and 2b; if a signature differs from the note, adapt the call and note it in the task's commit message.

## Global Constraints

- Module `github.com/emeland-io/custos`, Go 1.26. Third-party modules stay `go.yaml.in/yaml/v3`, `golang.org/x/mod`, `github.com/google/uuid`.
- Git ≥ 2.38 at runtime (ruling 2.9). Tests use real git in temp dirs.
- Bot identity: `gitrepo.Bot = gitrepo.Signature{Name: "custos-bot", Email: "custos-bot@localhost"}`. The committer of every server-side commit is `gitrepo.Bot`; the author is the acting person, or `gitrepo.Bot` for automatic changes (pin moves, proposals).
- Pin proposal branches are named `custos/pin/<catalog-commit>` (full commit hash).
- Exported names of `internal/distribute` exactly as in the architecture note: `Reconcile`, `Freeze`, `Unfreeze`, `PinProposal`, `Proposals`, `Accept`, `Reject`, `Diff`, `CatalogDiff` — plus `Register` (see "Decisions beyond the architecture note").
- Server-side writes go through `store` (one lock per repository, validation of `main`, compare-and-swap of the ref; ruling 2.3). Hooks never write (ruling 2.5).
- A pin may move to any commit on the catalog's `main`, also an older one (ruling 2.16).
- Editing `custos.yaml` decodes it into `workspace.Config`, changes the field, and encodes it with `yaml`; an unknown field is an error, never silently dropped.
- REST: JSON bodies; error body `{"error": "<message>"}`; 400 malformed request, 401 missing/invalid `X-Custos-Author` on a write, 404 unknown workspace or proposal, 409 conflict, 422 validation problems, 500 otherwise. Every write endpoint requires `X-Custos-Author: Name <email>` (ruling 2.6).
- Endpoints: `POST /api/workspaces/{id}/freeze`, `POST /api/workspaces/{id}/unfreeze`, `GET /api/workspaces/{id}/proposals`, `POST /api/workspaces/{id}/proposals/accept`, `POST /api/workspaces/{id}/proposals/reject` (body `{"branch"}`), `GET /api/workspaces/{id}/pin-diff`.

## Review Focus

1. **Two catalog pushes in quick succession.** Their reconciles overlap; the result must match the newest catalog `main` (pin at the newest commit, one proposal for it), never an older one and never a deadlock (test `TestConcurrentReconciles` in Task 3).
2. **A rejected proposal and a server restart.** Reconcile runs again at start; a rejected proposal must stay closed until the catalog moves on (test `TestRejectStaysRejected` in Task 5).
3. **An engineer answers while a proposal is open.** `main` has moved past the proposal's base; accepting must keep the new answer and still move the pin (test `TestAcceptDivergedMain` in Task 5).
4. **A `custos.yaml` with a field custos does not know, or the `frozen` flag.** The bot commit must keep every field and fail loudly instead of dropping an unknown one (tests in Task 1).
5. **One workspace that cannot be updated** (inconsistent on disk, §7). The other workspaces must still be updated, the error must name the workspace, and `serve` must keep running (tests `TestReconcileContinuesAfterFailingWorkspace` in Task 3 and `TestStartDistributionCatchesUp` in Task 7).

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/distribute/config.go` | Package doc; read, change and write `custos.yaml` without losing fields |
| `internal/distribute/diff.go` | `Diff`, `CatalogDiff`: what changes between two catalog commits |
| `internal/distribute/distribute.go` | `Reconcile` and the per-workspace reconcile (pin move, proposal branch, pruning) |
| `internal/distribute/freeze.go` | `Freeze`, `Unfreeze` |
| `internal/distribute/proposals.go` | `PinProposal`, `Proposals`, `Accept`, `Reject`, rejection marks |
| `internal/distribute/api.go` | `Register`: the six REST endpoints |
| `internal/distribute/*_test.go` | Tests on real repositories in temp dirs; `helpers_test.go` holds the shared helpers |
| `cmd/custos/distribute.go` | `startDistribution`: wiring for `serve` |
| `cmd/custos/distribute_test.go` | End-to-end: push to `catalog.git` over HTTP → workspaces updated |
| `cmd/custos/serve.go` | One call to `startDistribution` in `openServer` |
| `README.md`, spec §11.2 | User documentation and rulings |

---

### Task 1: Rewrite `custos.yaml` without losing fields

**Files:**
- Create: `internal/distribute/config.go`
- Test: `internal/distribute/config_test.go`

**Interfaces:**
- Consumes: `workspace.Config` (fields `Workspace`, `Catalog.URL`, `Catalog.Commit`, `Frozen` with tag `frozen,omitempty`), `frontmatter.DecodeStrict(data []byte, v any) error`, `gitrepo.Change`, `(*gitrepo.Repo).ReadFile(rev, path string) ([]byte, bool, error)` (2a).
- Produces (unexported, used by Tasks 2–6):
  - `const configPath = "custos.yaml"`
  - `func decodeConfig(data []byte) (workspace.Config, error)`
  - `func setConfig(data []byte, change func(*workspace.Config)) (out []byte, changed bool, err error)` — `out` nil when unchanged
  - `func editConfig(tree fs.FS, change func(*workspace.Config)) ([]gitrepo.Change, error)` — nil changes when unchanged; fits a `store.UpdateWorkspace` edit
  - `func readConfig(repo *gitrepo.Repo, rev string) (workspace.Config, error)`

- [ ] **Step 1: Write the failing test** `internal/distribute/config_test.go`

```go
package distribute

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/workspace"
)

const baseConfig = "workspace: " + fixture.WorkspaceID + "\ncatalog:\n  url: https://custos.example.org/git/catalog.git\n  commit: " + fixture.Commit + "\n"

func TestSetConfigKeepsOtherFields(t *testing.T) {
	out, changed, err := setConfig([]byte(baseConfig), func(c *workspace.Config) { c.Frozen = true })
	if err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	if want := baseConfig + "frozen: true\n"; string(out) != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	c, err := decodeConfig(out)
	if err != nil {
		t.Fatal(err)
	}
	if c.Workspace != fixture.WorkspaceID || c.Catalog.URL != "https://custos.example.org/git/catalog.git" || c.Catalog.Commit != fixture.Commit || !c.Frozen {
		t.Errorf("decoded %+v", c)
	}
}

func TestSetConfigUnfreezeDropsFlag(t *testing.T) {
	out, changed, err := setConfig([]byte(baseConfig+"frozen: true\n"), func(c *workspace.Config) { c.Frozen = false })
	if err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	if string(out) != baseConfig {
		t.Errorf("got:\n%s\nwant:\n%s", out, baseConfig)
	}
}

func TestSetConfigUnchanged(t *testing.T) {
	out, changed, err := setConfig([]byte(baseConfig), func(c *workspace.Config) { c.Catalog.Commit = fixture.Commit })
	if err != nil || changed || out != nil {
		t.Errorf("out %q, changed %v, err %v", out, changed, err)
	}
}

func TestSetConfigRejectsUnknownFields(t *testing.T) {
	_, _, err := setConfig([]byte(baseConfig+"owner: team-a\n"), func(c *workspace.Config) { c.Frozen = true })
	if err == nil || !strings.Contains(err.Error(), "owner") {
		t.Errorf("err %v, want an error naming the unknown field", err)
	}
}

func TestSetConfigNeedsCommit(t *testing.T) {
	_, _, err := setConfig([]byte("workspace: "+fixture.WorkspaceID+"\n"), func(c *workspace.Config) { c.Frozen = true })
	if err == nil || !strings.Contains(err.Error(), "catalog.commit is missing") {
		t.Errorf("err %v", err)
	}
}

func TestEditConfig(t *testing.T) {
	tree := fixture.MapFS(fixture.Workspace())
	head := strings.Repeat("a", 40)
	changes, err := editConfig(tree, func(c *workspace.Config) { c.Catalog.Commit = head })
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "custos.yaml" || changes[0].Delete || !strings.Contains(string(changes[0].Data), "commit: "+head+"\n") {
		t.Fatalf("changes %+v", changes)
	}
	changes, err = editConfig(tree, func(*workspace.Config) {})
	if err != nil || changes != nil {
		t.Errorf("no-op edit: changes %+v, err %v", changes, err)
	}
	if _, err := editConfig(fixture.MapFS(map[string]string{}), func(*workspace.Config) {}); err == nil {
		t.Error("a tree without custos.yaml must be an error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/distribute/`
Expected: FAIL, `undefined: setConfig` (and `decodeConfig`, `editConfig`)

- [ ] **Step 3: Write `internal/distribute/config.go`**

```go
// Package distribute brings workspaces in line with the catalog (spec §4.1).
// When the catalog's main advances, an unfrozen workspace gets a commit by
// custos-bot that moves its pin; a frozen workspace gets one pin proposal
// branch custos/pin/<catalog-commit>, which an engineer accepts or rejects.
package distribute

import (
	"bytes"
	"fmt"
	"io/fs"
	"reflect"

	"go.yaml.in/yaml/v3"

	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/workspace"
)

const configPath = "custos.yaml"

// decodeConfig reads custos.yaml strictly: an unknown field is an error, so
// rewriting the file can never drop a field custos does not know.
func decodeConfig(data []byte) (workspace.Config, error) {
	var c workspace.Config
	if err := frontmatter.DecodeStrict(data, &c); err != nil {
		return c, fmt.Errorf("%s: %w", configPath, err)
	}
	if c.Catalog.Commit == "" {
		return c, fmt.Errorf("%s: catalog.commit is missing", configPath)
	}
	return c, nil
}

// encodeConfig writes custos.yaml with the two-space indentation the
// examples in the README use.
func encodeConfig(c workspace.Config) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// setConfig applies change to the custos.yaml in data. changed is false, and
// out nil, when change left every field as it was.
func setConfig(data []byte, change func(*workspace.Config)) (out []byte, changed bool, err error) {
	c, err := decodeConfig(data)
	if err != nil {
		return nil, false, err
	}
	before := c
	change(&c)
	if reflect.DeepEqual(c, before) {
		return nil, false, nil
	}
	if out, err = encodeConfig(c); err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// editConfig is setConfig for the tree of a store.UpdateWorkspace edit. It
// returns no changes when change left custos.yaml as it was.
func editConfig(tree fs.FS, change func(*workspace.Config)) ([]gitrepo.Change, error) {
	data, err := fs.ReadFile(tree, configPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", configPath, err)
	}
	out, changed, err := setConfig(data, change)
	if err != nil || !changed {
		return nil, err
	}
	return []gitrepo.Change{{Path: configPath, Data: out}}, nil
}

// readConfig reads custos.yaml at revision rev of a workspace repository.
func readConfig(repo *gitrepo.Repo, rev string) (workspace.Config, error) {
	data, ok, err := repo.ReadFile(rev, configPath)
	if err != nil {
		return workspace.Config{}, err
	}
	if !ok {
		return workspace.Config{}, fmt.Errorf("%s is missing at %s", configPath, rev)
	}
	return decodeConfig(data)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/distribute/ -v`
Expected: PASS (6 tests). If `TestSetConfigKeepsOtherFields` shows `commit: "0123…"` in quotes, the encoder is not `yaml.v3`; check the import path.

- [ ] **Step 5: Commit**

```bash
git add internal/distribute/config.go internal/distribute/config_test.go
git commit -m "Rewrite custos.yaml without losing fields"
```

---

### Task 2: Diff two catalog commits

**Files:**
- Create: `internal/distribute/diff.go`
- Create: `internal/distribute/helpers_test.go` (shared test helpers for Tasks 2–6)
- Test: `internal/distribute/diff_test.go`

**Interfaces:**
- Consumes: `catalog.Load(fs.FS) (*catalog.Catalog, []problem.Problem)`, `(*task.Graph).Tasks/Current/Has`, `(*gitrepo.Repo).TreeFS(rev)`; in tests `store.Open`, `(*store.Store).CatalogRepo/CreateWorkspace/WorkspaceRepo/UpdateWorkspace`, `gitrepo.CommitRequest`, `(*gitrepo.Repo).WriteCommit/UpdateRef/ResolveRef/Refs`, `editConfig`, `readConfig` (Task 1).
- Produces:
  - `type Diff struct { NewVersions, Added, Superseded []task.Ref; GroupsChanged, BindingsChanged bool }` with JSON names `new_versions`, `added`, `superseded`, `groups_changed`, `bindings_changed`; slices are never nil.
  - `func CatalogDiff(cat *gitrepo.Repo, from, to string) (Diff, error)` — `from` "" is an empty catalog.
  - Test helpers (package `distribute`, file `helpers_test.go`): `wsA`, `wsB`, `taskD`, `person`, `newStore`, `commitCatalog`, `newTaskAVersion`, `createWorkspace`, `wsRepo`, `resolve`, `configAt`, `pinBranches`, `allRefs`, `authorOf`, `committerOf`, `parentCount`, `markFrozen`, `answerA`, `breakConfig`.

- [ ] **Step 1: Write the shared test helpers** `internal/distribute/helpers_test.go`

```go
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
```

- [ ] **Step 2: Write the failing test** `internal/distribute/diff_test.go`

```go
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/distribute/`
Expected: FAIL, `undefined: CatalogDiff`

- [ ] **Step 4: Write `internal/distribute/diff.go`**

```go
package distribute

import (
	"maps"
	"slices"
	"testing/fstest"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/task"
)

// Diff is what changes for a workspace when its pin moves from one catalog
// commit to another (spec §4.1 step 2). Lists are sorted by task id.
type Diff struct {
	// NewVersions are tasks with a current version at both commits that
	// differs; each entry is the version at the new commit.
	NewVersions []task.Ref `json:"new_versions"`
	// Added are tasks with a current version only at the new commit.
	Added []task.Ref `json:"added"`
	// Superseded are tasks with a current version only at the old commit
	// (merged into another task, or missing because the pin moves back);
	// each entry is the version at the old commit.
	Superseded []task.Ref `json:"superseded"`
	// GroupsChanged reports a change in groups/index.yaml or a group.yaml.
	GroupsChanged bool `json:"groups_changed"`
	// BindingsChanged reports a change in processors.yaml: a binding, or a
	// processor's image digest, timeout, network or secrets.
	BindingsChanged bool `json:"bindings_changed"`
}

// CatalogDiff compares the catalog at commit from with the catalog at commit
// to. from "" stands for an empty catalog. Files are compared by what they
// mean, not byte by byte. Problems in either tree are ignored: commits on
// the catalog's main were validated when they got there.
func CatalogDiff(cat *gitrepo.Repo, from, to string) (Diff, error) {
	a, err := loadCatalog(cat, from)
	if err != nil {
		return Diff{}, err
	}
	b, err := loadCatalog(cat, to)
	if err != nil {
		return Diff{}, err
	}
	d := Diff{NewVersions: []task.Ref{}, Added: []task.Ref{}, Superseded: []task.Ref{}}
	ids := append(a.Tasks.Tasks(), b.Tasks.Tasks()...)
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		old, hadOld := a.Tasks.Current(id)
		cur, hasCur := b.Tasks.Current(id)
		switch {
		case hasCur && !hadOld:
			d.Added = append(d.Added, cur.Ref())
		case hasCur && old.Ref() != cur.Ref():
			d.NewVersions = append(d.NewVersions, cur.Ref())
		case !hasCur && hadOld:
			d.Superseded = append(d.Superseded, old.Ref())
		}
	}
	d.GroupsChanged = !sameGroups(a, b)
	d.BindingsChanged = !sameRegistry(a.Registry, b.Registry)
	return d, nil
}

func loadCatalog(cat *gitrepo.Repo, rev string) (*catalog.Catalog, error) {
	if rev == "" {
		c, _ := catalog.Load(fstest.MapFS{})
		return c, nil
	}
	fsys, err := cat.TreeFS(rev)
	if err != nil {
		return nil, err
	}
	c, _ := catalog.Load(fsys)
	return c, nil
}

func sameGroups(a, b *catalog.Catalog) bool {
	return slices.Equal(a.Index, b.Index) && maps.EqualFunc(a.Groups, b.Groups, func(x, y *catalog.Group) bool {
		return x.Title == y.Title && slices.Equal(x.Children, y.Children)
	})
}

func sameRegistry(a, b catalog.Registry) bool {
	return maps.Equal(a.Bindings, b.Bindings) && maps.EqualFunc(a.Processors, b.Processors, func(x, y catalog.Processor) bool {
		return x.Image == y.Image && x.Timeout == y.Timeout && x.Network == y.Network && slices.Equal(x.Secrets, y.Secrets)
	})
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/distribute/ -v`
Expected: PASS (Task 1 tests plus 5 new ones)

- [ ] **Step 6: Commit**

```bash
git add internal/distribute/diff.go internal/distribute/diff_test.go internal/distribute/helpers_test.go
git commit -m "Diff two catalog commits for pin proposals"
```

---

### Task 3: Reconcile workspaces with the catalog

**Files:**
- Create: `internal/distribute/distribute.go`
- Test: `internal/distribute/distribute_test.go`

**Interfaces:**
- Consumes: `(*store.Store).CatalogRepo/WorkspaceIDs/WorkspaceRepo/UpdateWorkspace/Lock`, `store.ErrConflict`, `gitrepo.Bot`, `gitrepo.ErrRefMoved`, `(*gitrepo.Repo).ResolveRef/Refs/DeleteRef`, `editConfig`, `readConfig` (Task 1), test helpers (Task 2).
- Produces:
  - `func Reconcile(st *store.Store) error`
  - unexported, used by Tasks 4–6: `const mainRef = "refs/heads/main"`, `const pinBranch = "custos/pin/"`, `const pinRefPrefix = "refs/heads/custos/pin/"`, `var mu sync.Mutex` (held by every exported operation that writes), `func reconcile(st *store.Store, id, head string) error` (caller holds `mu`), `func propose(...)`, `func prune(st *store.Store, id string, repo *gitrepo.Repo, keep string) error`, `func pruneRefs(repo *gitrepo.Repo, prefix, keep string) error`, `func conflict(err error) error` (wraps `gitrepo.ErrRefMoved` as `store.ErrConflict`; nil stays nil).

- [ ] **Step 1: Write the failing test** `internal/distribute/distribute_test.go`

```go
package distribute

import (
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
)

func TestReconcileMovesPinOfUnfrozenWorkspace(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	repo := wsRepo(t, st, wsA)
	before := configAt(t, repo, "refs/heads/main")
	c2 := newTaskAVersion(t, st, "1.2.0", "1.1.0")

	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	after := configAt(t, repo, "refs/heads/main")
	if after.Catalog.Commit != c2 {
		t.Errorf("pin %s, want %s", after.Catalog.Commit, c2)
	}
	if after.Workspace != before.Workspace || after.Catalog.URL != before.Catalog.URL || after.Frozen {
		t.Errorf("other fields changed: before %+v, after %+v", before, after)
	}
	if got := authorOf(t, repo, "refs/heads/main"); got != gitrepo.Bot.String() {
		t.Errorf("author %q, want %q", got, gitrepo.Bot.String())
	}
	if b := pinBranches(t, repo); len(b) != 0 {
		t.Errorf("unfrozen workspace got proposals %v", b)
	}
}

func TestReconcileProposesForFrozenWorkspace(t *testing.T) {
	st := newStore(t)
	c1 := commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	markFrozen(t, st, wsA)
	repo := wsRepo(t, st, wsA)
	main := resolve(t, repo, "refs/heads/main")
	c2 := newTaskAVersion(t, st, "1.2.0", "1.1.0")

	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	if got := resolve(t, repo, "refs/heads/main"); got != main {
		t.Errorf("main of a frozen workspace moved from %s to %s", main, got)
	}
	if c := configAt(t, repo, "refs/heads/main"); c.Catalog.Commit != c1 {
		t.Errorf("pin on main %s, want %s", c.Catalog.Commit, c1)
	}
	branch := "custos/pin/" + c2
	if b := pinBranches(t, repo); !slices.Equal(b, []string{branch}) {
		t.Fatalf("branches %v, want [%s]", b, branch)
	}
	c := configAt(t, repo, "refs/heads/"+branch)
	if c.Catalog.Commit != c2 || !c.Frozen {
		t.Errorf("proposal config %+v", c)
	}
	if got := firstParent(t, repo, "refs/heads/"+branch); got != main {
		t.Errorf("proposal parent %s, want main %s", got, main)
	}
	if got := authorOf(t, repo, "refs/heads/"+branch); got != gitrepo.Bot.String() {
		t.Errorf("proposal author %q", got)
	}
}

func TestNewerCatalogCommitReplacesProposal(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	markFrozen(t, st, wsA)
	repo := wsRepo(t, st, wsA)
	newTaskAVersion(t, st, "1.2.0", "1.1.0")
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	c3 := newTaskAVersion(t, st, "1.3.0", "1.2.0")
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	if b := pinBranches(t, repo); !slices.Equal(b, []string{"custos/pin/" + c3}) {
		t.Fatalf("branches %v, want only the proposal for %s", b, c3)
	}
	if c := configAt(t, repo, "refs/heads/custos/pin/"+c3); c.Catalog.Commit != c3 {
		t.Errorf("proposal pins %s", c.Catalog.Commit)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	createWorkspace(t, st, wsB)
	markFrozen(t, st, wsB)
	newTaskAVersion(t, st, "1.2.0", "1.1.0")
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	a, b := wsRepo(t, st, wsA), wsRepo(t, st, wsB)
	refsA, refsB := allRefs(t, a), allRefs(t, b)
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	if got := allRefs(t, a); !maps.Equal(got, refsA) {
		t.Errorf("unfrozen workspace changed: %v → %v", refsA, got)
	}
	if got := allRefs(t, b); !maps.Equal(got, refsB) {
		t.Errorf("frozen workspace changed: %v → %v", refsB, got)
	}
}

func TestReconcileContinuesAfterFailingWorkspace(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	createWorkspace(t, st, wsB)
	breakConfig(t, st, wsA) // wsA sorts first, so its failure comes before wsB
	c2 := newTaskAVersion(t, st, "1.2.0", "1.1.0")

	err := Reconcile(st)
	if err == nil || !strings.Contains(err.Error(), "workspace "+wsA) {
		t.Fatalf("err %v, want an error naming %s", err, wsA)
	}
	if strings.Contains(err.Error(), wsB) {
		t.Errorf("err names the healthy workspace: %v", err)
	}
	if c := configAt(t, wsRepo(t, st, wsB), "refs/heads/main"); c.Catalog.Commit != c2 {
		t.Errorf("healthy workspace pins %s, want %s", c.Catalog.Commit, c2)
	}
}

func TestReconcileSkipsEmptyRepositories(t *testing.T) {
	st := newStore(t)
	if err := Reconcile(st); err != nil {
		t.Fatalf("empty catalog: %v", err)
	}
	if _, err := st.CreateWorkspaceRepo(wsB); err != nil { // no main yet, as during a fork
		t.Fatal(err)
	}
	commitCatalog(t, st, fixture.Catalog())
	if err := Reconcile(st); err != nil {
		t.Fatalf("workspace without main: %v", err)
	}
}

func TestConcurrentReconciles(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	createWorkspace(t, st, wsB)
	markFrozen(t, st, wsB)
	newTaskAVersion(t, st, "1.2.0", "1.1.0")
	c3 := newTaskAVersion(t, st, "1.3.0", "1.2.0")

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() { errs <- Reconcile(st) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if c := configAt(t, wsRepo(t, st, wsA), "refs/heads/main"); c.Catalog.Commit != c3 {
		t.Errorf("pin %s, want %s", c.Catalog.Commit, c3)
	}
	if b := pinBranches(t, wsRepo(t, st, wsB)); !slices.Equal(b, []string{"custos/pin/" + c3}) {
		t.Errorf("branches %v", b)
	}
}

// firstParent returns the first parent of rev.
func firstParent(t *testing.T, repo *gitrepo.Repo, rev string) string {
	t.Helper()
	return gittest.Run(t, repo.Dir, "rev-parse", rev+"^")
}
```


- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/distribute/`
Expected: FAIL, `undefined: Reconcile`

- [ ] **Step 3: Write `internal/distribute/distribute.go`**

```go
package distribute

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sync"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/workspace"
)

const (
	mainRef      = "refs/heads/main"
	pinBranch    = "custos/pin/" // + catalog commit
	pinRefPrefix = "refs/heads/" + pinBranch
)

// mu serialises the writing operations of this package. Two reconciles
// started by pushes in quick succession would otherwise race, and the one
// that read the older catalog main could move a pin back or delete the
// newer proposal. Pushes to workspaces are not covered by mu; they meet the
// compare-and-swap of the store instead.
var mu sync.Mutex

// Reconcile brings every workspace in line with the catalog's main: an
// unfrozen workspace whose pin differs gets a commit by gitrepo.Bot that
// moves the pin; a frozen one gets (or keeps) exactly one branch
// custos/pin/<catalog-main>, and older custos/pin/* branches are deleted.
// Workspaces without a main branch are skipped. Idempotent; errors of one
// workspace do not stop the others (returned joined).
func Reconcile(st *store.Store) error {
	mu.Lock()
	defer mu.Unlock()
	head, ok, err := st.CatalogRepo().ResolveRef(mainRef)
	if err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	if !ok {
		return nil // nothing published yet
	}
	ids, err := st.WorkspaceIDs()
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if err := reconcile(st, id, head); err != nil {
			errs = append(errs, fmt.Errorf("workspace %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// reconcile brings one workspace in line with catalog commit head. The
// caller holds mu.
func reconcile(st *store.Store, id, head string) error {
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		return err
	}
	main, ok, err := repo.ResolveRef(mainRef)
	if err != nil {
		return err
	}
	if !ok {
		return nil // still being created, for example by a fork
	}
	cfg, err := readConfig(repo, main)
	if err != nil {
		return err
	}
	switch {
	case cfg.Catalog.Commit == head:
		return prune(st, id, repo, "")
	case !cfg.Frozen:
		if err := movePin(st, id, head); err != nil {
			return err
		}
		return prune(st, id, repo, "")
	default:
		if err := propose(st, id, repo, head); err != nil {
			return err
		}
		return prune(st, id, repo, head)
	}
}

// movePin commits the pin head on main, authored by custos-bot.
func movePin(st *store.Store, id, head string) error {
	_, err := st.UpdateWorkspace(id, mainRef, gitrepo.Bot, "Pin catalog commit "+head, func(tree fs.FS) ([]gitrepo.Change, error) {
		return editConfig(tree, func(c *workspace.Config) {
			if !c.Frozen { // a push may have frozen the workspace since main was read
				c.Catalog.Commit = head
			}
		})
	})
	return err
}

// propose makes sure branch custos/pin/<head> exists. A new branch starts at
// main with one commit by custos-bot that changes the pin. An existing
// branch is kept as it is, even when main has moved on; Accept handles that.
func propose(st *store.Store, id string, repo *gitrepo.Repo, head string) error {
	ref := pinRefPrefix + head
	if _, ok, err := repo.ResolveRef(ref); err != nil || ok {
		return err
	}
	_, err := st.UpdateWorkspace(id, ref, gitrepo.Bot, "Propose catalog commit "+head, func(tree fs.FS) ([]gitrepo.Change, error) {
		return editConfig(tree, func(c *workspace.Config) { c.Catalog.Commit = head })
	})
	return err
}

// prune deletes the pin proposal branches other than custos/pin/<keep>;
// keep "" deletes them all.
func prune(st *store.Store, id string, repo *gitrepo.Repo, keep string) error {
	unlock := st.Lock(id)
	defer unlock()
	return pruneRefs(repo, pinRefPrefix, keep)
}

// pruneRefs deletes the refs below prefix except prefix+keep. The caller
// holds the workspace's lock.
func pruneRefs(repo *gitrepo.Repo, prefix, keep string) error {
	refs, err := repo.Refs(prefix)
	if err != nil {
		return err
	}
	for _, ref := range slices.Sorted(maps.Keys(refs)) {
		if keep != "" && ref == prefix+keep {
			continue
		}
		if err := repo.DeleteRef(ref, refs[ref]); err != nil {
			return conflict(err)
		}
	}
	return nil
}

// conflict reports a ref that moved under us as store.ErrConflict.
func conflict(err error) error {
	if errors.Is(err, gitrepo.ErrRefMoved) {
		return fmt.Errorf("%w: %w", store.ErrConflict, err)
	}
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/distribute/ -v`
Expected: PASS. A hang in `TestConcurrentReconciles` means a store lock is held while calling `UpdateWorkspace`; only `prune` may hold `st.Lock(id)`.

- [ ] **Step 5: Commit**

```bash
git add internal/distribute/distribute.go internal/distribute/distribute_test.go
git commit -m "Move pins of unfrozen workspaces and propose pins to frozen ones"
```

---

### Task 4: Freeze and unfreeze

**Files:**
- Create: `internal/distribute/freeze.go`
- Test: `internal/distribute/freeze_test.go`

**Interfaces:**
- Consumes: `mu`, `mainRef`, `reconcile` (Task 3), `editConfig` (Task 1), `(*store.Store).UpdateWorkspace`, `store.ErrNotFound`.
- Produces:
  - `func Freeze(st *store.Store, id string, author gitrepo.Signature) error` — commit `frozen: true` by author, then reconcile the workspace (opens a proposal at once if the pin lags).
  - `func Unfreeze(st *store.Store, id string, author gitrepo.Signature) error` — commit `frozen: false` by author, then reconcile the workspace (moves the pin, deletes proposals).
  - Both are no-ops on `main` when the flag already has the wanted value.

- [ ] **Step 1: Write the failing test** `internal/distribute/freeze_test.go`

```go
package distribute

import (
	"errors"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
)

func TestFreeze(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	repo := wsRepo(t, st, wsA)

	if err := Freeze(st, wsA, person); err != nil {
		t.Fatal(err)
	}
	if c := configAt(t, repo, "refs/heads/main"); !c.Frozen {
		t.Errorf("config %+v, want frozen", c)
	}
	if got := authorOf(t, repo, "refs/heads/main"); got != person.String() {
		t.Errorf("author %q, want %q", got, person.String())
	}
	if got := committerOf(t, repo, "refs/heads/main"); got != gitrepo.Bot.String() {
		t.Errorf("committer %q, want %q", got, gitrepo.Bot.String())
	}
	main := resolve(t, repo, "refs/heads/main")
	if err := Freeze(st, wsA, person); err != nil {
		t.Fatal(err)
	}
	if got := resolve(t, repo, "refs/heads/main"); got != main {
		t.Errorf("freezing a frozen workspace committed %s", got)
	}
}

func TestFreezeUnknownWorkspace(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	if err := Freeze(st, wsB, person); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err %v, want store.ErrNotFound", err)
	}
	if err := Unfreeze(st, wsB, person); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err %v, want store.ErrNotFound", err)
	}
}

func TestFreezeOpensProposalForLaggingPin(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	c2 := newTaskAVersion(t, st, "1.2.0", "1.1.0") // not reconciled yet
	if err := Freeze(st, wsA, person); err != nil {
		t.Fatal(err)
	}
	repo := wsRepo(t, st, wsA)
	if b := pinBranches(t, repo); len(b) != 1 || b[0] != "custos/pin/"+c2 {
		t.Errorf("branches %v, want the proposal for %s", b, c2)
	}
}

func TestUnfreezeMovesPinAndDropsProposal(t *testing.T) {
	st := newStore(t)
	c1 := commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	if err := Freeze(st, wsA, person); err != nil {
		t.Fatal(err)
	}
	c2 := newTaskAVersion(t, st, "1.2.0", "1.1.0")
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	repo := wsRepo(t, st, wsA)
	if len(pinBranches(t, repo)) != 1 {
		t.Fatalf("no proposal before unfreeze: %v", pinBranches(t, repo))
	}

	if err := Unfreeze(st, wsA, person); err != nil {
		t.Fatal(err)
	}
	c := configAt(t, repo, "refs/heads/main")
	if c.Frozen || c.Catalog.Commit != c2 {
		t.Errorf("main config %+v, want unfrozen and pinned to %s", c, c2)
	}
	if got := authorOf(t, repo, "refs/heads/main"); got != gitrepo.Bot.String() {
		t.Errorf("pin move author %q", got)
	}
	prev := configAt(t, repo, "refs/heads/main~1")
	if prev.Frozen || prev.Catalog.Commit != c1 {
		t.Errorf("unfreeze commit config %+v", prev)
	}
	if got := authorOf(t, repo, "refs/heads/main~1"); got != person.String() {
		t.Errorf("unfreeze author %q", got)
	}
	if b := pinBranches(t, repo); len(b) != 0 {
		t.Errorf("proposals left after unfreeze: %v", b)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/distribute/`
Expected: FAIL, `undefined: Freeze`

- [ ] **Step 3: Write `internal/distribute/freeze.go`**

```go
package distribute

import (
	"io/fs"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/workspace"
)

// Freeze commits frozen: true on the workspace's main, authored by author.
// From then on catalog changes arrive as pin proposals; if the pin already
// lags behind the catalog, the proposal is opened right away.
func Freeze(st *store.Store, id string, author gitrepo.Signature) error {
	return setFrozen(st, id, author, true, "Freeze the workspace")
}

// Unfreeze commits frozen: false on the workspace's main, authored by
// author, then reconciles the workspace: custos-bot moves the pin to the
// catalog's main and the pin proposals are deleted.
func Unfreeze(st *store.Store, id string, author gitrepo.Signature) error {
	return setFrozen(st, id, author, false, "Unfreeze the workspace")
}

func setFrozen(st *store.Store, id string, author gitrepo.Signature, frozen bool, message string) error {
	mu.Lock()
	defer mu.Unlock()
	_, err := st.UpdateWorkspace(id, mainRef, author, message, func(tree fs.FS) ([]gitrepo.Change, error) {
		return editConfig(tree, func(c *workspace.Config) { c.Frozen = frozen })
	})
	if err != nil {
		return err
	}
	head, ok, err := st.CatalogRepo().ResolveRef(mainRef)
	if err != nil || !ok {
		return err
	}
	return reconcile(st, id, head)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/distribute/ -v`
Expected: PASS. If `TestFreezeUnknownWorkspace` fails, check that 2a's `UpdateWorkspace` wraps `store.ErrNotFound` for a missing repository; do not paper over it here.

- [ ] **Step 5: Commit**

```bash
git add internal/distribute/freeze.go internal/distribute/freeze_test.go
git commit -m "Freeze and unfreeze workspaces"
```

---

### Task 5: List, accept and reject pin proposals

**Files:**
- Create: `internal/distribute/proposals.go`
- Modify: `internal/distribute/distribute.go` (functions `propose` and `prune`)
- Test: `internal/distribute/proposals_test.go`

**Interfaces:**
- Consumes: `mu`, `mainRef`, `pinBranch`, `pinRefPrefix`, `pruneRefs`, `conflict` (Task 3), `readConfig`, `setConfig`, `configPath` (Task 1), `CatalogDiff` (Task 2), `(*store.Store).SetRef/Lock/WorkspaceRepo/CatalogRepo`, `store.ErrNotFound`, `*store.RejectedError`, `problem.RulePin`, `(*gitrepo.Repo).IsAncestor/WriteCommit/UpdateRef/DeleteRef`.
- Produces:
  - `type PinProposal struct { Branch, Commit, From, To string; Changes Diff }` with JSON names `branch`, `commit`, `from`, `to`, `changes`. `Branch` is the short name `custos/pin/<catalog commit>`.
  - `func Proposals(st *store.Store, id string) ([]PinProposal, error)` — never nil; sorted by branch.
  - `func Accept(st *store.Store, id, branch string, author gitrepo.Signature) error` — fast-forward, or a merge commit by author with parents (main, proposal); validated by `SetRef`; deletes the branch.
  - `func Reject(st *store.Store, id, branch string) error` — deletes the branch and records `refs/custos/rejected-pin/<catalog commit>`, so reconcile does not reopen it.
  - Errors: unknown or malformed branch → wraps `store.ErrNotFound`; ref moved → wraps `store.ErrConflict`; invalid result → `*store.RejectedError`.

- [ ] **Step 1: Write the failing test** `internal/distribute/proposals_test.go`

```go
package distribute

import (
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// frozenWithProposal returns a store whose workspace wsA is frozen at c1 and
// has a proposal for c2.
func frozenWithProposal(t *testing.T) (st *store.Store, repo *gitrepo.Repo, c1, c2 string) {
	t.Helper()
	st = newStore(t)
	c1 = commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	markFrozen(t, st, wsA)
	c2 = newTaskAVersion(t, st, "1.2.0", "1.1.0")
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	return st, wsRepo(t, st, wsA), c1, c2
}

func TestProposals(t *testing.T) {
	st, repo, c1, c2 := frozenWithProposal(t)
	ps, err := Proposals(st, wsA)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Fatalf("proposals %+v", ps)
	}
	p := ps[0]
	if p.Branch != "custos/pin/"+c2 || p.Commit != resolve(t, repo, "refs/heads/"+p.Branch) || p.From != c1 || p.To != c2 {
		t.Errorf("proposal %+v", p)
	}
	wantRefs(t, "new versions", p.Changes.NewVersions, task.Ref{ID: fixture.TaskA, Version: "1.2.0"})

	createWorkspace(t, st, wsB) // unfrozen, pinned to catalog main
	ps, err = Proposals(st, wsB)
	if err != nil || ps == nil || len(ps) != 0 {
		t.Errorf("unfrozen workspace: proposals %#v, err %v", ps, err)
	}
	if _, err := Proposals(st, "e5f6a7b8-c9d0-4e1f-a2b3-c4d5e6f7a8b9"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown workspace: err %v", err)
	}
}

func TestAcceptFastForward(t *testing.T) {
	st, repo, _, c2 := frozenWithProposal(t)
	branch := "custos/pin/" + c2
	tip := resolve(t, repo, "refs/heads/"+branch)

	if err := Accept(st, wsA, branch, person); err != nil {
		t.Fatal(err)
	}
	if got := resolve(t, repo, "refs/heads/main"); got != tip {
		t.Errorf("main %s, want the proposal %s", got, tip)
	}
	if c := configAt(t, repo, "refs/heads/main"); c.Catalog.Commit != c2 || !c.Frozen {
		t.Errorf("config %+v", c)
	}
	if b := pinBranches(t, repo); len(b) != 0 {
		t.Errorf("branches left: %v", b)
	}
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	if b := pinBranches(t, repo); len(b) != 0 {
		t.Errorf("reconcile reopened an accepted proposal: %v", b)
	}
}

func TestAcceptDivergedMain(t *testing.T) {
	st, repo, _, c2 := frozenWithProposal(t)
	answerA(t, st, wsA) // main moves past the proposal's base
	branch := "custos/pin/" + c2

	if err := Accept(st, wsA, branch, person); err != nil {
		t.Fatal(err)
	}
	if n := parentCount(t, repo, "refs/heads/main"); n != 2 {
		t.Errorf("main has %d parents, want a merge commit", n)
	}
	if c := configAt(t, repo, "refs/heads/main"); c.Catalog.Commit != c2 || !c.Frozen {
		t.Errorf("config %+v", c)
	}
	if _, ok, err := repo.ReadFile("refs/heads/main", "answers/"+fixture.TaskA+".md"); err != nil || !ok {
		t.Errorf("answer lost in the merge: ok %v, err %v", ok, err)
	}
	if got := authorOf(t, repo, "refs/heads/main"); got != person.String() {
		t.Errorf("merge author %q", got)
	}
	if b := pinBranches(t, repo); len(b) != 0 {
		t.Errorf("branches left: %v", b)
	}
}

func TestAcceptIsValidated(t *testing.T) {
	st, repo, c1, _ := frozenWithProposal(t)
	// A catalog commit that is not on the catalog's main.
	stray, err := st.CatalogRepo().WriteCommit(gitrepo.CommitRequest{
		Base: c1, Parents: []string{c1}, Author: gitrepo.Bot, Message: "Draft",
		Changes: []gitrepo.Change{{Path: "README.md", Data: []byte("draft\n")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	branch := "custos/pin/" + stray
	if _, err := st.UpdateWorkspace(wsA, "refs/heads/"+branch, person, "Pin a draft", func(tree fs.FS) ([]gitrepo.Change, error) {
		return editConfig(tree, func(c *workspace.Config) { c.Catalog.Commit = stray })
	}); err != nil {
		t.Fatal(err)
	}
	main := resolve(t, repo, "refs/heads/main")

	err = Accept(st, wsA, branch, person)
	var rej *store.RejectedError
	if !errors.As(err, &rej) || !slices.ContainsFunc(rej.Problems, func(p problem.Problem) bool { return p.Rule == problem.RulePin }) {
		t.Fatalf("err %v, want a rejection with rule %s", err, problem.RulePin)
	}
	if got := resolve(t, repo, "refs/heads/main"); got != main {
		t.Errorf("main moved to %s", got)
	}
	if resolve(t, repo, "refs/heads/"+branch) == "" {
		t.Error("a rejected accept must keep the proposal")
	}
}

func TestAcceptUnknownBranch(t *testing.T) {
	st, _, _, _ := frozenWithProposal(t)
	for _, branch := range []string{"custos/pin/" + strings.Repeat("a", 40), "draft", "custos/pin/../main"} {
		if err := Accept(st, wsA, branch, person); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("accept %q: err %v, want store.ErrNotFound", branch, err)
		}
		if err := Reject(st, wsA, branch); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("reject %q: err %v, want store.ErrNotFound", branch, err)
		}
	}
}

func TestRejectStaysRejected(t *testing.T) {
	st, repo, _, c2 := frozenWithProposal(t)
	main := resolve(t, repo, "refs/heads/main")
	if err := Reject(st, wsA, "custos/pin/"+c2); err != nil {
		t.Fatal(err)
	}
	if b := pinBranches(t, repo); len(b) != 0 {
		t.Fatalf("branches after reject: %v", b)
	}
	if got := resolve(t, repo, "refs/heads/main"); got != main {
		t.Errorf("reject moved main to %s", got)
	}
	// A restart runs reconcile again; the rejected proposal stays closed.
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	if b := pinBranches(t, repo); len(b) != 0 {
		t.Fatalf("reconcile reopened a rejected proposal: %v", b)
	}
	// The next catalog commit opens a new proposal and forgets the rejection.
	c3 := newTaskAVersion(t, st, "1.3.0", "1.2.0")
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}
	if b := pinBranches(t, repo); !slices.Equal(b, []string{"custos/pin/" + c3}) {
		t.Errorf("branches %v, want the proposal for %s", b, c3)
	}
	if marks, err := repo.Refs(rejectedRefPrefix); err != nil || len(marks) != 0 {
		t.Errorf("rejection marks left: %v, err %v", marks, err)
	}
}
```


- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/distribute/`
Expected: FAIL, `undefined: Proposals` (and `Accept`, `Reject`, `rejectedRefPrefix`)

- [ ] **Step 3: Write `internal/distribute/proposals.go`**

```go
package distribute

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/workspace"
)

// rejectedRefPrefix + catalog commit marks the proposal for that commit as
// rejected, so that reconcile does not open it again. It points at the last
// commit of the rejected branch. It is not a branch, so clones do not fetch
// it. Reconcile deletes it once the catalog's main moves on.
const rejectedRefPrefix = "refs/custos/rejected-pin/"

var commitRE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// PinProposal is one open branch custos/pin/<catalog commit>.
type PinProposal struct {
	Branch  string `json:"branch"`  // custos/pin/<catalog commit>
	Commit  string `json:"commit"`  // last commit of the branch
	From    string `json:"from"`    // pin on the workspace's main
	To      string `json:"to"`      // pin on the branch
	Changes Diff   `json:"changes"` // CatalogDiff(From, To)
}

// Proposals lists the open pin proposals of a workspace, sorted by branch.
func Proposals(st *store.Store, id string) ([]PinProposal, error) {
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		return nil, err
	}
	out := []PinProposal{}
	main, ok, err := repo.ResolveRef(mainRef)
	if err != nil || !ok {
		return out, err
	}
	cfg, err := readConfig(repo, main)
	if err != nil {
		return nil, err
	}
	refs, err := repo.Refs(pinRefPrefix)
	if err != nil {
		return nil, err
	}
	for _, ref := range slices.Sorted(maps.Keys(refs)) {
		branch := strings.TrimPrefix(ref, "refs/heads/")
		pc, err := readConfig(repo, refs[ref])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", branch, err)
		}
		d, err := CatalogDiff(st.CatalogRepo(), cfg.Catalog.Commit, pc.Catalog.Commit)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", branch, err)
		}
		out = append(out, PinProposal{Branch: branch, Commit: refs[ref], From: cfg.Catalog.Commit, To: pc.Catalog.Commit, Changes: d})
	}
	return out, nil
}

// proposalRef returns the ref of a pin proposal branch and the catalog
// commit in its name. Other names are reported as not found.
func proposalRef(branch string) (ref, commit string, err error) {
	commit, ok := strings.CutPrefix(branch, pinBranch)
	if !ok || !commitRE.MatchString(commit) {
		return "", "", fmt.Errorf("%q is not a pin proposal (custos/pin/<catalog commit>): %w", branch, store.ErrNotFound)
	}
	return pinRefPrefix + commit, commit, nil
}

// Accept merges a pin proposal into the workspace's main: a fast-forward
// when main has not moved since the proposal was opened, otherwise a merge
// commit by author whose tree is main's tree with the proposal's pin. The
// new main is validated like any other update. The branch is deleted.
func Accept(st *store.Store, id, branch string, author gitrepo.Signature) error {
	mu.Lock()
	defer mu.Unlock()
	ref, _, err := proposalRef(branch)
	if err != nil {
		return err
	}
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		return err
	}
	tip, ok, err := repo.ResolveRef(ref)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("pin proposal %s: %w", branch, store.ErrNotFound)
	}
	main, ok, err := repo.ResolveRef(mainRef)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("workspace %s has no main branch: %w", id, store.ErrNotFound)
	}
	next := tip
	ff, err := repo.IsAncestor(main, tip)
	if err != nil {
		return err
	}
	if !ff {
		if next, err = mergeProposal(repo, main, tip, branch, author); err != nil {
			return err
		}
	}
	if err := st.SetRef(id, mainRef, next, main); err != nil {
		return err
	}
	unlock := st.Lock(id)
	defer unlock()
	return conflict(repo.DeleteRef(ref, tip))
}

// mergeProposal writes the merge of proposal tip into main. Proposal
// branches are written by custos and change nothing but the pin, so main's
// tree with the proposal's pin is what a three-way merge would produce.
func mergeProposal(repo *gitrepo.Repo, main, tip, branch string, author gitrepo.Signature) (string, error) {
	pc, err := readConfig(repo, tip)
	if err != nil {
		return "", err
	}
	data, ok, err := repo.ReadFile(main, configPath)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%s is missing on main", configPath)
	}
	out, changed, err := setConfig(data, func(c *workspace.Config) { c.Catalog.Commit = pc.Catalog.Commit })
	if err != nil {
		return "", err
	}
	var changes []gitrepo.Change
	if changed {
		changes = []gitrepo.Change{{Path: configPath, Data: out}}
	}
	return repo.WriteCommit(gitrepo.CommitRequest{
		Base:    main,
		Parents: []string{main, tip},
		Changes: changes,
		Author:  author,
		Message: "Accept pin proposal " + branch,
	})
}

// Reject deletes a pin proposal and marks its catalog commit as rejected
// for this workspace, so that reconcile does not open it again.
func Reject(st *store.Store, id, branch string) error {
	mu.Lock()
	defer mu.Unlock()
	ref, commit, err := proposalRef(branch)
	if err != nil {
		return err
	}
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		return err
	}
	unlock := st.Lock(id)
	defer unlock()
	tip, ok, err := repo.ResolveRef(ref)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("pin proposal %s: %w", branch, store.ErrNotFound)
	}
	mark := rejectedRefPrefix + commit
	_, marked, err := repo.ResolveRef(mark)
	if err != nil {
		return err
	}
	if !marked {
		if err := repo.UpdateRef(mark, tip, ""); err != nil {
			return conflict(err)
		}
	}
	return conflict(repo.DeleteRef(ref, tip))
}
```

- [ ] **Step 4: Make reconcile respect rejections** — in `internal/distribute/distribute.go`, replace the function `propose` with:

```go
// propose makes sure branch custos/pin/<head> exists, unless the proposal
// for head was rejected. A new branch starts at main with one commit by
// custos-bot that changes the pin. An existing branch is kept as it is,
// even when main has moved on; Accept handles that.
func propose(st *store.Store, id string, repo *gitrepo.Repo, head string) error {
	ref := pinRefPrefix + head
	for _, existing := range []string{ref, rejectedRefPrefix + head} {
		if _, ok, err := repo.ResolveRef(existing); err != nil || ok {
			return err
		}
	}
	_, err := st.UpdateWorkspace(id, ref, gitrepo.Bot, "Propose catalog commit "+head, func(tree fs.FS) ([]gitrepo.Change, error) {
		return editConfig(tree, func(c *workspace.Config) { c.Catalog.Commit = head })
	})
	return err
}
```

and replace the function `prune` with:

```go
// prune deletes the pin proposal branches and rejection marks other than
// those for catalog commit keep; keep "" deletes them all.
func prune(st *store.Store, id string, repo *gitrepo.Repo, keep string) error {
	unlock := st.Lock(id)
	defer unlock()
	if err := pruneRefs(repo, pinRefPrefix, keep); err != nil {
		return err
	}
	return pruneRefs(repo, rejectedRefPrefix, keep)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test -race ./internal/distribute/ -v`
Expected: PASS (all tests of Tasks 1–5). If `TestAcceptIsValidated` fails because `Accept` returned nil, 2a's `SetRef` does not validate `main`; that is a 2a bug to fix there.

- [ ] **Step 6: Commit**

```bash
git add internal/distribute/proposals.go internal/distribute/proposals_test.go internal/distribute/distribute.go
git commit -m "List, accept and reject pin proposals"
```

---

### Task 6: REST endpoints

**Files:**
- Create: `internal/distribute/api.go`
- Test: `internal/distribute/api_test.go`

**Interfaces:**
- Consumes: `api.New(st *store.Store, bl *blobs.Store) *api.API`, `(*api.API).Handle(pattern string, h http.HandlerFunc)`, `(*api.API).Handler() http.Handler`, `api.Author(r *http.Request) (gitrepo.Signature, error)`, `api.WriteJSON(w, status int, v any)`, `api.WriteError(w, err error)`, `blobs.Open(dir string) (*blobs.Store, error)` (2b); `Freeze`, `Unfreeze`, `Proposals`, `Accept`, `Reject`, `CatalogDiff`, `readConfig`, `mainRef` (Tasks 1–5).
- Produces:
  - `func Register(a *api.API, st *store.Store)` — registers the six endpoints; call before `a.Handler()`.
  - Responses: freeze/unfreeze `200 {"id", "frozen"}`; proposals `200 [PinProposal…]` (`[]` when none); accept/reject `200 {"branch"}`; pin-diff `200 {"from", "to", "changes"}`.

- [ ] **Step 1: Write the failing test** `internal/distribute/api_test.go`

```go
package distribute

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

const authorHeader = "Jane Doe <jane@example.org>"

func serveAPI(t *testing.T, st *store.Store) string {
	t.Helper()
	bl, err := blobs.Open(filepath.Join(st.DataDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(st, bl)
	Register(a, st)
	ts := httptest.NewServer(a.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func call(t *testing.T, method, url, author, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if author != "" {
		req.Header.Set("X-Custos-Author", author)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

func TestAPIFreezeProposeAccept(t *testing.T) {
	st := newStore(t)
	c1 := commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	ws := serveAPI(t, st) + "/api/workspaces/" + wsA

	if code, body := call(t, "POST", ws+"/freeze", "", ""); code != http.StatusUnauthorized || !strings.Contains(body, `"error"`) {
		t.Fatalf("freeze without author: %d %s", code, body)
	}
	if code, body := call(t, "POST", ws+"/freeze", authorHeader, ""); code != http.StatusOK || !strings.Contains(body, `"frozen":true`) {
		t.Fatalf("freeze: %d %s", code, body)
	}
	c2 := newTaskAVersion(t, st, "1.2.0", "1.1.0")
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}

	code, body := call(t, "GET", ws+"/pin-diff", "", "")
	var pd pinDiffResponse
	if code != http.StatusOK || json.Unmarshal([]byte(body), &pd) != nil {
		t.Fatalf("pin-diff: %d %s", code, body)
	}
	if pd.From != c1 || pd.To != c2 {
		t.Errorf("pin-diff %+v", pd)
	}
	wantRefs(t, "pin-diff new versions", pd.Changes.NewVersions, task.Ref{ID: fixture.TaskA, Version: "1.2.0"})

	code, body = call(t, "GET", ws+"/proposals", "", "")
	var ps []PinProposal
	if code != http.StatusOK || json.Unmarshal([]byte(body), &ps) != nil || len(ps) != 1 || ps[0].Branch != "custos/pin/"+c2 {
		t.Fatalf("proposals: %d %s", code, body)
	}

	if code, body := call(t, "POST", ws+"/proposals/accept", authorHeader, `{}`); code != http.StatusBadRequest {
		t.Errorf("accept without branch: %d %s", code, body)
	}
	if code, body := call(t, "POST", ws+"/proposals/accept", authorHeader, `{"branch":"custos/pin/`+strings.Repeat("a", 40)+`"}`); code != http.StatusNotFound {
		t.Errorf("accept unknown branch: %d %s", code, body)
	}
	if code, body := call(t, "POST", ws+"/proposals/accept", "", `{"branch":"custos/pin/`+c2+`"}`); code != http.StatusUnauthorized {
		t.Errorf("accept without author: %d %s", code, body)
	}
	if code, body := call(t, "POST", ws+"/proposals/accept", authorHeader, `{"branch":"custos/pin/`+c2+`"}`); code != http.StatusOK {
		t.Fatalf("accept: %d %s", code, body)
	}
	if c := configAt(t, wsRepo(t, st, wsA), "refs/heads/main"); c.Catalog.Commit != c2 {
		t.Errorf("pin after accept %s", c.Catalog.Commit)
	}

	if code, body := call(t, "POST", ws+"/unfreeze", authorHeader, ""); code != http.StatusOK || !strings.Contains(body, `"frozen":false`) {
		t.Errorf("unfreeze: %d %s", code, body)
	}
}

func TestAPIReject(t *testing.T) {
	st, _, _, c2 := frozenWithProposal(t)
	ws := serveAPI(t, st) + "/api/workspaces/" + wsA
	if code, body := call(t, "POST", ws+"/proposals/reject", authorHeader, `{"branch":"custos/pin/`+c2+`"}`); code != http.StatusOK {
		t.Fatalf("reject: %d %s", code, body)
	}
	if code, body := call(t, "GET", ws+"/proposals", "", ""); code != http.StatusOK || strings.TrimSpace(body) != "[]" {
		t.Errorf("proposals after reject: %d %s", code, body)
	}
}

func TestAPIUnknownWorkspace(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	ws := serveAPI(t, st) + "/api/workspaces/" + wsB
	for _, c := range []struct{ method, path, author string }{
		{"GET", "/proposals", ""},
		{"GET", "/pin-diff", ""},
		{"POST", "/freeze", authorHeader},
		{"POST", "/unfreeze", authorHeader},
	} {
		if code, body := call(t, c.method, ws+c.path, c.author, ""); code != http.StatusNotFound {
			t.Errorf("%s %s: %d %s", c.method, c.path, code, body)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/distribute/`
Expected: FAIL, `undefined: Register` and `undefined: pinDiffResponse`

- [ ] **Step 3: Write `internal/distribute/api.go`**

```go
package distribute

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
)

// Register adds the freeze and pin proposal endpoints to a. Call it before
// a.Handler().
func Register(a *api.API, st *store.Store) {
	a.Handle("POST /api/workspaces/{id}/freeze", frozenHandler(st, true))
	a.Handle("POST /api/workspaces/{id}/unfreeze", frozenHandler(st, false))
	a.Handle("GET /api/workspaces/{id}/proposals", func(w http.ResponseWriter, r *http.Request) {
		ps, err := Proposals(st, r.PathValue("id"))
		if err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, ps)
	})
	a.Handle("POST /api/workspaces/{id}/proposals/accept", func(w http.ResponseWriter, r *http.Request) {
		author, ok := requireAuthor(w, r)
		if !ok {
			return
		}
		branch, ok := readBranch(w, r)
		if !ok {
			return
		}
		if err := Accept(st, r.PathValue("id"), branch, author); err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]string{"branch": branch})
	})
	a.Handle("POST /api/workspaces/{id}/proposals/reject", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAuthor(w, r); !ok {
			return
		}
		branch, ok := readBranch(w, r)
		if !ok {
			return
		}
		if err := Reject(st, r.PathValue("id"), branch); err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]string{"branch": branch})
	})
	a.Handle("GET /api/workspaces/{id}/pin-diff", func(w http.ResponseWriter, r *http.Request) {
		pd, err := pinDiff(st, r.PathValue("id"))
		if err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, pd)
	})
}

func frozenHandler(st *store.Store, frozen bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		author, ok := requireAuthor(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		set := Unfreeze
		if frozen {
			set = Freeze
		}
		if err := set(st, id, author); err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "frozen": frozen})
	}
}

// requireAuthor answers 401 when the X-Custos-Author header is missing or
// malformed (ruling 2.6).
func requireAuthor(w http.ResponseWriter, r *http.Request) (gitrepo.Signature, bool) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return gitrepo.Signature{}, false
	}
	return author, true
}

// readBranch reads the body {"branch": "custos/pin/<catalog commit>"} and
// answers 400 when it is malformed.
func readBranch(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Branch string `json:"branch"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || body.Branch == "" {
		api.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": `body must be {"branch": "custos/pin/<catalog commit>"}`})
		return "", false
	}
	return body.Branch, true
}

// pinDiffResponse compares a workspace's pin with the catalog's main.
type pinDiffResponse struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Changes Diff   `json:"changes"`
}

func pinDiff(st *store.Store, id string) (pinDiffResponse, error) {
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		return pinDiffResponse{}, err
	}
	main, ok, err := repo.ResolveRef(mainRef)
	if err != nil {
		return pinDiffResponse{}, err
	}
	if !ok {
		return pinDiffResponse{}, fmt.Errorf("workspace %s has no main branch: %w", id, store.ErrNotFound)
	}
	cfg, err := readConfig(repo, main)
	if err != nil {
		return pinDiffResponse{}, err
	}
	head, ok, err := st.CatalogRepo().ResolveRef(mainRef)
	if err != nil {
		return pinDiffResponse{}, err
	}
	if !ok {
		return pinDiffResponse{}, fmt.Errorf("the catalog has no main branch: %w", store.ErrNotFound)
	}
	d, err := CatalogDiff(st.CatalogRepo(), cfg.Catalog.Commit, head)
	if err != nil {
		return pinDiffResponse{}, err
	}
	return pinDiffResponse{From: cfg.Catalog.Commit, To: head, Changes: d}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/distribute/ -v`
Expected: PASS. If an unknown workspace answers 500 instead of 404, `api.WriteError` is not matching `store.ErrNotFound` with `errors.Is`; fix that in `internal/api`, not here.

- [ ] **Step 5: Commit**

```bash
git add internal/distribute/api.go internal/distribute/api_test.go
git commit -m "Serve freeze, unfreeze and pin proposals over the REST API"
```

---

### Task 7: Distribute on start-up and after every catalog push

**Files:**
- Create: `cmd/custos/distribute.go`
- Modify: `cmd/custos/serve.go` (function `openServer`, one added line)
- Test: `cmd/custos/distribute_test.go`

**Interfaces:**
- Consumes: `store.Open`, `(*store.Store).InstallHooks/CreateWorkspace/CatalogRepo/Load`, `server.New(st *store.Store) *server.Server`, `(*server.Server).OnCatalogPush(f func())`, `(*server.Server).Handler() (http.Handler, error)`, `(*server.Server).WithAPI`, `api.New`, `blobs.Open`, and `openServer` with the `openAPI` block plan 2b gave it (2a, 2b); `distribute.Register`, `distribute.Reconcile`, `distribute.Freeze`, `distribute.Proposals` (Tasks 3–6).
- Produces: `func startDistribution(st *store.Store, a *api.API, srv *server.Server, stderr io.Writer)` in package `main`.

- [ ] **Step 1: Write the failing test** `cmd/custos/distribute_test.go`

```go
package main

import (
	"bytes"
	"maps"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/distribute"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/server"
	"github.com/emeland-io/custos/internal/store"
)

const otherWorkspace = "6c7d8e9f-0a1b-4c2d-b3e4-f5a6b7c8d9e0"

var jane = gitrepo.Signature{Name: "Jane Doe", Email: "jane@example.org"}

// lockedBuffer is a bytes.Buffer that the server goroutines and the test
// can use at the same time.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// wired opens a store in a fresh data directory with hooks that run exe and
// wires distribution the way serve does.
func wired(t *testing.T, exe string, log *lockedBuffer) (*store.Store, *server.Server) {
	t.Helper()
	data := t.TempDir()
	st, err := store.Open(data, exe, "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InstallHooks(); err != nil {
		t.Fatal(err)
	}
	bl, err := blobs.Open(filepath.Join(data, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(st)
	startDistribution(st, api.New(st, bl), srv, log)
	return st, srv
}

func pinOf(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	w, _, err := st.Load(id, "")
	if err != nil {
		t.Fatal(err)
	}
	return w.Config.Catalog.Commit
}

func TestCatalogPushIsDistributed(t *testing.T) {
	// The catalog's pre-receive hook runs the custos binary.
	bin := filepath.Join(t.TempDir(), "custos")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/emeland-io/custos/cmd/custos").CombinedOutput(); err != nil {
		t.Fatalf("build custos: %v\n%s", err, out)
	}
	var log lockedBuffer
	st, srv := wired(t, bin, &log)
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	remote := ts.URL + "/git/catalog.git"

	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", "--quiet", remote, "main")
	for _, id := range []string{fixture.WorkspaceID, otherWorkspace} {
		if err := st.CreateWorkspace(id, jane); err != nil {
			t.Fatal(err)
		}
	}
	if err := distribute.Freeze(st, otherWorkspace, jane); err != nil {
		t.Fatal(err)
	}

	c2 := gittest.Commit(t, work, map[string]string{
		fixture.TaskPath(fixture.TaskA, "1.2.0"): fixture.TaskFile(fixture.TaskA, "1.2.0", fixture.TaskA+"@1.1.0"),
	})
	gittest.Run(t, work, "push", "--quiet", remote, "main")

	// OnCatalogPush may run after the response is sent, so poll.
	deadline := time.Now().Add(10 * time.Second)
	for {
		ps, err := distribute.Proposals(st, otherWorkspace)
		if err != nil {
			t.Fatal(err)
		}
		if pinOf(t, st, fixture.WorkspaceID) == c2 && len(ps) == 1 && ps[0].Branch == "custos/pin/"+c2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("catalog commit %s was not distributed; pin %s, proposals %+v; log:\n%s",
				c2, pinOf(t, st, fixture.WorkspaceID), ps, log.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pinOf(t, st, otherWorkspace) == c2 {
		t.Error("the frozen workspace's pin moved")
	}
	if s := log.String(); s != "" {
		t.Errorf("unexpected log:\n%s", s)
	}
}

func TestStartDistributionCatchesUp(t *testing.T) {
	// Catalog commits made while the server was down are distributed when it
	// starts (ruling 2.5); a broken workspace is logged and the others are
	// still updated (spec §7). No push runs, so the hook binary is never used.
	exe := filepath.Join(t.TempDir(), "custos")
	data := t.TempDir()
	st, err := store.Open(data, exe, "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	cat := st.CatalogRepo()
	commit := func(base string, files map[string]string) string {
		req := gitrepo.CommitRequest{Author: gitrepo.Bot, Message: "Change the catalog"}
		if base != "" {
			req.Base, req.Parents = base, []string{base}
		}
		for _, p := range slices.Sorted(maps.Keys(files)) {
			req.Changes = append(req.Changes, gitrepo.Change{Path: p, Data: []byte(files[p])})
		}
		oid, err := cat.WriteCommit(req)
		if err != nil {
			t.Fatal(err)
		}
		if err := cat.UpdateRef("refs/heads/main", oid, base); err != nil {
			t.Fatal(err)
		}
		return oid
	}
	c1 := commit("", fixture.Catalog())
	for _, id := range []string{fixture.WorkspaceID, otherWorkspace} {
		if err := st.CreateWorkspace(id, jane); err != nil {
			t.Fatal(err)
		}
	}
	// Break the first workspace's custos.yaml behind the store's back.
	repo, err := st.WorkspaceRepo(fixture.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	main, _, err := repo.ResolveRef("refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	broken, err := repo.WriteCommit(gitrepo.CommitRequest{
		Base: main, Parents: []string{main}, Author: gitrepo.Bot, Message: "Break custos.yaml",
		Changes: []gitrepo.Change{{Path: "custos.yaml", Data: []byte("workspace: [\n")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRef("refs/heads/main", broken, main); err != nil {
		t.Fatal(err)
	}
	c2 := commit(c1, map[string]string{
		fixture.TaskPath(fixture.TaskA, "1.2.0"): fixture.TaskFile(fixture.TaskA, "1.2.0", fixture.TaskA+"@1.1.0"),
	})

	var log lockedBuffer
	bl, err := blobs.Open(filepath.Join(data, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	startDistribution(st, api.New(st, bl), server.New(st), &log)

	if got := pinOf(t, st, otherWorkspace); got != c2 {
		t.Errorf("pin %s, want %s", got, c2)
	}
	if s := log.String(); !bytes.Contains([]byte(s), []byte("workspace "+fixture.WorkspaceID)) {
		t.Errorf("log does not name the broken workspace:\n%s", s)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/custos/ -run 'TestCatalogPushIsDistributed|TestStartDistributionCatchesUp'`
Expected: FAIL, `undefined: startDistribution`

- [ ] **Step 3: Write `cmd/custos/distribute.go`**

```go
package main

import (
	"fmt"
	"io"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/distribute"
	"github.com/emeland-io/custos/internal/server"
	"github.com/emeland-io/custos/internal/store"
)

// startDistribution adds the freeze and pin proposal endpoints to a,
// distributes catalog changes after every push to catalog.git, and
// distributes once now to catch up with commits made while the server was
// down (ruling 2.5). Call it before a.Handler() and srv.Handler(). Failures
// are written to stderr and never stop the server (spec §7).
func startDistribution(st *store.Store, a *api.API, srv *server.Server, stderr io.Writer) {
	distribute.Register(a, st)
	reconcile := func() {
		if err := distribute.Reconcile(st); err != nil {
			fmt.Fprintf(stderr, "custos serve: distributing the catalog: %v\n", err)
		}
	}
	srv.OnCatalogPush(reconcile)
	reconcile()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./cmd/custos/ -run 'TestCatalogPushIsDistributed|TestStartDistributionCatchesUp' -v`
Expected: PASS. If `TestCatalogPushIsDistributed` times out with the proposal missing, check that 2a's server calls the `OnCatalogPush` functions only for `/git/catalog.git/git-receive-pack`, and after the request, not before.

- [ ] **Step 5: Call it from `serve`** — in `cmd/custos/serve.go`, function `openServer` (plan 2a) holds the store `st` after `store.Open` and `InstallHooks`, and plan 2b ended it with the API block. Replace:

```go
	srv := server.New(st)
	srv.WithAPI(a.Handler())
	return srv, nil
}
```

with:

```go
	srv := server.New(st)
	startDistribution(st, a, srv, stderr)
	srv.WithAPI(a.Handler())
	return srv, nil
}
```

`stderr` is `openServer`'s parameter. `runServe` calls `openServer` before it binds the address, so distribution catches up before the first request. Add nothing else; `startDistribution` imports what it needs.

- [ ] **Step 6: Run the whole suite**

Run: `make test`
Expected: PASS (`go vet` clean, all packages).

- [ ] **Step 7: Commit**

```bash
git add cmd/custos/distribute.go cmd/custos/distribute_test.go cmd/custos/serve.go
git commit -m "Distribute catalog changes at start-up and after every catalog push"
```

---

### Task 8: README and rulings

**Files:**
- Modify: `README.md` (custos.yaml example in `## Workspace`; new section `## Catalog updates and freezing`; the introduction's list of what follows)
- Modify: `docs/superpowers/specs/2026-10-02-custos-design.md` (§11.2 table, new rows at the end)

- [ ] **Step 1: Show the `frozen` flag** — in `README.md`, section `## Workspace`, replace the `custos.yaml` example block with:

````markdown
```yaml
# custos.yaml
workspace: 5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9
catalog:
  url: https://custos.example.org/git/catalog.git
  commit: <full commit hash>
frozen: true          # optional; see "Catalog updates and freezing"
```
````

- [ ] **Step 2: Add the section** — in `README.md`, insert directly before `## Command line`:

````markdown
## Catalog updates and freezing

When the catalog's `main` advances, custos brings every workspace up to
date. It does so right after each push to `catalog.git` and once when the
server starts.

- An **unfrozen** workspace gets a commit by `custos-bot` on its `main`
  that moves `catalog.commit` to the new catalog commit. Existing answers
  stay in effect; tasks with a newer version show as *pending update*.
- A **frozen** workspace keeps its pin. custos opens a **pin proposal**
  instead: the branch `custos/pin/<catalog-commit>`, with one commit that
  changes the pin. A newer catalog commit replaces the open proposal.
  Accepting it merges it into `main` (and is checked like any change of
  `main`); rejecting it deletes it, and custos does not open it again until
  the catalog moves on.

Freeze a workspace when its documentation must not move, for example
during an audit:

```sh
A='X-Custos-Author: Jane Doe <jane@example.org>'
W=http://127.0.0.1:8080/api/workspaces/5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9
curl -X POST -H "$A" $W/freeze
curl $W/pin-diff        # pinned catalog commit compared with catalog main
curl $W/proposals       # open pin proposals and what each one changes
curl -X POST -H "$A" -H 'Content-Type: application/json' \
     -d '{"branch":"custos/pin/<catalog-commit>"}' $W/proposals/accept
curl -X POST -H "$A" $W/unfreeze   # the pin moves to catalog main right away
```

| Endpoint | Purpose |
| --- | --- |
| `POST /api/workspaces/{id}/freeze` | commit `frozen: true` |
| `POST /api/workspaces/{id}/unfreeze` | commit `frozen: false`, then move the pin and delete the proposals |
| `GET /api/workspaces/{id}/proposals` | open proposals: `branch`, `commit`, `from`, `to`, `changes` |
| `POST /api/workspaces/{id}/proposals/accept` | body `{"branch"}`; fast-forward or merge commit into `main` |
| `POST /api/workspaces/{id}/proposals/reject` | body `{"branch"}`; delete the proposal |
| `GET /api/workspaces/{id}/pin-diff` | `from` (pin), `to` (catalog main), `changes` |

`changes` lists `new_versions`, `added` and `superseded` tasks and tells
whether groups (`groups_changed`) or processors and bindings
(`bindings_changed`) differ. Write requests need the `X-Custos-Author`
header.

A proposal is a normal branch, so it can also be merged with Git: fetch
it, merge it into `main` and push. custos deletes the branch once the pin
on `main` has reached the catalog's `main`.

custos rewrites `custos.yaml` when it moves a pin or freezes a workspace;
comments in that file are not kept. A workspace whose `custos.yaml` cannot
be read is skipped and reported on the server's standard error; the other
workspaces are still updated.
````

- [ ] **Step 3: Say what this version implements** — in `README.md`, the paragraph below the link to the design (as plan 2b left it) ends with a sentence on what follows. Replace:

```markdown
them along with the Git history. Distributing catalog updates to workspaces,
processors and the web UI follow.
```

with:

```markdown
them along with the Git history. Catalog updates reach the workspaces on
their own, as a new pin or, for frozen workspaces, as a pin proposal.
Forking and merging workspaces, processors and the web UI follow.
```

- [ ] **Step 4: Check the rulings** — these rows were recorded in §11.2 of `docs/superpowers/specs/2026-10-02-custos-design.md` when the plans were written. Check that rows 2.18–2.21 are present with this content and do not add them again; if the implementation had to differ, change the row to match the code and note it in your report:

```markdown
| 2.18 | A rejected pin proposal is remembered as the ref `refs/custos/rejected-pin/<catalog-commit>` in the workspace; reconcile does not reopen it, and deletes the mark once the catalog's `main` moves on. | Reconcile runs again after every push and at start-up and would otherwise reopen what the engineer rejected. | Hidden refs in workspace repositories. |
| 2.19 | Accepting a pin proposal after `main` moved on writes a merge commit whose tree is `main`'s tree with the proposal's pin, instead of a three-way merge. | Proposal branches are written by custos and change only the pin. | Commits pushed onto a `custos/pin/*` branch by hand are dropped on such an accept (a fast-forward keeps them). |
| 2.20 | Freezing a workspace whose pin lags behind the catalog opens the pin proposal at once; unfreezing moves the pin at once. | The workspace should not wait for the next catalog push. | None known. |
| 2.21 | custos re-encodes `custos.yaml` when it changes it; unknown fields are an error, comments are lost. | The file is small and machine-owned. | Hand-written comments in `custos.yaml` disappear on the next bot commit. |
```

- [ ] **Step 5: Run the full test suite**

Run: `make test`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add README.md docs/superpowers/specs/2026-10-02-custos-design.md
git commit -m "Describe catalog updates, freezing and pin proposals"
```

---

## Self-Review Notes

- **Spec coverage:**
  - §4.1 step 1 (catalog `main` advances by push, validated) → the hook of plan 2a; distribution is triggered after the push in Task 7 (`OnCatalogPush`) and at start-up (ruling 2.5).
  - §4.1 step 2 (diff pinned commit vs new `main`: new versions, added/removed tasks, group, binding and digest changes) → Task 2 (`CatalogDiff`); shown in proposals (Task 5) and `pin-diff` (Task 6). Removed tasks appear as `superseded` (tasks cannot be removed from `main`, only merged).
  - §4.1 step 3 (unfrozen: bot commit moves pin; frozen: proposal branch, newer commit replaces it, accepting merges) → Tasks 3 and 5.
  - §4.1 step 4 (pending update after the pin moves, answers stay) → status of plan 2a computes it from the moved pin; nothing to add here.
  - §4.1 step 5 (binding/digest changes start processor reruns) → phase 3; `Diff.BindingsChanged` is the signal it will use.
  - §4.5 (one queue per repository) → all writes via `store` locks; `mu` serialises distribution (Task 3).
  - §6.1 freeze/unfreeze → REST in Task 6 (UI in phase 4).
  - §7 (inconsistent workspace reported, no crash) → Tasks 3 and 7.
  - Rulings 2.3, 2.5, 2.6, 2.16 → Global Constraints; 2.16 exercised by the backwards diff in Task 2.
- **Placeholder scan:** no TBD/TODO. Task 7 Step 5 quotes the end of `openServer` exactly as plans 2a and 2b leave it. 
- **Type consistency:** `mainRef`, `pinBranch`, `pinRefPrefix`, `rejectedRefPrefix`, `reconcile`, `propose`, `prune`, `pruneRefs`, `conflict`, `readConfig`, `setConfig`, `editConfig`, `configPath`, `pinDiffResponse` are each defined once and used with the same signatures; exported names match the architecture note.
- **Review Focus:** each of the five items has its test in the owning task (Tasks 1, 3, 5, 7).

## Decisions beyond the architecture note

- A package-level mutex in `distribute` serialises `Reconcile`, `Freeze`, `Unfreeze`, `Accept` and `Reject` — two overlapping reconciles could otherwise move a pin back or delete the newer proposal — one distribution at a time per process; a slow reconcile delays freeze/accept requests.
- Rejections are recorded as `refs/custos/rejected-pin/<catalog-commit>` and respected by reconcile, pruned when catalog `main` moves — otherwise the idempotent reconcile at the next push or restart reopens a rejected proposal — extra hidden refs; a rejection silently ends when the catalog advances.
- `Accept` on a diverged `main` writes a merge commit with `main`'s tree plus the proposal's pin, not a `git merge-tree` merge — proposal branches only change the pin and real merging belongs to plan 2d — commits pushed by hand onto a `custos/pin/*` branch are dropped on a diverged accept.
- An existing proposal branch is kept unchanged when `main` moves on; `From` in `PinProposal` is always the current pin on `main` — rewriting the branch on every answer would churn commits — the branch's base can be old, so a fast-forward is rarer.
- `Freeze` also reconciles the workspace (the note says so only for `Unfreeze`) — a frozen workspace with a lagging pin should get its proposal immediately — none known.
- `Reconcile` skips workspaces without `main` and returns nil while the catalog has no `main` — `CreateWorkspaceRepo` (fork) makes repositories before history exists — a workspace stuck without `main` is never reported by distribution.
- Pin moves change only `catalog.commit`; `catalog.url` is left as it is — the requirement to keep other fields, and ruling 2.4 makes the URL informational — URLs from an old public URL stay stale.
- `custos.yaml` is decoded strictly and re-encoded with 2-space indentation; an unknown field fails the workspace's update — never drop data silently — comments in `custos.yaml` are lost; a newer field added by a later phase must be added to `workspace.Config` first.
- New exported function `distribute.Register(a *api.API, st *store.Store)` registers the six endpoints; `cmd/custos` wires it through `startDistribution` — the note names the endpoints but no registration function — one extra exported name.
- Reject requires `X-Custos-Author` although `Reject` takes no author — the convention says every write endpoint requires it — the header is checked but unused until authentication arrives.
- `PinProposal.Branch` and the `{"branch"}` body use the short name `custos/pin/<commit>`; any other name (including malformed hashes) is `store.ErrNotFound` (404) — branch names are what users see in Git — clients sending `refs/heads/…` get 404.
- JSON shapes not in the note: `Diff` and `PinProposal` use snake_case tags with empty lists as `[]`; freeze/unfreeze answer `{"id","frozen"}`, accept/reject `{"branch"}`, pin-diff `{"from","to","changes"}` — consistent with 2b's snake_case rule — clients written against other shapes need changes.
- `CatalogDiff` classifies per task by current version: current only at `to` → `Added`, only at `from` → `Superseded` (also for a backwards move, ruling 2.16), differing → `NewVersions`; `from` "" is an empty catalog; load problems are ignored; groups and bindings are compared semantically, and `BindingsChanged` covers processor image/timeout/network/secrets — `Diff` has no "removed" field and both commits were validated on `main` — a backwards move labels vanished tasks "superseded"; formatting-only edits do not count as changes.
- Reconcile errors at start-up and after pushes are written to `serve`'s stderr and never stop the server — spec §7 — no dashboard report until the UI exists.
- The end-to-end push test lives in `cmd/custos` and builds the custos binary for the catalog hook — it covers the exact wiring `serve` uses — a few seconds of build time in that test.
