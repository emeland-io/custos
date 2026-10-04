# Phase 3d (Runs, triggers, REST API, serve, merge reruns, dry run) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Answers of bound tasks run their processor automatically — after an answer is saved, a pin moves (digest or binding change), a proposal is accepted, a push or a merge — with run records and logs in the data directory, retry, output proposals reviewable and acceptable over the REST API, forced reruns where a merge took `main`'s side of conflicting processor output, and dry runs of a candidate image.

**Architecture:** A new package `internal/runs` owns a queue, workers and run records (`<data-dir>/runs/<workspace>/<run>.json` and `.log`). `Scan` compares each bound answer's key (workspace, answer path, answer blob, digest) with the records, so it is idempotent and covers every trigger of §4.3 without tracking events; it is called from the new `store.OnMainMoved`, from `server.OnWorkspacePush` and once at start-up. A run reads `main`, builds the contract input (3a), runs the image with `runner` (3a), parses the output, matches it with `match.Plan` (3c, verifying documents with 3b's verifier) and writes it with `proposal.Write` (3c). `merge.Merge` keeps `main`'s side of conflicting generated output and reports the producing tasks, which `serve` reruns.

**Tech Stack:** Go 1.26, the `git` binary (≥ 2.38), the `docker` (or `podman`) command-line client, `github.com/google/uuid`; standard library otherwise.

**Spec:** [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md) — §4.3 (runs and triggers), §4.4 (rerun on merge), §5.2–5.7, §6.1 (Processor-Author: run history, logs, dry run), §7, and §11 (rulings override earlier sections; 2.3, 2.6, 2.10, 2.40, 2.53 matter here). Shared names and formats: [2026-10-04-phase-3-architecture.md](2026-10-04-phase-3-architecture.md), section "Plan 3d".

**Requires:** plans 3a (`contract`, `runner`, `proctest`), 3b (`attest`, `attest/carabiner`) and 3c (`match`, `proposal`) executed first, with the names the architecture note gives them. This plan was verified against minimal stand-ins of those packages written to the note's signatures; if an executed plan differs, adapt the call and say so in the commit message.

## Global Constraints

- Module `github.com/emeland-io/custos`, Go 1.26. No new third-party module in this plan (`github.com/google/uuid` is already required).
- Git ≥ 2.38 at runtime (ruling 2.9). Tests use real git in temp dirs.
- **Tests use real Docker**: every test that runs a processor runs a real container built by `proctest.Image`; there is no fake runner and no skip when Docker is missing — such tests fail.
- All repository writes go through `store` (lock, validation of `main`, compare-and-swap; ruling 2.3). Hooks never write. Processor output branches are written by `proposal.Write` (authored by `gitrepo.Bot`); accepting is authored by the person in `X-Custos-Author`.
- Run records are files `<data-dir>/runs/<workspace-id>/<run-id>.json` and `<run-id>.log`, run id = UUID v4; never Git, never an index.
- Proposal branches `custos/proposal/<task-uuid>/<64 hex digest>`; the message of a run's proposal commit is `Propose output of <processor> for task <id>`.
- `--max-generation-depth` default 8, `--processor-workers` default 2, `--processor-memory` default `512m`, `--container-runtime` default `docker`, `--secrets-dir` and `--trusted-keys` default empty; each with the `CUSTOS_…` environment variable named in the architecture note.
- REST: JSON with snake_case fields, error body `{"error", "problems"?}` through `api.WriteError` (400 malformed, 401 missing/invalid `X-Custos-Author` on a write, 404 unknown, 409 conflict, 413, 422 validation, 500). Every POST requires `X-Custos-Author`. `/api/workspaces/{id}/proposals` stays the pin proposals of 2c; output proposals are `/processor-proposals`.
- Commit messages: one imperative sentence without prefix, a blank line, `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Edits to existing files are given as `replace … with …` blocks; each quoted block occurs exactly once in the file at that point of the plan. Apply the blocks of a step in the order given.

## Review Focus

1. **An engineer saves an answer twice in quick succession** (or the pin moves while a run waits). Expected: one run, of the newest answer, and a later scan does not run it again (test `TestQueuedRunTakesNewestAnswer`, Task 4).
2. **The server is stopped while a container runs.** Expected: the run is not lost and not marked failed; the next start runs it again (test `TestRestartQueuesInterruptedRuns`, Task 4).
3. **The processor image cannot be found or pulled, or the runtime is missing.** Expected: the run fails with the reason, the queue goes on, `serve` keeps running (test `TestUnavailableImage`, Task 4; dry run in `TestDryRunFailures`, Task 5).
4. **An answer references an attachment that is not in the blob store** (pushed with plain Git, ruling 2.32). Expected: the run fails naming the attachment instead of mounting nothing (test `TestAttachments`, Task 4).
5. **A merge where both sides changed processor output and also an answer.** Expected: only the answer is reported as a conflict; once resolved, `main`'s output is kept and the producing task reruns (tests `TestMergeAnswerConflictWithGeneratedOutput`, Task 7, and `TestProcessorRunsEndToEnd`, Task 8).

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/store/store.go`, `write.go` | `OnMainMoved` callbacks after `main` moves |
| `internal/gitrepo/blobids.go` | `BlobIDs`: blob oids of a tree directory (answer blobs for run keys) |
| `internal/blobs/path.go` | `Path`: host path of a blob, for mounting |
| `internal/runs/record.go` | Package doc, `State`, `Outcome`, `Record`, `Config`, reasons, run key, record and log files |
| `internal/runs/service.go` | `Service`: loading records, queue, workers, scanner goroutine, `Scan`, `ScanAll`, `Wait`, `Runs`, `Get`, `Retry` |
| `internal/runs/scan.go` | Bindings, snapshot of `main`, scanning, `Rerun` |
| `internal/runs/execute.go` | One run, and `process` shared with dry runs |
| `internal/runs/dryrun.go` | Dry runs, `ErrInvalid`, the processor list |
| `internal/runs/json.go` | REST shapes of match items and proposals |
| `internal/runs/api.go` | `Register`: the eleven endpoints |
| `internal/merge/merge.go`, `http.go` | Keep `main`'s side of generated output, `Result.Rerun`, `onRerun` |
| `cmd/custos/runs.go`, `serve.go`, `main.go` | Flags and wiring of `serve` |

---

### Task 1: `store.OnMainMoved`

**Files:**
- Modify: `internal/store/store.go` (struct `Store`, `CreateWorkspace`, new `OnMainMoved`, `mainMoved`)
- Modify: `internal/store/write.go` (`UpdateWorkspace`, `SetRef`)
- Test: `internal/store/onmain_test.go`

**Interfaces:**
- Consumes: the existing `store` package (`UpdateWorkspace`, `SetRef`, `CreateWorkspace`, `Lock`, `mainRef`), test helpers `open`, `created`, `mainOf`, `put`, `answerFile`, `answerA`, `jane` from `internal/store/*_test.go`.
- Produces: `func (s *Store) OnMainMoved(f func(id string))` — `f` is called with the workspace id after `UpdateWorkspace` (ref `refs/heads/main`), `SetRef` (ref `refs/heads/main`, new ≠ old) or `CreateWorkspace` moved that workspace's `main`, synchronously, after the workspace's lock is released; never for no-op, failed or other-branch writes. Tasks 4 and 8 register `runs.Service.Scan` with it.

- [ ] **Step 1: Write the failing test**

Create `internal/store/onmain_test.go`:

```go
package store

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
)

// moves records the ids OnMainMoved reports.
type moves struct {
	mu  sync.Mutex
	ids []string
}

func (m *moves) add(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ids = append(m.ids, id)
}

func (m *moves) take() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := m.ids
	m.ids = nil
	return ids
}

func TestOnMainMoved(t *testing.T) {
	s, _ := open(t)
	var m moves
	s.OnMainMoved(m.add)
	id := fixture.WorkspaceID

	if err := s.CreateWorkspace(id, jane); err != nil {
		t.Fatal(err)
	}
	if got := m.take(); !slices.Equal(got, []string{id}) {
		t.Errorf("CreateWorkspace: %v", got)
	}

	first := mainOf(t, s, mainRef)
	commit, err := s.UpdateWorkspace(id, mainRef, jane, "answer", put(map[string]string{answerA: answerFile("1.1.0")}))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.take(); !slices.Equal(got, []string{id}) {
		t.Errorf("UpdateWorkspace on main: %v", got)
	}

	// No change, another branch, a rejected write: no call.
	if _, err := s.UpdateWorkspace(id, mainRef, jane, "again", put(map[string]string{answerA: answerFile("1.1.0")})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateWorkspace(id, "refs/heads/draft", jane, "draft", put(map[string]string{answerA: answerFile("1.0.0")})); err != nil {
		t.Fatal(err)
	}
	var rejected *RejectedError
	if _, err := s.UpdateWorkspace(id, mainRef, jane, "bad", put(map[string]string{answerA: "garbage"})); !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a rejection", err)
	}
	if got := m.take(); len(got) != 0 {
		t.Errorf("unexpected calls %v", got)
	}

	// SetRef on main calls, a lost compare-and-swap does not.
	repo, _ := s.WorkspaceRepo(id)
	next, err := repo.WriteCommit(gitrepo.CommitRequest{Base: commit, Parents: []string{commit}, Author: jane, Message: "next",
		Changes: []gitrepo.Change{{Path: answerA, Data: []byte(answerFile("1.0.0"))}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRef(id, mainRef, next, first); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale SetRef: %v", err)
	}
	if got := m.take(); len(got) != 0 {
		t.Errorf("lost compare-and-swap called %v", got)
	}
	if err := s.SetRef(id, mainRef, next, commit); err != nil {
		t.Fatal(err)
	}
	if got := m.take(); !slices.Equal(got, []string{id}) {
		t.Errorf("SetRef on main: %v", got)
	}
}

// TestOnMainMovedOutsideLock writes from inside the callback; if the
// callback ran under the workspace's lock, this would deadlock.
func TestOnMainMovedOutsideLock(t *testing.T) {
	s, _ := created(t)
	id := fixture.WorkspaceID
	var once sync.Once
	s.OnMainMoved(func(got string) {
		once.Do(func() {
			if _, err := s.UpdateWorkspace(got, "refs/heads/notes", gitrepo.Bot, "note", put(map[string]string{"notes/x.txt": "x"})); err != nil {
				t.Error(err)
			}
		})
	})
	if _, err := s.UpdateWorkspace(id, mainRef, jane, "answer", put(map[string]string{answerA: answerFile("1.1.0")})); err != nil {
		t.Fatal(err)
	}
	if mainOf(t, s, "refs/heads/notes") == "" {
		t.Error("the callback's write is missing")
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/store/ -run OnMainMoved`
Expected: FAIL: `s.OnMainMoved undefined (type *Store has no field or method OnMainMoved)`

- [ ] **Step 3: Add the callback list, `OnMainMoved` and `mainMoved`, and call it from `CreateWorkspace`**

In `internal/store/store.go`, replace

```go
	publicURL string // without trailing slash

	mu    sync.Mutex
	locks map[string]*sync.Mutex // by "catalog" or workspace id
}

```

with

```go
	publicURL string // without trailing slash

	mu     sync.Mutex
	locks  map[string]*sync.Mutex // by "catalog" or workspace id
	onMain []func(id string)      // see OnMainMoved
}

```

In `internal/store/store.go`, replace

```go
// ErrExists if it exists; error if the catalog has no main. Only the new
// repository's hook is installed (ruling 1.8).
func (s *Store) CreateWorkspace(id string, author gitrepo.Signature) (err error) {
	if !task.ValidID(id) {
		return &RejectedError{Problems: []problem.Problem{{
```

with

```go
// ErrExists if it exists; error if the catalog has no main. Only the new
// repository's hook is installed (ruling 1.8).
func (s *Store) CreateWorkspace(id string, author gitrepo.Signature) error {
	if err := s.createWorkspace(id, author); err != nil {
		return err
	}
	s.mainMoved(id)
	return nil
}

// createWorkspace is CreateWorkspace under the workspace's lock.
func (s *Store) createWorkspace(id string, author gitrepo.Signature) error {
	if !task.ValidID(id) {
		return &RejectedError{Problems: []problem.Problem{{
```

In `internal/store/store.go`, replace

```go
}

// catalogMain returns the commit of the catalog's main.
func (s *Store) catalogMain() (string, error) {
```

with

```go
}

// OnMainMoved registers f to be called with a workspace's id after
// UpdateWorkspace, SetRef or CreateWorkspace moved that workspace's main.
// f runs synchronously in the writer's goroutine once the workspace's lock
// is released, so it may write to the store itself; it should return
// quickly. It is not called for writes to other branches, for writes that
// change nothing, or for writes that fail (validation, lost
// compare-and-swap). Pushes do not go through the store; see
// server.OnWorkspacePush.
func (s *Store) OnMainMoved(f func(id string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onMain = append(s.onMain, f)
}

// mainMoved calls the OnMainMoved callbacks. The caller holds no lock.
func (s *Store) mainMoved(id string) {
	s.mu.Lock()
	fs := slices.Clone(s.onMain)
	s.mu.Unlock()
	for _, f := range fs {
		f(id)
	}
}

// catalogMain returns the commit of the catalog's main.
func (s *Store) catalogMain() (string, error) {
```

- [ ] **Step 4: Split `UpdateWorkspace` and `SetRef` into a locked part and a wrapper that calls the callbacks after the lock is released**

In `internal/store/write.go`, replace

```go
// count. Updates of main are validated with rules.CheckWorkspaceUpdate
// (RejectedError). The ref is moved with the expected old value (ErrConflict
// when it moved).
func (s *Store) UpdateWorkspace(id, ref string, author gitrepo.Signature, message string,
	edit func(tree fs.FS) ([]gitrepo.Change, error)) (string, error) {
	repo, err := s.WorkspaceRepo(id)
	if err != nil {
		return "", err
	}
	if err := checkBranch(ref); err != nil {
		return "", err
	}
	unlock := s.Lock(id)
```

with

```go
// count. Updates of main are validated with rules.CheckWorkspaceUpdate
// (RejectedError). The ref is moved with the expected old value (ErrConflict
// when it moved). A move of main calls the OnMainMoved callbacks after the
// lock is released.
func (s *Store) UpdateWorkspace(id, ref string, author gitrepo.Signature, message string,
	edit func(tree fs.FS) ([]gitrepo.Change, error)) (string, error) {
	commit, moved, err := s.updateWorkspace(id, ref, author, message, edit)
	if moved && ref == mainRef {
		s.mainMoved(id)
	}
	return commit, err
}

// updateWorkspace is UpdateWorkspace under the workspace's lock; moved
// reports whether the ref was moved.
func (s *Store) updateWorkspace(id, ref string, author gitrepo.Signature, message string,
	edit func(tree fs.FS) ([]gitrepo.Change, error)) (commit string, moved bool, err error) {
	repo, err := s.WorkspaceRepo(id)
	if err != nil {
		return "", false, err
	}
	if err := checkBranch(ref); err != nil {
		return "", false, err
	}
	unlock := s.Lock(id)
```

In `internal/store/write.go`, replace

```go
	old, _, err := repo.ResolveRef(ref)
	if err != nil {
		return "", err
	}
	base := old
	if base == "" {
		if base, _, err = repo.ResolveRef(mainRef); err != nil {
			return "", err
		}
	}
```

with

```go
	old, _, err := repo.ResolveRef(ref)
	if err != nil {
		return "", false, err
	}
	base := old
	if base == "" {
		if base, _, err = repo.ResolveRef(mainRef); err != nil {
			return "", false, err
		}
	}
```

In `internal/store/write.go`, replace

```go
	if base != "" {
		if tree, err = repo.TreeFS(base); err != nil {
			return "", err
		}
	}
	changes, err := edit(tree)
	if err != nil {
		return "", err
	}
	changes = effective(tree, changes)
	if len(changes) == 0 {
		return old, nil
	}
	req := gitrepo.CommitRequest{Base: base, Changes: changes, Author: author, Message: message}
```

with

```go
	if base != "" {
		if tree, err = repo.TreeFS(base); err != nil {
			return "", false, err
		}
	}
	changes, err := edit(tree)
	if err != nil {
		return "", false, err
	}
	changes = effective(tree, changes)
	if len(changes) == 0 {
		return old, false, nil
	}
	req := gitrepo.CommitRequest{Base: base, Changes: changes, Author: author, Message: message}
```

In `internal/store/write.go`, replace

```go
		req.Parents = []string{base}
	}
	commit, err := repo.WriteCommit(req)
	if err != nil {
		return "", err
	}
	if ref == mainRef {
		if err := s.validateMain(repo, id, old, commit); err != nil {
			return "", err
		}
	}
	if err := repo.UpdateRef(ref, commit, old); err != nil {
		return "", conflict(err)
	}
	return commit, nil
}

// SetRef moves ref of a workspace to an existing commit (fast-forward or merge
// result computed by the caller) with the same lock, validation (main only) and
// compare-and-swap. oldOID "" = ref must not exist.
func (s *Store) SetRef(id, ref, newOID, oldOID string) error {
	repo, err := s.WorkspaceRepo(id)
	if err != nil {
```

with

```go
		req.Parents = []string{base}
	}
	commit, err = repo.WriteCommit(req)
	if err != nil {
		return "", false, err
	}
	if ref == mainRef {
		if err := s.validateMain(repo, id, old, commit); err != nil {
			return "", false, err
		}
	}
	if err := repo.UpdateRef(ref, commit, old); err != nil {
		return "", false, conflict(err)
	}
	return commit, true, nil
}

// SetRef moves ref of a workspace to an existing commit (fast-forward or merge
// result computed by the caller) with the same lock, validation (main only) and
// compare-and-swap. oldOID "" = ref must not exist. A move of main calls the
// OnMainMoved callbacks after the lock is released.
func (s *Store) SetRef(id, ref, newOID, oldOID string) error {
	if err := s.setRef(id, ref, newOID, oldOID); err != nil {
		return err
	}
	if ref == mainRef && newOID != oldOID {
		s.mainMoved(id)
	}
	return nil
}

// setRef is SetRef under the workspace's lock.
func (s *Store) setRef(id, ref, newOID, oldOID string) error {
	repo, err := s.WorkspaceRepo(id)
	if err != nil {
```

- [ ] **Step 5: Run the store tests**

Run: `go test ./internal/store/`
Expected: PASS (all existing store tests too)

- [ ] **Step 6: Commit**

```bash
git add internal/store/store.go internal/store/write.go internal/store/onmain_test.go
git commit -m "Call OnMainMoved callbacks after the store moves a workspace's main.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```


### Task 2: Answer blob ids and blob paths

**Files:**
- Create: `internal/gitrepo/blobids.go`, `internal/blobs/path.go`
- Test: `internal/gitrepo/blobids_test.go`, `internal/blobs/path_test.go`

