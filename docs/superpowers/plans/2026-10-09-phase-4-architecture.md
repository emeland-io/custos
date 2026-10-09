# Phase 4 (UI and authentication): shared architecture for plans 4a-4d

This note fixes the packages, exported names, formats and conventions that the
four phase-4 plans share, so they can be written separately and still fit
together. Each plan implements its part exactly as named here; anything not
named here is the plan's own business and goes into its "Decisions beyond the
architecture note" section.

- Spec: [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md).
  Phase 4 = §9 item 4. Read §6.1 (web UI), §6.4 (authentication and roles),
  §7, §8, and **§11 (rulings), which overrides earlier sections** — 2.2 (no
  index), 2.6 (`X-Custos-Author`, replaced by this phase), 2.7 (one blob
  store), 2.11 (book is Markdown only until the web UI — this phase adds
  HTML), 2.12 (catalog API is read-only until the web UI — this phase adds
  editing).
- Phases 1-3 are on `main` (not yet merged: branch `worktree-phase-3`,
  commit `b4827da`). Existing packages relevant here: `api`, `store`,
  `server`, `catalog`, `task`, `workspace`, `status`, `merge`, `distribute`,
  `runs`, `gitrepo`, `problem`, `blobs`. Read the code for exact signatures;
  this note only fixes what phase 4 adds or changes.
- Plans run in order 4a → 4b → 4c → 4d. A later plan may rely on everything
  an earlier plan produces as listed here.

## Conventions (all plans)

- Go 1.26, module `github.com/emeland-io/custos`. New third-party modules,
  **only where named below**: `github.com/coreos/go-oidc/v3` (already an
  indirect dependency at v3.20.0 via the sigstore toolchain — promote it to
  a direct `require`) and `golang.org/x/oauth2` (already indirect at
  v0.36.0 — promote it too). Nothing else for the backend. Standard
  library otherwise (sessions and tokens are hand-rolled, not a third-party
  session library — ruling 4.1).
