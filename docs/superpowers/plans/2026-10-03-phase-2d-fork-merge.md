# Phase 2d (Workspace Fork and Merge) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fork a workspace into a new repository with a new id, and merge a branch of a workspace into its `main` with custos-aware conflict handling (answers, generated tasks and documents by choosing a side, pins by catalog ancestry), validated like any `main` update, over Go functions and REST endpoints.

**Architecture:** A new package `internal/merge` holds `Fork`, `Merge`, `Branches` and their HTTP handlers (`merge.Register`). It works on the bare repositories through `internal/store` (plan 2a) and new plumbing helpers in `internal/gitrepo`: `MergeTree` (around `git merge-tree --write-tree -z --no-messages`), `MergeBase`, `HasCommit`, `Fetch` (copies a ref's history between local repositories) and `EditTree` (applies changes to a tree in a temporary index). `Merge` computes the merge without a lock and moves `main` with `store.SetRef` (compare-and-swap plus validation); `Fork` holds the new workspace's lock and validates with `rules.CheckWorkspaceUpdate` before it sets `main`.

**Tech Stack:** Go 1.26, git ≥ 2.38 at runtime, `go.yaml.in/yaml/v3`, `github.com/google/uuid`, standard library.

**Spec:** [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md) — §4.4 (fork and merge), §4.5 (concurrency), §7 (error handling), and the rulings in §11, which override earlier sections (here 2.3, 2.7, 2.9, 2.10). Shared names and conventions: [docs/superpowers/plans/2026-10-03-phase-2-architecture.md](2026-10-03-phase-2-architecture.md), section "Plan 2d".

**Requires plans 2a, 2b and 2c** to be merged first. This plan uses, exactly as the architecture note names them: `gitrepo.Signature`, `gitrepo.Bot`, `gitrepo.ErrRefMoved`, `gitrepo.Change`, `gitrepo.CommitRequest`, `(*gitrepo.Repo).ResolveRef/ReadFile/WriteCommit/CommitTree/UpdateRef/DeleteRef/Refs`, `rules.CheckWorkspaceUpdate`, the `store` package (`Open`, `CreateWorkspace`, `CreateWorkspaceRepo`, `WorkspaceRepo`, `CatalogRepo`, `CatalogURL`, `DataDir`, `Lock`, `UpdateWorkspace`, `SetRef`, `ErrNotFound`, `ErrExists`, `ErrConflict`, `RejectedError`), `problem.RuleAnswer`, `problem.RulePin`, `blobs.Open`, and `api.New`, `(*api.API).Handle/Handler`, `api.Author`, `api.WriteJSON`, `api.WriteError`. None of these exist on `main` today; do not start before they do.

## Global Constraints

- Module `github.com/emeland-io/custos`, Go 1.26. Third-party modules stay `go.yaml.in/yaml/v3`, `golang.org/x/mod`, `github.com/google/uuid`; standard library otherwise (HTTP routing with Go 1.22+ `http.ServeMux` patterns).
- Git ≥ 2.38 at runtime (ruling 2.9). Use no git option newer than 2.38 (for example, not `git merge-tree --merge-base`, which is 2.40).
- Tests use real git in temp dirs through `internal/gittest`.
- Server-side changes are written with Git plumbing on the bare repositories (temporary index, `commit-tree`, `update-ref` with the expected old value), one lock per repository, and validated with the same rules as the pre-receive hook before the ref moves (ruling 2.3). Never use the repository's index or an inherited `GIT_INDEX_FILE`.
- The committer of every server-side commit is `gitrepo.Bot` (`custos-bot <custos-bot@localhost>`); the author is the acting person.
- One content-addressed blob store serves all workspaces (ruling 2.7): a fork copies no blobs.
- Until phase 3, merge conflicts in generated tasks and documents are resolved by choosing one side, like answer conflicts (ruling 2.10).
- Workspace layout: `custos.yaml`, `answers/<task-uuid>.md`, `generated/<uuid>/<semver>.md`, `documents/<uuid>.json`. Workspace ids are lowercase UUID v4 (`task.ValidID`).
- REST: JSON bodies; error body `{"error": "<message>", "problems": [...]}`; 400 malformed request, 401 missing/invalid `X-Custos-Author` on a write, 404 unknown workspace/branch, 409 conflict (ref moved, merge conflicts, workspace exists), 413 too large, 422 validation problems, 500 otherwise.
- Every write endpoint requires `X-Custos-Author: Name <email>` (ruling 2.6), parsed by `api.Author`.
- Exported names and endpoints of plan 2d exactly as in the architecture note: `merge.Fork`, `merge.Merge`, `merge.Resolution`, `merge.Conflict`, `merge.Result`; `POST /api/workspaces/{id}/fork`, `POST /api/workspaces/{id}/merge`, `GET /api/workspaces/{id}/branches`.

## Review Focus

1. **Merging a fork back.** A fork's `main` pushed into the original workspace as a branch carries the fork's id in `custos.yaml`; a plain Git merge would take it and the merge would be rejected. Expected: `main` keeps its own workspace id and the fork's answers arrive (test `TestMergeKeepsMainWorkspaceID`, Task 4).
2. **One side deleted a file the other changed** (an answer removed on `main`, edited on the branch). Expected: a conflict whose `Ours` is `nil`; choosing `ours` deletes the file (test `TestMergeModifyDeleteConflict`, Task 3).
3. **Resolving only some conflicts.** Expected: nothing changes, and only the still unresolved paths come back (test `TestMergePartialResolution`, Task 3).
4. **A fork that fails half-way** (for example, the source's `main` was broken by a manual edit on disk, §7). Expected: no half-created workspace remains that would block a retry with the same id (test `TestForkOfInvalidMainLeavesNothingBehind`, Task 2).
5. **A branch that shares no history with `main`** (an orphan branch pushed by hand). Expected: a clear 409, not a 500 (test `TestMergeUnrelatedHistories`, Task 3).

---

### Task 1: Git plumbing for merging and copying history

Verified with git 2.55 (`git merge-tree --write-tree -z --no-messages OURS THEIRS`):

- Exit 0 = clean merge, output `<tree-oid>NUL`.
- Exit 1 = conflicts, output `<tree-oid>NUL` followed by one record per conflicted stage, `<mode> SP <blob-oid> SP <stage> TAB <path>NUL` (stage 1 = merge base, 2 = OURS, 3 = THEIRS; a missing stage means the file does not exist on that side). With `--no-messages` there is no further section. The written tree holds conflict markers in conflicted files.
- Exit 1 with empty output = an argument is not a commit (message on stderr).
- Exit 128 = unrelated histories ("refusing to merge unrelated histories").
- `git merge-base A B` exits 1 when there is no common ancestor; `git rev-parse --verify --quiet X^{commit}` exits 1 when X is missing or not a commit; `git fetch` from a local path into a bare repository with `+SRC:DST` copies the history and writes only DST.

**Files:**
- Create: `internal/gitrepo/merge.go`
- Test: `internal/gitrepo/merge_test.go`

**Interfaces:**
- Consumes: from `internal/gitrepo/gitrepo.go` (phase 1) the unexported `splitNUL(out []byte) []string` and `(r *Repo) readBlobs(oids []string) ([][]byte, error)`; from plan 2a `type Change struct{ Path string; Data []byte; Delete bool }` and the unexported `(r *Repo) command(env []string, args ...string) *exec.Cmd`, which runs `git -C r.Dir` with the environment isolated unless `Repo.InheritGitEnv` is set.
- Produces:

```go
type ConflictedFile struct {
	Path               string
	Base, Ours, Theirs []byte // nil when the file does not exist in that stage
}
type MergeTreeResult struct {
	Tree      string           // merged tree; conflicted files hold conflict markers
	Conflicts []ConflictedFile // sorted by path; empty for a clean merge
}
func (r *Repo) MergeTree(ours, theirs string) (*MergeTreeResult, error)
func (r *Repo) MergeBase(a, b string) (oid string, ok bool, err error) // ok=false: no common history
func (r *Repo) HasCommit(oid string) (bool, error)
func (r *Repo) Fetch(src *Repo, srcRef, dstRef string) (string, error) // copies srcRef of src into r as dstRef (forced), returns its oid
func (r *Repo) EditTree(tree string, changes []Change) (string, error) // tree must be a full oid
```

All unexported helpers in this file start with `merge` or `checkMerge` so they cannot collide with helpers plan 2a added to the package.

- [ ] **Step 1: Write the failing tests** `internal/gitrepo/merge_test.go`

```go
package gitrepo

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/gittest"
)

// sameFile compares file contents; nil (no file) differs from empty.
func sameFile(a, b []byte) bool {
	return (a == nil) == (b == nil) && bytes.Equal(a, b)
}

// mergeFixture makes a work repository where branch theirs and main both
// changed a.txt and added c.txt with different content, and theirs deleted
// d.txt while main changed it.
func mergeFixture(t *testing.T) (r *Repo, base, ours, theirs string) {
	t.Helper()
	dir := gittest.Init(t)
	base = gittest.Commit(t, dir, map[string]string{"a.txt": "base\n", "d.txt": "d\n", "keep.txt": "k\n"})
	gittest.Run(t, dir, "checkout", "-q", "-b", "theirs")
	if err := os.Remove(filepath.Join(dir, "d.txt")); err != nil {
		t.Fatal(err)
	}
	theirs = gittest.Commit(t, dir, map[string]string{"a.txt": "theirs\n", "c.txt": "c theirs\n"})
	gittest.Run(t, dir, "checkout", "-q", "main")
	ours = gittest.Commit(t, dir, map[string]string{"a.txt": "ours\n", "c.txt": "c ours\n", "d.txt": "d changed\n"})
	return &Repo{Dir: dir}, base, ours, theirs
}

// orphan commits a root commit on a new branch solo and returns it.
func orphan(t *testing.T, dir string) string {
	t.Helper()
	gittest.Run(t, dir, "checkout", "-q", "--orphan", "solo")
	c := gittest.Commit(t, dir, map[string]string{"solo.txt": "solo\n"})
	gittest.Run(t, dir, "checkout", "-q", "main")
	return c
}

func TestMergeTreeConflicts(t *testing.T) {
	r, _, ours, theirs := mergeFixture(t)
	res, err := r.MergeTree(ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if !mergeOIDRE.MatchString(res.Tree) {
		t.Errorf("tree = %q", res.Tree)
	}
	want := []ConflictedFile{
		{Path: "a.txt", Base: []byte("base\n"), Ours: []byte("ours\n"), Theirs: []byte("theirs\n")},
		{Path: "c.txt", Ours: []byte("c ours\n"), Theirs: []byte("c theirs\n")},
		{Path: "d.txt", Base: []byte("d\n"), Ours: []byte("d changed\n")},
	}
	if len(res.Conflicts) != len(want) {
		t.Fatalf("conflicts = %+v, want %d", res.Conflicts, len(want))
	}
	for i, w := range want {
		g := res.Conflicts[i]
		if g.Path != w.Path || !sameFile(g.Base, w.Base) || !sameFile(g.Ours, w.Ours) || !sameFile(g.Theirs, w.Theirs) {
			t.Errorf("conflict %d = %s base %q ours %q theirs %q, want %s base %q ours %q theirs %q",
				i, g.Path, g.Base, g.Ours, g.Theirs, w.Path, w.Base, w.Ours, w.Theirs)
		}
	}
}

func TestMergeTreeClean(t *testing.T) {
	dir := gittest.Init(t)
	gittest.Commit(t, dir, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	gittest.Run(t, dir, "checkout", "-q", "-b", "side")
	theirs := gittest.Commit(t, dir, map[string]string{"c.txt": "c\n"})
	gittest.Run(t, dir, "checkout", "-q", "main")
	ours := gittest.Commit(t, dir, map[string]string{"a.txt": "a2\n"})
	r := &Repo{Dir: dir}

	res, err := r.MergeTree(ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts %+v", res.Conflicts)
	}
	fsys, err := r.TreeFS(res.Tree)
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{"a.txt": "a2\n", "b.txt": "b\n", "c.txt": "c\n"} {
		if data, err := fs.ReadFile(fsys, p); err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", p, data, err, want)
		}
	}
}

func TestMergeTreeErrors(t *testing.T) {
	r, _, ours, _ := mergeFixture(t)
	if _, err := r.MergeTree("-x", ours); err == nil {
		t.Error("revisions starting with - must be rejected")
	}
	if _, err := r.MergeTree(ours, "does-not-exist"); err == nil {
		t.Error("unknown revision must fail")
	}
	solo := orphan(t, r.Dir)
	if _, err := r.MergeTree(ours, solo); err == nil {
		t.Error("unrelated histories must fail")
	}
}

func TestMergeBase(t *testing.T) {
	r, base, ours, theirs := mergeFixture(t)
	got, ok, err := r.MergeBase(ours, theirs)
	if err != nil || !ok || got != base {
		t.Errorf("MergeBase = %q, %v, %v; want %s", got, ok, err, base)
	}
	solo := orphan(t, r.Dir)
	if got, ok, err := r.MergeBase(ours, solo); err != nil || ok {
		t.Errorf("MergeBase of unrelated commits = %q, %v, %v; want no base, no error", got, ok, err)
	}
	if _, _, err := r.MergeBase("-x", ours); err == nil {
		t.Error("revisions starting with - must be rejected")
	}
}

func TestHasCommit(t *testing.T) {
	r, base, _, _ := mergeFixture(t)
	blob := gittest.Run(t, r.Dir, "rev-parse", base+":a.txt")
	for _, tc := range []struct {
		oid  string
		want bool
	}{{base, true}, {blob, false}, {strings.Repeat("1", 40), false}} {
		if got, err := r.HasCommit(tc.oid); err != nil || got != tc.want {
			t.Errorf("HasCommit(%s) = %v, %v; want %v", tc.oid, got, err, tc.want)
		}
	}
	if _, err := r.HasCommit("-x"); err == nil {
		t.Error("revisions starting with - must be rejected")
	}
}

func TestFetch(t *testing.T) {
	work := gittest.Init(t)
	first := gittest.Commit(t, work, map[string]string{"a.txt": "one"})
	second := gittest.Commit(t, work, map[string]string{"a.txt": "two"})
	src, err := InitBare(filepath.Join(t.TempDir(), "src.git"))
	if err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, work, "push", "--quiet", src.Dir, "main")
	dst, err := InitBare(filepath.Join(t.TempDir(), "dst.git"))
	if err != nil {
		t.Fatal(err)
	}

	oid, err := dst.Fetch(src, "refs/heads/main", "refs/custos/tmp")
	if err != nil || oid != second {
		t.Fatalf("Fetch = %q, %v; want %s", oid, err, second)
	}
	if got := gittest.Run(t, dst.Dir, "rev-parse", "refs/custos/tmp~1"); got != first {
		t.Errorf("history not copied: refs/custos/tmp~1 = %s, want %s", got, first)
	}
	if got := gittest.Run(t, dst.Dir, "for-each-ref", "--format=%(refname)"); got != "refs/custos/tmp" {
		t.Errorf("refs after fetch = %q, want only refs/custos/tmp", got)
	}
	if _, err := dst.Fetch(src, "refs/heads/nope", "refs/custos/x"); err == nil {
		t.Error("fetching a missing ref must fail")
	}
	if _, err := dst.Fetch(src, "main", "refs/custos/x"); err == nil {
		t.Error("short ref names must be rejected")
	}
}

func TestEditTree(t *testing.T) {
	dir := gittest.Init(t)
	c := gittest.Commit(t, dir, map[string]string{"a.txt": "one", "b.txt": "two"})
	tree := gittest.Run(t, dir, "rev-parse", c+"^{tree}")
	indexPath := filepath.Join(dir, ".git", "index")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(t.TempDir(), "inherited-index")
	t.Setenv("GIT_INDEX_FILE", stray)
	r := &Repo{Dir: dir}

	got, err := r.EditTree(tree, []Change{
		{Path: "a.txt", Data: []byte("changed")},
		{Path: "b.txt", Delete: true},
		{Path: "sub/c.txt", Data: []byte("three")},
		{Path: "empty.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	fsys, err := r.TreeFS(got)
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{"a.txt": "changed", "sub/c.txt": "three", "empty.txt": ""} {
		if data, err := fs.ReadFile(fsys, p); err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", p, data, err, want)
		}
	}
	if _, err := fs.Stat(fsys, "b.txt"); err == nil {
		t.Error("b.txt was not deleted")
	}
	old, err := r.TreeFS(tree)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := fs.ReadFile(old, "a.txt"); string(data) != "one" {
		t.Errorf("original tree changed: a.txt = %q", data)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Error("EditTree used the inherited GIT_INDEX_FILE")
	}
	if indexAfter, _ := os.ReadFile(indexPath); !bytes.Equal(indexBefore, indexAfter) {
		t.Error("EditTree changed the repository's index")
	}
	if _, err := r.EditTree("HEAD", nil); err == nil {
		t.Error("EditTree must require a full tree id")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/gitrepo/ -run 'MergeTree|MergeBase|HasCommit|Fetch|EditTree'`
Expected: FAIL, `undefined: mergeOIDRE`, `r.MergeTree undefined` and similar.

- [ ] **Step 3: Write `internal/gitrepo/merge.go`**

```go
package gitrepo

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var mergeOIDRE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// ConflictedFile is a path that git could not merge. Base, Ours and Theirs
// hold the file in the merge base and on each side; nil means the file does
// not exist there, for example because that side deleted it.
type ConflictedFile struct {
	Path               string
	Base, Ours, Theirs []byte
}

// MergeTreeResult is the outcome of MergeTree.
type MergeTreeResult struct {
	Tree      string           // merged tree; conflicted files hold conflict markers
	Conflicts []ConflictedFile // sorted by path; empty for a clean merge
}

// MergeTree merges commit theirs into commit ours without a work tree
// (git merge-tree --write-tree, git 2.38 or later) and writes the merged
// tree into the repository. Commits without common history are an error.
// Conflicts on symlinks or submodules are an error, because custos only
// stores regular files.
func (r *Repo) MergeTree(ours, theirs string) (*MergeTreeResult, error) {
	if err := checkMergeRevs(ours, theirs); err != nil {
		return nil, err
	}
	out, code, err := r.mergeGit(nil, nil, "merge-tree", "--write-tree", "-z", "--no-messages", ours, theirs)
	if err != nil && code != 1 {
		return nil, err
	}
	recs := splitNUL(out)
	if len(recs) == 0 || !mergeOIDRE.MatchString(recs[0]) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected git merge-tree output %q", out)
	}
	res := &MergeTreeResult{Tree: recs[0]}
	if code == 0 {
		return res, nil
	}
	// One record per conflicted stage: <mode> SP <oid> SP <stage> TAB <path>.
	stages := map[string]*[4]string{}
	var paths []string
	for _, rec := range recs[1:] {
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("unexpected git merge-tree output %q", rec)
		}
		if f[0] != "100644" && f[0] != "100755" {
			return nil, fmt.Errorf("%s: only regular files can be merged, not mode %s", path, f[0])
		}
		stage, serr := strconv.Atoi(f[2])
		if serr != nil || stage < 1 || stage > 3 {
			return nil, fmt.Errorf("unexpected stage in git merge-tree output %q", rec)
		}
		s := stages[path]
		if s == nil {
			s = new([4]string)
			stages[path] = s
			paths = append(paths, path)
		}
		s[stage] = f[1]
	}
	if len(paths) == 0 {
		return nil, errors.New("git merge-tree reported a conflict but no conflicted file")
	}
	slices.Sort(paths)
	var oids []string
	for _, p := range paths {
		for _, oid := range stages[p][1:] {
			if oid != "" {
				oids = append(oids, oid)
			}
		}
	}
	blobs, err := r.readBlobs(oids)
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		c := ConflictedFile{Path: p}
		for stage, oid := range stages[p] {
			if oid == "" {
				continue
			}
			data := blobs[0]
			blobs = blobs[1:]
			switch stage {
			case 1:
				c.Base = data
			case 2:
				c.Ours = data
			case 3:
				c.Theirs = data
			}
		}
		res.Conflicts = append(res.Conflicts, c)
	}
	return res, nil
}

// MergeBase returns the best common ancestor of commits a and b. ok is
// false when they share no history.
func (r *Repo) MergeBase(a, b string) (oid string, ok bool, err error) {
	if err := checkMergeRevs(a, b); err != nil {
		return "", false, err
	}
	out, code, err := r.mergeGit(nil, nil, "merge-base", a, b)
	if code == 1 {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(out)), true, nil
}

// HasCommit reports whether oid names a commit in the repository.
func (r *Repo) HasCommit(oid string) (bool, error) {
	if err := checkMergeRevs(oid); err != nil {
		return false, err
	}
	_, code, err := r.mergeGit(nil, nil, "rev-parse", "--verify", "--quiet", oid+"^{commit}")
	if code == 1 {
		return false, nil
	}
	return err == nil, err
}

// Fetch copies srcRef of the local repository src, with its history, into r
// as dstRef (replacing dstRef if it exists) and returns the copied commit.
// No other ref of r changes. Both names must be full ref names.
func (r *Repo) Fetch(src *Repo, srcRef, dstRef string) (string, error) {
	for _, ref := range []string{srcRef, dstRef} {
		if !strings.HasPrefix(ref, "refs/") || strings.ContainsAny(ref, ": \t\n") {
			return "", fmt.Errorf("%q is not a full ref name", ref)
		}
	}
	from, err := filepath.Abs(src.Dir)
	if err != nil {
		return "", err
	}
	if _, _, err := r.mergeGit(nil, nil, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", from, "+"+srcRef+":"+dstRef); err != nil {
		return "", err
	}
	out, _, err := r.mergeGit(nil, nil, "rev-parse", "--verify", dstRef)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// EditTree applies changes to tree and writes the resulting tree. tree must
// be a full object id. It uses a temporary index, never the repository's
// index or an inherited GIT_INDEX_FILE. Files are written with mode 100644.
func (r *Repo) EditTree(tree string, changes []Change) (string, error) {
	if !mergeOIDRE.MatchString(tree) {
		return "", fmt.Errorf("EditTree needs a full tree id, not %q", tree)
	}
	tmp, err := os.MkdirTemp("", "custos-tree-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	if _, _, err := r.mergeGit(env, nil, "read-tree", tree); err != nil {
		return "", err
	}
	zero := strings.Repeat("0", len(tree))
	var info bytes.Buffer
	for _, c := range changes {
		if c.Path == "" || strings.ContainsRune(c.Path, 0) {
			return "", fmt.Errorf("invalid path %q", c.Path)
		}
		if c.Delete {
			fmt.Fprintf(&info, "0 %s\t%s\x00", zero, c.Path)
			continue
		}
		out, _, err := r.mergeGit(nil, c.Data, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&info, "100644 %s\t%s\x00", strings.TrimSpace(string(out)), c.Path)
	}
	if info.Len() > 0 {
		if _, _, err := r.mergeGit(env, info.Bytes(), "update-index", "-z", "--index-info"); err != nil {
			return "", err
		}
	}
	out, _, err := r.mergeGit(env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// checkMergeRevs rejects revisions git would read as options.
func checkMergeRevs(revs ...string) error {
	for _, rev := range revs {
		if rev == "" || strings.HasPrefix(rev, "-") {
			return fmt.Errorf("invalid revision %q", rev)
		}
	}
	return nil
}

// mergeGit runs git in r with extra environment variables and standard
// input. It returns standard output and the exit code (0 on success, -1 when
// git did not run); the error is non-nil for any non-zero exit. It uses
// r.command (plan 2a), so variables such as GIT_DIR, GIT_OBJECT_DIRECTORY
// and GIT_INDEX_FILE are dropped unless r.InheritGitEnv is set.
func (r *Repo) mergeGit(env []string, stdin []byte, args ...string) ([]byte, int, error) {
	cmd := r.command(env, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), 0, nil
	}
	code := -1
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	}
	return stdout.Bytes(), code, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/gitrepo/`
Expected: PASS (all phase 1 and 2a tests too).

- [ ] **Step 5: Commit**

```bash
git add internal/gitrepo/merge.go internal/gitrepo/merge_test.go
git commit -m "Add git plumbing for merge-tree, merge-base, fetch and tree edits"
```

---

### Task 2: Fork a workspace

**Files:**
- Create: `internal/merge/doc.go`
- Create: `internal/merge/config.go`
- Create: `internal/merge/fork.go`
- Test: `internal/merge/helpers_test.go` (shared test helpers for Tasks 2–5)
- Test: `internal/merge/fork_test.go`

**Interfaces:**
- Consumes: Task 1 `(*gitrepo.Repo).Fetch`; plan 2a `store.Store` methods `WorkspaceRepo(id) (*gitrepo.Repo, error)`, `CreateWorkspaceRepo(id) (*gitrepo.Repo, error)`, `Lock(repo string) func()`, `CatalogRepo() *gitrepo.Repo`, `UpdateWorkspace(id, ref string, author gitrepo.Signature, message string, edit func(tree fs.FS) ([]gitrepo.Change, error)) (string, error)`, `CreateWorkspace(id string, author gitrepo.Signature) error`, `CatalogURL() string`; `store.ErrNotFound`, `store.ErrExists`, `store.ErrConflict`, `*store.RejectedError{Problems}`; `rules.CheckWorkspaceUpdate(ws, cat *gitrepo.Repo, id, oldOID, newOID string) ([]problem.Problem, error)`; `(*gitrepo.Repo).ResolveRef/ReadFile/WriteCommit/UpdateRef/DeleteRef`; `gitrepo.ErrRefMoved`.
- Produces:

```go
// package merge
var ErrInvalid = errors.New("invalid request") // 400 in the REST API
const mainRef = "refs/heads/main"                 // unexported
const configPath = "custos.yaml"                  // unexported
func Fork(st *store.Store, srcID, newID string, author gitrepo.Signature) error
func decodeConfig(data []byte) (workspace.Config, error) // unexported; nil data is an error
func encodeConfig(c workspace.Config) ([]byte, error)    // unexported; two-space YAML
```

Test helpers (package `merge`, `helpers_test.go`), used by Tasks 2–5: `alice`, `ws`, `forkID`, `unknownTask`, `whatIf`, `answerA`, `answerB`, `type env struct{ st *store.Store; catWork string; cat []string }`, `setup(t) *env`, `(*env).advanceCatalog(t, files) string`, `(*env).draftCatalog(t, from, files) string`, `(*env).config(id, pin string, frozen bool) string`, `commitFiles(t, st, id, ref, files) string`, `repoOf(t, st, id) *gitrepo.Repo`, `ref(t, st, id, name) string`, `file(t, st, id, rev, path) (string, bool)`, `gitOut(t, st, id, args...) string`, `readConfig(t, e, id, rev) workspace.Config`, `textAnswer(taskID, version, value string) string`, `hasRule(ps, rule) bool`.

- [ ] **Step 1: Write the test helpers** `internal/merge/helpers_test.go`

```go
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
```

- [ ] **Step 2: Write the failing tests** `internal/merge/fork_test.go`

```go
package merge

import (
	"errors"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/store"
)

func TestForkCopiesMainAndSetsID(t *testing.T) {
	e := setup(t)
	answer := textAnswer(fixture.TaskA, "1.1.0", "built by make")
	srcMain := commitFiles(t, e.st, ws, mainRef, map[string]string{answerA: answer})
	commitFiles(t, e.st, ws, whatIf, map[string]string{answerB: textAnswer(fixture.TaskB, "1.0.0", "draft")})

	if err := Fork(e.st, ws, forkID, alice); err != nil {
		t.Fatal(err)
	}

	forkMain := ref(t, e.st, forkID, mainRef)
	if got := gitOut(t, e.st, forkID, "rev-parse", forkMain+"^"); got != srcMain {
		t.Errorf("parent of the fork commit = %s, want the source's main %s", got, srcMain)
	}
	if got := gitOut(t, e.st, forkID, "log", "-1", "--format=%an <%ae>|%cn", forkMain); got != "Alice Example <alice@example.org>|custos-bot" {
		t.Errorf("author|committer = %q", got)
	}
	cfg, srcCfg := readConfig(t, e, forkID, forkMain), readConfig(t, e, ws, srcMain)
	if cfg.Workspace != forkID || cfg.Catalog != srcCfg.Catalog || cfg.Frozen != srcCfg.Frozen {
		t.Errorf("fork custos.yaml = %+v, want the source's %+v with workspace %s", cfg, srcCfg, forkID)
	}
	if got, ok := file(t, e.st, forkID, forkMain, answerA); !ok || got != answer {
		t.Errorf("answer in fork = %q (present %v), want %q", got, ok, answer)
	}
	if refs := gitOut(t, e.st, forkID, "for-each-ref", "--format=%(refname)"); refs != "refs/heads/main" {
		t.Errorf("fork refs = %q, want only refs/heads/main", refs)
	}
	if got := ref(t, e.st, ws, mainRef); got != srcMain {
		t.Errorf("source main moved to %s", got)
	}
}

func TestForkUnknownSource(t *testing.T) {
	e := setup(t)
	if err := Fork(e.st, unknownTask, forkID, alice); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Fork from unknown workspace: %v, want ErrNotFound", err)
	}
	if _, err := e.st.WorkspaceRepo(forkID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("fork repository exists after a failed fork: %v", err)
	}
}

func TestForkOntoExistingWorkspace(t *testing.T) {
	e := setup(t)
	if err := Fork(e.st, ws, forkID, alice); err != nil {
		t.Fatal(err)
	}
	forkMain, srcMain := ref(t, e.st, forkID, mainRef), ref(t, e.st, ws, mainRef)
	for _, target := range []string{forkID, ws} {
		if err := Fork(e.st, ws, target, alice); !errors.Is(err, store.ErrExists) {
			t.Errorf("Fork onto %s: %v, want ErrExists", target, err)
		}
	}
	if got := ref(t, e.st, forkID, mainRef); got != forkMain {
		t.Errorf("existing fork's main moved to %s", got)
	}
	if got := ref(t, e.st, ws, mainRef); got != srcMain {
		t.Errorf("source main moved to %s", got)
	}
}

func TestForkInvalidID(t *testing.T) {
	e := setup(t)
	for _, id := range []string{"", "not-a-uuid", strings.ToUpper(forkID), "../" + forkID} {
		if err := Fork(e.st, ws, id, alice); !errors.Is(err, ErrInvalid) {
			t.Errorf("Fork to %q: %v, want ErrInvalid", id, err)
		}
	}
}

func TestForkOfInvalidMainLeavesNothingBehind(t *testing.T) {
	e := setup(t)
	src := repoOf(t, e.st, ws)
	old := ref(t, e.st, ws, mainRef)
	// A manual edit on disk broke the source's main (spec §7).
	bad, err := src.WriteCommit(gitrepo.CommitRequest{
		Base:    old,
		Parents: []string{old},
		Changes: []gitrepo.Change{{Path: "answers/" + unknownTask + ".md", Data: []byte(textAnswer(unknownTask, "1.0.0", "x"))}},
		Author:  alice,
		Message: "bypass validation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := src.UpdateRef(mainRef, bad, old); err != nil {
		t.Fatal(err)
	}

	err = Fork(e.st, ws, forkID, alice)
	var rej *store.RejectedError
	if !errors.As(err, &rej) || !hasRule(rej.Problems, problem.RuleAnswer) {
		t.Fatalf("Fork of an invalid main: %v, want rejection with rule %s", err, problem.RuleAnswer)
	}
	if _, err := e.st.WorkspaceRepo(forkID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("half-created fork left behind: %v", err)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/merge/`
Expected: FAIL, `undefined: Fork`, `undefined: mainRef`, `undefined: decodeConfig`.

- [ ] **Step 4: Write `internal/merge/doc.go`**

```go
// Package merge forks workspaces and merges branches into a workspace's
// main branch with custos-aware conflict handling (spec §4.4).
package merge

import "errors"

// ErrInvalid reports a malformed fork or merge request: a bad workspace id,
// branch name or resolution. The REST API answers it with 400.
var ErrInvalid = errors.New("invalid request")

const (
	mainRef    = "refs/heads/main"
	configPath = "custos.yaml"
)
```

- [ ] **Step 5: Write `internal/merge/config.go`**

```go
package merge

import (
	"bytes"
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/workspace"
)

// decodeConfig parses custos.yaml strictly, like workspace.Load. nil data
// (no file) is an error.
func decodeConfig(data []byte) (workspace.Config, error) {
	var c workspace.Config
	if data == nil {
		return c, errors.New("custos.yaml is missing")
	}
	if err := frontmatter.DecodeStrict(bytes.TrimPrefix(data, []byte("\ufeff")), &c); err != nil {
		return workspace.Config{}, fmt.Errorf("custos.yaml: %w", err)
	}
	return c, nil
}

// encodeConfig writes custos.yaml with two-space indentation.
func encodeConfig(c workspace.Config) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
```

- [ ] **Step 6: Write `internal/merge/fork.go`**

```go
package merge

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/rules"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

// forkRef holds the source's main in the new repository until the fork
// commit is ready.
const forkRef = "refs/custos/fork-source"

// forkMu serialises forks, so two forks to the same new id cannot both
// pass the existence check and then remove each other's repository.
var forkMu sync.Mutex

// Fork creates workspace newID from the history of srcID's main branch: a
// new repository (store.CreateWorkspaceRepo) whose main is one commit, by
// author, on top of srcID's main that sets the workspace id in custos.yaml.
// Branches are not copied. All workspaces share one blob store (ruling
// 2.7), so no blobs are copied. The new main is validated like any main
// update; when a step fails, the new repository is removed again.
func Fork(st *store.Store, srcID, newID string, author gitrepo.Signature) error {
	if !task.ValidID(newID) {
		return fmt.Errorf("%w: workspace id %q is not a lowercase UUID v4", ErrInvalid, newID)
	}
	src, err := st.WorkspaceRepo(srcID)
	if err != nil {
		return err
	}
	if _, ok, err := src.ResolveRef(mainRef); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("workspace %s has no main branch: %w", srcID, store.ErrNotFound)
	}

	forkMu.Lock()
	defer forkMu.Unlock()
	if _, err := st.WorkspaceRepo(newID); err == nil {
		return fmt.Errorf("workspace %s: %w", newID, store.ErrExists)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	dst, err := st.CreateWorkspaceRepo(newID)
	if err != nil {
		return err
	}
	// Hold the new workspace's lock until main is set, so that in-process
	// writers (catalog distribution) never see the half-made repository's
	// missing main as their base.
	unlock := st.Lock(newID)
	defer unlock()
	if err := forkInto(st, src, dst, srcID, newID, author); err != nil {
		if rmErr := os.RemoveAll(dst.Dir); rmErr != nil {
			return errors.Join(err, rmErr)
		}
		return err
	}
	return nil
}

// forkInto copies src's main into the empty repository dst and sets dst's
// main to a commit that names newID in custos.yaml.
func forkInto(st *store.Store, src, dst *gitrepo.Repo, srcID, newID string, author gitrepo.Signature) error {
	base, err := dst.Fetch(src, mainRef, forkRef)
	if err != nil {
		return err
	}
	data, ok, err := dst.ReadFile(base, configPath)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("workspace %s: custos.yaml is missing on main", srcID)
	}
	cfg, err := decodeConfig(data)
	if err != nil {
		return fmt.Errorf("workspace %s: %w", srcID, err)
	}
	cfg.Workspace = newID
	out, err := encodeConfig(cfg)
	if err != nil {
		return err
	}
	commit, err := dst.WriteCommit(gitrepo.CommitRequest{
		Base:    base,
		Parents: []string{base},
		Changes: []gitrepo.Change{{Path: configPath, Data: out}},
		Author:  author,
		Message: fmt.Sprintf("Fork workspace %s as %s", srcID, newID),
	})
	if err != nil {
		return err
	}
	ps, err := rules.CheckWorkspaceUpdate(dst, st.CatalogRepo(), newID, "", commit)
	if err != nil {
		return err
	}
	if len(ps) > 0 {
		return &store.RejectedError{Problems: ps}
	}
	if err := dst.DeleteRef(forkRef, base); err != nil {
		return err
	}
	if err := dst.UpdateRef(mainRef, commit, ""); err != nil {
		if errors.Is(err, gitrepo.ErrRefMoved) {
			return fmt.Errorf("%w: %v", store.ErrConflict, err)
		}
		return err
	}
	return nil
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/merge/`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/merge/doc.go internal/merge/config.go internal/merge/fork.go internal/merge/helpers_test.go internal/merge/fork_test.go
git commit -m "Fork a workspace into a new repository with a new id"
```

---

### Task 3: Merge a branch into main

**Files:**
- Create: `internal/merge/merge.go`
- Test: `internal/merge/merge_test.go`

**Interfaces:**
- Consumes: Task 1 `MergeTree`, `MergeBase`, `EditTree`, `ConflictedFile`, `MergeTreeResult`; Task 2 `ErrInvalid`, `mainRef`, `configPath`; plan 2a `store.SetRef(id, ref, newOID, oldOID string) error` (validates main, compare-and-swap, `ErrConflict` when moved), `(*gitrepo.Repo).ResolveRef`, `IsAncestor`, `CommitTree(tree string, parents []string, author gitrepo.Signature, message string) (string, error)`.
- Produces (exported names as in the architecture note):

```go
const SideOurs, SideTheirs, SideContent = "ours", "theirs", "content"
const KindAnswer, KindGenerated, KindDocument, KindConfig, KindOther = "answer", "generated", "document", "custos.yaml", "other"
type Resolution struct{ Side string; Content []byte }
type Conflict struct{ Path string; Ours, Theirs []byte; Kind string }
type Result struct{ Commit string; Conflicts []Conflict }
func Merge(st *store.Store, id, branch string, author gitrepo.Signature, res map[string]Resolution) (*Result, error)
// unexported, replaced in Task 4:
func planMerge(st *store.Store, repo *gitrepo.Repo, base, ours, theirs string, mt *gitrepo.MergeTreeResult, res map[string]Resolution) (*plan, error)
type plan struct{ changes []gitrepo.Change; conflicts []Conflict; used map[string]bool }
func (p *plan) settle(path string, ours, theirs []byte, res map[string]Resolution) error
func (p *plan) finish(res map[string]Resolution) error
var beforeUpdate = func() {} // test hook between writing the merge commit and moving main
```

`branch` is a short branch name such as `what-if` (not `refs/heads/what-if`).

- [ ] **Step 1: Write the failing tests** `internal/merge/merge_test.go`

```go
package merge

import (
	"errors"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/store"
)

// diverge commits base to main (unless nil), then theirs to branch what-if
// (which starts at main) and ours to main. It returns main before the merge.
func diverge(t *testing.T, e *env, base, ours, theirs map[string]string) string {
	t.Helper()
	if base != nil {
		commitFiles(t, e.st, ws, mainRef, base)
	}
	commitFiles(t, e.st, ws, whatIf, theirs)
	return commitFiles(t, e.st, ws, mainRef, ours)
}

func TestMergeClean(t *testing.T) {
	e := setup(t)
	ours, theirs := textAnswer(fixture.TaskA, "1.1.0", "ours"), textAnswer(fixture.TaskB, "1.0.0", "theirs")
	before := diverge(t, e, nil, map[string]string{answerA: ours}, map[string]string{answerB: theirs})
	branch := ref(t, e.st, ws, whatIf)

	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 || res.Commit == "" {
		t.Fatalf("result %+v, want a merge commit", res)
	}
	if got := ref(t, e.st, ws, mainRef); got != res.Commit {
		t.Errorf("main = %s, want the merge commit %s", got, res.Commit)
	}
	if got := gitOut(t, e.st, ws, "rev-parse", res.Commit+"^1", res.Commit+"^2"); got != before+"\n"+branch {
		t.Errorf("parents = %q, want main then branch", got)
	}
	if got := gitOut(t, e.st, ws, "log", "-1", "--format=%an <%ae>|%cn|%s", res.Commit); got != "Alice Example <alice@example.org>|custos-bot|Merge branch 'what-if'" {
		t.Errorf("author|committer|subject = %q", got)
	}
	for path, want := range map[string]string{answerA: ours, answerB: theirs} {
		if got, ok := file(t, e.st, ws, res.Commit, path); !ok || got != want {
			t.Errorf("%s = %q (present %v), want %q", path, got, ok, want)
		}
	}
}

func TestMergeBranchAheadAndUpToDate(t *testing.T) {
	e := setup(t)
	commitFiles(t, e.st, ws, whatIf, map[string]string{answerB: textAnswer(fixture.TaskB, "1.0.0", "theirs")})

	first, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if parents := strings.Fields(gitOut(t, e.st, ws, "rev-list", "--parents", "-n", "1", first.Commit)); len(parents) != 3 {
		t.Errorf("a branch ahead of main gets a merge commit with two parents; got %v", parents)
	}
	second, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Commit != first.Commit || ref(t, e.st, ws, mainRef) != first.Commit {
		t.Errorf("merging a merged branch again = %+v, want main unchanged at %s", second, first.Commit)
	}
}

func TestMergeAnswerConflict(t *testing.T) {
	e := setup(t)
	ours, theirs := textAnswer(fixture.TaskA, "1.1.0", "ours"), textAnswer(fixture.TaskA, "1.1.0", "theirs")
	before := diverge(t, e,
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "base")},
		map[string]string{answerA: ours},
		map[string]string{answerA: theirs})

	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != "" || len(res.Conflicts) != 1 {
		t.Fatalf("result %+v, want one conflict and no commit", res)
	}
	c := res.Conflicts[0]
	if c.Path != answerA || c.Kind != KindAnswer || string(c.Ours) != ours || string(c.Theirs) != theirs {
		t.Errorf("conflict = %s %s ours %q theirs %q", c.Path, c.Kind, c.Ours, c.Theirs)
	}
	if got := ref(t, e.st, ws, mainRef); got != before {
		t.Errorf("main moved to %s despite conflicts", got)
	}
}