**Interfaces:**
- Consumes: `checkRev`, `checkPath`, `splitNUL`, `(*Repo).git` (package `gitrepo`); `(*blobs.Store).Has`, `path` (package `blobs`); test helpers `gittest.Init/Commit/Run`, blobs' `open` and `hash`.
- Produces:
  - `func (r *gitrepo.Repo) BlobIDs(rev, dir string) (map[string]string, error)` — path → git blob oid of the regular files below `dir` at `rev`; empty map for a missing directory. Task 4 uses it for `Record.AnswerBlob` (one `git ls-tree` per scan instead of one call per answer).
  - `func (s *blobs.Store) Path(sha string) (path string, ok bool)` — absolute host path of a stored blob, for the runner's read-only mount.

- [ ] **Step 1: Write the failing tests**

Create `internal/gitrepo/blobids_test.go`:

```go
package gitrepo

import (
	"testing"

	"github.com/emeland-io/custos/internal/gittest"
)

func TestBlobIDs(t *testing.T) {
	dir := gittest.Init(t)
	c := gittest.Commit(t, dir, map[string]string{
		"answers/a.md":  "one",
		"answers/b.md":  "two",
		"answersX/c.md": "not below answers/",
		"other.md":      "x",
	})
	r := &Repo{Dir: dir}
	ids, err := r.BlobIDs(c, "answers")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"answers/a.md": gittest.Run(t, dir, "rev-parse", c+":answers/a.md"),
		"answers/b.md": gittest.Run(t, dir, "rev-parse", c+":answers/b.md"),
	}
	if len(ids) != len(want) || ids["answers/a.md"] != want["answers/a.md"] || ids["answers/b.md"] != want["answers/b.md"] {
		t.Errorf("BlobIDs = %v, want %v", ids, want)
	}
	if ids, err := r.BlobIDs(c, "documents"); err != nil || len(ids) != 0 {
		t.Errorf("missing directory: %v %v", ids, err)
	}
	if _, err := r.BlobIDs("--all", "answers"); err == nil {
		t.Error("options must be rejected")
	}
}
```

Create `internal/blobs/path_test.go`:

```go
package blobs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPath(t *testing.T) {
	s, _ := open(t)
	sha, _, err := s.Put(strings.NewReader("scan"))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := s.Path(sha)
	if !ok || !filepath.IsAbs(p) {
		t.Fatalf("Path = %q, %v", p, ok)
	}
	if data, err := os.ReadFile(p); err != nil || string(data) != "scan" {
		t.Errorf("content %q, %v", data, err)
	}
	if _, ok := s.Path(hash("missing")); ok {
		t.Error("a missing blob has no path")
	}
	if _, ok := s.Path("../../etc/passwd"); ok {
		t.Error("a malformed hash has no path")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/gitrepo/ ./internal/blobs/`
Expected: FAIL: `r.BlobIDs undefined` and `s.Path undefined`

- [ ] **Step 3: Implement both**

Create `internal/gitrepo/blobids.go`:

```go
package gitrepo

import (
	"fmt"
	"strings"
)

// BlobIDs returns the object ids of the regular files below directory dir
// (such as "answers") in revision rev, keyed by path. A missing directory
// gives an empty map. Like TreeFS it reads the tree as stored.
func (r *Repo) BlobIDs(rev, dir string) (map[string]string, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	if err := checkPath(dir); err != nil {
		return nil, err
	}
	out, err := r.git("ls-tree", "-r", "-z", "--full-tree", rev, "--", dir+"/")
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	for _, rec := range splitNUL(out) {
		// <mode> SP <type> SP <oid> TAB <path>
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("unexpected git ls-tree output %q", rec)
		}
		if f[1] == "blob" && (f[0] == "100644" || f[0] == "100755") {
			ids[path] = f[2]
		}
	}
	return ids, nil
}
```

Create `internal/blobs/path.go`:

```go
package blobs

import "path/filepath"

// Path returns the absolute path of the blob with hash sha, for mounting it
// into a processor container; ok is false when the blob is not stored. The
// file is read-only and never changes.
func (s *Store) Path(sha string) (path string, ok bool) {
	if !s.Has(sha) {
		return "", false
	}
	abs, err := filepath.Abs(s.path(sha))
	if err != nil {
		return "", false
	}
	return abs, true
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/gitrepo/ ./internal/blobs/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gitrepo/blobids.go internal/gitrepo/blobids_test.go internal/blobs/path.go internal/blobs/path_test.go
git commit -m "Read the blob ids below a tree directory and the host path of a stored blob.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```


### Task 3: Run records on disk

**Files:**
- Create: `internal/runs/record.go`
- Test: `internal/runs/record_test.go`

**Interfaces:**
- Consumes: `task.Ref`, `task.ValidID`, `fixture.WriteDir`.
- Produces (exported names fixed by the architecture note): `State` (`Queued`, `Running`, `Succeeded`, `Failed`), `Outcome` (`Proposed`, `Unchanged`, `None`), `Record`, `Config{Workers, MaxDepth}`; plus the reason constants `ReasonAnswer`, `ReasonDigest`, `ReasonBinding`, `ReasonRetry`, `ReasonMerge`, `ReasonStartUp` (`"answer"`, `"digest"`, `"binding"`, `"retry"`, `"merge"`, `"start-up"`).
- Unexported, used by Tasks 4–6: `runKey(workspaceID, answerPath, answerBlob, digest string) string` (hex SHA-256 of the four values joined by `\n`); `type files struct{ dir string }` with `save(*Record) error`, `saveLog(*Record, []byte) error`, `log(*Record) ([]byte, error)` (missing → empty), `load() ([]*Record, error)`; `writeAtomic(path string, data []byte) error`.

- [ ] **Step 1: Write the failing test**

Create `internal/runs/record_test.go`:

```go
package runs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/task"
)

const runID = "1f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9"

func TestRunKey(t *testing.T) {
	k := runKey(fixture.WorkspaceID, "answers/x.md", "abc", "sha256:"+fixture.SHA256)
	if len(k) != 64 {
		t.Fatalf("key %q is not 64 hex digits", k)
	}
	if k != runKey(fixture.WorkspaceID, "answers/x.md", "abc", "sha256:"+fixture.SHA256) {
		t.Error("the key is not stable")
	}
	for _, other := range []string{
		runKey(fixture.WorkspaceID, "answers/x.md", "abd", "sha256:"+fixture.SHA256),
		runKey(fixture.WorkspaceID, "answers/y.md", "abc", "sha256:"+fixture.SHA256),
		runKey(fixture.WorkspaceID, "answers/x.md", "abc", "sha256:0"+fixture.SHA256[1:]),
		runKey("6c7d8e9f-0a1b-4c2d-b3e4-f5a6b7c8d9e0", "answers/x.md", "abc", "sha256:"+fixture.SHA256),
	} {
		if other == k {
			t.Error("a different input gives the same key")
		}
	}
}

func TestFilesRoundTrip(t *testing.T) {
	f := files{dir: filepath.Join(t.TempDir(), "runs")}
	if rs, err := f.load(); err != nil || len(rs) != 0 {
		t.Fatalf("empty: %v %v", rs, err)
	}
	r := &Record{
		ID: runID, Workspace: fixture.WorkspaceID, Task: task.Ref{ID: fixture.TaskB, Version: "1.0.0"},
		AnswerPath: "answers/" + fixture.TaskB + ".md", Reason: ReasonAnswer, State: Queued,
		Queued: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC),
	}
	if err := f.save(r); err != nil {
		t.Fatal(err)
	}
	if err := f.saveLog(r, []byte("boom\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(f.dir, fixture.WorkspaceID, runID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{`"started"`, `"finished"`, `"outcome"`, `"retry_of"`} {
		if strings.Contains(string(data), absent) {
			t.Errorf("record file has %s although it is empty:\n%s", absent, data)
		}
	}
	rs, err := f.load()
	if err != nil || len(rs) != 1 {
		t.Fatalf("load: %v %v", rs, err)
	}
	if got := rs[0]; got.ID != r.ID || got.Task != r.Task || got.State != Queued || !got.Queued.Equal(r.Queued) || !got.Started.IsZero() {
		t.Errorf("loaded %+v", got)
	}
	if log, err := f.log(r); err != nil || string(log) != "boom\n" {
		t.Errorf("log %q %v", log, err)
	}
	other := *r
	other.ID = "2f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9"
	if log, err := f.log(&other); err != nil || len(log) != 0 {
		t.Errorf("missing log: %q %v", log, err)
	}
}

func TestFilesLoadSkipsDamagedRecords(t *testing.T) {
	f := files{dir: filepath.Join(t.TempDir(), "runs")}
	dir := filepath.Join(f.dir, fixture.WorkspaceID)
	fixture.WriteDir(t, dir, map[string]string{
		runID + ".json": "{not json",
		"2f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9.json": `{"id":"3f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9","workspace":"` + fixture.WorkspaceID + `"}`,
		"notes.txt": "x",
	})
	fixture.WriteDir(t, filepath.Join(f.dir, "not-a-workspace"), map[string]string{runID + ".json": "{}"})
	rs, err := f.load()
	if err != nil || len(rs) != 0 {
		t.Errorf("load: %+v %v", rs, err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/runs/`
Expected: FAIL: `no non-test Go files` / `undefined: runKey`

- [ ] **Step 3: Write `internal/runs/record.go`**

Create `internal/runs/record.go`:

```go
// Package runs queues and executes processor runs (spec §4.3, §5): it finds
// the answers of bound tasks that have not been run yet, runs their
// processor through the runner, matches the output and writes it as a
// proposal branch. Run records are files in the data directory, not Git:
//
//	<data-dir>/runs/<workspace-id>/<run-id>.json   the record
//	<data-dir>/runs/<workspace-id>/<run-id>.log    the processor's stderr
package runs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emeland-io/custos/internal/task"
)

// State is where a run is in its life.
type State string

const (
	Queued    State = "queued"
	Running   State = "running"
	Succeeded State = "succeeded"
	Failed    State = "failed"
)

// Outcome is what a succeeded run produced.
type Outcome string

const (
	Proposed  Outcome = "proposed"  // a proposal branch was written
	Unchanged Outcome = "unchanged" // the output matches main; open proposals of the task were closed
	None      Outcome = ""          // queued, running or failed
)

// Reasons a run was queued.
const (
	ReasonAnswer  = "answer"   // a new or changed answer
	ReasonDigest  = "digest"   // the processor's image digest changed
	ReasonBinding = "binding"  // the task was bound to another processor
	ReasonRetry   = "retry"    // an engineer retried a failed run
	ReasonMerge   = "merge"    // a merge took main's side of generated output
	ReasonStartUp = "start-up" // found by the scan at start-up, with no earlier run of the answer
)

// Record is one run. Once a run starts, the answer, processor and digest
// fields show what it actually used.
type Record struct {
	ID           string    `json:"id"`
	Workspace    string    `json:"workspace"`
	Task         task.Ref  `json:"task"` // answered task id and the answer's task_version
	AnswerPath   string    `json:"answer_path"`
	AnswerBlob   string    `json:"answer_blob"` // git blob oid of the answer file
	AnswerCommit string    `json:"answer_commit"`
	Processor    string    `json:"processor"`
	Image        string    `json:"image"`
	Digest       string    `json:"digest"`
	Key          string    `json:"key"` // see runKey; a key with a record is not run again by Scan
	Reason       string    `json:"reason"`
	RetryOf      string    `json:"retry_of,omitempty"`
	State        State     `json:"state"`
	Outcome      Outcome   `json:"outcome,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	Error        string    `json:"error,omitempty"`
	Queued       time.Time `json:"queued"`
	Started      time.Time `json:"started,omitzero"`
	Finished     time.Time `json:"finished,omitzero"`
}

// Config tunes a Service.
type Config struct {
	Workers  int // runs executed at the same time; default 2
	MaxDepth int // maximum depth of generated tasks; default 8
}

// runKey identifies the work of a run: the hex SHA-256 of the workspace
// id, the answer path, the answer's blob oid and the processor digest,
// separated by newlines (none of them can contain one).
func runKey(workspaceID, answerPath, answerBlob, digest string) string {
	sum := sha256.Sum256([]byte(workspaceID + "\n" + answerPath + "\n" + answerBlob + "\n" + digest))
	return hex.EncodeToString(sum[:])
}

// files keeps records and logs below one directory.
type files struct{ dir string } // <data-dir>/runs

func (f files) path(r *Record, ext string) string {
	return filepath.Join(f.dir, r.Workspace, r.ID+ext)
}

// save writes the record atomically: a reader sees the old or the new
// file, never a partial one.
func (f files) save(r *Record) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(f.path(r, ".json"), append(data, '\n'))
}

// saveLog writes the run's log.
func (f files) saveLog(r *Record, log []byte) error {
	return writeAtomic(f.path(r, ".log"), log)
}

// log reads the run's log; a missing log is empty.
func (f files) log(r *Record) ([]byte, error) {
	data, err := os.ReadFile(f.path(r, ".log"))
	if errors.Is(err, fs.ErrNotExist) {
		return []byte{}, nil
	}
	return data, err
}

// load reads every record. Files that cannot be read as a record of the
// workspace directory they are in are skipped: one damaged file must not
// keep the server from starting, and the worst outcome is that its answer
// runs once more.
func (f files) load() ([]*Record, error) {
	wss, err := os.ReadDir(f.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Record
	for _, ws := range wss {
		if !ws.IsDir() || !task.ValidID(ws.Name()) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(f.dir, ws.Name()))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			id, ok := strings.CutSuffix(e.Name(), ".json")
			if !ok || !task.ValidID(id) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(f.dir, ws.Name(), e.Name()))
			if err != nil {
				return nil, err
			}
			var r Record
			if json.Unmarshal(data, &r) != nil || r.ID != id || r.Workspace != ws.Name() {
				continue
			}
			out = append(out, &r)
		}
	}
	return out, nil
}

// writeAtomic writes data to a temporary file next to path and renames it.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/runs/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/runs/record.go internal/runs/record_test.go
git commit -m "Store processor run records and logs as files in the data directory.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```


### Task 4: Run service: queue, scanning, execution

