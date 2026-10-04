# Phase 3c (Matching and output proposals) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the parsed output of a processor run into an output proposal: match it against what the same answer produced earlier (added / new version / unchanged / removed, documents verified), write it as branch `custos/proposal/<task>/<digest-hex>`, and let an engineer list, accept (all or some), or reject it, with cascading removal of the tasks generated below a removed task.

**Architecture:** `internal/match` is pure: `Plan` reads a loaded workspace and catalog, checks the output against them (processors, origins, depth limit), verifies documents through an `attest.Verifier`, and returns the items and the file changes to write. `internal/proposal` does the repository side through `internal/store`: `Write` replaces the task's proposal branch with one commit by `custos-bot` on top of `main`; `List`/`Get` recompute each proposal's items by comparing the branch tip with the current `main`; `Accept` applies the selected items to the current `main` in one commit by the accepting person (validated, compare-and-swap, retried) and opens cascade proposals; `Reject` deletes the branch.

**Tech Stack:** Go 1.26, the `git` binary (≥ 2.38), `go.yaml.in/yaml/v3` (through `internal/frontmatter`), `github.com/google/uuid`; standard library otherwise. One test runs a real container through Docker (plans 3a and 3b).

**Spec:** [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md), §3.3 (generated tasks, documents), §4.3 (proposals: review as a diff, accept all/some/none), §5.4 (matching), §5.5 (cascading removal, depth limit), §5.6 (verification stored with the document), §8 (unit tests for matching, cascades, depth limit), and §11 (rulings override earlier sections; 2.3 matters most). Shared names, formats and conventions: [docs/superpowers/plans/2026-10-04-phase-3-architecture.md](2026-10-04-phase-3-architecture.md), section "Plan 3c" and "Formats fixed for all plans".

**Requires:** plans 3a and 3b executed first. This plan uses `contract.Output`, `contract.OutputTask`, `contract.OutputDocument`, `contract.Origin`, `contract.NewInput`, `contract.ParseOutput` (3a), `runner.New`, `runner.Config`, `runner.Job`, `(*runner.Runner).Resolve/Run` (3a), `proctest.Image`, `proctest.SigningKey` (3a), `attest.Result`, `attest.Verifier`, `attest.Unverified`, `attest.Canonical`, `attest.StatusVerified/StatusFailed/StatusUnsigned` (3b) and `carabiner.New` (3b) exactly as the architecture note declares them. From phases 1–2 it uses `store.Open`, `store.Store.{WorkspaceRepo, UpdateWorkspace, Load, Lock, CreateWorkspace, CatalogRepo}`, `store.ErrNotFound`, `store.ErrConflict`, `gitrepo.{Repo, Change, CommitRequest, Signature, Bot, ErrRefMoved}`, `Repo.{ResolveRef, TreeFS, Refs, UpdateRef, DeleteRef, WriteCommit}`, `workspace.{Load, Check, Workspace, Generated, Document, Verification, Origin, ProducedBy}`, `task.{Ref, AnswerType, ValidID}`, `semver.{Bump, Step, Minor}`, `frontmatter.Encode`, `catalog.Load`, `fixture.*`, `gittest.Run`. If a signature differs, adapt the call and note it in the task's commit message.

## Global Constraints

