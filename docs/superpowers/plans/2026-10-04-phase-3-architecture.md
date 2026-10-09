# Phase 3 (Processors): shared architecture for plans 3a–3e

This note fixes the packages, exported names, formats and conventions that the five phase-3 plans share, so they can be written separately and still fit together. Each plan implements its part exactly as named here; anything not named here is the plan's own business and goes into its "Decisions beyond the architecture note" section.

- Spec: [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md). Phase 3 = §9 item 3. Read §3.2, §3.3 (generated tasks, documents), §4.3, §4.4 (rerun on merge), §5 (all), §6.2, §7, §8, and **§11 (rulings), which overrides earlier sections** — 2.2 (no index), 2.3 (writes through the store), 2.6 (`X-Custos-Author`), 2.7 (one blob store), 2.10 (replaced by this phase), 2.53 (distribution after pushes and API merges).
- ADR: [docs/adr/0001-in-toto-library.md](../../adr/0001-in-toto-library.md) — verification uses the carabiner-dev libraries behind an interface. The phase-0 code it describes was removed in `244f9de` ("restart project"); `git show 244f9de^:internal/attest/attest.go` and `244f9de^:internal/attest/carabiner/carabiner.go` (and its `eval_test.go`, `testdata/`) show working calls to reuse.
- Phases 1 and 2 are on `main` (README.md is the user view). Existing packages: `frontmatter`, `semver`, `problem`, `repofile`, `task`, `catalog`, `workspace`, `gitrepo`, `gittest`, `fixture`, `hook`, `rules`, `status`, `store`, `server`, `blobs`, `api`, `remote`, `distribute`, `merge`, and `cmd/custos`. Read the code for exact signatures; the phase-2 note [2026-10-03-phase-2-architecture.md](2026-10-03-phase-2-architecture.md) summarises them, and §11.2 rulings 2.42–2.56 record what changed during execution (notably `merge.Register(a, st, onMainMoved)`, `server.OnWorkspacePush`, `distribute.ReconcileWorkspace`).
- Plans run in order 3a → 3b → 3c → 3d → 3e. A later plan may rely on everything an earlier plan produces as listed here.

## Conventions (all plans)

- Go 1.26, module `github.com/emeland-io/custos`. Third-party modules: the existing `go.yaml.in/yaml/v3`, `golang.org/x/mod`, `github.com/google/uuid`, plus — **only in `internal/attest/carabiner`, added by 3b** — `github.com/carabiner-dev/collector` and `github.com/carabiner-dev/signer` (ADR 0001; use current versions; the `go` directive may rise if they require it). Nothing else. Standard library otherwise.
- The Go SDK is a separate module `github.com/emeland-io/custos/sdk/go` in directory `sdk/go` (3e), standard library only, so processor authors do not pull custos's dependencies. The Python SDK (3e) uses the Python standard library only (Python ≥ 3.10).
- Git ≥ 2.38 at runtime (ruling 2.9). Tests use real git in temp dirs through `internal/gittest`.
- **Containers.** Processors run through the `docker` or `podman` command-line client via `os/exec` (no Docker SDK). Flag `--container-runtime` (`CUSTOS_CONTAINER_RUNTIME`, default `docker`).
- **Tests use real Docker** (decision of the project owner): every test that runs a processor runs a real container. There is no fake runner and no skip when Docker is missing — such tests fail. Test images are built from `FROM scratch` plus a static Linux binary (`CGO_ENABLED=0 GOOS=linux GOARCH=<runtime.GOARCH>`), so building them needs no network. `internal/proctest` (3a) builds them.
- Problems: `problem.Problem{Path, Rule, Message}`. REST conventions as in phase 2: JSON, snake_case fields, error body `{"error", "problems"?}`, status codes 400/401/404/409/413/422/500, every write endpoint requires `X-Custos-Author` (`api.Author`), errors through `api.WriteError`.
- Bot identity `gitrepo.Bot`; every server-side commit has committer Bot. Processor output commits (proposal branches, cascades) are authored by Bot; accepting a proposal is authored by the accepting person.
- All repository writes go through `store` (lock, validation of `main`, compare-and-swap; ruling 2.3). Hooks never write.
- Commit messages: one imperative sentence without prefix, ending with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Formats fixed for all plans