**Files:**
- Create: `internal/runs/service.go` (Service, New, Start, Scan, ScanAll, Wait, Runs, Get, Retry, queue and workers)
- Create: `internal/runs/scan.go` (bindings, `Scan`'s work, `Rerun`)
- Create: `internal/runs/execute.go` (one run: read main, run, parse, match, write the proposal)
- Test: `internal/runs/helpers_test.go`, `internal/runs/service_test.go`, `internal/runs/sign_test.go`

**Interfaces:**
- Consumes: Tasks 1–3; from plan 3a `contract.NewInput`, `contract.ParseOutput`, `runner.Runner` (`New`, `Run`, `Resolve`), `runner.Config`, `runner.Job`, `proctest.Image`; from 3b `attest.Verifier`, `attest.Unverified`, `attest.StatusVerified/StatusFailed`, `carabiner.New` (tests only); `proctest.SigningKey`, `proctest.SigningKeySecret`, `match.KindDocument` (tests only); from 3c `match.Run`, `match.Plan`, `match.Changes`, `proposal.Write`, `proposal.Branch`, `proposal.Get`, `proposal.Accept`; `store.Load`, `store.WorkspaceRepo`, `store.WorkspaceIDs`, `catalog.Catalog`, `catalog.Processor`, `workspace.Workspace/Answer`, `distribute.Reconcile` (tests only).
- Produces (note's signatures): `New(st *store.Store, bl *blobs.Store, rn *runner.Runner, v attest.Verifier, cfg Config) (*Service, error)`, `(*Service).Start(ctx)`, `Scan(wsID)`, `ScanAll()`, `Rerun(wsID string, taskIDs []string, reason string) ([]Record, error)`, `Retry(wsID, runID string) (*Record, error)`, `Runs(wsID string) ([]Record, error)`, `Get(wsID, runID string) (*Record, []byte, error)`, `Wait()`.
- Unexported, used by Tasks 5–6: `binding{name, proc, digest, version}`, `bindingFor(w, c, a) (binding, bool)`, `cutDigest(image) (ref, digest string, ok bool)`, `snapshot{commit, w, c, blobs}`, `(*Service).load(wsID) (*snapshot, bool, error)`, `(*Service).process(ctx, wsID, snap, a, b, ref, digest) (*match.Changes, []byte, error)`, fields `mu`, `cond`, `ctx`, `busy`, `newID`, `st`, `rn`.
- Test helpers (Tasks 5–6 reuse them): `proc{name, image, timeout}`, `registry(procs, bindings) string`, `env{st, bl, svc, catWork}`, `newEnv(t, reg, cfg)`, `newStoppedEnv`, `(*env).open/advanceCatalog/rebind/commit/runs`, `markdownAnswer`, `textAnswer`, `digestOf`, `wantRun`, `treeContains`, constants `ws`, `otherWS`, `answerA`, `answerB`, `noSuchID`, `alice`.

How a run works (the architecture note fixes the steps; the rest is this plan's):

- **Binding.** An answer `answers/<task>.md` is bound when its task is a catalog task with a binding in the registry of the workspace's pinned catalog, or a generated task whose *answered version* (`task_version`) names a `processor`; the name must be registered at the pin. The digest is the `sha256:<hex>` part of the registered image.
- **Scan** (non-blocking, coalescing): queues a run for every bound answer whose key (workspace, answer path, answer blob oid, digest) has no record — unless a run of that answer is already queued or taken by a worker that has not read `main` yet, because that run reads `main` when it starts and so covers the newer answer. Reason: `start-up` for an answer without earlier record found by `ScanAll`, otherwise from the newest earlier record of the answer: different blob → `answer`, different processor → `binding`, different digest → `digest`.
- **Executing** a run reads `main` again, re-resolves answer and binding, and records what it actually uses (blob, commit, processor, image, digest, key). A mutex (`snapMu`) is held by a scan from reading `main` until it has queued, and by a starting run from reading `main` until its key is recorded, so a scan either sees the run's key or the run reads a `main` at least as new as the scan's.
- **Failure** at any step makes the record `failed` with `error`, and the processor's stderr is kept as the log. A cancelled context (server shutdown) leaves the record `running`; `New` queues it again at the next start.

- [ ] **Step 1: Write the test helpers and the failing tests**

Create `internal/runs/helpers_test.go`:

```go
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
```

Create `internal/runs/service_test.go`:

```go
package runs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/proposal"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

func TestScanRunsBoundAnswers(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	answer := e.commit(t, ws, map[string]string{
		answerB: markdownAnswer("hello\n"),
		answerA: textAnswer(fixture.TaskA, "1.1.0", "unbound task"),
	})

	rs := e.runs(t, ws)
	if len(rs) != 1 {
		t.Fatalf("runs %+v, want one run for the bound answer only", rs)
	}
	r := rs[0]
	wantRun(t, r, Succeeded, Proposed, ReasonAnswer)
	if r.Task != (task.Ref{ID: fixture.TaskB, Version: "1.0.0"}) || r.AnswerPath != answerB || r.AnswerCommit != answer ||
		r.Processor != "scan" || r.Image != echo || r.Digest != digestOf(echo) || r.Started.IsZero() || r.Finished.IsZero() {
		t.Errorf("record %+v", r)
	}
	if want := proposal.Branch(fixture.TaskB, r.Digest); r.Branch != want {
		t.Errorf("branch %q, want %q", r.Branch, want)
	}
	p, err := proposal.Get(e.st, ws, fixture.TaskB)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].Kind != "document" || p.Items[0].MatchKey != "input" {
		t.Errorf("items %+v, want echo's input document", p.Items)
	}

	// Scanning again finds nothing new; neither does an unrelated commit.
	e.svc.Scan(ws)
	e.commit(t, ws, map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "changed")})
	if rs := e.runs(t, ws); len(rs) != 1 {
		t.Errorf("runs %+v, want still one", rs)
	}
}

func TestChangedAnswerRunsAgain(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("one\n")})
	first := e.runs(t, ws)[0]
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("two\n")})
	rs := e.runs(t, ws)
	if len(rs) != 2 || rs[1].ID != first.ID {
		t.Fatalf("runs %+v, want the new run first", rs)
	}
	wantRun(t, rs[0], Succeeded, Proposed, ReasonAnswer)
	if rs[0].AnswerBlob == first.AnswerBlob || rs[0].Key == first.Key {
		t.Error("the new run must record the new answer")
	}
}

func TestDigestAndBindingChangesRerun(t *testing.T) {
	echo, gen, fail := proctest.Image(t, "echo"), proctest.Image(t, "generate"), proctest.Image(t, "fail")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("task host Host one\n")})
	wantRun(t, e.runs(t, ws)[0], Succeeded, Proposed, ReasonAnswer)

	e.rebind(t, registry([]proc{{name: "scan", image: gen}}, map[string]string{fixture.TaskB: "scan"}))
	rs := e.runs(t, ws)
	if len(rs) != 2 {
		t.Fatalf("runs %+v, want a rerun after the digest change", rs)
	}
	wantRun(t, rs[0], Succeeded, Proposed, ReasonDigest)
	if rs[0].Digest != digestOf(gen) {
		t.Errorf("digest %s, want %s", rs[0].Digest, digestOf(gen))
	}
	p, err := proposal.Get(e.st, ws, fixture.TaskB)
	if err != nil {
		t.Fatal(err)
	}
	if p.Digest != digestOf(gen) {
		t.Errorf("open proposal %s, want the one of the new digest", p.Branch)
	}

	// The key holds the digest, not the name: the new binding uses an
	// image this answer has not run with yet.
	e.rebind(t, registry([]proc{{name: "scan", image: gen}, {name: "other", image: fail}}, map[string]string{fixture.TaskB: "other"}))
	rs = e.runs(t, ws)
	if len(rs) != 3 {
		t.Fatalf("runs %+v, want a rerun after the binding change", rs)
	}
	wantRun(t, rs[0], Failed, None, ReasonBinding)
	if rs[0].Processor != "other" {
		t.Errorf("processor %s", rs[0].Processor)
	}
}

func TestFailedRuns(t *testing.T) {
	tests := []struct {
		name, image, timeout, body string
		wantErr, wantLog           string
	}{
		{"non-zero exit", "fail", "", "x\n", "exited with code 3", "boom"},
		{"invalid output", "generate", "", "stderr about to fail\ngarbage\n", "invalid output", "about to fail"},
		{"timeout", "loop", "2s", "x\n", "timed out", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			image := proctest.Image(t, tt.image)
			e := newEnv(t, registry([]proc{{name: "p", image: image, timeout: tt.timeout}}, map[string]string{fixture.TaskB: "p"}), Config{})
			e.commit(t, ws, map[string]string{answerB: markdownAnswer(tt.body)})
			rs := e.runs(t, ws)
			if len(rs) != 1 {
				t.Fatalf("runs %+v", rs)
			}
			wantRun(t, rs[0], Failed, None, ReasonAnswer)
			if !strings.Contains(rs[0].Error, tt.wantErr) {
				t.Errorf("error %q, want %q", rs[0].Error, tt.wantErr)
			}
			_, log, err := e.svc.Get(ws, rs[0].ID)
			if err != nil || !strings.Contains(string(log), tt.wantLog) {
				t.Errorf("log %q, %v; want %q", log, err, tt.wantLog)
			}
			if _, err := proposal.Get(e.st, ws, fixture.TaskB); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("a failed run must not open a proposal: %v", err)
			}
		})
	}
}

// TestUnavailableImage covers an image that is neither local nor
// pullable: the run fails instead of hanging the queue.
func TestUnavailableImage(t *testing.T) {
	missing := "custos.test/missing@sha256:" + strings.Repeat("0", 64)
	e := newEnv(t, registry([]proc{{name: "p", image: missing}}, map[string]string{fixture.TaskB: "p"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n")})
	rs := e.runs(t, ws)
	if len(rs) != 1 || rs[0].State != Failed || rs[0].Error == "" {
		t.Fatalf("runs %+v, want one failed run", rs)
	}
}

func TestRetry(t *testing.T) {
	fail, echo := proctest.Image(t, "fail"), proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "p", image: fail}}, map[string]string{fixture.TaskB: "p"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n")})
	failed := e.runs(t, ws)[0]

	// Nothing changed, so scanning finds nothing; only a retry runs again.
	e.svc.Scan(ws)
	if rs := e.runs(t, ws); len(rs) != 1 {
		t.Fatalf("runs %+v", rs)
	}
	r, err := e.svc.Retry(ws, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.RetryOf != failed.ID || r.Reason != ReasonRetry || r.State != Queued || r.ID == failed.ID {
		t.Errorf("retry record %+v", r)
	}
	rs := e.runs(t, ws)
	if len(rs) != 2 || rs[0].ID != r.ID {
		t.Fatalf("runs %+v", rs)
	}
	wantRun(t, rs[0], Failed, None, ReasonRetry)

	// Fixing the processor reruns the answer on its own (new digest).
	e.rebind(t, registry([]proc{{name: "p", image: echo}}, map[string]string{fixture.TaskB: "p"}))
	ok := e.runs(t, ws)[0]
	wantRun(t, ok, Succeeded, Proposed, ReasonDigest)
	if _, err := e.svc.Retry(ws, ok.ID); !errors.Is(err, store.ErrConflict) {
		t.Errorf("retrying a succeeded run: %v, want a conflict", err)
	}
	if _, err := e.svc.Retry(ws, noSuchID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown run: %v", err)
	}
	if _, err := e.svc.Retry(otherWS, failed.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("run of another workspace: %v", err)
	}
}

// TestRestartQueuesInterruptedRuns writes the record of a run that was
// running when the previous process stopped; the next Service runs it.
func TestRestartQueuesInterruptedRuns(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n")})
	first := e.runs(t, ws)[0]

	interrupted := Record{ID: runID, Workspace: ws, Task: first.Task, AnswerPath: answerB, Reason: ReasonAnswer,
		State: Running, Queued: time.Now().UTC(), Started: time.Now().UTC()}
	data, _ := json.Marshal(interrupted)
	if err := os.WriteFile(filepath.Join(e.st.DataDir(), "runs", ws, runID+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	e.svc = e.open(t, Config{})
	e.svc.Start(t.Context())
	e.svc.Wait()
	r, _, err := e.svc.Get(ws, runID)
	if err != nil {
		t.Fatal(err)
	}
	wantRun(t, *r, Succeeded, Proposed, ReasonAnswer)
	if r.Key != first.Key || r.Digest != first.Digest {
		t.Errorf("the restarted run must record what it used: %+v", r)
	}
}

func TestRerunIgnoresKey(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n"), answerA: textAnswer(fixture.TaskA, "1.1.0", "a")})
	first := e.runs(t, ws)[0]
	if _, err := proposal.Accept(e.st, ws, fixture.TaskB, nil, alice); err != nil {
		t.Fatal(err)
	}
	if rs := e.runs(t, ws); len(rs) != 1 {
		t.Fatalf("accepting the output must not run the unchanged answer again: %+v", rs)
	}

	got, err := e.svc.Rerun(ws, []string{fixture.TaskB, fixture.TaskA, noSuchID, fixture.TaskB}, ReasonMerge)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Task.ID != fixture.TaskB || got[0].Reason != ReasonMerge || got[0].Key != first.Key {
		t.Fatalf("Rerun = %+v, want one forced run of TaskB", got)
	}
	rs := e.runs(t, ws)
	if len(rs) != 2 {
		t.Fatalf("runs %+v", rs)
	}
	wantRun(t, rs[0], Succeeded, Unchanged, ReasonMerge)
	if rs[0].Branch != "" {
		t.Errorf("an unchanged run has no branch: %q", rs[0].Branch)
	}
	if _, err := e.svc.Rerun(noSuchID, []string{fixture.TaskB}, ReasonMerge); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown workspace: %v", err)
	}
}

// TestGeneratedTaskWithProcessor follows §5.5: a generated task that names
// a processor runs it when answered, and output below the depth limit
// fails the run.
func TestGeneratedTaskWithProcessor(t *testing.T) {
	gen := proctest.Image(t, "generate")
	e := newEnv(t, registry([]proc{{name: "gen", image: gen}}, map[string]string{fixture.TaskB: "gen"}), Config{MaxDepth: 1})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("task host Host one\nprocessor host gen\n")})
	wantRun(t, e.runs(t, ws)[0], Succeeded, Proposed, ReasonAnswer)
	if _, err := proposal.Accept(e.st, ws, fixture.TaskB, nil, alice); err != nil {
		t.Fatal(err)
	}
	w, _, err := e.st.Load(ws, "")
	if err != nil {
		t.Fatal(err)
	}
	var generated string
	for _, g := range w.Generated {
		generated = g.ID
	}
	if generated == "" || len(e.runs(t, ws)) != 1 {
		t.Fatalf("generated %q, runs %+v", generated, e.runs(t, ws))
	}

	e.commit(t, ws, map[string]string{"answers/" + generated + ".md": textAnswer(generated, "1.0.0", "task sub Sub task")})
	rs := e.runs(t, ws)
	if len(rs) != 2 || rs[0].Task.ID != generated || rs[0].Processor != "gen" {
		t.Fatalf("runs %+v, want a run for the generated task", rs)
	}
	wantRun(t, rs[0], Failed, None, ReasonAnswer)
	if !strings.Contains(rs[0].Error, "depth") {
		t.Errorf("error %q, want the depth limit", rs[0].Error)
	}
}

func TestAttachments(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	sha, _, err := e.bl.Put(strings.NewReader("scan report"))
	if err != nil {
		t.Fatal(err)
	}
	withAttachment := func(sha string) string {
		return "---\ntask: " + fixture.TaskB + "\ntask_version: 1.0.0\ntype: markdown\nattachments:\n  - name: scan.txt\n    sha256: " +
			sha + "\n    media_type: text/plain\n---\nsee attachment\n"
	}
	e.commit(t, ws, map[string]string{answerB: withAttachment(sha)})
	r := e.runs(t, ws)[0]
	wantRun(t, r, Succeeded, Proposed, ReasonAnswer)
	repo, _ := e.st.WorkspaceRepo(ws)
	tree, err := repo.TreeFS("refs/heads/" + r.Branch)
	if err != nil {
		t.Fatal(err)
	}
	if !treeContains(t, tree, "documents", `/input/blobs/`+sha) {
		t.Error("the echoed input does not name the mounted attachment")
	}

	missing := strings.Repeat("ab", 32)
	e.commit(t, ws, map[string]string{answerB: withAttachment(missing)})
	r = e.runs(t, ws)[0]
	wantRun(t, r, Failed, None, ReasonAnswer)
	if !strings.Contains(r.Error, "not in the blob store") {
		t.Errorf("error %q", r.Error)
	}
}

// TestQueuedRunTakesNewestAnswer changes an answer while its run waits in
// the queue: one run is made, with the newest answer.
func TestQueuedRunTakesNewestAnswer(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newStoppedEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("one\n")})
	if err := e.svc.scan(ws, false); err != nil {
		t.Fatal(err)
	}
	second := e.commit(t, ws, map[string]string{answerB: markdownAnswer("two\n")})
	if err := e.svc.scan(ws, false); err != nil {
		t.Fatal(err)
	}
	e.svc.Start(t.Context())
	rs := e.runs(t, ws)
	if len(rs) != 1 {
		t.Fatalf("runs %+v, want one", rs)
	}
	wantRun(t, rs[0], Succeeded, Proposed, ReasonAnswer)
	if rs[0].AnswerCommit != second {
		t.Errorf("the run read %s, want the newest main %s", rs[0].AnswerCommit, second)
	}
	e.svc.Scan(ws)
	if rs := e.runs(t, ws); len(rs) != 1 {
		t.Errorf("the newest answer was run already; runs %+v", rs)
	}
}
```

Create `internal/runs/sign_test.go` — the run path end to end with a signed document: the secret mounted from `runner.Config.SecretsDir`, the document verified by 3b's verifier as `serve` constructs it:

```go
package runs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/attest/carabiner"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/proposal"
	"github.com/emeland-io/custos/internal/runner"
)

// TestSignedDocumentIsVerified runs the sign image with its secret mounted
// from the secrets directory and checks that the proposal's document carries
// the verification result of the service's verifier: verified with the
// signing key trusted, failed without trusted keys (as serve does without
// --trusted-keys).
func TestSignedDocumentIsVerified(t *testing.T) {
	sign := proctest.Image(t, "sign")
	secrets, pub := proctest.SigningKey(t)
	reg := "processors:\n  signer:\n    image: " + sign + "\n    secrets: [" + proctest.SigningKeySecret + "]\n" +
		"bindings:\n  " + fixture.TaskB + ": signer\n"
	keys := t.TempDir()
	if err := os.WriteFile(filepath.Join(keys, "signer.pub"), pub, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, keysDir, want string
	}{
		{"trusted key", keys, attest.StatusVerified},
		{"no trusted keys", "", attest.StatusFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newStoppedEnv(t, reg, Config{})
			v, err := carabiner.New(c.keysDir)
			if err != nil {
				t.Fatal(err)
			}
			e.svc, err = New(e.st, e.bl, runner.New(runner.Config{SecretsDir: secrets}), v, Config{})
			if err != nil {
				t.Fatal(err)
			}
			e.st.OnMainMoved(e.svc.Scan)
			e.svc.Start(t.Context())
			e.commit(t, ws, map[string]string{answerB: markdownAnswer("doc slsa web-01\n")})
			wantRun(t, e.runs(t, ws)[0], Succeeded, Proposed, ReasonAnswer)
			p, err := proposal.Get(e.st, ws, fixture.TaskB)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Items) != 1 || p.Items[0].Kind != match.KindDocument || p.Items[0].Verification == nil ||
				p.Items[0].Verification.Status != c.want {
				t.Fatalf("items %+v, want one document with status %s", p.Items, c.want)
			}
			if c.want == attest.StatusVerified && len(p.Items[0].Verification.Signers) == 0 {
				t.Error("a verified document must name its signer")
			}
		})
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/runs/`
Expected: FAIL: `undefined: New` (and `Service`, `Config` users)

- [ ] **Step 3: Write the service**

Create `internal/runs/service.go`:

```go
package runs

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/runner"
	"github.com/emeland-io/custos/internal/store"
)

// Service owns the run queue and the run records of one server.
type Service struct {
	st    *store.Store
	bl    *blobs.Store
	rn    *runner.Runner
	v     attest.Verifier
	cfg   Config
	files files
	newID func() string // run, dry-run, generated task and document ids
	now   func() time.Time

	// snapMu orders reading main for runs: a scan (or Rerun) holds it from
	// reading main until its runs are queued, a starting run from reading
	// main until its key is recorded. So a scan either sees the key of a
	// run that read the same main, or the run reads main after the scan.
	snapMu sync.Mutex

	mu       sync.Mutex
	cond     *sync.Cond      // signalled whenever the fields below change
	ctx      context.Context // from Start; Background before
	started  bool
	records  map[string]map[string]*Record // workspace id → run id → record
	keys     map[string]bool               // keys that have a record
	queue    []*Record                     // queued records, oldest first
	starting map[*Record]bool              // taken from the queue, main not read yet
	scans    map[string]bool               // pending scans: workspace id → only ScanAll asked for it
	busy     int                           // scans, runs and dry runs in progress
}

// New loads the run records below <data-dir>/runs. Records that were
// queued or running when the previous process stopped are queued again,
// oldest first; they start once Start is called.
func New(st *store.Store, bl *blobs.Store, rn *runner.Runner, v attest.Verifier, cfg Config) (*Service, error) {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 8
	}
	s := &Service{
		st: st, bl: bl, rn: rn, v: v, cfg: cfg,
		files:    files{dir: filepath.Join(st.DataDir(), "runs")},
		newID:    uuid.NewString,
		now:      func() time.Time { return time.Now().UTC() },
		ctx:      context.Background(),
		records:  map[string]map[string]*Record{},
		keys:     map[string]bool{},
		scans:    map[string]bool{},
		starting: map[*Record]bool{},
	}
	s.cond = sync.NewCond(&s.mu)
	rs, err := s.files.load()
	if err != nil {
		return nil, fmt.Errorf("loading run records: %w", err)
	}
	slices.SortFunc(rs, func(a, b *Record) int { return cmp.Or(a.Queued.Compare(b.Queued), cmp.Compare(a.ID, b.ID)) })
	for _, r := range rs {
		s.add(r)
		if r.State == Queued || r.State == Running {
			r.State, r.Started = Queued, time.Time{}
			if err := s.files.save(r); err != nil {
				return nil, err
			}
			s.queue = append(s.queue, r)
		}
	}
	return s, nil
}

// add indexes a record. The caller holds mu (or owns s exclusively).
func (s *Service) add(r *Record) {
	m := s.records[r.Workspace]
	if m == nil {
		m = map[string]*Record{}
		s.records[r.Workspace] = m
	}
	m[r.ID] = r
	if r.Key != "" {
		s.keys[r.Key] = true
	}
}

// Start starts the scanner and cfg.Workers workers and returns at once.
// They stop when ctx is cancelled; a run interrupted that way stays
// "running" on disk and is queued again by the next New.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started, s.ctx = true, ctx
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	}()
	go s.scanner(ctx)
	for range s.cfg.Workers {
		go s.worker(ctx)
	}
}

// Scan asks for workspace wsID to be scanned for answers that need a run
// (see scan). It does not block; requests for the same workspace that
// arrive before the scan starts are served by one scan.
func (s *Service) Scan(wsID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scans[wsID] = false
	s.cond.Broadcast()
}

// ScanAll asks for every workspace to be scanned. Runs it finds for
// answers that were never run before get the reason "start-up", since
// serve calls it once at start to catch up with changes made while it was
// down.
func (s *Service) ScanAll() {
	ids, err := s.st.WorkspaceIDs()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if _, ok := s.scans[id]; !ok {
			s.scans[id] = true
		}
	}
	s.cond.Broadcast()
}

// Wait blocks until no scan is pending or running, the queue is empty and
// no run or dry run is active. Tests use it after Start.
func (s *Service) Wait() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.scans) > 0 || len(s.queue) > 0 || s.busy > 0 {
		s.cond.Wait()
	}
}

// Runs returns the records of workspace wsID, newest first.
func (s *Service) Runs(wsID string) ([]Record, error) {
	if _, err := s.st.WorkspaceRepo(wsID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, 0, len(s.records[wsID]))
	for _, r := range s.records[wsID] {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b Record) int { return cmp.Or(b.Queued.Compare(a.Queued), cmp.Compare(b.ID, a.ID)) })
	return out, nil
}

// Get returns one record and its log; store.ErrNotFound when the
// workspace has no such run.
func (s *Service) Get(wsID, runID string) (*Record, []byte, error) {
	s.mu.Lock()
	r, ok := s.records[wsID][runID]
	var rec Record
	if ok {
		rec = *r
	}
	s.mu.Unlock()
	if !ok {
		return nil, nil, fmt.Errorf("run %s of workspace %s: %w", runID, wsID, store.ErrNotFound)
	}
	log, err := s.files.log(&rec)
	if err != nil {
		return nil, nil, err
	}
	return &rec, log, nil
}

// Retry queues a new run of the answer of failed run runID, with reason
// "retry" and retry_of set. store.ErrNotFound for an unknown run,
// store.ErrConflict when the run did not fail.
func (s *Service) Retry(wsID, runID string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.records[wsID][runID]
	if !ok {
		return nil, fmt.Errorf("run %s of workspace %s: %w", runID, wsID, store.ErrNotFound)
	}
	if old.State != Failed {
		return nil, fmt.Errorf("%w: run %s is %s; only failed runs can be retried", store.ErrConflict, runID, old.State)
	}
	r := &Record{
		Workspace: wsID, Task: old.Task, AnswerPath: old.AnswerPath, AnswerBlob: old.AnswerBlob,
		AnswerCommit: old.AnswerCommit, Processor: old.Processor, Image: old.Image, Digest: old.Digest,
		Key: old.Key, Reason: ReasonRetry, RetryOf: old.ID,
	}
	if err := s.enqueue(r); err != nil {
		return nil, err
	}
	out := *r
	return &out, nil
}

// enqueue gives r an id, the state Queued and the queue time, saves it
// and appends it to the queue. The caller holds mu.
func (s *Service) enqueue(r *Record) error {
	r.ID, r.State, r.Queued = s.newID(), Queued, s.now()
	if err := s.files.save(r); err != nil {
		return err
	}
	s.add(r)
	s.queue = append(s.queue, r)
	s.cond.Broadcast()
	return nil
}

// queuedFor returns a record of an answer that will still read main: a
// queued one, or one taken by a worker that has not read main yet; nil if
// there is none. The caller holds mu.
func (s *Service) queuedFor(wsID, path string) *Record {
	for _, r := range s.queue {
		if r.Workspace == wsID && r.AnswerPath == path {
			return r
		}
	}
	for r := range s.starting {
		if r.Workspace == wsID && r.AnswerPath == path {
			return r
		}
	}
	return nil
}

// latestFor returns the newest record of an answer, or nil. The caller
// holds mu.
func (s *Service) latestFor(wsID, path string) *Record {
	var latest *Record
	for _, r := range s.records[wsID] {
		if r.AnswerPath == path && (latest == nil || r.Queued.After(latest.Queued)) {
			latest = r
		}
	}
	return latest
}

// scanner serves Scan and ScanAll requests one workspace at a time.
func (s *Service) scanner(ctx context.Context) {
	for {
		s.mu.Lock()
		for len(s.scans) == 0 && ctx.Err() == nil {
			s.cond.Wait()
		}
		if ctx.Err() != nil {
			s.mu.Unlock()
			return
		}
		var id string
		var startUp bool
		for k, v := range s.scans {
			id, startUp = k, v
			break
		}
		delete(s.scans, id)
		s.busy++
		s.mu.Unlock()

		s.scan(id, startUp) // errors mean nothing can be run now; the next scan retries

		s.mu.Lock()
		s.busy--
		s.cond.Broadcast()
		s.mu.Unlock()
	}
}

// worker executes queued runs until ctx is cancelled.
func (s *Service) worker(ctx context.Context) {
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && ctx.Err() == nil {
			s.cond.Wait()
		}
		if ctx.Err() != nil {
			s.mu.Unlock()
			return
		}
		r := s.queue[0]
		s.queue = s.queue[1:]
		s.starting[r] = true
		s.busy++
		s.mu.Unlock()

		s.execute(ctx, r)

		s.mu.Lock()
		s.busy--
		s.cond.Broadcast()
		s.mu.Unlock()
	}
}

// update changes r under mu and saves it.
func (s *Service) update(r *Record, change func(r *Record)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(r)
	if r.Key != "" {
		s.keys[r.Key] = true
	}
	return s.files.save(r)
}
```

- [ ] **Step 4: Write scanning and `Rerun`**

Create `internal/runs/scan.go`:

```go
package runs

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

const mainRef = "refs/heads/main"

// binding is the processor an answer is run with.
type binding struct {
	name    string            // processor name in the registry
	proc    catalog.Processor // its registry entry at the workspace's pin
	digest  string            // "sha256:<hex>" from proc.Image
	version *task.Version     // the task version the answer was written for
}

// bindingFor returns the processor that answer a is run with: for a catalog
// task the binding in the pinned catalog's registry, for a generated task
// the processor named by the answered version; ok is false when there is
// none, the name is not registered, or the answered version does not exist.
func bindingFor(w *workspace.Workspace, c *catalog.Catalog, a *workspace.Answer) (binding, bool) {
	ref := task.Ref{ID: a.Task, Version: a.TaskVersion}
	var name string
	var v *task.Version
	var ok bool
	switch {
	case c.Tasks.Has(a.Task):
		v, ok = c.Tasks.Lookup(ref)
		name = c.Registry.Bindings[a.Task]
	case w.Graph.Has(a.Task):
		v, ok = w.Graph.Lookup(ref)
		if ok {
			if g := w.Generated[v.Path]; g != nil {
				name = g.Processor
			}
		}
	}
	if !ok || name == "" {
		return binding{}, false
	}
	p, ok := c.Registry.Processors[name]
	if !ok {
		return binding{}, false
	}
	_, digest, ok := cutDigest(p.Image)
	if !ok {
		return binding{}, false
	}
	return binding{name: name, proc: p, digest: digest, version: v}, true
}

// cutDigest splits "<ref>@sha256:<hex>" into the reference and the digest
// "sha256:<hex>"; the catalog's validation guarantees that form on main.
func cutDigest(image string) (ref, digest string, ok bool) {
	ref, digest, ok = strings.Cut(image, "@")
	return ref, digest, ok && strings.HasPrefix(digest, "sha256:")
}

// snapshot is a workspace's main as one scan or run sees it.
type snapshot struct {
	commit string
	w      *workspace.Workspace
	c      *catalog.Catalog
	blobs  map[string]string // answer path → git blob oid
}

// load reads workspace wsID's main; ok is false when it has no main yet.
func (s *Service) load(wsID string) (*snapshot, bool, error) {
	repo, err := s.st.WorkspaceRepo(wsID)
	if err != nil {
		return nil, false, err
	}
	commit, ok, err := repo.ResolveRef(mainRef)
	if err != nil || !ok {
		return nil, false, err
	}
	w, c, err := s.st.Load(wsID, commit)
	if err != nil {
		return nil, false, err
	}
	ids, err := repo.BlobIDs(commit, "answers")
	if err != nil {
		return nil, false, err
	}
	return &snapshot{commit: commit, w: w, c: c, blobs: ids}, true, nil
}

// scan queues a run for every answer of a bound task on wsID's main whose
// key has no record and that has no queued run already (a queued run reads
// main when it starts, so it covers the newer answer). startUp sets the
// reason of answers never run before to "start-up".
func (s *Service) scan(wsID string, startUp bool) error {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	snap, ok, err := s.load(wsID)
	if err != nil || !ok {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range slices.Sorted(maps.Keys(snap.w.Answers)) {
		a := snap.w.Answers[path]
		b, ok := bindingFor(snap.w, snap.c, a)
		if !ok {
			continue
		}
		key := runKey(wsID, path, snap.blobs[path], b.digest)
		if s.keys[key] || s.queuedFor(wsID, path) != nil {
			continue
		}
		r := s.newRecord(wsID, snap, a, b)
		r.Key, r.Reason = key, s.reason(wsID, path, snap.blobs[path], b, startUp)
		if err := s.enqueue(r); err != nil {
			return err
		}
	}
	return nil
}

// reason tells why an answer needs a run, from its newest earlier record.
// The caller holds mu.
func (s *Service) reason(wsID, path, blob string, b binding, startUp bool) string {
	prev := s.latestFor(wsID, path)
	switch {
	case prev == nil && startUp:
		return ReasonStartUp
	case prev == nil || prev.AnswerBlob != blob:
		return ReasonAnswer
	case prev.Processor != b.name:
		return ReasonBinding
	case prev.Digest != b.digest:
		return ReasonDigest
	}
	return ReasonAnswer
}

// newRecord fills the answer and processor fields of a new record.
func (s *Service) newRecord(wsID string, snap *snapshot, a *workspace.Answer, b binding) *Record {
	return &Record{
		Workspace: wsID, Task: task.Ref{ID: a.Task, Version: a.TaskVersion},
		AnswerPath: a.Path, AnswerBlob: snap.blobs[a.Path], AnswerCommit: snap.commit,
		Processor: b.name, Image: b.proc.Image, Digest: b.digest,
	}
}

// Rerun queues a run for the answer of each task in taskIDs on wsID's
// main, whether or not its key has a record (merge reruns, §4.4). Tasks
// without an answer or without a processor are skipped. An answer that
// already has a queued run gets no second one; that record is returned
// instead. The result is sorted by task id.
func (s *Service) Rerun(wsID string, taskIDs []string, reason string) ([]Record, error) {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	snap, ok, err := s.load(wsID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("workspace %s has no main branch: %w", wsID, store.ErrNotFound)
	}
	ids := slices.Clone(taskIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Record{}
	for _, id := range ids {
		a := snap.w.Answers["answers/"+id+".md"]
		if a == nil {
			continue
		}
		b, ok := bindingFor(snap.w, snap.c, a)
		if !ok {
			continue
		}
		if q := s.queuedFor(wsID, a.Path); q != nil {
			out = append(out, *q)
			continue
		}
		r := s.newRecord(wsID, snap, a, b)
		r.Key, r.Reason = runKey(wsID, a.Path, snap.blobs[a.Path], b.digest), reason
		if err := s.enqueue(r); err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, nil
}
```

- [ ] **Step 5: Write the execution of one run**

Create `internal/runs/execute.go`:

```go
package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/proposal"
	"github.com/emeland-io/custos/internal/runner"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// execute performs one queued run: it reads main as it is now, runs the
// processor bound to the answer, matches the output and writes the
// proposal. Every failure ends the run as Failed with the error and the
// log kept. When ctx is cancelled (server shutdown) the record is left
// "running", so the next process queues it again.
func (s *Service) execute(ctx context.Context, r *Record) {
	s.update(r, func(r *Record) { r.State, r.Started = Running, s.now() })
	outcome, branch, log, err := s.perform(ctx, r)
	if ctx.Err() != nil {
		return
	}
	if log == nil {
		log = []byte{}
	}
	s.files.saveLog(r, log)
	s.update(r, func(r *Record) {
		r.Finished = s.now()
		if err != nil {
			r.State, r.Error = Failed, err.Error()
			return
		}
		r.State, r.Outcome, r.Branch = Succeeded, outcome, branch
	})
}

// perform runs r against main and returns the outcome, the proposal branch
// (when proposed) and the processor's log.
func (s *Service) perform(ctx context.Context, r *Record) (Outcome, string, []byte, error) {
	snap, a, b, err := s.prepare(r)
	if err != nil {
		return None, "", nil, err
	}
	ref, digest, err := s.rn.Resolve(ctx, b.proc.Image)
	if err != nil {
		return None, "", nil, err
	}
	ch, log, err := s.process(ctx, r.Workspace, snap, a, b, ref, digest)
	if err != nil {
		return None, "", log, err
	}
	branch, err := proposal.Write(s.st, r.Workspace, a.Task, digest, ch,
		fmt.Sprintf("Propose output of %s for task %s", b.name, a.Task))
	if err != nil {
		return None, "", log, fmt.Errorf("writing the proposal: %w", err)
	}
	if branch == "" {
		return Unchanged, "", log, nil
	}
	return Proposed, branch, log, nil
}

// prepare reads main for run r and records what the run uses, which may
// be newer than what was queued: answer, processor, digest and key.
func (s *Service) prepare(r *Record) (*snapshot, *workspace.Answer, binding, error) {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.starting, r)
		s.mu.Unlock()
	}()
	snap, ok, err := s.load(r.Workspace)
	if err != nil {
		return nil, nil, binding{}, err
	}
	if !ok {
		return nil, nil, binding{}, fmt.Errorf("workspace %s has no main branch", r.Workspace)
	}
	a := snap.w.Answers[r.AnswerPath]
	if a == nil {
		return nil, nil, binding{}, fmt.Errorf("%s is no longer on main", r.AnswerPath)
	}
	b, ok := bindingFor(snap.w, snap.c, a)
	if !ok {
		return nil, nil, binding{}, fmt.Errorf("task %s is no longer bound to a registered processor", a.Task)
	}
	err = s.update(r, func(r *Record) {
		r.Task = task.Ref{ID: a.Task, Version: a.TaskVersion}
		r.AnswerBlob, r.AnswerCommit = snap.blobs[a.Path], snap.commit
		r.Processor, r.Image, r.Digest = b.name, b.proc.Image, b.digest
		r.Key = runKey(r.Workspace, a.Path, r.AnswerBlob, b.digest)
	})
	return snap, a, b, err
}

// process runs image ref (digest "sha256:<hex>") with the timeout, network
// and secrets of b.proc on answer a and matches the output against snap;
// runs and dry runs (which pass another image) share it. It returns the
// processor's log also when it fails.
func (s *Service) process(ctx context.Context, wsID string, snap *snapshot, a *workspace.Answer, b binding,
	ref, digest string) (*match.Changes, []byte, error) {
	proc := b.proc
	input, err := json.Marshal(contract.NewInput(wsID, b.version, a))
	if err != nil {
		return nil, nil, err
	}
	job := runner.Job{Image: ref, Network: proc.Network, Secrets: proc.Secrets, Input: input, Blobs: map[string]string{}}
	if proc.Timeout != "" {
		if job.Timeout, err = time.ParseDuration(proc.Timeout); err != nil {
			return nil, nil, fmt.Errorf("processor %s: timeout %q: %w", b.name, proc.Timeout, err)
		}
	}
	for _, at := range a.Attachments {
		path, ok := s.bl.Path(at.SHA256)
		if !ok {
			return nil, nil, fmt.Errorf("attachment %q (sha256 %s) is not in the blob store", at.Name, at.SHA256)
		}
		job.Blobs[at.SHA256] = path
	}
	res, err := s.rn.Run(ctx, job)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case res.TimedOut:
		return nil, res.Log, fmt.Errorf("processor %s timed out after %s", b.name, res.Duration.Round(time.Second))
	case res.ExitCode != 0:
		return nil, res.Log, fmt.Errorf("processor %s exited with code %d", b.name, res.ExitCode)
	}
	out, err := contract.ParseOutput(res.Stdout)
	if err != nil {
		return nil, res.Log, fmt.Errorf("processor %s wrote invalid output: %w", b.name, err)
	}
	ch, err := match.Plan(match.Run{
		Workspace: snap.w, Catalog: snap.c,
		Task:      task.Ref{ID: a.Task, Version: a.TaskVersion},
		Processor: b.name, Digest: digest, AnswerCommit: snap.commit,
		MaxDepth: s.cfg.MaxDepth, Verifier: s.v, NewID: s.newID,
	}, out)
	if err != nil {
		return nil, res.Log, err
	}
	return ch, res.Log, nil
}
```

- [ ] **Step 6: Run the tests (Docker must be running; the first run builds the test images)**

Run: `go vet ./internal/runs/ && go test -race ./internal/runs/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/runs/service.go internal/runs/scan.go internal/runs/execute.go internal/runs/helpers_test.go internal/runs/service_test.go internal/runs/sign_test.go
git commit -m "Queue and execute processor runs for bound answers and write their output as proposals.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```


### Task 5: Dry runs and the processor list

**Files:**
- Create: `internal/runs/dryrun.go` (`DryRun`, `DryRunStatus`, `DryRunReport`, `Sample`, `ErrInvalid`, `Processors`, `ProcessorInfo`)
- Create: `internal/runs/json.go` (`ItemJSON`, `ProposalJSON`: the REST shapes of `match.Item` and `proposal.Proposal`)
- Modify: `internal/runs/service.go` (field `dry` and its initialisation)
- Test: `internal/runs/dryrun_test.go`

**Interfaces:**
- Consumes: Task 4 (`bindingFor`, `cutDigest`, `load`, `process`, `mu`, `cond`, `busy`, `ctx`, `newID`, test helpers); `catalog.Load`; `match.Item`, `match.Action`; `proposal.Proposal`; `workspace.Verification`.
- Produces:
  - `var ErrInvalid = errors.New("invalid request")` — the API answers 400.
  - `func (s *Service) DryRun(name, image string) (id string, err error)` — `ErrInvalid` unless `image` matches `^[^\s@]+@sha256:[0-9a-f]{64}$`; `store.ErrNotFound` unless `name` is registered on the catalog's `main`.
  - `func (s *Service) DryRunStatus(id string) (DryRunReport, error)` — `store.ErrNotFound` for unknown ids.
  - `type DryRunReport struct{ State State; Runs, Failed, Proposals, Workspaces int; Samples []Sample; Error string }` (JSON `state, runs, failed, proposals, workspaces, samples, error?`), `type Sample struct{ Workspace, Task string; Items []ItemJSON }`.
  - `func (s *Service) Processors() ([]ProcessorInfo, error)`, `type ProcessorInfo struct{ Name, Image, Digest, Timeout string; Network bool; Secrets, BoundTasks []string }` (JSON `name, image, digest, timeout, network, secrets, bound_tasks`).
  - `type ItemJSON` (JSON `kind, match_key, action, id, from?, to?, title, verification?`), `itemsJSON([]match.Item) []ItemJSON`, `type ProposalJSON` (JSON `branch, commit, task, digest?, cascade, items`), `proposalJSON(proposal.Proposal) ProposalJSON`.

A dry run (§5.7) runs the candidate image over every answer bound to the processor name — in every workspace, with each workspace's pinned bindings and the registered timeout, network and secrets — and matches each output against `main` with the candidate digest. It counts runs, failures, outputs that would open a proposal (`len(Changes.Files) > 0`) and the workspaces they are in, and keeps up to five samples. Nothing is written: no run records, no branches. Jobs run in their own goroutine (one container at a time), count towards `Wait`, and live in memory only (ruling of the note).

- [ ] **Step 1: Write the failing test**

Create `internal/runs/dryrun_test.go`:

```go
package runs

import (
	"errors"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/proposal"
	"github.com/emeland-io/custos/internal/store"
)

// dryEnv has processor "scan" (echo) bound to TaskB and an answer to TaskB
// in two workspaces, all runs done.
func dryEnv(t *testing.T) *env {
	t.Helper()
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	if err := e.st.CreateWorkspace(otherWS, alice); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ws, otherWS} {
		e.commit(t, id, map[string]string{answerB: markdownAnswer("task host Host one\n")})
	}
	e.svc.Wait()
	return e
}

func TestDryRun(t *testing.T) {
	e := dryEnv(t)
	gen := proctest.Image(t, "generate")
	id, err := e.svc.DryRun("scan", gen)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	rep, err := e.svc.DryRunStatus(id)
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != Succeeded || rep.Runs != 2 || rep.Failed != 0 || rep.Proposals != 2 || rep.Workspaces != 2 || len(rep.Samples) != 2 {
		t.Fatalf("report %+v", rep)
	}
	s := rep.Samples[0]
	if s.Task != fixture.TaskB || !strings.Contains(s.Workspace+otherWS+ws, s.Workspace) || len(s.Items) == 0 {
		t.Errorf("sample %+v", s)
	}
	// Nothing was written: the open proposals are still those of echo.
	for _, w := range []string{ws, otherWS} {
		p, err := proposal.Get(e.st, w, fixture.TaskB)
		if err != nil || p.Digest == digestOf(gen) {
			t.Errorf("workspace %s: proposal %+v, %v", w, p, err)
		}
		if rs, _ := e.svc.Runs(w); len(rs) != 1 {
			t.Errorf("workspace %s: a dry run must not create run records: %+v", w, rs)
		}
	}
}

func TestDryRunFailures(t *testing.T) {
	e := dryEnv(t)
	id, err := e.svc.DryRun("scan", proctest.Image(t, "fail"))
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	if rep, _ := e.svc.DryRunStatus(id); rep.State != Succeeded || rep.Runs != 2 || rep.Failed != 2 || rep.Proposals != 0 {
		t.Errorf("failing image: %+v", rep)
	}

	id, err = e.svc.DryRun("scan", "custos.test/missing@sha256:"+strings.Repeat("0", 64))
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	if rep, _ := e.svc.DryRunStatus(id); rep.State != Failed && rep.Failed != rep.Runs {
		t.Errorf("missing image: %+v", rep)
	}

	if _, err := e.svc.DryRun("scan", "custos.test/echo:latest"); !errors.Is(err, ErrInvalid) {
		t.Errorf("image without digest: %v", err)
	}
	if _, err := e.svc.DryRun("nope", proctest.Image(t, "echo")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown processor: %v", err)
	}
	if _, err := e.svc.DryRunStatus(noSuchID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown dry run: %v", err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/runs/ -run DryRun`
Expected: FAIL: `e.svc.DryRun undefined`

- [ ] **Step 3: Write the REST shapes**

Create `internal/runs/json.go`:

```go
package runs

import (
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/proposal"
	"github.com/emeland-io/custos/internal/workspace"
)

// ItemJSON is a match.Item in the REST API.
type ItemJSON struct {
	Kind         string                  `json:"kind"`
	MatchKey     string                  `json:"match_key"`
	Action       match.Action            `json:"action"`
	ID           string                  `json:"id"`
	From         string                  `json:"from,omitempty"`
	To           string                  `json:"to,omitempty"`
	Title        string                  `json:"title"`
	Verification *workspace.Verification `json:"verification,omitempty"`
}

func itemsJSON(items []match.Item) []ItemJSON {
	out := make([]ItemJSON, 0, len(items))
	for _, it := range items {
		out = append(out, ItemJSON{Kind: it.Kind, MatchKey: it.MatchKey, Action: it.Action, ID: it.ID,
			From: it.From, To: it.To, Title: it.Title, Verification: it.Verification})
	}
	return out
}

// ProposalJSON is a proposal.Proposal in the REST API.
type ProposalJSON struct {
	Branch  string     `json:"branch"`
	Commit  string     `json:"commit"`
	Task    string     `json:"task"`
	Digest  string     `json:"digest,omitempty"`
	Cascade bool       `json:"cascade"`
	Items   []ItemJSON `json:"items"`
}

func proposalJSON(p proposal.Proposal) ProposalJSON {
	return ProposalJSON{Branch: p.Branch, Commit: p.Commit, Task: p.Task, Digest: p.Digest, Cascade: p.Cascade, Items: itemsJSON(p.Items)}
}
```

- [ ] **Step 4: Write dry runs and the processor list**

Create `internal/runs/dryrun.go`:

```go
package runs

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/store"
)

// ErrInvalid reports a malformed request, such as a dry-run image that is
// not pinned by digest. The REST API answers it with 400.
var ErrInvalid = errors.New("invalid request")

// maxSamples bounds the samples of a dry-run report.
const maxSamples = 5

var pinnedImageRE = regexp.MustCompile(`^[^\s@]+@sha256:[0-9a-f]{64}$`)

// DryRunReport is the state of a dry run (spec §5.7): how many bound
// answers it ran, how many failed, how many proposals it would open in how
// many workspaces, and up to five samples.
type DryRunReport struct {
	State      State    `json:"state"` // running, succeeded or failed
	Runs       int      `json:"runs"`
	Failed     int      `json:"failed"`
	Proposals  int      `json:"proposals"`
	Workspaces int      `json:"workspaces"`
	Samples    []Sample `json:"samples"`
	Error      string   `json:"error,omitempty"` // why the dry run itself failed
}

// Sample is one proposal a dry run would open.
type Sample struct {
	Workspace string     `json:"workspace"`
	Task      string     `json:"task"`
	Items     []ItemJSON `json:"items"`
}

type dryRun struct {
	report     DryRunReport
	workspaces map[string]bool
}

// DryRun starts running image instead of the registered image of
// processor name over every answer bound to name, in every workspace
// (bindings at each workspace's pin), and returns the job id. Nothing is
// written: outputs are matched against main and counted. The image must be
// pinned by digest (ErrInvalid); the processor must be registered on the
// catalog's main (store.ErrNotFound). Jobs live in memory only.
func (s *Service) DryRun(name, image string) (string, error) {
	if !pinnedImageRE.MatchString(image) {
		return "", fmt.Errorf("%w: image %q is not pinned by digest, as in registry.example.org/name@sha256:<64 hex digits>", ErrInvalid, image)
	}
	procs, err := s.Processors()
	if err != nil {
		return "", err
	}
	if !slices.ContainsFunc(procs, func(p ProcessorInfo) bool { return p.Name == name }) {
		return "", fmt.Errorf("processor %q: %w", name, store.ErrNotFound)
	}
	id := s.newID()
	job := &dryRun{report: DryRunReport{State: Running, Samples: []Sample{}}, workspaces: map[string]bool{}}
	s.mu.Lock()
	s.dry[id] = job
	s.busy++
	ctx := s.ctx
	s.mu.Unlock()
	go func() {
		s.dryRun(ctx, job, name, image)
		s.mu.Lock()
		s.busy--
		s.cond.Broadcast()
		s.mu.Unlock()
	}()
	return id, nil
}

// DryRunStatus returns the report of dry run id; store.ErrNotFound when
// there is none (also after a restart).
func (s *Service) DryRunStatus(id string) (DryRunReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.dry[id]
	if !ok {
		return DryRunReport{}, fmt.Errorf("dry run %s: %w", id, store.ErrNotFound)
	}
	r := job.report
	r.Samples = slices.Clone(r.Samples)
	return r, nil
}

func (s *Service) dryRun(ctx context.Context, job *dryRun, name, image string) {
	finish := func(state State, err error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		job.report.State = state
		if err != nil {
			job.report.Error = err.Error()
		}
	}
	ref, digest, err := s.rn.Resolve(ctx, image)
	if err != nil {
		finish(Failed, err)
		return
	}
	ids, err := s.st.WorkspaceIDs()
	if err != nil {
		finish(Failed, err)
		return
	}
	for _, wsID := range ids {
		snap, ok, err := s.load(wsID)
		if err != nil || !ok {
			continue // a workspace that cannot be read has no answers to try
		}
		for _, path := range slices.Sorted(maps.Keys(snap.w.Answers)) {
			if ctx.Err() != nil {
				finish(Failed, ctx.Err())
				return
			}
			a := snap.w.Answers[path]
			b, ok := bindingFor(snap.w, snap.c, a)
			if !ok || b.name != name {
				continue
			}
			ch, _, err := s.process(ctx, wsID, snap, a, b, ref, digest)
			s.mu.Lock()
			job.report.Runs++
			switch {
			case err != nil:
				job.report.Failed++
			case len(ch.Files) > 0:
				job.report.Proposals++
				job.workspaces[wsID] = true
				job.report.Workspaces = len(job.workspaces)
				if len(job.report.Samples) < maxSamples {
					job.report.Samples = append(job.report.Samples, Sample{Workspace: wsID, Task: a.Task, Items: itemsJSON(ch.Items)})
				}
			}
			s.mu.Unlock()
		}
	}
	finish(Succeeded, nil)
}

// ProcessorInfo is one entry of the registry on the catalog's main.
type ProcessorInfo struct {
	Name       string   `json:"name"`
	Image      string   `json:"image"`
	Digest     string   `json:"digest"`
	Timeout    string   `json:"timeout"`
	Network    bool     `json:"network"`
	Secrets    []string `json:"secrets"`
	BoundTasks []string `json:"bound_tasks"`
}

// Processors returns the registry on the catalog's main, sorted by name;
// empty while the catalog has no main.
func (s *Service) Processors() ([]ProcessorInfo, error) {
	cat := s.st.CatalogRepo()
	head, ok, err := cat.ResolveRef(mainRef)
	if err != nil {
		return nil, err
	}
	out := []ProcessorInfo{}
	if !ok {
		return out, nil
	}
	fsys, err := cat.TreeFS(head)
	if err != nil {
		return nil, err
	}
	c, _ := catalog.Load(fsys)
	for _, name := range slices.Sorted(maps.Keys(c.Registry.Processors)) {
		p := c.Registry.Processors[name]
		info := ProcessorInfo{Name: name, Image: p.Image, Timeout: p.Timeout, Network: p.Network,
			Secrets: slices.Clone(p.Secrets), BoundTasks: []string{}}
		if _, d, ok := cutDigest(p.Image); ok {
			info.Digest = d
		}
		if info.Timeout == "" {
			info.Timeout = "60s"
		}
		if info.Secrets == nil {
			info.Secrets = []string{}
		}
		for _, id := range slices.Sorted(maps.Keys(c.Registry.Bindings)) {
			if c.Registry.Bindings[id] == name {
				info.BoundTasks = append(info.BoundTasks, id)
			}
		}
		out = append(out, info)
	}
	return out, nil
}
```

- [ ] **Step 5: Keep dry-run jobs in the service**

In `internal/runs/service.go`, replace

```go
	scans    map[string]bool               // pending scans: workspace id → only ScanAll asked for it
	busy     int                           // scans, runs and dry runs in progress
}

```

with

```go
	scans    map[string]bool               // pending scans: workspace id → only ScanAll asked for it
	busy     int                           // scans, runs and dry runs in progress
	dry      map[string]*dryRun            // dry-run jobs by id
}

```

In `internal/runs/service.go`, replace

```go
		keys:     map[string]bool{},
		scans:    map[string]bool{},
		starting: map[*Record]bool{},
	}
```

with

```go
		keys:     map[string]bool{},
		scans:    map[string]bool{},
		dry:      map[string]*dryRun{},
		starting: map[*Record]bool{},
	}
```

- [ ] **Step 6: Run the tests**

Run: `go vet ./internal/runs/ && go test -race ./internal/runs/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/runs/dryrun.go internal/runs/json.go internal/runs/service.go internal/runs/dryrun_test.go
git commit -m "Dry-run a candidate processor image over all bound answers without writing anything.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```


### Task 6: REST endpoints for processors, runs, output proposals and dry runs

**Files:**
- Create: `internal/runs/api.go` (`Register`)
- Test: `internal/runs/api_test.go`

**Interfaces:**
- Consumes: Tasks 4–5; `api.API.Handle`, `api.Author`, `api.WriteJSON`, `api.WriteError`; `proposal.List`, `proposal.Get`, `proposal.Accept`, `proposal.Reject`, `proposal.ErrInvalid`.
- Produces: `func Register(a *api.API, s *Service)` with the eleven endpoints of the note:

| Method and path | Answer |
| --- | --- |
| `GET /api/processors` | 200 `[ProcessorInfo]` (`[]` while the catalog is empty) |
| `GET /api/workspaces/{id}/runs` | 200 `[Record]`, newest first; 404 unknown workspace |
| `GET /api/workspaces/{id}/runs/{run}` | 200 `Record`; 404 |
| `GET /api/workspaces/{id}/runs/{run}/log` | 200 `text/plain; charset=utf-8`, `nosniff`; 404 |
| `POST /api/workspaces/{id}/runs/{run}/retry` | 401 without author; 202 new `Record`; 404; 409 unless failed |
| `GET /api/workspaces/{id}/processor-proposals` | 200 `[ProposalJSON]` |
| `GET /api/workspaces/{id}/processor-proposals/{task}` | 200 `ProposalJSON`; 404 none open |
| `POST …/processor-proposals/{task}/accept` | body `{"items"?: [...]}` (may be empty); 200 `{"commit", "warning"?}` (`warning` when the commit landed but deleting the branch or opening a cascade failed); 400 unknown selector or bad body; 401; 404; 409; 422 |
| `POST …/processor-proposals/{task}/reject` | 204; 401; 404 |
| `POST /api/processors/{name}/dry-run` | body `{"image"}`; 202 `{"id"}`; 400 not digest-pinned or bad body; 401; 404 unknown processor |
| `GET /api/dry-runs/{id}` | 200 `DryRunReport`; 404 |

- [ ] **Step 1: Write the failing test**

Create `internal/runs/api_test.go`:

```go
package runs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/proctest"
)

const aliceHeader = "Alice Example <alice@example.org>"

func handler(e *env) http.Handler {
	a := api.New(e.st, e.bl)
	Register(a, e.svc)
	return a.Handler()
}

func call(h http.Handler, method, path, author, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if author != "" {
		req.Header.Set("X-Custos-Author", author)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, code int) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("status %d, want %d; body %s", rec.Code, code, rec.Body.String())
	}
}

func TestProcessorsEndpoint(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}, {name: "slow", image: echo, timeout: "5m"}},
		map[string]string{fixture.TaskB: "scan"}), Config{})
	rec := call(handler(e), "GET", "/api/processors", "", "")
	wantStatus(t, rec, http.StatusOK)
	var got []ProcessorInfo
	decode(t, rec, &got)
	if len(got) != 2 || got[0].Name != "scan" || got[0].Digest != digestOf(echo) || got[0].Timeout != "60s" ||
		len(got[0].BoundTasks) != 1 || got[0].BoundTasks[0] != fixture.TaskB || got[1].Timeout != "5m" || len(got[1].BoundTasks) != 0 {
		t.Errorf("processors %+v", got)
	}
	if !strings.Contains(rec.Body.String(), `"secrets":[]`) {
		t.Errorf("secrets must be a list: %s", rec.Body.String())
	}
}

func TestRunEndpoints(t *testing.T) {
	fail := proctest.Image(t, "fail")
	e := newEnv(t, registry([]proc{{name: "p", image: fail}}, map[string]string{fixture.TaskB: "p"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n")})
	e.svc.Wait()
	h := handler(e)

	rec := call(h, "GET", "/api/workspaces/"+ws+"/runs", "", "")
	wantStatus(t, rec, http.StatusOK)
	var list []Record
	decode(t, rec, &list)
	if len(list) != 1 || list[0].State != Failed {
		t.Fatalf("runs %+v", list)
	}
	id := list[0].ID

	rec = call(h, "GET", "/api/workspaces/"+ws+"/runs/"+id, "", "")
	wantStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"answer_path":"`+answerB+`"`) {
		t.Errorf("record %s", rec.Body.String())
	}
	rec = call(h, "GET", "/api/workspaces/"+ws+"/runs/"+id+"/log", "", "")
	wantStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") || !strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("log %q (%s)", rec.Body.String(), ct)
	}

	wantStatus(t, call(h, "GET", "/api/workspaces/"+ws+"/runs/"+noSuchID, "", ""), http.StatusNotFound)
	wantStatus(t, call(h, "GET", "/api/workspaces/"+ws+"/runs/..%2f..%2fx/log", "", ""), http.StatusNotFound)
	wantStatus(t, call(h, "GET", "/api/workspaces/"+noSuchID+"/runs", "", ""), http.StatusNotFound)
	wantStatus(t, call(h, "POST", "/api/workspaces/"+ws+"/runs/"+id+"/retry", "", ""), http.StatusUnauthorized)

	rec = call(h, "POST", "/api/workspaces/"+ws+"/runs/"+id+"/retry", aliceHeader, "")
	wantStatus(t, rec, http.StatusAccepted)
	var retry Record
	decode(t, rec, &retry)
	if retry.RetryOf != id || retry.Reason != ReasonRetry {
		t.Errorf("retry %+v", retry)
	}
	e.svc.Wait()
	e.rebind(t, registry([]proc{{name: "p", image: proctest.Image(t, "echo")}}, map[string]string{fixture.TaskB: "p"}))
	e.svc.Wait()
	rs, _ := e.svc.Runs(ws)
	wantStatus(t, call(h, "POST", "/api/workspaces/"+ws+"/runs/"+rs[0].ID+"/retry", aliceHeader, ""), http.StatusConflict)
}

