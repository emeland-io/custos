# Phase 2 (Flows): shared architecture for plans 2a–2d

This note fixes the packages, exported names and conventions that the four phase-2 plans share, so they can be written separately and still fit together. Each plan implements its part exactly as named here; anything not named here is the plan's own business.

- Spec: [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md). Phase 2 = §9 item 2. Read §3, §4.1, §4.2, §4.4, §4.5, §6.2, §6.3, §7, and **§11 (rulings), which overrides earlier sections**.
- Phase 1 code is on `main` (see README.md for the user view). Existing packages: `frontmatter`, `semver`, `problem`, `repofile`, `task`, `catalog`, `workspace`, `gitrepo`, `gittest`, `fixture`, `hook`, `server`, and `cmd/custos`.
- Plans run in order 2a → 2b → 2c → 2d. A later plan may rely on everything an earlier plan produces as listed here.

## Conventions (all plans)

- Go 1.26, module `github.com/emeland-io/custos`. Third-party modules stay `go.yaml.in/yaml/v3`, `golang.org/x/mod`, `github.com/google/uuid`. Standard library otherwise (HTTP routing with Go 1.22+ `http.ServeMux` patterns).
- Git ≥ 2.38 at runtime (ruling 2.9). Tests use real git in temp dirs through `internal/gittest`.
- Problems: `problem.Problem{Path, Rule, Message}`; new rule constants (added by 2a in `internal/problem`): `RuleWorkspaceID = "workspace-id"`, `RulePin = "pin"`, `RuleAnswer = "answer"`.
- Bot identity for automatic commits: `gitrepo.Bot = gitrepo.Signature{Name: "custos-bot", Email: "custos-bot@localhost"}`. The committer of every server-side commit is `gitrepo.Bot`; the author is the acting person (or Bot for automatic changes).
- Branch names: pin proposals `custos/pin/<catalog-commit>` (2c). Draft/what-if branches are any other branch.
- REST: JSON bodies, `Content-Type: application/json`. Error body `{"error": "<message>", "problems": [{"path":…, "rule":…, "message":…}]}` (problems optional). Status codes: 400 malformed request, 401 missing/invalid `X-Custos-Author` on a write, 404 unknown workspace/task/blob/proposal, 409 conflict (ref moved, merge conflicts, workspace exists), 413 too large, 422 validation problems, 500 otherwise.
- Every write endpoint requires the header `X-Custos-Author: Name <email>` (ruling 2.6), parsed with `gitrepo.ParseSignature`.

## Plan 2a — store, catalog-aware checks, status

### `internal/gitrepo` (additions)

```go
type Signature struct{ Name, Email string }
func ParseSignature(s string) (Signature, error)      // "Jane Doe <jane@example.org>"; rejects empty name/email, '<' '>' or newlines inside
func (s Signature) String() string                    // "Jane Doe <jane@example.org>"
var Bot = Signature{Name: "custos-bot", Email: "custos-bot@localhost"}

var ErrRefMoved = errors.New("ref moved")             // UpdateRef/DeleteRef: the ref's value was not the expected one

type Change struct {
	Path   string // slash-separated, relative to the repository root
	Data   []byte
	Delete bool
}

type CommitRequest struct {
	Base    string   // commit whose tree is the starting point; "" = empty tree
	Parents []string // usually []string{Base}; nil for a root commit
	Changes []Change
	Author  Signature
	Message string
}

func (r *Repo) ResolveRef(ref string) (oid string, ok bool, err error) // ok=false when the ref does not exist
func (r *Repo) ReadFile(rev, path string) (data []byte, ok bool, err error)
func (r *Repo) WriteCommit(req CommitRequest) (string, error)           // writes objects only; committer = Bot; uses a temporary index file, never the repo's index or an inherited GIT_INDEX_FILE
func (r *Repo) CommitTree(tree string, parents []string, author Signature, message string) (string, error)
func (r *Repo) UpdateRef(ref, newOID, oldOID string) error              // oldOID "" = ref must not exist; ErrRefMoved on mismatch
func (r *Repo) DeleteRef(ref, oldOID string) error                      // ErrRefMoved on mismatch
func (r *Repo) Refs(prefix string) (map[string]string, error)           // full ref name → oid, e.g. prefix "refs/heads/custos/pin/"
```

### `internal/workspace` (addition)

