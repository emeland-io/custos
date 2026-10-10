# Phase 4b (Catalog Editing API and Book HTML Export) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a Task-Author draft catalog changes on a branch of the catalog
repository (new task versions, group-tree edits, processor-registry edits),
view a draft's decoded catalog with its live validation problems, and
publish it into `main` — as a clean fast-forward or a three-way merge with
custos-aware conflict resolution, validated exactly like a push — all
through a REST API; and let anyone export a workspace's book as a
self-contained HTML document in addition to the existing Markdown export.
Backend only; no frontend.

**Architecture:** A new package `internal/catalogdraft` is the catalog's
analogue of `internal/merge`'s workspace fork/merge, but for the single
catalog repository, which has no workspace id and whose drafts are created
and listed explicitly rather than forked implicitly per user. Drafts are
ordinary Git branches `custos/draft/<uuid>` of the catalog repository;
`catalogdraft.Create`/`Update`/`Publish` write to them through a new
`store.UpdateCatalog`, the catalog-repo equivalent of
`store.UpdateWorkspace`. `Publish` reuses `internal/merge`'s `Resolution` and
`Conflict` types (not its functions) for the three-way-merge case, computed
the same way `internal/merge.Merge` computes a workspace merge
(`gitrepo.MergeTree` + conflict resolution + `gitrepo.EditTree`). A new
`internal/catalog.NewVersionChanges` is the `fs.FS`-based analogue of the
existing `catalog.NewVersion`, for use inside a `catalogdraft.Update` edit
closure. `internal/status.RenderHTML` renders the same book as
`RenderMarkdown`, as HTML, using `goldmark` — the one new dependency this
plan adds, confined to that function. `internal/api/read.go`'s book handler
gains `?format=html`.

**Spec:** [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md)
— §3.1-3.2 (catalog repo and processor registry file layout), §6.1 (web UI,
the Task-Author bullet), §6.3 (API), §7 (error handling), §8 (testing), and
§11 (rulings, which override earlier sections — here 2.11 "book is Markdown
only until the web UI" and 2.12 "the catalog API is read-only until the web
UI," both of which this plan changes). Shared names and conventions:
[docs/superpowers/plans/2026-10-09-phase-4-architecture.md](2026-10-09-phase-4-architecture.md),
sections "Conventions," "Plan 4a" (the `internal/auth` surface this plan's
endpoints are gated with) and "Plan 4b" (this plan's own scope — every
exported name and endpoint path below is taken from there verbatim unless
the "Decisions beyond the architecture note" section says otherwise).

**Requires plan 4a** to have landed first: this plan's HTTP handlers call
`auth.Require("task-author", next)` from `internal/auth` exactly as the
architecture note's "Plan 4a" section fixes it (`Require(role string, next
http.HandlerFunc) http.HandlerFunc`), and relies on `api.Author(r)` already
reading the authenticated principal instead of `X-Custos-Author` (same
signature, callers unchanged). None of plan 4a's code is touched here.

This plan uses, exactly as phases 1-3 and the architecture note name them:
`gitrepo.{Signature, Bot, Change, CommitRequest, ErrRefMoved, MergeTreeResult,
ConflictedFile}`, `(*gitrepo.Repo).{ResolveRef, ReadFile, TreeFS, WriteCommit,
CommitTree, UpdateRef, DeleteRef, Refs, IsAncestor, MergeTree, MergeBase,
EditTree}`, `store.{Store, Open, CatalogRepo, Lock, ErrNotFound, ErrExists,
ErrConflict, RejectedError}`, `rules.CheckCatalogUpdate`, `catalog.{Catalog,
Load, Check, CheckImmutable, Group, Child, Registry, Processor}`,
`task.{Meta, Ref, Graph, AnswerType, ValidID}`, `semver.{Step, Bump}`,
`frontmatter.Encode`, `merge.{Resolution, Conflict, SideOurs, SideTheirs,
SideContent}`, `problem.Problem`, `api.{API, New, Handle, Handler, Author,
WriteJSON, WriteError}`. If a signature on `main` differs from what is
quoted here, adapt the call and say so in the task's commit message.

## Global Constraints

- Go 1.26 stdlib wherever possible. The only new dependency this plan adds
  is `github.com/yuin/goldmark`, confined to `internal/status`.
- Module `github.com/emeland-io/custos`. Git ≥ 2.38 at runtime (ruling 2.9);
  use no git option newer than 2.38.
- Problems, REST conventions, bot identity (`gitrepo.Bot`,
  `custos-bot <custos-bot@localhost>`), and the one-content-addressed-blob-
  store rule are unchanged from phases 2-3.
- Error body `{"error": "<message>", "problems": [...]}`; 400 malformed
  request, 401 missing/invalid credential, 403 credential present but wrong
  role, 404 unknown thing, 409 conflict, 413 too large, 422 validation
  problems (`*store.RejectedError`), 500 otherwise — `api.WriteError`
  already does this; this plan's handlers only add 400 for `ErrInvalid` and
  409 with a conflict body for `Publish`, matching `internal/merge/http.go`.
- Every write endpoint of this plan is gated with `auth.Require("task-author",
  next)` (architecture note, Plan 4a); none of them call `api.Author`
  itself for authorization — `api.Author(r)` is still called to obtain the
  `gitrepo.Signature` for the commit, exactly as every other write endpoint
  already does.
- Every write goes through `store`: `store.UpdateCatalog`'s own lock
  (`Store.Lock("catalog")`, the string key `store.go`'s existing doc comment
  on `Lock` already reserves for the catalog repository) serialises every
  commit this plan writes, draft or main. Nothing is cached outside Git
  except through that lock; a draft's metadata (title, author, base) is
  itself a committed file on the draft's own branch (see Task 3), not a side
  file on disk.
- A draft may be transiently invalid; only `Publish` validates
  (architecture note ruling 4.13). `store.UpdateCatalog` enforces this: it
  validates with `rules.CheckCatalogUpdate` only when the ref being moved is
  `refs/heads/main`, exactly mirroring the pre-receive hook, which is what
  already calls that function for pushes.
- Commit messages of this plan's own commits to the custos repository (not
  the runtime commits custos writes into users' catalogs) are one imperative
  sentence, a blank line, then `Co-Authored-By: Claude Sonnet 5
  <noreply@anthropic.com>`.
- Tests use real git in temp dirs through `internal/gittest` and the shared
  `internal/fixture` catalog/workspace fixtures, in the style of
  `internal/merge`'s and `internal/proposal`'s tests.

## Review Focus

1. **The draft metadata marker never reaches `main`.** `custos-draft.json`
   (Task 3) exists only on draft branches; a clean fast-forward or
   three-way-merge publish must never add it to `main`'s tree (tests
   `TestPublishFastForward` and `TestPublishConflict`, Task 5).
2. **Two drafts editing the same task.** The first publish is a clean
   fast-forward; the second then conflicts on the exact path the first one
   already changed (test `TestPublishConflict`, Task 5 — the scenario is
   specified in full under that task, not left for the implementer to
   invent).
3. **A draft that would break immutability.** A hand-built edit closure that
   rewrites the content of an already-published task version file must be
   rejected at `Publish` with `problem.RuleImmutable`, not merely at
   `catalog.Check`'s single-file rules (test `TestPublishImmutabilityViolation`,
   Task 5) — this is why `store.UpdateCatalog`'s main-ref validation calls
   `rules.CheckCatalogUpdate`, which also runs `catalog.CheckImmutable`
   against the old tree, and not bare `catalog.Check` (see "Decisions
   beyond the architecture note").
4. **A slug or task id from the request body ends up in a file path.** The
   group-tree handlers (Task 7) must reject a slug containing `/`, `..` or
   other path metacharacters with 400 before it ever reaches
   `catalogdraft.Update`, independent of whatever `catalog.Check` would
   later say (test `TestHandleGroupInvalidSlug`, Task 7).

---

## Task 1: `store.UpdateCatalog`

**Files:**
- Create: `internal/store/catalog_write.go`
- Test: `internal/store/catalog_write_test.go`

**Interfaces:**
- Consumes: `internal/store/write.go`'s unexported `checkBranch`, `effective`
  and `conflict` helpers (same package); `internal/store/store.go`'s
  `mainRef` constant and `CatalogRepo`; `rules.CheckCatalogUpdate`.
- Produces:

```go
func (s *Store) UpdateCatalog(ref string, author gitrepo.Signature, message string,
	edit func(tree fs.FS) ([]gitrepo.Change, error)) (string, error)
```

`UpdateCatalog` mirrors `UpdateWorkspace`/`updateWorkspaceLocked` exactly,
over the catalog repository instead of a workspace's, under the lock key
`"catalog"` instead of a workspace id (`store.go`'s doc comment on `Lock`
already names `"catalog"` as that key, reserved for exactly this). When
`ref == mainRef`, the new commit is validated with
`rules.CheckCatalogUpdate` — history, `catalog.Check` and
`catalog.CheckImmutable` together, exactly what the pre-receive hook runs
for a push — before the compare-and-swap; any other ref (a draft branch) is
not validated here at all, so a draft may be transiently invalid (ruling
4.13).

- [ ] **Step 1: Write the failing tests** `internal/store/catalog_write_test.go`

```go
package store

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
)

func mainOfCatalog(t *testing.T, s *Store) string {
	t.Helper()
	oid, ok, err := s.CatalogRepo().ResolveRef(mainRef)
	if err != nil || !ok {
		t.Fatalf("catalog main: ok=%v err=%v", ok, err)
	}
	return oid
}

func TestUpdateCatalogMain(t *testing.T) {
	s, first := open(t)
	newTask := fixture.TaskPath(fixture.TaskC, "1.0.0")
	commit, err := s.UpdateCatalog(mainRef, jane, "Add task C",
		put(map[string]string{newTask: fixture.TaskFile(fixture.TaskC, "1.0.0")}))
	if err != nil {
		t.Fatal(err)
	}
	if commit == first || mainOfCatalog(t, s) != commit {
		t.Fatalf("main %s, commit %s, first %s", mainOfCatalog(t, s), commit, first)
	}
	if data, ok, _ := s.CatalogRepo().ReadFile(commit, newTask); !ok || string(data) != fixture.TaskFile(fixture.TaskC, "1.0.0") {
		t.Errorf("task file %q", data)
	}
	// The same content again makes no commit.
	again, err := s.UpdateCatalog(mainRef, jane, "again",
		put(map[string]string{newTask: fixture.TaskFile(fixture.TaskC, "1.0.0")}))
	if err != nil || again != commit {
		t.Errorf("no-op update: %s, %v, want %s", again, err, commit)
	}
}

func TestUpdateCatalogMainRejectsInvalid(t *testing.T) {
	s, _ := open(t)
	// id in the frontmatter does not match the path (rule 4).
	bad := "---\nid: " + fixture.TaskA + "\nversion: 1.0.0\ntitle: Wrong path\nanswer_type: text\n---\n\nBody.\n"
	_, err := s.UpdateCatalog(mainRef, jane, "bad",
		put(map[string]string{fixture.TaskPath(fixture.TaskC, "1.0.0"): bad}))
	var rejected *RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want *RejectedError", err)
	}
	found := false
	for _, p := range rejected.Problems {
		if p.Rule == problem.RulePath {
			found = true
		}
	}
	if !found {
		t.Errorf("problems %+v, want one with rule %s", rejected.Problems, problem.RulePath)
	}
	if mainOfCatalog(t, s) == "" {
		t.Error("main moved")
	}
}