func TestProcessorProposalEndpoints(t *testing.T) {
	gen := proctest.Image(t, "generate")
	e := newEnv(t, registry([]proc{{name: "gen", image: gen}}, map[string]string{fixture.TaskB: "gen"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("task host Host one\ndoc slsa web-01\n")})
	e.svc.Wait()
	h := handler(e)
	base := "/api/workspaces/" + ws + "/processor-proposals"

	rec := call(h, "GET", base, "", "")
	wantStatus(t, rec, http.StatusOK)
	var list []ProposalJSON
	decode(t, rec, &list)
	if len(list) != 1 || list[0].Task != fixture.TaskB || list[0].Digest != digestOf(gen) || len(list[0].Items) != 2 {
		t.Fatalf("proposals %+v", list)
	}
	if !strings.Contains(rec.Body.String(), `"kind":"document"`) || !strings.Contains(rec.Body.String(), `"match_key":"host"`) {
		t.Errorf("items are not in snake_case JSON: %s", rec.Body.String())
	}
	wantStatus(t, call(h, "GET", base+"/"+fixture.TaskB, "", ""), http.StatusOK)
	wantStatus(t, call(h, "GET", base+"/"+fixture.TaskA, "", ""), http.StatusNotFound)
	wantStatus(t, call(h, "GET", base+"/not-a-task", "", ""), http.StatusNotFound)

	wantStatus(t, call(h, "POST", base+"/"+fixture.TaskB+"/accept", "", ""), http.StatusUnauthorized)
	wantStatus(t, call(h, "POST", base+"/"+fixture.TaskB+"/accept", aliceHeader, `{"items":["task:nope"]}`), http.StatusBadRequest)
	wantStatus(t, call(h, "POST", base+"/"+fixture.TaskB+"/accept", aliceHeader, `{"colour":"red"}`), http.StatusBadRequest)
	rec = call(h, "POST", base+"/"+fixture.TaskB+"/accept", aliceHeader, `{"items":["task:host"]}`)
	wantStatus(t, rec, http.StatusOK)
	var acc map[string]string
	decode(t, rec, &acc)
	repo, _ := e.st.WorkspaceRepo(ws)
	if main, _, _ := repo.ResolveRef("refs/heads/main"); acc["commit"] == "" || acc["commit"] != main {
		t.Errorf("accept %v, main %s", acc, main)
	}
	wantStatus(t, call(h, "POST", base+"/"+fixture.TaskB+"/reject", aliceHeader, ""), http.StatusNotFound)

	// Without a selection, accepting takes everything; reject closes.
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("task host Host one\ndoc slsa web-02\n")})
	e.svc.Wait()
	wantStatus(t, call(h, "POST", base+"/"+fixture.TaskB+"/reject", "", ""), http.StatusUnauthorized)
	wantStatus(t, call(h, "POST", base+"/"+fixture.TaskB+"/reject", aliceHeader, ""), http.StatusNoContent)
	wantStatus(t, call(h, "GET", base+"/"+fixture.TaskB, "", ""), http.StatusNotFound)
}