- **Frontend toolchain** (new in this phase): directory `web/`, a separate
  npm project — Vue 3 (`<script setup>`, TypeScript), Vite, vue-router,
  Pinia (session/auth store), Tailwind CSS. Node ≥ 20, npm as the package
  manager; `web/package-lock.json` is committed. `web/node_modules/` and
  `web/dist/*` are already gitignored except `web/dist/.gitkeep` (see
  `.gitignore`, present since the project's first commit) — this fixes the
  directory names: the frontend project root is `web/`, its build output is
  `web/dist/`.
- **Go embeds the built frontend.** New package `internal/webui` holds
  `//go:embed dist` over a copy of `web/dist` (see plan 4c for exactly how
  the build output reaches `internal/webui/dist` — a `go:generate` step or
  Makefile copy; `go:embed` cannot reach outside its own module directory
  with `..`). Because `go build`/`go test` must work without Node installed,
  `internal/webui/dist/` is **committed to Git** with a placeholder
  `index.html` (a one-line "custos UI not built" page) plus `.gitkeep`;
  `make build` and `make test` overwrite it with the real build before
  compiling (ruling 4.2). `go vet ./...` / `go test ./...` alone (no Make)
  therefore embed whatever is currently on disk, which is fine for
  non-UI packages.
- **`make test` now also needs Node.js and npm on `PATH`**, in addition to
  Docker and python3 (ruling 3.80 extended by 4.3): it runs
  `cd web && npm ci && npm run type-check` (spec §8 "frontend type checks")
  before the Go tests. **No frontend component/unit test runner is added in
  this phase** — type-checking (`vue-tsc --noEmit` via `npm run type-check`)
  is the only automated frontend check; behaviour is verified by the Go API
  tests underneath it and by manually exercising the UI (ruling 4.4; keeps
  this already-large phase from also growing a second test pyramid).
- Problems, REST conventions, bot identity, store-only writes, commit
  message style: unchanged from phases 2-3 (see their architecture notes).
  Commit messages end with `Co-Authored-By: Claude Sonnet 5
  <noreply@anthropic.com>`.
- **`X-Custos-Author` is removed** (replaces ruling 2.6). `api.Author(r)`
  keeps its exact signature — `func Author(r *http.Request)
  (gitrepo.Signature, error)` — callers do not change, but its
  implementation now reads the authenticated principal that
  `auth.Middleware` put in the request's context (via `auth.Principal(r)`)
  instead of a client-supplied header, returning the same 401
  `*httpError` shape when there is none. Every existing call site in
  `internal/api`, `internal/merge/http.go` etc. keeps working unmodified.

## Plan 4a — authentication, roles, tokens

### `internal/auth` (new)

```go
// Principal is the authenticated identity of a request: either a human who
// signed in through OIDC, or the owner of a personal access token. Name and
// Email make a gitrepo.Signature for commits the person's actions cause.
type Principal struct {
	Subject string   // OIDC "sub", or "token:<token id>" for a PAT-only identity with no OIDC login on record
	Name    string
	Email   string
	Roles   Roles
}

func (p Principal) Signature() gitrepo.Signature { return gitrepo.Signature{Name: p.Name, Email: p.Email} }

// Roles. Engineer is scoped per workspace; the others are global. Admin
// implies every other role everywhere (§6.4: "admin: create and fork
// workspaces, manage roles").
type Roles struct {
	Admin          bool
	TaskAuthor     bool
	ProcessorAuthor bool
	Engineer       []string // workspace ids; "*" grants every workspace (ruling 4.9)
}
func (r Roles) Has(role string, workspaceID string) bool // role one of "admin","task-author","processor-author","engineer"; workspaceID ignored except for "engineer"; Admin always true

// Manager owns the identity store under <data-dir>/auth and issues/
// verifies sessions and tokens. One per process, created by cmd/custos.
type Config struct {
	DataDir        string
	SessionSecret  []byte        // HMAC-SHA256 key for session cookies; random if empty (ruling 4.5: sessions do not survive a restart unless the operator sets one)
	SessionTTL     time.Duration // default 12h
	AdminEmails    []string      // users whose email is in this list get Admin on every login/token use, in addition to whatever is on file (ruling 4.6: operator-controlled admin bootstrap, read fresh on every check — no server restart needed to add an admin)
	OIDC           *OIDCConfig   // nil disables OIDC login entirely (token-only mode, e.g. CI/tests)
}
type OIDCConfig struct {
	IssuerURL, ClientID, ClientSecret, RedirectURL string
}

func NewManager(cfg Config) (*Manager, error) // loads/creates <data-dir>/auth/users.json; builds an oidc.Provider from cfg.OIDC when set (fails fast if the issuer is unreachable)

// Middleware resolves the caller from the session cookie or an
// Authorization: Bearer <token> / Basic <token-as-password> header, and
// stores the Principal in the request context for Principal and Author to
// read. It never itself rejects an unauthenticated request — route-level
// checks (Require*, or api.Author's 401) do that — except that requests
// under excludedPrefixes (at least "/healthz", "/auth/login",
// "/auth/callback", and "/": the SPA shell must load before anyone is
// signed in) are passed through without even attempting resolution.
func (m *Manager) Middleware(excludedPrefixes ...string) func(http.Handler) http.Handler

// Principal returns the caller resolved by Middleware, or ok=false when
// the request carries no valid credential.
func Principal(r *http.Request) (Principal, bool)

// Require wraps next so it 401s (no credential) or 403s (credential present,
// role missing) before being called; role is one of the four above. For
// "engineer", r.PathValue(workspaceIDParam) (usually "id") is checked
// against the principal's workspace list.
func Require(role string, next http.HandlerFunc) http.HandlerFunc
func RequireWorkspace(workspaceIDParam string, next http.HandlerFunc) http.HandlerFunc // role "engineer" scoped to that param

// Register adds the OIDC and token endpoints below to the API's mux (full
// paths, not under /api/ — see the server wiring note). Call it before
// building the handler server.New wraps.
func Register(a *api.API, m *Manager)
```

Endpoints `Register` adds:

| Method and path | Purpose |
| --- | --- |
| `GET /auth/login` | 302 to the IdP (state + PKCE verifier in a short-lived cookie); 404 when OIDC is not configured |
| `GET /auth/callback` | exchanges the code, verifies the ID token, upserts the user's name/email into `users.json` (roles untouched), sets the session cookie, 302 to `/` |
| `POST /auth/logout` | clears the session cookie, 204 |
| `GET /auth/me` | `{"name","email","roles":{"admin","task_author","processor_author","engineer":[...]}}` for the caller, or 401 |
| `GET /auth/tokens` | the caller's own tokens (id, label, created, last_used — never the token value) |
| `POST /auth/tokens` | body `{"label"}` → `{"id","token"}`, token shown once, never stored in plaintext (ruling 4.7: `sha256(token)` is what's on disk; `/auth/tokens` list can never show it again) |
| `DELETE /auth/tokens/{id}` | revoke one of the caller's own tokens, 204 |
| `GET /api/admin/users` | **admin only** (`auth.Require("admin", ...)`): every user on file with their roles |
| `PUT /api/admin/users/{email}/roles` | **admin only**: body `{"admin"?,"task_author"?,"processor_author"?,"engineer"?:[...]}`, merges into that user's record (creating it if the email has never signed in), 200 with the resulting roles |

### Storage under `<data-dir>/auth` (ruling 4.8: files, not Git, not the
index — consistent with 2.2 and the run records of 3.2; identity and roles
are operational state, not content)

```
<data-dir>/auth/users.json           // map[email]Roles, one file, written with the same lock-and-rewrite pattern as runs
<data-dir>/auth/tokens/<token-id>.json  // {id, owner_email, label, sha256, created, last_used}
```

`token-id` is a UUID v4; the bearer value returned once at creation is
`token-id + "." + 32 random bytes, base64url` (so a token's id — used for
`DELETE /auth/tokens/{id}` and for logging "which token" without ever
logging the secret — can be read off the credential itself, the same way a
JWT's header is readable without verifying its signature). `sha256` on disk
hashes the whole bearer value, not just the random part.

### Git and API gated by the middleware

- `internal/server.Server` gains `func (s *Server) WithAuth(h http.Handler) *Server`
  (mounted at `/auth/`, same pattern as `WithAPI`) and
  `func (s *Server) WithMiddleware(mw func(http.Handler) http.Handler) *Server`,
  applied to the whole mux (`/git/`, `/auth/`, `/api/`, `/` together) **after**
  routing, so `excludedPrefixes` above still work ("/healthz" bypasses
  everything, "/" (the UI shell) is unauthenticated at the HTTP layer — the
  Vue router guards what it shows once `/auth/me` answers 401).
- `/git/...-receive-pack` (push) requires, inside the existing hook/handler
  path, the caller to have the role that write needs: `task-author` for
  `/git/catalog...`, `engineer` on that workspace id for
  `/git/workspaces/{id}...`. **Reading** (`info/refs`, `...-upload-pack`,
  clone) requires only *some* authenticated principal, no specific role
  (ruling 4.10 — this project is "a few hundred tasks" for one trusted
  organization per §1.20; read access is not further restricted). This
  check is a small addition inside `internal/server`'s existing
  `catalogPushPaths`/`workspacePushPath` matching, not a separate package.
- `cmd/custos serve` flags (convention: kebab-case flag, `CUSTOS_<UPPER_SNAKE>`
  env, usage ends with `(env CUSTOS_X)`): `--oidc-issuer-url`,
  `--oidc-client-id`, `--oidc-client-secret`, `--oidc-redirect-url`
  (default `<public-url>/auth/callback`), `--admin-emails` (comma-separated,
  `CUSTOS_ADMIN_EMAILS`), `--session-secret` (`CUSTOS_SESSION_SECRET`, empty
  = random). Leaving `--oidc-issuer-url` empty disables `/auth/login`
  entirely — `custos serve` then runs in **token-only mode** (ruling 4.11:
  needed for CI, phase-4's own tests, and air-gapped deployments without an
  IdP); an operator bootstraps the first admin and hands out tokens with the
  new offline CLI command below.

### CLI (new subcommand, same `flag.NewFlagSet` convention as `workspace.go`/`task.go`)

```
custos token create --data-dir DIR --email E --name "Name" [--admin] [--task-author] [--processor-author] [--engineer WORKSPACE-UUID,...]
```
Creates (or updates the roles of) the user directly in `<data-dir>/auth/users.json`
and prints a fresh personal access token for them to stdout once — this is
how the very first admin is bootstrapped, and how test fixtures and CI get a
token without a running server or a real IdP (ruling 4.12). No HTTP
involved; it opens the auth store the same way `custos workspace create`
opens the repo store.

### Tests

OIDC is tested against a real minimal OIDC provider run as an
`httptest.Server` inside the test binary (discovery document, JWKS,
token endpoint issuing a real signed ID token) — not a mocked
`oidc.Provider` — exercising `coreos/go-oidc`'s actual verification path
exactly as `custos serve` would use it (same empirical-testing norm as
phases 2-3: `internal/proctest` builds and runs real containers rather than
faking the runner). Session cookie tampering (wrong signature, expired,
wrong issuer) and token lookup (unknown id, revoked, wrong hash) each get a
direct unit test.

## Plan 4b — catalog editing API and book HTML export

### `internal/catalogdraft` (new; the catalog's analogue of workspace
fork/merge from `internal/merge`, but for the catalog repo, which has no
workspace id and where drafts are created and listed, not implicitly
forked per user)

```go
const BranchPrefix = "custos/draft/" // custos/draft/<draft-id>

type Draft struct {
	ID     string // UUID v4
	Branch string
	Title  string
	Author gitrepo.Signature // who created it
	Base   string            // main's commit when the draft was created
	Commit string            // current head of the branch
}

// Create starts a new draft from the catalog's current main.
func Create(st *store.Store, author gitrepo.Signature, title string) (*Draft, error)
func List(st *store.Store) ([]Draft, error)
func Get(st *store.Store, id string) (*Draft, error) // store.ErrNotFound
func Delete(st *store.Store, id string, author gitrepo.Signature) error

// Update applies edit to the draft's current tree and commits the result
// on its branch (author becomes the commit's Git author; committer is
// always gitrepo.Bot, same as every other store write). No validation
// beyond "does this still parse as YAML/frontmatter" runs here — a draft
// may be transiently invalid (ruling 4.13: Validate only gates Publish, so
// a task-author can save half-finished edits); edit gets the draft's tree,
// exactly like store.UpdateWorkspace's edit parameter.
func Update(st *store.Store, id string, author gitrepo.Signature, message string, edit func(tree fs.FS) ([]gitrepo.Change, error)) (string, error)

// Publish merges the draft into the catalog's main: a plain fast-forward
// when main has not moved past Base; otherwise a three-way merge identical
// in method to internal/merge.Merge (same Resolution/Conflict shapes,
// reused from that package — catalogdraft imports merge for them, not the
// other way around) with res supplying the caller's choices for any path
// both sides touched differently. A clean result is validated with
// catalog.Check(tree) before the compare-and-swap onto main; problems there
// come back as *store.RejectedError exactly like a rejected push, and the
// draft branch is left untouched so the author can keep editing. Deletes
// the draft branch on success.
func Publish(st *store.Store, id string, author gitrepo.Signature, res map[string]merge.Resolution) (*PublishResult, error)
type PublishResult struct {
	Commit    string
	Conflicts []merge.Conflict // non-empty → nothing changed, same contract as internal/merge.Merge
}
```

`store` gains the catalog-repo equivalent of `UpdateWorkspace`, used by
`catalogdraft.Update`/`Publish` and nothing else:

```go
// UpdateCatalog writes a commit on ref of the catalog repo with the given
// edit, under the catalog's own lock (store.Lock("catalog")). ref == the
// catalog's main ref additionally runs catalog.Check on the resulting tree
// (mirrors the pre-receive hook) before the compare-and-swap; any other
// ref (a draft branch) skips that check (ruling 4.13 above).
func (s *Store) UpdateCatalog(ref string, author gitrepo.Signature, message string, edit func(tree fs.FS) ([]gitrepo.Change, error)) (string, error)
```

### Task editing helpers used by the API (still `internal/catalog`, new file)

```go
// NewVersionChanges returns the gitrepo.Change that adds the next version
// file for id (current version bumped by step, previous set), for use
// inside a catalogdraft.Update edit callback — the API's in-draft analogue
// of the existing CLI-only catalog.NewVersion (which writes straight to a
// working-directory checkout and is unchanged; the two do not share code
// because one works on fs.FS content and the other on disk paths, but they
// compute the same next version/previous values).
func NewVersionChanges(fsys fs.FS, id string, step semver.Step, title, body string, answerType task.AnswerType, choices []string) ([]gitrepo.Change, error)
```

Group-tree edits (reorder, add/remove a child, rename a group) and
`processors.yaml` edits are plain file rewrites the API handlers build
themselves from the decoded request body with `go.yaml.in/yaml/v3` — no new
package needed; they call `catalogdraft.Update` with an `edit` closure that
reads the current `groups/index.yaml` / `groups/<slug>/group.yaml` /
`processors.yaml` from `tree`, applies the change, and re-marshals it.

### Endpoints (gated with `auth.Require("task-author", ...)`, registered by
a new `Register(a *api.API, st *store.Store) ` in `internal/catalogdraft/http.go`,
same call-it-before-`a.Handler()` convention as `merge.Register`)