- Module `github.com/emeland-io/custos`, Go 1.26. This plan adds no third-party module.
- Git ≥ 2.38 at runtime (ruling 2.9). Tests use real git in temp dirs; the one test that runs a processor uses real Docker and fails (never skips) without it.
- Bot identity `gitrepo.Bot` (`custos-bot <custos-bot@localhost>`). Proposal and cascade commits: author and committer Bot. Accepting: author = the accepting person, committer Bot.
- All repository writes go through `store` (lock, validation of `main`, compare-and-swap; ruling 2.3). A write that loses the compare-and-swap is retried up to three times.
- Proposal branch `custos/proposal/<task-uuid>/<digest-hex>` (64 hex digits, no `sha256:`); cascade branch `custos/proposal/<removed-task-uuid>/cascade`; at most one open proposal per task — writing one deletes the others under `custos/proposal/<task-uuid>/`.
- "Produced earlier for the same answer" = generated tasks (current version per id) and documents in `main` whose `produced_by.task.id` is the answered task's id.
- Depth: catalog task 0, generated task = depth of the task whose answer produced it + 1; output tasks of a run on a task at depth d fail with `match.ErrTooDeep` when d + 1 > max depth (default 8).
- New generated task: version `1.0.0`; changed task: `semver.Bump(old, bump)` (`bump` "" counts as `minor`) with `previous: [{id, old}]`. Task content = title, body, answer_type, choices, origin, processor.
- Documents compare on `attest.Result.Payload` (not envelope or signature bytes); a new document gets a fresh UUID v4 file name, a changed one keeps its file.
- `produced_by` of every written item: `{task: {id, version: answer's task_version}, processor, digest: "sha256:<hex>", answer_commit}`.
- Removing a generated task deletes `generated/<uuid>/*.md` and `answers/<uuid>.md`; removing a document deletes `documents/<uuid>.json`.
- Exported names exactly as in the architecture note: `match.{ErrTooDeep, Run, Action, Added, NewVersion, Unchanged, Removed, Item, Changes, Plan, Depth, Below}`, `proposal.{BranchPrefix, Branch, Proposal, Write, List, Get, Accept, Reject, ErrInvalid}` — plus the additions listed under "Decisions beyond the architecture note".
- Commit messages: one imperative sentence without prefix, a blank line, `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **A processor emits a title or match key that YAML cannot write plainly** (leading tab, `: `, `#`) or a body with Windows line endings. The file must read back exactly, pass validation, and a second run with the same output must change nothing (test `TestPlanWritesTextYAMLCannotWritePlainly` in Task 2).
2. **A processor re-signs a document it already produced.** Only the signature changed, so the item must be unchanged and no file written (test `TestPlanResignedDocumentIsUnchanged` in Task 3).
3. **The engineer keeps working while a proposal is open.** Accepting must build on the current `main` and keep the new answers; a run whose output `main` already holds must open no proposal (tests `TestAcceptAfterMainMovedOn` in Task 5 and `TestWriteChangesMainAlreadyHasOpenNothing` in Task 4).
4. **Accept is clicked twice, or by two people at once.** Exactly one commit lands, the other calls answer 404 or change nothing (test `TestConcurrentAccepts` in Task 5).
5. **Somebody pushes a branch below `custos/proposal/` by hand** with a name custos never writes. Listing must ignore it instead of failing (test `TestListIgnoresForeignBranches` in Task 4).

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/match/doc.go` | Package doc |
| `internal/match/depth.go` | `Current`, `Depth`, `Below`: the producer chain of generated tasks |
| `internal/match/match.go` | `Run`, `Action`, `Item`, `Changes`, `CompareItems`, `Plan`, the output checks and the depth limit |
| `internal/match/tasks.go` | Matching output tasks; encoding generated task files |
| `internal/match/documents.go` | Matching and verifying output documents; encoding document files |
| `internal/match/*_test.go` | Pure tests on in-memory trees; `sign_test.go` runs the real `sign` image |
| `internal/proposal/proposal.go` | Branch names, `Proposal`, `Write`, `List`, `Get`, `Reject` |
| `internal/proposal/diff.go` | Recomputing a proposal's items (and the changes that apply them) from its tip against `main` |
| `internal/proposal/accept.go` | `Accept`, selection, cascade proposals |
| `internal/proposal/*_test.go` | Tests on real repositories in temp dirs; `helpers_test.go` holds the shared helpers |

### How a proposal's items are recomputed (settled here, as the note asks)

A proposal branch holds a whole workspace tree: `main` at the time of `Write` plus the run's changes. `List`, `Get` and `Accept` compare the branch tip with the **current** `main`, restricted to a scope:

- **ordinary proposal of task T:** the generated tasks whose current version has `produced_by.task.id == T`, and the documents with `produced_by.task.id == T`, on either side;
- **cascade of removed task R:** the generated tasks `match.Below(R)` on either side, and the documents produced by R or by one of those tasks.

Within the scope, generated tasks are compared by id, documents by file path:

| Tip | Main | Item | Changes that `Accept` applies to `main` |
| --- | --- | --- | --- |
| task id present | absent | `added` | write all version files of the id from the tip |
| present | has the tip's current version | `unchanged` | none |
| present | present, lacks that version | `new-version` (From = main's current) | write the tip's version files `main` lacks |
| absent | present | `removed` | delete all version files and `answers/<id>.md` |
| document present | absent | `added` | write the tip's file |
| present | same bytes | `unchanged` | none |
| present | other bytes | `new-version` | write the tip's file |
| absent | present | `removed` | delete the file |

Unchanged output items stay in the tree of the branch (they are part of `main`), so they are listed as `unchanged`; an item that was accepted meanwhile also turns `unchanged`. Selectors are `task:<match_key>` and `document:<match_key>`; every item with that kind and key is selected.

---

### Task 1: Depth and descendants of generated tasks

**Files:**
- Create: `internal/match/doc.go`, `internal/match/depth.go`
- Test: `internal/match/helpers_test.go`, `internal/match/depth_test.go`

**Interfaces:**
- Consumes: `workspace.Workspace` (`Graph *task.Graph`, `Generated map[string]*workspace.Generated` by path), `task.Graph.{Has, Current, Versions, Tasks}`, `workspace.Generated.ProducedBy.Task.ID`.
- Produces:
  - `func Current(w *workspace.Workspace, id string) *workspace.Generated` — current version of generated task id (highest version when there is not exactly one head), nil when unknown.
  - `func Depth(w *workspace.Workspace, id string) int` — ≥ 1 for a generated task, -1 when id is not a generated task of w, `math.MaxInt` on a cycle.
  - `func Below(w *workspace.Workspace, id string) []string` — sorted, transitive, never contains id.
  - Test helpers (package `match`, used by Tasks 2–3): `digest`, `gid(n int) string`, `genPath(id, version string) string`, `genFile(id, version, key, producer string, previous ...string) string`, `answerFile(id string) string`, `emptyWorkspace() map[string]string`, `with(files, more map[string]string) map[string]string`, `load(t, files) *workspace.Workspace`, `valid(t, files)`, `fixtureCatalog(t) *catalog.Catalog`.

- [ ] **Step 1: Write the test helpers** `internal/match/helpers_test.go`

```go
package match

import (
	"fmt"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/workspace"
)

// digest is the image digest of the runs in these tests.
const digest = "sha256:" + fixture.SHA256

// gid returns the n-th test UUID v4.
func gid(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

// genPath returns the path of a generated task version.
func genPath(id, version string) string { return "generated/" + id + "/" + version + ".md" }

// genFile returns a generated text task version with match key key, made
// from the answer to task producer. Each previous entry is "<id>@<version>".
func genFile(id, version, key, producer string, previous ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nid: %s\nversion: %s\ntitle: Generated %s\nanswer_type: text\n", id, version, key)
	if len(previous) > 0 {
		b.WriteString("previous:\n")
		for _, p := range previous {
			pid, pv, _ := strings.Cut(p, "@")
			fmt.Fprintf(&b, "  - id: %s\n    version: %s\n", pid, pv)
		}
	}
	fmt.Fprintf(&b, "match_key: %s\nproduced_by:\n  task:\n    id: %s\n    version: 1.0.0\n  processor: host-scanner\n"+
		"  digest: %s\n  answer_commit: %s\n---\n\nBody of %s.\n", key, producer, digest, fixture.Commit, key)
	return b.String()
}

// answerFile returns a text answer to task id at version 1.0.0.
func answerFile(id string) string {
	return "---\ntask: " + id + "\ntask_version: 1.0.0\ntype: text\nvalue: done\n---\n"
}

// emptyWorkspace returns the files of a workspace without answers or output.
func emptyWorkspace() map[string]string {
	return map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, fixture.Commit)}
}

// with returns files plus more.
func with(files map[string]string, more map[string]string) map[string]string {
	out := map[string]string{}
	for p, c := range files {
		out[p] = c
	}
	for p, c := range more {
		out[p] = c
	}
	return out
}

// load reads a workspace tree and ignores its problems; tests that need a
// valid tree check it with valid.
func load(t *testing.T, files map[string]string) *workspace.Workspace {
	t.Helper()
	w, _ := workspace.Load(fixture.MapFS(files))
	return w
}

// valid fails the test unless the tree passes workspace.Check.
func valid(t *testing.T, files map[string]string) {
	t.Helper()
	fixture.WantNone(t, workspace.Check(fixture.MapFS(files)))
}

// fixtureCatalog loads fixture.Catalog: TaskA, TaskB and the processor
// host-scanner.
func fixtureCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, ps := catalog.Load(fixture.MapFS(fixture.Catalog()))
	fixture.WantNone(t, ps)
	return c
}
```

- [ ] **Step 2: Write the failing test** `internal/match/depth_test.go`

```go
package match

import (
	"math"
	"slices"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
)

// chain is a workspace whose generated tasks form the chain
// TaskB → gid(1) → gid(2) → gid(3), plus gid(4) made from TaskA's answer.
func chain() map[string]string {
	return with(emptyWorkspace(), map[string]string{
		genPath(gid(1), "1.0.0"): genFile(gid(1), "1.0.0", "one", fixture.TaskB),
		genPath(gid(2), "1.0.0"): genFile(gid(2), "1.0.0", "two", gid(1)),
		genPath(gid(3), "1.0.0"): genFile(gid(3), "1.0.0", "three", gid(2)),
		genPath(gid(4), "1.0.0"): genFile(gid(4), "1.0.0", "four", fixture.TaskA),
	})
}

func TestDepth(t *testing.T) {
	files := chain()
	valid(t, files)
	w := load(t, files)
	for _, c := range []struct {
		id   string
		want int
	}{
		{gid(1), 1}, {gid(2), 2}, {gid(3), 3}, {gid(4), 1},
		{fixture.TaskB, -1}, // a catalog task is not in the workspace
		{gid(99), -1},
	} {
		if got := Depth(w, c.id); got != c.want {
			t.Errorf("Depth(%s) = %d, want %d", c.id, got, c.want)
		}
	}
}

func TestDepthCycle(t *testing.T) {
	w := load(t, with(emptyWorkspace(), map[string]string{
		genPath(gid(5), "1.0.0"): genFile(gid(5), "1.0.0", "five", gid(6)),
		genPath(gid(6), "1.0.0"): genFile(gid(6), "1.0.0", "six", gid(5)),
		genPath(gid(7), "1.0.0"): genFile(gid(7), "1.0.0", "seven", gid(6)),
	}))
	for _, id := range []string{gid(5), gid(6), gid(7)} {
		if got := Depth(w, id); got != math.MaxInt {
			t.Errorf("Depth(%s) = %d, want math.MaxInt for a cycle", id, got)
		}
	}
}

func TestDepthFollowsCurrentVersion(t *testing.T) {
	// Version 1.0.0 of gid(2) was made from TaskA's answer, the current
	// version 1.1.0 from gid(1)'s.
	w := load(t, with(emptyWorkspace(), map[string]string{
		genPath(gid(1), "1.0.0"): genFile(gid(1), "1.0.0", "one", fixture.TaskB),
		genPath(gid(2), "1.0.0"): genFile(gid(2), "1.0.0", "two", fixture.TaskA),
		genPath(gid(2), "1.1.0"): genFile(gid(2), "1.1.0", "two", gid(1), gid(2)+"@1.0.0"),
	}))
	if got := Depth(w, gid(2)); got != 2 {
		t.Errorf("Depth = %d, want 2", got)
	}
	if g := Current(w, gid(2)); g == nil || g.Version != "1.1.0" {
		t.Errorf("Current = %+v, want version 1.1.0", g)
	}
	if g := Current(w, gid(99)); g != nil {
		t.Errorf("Current of an unknown task = %+v, want nil", g)
	}
}

func TestCurrentWithTwoHeads(t *testing.T) {
	w := load(t, with(emptyWorkspace(), map[string]string{
		genPath(gid(1), "1.0.0"): genFile(gid(1), "1.0.0", "one", fixture.TaskB),
		genPath(gid(1), "2.0.0"): genFile(gid(1), "2.0.0", "one", fixture.TaskB),
	}))
	if g := Current(w, gid(1)); g == nil || g.Version != "2.0.0" {
		t.Errorf("Current = %+v, want the highest version 2.0.0", g)
	}
}

func TestBelow(t *testing.T) {
	w := load(t, chain())
	for _, c := range []struct {
		id   string
		want []string
	}{
		{fixture.TaskB, []string{gid(1), gid(2), gid(3)}},
		{gid(1), []string{gid(2), gid(3)}},
		{gid(3), nil},
		{fixture.TaskA, []string{gid(4)}},
		{gid(99), nil},
	} {
		if got := Below(w, c.id); !slices.Equal(got, c.want) {
			t.Errorf("Below(%s) = %v, want %v", c.id, got, c.want)
		}
	}
}

func TestBelowCycle(t *testing.T) {
	w := load(t, with(emptyWorkspace(), map[string]string{
		genPath(gid(5), "1.0.0"): genFile(gid(5), "1.0.0", "five", gid(6)),
		genPath(gid(6), "1.0.0"): genFile(gid(6), "1.0.0", "six", gid(5)),
	}))
	if got := Below(w, gid(5)); !slices.Equal(got, []string{gid(6)}) {
		t.Errorf("Below = %v, want [%s]", got, gid(6))
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/match/`
Expected: FAIL — `undefined: Depth`, `undefined: Current`, `undefined: Below` (and no package doc yet).

- [ ] **Step 4: Write the package doc** `internal/match/doc.go`

```go
// Package match compares the output of a processor run with what the same
// answer produced earlier (spec §5.4) and plans the files of an output
// proposal: new generated tasks and documents, new versions, removals. It
// reads only the trees it is given and never touches Git.
package match
```

- [ ] **Step 5: Implement** `internal/match/depth.go`

```go
package match

import (
	"maps"
	"math"
	"slices"

	"github.com/emeland-io/custos/internal/workspace"
)

// Current returns the current version of generated task id in w: the
// version no other version lists as previous, or the highest version when
// there is not exactly one such version (a broken history that validation
// reports). nil when w has no generated task id.
func Current(w *workspace.Workspace, id string) *workspace.Generated {
	if !w.Graph.Has(id) {
		return nil
	}
	v, ok := w.Graph.Current(id)
	if !ok {
		vs := w.Graph.Versions(id)
		v = vs[len(vs)-1]
	}
	return w.Generated[v.Path]
}

// producer returns produced_by.task.id of the current version of generated
// task id, or "" when id is not a generated task of w.
func producer(w *workspace.Workspace, id string) string {
	if g := Current(w, id); g != nil {
		return g.ProducedBy.Task.ID
	}
	return ""
}

// Depth returns the depth of task id in w: a generated task has the depth of
// the task whose answer produced it plus one, and a task that is not a
// generated task of w ends the chain with depth 0 (a catalog task; w does
// not hold the catalog). Depth returns -1 when id itself is not a generated
// task of w, so callers decide whether it is a catalog task, and
// math.MaxInt when the chain runs in a cycle, which counts as too deep.
func Depth(w *workspace.Workspace, id string) int {
	if !w.Graph.Has(id) {
		return -1
	}
	seen := map[string]bool{}
	d := 0
	for w.Graph.Has(id) {
		if seen[id] {
			return math.MaxInt
		}
		seen[id] = true
		d++
		id = producer(w, id)
	}
	return d
}

// Below returns the generated task ids whose produced_by.task.id is id,
// transitively, sorted (cascading removal). id itself is never part of the
// result, also when a cycle leads back to it.
func Below(w *workspace.Workspace, id string) []string {
	children := map[string][]string{}
	for _, t := range w.Graph.Tasks() {
		p := producer(w, t)
		children[p] = append(children[p], t)
	}
	found := map[string]bool{}
	queue := []string{id}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, c := range children[next] {
			if c != id && !found[c] {
				found[c] = true
				queue = append(queue, c)
			}
		}
	}
	return slices.Sorted(maps.Keys(found))
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/match/ && go vet ./internal/match/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/match
git commit -m "Compute the depth and the descendants of generated tasks

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Match output tasks (`match.Plan`)

**Files:**
- Create: `internal/match/match.go`, `internal/match/tasks.go`
- Test: `internal/match/plan_test.go`

**Interfaces:**
- Consumes: Task 1 (`Current`, `Depth`, test helpers); `contract.Output`, `contract.OutputTask`, `contract.Origin` (3a); `attest.Verifier`, `attest.Unverified` (3b); `catalog.Catalog.{Tasks, Registry.Processors}`; `semver.Bump`; `frontmatter.Encode(v any, body string) ([]byte, error)`; `gitrepo.Change`.
- Produces (used by Task 3, plan 3c's `proposal`, 3d's runs and 3e's `processor test`):
  - `var ErrTooDeep`, `var ErrInvalidOutput = errors.New("invalid processor output")`, `const DefaultMaxDepth = 8`
  - `type Run`, `type Action` with `Added`, `NewVersion`, `Unchanged`, `Removed`; `type Item`; `const KindTask = "task"`, `const KindDocument = "document"`; `type Changes`
  - `func CompareItems(a, b Item) int` — the order of `Changes.Items` (Kind, MatchKey, ID)
  - `func Plan(run Run, out *contract.Output) (*Changes, error)` — `Run.NewID` nil = `uuid.NewString`, `Run.Verifier` nil = `attest.Unverified`, `Run.MaxDepth` ≤ 0 = 8
  - unexported, used by Task 3: `type planner` with `run`, `produced`, `items`, `files`, methods `write(path string, data []byte)`, `remove(path string)`.

- [ ] **Step 1: Write the failing test** `internal/match/plan_test.go`

```go
package match

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

// newRun returns a run of host-scanner on the answer to fixture.TaskB in w;
// new ids are gid(100), gid(101), …
func newRun(t *testing.T, files map[string]string) Run {
	t.Helper()
	next := 100
	return Run{
		Workspace:    load(t, files),
		Catalog:      fixtureCatalog(t),
		Task:         task.Ref{ID: fixture.TaskB, Version: "1.0.0"},
		Processor:    "host-scanner",
		Digest:       digest,
		AnswerCommit: fixture.Commit,
		Verifier:     attest.Unverified,
		NewID: func() string {
			next++
			return gid(next - 1)
		},
	}
}

// tasksOnly is fixture.Workspace without its document: TaskA's answer and
// the generated task TaskC (key host:web-01) made from TaskB's answer.
func tasksOnly() map[string]string {
	files := fixture.Workspace()
	delete(files, "documents/"+fixture.DocID+".json")
	return files
}

// webTask is the output task that reproduces fixture.GeneratedFile.
func webTask() contract.OutputTask {
	return contract.OutputTask{
		MatchKey: "host:web-01", Title: "Host web-01", Body: "When was web-01 last patched?",
		AnswerType: task.AnswerTimestamp, Origin: &contract.Origin{ID: fixture.TaskB},
	}
}

// apply returns files with changes applied.
func apply(files map[string]string, changes []gitrepo.Change) map[string]string {
	out := with(files, nil)
	for _, c := range changes {
		if c.Delete {
			delete(out, c.Path)
		} else {
			out[c.Path] = string(c.Data)
		}
	}
	return out
}

func paths(changes []gitrepo.Change) []string {
	var ps []string
	for _, c := range changes {
		mark := "+"
		if c.Delete {
			mark = "-"
		}
		ps = append(ps, mark+c.Path)
	}
	return ps
}

func wantItems(t *testing.T, got []Item, want ...Item) {
	t.Helper()
	if !slices.EqualFunc(got, want, func(a, b Item) bool {
		return a.Kind == b.Kind && a.MatchKey == b.MatchKey && a.Action == b.Action && a.ID == b.ID &&
			a.From == b.From && a.To == b.To && a.Title == b.Title
	}) {
		t.Errorf("items:\n%s\nwant:\n%s", fmtItems(got), fmtItems(want))
	}
}

func fmtItems(items []Item) string {
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "  %s %s %s %s %s→%s %q\n", it.Kind, it.MatchKey, it.Action, it.ID, it.From, it.To, it.Title)
	}
	return b.String()
}

func TestPlanAddsTasks(t *testing.T) {
	files := with(emptyWorkspace(), nil)
	run := newRun(t, files)
	web := webTask()
	web.Processor = "host-scanner"
	db := contract.OutputTask{MatchKey: "host:db-01", Title: "Host db-01", Body: "Is db-01 patched?\n",
		AnswerType: task.AnswerChoice, Choices: []string{"yes", "no"}}
	ch, err := Plan(run, &contract.Output{Tasks: []contract.OutputTask{web, db}})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items,
		Item{Kind: KindTask, MatchKey: "host:db-01", Action: Added, ID: gid(101), To: "1.0.0", Title: "Host db-01"},
		Item{Kind: KindTask, MatchKey: "host:web-01", Action: Added, ID: gid(100), To: "1.0.0", Title: "Host web-01"},
	)
	if got, want := paths(ch.Files), []string{"+" + genPath(gid(100), "1.0.0"), "+" + genPath(gid(101), "1.0.0")}; !slices.Equal(got, want) {
		t.Fatalf("files %v, want %v", got, want)
	}
	wantFile := "---\nid: " + gid(100) + "\nversion: 1.0.0\ntitle: Host web-01\nanswer_type: timestamp\n" +
		"origin:\n  id: " + fixture.TaskB + "\nmatch_key: host:web-01\nprocessor: host-scanner\nproduced_by:\n" +
		"  task:\n    id: " + fixture.TaskB + "\n    version: 1.0.0\n  processor: host-scanner\n" +
		"  digest: " + digest + "\n  answer_commit: " + fixture.Commit + "\n---\n\nWhen was web-01 last patched?\n"
	if got := string(ch.Files[0].Data); got != wantFile {
		t.Errorf("file:\n%s\nwant:\n%s", got, wantFile)
	}
	after := apply(files, ch.Files)
	valid(t, after)
	g := Current(load(t, after), gid(101))
	if g == nil || !slices.Equal(g.Choices, []string{"yes", "no"}) || g.ProducedBy.Task.ID != fixture.TaskB || g.Body != "Is db-01 patched?\n" {
		t.Errorf("db-01 read back as %+v", g)
	}
}

func TestPlanUnchangedTask(t *testing.T) {
	run := newRun(t, tasksOnly())
	web := webTask()
	web.Body = "When was web-01 last patched?\r\n" // line endings do not count
	web.Bump = "major"                             // bump is not content
	ch, err := Plan(run, &contract.Output{Tasks: []contract.OutputTask{web}})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items, Item{Kind: KindTask, MatchKey: "host:web-01", Action: Unchanged, ID: fixture.TaskC, From: "1.0.0", To: "1.0.0", Title: "Host web-01"})
	if len(ch.Files) != 0 {
		t.Errorf("files %v, want none", paths(ch.Files))
	}
}

func TestPlanNewTaskVersion(t *testing.T) {
	for _, c := range []struct {
		bump string
		want string
	}{{"", "1.1.0"}, {"patch", "1.0.1"}, {"minor", "1.1.0"}, {"major", "2.0.0"}} {
		t.Run("bump="+c.bump, func(t *testing.T) {
			files := tasksOnly()
			web := webTask()
			web.Title = "Host web-01 (production)"
			web.Bump = semver.Step(c.bump)
			ch, err := Plan(newRun(t, files), &contract.Output{Tasks: []contract.OutputTask{web}})
			if err != nil {
				t.Fatal(err)
			}
			wantItems(t, ch.Items, Item{Kind: KindTask, MatchKey: "host:web-01", Action: NewVersion, ID: fixture.TaskC,
				From: "1.0.0", To: c.want, Title: "Host web-01 (production)"})
			if got := paths(ch.Files); !slices.Equal(got, []string{"+" + genPath(fixture.TaskC, c.want)}) {
				t.Fatalf("files %v", got)
			}
			after := apply(files, ch.Files)
			valid(t, after)
			g := Current(load(t, after), fixture.TaskC)
			if g.Version != c.want || !slices.Equal(g.Previous, []task.Ref{{ID: fixture.TaskC, Version: "1.0.0"}}) {
				t.Errorf("new version %s, previous %v", g.Version, g.Previous)
			}
		})
	}
}

func TestPlanEachFieldIsContent(t *testing.T) {
	for name, change := range map[string]func(*contract.OutputTask){
		"body":        func(o *contract.OutputTask) { o.Body = "Other body" },
		"answer_type": func(o *contract.OutputTask) { o.AnswerType = task.AnswerText },
		"choices": func(o *contract.OutputTask) {
			o.AnswerType, o.Choices = task.AnswerChoice, []string{"yes", "no"}
		},
		"origin":         func(o *contract.OutputTask) { o.Origin = &contract.Origin{ID: fixture.TaskA} },
		"origin version": func(o *contract.OutputTask) { o.Origin.Version = "1.0.0" },
		"no origin":      func(o *contract.OutputTask) { o.Origin = nil },
		"processor":      func(o *contract.OutputTask) { o.Processor = "host-scanner" },
	} {
		t.Run(name, func(t *testing.T) {
			web := webTask()
			change(&web)
			ch, err := Plan(newRun(t, tasksOnly()), &contract.Output{Tasks: []contract.OutputTask{web}})
			if err != nil {
				t.Fatal(err)
			}
			if len(ch.Items) != 1 || ch.Items[0].Action != NewVersion {
				t.Errorf("items:\n%s", fmtItems(ch.Items))
			}
		})
	}
}

func TestPlanRemovesTasks(t *testing.T) {
	files := with(tasksOnly(), map[string]string{
		"answers/" + fixture.TaskC + ".md": "---\ntask: " + fixture.TaskC + "\ntask_version: 1.0.0\ntype: timestamp\nvalue: 2026-10-01T10:00:00Z\n---\n",
		genPath(gid(1), "1.0.0"):           genFile(gid(1), "1.0.0", "host:db-01", fixture.TaskB),
		genPath(gid(1), "1.1.0"):           genFile(gid(1), "1.1.0", "host:db-01", fixture.TaskB, gid(1)+"@1.0.0"),
		genPath(gid(2), "1.0.0"):           genFile(gid(2), "1.0.0", "other", fixture.TaskA), // another answer's output
	})
	valid(t, files)
	ch, err := Plan(newRun(t, files), &contract.Output{})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items,
		Item{Kind: KindTask, MatchKey: "host:db-01", Action: Removed, ID: gid(1), From: "1.1.0", Title: "Generated host:db-01"},
		Item{Kind: KindTask, MatchKey: "host:web-01", Action: Removed, ID: fixture.TaskC, From: "1.0.0", Title: "Host web-01"},
	)
	want := []string{"-" + genPath(gid(1), "1.0.0"), "-" + genPath(gid(1), "1.1.0"),
		"-answers/" + fixture.TaskC + ".md", "-" + genPath(fixture.TaskC, "1.0.0")}
	slices.Sort(want)
	if got := paths(ch.Files); !slices.Equal(got, want) {
		t.Errorf("files %v, want %v", got, want)
	}
	valid(t, apply(files, ch.Files))
}

func TestPlanDuplicateEarlierKeys(t *testing.T) {
	// Two generated tasks of TaskB's answer share a key, as only a hand
	// edit can do: the smaller id is matched, the other removed.
	files := with(emptyWorkspace(), map[string]string{
		genPath(gid(1), "1.0.0"): genFile(gid(1), "1.0.0", "k", fixture.TaskB),
		genPath(gid(2), "1.0.0"): genFile(gid(2), "1.0.0", "k", fixture.TaskB),
	})
	out := contract.OutputTask{MatchKey: "k", Title: "Generated k", Body: "Body of k.", AnswerType: task.AnswerText}
	ch, err := Plan(newRun(t, files), &contract.Output{Tasks: []contract.OutputTask{out}})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items,
		Item{Kind: KindTask, MatchKey: "k", Action: Unchanged, ID: gid(1), From: "1.0.0", To: "1.0.0", Title: "Generated k"},
		Item{Kind: KindTask, MatchKey: "k", Action: Removed, ID: gid(2), From: "1.0.0", Title: "Generated k"},
	)
}

func TestPlanChecksOutput(t *testing.T) {
	run := newRun(t, tasksOnly())
	ok := []contract.OutputTask{
		{MatchKey: "a", Title: "A", AnswerType: task.AnswerText, Origin: &contract.Origin{ID: fixture.TaskA}}, // catalog task
		{MatchKey: "b", Title: "B", AnswerType: task.AnswerText, Origin: &contract.Origin{ID: fixture.TaskC}}, // generated task
		{MatchKey: "c", Title: "C", AnswerType: task.AnswerText, Processor: "host-scanner"},
	}
	if _, err := Plan(run, &contract.Output{Tasks: ok}); err != nil {
		t.Fatalf("valid output: %v", err)
	}
	bad := &contract.Output{
		Tasks: []contract.OutputTask{
			{MatchKey: "a", Title: "A", AnswerType: task.AnswerText, Processor: "nope"},
			{MatchKey: "b", Title: "B", AnswerType: task.AnswerText, Origin: &contract.Origin{ID: gid(77)}},
			{MatchKey: "b", Title: "B again", AnswerType: task.AnswerText},
		},
	}
	_, err := Plan(run, bad)
	if !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("err %v, want ErrInvalidOutput", err)
	}
	for _, s := range []string{`processor "nope" is not registered`, "origin " + gid(77), `task "b": match_key appears more than once`} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q lacks %q", err, s)
		}
	}
}

// chainTo returns a workspace whose generated tasks gid(1) … gid(n) form a
// chain below TaskB, so gid(n) has depth n.
func chainTo(n int) map[string]string {
	files := emptyWorkspace()
	parent := fixture.TaskB
	for i := 1; i <= n; i++ {
		files[genPath(gid(i), "1.0.0")] = genFile(gid(i), "1.0.0", fmt.Sprintf("level-%d", i), parent)
		parent = gid(i)
	}
	return files
}

func TestPlanDepthLimit(t *testing.T) {
	one := &contract.Output{Tasks: []contract.OutputTask{{MatchKey: "deeper", Title: "Deeper", AnswerType: task.AnswerText}}}
	for _, c := range []struct {
		name    string
		depth   int // depth of the answered task
		max     int
		out     *contract.Output
		tooDeep bool
	}{
		{"catalog task", 0, 8, one, false},
		{"below the limit", 7, 8, one, false},
		{"at the limit", 8, 8, one, true},
		{"default limit", 8, 0, one, true},
		{"default limit not reached", 7, 0, one, false},
		{"small limit", 3, 3, one, true},
		{"no tasks in the output", 8, 8, &contract.Output{}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			run := newRun(t, chainTo(c.depth))
			if c.depth > 0 {
				run.Task = task.Ref{ID: gid(c.depth), Version: "1.0.0"}
			}
			run.MaxDepth = c.max
			_, err := Plan(run, c.out)
			if got := errors.Is(err, ErrTooDeep); got != c.tooDeep || (!c.tooDeep && err != nil) {
				t.Errorf("err %v, want too deep: %v", err, c.tooDeep)
			}
		})
	}
}

func TestPlanCycleIsTooDeep(t *testing.T) {
	files := with(emptyWorkspace(), map[string]string{
		genPath(gid(5), "1.0.0"): genFile(gid(5), "1.0.0", "five", gid(6)),
		genPath(gid(6), "1.0.0"): genFile(gid(6), "1.0.0", "six", gid(5)),
	})
	run := newRun(t, files)
	run.Task = task.Ref{ID: gid(5), Version: "1.0.0"}
	out := &contract.Output{Tasks: []contract.OutputTask{{MatchKey: "x", Title: "X", AnswerType: task.AnswerText}}}
	if _, err := Plan(run, out); !errors.Is(err, ErrTooDeep) || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("err %v, want ErrTooDeep naming the cycle", err)
	}
}

func TestPlanUnknownTask(t *testing.T) {
	run := newRun(t, emptyWorkspace())
	run.Task = task.Ref{ID: gid(42), Version: "1.0.0"}
	_, err := Plan(run, &contract.Output{})
	if err == nil || errors.Is(err, ErrTooDeep) || !strings.Contains(err.Error(), gid(42)) {
		t.Errorf("err %v, want an error naming the unknown task", err)
	}
}

func TestPlanWritesTextYAMLCannotWritePlainly(t *testing.T) {
	files := emptyWorkspace()
	out := &contract.Output{Tasks: []contract.OutputTask{{
		MatchKey: "\tkey: with colon", Title: "\tIndented: title #1", Body: "line one\r\nline two",
		AnswerType: task.AnswerText,
	}}}
	ch, err := Plan(newRun(t, files), out)
	if err != nil {
		t.Fatal(err)
	}
	after := apply(files, ch.Files)
	valid(t, after)
	g := Current(load(t, after), gid(100))
	if g == nil || g.Title != "\tIndented: title #1" || g.MatchKey != "\tkey: with colon" || g.Body != "line one\nline two\n" {
		t.Fatalf("read back as %+v", g)
	}
	// Running again with the same output changes nothing.
	again, err := Plan(newRun(t, after), out)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Files) != 0 || again.Items[0].Action != Unchanged {
		t.Errorf("second run: files %v, items:\n%s", paths(again.Files), fmtItems(again.Items))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/match/`
Expected: FAIL — `undefined: Run`, `undefined: Plan`, `undefined: Item` …

- [ ] **Step 3: Implement the types, the checks and `Plan`** `internal/match/match.go`

```go
package match

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// ErrTooDeep fails a run whose output tasks would lie deeper than the
// maximum depth of generated tasks (spec §5.5).
var ErrTooDeep = errors.New("output exceeds the maximum depth of generated tasks")

// ErrInvalidOutput fails a run whose output names an unknown processor or
// origin, or repeats a match key. Plan wraps it with all such problems.
var ErrInvalidOutput = errors.New("invalid processor output")

// DefaultMaxDepth is the depth limit used when Run.MaxDepth is not positive.
const DefaultMaxDepth = 8

// Run describes the processor run whose output Plan matches.
type Run struct {
	Workspace    *workspace.Workspace // main at AnswerCommit
	Catalog      *catalog.Catalog     // at the workspace's pin
	Task         task.Ref             // the answered task (catalog or generated) and the answer's task_version
	Processor    string
	Digest       string // "sha256:<hex>"
	AnswerCommit string
	MaxDepth     int
	Verifier     attest.Verifier
	NewID        func() string // UUID v4 source; tests make it deterministic
}

// Action says what a proposal does with one item.
type Action string

const (
	Added      Action = "added"
	NewVersion Action = "new-version"
	Unchanged  Action = "unchanged"
	Removed    Action = "removed"
)

// Item is one generated task or document of a proposal.
type Item struct {
	Kind         string // "task" | "document"
	MatchKey     string
	Action       Action
	ID           string                  // generated task id or document file uuid
	From, To     string                  // task versions (From empty when added, To empty when removed)
	Title        string                  // task title or document name
	Verification *workspace.Verification // documents only, nil when removed
}

// Item kinds.
const (
	KindTask     = "task"
	KindDocument = "document"
)

// Changes is the result of Plan.
type Changes struct {
	Items []Item           // sorted by Kind, then MatchKey; includes Unchanged
	Files []gitrepo.Change // what to write on a branch based on main (empty when nothing changed)
}

// CompareItems orders items by Kind, then MatchKey, then ID, the order of
// Changes.Items.
func CompareItems(a, b Item) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.MatchKey, b.MatchKey), cmp.Compare(a.ID, b.ID))
}

// Plan matches out against what the answer's task produced earlier (§5.4):
// checks that every named processor is registered in the catalog, that
// origin ids name a catalog task or a generated task of the workspace, and
// the depth limit (ErrTooDeep wrapped); verifies documents with
// Run.Verifier and stores the result in the document's verification field.
func Plan(run Run, out *contract.Output) (*Changes, error) {
	if out == nil {
		out = &contract.Output{}
	}
	if run.NewID == nil {
		run.NewID = uuid.NewString
	}
	if run.Verifier == nil {
		run.Verifier = attest.Unverified
	}
	if err := check(run, out); err != nil {
		return nil, err
	}
	if err := checkDepth(run, out); err != nil {
		return nil, err
	}
	p := &planner{run: run, produced: workspace.ProducedBy{
		Task:         run.Task,
		Processor:    run.Processor,
		Digest:       run.Digest,
		AnswerCommit: run.AnswerCommit,
	}}
	if err := p.tasks(out.Tasks); err != nil {
		return nil, err
	}
	slices.SortFunc(p.items, CompareItems)
	slices.SortStableFunc(p.files, func(a, b gitrepo.Change) int { return cmp.Compare(a.Path, b.Path) })
	return &Changes{Items: p.items, Files: p.files}, nil
}

// planner collects the items and files of one Plan call.
type planner struct {
	run      Run
	produced workspace.ProducedBy
	items    []Item
	files    []gitrepo.Change
}

func (p *planner) write(path string, data []byte) {
	p.files = append(p.files, gitrepo.Change{Path: path, Data: data})
}

func (p *planner) remove(path string) {
	p.files = append(p.files, gitrepo.Change{Path: path, Delete: true})
}

// check reports unknown processors and origins and repeated match keys, all
// in one error wrapping ErrInvalidOutput.
func check(run Run, out *contract.Output) error {
	var msgs []string
	seen := map[string]bool{}
	for _, t := range out.Tasks {
		if seen[t.MatchKey] {
			msgs = append(msgs, fmt.Sprintf("task %q: match_key appears more than once", t.MatchKey))
		}
		seen[t.MatchKey] = true
		if t.Processor != "" {
			if _, ok := run.Catalog.Registry.Processors[t.Processor]; !ok {
				msgs = append(msgs, fmt.Sprintf("task %q: processor %q is not registered in the catalog", t.MatchKey, t.Processor))
			}
		}
		if t.Origin != nil && !run.Catalog.Tasks.Has(t.Origin.ID) && !run.Workspace.Graph.Has(t.Origin.ID) {
			msgs = append(msgs, fmt.Sprintf("task %q: origin %s is neither a catalog task nor a generated task of the workspace", t.MatchKey, t.Origin.ID))
		}
	}
	seen = map[string]bool{}
	for _, d := range out.Documents {
		if seen[d.MatchKey] {
			msgs = append(msgs, fmt.Sprintf("document %q: match_key appears more than once", d.MatchKey))
		}
		seen[d.MatchKey] = true
	}
	if len(msgs) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidOutput, strings.Join(msgs, "; "))
	}
	return nil
}

// checkDepth fails output with tasks when the answered task already lies
// at the maximum depth, and a run on a task that is unknown.
func checkDepth(run Run, out *contract.Output) error {
	depth := 0
	if !run.Catalog.Tasks.Has(run.Task.ID) {
		if depth = Depth(run.Workspace, run.Task.ID); depth < 0 {
			return fmt.Errorf("task %s is neither a catalog task nor a generated task of the workspace", run.Task.ID)
		}
	}
	limit := run.MaxDepth
	if limit <= 0 {
		limit = DefaultMaxDepth
	}
	if len(out.Tasks) > 0 && depth >= limit {
		if depth == math.MaxInt {
			return fmt.Errorf("%w: task %s lies on a cycle of generated tasks", ErrTooDeep, run.Task.ID)
		}
		return fmt.Errorf("%w: task %s is at depth %d, its output tasks would be at depth %d, the maximum is %d",
			ErrTooDeep, run.Task.ID, depth, depth+1, limit)
	}
	return nil
}

// earlierTasks returns the current versions of the generated tasks the
// answered task produced earlier, by match key. When two share a key (only
// possible after hand edits), the one with the smallest id is matched and
// the others are returned in extra, to be removed.
func earlierTasks(w *workspace.Workspace, taskID string) (byKey map[string]*workspace.Generated, extra []*workspace.Generated) {
	byKey = map[string]*workspace.Generated{}
	for _, id := range w.Graph.Tasks() {
		g := Current(w, id)
		if g == nil || g.ProducedBy.Task.ID != taskID {
			continue
		}
		if _, dup := byKey[g.MatchKey]; dup {
			extra = append(extra, g)
			continue
		}
		byKey[g.MatchKey] = g
	}
	return byKey, extra
}
```

- [ ] **Step 4: Implement task matching** `internal/match/tasks.go`

```go
package match

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// firstVersion is the version of a new generated task.
const firstVersion = "1.0.0"

// generatedFile is the frontmatter of a generated task version as custos
// writes it. It lists the fields of workspace.GeneratedMeta flat, because
// frontmatter.Encode's quoting fallback cannot encode an inlined struct.
type generatedFile struct {
	ID         string               `yaml:"id"`
	Version    string               `yaml:"version"`
	Title      string               `yaml:"title"`
	AnswerType task.AnswerType      `yaml:"answer_type"`
	Choices    []string             `yaml:"choices,omitempty"`
	Previous   []task.Ref           `yaml:"previous,omitempty"`
	Origin     *workspace.Origin    `yaml:"origin,omitempty"`
	MatchKey   string               `yaml:"match_key"`
	Processor  string               `yaml:"processor,omitempty"`
	ProducedBy workspace.ProducedBy `yaml:"produced_by"`
}

// tasks matches the output tasks against the generated tasks the answered
// task produced earlier.
func (p *planner) tasks(out []contract.OutputTask) error {
	w := p.run.Workspace
	earlier, extra := earlierTasks(w, p.run.Task.ID)
	for _, t := range out {
		old, found := earlier[t.MatchKey]
		delete(earlier, t.MatchKey)
		body := normalizeBody(t.Body)
		switch {
		case !found:
			id := p.run.NewID()
			if err := p.writeTask(id, firstVersion, nil, t, body); err != nil {
				return err
			}
			p.items = append(p.items, Item{Kind: KindTask, MatchKey: t.MatchKey, Action: Added, ID: id, To: firstVersion, Title: t.Title})
		case sameTask(old, t, body):
			p.items = append(p.items, Item{Kind: KindTask, MatchKey: t.MatchKey, Action: Unchanged, ID: old.ID,
				From: old.Version, To: old.Version, Title: t.Title})
		default:
			step := t.Bump
			if step == "" {
				step = semver.Minor
			}
			next, err := semver.Bump(old.Version, step)
			if err != nil {
				return fmt.Errorf("task %q: %w", t.MatchKey, err)
			}
			if _, exists := w.Graph.Lookup(task.Ref{ID: old.ID, Version: next}); exists {
				return fmt.Errorf("task %q: version %s of generated task %s already exists; use a larger bump", t.MatchKey, next, old.ID)
			}
			if err := p.writeTask(old.ID, next, []task.Ref{old.Ref()}, t, body); err != nil {
				return err
			}
			p.items = append(p.items, Item{Kind: KindTask, MatchKey: t.MatchKey, Action: NewVersion, ID: old.ID,
				From: old.Version, To: next, Title: t.Title})
		}
	}
	gone := slices.Collect(maps.Values(earlier))
	for _, g := range append(gone, extra...) {
		p.removeTask(g)
	}
	return nil
}

// writeTask adds the file of version of generated task id.
func (p *planner) writeTask(id, version string, previous []task.Ref, t contract.OutputTask, body string) error {
	f := generatedFile{
		ID: id, Version: version, Title: t.Title, AnswerType: t.AnswerType, Choices: t.Choices, Previous: previous,
		Origin: origin(t.Origin), MatchKey: t.MatchKey, Processor: t.Processor, ProducedBy: p.produced,
	}
	data, err := frontmatter.Encode(f, body)
	if err != nil {
		return fmt.Errorf("task %q: %w", t.MatchKey, err)
	}
	p.write("generated/"+id+"/"+version+".md", data)
	return nil
}

// removeTask deletes every version file of generated task g and its answer
// (answers stay in Git history, §5.5).
func (p *planner) removeTask(g *workspace.Generated) {
	w := p.run.Workspace
	for _, v := range w.Graph.Versions(g.ID) {
		p.remove(v.Path)
	}
	if answer := "answers/" + g.ID + ".md"; w.Answers[answer] != nil {
		p.remove(answer)
	}
	p.items = append(p.items, Item{Kind: KindTask, MatchKey: g.MatchKey, Action: Removed, ID: g.ID, From: g.Version, Title: g.Title})
}

// sameTask reports whether output t has the content of generated task g:
// title, body, answer_type, choices, origin and processor (bump is not
// content). body is t.Body normalised.
func sameTask(g *workspace.Generated, t contract.OutputTask, body string) bool {
	return g.Title == t.Title && g.Body == body && g.AnswerType == t.AnswerType &&
		slices.Equal(g.Choices, t.Choices) && sameOrigin(g.Origin, origin(t.Origin)) && g.Processor == t.Processor
}

func origin(o *contract.Origin) *workspace.Origin {
	if o == nil {
		return nil
	}
	return &workspace.Origin{ID: o.ID, Version: o.Version}
}

func sameOrigin(a, b *workspace.Origin) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// normalizeBody returns body as a task file stores it: "\n" line endings and
// a final newline unless it is empty, which is what reading the file back
// yields (frontmatter.Split).
func normalizeBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return body
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/match/ && go vet ./internal/match/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/match
git commit -m "Match processor output tasks against the tasks the same answer produced earlier

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Match and verify output documents

**Files:**
- Create: `internal/match/documents.go`
- Modify: `internal/match/match.go` (`Plan`)
- Test: `internal/match/documents_test.go`, `internal/match/sign_test.go`

**Interfaces:**
- Consumes: Task 2 (`planner`, `Item`, `KindDocument`, test helpers `newRun`, `webTask`, `apply`, `paths`, `wantItems`); `attest.Result`, `attest.Canonical`, `attest.Unverified`, `attest.StatusVerified/StatusFailed/StatusUnsigned` (3b); `carabiner.New(keysDir string) (*carabiner.Verifier, error)` (3b); `proctest.Image`, `proctest.SigningKey`, `runner.New`, `runner.Config{SecretsDir}`, `runner.Job{Image, Secrets, Input}`, `(*runner.Runner).Resolve(ctx, image) (ref, digest string, err error)`, `(*runner.Runner).Run(ctx, job) (*runner.Result, error)`, `contract.NewInput(workspaceID string, v *task.Version, a *workspace.Answer) contract.Input`, `contract.ParseOutput([]byte) (*contract.Output, error)` (3a).
- Produces: `Plan` now also returns document items (with `Verification`) and document files; unexported `earlierDocuments`, `documentID`, `encodeDocument`.

- [ ] **Step 1: Write the failing tests** `internal/match/documents_test.go`

```go
package match

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
)

// envelopeVerifier treats {"payload": P, "sig": S} as a signed envelope
// with payload P, verified as signer "key-S"; anything else is unsigned
// with the canonical content as payload. Re-signing changes S, not P.
type envelopeVerifier struct{}

func (envelopeVerifier) Verify(content []byte) attest.Result {
	var env struct {
		Payload json.RawMessage `json:"payload"`
		Sig     string          `json:"sig"`
	}
	if err := json.Unmarshal(content, &env); err != nil || env.Sig == "" {
		return attest.Unverified.Verify(content)
	}
	p, _ := attest.Canonical(env.Payload)
	return attest.Result{Status: attest.StatusVerified, Signers: []string{"key-" + env.Sig}, Payload: p}
}

// docPath returns the path of document id.
func docPath(id string) string { return "documents/" + id + ".json" }

// slsaDoc is the output document that reproduces fixture.DocumentFile.
func slsaDoc() contract.OutputDocument {
	return contract.OutputDocument{MatchKey: "slsa:web-01", Name: "provenance", MediaType: "application/vnd.in-toto+json",
		Content: json.RawMessage(`{ "_type": "https://in-toto.io/Statement/v1" }`)}
}

// signedDocWorkspace is emptyWorkspace with a document of TaskB's answer
// whose content is an envelope signed with sig.
func signedDocWorkspace(sig string) map[string]string {
	return with(emptyWorkspace(), map[string]string{docPath(fixture.DocID): `{"match_key":"att","name":"att","media_type":"application/json",` +
		`"produced_by":{"task":{"id":"` + fixture.TaskB + `","version":"1.0.0"},"processor":"host-scanner","digest":"` + digest +
		`","answer_commit":"` + fixture.Commit + `"},"verification":{"status":"verified","signers":["key-` + sig + `"]},` +
		`"content":{"payload":{"subject":"web-01"},"sig":"` + sig + `"}}`})
}

func TestPlanAddsDocument(t *testing.T) {
	files := emptyWorkspace()
	doc := contract.OutputDocument{MatchKey: "sbom", Name: "SBOM <web-01>", MediaType: "application/json",
		Content: json.RawMessage(`{"components":[1,2]}`)}
	ch, err := Plan(newRun(t, files), &contract.Output{Documents: []contract.OutputDocument{doc}})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items, Item{Kind: KindDocument, MatchKey: "sbom", Action: Added, ID: gid(100), Title: "SBOM <web-01>"})
	if v := ch.Items[0].Verification; v == nil || v.Status != attest.StatusUnsigned {
		t.Errorf("verification %+v, want unsigned", v)
	}
	if got := paths(ch.Files); !slices.Equal(got, []string{"+" + docPath(gid(100))}) {
		t.Fatalf("files %v", got)
	}
	want := `{
  "match_key": "sbom",
  "name": "SBOM <web-01>",
  "media_type": "application/json",
  "produced_by": {
    "task": {
      "id": "` + fixture.TaskB + `",
      "version": "1.0.0"
    },
    "processor": "host-scanner",
    "digest": "` + digest + `",
    "answer_commit": "` + fixture.Commit + `"
  },
  "verification": {
    "status": "unsigned"
  },
  "content": {
    "components": [
      1,
      2
    ]
  }
}
`
	if got := string(ch.Files[0].Data); got != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
	valid(t, apply(files, ch.Files))
}

func TestPlanUnchangedDocument(t *testing.T) {
	// fixture.DocumentFile has the same statement without the spaces.
	ch, err := Plan(newRun(t, fixture.Workspace()), &contract.Output{
		Tasks:     []contract.OutputTask{webTask()},
		Documents: []contract.OutputDocument{slsaDoc()},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items,
		Item{Kind: KindDocument, MatchKey: "slsa:web-01", Action: Unchanged, ID: fixture.DocID, Title: "provenance"},
		Item{Kind: KindTask, MatchKey: "host:web-01", Action: Unchanged, ID: fixture.TaskC, From: "1.0.0", To: "1.0.0", Title: "Host web-01"},
	)
	if len(ch.Files) != 0 {
		t.Errorf("files %v, want none", paths(ch.Files))
	}
}

func TestPlanResignedDocumentIsUnchanged(t *testing.T) {
	run := newRun(t, signedDocWorkspace("1"))
	run.Verifier = envelopeVerifier{}
	doc := contract.OutputDocument{MatchKey: "att", Name: "att", MediaType: "application/json",
		Content: json.RawMessage(`{"sig":"2","payload":{"subject":"web-01"}}`)}
	ch, err := Plan(run, &contract.Output{Documents: []contract.OutputDocument{doc}})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items, Item{Kind: KindDocument, MatchKey: "att", Action: Unchanged, ID: fixture.DocID, Title: "att"})
	if len(ch.Files) != 0 {
		t.Errorf("files %v, want none", paths(ch.Files))
	}
}

func TestPlanChangedDocumentKeepsItsFile(t *testing.T) {
	for name, c := range map[string]struct {
		doc    contract.OutputDocument
		signer string
	}{
		"payload":    {contract.OutputDocument{MatchKey: "att", Name: "att", MediaType: "application/json", Content: json.RawMessage(`{"payload":{"subject":"db-01"},"sig":"2"}`)}, "key-2"},
		"name":       {contract.OutputDocument{MatchKey: "att", Name: "renamed", MediaType: "application/json", Content: json.RawMessage(`{"payload":{"subject":"web-01"},"sig":"1"}`)}, "key-1"},
		"media type": {contract.OutputDocument{MatchKey: "att", Name: "att", MediaType: "application/vnd.in-toto+json", Content: json.RawMessage(`{"payload":{"subject":"web-01"},"sig":"1"}`)}, "key-1"},
	} {
		t.Run(name, func(t *testing.T) {
			files := signedDocWorkspace("1")
			run := newRun(t, files)
			run.Verifier = envelopeVerifier{}
			ch, err := Plan(run, &contract.Output{Documents: []contract.OutputDocument{c.doc}})
			if err != nil {
				t.Fatal(err)
			}
			wantItems(t, ch.Items, Item{Kind: KindDocument, MatchKey: "att", Action: NewVersion, ID: fixture.DocID, Title: c.doc.Name})
			if got := paths(ch.Files); !slices.Equal(got, []string{"+" + docPath(fixture.DocID)}) {
				t.Fatalf("files %v", got)
			}
			after := apply(files, ch.Files)
			valid(t, after)
			d := load(t, after).Documents[docPath(fixture.DocID)]
			if d.Verification == nil || d.Verification.Status != attest.StatusVerified || !slices.Equal(d.Verification.Signers, []string{c.signer}) {
				t.Errorf("verification %+v, want verified by %s", d.Verification, c.signer)
			}
			if d.Name != c.doc.Name || d.MediaType != c.doc.MediaType {
				t.Errorf("name %q, media type %q", d.Name, d.MediaType)
			}
		})
	}
}

func TestPlanRemovesDocument(t *testing.T) {
	files := fixture.Workspace()
	ch, err := Plan(newRun(t, files), &contract.Output{Tasks: []contract.OutputTask{webTask()}})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items,
		Item{Kind: KindDocument, MatchKey: "slsa:web-01", Action: Removed, ID: fixture.DocID, Title: "provenance"},
		Item{Kind: KindTask, MatchKey: "host:web-01", Action: Unchanged, ID: fixture.TaskC, From: "1.0.0", To: "1.0.0", Title: "Host web-01"},
	)
	if ch.Items[0].Verification != nil {
		t.Errorf("removed item has verification %+v", ch.Items[0].Verification)
	}
	if got := paths(ch.Files); !slices.Equal(got, []string{"-" + docPath(fixture.DocID)}) {
		t.Errorf("files %v", got)
	}
}

func TestPlanDocumentsOfOtherAnswersStay(t *testing.T) {
	files := fixture.Workspace() // its document was made from TaskB's answer
	run := newRun(t, files)
	run.Task.ID = fixture.TaskA
	ch, err := Plan(run, &contract.Output{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Items) != 0 || len(ch.Files) != 0 {
		t.Errorf("items:\n%sfiles %v", fmtItems(ch.Items), paths(ch.Files))
	}
}
```

And `internal/match/sign_test.go` — the end-to-end check that a document signed by plan 3a's real `sign` image is stored as `verified` with the matching key and `failed` with another key (it needs Docker):

```go
package match_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/attest/carabiner"
	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/runner"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// TestPlanVerifiesSignedDocumentFromRealProcessor runs the sign test image
// (plan 3a) in Docker, verifies its DSSE envelope with the matching public
// key through the carabiner verifier (plan 3b), and expects the document
// to be stored as verified. With another key it must be failed.
func TestPlanVerifiesSignedDocumentFromRealProcessor(t *testing.T) {
	secrets, pub := proctest.SigningKey(t)
	_, otherPub := proctest.SigningKey(t)
	image := proctest.Image(t, "sign")

	w, _ := workspace.Load(fixture.MapFS(map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, fixture.Commit)}))
	c, ps := catalog.Load(fixture.MapFS(fixture.Catalog()))
	fixture.WantNone(t, ps)
	v, ok := c.Tasks.Lookup(task.Ref{ID: fixture.TaskB, Version: "1.0.0"})
	if !ok {
		t.Fatal("fixture catalog lacks TaskB 1.0.0")
	}
	answer := &workspace.Answer{Task: fixture.TaskB, TaskVersion: "1.0.0", Type: task.AnswerText,
		Value: "doc provenance web-01", Path: "answers/" + fixture.TaskB + ".md"}
	input, err := json.Marshal(contract.NewInput(fixture.WorkspaceID, v, answer))
	if err != nil {
		t.Fatal(err)
	}

	rn := runner.New(runner.Config{SecretsDir: secrets})
	ctx := context.Background()
	ref, digest, err := rn.Resolve(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	res, err := rn.Run(ctx, runner.Job{Image: ref, Secrets: []string{"test-signing-key"}, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.TimedOut {
		t.Fatalf("sign exited %d (timed out %v):\n%s", res.ExitCode, res.TimedOut, res.Log)
	}
	out, err := contract.ParseOutput(res.Stdout)
	if err != nil {
		t.Fatal(err)
	}

	for _, c2 := range []struct {
		name string
		key  []byte
		want string
	}{
		{"trusted key", pub, attest.StatusVerified},
		{"other key", otherPub, attest.StatusFailed},
	} {
		t.Run(c2.name, func(t *testing.T) {
			keys := t.TempDir()
			if err := os.WriteFile(filepath.Join(keys, "test.pub"), c2.key, 0o600); err != nil {
				t.Fatal(err)
			}
			verifier, err := carabiner.New(keys)
			if err != nil {
				t.Fatal(err)
			}
			ch, err := match.Plan(match.Run{
				Workspace: w, Catalog: c, Task: task.Ref{ID: fixture.TaskB, Version: "1.0.0"},
				Processor: "host-scanner", Digest: digest, AnswerCommit: fixture.Commit, Verifier: verifier,
			}, out)
			if err != nil {
				t.Fatal(err)
			}
			if len(ch.Items) != 1 || ch.Items[0].Kind != match.KindDocument || ch.Items[0].Verification == nil {
				t.Fatalf("items %+v", ch.Items)
			}
			if got := ch.Items[0].Verification.Status; got != c2.want {
				t.Errorf("status %q, want %q", got, c2.want)
			}
			after, _ := workspace.Load(fixture.MapFS(map[string]string{
				"custos.yaml":    fixture.Config(fixture.WorkspaceID, fixture.Commit),
				ch.Files[0].Path: string(ch.Files[0].Data),
			}))
			for _, d := range after.Documents {
				if d.Verification == nil || d.Verification.Status != c2.want {
					t.Errorf("stored verification %+v, want %s", d.Verification, c2.want)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/match/`
Expected: FAIL — `TestPlanAddsDocument` gets no items, `TestPlanRemovesDocument` no removal, `TestPlanVerifiesSignedDocumentFromRealProcessor` `items []` (documents are not matched yet).

- [ ] **Step 3: Implement** `internal/match/documents.go`

```go
package match

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/workspace"
)

// documents matches the output documents against the documents the answered
// task produced earlier. A document is unchanged when its payload (as the
// verifier reports it), name and media type are unchanged; re-signing alone
// is not a change (§5.4).
func (p *planner) documents(out []contract.OutputDocument) error {
	earlier, extra := earlierDocuments(p.run.Workspace, p.run.Task.ID)
	for _, d := range out {
		res := p.run.Verifier.Verify(d.Content)
		v := &workspace.Verification{Status: res.Status, Signers: res.Signers}
		old, found := earlier[d.MatchKey]
		delete(earlier, d.MatchKey)
		item := Item{Kind: KindDocument, MatchKey: d.MatchKey, Title: d.Name, Verification: v}
		switch {
		case !found:
			item.Action, item.ID = Added, p.run.NewID()
		case old.Name == d.Name && old.MediaType == d.MediaType &&
			bytes.Equal(p.run.Verifier.Verify(old.Content).Payload, res.Payload):
			item.Action, item.ID = Unchanged, documentID(old.Path)
			p.items = append(p.items, item)
			continue
		default:
			item.Action, item.ID = NewVersion, documentID(old.Path)
		}
		data, err := encodeDocument(workspace.Document{
			MatchKey: d.MatchKey, Name: d.Name, MediaType: d.MediaType,
			ProducedBy: p.produced, Verification: v, Content: d.Content,
		})
		if err != nil {
			return fmt.Errorf("document %q: %w", d.MatchKey, err)
		}
		p.write("documents/"+item.ID+".json", data)
		p.items = append(p.items, item)
	}
	gone := slices.Collect(maps.Values(earlier))
	for _, d := range append(gone, extra...) {
		p.remove(d.Path)
		p.items = append(p.items, Item{Kind: KindDocument, MatchKey: d.MatchKey, Action: Removed, ID: documentID(d.Path), Title: d.Name})
	}
	return nil
}

// earlierDocuments returns the documents the answered task produced
// earlier, by match key. When two share a key (only possible after hand
// edits), the one with the smallest path is matched and the others are
// returned in extra, to be removed.
func earlierDocuments(w *workspace.Workspace, taskID string) (byKey map[string]*workspace.Document, extra []*workspace.Document) {
	byKey = map[string]*workspace.Document{}
	for _, path := range slices.Sorted(maps.Keys(w.Documents)) {
		d := w.Documents[path]
		if d.ProducedBy.Task.ID != taskID {
			continue
		}
		if _, dup := byKey[d.MatchKey]; dup {
			extra = append(extra, d)
			continue
		}
		byKey[d.MatchKey] = d
	}
	return byKey, extra
}

// documentID returns the uuid in documents/<uuid>.json.
func documentID(path string) string {
	return strings.TrimSuffix(strings.TrimPrefix(path, "documents/"), ".json")
}

// encodeDocument writes a document file: indented JSON without HTML
// escaping, ending in a newline.
func encodeDocument(d workspace.Document) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
```

- [ ] **Step 4: Call it from `Plan`** — in `internal/match/match.go`, replace:

```go
	if err := p.tasks(out.Tasks); err != nil {
		return nil, err
	}
	slices.SortFunc(p.items, CompareItems)
```

with:

```go
	if err := p.tasks(out.Tasks); err != nil {
		return nil, err
	}
	if err := p.documents(out.Documents); err != nil {
		return nil, err
	}
	slices.SortFunc(p.items, CompareItems)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/match/ && go vet ./internal/match/`
Expected: PASS (Docker must be running for `TestPlanVerifiesSignedDocumentFromRealProcessor`).

- [ ] **Step 6: Commit**

```bash
git add internal/match
git commit -m "Match processor output documents on their verified payload and store the verification

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Write, list, get and reject output proposals

**Files:**
- Create: `internal/proposal/proposal.go`, `internal/proposal/diff.go`
- Test: `internal/proposal/helpers_test.go`, `internal/proposal/proposal_test.go`

**Interfaces:**
- Consumes: `match.{Plan, Run, Changes, Item, CompareItems, Current, Below, KindTask, KindDocument, Added, NewVersion, Unchanged, Removed}` (Tasks 1–3); `store.Store.{WorkspaceRepo, UpdateWorkspace(id, ref string, author gitrepo.Signature, message string, edit func(fs.FS) ([]gitrepo.Change, error)) (string, error), Lock(repo string) func(), Load(id, rev string)}`; `gitrepo.Repo.{ResolveRef, TreeFS, Refs, DeleteRef, UpdateRef}`; `workspace.Load`.
- Produces (used by Task 5 and plan 3d):
  - `const BranchPrefix = "custos/proposal/"`, `func Branch(taskID, digest string) string`, `var ErrInvalid`
  - `type Proposal struct{Branch, Commit, Task, Digest string; Cascade bool; Items []match.Item}`
  - `func Write(st *store.Store, wsID, taskID, digest string, ch *match.Changes, message string) (string, error)`
  - `func List(st *store.Store, wsID string) ([]Proposal, error)` — never nil
  - `func Get(st *store.Store, wsID, taskID string) (*Proposal, error)`
  - `func Reject(st *store.Store, wsID, taskID string) error`
  - unexported for Task 5: constants `mainRef`, `headsPrefix`, `refPrefix`, `maxConflictRetries`; `cascadeBranch(taskID string) string`; `first(repo, taskID) (*Proposal, error)`; `writeBranch(st, wsID, taskID, branch string, files []gitrepo.Change, message string) (string, error)`; `conflict(error) error`; `type side`, `loadSide(fs.FS) side`, `type entry{item match.Item; changes []gitrepo.Change}`, `selector(match.Item) string`, `diff(main, tip side, taskID string, cascade bool) ([]entry, error)`, `scope(w, taskID, cascade) (tasks, docs []string)`
  - test helpers for Task 5: `ws`, `digest1`, `digest2`, `person`, `newStore`, `answer`, `commitMain`, `textTask`, `doc`, `plan`, `propose`, `repoOf`, `mainOID`, `resolve`, `branches`, `mainFiles`, `logOf`, `summary`, `wantSummary`, `idOf`.

- [ ] **Step 1: Write the test helpers** `internal/proposal/helpers_test.go`

```go
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
```

- [ ] **Step 2: Write the failing test** `internal/proposal/proposal_test.go`

```go
package proposal

import (
	"errors"
	"slices"
	"testing"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/store"
)

func TestBranch(t *testing.T) {
	want := "custos/proposal/" + fixture.TaskB + "/" + fixture.SHA256
	if got := Branch(fixture.TaskB, digest1); got != want {
		t.Errorf("Branch = %s, want %s", got, want)
	}
}

func TestWriteOpensProposal(t *testing.T) {
	st := newStore(t)
	before := mainOID(t, st)
	branch := propose(t, st, fixture.TaskB, digest1, contract.Output{
		Tasks:     []contract.OutputTask{textTask("host:web-01", "Host web-01")},
		Documents: []contract.OutputDocument{doc("sbom", "web-01")},
	})
	if branch != Branch(fixture.TaskB, digest1) {
		t.Fatalf("branch %q", branch)
	}
	tip := resolve(t, st, headsPrefix+branch)
	if got := logOf(t, st, "%P", tip); got != before {
		t.Errorf("parent %s, want main %s", got, before)
	}
	if got := logOf(t, st, "%an <%ae>|%cn <%ce>|%s", tip); got != gitrepo.Bot.String()+"|"+gitrepo.Bot.String()+"|Propose output of host-scanner for task "+fixture.TaskB {
		t.Errorf("author|committer|subject = %s", got)
	}
	if mainOID(t, st) != before {
		t.Error("Write moved main")
	}
	p, err := Get(st, ws, fixture.TaskB)
	if err != nil {
		t.Fatal(err)
	}
	if p.Branch != branch || p.Commit != tip || p.Task != fixture.TaskB || p.Digest != digest1 || p.Cascade {
		t.Errorf("proposal %+v", p)
	}
	wantSummary(t, p.Items, "document sbom added →", "task host:web-01 added →1.0.0")
	if v := p.Items[0].Verification; v == nil || v.Status != "unsigned" {
		t.Errorf("document verification %+v", v)
	}
}

func TestWriteReplacesProposalsOfTheTask(t *testing.T) {
	st := newStore(t)
	answer(t, st, fixture.TaskA, "done")
	propose(t, st, fixture.TaskA, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("a", "A")}})
	propose(t, st, fixture.TaskB, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("one", "One")}})
	first := resolve(t, st, headsPrefix+Branch(fixture.TaskB, digest1))
	// Same digest again: the branch is replaced, not stacked on.
	propose(t, st, fixture.TaskB, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("two", "Two")}})
	second := resolve(t, st, headsPrefix+Branch(fixture.TaskB, digest1))
	if second == first || logOf(t, st, "%P", second) != mainOID(t, st) {
		t.Errorf("replaced branch %s (was %s) must be one commit on main", second, first)
	}
	// Another digest: the older proposal of the task is deleted.
	propose(t, st, fixture.TaskB, digest2, contract.Output{Tasks: []contract.OutputTask{textTask("three", "Three")}})
	want := []string{Branch(fixture.TaskA, digest1), Branch(fixture.TaskB, digest2)}
	slices.Sort(want)
	if got := branches(t, st); !slices.Equal(got, want) {
		t.Errorf("branches %v, want %v", got, want)
	}
	p, err := Get(st, ws, fixture.TaskB)
	if err != nil {
		t.Fatal(err)
	}
	wantSummary(t, p.Items, "task three added →1.0.0")
}

func TestWriteWithoutChangesClosesProposals(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("one", "One")}})
	branch, err := Write(st, ws, fixture.TaskB, digest2, &match.Changes{}, "nothing")
	if err != nil || branch != "" {
		t.Fatalf("branch %q, err %v", branch, err)
	}
	if got := branches(t, st); len(got) != 0 {
		t.Errorf("branches %v, want none", got)
	}
	if _, err := Get(st, ws, fixture.TaskB); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get: %v, want ErrNotFound", err)
	}
}

func TestWriteChangesMainAlreadyHasOpenNothing(t *testing.T) {
	st := newStore(t)
	out := contract.Output{Tasks: []contract.OutputTask{textTask("one", "One")}}
	ch := plan(t, st, fixture.TaskB, out)
	// main gets the same files meanwhile (an accepted proposal of an
	// identical run).
	files := map[string]string{}
	for _, c := range ch.Files {
		files[c.Path] = string(c.Data)
	}
	commitMain(t, st, files)
	branch, err := Write(st, ws, fixture.TaskB, digest1, ch, "late")
	if err != nil || branch != "" || len(branches(t, st)) != 0 {
		t.Errorf("branch %q, err %v, branches %v", branch, err, branches(t, st))
	}
}

func TestListIgnoresForeignBranches(t *testing.T) {
	st := newStore(t)
	repo := repoOf(t, st)
	main := mainOID(t, st)
	for _, name := range []string{"custos/proposal/not-a-uuid/" + fixture.SHA256, "custos/proposal/" + fixture.TaskB + "/latest"} {
		if err := repo.UpdateRef(headsPrefix+name, main, ""); err != nil {
			t.Fatal(err)
		}
	}
	ps, err := List(st, ws)
	if err != nil || len(ps) != 0 {
		t.Errorf("proposals %+v, err %v", ps, err)
	}
	if _, err := Get(st, ws, fixture.TaskB); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get: %v, want ErrNotFound", err)
	}
}

func TestListEmptyAndUnknownWorkspace(t *testing.T) {
	st := newStore(t)
	ps, err := List(st, ws)
	if err != nil || ps == nil || len(ps) != 0 {
		t.Errorf("proposals %#v, err %v; want an empty, non-nil list", ps, err)
	}
	if _, err := List(st, "6c7d8e9f-0a1b-4c2d-b3e4-f5a6b7c8d9e0"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown workspace: %v", err)
	}
	if _, err := Get(st, ws, "not-a-task"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("malformed task id: %v", err)
	}
}

func TestReject(t *testing.T) {
	st := newStore(t)
	before := mainOID(t, st)
	propose(t, st, fixture.TaskB, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("one", "One")}})
	if err := Reject(st, ws, fixture.TaskB); err != nil {
		t.Fatal(err)
	}
	if len(branches(t, st)) != 0 || mainOID(t, st) != before {
		t.Errorf("branches %v, main moved %v", branches(t, st), mainOID(t, st) != before)
	}
	if err := Reject(st, ws, fixture.TaskB); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second reject: %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/proposal/`
Expected: FAIL — `undefined: Write`, `undefined: Branch`, `undefined: mainRef` …

- [ ] **Step 4: Implement recomputing items** `internal/proposal/diff.go`

```go
package proposal

import (
	"bytes"
	"errors"
	"io/fs"
	"maps"
	"slices"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/workspace"
)

// side is one tree: the workspace's main or a proposal branch's tip.
type side struct {
	ws   *workspace.Workspace
	fsys fs.FS
}

func loadSide(fsys fs.FS) side {
	w, _ := workspace.Load(fsys)
	return side{ws: w, fsys: fsys}
}

// entry is one recomputed item and the changes that apply it to main.
type entry struct {
	item    match.Item
	changes []gitrepo.Change
}

// selector returns the name Accept selects an item by.
func selector(it match.Item) string { return it.Kind + ":" + it.MatchKey }

// diff recomputes the items of a proposal from its tip against main.
//
// The scope of an ordinary proposal of task taskID is every generated task
// whose current version names taskID in produced_by.task.id, and every
// document that does, on either side. The scope of a cascade of removed
// task taskID is match.Below(taskID) on either side, and the documents
// produced by taskID or by one of those tasks, on either side.
//
// Within the scope, generated tasks are compared by id and documents by
// file path:
//   - only on the tip: added (task: all its version files are written;
//     document: its file is written);
//   - on both: unchanged when main has the tip's current version (task) or
//     the same bytes (document), else new-version (task: the tip's version
//     files main lacks are written; document: the tip's file is written);
//   - only on main: removed (task: all its version files and its answer are
//     deleted; document: its file is deleted).
func diff(main, tip side, taskID string, cascade bool) ([]entry, error) {
	mt, md := scope(main.ws, taskID, cascade)
	tt, td := scope(tip.ws, taskID, cascade)
	var out []entry
	for _, id := range union(mt, tt) {
		e, err := diffTask(main, tip, id)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	for _, path := range union(md, td) {
		e, err := diffDocument(main, tip, path)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	slices.SortStableFunc(out, func(a, b entry) int { return match.CompareItems(a.item, b.item) })
	return out, nil
}

// scope returns the generated task ids and document paths of w that belong
// to the proposal of taskID (see diff), sorted.
func scope(w *workspace.Workspace, taskID string, cascade bool) (tasks, docs []string) {
	producers := map[string]bool{taskID: true}
	if cascade {
		tasks = match.Below(w, taskID)
		for _, id := range tasks {
			producers[id] = true
		}
	} else {
		for _, id := range w.Graph.Tasks() {
			if g := match.Current(w, id); g != nil && g.ProducedBy.Task.ID == taskID {
				tasks = append(tasks, id)
			}
		}
	}
	for _, path := range slices.Sorted(maps.Keys(w.Documents)) {
		if producers[w.Documents[path].ProducedBy.Task.ID] {
			docs = append(docs, path)
		}
	}
	return tasks, docs
}

func union(a, b []string) []string {
	return slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(a), b...))))
}

func diffTask(main, tip side, id string) (entry, error) {
	m, t := match.Current(main.ws, id), match.Current(tip.ws, id)
	switch {
	case t == nil: // only on main
		e := entry{item: match.Item{Kind: match.KindTask, MatchKey: m.MatchKey, Action: match.Removed, ID: id, From: m.Version, Title: m.Title}}
		for _, v := range main.ws.Graph.Versions(id) {
			e.changes = append(e.changes, gitrepo.Change{Path: v.Path, Delete: true})
		}
		if answer := "answers/" + id + ".md"; main.ws.Answers[answer] != nil {
			e.changes = append(e.changes, gitrepo.Change{Path: answer, Delete: true})
		}
		return e, nil
	case m != nil:
		if _, ok := main.ws.Graph.Lookup(t.Ref()); ok {
			return entry{item: match.Item{Kind: match.KindTask, MatchKey: t.MatchKey, Action: match.Unchanged, ID: id,
				From: t.Version, To: t.Version, Title: t.Title}}, nil
		}
	}
	e := entry{item: match.Item{Kind: match.KindTask, MatchKey: t.MatchKey, Action: match.Added, ID: id, To: t.Version, Title: t.Title}}
	if m != nil {
		e.item.Action, e.item.From = match.NewVersion, m.Version
	}
	for _, v := range tip.ws.Graph.Versions(id) {
		if _, ok := main.ws.Graph.Lookup(v.Ref()); ok {
			continue
		}
		data, err := fs.ReadFile(tip.fsys, v.Path)
		if err != nil {
			return entry{}, err
		}
		e.changes = append(e.changes, gitrepo.Change{Path: v.Path, Data: data})
	}
	return e, nil
}

func diffDocument(main, tip side, path string) (entry, error) {
	m, t := main.ws.Documents[path], tip.ws.Documents[path]
	id := documentID(path)
	if t == nil {
		return entry{
			item:    match.Item{Kind: match.KindDocument, MatchKey: m.MatchKey, Action: match.Removed, ID: id, Title: m.Name},
			changes: []gitrepo.Change{{Path: path, Delete: true}},
		}, nil
	}
	data, err := fs.ReadFile(tip.fsys, path)
	if err != nil {
		return entry{}, err
	}
	e := entry{item: match.Item{Kind: match.KindDocument, MatchKey: t.MatchKey, Action: match.Added, ID: id,
		Title: t.Name, Verification: t.Verification}}
	if m != nil {
		cur, err := fs.ReadFile(main.fsys, path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return entry{}, err
		}
		if bytes.Equal(cur, data) {
			e.item.Action = match.Unchanged
			return e, nil
		}
		e.item.Action = match.NewVersion
	}
	e.changes = []gitrepo.Change{{Path: path, Data: data}}
	return e, nil
}

// documentID returns the uuid in documents/<uuid>.json.
func documentID(path string) string {
	return path[len("documents/") : len(path)-len(".json")]
}
```

- [ ] **Step 5: Implement branches, `Write`, `List`, `Get`, `Reject`** `internal/proposal/proposal.go`

```go
// Package proposal keeps the output proposals of processor runs as
// branches of a workspace repository: custos/proposal/<task>/<digest-hex>
// for a run's output and custos/proposal/<task>/cascade for the removal of
// the tasks generated below a removed task. It writes, lists, accepts and
// rejects them through the store (ruling 2.3).
package proposal

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

// BranchPrefix starts the short name of every output proposal branch.
const BranchPrefix = "custos/proposal/"

const (
	mainRef     = "refs/heads/main"
	headsPrefix = "refs/heads/"
	refPrefix   = headsPrefix + BranchPrefix
	cascadeName = "cascade"
)

// ErrInvalid reports a request that cannot be carried out as given, such as
// an unknown selector (HTTP 400).
var ErrInvalid = errors.New("invalid request")

var hexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// maxConflictRetries bounds the attempts of a write that loses its
// compare-and-swap to a push.
const maxConflictRetries = 3

// Branch returns the proposal branch of a run of the image with digest
// "sha256:<hex>" on the answer to task taskID.
func Branch(taskID, digest string) string {
	return BranchPrefix + taskID + "/" + strings.TrimPrefix(digest, "sha256:")
}

func cascadeBranch(taskID string) string { return BranchPrefix + taskID + "/" + cascadeName }

// Proposal is one open output proposal.
type Proposal struct {
	Branch  string // short name, e.g. custos/proposal/<task>/<hex>
	Commit  string
	Task    string // answered task id (or removed task id for a cascade)
	Digest  string // "sha256:<hex>", "" for a cascade
	Cascade bool
	Items   []match.Item // recomputed from the branch against current main; Unchanged included
}

// parseBranch splits a short branch name below BranchPrefix. ok is false
// for names custos does not write (pushed by hand), which are ignored.
func parseBranch(branch string) (p Proposal, ok bool) {
	rest, ok := strings.CutPrefix(branch, BranchPrefix)
	if !ok {
		return Proposal{}, false
	}
	id, last, ok := strings.Cut(rest, "/")
	if !ok || !task.ValidID(id) {
		return Proposal{}, false
	}
	switch {
	case last == cascadeName:
		return Proposal{Branch: branch, Task: id, Cascade: true}, true
	case hexRE.MatchString(last):
		return Proposal{Branch: branch, Task: id, Digest: "sha256:" + last}, true
	}
	return Proposal{}, false
}

// open returns the open proposals of the workspace (of one task when taskID
// is not ""), sorted by branch, with Commit set and Items empty.
func open(repo *gitrepo.Repo, taskID string) ([]Proposal, error) {
	prefix := refPrefix
	if taskID != "" {
		prefix += taskID + "/"
	}
	refs, err := repo.Refs(prefix)
	if err != nil {
		return nil, err
	}
	var out []Proposal
	for _, ref := range slices.Sorted(maps.Keys(refs)) {
		p, ok := parseBranch(strings.TrimPrefix(ref, headsPrefix))
		if !ok || (taskID != "" && p.Task != taskID) {
			continue
		}
		p.Commit = refs[ref]
		out = append(out, p)
	}
	return out, nil
}

// List returns the open output proposals of workspace wsID, sorted by
// branch, with their items recomputed against the current main.
func List(st *store.Store, wsID string) ([]Proposal, error) {
	repo, err := st.WorkspaceRepo(wsID)
	if err != nil {
		return nil, err
	}
	ps, err := open(repo, "")
	if err != nil {
		return nil, err
	}
	main, err := mainSide(repo)
	if err != nil {
		return nil, err
	}
	out := []Proposal{}
	for _, p := range ps {
		if err := fill(repo, main, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", p.Branch, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// Get returns the open proposal of taskID (store.ErrNotFound when none). If
// a hand push left several, the first by branch name is used.
func Get(st *store.Store, wsID, taskID string) (*Proposal, error) {
	repo, err := st.WorkspaceRepo(wsID)
	if err != nil {
		return nil, err
	}
	p, err := first(repo, taskID)
	if err != nil {
		return nil, err
	}
	main, err := mainSide(repo)
	if err != nil {
		return nil, err
	}
	if err := fill(repo, main, p); err != nil {
		return nil, err
	}
	return p, nil
}

func first(repo *gitrepo.Repo, taskID string) (*Proposal, error) {
	if !task.ValidID(taskID) {
		return nil, fmt.Errorf("%w: no open output proposal for task %q", store.ErrNotFound, taskID)
	}
	ps, err := open(repo, taskID)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, fmt.Errorf("%w: no open output proposal for task %s", store.ErrNotFound, taskID)
	}
	return &ps[0], nil
}

// mainSide loads the workspace's main.
func mainSide(repo *gitrepo.Repo) (side, error) {
	main, ok, err := repo.ResolveRef(mainRef)
	if err != nil {
		return side{}, err
	}
	if !ok {
		return side{}, fmt.Errorf("%w: the workspace has no main branch yet", store.ErrNotFound)
	}
	fsys, err := repo.TreeFS(main)
	if err != nil {
		return side{}, err
	}
	return loadSide(fsys), nil
}

// fill sets p.Items from the branch tip against main.
func fill(repo *gitrepo.Repo, main side, p *Proposal) error {
	fsys, err := repo.TreeFS(p.Commit)
	if err != nil {
		return err
	}
	es, err := diff(main, loadSide(fsys), p.Task, p.Cascade)
	if err != nil {
		return err
	}
	p.Items = []match.Item{}
	for _, e := range es {
		p.Items = append(p.Items, e.item)
	}
	return nil
}

// Write puts ch.Files on branch Branch(taskID, digest) as one commit by Bot
// based on the current main, replacing the branch if it exists, and deletes
// other open proposals of taskID. No files → deletes open proposals of
// taskID and returns "". Files that leave main as it is (because main moved
// on since the run) do not count, so a run whose output main already holds
// opens no proposal either.
func Write(st *store.Store, wsID, taskID, digest string, ch *match.Changes, message string) (branch string, err error) {
	var files []gitrepo.Change
	if ch != nil {
		files = ch.Files
	}
	return writeBranch(st, wsID, taskID, Branch(taskID, digest), files, message)
}

// writeBranch deletes every open proposal of taskID and then writes files
// as a new branch from main, retrying when a push wins a compare-and-swap.
func writeBranch(st *store.Store, wsID, taskID, branch string, files []gitrepo.Change, message string) (string, error) {
	if !task.ValidID(taskID) {
		return "", fmt.Errorf("%w: task id %q is not a lowercase UUID v4", ErrInvalid, taskID)
	}
	repo, err := st.WorkspaceRepo(wsID)
	if err != nil {
		return "", err
	}
	var commit string
	for range maxConflictRetries {
		if err = deleteAll(st, wsID, repo, taskID); err != nil {
			break
		}
		if len(files) == 0 {
			return "", nil
		}
		commit, err = st.UpdateWorkspace(wsID, headsPrefix+branch, gitrepo.Bot, message, func(fs.FS) ([]gitrepo.Change, error) {
			return files, nil
		})
		if !errors.Is(err, store.ErrConflict) {
			break
		}
	}
	if err != nil || commit == "" {
		return "", err
	}
	return branch, nil
}

// deleteAll deletes the open proposal branches of taskID under the
// workspace's lock.
func deleteAll(st *store.Store, wsID string, repo *gitrepo.Repo, taskID string) error {
	unlock := st.Lock(wsID)
	defer unlock()
	refs, err := repo.Refs(refPrefix + taskID + "/")
	if err != nil {
		return err
	}
	for _, ref := range slices.Sorted(maps.Keys(refs)) {
		if err := repo.DeleteRef(ref, refs[ref]); err != nil {
			return conflict(err)
		}
	}
	return nil
}

// Reject deletes the open proposal of taskID; store.ErrNotFound when none.
func Reject(st *store.Store, wsID, taskID string) error {
	repo, err := st.WorkspaceRepo(wsID)
	if err != nil {
		return err
	}
	if _, err := first(repo, taskID); err != nil {
		return err
	}
	return deleteAll(st, wsID, repo, taskID)
}

// conflict reports a ref that moved under us as store.ErrConflict.
func conflict(err error) error {
	if errors.Is(err, gitrepo.ErrRefMoved) {
		return fmt.Errorf("%w: %w", store.ErrConflict, err)
	}
	return err
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/proposal/ && go vet ./internal/proposal/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/proposal
git commit -m "Keep processor output proposals as branches and list them against the current main

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Accept proposals and cascade removals

**Files:**
- Create: `internal/proposal/accept.go`
- Test: `internal/proposal/accept_test.go`

**Interfaces:**
- Consumes: everything Task 4 lists as unexported for Task 5, and its test helpers; `store.ErrConflict`, `store.ErrNotFound`, `gitrepo.ErrRefMoved`.
- Produces: `func Accept(st *store.Store, wsID, taskID string, selected []string, author gitrepo.Signature) (commit string, err error)` (used by 3d's `POST …/processor-proposals/{task}/accept`); unexported `choose`, `deleteBranch`, `openCascades`, `cascadeFiles`.

- [ ] **Step 1: Write the failing test** `internal/proposal/accept_test.go`

```go
package proposal

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/store"
)

// twoTasksAndDoc is the output of the first run in most tests below.
var twoTasksAndDoc = contract.Output{
	Tasks:     []contract.OutputTask{textTask("host:web-01", "Host web-01"), textTask("host:db-01", "Host db-01")},
	Documents: []contract.OutputDocument{doc("sbom", "web-01")},
}

func TestAcceptAll(t *testing.T) {
	st := newStore(t)
	before := mainOID(t, st)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	p, _ := Get(st, ws, fixture.TaskB)
	commit, err := Accept(st, ws, fixture.TaskB, nil, person)
	if err != nil {
		t.Fatal(err)
	}
	if commit != mainOID(t, st) || logOf(t, st, "%P", commit) != before {
		t.Errorf("commit %s, main %s, parent %s", commit, mainOID(t, st), logOf(t, st, "%P", commit))
	}
	if got := logOf(t, st, "%an <%ae>|%cn <%ce>|%s", commit); got != person.String()+"|"+gitrepo.Bot.String()+"|Accept output proposal "+p.Branch {
		t.Errorf("author|committer|subject = %s", got)
	}
	want := []string{
		"answers/" + fixture.TaskB + ".md",
		"documents/" + idOf(t, p.Items, match.KindDocument, "sbom") + ".json",
		"generated/" + idOf(t, p.Items, match.KindTask, "host:db-01") + "/1.0.0.md",
		"generated/" + idOf(t, p.Items, match.KindTask, "host:web-01") + "/1.0.0.md",
	}
	slices.Sort(want)
	if got := mainFiles(t, st); !slices.Equal(got, want) {
		t.Errorf("main holds %v, want %v", got, want)
	}
	if len(branches(t, st)) != 0 {
		t.Errorf("branches %v, want none", branches(t, st))
	}
	if _, err := Accept(st, ws, fixture.TaskB, nil, person); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second accept: %v, want ErrNotFound", err)
	}
}

func TestAcceptSome(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	p, _ := Get(st, ws, fixture.TaskB)
	if _, err := Accept(st, ws, fixture.TaskB, []string{"task:host:web-01"}, person); err != nil {
		t.Fatal(err)
	}
	want := []string{"answers/" + fixture.TaskB + ".md", "generated/" + idOf(t, p.Items, match.KindTask, "host:web-01") + "/1.0.0.md"}
	if got := mainFiles(t, st); !slices.Equal(got, want) {
		t.Errorf("main holds %v, want %v", got, want)
	}
	if len(branches(t, st)) != 0 {
		t.Errorf("the rest of the proposal must be dropped: branches %v", branches(t, st))
	}
}

func TestAcceptRejectsBadSelections(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	before := mainOID(t, st)
	for name, sel := range map[string][]string{
		"unknown key":     {"task:host:web-01", "task:nope"},
		"wrong kind":      {"document:host:web-01"},
		"no kind":         {"host:web-01"},
		"empty selection": {},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Accept(st, ws, fixture.TaskB, sel, person)
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("err %v, want ErrInvalid", err)
			}
		})
	}
	if mainOID(t, st) != before || len(branches(t, st)) != 1 {
		t.Errorf("a rejected selection changed main or the branches")
	}
	if _, err := Accept(st, ws, fixture.TaskA, nil, person); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("no proposal: %v, want ErrNotFound", err)
	}
}

func TestAcceptAfterMainMovedOn(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	answer(t, st, fixture.TaskA, "done") // the engineer keeps working
	moved := mainOID(t, st)
	commit, err := Accept(st, ws, fixture.TaskB, nil, person)
	if err != nil {
		t.Fatal(err)
	}
	if logOf(t, st, "%P", commit) != moved {
		t.Errorf("accept must build on the current main %s", moved)
	}
	got := mainFiles(t, st)
	if !slices.Contains(got, "answers/"+fixture.TaskA+".md") || len(got) != 5 {
		t.Errorf("main holds %v", got)
	}
}

func TestAcceptNewVersionAndRemoval(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	first, _ := Get(st, ws, fixture.TaskB)
	if _, err := Accept(st, ws, fixture.TaskB, nil, person); err != nil {
		t.Fatal(err)
	}
	db := idOf(t, first.Items, match.KindTask, "host:db-01")
	answer(t, st, db, "patched")
	web := textTask("host:web-01", "Host web-01")
	web.Body = "About host:web-01, now with more detail."
	web.Bump = "major"
	propose(t, st, fixture.TaskB, digest2, contract.Output{Tasks: []contract.OutputTask{web}})
	p, err := Get(st, ws, fixture.TaskB)
	if err != nil {
		t.Fatal(err)
	}
	wantSummary(t, p.Items, "document sbom removed →", "task host:db-01 removed 1.0.0→", "task host:web-01 new-version 1.0.0→2.0.0")
	if _, err := Accept(st, ws, fixture.TaskB, nil, person); err != nil {
		t.Fatal(err)
	}
	webID := idOf(t, first.Items, match.KindTask, "host:web-01")
	want := []string{"answers/" + fixture.TaskB + ".md", "generated/" + webID + "/1.0.0.md", "generated/" + webID + "/2.0.0.md"}
	if got := mainFiles(t, st); !slices.Equal(got, want) {
		t.Errorf("main holds %v, want %v (the removed task's answer goes too)", got, want)
	}
	if len(branches(t, st)) != 0 {
		t.Errorf("branches %v: db-01 had nothing below it, so no cascade", branches(t, st))
	}
}

func TestAcceptedRemovalOpensCascade(t *testing.T) {
	st := newStore(t)
	// TaskB's answer makes parent; parent's answer makes child and a
	// document; child's answer makes grandchild.
	propose(t, st, fixture.TaskB, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("parent", "Parent")}})
	p, _ := Get(st, ws, fixture.TaskB)
	parent := idOf(t, p.Items, match.KindTask, "parent")
	mustAccept(t, st, fixture.TaskB)
	answer(t, st, parent, "yes")
	propose(t, st, parent, digest1, contract.Output{
		Tasks:     []contract.OutputTask{textTask("child", "Child")},
		Documents: []contract.OutputDocument{doc("report", "parent")},
	})
	p, _ = Get(st, ws, parent)
	child := idOf(t, p.Items, match.KindTask, "child")
	mustAccept(t, st, parent)
	answer(t, st, child, "yes")
	propose(t, st, child, digest1, contract.Output{Tasks: []contract.OutputTask{textTask("grandchild", "Grandchild")}})
	p, _ = Get(st, ws, child)
	grandchild := idOf(t, p.Items, match.KindTask, "grandchild")
	mustAccept(t, st, child)

	// TaskB's answer no longer yields parent.
	propose(t, st, fixture.TaskB, digest2, contract.Output{})
	mustAccept(t, st, fixture.TaskB)
	if got := branches(t, st); !slices.Equal(got, []string{cascadeBranch(parent)}) {
		t.Fatalf("branches %v, want the cascade of parent", got)
	}
	c, err := Get(st, ws, parent)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Cascade || c.Digest != "" || c.Task != parent {
		t.Errorf("cascade %+v", c)
	}
	if logOf(t, st, "%an <%ae>", c.Commit) != gitrepo.Bot.String() {
		t.Errorf("cascade author %s", logOf(t, st, "%an <%ae>", c.Commit))
	}
	wantSummary(t, c.Items, "document report removed →", "task child removed 1.0.0→", "task grandchild removed 1.0.0→")

	// Accepting only child's removal opens the cascade of child.
	if _, err := Accept(st, ws, parent, []string{"task:child"}, person); err != nil {
		t.Fatal(err)
	}
	if got := branches(t, st); !slices.Equal(got, []string{cascadeBranch(child)}) {
		t.Fatalf("branches %v, want the cascade of child", got)
	}
	mustAccept(t, st, child)
	for _, f := range mainFiles(t, st) {
		if strings.Contains(f, child) || strings.Contains(f, grandchild) || strings.Contains(f, parent) {
			t.Errorf("main still holds %s", f)
		}
	}
	// The report was not selected and stays; nothing is open any more.
	if len(branches(t, st)) != 0 {
		t.Errorf("branches %v", branches(t, st))
	}
}

func TestListRecomputesAgainstMain(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, contract.Output{
		Tasks:     []contract.OutputTask{textTask("keep", "Keep"), textTask("old", "Old")},
		Documents: []contract.OutputDocument{doc("d", "x")},
	})
	if _, err := Accept(st, ws, fixture.TaskB, nil, person); err != nil {
		t.Fatal(err)
	}
	answer(t, st, fixture.TaskA, "done")
	propose(t, st, fixture.TaskA, digest2, contract.Output{Tasks: []contract.OutputTask{textTask("a", "A")}})
	changed := textTask("keep", "Keep")
	changed.Title = "Keep (new title)"
	propose(t, st, fixture.TaskB, digest1, contract.Output{
		Tasks:     []contract.OutputTask{changed, textTask("new", "New")},
		Documents: []contract.OutputDocument{doc("d", "x")},
	})
	ps, err := List(st, ws)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range ps {
		names = append(names, p.Branch)
	}
	want := []string{Branch(fixture.TaskA, digest2), Branch(fixture.TaskB, digest1)}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("proposals %v, want %v", names, want)
	}
	for _, p := range ps {
		if p.Task == fixture.TaskB {
			wantSummary(t, p.Items,
				"document d unchanged →",
				"task keep new-version 1.0.0→1.1.0",
				"task new added →1.0.0",
				"task old removed 1.0.0→",
			)
		}
	}
}

func TestConcurrentAccepts(t *testing.T) {
	st := newStore(t)
	propose(t, st, fixture.TaskB, digest1, twoTasksAndDoc)
	before := mainOID(t, st)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = Accept(st, ws, fixture.TaskB, nil, person)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			t.Errorf("accept: %v", err)
		}
	}
	if n := gitCount(t, st, before+"..main"); n != 1 {
		t.Errorf("%d commits on main, want exactly one", n)
	}
	if len(mainFiles(t, st)) != 4 || len(branches(t, st)) != 0 {
		t.Errorf("main %v, branches %v", mainFiles(t, st), branches(t, st))
	}
}

func mustAccept(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if _, err := Accept(st, ws, id, nil, person); err != nil {
		t.Fatal(err)
	}
}

// gitCount returns the number of commits in the range rng.
func gitCount(t *testing.T, st *store.Store, rng string) int {
	t.Helper()
	return len(strings.Fields(gittest.Run(t, repoOf(t, st).Dir, "rev-list", rng)))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/proposal/`
Expected: FAIL — `undefined: Accept`.

- [ ] **Step 3: Implement** `internal/proposal/accept.go`

```go
package proposal

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/store"
)

// Accept applies the selected items ("task:<match_key>" / "document:<match_key>";
// nil = all that are not Unchanged) of the open proposal of taskID to the
// current main in one commit by author (validated, compare-and-swap; on
// store.ErrConflict it re-reads and retries up to three times), deletes the
// proposal branch, and opens a cascade proposal for every accepted removal
// of a generated task that has tasks below it (match.Below) or documents
// made from its answer. An unknown selector or an empty, non-nil selection
// is ErrInvalid (400); no open proposal is store.ErrNotFound (404).
// Selecting an unchanged item is allowed and changes nothing. When main
// already holds everything selected, no commit is made and main's current
// commit is returned.
func Accept(st *store.Store, wsID, taskID string, selected []string, author gitrepo.Signature) (commit string, err error) {
	if selected != nil && len(selected) == 0 {
		return "", fmt.Errorf("%w: select at least one item, or reject the proposal", ErrInvalid)
	}
	repo, err := st.WorkspaceRepo(wsID)
	if err != nil {
		return "", err
	}
	var p *Proposal
	var removed []string
	for range maxConflictRetries {
		if p, err = first(repo, taskID); err != nil {
			return "", err
		}
		var tipFS fs.FS
		if tipFS, err = repo.TreeFS(p.Commit); err != nil {
			return "", err
		}
		tip := loadSide(tipFS)
		commit, err = st.UpdateWorkspace(wsID, mainRef, author, "Accept output proposal "+p.Branch,
			func(tree fs.FS) ([]gitrepo.Change, error) {
				es, err := diff(loadSide(tree), tip, p.Task, p.Cascade)
				if err != nil {
					return nil, err
				}
				chosen, err := choose(es, selected)
				if err != nil {
					return nil, err
				}
				removed = removed[:0]
				var changes []gitrepo.Change
				for _, e := range chosen {
					changes = append(changes, e.changes...)
					if e.item.Kind == match.KindTask && e.item.Action == match.Removed {
						removed = append(removed, e.item.ID)
					}
				}
				return changes, nil
			})
		if !errors.Is(err, store.ErrConflict) {
			break
		}
	}
	if err != nil {
		return "", err
	}
	if err := deleteBranch(st, wsID, repo, p); err != nil {
		return commit, fmt.Errorf("accepted as %s, but the proposal branch was not deleted: %w", commit, err)
	}
	if err := openCascades(st, wsID, repo, commit, removed); err != nil {
		return commit, fmt.Errorf("accepted as %s, but a cascade proposal could not be opened: %w", commit, err)
	}
	return commit, nil
}

// choose returns the entries selected (nil = all that are not unchanged).
func choose(es []entry, selected []string) ([]entry, error) {
	if selected == nil {
		var out []entry
		for _, e := range es {
			if e.item.Action != match.Unchanged {
				out = append(out, e)
			}
		}
		return out, nil
	}
	known := map[string]bool{}
	for _, e := range es {
		known[selector(e.item)] = true
	}
	for _, s := range selected {
		if !known[s] {
			return nil, fmt.Errorf("%w: the proposal has no item %q (use task:<match_key> or document:<match_key>)", ErrInvalid, s)
		}
	}
	var out []entry
	for _, e := range es {
		if slices.Contains(selected, selector(e.item)) {
			out = append(out, e)
		}
	}
	return out, nil
}

// deleteBranch deletes the accepted proposal's branch. When a newer run
// replaced the branch meanwhile, the newer proposal is kept.
func deleteBranch(st *store.Store, wsID string, repo *gitrepo.Repo, p *Proposal) error {
	unlock := st.Lock(wsID)
	defer unlock()
	err := repo.DeleteRef(headsPrefix+p.Branch, p.Commit)
	if errors.Is(err, gitrepo.ErrRefMoved) {
		return nil
	}
	return err
}

// openCascades opens a cascade proposal, from main at commit, for each
// removed generated task that has generated tasks below it or documents made
// from its answer or theirs.
func openCascades(st *store.Store, wsID string, repo *gitrepo.Repo, commit string, removed []string) error {
	if len(removed) == 0 {
		return nil
	}
	fsys, err := repo.TreeFS(commit)
	if err != nil {
		return err
	}
	main := loadSide(fsys)
	var errs []error
	for _, id := range removed {
		files := cascadeFiles(main, id)
		if len(files) == 0 {
			continue
		}
		msg := "Propose removing what was generated below task " + id
		if _, err := writeBranch(st, wsID, id, cascadeBranch(id), files, msg); err != nil {
			errs = append(errs, fmt.Errorf("task %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// cascadeFiles returns the deletions a cascade of removed task id proposes
// on main: every version file and answer of the generated tasks below id,
// and the documents made from the answers of id or of those tasks.
func cascadeFiles(main side, id string) []gitrepo.Change {
	tasks, docs := scope(main.ws, id, true)
	var files []gitrepo.Change
	for _, t := range tasks {
		for _, v := range main.ws.Graph.Versions(t) {
			files = append(files, gitrepo.Change{Path: v.Path, Delete: true})
		}
		if answer := "answers/" + t + ".md"; main.ws.Answers[answer] != nil {
			files = append(files, gitrepo.Change{Path: answer, Delete: true})
		}
	}
	for _, d := range docs {
		files = append(files, gitrepo.Change{Path: d, Delete: true})
	}
	return files
}
```

- [ ] **Step 4: Run the tests to verify they pass, also under the race detector**

Run: `go test -race ./internal/proposal/ ./internal/match/ && go vet ./... && gofmt -l internal/`
Expected: PASS, and `gofmt -l` prints nothing.

- [ ] **Step 5: Run the full test suite**

Run: `make test`
Expected: PASS (needs git and Docker).

- [ ] **Step 6: Commit**

```bash
git add internal/proposal
git commit -m "Accept all or some items of an output proposal and propose cascading removals

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-Review Notes

- **Spec coverage:**
  - §5.4 matching table (unchanged / new version with `bump` and `previous` / new task with fresh UUID v4 / removal of missing keys; documents compared on payload) → Tasks 2 and 3.
  - §5.4 "answer stays in effect after a new version" → a new version adds a file and never touches `answers/`; status (phase 2) shows *pending update*.
  - §5.5 depth limit (8, configurable) → Task 2 (`Run.MaxDepth`, `ErrTooDeep`); cascading removal → Task 5; "answers stay in Git history" → removal deletes the answer file from `main` only.
  - §5.6 verification stored with the document → Task 3, including a real signed document from the `sign` image (`sign_test.go`).
  - §4.3 proposals on `custos/proposal/<task>/<digest>`, "a run whose output matches the current state creates no proposal", "a newer proposal replaces an open one", review as a diff with verification status, accept all/some/none → Tasks 4–5 (none = `Reject`).
  - §3.3 generated task and document fields (`origin`, `match_key`, `processor`, `produced_by`, `verification`) → written by Tasks 2–3 and checked with `workspace.Check` in the tests.
  - §8 unit tests for matching, cascades, depth limit → Tasks 1–3 and 5.
  - Not in this plan (by the note): running images and triggers (3d), HTTP endpoints (3d), `processor test` (3e).
- **What I could run:** in a scratch clone, with minimal stand-ins for `internal/contract` (types, `NewInput`, `ParseOutput`), `internal/attest` (`Canonical` via `encoding/json` with `UseNumber`, `Unverified`), `internal/runner`, `internal/proctest` and `internal/attest/carabiner`: `gofmt -l` (clean), `go vet ./...` (clean), `go test ./internal/match ./internal/proposal` (all pass, also with `-race` for `proposal`), and each task's state on its own (Task 2 without `documents.go`, Task 4 without `accept.go`) compiles and passes. **Not run:** `TestPlanVerifiesSignedDocumentFromRealProcessor` (it needs the real 3a images and runner and the 3b verifier; it type-checks against the note's signatures). The document tests that use `attest.Unverified` rely on 3b's `Unverified` returning the canonical JSON of a bare statement as `Payload`, as the note says; `TestPlanUnchangedDocument` compares `{ "_type": … }` with spaces against the fixture's compact form through it.
- **Placeholder scan:** none; every step holds complete files or an exact replacement.
- **Set review (2026-10-04):** plans 3a–3e were applied in order, as written, to one clone of `main`; `gofmt -l .`, `go vet ./...` and `go test ./...` passed after each plan, `make test` after 3e, and `go test -race` on runner, runs, store, proposal, merge and cmd/custos at the end (Docker 27.4, Go 1.27.1, Python 3.14). This included `TestPlanVerifiesSignedDocumentFromRealProcessor` with the real 3a images and 3b verifier.
- **Type consistency:** `CompareItems`, `KindTask`, `KindDocument`, `Current`, `ErrInvalidOutput`, `DefaultMaxDepth` are defined once in Tasks 1–2 and used with the same signatures in Tasks 3–5; `side`, `entry`, `diff`, `scope`, `first`, `writeBranch`, `cascadeBranch`, `deleteAll`, `conflict` are defined in Task 4 and used in Task 5.
- **Review Focus:** each of the five items has its test in the owning task (Tasks 2, 3, 4, 5).

## Decisions beyond the architecture note

Each is a candidate row for spec §11.3.

- `match.Depth(w, id)` returns -1 for every id that is not a generated task of w (also catalog tasks), and a producer that is not a generated task ends the chain at depth 0; `Plan` treats an answered task found in the catalog as depth 0 and fails a run on a task that is neither — `Depth` has no catalog parameter — a generated task whose producer was a hand-made unknown id counts as depth 1.
- A run on a task on a producer cycle fails with `ErrTooDeep` when it outputs tasks; the depth check is `depth >= max`, so `math.MaxInt` cannot overflow — the note says cycles count as too deep — none known.
- `Run.MaxDepth` ≤ 0 means 8 (`match.DefaultMaxDepth`), `Run.NewID` nil means `uuid.NewString`, `Run.Verifier` nil means `attest.Unverified` — safe defaults for 3e's `processor test` — a caller that forgets the verifier stores everything as unsigned.
- New exported `match.ErrInvalidOutput` wraps all output problems Plan finds (unregistered processor, unknown origin id, repeated match key) in one error — 3d records one error per run — none known.
- An origin id must name a catalog task (at the pin) or an existing generated task; its `version` is not checked, and a task created by the same output cannot be an origin — processors do not know the ids custos assigns — a processor cannot nest new tasks under each other in one run.
- A document is unchanged only when payload, `name` and `media_type` are all unchanged; a changed verification status alone (e.g. new trusted keys) is not a change — otherwise a rename would never reach `main`, while re-verification would churn proposals — stored verification of an unchanged document can go stale until its payload changes.
- Generated task bodies are stored normalised (`\r\n` → `\n`, final newline) and compared normalised; files are encoded from a flat struct, not `workspace.GeneratedMeta`, because `frontmatter.Encode`'s quoting fallback cannot encode an inlined struct — titles like `\tx` must round-trip — a later field added to `GeneratedMeta` must be added to `generatedFile` too.
- Document files are indented JSON (two spaces, no HTML escaping, final newline) in the field order of `workspace.Document` — readable Git diffs — content is re-indented, not kept byte for byte (signatures are in base64 payloads, so verification is unaffected).
- When two earlier items of one answer share a match key (only after hand edits), the smallest id/path is matched and the others are proposed for removal — deterministic — a hand-made duplicate is removed.
- Unchanged items are never rewritten, so their `produced_by` keeps the digest and answer commit of the run that last changed them — §5.4 makes unchanged a no-op — `produced_by.digest` does not tell which image last confirmed an item.
- `Write` deletes the task's open proposals first and then creates the branch with `store.UpdateWorkspace` (base = current `main`, changes that leave `main` as it is are dropped); if nothing remains, no branch is created and `""` is returned — `UpdateWorkspace` bases an existing branch on itself, and a run whose output `main` already holds must not open a proposal — for a moment the task has no open proposal; a concurrent writer of the same task makes one retry.
- Items are recomputed by comparing the branch tip with the current `main` within a scope (see "How a proposal's items are recomputed"); tasks by id, documents by path — the branch holds the full proposed state, so unchanged items and items accepted meanwhile show correctly — a generated task of the same answer that reaches `main` by hand while a proposal is open is listed as `removed`.
- `Accept` recomputes inside the `UpdateWorkspace` edit (so against the `main` it commits on), writes a normal commit on `main` (no merge commit with the branch), and deletes the branch only if it still points at the accepted commit; a newer proposal written meanwhile is kept — the proposal is a set of item changes, not a history to merge — commits pushed onto a proposal branch by hand are applied only as far as they change in-scope files.
- Accept with an empty, non-nil selection is `ErrInvalid` ("reject the proposal instead"); selecting an unchanged item is allowed and does nothing; nothing effective to apply returns `main`'s commit without a new commit and still closes the proposal — `{"items": []}` is more likely a client bug than a wish to drop everything — clients must call reject to drop a proposal.
- Errors after the accepting commit landed (deleting the branch, opening a cascade) are returned together with the commit — the change was made and must not be reported as not made — 3d must answer such errors with the commit, not as plain failure.
- A cascade also removes the documents produced by the removed task and by the tasks below it, and is opened when a removed task has tasks **or** such documents below it — nothing would ever remove those documents otherwise, since their answer is gone — documents disappear that the note's wording ("tasks below") did not mention.
- Cascades recurse through acceptance: accepting the removal of a task listed in a cascade opens that task's own cascade for what is still below it — partial accepts must not leave orphans unproposed — several cascade branches can be open at once (one per removed task).
- `Get` (and `Accept`, `Reject`) use the first branch by name when a hand push left several below `custos/proposal/<task>/`; `Reject` deletes all of them; branches with names custos never writes are ignored by `List`/`Get` — robustness against pushes — such branches are deleted by the next `Write` or `Reject` of that task.