```go
// CheckAgainstCatalog applies ruling 2.14 to answers: each answer names a task
// of the catalog or a generated task of w, a task_version that exists for it,
// the same answer type, and for choice tasks one of its choices.
func CheckAgainstCatalog(w *Workspace, c *catalog.Catalog) []problem.Problem
```

### `internal/rules` (new; the hook and the store share it)

```go
// CheckCatalogUpdate validates moving the catalog's main from oldOID ("" or
// all zeros = no previous main) to newOID: history (no delete, no rewrite),
// catalog.Check and catalog.CheckImmutable. newOID all zeros = delete.
func CheckCatalogUpdate(cat *gitrepo.Repo, oldOID, newOID string) ([]problem.Problem, error)

// CheckWorkspaceUpdate validates moving a workspace's main: history,
// workspace.Check, the workspace id in custos.yaml equals id, the pin is a
// commit on the catalog's main, and workspace.CheckAgainstCatalog against the
// catalog tree at the pin.
func CheckWorkspaceUpdate(ws, cat *gitrepo.Repo, id, oldOID, newOID string) ([]problem.Problem, error)
```

`hook.PreReceive` delegates to these. The workspace hook script gains the catalog location and the workspace id:

```go
type ScriptOptions struct {
	Kind        Kind
	CatalogDir  string // absolute path of catalog.git; workspace hooks only
	WorkspaceID string // workspace hooks only
}
func Script(exe string, opts ScriptOptions) (string, error)
// generated command: '<exe>' hook pre-receive --kind workspace --catalog '<dir>' --workspace <id>
func PreReceive(repo *gitrepo.Repo, opts ScriptOptions, updates io.Reader) ([]problem.Problem, error)
```

### `internal/status` (new)

```go
type State string
const (
	Unanswered    State = "unanswered"
	Answered      State = "answered"       // answer exists for the current version
	PendingUpdate State = "pending-update" // only answers for older versions, or answers of merged tasks, are in effect
)

type TaskStatus struct {
	ID         string
	Current    task.Ref            // current version
	Title      string
	AnswerType task.AnswerType
	Generated  bool
	State      State
	Answer     *workspace.Answer   // this task's own answer file, any version; nil if none
	Merged     []*workspace.Answer // answers of tasks merged into this one (superseded tasks), for reference, always listed
	InEffect   []*workspace.Answer // what the book shows: own answer at the current version; otherwise own older answer plus Merged
}

type Entry struct {   // one line of the book, in order
	Depth int           // 0 = top-level group
	Group *catalog.Group // heading entry when non-nil
	Task  *TaskStatus    // task entry when non-nil
}

type Status struct {
	Workspace string
	Pin       string        // catalog commit
	Tasks     []*TaskStatus // book order
	Book      []Entry
}

func Compute(w *workspace.Workspace, c *catalog.Catalog) *Status
func RenderMarkdown(s *Status) []byte
```

Book order: groups depth-first from `groups/index.yaml`, children in listed order; a generated task follows the task named by its `origin`, or else the task whose answer produced it (`produced_by.task.id`); current, non-superseded catalog tasks that no group lists come last under a heading "Ungrouped" (Group with Slug "", Title "Ungrouped"), in ID order.

### `internal/store` (new; ruling 2.17)