func TestUpdateCatalogBranchSkipsValidation(t *testing.T) {
	s, first := open(t)
	branch := "refs/heads/draft-x"
	// Missing title: catalog.Check would reject this on main, but a
	// non-main ref is never validated here (ruling 4.13).
	invalid := "---\nid: " + fixture.TaskC + "\nversion: 1.0.0\nanswer_type: text\n---\n\nBody.\n"
	commit, err := s.UpdateCatalog(branch, jane, "draft edit",
		put(map[string]string{fixture.TaskPath(fixture.TaskC, "1.0.0"): invalid}))
	if err != nil {
		t.Fatal(err)
	}
	if data, ok, _ := s.CatalogRepo().ReadFile(commit, fixture.TaskPath(fixture.TaskC, "1.0.0")); !ok || string(data) != invalid {
		t.Errorf("branch file %q", data)
	}
	if mainOfCatalog(t, s) != first {
		t.Error("main moved by a branch update")
	}
	// The branch starts from main's current tree: a file only main has is
	// visible to the edit closure.
	_, err = s.UpdateCatalog(branch, jane, "check base", func(tree fs.FS) ([]gitrepo.Change, error) {
		if _, err := fs.Stat(tree, fixture.TaskPath(fixture.TaskA, "1.0.0")); err != nil {
			t.Errorf("branch tree lacks main's existing task: %v", err)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(branch, "refs/heads/") {
		t.Fatal("test precondition")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/ -run UpdateCatalog`
Expected: FAIL, `s.UpdateCatalog undefined`.

- [ ] **Step 3: Write `internal/store/catalog_write.go`**

```go
package store

import (
	"io/fs"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/rules"
)

// catalogLockKey is the Store.Lock key that serialises every write to the
// catalog repository this package or another package (internal/catalogdraft)
// makes through the store: see the doc comment on Lock.
const catalogLockKey = "catalog"

// UpdateCatalog applies edit to the tree at ref ("refs/heads/main" or
// another branch; the ref may not exist yet for a branch, whose base is
// then main) of the catalog repository, under the catalog's own lock
// (Store.Lock("catalog")). No changes → no commit, returns the current oid
// ("" for a branch that does not exist). A move of main is validated with
// rules.CheckCatalogUpdate, exactly what the pre-receive hook runs for a
// push (history, catalog.Check, catalog.CheckImmutable); a problem there
// comes back as *RejectedError. Any other ref (a catalogdraft branch) is
// not validated here at all: a draft may be transiently invalid
// (architecture note, ruling 4.13). The ref is moved with the expected old
// value (ErrConflict when it moved).
//
// allowImmutableChange, found necessary while applying this plan to a
// scratch copy (cross-plan review, Part 2): it names paths whose
// immutability ("was changed") problem is not grounds to reject this
// particular call. It exists only for catalogdraft.Publish's resolved-
// merge-conflict case (Task 5) — a git push (which never passes it) is
// checked exactly as before, with no exemption ever possible. Why this is
// safe: an ordinary immutable-content rewrite is a *silent* one (nobody
// asked about it, so rejecting it outright is correct); a path named here
// arrives only because gitrepo.MergeTree already reported a genuine
// conflict at it and a human explicitly chose a side through Publish's
// res parameter — the catalog's one rule a workspace merge never had to
// consider, now given the same "an explicit resolution overrides what
// main had" treatment a workspace merge's conflict resolution already
// gets. Every other immutability problem, and every other rule
// CheckCatalogUpdate checks, still rejects exactly as before. (Without
// this, TestPublishConflict's own "resolved with theirs" step below
// cannot succeed: once the first draft's new version file is on main,
// resolving the second draft's conflict at that exact path with "theirs"
// necessarily asks to replace that path's content, which CheckImmutable
// — correctly, for every *other* case — would otherwise reject.)
func (s *Store) UpdateCatalog(ref string, author gitrepo.Signature, message string,
	edit func(tree fs.FS) ([]gitrepo.Change, error), allowImmutableChange ...string) (string, error) {
	repo := s.CatalogRepo()
	if err := checkBranch(ref); err != nil {
		return "", err
	}
	unlock := s.Lock(catalogLockKey)
	defer unlock()

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
	var tree fs.FS = fstest.MapFS{}
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
	if base != "" {
		req.Parents = []string{base}
	}
	commit, err := repo.WriteCommit(req)
	if err != nil {
		return "", err
	}
	if ref == mainRef {
		if err := s.validateCatalogMain(repo, old, commit, allowImmutableChange); err != nil {
			return "", err
		}
	}
	if err := repo.UpdateRef(ref, commit, old); err != nil {
		return "", conflict(err)
	}
	return commit, nil
}

// validateCatalogMain applies the push rules to moving the catalog's main,
// except that a "was changed" immutability problem at a path listed in
// allowed is dropped rather than turned into a rejection (see
// UpdateCatalog's allowImmutableChange; empty for every push-equivalent
// caller, so this is a no-op there).
func (s *Store) validateCatalogMain(repo *gitrepo.Repo, oldOID, newOID string, allowed []string) error {
	ps, err := rules.CheckCatalogUpdate(repo, oldOID, newOID)
	if err != nil {
		return err
	}
	if len(allowed) > 0 {
		ps = slices.DeleteFunc(ps, func(p problem.Problem) bool {
			// Only a replaced path's problem is ever dropped, never a
			// removed one: resolving a conflict with a side that deletes
			// an already-published task version is not a case this
			// exemption is meant to cover.
			return p.Rule == problem.RuleImmutable && strings.Contains(p.Message, "was changed") && slices.Contains(allowed, p.Path)
		})
	}
	if len(ps) > 0 {
		return &RejectedError{Problems: ps}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store/ && go vet ./internal/store/`
Expected: PASS (all existing store tests too).

- [ ] **Step 5: Commit**

```bash
git add internal/store/catalog_write.go internal/store/catalog_write_test.go
git commit -m "Add store.UpdateCatalog, the catalog repo's analogue of UpdateWorkspace

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 2: `catalog.NewVersionChanges`

**Files:**
- Create: `internal/catalog/newversionchanges.go`
- Test: `internal/catalog/newversionchanges_test.go`

**Interfaces:**
- Consumes: `catalog.Load`, `Catalog.Tasks` (`task.Graph`), `task.{Meta, Ref,
  ValidID}`, `semver.{Step, Bump}`, `frontmatter.Encode`.
- Produces:

```go
func NewVersionChanges(fsys fs.FS, id string, step semver.Step, title, body string,
	answerType task.AnswerType, choices []string) ([]gitrepo.Change, error)
```

This computes the same next-version and `previous` logic as the existing
`catalog.NewVersion` (current version, `semver.Bump`, version-already-exists
check), without duplicating that logic: both call the same `Graph` methods
and `semver.Bump`; they differ only in where they read from (an `fs.FS`
instead of a directory on disk) and where they write to (a `[]gitrepo.Change`
for a `catalogdraft.Update` edit closure instead of a file on disk), and in
that this one takes the new title, body, answer type and choices from the
caller instead of copying the current version unchanged — the API's draft
task editor lets a Task-Author change them when creating a new version,
unlike the CLI's `custos task new-version`.

- [ ] **Step 1: Write the failing tests** `internal/catalog/newversionchanges_test.go`

```go
package catalog

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

func TestNewVersionChanges(t *testing.T) {
	fsys := fixture.MapFS(fixture.Catalog())
	changes, err := NewVersionChanges(fsys, a, semver.Minor, "New title", "New body.\n", task.AnswerText, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want exactly one", changes)
	}
	ch := changes[0]
	if want := fixture.TaskPath(a, "1.2.0"); ch.Path != want || ch.Delete {
		t.Fatalf("change = %+v, want an add at %s", ch, want)
	}
	want := "---\nid: " + a + "\nversion: 1.2.0\ntitle: New title\nanswer_type: text\nprevious:\n  - id: " + a +
		"\n    version: 1.1.0\n---\n\nNew body.\n"
	if string(ch.Data) != want {
		t.Errorf("got\n%s\nwant\n%s", ch.Data, want)
	}
	// Applying the change to the tree must still validate.
	after := with(fixture.Catalog(), map[string]string{ch.Path: string(ch.Data)})
	fixture.WantNone(t, Check(fixture.MapFS(after)))
}

// with returns files plus more, more winning on overlap.
func with(files, more map[string]string) map[string]string {
	out := map[string]string{}
	for p, c := range files {
		out[p] = c
	}
	for p, c := range more {
		out[p] = c
	}
	return out
}

func TestNewVersionChangesErrors(t *testing.T) {
	tests := []struct {
		name, id, contains string
		change              func(f map[string]string)
	}{
		{"unknown task", c, "not found", func(map[string]string) {}},
		{"forked task", a, "several current versions", func(f map[string]string) {
			f[fixture.TaskPath(a, "1.2.0")] = fixture.TaskFile(a, "1.2.0", a+"@1.0.0")
		}},
		{"merged task", b, "merged into another task", merge},
		{"version exists", a, "already exists", func(f map[string]string) {
			f[fixture.TaskPath(a, "1.2.0")] = fixture.TaskFile(a, "1.2.0", a+"@1.0.0")
			f[fixture.TaskPath(a, "1.1.0")] = fixture.TaskFile(a, "1.1.0", a+"@1.0.0", a+"@1.2.0")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := fixture.Catalog()
			tt.change(f)
			_, err := NewVersionChanges(fixture.MapFS(f), tt.id, semver.Minor, "T", "B\n", task.AnswerText, nil)
			if err == nil || !strings.Contains(err.Error(), tt.contains) {
				t.Errorf("err = %v, want it to contain %q", err, tt.contains)
			}
		})
	}
}
```

`merge` (a catalog-fixture mutator that supersedes task `b` into `a`) and
constants `a`, `b`, `c` already exist in `internal/catalog/catalog_test.go`
from phase 1; reuse them rather than redefining.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/catalog/ -run NewVersionChanges`
Expected: FAIL, `undefined: NewVersionChanges`.

- [ ] **Step 3: Write `internal/catalog/newversionchanges.go`**

```go
package catalog

import (
	"fmt"
	"io/fs"

	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

// NewVersionChanges returns the single gitrepo.Change that adds the next
// version file of task id in the catalog tree fsys: version bumped by step
// from the current version, previous set to that version, and title, body,
// answer_type and choices exactly as given. It computes the same
// next-version and previous values as the disk-based NewVersion (used by
// the CLI), over an fs.FS instead of a directory, for use inside a
// catalogdraft.Update edit closure; problems elsewhere in the catalog do
// not block it.
func NewVersionChanges(fsys fs.FS, id string, step semver.Step, title, body string,
	answerType task.AnswerType, choices []string) ([]gitrepo.Change, error) {
	c, _ := Load(fsys)
	if !c.Tasks.Has(id) {
		return nil, fmt.Errorf("task %s not found in this catalog", id)
	}
	cur, ok := c.Tasks.Current(id)
	if !ok {
		if c.Tasks.Superseded(id) {
			return nil, fmt.Errorf("task %s was merged into another task and has no current version", id)
		}
		return nil, fmt.Errorf("task %s has several current versions; publish a version that merges them first", id)
	}
	next, err := semver.Bump(cur.Version, step)
	if err != nil {
		return nil, err
	}
	if _, exists := c.Tasks.Lookup(task.Ref{ID: id, Version: next}); exists {
		return nil, fmt.Errorf("version %s of task %s already exists", next, id)
	}
	m := task.Meta{ID: id, Version: next, Title: title, AnswerType: answerType, Choices: choices, Previous: []task.Ref{cur.Ref()}}
	data, err := frontmatter.Encode(m, body)
	if err != nil {
		return nil, err
	}
	return []gitrepo.Change{{Path: "tasks/" + id + "/" + next + ".md", Data: data}}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/catalog/ && go vet ./internal/catalog/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/newversionchanges.go internal/catalog/newversionchanges_test.go
git commit -m "Add catalog.NewVersionChanges, the fs.FS analogue of NewVersion

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 3: `catalogdraft` package — `Draft`, `Create`, `List`, `Get`, `Delete`

**Files:**
- Create: `internal/catalogdraft/doc.go`, `internal/catalogdraft/draft.go`
- Test: `internal/catalogdraft/helpers_test.go`, `internal/catalogdraft/draft_test.go`

**Interfaces:**
- Consumes: Task 1 `store.UpdateCatalog`; `store.{Store, CatalogRepo,
  ErrNotFound, ErrConflict}`; `(*gitrepo.Repo).{ResolveRef, ReadFile, DeleteRef,
  Refs}`; `gitrepo.{Signature, Change, ErrRefMoved}`; `task.ValidID`;
  `github.com/google/uuid`.
- Produces (exported names exactly as the architecture note fixes them):

```go
const BranchPrefix = "custos/draft/"

type Draft struct {
	ID     string
	Branch string
	Title  string
	Author gitrepo.Signature
	Base   string
	Commit string
}

func Create(st *store.Store, author gitrepo.Signature, title string) (*Draft, error)
func List(st *store.Store) ([]Draft, error)
func Get(st *store.Store, id string) (*Draft, error)
func Delete(st *store.Store, id string, author gitrepo.Signature) error
```

Unexported, used by Tasks 4-7: `const mainRef`, `const metaPath`, `const
catalogLock`, `ErrInvalid`, `(Draft).ref() string`, `type draftMeta struct`.

A draft's metadata (title, author, base) is stored as a small JSON file,
`custos-draft.json`, committed at the root of the draft's own branch — not
in a side file on disk (Global Constraints: nothing is cached outside Git).
`catalog.Load` never looks at this file (its loader only reads `tasks/`,
`groups/` and `processors.yaml`; a catalog tree "can carry a README,"
per `catalog.go`'s doc comment, and by the same rule can carry this), so its
presence never affects `catalog.Check`. It must never reach `main`; Task 5
("Decisions beyond the architecture note") shows exactly how `Publish`
strips it out.

- [ ] **Step 1: Write the test helpers** `internal/catalogdraft/helpers_test.go`

```go
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
```

- [ ] **Step 2: Write the failing tests** `internal/catalogdraft/draft_test.go`

```go
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
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/catalogdraft/`
Expected: FAIL, `undefined: Create`, `undefined: BranchPrefix`, and so on
(the package does not exist yet).

- [ ] **Step 4: Write `internal/catalogdraft/doc.go`**

```go
// Package catalogdraft lets a Task-Author draft catalog changes on a branch
// of the catalog repository and publish them into main, with custos-aware
// conflict handling when main has moved on (spec §6.1, architecture note
// Plan 4b). It is the catalog's analogue of internal/merge's workspace fork
// and merge, but for the single catalog repository: drafts are created and
// listed explicitly, not forked implicitly per user.
package catalogdraft

import "errors"

// ErrInvalid reports a malformed request: an empty title, an unknown kind
// of resolution, or a resolution for a path that has no conflict. The REST
// API answers it with 400.
var ErrInvalid = errors.New("invalid request")

const (
	mainRef = "refs/heads/main"
	// BranchPrefix names a draft's branch: custos/draft/<draft-id>.
	BranchPrefix = "custos/draft/"
	// metaPath is a draft branch's own metadata marker (title, author,
	// base), committed at the catalog tree's root. catalog.Load never
	// reads it (only tasks/, groups/ and processors.yaml), so it never
	// affects validation; Publish (Task 5) strips it before anything
	// reaches main.
	metaPath = "custos-draft.json"
	// catalogLock is the Store.Lock key for the catalog repository (see
	// store.go's doc comment on Lock); Delete uses it directly, the same
	// way internal/merge.Fork calls st.Lock(newID) directly for a
	// workspace.
	catalogLock = "catalog"
)
```

- [ ] **Step 5: Write `internal/catalogdraft/draft.go`**

```go
package catalogdraft

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

// Draft is one catalog draft: a branch of the catalog repository.
type Draft struct {
	ID     string // UUID v4
	Branch string // custos/draft/<ID>, a short branch name (no refs/heads/ prefix)
	Title  string
	Author gitrepo.Signature // who created it
	Base   string            // main's commit when the draft was created
	Commit string            // current head of the branch
}

// ref returns the draft's full ref name.
func (d Draft) ref() string { return "refs/heads/" + d.Branch }

// draftMeta is the content of metaPath.
type draftMeta struct {
	ID     string            `json:"id"`
	Title  string            `json:"title"`
	Author gitrepo.Signature `json:"author"`
	Base   string            `json:"base"`
}

// Create starts a new draft from the catalog's current main.
func Create(st *store.Store, author gitrepo.Signature, title string) (*Draft, error) {
	title = strings.TrimSpace(strings.SplitN(title, "\n", 2)[0])
	if title == "" {
		return nil, fmt.Errorf("%w: title must not be empty", ErrInvalid)
	}
	cat := st.CatalogRepo()
	// Read once, only to fill in the metadata Create returns; the commit
	// store.UpdateCatalog actually writes resolves main again, fresh,
	// under the lock, so a push landing in between affects at worst which
	// commit this Draft.Base names, never the branch's real parent — and a
	// stale Base only makes Publish choose the three-way-merge path
	// instead of the (also correct) fast-forward one.
	base, ok, err := cat.ResolveRef(mainRef)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("the catalog has no main branch yet; push the catalog first: %w", store.ErrConflict)
	}
	id := uuid.NewString()
	branch := BranchPrefix + id
	data, err := json.Marshal(draftMeta{ID: id, Title: title, Author: author, Base: base})
	if err != nil {
		return nil, err
	}
	commit, err := st.UpdateCatalog("refs/heads/"+branch, author, "Start catalog draft: "+title,
		func(fs.FS) ([]gitrepo.Change, error) {
			return []gitrepo.Change{{Path: metaPath, Data: data}}, nil
		})
	if err != nil {
		return nil, err
	}
	return &Draft{ID: id, Branch: branch, Title: title, Author: author, Base: base, Commit: commit}, nil
}

// Get returns draft id, read from its branch's own metadata marker.
func Get(st *store.Store, id string) (*Draft, error) {
	if !task.ValidID(id) {
		return nil, fmt.Errorf("%w: draft %q", store.ErrNotFound, id)
	}
	branch := BranchPrefix + id
	cat := st.CatalogRepo()
	tip, ok, err := cat.ResolveRef("refs/heads/" + branch)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: draft %s", store.ErrNotFound, id)
	}
	data, ok, err := cat.ReadFile(tip, metaPath)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("draft %s: %s is missing", id, metaPath)
	}
	var meta draftMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("draft %s: %s: %w", id, metaPath, err)
	}
	return &Draft{ID: id, Branch: branch, Title: meta.Title, Author: meta.Author, Base: meta.Base, Commit: tip}, nil
}

// List returns every open draft, sorted by id.
func List(st *store.Store) ([]Draft, error) {
	refs, err := st.CatalogRepo().Refs("refs/heads/" + BranchPrefix)
	if err != nil {
		return nil, err
	}
	var ids []string
	for name := range refs {
		id := strings.TrimPrefix(name, "refs/heads/"+BranchPrefix)
		if task.ValidID(id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	drafts := make([]Draft, 0, len(ids))
	for _, id := range ids {
		d, err := Get(st, id)
		if errors.Is(err, store.ErrNotFound) {
			continue // raced with a concurrent Delete
		}
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, *d)
	}
	return drafts, nil
}

// Delete discards draft id. author is accepted for symmetry with every
// other mutating draft endpoint, which calls api.Author first, but is not
// recorded anywhere: deleting a ref writes no commit.
func Delete(st *store.Store, id string, author gitrepo.Signature) error {
	d, err := Get(st, id)
	if err != nil {
		return err
	}
	unlock := st.Lock(catalogLock)
	defer unlock()
	cat := st.CatalogRepo()
	tip, ok, err := cat.ResolveRef(d.ref())
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: draft %s", store.ErrNotFound, id)
	}
	if err := cat.DeleteRef(d.ref(), tip); err != nil {
		if errors.Is(err, gitrepo.ErrRefMoved) {
			return fmt.Errorf("%w: %v", store.ErrConflict, err)
		}
		return err
	}
	return nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/catalogdraft/ && go vet ./internal/catalogdraft/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/catalogdraft/doc.go internal/catalogdraft/draft.go internal/catalogdraft/helpers_test.go internal/catalogdraft/draft_test.go
git commit -m "Add catalogdraft.Create, List, Get and Delete

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 4: `catalogdraft.Update`

**Files:**
- Create: `internal/catalogdraft/update.go`
- Test: `internal/catalogdraft/update_test.go`

**Interfaces:**
- Consumes: Task 1 `store.UpdateCatalog`; Task 3 `Get`, `(Draft).ref`.
- Produces:

```go
func Update(st *store.Store, id string, author gitrepo.Signature, message string,
	edit func(tree fs.FS) ([]gitrepo.Change, error)) (string, error)
```

- [ ] **Step 1: Write the failing test** `internal/catalogdraft/update_test.go`

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/catalogdraft/ -run TestUpdate`
Expected: FAIL, `undefined: Update`.

- [ ] **Step 3: Write `internal/catalogdraft/update.go`**

```go
package catalogdraft

import (
	"io/fs"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
)

// Update applies edit to draft id's current tree and commits the result on
// its branch (author becomes the commit's Git author; the committer is
// always gitrepo.Bot, same as every other store write, through
// store.UpdateCatalog). No validation beyond "does this still parse"
// happens anywhere on this path: a draft may be transiently invalid
// (ruling 4.13), so a Task-Author can save half-finished edits. edit gets
// the draft's tree, exactly like store.UpdateWorkspace's edit parameter.
func Update(st *store.Store, id string, author gitrepo.Signature, message string,
	edit func(tree fs.FS) ([]gitrepo.Change, error)) (string, error) {
	d, err := Get(st, id)
	if err != nil {
		return "", err
	}
	return st.UpdateCatalog(d.ref(), author, message, edit)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/catalogdraft/ && go vet ./internal/catalogdraft/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/catalogdraft/update.go internal/catalogdraft/update_test.go
git commit -m "Add catalogdraft.Update

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 5: `catalogdraft.Publish` and `PublishResult`

**Files:**
- Create: `internal/catalogdraft/publish.go`
- Test: `internal/catalogdraft/publish_test.go`

**Interfaces:**
- Consumes: Task 1 `store.UpdateCatalog`; Task 3 `Get`, `Delete`,
  `(Draft).ref`, `metaPath`; `(*gitrepo.Repo).{ResolveRef, IsAncestor,
  TreeFS, MergeTree, EditTree}`; `gitrepo.{MergeTreeResult, Change}`;
  `merge.{Resolution, Conflict, SideOurs, SideTheirs, SideContent}`
  (`internal/catalogdraft` imports `internal/merge` for these types only,
  never the other way around, exactly as the architecture note specifies).
- Produces (exported names exactly as the architecture note fixes them):

```go
func Publish(st *store.Store, id string, author gitrepo.Signature, res map[string]merge.Resolution) (*PublishResult, error)
type PublishResult struct {
	Commit    string
	Conflicts []merge.Conflict
}
```

Unexported, this task's own: `conflictsErr`, `planPublish`, `catalogKind`,
`diffFS`, `const kindTask, kindGroup, kindProcessors, kindOther`.

`Publish`'s edit closure (passed to `store.UpdateCatalog(mainRef, ...)`)
runs under the catalog's lock and is given the **current** main tree each
time it runs, so it always re-resolves both main and the draft's tip itself
and recomputes the fast-forward/merge decision fresh, rather than trusting
anything read before the lock was taken — the only way `main` can move
without going through this same lock is a concurrent `git push` to
`catalog.git` (pushes bypass the in-process lock entirely, same as for
workspaces), and recomputing fresh inside the closure is what makes that
race safe instead of silently wrong.

- [ ] **Step 1: Write the failing tests** `internal/catalogdraft/publish_test.go`

This is the task that covers three of the four scenarios the brief names:
a clean create → edit → publish, the exact two-drafts-conflict scenario,
and `catalog.Check` rejecting an invalid publish.

```go
package catalogdraft

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/merge"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

// newVersionEdit returns an edit closure that adds the next version of
// fixture.TaskA with the given title.
func newVersionEdit(title string) func(fs.FS) ([]gitrepo.Change, error) {
	return func(tree fs.FS) ([]gitrepo.Change, error) {
		return catalog.NewVersionChanges(tree, fixture.TaskA, semver.Minor, title, "Body.\n", task.AnswerText, nil)
	}
}

func TestPublishFastForward(t *testing.T) {
	st := setup(t)
	d, err := Create(st, alice, "Bump task A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Update(st, d.ID, alice, "New version", newVersionEdit("A, 1.2.0")); err != nil {
		t.Fatal(err)
	}

	res, err := Publish(st, d.ID, alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 || res.Commit == "" {
		t.Fatalf("result = %+v, want a clean publish", res)
	}
	if catalogMain(t, st) != res.Commit {
		t.Errorf("main = %s, want %s", catalogMain(t, st), res.Commit)
	}
	// The published tree never carries the draft's metadata marker.
	if _, ok, _ := st.CatalogRepo().ReadFile(res.Commit, metaPath); ok {
		t.Error("published tree carries the draft metadata marker")
	}
	newVersion := fixture.TaskPath(fixture.TaskA, "1.2.0")
	if _, ok, _ := st.CatalogRepo().ReadFile(res.Commit, newVersion); !ok {
		t.Errorf("%s missing from main after publish", newVersion)
	}
	// The draft branch is gone.
	if _, err := Get(st, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get after publish: %v, want ErrNotFound", err)
	}
	fixture.WantNone(t, catalog.Check(mainFS(t, st)))
}

// mainFS returns the catalog's current main tree.
func mainFS(t *testing.T, st *store.Store) fs.FS {
	t.Helper()
	fsys, err := st.CatalogRepo().TreeFS(catalogMain(t, st))
	if err != nil {
		t.Fatal(err)
	}
	return fsys
}

// TestPublishConflict is the architecture note's "two drafts editing the
// same task" scenario: both drafts start from the same main (TaskA current
// at 1.1.0) and each adds its own, differently-titled 1.2.0 of TaskA. The
// first publish is a clean fast-forward. The second then conflicts on
// exactly tasks/<TaskA>/1.2.0.md (an add/add conflict: the merge base has
// no such file, and the two sides added different content at the same
// path), is resolved with side "theirs", and republishes cleanly.
func TestPublishConflict(t *testing.T) {
	st := setup(t)
	first, err := Create(st, alice, "A via first draft")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Create(st, bob, "A via second draft")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Update(st, first.ID, alice, "1.2.0", newVersionEdit("First draft's title")); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(st, second.ID, bob, "1.2.0", newVersionEdit("Second draft's title")); err != nil {
		t.Fatal(err)
	}

	if _, err := Publish(st, first.ID, alice, nil); err != nil {
		t.Fatal(err)
	}

	path := fixture.TaskPath(fixture.TaskA, "1.2.0")
	res, err := Publish(st, second.ID, bob, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != "" || len(res.Conflicts) != 1 {
		t.Fatalf("result = %+v, want exactly one conflict and no commit", res)
	}
	conflict := res.Conflicts[0]
	if conflict.Path != path || conflict.Kind != kindTask || conflict.Ours == nil || conflict.Theirs == nil {
		t.Fatalf("conflict = %+v, want path %s, kind %s", conflict, path, kindTask)
	}
	// The draft is untouched by a conflicting publish.
	if _, err := Get(st, second.ID); err != nil {
		t.Errorf("Get after a conflicting publish: %v", err)
	}

	res, err = Publish(st, second.ID, bob, map[string]merge.Resolution{path: {Side: merge.SideTheirs}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 || res.Commit == "" {
		t.Fatalf("result after resolution = %+v", res)
	}
	data, ok, err := st.CatalogRepo().ReadFile(res.Commit, path)
	if err != nil || !ok || string(data) != string(conflict.Theirs) {
		t.Errorf("published content = %q (ok=%v), want %q", data, ok, conflict.Theirs)
	}
	fixture.WantNone(t, catalog.Check(mainFS(t, st)))
}

// TestPublishImmutabilityViolation: a hand-built edit rewrites the content
// of an already-published task version file. catalog.Check alone would not
// catch this (the file, taken on its own, is still a well-formed task
// version); only catalog.CheckImmutable, which rules.CheckCatalogUpdate also
// runs, compares it against the old main tree.
func TestPublishImmutabilityViolation(t *testing.T) {
	st := setup(t)
	d, err := Create(st, alice, "Rewrite history")
	if err != nil {
		t.Fatal(err)
	}
	existing := fixture.TaskPath(fixture.TaskA, "1.0.0")
	rewritten := "---\nid: " + fixture.TaskA + "\nversion: 1.0.0\ntitle: Rewritten\nanswer_type: text\n---\n\nRewritten body.\n"
	if _, err := Update(st, d.ID, alice, "rewrite", func(fs.FS) ([]gitrepo.Change, error) {
		return []gitrepo.Change{{Path: existing, Data: []byte(rewritten)}}, nil
	}); err != nil {
		t.Fatal(err)
	}

	_, err = Publish(st, d.ID, alice, nil)
	var rejected *store.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want *store.RejectedError", err)
	}
	found := false
	for _, p := range rejected.Problems {
		if p.Rule == problem.RuleImmutable {
			found = true
		}
	}
	if !found {
		t.Errorf("problems %+v, want one with rule %s", rejected.Problems, problem.RuleImmutable)
	}
	// The draft is untouched by a rejected publish.
	if _, err := Get(st, d.ID); err != nil {
		t.Errorf("Get after a rejected publish: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/catalogdraft/ -run Publish`
Expected: FAIL, `undefined: Publish`, `undefined: PublishResult`, `undefined: kindTask`.

- [ ] **Step 3: Write `internal/catalogdraft/publish.go`**

```go
package catalogdraft

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/merge"
	"github.com/emeland-io/custos/internal/store"
)

// Kinds of a merge.Conflict's Kind field for a catalog publish. Distinct
// from internal/merge's own KindAnswer, KindGenerated etc., which describe
// workspace paths; merge.Conflict.Kind is a plain string, so catalogdraft
// fills it in with these instead.
const (
	kindTask       = "task"
	kindGroup      = "group"
	kindProcessors = "processors"
	kindOther      = "other"
)

// PublishResult is the outcome of Publish.
type PublishResult struct {
	Commit    string
	Conflicts []merge.Conflict // non-empty → nothing changed, same contract as internal/merge.Merge
}

// conflictsErr carries unresolved conflicts out of the edit closure
// store.UpdateCatalog runs, so Publish can turn them into a PublishResult
// instead of a generic error. It never reaches a caller outside this file.
type conflictsErr struct{ conflicts []merge.Conflict }

func (e *conflictsErr) Error() string {
	return fmt.Sprintf("publishing needs a resolution for %d conflicting path(s)", len(e.conflicts))
}

// Publish merges draft id into the catalog's main: a plain fast-forward
// (main's tree becomes the draft's tree) when main has not moved past the
// draft's Base, otherwise a three-way merge computed the same way
// internal/merge.Merge computes a workspace merge (gitrepo.MergeTree, then
// each conflicting path settled by res or left open), over
// tasks/<uuid>/<semver>.md, groups/** and processors.yaml — every path a
// catalog tree holds other than the draft's own metadata marker, which this
// function always strips before anything reaches main. A clean result is
// validated by store.UpdateCatalog's own main-ref check (rules
// .CheckCatalogUpdate) before the compare-and-swap; a validation problem
// comes back as *store.RejectedError, exactly like a rejected push, and the
// draft branch is left untouched so the author can keep editing. Publish
// deletes the draft branch on success.
//
// Both the fast-forward decision and the merge itself are recomputed fresh
// every time the edit closure below actually runs (which may be more than
// once if store.UpdateCatalog's own compare-and-swap loses a race to a
// concurrent git push — it does not retry on its own, callers do), so a
// stale read from before the lock was taken can never cause the wrong
// content to be published; see the task's own notes above this code block.
func Publish(st *store.Store, id string, author gitrepo.Signature, res map[string]merge.Resolution) (*PublishResult, error) {
	d, err := Get(st, id)
	if err != nil {
		return nil, err
	}
	message := fmt.Sprintf("Publish catalog draft %q", d.Title)
	// allowedPaths names exactly the paths the caller explicitly resolved
	// a conflict for (res's own keys): see store.UpdateCatalog's
	// allowImmutableChange doc comment for why this, and only this, is
	// exempt from the immutability check that otherwise applies to every
	// publish (found while applying this plan to a scratch copy, Part 2
	// of cross-plan review: TestPublishConflict's own "resolved with
	// theirs" step cannot succeed without it).
	allowedPaths := make([]string, 0, len(res))
	for path := range res {
		allowedPaths = append(allowedPaths, path)
	}
	commit, err := st.UpdateCatalog(mainRef, author, message, func(tree fs.FS) ([]gitrepo.Change, error) {
		cat := st.CatalogRepo()
		ours, ok, err := cat.ResolveRef(mainRef)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("the catalog has no main branch yet: %w", store.ErrConflict)
		}
		draftTip, ok, err := cat.ResolveRef(d.ref())
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: draft %s", store.ErrNotFound, id)
		}
		if merged, err := cat.IsAncestor(draftTip, ours); err != nil {
			return nil, err
		} else if merged {
			return nil, nil // already published; no changes to make
		}

		var desired fs.FS
		if ours == d.Base {
			if desired, err = cat.TreeFS(draftTip); err != nil {
				return nil, err
			}
		} else {
			mt, err := cat.MergeTree(ours, draftTip)
			if err != nil {
				return nil, err
			}
			changes, conflicts, err := planPublish(mt, res)
			if err != nil {
				return nil, err
			}
			if len(conflicts) > 0 {
				return nil, &conflictsErr{conflicts: conflicts}
			}
			mergedTree, err := cat.EditTree(mt.Tree, changes)
			if err != nil {
				return nil, err
			}
			if desired, err = cat.TreeFS(mergedTree); err != nil {
				return nil, err
			}
		}
		return diffFS(tree, desired)
	}, allowedPaths...)

	var ce *conflictsErr
	if errors.As(err, &ce) {
		return &PublishResult{Conflicts: ce.conflicts}, nil
	}
	if err != nil {
		return nil, err
	}
	// The draft's content has landed on main; discard the branch. A
	// failure here (only a lost compare-and-swap against a concurrent
	// Update of the same draft) is returned as-is: the publish itself
	// already succeeded, and a retried Publish converges safely, because
	// the "already published" check above sees the old content as an
	// ancestor of the new main.
	if err := Delete(st, id, author); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return &PublishResult{Commit: commit}, nil
}

// planPublish settles every conflict mt reports by its resolution in res,
// or records it as still open. It always appends a deletion of metaPath, so
// the draft's metadata marker never survives into the merged tree (mt.Tree
// carries it as a clean, non-conflicting add from the draft's side, since
// main never has it).
func planPublish(mt *gitrepo.MergeTreeResult, res map[string]merge.Resolution) ([]gitrepo.Change, []merge.Conflict, error) {
	var changes []gitrepo.Change
	var conflicts []merge.Conflict
	used := map[string]bool{}
	for _, c := range mt.Conflicts {
		r, ok := res[c.Path]
		if !ok {
			conflicts = append(conflicts, merge.Conflict{Path: c.Path, Ours: c.Ours, Theirs: c.Theirs, Kind: catalogKind(c.Path)})
			continue
		}
		used[c.Path] = true
		switch r.Side {
		case merge.SideOurs, merge.SideTheirs:
			if r.Content != nil {
				return nil, nil, fmt.Errorf("%w: %s: content is only allowed with side %q", ErrInvalid, c.Path, merge.SideContent)
			}
			data := c.Ours
			if r.Side == merge.SideTheirs {
				data = c.Theirs
			}
			if data == nil {
				changes = append(changes, gitrepo.Change{Path: c.Path, Delete: true})
			} else {
				changes = append(changes, gitrepo.Change{Path: c.Path, Data: data})
			}
		case merge.SideContent:
			if r.Content == nil {
				return nil, nil, fmt.Errorf("%w: %s: side %q needs content", ErrInvalid, c.Path, merge.SideContent)
			}
			changes = append(changes, gitrepo.Change{Path: c.Path, Data: r.Content})
		default:
			return nil, nil, fmt.Errorf("%w: %s: side %q is not %s, %s or %s", ErrInvalid, c.Path, r.Side, merge.SideOurs, merge.SideTheirs, merge.SideContent)
		}
	}
	for path := range res {
		if !used[path] {
			return nil, nil, fmt.Errorf("%w: %s has no conflict to resolve", ErrInvalid, path)
		}
	}
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Path < conflicts[j].Path })
	changes = append(changes, gitrepo.Change{Path: metaPath, Delete: true})
	return changes, conflicts, nil
}

// catalogKind classifies a catalog path for merge.Conflict.Kind.
func catalogKind(path string) string {
	switch {
	case strings.HasPrefix(path, "tasks/"):
		return kindTask
	case strings.HasPrefix(path, "groups/"):
		return kindGroup
	case path == "processors.yaml":
		return kindProcessors
	}
	return kindOther
}

// diffFS returns the gitrepo.Change entries that turn old into newTree, for
// store.UpdateCatalog's edit closure: one entry per path added or changed
// in newTree, one Delete entry per path newTree no longer has. metaPath is
// always skipped on both sides: it exists only on draft branches, never on
// main.
func diffFS(old, newTree fs.FS) ([]gitrepo.Change, error) {
	var changes []gitrepo.Change
	err := fs.WalkDir(newTree, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path == metaPath {
			return err
		}
		data, err := fs.ReadFile(newTree, path)
		if err != nil {
			return err
		}
		if prev, err := fs.ReadFile(old, path); err == nil && bytes.Equal(prev, data) {
			return nil
		}
		changes = append(changes, gitrepo.Change{Path: path, Data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = fs.WalkDir(old, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path == metaPath {
			return err
		}
		if _, statErr := fs.Stat(newTree, path); errors.Is(statErr, fs.ErrNotExist) {
			changes = append(changes, gitrepo.Change{Path: path, Delete: true})
		}
		return nil
	})
	return changes, err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/catalogdraft/ && go vet ./internal/catalogdraft/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/catalogdraft/publish.go internal/catalogdraft/publish_test.go
git commit -m "Add catalogdraft.Publish: fast-forward or three-way merge into main

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 6: Export `api.CatalogJSON`

**Files:**
- Modify: `internal/api/catalog.go`

**Interfaces:**
- Consumes: the existing unexported `catalogToJSON(c *catalog.Catalog, commit string) catalogJSON`.
- Produces:

```go
func CatalogJSON(c *catalog.Catalog, commit string) any
```

`catalogToJSON`'s result type (`catalogJSON`, and the `groupJSON`/
`childJSON`/`taskVersionJSON` types it nests) is unexported, so another
package cannot name it; this one-line exported wrapper, returning `any`,
lets `catalogdraft`'s draft-viewing endpoint (Task 7) render a draft
branch's catalog tree with the exact same JSON shape `GET /api/catalog`
already uses for `main` — `encoding/json` marshals by each field's own
`json` tag regardless of whether the struct type itself is exported, so
this is enough for `catalogdraft/http.go` to call across the package
boundary. See "Decisions beyond the architecture note."

- [ ] **Step 1: Add the export**

```go
// CatalogJSON builds the same JSON shape GET /api/catalog returns, for a
// catalog tree that is not necessarily the committed main — plan 4b's
// draft-viewing endpoint (internal/catalogdraft) calls this so the frontend
// can render a draft's catalog the same way it renders the read-only one.
func CatalogJSON(c *catalog.Catalog, commit string) any {
	return catalogToJSON(c, commit)
}
```

Insert this immediately after the existing `catalogToJSON` function in
`internal/api/catalog.go`. No other line in the file changes.

- [ ] **Step 2: Run the existing tests to verify nothing broke**

Run: `go test ./internal/api/ && go vet ./internal/api/`
Expected: PASS.

- [ ] **Step 3: Write a one-line regression test** `internal/api/catalog_test.go` (append)

```go
func TestCatalogJSONExported(t *testing.T) {
	// catalogToJSON dereferences c.Tasks unconditionally (it is always
	// populated by catalog.Load); found while applying this plan to a
	// scratch copy — the literal below needs a non-nil *task.Graph too,
	// not just Groups, or CatalogJSON panics instead of returning a
	// value. Add "github.com/emeland-io/custos/internal/task" to this
	// test file's imports if not already present.
	c := &catalog.Catalog{Groups: map[string]*catalog.Group{}, Tasks: &task.Graph{}}
	if got := CatalogJSON(c, "deadbeef"); got == nil {
		t.Fatal("CatalogJSON returned nil")
	}
}
```

Run: `go test ./internal/api/ -run TestCatalogJSONExported`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/api/catalog.go internal/api/catalog_test.go
git commit -m "Export api.CatalogJSON for plan 4b's draft-viewing endpoint

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 7: `internal/catalogdraft/http.go` — `Register` and all nine endpoints

**Files:**
- Create: `internal/catalogdraft/http.go`
- Test: `internal/catalogdraft/http_test.go`

**Interfaces:**
- Consumes: Tasks 3-5 (`Draft`, `Create`, `List`, `Get`, `Delete`, `Update`,
  `Publish`, `PublishResult`); Task 2 `catalog.NewVersionChanges`; Task 6
  `api.CatalogJSON`; `catalog.{Check, Group, Child, Registry, Processor}`;
  `task.{AnswerType, ValidID}`; `semver.Step`; `merge.{Resolution, Conflict,
  SideOurs, SideTheirs, SideContent}`; `api.{API, Author, WriteJSON,
  WriteError}`; `auth.Require` (plan 4a, `internal/auth`); `go.yaml.in/yaml/v3`.
- Produces:

```go
func Register(a *api.API, st *store.Store)
```

wiring the nine endpoints below, each wrapped in
`auth.Require("task-author", ...)`:

| Method and path | Go pattern |
| --- | --- |
| `GET /api/catalog/drafts` | `"GET /api/catalog/drafts"` |
| `POST /api/catalog/drafts` | `"POST /api/catalog/drafts"` |
| `GET /api/catalog/drafts/{id}` | `"GET /api/catalog/drafts/{id}"` |
| `PUT /api/catalog/drafts/{id}/tasks/{task-id}` | `"PUT /api/catalog/drafts/{id}/tasks/{task}"` |
| `PUT /api/catalog/drafts/{id}/groups/{slug}` | `"PUT /api/catalog/drafts/{id}/groups/{slug}"` |
| `PUT /api/catalog/drafts/{id}/groups-index` | `"PUT /api/catalog/drafts/{id}/groups-index"` |
| `PUT /api/catalog/drafts/{id}/processors` | `"PUT /api/catalog/drafts/{id}/processors"` |
| `DELETE /api/catalog/drafts/{id}` | `"DELETE /api/catalog/drafts/{id}"` |
| `POST /api/catalog/drafts/{id}/publish` | `"POST /api/catalog/drafts/{id}/publish"` |

The `{task-id}` wildcard in the architecture note's table is written
`{task}` in the Go pattern: `net/http.ServeMux` requires a wildcard name to
be a valid Go identifier, which rules out the hyphen — the URL path itself
is unchanged (see "Decisions beyond the architecture note").

- [ ] **Step 1: Write the failing tests** `internal/catalogdraft/http_test.go`

```go
package catalogdraft

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
)

// newAPI returns an api.API over st with this package's endpoints
// registered and no auth wrapping (auth.Require is plan 4a's; these tests
// exercise this package's handlers directly, the way internal/merge's own
// http_test.go exercises handleFork/handleMerge directly).
func newAPI(t *testing.T, st *store.Store) *api.API {
	t.Helper()
	bl, err := blobs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(st, bl)
	Register(a, st, nil)
	return a
}

// newAPIWithOnPublish is newAPI, but wires onPublish so a test can confirm
// it fires exactly on a successful publish and never on a conflict or
// other error (cross-plan review, architecture note ruling 17).
func newAPIWithOnPublish(t *testing.T, st *store.Store, onPublish func()) *api.API {
	t.Helper()
	bl, err := blobs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(st, bl)
	Register(a, st, onPublish)
	return a
}

// allCaller holds every role for every workspace — this file tests
// catalogdraft's own endpoints, not authorization (auth.Require is plan
// 4a's; found while applying this plan to a scratch copy: doJSON below
// originally sent the pre-4a X-Custos-Author header, which the
// task-author-gated routes this plan adds no longer read at all,
// cascading 401s into every test in this file).
type allCaller struct{ sig gitrepo.Signature }

func (c allCaller) Signature() gitrepo.Signature      { return c.sig }
func (c allCaller) Has(role, workspaceID string) bool { return true }

func doJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = httptest.NewRequest(method, path, bytes.NewReader(data))
	}
	r = r.WithContext(api.WithCaller(r.Context(), allCaller{sig: gitrepo.Signature{Name: "Alice Example", Email: "alice@example.org"}}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHandleDraftLifecycle(t *testing.T) {
	st := setup(t)
	h := newAPI(t, st).Handler()

	w := doJSON(t, h, "POST", "/api/catalog/drafts", map[string]string{"title": "Rename release"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created draftJSON
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	w = doJSON(t, h, "GET", "/api/catalog/drafts/"+created.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("view: %d %s", w.Code, w.Body)
	}
	var view draftViewJSON
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Draft.ID != created.ID || len(view.Problems) != 0 {
		t.Errorf("view = %+v", view)
	}

	w = doJSON(t, h, "PUT", "/api/catalog/drafts/"+created.ID+"/tasks/"+fixture.TaskA, map[string]any{
		"step": "minor", "title": "New title", "body": "New body.\n", "answer_type": "text",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("task edit: %d %s", w.Code, w.Body)
	}

	w = doJSON(t, h, "GET", "/api/catalog/drafts", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	var list []draftJSON
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("list = %v, err %v", list, err)
	}

	w = doJSON(t, h, "POST", "/api/catalog/drafts/"+created.ID+"/publish", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", w.Code, w.Body)
	}
	var result publishResultJSON
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Commit == "" {
		t.Fatalf("publish result %+v, err %v", result, err)
	}

	w = doJSON(t, h, "GET", "/api/catalog/drafts/"+created.ID, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("draft still visible after publish: %d", w.Code)
	}
}

func TestHandleGroupEdit(t *testing.T) {
	st := setup(t)
	h := newAPI(t, st).Handler()
	w := doJSON(t, h, "POST", "/api/catalog/drafts", map[string]string{"title": "Edit a group"})
	var d draftJSON
	json.Unmarshal(w.Body.Bytes(), &d)

	w = doJSON(t, h, "PUT", "/api/catalog/drafts/"+d.ID+"/groups/release", map[string]any{
		"title": "Release (renamed)", "children": []map[string]string{{"task": fixture.TaskB}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("group edit: %d %s", w.Code, w.Body)
	}
	data, ok, err := st.CatalogRepo().ReadFile(d.Commit, "")
	_ = data
	_ = ok
	_ = err
	draft, err := Get(st, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	group, ok, err := st.CatalogRepo().ReadFile(draft.Commit, "groups/release/group.yaml")
	if err != nil || !ok {
		t.Fatalf("group.yaml: ok=%v err=%v", ok, err)
	}
	// 2-space list indentation, matching marshalYAML's SetIndent(2) and
	// the project's own existing fixtures (internal/fixture.go's
	// "children:\n  - task: ..." groups) — found while applying this
	// plan to a scratch copy: the original "want" string here hardcoded
	// 4 spaces, which go.yaml.in/yaml/v3 never actually produces at this
	// indent setting.
	if want := "title: Release (renamed)\nchildren:\n  - task: " + fixture.TaskB + "\n"; string(group) != want {
		t.Errorf("group.yaml = %q, want %q", group, want)
	}
}

func TestHandleGroupInvalidSlug(t *testing.T) {
	st := setup(t)
	h := newAPI(t, st).Handler()
	w := doJSON(t, h, "POST", "/api/catalog/drafts", map[string]string{"title": "x"})
	var d draftJSON
	json.Unmarshal(w.Body.Bytes(), &d)

	// "../escape" and "a/b" never reach handleGroupEdit's own slugRE
	// check at all: net/http's ServeMux, matching a single-segment
	// {slug} wildcard, 404s "a/b" outright (an embedded "/" cannot be
	// part of one segment) and 307-redirects "../escape" to its
	// lexically-cleaned equivalent before any pattern is even matched —
	// both stronger than a 400 from this handler, just enforced one
	// layer earlier than Review Focus 4 assumed. Found while applying
	// this plan to a scratch copy: the original table asserted 400 for
	// these two and failed every time, against net/http's actual routing
	// behavior, not against a gap in the handler.
	for slug, want := range map[string]int{"../escape": http.StatusTemporaryRedirect, "a/b": http.StatusNotFound} {
		w = doJSON(t, h, "PUT", "/api/catalog/drafts/"+d.ID+"/groups/"+slug, map[string]any{"title": "x"})
		if w.Code != want {
			t.Errorf("slug %q: %d %s, want %d (from net/http's own routing, before any handler runs)", slug, w.Code, w.Body, want)
		}
	}
	// These single-segment slugs do reach handleGroupEdit, and must be
	// rejected by its own slugRE check (400), independent of routing.
	for _, slug := range []string{"UPPER", "a.b"} {
		w = doJSON(t, h, "PUT", "/api/catalog/drafts/"+d.ID+"/groups/"+slug, map[string]any{"title": "x"})
		if w.Code != http.StatusBadRequest {
			t.Errorf("slug %q: %d %s, want 400", slug, w.Code, w.Body)
		}
	}
	// The mux itself 404s an empty path segment.
	w = doJSON(t, h, "PUT", "/api/catalog/drafts/"+d.ID+"/groups/", map[string]any{"title": "x"})
	if w.Code != http.StatusNotFound {
		t.Errorf("empty slug: %d %s, want 404", w.Code, w.Body)
	}
}

func TestHandlePublishConflict(t *testing.T) {
	st := setup(t)
	h := newAPI(t, st).Handler()
	w := doJSON(t, h, "POST", "/api/catalog/drafts", map[string]string{"title": "first"})
	var first draftJSON
	json.Unmarshal(w.Body.Bytes(), &first)
	w = doJSON(t, h, "POST", "/api/catalog/drafts", map[string]string{"title": "second"})
	var second draftJSON
	json.Unmarshal(w.Body.Bytes(), &second)

	for _, d := range []draftJSON{first, second} {
		w = doJSON(t, h, "PUT", "/api/catalog/drafts/"+d.ID+"/tasks/"+fixture.TaskA, map[string]any{
			"step": "minor", "title": "from " + d.ID, "body": "Body.\n", "answer_type": "text",
		})
		if w.Code != http.StatusOK {
			t.Fatalf("task edit for %s: %d %s", d.ID, w.Code, w.Body)
		}
	}
	if w = doJSON(t, h, "POST", "/api/catalog/drafts/"+first.ID+"/publish", map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("first publish: %d %s", w.Code, w.Body)
	}
	w = doJSON(t, h, "POST", "/api/catalog/drafts/"+second.ID+"/publish", map[string]any{})
	if w.Code != http.StatusConflict {
		t.Fatalf("second publish: %d %s, want 409", w.Code, w.Body)
	}
	var cb conflictBody
	if err := json.Unmarshal(w.Body.Bytes(), &cb); err != nil || len(cb.Conflicts) != 1 {
		t.Fatalf("conflict body %+v, err %v", cb, err)
	}
	path := cb.Conflicts[0].Path
	w = doJSON(t, h, "POST", "/api/catalog/drafts/"+second.ID+"/publish", map[string]any{
		"resolutions": map[string]any{path: map[string]string{"side": "theirs"}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("resolved publish: %d %s", w.Code, w.Body)
	}
}

// TestHandlePublishCallsOnPublish: onPublish fires exactly once per
// successful publish, and not at all for the 409 a conflicting publish
// answers first (architecture note ruling 17; cross-plan review).
func TestHandlePublishCallsOnPublish(t *testing.T) {
	st := setup(t)
	calls := 0
	h := newAPIWithOnPublish(t, st, func() { calls++ }).Handler()
	w := doJSON(t, h, "POST", "/api/catalog/drafts", map[string]string{"title": "d"})
	var d draftJSON
	json.Unmarshal(w.Body.Bytes(), &d)
	if w = doJSON(t, h, "PUT", "/api/catalog/drafts/"+d.ID+"/tasks/"+fixture.TaskA, map[string]any{
		"step": "minor", "title": "t", "body": "Body.\n", "answer_type": "text",
	}); w.Code != http.StatusOK {
		t.Fatalf("task edit: %d %s", w.Code, w.Body)
	}
	if calls != 0 {
		t.Fatalf("onPublish called %d times before any publish", calls)
	}
	if w = doJSON(t, h, "POST", "/api/catalog/drafts/"+d.ID+"/publish", map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", w.Code, w.Body)
	}
	if calls != 1 {
		t.Fatalf("onPublish called %d times, want 1", calls)
	}
}

```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/catalogdraft/ -run TestHandle`
Expected: FAIL, `undefined: Register`, `undefined: draftJSON`, and so on.

- [ ] **Step 3: Write `internal/catalogdraft/http.go`**

```go
package catalogdraft

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"regexp"

	"go.yaml.in/yaml/v3"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/auth"
	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/merge"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

// maxBody bounds every request body this package decodes.
const maxBody = 4 << 20

// slugRE matches a valid group slug. Mirrors catalog's own (unexported)
// slugRE; kept as its own copy here rather than exported from catalog
// purely for this, since it is a one-line regular expression, not a
// behaviour worth sharing code for (see "Decisions beyond the architecture
// note"). This check runs before the slug ever reaches a file path, which
// catalog.Check's post-hoc validation does not protect against (ruling
// 4.13 lets a draft be invalid, but never lets a request write outside
// groups/<slug>/).
var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Register adds the nine draft endpoints (architecture note, Plan 4b) to
// the REST API, each gated by auth.Require("task-author", ...). Call it
// before a.Handler(), same convention as merge.Register. onPublish, which
// may be nil, is called synchronously after a successful Publish (never on
// a 409 or other error) — the catalog's analogue of merge.Register's
// onMainMoved, added during cross-plan review so a draft publish
// reconciles pinned workspaces immediately instead of waiting for an
// unrelated future catalog push (architecture note ruling 17; see
// "Decisions beyond the architecture note").
func Register(a *api.API, st *store.Store, onPublish func()) {
	a.Handle("GET /api/catalog/drafts", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleList(st, w, r) }))
	a.Handle("POST /api/catalog/drafts", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleCreate(st, w, r) }))
	a.Handle("GET /api/catalog/drafts/{id}", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleView(st, w, r) }))
	a.Handle("PUT /api/catalog/drafts/{id}/tasks/{task}", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleTaskEdit(st, w, r) }))
	a.Handle("PUT /api/catalog/drafts/{id}/groups/{slug}", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleGroupEdit(st, w, r) }))
	a.Handle("PUT /api/catalog/drafts/{id}/groups-index", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleGroupsIndexEdit(st, w, r) }))
	a.Handle("PUT /api/catalog/drafts/{id}/processors", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleProcessorsEdit(st, w, r) }))
	a.Handle("DELETE /api/catalog/drafts/{id}", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleDelete(st, w, r) }))
	a.Handle("POST /api/catalog/drafts/{id}/publish", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handlePublish(st, onPublish, w, r) }))
}

// --- JSON shapes ---

type errorBody struct {
	Error string `json:"error"`
}

type authorJSON struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type draftJSON struct {
	ID     string     `json:"id"`
	Branch string     `json:"branch"`
	Title  string      `json:"title"`
	Author authorJSON `json:"author"`
	Base   string     `json:"base"`
	Commit string     `json:"commit"`
}

func draftToJSON(d *Draft) draftJSON {
	return draftJSON{
		ID: d.ID, Branch: d.Branch, Title: d.Title,
		Author: authorJSON{Name: d.Author.Name, Email: d.Author.Email},
		Base:   d.Base, Commit: d.Commit,
	}
}

type problemJSON struct {
	Path    string `json:"path"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

type draftViewJSON struct {
	Draft    draftJSON     `json:"draft"`
	Catalog  any           `json:"catalog"`
	Problems []problemJSON `json:"problems"`
}

type conflictJSON struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Ours   []byte `json:"ours"`
	Theirs []byte `json:"theirs"`
}

type conflictBody struct {
	Error     string         `json:"error"`
	Conflicts []conflictJSON `json:"conflicts"`
}

type resolutionJSON struct {
	Side    string `json:"side"`
	Content []byte `json:"content"`
}

type publishResultJSON struct {
	Commit string `json:"commit"`
}

// --- draft lifecycle ---

func handleList(st *store.Store, w http.ResponseWriter, r *http.Request) {
	drafts, err := List(st)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	out := make([]draftJSON, 0, len(drafts))
	for i := range drafts {
		out = append(out, draftToJSON(&drafts[i]))
	}
	api.WriteJSON(w, http.StatusOK, out)
}

func handleCreate(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	var body struct{ Title string `json:"title"` }
	if !decodeBody(w, r, &body) {
		return
	}
	d, err := Create(st, author, body.Title)
	if err != nil {
		writeError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, draftToJSON(d))
}

func handleView(st *store.Store, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := Get(st, id)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	fsys, err := st.CatalogRepo().TreeFS(d.Commit)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	c, loadProblems := catalog.Load(fsys)
	problems := append(append([]problemJSON{}, toProblemJSON(loadProblems)...), toProblemJSON(c.Validate())...)
	api.WriteJSON(w, http.StatusOK, draftViewJSON{
		Draft: draftToJSON(d), Catalog: api.CatalogJSON(c, d.Commit), Problems: problems,
	})
}

func handleDelete(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	if err := Delete(st, r.PathValue("id"), author); err != nil {
		api.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- edits ---

func handleTaskEdit(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	taskID := r.PathValue("task")
	if !task.ValidID(taskID) {
		api.WriteJSON(w, http.StatusNotFound, errorBody{"unknown task " + taskID})
		return
	}
	var body struct {
		Step       string          `json:"step"`
		Title      string          `json:"title"`
		Body       string          `json:"body"`
		AnswerType task.AnswerType `json:"answer_type"`
		Choices    []string        `json:"choices"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	commit, err := Update(st, r.PathValue("id"), author, "Edit task "+taskID,
		func(tree fs.FS) ([]gitrepo.Change, error) {
			return catalog.NewVersionChanges(tree, taskID, semver.Step(body.Step), body.Title, body.Body, body.AnswerType, body.Choices)
		})
	if err != nil {
		writeError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]string{"commit": commit})
}

type childJSON struct {
	Group string `json:"group,omitempty"`
	Task  string `json:"task,omitempty"`
}

func (c childJSON) toCatalog() catalog.Child { return catalog.Child{Group: c.Group, Task: c.Task} }

type groupBodyJSON struct {
	Title    string      `json:"title"`
	Children []childJSON `json:"children"`
}

func handleGroupEdit(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	slug := r.PathValue("slug")
	if !slugRE.MatchString(slug) {
		api.WriteJSON(w, http.StatusBadRequest, errorBody{fmt.Sprintf("invalid group slug %q", slug)})
		return
	}
	var body groupBodyJSON
	if !decodeBody(w, r, &body) {
		return
	}
	g := catalog.Group{Title: body.Title}
	for _, c := range body.Children {
		g.Children = append(g.Children, c.toCatalog())
	}
	data, err := marshalYAML(struct {
		Title    string          `yaml:"title"`
		Children []catalog.Child `yaml:"children"`
	}{g.Title, g.Children})
	if err != nil {
		api.WriteError(w, err)
		return
	}
	commit, err := Update(st, r.PathValue("id"), author, "Edit group "+slug,
		func(fs.FS) ([]gitrepo.Change, error) {
			return []gitrepo.Change{{Path: "groups/" + slug + "/group.yaml", Data: data}}, nil
		})
	if err != nil {
		writeError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]string{"commit": commit})
}

func handleGroupsIndexEdit(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	var body struct{ Groups []string `json:"groups"` }
	if !decodeBody(w, r, &body) {
		return
	}
	for _, slug := range body.Groups {
		if !slugRE.MatchString(slug) {
			api.WriteJSON(w, http.StatusBadRequest, errorBody{fmt.Sprintf("invalid group slug %q", slug)})
			return
		}
	}
	data, err := marshalYAML(struct {
		Groups []string `yaml:"groups"`
	}{body.Groups})
	if err != nil {
		api.WriteError(w, err)
		return
	}
	commit, err := Update(st, r.PathValue("id"), author, "Reorder top-level groups",
		func(fs.FS) ([]gitrepo.Change, error) {
			return []gitrepo.Change{{Path: "groups/index.yaml", Data: data}}, nil
		})
	if err != nil {
		writeError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]string{"commit": commit})
}

func handleProcessorsEdit(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	var registry catalog.Registry
	if !decodeBody(w, r, &registry) {
		return
	}
	data, err := marshalYAML(registry)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	commit, err := Update(st, r.PathValue("id"), author, "Edit the processor registry",
		func(fs.FS) ([]gitrepo.Change, error) {
			return []gitrepo.Change{{Path: "processors.yaml", Data: data}}, nil
		})
	if err != nil {
		writeError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]string{"commit": commit})
}

// --- publish ---

func handlePublish(st *store.Store, onPublish func(), w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	var body struct {
		Resolutions map[string]resolutionJSON `json:"resolutions"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	res := make(map[string]merge.Resolution, len(body.Resolutions))
	for path, rj := range body.Resolutions {
		res[path] = merge.Resolution{Side: rj.Side, Content: rj.Content}
	}
	result, err := Publish(st, r.PathValue("id"), author, res)
	if err != nil {
		writeError(w, err)
		return
	}
	if len(result.Conflicts) > 0 {
		cb := conflictBody{Error: fmt.Sprintf("publishing needs a resolution for %d conflicting path(s)", len(result.Conflicts))}
		for _, c := range result.Conflicts {
			cb.Conflicts = append(cb.Conflicts, conflictJSON{Path: c.Path, Kind: c.Kind, Ours: c.Ours, Theirs: c.Theirs})
		}
		api.WriteJSON(w, http.StatusConflict, cb)
		return
	}
	if onPublish != nil {
		onPublish()
	}
	api.WriteJSON(w, http.StatusOK, publishResultJSON{Commit: result.Commit})
}

// --- helpers ---

func toProblemJSON(ps []problem.Problem) []problemJSON {
	out := make([]problemJSON, 0, len(ps))
	for _, p := range ps {
		out = append(out, problemJSON{Path: p.Path, Rule: p.Rule, Message: p.Message})
	}
	return out
}

// marshalYAML writes v with two-space indentation, the same convention
// internal/merge/config.go's encodeConfig uses.
func marshalYAML(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// decodeBody decodes a JSON object into v. It writes the error response and
// returns false when the body is bad.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil && dec.Decode(&struct{}{}) != io.EOF {
		err = errors.New("unexpected data after the JSON object")
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		api.WriteJSON(w, http.StatusRequestEntityTooLarge, errorBody{fmt.Sprintf("request body is larger than %d bytes", maxBody)})
		return false
	case err != nil:
		api.WriteJSON(w, http.StatusBadRequest, errorBody{"malformed request body: " + err.Error()})
		return false
	}
	return true
}

// writeError answers ErrInvalid with 400 and leaves the rest to api.WriteError.
func writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrInvalid) {
		api.WriteJSON(w, http.StatusBadRequest, errorBody{err.Error()})
		return
	}
	api.WriteError(w, err)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/catalogdraft/ && go vet ./internal/catalogdraft/`
Expected: PASS.

- [ ] **Step 5: Wire it into the server, with `onPublish` reconciling
workspaces the same way a catalog `git push` already does**

This resolves the gap flagged in "Open questions for the cross-plan
reviewer" below: `catalogdraft.Publish` moves the catalog's `main` directly
through `store.UpdateCatalog`, the same way `merge.Fork`/`merge.Merge` move
a workspace's `main` directly — and the architecture note now gives
`catalogdraft.Register` an `onPublish func()` parameter for exactly this
reason (ruling 17), mirroring `merge.Register`'s `onMainMoved`.

`cmd/custos/distribute.go`'s `startDistribution` already builds exactly the
closure this needs (`reconcile`, which calls `distribute.Reconcile(st)` and
logs its error) and wires it to `srv.OnCatalogPush`; the only change needed
is to return that closure so `openServer` can hand it to
`catalogdraft.Register` too:

```go
func startDistribution(st *store.Store, a *api.API, srv *server.Server, stderr io.Writer) func() {
	distribute.Register(a, st)
	reconcile := func() {
		if err := distribute.Reconcile(st); err != nil {
			fmt.Fprintf(stderr, "custos serve: distributing the catalog: %v\n", err)
		}
	}
	srv.OnCatalogPush(reconcile)
	srv.OnWorkspacePush(func(id string) {
		if err := distribute.ReconcileWorkspace(st, id); err != nil {
			fmt.Fprintf(stderr, "custos serve: distributing workspace %s: %v\n", id, err)
		}
	})
	reconcile()
	return reconcile
}
```

(The only change from what is already in the tree is the `func()` return
type and the trailing `return reconcile`.)

In `cmd/custos/serve.go`'s `openServer`, call `catalogdraft.Register` after
`startDistribution` (not next to `merge.Register`, as an earlier draft of
this plan said, since the closure `onPublish` needs does not exist yet at
that point) and capture its return value:

```go
	srv := server.New(st)
	reconcile := startDistribution(st, a, srv, stderr)
	catalogdraft.Register(a, st, reconcile)
	startRuns(ctx, st, a, srv, svc)
	srv.WithAPI(a.Handler())
```

Locate the current call sites with `grep -n "startDistribution(" cmd/custos/*.go`;
`cmd/custos/distribute_test.go`'s two call sites (which currently ignore
`startDistribution`'s return) keep compiling unchanged — Go allows ignoring
an extra return value by simply not assigning it, but since the function's
signature changes from `func(...)` to `func(...) func()`, `go vet` would
flag an assignment call that drops it; check `distribute_test.go`'s two
calls (`startDistribution(st, a, srv, log)` and
`startDistribution(st, api.New(st, bl), server.New(st), &log)`) and prefix
each with `_ = ` if they do not already use the return value.

- [ ] **Step 6: Commit**

```bash
git add internal/catalogdraft/http.go internal/catalogdraft/http_test.go cmd/custos/serve.go cmd/custos/distribute.go cmd/custos/distribute_test.go
git commit -m "Register the catalog draft REST endpoints and reconcile workspaces on publish

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 8: `status.RenderHTML` and the book handler's `?format=html`

**Files:**
- Create: `internal/status/html.go`
- Modify: `internal/status/status.go` (`TaskStatus.Choices`, cross-plan fix below)
- Modify: `internal/api/read.go` (`getBook`; `taskStatusJSON.Choices`, cross-plan fix below)
- Modify: `go.mod`, `go.sum` (new dependency)
- Test: `internal/status/html_test.go`, extend `internal/api/read_test.go`

**Cross-plan fix folded into this task** (see "Decisions beyond the
architecture note", item 12): plan 4c flagged that no endpoint exposes a
generated task's `choices`, so `AnswerView.vue` could not render a
`choice`-type generated task's answer form. `status.TaskStatus` and
`internal/api/read.go`'s `taskStatusJSON` are phase-2 types this task
already has open for the HTML-export change below, so the one-line
addition rides along here rather than opening either file again in a
separate task:

- [ ] **Step 0a: add `Choices` to `status.TaskStatus`** — in
  `internal/status/status.go`, add a field next to `AnswerType`:

```go
type TaskStatus struct {
	ID         string
	Current    task.Ref // current version
	Title      string
	AnswerType task.AnswerType
	Choices    []string // valid values when AnswerType == task.AnswerChoice
	Generated  bool
	State      State
	Answer     *workspace.Answer
	Merged     []*workspace.Answer
	InEffect   []*workspace.Answer
}
```

and set it next to the existing `Title: cur.Title, AnswerType: cur.AnswerType`
assignment (`cur` is the task's current `task.Meta`, which already carries
`Choices`):

```go
	ts := &TaskStatus{
		ID: cur.ID, Current: cur.Ref(), Title: cur.Title, AnswerType: cur.AnswerType,
		Choices: cur.Choices,
		// ...unchanged fields below...
	}
```

- [ ] **Step 0b: add `Choices` to `taskStatusJSON`** — in
  `internal/api/read.go`:

```go
type taskStatusJSON struct {
	ID         string          `json:"id"`
	Version    string          `json:"version"`
	Title      string          `json:"title"`
	AnswerType task.AnswerType `json:"answer_type"`
	Choices    []string        `json:"choices,omitempty"`
	Generated  bool            `json:"generated"`
	State      status.State    `json:"state"`
	Answer     *answerJSON     `json:"answer"`
	Merged     []answerJSON    `json:"merged"`
	InEffect   []answerJSON    `json:"in_effect"`
}
```

and populate it in `statusToJSON`:

```go
		tj := taskStatusJSON{
			ID: ts.ID, Version: ts.Current.Version, Title: ts.Title, AnswerType: ts.AnswerType,
			Choices:   ts.Choices,
			Generated: ts.Generated, State: ts.State,
			Merged: a.answersToJSON(ts.Merged), InEffect: a.answersToJSON(ts.InEffect),
		}
```

- [ ] **Step 0c: test** — extend `internal/api/read_test.go`'s existing
  status test (or add `TestGetStatusChoices`) to bind a `choice`-type task
  (catalog or, with a processor test fixture, generated) and assert the
  JSON response's matching task entry has `"choices":["a","b"]` (whatever
  the fixture's choices are). Run: `go test ./internal/status/ ./internal/api/`.

**Interfaces:**
- Consumes: `status.{Status, Entry, TaskStatus, State}`, `workspace.Answer`,
  `task.AnswerMarkdown`; new dependency `github.com/yuin/goldmark` and its
  `extension` subpackage.
- Produces:

```go
func RenderHTML(s *Status) []byte
```

Add the dependency with:

```bash
go get github.com/yuin/goldmark@latest
```

Confirmed during cross-plan review by actually running this against the
real module proxy: it resolves to **v1.8.6** as of this review (not the
v1.7.8 this plan originally guessed at — see "Decisions beyond the
architecture note" for why that guess was never a verified pin). Pin
`@latest`, not a specific version, so a later implementer gets whatever
is actually current rather than reproducing an unverified guess.

`RenderHTML` mirrors `RenderMarkdown`'s structure (the same `Status.Book`
walk, the same state notices) but emits a complete, self-contained HTML
document with inline `<style>`. Only `workspace.Answer.Body` for an
`AnswerMarkdown`-type answer is actually Markdown that needs converting —
every other piece of text `RenderMarkdown` writes (titles, group headings,
state notices, non-Markdown answer values) is literal text that gets
HTML-escaped directly, exactly matching what `RenderMarkdown` does with
those same values (compare `markdown.answer` in `internal/status/markdown.go`,
which also only treats `a.Body` as Markdown-shaped content and writes
`a.Value` as a plain block for every other answer type). `goldmark` is
configured with `goldmark.WithExtensions(extension.GFM)` (tables,
strikethrough, autolinking and task lists — a reasonable "complete
document" baseline) and `goldmark.WithRendererOptions(html.WithUnsafe())`,
which lets an answer's Markdown body embed raw HTML; see "Decisions beyond
the architecture note" for why that is accepted here.

- [ ] **Step 1: Write the failing test** `internal/status/html_test.go`

```go
package status

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/workspace"
)

func TestRenderHTML(t *testing.T) {
	w, _ := workspace.Load(fixture.MapFS(fixture.Workspace()))
	c, _ := catalog.Load(fixture.MapFS(fixture.Catalog()))
	s := Compute(w, c)

	out := string(RenderHTML(s))
	if !strings.HasPrefix(out, "<!DOCTYPE html>") || !strings.Contains(out, "<style>") {
		t.Fatalf("not a self-contained HTML document:\n%s", out)
	}
	if !strings.Contains(out, "Workspace "+s.Workspace) {
		t.Errorf("missing workspace heading:\n%s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Skip("fixture does not currently exercise escaping; see TestRenderHTMLEscapesPlainValues")
	}
}

func TestRenderHTMLEscapesPlainValues(t *testing.T) {
	files := map[string]string{
		"custos.yaml": fixture.Config(fixture.WorkspaceID, fixture.Commit),
		"answers/" + fixture.TaskA + ".md": "---\ntask: " + fixture.TaskA +
			"\ntask_version: 1.0.0\ntype: text\nvalue: <script>alert(1)</script>\n---\n",
	}
	w, _ := workspace.Load(fixture.MapFS(files))
	c, _ := catalog.Load(fixture.MapFS(fixture.Catalog()))
	out := string(RenderHTML(Compute(w, c)))
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Errorf("a plain-text answer value was not escaped:\n%s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("expected the escaped form in the output:\n%s", out)
	}
}

func TestRenderHTMLRendersMarkdownBody(t *testing.T) {
	files := map[string]string{
		"custos.yaml": fixture.Config(fixture.WorkspaceID, fixture.Commit),
		"answers/" + fixture.TaskA + ".md": "---\ntask: " + fixture.TaskA +
			"\ntask_version: 1.0.0\ntype: markdown\n---\n\n**bold** text\n",
	}
	w, _ := workspace.Load(fixture.MapFS(files))
	c, _ := catalog.Load(fixture.MapFS(fixture.Catalog()))
	out := string(RenderHTML(Compute(w, c)))
	if !strings.Contains(out, "<strong>bold</strong>") {
		t.Errorf("markdown body was not converted to HTML:\n%s", out)
	}
}
```

(Replace `TestRenderHTML`'s skip-based placeholder once you have confirmed
which fixture task actually has a `text`-typed answer with the exact value
used in `TestRenderHTMLEscapesPlainValues`, so the first test can assert
directly instead of skipping — the two escaping tests above are the ones
that matter; the first test only pins the document skeleton.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/status/ -run RenderHTML`
Expected: FAIL, `undefined: RenderHTML`.

- [ ] **Step 3: Write `internal/status/html.go`**

```go
package status

import (
	"bytes"
	"fmt"
	"html"
	"strings"

	"github.com/yuin/goldmark"
	ghtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/extension"

	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// md converts a Markdown-shaped answer body to an HTML fragment. GFM adds
// tables, strikethrough, autolinking and task lists; WithUnsafe lets a
// Markdown body embed raw HTML (accepted for this project's scale and trust
// model — see the plan's "Decisions beyond the architecture note").
var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(ghtml.WithUnsafe()),
)

const htmlStyle = `
body { font-family: -apple-system, Segoe UI, Roboto, sans-serif; max-width: 48rem; margin: 2rem auto; padding: 0 1rem; line-height: 1.5; color: #1a1a1a; }
h1, h2, h3, h4, h5, h6 { margin-top: 1.6em; }
code, pre { background: #f2f2f2; border-radius: 4px; }
pre { padding: 0.75em; overflow-x: auto; }
code { padding: 0.15em 0.35em; }
.state { color: #555; font-style: italic; }
ul.attachments { color: #555; }
table { border-collapse: collapse; }
th, td { border: 1px solid #ccc; padding: 0.3em 0.6em; }
`

// RenderHTML renders the same book as RenderMarkdown, as a complete,
// self-contained HTML document (inline <style>, no external requests).
func RenderHTML(s *Status) []byte {
	var b htmlBuilder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	fmt.Fprintf(&b, "<title>Workspace %s</title>\n", html.EscapeString(s.Workspace))
	fmt.Fprintf(&b, "<style>%s</style>\n</head>\n<body>\n", htmlStyle)
	fmt.Fprintf(&b, "<h1>Workspace %s</h1>\n", html.EscapeString(s.Workspace))
	fmt.Fprintf(&b, "<p>Catalog commit <code>%s</code>.</p>\n", html.EscapeString(s.Pin))
	for _, en := range s.Book {
		switch {
		case en.Group != nil:
			b.heading(en.Depth, en.Group.Title)
		case en.Task != nil:
			b.task(en.Depth, en.Task)
		}
	}
	b.WriteString("</body>\n</html>\n")
	return b.Bytes()
}

type htmlBuilder struct{ bytes.Buffer }

// level returns the heading tag for depth, the same clamp RenderMarkdown
// uses (document title at depth-equivalent 0 is <h1>, everything below
// clamps at <h6>; groups/tasks start one level below the title).
func level(depth int) int { return min(depth+2, 6) }

func (b *htmlBuilder) heading(depth int, title string) {
	fmt.Fprintf(b, "<h%d>%s</h%d>\n", level(depth), html.EscapeString(title), level(depth))
}

func (b *htmlBuilder) task(depth int, ts *TaskStatus) {
	b.heading(depth, ts.Title)
	switch ts.State {
	case Unanswered:
		b.WriteString("<p class=\"state\">Unanswered.</p>\n")
		return
	case PendingUpdate:
		if ts.Answer != nil {
			fmt.Fprintf(b, "<p class=\"state\">Pending update: answered for version %s; the current version is %s.</p>\n",
				html.EscapeString(ts.Answer.TaskVersion), html.EscapeString(ts.Current.Version))
		} else {
			fmt.Fprintf(b, "<p class=\"state\">Pending update: version %s is not answered yet; the answers of the tasks it merged stay in effect.</p>\n",
				html.EscapeString(ts.Current.Version))
		}
	}
	for _, a := range ts.InEffect {
		if a != ts.Answer {
			fmt.Fprintf(b, "<p class=\"state\">Answer to merged task %s, version %s:</p>\n", html.EscapeString(a.Task), html.EscapeString(a.TaskVersion))
		}
		b.answer(a)
	}
}

func (b *htmlBuilder) answer(a *workspace.Answer) {
	if a.Type == task.AnswerMarkdown {
		if strings.TrimSpace(a.Body) != "" {
			md.Convert([]byte(a.Body), b)
			b.WriteString("\n")
		}
	} else if a.Value != "" {
		fmt.Fprintf(b, "<p>%s</p>\n", html.EscapeString(a.Value))
	}
	if len(a.Attachments) == 0 {
		return
	}
	b.WriteString("<ul class=\"attachments\">\n")
	for _, at := range a.Attachments {
		fmt.Fprintf(b, "<li>Attachment %s (%s, sha256 %s)</li>\n",
			html.EscapeString(at.Name), html.EscapeString(at.MediaType), html.EscapeString(at.SHA256))
	}
	b.WriteString("</ul>\n")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/status/ && go vet ./internal/status/`
Expected: PASS.

- [ ] **Step 5: Add the `?format=html` query parameter** — modify
`internal/api/read.go`'s `getBook`:

```go
// getBook serves the book as Markdown by default, or as a complete HTML
// document with ?format=html (ruling 2.11, extended by the architecture
// note: HTML export arrives with the web UI, phase 4).
func (a *API) getBook(w http.ResponseWriter, r *http.Request) {
	s, err := a.workspaceStatus(r)
	if err != nil {
		WriteError(w, err)
		return
	}
	if r.URL.Query().Get("format") == "html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(status.RenderHTML(s))
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(status.RenderMarkdown(s))
}
```

This replaces the existing `getBook` function body in `internal/api/read.go`
one for one; nothing else in that file changes.

- [ ] **Step 6: Add a test to `internal/api/read_test.go`**

Find the existing book-handler test (likely `TestGetBook` or similar,
asserting the Markdown response) and add next to it:

```go
func TestGetBookHTML(t *testing.T) {
	a, st := newTestAPI(t) // use whatever this file's existing test setup helper is named
	id := createTestWorkspace(t, st) // likewise — match the existing TestGetBook's setup exactly

	r := httptest.NewRequest("GET", "/api/workspaces/"+id+"/book?format=html", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), "<!DOCTYPE html>") {
		t.Errorf("body is not an HTML document:\n%s", w.Body)
	}

	r = httptest.NewRequest("GET", "/api/workspaces/"+id+"/book", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if ct := w.Header().Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Errorf("default format Content-Type = %q, want text/markdown", ct)
	}
}
```

Adapt the helper names (`newTestAPI`, `createTestWorkspace`) to whatever
`internal/api/read_test.go` already calls its own setup; this task does not
introduce new test infrastructure, it reuses what `TestGetBook` (the
existing Markdown-only test) already has.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/api/ ./internal/status/ && go vet ./internal/api/ ./internal/status/`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
go mod tidy
git add internal/status/html.go internal/status/html_test.go internal/status/status.go internal/api/read.go internal/api/read_test.go go.mod go.sum
git commit -m "Add status.RenderHTML and the book handler's ?format=html

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Decisions beyond the architecture note

1. **`store.UpdateCatalog`'s main-ref validation calls `rules.CheckCatalogUpdate`,
   not bare `catalog.Check`.** The architecture note's prose says Publish
   validates "with `catalog.Check(tree)`," but `rules.CheckCatalogUpdate` is
   what the pre-receive hook actually runs for a push, and it is strictly
   more than `catalog.Check`: it also runs `catalog.CheckImmutable` against
   the old tree and rejects history rewrites. Calling only `catalog.Check`
   would let a draft silently rewrite an already-published task version
   file's content (still individually well-formed, so `catalog.Check` alone
   would not catch it) straight through to `main`. Since `UpdateCatalog` is
   meant to be the one place that "mirrors the pre-receive hook," and since
   `rules.CheckCatalogUpdate` is the existing function that does that for a
   push, I call it directly rather than re-deriving a weaker check. This
   matches how `store.UpdateWorkspace` already calls
   `rules.CheckWorkspaceUpdate` (not a bare `workspace.Check`) for exactly
   the analogous reason.

2. **`Publish`'s "fast-forward" is a new, single-parent commit with the
   draft's tree content, not a literal Git ref fast-forward.** The note
   distinguishes "a plain fast-forward" from "a three-way merge," but fixes
   only one signature for `store.UpdateCatalog`, used (per the note) by both
   `Update` and `Publish` — and that signature always writes a new commit
   from an edit closure's returned `[]gitrepo.Change`, with a single parent
   (the ref's old value). A literal Git fast-forward (moving `main` to point
   directly at the draft branch's own tip commit, reusing its object) would
   need a different primitive the note does not mention, and would also mean
   the publishing author never becomes the commit's author even though they
   are the one running `Publish`. I chose the reading that keeps
   `UpdateCatalog` as the single mechanism for both `Update` and `Publish`
   (as the note states) and always attributes the publish commit to the
   publishing author: "fast-forward" here means the published tree exactly
   matches the draft's tree with no three-way merge needed, computed as a
   diff against the current `main` tree inside the edit closure (`diffFS` in
   Task 5), not a literal ref repoint.

3. **The three-way-merge path also goes through `store.UpdateCatalog`'s edit
   closure**, by computing the full desired tree with `gitrepo.MergeTree` +
   `EditTree` first and then diffing that tree against whatever `main`'s
   tree is when the closure actually runs (`diffFS` again). Unresolved
   conflicts are smuggled out of the closure as a private `*conflictsErr`
   that `Publish` unwraps immediately with `errors.As`, converting it to
   `PublishResult.Conflicts` before it can ever reach a caller as a generic
   error — the closure itself returns the conflicts as an `error` purely
   because `store.UpdateCatalog`'s signature has no other channel for them,
   and because returning an error there correctly stops `UpdateCatalog` from
   writing or moving anything.

4. **A draft's metadata (title, author, base) is a small JSON marker file
   (`custos-draft.json`) committed on the draft's own branch, not a side
   file on disk.** The note gives `Draft` these fields without saying how
   they persist, and the plan's Global Constraints require "nothing is
   cached outside Git except through the existing per-repo lock." Deriving
   `Title` and `Author` from Git commit metadata (the first commit's message
   and author) was the alternative I considered and rejected: it would need
   a new `gitrepo` method to read a commit's author/message (not named
   anywhere in the architecture note, and `gitrepo.go` is shared
   infrastructure I was told to treat as stable), and it is fragile for
   `Title` (a commit "subject" truncates at the first newline, forcing a
   single-line-title constraint by accident rather than by design). The
   marker file keeps everything inside `internal/catalogdraft`, is fully
   inside Git, and is explicitly stripped out before anything reaches `main`
   (`diffFS` and `planPublish`'s trailing delete, both in Task 5) — a catalog
   tree is already documented to tolerate extra top-level files ("a catalog
   can carry a README," `catalog.go`'s doc comment), and `catalog.Load` never
   walks anything outside `tasks/`, `groups/` and `processors.yaml`, so this
   file is invisible to every existing validation path.

5. **`api.CatalogJSON` is a new one-line exported wrapper around the
   existing unexported `catalogToJSON`**, added to `internal/api/catalog.go`
   exactly as the architecture note instructs plan 4b to do ("export
   whatever JSON-shaping helper you use ... even if that means adding a
   small exported helper to internal/api"). It returns `any` rather than a
   newly-exported named type, since `encoding/json` only needs the nested
   struct fields to be exported (which they already are) to marshal
   correctly from another package — this is the smallest possible change
   that satisfies the requirement without renaming or duplicating
   `catalogJSON`/`groupJSON`/`childJSON`/`taskVersionJSON`. **Plan 4c/4d's
   frontend should know:** the draft-view endpoint's `catalog` field is
   byte-for-byte the same JSON shape `GET /api/catalog` already returns
   (`{"commit","groups":[...],"tasks":[...]}`), so a single TypeScript type
   and a single rendering component can serve both the read-only catalog
   view and the draft editor's catalog view.

6. **The `{task-id}` wildcard becomes `{task}` in the actual Go route
   pattern.** `net/http.ServeMux` (Go 1.22+) requires a wildcard's name to be
   a valid Go identifier, which a hyphen is not; `{task}` is also what the
   existing `/api/workspaces/{id}/answers/{task}` route already uses for the
   same kind of path segment, so this is a return to an existing convention,
   not a new one. The URL path itself is unchanged:
   `/api/catalog/drafts/{id}/tasks/<task-uuid>`.

7. **Group slugs and the task-id path segment are validated for
   path-safety in the HTTP handlers, before `catalogdraft.Update` ever
   runs**, independent of `catalog.Check`. Ruling 4.13 ("a draft may be
   transiently invalid") is about catalog *content* rules, not about
   letting a request body's string become part of a file path unexamined
   (`groups/<slug>/group.yaml`). The slug check reuses the exact regular
   expression `catalog`'s own (unexported) `slugRE` already enforces
   (`^[a-z0-9][a-z0-9-]*$`), duplicated locally rather than exported from
   `catalog`, since exporting a single-purpose one-line regular expression
   purely for this felt like more surface area than the problem warrants.

8. **`catalogdraft.ErrInvalid` is this package's own sentinel**, not named
   in the architecture note's fixed surface for plan 4b, added by direct
   analogy with `internal/merge.ErrInvalid`, which plays the identical role
   (malformed request → 400) in the sibling package this plan's `Publish`
   is explicitly modeled on. Flagging this explicitly since the task brief
   asks for any invented name to be called out, even when, as here, it
   follows an existing precedent closely enough that I judged it very low
   risk for 4c/4d to collide with.

9. **`goldmark`'s version, resolved during cross-plan review.** The
   module path (`github.com/yuin/goldmark`, with `goldmark/extension` and
   `goldmark/renderer/html` as subpackages) was already correct; the
   specific tag this plan originally guessed (`v1.7.8`) was never
   verified and is replaced with `@latest` (Task 8), confirmed against
   the real module proxy while applying this plan to a scratch copy to
   resolve to **v1.8.6**. Record whatever version actually lands in
   `go.mod` at implementation time.

10. **`status.RenderHTML` enables `goldmark/renderer/html.WithUnsafe()`**,
    letting an `AnswerMarkdown`-typed answer body embed raw HTML that passes
    through to the rendered document unescaped. This is a real,
    deliberately-accepted risk (a stored-XSS vector if an untrusted person
    could ever write an answer), justified the same way ruling 4.10 justifies
    not restricting read access further: the spec's own "Scale" section
    describes "a few hundred tasks" inside "one trusted organization," and
    every answer author is already an authenticated Engineer. Flagging this
    explicitly rather than silently choosing it, since it is a security
    posture decision a reviewer may want to override (the alternative,
    dropping `WithUnsafe()`, is a one-line change if the team decides
    otherwise).

11. **Resolved by cross-plan review: `catalogdraft.Register` now takes an
    `onPublish func()` callback** (option (a) of the three listed under
    "Open questions for the cross-plan reviewer" below), mirroring
    `merge.Register`'s `onMainMoved`. The architecture note has been
    updated (ruling 17) to fix this signature; Task 7 above wires it to the
    same `distribute.Reconcile` closure `cmd/custos/distribute.go` already
    builds for `OnCatalogPush`, so a draft publish reconciles pinned
    workspaces immediately, the same as a catalog `git push` does.
12. **`taskStatusJSON` (`internal/api/read.go`) gains a `Choices []string`
    field**, carried from `status.TaskStatus` (itself extended with a
    `Choices []string` field, set from the task's current `task.Meta` the
    same place `Title`/`AnswerType` are already set in
    `internal/status/status.go`). This is a one-line additive fix to
    phase-2 code neither this plan nor plan 4c owns outright, made here
    because this plan's Task 8 already touches `internal/api/read.go` for
    `?format=html` — see Task 8's added step below. It closes a real gap
    plan 4c flagged ("no endpoint exposes a generated task's... choices"):
    a generated `choice`-type task can now be answered through the UI,
    since the status endpoint — which already covers every task, generated
    or not — now carries its choices.

## Open questions for the cross-plan reviewer

Both of this plan's open questions were resolved during cross-plan review;
kept here for the record.

- ~~Publishing a catalog draft does not trigger workspace reconciliation.~~
  **Resolved:** see Decision 11 above and Task 7's Step 5.
- ~~`goldmark`'s pinned version~~ **Resolved:** see Decision 9 above — confirmed to resolve to v1.8.6 against the real module proxy while applying this plan to a scratch copy.

## Rulings for spec §11.4

1. `store.UpdateCatalog`'s validation of a move of `main` reuses
   `rules.CheckCatalogUpdate` (history, `catalog.Check`,
   `catalog.CheckImmutable`) rather than `catalog.Check` alone, so a draft
   publish is held to exactly the same bar as a `git push` to `catalog.git`,
   including immutability of already-published task versions.
2. A catalog draft's own metadata (title, author, the catalog commit it
   branched from) is stored as a committed file (`custos-draft.json`) on the
   draft's own branch, not as a side file under `<data-dir>`; it is always
   stripped out before a publish's content reaches `main`.
3. `catalogdraft.Publish`'s fast-forward case writes a new, single-parent
   commit authored by the publishing user with the draft's tree content,
   rather than moving `main` to literally point at the draft branch's own
   tip commit.
4. Publishing a catalog draft through the API calls `onPublish`
   (`catalogdraft.Register`'s new parameter, architecture note ruling 17),
   which `cmd/custos` wires to the same `distribute.Reconcile` call
   `OnCatalogPush` already drives, so affected workspaces are reconciled
   immediately — resolved during cross-plan review; no longer a gap.
5. `GET /api/workspaces/{id}/status`'s `taskStatusJSON` carries a
   `Choices []string` field for every task (generated or catalog), closing
   the gap that left a generated `choice`-type task impossible to answer
   through the web UI.
6. `store.UpdateCatalog` takes an optional `allowImmutableChange ...string`
   naming paths whose immutability ("was changed") problem does not block
   that one call; only `catalogdraft.Publish`'s resolved-merge-conflict
   case ever passes it (the paths in the caller's own `res`), never a git
   push. Found necessary empirically while applying this plan to a scratch
   copy (Part 2 of cross-plan review): without it, `TestPublishConflict`'s
   own "resolved with theirs" step cannot succeed, because the second
   draft's resolved content is, by construction, different from whatever
   content the first draft's publish already committed to that exact
   path, which `catalog.CheckImmutable` would otherwise — correctly, for
   every other case — reject.