func TestDryRunEndpoints(t *testing.T) {
	e := dryEnv(t)
	h := handler(e)
	gen := proctest.Image(t, "generate")
	wantStatus(t, call(h, "POST", "/api/processors/scan/dry-run", "", `{"image":"`+gen+`"}`), http.StatusUnauthorized)
	wantStatus(t, call(h, "POST", "/api/processors/scan/dry-run", aliceHeader, `{"image":"custos.test/echo:latest"}`), http.StatusBadRequest)
	wantStatus(t, call(h, "POST", "/api/processors/scan/dry-run", aliceHeader, ``), http.StatusBadRequest)
	wantStatus(t, call(h, "POST", "/api/processors/nope/dry-run", aliceHeader, `{"image":"`+gen+`"}`), http.StatusNotFound)
	rec := call(h, "POST", "/api/processors/scan/dry-run", aliceHeader, `{"image":"`+gen+`"}`)
	wantStatus(t, rec, http.StatusAccepted)
	var started map[string]string
	decode(t, rec, &started)
	e.svc.Wait()
	rec = call(h, "GET", "/api/dry-runs/"+started["id"], "", "")
	wantStatus(t, rec, http.StatusOK)
	var rep struct {
		State      string `json:"state"`
		Runs       int    `json:"runs"`
		Failed     int    `json:"failed"`
		Proposals  int    `json:"proposals"`
		Workspaces int    `json:"workspaces"`
		Samples    []struct {
			Workspace string     `json:"workspace"`
			Task      string     `json:"task"`
			Items     []ItemJSON `json:"items"`
		} `json:"samples"`
	}
	decode(t, rec, &rep)
	if rep.State != "succeeded" || rep.Runs != 2 || rep.Proposals != 2 || rep.Workspaces != 2 || len(rep.Samples) != 2 || len(rep.Samples[0].Items) == 0 {
		t.Errorf("report %s", rec.Body.String())
	}
	wantStatus(t, call(h, "GET", "/api/dry-runs/"+noSuchID, "", ""), http.StatusNotFound)
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/runs/ -run 'Endpoint'`
Expected: FAIL: `undefined: Register`

- [ ] **Step 3: Write the endpoints**

Create `internal/runs/api.go`:

```go
package runs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/proposal"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

// maxBody bounds the JSON bodies of these endpoints.
const maxBody = 64 << 10

// Register adds the processor, run, output proposal and dry-run endpoints
// to a. Call it before a.Handler().
func Register(a *api.API, s *Service) {
	a.Handle("GET /api/processors", func(w http.ResponseWriter, r *http.Request) {
		ps, err := s.Processors()
		if err != nil {
			writeError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, ps)
	})
	a.Handle("GET /api/workspaces/{id}/runs", func(w http.ResponseWriter, r *http.Request) {
		rs, err := s.Runs(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, rs)
	})
	a.Handle("GET /api/workspaces/{id}/runs/{run}", func(w http.ResponseWriter, r *http.Request) {
		rec, _, err := s.Get(r.PathValue("id"), r.PathValue("run"))
		if err != nil {
			writeError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, rec)
	})
	a.Handle("GET /api/workspaces/{id}/runs/{run}/log", func(w http.ResponseWriter, r *http.Request) {
		_, log, err := s.Get(r.PathValue("id"), r.PathValue("run"))
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(log)
	})
	a.Handle("POST /api/workspaces/{id}/runs/{run}/retry", func(w http.ResponseWriter, r *http.Request) {
		if _, err := api.Author(r); err != nil {
			writeError(w, err)
			return
		}
		rec, err := s.Retry(r.PathValue("id"), r.PathValue("run"))
		if err != nil {
			writeError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusAccepted, rec)
	})
	a.Handle("GET /api/workspaces/{id}/processor-proposals", func(w http.ResponseWriter, r *http.Request) {
		ps, err := proposal.List(s.st, r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := make([]ProposalJSON, 0, len(ps))
		for _, p := range ps {
			out = append(out, proposalJSON(p))
		}
		api.WriteJSON(w, http.StatusOK, out)
	})
	a.Handle("GET /api/workspaces/{id}/processor-proposals/{task}", func(w http.ResponseWriter, r *http.Request) {
		tid, err := taskParam(r)
		if err != nil {
			writeError(w, err)
			return
		}
		p, err := proposal.Get(s.st, r.PathValue("id"), tid)
		if err != nil {
			writeError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, proposalJSON(*p))
	})
	a.Handle("POST /api/workspaces/{id}/processor-proposals/{task}/accept", func(w http.ResponseWriter, r *http.Request) {
		author, err := api.Author(r)
		if err != nil {
			writeError(w, err)
			return
		}
		tid, err := taskParam(r)
		if err != nil {
			writeError(w, err)
			return
		}
		var body struct {
			Items []string `json:"items"`
		}
		if err := decodeBody(w, r, &body, true); err != nil {
			writeError(w, err)
			return
		}
		commit, err := proposal.Accept(s.st, r.PathValue("id"), tid, body.Items, author)
		if err != nil && commit == "" {
			writeError(w, err)
			return
		}
		res := map[string]string{"commit": commit}
		if err != nil {
			// The commit landed on main; only deleting the proposal branch or
			// opening a cascade proposal failed. Report it without hiding the commit.
			res["warning"] = err.Error()
		}
		api.WriteJSON(w, http.StatusOK, res)
	})
	a.Handle("POST /api/workspaces/{id}/processor-proposals/{task}/reject", func(w http.ResponseWriter, r *http.Request) {
		if _, err := api.Author(r); err != nil {
			writeError(w, err)
			return
		}
		tid, err := taskParam(r)
		if err != nil {
			writeError(w, err)
			return
		}
		if err := proposal.Reject(s.st, r.PathValue("id"), tid); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	a.Handle("POST /api/processors/{name}/dry-run", func(w http.ResponseWriter, r *http.Request) {
		if _, err := api.Author(r); err != nil {
			writeError(w, err)
			return
		}
		var body struct {
			Image string `json:"image"`
		}
		if err := decodeBody(w, r, &body, false); err != nil {
			writeError(w, err)
			return
		}
		id, err := s.DryRun(r.PathValue("name"), body.Image)
		if err != nil {
			writeError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusAccepted, map[string]string{"id": id})
	})
	a.Handle("GET /api/dry-runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		rep, err := s.DryRunStatus(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, rep)
	})
}

// taskParam returns the path value "task"; anything but a task UUID is an
// unknown proposal.
func taskParam(r *http.Request) (string, error) {
	id := r.PathValue("task")
	if !task.ValidID(id) {
		return "", fmt.Errorf("no open proposal for task %q: %w", id, store.ErrNotFound)
	}
	return id, nil
}

// decodeBody decodes a JSON object into v, rejecting unknown fields and
// trailing data; an empty body is allowed when optional is set. Errors
// wrap ErrInvalid, or are *http.MaxBytesError.
func decodeBody(w http.ResponseWriter, r *http.Request, v any, optional bool) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.Is(err, io.EOF) && optional:
		return nil
	case errors.Is(err, io.EOF):
		return fmt.Errorf("%w: request body is empty; send a JSON object", ErrInvalid)
	case errors.As(err, &tooLarge):
		return err
	case err != nil:
		return fmt.Errorf("%w: request body: %v", ErrInvalid, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: request body: unexpected data after the JSON object", ErrInvalid)
	}
	return nil
}

// writeError answers ErrInvalid and proposal.ErrInvalid with 400 and
// leaves the rest to api.WriteError.
func writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrInvalid) || errors.Is(err, proposal.ErrInvalid) {
		api.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	api.WriteError(w, err)
}
```

- [ ] **Step 4: Run the package tests**

Run: `go vet ./internal/runs/ && go test -race ./internal/runs/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/runs/api.go internal/runs/api_test.go
git commit -m "Serve processors, runs, output proposals and dry runs over the REST API.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```


### Task 7: Merge reruns instead of choosing a side (replaces ruling 2.10)

**Files:**
- Modify: `internal/merge/merge.go` (`Result.Rerun`, `keepMain`, `rerun`, `producer`, doc comment of `Merge`)
- Modify: `internal/merge/http.go` (`Register` gains `onRerun`, `handleMerge` calls it)
- Modify: `cmd/custos/serve.go`, `cmd/custos/distribute_test.go` (pass `nil` as `onRerun` until Task 8 wires it)
- Test: `internal/merge/merge_test.go` (replace `TestMergeGeneratedAndDocumentConflicts`, add two tests), `internal/merge/http_test.go` (call site, new `TestOnRerunCallback`)

**Interfaces:**
- Consumes: `frontmatter.Decode`, `workspace.GeneratedMeta`, `workspace.Document`, `task.ValidID`; merge test helpers `setup`, `diverge`, `file`, `commitFiles`, `call`, `textAnswer`, `ws`, `answerA`, `answerB`, `aliceHeader`, `whatIf`.
- Produces: `merge.Result.Rerun []string` (sorted, nil when none; only set when the merge was made) and `func Register(a *api.API, st *store.Store, onMainMoved func(id string), onRerun func(id string, tasks []string))` — `onRerun` (may be nil) is called after `onMainMoved` for a successful merge with a non-empty `Rerun`. Task 8 passes a function that calls `Service.Rerun(id, tasks, runs.ReasonMerge)`.

Conflicts that git reports on `generated/…` and `documents/…` are no longer returned: the merged tree keeps `main`'s version (or `main`'s deletion), and `produced_by.task.id` of both sides (where readable) goes into `Rerun`. A resolution sent for such a path now hits "has no conflict to resolve" (400, ruling 2.40). The response body of the merge endpoint does not change.