func TestMergeResolutions(t *testing.T) {
	ours, theirs := textAnswer(fixture.TaskA, "1.1.0", "ours"), textAnswer(fixture.TaskA, "1.1.0", "theirs")
	resolved := textAnswer(fixture.TaskA, "1.1.0", "both")
	for _, tc := range []struct {
		name string
		res  Resolution
		want string
	}{
		{"ours", Resolution{Side: SideOurs}, ours},
		{"theirs", Resolution{Side: SideTheirs}, theirs},
		{"content", Resolution{Side: SideContent, Content: []byte(resolved)}, resolved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			diverge(t, e,
				map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "base")},
				map[string]string{answerA: ours},
				map[string]string{answerA: theirs})
			res, err := Merge(e.st, ws, "what-if", alice, map[string]Resolution{answerA: tc.res})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Conflicts) != 0 || ref(t, e.st, ws, mainRef) != res.Commit {
				t.Fatalf("result %+v, want merged", res)
			}
			if got, _ := file(t, e.st, ws, res.Commit, answerA); got != tc.want {
				t.Errorf("answer = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMergeModifyDeleteConflict(t *testing.T) {
	e := setup(t)
	theirs := textAnswer(fixture.TaskA, "1.1.0", "theirs")
	diverge(t, e,
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "base")},
		map[string]string{answerA: ""}, // main deletes the answer
		map[string]string{answerA: theirs})

	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Ours != nil || string(res.Conflicts[0].Theirs) != theirs {
		t.Fatalf("result %+v, want one conflict with no file on main", res)
	}
	res, err = Merge(e.st, ws, "what-if", alice, map[string]Resolution{answerA: {Side: SideOurs}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := file(t, e.st, ws, res.Commit, answerA); ok {
		t.Error("choosing the deleting side must delete the answer")
	}
}

func TestMergeGeneratedAndDocumentConflicts(t *testing.T) {
	// Ruling 2.10: until phase 3 these are resolved by choosing a side.
	e := setup(t)
	gen := "generated/" + fixture.TaskC + "/1.0.0.md"
	doc := "documents/" + fixture.DocID + ".json"
	genOn := func(side string) string {
		return strings.Replace(fixture.GeneratedFile, "title: Host web-01", "title: Host web-01 ("+side+")", 1)
	}
	docOn := func(side string) string {
		return strings.Replace(fixture.DocumentFile, `"name":"provenance"`, `"name":"provenance-`+side+`"`, 1)
	}
	diverge(t, e,
		map[string]string{gen: fixture.GeneratedFile, doc: fixture.DocumentFile},
		map[string]string{gen: genOn("main"), doc: docOn("main")},
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

func TestMergePartialResolution(t *testing.T) {
	e := setup(t)
	a := func(v string) string { return textAnswer(fixture.TaskA, "1.1.0", v) }
	b := func(v string) string { return textAnswer(fixture.TaskB, "1.0.0", v) }
	before := diverge(t, e,
		map[string]string{answerA: a("base"), answerB: b("base")},
		map[string]string{answerA: a("ours"), answerB: b("ours")},
		map[string]string{answerA: a("theirs"), answerB: b("theirs")})

	res, err := Merge(e.st, ws, "what-if", alice, map[string]Resolution{answerA: {Side: SideTheirs}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != "" || len(res.Conflicts) != 1 || res.Conflicts[0].Path != answerB {
		t.Fatalf("result %+v, want only the unresolved %s", res, answerB)
	}
	if got := ref(t, e.st, ws, mainRef); got != before {
		t.Errorf("main moved to %s", got)
	}
}

func TestMergeRejectsInvalidResult(t *testing.T) {
	e := setup(t)
	commitFiles(t, e.st, ws, whatIf, map[string]string{"answers/" + unknownTask + ".md": textAnswer(unknownTask, "1.0.0", "x")})
	before := ref(t, e.st, ws, mainRef)

	_, err := Merge(e.st, ws, "what-if", alice, nil)
	var rej *store.RejectedError
	if !errors.As(err, &rej) || !hasRule(rej.Problems, problem.RuleAnswer) {
		t.Fatalf("Merge: %v, want rejection with rule %s", err, problem.RuleAnswer)
	}
	if got := ref(t, e.st, ws, mainRef); got != before {
		t.Errorf("main moved to %s", got)
	}
}

func TestMergeBadRequests(t *testing.T) {
	e := setup(t)
	before := diverge(t, e,
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "base")},
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "ours")},
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "theirs")})
	for name, tc := range map[string]struct {
		branch string
		res    map[string]Resolution
	}{
		"main":                {"main", nil},
		"empty":               {"", nil},
		"dot-dot":             {"a/../main", nil},
		"option":              {"-x", nil},
		"full ref":            {"refs/heads/what-if", nil},
		"unknown side":        {"what-if", map[string]Resolution{answerA: {Side: "mine"}}},
		"content missing":     {"what-if", map[string]Resolution{answerA: {Side: SideContent}}},
		"content with ours":   {"what-if", map[string]Resolution{answerA: {Side: SideOurs, Content: []byte("x")}}},
		"no conflict at path": {"what-if", map[string]Resolution{answerA: {Side: SideOurs}, answerB: {Side: SideOurs}}},
	} {
		if _, err := Merge(e.st, ws, tc.branch, alice, tc.res); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if got := ref(t, e.st, ws, mainRef); got != before {
		t.Errorf("main moved to %s", got)
	}
}