- **Proposal branch:** `custos/proposal/<task-uuid>/<digest-hex>` — `<task-uuid>` is the answered task (catalog or generated), `<digest-hex>` the 64 hex digits of the processor image digest (Git ref names cannot contain `:`). At most one open proposal per task: writing a new one deletes the others under `custos/proposal/<task-uuid>/`.
- **Cascade proposal branch:** `custos/proposal/<removed-task-uuid>/cascade` — proposes removing the generated tasks below a generated task whose removal was accepted (§5.5).
- **Removing a generated task** deletes all its files `generated/<uuid>/*.md` and its answer `answers/<uuid>.md` (answers stay in Git history, §5.5). Removing a document deletes `documents/<uuid>.json`.
- **"Produced earlier for the same answer"** (§5.4) = the generated tasks (current version per generated task id) and documents in the workspace's `main` whose `produced_by.task.id` equals the answered task's id.
- **Depth:** a catalog task has depth 0; a generated task has depth = depth of the task whose answer produced it (`produced_by.task.id`) + 1. A run on a task at depth d that outputs tasks would create depth d+1; when d+1 > max depth (default 8, flag `--max-generation-depth`, `CUSTOS_MAX_GENERATION_DEPTH`) the run fails (§5.5).
- **First version** of a new generated task is `1.0.0`; a changed task gets `semver.Bump(old, bump)` with `previous: [{id, old}]`. A generated task's content is unchanged when title, body, answer_type, choices, origin and processor are all equal (`bump` is not content).
- **Documents** compare on `attest.Result.Payload` (§5.4: payload, not envelope or signature bytes). A new document gets a fresh UUID v4 file name; a changed one keeps its file and is replaced.
- **`produced_by`** of every written item: `{task: {id, version: answer's task_version}, processor: <name>, digest: "sha256:<hex>", answer_commit: <main commit the run read the answer from>}`.
- **Run records** (ruling to be recorded: files in the data dir, not Git): `<data-dir>/runs/<workspace-id>/<run-id>.json` and `<run-id>.log`, run id = UUID v4.

## Plan 3a — contract, runner, test images

### `internal/contract` (new)

```go
const Version = "custos.processor/v1"
const MaxOutputSize = 16 << 20 // stdout above this fails the run

type Input struct {
	Contract  string         `json:"contract"`  // Version
	Workspace InputWorkspace `json:"workspace"`
	Task      InputTask      `json:"task"`
	Answer    InputAnswer    `json:"answer"`
}
type InputWorkspace struct{ ID string `json:"id"` }
type InputTask struct {
	ID         string          `json:"id"`
	Version    string          `json:"version"`
	Title      string          `json:"title"`
	Body       string          `json:"body"`
	AnswerType task.AnswerType `json:"answer_type"`
	Choices    []string        `json:"choices,omitempty"`
}
type InputAnswer struct {
	TaskVersion string            `json:"task_version"`
	Value       *string           `json:"value"` // null for markdown answers
	Body        string            `json:"body"`
	Attachments []InputAttachment `json:"attachments"` // never null
}
type InputAttachment struct {
	Name      string `json:"name"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type"`
	Path      string `json:"path"` // "/input/blobs/<sha256>"
}

type Output struct {
	Tasks     []OutputTask     `json:"tasks"`
	Documents []OutputDocument `json:"documents"`
}
type OutputTask struct {
	MatchKey   string          `json:"match_key"`
	Title      string          `json:"title"`
	Body       string          `json:"body"`
	AnswerType task.AnswerType `json:"answer_type"`
	Choices    []string        `json:"choices,omitempty"` // required for choice, forbidden otherwise
	Origin     *Origin         `json:"origin,omitempty"`
	Processor  string          `json:"processor,omitempty"`
	Bump       semver.Step     `json:"bump,omitempty"` // ParseOutput sets "minor" when empty
}
type Origin struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
}
type OutputDocument struct {
	MatchKey  string          `json:"match_key"`
	Name      string          `json:"name"`
	MediaType string          `json:"media_type"`
	Content   json.RawMessage `json:"content"` // any JSON value except null
}