- [ ] **Step 1: Replace the test of ruling 2.10 and add the new tests**

In `internal/merge/merge_test.go`, replace

```go

func TestMergeGeneratedAndDocumentConflicts(t *testing.T) {
	// Ruling 2.10: until phase 3 these are resolved by choosing a side.
	e := setup(t)
	gen := "generated/" + fixture.TaskC + "/1.0.0.md"
```

with

```go

func TestMergeGeneratedAndDocumentConflicts(t *testing.T) {
	// §4.4, replacing ruling 2.10: main's side is kept and the producing
	// task is reported for a rerun.
	e := setup(t)
	gen := "generated/" + fixture.TaskC + "/1.0.0.md"
```

In `internal/merge/merge_test.go`, replace

```go
		map[string]string{gen: genOn("branch"), doc: docOn("branch")})

	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 2 || res.Conflicts[0].Path != doc || res.Conflicts[0].Kind != KindDocument ||
		res.Conflicts[1].Path != gen || res.Conflicts[1].Kind != KindGenerated {
		t.Fatalf("conflicts %+v, want document then generated", res.Conflicts)
	}
	res, err = Merge(e.st, ws, "what-if", alice, map[string]Resolution{gen: {Side: SideTheirs}, doc: {Side: SideOurs}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := file(t, e.st, ws, res.Commit, gen); got != genOn("branch") {
		t.Errorf("generated task = %q, want the branch's", got)
	}
	if got, _ := file(t, e.st, ws, res.Commit, doc); got != docOn("main") {
		t.Errorf("document = %q, want main's", got)
	}
}
```

with

```go
		map[string]string{gen: genOn("branch"), doc: docOn("branch")})

	if _, err := Merge(e.st, ws, "what-if", alice, map[string]Resolution{gen: {Side: SideTheirs}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a resolution for generated output must be refused, got %v", err)
	}
	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 || res.Commit == "" {
		t.Fatalf("result %+v, want a merge without open conflicts", res)
	}
	if len(res.Rerun) != 1 || res.Rerun[0] != fixture.TaskB {
		t.Errorf("rerun %v, want the producing task %s", res.Rerun, fixture.TaskB)
	}
	if got, _ := file(t, e.st, ws, res.Commit, gen); got != genOn("main") {
		t.Errorf("generated task = %q, want main's", got)
	}
	if got, _ := file(t, e.st, ws, res.Commit, doc); got != docOn("main") {
		t.Errorf("document = %q, want main's", got)
	}
}

// TestMergeGeneratedDeletedOnMain keeps main's deletion of a generated
// file the branch changed, and still reruns its producer.
func TestMergeGeneratedDeletedOnMain(t *testing.T) {
	e := setup(t)
	doc := "documents/" + fixture.DocID + ".json"
	diverge(t, e,
		map[string]string{doc: fixture.DocumentFile},
		map[string]string{doc: ""},
		map[string]string{doc: strings.Replace(fixture.DocumentFile, `"name":"provenance"`, `"name":"changed"`, 1)})
	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := file(t, e.st, ws, res.Commit, doc); ok {
		t.Error("main deleted the document; the merge must keep it deleted")
	}
	if len(res.Rerun) != 1 || res.Rerun[0] != fixture.TaskB {
		t.Errorf("rerun %v", res.Rerun)
	}
}

// TestMergeAnswerConflictWithGeneratedOutput reports only the answer as
// a conflict and the rerun only once the merge is made.
func TestMergeAnswerConflictWithGeneratedOutput(t *testing.T) {
	e := setup(t)
	doc := "documents/" + fixture.DocID + ".json"
	docOn := func(side string) string {
		return strings.Replace(fixture.DocumentFile, `"name":"provenance"`, `"name":"provenance-`+side+`"`, 1)
	}
	b := func(v string) string { return textAnswer(fixture.TaskB, "1.0.0", v) }
	diverge(t, e,
		map[string]string{doc: fixture.DocumentFile, answerB: b("base")},
		map[string]string{doc: docOn("main"), answerB: b("main")},
		map[string]string{doc: docOn("branch"), answerB: b("branch")})
	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Path != answerB || res.Rerun != nil || res.Commit != "" {
		t.Fatalf("result %+v, want only the answer conflict", res)
	}
	res, err = Merge(e.st, ws, "what-if", alice, map[string]Resolution{answerB: {Side: SideTheirs}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit == "" || len(res.Rerun) != 1 || res.Rerun[0] != fixture.TaskB {
		t.Errorf("result %+v", res)
	}
}
```

In `internal/merge/http_test.go`, replace

```go
	}
	a := api.New(e.st, bl)
	Register(a, e.st, onMainMoved)
	return a.Handler()
}
```

with

```go
	}
	a := api.New(e.st, bl)
	Register(a, e.st, onMainMoved, nil)
	return a.Handler()
}
```

In `internal/merge/http_test.go`, replace

```go
		t.Errorf("calls = %v, want %v", calls, want)
	}
}
```

with