func TestMergeNotFound(t *testing.T) {
	e := setup(t)
	if _, err := Merge(e.st, ws, "nope", alice, nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown branch: %v, want ErrNotFound", err)
	}
	if _, err := Merge(e.st, unknownTask, "what-if", alice, nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown workspace: %v, want ErrNotFound", err)
	}
}

func TestMergeUnrelatedHistories(t *testing.T) {
	e := setup(t)
	repo := repoOf(t, e.st, ws)
	solo, err := repo.WriteCommit(gitrepo.CommitRequest{
		Changes: []gitrepo.Change{{Path: configPath, Data: []byte(e.config(ws, e.cat[0], false))}},
		Author:  alice,
		Message: "orphan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRef("refs/heads/orphan", solo, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(e.st, ws, "orphan", alice, nil); !errors.Is(err, store.ErrConflict) {
		t.Errorf("merging unrelated history: %v, want ErrConflict", err)
	}
}

func TestMergeMainMovedConcurrently(t *testing.T) {
	e := setup(t)
	diverge(t, e, nil,
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "ours")},
		map[string]string{answerB: textAnswer(fixture.TaskB, "1.0.0", "theirs")})
	var moved string
	beforeUpdate = func() {
		moved = commitFiles(t, e.st, ws, mainRef, map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "concurrent")})
	}
	t.Cleanup(func() { beforeUpdate = func() {} })

	if _, err := Merge(e.st, ws, "what-if", alice, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("Merge while main moved: %v, want ErrConflict", err)
	}
	if got := ref(t, e.st, ws, mainRef); got != moved {
		t.Errorf("main = %s, want the concurrent commit %s", got, moved)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/merge/ -run TestMerge`
Expected: FAIL, `undefined: Merge`, `undefined: Resolution` and similar.

- [ ] **Step 3: Write `internal/merge/merge.go`**

```go
package merge

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
)