// ParseOutput decodes stdout strictly (unknown fields, trailing data and
// sizes above MaxOutputSize are errors), applies defaults, and validates:
// match_key present and unique per kind, title present, answer_type valid,
// choices rules, origin id a UUID v4 and version a semver, processor name
// matching [a-z0-9][a-z0-9-]*, bump one of patch/minor/major, document
// name/media_type present, content present. All problems are reported in
// one error. Whether a named processor exists is checked by 3c, which has
// the catalog.
func ParseOutput(data []byte) (*Output, error)

// NewInput builds the input for one answer. path of attachment i is
// "/input/blobs/<sha256>".
func NewInput(workspaceID string, v *task.Version, a *workspace.Answer) Input
```

### `internal/runner` (new)

```go
type Config struct {
	Runtime        string        // "docker" (default) or "podman", or a path to the binary
	SecretsDir     string        // secret <name> is the file <SecretsDir>/<name>
	Memory         string        // container memory limit, default "512m"
	DefaultTimeout time.Duration // default 60s; used when Job.Timeout == 0
}

type Job struct {
	Image   string            // "<ref>@sha256:<hex>" as in processors.yaml, or a local image reference (processor test)
	Timeout time.Duration
	Network bool
	Secrets []string          // mounted read-only at /run/secrets/<name>
	Blobs   map[string]string // sha256 → host file; mounted read-only at /input/blobs/<sha256>
	Input   []byte            // stdin
}

type Result struct {
	Stdout   []byte        // at most contract.MaxOutputSize+1 bytes are kept
	Log      []byte        // stderr, at most 1 MiB kept, then "\n[log truncated]\n"
	ExitCode int
	TimedOut bool
	Duration time.Duration
}

var ErrUnavailable = errors.New("container could not be started") // wrapped with the reason

func New(cfg Config) *Runner
// Run starts the container with a read-only root filesystem, no network
// unless Job.Network, the memory limit, no capabilities, no new privileges,
// a small tmpfs at /tmp, and the mounts above; kills it when the timeout
// expires. A missing secret, an image that cannot be found or pulled, or a
// missing runtime are ErrUnavailable; a non-zero exit or timeout is reported
// in Result, not as an error.
func (r *Runner) Run(ctx context.Context, job Job) (*Result, error)

// Resolve returns the reference to run and the image digest
// "sha256:<hex>". For "<ref>@sha256:<hex>" it uses a local image whose ID is
// sha256:<hex> if one exists (locally built images have no registry
// digest), else the reference itself (pulled on first use), and the digest
// from the reference. For any other reference (processor test) it inspects
// the local image and returns its ID as digest.
func (r *Runner) Resolve(ctx context.Context, image string) (ref, digest string, err error)
```

### `internal/proctest` (new; a normal package used by tests of 3a–3e)

```go
// Image builds the named test image once per process and returns
// "custos.test/<name>@sha256:<image-id-hex>". Names: echo, generate, sign,
// fail, loop. Fails the test when Docker is unavailable.
func Image(t testing.TB, name string) string