```go
var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	ErrConflict = errors.New("conflict")  // wraps gitrepo.ErrRefMoved and similar
)

// RejectedError reports validation problems of a write.
type RejectedError struct{ Problems []problem.Problem }
func (e *RejectedError) Error() string

type Store struct{ /* dataDir, exe, publicURL, per-repo mutexes */ }

func New(dataDir, exe, publicURL string) *Store       // no I/O
func Open(dataDir, exe, publicURL string) (*Store, error) // creates catalog.git if missing; does not touch hooks
func (s *Store) InstallHooks() error                   // rewrites the hooks of the catalog and all workspaces (serve calls it at start)
func (s *Store) DataDir() string
func (s *Store) CatalogRepo() *gitrepo.Repo
func (s *Store) WorkspaceRepo(id string) (*gitrepo.Repo, error) // ErrNotFound
func (s *Store) WorkspaceIDs() ([]string, error)       // sorted
func (s *Store) CatalogURL() string                    // publicURL + "/git/catalog.git"

// CreateWorkspace creates the bare repository with its hook and an initial
// commit on main whose custos.yaml pins the current catalog main (ruling 2.13).
// ErrExists if it exists; error if the catalog has no main.
func (s *Store) CreateWorkspace(id string, author gitrepo.Signature) error

// CreateWorkspaceRepo creates an empty bare repository with its hook (used by fork in 2d).
func (s *Store) CreateWorkspaceRepo(id string) (*gitrepo.Repo, error)

// Lock serialises writes to one repository ("catalog" or a workspace id). It returns the unlock function.
func (s *Store) Lock(repo string) func()

// UpdateWorkspace applies edit to the tree at ref ("refs/heads/main" or another
// branch; the ref may not exist yet for branches, then the base is main) under
// the workspace's lock. No changes → no commit, returns the current oid.
// Updates of main are validated with rules.CheckWorkspaceUpdate (RejectedError).
// The ref is moved with the expected old value (ErrConflict when it moved).
func (s *Store) UpdateWorkspace(id, ref string, author gitrepo.Signature, message string,
	edit func(tree fs.FS) ([]gitrepo.Change, error)) (string, error)

// SetRef moves ref of a workspace to an existing commit (fast-forward or merge
// result computed by the caller) with the same lock, validation (main only) and
// compare-and-swap. oldOID "" = ref must not exist.
func (s *Store) SetRef(id, ref, newOID, oldOID string) error

// Load reads a workspace at rev ("" = main) and the catalog at its pin.
func (s *Store) Load(id, rev string) (*workspace.Workspace, *catalog.Catalog, error)

// Status computes status.Compute for the workspace's main.
func (s *Store) Status(id string) (*status.Status, error)
```

Hook installation, `CreateWorkspace` and the repository layout move here from `internal/server`; `server` keeps HTTP only:

```go
// package server
func New(st *store.Store) *Server
func (s *Server) Handler() (http.Handler, error)        // /git/, /healthz (2a); /api/ added in 2b
func (s *Server) OnCatalogPush(f func())                // called after each request to /git/catalog.git/git-receive-pack completes (2a adds the hook point, 2c uses it)
```

CLI in 2a: `serve` and `workspace create` use `store` (`Open` + `InstallHooks` for serve; `New` + `CreateWorkspace` for create), both gain `--public-url` (`CUSTOS_PUBLIC_URL`, default `http://127.0.0.1:8080`); `workspace create` needs `--author "Name <email>"` (`CUSTOS_AUTHOR`).

## Plan 2b — answers, blobs, REST API, clone/push

### `internal/blobs` (new; rulings 2.7, 2.8)

```go
const MaxSize = 1 << 30
var ErrTooLarge = errors.New("blob too large")
type Store struct{ /* dir */ }
func Open(dir string) (*Store, error)                          // dir = <data-dir>/blobs; files at <dir>/sha256/<hash>
func (s *Store) Put(r io.Reader) (sha string, size int64, err error) // atomic (temp file + rename), idempotent
func (s *Store) Open(sha string) (io.ReadCloser, int64, error)   // ErrNotFound-like error via errors.Is(err, fs.ErrNotExist)
func (s *Store) Has(sha string) bool
```

### `internal/api` (new)

```go
type API struct{ /* store, blobs, mux */ }
func New(st *store.Store, bl *blobs.Store) *API
func (a *API) Handler() http.Handler   // mounted by server at /api/
// helpers later plans use for their endpoints:
func (a *API) Handle(pattern string, h http.HandlerFunc) // registers e.g. "POST /api/workspaces/{id}/freeze"
func Author(r *http.Request) (gitrepo.Signature, error)  // from X-Custos-Author
func WriteJSON(w http.ResponseWriter, status int, v any)
func WriteError(w http.ResponseWriter, err error)        // maps store.ErrNotFound/ErrExists/ErrConflict/*RejectedError/blobs.ErrTooLarge to the status codes above
```