// Sides of a Resolution.
const (
	SideOurs    = "ours"    // keep main's version
	SideTheirs  = "theirs"  // take the branch's version
	SideContent = "content" // write Content
)

// Kinds of a Conflict, derived from its path.
const (
	KindAnswer    = "answer"
	KindGenerated = "generated"
	KindDocument  = "document"
	KindConfig    = "custos.yaml"
	KindOther     = "other"
)

// Resolution settles the conflict at one path.
type Resolution struct {
	Side    string // "ours" | "theirs" | "content"
	Content []byte // when Side == "content"
}

// Conflict is a path the merge cannot settle on its own.
type Conflict struct {
	Path         string
	Ours, Theirs []byte // nil when the side deleted the file
	Kind         string // "answer" | "generated" | "document" | "custos.yaml" | "other"
}

// Result is the outcome of Merge.
type Result struct {
	Commit    string     // merge commit on main when merged
	Conflicts []Conflict // non-empty → nothing changed
}

var branchRE = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// beforeUpdate runs after the merge commit is written and before main
// moves. Tests replace it to move main concurrently.
var beforeUpdate = func() {}

// Merge merges branch (a short name such as "what-if") into the workspace's
// main with git merge-tree --write-tree and always records a merge commit,
// authored by author, with main and the branch as parents.
//
// Every path git cannot merge needs a resolution in res; until all have
// one, Merge changes nothing and Result.Conflicts lists the paths still
// without one. A resolution for a path that has no conflict, or a malformed
// one, is ErrInvalid. The merged main is validated like any main update
// (*store.RejectedError) and moved only if it is still where the merge
// started (store.ErrConflict otherwise). A branch already contained in main
// returns main's commit and ignores res.
func Merge(st *store.Store, id, branch string, author gitrepo.Signature, res map[string]Resolution) (*Result, error) {
	if branch == "main" || !branchRE.MatchString(branch) || strings.Contains(branch, "..") ||
		strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, ".") || strings.HasPrefix(branch, "refs/") {
		return nil, fmt.Errorf("%w: %q is not a branch that can be merged into main", ErrInvalid, branch)
	}
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		return nil, err
	}
	ours, ok, err := repo.ResolveRef(mainRef)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("workspace %s has no main branch: %w", id, store.ErrNotFound)
	}
	theirs, ok, err := repo.ResolveRef("refs/heads/" + branch)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("branch %s: %w", branch, store.ErrNotFound)
	}
	if merged, err := repo.IsAncestor(theirs, ours); err != nil {
		return nil, err
	} else if merged {
		return &Result{Commit: ours}, nil
	}
	base, ok, err := repo.MergeBase(ours, theirs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: branch %s shares no history with main", store.ErrConflict, branch)
	}
	mt, err := repo.MergeTree(ours, theirs)
	if err != nil {
		return nil, err
	}
	p, err := planMerge(st, repo, base, ours, theirs, mt, res)
	if err != nil {
		return nil, err
	}
	if len(p.conflicts) > 0 {
		return &Result{Conflicts: p.conflicts}, nil
	}
	tree, err := repo.EditTree(mt.Tree, p.changes)
	if err != nil {
		return nil, err
	}
	commit, err := repo.CommitTree(tree, []string{ours, theirs}, author, fmt.Sprintf("Merge branch '%s'", branch))
	if err != nil {
		return nil, err
	}
	beforeUpdate()
	if err := st.SetRef(id, mainRef, commit, ours); err != nil {
		return nil, err
	}
	return &Result{Commit: commit}, nil
}

