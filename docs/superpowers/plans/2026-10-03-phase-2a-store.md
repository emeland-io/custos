# Phase 2a (Store, catalog-aware checks, status) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `store` package that owns the repositories and writes validated commits to them with Git plumbing, pushes to a workspace's `main` checked against the server's catalog (workspace id, pin, answers), and a `status` package that computes task states and the workspace book.

**Architecture:** `gitrepo` gains signatures and write plumbing (temporary index, `commit-tree`, compare-and-swap `update-ref`). A new package `rules` holds the checks for moving `main` of a catalog or workspace; the pre-receive hook and the store both call it. `status` derives task states and the book from loaded trees, without git. `store` takes over repository layout, hook installation and workspace creation from `server`, which keeps HTTP only. `cmd/custos` wires `serve` and `workspace create` to the store.

**Tech Stack:** Go 1.26, the `git` binary (≥ 2.38) at runtime, `go.yaml.in/yaml/v3`, `golang.org/x/mod`, `github.com/google/uuid`; standard library otherwise.

**Spec:** [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md) — this plan covers the 2a part of phase 2: §3.3 (answers in effect, pending update, merged tasks), §4.2 (the status side of answering; the write API is 2b), §4.5 (one write queue per repository, hook validation), §7 (inconsistent workspaces are reported, not repaired) and rulings 2.2, 2.3, 2.4, 2.6, 2.9, 2.13, 2.14, 2.16, 2.17 of §11.

**Architecture note:** [docs/superpowers/plans/2026-10-03-phase-2-architecture.md](2026-10-03-phase-2-architecture.md) — this plan produces exactly the exported names listed there under "Plan 2a". Where it had to decide more, see "Decisions beyond the architecture note" at the end.

## Global Constraints

- Module `github.com/emeland-io/custos`, `go 1.26.0` in `go.mod`. Third-party modules stay `go.yaml.in/yaml/v3`, `golang.org/x/mod`, `github.com/google/uuid`; nothing new.
- Git ≥ 2.38 at runtime (ruling 2.9). Tests use real git in temp dirs through `internal/gittest`; no mocks of git.
- Problems are `problem.Problem{Path, Rule, Message}`, printed `<path>: <rule>: <message>`. New rule names: `workspace-id`, `pin`, `answer` (ruling 2.14).
- Bot identity: `gitrepo.Bot = gitrepo.Signature{Name: "custos-bot", Email: "custos-bot@localhost"}`. The committer of every server-side commit is `gitrepo.Bot`; the author is the acting person.
- Server-side changes use Git plumbing on the bare repositories (temporary index, `commit-tree`, `update-ref` with the expected old value), one lock per repository, validated with the same rules as the pre-receive hook before the ref moves (ruling 2.3).
- A workspace's pin is resolved in the catalog repository of the same server; `catalog.url` in `custos.yaml` is informational and set from the public URL (ruling 2.4). A pin may move to any commit on the catalog's `main`, also an older one (ruling 2.16).
- Creating a workspace makes an initial commit with `custos.yaml` pinned to the current catalog `main` and fails while the catalog is empty. New flag `--public-url` (`CUSTOS_PUBLIC_URL`, default `http://127.0.0.1:8080`) (ruling 2.13). `workspace create` needs `--author "Name <email>"` (`CUSTOS_AUTHOR`) (ruling 2.6).
- No SQLite index: status and the book are computed on demand from the Git trees (ruling 2.2).
- Only `refs/heads/main` is validated; other branches accept anything.
- Commit messages: one imperative sentence without prefix, like the existing history ("Add …", "Reject …").

## Review Focus

1. **A workspace push runs its hook inside git's quarantine environment** (`GIT_OBJECT_DIRECTORY`, `GIT_ALTERNATE_OBJECT_DIRECTORIES`, `GIT_QUARANTINE_PATH`). The hook must still read the *catalog* repository, and still see the pushed objects of its own repository. Expected: valid pushes pass, invalid ones are rejected naming file and rule (tests `TestHookEnvironment` in Task 1, `TestWorkspacePushesAreCheckedAgainstCatalog` in Task 10).
2. **A push lands while the server writes to the same workspace.** Expected: the server write fails with `ErrConflict`; the push is never overwritten (test `TestUpdateWorkspaceConflict` in Task 8).
3. **Several server writes to one workspace at once.** Expected: they are serialised; none is lost or fails (test `TestUpdateWorkspaceConcurrent` in Task 8).
4. **Trees that never passed validation**, e.g. a repository edited on disk: group cycles, tasks with two current versions, generated tasks whose origins form a cycle, answer files stored under the wrong task. Expected: status and the book are computed without panic or endless loop, and `serve` reports the workspace instead of crashing (§7) (tests `TestComputeBrokenTrees`, `TestComputeOriginCycle` in Task 5, `TestCheckWorkspace` in Task 8).
5. **Creating a workspace too early or with bad input**: before the catalog was pushed, with an id that is not a UUID, without or with a malformed `--author`. Expected: a clear error and no half-created repository left behind (tests `TestCreateWorkspaceNeedsCatalog`, `TestCreateWorkspaceInvalidID` in Task 7, `TestWorkspaceCreate`, `TestWorkspaceCreateAuthor` in Task 9).

---

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/gitrepo/gitrepo.go` (modify) | Environment isolation for every git command (`Repo.InheritGitEnv`), `run` helper, `checkRev` |
| `internal/gitrepo/signature.go` (create) | `Signature`, `ParseSignature`, `Bot` |
| `internal/gitrepo/refs.go` (create) | `ResolveRef`, `ReadFile`, `Refs`, path checks |
| `internal/gitrepo/write.go` (create) | `Change`, `CommitRequest`, `WriteCommit`, `CommitTree`, `UpdateRef`, `DeleteRef`, `ErrRefMoved` |
| `internal/problem/problem.go` (modify) | Rule names `workspace-id`, `pin`, `answer` |
| `internal/workspace/against.go` (create) | `CheckAgainstCatalog` (answers against the pinned catalog) |
| `internal/workspace/config.go` (create) | `MarshalConfig` for `custos.yaml` |
| `internal/workspace/workspace.go` (modify) | Export `ConfigPath` |
| `internal/fixture/fixture.go` (modify) | `Config`, `PinnedWorkspace` helpers |
| `internal/rules/rules.go` (create) | `CheckCatalogUpdate`, `CheckWorkspaceUpdate`, shared by hook and store |
| `internal/status/status.go` (create) | `State`, `TaskStatus`, `Entry`, `Status`, `Compute` |
| `internal/status/markdown.go` (create) | `RenderMarkdown` |
| `internal/store/store.go` (create) | Layout, hooks, locks, `CreateWorkspace`, `CreateWorkspaceRepo`, errors |
| `internal/store/write.go` (create) | `UpdateWorkspace`, `SetRef`, `Load`, `Status`, `CheckWorkspace` |
| `internal/server/server.go` (modify) | HTTP only: `New(st)`, `Handler`, `OnCatalogPush` |
| `internal/hook/hook.go` (modify) | `ScriptOptions`, `Script`, `PreReceive` delegating to `rules` |
| `cmd/custos/serve.go`, `workspace.go`, `hook.go`, `main.go` (modify) | Flags `--public-url`, `--author`, `--catalog`, `--workspace`; store wiring |
| `README.md` (modify) | Requirements, flags, new rules |

Tests sit next to each file (`*_test.go`). Existing tests that change: `internal/server/server_test.go` (Task 9: `start` uses the store; the workspace and hook-installation tests move to `internal/store/store_test.go`), `internal/hook/hook_test.go` (Task 10: `PreReceive`/`Script` take `ScriptOptions`; `TestWorkspace` needs a real catalog), `cmd/custos/serve_test.go` (Task 9: `TestWorkspaceCreate` needs a catalog and `--author`). All other phase-1 tests stay unchanged and must keep passing.

---

### Task 1: Signatures, isolated git environment, reading refs and files

**Files:**
- Modify: `internal/gitrepo/gitrepo.go` (struct `Repo`, `InitBare`, `TreeFS`, `readBlobs`, `git`)
- Modify: `cmd/custos/hook.go:31`
- Create: `internal/gitrepo/signature.go`, `internal/gitrepo/refs.go`
- Test: `internal/gitrepo/signature_test.go`, `internal/gitrepo/refs_test.go`

**Interfaces:**
- Consumes: existing `Repo.git`, `Repo.entriesFS`, `splitNUL`, `entry` in `internal/gitrepo/gitrepo.go`.
- Produces:
  - `type Signature struct{ Name, Email string }`, `func ParseSignature(s string) (Signature, error)`, `func (s Signature) String() string`, `var Bot = Signature{Name: "custos-bot", Email: "custos-bot@localhost"}`; unexported `func (s Signature) check() error` (Task 2 uses it).
  - `Repo.InheritGitEnv bool` — only the repository a hook runs in sets it.
  - `func (r *Repo) ResolveRef(ref string) (oid string, ok bool, err error)` — `ok=false` when the ref does not exist; a full object id resolves to itself, append `"^{commit}"` to check that a commit exists.
  - `func (r *Repo) ReadFile(rev, path string) (data []byte, ok bool, err error)`
  - `func (r *Repo) Refs(prefix string) (map[string]string, error)` — full ref name → oid.
  - Unexported helpers for Task 2: `func (r *Repo) run(env []string, stdin []byte, args ...string) ([]byte, error)`, `func checkRev(rev string) error`, `func checkPath(path string) error`.

Why the environment matters: git runs a pre-receive hook with `GIT_DIR`, `GIT_OBJECT_DIRECTORY`, `GIT_ALTERNATE_OBJECT_DIRECTORIES` and `GIT_QUARANTINE_PATH` pointing at the pushed repository and its quarantined objects. From Task 10 on, a workspace hook also reads the catalog repository; if those variables leaked into `git -C catalog.git …`, git would look for catalog objects in the workspace. So every `Repo` drops them, except the hook's own repository (`InheritGitEnv: true`), which needs them to see the pushed objects.

- [ ] **Step 1: Write the failing tests**

`internal/gitrepo/signature_test.go`:

```go
package gitrepo

import "testing"

func TestParseSignature(t *testing.T) {
	sig, err := ParseSignature("  Jane Doe <jane@example.org> ")
	if err != nil || sig != (Signature{Name: "Jane Doe", Email: "jane@example.org"}) {
		t.Fatalf("%+v %v", sig, err)
	}
	if sig.String() != "Jane Doe <jane@example.org>" {
		t.Errorf("String() = %q", sig.String())
	}
	for _, bad := range []string{
		"",
		"Jane Doe",
		"jane@example.org",
		"<jane@example.org>",
		"Jane Doe <>",
		"Jane <x> <jane@example.org>",
		"Jane\nDoe <jane@example.org>",
		"Jane Doe <jane@example.org>\nX",
		"Jane Doe <jane @example.org>",
		"Jane Doe <jane@example.org",
	} {
		if _, err := ParseSignature(bad); err == nil {
			t.Errorf("ParseSignature(%q) accepted", bad)
		}
	}
}

func TestBot(t *testing.T) {
	if Bot.String() != "custos-bot <custos-bot@localhost>" {
		t.Errorf("Bot = %q", Bot.String())
	}
}
```

`internal/gitrepo/refs_test.go`:

```go
package gitrepo

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/gittest"
)

func TestResolveRef(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, map[string]string{"a.txt": "one"})
	r := &Repo{Dir: dir}
	if oid, ok, err := r.ResolveRef("refs/heads/main"); oid != c1 || !ok || err != nil {
		t.Errorf("main: %q %v %v", oid, ok, err)
	}
	if oid, ok, err := r.ResolveRef("refs/heads/missing"); oid != "" || ok || err != nil {
		t.Errorf("missing: %q %v %v", oid, ok, err)
	}
	if _, ok, err := r.ResolveRef(strings.Repeat("1", 40) + "^{commit}"); ok || err != nil {
		t.Errorf("unknown commit: %v %v", ok, err)
	}
	if _, _, err := r.ResolveRef("--all"); err == nil {
		t.Error("options must be rejected")
	}
	if _, _, err := (&Repo{Dir: filepath.Join(t.TempDir(), "none")}).ResolveRef("main"); err == nil {
		t.Error("a missing repository must be an error, not a missing ref")
	}
}

// TestHookEnvironment checks that a Repo ignores the variables git sets for
// a hook, which point at another repository's objects, unless it is the
// repository the hook runs in.
func TestHookEnvironment(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, map[string]string{"a.txt": "one"})
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "other.git"))
	t.Setenv("GIT_OBJECT_DIRECTORY", t.TempDir())
	if oid, ok, err := (&Repo{Dir: dir}).ResolveRef(c1 + "^{commit}"); oid != c1 || !ok || err != nil {
		t.Errorf("clean environment: %q %v %v", oid, ok, err)
	}
	if _, ok, _ := (&Repo{Dir: dir, InheritGitEnv: true}).ResolveRef(c1 + "^{commit}"); ok {
		t.Error("InheritGitEnv must keep GIT_DIR and GIT_OBJECT_DIRECTORY")
	}
}

func TestReadFile(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, map[string]string{"a.txt": "one", "sub/b.txt": "two"})
	r := &Repo{Dir: dir}
	if data, ok, err := r.ReadFile(c1, "sub/b.txt"); string(data) != "two" || !ok || err != nil {
		t.Errorf("sub/b.txt: %q %v %v", data, ok, err)
	}
	if data, ok, err := r.ReadFile("main", "a.txt"); string(data) != "one" || !ok || err != nil {
		t.Errorf("a.txt at main: %q %v %v", data, ok, err)
	}
	for _, path := range []string{"missing.txt", "sub", "b.txt"} {
		if _, ok, err := r.ReadFile(c1, path); ok || err != nil {
			t.Errorf("%s: %v %v", path, ok, err)
		}
	}
	for _, path := range []string{"", "/a.txt", "../a.txt", ".git/config", "sub/"} {
		if _, _, err := r.ReadFile(c1, path); err == nil {
			t.Errorf("path %q must be rejected", path)
		}
	}
	if _, _, err := r.ReadFile("missing", "a.txt"); err == nil {
		t.Error("an unknown revision must fail")
	}
	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	c2 := gittest.Commit(t, dir, nil)
	if _, _, err := r.ReadFile(c2, "link"); err == nil || !strings.Contains(err.Error(), "only regular files") {
		t.Errorf("symlink: %v", err)
	}
}

func TestRefs(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, map[string]string{"a.txt": "one"})
	gittest.Run(t, dir, "branch", "custos/pin/x")
	gittest.Run(t, dir, "branch", "custos/pinned")
	gittest.Run(t, dir, "tag", "v1")
	r := &Repo{Dir: dir}
	refs, err := r.Refs("refs/heads/custos/pin/")
	if err != nil || !maps.Equal(refs, map[string]string{"refs/heads/custos/pin/x": c1}) {
		t.Errorf("pin branches: %v %v", refs, err)
	}
	if all, err := r.Refs(""); err != nil || len(all) != 4 || all["refs/tags/v1"] != c1 {
		t.Errorf("all refs: %v %v", all, err)
	}
	empty, err := InitBare(filepath.Join(t.TempDir(), "e.git"))
	if err != nil {
		t.Fatal(err)
	}
	if refs, err := empty.Refs(""); err != nil || len(refs) != 0 {
		t.Errorf("empty repository: %v %v", refs, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/gitrepo/`
Expected: FAIL to compile: `undefined: ParseSignature`, `undefined: Bot`, `r.ResolveRef undefined`, `unknown field InheritGitEnv`.

- [ ] **Step 3: Isolate the environment in `internal/gitrepo/gitrepo.go`**

Replace:

```go
	"path/filepath"
	"strconv"
	"strings"
	"testing/fstest"
)

// Repo is a git repository, bare or not, at Dir.
type Repo struct {
	Dir string
}
```

with:

```go
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing/fstest"
)

// Repo is a git repository, bare or not, at Dir.
type Repo struct {
	Dir string
	// InheritGitEnv keeps the environment variables that point git at a
	// repository and its objects (GIT_DIR, GIT_OBJECT_DIRECTORY, ...). Only
	// the repository a pre-receive hook runs in sets it, because git passes
	// the quarantined objects of the push that way. Every other Repo drops
	// them, so that a hook can also read a second repository.
	InheritGitEnv bool
}

// repoEnv lists the variables that make git use another repository, object
// store or index than the one at Dir.
var repoEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_QUARANTINE_PATH", "GIT_COMMON_DIR",
	"GIT_NAMESPACE", "GIT_SHALLOW_FILE", "GIT_GRAFT_FILE", "GIT_PREFIX",
}

// cleanEnv returns the process environment without the variables in repoEnv.
func cleanEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(repoEnv, name)
	})
}