```go
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

// TestOnRerunCallback checks that a merge that kept main's side of
// conflicting generated output calls onRerun with the producing tasks, and
// that a merge without such conflicts does not.
func TestOnRerunCallback(t *testing.T) {
	e := setup(t)
	doc := "documents/" + fixture.DocID + ".json"
	docOn := func(side string) string {
		return strings.Replace(fixture.DocumentFile, `"name":"provenance"`, `"name":"provenance-`+side+`"`, 1)
	}
	diverge(t, e,
		map[string]string{doc: fixture.DocumentFile},
		map[string]string{doc: docOn("main")},
		map[string]string{doc: docOn("branch")})
	commitFiles(t, e.st, ws, "refs/heads/clean", map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "clean")})

	bl, err := blobs.Open(filepath.Join(e.st.DataDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(e.st, bl)
	var order []string
	var reruns [][]string
	Register(a, e.st, func(id string) { order = append(order, "moved "+id) }, func(id string, tasks []string) {
		order = append(order, "rerun "+id)
		reruns = append(reruns, tasks)
	})
	h := a.Handler()

	rec := call(h, "POST", "/api/workspaces/"+ws+"/merge", aliceHeader, `{"branch":"what-if"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge: %d %s", rec.Code, rec.Body)
	}
	if want := []string{"moved " + ws, "rerun " + ws}; !slices.Equal(order, want) {
		t.Errorf("calls %v, want %v", order, want)
	}
	if len(reruns) != 1 || !slices.Equal(reruns[0], []string{fixture.TaskB}) {
		t.Errorf("reruns %v", reruns)
	}

	rec = call(h, "POST", "/api/workspaces/"+ws+"/merge", aliceHeader, `{"branch":"clean"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clean merge: %d %s", rec.Code, rec.Body)
	}
	if len(reruns) != 1 {
		t.Errorf("a merge without generated conflicts called onRerun: %v", reruns)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/merge/`
Expected: FAIL: `res.Rerun undefined` and `too many arguments in call to Register`

- [ ] **Step 3: Keep `main`'s side of generated output and collect the producers**

In `internal/merge/merge.go`, replace

```go

import (
	"fmt"
	"maps"
```

with

```go

import (
	"encoding/json"
	"fmt"
	"maps"
```

In `internal/merge/merge.go`, replace

```go
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
)

```

with

```go
	"strings"

	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

```

In `internal/merge/merge.go`, replace

```go
	Commit    string     // merge commit on main when merged
	Conflicts []Conflict // non-empty → nothing changed
}

```

with

```go
	Commit    string     // merge commit on main when merged
	Conflicts []Conflict // non-empty → nothing changed
	Rerun     []string   // when merged: tasks whose output conflicted and must be run again, sorted
}

```

In `internal/merge/merge.go`, replace

```go
// authored by author, with main and the branch as parents.
//
// Every path git cannot merge needs a resolution in res; until all have
// one, Merge changes nothing and Result.Conflicts lists the paths still
// without one. A resolution for a path that has no conflict, or a malformed
// one, is ErrInvalid. The merged main is validated like any main update
```

with

```go
// authored by author, with main and the branch as parents.
//
// Conflicting generated tasks and documents are not reported: main's side
// is kept, and Result.Rerun lists the tasks that produced them (on either
// side), whose processors must run again on the merged answer (§4.4).
// Every other path git cannot merge needs a resolution in res; until all
// have one, Merge changes nothing and Result.Conflicts lists the paths still
// without one. A resolution for a path that has no conflict, or a malformed
// one, is ErrInvalid. The merged main is validated like any main update
```

In `internal/merge/merge.go`, replace

```go
		return nil, err
	}
	return &Result{Commit: commit}, nil
}

```

with

```go
		return nil, err
	}
	return &Result{Commit: commit, Rerun: p.rerun()}, nil
}

```

In `internal/merge/merge.go`, replace

```go
	conflicts []Conflict       // paths still without a resolution
	used      map[string]bool  // paths whose resolution was applied
}

```

with

```go
	conflicts []Conflict       // paths still without a resolution
	used      map[string]bool  // paths whose resolution was applied
	producers map[string]bool  // tasks whose generated output conflicted
}

```

In `internal/merge/merge.go`, replace

```go
// could not merge, by its resolution or as an open conflict.
func planMerge(st *store.Store, repo *gitrepo.Repo, base, ours, theirs string, mt *gitrepo.MergeTreeResult, res map[string]Resolution) (*plan, error) {
	p := &plan{used: map[string]bool{}}
	if err := p.config(st, repo, base, ours, theirs, mt, res); err != nil {
		return nil, err
```

with

```go
// could not merge, by its resolution or as an open conflict.
func planMerge(st *store.Store, repo *gitrepo.Repo, base, ours, theirs string, mt *gitrepo.MergeTreeResult, res map[string]Resolution) (*plan, error) {
	p := &plan{used: map[string]bool{}, producers: map[string]bool{}}
	if err := p.config(st, repo, base, ours, theirs, mt, res); err != nil {
		return nil, err
```

In `internal/merge/merge.go`, replace

```go
		if c.Path == configPath {
			continue // settled by p.config
		}
		if err := p.settle(c.Path, c.Ours, c.Theirs, res); err != nil {
```

with

```go
		if c.Path == configPath {
			continue // settled by p.config
		}
		if k := kindOf(c.Path); k == KindGenerated || k == KindDocument {
			p.keepMain(c.Path, c.Ours, c.Theirs)
			continue
		}
		if err := p.settle(c.Path, c.Ours, c.Theirs, res); err != nil {
```

In `internal/merge/merge.go`, replace

```go
	}
	return p, nil
}

```

with

```go
	}
	return p, nil
}

// keepMain settles a conflict on generated output (§4.4, replacing ruling
// 2.10): main's version stays (or the file stays deleted when main deleted
// it), and the tasks that produced either side are rerun after the merge.
func (p *plan) keepMain(path string, ours, theirs []byte) {
	if ours == nil {
		p.changes = append(p.changes, gitrepo.Change{Path: path, Delete: true})
	} else {
		p.changes = append(p.changes, gitrepo.Change{Path: path, Data: ours})
	}
	for _, data := range [][]byte{ours, theirs} {
		if id := producer(path, data); task.ValidID(id) {
			p.producers[id] = true
		}
	}
}

// rerun returns the producing tasks collected by keepMain, sorted.
func (p *plan) rerun() []string {
	if len(p.producers) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(p.producers))
}

// producer returns produced_by.task.id of a generated task or document
// file, or "" when data is nil or cannot be read.
func producer(path string, data []byte) string {
	if data == nil {
		return ""
	}
	if kindOf(path) == KindDocument {
		var d workspace.Document
		if json.Unmarshal(data, &d) != nil {
			return ""
		}
		return d.ProducedBy.Task.ID
	}
	var m workspace.GeneratedMeta
	if _, err := frontmatter.Decode(data, &m); err != nil {
		return ""
	}
	return m.ProducedBy.Task.ID
}

```

- [ ] **Step 4: Add `onRerun` to `Register`**

In `internal/merge/http.go`, replace

```go
// to reconcile the workspace with the catalog right away, since such a
// move does not go through a Git push (ruling 2.20).
func Register(a *api.API, st *store.Store, onMainMoved func(id string)) {
	a.Handle("POST /api/workspaces/{id}/fork", func(w http.ResponseWriter, r *http.Request) { handleFork(st, onMainMoved, w, r) })
	a.Handle("POST /api/workspaces/{id}/merge", func(w http.ResponseWriter, r *http.Request) { handleMerge(st, onMainMoved, w, r) })
	a.Handle("GET /api/workspaces/{id}/branches", func(w http.ResponseWriter, r *http.Request) { handleBranches(st, w, r) })
}
```

with

```go
// to reconcile the workspace with the catalog right away, since such a
// move does not go through a Git push (ruling 2.20).
//
// onRerun, which may be nil, is called after onMainMoved for a merge whose
// Result.Rerun is not empty, with the target workspace id and those task
// ids; the caller reruns their processors on the merged answers (§4.4).
func Register(a *api.API, st *store.Store, onMainMoved func(id string), onRerun func(id string, tasks []string)) {
	a.Handle("POST /api/workspaces/{id}/fork", func(w http.ResponseWriter, r *http.Request) { handleFork(st, onMainMoved, w, r) })
	a.Handle("POST /api/workspaces/{id}/merge", func(w http.ResponseWriter, r *http.Request) { handleMerge(st, onMainMoved, onRerun, w, r) })
	a.Handle("GET /api/workspaces/{id}/branches", func(w http.ResponseWriter, r *http.Request) { handleBranches(st, w, r) })
}
```

In `internal/merge/http.go`, replace

```go
}

func handleMerge(st *store.Store, onMainMoved func(id string), w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
```

with

```go
}

func handleMerge(st *store.Store, onMainMoved func(id string), onRerun func(id string, tasks []string), w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
```

In `internal/merge/http.go`, replace

```go
	if onMainMoved != nil && result.Commit != before {
		onMainMoved(id)
	}
	api.WriteJSON(w, http.StatusOK, map[string]string{"commit": result.Commit})
```

with

```go
	if onMainMoved != nil && result.Commit != before {
		onMainMoved(id)
	}
	if onRerun != nil && len(result.Rerun) > 0 {
		onRerun(id, result.Rerun)
	}
	api.WriteJSON(w, http.StatusOK, map[string]string{"commit": result.Commit})
```

- [ ] **Step 5: Keep `cmd/custos` compiling (Task 8 replaces the `nil` in `serve.go`)**

In `cmd/custos/serve.go`, replace

```go
			fmt.Fprintf(stderr, "custos serve: workspace %s: %v\n", id, err)
		}
	})
	srv := server.New(st)
	startDistribution(st, a, srv, stderr)
```

with

```go
			fmt.Fprintf(stderr, "custos serve: workspace %s: %v\n", id, err)
		}
	}, nil)
	srv := server.New(st)
	startDistribution(st, a, srv, stderr)
```

In `cmd/custos/distribute_test.go`, replace

```go
			fmt.Fprintf(log, "custos serve: workspace %s: %v\n", id, err)
		}
	})
	srv := server.New(st)
	startDistribution(st, a, srv, log)
```

with

```go
			fmt.Fprintf(log, "custos serve: workspace %s: %v\n", id, err)
		}
	}, nil)
	srv := server.New(st)
	startDistribution(st, a, srv, log)
```

- [ ] **Step 6: Run the tests**

Run: `go vet ./... && go test ./internal/merge/ ./cmd/custos/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/merge/merge.go internal/merge/http.go internal/merge/merge_test.go internal/merge/http_test.go cmd/custos/serve.go cmd/custos/distribute_test.go
git commit -m "Keep main's side of conflicting processor output in merges and report the producing tasks for a rerun.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```


### Task 8: Wire runs into `custos serve`

**Files:**
- Create: `cmd/custos/runs.go` (processor flags, `openRuns`, `startRuns`, `rerunAfterMerge`)
- Modify: `cmd/custos/serve.go` (flags, context before `openServer`, `openServer` signature and wiring)
- Modify: `cmd/custos/main.go` (usage), `cmd/custos/api_test.go` (new `openServer` call)
- Test: `cmd/custos/runs_test.go`

**Interfaces:**
- Consumes: Tasks 1–7; `carabiner.New(keysDir string) (*carabiner.Verifier, error)` (3b); `runner.New`, `runner.Config{Runtime, SecretsDir, Memory}` (3a); `blobs.Open`; `server.OnWorkspacePush`; `startDistribution`, `envOr`, `helpOrUsage`, `runCmd`, `lockedBuffer` (existing `cmd/custos` code).
- Produces: `openServer(ctx context.Context, dataDir, publicURL string, procOpts processorOptions, stderr io.Writer) (*server.Server, error)`; `type processorOptions`; `defaultProcessorOptions()`; flags `--container-runtime` (`CUSTOS_CONTAINER_RUNTIME`, `docker`), `--secrets-dir` (`CUSTOS_SECRETS_DIR`, empty), `--processor-memory` (`CUSTOS_PROCESSOR_MEMORY`, `512m`), `--processor-workers` (`CUSTOS_PROCESSOR_WORKERS`, 2), `--max-generation-depth` (`CUSTOS_MAX_GENERATION_DEPTH`, 8), `--trusted-keys` (`CUSTOS_TRUSTED_KEYS`, empty). Invalid values (workers or depth below 1, empty runtime or memory, non-numeric environment values) exit with code 2.

Order in `openServer`: open the store and the API, open the run service, register merge (its `onMainMoved` reconciles **and** scans, because a fork's `main` does not move through the store; its `onRerun` calls `Service.Rerun`), start distribution (which reconciles once), then `startRuns`: register the run endpoints, `st.OnMainMoved(svc.Scan)` (answers, pin moves, accepted proposals, merges), `srv.OnWorkspacePush(svc.Scan)` (pushes), start the workers and call `ScanAll` — so the start-up scan sees the pins distribution just moved. The signal context is created before `openServer`, so shutting down stops the workers; runs it interrupts stay `running` on disk and are queued again at the next start.

- [ ] **Step 1: Write the failing tests**

Create `cmd/custos/runs_test.go`:

```go
package main

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/runs"
	"github.com/emeland-io/custos/internal/store"
)

func TestServeProcessorFlags(t *testing.T) {
	for _, name := range []string{"CUSTOS_CONTAINER_RUNTIME", "CUSTOS_SECRETS_DIR", "CUSTOS_PROCESSOR_MEMORY",
		"CUSTOS_PROCESSOR_WORKERS", "CUSTOS_MAX_GENERATION_DEPTH", "CUSTOS_TRUSTED_KEYS"} {
		t.Setenv(name, "")
	}
	code, _, errs := runCmd(t, "serve", "-h")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{
		"-container-runtime", `(default "docker")`, "-secrets-dir", "-processor-memory", `(default "512m")`,
		"-processor-workers", "(default 2)", "-max-generation-depth", "(default 8)", "-trusted-keys",
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("help lacks %q:\n%s", want, errs)
		}
	}
	for _, args := range [][]string{
		{"--processor-workers", "0"},
		{"--max-generation-depth", "0"},
		{"--container-runtime", ""},
		{"--processor-memory", ""},
	} {
		code, _, errs := runCmd(t, append([]string{"serve", "--data-dir", t.TempDir()}, args...)...)
		if code != 2 || !strings.Contains(errs, args[0]) {
			t.Errorf("%v: code %d err %q", args, code, errs)
		}
	}
	t.Setenv("CUSTOS_PROCESSOR_WORKERS", "many")
	if code, _, errs := runCmd(t, "serve", "--data-dir", t.TempDir()); code != 2 || !strings.Contains(errs, "CUSTOS_PROCESSOR_WORKERS") {
		t.Errorf("bad env: code %d err %q", code, errs)
	}
}

// api calls the server's REST API and decodes the JSON answer into out
// (unless nil); it fails the test on another status than want.
func apiCall(t *testing.T, ts *httptest.Server, method, path, body string, want int, out any) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Custos-Author", "Jane Doe <jane@example.org>")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: %d, want %d: %s", method, path, resp.StatusCode, want, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, data)
		}
	}
}

// waitForRun polls the runs of a workspace until one with the given reason
// has finished, and returns it.
func waitForRun(t *testing.T, ts *httptest.Server, id, reason string) runs.Record {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		var rs []runs.Record
		apiCall(t, ts, "GET", "/api/workspaces/"+id+"/runs", "", http.StatusOK, &rs)
		for _, r := range rs {
			if r.Reason == reason && (r.State == runs.Succeeded || r.State == runs.Failed) {
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no finished %s run; runs %+v", reason, rs)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestProcessorRunsEndToEnd runs the wiring of serve: an answer saved
// through the API runs the bound processor in a real container, the
// proposal is accepted through the API, and a merge whose generated output
// conflicts reruns the processor.
func TestProcessorRunsEndToEnd(t *testing.T) {
	gen := proctest.Image(t, "generate")
	data := t.TempDir()
	cat := fixture.Catalog()
	cat["processors.yaml"] = "processors:\n  scan:\n    image: " + gen + "\nbindings:\n  " + fixture.TaskB + ": scan\n"
	seedCatalogFiles(t, data, cat)

	var log lockedBuffer
	srv, err := openServer(t.Context(), data, "http://127.0.0.1:8080", defaultProcessorOptions(), &log)
	if err != nil {
		t.Fatal(err)
	}
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	var procs []runs.ProcessorInfo
	apiCall(t, ts, "GET", "/api/processors", "", http.StatusOK, &procs)
	if len(procs) != 1 || procs[0].Name != "scan" || procs[0].BoundTasks[0] != fixture.TaskB {
		t.Fatalf("processors %+v", procs)
	}

	ws := fixture.WorkspaceID
	apiCall(t, ts, "POST", "/api/workspaces", `{"id":"`+ws+`"}`, http.StatusCreated, nil)
	apiCall(t, ts, "PUT", "/api/workspaces/"+ws+"/answers/"+fixture.TaskB, `{"value":"task host Host one"}`, http.StatusOK, nil)
	r := waitForRun(t, ts, ws, runs.ReasonAnswer)
	if r.State != runs.Succeeded || r.Outcome != runs.Proposed {
		t.Fatalf("run %+v", r)
	}
	var accepted map[string]string
	apiCall(t, ts, "POST", "/api/workspaces/"+ws+"/processor-proposals/"+fixture.TaskB+"/accept", "", http.StatusOK, &accepted)

	// Change the generated task differently on main and on a branch.
	st := store.New(data, "/custos", "http://127.0.0.1:8080")
	w, _, err := st.Load(ws, "")
	if err != nil {
		t.Fatal(err)
	}
	var path, content string
	repo, err := st.WorkspaceRepo(ws)
	if err != nil {
		t.Fatal(err)
	}
	for p := range w.Generated {
		raw, _, err := repo.ReadFile("refs/heads/main", p)
		if err != nil {
			t.Fatal(err)
		}
		path, content = p, string(raw)
	}
	if path == "" {
		t.Fatal("the accepted proposal added no generated task")
	}
	edit := func(ref, title string) {
		if _, err := st.UpdateWorkspace(ws, ref, gitrepo.Bot, "edit", func(fs.FS) ([]gitrepo.Change, error) {
			return []gitrepo.Change{{Path: path, Data: []byte(strings.Replace(content, "title: Host one", "title: "+title, 1))}}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	edit("refs/heads/what-if", "Host one (branch)")
	edit("refs/heads/main", "Host one (main)")

	apiCall(t, ts, "POST", "/api/workspaces/"+ws+"/merge", `{"branch":"what-if"}`, http.StatusOK, nil)
	r = waitForRun(t, ts, ws, runs.ReasonMerge)
	if r.State != runs.Succeeded || r.Outcome != runs.Proposed {
		t.Errorf("merge rerun %+v", r)
	}
	if s := log.String(); s != "" {
		t.Errorf("unexpected log:\n%s", s)
	}
}

// seedCatalogFiles puts files on main of the data directory's catalog,
// without hooks.
func seedCatalogFiles(t *testing.T, data string, files map[string]string) {
	t.Helper()
	st, err := store.Open(data, "/custos", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	work := gittest.Init(t)
	gittest.Commit(t, work, files)
	gittest.Run(t, work, "push", "--quiet", st.CatalogRepo().Dir, "main")
}
```

In `cmd/custos/api_test.go`, replace

```go
// TestOpenServerServesAPI checks the wiring serve uses.
func TestOpenServerServesAPI(t *testing.T) {
	srv, err := openServer(t.TempDir(), "http://127.0.0.1:8080", io.Discard)
	if err != nil {
		t.Fatal(err)
```

with

```go
// TestOpenServerServesAPI checks the wiring serve uses.
func TestOpenServerServesAPI(t *testing.T) {
	srv, err := openServer(t.Context(), t.TempDir(), "http://127.0.0.1:8080", defaultProcessorOptions(), io.Discard)
	if err != nil {
		t.Fatal(err)
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./cmd/custos/ -run 'ProcessorFlags|ProcessorRunsEndToEnd|OpenServer'`
Expected: FAIL: `undefined: defaultProcessorOptions` / `too many arguments in call to openServer`

- [ ] **Step 3: Write the processor wiring**

Create `cmd/custos/runs.go`:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/attest/carabiner"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/runner"
	"github.com/emeland-io/custos/internal/runs"
	"github.com/emeland-io/custos/internal/server"
	"github.com/emeland-io/custos/internal/store"
)

// processorOptions are the processor flags of serve.
type processorOptions struct {
	runtime     string // docker, podman, or a path to the client binary
	secretsDir  string // "" = no secrets available
	memory      string
	trustedKeys string // "" = no trusted keys
	workers     int
	maxDepth    int
}

// defaultProcessorOptions are the defaults of the processor flags.
func defaultProcessorOptions() processorOptions {
	return processorOptions{runtime: "docker", memory: "512m", workers: 2, maxDepth: 8}
}

// processorFlags holds the processor flags while serve parses them.
type processorFlags struct {
	runtime, secretsDir, memory, trustedKeys *string
	workers, maxDepth                        *int
	envErr                                   error // malformed numbers in the environment
}

// defineProcessorFlags defines the processor flags of serve on fl, with
// defaults taken from the environment.
func defineProcessorFlags(fl *flag.FlagSet) *processorFlags {
	d := defaultProcessorOptions()
	workers, errW := envInt("CUSTOS_PROCESSOR_WORKERS", d.workers)
	depth, errD := envInt("CUSTOS_MAX_GENERATION_DEPTH", d.maxDepth)
	return &processorFlags{
		runtime:     fl.String("container-runtime", envOr("CUSTOS_CONTAINER_RUNTIME", d.runtime), "docker, podman, or the path of a compatible client that runs processors (env CUSTOS_CONTAINER_RUNTIME)"),
		secretsDir:  fl.String("secrets-dir", os.Getenv("CUSTOS_SECRETS_DIR"), "directory whose file <name> is mounted as processor secret <name>; empty: no secrets (env CUSTOS_SECRETS_DIR)"),
		memory:      fl.String("processor-memory", envOr("CUSTOS_PROCESSOR_MEMORY", d.memory), "memory limit of a processor container (env CUSTOS_PROCESSOR_MEMORY)"),
		trustedKeys: fl.String("trusted-keys", os.Getenv("CUSTOS_TRUSTED_KEYS"), "directory of public keys (*.pem, *.pub) that verify signed documents; empty: none (env CUSTOS_TRUSTED_KEYS)"),
		workers:     fl.Int("processor-workers", workers, "processor runs executed at the same time (env CUSTOS_PROCESSOR_WORKERS)"),
		maxDepth:    fl.Int("max-generation-depth", depth, "levels of generated tasks allowed below a catalog task (env CUSTOS_MAX_GENERATION_DEPTH)"),
		envErr:      errors.Join(errW, errD),
	}
}

// options checks the parsed flags.
func (f *processorFlags) options() (processorOptions, error) {
	o := processorOptions{runtime: *f.runtime, secretsDir: *f.secretsDir, memory: *f.memory,
		trustedKeys: *f.trustedKeys, workers: *f.workers, maxDepth: *f.maxDepth}
	switch {
	case f.envErr != nil:
		return o, f.envErr
	case o.runtime == "":
		return o, errors.New("--container-runtime must not be empty")
	case o.memory == "":
		return o, errors.New("--processor-memory must not be empty")
	case o.workers < 1:
		return o, fmt.Errorf("--processor-workers must be at least 1, got %d", o.workers)
	case o.maxDepth < 1:
		return o, fmt.Errorf("--max-generation-depth must be at least 1, got %d", o.maxDepth)
	}
	return o, nil
}

// envInt reads an integer from the environment; unset or empty gives
// fallback.
func envInt(name string, fallback int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback, fmt.Errorf("%s=%q is not a whole number", name, v)
	}
	return n, nil
}