// plan is how a merge settles its conflicts.
type plan struct {
	changes   []gitrepo.Change // applied to the tree git merged
	conflicts []Conflict       // paths still without a resolution
	used      map[string]bool  // paths whose resolution was applied
}

// planMerge settles every path git could not merge, by its resolution or
// as an open conflict.
func planMerge(st *store.Store, repo *gitrepo.Repo, base, ours, theirs string, mt *gitrepo.MergeTreeResult, res map[string]Resolution) (*plan, error) {
	p := &plan{used: map[string]bool{}}
	for _, c := range mt.Conflicts {
		if err := p.settle(c.Path, c.Ours, c.Theirs, res); err != nil {
			return nil, err
		}
	}
	if err := p.finish(res); err != nil {
		return nil, err
	}
	return p, nil
}

// settle applies the resolution for path, or records an open conflict.
// ours and theirs are nil when that side has no file.
func (p *plan) settle(path string, ours, theirs []byte, res map[string]Resolution) error {
	r, ok := res[path]
	if !ok {
		p.conflicts = append(p.conflicts, Conflict{Path: path, Ours: ours, Theirs: theirs, Kind: kindOf(path)})
		return nil
	}
	p.used[path] = true
	switch r.Side {
	case SideOurs, SideTheirs:
		if r.Content != nil {
			return fmt.Errorf("%w: %s: content is only allowed with side %q", ErrInvalid, path, SideContent)
		}
		data := ours
		if r.Side == SideTheirs {
			data = theirs
		}
		if data == nil {
			p.changes = append(p.changes, gitrepo.Change{Path: path, Delete: true})
		} else {
			p.changes = append(p.changes, gitrepo.Change{Path: path, Data: data})
		}
	case SideContent:
		if r.Content == nil {
			return fmt.Errorf("%w: %s: side %q needs content", ErrInvalid, path, SideContent)
		}
		p.changes = append(p.changes, gitrepo.Change{Path: path, Data: r.Content})
	default:
		return fmt.Errorf("%w: %s: side %q is not %s, %s or %s", ErrInvalid, path, r.Side, SideOurs, SideTheirs, SideContent)
	}
	return nil
}

// finish rejects resolutions for paths without a conflict and sorts the
// open conflicts by path.
func (p *plan) finish(res map[string]Resolution) error {
	for _, path := range slices.Sorted(maps.Keys(res)) {
		if !p.used[path] {
			return fmt.Errorf("%w: %s has no conflict to resolve", ErrInvalid, path)
		}
	}
	slices.SortFunc(p.conflicts, func(a, b Conflict) int { return strings.Compare(a.Path, b.Path) })
	return nil
}

func kindOf(path string) string {
	switch {
	case path == configPath:
		return KindConfig
	case strings.HasPrefix(path, "answers/"):
		return KindAnswer
	case strings.HasPrefix(path, "generated/"):
		return KindGenerated
	case strings.HasPrefix(path, "documents/"):
		return KindDocument
	}
	return KindOther
}
```

`planMerge` takes `st`, `base`, `ours` and `theirs` already, unused until Task 4 replaces it; Go accepts unused parameters.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/merge/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/merge/merge.go internal/merge/merge_test.go
git commit -m "Merge a workspace branch into main with per-path resolutions"
```

---

### Task 4: custos.yaml merges field by field; pins resolve to the descendant

Git merges `custos.yaml` as text, which is wrong twice: a fork's branch would bring the fork's workspace id, and two moved pins conflict even when one catalog commit descends from the other (§4.4). Whenever `main` and the branch hold different `custos.yaml` files, the merge now computes it from the three parsed versions: the workspace id is always `main`'s; `catalog.url` and `frozen` take the side that changed them (`main` when both did); the pin takes the side that moved it, and when both moved it, the descendant catalog commit — or stays a conflict (kind `custos.yaml`) when neither descends from the other. If any version cannot be parsed, Git's own result stands (and a Git conflict on it needs a resolution).

**Files:**
- Modify: `internal/merge/config.go` (replace whole file)
- Modify: `internal/merge/merge.go` (replace function `planMerge`)
- Test: `internal/merge/config_test.go`

**Interfaces:**
- Consumes: Task 1 `(*gitrepo.Repo).HasCommit`, plan 2a `(*gitrepo.Repo).IsAncestor` (phase 1), `ReadFile`; Task 3 `plan`, `(*plan).settle`, `(*plan).finish`.
- Produces (unexported):

```go
func (p *plan) config(st *store.Store, repo *gitrepo.Repo, base, ours, theirs string, mt *gitrepo.MergeTreeResult, res map[string]Resolution) error
func mergeConfig(cat *gitrepo.Repo, base, ours, theirs []byte) ([]byte, configOutcome, error)
func mergePin(cat *gitrepo.Repo, base, ours, theirs string) (pin string, ok bool, err error)
```

- [ ] **Step 1: Write the failing tests** `internal/merge/config_test.go`