// command prepares git with args in r.Dir, with extra environment variables.
func (r *Repo) command(env []string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{"-C", r.Dir}, args...)...)
	base := cleanEnv()
	if r.InheritGitEnv {
		base = os.Environ()
	}
	cmd.Env = append(base, env...)
	return cmd
}
```

In `InitBare`:

Replace:

```go
	if out, err := exec.Command("git", "init", "--quiet", "--bare", "--initial-branch=main", dir).CombinedOutput(); err != nil {
```

with:

```go
	cmd := exec.Command("git", "init", "--quiet", "--bare", "--initial-branch=main", dir)
	cmd.Env = cleanEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
```

In `TreeFS`:

Replace:

```go
	if rev == "" || strings.HasPrefix(rev, "-") {
		return nil, fmt.Errorf("invalid revision %q", rev)
	}
```

with:

```go
	if err := checkRev(rev); err != nil {
		return nil, err
	}
```

In `readBlobs`:

Replace:

```go
	cmd := exec.Command("git", "-C", r.Dir, "cat-file", "--batch")
```

with:

```go
	cmd := r.command(nil, "cat-file", "--batch")
```

And the `git` method at the end of the file:

Replace:

```go
func (r *Repo) git(args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", r.Dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
```

with:

```go
func (r *Repo) git(args ...string) ([]byte, error) {
	return r.run(nil, nil, args...)
}

// run runs git with extra environment variables and stdin (nil for none).
func (r *Repo) run(env []string, stdin []byte, args ...string) ([]byte, error) {
	cmd := r.command(env, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// checkRev rejects revisions git would read as an option.
func checkRev(rev string) error {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return fmt.Errorf("invalid revision %q", rev)
	}
	return nil
}
```

- [ ] **Step 4: Keep the hook's own repository environment in `cmd/custos/hook.go`**

Without this the phase-1 push tests in `internal/server` fail with `not a tree object`, because the hook can no longer see the quarantined objects.

Replace:

```go
	ps, err := hook.PreReceive(&gitrepo.Repo{Dir: "."}, kind, stdin)
```

with:

```go
	ps, err := hook.PreReceive(&gitrepo.Repo{Dir: ".", InheritGitEnv: true}, kind, stdin)
```

- [ ] **Step 5: Write `internal/gitrepo/signature.go`**

```go
package gitrepo

import (
	"errors"
	"fmt"
	"strings"
)

// Signature names the author or committer of a commit.
type Signature struct{ Name, Email string }

// Bot is the committer of every commit custos writes, and the author of the
// commits it makes on its own.
var Bot = Signature{Name: "custos-bot", Email: "custos-bot@localhost"}

// ParseSignature parses "Jane Doe <jane@example.org>".
func ParseSignature(s string) (Signature, error) {
	s = strings.TrimSpace(s)
	open := strings.LastIndex(s, "<")
	if open < 0 || !strings.HasSuffix(s, ">") {
		return Signature{}, fmt.Errorf("%q is not of the form Name <email>", s)
	}
	sig := Signature{Name: strings.TrimSpace(s[:open]), Email: s[open+1 : len(s)-1]}
	if err := sig.check(); err != nil {
		return Signature{}, err
	}
	return sig, nil
}

// String returns the signature as "Name <email>".
func (s Signature) String() string { return s.Name + " <" + s.Email + ">" }

func (s Signature) check() error {
	if s.Name == "" || s.Email == "" {
		return errors.New("a signature needs a name and an email, as in Jane Doe <jane@example.org>")
	}
	if strings.ContainsAny(s.Name, "<>\n\r\x00") || strings.ContainsAny(s.Email, "<>\n\r\x00 \t") {
		return fmt.Errorf("%q: name and email must not contain <, > or line breaks, and the email no spaces", s.String())
	}
	return nil
}
```

- [ ] **Step 6: Write `internal/gitrepo/refs.go`**

`git rev-parse --verify --quiet` exits 1 without output for a missing ref; `git ls-tree` takes the path literally (no globbing). `ReadFile` reuses `entriesFS`, so symlinks and submodules are rejected exactly as in `TreeFS`.

```go
package gitrepo

import (
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
)

// ResolveRef returns the object id ref points to; ok is false when there is
// no such ref. ref may be any revision git understands. A full object id
// resolves to itself even when the object does not exist; append "^{commit}"
// to check that a commit exists.
func (r *Repo) ResolveRef(ref string) (oid string, ok bool, err error) {
	if err := checkRev(ref); err != nil {
		return "", false, err
	}
	out, err := r.git("rev-parse", "--verify", "--quiet", "--end-of-options", ref)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(out)), true, nil
}

// ReadFile returns the content of the file at path in revision rev; ok is
// false when rev has no file at path. Like TreeFS it reads the blob as
// stored and rejects symlinks and submodules.
func (r *Repo) ReadFile(rev, path string) (data []byte, ok bool, err error) {
	if err := checkRev(rev); err != nil {
		return nil, false, err
	}
	if err := checkPath(path); err != nil {
		return nil, false, err
	}
	out, err := r.git("ls-tree", "-z", "--full-tree", rev, "--", path)
	if err != nil {
		return nil, false, err
	}
	for _, rec := range splitNUL(out) {
		meta, p, _ := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if p != path || len(f) != 3 || f[1] == "tree" {
			continue
		}
		files, err := r.entriesFS([]entry{{mode: f[0], oid: f[2], path: p}})
		if err != nil {
			return nil, false, err
		}
		return files[p].Data, true, nil
	}
	return nil, false, nil
}

// Refs returns the refs whose full name starts with prefix ("" for all),
// mapped to the object ids they point to.
func (r *Repo) Refs(prefix string) (map[string]string, error) {
	out, err := r.git("for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		oid, name, ok := strings.Cut(line, " ")
		if ok && strings.HasPrefix(name, prefix) {
			refs[name] = oid
		}
	}
	return refs, nil
}

// checkPath accepts slash-separated paths relative to the repository root
// that git can store: no empty, "." or ".." elements, no .git directory.
func checkPath(path string) error {
	if !fs.ValidPath(path) || path == "." || strings.ContainsAny(path, "\\\x00") {
		return fmt.Errorf("invalid path %q", path)
	}
	for _, elem := range strings.Split(path, "/") {
		if strings.EqualFold(elem, ".git") {
			return fmt.Errorf("invalid path %q: .git is reserved", path)
		}
	}
	return nil
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/gitrepo/ && go test ./...`
Expected: PASS (the second command proves that pushes in `internal/server` still work).

- [ ] **Step 8: Commit**

```bash
git add internal/gitrepo cmd/custos/hook.go
git commit -m "Add signatures and ref reading, and isolate git from hook variables"
```

---

### Task 2: Writing commits and moving refs

**Files:**
- Create: `internal/gitrepo/write.go`
- Test: `internal/gitrepo/write_test.go`

**Interfaces:**
- Consumes (Task 1): `Repo.run`, `Repo.ResolveRef`, `checkRev`, `checkPath`, `Signature.check`, `Bot`.
- Produces:
  - `var ErrRefMoved = errors.New("ref moved")`
  - `type Change struct { Path string; Data []byte; Delete bool }`
  - `type CommitRequest struct { Base string; Parents []string; Changes []Change; Author Signature; Message string }`
  - `func (r *Repo) WriteCommit(req CommitRequest) (string, error)` — writes objects only; committer `Bot`; temporary index file.
  - `func (r *Repo) CommitTree(tree string, parents []string, author Signature, message string) (string, error)`
  - `func (r *Repo) UpdateRef(ref, newOID, oldOID string) error` — `oldOID ""` = ref must not exist; error wrapping `ErrRefMoved` on mismatch.
  - `func (r *Repo) DeleteRef(ref, oldOID string) error`

Plumbing facts this code relies on (checked with git 2.55): `git update-index --index-info` silently *ignores* invalid paths such as `.git/config` or `../x` (exit 0), so paths are checked before git sees them; it replaces a directory by a file of the same name and vice versa; `--force-remove` needs a work tree, so deletions are written as index-info lines with mode `0` and an all-zero object id of the repository's hash length. `update-ref` messages are localized, so a lost compare-and-swap is detected by reading the ref again. `commit-tree` honours `commit.gpgSign`, so `--no-gpg-sign` is passed.

- [ ] **Step 1: Write the failing test** `internal/gitrepo/write_test.go`

```go
package gitrepo

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/gittest"
)

var jane = Signature{Name: "Jane Doe", Email: "jane@example.org"}

func bare(t *testing.T) *Repo {
	t.Helper()
	r, err := InitBare(filepath.Join(t.TempDir(), "x.git"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func readAll(t *testing.T, r *Repo, rev string) map[string]string {
	t.Helper()
	fsys, err := r.TreeFS(rev)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := fs.ReadFile(fsys, p)
			files[p] = string(data)
		}
		return err
	})
	return files
}

func TestWriteCommit(t *testing.T) {
	r := bare(t)
	c1, err := r.WriteCommit(CommitRequest{
		Changes: []Change{{Path: "a.txt", Data: []byte("one")}, {Path: "sub/b.txt", Data: []byte("two")}, {Path: "gone.txt", Delete: true}},
		Author:  jane,
		Message: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r, c1); len(got) != 2 || got["a.txt"] != "one" || got["sub/b.txt"] != "two" {
		t.Errorf("first tree: %v", got)
	}
	c2, err := r.WriteCommit(CommitRequest{
		Base:    c1,
		Parents: []string{c1},
		Changes: []Change{{Path: "a.txt", Delete: true}, {Path: "sub/c.txt", Data: []byte("three")}},
		Author:  jane,
		Message: "second",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r, c2); len(got) != 2 || got["sub/b.txt"] != "two" || got["sub/c.txt"] != "three" {
		t.Errorf("second tree: %v", got)
	}
	if parent := gittest.Run(t, r.Dir, "rev-parse", c2+"^"); parent != c1 {
		t.Errorf("parent = %s, want %s", parent, c1)
	}
	if who := gittest.Run(t, r.Dir, "log", "-1", "--format=%an <%ae>|%cn <%ce>|%B", c2); who != "Jane Doe <jane@example.org>|custos-bot <custos-bot@localhost>|second" {
		t.Errorf("commit: %q", who)
	}
	if refs, _ := r.Refs(""); len(refs) != 0 {
		t.Errorf("WriteCommit must not move refs: %v", refs)
	}
}

func TestWriteCommitLeavesIndexAlone(t *testing.T) {
	dir := gittest.Init(t)
	base := gittest.Commit(t, dir, map[string]string{"a.txt": "one"})
	before := gittest.Run(t, dir, "ls-files", "-s")
	outside := filepath.Join(t.TempDir(), "index")
	t.Setenv("GIT_INDEX_FILE", outside)
	r := &Repo{Dir: dir}
	if _, err := r.WriteCommit(CommitRequest{Base: base, Parents: []string{base}, Changes: []Change{{Path: "b.txt", Data: []byte("x")}}, Author: jane, Message: "m"}); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("GIT_INDEX_FILE") // t.Setenv restores it after the test
	if after := gittest.Run(t, dir, "ls-files", "-s"); after != before {
		t.Errorf("index changed:\n%s\nwas\n%s", after, before)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Error("an inherited GIT_INDEX_FILE was written")
	}
}

func TestWriteCommitRejects(t *testing.T) {
	r := bare(t)
	for _, req := range []CommitRequest{
		{Changes: []Change{{Path: ".git/config", Data: []byte("x")}}, Author: jane, Message: "m"},
		{Changes: []Change{{Path: "../x", Data: []byte("x")}}, Author: jane, Message: "m"},
		{Changes: []Change{{Path: "/abs", Data: []byte("x")}}, Author: jane, Message: "m"},
		{Changes: []Change{{Path: "a.txt", Data: []byte("x")}}, Author: Signature{Name: "No Email"}, Message: "m"},
		{Base: strings.Repeat("1", 40), Author: jane, Message: "m"},
		{Base: "--all", Author: jane, Message: "m"},
	} {
		if _, err := r.WriteCommit(req); err == nil {
			t.Errorf("accepted %+v", req)
		}
	}
}

func TestWriteCommitIgnoresSigningConfig(t *testing.T) {
	r := bare(t)
	gittest.Run(t, r.Dir, "config", "commit.gpgsign", "true")
	gittest.Run(t, r.Dir, "config", "gpg.program", "false")
	if _, err := r.WriteCommit(CommitRequest{Author: jane, Message: "m"}); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateAndDeleteRef(t *testing.T) {
	r := bare(t)
	c1, err := r.WriteCommit(CommitRequest{Changes: []Change{{Path: "a", Data: []byte("1")}}, Author: jane, Message: "1"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := r.WriteCommit(CommitRequest{Base: c1, Parents: []string{c1}, Changes: []Change{{Path: "a", Data: []byte("2")}}, Author: jane, Message: "2"})
	if err != nil {
		t.Fatal(err)
	}
	const main = "refs/heads/main"
	if err := r.UpdateRef(main, c1, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef(main, c2, ""); !errors.Is(err, ErrRefMoved) {
		t.Errorf("create over existing ref: %v", err)
	}
	if err := r.UpdateRef(main, c2, c2); !errors.Is(err, ErrRefMoved) {
		t.Errorf("wrong expected value: %v", err)
	}
	if err := r.UpdateRef(main, c2, c1); err != nil {
		t.Fatal(err)
	}
	if oid, _, _ := r.ResolveRef(main); oid != c2 {
		t.Errorf("main = %s, want %s", oid, c2)
	}
	if err := r.UpdateRef("refs/heads/bad..name", c1, ""); err == nil || errors.Is(err, ErrRefMoved) {
		t.Errorf("bad name: %v", err)
	}
	if err := r.UpdateRef("main", c1, ""); err == nil {
		t.Error("a ref outside refs/ must be rejected")
	}
	if err := r.UpdateRef(main, "HEAD", c2); err == nil {
		t.Error("an abbreviated new value must be rejected")
	}
	if err := r.DeleteRef(main, c1); !errors.Is(err, ErrRefMoved) {
		t.Errorf("delete with wrong value: %v", err)
	}
	if err := r.DeleteRef(main, c2); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := r.ResolveRef(main); ok {
		t.Error("main still exists")
	}
	if err := r.DeleteRef(main, c2); !errors.Is(err, ErrRefMoved) {
		t.Errorf("delete of a missing ref: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/gitrepo/`
Expected: FAIL to compile: `undefined: CommitRequest`, `undefined: Change`, `undefined: ErrRefMoved`.

- [ ] **Step 3: Write `internal/gitrepo/write.go`**

```go
package gitrepo

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrRefMoved reports that UpdateRef or DeleteRef found the ref at another
// value than the expected one, because someone else moved it meanwhile.
var ErrRefMoved = errors.New("ref moved")

var oidRE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// Change sets or deletes one file in a commit.
type Change struct {
	Path   string // slash-separated, relative to the repository root
	Data   []byte
	Delete bool
}

// CommitRequest describes a commit for WriteCommit.
type CommitRequest struct {
	Base    string   // commit whose tree is the starting point; "" = empty tree
	Parents []string // usually []string{Base}; nil for a root commit
	Changes []Change
	Author  Signature
	Message string
}

// WriteCommit writes the tree of Base with Changes applied and a commit of
// it, and returns the commit id. It moves no ref. It builds the tree in a
// temporary index file, never in the repository's own index or one named by
// an inherited GIT_INDEX_FILE. Files are written as regular, non-executable
// files; a file replaces a directory of the same name and vice versa.
func (r *Repo) WriteCommit(req CommitRequest) (string, error) {
	tree, err := r.writeTree(req.Base, req.Changes)
	if err != nil {
		return "", err
	}
	return r.CommitTree(tree, req.Parents, req.Author, req.Message)
}

func (r *Repo) writeTree(base string, changes []Change) (string, error) {
	tmp, err := os.MkdirTemp("", "custos-index-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	zero := ""
	if base == "" {
		if _, err := r.run(env, nil, "read-tree", "--empty"); err != nil {
			return "", err
		}
	} else {
		oid, ok, err := r.ResolveRef(base + "^{commit}")
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("base %s is not a commit", base)
		}
		if _, err := r.run(env, nil, "read-tree", oid); err != nil {
			return "", err
		}
		zero = strings.Repeat("0", len(oid))
	}
	var info bytes.Buffer
	for _, c := range changes {
		if err := checkPath(c.Path); err != nil {
			return "", err
		}
		if c.Delete {
			if zero != "" { // nothing to delete in an empty tree
				fmt.Fprintf(&info, "0 %s\t%s\x00", zero, c.Path)
			}
			continue
		}
		out, err := r.run(nil, c.Data, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&info, "100644 %s\t%s\x00", strings.TrimSpace(string(out)), c.Path)
	}
	if info.Len() > 0 {
		if _, err := r.run(env, info.Bytes(), "update-index", "-z", "--index-info"); err != nil {
			return "", err
		}
	}
	out, err := r.run(env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CommitTree writes a commit of tree with the given parents, authored by
// author and committed by Bot, and returns its id. It moves no ref and
// never signs, whatever the git configuration says.
func (r *Repo) CommitTree(tree string, parents []string, author Signature, message string) (string, error) {
	if err := author.check(); err != nil {
		return "", err
	}
	if err := checkRev(tree); err != nil {
		return "", err
	}
	args := []string{"commit-tree", "--no-gpg-sign"}
	for _, p := range parents {
		if err := checkRev(p); err != nil {
			return "", err
		}
		args = append(args, "-p", p)
	}
	args = append(args, "-F", "-", tree)
	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + author.Name, "GIT_AUTHOR_EMAIL=" + author.Email,
		"GIT_COMMITTER_NAME=" + Bot.Name, "GIT_COMMITTER_EMAIL=" + Bot.Email,
	}
	out, err := r.run(env, []byte(message), args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// UpdateRef points ref at newOID if it currently points at oldOID; oldOID
// "" means the ref must not exist yet. It returns an error wrapping
// ErrRefMoved when the ref has another value.
func (r *Repo) UpdateRef(ref, newOID, oldOID string) error {
	if err := checkRefName(ref); err != nil {
		return err
	}
	if !oidRE.MatchString(newOID) || (oldOID != "" && !oidRE.MatchString(oldOID)) {
		return fmt.Errorf("update %s: object ids must be full hashes, got %q and %q", ref, newOID, oldOID)
	}
	expect := oldOID
	if expect == "" {
		expect = strings.Repeat("0", len(newOID))
	}
	if _, err := r.git("update-ref", "--no-deref", ref, newOID, expect); err != nil {
		return r.moved(ref, oldOID, err)
	}
	return nil
}

// DeleteRef deletes ref if it currently points at oldOID. It returns an
// error wrapping ErrRefMoved when the ref has another value or is gone.
func (r *Repo) DeleteRef(ref, oldOID string) error {
	if err := checkRefName(ref); err != nil {
		return err
	}
	if !oidRE.MatchString(oldOID) {
		return fmt.Errorf("delete %s: expected value must be a full hash, got %q", ref, oldOID)
	}
	if _, err := r.git("update-ref", "--no-deref", "-d", ref, oldOID); err != nil {
		return r.moved(ref, oldOID, err)
	}
	return nil
}

// moved turns the failure of a compare-and-swap into ErrRefMoved when the ref
// is not at the expected value, without parsing git's localized messages.
func (r *Repo) moved(ref, want string, err error) error {
	cur, ok, rerr := r.ResolveRef(ref)
	if rerr != nil || cur == want {
		return err
	}
	if !ok {
		cur = "nothing"
	}
	if want == "" {
		want = "nothing"
	}
	return fmt.Errorf("%w: %s points to %s, expected %s", ErrRefMoved, ref, cur, want)
}

func checkRefName(ref string) error {
	if !strings.HasPrefix(ref, "refs/") {
		return fmt.Errorf("invalid ref %q: must start with refs/", ref)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/gitrepo/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gitrepo/write.go internal/gitrepo/write_test.go
git commit -m "Write commits with a temporary index and move refs with compare-and-swap"
```

---

### Task 3: Answers checked against the pinned catalog

**Files:**
- Modify: `internal/problem/problem.go` (rule constants)
- Create: `internal/workspace/against.go`
- Test: `internal/workspace/against_test.go`

**Interfaces:**
- Consumes: `catalog.Catalog` (`Tasks *task.Graph`), `workspace.Workspace` (`Answers`, `Graph`), `task.Graph.Has/Lookup`.
- Produces: `problem.RuleWorkspaceID = "workspace-id"`, `problem.RulePin = "pin"`, `problem.RuleAnswer = "answer"`; `func CheckAgainstCatalog(w *Workspace, c *catalog.Catalog) []problem.Problem` in package `workspace`, problems sorted by path.

- [ ] **Step 1: Add the rule names to `internal/problem/problem.go`**

Replace:

```go
	RuleHistory       = "history"        // main was deleted or rewritten
)
```

with:

```go
	RuleHistory       = "history"        // main was deleted or rewritten

	// Workspace rules that need the server's catalog (ruling 2.14).
	RuleWorkspaceID = "workspace-id" // custos.yaml names another workspace than the repository
	RulePin         = "pin"          // the pinned commit is not on the catalog's main
	RuleAnswer      = "answer"       // an answer does not fit the task version it names
)
```

- [ ] **Step 2: Write the failing test** `internal/workspace/against_test.go`

```go
package workspace

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/problem"
)

// choiceCatalog is fixture.Catalog with TaskB 1.0.0 asking for a choice.
func choiceCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	f := fixture.Catalog()
	path := fixture.TaskPath(fixture.TaskB, "1.0.0")
	f[path] = strings.Replace(f[path], "answer_type: text\n", "answer_type: choice\nchoices: [yes, no]\n", 1)
	c, ps := catalog.Load(fixture.MapFS(f))
	fixture.WantNone(t, ps)
	return c
}

func answer(id, version, typ, value string) string {
	return "---\ntask: " + id + "\ntask_version: " + version + "\ntype: " + typ + "\nvalue: " + value + "\n---\n"
}

func TestCheckAgainstCatalog(t *testing.T) {
	pathB := "answers/" + fixture.TaskB + ".md"
	pathC := "answers/" + fixture.TaskC + ".md"
	unknown := "0f0e0d0c-0b0a-4908-8706-050403020100"
	tests := []struct {
		name                 string
		files                map[string]string
		path, rule, contains string // empty path: no problems expected
	}{
		{"catalog task", nil, "", "", ""},
		{"generated task", map[string]string{pathC: answer(fixture.TaskC, "1.0.0", "timestamp", "2026-10-02T14:00:00Z")}, "", "", ""},
		{"choice", map[string]string{pathB: answer(fixture.TaskB, "1.0.0", "choice", "yes")}, "", "", ""},
		{"unknown task", map[string]string{"answers/" + unknown + ".md": answer(unknown, "1.0.0", "text", "x")},
			"answers/" + unknown + ".md", problem.RuleAnswer, "neither in the pinned catalog nor a generated task"},
		{"unknown catalog version", map[string]string{answerPath: answer(fixture.TaskA, "9.0.0", "text", "x")},
			answerPath, problem.RuleAnswer, "has no version 9.0.0 in the pinned catalog"},
		{"unknown generated version", map[string]string{pathC: answer(fixture.TaskC, "2.0.0", "timestamp", "2026-10-02T14:00:00Z")},
			pathC, problem.RuleAnswer, "has no version 2.0.0 in the generated tasks"},
		{"wrong type", map[string]string{answerPath: answer(fixture.TaskA, "1.0.0", "path", "docs/build.md")},
			answerPath, problem.RuleAnswer, "type path does not match answer_type text of task " + fixture.TaskA + "@1.0.0"},
		{"value not a choice", map[string]string{pathB: answer(fixture.TaskB, "1.0.0", "choice", "maybe")},
			pathB, problem.RuleAnswer, `value "maybe" is not one of the choices of task ` + fixture.TaskB + "@1.0.0: yes, no"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := fixture.Workspace()
			for p, c := range tt.files {
				f[p] = c
			}
			w, ps := Load(fixture.MapFS(f))
			fixture.WantNone(t, ps)
			got := CheckAgainstCatalog(w, choiceCatalog(t))
			if tt.path == "" {
				fixture.WantNone(t, got)
				return
			}
			fixture.WantProblem(t, got, tt.path, tt.rule, tt.contains)
			if len(got) != 1 {
				t.Errorf("want exactly one problem, got %v", got)
			}
		})
	}
}

// TestCheckAgainstCatalogSkipsMalformedAnswers leaves malformed answers to
// Check, so a push shows each mistake once.
func TestCheckAgainstCatalogSkipsMalformedAnswers(t *testing.T) {
	f := fixture.Workspace()
	f[answerPath] = answer(fixture.TaskA, "one", "text", "x")
	f["answers/not-a-uuid.md"] = answer("not-a-uuid", "1.0.0", "text", "x")
	w, _ := Load(fixture.MapFS(f))
	fixture.WantNone(t, CheckAgainstCatalog(w, choiceCatalog(t)))
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/workspace/`
Expected: FAIL to compile: `undefined: CheckAgainstCatalog`.

- [ ] **Step 4: Write `internal/workspace/against.go`**

Package `workspace` may import `catalog`: `catalog` imports only `task`, `problem`, `repofile` and `semver`, so there is no cycle.

```go
package workspace

import (
	"maps"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

// CheckAgainstCatalog applies ruling 2.14 to answers: each answer names a task
// of the catalog or a generated task of w, a task_version that exists for it,
// the same answer type, and for choice tasks one of its choices. c is the
// catalog at the workspace's pin. Answers whose task or task_version is
// malformed are skipped; Check reports them.
func CheckAgainstCatalog(w *Workspace, c *catalog.Catalog) []problem.Problem {
	var ps problem.List
	for _, path := range slices.Sorted(maps.Keys(w.Answers)) {
		a := w.Answers[path]
		if !task.ValidID(a.Task) || !semver.Valid(a.TaskVersion) {
			continue
		}
		graph, where := c.Tasks, "the pinned catalog"
		if !graph.Has(a.Task) {
			graph, where = w.Graph, "the generated tasks"
		}
		if !graph.Has(a.Task) {
			ps.Add(path, problem.RuleAnswer, "task %s is neither in the pinned catalog nor a generated task", a.Task)
			continue
		}
		v, ok := graph.Lookup(task.Ref{ID: a.Task, Version: a.TaskVersion})
		if !ok {
			ps.Add(path, problem.RuleAnswer, "task %s has no version %s in %s", a.Task, a.TaskVersion, where)
			continue
		}
		if a.Type != v.AnswerType {
			ps.Add(path, problem.RuleAnswer, "type %s does not match answer_type %s of task %s", a.Type, v.AnswerType, v.Ref())
			continue
		}
		if v.AnswerType == task.AnswerChoice && !slices.Contains(v.Choices, a.Value) {
			ps.Add(path, problem.RuleAnswer, "value %q is not one of the choices of task %s: %s", a.Value, v.Ref(), strings.Join(v.Choices, ", "))
		}
	}
	return ps
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/workspace/ ./internal/problem/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/problem/problem.go internal/workspace/against.go internal/workspace/against_test.go
git commit -m "Check answers against the task versions of the pinned catalog"
```

---

### Task 4: Shared rules for moving `main`

**Files:**
- Modify: `internal/workspace/workspace.go:18` (export `ConfigPath`)
- Modify: `internal/fixture/fixture.go` (`Config`, `PinnedWorkspace`)
- Create: `internal/rules/rules.go`
- Test: `internal/rules/rules_test.go`

**Interfaces:**
- Consumes: `gitrepo.Repo.TreeFS/IsAncestor/ResolveRef` (Task 1), `gitrepo.IsZero`, `catalog.Check/CheckImmutable/Load`, `workspace.Load`, `workspace.CheckAgainstCatalog` (Task 3), rule names (Task 3).
- Produces:
  - `func CheckCatalogUpdate(cat *gitrepo.Repo, oldOID, newOID string) ([]problem.Problem, error)`
  - `func CheckWorkspaceUpdate(ws, cat *gitrepo.Repo, id, oldOID, newOID string) ([]problem.Problem, error)`
  - `oldOID` `""` or all zeros = no previous `main`; `newOID` `""` or all zeros = delete.
  - `workspace.ConfigPath = "custos.yaml"`
  - `fixture.Config(id, commit string) string`, `fixture.PinnedWorkspace(commit string) map[string]string` (test helpers).

The catalog half is the existing `hook.checkMain` logic for catalogs, moved; `hook` keeps its own copy until Task 10 switches it over.

- [ ] **Step 1: Export the config path in `internal/workspace/workspace.go`**

Replace:

```go
const configPath = "custos.yaml"
```

with:

```go
// ConfigPath is the path of the workspace configuration file.
const ConfigPath = "custos.yaml"
```

and rename the five uses of `configPath` in `loadConfig` to `ConfigPath` (`repofile.ReadYAML(fsys, ConfigPath, &w.Config)` and the four `ps.Add(ConfigPath, …)` calls). Run `grep -n configPath internal/workspace/*.go` afterwards; it must print nothing.

- [ ] **Step 2: Add test helpers to `internal/fixture/fixture.go`**

Replace:

```go
		"custos.yaml":                      "workspace: " + WorkspaceID + "\ncatalog:\n  url: https://custos.example.org/git/catalog.git\n  commit: " + Commit + "\n",
```

with:

```go
		"custos.yaml":                      Config(WorkspaceID, Commit),
```

and insert before `// MapFS turns a file map into an fs.FS.`:

```go
// Config returns a custos.yaml for workspace id, pinned to catalog commit.
func Config(id, commit string) string {
	return "workspace: " + id + "\ncatalog:\n  url: https://custos.example.org/git/catalog.git\n  commit: " + commit + "\n"
}

// PinnedWorkspace returns Workspace with its pin set to commit, usually a
// commit of Catalog in a test repository.
func PinnedWorkspace(commit string) map[string]string {
	f := Workspace()
	f["custos.yaml"] = Config(WorkspaceID, commit)
	return f
}
```

- [ ] **Step 3: Write the failing test** `internal/rules/rules_test.go`

```go
package rules

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/problem"
)

var zero = strings.Repeat("0", 40)

func checkCatalog(t *testing.T, dir, oldOID, newOID string) []problem.Problem {
	t.Helper()
	ps, err := CheckCatalogUpdate(&gitrepo.Repo{Dir: dir}, oldOID, newOID)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestCatalogUpdate(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Catalog())
	fixture.WantNone(t, checkCatalog(t, dir, "", c1))
	fixture.WantNone(t, checkCatalog(t, dir, zero, c1))

	path := fixture.TaskPath(fixture.TaskA, "1.0.0")
	c2 := gittest.Commit(t, dir, map[string]string{path: fixture.TaskFile(fixture.TaskA, "1.0.0") + "Changed.\n"})
	fixture.WantProblem(t, checkCatalog(t, dir, c1, c2), path, problem.RuleImmutable, "was changed")
	fixture.WantProblem(t, checkCatalog(t, dir, c2, c1), "", problem.RuleHistory, "cannot be rewritten")
	fixture.WantProblem(t, checkCatalog(t, dir, c1, zero), "", problem.RuleHistory, "cannot be deleted")

	c3 := gittest.Commit(t, dir, map[string]string{"groups/index.yaml": "groups: [missing]\n"})
	fixture.WantProblem(t, checkCatalog(t, dir, c2, c3), "groups/index.yaml", problem.RuleGroups, "unknown group")
}

// world is a catalog and a workspace repository, the workspace pinned to
// the catalog's first commit.
type world struct {
	cat, ws  *gitrepo.Repo
	catMain  string // first commit on the catalog's main
	wsCommit string // first commit on the workspace's main
}

func newWorld(t *testing.T) world {
	t.Helper()
	catDir := gittest.Init(t)
	c1 := gittest.Commit(t, catDir, fixture.Catalog())
	wsDir := gittest.Init(t)
	w1 := gittest.Commit(t, wsDir, fixture.PinnedWorkspace(c1))
	return world{cat: &gitrepo.Repo{Dir: catDir}, ws: &gitrepo.Repo{Dir: wsDir}, catMain: c1, wsCommit: w1}
}

func (w world) check(t *testing.T, oldOID, newOID string) []problem.Problem {
	t.Helper()
	ps, err := CheckWorkspaceUpdate(w.ws, w.cat, fixture.WorkspaceID, oldOID, newOID)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

// commit adds files to the workspace and returns the new commit.
func (w world) commit(t *testing.T, files map[string]string) string {
	t.Helper()
	return gittest.Commit(t, w.ws.Dir, files)
}

func TestWorkspaceUpdateValid(t *testing.T) {
	w := newWorld(t)
	fixture.WantNone(t, w.check(t, "", w.wsCommit))
	fixture.WantNone(t, w.check(t, zero, w.wsCommit))
}

func TestWorkspaceUpdateHistory(t *testing.T) {
	w := newWorld(t)
	w2 := w.commit(t, map[string]string{"README.md": "x"})
	fixture.WantNone(t, w.check(t, w.wsCommit, w2))
	fixture.WantProblem(t, w.check(t, w2, w.wsCommit), "", problem.RuleHistory, "cannot be rewritten")
	fixture.WantProblem(t, w.check(t, w2, zero), "", problem.RuleHistory, "cannot be deleted")
}

func TestWorkspaceUpdateID(t *testing.T) {
	w := newWorld(t)
	other := "0f0e0d0c-0b0a-4908-8706-050403020100"
	w2 := w.commit(t, map[string]string{"custos.yaml": fixture.Config(other, w.catMain)})
	fixture.WantProblem(t, w.check(t, w.wsCommit, w2), "custos.yaml", problem.RuleWorkspaceID,
		"workspace "+other+" does not match this repository, which belongs to workspace "+fixture.WorkspaceID)
}

func TestWorkspaceUpdatePin(t *testing.T) {
	w := newWorld(t)
	// A newer catalog main, and a commit on a catalog draft branch.
	newer := gittest.Commit(t, w.cat.Dir, map[string]string{"README.md": "catalog"})
	gittest.Run(t, w.cat.Dir, "checkout", "--quiet", "-b", "draft")
	draft := gittest.Commit(t, w.cat.Dir, map[string]string{"README.md": "draft"})
	gittest.Run(t, w.cat.Dir, "checkout", "--quiet", "main")

	toNewer := w.commit(t, map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, newer)})
	fixture.WantNone(t, w.check(t, w.wsCommit, toNewer))
	back := w.commit(t, map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, w.catMain)})
	fixture.WantNone(t, w.check(t, toNewer, back)) // ruling 2.16: older pins are fine

	toDraft := w.commit(t, map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, draft)})
	fixture.WantProblem(t, w.check(t, back, toDraft), "custos.yaml", problem.RulePin, "is not on the catalog's main branch")

	unknown := w.commit(t, map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, fixture.Commit)})
	fixture.WantProblem(t, w.check(t, toDraft, unknown), "custos.yaml", problem.RulePin, "does not exist in this server's catalog")

	tree := gittest.Run(t, w.cat.Dir, "rev-parse", "main^{tree}")
	toTree := w.commit(t, map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, tree)})
	fixture.WantProblem(t, w.check(t, unknown, toTree), "custos.yaml", problem.RulePin, "does not exist in this server's catalog")
}

func TestWorkspaceUpdateEmptyCatalog(t *testing.T) {
	w := newWorld(t)
	empty := &gitrepo.Repo{Dir: gittest.Init(t)}
	ps, err := CheckWorkspaceUpdate(w.ws, empty, fixture.WorkspaceID, "", w.wsCommit)
	if err != nil {
		t.Fatal(err)
	}
	fixture.WantProblem(t, ps, "custos.yaml", problem.RulePin, "the catalog has no main branch yet")
}

func TestWorkspaceUpdateAnswers(t *testing.T) {
	w := newWorld(t)
	path := "answers/" + fixture.TaskA + ".md"
	w2 := w.commit(t, map[string]string{path: strings.Replace(fixture.AnswerFile, "task_version: 1.0.0", "task_version: 1.2.0", 1)})
	ps := w.check(t, w.wsCommit, w2)
	fixture.WantProblem(t, ps, path, problem.RuleAnswer, "has no version 1.2.0 in the pinned catalog")
	if len(ps) != 1 {
		t.Errorf("want one problem, got %v", ps)
	}
}

// TestWorkspaceUpdateBrokenConfig reports a broken custos.yaml once, without
// follow-up problems about the id, the pin or the answers.
func TestWorkspaceUpdateBrokenConfig(t *testing.T) {
	w := newWorld(t)
	w2 := w.commit(t, map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, "abc123")})
	ps := w.check(t, w.wsCommit, w2)
	fixture.WantProblem(t, ps, "custos.yaml", problem.RuleFormat, "full commit hash")
	if len(ps) != 1 {
		t.Errorf("want one problem, got %v", ps)
	}
}

func TestWorkspaceUpdateFormat(t *testing.T) {
	w := newWorld(t)
	w2 := w.commit(t, map[string]string{"answers/notes.txt": "x"})
	fixture.WantProblem(t, w.check(t, w.wsCommit, w2), "answers/notes.txt", problem.RulePath, "unexpected file")
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./internal/rules/`
Expected: FAIL to compile: `undefined: CheckCatalogUpdate`, `undefined: CheckWorkspaceUpdate`.

- [ ] **Step 5: Write `internal/rules/rules.go`**

```go
// Package rules decides whether the main branch of a catalog or workspace
// repository may move to a new commit. The pre-receive hook applies them to
// pushes, the store to the commits it writes itself.
package rules

import (
	"slices"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/workspace"
)

const mainRef = "refs/heads/main"

// CheckCatalogUpdate validates moving the catalog's main from oldOID ("" or
// all zeros = no previous main) to newOID: history (no delete, no rewrite),
// catalog.Check and catalog.CheckImmutable. newOID all zeros = delete.
func CheckCatalogUpdate(cat *gitrepo.Repo, oldOID, newOID string) ([]problem.Problem, error) {
	if ps, err := checkHistory(cat, oldOID, newOID); err != nil || len(ps) > 0 {
		return ps, err
	}
	newFS, err := cat.TreeFS(newOID)
	if err != nil {
		return nil, err
	}
	ps := catalog.Check(newFS)
	if !none(oldOID) {
		oldFS, err := cat.TreeFS(oldOID)
		if err != nil {
			return nil, err
		}
		ps = append(ps, catalog.CheckImmutable(oldFS, newFS)...)
	}
	problem.Sort(ps)
	return ps, nil
}

// CheckWorkspaceUpdate validates moving a workspace's main: history,
// workspace.Check, the workspace id in custos.yaml equals id, the pin is a
// commit on the catalog's main, and workspace.CheckAgainstCatalog against the
// catalog tree at the pin. The id and pin are only checked when custos.yaml
// itself is valid, and the answers only when the pin is.
func CheckWorkspaceUpdate(ws, cat *gitrepo.Repo, id, oldOID, newOID string) ([]problem.Problem, error) {
	if ps, err := checkHistory(ws, oldOID, newOID); err != nil || len(ps) > 0 {
		return ps, err
	}
	fsys, err := ws.TreeFS(newOID)
	if err != nil {
		return nil, err
	}
	w, ps := workspace.Load(fsys)
	ps = append(ps, w.Graph.Check()...)
	configOK := !slices.ContainsFunc(ps, func(p problem.Problem) bool { return p.Path == workspace.ConfigPath })
	if configOK {
		if w.Config.Workspace != id {
			ps = append(ps, problem.Problem{Path: workspace.ConfigPath, Rule: problem.RuleWorkspaceID,
				Message: "workspace " + w.Config.Workspace + " does not match this repository, which belongs to workspace " + id})
		}
		pps, c, err := pinnedCatalog(cat, w.Config.Catalog.Commit)
		if err != nil {
			return nil, err
		}
		ps = append(ps, pps...)
		if c != nil {
			ps = append(ps, workspace.CheckAgainstCatalog(w, c)...)
		}
	}
	problem.Sort(ps)
	return ps, nil
}

// pinnedCatalog loads the catalog at pin, which must be a commit on the
// catalog's main (ruling 2.16 allows any of them, also older ones).
func pinnedCatalog(cat *gitrepo.Repo, pin string) ([]problem.Problem, *catalog.Catalog, error) {
	var ps problem.List
	main, ok, err := cat.ResolveRef(mainRef)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		ps.Add(workspace.ConfigPath, problem.RulePin, "the catalog has no main branch yet; push the catalog first")
		return ps, nil, nil
	}
	oid, ok, err := cat.ResolveRef(pin + "^{commit}")
	if err != nil {
		return nil, nil, err
	}
	if !ok || oid != pin {
		ps.Add(workspace.ConfigPath, problem.RulePin, "catalog commit %s does not exist in this server's catalog", pin)
		return ps, nil, nil
	}
	onMain, err := cat.IsAncestor(pin, main)
	if err != nil {
		return nil, nil, err
	}
	if !onMain {
		ps.Add(workspace.ConfigPath, problem.RulePin, "catalog commit %s is not on the catalog's main branch", pin)
		return ps, nil, nil
	}
	fsys, err := cat.TreeFS(pin)
	if err != nil {
		return nil, nil, err
	}
	c, _ := catalog.Load(fsys) // valid: every commit on main passed CheckCatalogUpdate
	return nil, c, nil
}

func checkHistory(repo *gitrepo.Repo, oldOID, newOID string) ([]problem.Problem, error) {
	var ps problem.List
	if none(newOID) {
		ps.Add("", problem.RuleHistory, "main cannot be deleted")
		return ps, nil
	}
	if !none(oldOID) {
		ok, err := repo.IsAncestor(oldOID, newOID)
		if err != nil {
			return nil, err
		}
		if !ok {
			ps.Add("", problem.RuleHistory, "main cannot be rewritten; add commits on top of it instead of force-pushing")
		}
	}
	return ps, nil
}

// none reports whether oid stands for a missing ref.
func none(oid string) bool { return oid == "" || gitrepo.IsZero(oid) }
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/rules/ ./internal/workspace/ ./internal/hook/ ./cmd/custos/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/rules internal/workspace/workspace.go internal/fixture/fixture.go
git commit -m "Add the rules for moving main of a catalog or workspace"
```

---

### Task 5: Task states and book order

**Files:**
- Create: `internal/status/status.go`
- Test: `internal/status/status_test.go`

**Interfaces:**
- Consumes: `catalog.Catalog` (`Tasks`, `Groups`, `Index`), `catalog.Group`, `workspace.Workspace` (`Config`, `Answers`, `Generated`, `Graph`), `workspace.Answer`, `task.Graph` (`Tasks`, `Current`, `Superseded`, `Lookup`, `Has`), `task.Ref`, `task.Version`.
- Produces (exactly as in the architecture note):
  - `type State string`; `Unanswered`, `Answered`, `PendingUpdate` (`"unanswered"`, `"answered"`, `"pending-update"`).
  - `type TaskStatus struct { ID string; Current task.Ref; Title string; AnswerType task.AnswerType; Generated bool; State State; Answer *workspace.Answer; Merged []*workspace.Answer; InEffect []*workspace.Answer }`
  - `type Entry struct { Depth int; Group *catalog.Group; Task *TaskStatus }`
  - `type Status struct { Workspace string; Pin string; Tasks []*TaskStatus; Book []Entry }`
  - `func Compute(w *workspace.Workspace, c *catalog.Catalog) *Status`

Semantics (spec §3.3, §5.5 and the architecture note):
- Tasks: every catalog task with a current version, then every generated task with a current version whose id is not a catalog task. Superseded tasks get no `TaskStatus`.
- `Merged`: answers of all superseded tasks reachable from the current version through `previous` (directly or through further merges), sorted by task id.
- `Answered` when the own answer names the current version; `InEffect` is that answer. Otherwise `PendingUpdate` when there is an own answer or a merged answer: `InEffect` is the own answer, followed by the merged answers whose task the own answer's version does not already reach through `previous` (an answer to A@2.0.0, which merged B, replaces B's answer also after A@3.0.0 appears). Otherwise `Unanswered`.
- Book: groups depth-first from `groups/index.yaml`, children in listed order. A group entry has the group's nesting depth (0 = top level); a task entry has its group's depth + 1. After each task follow the generated tasks below it (depth + 1), by id: below the task named by `origin.id`, or else by `produced_by.task.id`; a parent that was merged into another task is replaced by the task that merged it. Then a heading `Ungrouped` (`Group{Slug: "", Title: "Ungrouped"}`, depth 0) with the catalog tasks no group lists, by id, and after them generated tasks that are still unplaced (unknown parent, origin cycles); the heading is left out when nothing is under it. Every `TaskStatus` appears exactly once; `Status.Tasks` is in book order.

- [ ] **Step 1: Write the failing test** `internal/status/status_test.go`

```go
package status

import (
	"fmt"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/workspace"
)

const (
	a = fixture.TaskA
	b = fixture.TaskB
	c = fixture.TaskC
	d = "0f0e0d0c-0b0a-4908-8706-050403020100"
	e = "1e2d3c4b-5a69-4788-9766-554433221100"
)

func compute(t *testing.T, cat, ws map[string]string) *Status {
	t.Helper()
	cl, ps := catalog.Load(fixture.MapFS(cat))
	fixture.WantNone(t, append(ps, cl.Validate()...))
	w, ps := workspace.Load(fixture.MapFS(ws))
	fixture.WantNone(t, ps)
	return Compute(w, cl)
}

// book renders the book as "<depth>:<group slug or task id>" items, with
// the state after task ids.
func book(s *Status) string {
	var items []string
	for _, en := range s.Book {
		switch {
		case en.Group != nil:
			items = append(items, fmt.Sprintf("%d:group %s", en.Depth, en.Group.Title))
		case en.Task != nil:
			items = append(items, fmt.Sprintf("%d:%s %s", en.Depth, en.Task.ID[:4], en.Task.State))
		}
	}
	return strings.Join(items, ", ")
}

func answerFile(id, version string) string {
	return "---\ntask: " + id + "\ntask_version: " + version + "\ntype: text\nvalue: answer to " + id[:4] + "@" + version + "\n---\n"
}

func answerPath(id string) string { return "answers/" + id + ".md" }

// generated returns a generated task file for id below origin; an empty
// origin leaves only produced_by, which names TaskB.
func generated(id, origin string) (string, string) {
	f := strings.ReplaceAll(fixture.GeneratedFile, "id: "+c+"\n", "id: "+id+"\n")
	if origin == "" {
		f = strings.Replace(f, "origin:\n  id: "+b+"\n", "", 1)
	} else {
		f = strings.Replace(f, "origin:\n  id: "+b+"\n", "origin:\n  id: "+origin+"\n", 1)
	}
	return "generated/" + id + "/1.0.0.md", f
}

func values(as []*workspace.Answer) string {
	var vs []string
	for _, x := range as {
		vs = append(vs, x.Value)
	}
	return strings.Join(vs, " + ")
}

func TestComputeFixture(t *testing.T) {
	s := compute(t, fixture.Catalog(), fixture.Workspace())
	if s.Workspace != fixture.WorkspaceID || s.Pin != fixture.Commit {
		t.Errorf("workspace %q pin %q", s.Workspace, s.Pin)
	}
	want := "0:group Build, 1:3f1c pending-update, 1:group Release, 2:7d9e unanswered, 3:a1b2 unanswered"
	if got := book(s); got != want {
		t.Errorf("book\n got %s\nwant %s", got, want)
	}
	if len(s.Tasks) != 3 || s.Tasks[0].ID != a || s.Tasks[1].ID != b || s.Tasks[2].ID != c {
		t.Fatalf("tasks %v", s.Tasks)
	}
	ta, tc := s.Tasks[0], s.Tasks[2]
	if ta.Current.Version != "1.1.0" || ta.Title != "Task 1.1.0" || ta.Generated || ta.Answer == nil || values(ta.InEffect) != "done with make" {
		t.Errorf("TaskA %+v", ta)
	}
	if !tc.Generated || tc.Title != "Host web-01" || tc.AnswerType != "timestamp" || tc.Answer != nil || tc.InEffect != nil {
		t.Errorf("TaskC %+v", tc)
	}
}

func TestComputeAnswered(t *testing.T) {
	ws := fixture.Workspace()
	ws[answerPath(a)] = answerFile(a, "1.1.0")
	ta := compute(t, fixture.Catalog(), ws).Tasks[0]
	if ta.State != Answered || values(ta.InEffect) != "answer to 3f1c@1.1.0" {
		t.Errorf("TaskA %+v", ta)
	}
}

// mergedCatalog is the fixture catalog after TaskA 2.0.0 merged TaskA 1.1.0
// and TaskB 1.0.0.
func mergedCatalog() map[string]string {
	cat := fixture.Catalog()
	cat[fixture.TaskPath(a, "2.0.0")] = fixture.TaskFile(a, "2.0.0", a+"@1.1.0", b+"@1.0.0")
	cat["groups/release/group.yaml"] = "title: Release\nchildren: []\n"
	cat["processors.yaml"] = "processors: {}\nbindings: {}\n"
	return cat
}

func TestComputeMergedTasks(t *testing.T) {
	ws := fixture.Workspace()
	ws[answerPath(b)] = answerFile(b, "1.0.0")
	s := compute(t, mergedCatalog(), ws)
	// TaskB is gone; TaskC, generated from TaskB's answer, follows TaskA.
	if got, want := book(s), "0:group Build, 1:3f1c pending-update, 2:a1b2 unanswered, 1:group Release"; got != want {
		t.Errorf("book\n got %s\nwant %s", got, want)
	}
	ta := s.Tasks[0]
	if values(ta.InEffect) != "done with make + answer to 7d9e@1.0.0" || values(ta.Merged) != "answer to 7d9e@1.0.0" {
		t.Errorf("before answering the merge: in effect %q, merged %q", values(ta.InEffect), values(ta.Merged))
	}

	// Answering the merged version replaces both answers in the book.
	ws[answerPath(a)] = answerFile(a, "2.0.0")
	ta = compute(t, mergedCatalog(), ws).Tasks[0]
	if ta.State != Answered || values(ta.InEffect) != "answer to 3f1c@2.0.0" || values(ta.Merged) != "answer to 7d9e@1.0.0" {
		t.Errorf("after answering: %s, in effect %q, merged %q", ta.State, values(ta.InEffect), values(ta.Merged))
	}

	// A later version keeps the merged answer out of the book: the answer
	// to 2.0.0 already covers TaskB.
	cat := mergedCatalog()
	cat[fixture.TaskPath(a, "3.0.0")] = fixture.TaskFile(a, "3.0.0", a+"@2.0.0")
	ta = compute(t, cat, ws).Tasks[0]
	if ta.State != PendingUpdate || values(ta.InEffect) != "answer to 3f1c@2.0.0" {
		t.Errorf("after a new version: %s, in effect %q", ta.State, values(ta.InEffect))
	}

	// Only the merged task was answered.
	delete(ws, answerPath(a))
	ta = compute(t, mergedCatalog(), ws).Tasks[0]
	if ta.State != PendingUpdate || ta.Answer != nil || values(ta.InEffect) != "answer to 7d9e@1.0.0" {
		t.Errorf("only TaskB answered: %s, in effect %q", ta.State, values(ta.InEffect))
	}
}

func TestComputeUngrouped(t *testing.T) {
	cat := fixture.Catalog()
	cat[fixture.TaskPath(d, "1.0.0")] = fixture.TaskFile(d, "1.0.0")
	ws := fixture.Workspace()
	path, file := generated(e, "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d") // origin is no task
	ws[path] = file
	got := book(compute(t, cat, ws))
	want := "0:group Build, 1:3f1c pending-update, 1:group Release, 2:7d9e unanswered, 3:a1b2 unanswered, " +
		"0:group Ungrouped, 1:0f0e unanswered, 1:1e2d unanswered"
	if got != want {
		t.Errorf("book\n got %s\nwant %s", got, want)
	}
}

func TestComputeGeneratedWithoutOrigin(t *testing.T) {
	ws := fixture.Workspace()
	path, file := generated(c, "")
	ws[path] = file
	want := "0:group Build, 1:3f1c pending-update, 1:group Release, 2:7d9e unanswered, 3:a1b2 unanswered"
	if got := book(compute(t, fixture.Catalog(), ws)); got != want {
		t.Errorf("book\n got %s\nwant %s", got, want)
	}
}

// TestComputeOriginCycle must terminate and list each task once.
func TestComputeOriginCycle(t *testing.T) {
	ws := fixture.Workspace()
	p1, f1 := generated(d, e)
	p2, f2 := generated(e, d)
	ws[p1], ws[p2] = f1, f2
	s := compute(t, fixture.Catalog(), ws)
	if got, want := book(s), "0:group Build, 1:3f1c pending-update, 1:group Release, 2:7d9e unanswered, 3:a1b2 unanswered, 0:group Ungrouped, 1:0f0e unanswered, 2:1e2d unanswered"; got != want {
		t.Errorf("book\n got %s\nwant %s", got, want)
	}
	if len(s.Tasks) != 5 {
		t.Errorf("%d tasks", len(s.Tasks))
	}
}

// TestComputeBrokenTrees must not panic on trees that never passed
// validation, such as a repository edited on disk (spec section 7).
func TestComputeBrokenTrees(t *testing.T) {
	cat := fixture.Catalog()
	cat["groups/index.yaml"] = "groups: [build, build, missing]\n"
	cat["groups/release/group.yaml"] = "title: Release\nchildren:\n  - group: build\n  - task: " + d + "\n"
	cat[fixture.TaskPath(a, "1.2.0")] = fixture.TaskFile(a, "1.2.0", a+"@1.0.0") // two current versions
	cl, _ := catalog.Load(fixture.MapFS(cat))
	ws := fixture.Workspace()
	ws["custos.yaml"] = "- garbage\n"
	ws[answerPath(b)] = answerFile(c, "1.0.0") // stored under the wrong task
	w, _ := workspace.Load(fixture.MapFS(ws))
	s := Compute(w, cl)
	// Release lists build again (a cycle) and an unknown task instead of
	// TaskB; TaskA has two current versions and is left out.
	if got, want := book(s), "0:group Build, 1:group Release, 0:group Ungrouped, 1:7d9e unanswered, 2:a1b2 unanswered"; got != want {
		t.Errorf("book\n got %s\nwant %s", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/status/`
Expected: FAIL to compile: `undefined: Compute`, `undefined: Status`.

- [ ] **Step 3: Write `internal/status/status.go`**

```go
// Package status computes which tasks of a workspace are answered, which
// answers are in effect, and the order in which they form the workspace's
// book. It works on loaded trees and never touches git.
package status

import (
	"maps"
	"slices"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// State tells whether a task needs work.
type State string

const (
	Unanswered    State = "unanswered"
	Answered      State = "answered"       // answer exists for the current version
	PendingUpdate State = "pending-update" // only answers for older versions, or answers of merged tasks, are in effect
)

// TaskStatus is the state of one current task, from the catalog or
// generated by a processor.
type TaskStatus struct {
	ID         string
	Current    task.Ref // current version
	Title      string
	AnswerType task.AnswerType
	Generated  bool
	State      State
	Answer     *workspace.Answer   // this task's own answer file, any version; nil if none
	Merged     []*workspace.Answer // answers of tasks merged into this one (superseded tasks), for reference, always listed
	InEffect   []*workspace.Answer // what the book shows: own answer at the current version; otherwise own older answer plus the Merged answers it does not cover
}

// Entry is one line of the book, in order.
type Entry struct {
	Depth int            // 0 = top-level group
	Group *catalog.Group // heading entry when non-nil
	Task  *TaskStatus    // task entry when non-nil
}

// Status is the state of a workspace at one commit.
type Status struct {
	Workspace string
	Pin       string        // catalog commit
	Tasks     []*TaskStatus // book order
	Book      []Entry
}

// Compute derives the status of workspace w from the catalog c at its pin.
// The trees are expected to be valid; for invalid ones it does its best and
// never panics: tasks without a single current version are left out.
func Compute(w *workspace.Workspace, c *catalog.Catalog) *Status {
	b := &builder{
		w: w, c: c,
		byID:       map[string]*TaskStatus{},
		mergedInto: map[string]string{},
		children:   map[string][]string{},
		placed:     map[string]bool{},
		groupSeen:  map[string]bool{},
	}
	for _, id := range c.Tasks.Tasks() {
		if cur, ok := c.Tasks.Current(id); ok {
			b.add(c.Tasks, cur, false)
		}
	}
	var generated []string
	for _, id := range w.Graph.Tasks() {
		if cur, ok := w.Graph.Current(id); ok && !c.Tasks.Has(id) {
			b.add(w.Graph, cur, true)
			generated = append(generated, id)
		}
	}
	for _, id := range generated {
		parent := b.parent(id)
		if into, ok := b.mergedInto[parent]; ok {
			parent = into
		}
		b.children[parent] = append(b.children[parent], id)
	}
	b.layout(generated)
	return &Status{Workspace: w.Config.Workspace, Pin: w.Config.Catalog.Commit, Tasks: b.tasks, Book: b.book}
}

type builder struct {
	w          *workspace.Workspace
	c          *catalog.Catalog
	byID       map[string]*TaskStatus
	mergedInto map[string]string   // superseded task → current task that merged it
	children   map[string][]string // task → generated tasks shown below it
	placed     map[string]bool
	groupSeen  map[string]bool
	tasks      []*TaskStatus
	book       []Entry
}

// add records the status of the task whose current version is cur in graph g.
func (b *builder) add(g *task.Graph, cur *task.Version, generated bool) {
	ts := &TaskStatus{
		ID: cur.ID, Current: cur.Ref(), Title: cur.Title, AnswerType: cur.AnswerType,
		Generated: generated, Answer: b.answer(cur.ID),
	}
	for _, id := range slices.Sorted(maps.Keys(reach(g, cur))) {
		if id == cur.ID || !g.Superseded(id) {
			continue
		}
		b.mergedInto[id] = cur.ID
		if a := b.answer(id); a != nil {
			ts.Merged = append(ts.Merged, a)
		}
	}
	switch {
	case ts.Answer != nil && ts.Answer.TaskVersion == cur.Version:
		ts.State = Answered
		ts.InEffect = []*workspace.Answer{ts.Answer}
	case ts.Answer != nil || len(ts.Merged) > 0:
		ts.State = PendingUpdate
		covered := map[string]bool{}
		if ts.Answer != nil {
			ts.InEffect = append(ts.InEffect, ts.Answer)
			if v, ok := g.Lookup(task.Ref{ID: cur.ID, Version: ts.Answer.TaskVersion}); ok {
				covered = reach(g, v)
			}
		}
		for _, a := range ts.Merged {
			if !covered[a.Task] {
				ts.InEffect = append(ts.InEffect, a)
			}
		}
	default:
		ts.State = Unanswered
	}
	b.byID[cur.ID] = ts
}

// answer returns the answer file of task id, if any.
func (b *builder) answer(id string) *workspace.Answer {
	if a := b.w.Answers["answers/"+id+".md"]; a != nil && a.Task == id {
		return a
	}
	return nil
}

// parent returns the task a generated task is shown below: its origin, or
// else the task whose answer produced it.
func (b *builder) parent(id string) string {
	cur, _ := b.w.Graph.Current(id)
	g := b.w.Generated[cur.Path]
	if g == nil {
		return ""
	}
	if g.Origin != nil && g.Origin.ID != "" {
		return g.Origin.ID
	}
	return g.ProducedBy.Task.ID
}

// layout builds the book: the groups depth-first from groups/index.yaml,
// then the tasks no group lists under Ungrouped, catalog tasks first.
func (b *builder) layout(generated []string) {
	for _, slug := range b.c.Index {
		b.group(slug, 0)
	}
	heading := len(b.book)
	b.book = append(b.book, Entry{Depth: 0, Group: &catalog.Group{Slug: "", Title: "Ungrouped"}})
	for _, id := range b.c.Tasks.Tasks() {
		if b.byID[id] != nil && !b.placed[id] {
			b.task(id, 1)
		}
	}
	for _, id := range generated { // parent unknown, or a cycle of origins
		if !b.placed[id] {
			b.task(id, 1)
		}
	}
	if len(b.book) == heading+1 {
		b.book = b.book[:heading]
	}
}

func (b *builder) group(slug string, depth int) {
	g := b.c.Groups[slug]
	if g == nil || b.groupSeen[slug] {
		return
	}
	b.groupSeen[slug] = true
	b.book = append(b.book, Entry{Depth: depth, Group: g})
	for _, ch := range g.Children {
		switch {
		case ch.Group != "":
			b.group(ch.Group, depth+1)
		case b.byID[ch.Task] != nil && !b.placed[ch.Task]:
			b.task(ch.Task, depth+1)
		}
	}
}

func (b *builder) task(id string, depth int) {
	ts := b.byID[id]
	b.placed[id] = true
	b.book = append(b.book, Entry{Depth: depth, Task: ts})
	b.tasks = append(b.tasks, ts)
	for _, child := range b.children[id] {
		if !b.placed[child] {
			b.task(child, depth+1)
		}
	}
}

// reach returns the ids of the tasks whose versions v lists as previous,
// directly or indirectly, and v's own task.
func reach(g *task.Graph, v *task.Version) map[string]bool {
	ids := map[string]bool{}
	seen := map[task.Ref]bool{}
	stack := []*task.Version{v}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur.Ref()] {
			continue
		}
		seen[cur.Ref()] = true
		ids[cur.ID] = true
		for _, p := range cur.Previous {
			if pv, ok := g.Lookup(p); ok {
				stack = append(stack, pv)
			}
		}
	}
	return ids
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/status/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/status
git commit -m "Compute task states, answers in effect and book order of a workspace"
```

---

### Task 6: The book as Markdown

**Files:**
- Create: `internal/status/markdown.go`
- Test: `internal/status/markdown_test.go`

**Interfaces:**
- Consumes (Task 5): `Status`, `Entry`, `TaskStatus`, the states; test helpers `compute`, `answerPath`, `answerFile`, `mergedCatalog`, constants `a`, `b` from `status_test.go`.
- Produces: `func RenderMarkdown(s *Status) []byte` (ruling 2.11: Markdown now, HTML with the UI).

Format: `# Workspace <id>`, a line ``Catalog commit `<pin>`.``, then per book entry a heading of level `Depth+2` (at most 6) with the group or task title. Below a task: `*Unanswered.*`, or for a pending update a line naming the answered and the current version, then each answer in effect (Markdown body, or the value as a paragraph, then a list of attachments). Answers of merged tasks are introduced by `*Answer to merged task <id>, version <v>:*`. Blocks are separated by one blank line; the document ends with one newline.

- [ ] **Step 1: Write the failing test** `internal/status/markdown_test.go`

```go
package status

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
)

func TestRenderMarkdown(t *testing.T) {
	ws := fixture.Workspace()
	ws[answerPath(b)] = "---\ntask: " + b + "\ntask_version: 1.0.0\ntype: text\nvalue: shipped\n" +
		"attachments:\n  - name: log.txt\n    sha256: " + fixture.SHA256 + "\n    media_type: text/plain\n---\n"
	got := string(RenderMarkdown(compute(t, fixture.Catalog(), ws)))
	want := "# Workspace " + fixture.WorkspaceID + "\n\n" +
		"Catalog commit `" + fixture.Commit + "`.\n\n" +
		"## Build\n\n" +
		"### Task 1.1.0\n\n" +
		"*Pending update: answered for version 1.0.0; the current version is 1.1.0.*\n\n" +
		"done with make\n\n" +
		"### Release\n\n" +
		"#### Task 1.0.0\n\n" +
		"shipped\n\n" +
		"- Attachment log.txt (text/plain, sha256 " + fixture.SHA256 + ")\n\n" +
		"##### Host web-01\n\n" +
		"*Unanswered.*\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderMarkdownMerged(t *testing.T) {
	ws := fixture.Workspace()
	delete(ws, answerPath(a))
	ws[answerPath(b)] = answerFile(b, "1.0.0")
	got := string(RenderMarkdown(compute(t, mergedCatalog(), ws)))
	want := "### Task 2.0.0\n\n" +
		"*Pending update: version 2.0.0 is not answered yet; the answers of the tasks it merged stay in effect.*\n\n" +
		"*Answer to merged task " + b + ", version 1.0.0:*\n\n" +
		"answer to 7d9e@1.0.0\n\n"
	if !strings.Contains(got, want) {
		t.Errorf("got:\n%s\nwant it to contain:\n%s", got, want)
	}
}

func TestRenderMarkdownBody(t *testing.T) {
	cat := fixture.Catalog()
	path := fixture.TaskPath(a, "1.1.0")
	cat[path] = strings.Replace(cat[path], "answer_type: text", "answer_type: markdown", 1)
	ws := fixture.Workspace()
	ws[answerPath(a)] = "---\ntask: " + a + "\ntask_version: 1.1.0\ntype: markdown\n---\n\nBuilt with `make release`.\n\nSee the pipeline.\n"
	got := string(RenderMarkdown(compute(t, cat, ws)))
	if !strings.Contains(got, "### Task 1.1.0\n\nBuilt with `make release`.\n\nSee the pipeline.\n\n### Release") {
		t.Errorf("got:\n%s", got)
	}
}

func TestRenderMarkdownDeepNesting(t *testing.T) {
	cat := fixture.Catalog()
	cat["groups/index.yaml"] = "groups: [g1]\n"
	cat["groups/g1/group.yaml"] = "title: G1\nchildren:\n  - group: g2\n"
	cat["groups/g2/group.yaml"] = "title: G2\nchildren:\n  - group: g3\n"
	cat["groups/g3/group.yaml"] = "title: G3\nchildren:\n  - group: g4\n"
	cat["groups/g4/group.yaml"] = "title: G4\nchildren:\n  - group: g5\n"
	cat["groups/g5/group.yaml"] = "title: G5\nchildren:\n  - group: build\n"
	got := string(RenderMarkdown(compute(t, cat, fixture.Workspace())))
	if !strings.Contains(got, "\n###### G5\n") || !strings.Contains(got, "\n###### Build\n") || strings.Contains(got, "#######") {
		t.Errorf("got:\n%s", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/status/`
Expected: FAIL to compile: `undefined: RenderMarkdown`.

- [ ] **Step 3: Write `internal/status/markdown.go`**

```go
package status

import (
	"fmt"
	"strings"

	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// RenderMarkdown writes the book of s as one Markdown document: a heading
// per group and task, nested by depth, and the answers in effect below each
// task.
func RenderMarkdown(s *Status) []byte {
	var md markdown
	md.block("# Workspace " + s.Workspace)
	md.block("Catalog commit `" + s.Pin + "`.")
	for _, en := range s.Book {
		switch {
		case en.Group != nil:
			md.heading(en.Depth, en.Group.Title)
		case en.Task != nil:
			md.task(en.Depth, en.Task)
		}
	}
	return []byte(strings.TrimRight(md.String(), "\n") + "\n")
}

type markdown struct{ strings.Builder }

// block writes one paragraph, list or heading, followed by a blank line.
func (md *markdown) block(s string) {
	md.WriteString(strings.TrimRight(s, "\n"))
	md.WriteString("\n\n")
}

// heading writes a heading for depth: groups and tasks at depth 0 get "##",
// the document title being "#"; Markdown has no level beyond 6.
func (md *markdown) heading(depth int, title string) {
	md.block(strings.Repeat("#", min(depth+2, 6)) + " " + title)
}

func (md *markdown) task(depth int, ts *TaskStatus) {
	md.heading(depth, ts.Title)
	switch ts.State {
	case Unanswered:
		md.block("*Unanswered.*")
		return
	case PendingUpdate:
		if ts.Answer != nil {
			md.block(fmt.Sprintf("*Pending update: answered for version %s; the current version is %s.*", ts.Answer.TaskVersion, ts.Current.Version))
		} else {
			md.block(fmt.Sprintf("*Pending update: version %s is not answered yet; the answers of the tasks it merged stay in effect.*", ts.Current.Version))
		}
	}
	for _, a := range ts.InEffect {
		if a != ts.Answer {
			md.block(fmt.Sprintf("*Answer to merged task %s, version %s:*", a.Task, a.TaskVersion))
		}
		md.answer(a)
	}
}

func (md *markdown) answer(a *workspace.Answer) {
	if a.Type == task.AnswerMarkdown {
		if strings.TrimSpace(a.Body) != "" {
			md.block(a.Body)
		}
	} else {
		md.block(a.Value)
	}
	if len(a.Attachments) == 0 {
		return
	}
	var list strings.Builder
	for _, at := range a.Attachments {
		fmt.Fprintf(&list, "- Attachment %s (%s, sha256 %s)\n", at.Name, at.MediaType, at.SHA256)
	}
	md.block(list.String())
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/status/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/status/markdown.go internal/status/markdown_test.go
git commit -m "Render the workspace book as Markdown"
```

---

### Task 7: The store: layout, hooks, locks and workspace creation

**Files:**
- Create: `internal/workspace/config.go`, `internal/workspace/config_test.go`
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `gitrepo.InitBare`, `Repo.InstallHook`, `Repo.ResolveRef`, `Repo.WriteCommit`, `Repo.UpdateRef`, `gitrepo.ErrRefMoved`, `gitrepo.Signature` (Tasks 1–2); `rules.CheckWorkspaceUpdate` (Task 4); `hook.Script(exe string, kind hook.Kind)` and `hook.Catalog`/`hook.Workspace` as they exist in phase 1 (Task 10 changes this call); `task.ValidID`; `workspace.Config`, `workspace.CatalogPin`, `workspace.ConfigPath`.
- Produces:
  - `func MarshalConfig(c Config) ([]byte, error)` in package `workspace` (2-space YAML, `frozen` omitted when false; 2c uses it for freeze).
  - Package `store`: `ErrNotFound`, `ErrExists`, `ErrConflict`; `type RejectedError struct{ Problems []problem.Problem }` with `Error() string` = `"rejected: " + problems joined by "; "`; `type Store`; `func New(dataDir, exe, publicURL string) *Store` (no I/O; makes `dataDir` absolute, trims a trailing `/` from `publicURL`); `func Open(dataDir, exe, publicURL string) (*Store, error)`; `func (s *Store) InstallHooks() error`; `DataDir() string`; `CatalogRepo() *gitrepo.Repo`; `WorkspaceRepo(id string) (*gitrepo.Repo, error)`; `WorkspaceIDs() ([]string, error)`; `CatalogURL() string`; `CreateWorkspace(id string, author gitrepo.Signature) error`; `CreateWorkspaceRepo(id string) (*gitrepo.Repo, error)`; `Lock(repo string) func()`.
  - Unexported, used by Task 8: `mainRef`, `validateMain(repo *gitrepo.Repo, id, oldOID, newOID string) error`, `conflict(err error) error`, `installHook`, `workspaceDir`, `workspacesDir`.
  - Error classes: invalid workspace id → `*RejectedError` with rule `workspace-id`; existing workspace → wraps `ErrExists`; catalog without `main` → wraps `ErrConflict` with "the catalog has no main branch yet; push the catalog first"; unknown workspace → wraps `ErrNotFound`.

The store duplicates `server.Open`/`server.CreateWorkspace` for one task; Task 9 removes them from `server`.

- [ ] **Step 1: Write the failing tests**

`internal/workspace/config_test.go`:

```go
package workspace

import (
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
)

func TestMarshalConfig(t *testing.T) {
	c := Config{Workspace: fixture.WorkspaceID, Catalog: CatalogPin{URL: "https://custos.example.org/git/catalog.git", Commit: fixture.Commit}}
	data, err := MarshalConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	if want := fixture.Config(fixture.WorkspaceID, fixture.Commit); string(data) != want {
		t.Errorf("got:\n%s\nwant:\n%s", data, want)
	}
	c.Frozen = true
	data, err = MarshalConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	f := fixture.Workspace()
	f[ConfigPath] = string(data)
	w, ps := Load(fixture.MapFS(f))
	fixture.WantNone(t, ps)
	if w.Config != c {
		t.Errorf("round trip: %+v", w.Config)
	}
}
```

`internal/store/store_test.go`:

```go
package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/problem"
)

const publicURL = "https://custos.example.org"

var jane = gitrepo.Signature{Name: "Jane Doe", Email: "jane@example.org"}

// seedCatalog commits files to the catalog's main without running hooks and
// returns the commit.
func seedCatalog(t *testing.T, s *Store, files map[string]string) string {
	t.Helper()
	cat := s.CatalogRepo()
	old, _, err := cat.ResolveRef(mainRef)
	if err != nil {
		t.Fatal(err)
	}
	var changes []gitrepo.Change
	for p, c := range files {
		changes = append(changes, gitrepo.Change{Path: p, Data: []byte(c)})
	}
	req := gitrepo.CommitRequest{Base: old, Changes: changes, Author: jane, Message: "catalog"}
	if old != "" {
		req.Parents = []string{old}
	}
	commit, err := cat.WriteCommit(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.UpdateRef(mainRef, commit, old); err != nil {
		t.Fatal(err)
	}
	return commit
}

// open returns a store with the fixture catalog on main.
func open(t *testing.T) (*Store, string) {
	t.Helper()
	s, err := Open(t.TempDir(), "/custos", publicURL)
	if err != nil {
		t.Fatal(err)
	}
	return s, seedCatalog(t, s, fixture.Catalog())
}

func hookOf(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "hooks", "pre-receive"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestOpen(t *testing.T) {
	data := t.TempDir()
	s, err := Open(data, "/custos", publicURL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if s.DataDir() != data {
		t.Errorf("DataDir = %s", s.DataDir())
	}
	if s.CatalogRepo().Dir != filepath.Join(data, "repos", "catalog.git") {
		t.Errorf("catalog at %s", s.CatalogRepo().Dir)
	}
	if got := gittest.Run(t, s.CatalogRepo().Dir, "config", "http.receivepack"); got != "true" {
		t.Errorf("http.receivepack = %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.CatalogRepo().Dir, "hooks", "pre-receive")); err == nil {
		t.Error("Open must not install hooks")
	}
	if s.CatalogURL() != publicURL+"/git/catalog.git" {
		t.Errorf("CatalogURL = %s", s.CatalogURL())
	}
	if ids, err := s.WorkspaceIDs(); err != nil || len(ids) != 0 {
		t.Errorf("ids %v %v", ids, err)
	}
}

func TestNewMakesDataDirAbsolute(t *testing.T) {
	t.Chdir(t.TempDir())
	s := New("data", "/custos", publicURL)
	if !filepath.IsAbs(s.DataDir()) {
		t.Errorf("DataDir = %s", s.DataDir())
	}
}

func TestCreateWorkspace(t *testing.T) {
	s, pin := open(t)
	if err := s.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	repo, err := s.WorkspaceRepo(fixture.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	data, ok, err := repo.ReadFile(mainRef, "custos.yaml")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	want := "workspace: " + fixture.WorkspaceID + "\ncatalog:\n  url: " + publicURL + "/git/catalog.git\n  commit: " + pin + "\n"
	if string(data) != want {
		t.Errorf("custos.yaml:\n%s\nwant:\n%s", data, want)
	}
	if who := gittest.Run(t, repo.Dir, "log", "-1", "--format=%an <%ae>|%cn <%ce>|%s", "main"); who != "Jane Doe <jane@example.org>|custos-bot <custos-bot@localhost>|Create workspace "+fixture.WorkspaceID {
		t.Errorf("commit %q", who)
	}
	if !strings.Contains(hookOf(t, repo.Dir), "'/custos' hook pre-receive --kind workspace") {
		t.Errorf("hook %q", hookOf(t, repo.Dir))
	}
	if ids, err := s.WorkspaceIDs(); err != nil || !slices.Equal(ids, []string{fixture.WorkspaceID}) {
		t.Errorf("ids %v %v", ids, err)
	}
	if err := s.CreateWorkspace(fixture.WorkspaceID, jane); !errors.Is(err, ErrExists) {
		t.Errorf("second create: %v", err)
	}
}

func TestCreateWorkspaceInvalidID(t *testing.T) {
	s, _ := open(t)
	for _, id := range []string{"../escape", "ws-1", strings.ToUpper(fixture.WorkspaceID)} {
		err := s.CreateWorkspace(id, jane)
		var rej *RejectedError
		if !errors.As(err, &rej) || rej.Problems[0].Rule != problem.RuleWorkspaceID {
			t.Errorf("%q: %v", id, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(s.DataDir(), "repos", "workspaces"))
	if len(entries) != 0 {
		t.Errorf("left %v behind", entries)
	}
}

func TestCreateWorkspaceNeedsCatalog(t *testing.T) {
	s, err := Open(t.TempDir(), "/custos", publicURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkspace(fixture.WorkspaceID, jane); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "push the catalog first") {
		t.Errorf("empty catalog: %v", err)
	}
	if ids, _ := s.WorkspaceIDs(); len(ids) != 0 {
		t.Errorf("ids %v", ids)
	}
	// A data directory that was never opened has no catalog at all.
	if err := New(t.TempDir(), "/custos", publicURL).CreateWorkspace(fixture.WorkspaceID, jane); !errors.Is(err, ErrConflict) {
		t.Errorf("no catalog: %v", err)
	}
}

func TestCreateWorkspaceRepo(t *testing.T) {
	s := New(t.TempDir(), "/custos", publicURL)
	repo, err := s.CreateWorkspaceRepo(fixture.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if refs, err := repo.Refs(""); err != nil || len(refs) != 0 {
		t.Errorf("refs %v %v", refs, err)
	}
	if !strings.Contains(hookOf(t, repo.Dir), "--kind workspace") {
		t.Error("hook missing")
	}
	if _, err := s.CreateWorkspaceRepo(fixture.WorkspaceID); !errors.Is(err, ErrExists) {
		t.Errorf("second create: %v", err)
	}
}

func TestWorkspaceRepoNotFound(t *testing.T) {
	s, _ := open(t)
	for _, id := range []string{fixture.WorkspaceID, "../catalog", ""} {
		if _, err := s.WorkspaceRepo(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v", id, err)
		}
	}
}

func TestWorkspaceIDsIgnoresOtherDirectories(t *testing.T) {
	s, _ := open(t)
	if err := s.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(s.DataDir(), "repos", "workspaces")
	os.Mkdir(filepath.Join(ws, "notes"), 0o755)
	os.Mkdir(filepath.Join(ws, "scratch.git"), 0o755)
	os.WriteFile(filepath.Join(ws, "0f0e0d0c-0b0a-4908-8706-050403020100.git"), nil, 0o644)
	if ids, err := s.WorkspaceIDs(); err != nil || !slices.Equal(ids, []string{fixture.WorkspaceID}) {
		t.Errorf("ids %v %v", ids, err)
	}
}

func TestInstallHooks(t *testing.T) {
	data := t.TempDir()
	first, err := Open(data, "/first/custos", publicURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.InstallHooks(); err != nil {
		t.Fatal(err)
	}
	seedCatalog(t, first, fixture.Catalog())
	// workspace create runs from another binary path and leaves the other
	// hooks alone (ruling 1.8).
	if err := New(data, "/second/custos", publicURL).CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	catDir := first.CatalogRepo().Dir
	wsDir := filepath.Join(data, "repos", "workspaces", fixture.WorkspaceID+".git")
	if !strings.Contains(hookOf(t, catDir), "'/first/custos'") || !strings.Contains(hookOf(t, wsDir), "'/second/custos'") {
		t.Errorf("hooks %q %q", hookOf(t, catDir), hookOf(t, wsDir))
	}
	third, err := Open(data, "/third/custos", publicURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.InstallHooks(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hookOf(t, catDir), "'/third/custos' hook pre-receive --kind catalog") ||
		!strings.Contains(hookOf(t, wsDir), "'/third/custos' hook pre-receive --kind workspace") {
		t.Errorf("hooks %q %q", hookOf(t, catDir), hookOf(t, wsDir))
	}
}

func TestLock(t *testing.T) {
	s := New(t.TempDir(), "/custos", publicURL)
	unlock := s.Lock("a")
	got := make(chan bool)
	go func() {
		u := s.Lock("a")
		got <- true
		u()
	}()
	s.Lock("b")() // another repository is not blocked
	select {
	case <-got:
		t.Fatal("second Lock of a returned while a was locked")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("Lock of a did not return after unlock")
	}
}

func TestRejectedError(t *testing.T) {
	err := &RejectedError{Problems: []problem.Problem{
		{Path: "a.md", Rule: problem.RuleAnswer, Message: "one"},
		{Rule: problem.RuleHistory, Message: "two"},
	}}
	if err.Error() != "rejected: a.md: answer: one; history: two" {
		t.Errorf("%q", err.Error())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/workspace/ ./internal/store/`
Expected: FAIL to compile: `undefined: MarshalConfig`; `no non-test Go files in .../internal/store`.

- [ ] **Step 3: Write `internal/workspace/config.go`**

```go
package workspace

import (
	"bytes"

	"go.yaml.in/yaml/v3"
)

// MarshalConfig encodes c as the content of custos.yaml.
func MarshalConfig(c Config) ([]byte, error) {
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

- [ ] **Step 4: Write `internal/store/store.go`**

`CreateWorkspaceRepo` reserves the directory with `os.Mkdir`, which fails atomically when it exists. `CreateWorkspace` removes the new repository again on any later failure, validates the initial commit like any update of `main`, and creates `main` with the expected old value "does not exist".

```go
// Package store owns the repositories below <data-dir>/repos, keeps their
// pre-receive hooks installed, and writes commits to them (ruling 2.17):
//
//	catalog.git
//	workspaces/<workspace-uuid>.git
//
// Writes to one repository are serialised by a lock in this process; pushes
// do not take it, so every ref update is a compare-and-swap (ruling 2.3).
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/hook"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/rules"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	ErrConflict = errors.New("conflict") // wraps gitrepo.ErrRefMoved and similar
)

// RejectedError reports validation problems of a write.
type RejectedError struct{ Problems []problem.Problem }

func (e *RejectedError) Error() string {
	msgs := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		msgs[i] = p.String()
	}
	return "rejected: " + strings.Join(msgs, "; ")
}

const mainRef = "refs/heads/main"

// Store is the data directory of one server.
type Store struct {
	dataDir   string
	reposDir  string
	exe       string // the custos binary the hooks run
	publicURL string // without trailing slash

	mu    sync.Mutex
	locks map[string]*sync.Mutex // by "catalog" or workspace id
}

// New returns a store for the data directory without touching it. exe is the
// custos binary that hooks of repositories it creates run; publicURL is the
// base URL clients reach the server at, such as http://127.0.0.1:8080.
func New(dataDir, exe, publicURL string) *Store {
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	return &Store{
		dataDir:   dataDir,
		reposDir:  filepath.Join(dataDir, "repos"),
		exe:       exe,
		publicURL: strings.TrimRight(publicURL, "/"),
		locks:     map[string]*sync.Mutex{},
	}
}

// Open prepares the data directory: it creates the directories and the
// catalog repository if needed. It does not touch hooks; see InstallHooks.
func Open(dataDir, exe, publicURL string) (*Store, error) {
	s := New(dataDir, exe, publicURL)
	if err := os.MkdirAll(s.workspacesDir(), 0o755); err != nil {
		return nil, err
	}
	dir := s.CatalogRepo().Dir
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		if _, err := gitrepo.InitBare(dir); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return s, nil
}

// InstallHooks rewrites the pre-receive hooks of the catalog and of all
// workspaces to run exe, so moving the binary between starts is safe.
func (s *Store) InstallHooks() error {
	if err := s.installHook(s.CatalogRepo(), ""); err != nil {
		return err
	}
	ids, err := s.WorkspaceIDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.installHook(&gitrepo.Repo{Dir: s.workspaceDir(id)}, id); err != nil {
			return err
		}
	}
	return nil
}

// installHook installs the hook of the catalog (id "") or of workspace id.
func (s *Store) installHook(repo *gitrepo.Repo, id string) error {
	kind := hook.Catalog
	if id != "" {
		kind = hook.Workspace
	}
	script, err := hook.Script(s.exe, kind)
	if err != nil {
		return err
	}
	return repo.InstallHook("pre-receive", script)
}

// DataDir returns the absolute data directory.
func (s *Store) DataDir() string { return s.dataDir }

// CatalogRepo returns the catalog repository.
func (s *Store) CatalogRepo() *gitrepo.Repo {
	return &gitrepo.Repo{Dir: filepath.Join(s.reposDir, "catalog.git")}
}

// CatalogURL returns the URL clients clone the catalog from.
func (s *Store) CatalogURL() string { return s.publicURL + "/git/catalog.git" }

// WorkspaceRepo returns the repository of workspace id, or an error wrapping
// ErrNotFound.
func (s *Store) WorkspaceRepo(id string) (*gitrepo.Repo, error) {
	if !task.ValidID(id) {
		return nil, fmt.Errorf("%w: workspace %q", ErrNotFound, id)
	}
	dir := s.workspaceDir(id)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: workspace %s", ErrNotFound, id)
	} else if err != nil {
		return nil, err
	}
	return &gitrepo.Repo{Dir: dir}, nil
}

// WorkspaceIDs returns the ids of all workspaces, sorted. Directories whose
// name is not "<uuid>.git" are ignored.
func (s *Store) WorkspaceIDs() ([]string, error) {
	entries, err := os.ReadDir(s.workspacesDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".git"); ok && e.IsDir() && task.ValidID(id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// CreateWorkspace creates the bare repository with its hook and an initial
// commit on main whose custos.yaml pins the current catalog main (ruling 2.13).
// ErrExists if it exists; error if the catalog has no main. Only the new
// repository's hook is installed (ruling 1.8).
func (s *Store) CreateWorkspace(id string, author gitrepo.Signature) (err error) {
	unlock := s.Lock(id)
	defer unlock()
	pin, err := s.catalogMain()
	if err != nil {
		return err
	}
	repo, err := s.CreateWorkspaceRepo(id)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(repo.Dir)
		}
	}()
	config, err := workspace.MarshalConfig(workspace.Config{
		Workspace: id,
		Catalog:   workspace.CatalogPin{URL: s.CatalogURL(), Commit: pin},
	})
	if err != nil {
		return err
	}
	commit, err := repo.WriteCommit(gitrepo.CommitRequest{
		Changes: []gitrepo.Change{{Path: workspace.ConfigPath, Data: config}},
		Author:  author,
		Message: "Create workspace " + id,
	})
	if err != nil {
		return err
	}
	if err := s.validateMain(repo, id, "", commit); err != nil {
		return err
	}
	return conflict(repo.UpdateRef(mainRef, commit, ""))
}

// CreateWorkspaceRepo creates an empty bare repository with its hook (used by
// fork in 2d). It does not take the workspace's lock.
func (s *Store) CreateWorkspaceRepo(id string) (*gitrepo.Repo, error) {
	if !task.ValidID(id) {
		return nil, &RejectedError{Problems: []problem.Problem{{
			Rule: problem.RuleWorkspaceID, Message: fmt.Sprintf("workspace id %q is not a lowercase UUID v4", id),
		}}}
	}
	if err := os.MkdirAll(s.workspacesDir(), 0o755); err != nil {
		return nil, err
	}
	dir := s.workspaceDir(id)
	if err := os.Mkdir(dir, 0o755); errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("%w: workspace %s", ErrExists, id)
	} else if err != nil {
		return nil, err
	}
	repo, err := gitrepo.InitBare(dir)
	if err == nil {
		err = s.installHook(repo, id)
	}
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return repo, nil
}

// Lock serialises writes to one repository ("catalog" or a workspace id). It
// returns the unlock function.
func (s *Store) Lock(repo string) func() {
	s.mu.Lock()
	m := s.locks[repo]
	if m == nil {
		m = &sync.Mutex{}
		s.locks[repo] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// catalogMain returns the commit of the catalog's main.
func (s *Store) catalogMain() (string, error) {
	errEmpty := fmt.Errorf("%w: the catalog has no main branch yet; push the catalog first", ErrConflict)
	cat := s.CatalogRepo()
	if _, err := os.Stat(cat.Dir); errors.Is(err, fs.ErrNotExist) {
		return "", errEmpty
	}
	oid, ok, err := cat.ResolveRef(mainRef)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errEmpty
	}
	return oid, nil
}

// validateMain applies the push rules to moving workspace id's main.
func (s *Store) validateMain(repo *gitrepo.Repo, id, oldOID, newOID string) error {
	ps, err := rules.CheckWorkspaceUpdate(repo, s.CatalogRepo(), id, oldOID, newOID)
	if err != nil {
		return err
	}
	if len(ps) > 0 {
		return &RejectedError{Problems: ps}
	}
	return nil
}

// conflict marks a lost compare-and-swap as ErrConflict.
func conflict(err error) error {
	if errors.Is(err, gitrepo.ErrRefMoved) {
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	return err
}

func (s *Store) workspacesDir() string { return filepath.Join(s.reposDir, "workspaces") }

func (s *Store) workspaceDir(id string) string {
	return filepath.Join(s.workspacesDir(), id+".git")
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/store/ ./internal/workspace/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store internal/workspace/config.go internal/workspace/config_test.go
git commit -m "Add the store with repository layout, hooks, locks and workspace creation"
```

---

### Task 8: Store writes, loading and status

**Files:**
- Create: `internal/store/write.go`
- Test: `internal/store/write_test.go`

**Interfaces:**
- Consumes (Task 7): `Store`, `WorkspaceRepo`, `CatalogRepo`, `Lock`, `validateMain`, `conflict`, `mainRef`, `ErrNotFound`, `ErrConflict`, `RejectedError`; test helpers `open`, `jane` from `store_test.go`. (Tasks 1–5): `Repo.ResolveRef/TreeFS/WriteCommit/UpdateRef`, `rules.CheckWorkspaceUpdate`, `status.Compute`, `workspace.Load`, `catalog.Load`.
- Produces:
  - `func (s *Store) UpdateWorkspace(id, ref string, author gitrepo.Signature, message string, edit func(tree fs.FS) ([]gitrepo.Change, error)) (string, error)` — `ref` must start with `refs/heads/`; a missing branch starts at `main`; changes that leave a file as it is are dropped; no changes → no commit, returns the ref's current oid (`""` for a branch that does not exist, which is then not created).
  - `func (s *Store) SetRef(id, ref, newOID, oldOID string) error`
  - `func (s *Store) Load(id, rev string) (*workspace.Workspace, *catalog.Catalog, error)` — `rev ""` = main; unknown rev wraps `ErrNotFound`; problems in the trees are ignored.
  - `func (s *Store) Status(id string) (*status.Status, error)`
  - `func (s *Store) CheckWorkspace(id string) ([]problem.Problem, error)` — the push rules applied to `main` as it is (empty repository: none); `serve` uses it at start (Task 9, spec §7).

- [ ] **Step 1: Write the failing test** `internal/store/write_test.go`

```go
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/status"
)

var answerA = "answers/" + fixture.TaskA + ".md"

func answerFile(version string) string {
	return "---\ntask: " + fixture.TaskA + "\ntask_version: " + version + "\ntype: text\nvalue: built with make\n---\n"
}

// put returns an edit that writes files.
func put(files map[string]string) func(fs.FS) ([]gitrepo.Change, error) {
	return func(fs.FS) ([]gitrepo.Change, error) {
		var cs []gitrepo.Change
		for p, c := range files {
			cs = append(cs, gitrepo.Change{Path: p, Data: []byte(c)})
		}
		return cs, nil
	}
}

// created returns a store with the fixture catalog and the workspace
// fixture.WorkspaceID, and the workspace's main.
func created(t *testing.T) (*Store, string) {
	t.Helper()
	s, _ := open(t)
	if err := s.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	return s, mainOf(t, s, mainRef)
}

func mainOf(t *testing.T, s *Store, ref string) string {
	t.Helper()
	repo, err := s.WorkspaceRepo(fixture.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	oid, _, err := repo.ResolveRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func TestUpdateWorkspaceMain(t *testing.T) {
	s, first := created(t)
	commit, err := s.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "Answer TaskA", put(map[string]string{answerA: answerFile("1.1.0")}))
	if err != nil {
		t.Fatal(err)
	}
	if commit == first || mainOf(t, s, mainRef) != commit {
		t.Fatalf("main %s, commit %s, first %s", mainOf(t, s, mainRef), commit, first)
	}
	repo, _ := s.WorkspaceRepo(fixture.WorkspaceID)
	if data, ok, _ := repo.ReadFile(commit, answerA); !ok || string(data) != answerFile("1.1.0") {
		t.Errorf("answer %q", data)
	}
	// The same content again makes no commit.
	again, err := s.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "again", put(map[string]string{answerA: answerFile("1.1.0")}))
	if err != nil || again != commit {
		t.Errorf("no-op update: %s %v, want %s", again, err, commit)
	}
	// The edit sees the current tree.
	_, err = s.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "check", func(tree fs.FS) ([]gitrepo.Change, error) {
		if _, err := fs.Stat(tree, answerA); err != nil {
			t.Errorf("edit does not see the answer: %v", err)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpdateWorkspaceRejected(t *testing.T) {
	s, first := created(t)
	_, err := s.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "bad", put(map[string]string{answerA: answerFile("9.0.0")}))
	var rej *RejectedError
	if !errors.As(err, &rej) {
		t.Fatalf("err = %v", err)
	}
	fixture.WantProblem(t, rej.Problems, answerA, problem.RuleAnswer, "has no version 9.0.0")
	if mainOf(t, s, mainRef) != first {
		t.Error("main moved")
	}
}

func TestUpdateWorkspaceBranch(t *testing.T) {
	s, first := created(t)
	const draft = "refs/heads/what-if"
	// Branches are not validated, and start at main.
	commit, err := s.UpdateWorkspace(fixture.WorkspaceID, draft, jane, "draft", put(map[string]string{"answers/notes.txt": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	if mainOf(t, s, draft) != commit || mainOf(t, s, mainRef) != first {
		t.Errorf("draft %s main %s", mainOf(t, s, draft), mainOf(t, s, mainRef))
	}
	repo, _ := s.WorkspaceRepo(fixture.WorkspaceID)
	if parent, _, _ := repo.ResolveRef(commit + "^"); parent != first {
		t.Errorf("parent %s, want %s", parent, first)
	}
	if oid, err := s.UpdateWorkspace(fixture.WorkspaceID, "refs/heads/empty", jane, "nothing", put(nil)); oid != "" || err != nil {
		t.Errorf("no-op on a new branch: %q %v", oid, err)
	}
	if _, ok, _ := repo.ResolveRef("refs/heads/empty"); ok {
		t.Error("a no-op created a branch")
	}
	if _, err := s.UpdateWorkspace(fixture.WorkspaceID, "refs/tags/v1", jane, "m", put(nil)); err == nil {
		t.Error("a tag must be rejected")
	}
}

// TestUpdateWorkspaceConflict moves main while the edit runs, as a push
// would, and expects the write to fail instead of overwriting the push.
func TestUpdateWorkspaceConflict(t *testing.T) {
	s, first := created(t)
	repo, _ := s.WorkspaceRepo(fixture.WorkspaceID)
	pushed, err := repo.WriteCommit(gitrepo.CommitRequest{Base: first, Parents: []string{first}, Author: jane, Message: "pushed",
		Changes: []gitrepo.Change{{Path: "README.md", Data: []byte("pushed")}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "answer", func(fs.FS) ([]gitrepo.Change, error) {
		if err := repo.UpdateRef(mainRef, pushed, first); err != nil {
			t.Fatal(err)
		}
		return put(map[string]string{answerA: answerFile("1.1.0")})(nil)
	})
	if !errors.Is(err, ErrConflict) || !errors.Is(err, gitrepo.ErrRefMoved) {
		t.Errorf("err = %v", err)
	}
	if mainOf(t, s, mainRef) != pushed {
		t.Error("the push was overwritten")
	}
}

func TestUpdateWorkspaceErrors(t *testing.T) {
	s, _ := created(t)
	boom := errors.New("boom")
	if _, err := s.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "m", func(fs.FS) ([]gitrepo.Change, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Errorf("edit error: %v", err)
	}
	if _, err := s.UpdateWorkspace("0f0e0d0c-0b0a-4908-8706-050403020100", mainRef, jane, "m", put(nil)); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown workspace: %v", err)
	}
}

// TestUpdateWorkspaceConcurrent runs writes in parallel; the lock must
// serialise them so that none is lost or fails.
func TestUpdateWorkspaceConcurrent(t *testing.T) {
	s, _ := created(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := fmt.Sprintf("notes/%d.txt", i)
			if _, err := s.UpdateWorkspace(fixture.WorkspaceID, "refs/heads/draft", jane, path, put(map[string]string{path: "x"})); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	repo, _ := s.WorkspaceRepo(fixture.WorkspaceID)
	tree, err := repo.TreeFS("refs/heads/draft")
	if err != nil {
		t.Fatal(err)
	}
	if entries, _ := fs.ReadDir(tree, "notes"); len(entries) != 8 {
		t.Errorf("%d notes, want 8", len(entries))
	}
}

func TestSetRef(t *testing.T) {
	s, first := created(t)
	repo, _ := s.WorkspaceRepo(fixture.WorkspaceID)
	commit := func(base, path, content string) string {
		c, err := repo.WriteCommit(gitrepo.CommitRequest{Base: base, Parents: []string{base}, Author: jane, Message: path,
			Changes: []gitrepo.Change{{Path: path, Data: []byte(content)}}})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	next := commit(first, answerA, answerFile("1.1.0"))
	if err := s.SetRef(fixture.WorkspaceID, mainRef, next, first); err != nil {
		t.Fatal(err)
	}
	var rej *RejectedError
	if err := s.SetRef(fixture.WorkspaceID, mainRef, first, next); !errors.As(err, &rej) || rej.Problems[0].Rule != problem.RuleHistory {
		t.Errorf("rewind: %v", err)
	}
	bad := commit(next, answerA, answerFile("9.0.0"))
	if err := s.SetRef(fixture.WorkspaceID, mainRef, bad, next); !errors.As(err, &rej) {
		t.Errorf("invalid content: %v", err)
	}
	ok := commit(next, "README.md", "x")
	if err := s.SetRef(fixture.WorkspaceID, mainRef, ok, first); !errors.Is(err, ErrConflict) {
		t.Errorf("stale old value: %v", err)
	}
	if err := s.SetRef(fixture.WorkspaceID, "refs/heads/draft", bad, ""); err != nil {
		t.Errorf("new branch: %v", err)
	}
	if err := s.SetRef(fixture.WorkspaceID, "refs/heads/draft", bad, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("branch exists: %v", err)
	}
}

func TestLoadAndStatus(t *testing.T) {
	s, _ := created(t)
	pin := mainOf(t, s, mainRef)
	if _, err := s.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "answer", put(map[string]string{answerA: answerFile("1.1.0")})); err != nil {
		t.Fatal(err)
	}
	w, c, err := s.Load(fixture.WorkspaceID, "")
	if err != nil {
		t.Fatal(err)
	}
	if w.Config.Workspace != fixture.WorkspaceID || w.Answers[answerA] == nil || !c.Tasks.Has(fixture.TaskA) {
		t.Errorf("loaded %+v", w.Config)
	}
	if w, _, err := s.Load(fixture.WorkspaceID, pin); err != nil || w.Answers[answerA] != nil {
		t.Errorf("load at the first commit: %v", err)
	}
	if _, _, err := s.Load(fixture.WorkspaceID, "refs/heads/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing branch: %v", err)
	}
	st, err := s.Status(fixture.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Workspace != fixture.WorkspaceID || len(st.Tasks) != 2 || st.Tasks[0].State != status.Answered || st.Tasks[1].State != status.Unanswered {
		t.Errorf("status %+v", st)
	}
}

// TestCheckWorkspace finds a main that was changed behind custos's back.
func TestCheckWorkspace(t *testing.T) {
	s, first := created(t)
	if ps, err := s.CheckWorkspace(fixture.WorkspaceID); err != nil || len(ps) != 0 {
		t.Fatalf("fresh workspace: %v %v", ps, err)
	}
	repo, _ := s.WorkspaceRepo(fixture.WorkspaceID)
	bad, err := repo.WriteCommit(gitrepo.CommitRequest{Base: first, Parents: []string{first}, Author: jane, Message: "on disk",
		Changes: []gitrepo.Change{{Path: answerA, Data: []byte(answerFile("9.0.0"))}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRef(mainRef, bad, first); err != nil {
		t.Fatal(err)
	}
	ps, err := s.CheckWorkspace(fixture.WorkspaceID)
	if err != nil || len(ps) != 1 || !strings.Contains(ps[0].Message, "9.0.0") {
		t.Errorf("%v %v", ps, err)
	}
	if _, err := s.Status(fixture.WorkspaceID); err != nil {
		t.Errorf("status of an inconsistent workspace: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/`
Expected: FAIL to compile: `s.UpdateWorkspace undefined`, `s.SetRef undefined`, `s.Load undefined`.

- [ ] **Step 3: Write `internal/store/write.go`**

```go
package store

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing/fstest"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/rules"
	"github.com/emeland-io/custos/internal/status"
	"github.com/emeland-io/custos/internal/workspace"
)

// UpdateWorkspace applies edit to the tree at ref ("refs/heads/main" or another
// branch; the ref may not exist yet for branches, then the base is main) under
// the workspace's lock. No changes → no commit, returns the current oid ("" for
// a branch that does not exist). Changes that leave a file as it is do not
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
		return err
	}
	if err := checkBranch(ref); err != nil {
		return err
	}
	unlock := s.Lock(id)
	defer unlock()
	if ref == mainRef {
		if err := s.validateMain(repo, id, oldOID, newOID); err != nil {
			return err
		}
	}
	return conflict(repo.UpdateRef(ref, newOID, oldOID))
}

// Load reads a workspace at rev ("" = main) and the catalog at its pin.
// Problems in the trees are not reported here; see CheckWorkspace.
func (s *Store) Load(id, rev string) (*workspace.Workspace, *catalog.Catalog, error) {
	repo, err := s.WorkspaceRepo(id)
	if err != nil {
		return nil, nil, err
	}
	if rev == "" {
		rev = mainRef
	}
	oid, ok, err := repo.ResolveRef(rev + "^{commit}")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("%w: revision %s of workspace %s", ErrNotFound, rev, id)
	}
	fsys, err := repo.TreeFS(oid)
	if err != nil {
		return nil, nil, err
	}
	w, _ := workspace.Load(fsys)
	pin := w.Config.Catalog.Commit
	cat := s.CatalogRepo()
	if pin == "" || strings.HasPrefix(pin, "-") {
		return nil, nil, fmt.Errorf("workspace %s at %s pins no catalog commit", id, rev)
	}
	pinOID, ok, err := cat.ResolveRef(pin + "^{commit}")
	if err != nil {
		return nil, nil, err
	}
	if !ok || pinOID != pin {
		return nil, nil, fmt.Errorf("workspace %s pins catalog commit %s, which this server's catalog does not have", id, pin)
	}
	cfs, err := cat.TreeFS(pin)
	if err != nil {
		return nil, nil, err
	}
	c, _ := catalog.Load(cfs)
	return w, c, nil
}

// Status computes status.Compute for the workspace's main.
func (s *Store) Status(id string) (*status.Status, error) {
	w, c, err := s.Load(id, "")
	if err != nil {
		return nil, err
	}
	return status.Compute(w, c), nil
}

// CheckWorkspace applies the push rules to the workspace's main as it is, so
// that serve can report a repository edited on disk (spec section 7). An
// empty repository has no problems.
func (s *Store) CheckWorkspace(id string) ([]problem.Problem, error) {
	repo, err := s.WorkspaceRepo(id)
	if err != nil {
		return nil, err
	}
	main, ok, err := repo.ResolveRef(mainRef)
	if err != nil || !ok {
		return nil, err
	}
	return rules.CheckWorkspaceUpdate(repo, s.CatalogRepo(), id, "", main)
}

// effective drops changes that leave tree as it is.
func effective(tree fs.FS, changes []gitrepo.Change) []gitrepo.Change {
	var out []gitrepo.Change
	for _, c := range changes {
		cur, err := fs.ReadFile(tree, c.Path)
		missing := errors.Is(err, fs.ErrNotExist)
		if c.Delete && missing || !c.Delete && err == nil && bytes.Equal(cur, c.Data) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func checkBranch(ref string) error {
	if !strings.HasPrefix(ref, "refs/heads/") {
		return fmt.Errorf("invalid branch %q: must start with refs/heads/", ref)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/store/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/write.go internal/store/write_test.go
git commit -m "Write, load and report workspaces through the store"
```

---

### Task 9: Server keeps HTTP only; CLI uses the store

**Files:**
- Modify: `internal/server/server.go` (replace everything above `// maxRequestBytes`)
- Modify: `internal/server/server_test.go` (replace whole file)
- Modify: `cmd/custos/serve.go`, `cmd/custos/workspace.go` (replace whole files), `cmd/custos/main.go` (usage)
- Modify: `cmd/custos/serve_test.go` (replace whole file)
- Modify: `README.md`

**Interfaces:**
- Consumes: `store.Open`, `store.New`, `Store.InstallHooks`, `Store.DataDir`, `Store.WorkspaceIDs`, `Store.CheckWorkspace`, `Store.CreateWorkspace`, `Store.CatalogRepo` (Tasks 7–8); `gitrepo.ParseSignature` (Task 1).
- Produces:
  - `func New(st *store.Store) *Server`, `func (s *Server) Handler() (http.Handler, error)` (`/git/`, `/healthz`), `func (s *Server) OnCatalogPush(f func())` — called after each `POST /git/catalog.git/git-receive-pack` completes (accepted or rejected), synchronously before the response ends. `server.Open` and `Server.CreateWorkspace` are removed.
  - CLI: `serve --public-url` (`CUSTOS_PUBLIC_URL`, default `http://127.0.0.1:8080`); `workspace create --public-url --author` (`CUSTOS_AUTHOR`, required). Helpers in `cmd/custos/serve.go`: `publicURLFlag(fl *flag.FlagSet) *string`, `checkPublicURL(s string) error`.

Test changes: in `internal/server/server_test.go`, `start` now builds `store.Open` + `InstallHooks` + `New(st)` and returns the store, the server and the URL; `TestWorkspaces` clones the workspace that `CreateWorkspace` made and pushes on top of it; `TestOpenReinstallsHooks`, `TestCreateWorkspaceLeavesOtherHooks` and `TestCreateWorkspaceInEmptyDataDir` are deleted, because `TestInstallHooks`, `TestCreateWorkspaceRepo` and `TestCreateWorkspaceNeedsCatalog` in `internal/store` (Task 7) cover them; `TestOnCatalogPush` is new; `TestHealthz` uses `store.New`. In `cmd/custos/serve_test.go`, `TestWorkspaceCreate` seeds a catalog and passes `--author`; `TestServePublicURL` and `TestWorkspaceCreateAuthor` are new.

- [ ] **Step 1: Replace `internal/server/server_test.go`**

```go
package server

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/store"
)

// custosBin is a custos binary built for these tests, because the
// pre-receive hook runs it.
var custosBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "custos-bin-*")
	if err != nil {
		panic(err)
	}
	custosBin = filepath.Join(dir, "custos")
	if out, err := exec.Command("go", "build", "-o", custosBin, "github.com/emeland-io/custos/cmd/custos").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build custos: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// start serves a new data directory the way custos serve does.
func start(t *testing.T) (*store.Store, *Server, string) {
	t.Helper()
	st, err := store.Open(t.TempDir(), custosBin, "http://custos.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InstallHooks(); err != nil {
		t.Fatal(err)
	}
	srv := New(st)
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return st, srv, ts.URL
}

var jane = gitrepo.Signature{Name: "Jane Doe", Email: "jane@example.org"}

func TestPushAndCloneCatalog(t *testing.T) {
	_, _, url := start(t)
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", url+"/git/catalog.git", "main")

	clone := filepath.Join(t.TempDir(), "c")
	gittest.Run(t, t.TempDir(), "clone", "--quiet", url+"/git/catalog.git", clone)
	if _, err := os.Stat(filepath.Join(clone, "groups", "index.yaml")); err != nil {
		t.Error(err)
	}
}

func TestInvalidPushIsRejected(t *testing.T) {
	_, _, url := start(t)
	work := gittest.Init(t)
	f := fixture.Catalog()
	f["groups/index.yaml"] = "groups: [missing]\n"
	gittest.Commit(t, work, f)
	out, err := gittest.Try(work, "push", url+"/git/catalog.git", "main")
	if err == nil || !strings.Contains(out, "custos rejected the push") || !strings.Contains(out, "groups/index.yaml: groups: lists unknown group") {
		t.Fatalf("err %v, output:\n%s", err, out)
	}
}

func TestPublishedVersionsAreImmutableButDraftsAreNot(t *testing.T) {
	_, _, url := start(t)
	remote := url + "/git/catalog.git"
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", remote, "main")

	path := fixture.TaskPath(fixture.TaskA, "1.0.0")
	gittest.Commit(t, work, map[string]string{path: fixture.TaskFile(fixture.TaskA, "1.0.0") + "Edited.\n"})
	out, err := gittest.Try(work, "push", remote, "main")
	if err == nil || !strings.Contains(out, path+": immutable: was changed") {
		t.Fatalf("err %v, output:\n%s", err, out)
	}
	gittest.Run(t, work, "push", remote, "HEAD:refs/heads/draft")
	gittest.Run(t, work, "commit", "--quiet", "--amend", "-m", "reworded")
	gittest.Run(t, work, "push", "--force", remote, "HEAD:refs/heads/draft")
}

func TestForcePushToMainIsRejected(t *testing.T) {
	_, _, url := start(t)
	remote := url + "/git/catalog.git"
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Commit(t, work, map[string]string{"README.md": "hello"})
	gittest.Run(t, work, "push", remote, "main")
	gittest.Run(t, work, "reset", "--quiet", "--hard", "HEAD~1")
	out, err := gittest.Try(work, "push", "--force", remote, "main")
	if err == nil || !strings.Contains(out, "cannot be rewritten") {
		t.Fatalf("err %v, output:\n%s", err, out)
	}
}

func TestLargePush(t *testing.T) {
	_, _, url := start(t)
	work := gittest.Init(t)
	f := fixture.Catalog()
	big := make([]byte, 3<<20) // above git's 1 MiB http.postBuffer, so git sends it chunked
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	f["big.bin"] = string(big)
	gittest.Commit(t, work, f)
	gittest.Run(t, work, "push", url+"/git/catalog.git", "main")
}

func TestWorkspaces(t *testing.T) {
	st, _, url := start(t)
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", url+"/git/catalog.git", "main")
	if err := st.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	remote := url + "/git/workspaces/" + fixture.WorkspaceID + ".git"
	clone := filepath.Join(t.TempDir(), "ws")
	gittest.Run(t, t.TempDir(), "clone", "--quiet", remote, clone)
	if _, err := os.Stat(filepath.Join(clone, "custos.yaml")); err != nil {
		t.Fatal(err)
	}

	gittest.Commit(t, clone, map[string]string{"answers/notes.txt": "x"})
	if out, err := gittest.Try(clone, "push", "origin", "main"); err == nil || !strings.Contains(out, "answers/notes.txt: path: unexpected file") {
		t.Fatalf("err %v, output:\n%s", err, out)
	}
	gittest.Run(t, clone, "reset", "--quiet", "--hard", "HEAD~1")
	gittest.Commit(t, clone, map[string]string{"answers/" + fixture.TaskA + ".md": fixture.AnswerFile})
	gittest.Run(t, clone, "push", "origin", "main")
}

func TestOnCatalogPush(t *testing.T) {
	_, srv, url := start(t)
	calls := 0
	srv.OnCatalogPush(func() { calls++ })
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", url+"/git/catalog.git", "main")
	if calls != 1 {
		t.Fatalf("%d calls after one push", calls)
	}
	gittest.Run(t, t.TempDir(), "ls-remote", url+"/git/catalog.git")
	f := fixture.Catalog()
	f["groups/index.yaml"] = "groups: [missing]\n"
	gittest.Commit(t, work, f)
	gittest.Try(work, "push", url+"/git/catalog.git", "main")
	if calls != 2 {
		t.Errorf("%d calls; want 2: a fetch does not count, a rejected push does", calls)
	}
}

func TestWithContentLengthLimitsBody(t *testing.T) {
	old := maxRequestBytes
	maxRequestBytes = 16
	t.Cleanup(func() { maxRequestBytes = old })

	var ran bool
	var gotLength int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran = true
		gotLength = r.ContentLength
	})
	h := withContentLength(inner)

	// A chunked body over the limit must be rejected before the inner
	// handler runs.
	ran = false
	req := httptest.NewRequest("POST", "/git/catalog.git/git-receive-pack", strings.NewReader(strings.Repeat("x", 64)))
	req.TransferEncoding = []string{"chunked"}
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("code = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if ran {
		t.Error("inner handler ran for an oversized chunked body")
	}

	// A body with a Content-Length over the limit must be rejected up front
	// too, not cut off inside the backend.
	ran = false
	req = httptest.NewRequest("POST", "/git/catalog.git/git-receive-pack", strings.NewReader(strings.Repeat("x", 64)))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "push too large") {
		t.Errorf("code = %d body %q, want %d", rec.Code, rec.Body.String(), http.StatusRequestEntityTooLarge)
	}
	if ran {
		t.Error("inner handler ran for an oversized body with Content-Length")
	}

	// A small body with Content-Length passes through unchanged.
	ran = false
	req = httptest.NewRequest("POST", "/git/catalog.git/git-receive-pack", strings.NewReader("small"))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !ran || gotLength != 5 {
		t.Errorf("ran %v, ContentLength = %d, want 5", ran, gotLength)
	}

	// A small chunked body must still reach the inner handler with the
	// right content length.
	ran = false
	body := "small"
	req = httptest.NewRequest("POST", "/git/catalog.git/git-receive-pack", strings.NewReader(body))
	req.TransferEncoding = []string{"chunked"}
	req.ContentLength = -1
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !ran {
		t.Error("inner handler did not run for a small chunked body")
	}
	if gotLength != int64(len(body)) {
		t.Errorf("ContentLength = %d, want %d", gotLength, len(body))
	}
}

func TestHealthz(t *testing.T) {
	h, err := New(store.New(t.TempDir(), custosBin, "http://custos.test")).Handler()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 || rec.Body.String() != "ok\n" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Replace `cmd/custos/serve_test.go`**

```go
package main

import (
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/store"
)

func TestServeNeedsDataDir(t *testing.T) {
	t.Setenv("CUSTOS_DATA_DIR", "")
	code, _, errs := runCmd(t, "serve")
	if code != 2 || !strings.Contains(errs, "--data-dir") {
		t.Errorf("code %d err %q", code, errs)
	}
}

func TestServeDefaultsToLoopback(t *testing.T) {
	t.Setenv("CUSTOS_ADDR", "")
	code, _, errs := runCmd(t, "serve", "-h")
	if code != 0 || !strings.Contains(errs, `(default "127.0.0.1:8080")`) {
		t.Errorf("code %d err %q", code, errs)
	}
}

func TestServeAddressInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	code, out, errs := runCmd(t, "serve", "--data-dir", t.TempDir(), "--addr", ln.Addr().String())
	if code != 1 || strings.Contains(out, "listening") || !strings.Contains(errs, "custos serve:") {
		t.Errorf("code %d out %q err %q", code, out, errs)
	}
}

func TestServePublicURL(t *testing.T) {
	t.Setenv("CUSTOS_PUBLIC_URL", "")
	code, _, errs := runCmd(t, "serve", "-h")
	if code != 0 || !strings.Contains(errs, `(default "http://127.0.0.1:8080")`) {
		t.Errorf("code %d err %q", code, errs)
	}
	for _, bad := range []string{"127.0.0.1:8080", "ftp://x", "http://", "http://x/?a=b"} {
		if code, _, errs := runCmd(t, "serve", "--data-dir", t.TempDir(), "--public-url", bad); code != 2 || !strings.Contains(errs, "--public-url") {
			t.Errorf("%q: code %d err %q", bad, code, errs)
		}
	}
}

// seedCatalog puts the fixture catalog on main of the data directory's
// catalog, without hooks.
func seedCatalog(t *testing.T, data string) {
	t.Helper()
	st, err := store.Open(data, "/custos", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", "--quiet", st.CatalogRepo().Dir, "main")
}

func TestWorkspaceCreate(t *testing.T) {
	t.Setenv("CUSTOS_AUTHOR", "")
	t.Setenv("CUSTOS_PUBLIC_URL", "")
	data := t.TempDir()
	if code, _, errs := runCmd(t, "workspace", "create", "--data-dir", data, "--author", "Jane Doe <jane@example.org>", fixture.WorkspaceID); code != 1 || !strings.Contains(errs, "push the catalog first") {
		t.Fatalf("empty catalog: code %d err %q", code, errs)
	}
	seedCatalog(t, data)
	code, out, errs := runCmd(t, "workspace", "create", "--data-dir", data, "--public-url", "https://custos.example.org/",
		"--author", "Jane Doe <jane@example.org>", fixture.WorkspaceID)
	if code != 0 || !strings.Contains(out, "clone it from https://custos.example.org/git/workspaces/"+fixture.WorkspaceID+".git") {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
	dir := filepath.Join(data, "repos", "workspaces", fixture.WorkspaceID+".git")
	if who := gittest.Run(t, dir, "log", "-1", "--format=%an <%ae>", "main"); who != "Jane Doe <jane@example.org>" {
		t.Errorf("author %q", who)
	}
	if config := gittest.Run(t, dir, "show", "main:custos.yaml"); !strings.Contains(config, "url: https://custos.example.org/git/catalog.git") {
		t.Errorf("custos.yaml %q", config)
	}
	if code, _, _ := runCmd(t, "workspace", "create", "--data-dir", data, "--author", "Jane Doe <jane@example.org>", "not-a-uuid"); code != 1 {
		t.Errorf("invalid id: code %d", code)
	}
}

func TestWorkspaceCreateAuthor(t *testing.T) {
	t.Setenv("CUSTOS_AUTHOR", "")
	data := t.TempDir()
	if code, _, errs := runCmd(t, "workspace", "create", "--data-dir", data, fixture.WorkspaceID); code != 2 || !strings.Contains(errs, "--author") {
		t.Errorf("no author: code %d err %q", code, errs)
	}
	if code, _, errs := runCmd(t, "workspace", "create", "--data-dir", data, "--author", "jane", fixture.WorkspaceID); code != 2 || !strings.Contains(errs, "Name <email>") {
		t.Errorf("bad author: code %d err %q", code, errs)
	}
	seedCatalog(t, data)
	t.Setenv("CUSTOS_AUTHOR", "Env Author <env@example.org>")
	if code, _, errs := runCmd(t, "workspace", "create", "--data-dir", data, fixture.WorkspaceID); code != 0 {
		t.Fatalf("code %d err %q", code, errs)
	}
	dir := filepath.Join(data, "repos", "workspaces", fixture.WorkspaceID+".git")
	if who := gittest.Run(t, dir, "log", "-1", "--format=%an", "main"); who != "Env Author" {
		t.Errorf("author %q", who)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/server/ ./cmd/custos/`
Expected: FAIL to compile: `too many arguments in call to New` / `cannot use st (variable of type *store.Store)`, `undefined: srv.OnCatalogPush`.

- [ ] **Step 4: Rewrite the top of `internal/server/server.go`**

Replace everything from the first line up to (not including) the comment `// maxRequestBytes bounds every request body …` with:

```go
// Package server serves the repositories of a store over HTTP.
package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/emeland-io/custos/internal/store"
)

// catalogPushPath is the request that carries a push to the catalog.
const catalogPushPath = "/git/catalog.git/git-receive-pack"

// Server serves the repositories of a store. Repository layout, hooks and
// writes belong to the store (ruling 2.17).
type Server struct {
	st *store.Store

	mu            sync.Mutex
	onCatalogPush []func()
}

// New returns a server for st.
func New(st *store.Store) *Server { return &Server{st: st} }

// OnCatalogPush registers f to be called after each request to
// /git/catalog.git/git-receive-pack completes, whether the push was accepted
// or not. f runs before the response ends, so the pusher waits for it.
func (s *Server) OnCatalogPush(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onCatalogPush = append(s.onCatalogPush, f)
}

// Handler serves the repositories below /git through git http-backend, and
// GET /healthz.
func (s *Server) Handler() (http.Handler, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("custos needs git on PATH: %w", err)
	}
	backend := &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Root: "/git",
		Env:  []string{"GIT_PROJECT_ROOT=" + filepath.Join(s.st.DataDir(), "repos"), "GIT_HTTP_EXPORT_ALL=1"},
	}
	mux := http.NewServeMux()
	mux.Handle("/git/", withContentLength(s.notifyCatalogPush(backend)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	return mux, nil
}

func (s *Server) notifyCatalogPush(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		if r.Method != http.MethodPost || r.URL.Path != catalogPushPath {
			return
		}
		s.mu.Lock()
		fs := append([]func(){}, s.onCatalogPush...)
		s.mu.Unlock()
		for _, f := range fs {
			f()
		}
	})
}
```

`maxRequestBytes` and `withContentLength` below stay unchanged.

- [ ] **Step 5: Replace `cmd/custos/serve.go`**

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/emeland-io/custos/internal/server"
	"github.com/emeland-io/custos/internal/store"
)

// defaultAddr listens on loopback only: there is no authentication yet.
const defaultAddr = "127.0.0.1:8080"

// defaultPublicURL is where clients reach a server on defaultAddr.
const defaultPublicURL = "http://127.0.0.1:8080"

func runServe(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dataDir := fl.String("data-dir", os.Getenv("CUSTOS_DATA_DIR"), "directory holding the repositories (env CUSTOS_DATA_DIR)")
	addr := fl.String("addr", envOr("CUSTOS_ADDR", defaultAddr), "listen address (env CUSTOS_ADDR); there is no authentication yet, so keep it on loopback unless the network is trusted")
	publicURL := publicURLFlag(fl)
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	if *dataDir == "" {
		fmt.Fprintln(stderr, "custos serve: --data-dir or CUSTOS_DATA_DIR is required")
		return 2
	}
	if err := checkPublicURL(*publicURL); err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 2
	}
	srv, err := openServer(*dataDir, *publicURL, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 1
	}
	h, err := srv.Handler()
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Bind before announcing, so "listening" is only printed when it is true.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 1
	}
	hs := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	fmt.Fprintf(stdout, "custos listening on %s, repositories in %s\n", *addr, *dataDir)
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "custos serve: %v\n", err)
			return 1
		}
		return 0
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := hs.Shutdown(shutdown); err != nil {
			fmt.Fprintf(stderr, "custos serve: %v\n", err)
			return 1
		}
		return 0
	}
}

// openServer opens the data directory, points all hooks at this binary and
// reports workspaces whose main breaks the rules, for example after an edit
// on disk (spec section 7); it serves them anyway.
func openServer(dataDir, publicURL string, stderr io.Writer) (*server.Server, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(dataDir, exe, publicURL)
	if err != nil {
		return nil, err
	}
	if err := st.InstallHooks(); err != nil {
		return nil, err
	}
	ids, err := st.WorkspaceIDs()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		ps, err := st.CheckWorkspace(id)
		if err != nil {
			fmt.Fprintf(stderr, "custos serve: workspace %s: %v\n", id, err)
		}
		for _, p := range ps {
			fmt.Fprintf(stderr, "custos serve: workspace %s is inconsistent: %s\n", id, p)
		}
	}
	return server.New(st), nil
}

// publicURLFlag defines --public-url.
func publicURLFlag(fl *flag.FlagSet) *string {
	return fl.String("public-url", envOr("CUSTOS_PUBLIC_URL", defaultPublicURL), "base URL clients reach the server at, written into new workspaces (env CUSTOS_PUBLIC_URL)")
}

// checkPublicURL accepts absolute http and https URLs without query.
func checkPublicURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(s, "'") {
		return fmt.Errorf("--public-url %q is not an absolute http or https URL such as %s", s, defaultPublicURL)
	}
	return nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
```

- [ ] **Step 6: Replace `cmd/custos/workspace.go`**

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
)

const workspaceUsage = "usage: custos workspace create [--data-dir DIR] [--public-url URL] --author \"Name <email>\" WORKSPACE-UUID\n"

func runWorkspace(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "create" {
		fmt.Fprint(stderr, workspaceUsage)
		return 2
	}
	fl := flag.NewFlagSet("workspace create", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dataDir := fl.String("data-dir", os.Getenv("CUSTOS_DATA_DIR"), "directory holding the repositories (env CUSTOS_DATA_DIR)")
	publicURL := publicURLFlag(fl)
	authorFlag := fl.String("author", os.Getenv("CUSTOS_AUTHOR"), "author of the initial commit, as \"Name <email>\" (env CUSTOS_AUTHOR)")
	if err := fl.Parse(args[1:]); err != nil {
		return helpOrUsage(err)
	}
	if fl.NArg() != 1 || *dataDir == "" || *authorFlag == "" {
		fmt.Fprint(stderr, workspaceUsage)
		return 2
	}
	author, err := gitrepo.ParseSignature(*authorFlag)
	if err != nil {
		fmt.Fprintf(stderr, "custos workspace create: --author: %v\n", err)
		return 2
	}
	if err := checkPublicURL(*publicURL); err != nil {
		fmt.Fprintf(stderr, "custos workspace create: %v\n", err)
		return 2
	}
	// Only the new repository gets a hook; the hooks of existing ones stay
	// as custos serve installed them.
	exe, err := os.Executable()
	if err == nil {
		err = store.New(*dataDir, exe, *publicURL).CreateWorkspace(fl.Arg(0), author)
	}
	if err != nil {
		fmt.Fprintf(stderr, "custos workspace create: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "created workspace %s; clone it from %s/git/workspaces/%s.git\n", fl.Arg(0), strings.TrimRight(*publicURL, "/"), fl.Arg(0))
	return 0
}
```

- [ ] **Step 7: Update the usage in `cmd/custos/main.go`**

Replace:

```text
  custos serve [--data-dir DIR] [--addr ADDR]
  custos validate [--against REV] [DIR]
  custos task new-version (--patch | --minor | --major) [--dir DIR] TASK-UUID
  custos workspace create [--data-dir DIR] WORKSPACE-UUID
```

with:

```text
  custos serve [--data-dir DIR] [--addr ADDR] [--public-url URL]
  custos validate [--against REV] [DIR]
  custos task new-version (--patch | --minor | --major) [--dir DIR] TASK-UUID
  custos workspace create [--data-dir DIR] [--public-url URL] --author "Name <email>" WORKSPACE-UUID
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go vet ./... && go test ./...`
Expected: PASS

- [ ] **Step 9: Update `README.md`**

Replace:

```markdown
This version implements its first phase: file formats, validation, Git over
HTTP and the command line. Distributing catalog updates to workspaces,
processors and the web UI follow.
```

with:

```markdown
This version implements the first phase (file formats, validation, Git over
HTTP and the command line) and the start of the second: workspaces are
created pinned to the catalog, and pushes to a workspace are checked against
that catalog. Distributing catalog updates to workspaces, the REST API,
processors and the web UI follow.
```

Replace:

````markdown
custos workspace create [--data-dir DIR] WORKSPACE-UUID  # create an empty workspace repository
custos serve [--data-dir DIR] [--addr ADDR]              # serve the repositories over HTTP
```

`workspace create` and `serve` need `--data-dir` or `CUSTOS_DATA_DIR`.
````

with:

````markdown
custos workspace create --author "Jane Doe <jane@example.org>" WORKSPACE-UUID
                                                         # create a workspace pinned to the catalog
custos serve [--data-dir DIR] [--addr ADDR] [--public-url URL]
                                                         # serve the repositories over HTTP
```

`workspace create` and `serve` need `--data-dir` or `CUSTOS_DATA_DIR`.
`workspace create` also needs `--author` or `CUSTOS_AUTHOR`, the author of
the workspace's first commit.
````

Replace:

```markdown
Requires Go 1.26 and git 2.28 or later.
```

with:

```markdown
Requires Go 1.26 and git 2.38 or later.
```

Replace:

````markdown
A workspace repository must be created before its first push:

```sh
./custos workspace create --data-dir ./data 5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9
git push http://127.0.0.1:8080/git/workspaces/5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9.git main
```

| Flag | Environment | Default |
| --- | --- | --- |
| `--data-dir` | `CUSTOS_DATA_DIR` | required |
| `--addr` | `CUSTOS_ADDR` | `127.0.0.1:8080` |
````

with:

````markdown
Push the catalog first; a workspace can only be created once the catalog
has a `main` branch. Creating a workspace makes its first commit, a
`custos.yaml` pinned to the catalog's current `main`, so clone it and push
your answers on top:

```sh
./custos workspace create --data-dir ./data --author "Jane Doe <jane@example.org>" \
    5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9
git clone http://127.0.0.1:8080/git/workspaces/5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9.git
```

| Flag | Environment | Default |
| --- | --- | --- |
| `--data-dir` | `CUSTOS_DATA_DIR` | required |
| `--addr` | `CUSTOS_ADDR` | `127.0.0.1:8080` |
| `--public-url` | `CUSTOS_PUBLIC_URL` | `http://127.0.0.1:8080` |
| `--author` (`workspace create`) | `CUSTOS_AUTHOR` | required |

`--public-url` is the address clients reach the server at. custos writes it
into the `catalog.url` of new workspaces, so set it whenever the server is
reached under another address than the default, for example behind a proxy
or with another `--addr`.

At start, `serve` reports workspaces whose `main` breaks the rules, for
example after an edit on disk, and serves them anyway.
````

Replace:

```markdown
docker run -d --name custos -p 127.0.0.1:9090:8080 -v custos-data:/data custos:dev
docker exec custos custos workspace create 5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9
```

with:

```markdown
docker run -d --name custos -p 127.0.0.1:9090:8080 -v custos-data:/data \
    -e CUSTOS_PUBLIC_URL=http://127.0.0.1:9090 custos:dev
git push http://127.0.0.1:9090/git/catalog.git main
docker exec custos custos workspace create --author "Jane Doe <jane@example.org>" \
    5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9
```

- [ ] **Step 10: Commit**

```bash
git add internal/server cmd/custos README.md
git commit -m "Serve the store over HTTP and create workspaces pinned to the catalog"
```

---

### Task 10: Workspace pushes checked against the catalog

**Files:**
- Modify: `internal/hook/hook.go`, `internal/hook/hook_test.go` (replace whole files)
- Modify: `internal/store/store.go` (`installHook`), `internal/store/store_test.go` (`TestCreateWorkspace`)
- Modify: `cmd/custos/hook.go`
- Modify: `internal/server/server_test.go` (add one test)
- Modify: `README.md`

**Interfaces:**
- Consumes: `rules.CheckCatalogUpdate`, `rules.CheckWorkspaceUpdate` (Task 4), `Store.CatalogRepo` (Task 7), `fixture.PinnedWorkspace`, `fixture.Config` (Task 4), test helper `start`, `jane` in `internal/server/server_test.go` (Task 9).
- Produces (architecture note):
  - `type ScriptOptions struct { Kind Kind; CatalogDir string; WorkspaceID string }`
  - `func Script(exe string, opts ScriptOptions) (string, error)` — workspace hooks run `'<exe>' hook pre-receive --kind workspace --catalog '<dir>' --workspace <id>`; a relative or quoted `CatalogDir` or an invalid id is an error.
  - `func PreReceive(repo *gitrepo.Repo, opts ScriptOptions, updates io.Reader) ([]problem.Problem, error)` — delegates `main` to `rules`; the phase-1 `checkMain` is gone.
  - `custos hook pre-receive` gains `--catalog DIR` and `--workspace UUID`.

Test changes in `internal/hook/hook_test.go`: `check` passes `ScriptOptions{Kind: kind}`; `TestWorkspace` builds a catalog repository and pins the workspace to it (the phase-1 fixture's pin `0123…` is no catalog commit and is now rejected with rule `pin`); `TestScript` expects the new workspace command line; `TestMalformedInput` passes `ScriptOptions`; `TestWorkspaceNeedsOptions` is new. The catalog tests are unchanged. After this task, hooks installed by an older binary lack `--catalog`; `serve` rewrites all hooks at start (`InstallHooks`), and a hook without the flags rejects pushes instead of skipping the checks.

- [ ] **Step 1: Replace `internal/hook/hook_test.go`**

```go
package hook

import (
	"io"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/problem"
)

var zero = strings.Repeat("0", 40)

func update(old, new, ref string) io.Reader {
	return strings.NewReader(old + " " + new + " " + ref + "\n")
}

func check(t *testing.T, dir string, kind Kind, input io.Reader) []problem.Problem {
	t.Helper()
	ps, err := PreReceive(&gitrepo.Repo{Dir: dir}, ScriptOptions{Kind: kind}, input)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestFirstPushOfValidCatalog(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Catalog())
	fixture.WantNone(t, check(t, dir, Catalog, update(zero, c1, "refs/heads/main")))
}

func TestInvalidCatalog(t *testing.T) {
	dir := gittest.Init(t)
	f := fixture.Catalog()
	f["groups/index.yaml"] = "groups: [missing]\n"
	c1 := gittest.Commit(t, dir, f)
	fixture.WantProblem(t, check(t, dir, Catalog, update(zero, c1, "refs/heads/main")), "groups/index.yaml", problem.RuleGroups, "unknown group")
}

func TestChangedPublishedVersion(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Catalog())
	path := fixture.TaskPath(fixture.TaskA, "1.0.0")
	c2 := gittest.Commit(t, dir, map[string]string{path: fixture.TaskFile(fixture.TaskA, "1.0.0") + "Changed.\n"})
	fixture.WantProblem(t, check(t, dir, Catalog, update(c1, c2, "refs/heads/main")), path, problem.RuleImmutable, "was changed")
}

// TestGitattributesCannotHideFiles guards against .gitattributes hiding a
// file from validation, for example with export-ignore.
func TestGitattributesCannotHideFiles(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Catalog())
	path := fixture.TaskPath(fixture.TaskA, "2.0.0")
	c2 := gittest.Commit(t, dir, map[string]string{
		path:             "not a task\n",
		".gitattributes": path + " export-ignore\n",
	})
	fixture.WantProblem(t, check(t, dir, Catalog, update(c1, c2, "refs/heads/main")), path, problem.RuleFormat, "")
}

func TestDraftBranchesAreNotChecked(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, map[string]string{"groups/index.yaml": "groups: [missing]\n"})
	fixture.WantNone(t, check(t, dir, Catalog, update(zero, c1, "refs/heads/draft")))
}

func TestDeletingMain(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Catalog())
	fixture.WantProblem(t, check(t, dir, Catalog, update(c1, zero, "refs/heads/main")), "", problem.RuleHistory, "cannot be deleted")
}

func TestRewritingMain(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Catalog())
	c2 := gittest.Commit(t, dir, map[string]string{"README.md": "hello"})
	fixture.WantProblem(t, check(t, dir, Catalog, update(c2, c1, "refs/heads/main")), "", problem.RuleHistory, "cannot be rewritten")
}

func TestWorkspace(t *testing.T) {
	cat := gittest.Init(t)
	c1 := gittest.Commit(t, cat, fixture.Catalog())
	opts := ScriptOptions{Kind: Workspace, CatalogDir: cat, WorkspaceID: fixture.WorkspaceID}
	run := func(dir string, input io.Reader) []problem.Problem {
		t.Helper()
		ps, err := PreReceive(&gitrepo.Repo{Dir: dir}, opts, input)
		if err != nil {
			t.Fatal(err)
		}
		return ps
	}

	dir := gittest.Init(t)
	w1 := gittest.Commit(t, dir, fixture.PinnedWorkspace(c1))
	fixture.WantNone(t, run(dir, update(zero, w1, "refs/heads/main")))
	// The fixture's pin is no commit of this catalog.
	w2 := gittest.Commit(t, dir, fixture.Workspace())
	fixture.WantProblem(t, run(dir, update(w1, w2, "refs/heads/main")), "custos.yaml", problem.RulePin, "does not exist")
	fixture.WantNone(t, run(dir, update(w1, w2, "refs/heads/draft")))

	other := gittest.Init(t)
	c2 := gittest.Commit(t, other, fixture.Catalog())
	fixture.WantProblem(t, run(other, update(zero, c2, "refs/heads/main")), "custos.yaml", problem.RuleFormat, "missing")
}

func TestWorkspaceNeedsOptions(t *testing.T) {
	for _, opts := range []ScriptOptions{
		{Kind: Workspace, WorkspaceID: fixture.WorkspaceID},
		{Kind: Workspace, CatalogDir: "relative/catalog.git", WorkspaceID: fixture.WorkspaceID},
		{Kind: Workspace, CatalogDir: "/data/repos/catalog.git", WorkspaceID: "ws-1"},
		{Kind: "other"},
	} {
		if _, err := PreReceive(&gitrepo.Repo{Dir: t.TempDir()}, opts, strings.NewReader("")); err == nil {
			t.Errorf("PreReceive accepted %+v", opts)
		}
		if _, err := Script("/custos", opts); err == nil {
			t.Errorf("Script accepted %+v", opts)
		}
	}
}

func TestMalformedInput(t *testing.T) {
	if _, err := PreReceive(&gitrepo.Repo{Dir: t.TempDir()}, ScriptOptions{Kind: Catalog}, strings.NewReader("garbage\n")); err == nil {
		t.Error("want error")
	}
}

func TestScript(t *testing.T) {
	s, err := Script("/usr/local/bin/custos", ScriptOptions{Kind: Catalog})
	if err != nil {
		t.Fatal(err)
	}
	if want := "#!/bin/sh\nexec '/usr/local/bin/custos' hook pre-receive --kind catalog\n"; s != want {
		t.Errorf("got %q, want %q", s, want)
	}
	s, err = Script("/usr/local/bin/custos", ScriptOptions{Kind: Workspace, CatalogDir: "/data/repos/catalog.git", WorkspaceID: fixture.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	want := "#!/bin/sh\nexec '/usr/local/bin/custos' hook pre-receive --kind workspace --catalog '/data/repos/catalog.git' --workspace " + fixture.WorkspaceID + "\n"
	if s != want {
		t.Errorf("got %q, want %q", s, want)
	}
	if _, err := Script("/tmp/it's/custos", ScriptOptions{Kind: Catalog}); err == nil {
		t.Error("a quote in the path must be rejected")
	}
	if _, err := Script("/custos", ScriptOptions{Kind: Workspace, CatalogDir: "/it's/catalog.git", WorkspaceID: fixture.WorkspaceID}); err == nil {
		t.Error("a quote in the catalog path must be rejected")
	}
}

func TestParseKind(t *testing.T) {
	if k, err := ParseKind("catalog"); k != Catalog || err != nil {
		t.Errorf("%v %v", k, err)
	}
	if _, err := ParseKind("other"); err == nil {
		t.Error("want error")
	}
}
```

- [ ] **Step 2: Add the end-to-end test to `internal/server/server_test.go`**

Insert before `func TestOnCatalogPush(`:

```go
// TestWorkspacePushesAreCheckedAgainstCatalog runs the hook with git's
// quarantine environment, in which it must still read the catalog.
func TestWorkspacePushesAreCheckedAgainstCatalog(t *testing.T) {
	st, _, url := start(t)
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", url+"/git/catalog.git", "main")
	if err := st.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(t.TempDir(), "ws")
	gittest.Run(t, t.TempDir(), "clone", "--quiet", url+"/git/workspaces/"+fixture.WorkspaceID+".git", clone)
	answer := "answers/" + fixture.TaskA + ".md"

	for _, bad := range []struct{ path, content, want string }{
		{answer, strings.Replace(fixture.AnswerFile, "1.0.0", "7.0.0", 1), answer + ": answer: task " + fixture.TaskA + " has no version 7.0.0"},
		{"custos.yaml", fixture.Config("0f0e0d0c-0b0a-4908-8706-050403020100", fixture.Commit), "custos.yaml: workspace-id:"},
		{"custos.yaml", fixture.Config(fixture.WorkspaceID, fixture.Commit), "custos.yaml: pin: catalog commit " + fixture.Commit + " does not exist"},
	} {
		gittest.Commit(t, clone, map[string]string{bad.path: bad.content})
		if out, err := gittest.Try(clone, "push", "origin", "main"); err == nil || !strings.Contains(out, bad.want) {
			t.Errorf("err %v, output:\n%s\nwant %q", err, out, bad.want)
		}
		gittest.Run(t, clone, "reset", "--quiet", "--hard", "origin/main")
	}
	gittest.Commit(t, clone, map[string]string{answer: fixture.AnswerFile})
	gittest.Run(t, clone, "push", "origin", "main")
}
```

- [ ] **Step 3: Expect the full hook command in `internal/store/store_test.go`**

In `TestCreateWorkspace`:

Replace:

```go
	if !strings.Contains(hookOf(t, repo.Dir), "'/custos' hook pre-receive --kind workspace") {
```

with:

```go
	if want := "'/custos' hook pre-receive --kind workspace --catalog '" + s.CatalogRepo().Dir + "' --workspace " + fixture.WorkspaceID; !strings.Contains(hookOf(t, repo.Dir), want) {
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/hook/ ./internal/store/ ./internal/server/`
Expected: FAIL to compile in `internal/hook`: `undefined: ScriptOptions`; `internal/server` fails `TestWorkspacePushesAreCheckedAgainstCatalog` (the answer to version 7.0.0 is accepted).

- [ ] **Step 5: Replace `internal/hook/hook.go`**

```go
// Package hook implements the pre-receive hook that keeps the main branch
// of every custos repository valid. The rules themselves live in package
// rules, which the store applies to its own writes too.
package hook

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/rules"
	"github.com/emeland-io/custos/internal/task"
)

// Kind is the kind of repository a hook guards.
type Kind string

const (
	Catalog   Kind = "catalog"
	Workspace Kind = "workspace"
)

const mainRef = "refs/heads/main"

// ParseKind parses the --kind flag of the hook command.
func ParseKind(s string) (Kind, error) {
	switch k := Kind(s); k {
	case Catalog, Workspace:
		return k, nil
	}
	return "", fmt.Errorf("unknown repository kind %q, want catalog or workspace", s)
}

// ScriptOptions describe the repository a hook guards.
type ScriptOptions struct {
	Kind        Kind
	CatalogDir  string // absolute path of catalog.git; workspace hooks only
	WorkspaceID string // workspace hooks only
}

func (o ScriptOptions) check() error {
	switch o.Kind {
	case Catalog:
		return nil
	case Workspace:
		if !filepath.IsAbs(o.CatalogDir) || strings.ContainsAny(o.CatalogDir, "'\n") {
			return fmt.Errorf("workspace hook: catalog directory %q must be an absolute path without quotes or line breaks", o.CatalogDir)
		}
		if !task.ValidID(o.WorkspaceID) {
			return fmt.Errorf("workspace hook: workspace id %q is not a lowercase UUID v4", o.WorkspaceID)
		}
		return nil
	}
	_, err := ParseKind(string(o.Kind))
	return err
}

// PreReceive checks the ref updates git passes to a pre-receive hook, one
// "<old> <new> <ref>" line each. Only main is checked; draft branches may
// hold anything and may be rewritten.
func PreReceive(repo *gitrepo.Repo, opts ScriptOptions, updates io.Reader) ([]problem.Problem, error) {
	if err := opts.check(); err != nil {
		return nil, err
	}
	var ps []problem.Problem
	sc := bufio.NewScanner(updates)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 {
			return nil, fmt.Errorf("unexpected hook input %q", sc.Text())
		}
		if f[2] != mainRef {
			continue
		}
		var mps []problem.Problem
		var err error
		if opts.Kind == Catalog {
			mps, err = rules.CheckCatalogUpdate(repo, f[0], f[1])
		} else {
			mps, err = rules.CheckWorkspaceUpdate(repo, &gitrepo.Repo{Dir: opts.CatalogDir}, opts.WorkspaceID, f[0], f[1])
		}
		if err != nil {
			return nil, err
		}
		ps = append(ps, mps...)
	}
	return ps, sc.Err()
}

// Script returns a pre-receive hook that runs the custos binary at exe.
func Script(exe string, opts ScriptOptions) (string, error) {
	if strings.ContainsAny(exe, "'\n") {
		return "", fmt.Errorf("cannot use %q in a hook script", exe)
	}
	if err := opts.check(); err != nil {
		return "", err
	}
	cmd := fmt.Sprintf("exec '%s' hook pre-receive --kind %s", exe, opts.Kind)
	if opts.Kind == Workspace {
		cmd += fmt.Sprintf(" --catalog '%s' --workspace %s", opts.CatalogDir, opts.WorkspaceID)
	}
	return "#!/bin/sh\n" + cmd + "\n", nil
}
```

- [ ] **Step 6: Pass the catalog and the workspace id when installing hooks, in `internal/store/store.go`**

Replace:

```go
	kind := hook.Catalog
	if id != "" {
		kind = hook.Workspace
	}
	script, err := hook.Script(s.exe, kind)
```

with:

```go
	opts := hook.ScriptOptions{Kind: hook.Catalog}
	if id != "" {
		opts = hook.ScriptOptions{Kind: hook.Workspace, CatalogDir: s.CatalogRepo().Dir, WorkspaceID: id}
	}
	script, err := hook.Script(s.exe, opts)
```

`CatalogRepo().Dir` is absolute because `New` makes the data directory absolute.

- [ ] **Step 7: Add the flags to `cmd/custos/hook.go`**

Replace:

```go
		fmt.Fprintln(stderr, "usage: custos hook pre-receive --kind catalog|workspace")
```

with:

```go
		fmt.Fprintln(stderr, "usage: custos hook pre-receive --kind catalog|workspace [--catalog DIR --workspace UUID]")
```

Replace:

```go
	kindFlag := fl.String("kind", "", "catalog or workspace")
```

with:

```go
	kindFlag := fl.String("kind", "", "catalog or workspace")
	catalogDir := fl.String("catalog", "", "absolute path of catalog.git (workspace hooks)")
	workspaceID := fl.String("workspace", "", "id of the workspace the repository belongs to (workspace hooks)")
```

Replace:

```go
	ps, err := hook.PreReceive(&gitrepo.Repo{Dir: ".", InheritGitEnv: true}, kind, stdin)
```

with:

```go
	// The repository the hook runs in sees the quarantined objects of the
	// push through git's environment; the catalog must not.
	opts := hook.ScriptOptions{Kind: kind, CatalogDir: *catalogDir, WorkspaceID: *workspaceID}
	ps, err := hook.PreReceive(&gitrepo.Repo{Dir: ".", InheritGitEnv: true}, opts, stdin)
```

- [ ] **Step 8: Run all tests to verify they pass**

Run: `go vet ./... && go test -race ./...`
Expected: PASS

- [ ] **Step 9: Document the new rules in `README.md`**

Replace:

```markdown
Markdown answers keep their text in the body and have no `value`.
```

with:

```markdown
Markdown answers keep their text in the body and have no `value`.

### Rules for a workspace's `main`

Besides the format rules above (`format`, `path`, `previous`, `history`), a
push to a workspace's `main` is checked against the server's catalog:

| Rule | Meaning |
| --- | --- |
| `workspace-id` | `workspace` in `custos.yaml` is the id of the repository pushed to. |
| `pin` | `catalog.commit` is a commit on the catalog's `main`, the current one or an older one. |
| `answer` | Each answer names a task of the pinned catalog or a generated task, a `task_version` that exists, the task's answer type, and for `choice` one of its choices. |

`custos validate` in a workspace checkout checks the workspace on its own;
these three rules are checked on push.
```

- [ ] **Step 10: Run the full suite and check the container image (skip Docker if unavailable, and say so)**

Run: `make test`
Expected: PASS

Run:
```bash
make docker
docker run -d --name custos-smoke -p 127.0.0.1:9090:8080 -e CUSTOS_PUBLIC_URL=http://127.0.0.1:9090 custos:dev
sleep 2
curl -fsS http://127.0.0.1:9090/healthz
docker exec custos-smoke custos workspace create --author "Smoke <smoke@example.org>" 5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9
docker rm -f custos-smoke
```
Expected: `ok`, then `workspace create` exits 1 with `push the catalog first` (the catalog is empty), which shows the binary, the data volume and the error path work inside the image. The image's Alpine git is newer than 2.38.

- [ ] **Step 11: Commit**

```bash
git add internal/hook internal/store internal/server/server_test.go cmd/custos/hook.go README.md
git commit -m "Check workspace pushes against the server's catalog"
```

---

## Self-Review Notes

- **Spec coverage (2a scope):**
  - §3.3 answers in effect, *pending update*, answers of merged tasks shown together until the merge is answered, merged answers kept for reference → Task 5 (`TestComputeMergedTasks`), Task 6.
  - §4.2 status side of answering (merged answers as reference) → Task 5; the write endpoint itself is 2b, built on `Store.UpdateWorkspace` (Task 8).
  - §4.5 one write queue per repository → `Store.Lock`, used by `UpdateWorkspace`, `SetRef`, `CreateWorkspace` (Tasks 7–8); pushes validated by the same rules → Tasks 4 and 10.
  - §5.5 generated tasks in the book under their origin or producing task → Task 5.
  - §7 inconsistent workspace reported, no crash, no silent repair → `Store.CheckWorkspace` (Task 8), `serve` start-up report (Task 9), status robust on broken trees (Task 5). Invalid catalog changes name file and rule → Task 4 (unchanged behaviour).
  - Rulings: 2.2 (no index; status on demand) Tasks 5, 8; 2.3 (plumbing, temporary index, CAS, lock, validation) Tasks 2, 7, 8; 2.4 (pin resolved in this server's catalog, `catalog.url` from public URL) Tasks 4, 7; 2.6 (author `Name <email>`) Tasks 1, 9 — the HTTP header itself is 2b; 2.9 (git ≥ 2.38) README in Task 9; 2.11 (Markdown book) Task 6; 2.13 (initial commit, `--public-url`, empty catalog fails) Tasks 7, 9; 2.14 (workspace-id, pin, answer) Tasks 3, 4, 10, resolving 1.11; 2.15 (`validate` unchanged) — no task touches it; 2.16 (older pins allowed) Task 4 `TestWorkspaceUpdatePin`; 2.17 (store owns layout, hooks, writes) Tasks 7, 9.
  - Not in 2a (other plans): blobs, REST API and `X-Custos-Author` (2b), distribution, freeze and pin proposals (2c, which uses `OnCatalogPush` and `Refs`), fork and merge (2d, which uses `CreateWorkspaceRepo`, `SetRef`, `CommitTree`).
- **Exported names** match the architecture note's "Plan 2a" list. Additional exported names, all within 2a's packages: `gitrepo.Repo.InheritGitEnv`, `workspace.ConfigPath`, `workspace.MarshalConfig`, `store.Store.CheckWorkspace`, `fixture.Config`, `fixture.PinnedWorkspace`.
- **Type consistency:** `Signature`, `Change`, `CommitRequest`, `ScriptOptions`, `TaskStatus`, `Entry`, `Status` are used with the same fields in every task; `hook.Script` is called with `(exe, kind)` in Task 7 and switched to `(exe, ScriptOptions)` in Task 10 together with its only caller `store.installHook`.
- **Placeholders:** none; every code step holds complete files or exact replacements, and every code block was compiled and tested against the phase-1 code with git 2.55 (including the state after Task 9).
- **Review Focus:** each of the five items has its test in the owning task (Tasks 1, 5, 7, 8, 9, 10).

## Decisions beyond the architecture note

- `gitrepo.Repo` gains `InheritGitEnv`; every other `Repo` drops `GIT_DIR`, `GIT_OBJECT_DIRECTORY`, `GIT_ALTERNATE_OBJECT_DIRECTORIES`, `GIT_QUARANTINE_PATH`, `GIT_INDEX_FILE` and similar — a workspace hook reading the catalog would otherwise look in the pushed workspace's quarantine — cost if wrong: a hook-side `Repo` that forgets the flag cannot see the pushed objects (the server push tests catch it).
- `ResolveRef` is `git rev-parse --verify --quiet`, so a full object id resolves even when the object is missing; callers append `^{commit}` to check existence — matches git's semantics and keeps one function — cost: a caller that forgets the suffix trusts a missing object.
- `UpdateRef`/`DeleteRef` detect `ErrRefMoved` by re-reading the ref after `update-ref` fails, not by parsing its output — git's messages are localized — cost: in a tiny window a second concurrent move can make an unrelated failure look like a conflict.
- `WriteCommit` writes regular `100644` files only, rejects `.git`, `..` and absolute paths itself, and lets a file replace a directory of the same name — `update-index --index-info` silently ignores bad paths — cost: no executable bits or symlinks can be written (custos rejects symlinks anyway).
- `CommitTree` passes `--no-gpg-sign` — a host `commit.gpgSign` would otherwise break server writes — cost: none until custos signs its own commits (§6.4 option).
- `ParseSignature` also rejects whitespace inside the email — git would store it oddly — cost: such addresses must be fixed by the user.
- `InEffect` for an older own answer leaves out merged answers that the own answer's version already reaches through `previous` — spec §3.3 says the answer to the merging version replaces both — cost: none known; the note's comment ("own older answer plus Merged") is read narrowly.
- `Merged` collects superseded tasks transitively (merges of merges), sorted by id — they are all "previous tasks" of the current version — cost: long merge chains list many reference answers.
- Generated tasks whose parent was merged follow the merging task; generated tasks with an unknown parent or an origin cycle go under "Ungrouped" after the ungrouped catalog tasks — the note only places catalog tasks there — cost: an orphaned generated task is shown far from its context.
- Book depth: a task's depth is its group's depth + 1, a generated task's its parent's + 1; Markdown headings are level depth + 2 (at most 6) under `# Workspace <id>` — the note fixes only "0 = top-level group" — cost: very deep trees flatten at level 6.
- `Compute` skips tasks without a single current version and answer files whose `task` does not match their path — such trees fail validation anyway — cost: they vanish from status instead of showing an error.
- Invalid workspace id in `CreateWorkspace`/`CreateWorkspaceRepo` → `*RejectedError` with rule `workspace-id`; empty catalog → error wrapping `ErrConflict` — gives 2b's `WriteError` 422 and 409 without new error values — cost: 2b must keep that mapping.
- `CreateWorkspace` validates its initial commit with `rules.CheckWorkspaceUpdate` and removes the new repository on any failure — same rules for every `main` update, no half-created workspaces — cost: one extra tree read per creation.
- `CreateWorkspaceRepo` does not take the workspace lock and reserves the directory with `os.Mkdir` — 2d may already hold the lock, and `Mkdir` makes `ErrExists` atomic — cost: callers must lock themselves when they need to.
- `UpdateWorkspace` drops changes that leave a file as it is, only accepts `refs/heads/…`, and returns `""` without creating anything for a no-op on a missing branch — "no changes" should not depend on how the edit phrases them — cost: none known.
- `Store.Load` ignores load problems and fails when the pin is not in the catalog; new `Store.CheckWorkspace` and a start-up report in `serve` cover §7 — status needs no problem list, the dashboard comes later — cost: start-up time grows with the number of workspaces.
- `OnCatalogPush` callbacks run synchronously after every `POST /git/catalog.git/git-receive-pack`, accepted or rejected, before the response ends — deterministic for tests, and reconcile (2c) is idempotent — cost: a slow reconcile delays the catalog pusher.
- `store.New` makes the data directory absolute, and `hook.Script` rejects a relative `CatalogDir` — hooks run with the repository as working directory — cost: none.
- `WorkspaceIDs` ignores directories not named `<uuid>.git`, so they get no hook — only custos creates workspaces — cost: a repository made by hand under another name is served without validation.
- `--public-url` must be an absolute `http`/`https` URL without query; its default does not follow `--addr` — ruling 2.13 fixes the default — cost: users changing `--addr` must also set `--public-url` (README says so).
- `workspace.ConfigPath` is exported and `workspace.MarshalConfig` added; test helpers `fixture.Config` and `fixture.PinnedWorkspace` added — the store and rules need the path and the encoder — cost: none.