// SigningKey writes a fresh ed25519 private key (PKCS#8 PEM) as secret
// "test-signing-key" into a new temp directory and returns that secrets
// directory and the matching public key (PKIX PEM).
func SigningKey(t testing.TB) (secretsDir string, publicKeyPEM []byte)
```

Image behaviour (programs in `internal/proctest/images/<name>/main.go`, reading the contract input from stdin; tests of later plans rely on it exactly):

- **echo** — outputs no tasks and one document `{"match_key":"input","name":"input","media_type":"application/json","content":<the input JSON object>}`.
- **generate** — reads the answer text (`value` if not null, else `body`), one directive per line, blank lines ignored:
  - `task <key> <title words…>` → task `{match_key: key, title, body: "Generated for <task.id>", answer_type: "text"}`
  - `choice <key> <title words…>` → like `task` with `answer_type: "choice"`, `choices: ["yes","no"]`
  - `bump <key> <patch|minor|major>`, `processor <key> <name>`, `origin <key> <uuid>` → set that field on the task with that key (a later line refers to an earlier `task`)
  - `doc <key> <subject>` → document `{match_key: key, name: key, media_type: "application/vnd.in-toto+json", content: <bare in-toto v1 statement with one subject {name: subject, digest: {sha256: <64 hex of sha256(subject)>}} and predicateType "https://example.org/custos-test/v1", predicate {}>}`
  - `stderr <words…>` → writes the words to stderr
  - `garbage` → writes `not json` to stdout instead of an output and exits 0
  - `exit <n>` → exits with code n after writing nothing
  - `sleep <duration>` → sleeps for the given Go duration (e.g. `3s`) before continuing; added in plan 3d's task 4 fix round to give tests deterministic control over how long a run stays "running," without relying on timing races (test infrastructure only, no production code depends on it)
- **sign** — like `generate` (same directives), but each `doc` is wrapped in a DSSE envelope (payloadType `application/vnd.in-toto+json`) signed with the ed25519 key in `/run/secrets/test-signing-key`; without that file it exits 2.
- **fail** — writes `boom` to stderr and exits 3.
- **loop** — sleeps until killed.

## Plan 3b — attestation verification

### `internal/attest` (new)

```go
const (
	StatusVerified = "verified" // signed by a trusted key or a valid Sigstore bundle
	StatusFailed   = "failed"   // signatures present but none verifies, or payload tampered
	StatusUnsigned = "unsigned" // bare statement, envelope without signatures, or not an attestation
)

type Result struct {
	Status  string
	Signers []string // identities of verified signers, sorted; empty unless verified
	Payload []byte   // canonical JSON of the statement (or of the content when it is not an attestation); documents compare on it (§5.4)
}

type Verifier interface {
	Verify(content []byte) Result // never fails: unreadable input is Unsigned with Payload = canonical JSON of content
}

// Canonical re-encodes JSON with sorted object keys and no insignificant
// whitespace (numbers kept as written). Error on invalid JSON.
func Canonical(data []byte) ([]byte, error)

// Unverified is a Verifier that never checks signatures: Status is
// "unsigned" for everything, Payload as above. processor test uses it when no
// trusted keys are given.
var Unverified Verifier
```

### `internal/attest/carabiner` (new; the only importer of carabiner-dev)

```go
// New loads every *.pem / *.pub public key in keysDir ("" = none) and
// returns a Verifier. Sigstore bundles verify offline with the embedded
// trust root (ADR 0001 findings 2 and 4); one signer.Verifier per process.
func New(keysDir string) (*Verifier, error)
func (v *Verifier) Verify(content []byte) attest.Result
```

3b's tests include a DSSE envelope signed by `proctest`'s `SigningKey` (verified with that key, failed with another, unsigned without signatures), a bare statement, a tampered payload, and the Sigstore bundle test fixture from `244f9de^:internal/attest/carabiner/testdata/`.

Flag for `serve` (wired in 3d): `--trusted-keys DIR` (`CUSTOS_TRUSTED_KEYS`).

## Plan 3c — matching and output proposals

### `internal/match` (new; pure, no git)

```go
var ErrTooDeep = errors.New("output exceeds the maximum depth of generated tasks")

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

type Action string
const (
	Added      Action = "added"
	NewVersion Action = "new-version"
	Unchanged  Action = "unchanged"
	Removed    Action = "removed"
)

type Item struct {
	Kind         string // "task" | "document"
	MatchKey     string
	Action       Action
	ID           string // generated task id or document file uuid
	From, To     string // task versions (From empty when added, To empty when removed)
	Title        string // task title or document name
	Verification *workspace.Verification // documents only, nil when removed
}