```go
package merge

import (
	"errors"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/store"
)

// Valid catalog commits on top of fixture.Catalog().
var (
	catalogStep2 = map[string]string{fixture.TaskPath(fixture.TaskB, "1.1.0"): fixture.TaskFile(fixture.TaskB, "1.1.0", fixture.TaskB+"@1.0.0")}
	catalogStep3 = map[string]string{fixture.TaskPath(fixture.TaskA, "1.2.0"): fixture.TaskFile(fixture.TaskA, "1.2.0", fixture.TaskA+"@1.1.0")}
)

func TestMergePinMovesToDescendant(t *testing.T) {
	for _, tc := range []struct {
		name               string
		mainPin, branchPin int // index into e.cat
	}{{"branch newer", 1, 2}, {"main newer", 2, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			e.advanceCatalog(t, catalogStep2)
			e.advanceCatalog(t, catalogStep3)
			commitFiles(t, e.st, ws, whatIf, map[string]string{configPath: e.config(ws, e.cat[tc.branchPin], false)})
			commitFiles(t, e.st, ws, mainRef, map[string]string{configPath: e.config(ws, e.cat[tc.mainPin], false)})

			res, err := Merge(e.st, ws, "what-if", alice, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Conflicts) != 0 {
				t.Fatalf("conflicts %+v, want the pin to move to the descendant", res.Conflicts)
			}
			if cfg := readConfig(t, e, ws, res.Commit); cfg.Catalog.Commit != e.cat[2] || cfg.Workspace != ws {
				t.Errorf("custos.yaml = %+v, want pin %s", cfg, e.cat[2])
			}
		})
	}
}

func TestMergePinWithoutCommonLineIsAConflict(t *testing.T) {
	e := setup(t)
	draft := e.draftCatalog(t, e.cat[0], map[string]string{
		fixture.TaskPath(fixture.TaskB, "2.0.0"): fixture.TaskFile(fixture.TaskB, "2.0.0", fixture.TaskB+"@1.0.0"),
	})
	c2 := e.advanceCatalog(t, catalogStep2)
	ours, theirs := e.config(ws, c2, false), e.config(ws, draft, false)
	commitFiles(t, e.st, ws, whatIf, map[string]string{configPath: theirs})
	before := commitFiles(t, e.st, ws, mainRef, map[string]string{configPath: ours})

	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("conflicts %+v, want one on custos.yaml", res.Conflicts)
	}
	if c := res.Conflicts[0]; c.Path != configPath || c.Kind != KindConfig || string(c.Ours) != ours || string(c.Theirs) != theirs {
		t.Errorf("conflict = %s %s ours %q theirs %q", c.Path, c.Kind, c.Ours, c.Theirs)
	}
	if got := ref(t, e.st, ws, mainRef); got != before {
		t.Errorf("main moved to %s", got)
	}

	// The draft commit is not on the catalog's main, so taking it is rejected.
	_, err = Merge(e.st, ws, "what-if", alice, map[string]Resolution{configPath: {Side: SideTheirs}})
	var rej *store.RejectedError
	if !errors.As(err, &rej) || !hasRule(rej.Problems, problem.RulePin) {
		t.Fatalf("choosing the draft pin: %v, want rejection with rule %s", err, problem.RulePin)
	}
	res, err = Merge(e.st, ws, "what-if", alice, map[string]Resolution{configPath: {Side: SideOurs}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg := readConfig(t, e, ws, res.Commit); cfg.Catalog.Commit != c2 {
		t.Errorf("pin = %s, want %s", cfg.Catalog.Commit, c2)
	}
}

func TestMergeKeepsMainWorkspaceID(t *testing.T) {
	e := setup(t)
	if err := Fork(e.st, ws, forkID, alice); err != nil {
		t.Fatal(err)
	}
	fromFork := textAnswer(fixture.TaskB, "1.0.0", "answered in the fork")
	commitFiles(t, e.st, forkID, mainRef, map[string]string{answerB: fromFork})
	// The fork's main arrives as a branch, as a git push of it would bring it.
	if _, err := repoOf(t, e.st, ws).Fetch(repoOf(t, e.st, forkID), mainRef, "refs/heads/from-fork"); err != nil {
		t.Fatal(err)
	}

	res, err := Merge(e.st, ws, "from-fork", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
	if cfg := readConfig(t, e, ws, res.Commit); cfg.Workspace != ws {
		t.Errorf("workspace id after merging the fork = %s, want %s", cfg.Workspace, ws)
	}
	if got, _ := file(t, e.st, ws, res.Commit, answerB); got != fromFork {
		t.Errorf("answer from the fork = %q", got)
	}
}

func TestMergeConfigFieldByField(t *testing.T) {
	e := setup(t)
	c2 := e.advanceCatalog(t, catalogStep2)
	commitFiles(t, e.st, ws, whatIf, map[string]string{configPath: e.config(ws, e.cat[0], true)}) // branch freezes
	commitFiles(t, e.st, ws, mainRef, map[string]string{configPath: e.config(ws, c2, false)})     // main moves the pin

	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
	if cfg := readConfig(t, e, ws, res.Commit); !cfg.Frozen || cfg.Catalog.Commit != c2 || cfg.Workspace != ws {
		t.Errorf("custos.yaml = %+v, want frozen and pinned to %s", cfg, c2)
	}
}

func TestMergeUnreadableConfigIsAConflict(t *testing.T) {
	e := setup(t)
	c2 := e.advanceCatalog(t, catalogStep2)
	commitFiles(t, e.st, ws, whatIf, map[string]string{configPath: "workspace: [\n"})
	before := commitFiles(t, e.st, ws, mainRef, map[string]string{configPath: e.config(ws, c2, false)})

	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Kind != KindConfig || string(res.Conflicts[0].Theirs) != "workspace: [\n" {
		t.Fatalf("conflicts %+v, want one on custos.yaml", res.Conflicts)
	}
	if got := ref(t, e.st, ws, mainRef); got != before {
		t.Errorf("main moved to %s", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/merge/ -run 'TestMergePin|TestMergeKeeps|TestMergeConfig|TestMergeUnreadable'`
Expected: FAIL — `TestMergePinMovesToDescendant` reports a `custos.yaml` conflict, `TestMergeKeepsMainWorkspaceID` is rejected with rule `workspace-id`, `TestMergeConfigFieldByField` reports a conflict.

- [ ] **Step 3: Replace `internal/merge/config.go`**

```go
package merge

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"

	"go.yaml.in/yaml/v3"

	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/workspace"
)

var pinRE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// decodeConfig parses custos.yaml strictly, like workspace.Load. nil data
// (no file) is an error.
func decodeConfig(data []byte) (workspace.Config, error) {
	var c workspace.Config
	if data == nil {
		return c, errors.New("custos.yaml is missing")
	}
	if err := frontmatter.DecodeStrict(bytes.TrimPrefix(data, []byte("\ufeff")), &c); err != nil {
		return workspace.Config{}, fmt.Errorf("custos.yaml: %w", err)
	}
	return c, nil
}

// encodeConfig writes custos.yaml with two-space indentation.
func encodeConfig(c workspace.Config) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

type configOutcome int

const (
	configUnreadable  configOutcome = iota // a version cannot be parsed; git's merge applies
	configMerged                           // merged field by field
	configPinConflict                      // both sides moved the pin to unrelated commits
)

// config settles custos.yaml whenever main and the branch hold different
// versions of it, whether or not git found a textual conflict.
func (p *plan) config(st *store.Store, repo *gitrepo.Repo, base, ours, theirs string, mt *gitrepo.MergeTreeResult, res map[string]Resolution) error {
	var files [3][]byte
	for i, rev := range []string{base, ours, theirs} {
		data, _, err := repo.ReadFile(rev, configPath)
		if err != nil {
			return err
		}
		files[i] = data
	}
	if (files[1] == nil) == (files[2] == nil) && bytes.Equal(files[1], files[2]) {
		return nil
	}
	merged, outcome, err := mergeConfig(st.CatalogRepo(), files[0], files[1], files[2])
	if err != nil {
		return err
	}
	switch outcome {
	case configMerged:
		p.changes = append(p.changes, gitrepo.Change{Path: configPath, Data: merged})
		return nil
	case configPinConflict:
		return p.settle(configPath, files[1], files[2], res)
	}
	for _, c := range mt.Conflicts {
		if c.Path == configPath {
			return p.settle(c.Path, c.Ours, c.Theirs, res)
		}
	}
	return nil
}

// mergeConfig merges three versions of custos.yaml. The workspace id is
// always main's, because a branch may come from a fork. catalog.url and
// frozen take the side that changed them, main's when both did. The pin
// takes the side that moved it; when both moved it, the descendant of the
// two catalog commits (spec §4.4), or configPinConflict when neither
// descends from the other.
func mergeConfig(cat *gitrepo.Repo, base, ours, theirs []byte) ([]byte, configOutcome, error) {
	b, errB := decodeConfig(base)
	o, errO := decodeConfig(ours)
	t, errT := decodeConfig(theirs)
	if errB != nil || errO != nil || errT != nil {
		return nil, configUnreadable, nil
	}
	m := o
	m.Catalog.URL = pick(b.Catalog.URL, o.Catalog.URL, t.Catalog.URL)
	m.Frozen = pick(b.Frozen, o.Frozen, t.Frozen)
	pin, ok, err := mergePin(cat, b.Catalog.Commit, o.Catalog.Commit, t.Catalog.Commit)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return nil, configPinConflict, nil
	}
	m.Catalog.Commit = pin
	data, err := encodeConfig(m)
	if err != nil {
		return nil, 0, err
	}
	return data, configMerged, nil
}

// pick is a three-way merge of one value: main's unless only the branch
// changed it.
func pick[T comparable](base, ours, theirs T) T {
	if ours == base {
		return theirs
	}
	return ours
}

// mergePin merges the pinned catalog commit. ok is false when both sides
// moved it and neither commit descends from the other, or when a pin is not
// a commit of the catalog.
func mergePin(cat *gitrepo.Repo, base, ours, theirs string) (pin string, ok bool, err error) {
	if ours == theirs || theirs == base {
		return ours, true, nil
	}
	if ours == base {
		return theirs, true, nil
	}
	for _, c := range []string{ours, theirs} {
		if !pinRE.MatchString(c) {
			return "", false, nil
		}
		if has, err := cat.HasCommit(c); err != nil || !has {
			return "", false, err
		}
	}
	up, err := cat.IsAncestor(ours, theirs)
	if err != nil {
		return "", false, err
	}
	if up {
		return theirs, true, nil
	}
	down, err := cat.IsAncestor(theirs, ours)
	if err != nil {
		return "", false, err
	}
	if down {
		return ours, true, nil
	}
	return "", false, nil
}
```

- [ ] **Step 4: Replace function `planMerge` in `internal/merge/merge.go`**

```go
// planMerge settles custos.yaml field by field and every other path git
// could not merge, by its resolution or as an open conflict.
func planMerge(st *store.Store, repo *gitrepo.Repo, base, ours, theirs string, mt *gitrepo.MergeTreeResult, res map[string]Resolution) (*plan, error) {
	p := &plan{used: map[string]bool{}}
	if err := p.config(st, repo, base, ours, theirs, mt, res); err != nil {
		return nil, err
	}
	for _, c := range mt.Conflicts {
		if c.Path == configPath {
			continue // settled by p.config
		}
		if err := p.settle(c.Path, c.Ours, c.Theirs, res); err != nil {
			return nil, err
		}
	}
	if err := p.finish(res); err != nil {
		return nil, err
	}
	return p, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/merge/`
Expected: PASS (all fork, merge and config tests).

- [ ] **Step 6: Commit**

```bash
git add internal/merge/config.go internal/merge/merge.go internal/merge/config_test.go
git commit -m "Merge custos.yaml field by field and move conflicting pins to the descendant"
```

---

### Task 5: REST endpoints for fork, merge and branches

**Files:**
- Create: `internal/merge/branches.go`
- Create: `internal/merge/http.go`
- Modify: `cmd/custos/serve.go` (function `openServer`: register the endpoints where plan 2b creates the API)
- Test: `internal/merge/http_test.go`

**Interfaces:**
- Consumes: plan 2b `api.New(st *store.Store, bl *blobs.Store) *API`, `(*API).Handle(pattern string, h http.HandlerFunc)`, `(*API).Handler() http.Handler`, `api.Author(r *http.Request) (gitrepo.Signature, error)`, `api.WriteJSON(w, status int, v any)`, `api.WriteError(w, err error)`, `blobs.Open(dir string) (*blobs.Store, error)`; plan 2a `(*gitrepo.Repo).Refs(prefix string) (map[string]string, error)`, `(*store.Store).DataDir() string`; Tasks 2–4 `Fork`, `Merge`, `ErrInvalid`.
- Produces:

```go
type Branch struct {
	Name   string `json:"name"`   // short name, e.g. "main", "what-if"
	Commit string `json:"commit"`
}
func Branches(st *store.Store, id string) ([]Branch, error) // sorted by name
func Register(a *api.API, st *store.Store) // must be called before a.Handler()
```