Server: `server.New(st)` gains `WithAPI(h http.Handler)` (or the plan's equivalent) to mount `/api/`.

Endpoints in 2b:

| Method and path | Purpose |
| --- | --- |
| `GET /api/workspaces` | `[{"id","pin","frozen"}]` |
| `POST /api/workspaces` | body `{"id"?}` (generated when missing) → 201 `{"id"}`; uses `store.CreateWorkspace` |
| `GET /api/workspaces/{id}/status` | status JSON (tasks with state, answers in effect, merged answers) |
| `GET /api/workspaces/{id}/book` | `text/markdown`, `status.RenderMarkdown` |
| `GET /api/workspaces/{id}/answers/{task}` | answer JSON |
| `PUT /api/workspaces/{id}/answers/{task}` | body `{"task_version"? (default current), "value"?, "body"?, "attachments"?}`; validated against the task; one commit on main (§4.2) |
| `POST /api/workspaces/{id}/answers/{task}/still-valid` | rewrites the answer for the current version (§4.2) |
| `POST /api/blobs` | raw body → `{"sha256","size"}` |
| `GET /api/blobs/{sha256}` | raw content |
| `GET /api/catalog` | groups tree and current task versions at catalog main |
| `GET /api/catalog/tasks/{id}` | all versions of a task with previous links |

JSON field names are snake_case, matching the file formats (`task_version`, `media_type`, `answer_type`).

CLI in 2b (§6.2): `custos clone URL DIR` (git clone, then download attachments referenced by answers on the checked-out main from `<server>/api/blobs/` into `DIR/.custos/blobs/sha256/`), `custos push` (upload attachments referenced in commits not yet on the remote and missing from the server, then `git push`). The server base URL is derived from the clone URL by cutting `/git/...`.

## Plan 2c — catalog distribution, freeze, pin proposals (§4.1)

```go
// package distribute
// Reconcile brings every workspace in line with the catalog's main: an unfrozen
// workspace whose pin differs gets a commit by gitrepo.Bot that moves the pin;
// a frozen one gets (or keeps) exactly one branch custos/pin/<catalog-main>,
// older custos/pin/* branches are deleted. Idempotent; errors of one workspace
// do not stop the others (returned joined).
func Reconcile(st *store.Store) error
func Freeze(st *store.Store, id string, author gitrepo.Signature) error   // commit setting frozen: true
func Unfreeze(st *store.Store, id string, author gitrepo.Signature) error // commit setting frozen: false, then reconcile that workspace
type PinProposal struct{ Branch, Commit, From, To string; Changes Diff }
func Proposals(st *store.Store, id string) ([]PinProposal, error)
func Accept(st *store.Store, id, branch string, author gitrepo.Signature) error // fast-forward or merge commit into main; validated
func Reject(st *store.Store, id, branch string) error                            // deletes the branch
type Diff struct{ NewVersions, Added, Superseded []task.Ref; GroupsChanged, BindingsChanged bool }
func CatalogDiff(cat *gitrepo.Repo, from, to string) (Diff, error)
```

Wiring: `serve` calls `distribute.Reconcile` after `InstallHooks` and registers it with `server.OnCatalogPush`. Endpoints: `POST /api/workspaces/{id}/freeze`, `POST /api/workspaces/{id}/unfreeze`, `GET /api/workspaces/{id}/proposals`, `POST /api/workspaces/{id}/proposals/accept` and `/reject` with body `{"branch"}`, `GET /api/workspaces/{id}/pin-diff` (pinned vs catalog main).

## Plan 2d — fork and merge (§4.4)

```go
// package merge
func Fork(st *store.Store, srcID, newID string, author gitrepo.Signature) error
// creates the new repository (store.CreateWorkspaceRepo), copies main's history,
// and commits custos.yaml with the new workspace id. Blobs need no copy (ruling 2.7).

type Resolution struct {
	Side    string // "ours" | "theirs" | "content"
	Content []byte // when Side == "content"
}
type Conflict struct {
	Path          string
	Ours, Theirs  []byte // nil when the side deleted the file
	Kind          string // "answer" | "generated" | "document" | "custos.yaml" | "other"
}
type Result struct {
	Commit    string     // merge commit on main when merged
	Conflicts []Conflict // non-empty → nothing changed
}
// Merge merges branch into the workspace's main with git merge-tree --write-tree.
// custos.yaml conflicts on the pin resolve to the descendant catalog commit
// (or stay a conflict when neither is an ancestor); other conflicts need a
// resolution per path. The result is validated like any main update.
func Merge(st *store.Store, id, branch string, author gitrepo.Signature, res map[string]Resolution) (*Result, error)
```

Endpoints: `POST /api/workspaces/{id}/fork` body `{"id"?}` → 201 `{"id"}`; `POST /api/workspaces/{id}/merge` body `{"branch", "resolutions": {"<path>": {"side", "content"?}}}` → 200 `{"commit"}` or 409 `{"error", "conflicts": [...]}` (contents base64 in JSON). `GET /api/workspaces/{id}/branches`.