type Changes struct {
	Items []Item           // sorted by Kind, then MatchKey; includes Unchanged
	Files []gitrepo.Change // what to write on a branch based on main (empty when nothing changed)
}

// Plan matches out against what the answer's task produced earlier (§5.4,
// formats above): checks that every named processor is registered in the
// catalog, that origin ids name a catalog task or a generated task of the
// workspace, and the depth limit (ErrTooDeep wrapped); verifies documents
// with Run.Verifier and stores the result in the document's verification
// field.
func Plan(run Run, out *contract.Output) (*Changes, error)

// Depth returns the depth of task id in w (formats above); -1 for an
// unknown id; cycles count as too deep.
func Depth(w *workspace.Workspace, id string) int

// Below returns the generated task ids whose produced_by.task.id is id,
// transitively, sorted (cascading removal).
func Below(w *workspace.Workspace, id string) []string
```

### `internal/proposal` (new; repository side)

```go
const BranchPrefix = "custos/proposal/"
func Branch(taskID, digest string) string // digest "sha256:<hex>" → "custos/proposal/<task>/<hex>"

type Proposal struct {
	Branch string // short name, e.g. custos/proposal/<task>/<hex>
	Commit string
	Task   string // answered task id (or removed task id for a cascade)
	Digest string // "sha256:<hex>", "" for a cascade
	Cascade bool
	Items  []match.Item // recomputed from the branch against current main; Unchanged included
}

// Write puts ch.Files on branch Branch(taskID, digest) as one commit by Bot
// based on the current main, replacing the branch if it exists, and deletes
// other open proposals of taskID. No files → deletes open proposals of
// taskID and returns "".
func Write(st *store.Store, wsID, taskID, digest string, ch *match.Changes, message string) (branch string, err error)

func List(st *store.Store, wsID string) ([]Proposal, error)
func Get(st *store.Store, wsID, taskID string) (*Proposal, error) // store.ErrNotFound when none is open

// Accept applies the selected items ("task:<match_key>" / "document:<match_key>";
// nil = all that are not Unchanged) of the open proposal of taskID to the
// current main in one commit by author (validated, compare-and-swap; on
// store.ErrConflict it re-reads and retries up to three times), deletes the
// proposal branch, and opens a cascade proposal for every accepted removal
// of a generated task that has tasks below it (match.Below). An unknown
// selector is ErrInvalid (400); no open proposal is store.ErrNotFound (404).
func Accept(st *store.Store, wsID, taskID string, selected []string, author gitrepo.Signature) (commit string, err error)
func Reject(st *store.Store, wsID, taskID string) error // deletes the branch; store.ErrNotFound when none

var ErrInvalid = errors.New("invalid request") // unknown selector etc. → 400
```

The 3c plan settles how items are recomputed from a branch (e.g. diff of the branch's `generated/` and `documents/` against current main, mapped to match keys) and documents it.

## Plan 3d — runs, triggers, API, serve, merge reruns, dry run

### `internal/runs` (new)

```go
type State string
const (Queued State = "queued"; Running State = "running"; Succeeded State = "succeeded"; Failed State = "failed")

type Outcome string
const (Proposed Outcome = "proposed"; Unchanged Outcome = "unchanged"; None Outcome = "") // None while queued/running or when failed