Endpoints (architecture note): `POST /api/workspaces/{id}/fork` body `{"id"?}` → 201 `{"id"}`; `POST /api/workspaces/{id}/merge` body `{"branch", "resolutions": {"<path>": {"side", "content"?}}}` → 200 `{"commit"}` or 409 `{"error", "conflicts": [{"path", "kind", "ours", "theirs"}]}` (contents base64, `null` for a deleted side); `GET /api/workspaces/{id}/branches` → 200 `[{"name", "commit"}]`. `ErrInvalid` → 400; everything else through `api.WriteError`.

- [ ] **Step 1: Write the failing tests** `internal/merge/http_test.go`

```go
package merge

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/task"
)

const aliceHeader = "Alice Example <alice@example.org>"

func handler(t *testing.T, e *env) http.Handler {
	t.Helper()
	bl, err := blobs.Open(filepath.Join(e.st.DataDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(e.st, bl)
	Register(a, e.st)
	return a.Handler()
}

func call(h http.Handler, method, path, author, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if author != "" {
		req.Header.Set("X-Custos-Author", author)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
}

func TestForkEndpoint(t *testing.T) {
	e := setup(t)
	h := handler(t, e)
	path := "/api/workspaces/" + ws + "/fork"

	rec := call(h, "POST", path, aliceHeader, `{"id":"`+forkID+`"}`)
	var got struct {
		ID string `json:"id"`
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("fork: %d %s", rec.Code, rec.Body)
	}
	if decodeJSON(t, rec, &got); got.ID != forkID {
		t.Errorf("fork id = %q, want %s", got.ID, forkID)
	}
	for _, body := range []string{`{}`, ``} {
		rec = call(h, "POST", path, aliceHeader, body)
		got.ID = ""
		if rec.Code != http.StatusCreated {
			t.Fatalf("fork with body %q: %d %s", body, rec.Code, rec.Body)
		}
		if decodeJSON(t, rec, &got); !task.ValidID(got.ID) || got.ID == forkID {
			t.Errorf("generated id = %q", got.ID)
		}
	}
	for _, tc := range []struct {
		name, path, author, body string
		want                     int
	}{
		{"no author", path, "", `{}`, http.StatusUnauthorized},
		{"existing id", path, aliceHeader, `{"id":"` + forkID + `"}`, http.StatusConflict},
		{"unknown source", "/api/workspaces/" + unknownTask + "/fork", aliceHeader, `{}`, http.StatusNotFound},
		{"invalid id", path, aliceHeader, `{"id":"nope"}`, http.StatusBadRequest},
		{"malformed", path, aliceHeader, `{"id":`, http.StatusBadRequest},
		{"unknown field", path, aliceHeader, `{"id":"` + forkID + `","x":1}`, http.StatusBadRequest},
	} {
		if rec := call(h, "POST", tc.path, tc.author, tc.body); rec.Code != tc.want {
			t.Errorf("%s: %d %s, want %d", tc.name, rec.Code, rec.Body, tc.want)
		}
	}
}

func TestMergeEndpoint(t *testing.T) {
	e := setup(t)
	ours, theirs := textAnswer(fixture.TaskA, "1.1.0", "ours"), textAnswer(fixture.TaskA, "1.1.0", "theirs")
	before := diverge(t, e,
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "base")},
		map[string]string{answerA: ours},
		map[string]string{answerA: theirs})
	h := handler(t, e)
	path := "/api/workspaces/" + ws + "/merge"

	rec := call(h, "POST", path, aliceHeader, `{"branch":"what-if"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("merge with conflicts: %d %s", rec.Code, rec.Body)
	}
	var conflicts struct {
		Error     string `json:"error"`
		Conflicts []struct {
			Path   string `json:"path"`
			Kind   string `json:"kind"`
			Ours   []byte `json:"ours"`
			Theirs []byte `json:"theirs"`
		} `json:"conflicts"`
	}
	decodeJSON(t, rec, &conflicts)
	if conflicts.Error == "" || len(conflicts.Conflicts) != 1 {
		t.Fatalf("conflict body %s", rec.Body)
	}
	if c := conflicts.Conflicts[0]; c.Path != answerA || c.Kind != KindAnswer || string(c.Ours) != ours || string(c.Theirs) != theirs {
		t.Errorf("conflict = %+v", c)
	}
	if got := ref(t, e.st, ws, mainRef); got != before {
		t.Errorf("main moved to %s", got)
	}

	resolved := textAnswer(fixture.TaskA, "1.1.0", "both")
	body := `{"branch":"what-if","resolutions":{"` + answerA + `":{"side":"content","content":"` +
		base64.StdEncoding.EncodeToString([]byte(resolved)) + `"}}}`
	rec = call(h, "POST", path, aliceHeader, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge with resolution: %d %s", rec.Code, rec.Body)
	}
	var merged struct {
		Commit string `json:"commit"`
	}
	if decodeJSON(t, rec, &merged); merged.Commit != ref(t, e.st, ws, mainRef) {
		t.Errorf("commit %q is not main", merged.Commit)
	}
	if got, _ := file(t, e.st, ws, merged.Commit, answerA); got != resolved {
		t.Errorf("answer = %q, want %q", got, resolved)
	}

	commitFiles(t, e.st, ws, "refs/heads/bad", map[string]string{"answers/" + unknownTask + ".md": textAnswer(unknownTask, "1.0.0", "x")})
	for _, tc := range []struct {
		name, author, body string
		want               int
	}{
		{"no author", "", `{"branch":"what-if"}`, http.StatusUnauthorized},
		{"unknown branch", aliceHeader, `{"branch":"nope"}`, http.StatusNotFound},
		{"main", aliceHeader, `{"branch":"main"}`, http.StatusBadRequest},
		{"no branch", aliceHeader, ``, http.StatusBadRequest},
		{"bad side", aliceHeader, `{"branch":"bad","resolutions":{"x":{"side":"mine"}}}`, http.StatusBadRequest},
		{"invalid result", aliceHeader, `{"branch":"bad"}`, http.StatusUnprocessableEntity},
	} {
		if rec := call(h, "POST", path, tc.author, tc.body); rec.Code != tc.want {
			t.Errorf("%s: %d %s, want %d", tc.name, rec.Code, rec.Body, tc.want)
		}
	}
}

func TestBranchesEndpoint(t *testing.T) {
	e := setup(t)
	tip := commitFiles(t, e.st, ws, whatIf, map[string]string{answerB: textAnswer(fixture.TaskB, "1.0.0", "draft")})
	h := handler(t, e)

	rec := call(h, "GET", "/api/workspaces/"+ws+"/branches", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("branches: %d %s", rec.Code, rec.Body)
	}
	var got []Branch
	decodeJSON(t, rec, &got)
	want := []Branch{{Name: "main", Commit: ref(t, e.st, ws, mainRef)}, {Name: "what-if", Commit: tip}}
	if !slices.Equal(got, want) {
		t.Errorf("branches = %+v, want %+v", got, want)
	}
	if rec := call(h, "GET", "/api/workspaces/"+unknownTask+"/branches", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown workspace: %d, want 404", rec.Code)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/merge/ -run Endpoint`
Expected: FAIL, `undefined: Register`, `undefined: Branch`.

- [ ] **Step 3: Write `internal/merge/branches.go`**

```go
package merge

import (
	"maps"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/store"
)

// Branch is a branch of a workspace repository.
type Branch struct {
	Name   string `json:"name"` // short name, e.g. "main", "what-if"
	Commit string `json:"commit"`
}

// Branches lists the branches of a workspace, sorted by name.
func Branches(st *store.Store, id string) ([]Branch, error) {
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		return nil, err
	}
	refs, err := repo.Refs("refs/heads/")
	if err != nil {
		return nil, err
	}
	out := make([]Branch, 0, len(refs))
	for _, name := range slices.Sorted(maps.Keys(refs)) {
		out = append(out, Branch{Name: strings.TrimPrefix(name, "refs/heads/"), Commit: refs[name]})
	}
	return out, nil
}
```

- [ ] **Step 4: Write `internal/merge/http.go`**

```go
package merge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/store"
)

// maxBody bounds fork and merge request bodies; resolutions carry file
// contents.
const maxBody = 64 << 20

// Register adds the fork, merge and branch endpoints (spec §4.4) to the
// REST API. Call it before a.Handler().
func Register(a *api.API, st *store.Store) {
	a.Handle("POST /api/workspaces/{id}/fork", func(w http.ResponseWriter, r *http.Request) { handleFork(st, w, r) })
	a.Handle("POST /api/workspaces/{id}/merge", func(w http.ResponseWriter, r *http.Request) { handleMerge(st, w, r) })
	a.Handle("GET /api/workspaces/{id}/branches", func(w http.ResponseWriter, r *http.Request) { handleBranches(st, w, r) })
}

type errorBody struct {
	Error string `json:"error"`
}

type conflictJSON struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Ours   []byte `json:"ours"`   // base64; null when main deleted the file
	Theirs []byte `json:"theirs"` // base64; null when the branch deleted the file
}

type conflictBody struct {
	Error     string         `json:"error"`
	Conflicts []conflictJSON `json:"conflicts"`
}

type resolutionJSON struct {
	Side    string `json:"side"`
	Content []byte `json:"content"` // base64
}

func handleFork(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.ID == "" {
		body.ID = uuid.NewString()
	}
	if err := Fork(st, r.PathValue("id"), body.ID, author); err != nil {
		writeError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, map[string]string{"id": body.ID})
}

func handleMerge(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	var body struct {
		Branch      string                    `json:"branch"`
		Resolutions map[string]resolutionJSON `json:"resolutions"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	res := make(map[string]Resolution, len(body.Resolutions))
	for path, rj := range body.Resolutions {
		res[path] = Resolution{Side: rj.Side, Content: rj.Content}
	}
	result, err := Merge(st, r.PathValue("id"), body.Branch, author, res)
	if err != nil {
		writeError(w, err)
		return
	}
	if len(result.Conflicts) > 0 {
		cb := conflictBody{Error: fmt.Sprintf("merging %s into main needs a resolution for %d conflicting paths", body.Branch, len(result.Conflicts))}
		for _, c := range result.Conflicts {
			cb.Conflicts = append(cb.Conflicts, conflictJSON{Path: c.Path, Kind: c.Kind, Ours: c.Ours, Theirs: c.Theirs})
		}
		api.WriteJSON(w, http.StatusConflict, cb)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]string{"commit": result.Commit})
}

func handleBranches(st *store.Store, w http.ResponseWriter, r *http.Request) {
	bs, err := Branches(st, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, bs)
}

// decodeBody decodes a JSON object into v; an empty body leaves v unchanged.
// It writes the error response and returns false when the body is bad.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if errors.Is(err, io.EOF) {
		return true
	}
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

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/merge/`
Expected: PASS.

- [ ] **Step 6: Register the endpoints in `cmd/custos/serve.go`**

Plan 2b creates the REST API in `openServer` with `a, err := openAPI(st)` (which calls `api.New`) and mounts `a.Handler()` at the end of the function; plan 2c added `startDistribution` there. Register the merge endpoints right after the API is created. Replace:

```go
	a, err := openAPI(st)
	if err != nil {
		return nil, err
	}
```

with:

```go
	a, err := openAPI(st)
	if err != nil {
		return nil, err
	}
	merge.Register(a, st)