| Method and path | Purpose |
| --- | --- |
| `GET /api/catalog/drafts` | list open drafts |
| `POST /api/catalog/drafts` | body `{"title"}` → the new `Draft` |
| `GET /api/catalog/drafts/{id}` | one draft: its branch's catalog, decoded with `catalog.Load`, plus `problems` from `catalog.Check` so the editor can show them live without blocking save |
| `PUT /api/catalog/drafts/{id}/tasks/{task-id}` | body `{"step":"patch\|minor\|major","title","body","answer_type","choices"}` → new version file via `NewVersionChanges` |
| `PUT /api/catalog/drafts/{id}/groups/{slug}` | body `{"title","children":[{"group"?,"task"?}]}` |
| `PUT /api/catalog/drafts/{id}/groups-index` | body `{"groups":["slug",...]}` (top-level order) |
| `PUT /api/catalog/drafts/{id}/processors` | body matching `catalog.Registry` |
| `DELETE /api/catalog/drafts/{id}` | discard |
| `POST /api/catalog/drafts/{id}/publish` | body `{"resolutions"?: {...}}` (same shape as `merge.Register`'s merge endpoint) → `{"commit"}` or 409 `conflictBody` |

### Book HTML export (replaces part of ruling 2.11: Markdown stays; HTML is added)

`internal/status` gains, alongside `RenderMarkdown`:

```go
// RenderHTML renders the same book as RenderMarkdown, as a complete,
// self-contained HTML document (inline <style>, no external requests) —
// goldmark is added as the one new dependency for Markdown→HTML, confined
// to this function (ruling 4.14: every other package keeps the "no new
// deps" rule from phases 2-3).
func RenderHTML(s *Status) []byte
```

`internal/api`'s existing `GET /api/workspaces/{id}/book` gains a
`?format=html` query parameter (default stays `markdown`); `html` sets
`Content-Type: text/html; charset=utf-8` and calls `RenderHTML`. No new
route — this is a one-line addition to the existing handler in `read.go`.

### Tests

Draft create → edit (one of each kind: new task version, group reorder,
processor binding) → publish-with-no-conflict, and publish-with-conflict
(two drafts editing the same task, first one published, second one 409s
with the exact conflicting path and resolves it) are repo-level tests on
temp Git repos, same style as phase 2/3's `internal/merge` tests.

## Plan 4c — frontend scaffold and Engineer views

### `internal/webui` (new)

```go
//go:embed dist
var distFS embed.FS

// Handler serves dist/ at the root: real files as themselves (with their
// natural Content-Type from file extension), and index.html for any other
// path (client-side routing) — except it 404s paths that look like a file
// request (contain a ".") that dist doesn't have, so a typo'd asset URL
// doesn't silently return the HTML shell.
func Handler() (http.Handler, error)
```

`cmd/custos/serve.go` wires `srv.WithUI(h)` (new chain method on
`server.Server`, §4a) right after `srv.WithAPI(...)`; `--addr`'s usage text
("there is no authentication yet, so keep it on loopback") is updated to
describe the real auth model instead (ruling 4.15: this sentence is now
false and must be corrected in the same plan that makes it false).

### `web/` project layout (fixed so 4c and 4d's components agree on where
things live)