type Record struct {
	ID           string    `json:"id"`
	Workspace    string    `json:"workspace"`
	Task         task.Ref  `json:"task"`         // answered task id and the answer's task_version
	AnswerPath   string    `json:"answer_path"`
	AnswerBlob   string    `json:"answer_blob"`  // git blob oid of the answer file
	AnswerCommit string    `json:"answer_commit"`
	Processor    string    `json:"processor"`
	Image        string    `json:"image"`
	Digest       string    `json:"digest"`
	Key          string    `json:"key"`          // hex sha256 of workspace, answer path, answer blob, digest — a key with a record is not run again by Scan
	Reason       string    `json:"reason"`       // "answer", "digest", "binding", "retry", "merge", "start-up"
	RetryOf      string    `json:"retry_of,omitempty"`
	State        State     `json:"state"`
	Outcome      Outcome   `json:"outcome,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	Error        string    `json:"error,omitempty"`
	Queued       time.Time `json:"queued"`
	Started      time.Time `json:"started,omitzero"`
	Finished     time.Time `json:"finished,omitzero"`
}

type Config struct {
	Workers  int // default 2
	MaxDepth int // default 8
}

func New(st *store.Store, bl *blobs.Store, rn *runner.Runner, v attest.Verifier, cfg Config) (*Service, error) // loads records; queued/running records from a previous process are queued again
func (s *Service) Start(ctx context.Context)            // starts the workers; returns at once
func (s *Service) Scan(wsID string)                     // non-blocking; coalesces; enqueues a run for every answer of a bound task (catalog binding at the pin, or generated task with a registered processor) whose Key has no record
func (s *Service) ScanAll()
func (s *Service) Rerun(wsID string, taskIDs []string, reason string) ([]Record, error) // forced, ignores Key (merge reruns)
func (s *Service) Retry(wsID, runID string) (*Record, error)                            // failed runs only
func (s *Service) Runs(wsID string) ([]Record, error)                                   // newest first
func (s *Service) Get(wsID, runID string) (*Record, []byte, error)                      // record and log
func (s *Service) Wait()                                                                 // tests: blocks until the queue is empty and no run is active
func Register(a *api.API, s *Service)
```

A run: load main at the time it starts (`store.Load`), build `contract.NewInput`, resolve and run the image (`runner`), `contract.ParseOutput`, `match.Plan`, `proposal.Write` with message "Propose output of <processor> for task <id>"; failures at any step make the record Failed with Error and the log kept.

### Triggers (wiring in 3d)

- `store` gains `func (s *Store) OnMainMoved(f func(id string))`: called after `UpdateWorkspace`, `SetRef` or `CreateWorkspace` moved a workspace's `main`, outside the lock. `serve` registers `Service.Scan` there (answers through the API, pin moves by distribution, accepted proposals, merges) and on `server.OnWorkspacePush` (pushes), and calls `ScanAll` at start-up after distribution.
- **Merge reruns (replaces ruling 2.10):** `merge.Merge` no longer reports conflicts on `generated/` and `documents/` paths; it takes `main`'s side for them and lists the producing task ids (`produced_by.task.id` from either side) in a new field `Result.Rerun []string`. `merge.Register` gains a fourth parameter `onRerun func(id string, tasks []string)`, called after a successful merge; `serve` passes `Service.Rerun(id, tasks, "merge")`.

### Endpoints (3d)

| Method and path | Purpose |
| --- | --- |
| `GET /api/processors` | registry at catalog `main`: `[{"name","image","digest","timeout","network","secrets","bound_tasks"}]` |
| `GET /api/workspaces/{id}/runs` | run records, newest first |
| `GET /api/workspaces/{id}/runs/{run}` | record |
| `GET /api/workspaces/{id}/runs/{run}/log` | `text/plain` log |
| `POST /api/workspaces/{id}/runs/{run}/retry` | 202 new record (failed runs only; 409 otherwise) |
| `GET /api/workspaces/{id}/processor-proposals` | open output proposals with items |
| `GET /api/workspaces/{id}/processor-proposals/{task}` | one proposal |
| `POST /api/workspaces/{id}/processor-proposals/{task}/accept` | body `{"items"?: ["task:<key>", …]}` → `{"commit"}` |
| `POST /api/workspaces/{id}/processor-proposals/{task}/reject` | → 204 |
| `POST /api/processors/{name}/dry-run` | body `{"image"}` (digest-pinned) → 202 `{"id"}` |
| `GET /api/dry-runs/{id}` | `{"state","runs","failed","proposals","workspaces","samples":[{"workspace","task","items"}]}` (≤ 5 samples); jobs kept in memory |

(`/api/workspaces/{id}/proposals` is taken by pin proposals from 2c.)

### Flags of `serve` (3d)

`--container-runtime` (`CUSTOS_CONTAINER_RUNTIME`, `docker`), `--secrets-dir` (`CUSTOS_SECRETS_DIR`, empty = no secrets available), `--processor-memory` (`CUSTOS_PROCESSOR_MEMORY`, `512m`), `--processor-workers` (`CUSTOS_PROCESSOR_WORKERS`, 2), `--max-generation-depth` (`CUSTOS_MAX_GENERATION_DEPTH`, 8), `--trusted-keys` (`CUSTOS_TRUSTED_KEYS`, empty = none).

## Plan 3e — SDKs, `custos processor test`, README

- **Go SDK** `sdk/go` (module `github.com/emeland-io/custos/sdk/go`, package `processor`): own copies of the contract types (`Task`, `Answer`, `Attachment`, `Output`, `OutputTask`, `OutputDocument`, `Origin`) with the same JSON names as `internal/contract`; `type Func func(task Task, answer Answer) (Output, error)`; `func Main(f Func)` reads and checks stdin (contract version), calls f, validates the output with the same rules as `contract.ParseOutput` (except processor existence), writes JSON to stdout, exits 1 with the error on stderr otherwise. Example processor `sdk/go/examples/hostlist`. Tests inside the module; the root module's tests build the example as an image and run it through `runner` (cross-module `go build` via `exec`).
- **Python SDK** `sdk/python/custos_processor/` (stdlib only): dataclasses mirroring the contract, `run(process)` with the same behaviour; tests with `unittest` (`python3 -m unittest discover -s sdk/python`).
- **`custos processor test <image> --answer FILE [--task FILE] [--previous DIR] [--secrets-dir DIR] [--network] [--timeout D] [--trusted-keys DIR] [--workspace-id UUID]`** (§5.7): runs the image locally through `runner` (`Resolve` for local images), with the answer file (workspace answer format), the task version file (default: a task synthesized from the answer: id from the answer, version = task_version, title "Test task", answer type from the answer), and `--previous` a directory laid out like a workspace (`generated/`, `documents/`) holding earlier output to match against. Prints the proposal items (`match.Plan`, with `attest.Unverified` unless `--trusted-keys`) as text, then the log; exit 1 when the run failed. Golden-file tests (`testdata/*.golden`, `-update` flag) using `proctest` images.
- `Makefile` `test` runs the SDK tests too; README sections for processors (registry, flags, endpoints) and for processor authors (contract, SDKs, `processor test`).

## Rulings this note already makes (to be recorded in spec §11.3)

1. Phase 3 is delivered as five plans 3a–3e, run in order.
2. Run records are files under `<data-dir>/runs/`, not Git and not an index (project owner's choice; consistent with 2.2). Deleting them makes custos run every bound answer again once.
3. A run is identified for "already done" by the key (workspace, answer path, answer blob, processor digest); scanning is idempotent and also covers answers pushed with plain Git, digest changes and binding changes (§4.3 triggers) without tracking events.
4. Processors run through the `docker`/`podman` CLI; tests always use real Docker.
5. `<ref>@sha256:<hex>` runs a local image whose ID is `sha256:<hex>` when present, so locally built images (tests, air-gapped hosts) work without a registry; otherwise the reference is pulled.
6. Proposal branch names carry the digest as 64 hex digits (`:` is not allowed in ref names).
7. Merge conflicts on generated tasks and documents take `main`'s side and trigger a forced rerun of the producing tasks; this replaces ruling 2.10.
8. Removing a generated task also removes its answer file from `main`; cascades are separate proposals on `custos/proposal/<task>/cascade`.
9. Output task items may carry `choices` (needed for `choice` tasks; §5.3 does not list it).
10. Dry-run jobs are kept in memory only; a restart forgets them.
11. The Go SDK is its own module so processors do not depend on custos's dependencies.