```

and add the import `"github.com/emeland-io/custos/internal/merge"` to `cmd/custos/serve.go`. The end of `openServer` then reads:

```go
	a, err := openAPI(st)
	if err != nil {
		return nil, err
	}
	merge.Register(a, st)
	srv := server.New(st)
	startDistribution(st, a, srv, stderr)
	srv.WithAPI(a.Handler())
	return srv, nil
}
```

Run: `go build ./... && go vet ./... && go test ./cmd/custos/`
Expected: no output from build and vet; tests PASS.

- [ ] **Step 7: Smoke-test the running server**

```bash
make build
D=$(mktemp -d)
./custos serve --data-dir "$D" --addr 127.0.0.1:8099 &
PID=$!
sleep 1
curl -s -o /dev/null -w '%{http_code}\n' -X POST -d '{}' http://127.0.0.1:8099/api/workspaces/5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9/fork
curl -s -w '\n%{http_code}\n' -X POST -H 'X-Custos-Author: Ann Example <ann@example.org>' -d '{}' http://127.0.0.1:8099/api/workspaces/5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9/fork
curl -s -w '\n%{http_code}\n' http://127.0.0.1:8099/api/workspaces/5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9/branches
kill $PID
rm -rf "$D"
```

Expected: `401` (the route exists and requires an author; an unregistered route would answer 404 or 405), then a JSON body `{"error": ...}` with `404`, then a JSON body `{"error": ...}` with `404`.

- [ ] **Step 8: Commit**

```bash
git add internal/merge/branches.go internal/merge/http.go internal/merge/http_test.go cmd/custos/serve.go
git commit -m "Serve fork, merge and branch listing over the REST API"
```

---

### Task 6: README

**Files:**
- Modify: `README.md` (insert one section; update the introduction's list of what follows)

Plan 2a already documents git 2.38 as the minimum version in "Running"; this section only refers to it.

- [ ] **Step 1: Insert this section into `README.md` directly before the heading `## Container image`**

````markdown
## Fork and merge

**Fork** a workspace to start a new one from its history, for example for
the next release of a product. The fork's `main` is the source's `main`
plus one commit that sets the new id in `custos.yaml`. Branches are not
copied. Attachments live in one blob store shared by all workspaces, so
nothing is copied there.

```sh
curl -X POST -H 'X-Custos-Author: Jane Doe <jane@example.org>' \
  -d '{"id": "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"}' \
  http://127.0.0.1:8080/api/workspaces/5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9/fork
```

Leave out `id` to get a generated one; the answer is `201 {"id": "…"}`.

**Branches** of a workspace are for parallel or what-if work. Push any
branch other than `main` with Git; only `main` is validated.
`GET /api/workspaces/<id>/branches` lists them as `[{"name", "commit"}]`.
To bring a fork's work back, push its `main` to a branch of the original
workspace:

```sh
git push http://127.0.0.1:8080/git/workspaces/<original-id>.git main:from-fork
```

**Merge** a branch into `main`:

```sh
curl -X POST -H 'X-Custos-Author: Jane Doe <jane@example.org>' \
  -d '{"branch": "what-if"}' \
  http://127.0.0.1:8080/api/workspaces/<id>/merge
```

custos always records a merge commit, authored by you, and validates the
result like a push to `main` before `main` moves (`200 {"commit": "…"}`).
When a file changed on both sides, nothing changes and the answer is `409`
with the conflicts. `ours` is the file on `main`, `theirs` the file on the
branch, both base64-encoded, `null` when that side deleted the file:

```json
{"error": "…", "conflicts": [{"path": "answers/<task-uuid>.md", "kind": "answer", "ours": "LS0t…", "theirs": "LS0t…"}]}
```

Send the merge again with a resolution for every conflicting path: `ours`
keeps `main`'s version, `theirs` takes the branch's, `content` writes the
given base64 content.

```json
{"branch": "what-if", "resolutions": {"answers/<task-uuid>.md": {"side": "theirs"}}}
```

- `custos.yaml` is merged field by field. `main` keeps its workspace id, so
  a fork's work merges back. When both sides moved the pin, it moves to the
  newer catalog commit; when neither commit descends from the other,
  `custos.yaml` is a conflict like any other file.
- Generated tasks and documents in conflict are resolved by choosing a side,
  like answers, until processors can be rerun (phase 3).
- `400` means a bad branch name or resolution (including one for a path
  without a conflict), `404` an unknown workspace or branch, `422` a merge
  result that breaks a rule. `409` without conflicts means `main` moved
  while the merge ran, or the branch shares no history with `main`; send
  the merge again after checking.

Merging needs git 2.38 or later (see [Running](#running)).
````

- [ ] **Step 2: Say what this version implements** — in the paragraph below the link to the design (as plan 2c left it), replace:

```markdown
Forking and merging workspaces, processors and the web UI follow.
```

with:

```markdown
Workspaces can be forked, and branches merged into `main`. Processors and
the web UI follow.
```

- [ ] **Step 3: Check the README still renders as intended**

Run: `grep -n '^## ' README.md`
Expected: `## Fork and merge` appears once, directly before `## Container image`.

- [ ] **Step 4: Run all checks**

Run: `make test`
Expected: `go vet` prints nothing and all packages PASS.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "Describe workspace fork and merge in the README"
```

---

## Self-Review Notes

- **Spec coverage:**
  - §4.4 "Fork a workspace: a new repo from the history of an existing one, with a new workspace id and its own blob store (blobs copied by reference)" → Task 2 (blob store shared per ruling 2.7, nothing to copy).
  - §4.4 "Branches inside a workspace are for parallel or what-if work" → branches are written by Git push and `store.UpdateWorkspace` (2a/2b); listing in Task 5; merging in Task 3.
  - §4.4 "conflicting answer files are shown side by side for the engineer to choose" → Task 3 (`Conflict.Ours/Theirs`, resolutions `ours`/`theirs`/`content`), Task 5 (409 body).
  - §4.4 "conflicting pins resolve to the descendant commit when one is an ancestor of the other; otherwise the merge is blocked" → Task 4.
  - §4.4 "conflicting generated tasks or documents are resolved by rerunning the processor" → replaced by ruling 2.10: choosing a side, Task 3 (`TestMergeGeneratedAndDocumentConflicts`).
  - §4.4 "Fork the catalog" → nothing to build: a catalog is forked with `git clone`/`git push`, and ruling 2.4 keeps workspaces on the server's own catalog.
  - §4.5 one write queue per repository, hook-equivalent validation → `Merge` moves `main` through `store.SetRef` (lock, validation, compare-and-swap; `TestMergeMainMovedConcurrently`), `Fork` holds the new workspace's lock and validates with `rules.CheckWorkspaceUpdate`.
  - §7 invalid changes rejected naming file and rule → `*store.RejectedError` from `Merge` (`TestMergeRejectsInvalidResult`) and `Fork` (`TestForkOfInvalidMainLeavesNothingBehind`), 422 over HTTP.
  - Ruling 2.9 (`git merge-tree --write-tree`) → Task 1, output format verified with git 2.55 and restricted to 2.38 options.
- **Placeholder scan:** every code step has complete code. The one edit to code that does not exist yet (`openServer` in `cmd/custos/serve.go`, as plans 2a, 2b and 2c leave it) quotes the exact text it replaces.
- **Type consistency:** `ConflictedFile`, `MergeTreeResult`, `MergeTree`, `MergeBase`, `HasCommit`, `Fetch`, `EditTree` (Task 1) are used with the same signatures in Tasks 2–4; `plan`, `settle`, `finish` (Task 3) are used unchanged by Task 4's `planMerge` and `(*plan).config`; `Resolution`, `Conflict`, `Result`, `Fork`, `Merge` match the architecture note.
- **Review Focus:** each of the five lines has its named test in Task 2, 3 or 4.

## Decisions beyond the architecture note

- `Merge` always records a merge commit, also when `main` could fast-forward — so the `custos.yaml` rules and validation apply the same way to every merge and the merge is visible in history — cost if wrong: an extra merge commit per trivially fast-forwardable branch; dropping it needs a separate path that still fixes `custos.yaml`.
- `custos.yaml` is merged field by field whenever both sides differ, not only on a Git conflict: workspace id always `main`'s, `catalog.url`/`frozen` three-way with `main` winning, pin by the descendant rule — otherwise merging a fork's branch back carries the fork's id and is rejected — cost if wrong: comments and formatting in `custos.yaml` are rewritten by such merges.
- A pin conflict that stays a conflict can still be settled by an explicit `custos.yaml` resolution, which is then validated (pin must be on catalog `main`) — the note says other conflicts need a per-path resolution and ruling 2.16 allows any pin on `main` — cost if wrong: if §4.4's "blocked" must be absolute, reject resolutions for `custos.yaml` (one check).
- `Result.Conflicts` lists only the conflicts still without a resolution; a resolution for a path without a conflict, an unknown side, `content` without content, or content with `ours`/`theirs` is `ErrInvalid` — stale resolutions should not be silently ignored — cost if wrong: clients must resend exactly the open paths.
- New exported `merge.ErrInvalid`, answered with 400 by the merge handlers themselves, since `api.WriteError` only knows store and blob errors — cost if wrong: a later shared error mapping must add it.
- Endpoints are registered by `merge.Register(a *api.API, st *store.Store)`, called in `openServer` in `cmd/custos/serve.go` right after plan 2b's `openAPI` — the note does not say where later plans register endpoints, and this keeps `api` from importing `merge` — cost if wrong: one moved line if the wiring moves.
- `Fork` does not use `store.SetRef`: it holds `store.Lock(newID)` and validates with `rules.CheckWorkspaceUpdate`, then `gitrepo.UpdateRef` — `SetRef` takes the same lock itself, and holding it keeps catalog distribution away from the half-made repository — cost if wrong: if `SetRef` gains behaviour, `Fork` must copy it.
- `Fork` copies only `main` (not branches or `custos/pin/*`), through a temporary ref `refs/custos/fork-source` that is deleted before `main` is set — the note says "copies main's history"; the source's pin proposals do not apply to the fork — cost if wrong: what-if branches must be pushed to the fork again.
- `Fork` checks `WorkspaceRepo(newID)` first, relies on `CreateWorkspaceRepo` failing with `store.ErrExists` for an existing repository, serialises forks with a package mutex, and removes the new repository if any later step fails — a failed fork must not block a retry — plan 2a's `CreateWorkspaceRepo` reserves the directory with `os.Mkdir` (atomic `store.ErrExists`) and takes no lock, so `Fork` takes `store.Lock(newID)` itself right after it — cost if wrong: a concurrent `CreateWorkspace` to the same id fails with `ErrExists` instead of waiting.
- `Fork` of a workspace without `main` and `Merge` into one are `store.ErrNotFound` (404) — such a repository is only a fork in progress — cost if wrong: a status code.
- A branch that shares no history with `main` is `store.ErrConflict` (409), not 400 — it is a state of the repository, not a malformed request — cost if wrong: a status code.
- `merge` takes short branch names only (`what-if`, not `refs/heads/what-if`, not `main`), checked by a conservative pattern — the API body names a branch — cost if wrong: unusual but valid Git branch names (for example with `@` or `+`) cannot be merged through the API.
- `gitrepo.MergeTree` uses `-z --no-messages` and reads the stage entries instead of `--name-only`/`--messages` — the stage object ids give both sides' contents directly, and messages are translated by the locale — cost if wrong: Git's explanation texts (e.g. rename details) are not shown to the user.
- Conflicts on non-regular files (symlink or submodule modes) make `MergeTree` fail instead of becoming a `Conflict` — `main` rejects such files anyway (ruling 1.6) — cost if wrong: a 500 instead of a resolvable conflict for such a branch.
- New `gitrepo.EditTree` (tree + changes → tree) instead of reusing `WriteCommit` — `WriteCommit`'s `Base` is a commit, and the merged tree is not one — cost if wrong: two similar temporary-index code paths in `gitrepo`.
- Fork and merge request bodies are limited to 64 MiB (413 above) — resolutions carry file contents; attachments are blobs, not part of the body — cost if wrong: very large Markdown answers cannot be resolved with `content` through the API; the limit is one constant.
- The fork commit's message is `Fork workspace <src> as <new>`, the merge commit's `Merge branch '<branch>'` — Git's familiar wording — cost if wrong: none.