```
web/
  package.json  vite.config.ts  tsconfig.json  tailwind.config.js  index.html
  src/
    main.ts              # createApp, router, pinia, mounts #app
    App.vue              # top nav (role-aware), <router-view>
    router/index.ts       # routes below; a global beforeEach redirects to
                           # /auth/login (full page navigation, not a
                           # client route — OIDC is a real redirect) when
                           # the auth store has no principal and the target
                           # route needs one
    stores/auth.ts        # Pinia store: principal + roles, loaded once
                           # from GET /auth/me on app start; login()/logout()
    api/client.ts          # fetch wrapper: same-origin, credentials:
                           # "include" (session cookie), JSON in/out,
                           # throws a typed ApiError{status, body} the
                           # views catch to show problems/messages
    types/                 # TypeScript mirrors of the JSON shapes in
                           # internal/api/*.go and this note (by hand, not
                           # generated — the Go structs are the source of
                           # truth; a type drifting out of sync is caught
                           # by vue-tsc only at the call site that misuses
                           # it, not automatically — acceptable at this
                           # project's scale per ruling 4.4)
    views/
      LoginView.vue                    # shown when unauthenticated; also
                                        # the token-only-mode fallback (a
                                        # paste-a-token box) when GET
                                        # /auth/me 404s because OIDC is off
      engineer/
        DashboardView.vue              # workspace picker + GET status
        AnswerView.vue                 # one task: form by answer_type,
                                        # attachment upload, task-version
                                        # diff + "still valid", submit
        ProposalsView.vue              # processor output proposals (3d)
                                        # and catalog pin-update proposals
                                        # (2c), per-item accept/reject
        BookView.vue                   # iframe to ?format=html, link to
                                        # ?format=markdown, freeze/unfreeze
                                        # toggle
      taskauthor/   # components only; views land in plan 4d
      processorauthor/
    components/      # shared widgets: RoleGate.vue, ProblemList.vue (renders
                      # []{path,rule,message} the same way everywhere),
                      # MarkdownView.vue
  dist/   (gitignored except .gitkeep; see Conventions)
```