// openRuns returns the run service of st, with attachments from
// <data-dir>/blobs, the container runtime and limits of o, and documents
// verified against the keys in o.trustedKeys.
func openRuns(st *store.Store, o processorOptions) (*runs.Service, error) {
	bl, err := blobs.Open(filepath.Join(st.DataDir(), "blobs"))
	if err != nil {
		return nil, err
	}
	v, err := carabiner.New(o.trustedKeys)
	if err != nil {
		return nil, fmt.Errorf("--trusted-keys: %w", err)
	}
	rn := runner.New(runner.Config{Runtime: o.runtime, SecretsDir: o.secretsDir, Memory: o.memory})
	return runs.New(st, bl, rn, v, runs.Config{Workers: o.workers, MaxDepth: o.maxDepth})
}

// startRuns adds the run endpoints to a, scans a workspace for answers to
// run after every move of its main through the store (answers, pin moves,
// accepted proposals, merges) and after every push to it, starts the
// workers, and scans every workspace once to catch up with changes made
// while the server was down. Call it after startDistribution, so the
// start-up scan sees the pins distribution moved.
func startRuns(ctx context.Context, st *store.Store, a *api.API, srv *server.Server, svc *runs.Service) {
	runs.Register(a, svc)
	st.OnMainMoved(svc.Scan)
	srv.OnWorkspacePush(svc.Scan)
	svc.Start(ctx)
	svc.ScanAll()
}

// rerunAfterMerge is merge.Register's onRerun: it queues forced runs of the
// tasks whose generated output conflicted in a merge (§4.4).
func rerunAfterMerge(svc *runs.Service, stderr io.Writer) func(id string, tasks []string) {
	return func(id string, tasks []string) {
		if _, err := svc.Rerun(id, tasks, runs.ReasonMerge); err != nil {
			fmt.Fprintf(stderr, "custos serve: workspace %s: rerunning processors after a merge: %v\n", id, err)
		}
	}
}
```

- [ ] **Step 4: Parse the flags and wire the service in `serve`**

In `cmd/custos/serve.go`, replace

```go
	addr := fl.String("addr", envOr("CUSTOS_ADDR", defaultAddr), "listen address (env CUSTOS_ADDR); there is no authentication yet, so keep it on loopback unless the network is trusted")
	publicURL := publicURLFlag(fl)
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	if *dataDir == "" {
```

with

```go
	addr := fl.String("addr", envOr("CUSTOS_ADDR", defaultAddr), "listen address (env CUSTOS_ADDR); there is no authentication yet, so keep it on loopback unless the network is trusted")
	publicURL := publicURLFlag(fl)
	procFlags := defineProcessorFlags(fl)
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	procOpts, err := procFlags.options()
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 2
	}
	if *dataDir == "" {
```

In `cmd/custos/serve.go`, replace

```go
		return 2
	}
	srv, err := openServer(*dataDir, *publicURL, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
```

with

```go
		return 2
	}
	// The context also stops the processor workers; runs it interrupts are
	// queued again at the next start.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv, err := openServer(ctx, *dataDir, *publicURL, procOpts, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
```

In `cmd/custos/serve.go`, replace

```go
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Bind before announcing, so "listening" is only printed when it is true.
	ln, err := net.Listen("tcp", *addr)
```

with

```go
		return 1
	}
	// Bind before announcing, so "listening" is only printed when it is true.
	ln, err := net.Listen("tcp", *addr)
```

In `cmd/custos/serve.go`, replace

```go
// openServer opens the data directory, points all hooks at this binary and
// reports workspaces whose main breaks the rules, for example after an edit
// on disk (spec section 7); it serves them anyway.
func openServer(dataDir, publicURL string, stderr io.Writer) (*server.Server, error) {
	exe, err := os.Executable()
	if err != nil {
```

with

```go
// openServer opens the data directory, points all hooks at this binary and
// reports workspaces whose main breaks the rules, for example after an edit
// on disk (spec section 7); it serves them anyway. It starts the processor
// workers, which stop when ctx is cancelled.
func openServer(ctx context.Context, dataDir, publicURL string, procOpts processorOptions, stderr io.Writer) (*server.Server, error) {
	exe, err := os.Executable()
	if err != nil {
```

In `cmd/custos/serve.go`, replace

```go
		return nil, err
	}
	merge.Register(a, st, func(id string) {
		if err := distribute.ReconcileWorkspace(st, id); err != nil {
			fmt.Fprintf(stderr, "custos serve: workspace %s: %v\n", id, err)
		}
	}, nil)
	srv := server.New(st)
	startDistribution(st, a, srv, stderr)
	srv.WithAPI(a.Handler())
	return srv, nil
```

with

```go
		return nil, err
	}
	svc, err := openRuns(st, procOpts)
	if err != nil {
		return nil, err
	}
	merge.Register(a, st, func(id string) {
		if err := distribute.ReconcileWorkspace(st, id); err != nil {
			fmt.Fprintf(stderr, "custos serve: workspace %s: %v\n", id, err)
		}
		svc.Scan(id) // a fork's main does not move through the store
	}, rerunAfterMerge(svc, stderr))
	srv := server.New(st)
	startDistribution(st, a, srv, stderr)
	startRuns(ctx, st, a, srv, svc)
	srv.WithAPI(a.Handler())
	return srv, nil
```

- [ ] **Step 5: Document the flags in the usage text**

In `cmd/custos/main.go`, replace

```go

Usage:
  custos serve [--data-dir DIR] [--addr ADDR] [--public-url URL]
  custos validate [--against REV] [DIR]
  custos task new-version (--patch | --minor | --major) [--dir DIR] TASK-UUID
```

with

```go

Usage:
  custos serve [--data-dir DIR] [--addr ADDR] [--public-url URL] [--container-runtime CMD]
               [--secrets-dir DIR] [--processor-memory SIZE] [--processor-workers N]
               [--max-generation-depth N] [--trusted-keys DIR]
  custos validate [--against REV] [DIR]
  custos task new-version (--patch | --minor | --major) [--dir DIR] TASK-UUID
```

- [ ] **Step 6: Run all tests**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: no files listed by gofmt; PASS

- [ ] **Step 7: Commit**

```bash
git add cmd/custos/runs.go cmd/custos/runs_test.go cmd/custos/serve.go cmd/custos/main.go cmd/custos/api_test.go
git commit -m "Run processors from custos serve, with flags for the container runtime, secrets, limits and trusted keys.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```


## Self-Review Notes

- **Spec coverage:**
  - §4.3 triggers: answer saved (API → `store.OnMainMoved`; push → `server.OnWorkspacePush`) and accepted (proposal accept moves `main` through the store); digest change and binding change (distribution moves the pin through the store → scan; the key holds the digest; reasons `digest`/`binding`); retry (`Retry`, endpoint). Tasks 1, 4, 6, 8.
  - §4.3 runs, output to `custos/proposal/<task>/<digest>`, no proposal when unchanged, newer replaces older (`proposal.Write`, 3c), review/accept/reject (Task 6).
  - §4.4 rerun on merge, replacing ruling 2.10 — Task 7 (merge) and Task 8 (wiring, end-to-end test).
  - §5.2 running (read-only, timeout, memory, network, secrets, attachments) — `runner` (3a) with the job built in Task 4; flags in Task 8.
  - §5.3 failed runs (exit, timeout, invalid output) kept with logs and shown — Task 4 (`TestFailedRuns`), Task 6 (log endpoint).
  - §5.5 generated tasks with a processor run when answered; depth limit fails the run — Task 4 (`TestGeneratedTaskWithProcessor`, with `--max-generation-depth` passed through `Config.MaxDepth`).
  - §5.6 verification with trusted keys — `--trusted-keys` → `carabiner.New` → `match.Run.Verifier` (Tasks 4, 8); a real signed run in `TestSignedDocumentIsVerified` (Task 4).
  - §5.7 dry run — Task 5 and endpoints in Task 6.
  - §6.1 Processor-Author: registry list, run history and logs, dry run — Task 6 (UI is phase 4).
  - §7 failed runs stored with logs and retried; an unavailable runtime does not stop `serve` — Tasks 4, 6, 8.
- **What was verified:** every code block of this plan was applied, task by task, to a fresh clone of `main` plus minimal stand-ins of `contract`, `runner`, `proctest` (echo, generate, fail, loop images), `attest`, `attest/carabiner`, `match` and `proposal` written to the architecture note's signatures; after each task `go build ./...`, `go vet ./...` and the task's tests passed with real Docker 27.4 (Go 1.27.1 toolchain, module directive 1.26), and `go test -race ./...` passed at the end. The stand-ins are simplified (no signatures, no cascades, items recomputed by a plain diff), so the behaviour of the real 3a–3c packages is not exercised; the tests here only rely on behaviour the note fixes (echo's `input` document; generate's `task`, `processor`, `doc`, `stderr`, `garbage` directives; fail's exit 3 and `boom`; loop never ending; `ErrTooDeep`'s message containing "depth"; `Write` returning "" when nothing changed).
- **Placeholder scan:** none; every step has complete code or an exact replacement.
- **Set review (2026-10-04):** plans 3a–3e were applied in order, as written, to one clone of `main`; `gofmt -l .`, `go vet ./...` and `go test ./...` passed after each plan, `make test` after 3e, and `go test -race` on runner, runs, store, proposal, merge and cmd/custos at the end (Docker 27.4, Go 1.27.1, Python 3.14). The set review added `internal/runs/sign_test.go` (signed documents through a real run) and the `warning` of the accept endpoint.
- **Type consistency:** `Record`, `State`, `Outcome`, `Config`, `New`, `Start`, `Scan`, `ScanAll`, `Rerun`, `Retry`, `Runs`, `Get`, `Wait`, `Register` match the note; `binding`, `snapshot`, `process`, `prepare`, `queuedFor`, `starting`, `snapMu` are defined once (Task 4) and used with the same signatures in Tasks 5–6.
- **Review Focus:** each of the five items has its test in the owning task.

## Decisions beyond the architecture note

- `store.OnMainMoved` callbacks run synchronously after the lock is released, and not for no-op, failed or other-branch writes — they may write to the store themselves; `Scan` is non-blocking — a slow callback would delay the writer.
- `CreateWorkspace` fires `OnMainMoved`; `merge.Fork` does not move `main` through the store, so `serve`'s merge `onMainMoved` also calls `Scan` — a forked workspace is scanned at once, so its bound answers run once more (the key includes the workspace id), usually with unchanged outcome — the alternative is to scan the fork later at an arbitrary time — one run per bound answer per fork.
- New helpers `gitrepo.(*Repo).BlobIDs` and `blobs.(*Store).Path` — answer blob oids for keys in one `ls-tree`; mounting needs a host path — two small exported functions.
- Reason constants `ReasonAnswer` … `ReasonStartUp` exported; classification from the newest earlier record of the answer, other blob → `answer`, other processor → `binding`, other digest → `digest`; without an earlier record `answer`, or `start-up` when the start-up scan (`ScanAll`) found it — scans keep no event history (note ruling 3) — reasons are approximate after deleted records; after deleting the run records every answer shows `start-up` once.
- A queued run reads `main` when it starts and updates its record (blob, commit, processor, image, digest, key); a scan skips answers with a queued or starting run; `snapMu` orders scans and run starts — answers saved in quick succession cause one run, not one per save, and no duplicate keys — a run's record shows the answer it ran, not the one that triggered it.
- A run whose answer disappeared or lost its binding before it started fails with that message — the record must end in a final state — such runs show as failed although nothing went wrong.
- Shutdown (context cancelled) leaves running records `running`; `New` re-queues `queued` and `running` records oldest first, keeping their reason — an interrupted run must not be lost — a run that crashed the container runtime is retried at every start.
- Damaged record files are skipped at start-up — a corrupt file must not stop `serve` — that answer may run once more.
- `Retry` creates a new record with `retry_of` and reason `retry` and runs the answer as it is on `main` then — the note allows retry for failed runs only (409 otherwise) — a retry after the answer changed runs the new answer.
- `Rerun` returns the existing queued record of an answer instead of queuing a second one and skips tasks without answer or processor — merges and scans can trigger the same answer at once — a forced rerun can be absorbed by a queued non-forced run (same effect).
- Attachments missing from the blob store fail the run with a message naming the attachment; invalid registry timeouts fail the run (the catalog validation rejects them anyway) — a processor must not see an empty mount (§7: the attachment is unavailable) — the engineer must re-upload the attachment and retry.
- Failure messages: `processor <name> exited with code <n>`, `processor <name> timed out after <d>`, `processor <name> wrote invalid output: …`, `writing the proposal: …`, and the runner's/matcher's errors as they are — the record's `error` is shown to engineers — wording is not a stable API.
- The log file holds the processor's stderr only (empty when it never ran) — `error` carries custos's own message — two places to look.
- Dry runs: one goroutine per job, one container at a time, each workspace's pinned bindings and registry settings, outputs compared with `len(Changes.Files) > 0`, samples carry `ItemJSON` items, state `failed` with `error` only when the image cannot be resolved or the server stops; nothing is written; `POST …/dry-run` requires `X-Custos-Author` like every POST; the report gains `error` — §5.7 asks for the effect of the new digest on what would actually rerun — a big dry run is slow and cannot be cancelled except by a restart.
- `Processors()` lists the registry at the catalog's `main` with `timeout` defaulting to `"60s"`, `secrets` and `bound_tasks` as `[]` when empty, sorted — JSON lists are never null in this API — none known.
- REST shapes `ItemJSON` (`kind, match_key, action, id, from?, to?, title, verification?`) and `ProposalJSON` (`branch, commit, task, digest?, cascade, items`) are defined in `runs`, since 3c's types have no JSON tags — one mapping to keep in sync with `match.Item`.
- `ErrInvalid` of `runs` (dry-run image, malformed bodies) and `proposal.ErrInvalid` (unknown selector) both answer 400; bodies of these endpoints are limited to 64 KiB with unknown fields rejected — consistent with 2b — clients sending extra fields get 400.
- `POST …/accept` answers 200 `{"commit", "warning"}` when `proposal.Accept` returns a commit together with an error (the commit landed; deleting the branch or opening a cascade failed) — the change was made and must not be reported as failed or lose its commit — clients that ignore `warning` miss a proposal branch left open (removed by the next write or reject of that task) or a cascade proposal that was not opened (the tasks below stay until removed by hand).
- `GET …/runs/{run}/log` answers `text/plain; charset=utf-8` with `nosniff` — logs are untrusted processor output — none known.
- `merge.Merge` sets `Result.Rerun` only when the merge was made, and a resolution sent for a generated or document path is now rejected as "no conflict" (ruling 2.40) — main's side is final — clients that sent such resolutions get 400.
- `merge.Register`'s `onRerun` runs after `onMainMoved`; the merge response body is unchanged — clients do not need the rerun list (it is visible in the run records) — none known.
- `serve` creates its signal context before `openServer` and passes it to the workers; processor flags are validated before the data directory is opened (exit 2) — consistent with `--public-url` — none known.
- `openRuns` opens the blob store a second time (`openAPI` keeps its own) — avoids changing `openAPI`'s signature — two `*blobs.Store` values over one directory, which is safe (content-addressed, no locks).
- A run's key holds the processor digest, not its name: rebinding an answer to another processor name whose image already ran on the same answer does not run it again; a forced rerun (merge) or a retry still does — the note fixes the key (workspace, answer path, answer blob, digest), and the output of the same image on the same answer differs only in `produced_by.processor` — a rebinding to an identical image keeps the old `produced_by.processor` until the answer changes.
- A generated task is bound by the `processor` field of the version its answer names (`task_version`), looked up in the registry of the workspace's pin — the answer was written for that version — a new version that changes `processor` takes effect when the answer is updated.
- This plan does not edit the spec: the controller of phase 3 records these decisions in §11.3 and changes the "Cost if wrong" cell of ruling 2.10 to say it was replaced in phase 3 — five plans editing one table would conflict — none.