Routes (vue-router, all under `/`, matching the views above; 4d adds the
task-author/processor-author branch):

```
/login
/w/:id                 -> DashboardView
/w/:id/tasks/:taskId    -> AnswerView
/w/:id/proposals        -> ProposalsView
/w/:id/book             -> BookView
```

### `web/package.json` scripts (fixed names; `make build`/`make test` call these)

```
"dev": "vite"                     # local dev server, proxies /api,/auth,/git to CUSTOS_DEV_BACKEND (default http://127.0.0.1:8080) via vite.config.ts server.proxy
"build": "vue-tsc --noEmit && vite build"   # output to dist/
"type-check": "vue-tsc --noEmit"  # what `make test` runs (ruling 4.3)
```

### `Makefile` changes

```make
build: web-build
	go build -o $(BINARY) ./cmd/custos

web-build:
	cd web && npm ci && npm run build
	rm -rf internal/webui/dist && cp -r web/dist internal/webui/dist

test: web-typecheck
	go vet ./...
	go test ./...
	cd sdk/go && go vet ./... && go test ./...
	python3 -m unittest discover -s sdk/python

web-typecheck:
	cd web && npm ci && npm run type-check
```

(`dev-server`'s target gains a note that `make build` must run first so the
embedded UI is current — not a new target.)

### Tests

Go-side: `internal/webui`'s handler is tested with the committed placeholder
(serves it, falls back to it for an unknown path, 404s a dotted path that
isn't there) — it does not need a real build. Frontend: `vue-tsc --noEmit`
only (ruling 4.4); the views are exercised by hand against `make
dev-server` + `npm run dev` before the task is marked done, and that manual
pass is recorded in the task's report the same way phase 3 tasks recorded
real-Docker runs.

## Plan 4d — Task-Author and Processor-Author views

Builds entirely on 4a-4c; adds no new Go package. New views, same `web/src/views/`
layout:

```
views/taskauthor/
  GroupTreeView.vue     # drag-to-reorder (any HTML5-dnd approach; no new
                         # npm dependency required — ruling 4.16, keep the
                         # frontend dependency list as short as the backend's)
                         # calling PUT /api/catalog/drafts/{id}/groups/{slug}
                         # and .../groups-index
  TaskEditorView.vue     # new-version wizard (patch/minor/major, diff
                         # against the current version shown with
                         # MarkdownView) -> PUT .../tasks/{task-id}
  DraftsView.vue         # list/create/delete drafts, publish button ->
                         # on 409 shows MergeToolView inline
  MergeToolView.vue      # per-conflicting-path choice (ours/theirs/edit),
                         # same resolutions shape as the existing workspace
                         # fork/merge tool this mirrors (phase 2's merge UI
                         # concept, now built for real)
  VersionGraphView.vue   # task.Ref.previous chains rendered as a simple
                         # list-of-generations (no graph-drawing library —
                         # ruling 4.16 again)
views/processorauthor/
  RegistryView.vue       # GET /api/processors
  RunsView.vue            # GET /api/workspaces/{id}/runs (+ /{run}/log)
  DryRunView.vue          # POST /api/processors/{name}/dry-run, polls
                           # GET /api/dry-runs/{id} until state is terminal
```

Router additions:
```
/catalog/drafts
/catalog/drafts/:draftId
/catalog/drafts/:draftId/groups/:slug
/catalog/drafts/:draftId/tasks/:taskId
/processors
/processors/:name/runs
/processors/:name/dry-run
```

`App.vue`'s nav shows the Task-Author and Processor-Author sections only
when the signed-in principal's roles include them (`RoleGate` from 4c).

### Tests

Same as 4c: `vue-tsc --noEmit` plus a recorded manual pass. No new Go code,
so no new Go tests beyond whatever 4b/4c endpoints needed and already got.

## Rulings this note already makes (to be recorded in spec §11.4)

1. Phase 4 is delivered as four plans 4a-4d, run in order.
2. Sessions and personal access tokens are hand-rolled (signed cookie,
   hashed bearer token) rather than a third-party session/auth library.
3. `go build`/`go test` never require Node: `internal/webui/dist` ships a
   committed placeholder; `make build`/`make test` overwrite it with the
   real frontend build.
4. `make test` now also needs Node.js/npm on `PATH` (extends ruling 3.80),
   running `vue-tsc --noEmit` as the only automated frontend check — no
   frontend component/unit test runner in this phase.
5. Sessions do not survive a server restart unless the operator sets
   `--session-secret`; this is accepted for the project's single-server
   scale (§1 "Scale").
6. `admin-emails` is checked fresh on every request (not cached at
   start-up), so granting admin to an email takes effect on that person's
   next request without a restart.
7. Personal access tokens are stored as `sha256(bearer value)`; the
   plaintext is shown exactly once, at creation.
8. Identity and role records live as files under `<data-dir>/auth/`, not
   in Git and not in an index (consistent with 2.2 and 3.2).
9. Engineer role entries may be a literal workspace id or `"*"` for every
   workspace.
10. Reading (clone, GET endpoints) requires only an authenticated
    principal, not a specific role; writes require the role named in each
    endpoint's table.
11. `custos serve` runs in token-only mode (no `/auth/login`) when
    `--oidc-issuer-url` is empty, for CI, tests and air-gapped
    deployments.
12. `custos token create` is a new offline CLI subcommand that creates or
    updates a user and mints a token directly against `<data-dir>/auth`,
    bootstrapping the first admin without any HTTP call.
13. A catalog draft may be transiently invalid; only `Publish` runs
    `catalog.Check`.
14. `goldmark` is added as the one new dependency, confined to
    `internal/status.RenderHTML`.
15. `--addr`'s usage text is corrected once real authentication exists.
16. The frontend takes on no drag-and-drop or graph-drawing npm dependency
    in this phase; the group tree and version graph use plain HTML/CSS
    approaches.
