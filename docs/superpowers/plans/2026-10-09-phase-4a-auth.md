# Phase 4a (Authentication, roles, tokens) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** every request to custos has a known caller — a person signed in through the organization's OIDC identity provider (session cookie) or the owner of a personal access token (Git over HTTP, CLI, scripts) — commits name that caller as their author, and writes need the role §6.4 assigns to them. `X-Custos-Author` is gone.

**Architecture:** new package `internal/auth` (standard library plus `coreos/go-oidc/v3` and `golang.org/x/oauth2`) owns identity: `users.json` and one file per token under `<data-dir>/auth`, HMAC-signed session cookies, the OIDC authorization-code flow with PKCE, `Middleware` that resolves the caller of each request, `Require`/`RequireWorkspace` guards, and `Register`, which adds `/auth/*` and the admin endpoints to the API. `internal/api` gets a small seam — the `api.Caller` interface carried in the request context — because `auth` imports `api` (for `Register`), so `api` cannot import `auth`; `api.Author` keeps its signature and reads that context value. `internal/server` mounts `/auth/` and the UI, applies middleware to the whole mux, demands a signed-in caller on `/api/` and `/git/`, and checks the push role inside its existing push-path matching. `cmd/custos serve` gets the OIDC flags, and a new offline `custos token create` bootstraps admins and tokens.

**Tech Stack:** Go 1.26; `github.com/coreos/go-oidc/v3` v3.20.0 and `golang.org/x/oauth2` v0.36.0 (both already indirect, promoted to direct); tests use `github.com/coreos/go-oidc/v3/oidc/oidctest` (same module) for the test identity provider's discovery document and JWKS.

**Spec:** [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md) §1 (Scale; Personas: "Processor-Authors and Task-Authors are trusted by the organization"), §6.4 (OIDC login, personal access tokens for Git over HTTP and the CLI, the four roles, commits authored by the acting user), §7, §8, §11 (rulings override earlier sections; this plan carries out the note's replacement of 2.6, replaces 2.31, and settles the cost named in 2.7). Shared names and conventions: [2026-10-09-phase-4-architecture.md](2026-10-09-phase-4-architecture.md), sections "Conventions" and "Plan 4a", and its rulings 1-16 (cited here as 4.1-4.16).

**Requires:** phases 1-3 (`main` at `67f8d42` or later). Nothing from plans 4b-4d; they build on this one.

## Global Constraints

- Module `github.com/emeland-io/custos`, Go 1.26 (`go 1.26.0` in `go.mod`, unchanged).
- Third-party modules: only `github.com/coreos/go-oidc/v3` v3.20.0 and `golang.org/x/oauth2` v0.36.0 become direct requirements (they are indirect today; `go mod tidy` promotes them). They are imported only by `internal/auth` (whose tests also import `go-oidc`'s own `oidc/oidctest`). `github.com/go-jose/go-jose/v4` stays indirect: nothing in this repository imports it. Sessions and tokens are hand-rolled with `crypto/hmac`, `crypto/sha256`, `crypto/rand` (ruling 4.2); CSRF protection is the standard library's `http.CrossOriginProtection`.
- Exported names exactly as the architecture note fixes them — `auth.Principal{Subject, Name, Email, Roles}`, `Principal.Signature()`, `auth.Roles{Admin, TaskAuthor, ProcessorAuthor, Engineer}`, `Roles.Has(role string, workspaceID string) bool`, `auth.Config{DataDir, SessionSecret, SessionTTL, AdminEmails, OIDC}`, `auth.OIDCConfig{IssuerURL, ClientID, ClientSecret, RedirectURL}`, `auth.NewManager(cfg Config) (*Manager, error)`, `(*Manager).Middleware(excludedPrefixes ...string) func(http.Handler) http.Handler`, `auth.Require(role string, next http.HandlerFunc) http.HandlerFunc`, `auth.RequireWorkspace(workspaceIDParam string, next http.HandlerFunc) http.HandlerFunc`, `auth.Register(a *api.API, m *Manager)`, `(*server.Server).WithAuth(h http.Handler) *Server`, `(*server.Server).WithMiddleware(mw func(http.Handler) http.Handler) *Server` — with **one forced exception**: the note declares both `type Principal struct` and `func Principal(r *http.Request) (Principal, bool)` in package `auth`, which does not compile (one identifier, two declarations in one package). The type keeps its name; the function is **`auth.FromRequest(r *http.Request) (Principal, bool)`**. See "Decisions beyond the architecture note", first entry.
- `api.Author(r *http.Request) (gitrepo.Signature, error)` keeps its exact signature and its callers do not change; it answers 401 (same `{"error": ...}` body) when the request has no caller.
- Role strings are exactly `"admin"`, `"task-author"`, `"processor-author"`, `"engineer"`; `Admin` implies every role everywhere; an `Engineer` entry is a workspace UUID or `"*"` for every workspace (ruling 4.9).
- Files under `<data-dir>/auth/` (ruling 4.8): `users.json` and `tokens/<token-id>.json`, written as a temporary file plus rename, directory mode 0700, file mode 0600. A token's bearer value is `<token-id>.<base64url of 32 random bytes>`; only `sha256(bearer value)` is stored (ruling 4.7).
- Reading (clone, fetch, every `GET`) needs a signed-in caller and no role (ruling 4.10); writes need the role in this plan's endpoint table.
- REST conventions unchanged: error bodies `{"error": "...", "problems": [...]}` through `api.WriteJSON`/`api.WriteError`; 400 malformed, 401 not signed in, 403 role missing, 404 unknown, 409 conflict, 413 too large; JSON lists are never `null`.
- Every write to a repository still goes through `store`; this plan writes no repository. The files under `<data-dir>/auth` are not repositories.
- Bot identity unchanged: `gitrepo.Bot` commits; the signed-in caller authors.
- Tests: table-driven where the existing code is; repo-level tests on temporary Git repositories through `internal/gittest`; OIDC against a real identity provider served by `httptest` and the real `go-oidc` verification path — no mocked `oidc.Provider`.
- Commit messages: one imperative sentence without prefix, then a blank line and `Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>`.

## Review Focus

1. **Someone forges or replays a credential.** A session cookie whose payload was changed, which was signed with another secret, which expired, which was minted for another OIDC issuer, or which is really the sign-in state cookie must not resolve to anyone (`TestSessionTampering`, Task 3). A token with an unknown or malformed id, a revoked token, a token whose secret part was changed, a token file copied under another id, and a token whose owner is no longer on file must not resolve either (`TestTokenLookupFails`, Task 2). An OIDC callback with a changed `state`, a replayed authorization code, an ID token signed with another key, for another audience, from another issuer, expired, or with another nonce must not create a session (`TestOIDCCallbackRejects`, `TestOIDCCallbackState`, Task 4).
2. **A page on another site makes the signed-in user's browser write.** Unsafe requests that carry only ambient credentials (the session cookie, or Basic credentials a browser cached for `/git/`) and come cross-origin resolve to nobody, so the write answers 401 (`TestMiddlewareCrossOrigin`, Task 3).
3. **The import cycle `auth → api → auth`.** `api` must not import `auth`; it names the caller through `api.Caller`. `go list -deps ./internal/api` must not list `internal/auth` (Task 1, Step 6).
4. **Someone pushes without the role.** A push to the catalog needs `task-author`; a push to a workspace needs `engineer` on that workspace; reading needs only a signed-in caller. The check runs before the push body is read (`TestPushNeedsRole`, Task 8).
5. **Nothing is left open by accident.** `/api/` and `/git/` answer 401 to anonymous requests in every `server.Server`, not only in `custos serve` (enforced in `server.Handler`, not by wiring): a server without the middleware refuses everything there (`TestAPINeedsSignIn`, Task 8). `/healthz`, `/auth/login`, `/auth/callback` and the UI shell stay reachable.
6. **Role changes take effect without a restart.** `users.json` and the token files are read on every request; `custos token create` run next to a running server, and `PUT /api/admin/users/{email}/roles`, change what the very next request may do (`TestTokenCreateWhileServing`, Task 7; `TestPutRoles`, Task 5).

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/api/caller.go` | `api.Caller`, `WithCaller`, `CallerOf`, `RequireRole` — the seam that lets `api` see the caller without importing `auth` |
| `internal/api/caller_test.go` | Tests of the seam |
| `internal/api/request.go` | `Author` reads the caller; `X-Custos-Author` removed (Task 9) |
| `internal/api/api.go` | Role guards on `POST /api/workspaces`, `PUT …/answers/{task}`, `POST …/still-valid` (Task 9) |
| `internal/auth/auth.go` | Package doc, role constants, `Roles`, `Principal`, `Config`, `OIDCConfig`, `Manager`, `NewManager` |
| `internal/auth/users.go` | `User`, `ErrInvalid`, `users.json`, `UpdateUser`, principal building, `writeAtomic` |
| `internal/auth/tokens.go` | Token files, `CreateToken`, lookup, list, revoke |
| `internal/auth/session.go` | Signed cookies: session and sign-in state |
| `internal/auth/middleware.go` | `Middleware`, `FromRequest`, `Require`, `RequireWorkspace` |
| `internal/auth/oidc.go` | OIDC client, `/auth/login`, `/auth/callback` |
| `internal/auth/http.go` | `Register`; `/auth/logout`, `/auth/config`, `/auth/me`, `/auth/tokens…`, `/api/admin/users…` |
| `internal/auth/*_test.go` | Unit tests; `idp_test.go` is the test identity provider |
| `internal/server/server.go` | `WithAuth`, `WithUI`, `WithMiddleware`; sign-in gate on `/api/` and `/git/`; push role check |
| `internal/server/auth_test.go` | Mounting, middleware order, gates, push roles |
| `internal/merge/http.go`, `internal/distribute/api.go`, `internal/runs/api.go` | Role guards on their write endpoints (Task 9) |
| `internal/remote/remote.go`, `push.go`, `clone.go` | custos's own HTTP requests authenticate from the remote URL's user info or `CUSTOS_TOKEN`; no author header (Task 9) |
| `internal/gittest/gittest.go` | `GIT_TERMINAL_PROMPT=0`, so a refused push fails instead of waiting for a password |
| `cmd/custos/auth.go` | `serve`'s auth flags |
| `cmd/custos/token.go` | `custos token create` |
| `cmd/custos/serve.go`, `main.go`, `remote.go` | Wiring, usage, `push` without `--author` |
| `README.md` | Authentication section; examples send a token |
| `go.mod`, `go.sum` | The two modules become direct requirements |

## Endpoints this plan adds or guards

| Method and path | Who | Success | Errors |
| --- | --- | --- | --- |
| `GET /auth/login` | anyone | 302 to the IdP, sets `custos_login` | 404 token-only mode |
| `GET /auth/callback` | anyone holding `custos_login` | 302 to `/`, sets `custos_session`, clears `custos_login` | 404 token-only mode; 400 missing, expired or mismatched sign-in state; 401 IdP refused or ID token invalid; 403 no or unverified email, or name/email unusable as a commit author; 502 IdP unreachable or answered without `id_token` |
| `POST /auth/logout` | anyone | 204, clears `custos_session` | — |
| `GET /auth/config` | anyone | 200 `{"oidc": bool}` | — |
| `GET /auth/me` | signed in | 200 `{"name","email","roles":{"admin","task_author","processor_author","engineer":[]}}` | 401 (always — never 404; see `GET /auth/config` for how a caller distinguishes token-only mode) |
| `GET /auth/tokens` | signed in | 200 `[{"id","label","created","last_used"}]`, oldest first, `last_used` `null` until first use | 401 |
| `POST /auth/tokens` | signed in | 201 `{"id","token"}` with `Cache-Control: no-store` | 400 bad body or label; 401; 413 |
| `DELETE /auth/tokens/{id}` | signed in | 204 | 401; 404 unknown or not the caller's |
| `GET /api/admin/users` | admin | 200 `[{"email","name","roles":{…},"admin_by_config"}]` sorted by email | 401; 403 |
| `PUT /api/admin/users/{email}/roles` | admin | 200 `{"admin","task_author","processor_author","engineer":[]}` | 400 bad email, body or engineer entry; 401; 403; 413 |
| `POST /api/workspaces`, `POST /api/workspaces/{id}/fork` | admin | as before | + 401, 403 |
| `PUT …/answers/{task}`, `POST …/answers/{task}/still-valid`, `POST …/merge`, `POST …/freeze`, `POST …/unfreeze`, `POST …/proposals/accept`, `POST …/proposals/reject`, `POST …/runs/{run}/retry`, `POST …/processor-proposals/{task}/accept`, `POST …/processor-proposals/{task}/reject` (all below `/api/workspaces/{id}/`) | engineer on `{id}` | as before | + 401, 403 |
| `POST /api/processors/{name}/dry-run` | processor-author | as before | + 401, 403 |
| `POST /api/blobs` | signed in | as before | 401 |
| every other `/api/…` (reads) | signed in | as before | + 401 |
| `/git/…` reads (`info/refs?service=git-upload-pack`, `git-upload-pack`) | signed in | as before | 401 with `WWW-Authenticate: Basic realm="custos"` |
| `/git/catalog.git/…` and `/git/catalog/…` push (`info/refs?service=git-receive-pack`, `git-receive-pack`) | task-author | as before | 401; 403 |
| `/git/workspaces/{id}[.git]/…` push | engineer on `{id}` | as before | 401; 403 |

401 answers below `/api/` and `/auth/` carry no `WWW-Authenticate` header, so a browser running the UI never shows its own password dialog; only `/git/` sends the Basic challenge, which Git needs before it sends credentials from the URL or a credential helper.

---

## Task 1 — The caller seam in `internal/api`

**Files:**
- Create: `internal/api/caller.go`
- Test: `internal/api/caller_test.go`

**Interfaces:**
- Consumes: `errorf`, `WriteError` (existing, `internal/api/response.go`).
- Produces (package `api`):
  - `type Caller interface { Signature() gitrepo.Signature; Has(role, workspaceID string) bool }`
  - `func WithCaller(ctx context.Context, c Caller) context.Context`
  - `func CallerOf(r *http.Request) (Caller, bool)`
  - `func RequireRole(role, workspaceIDParam string, next http.HandlerFunc) http.HandlerFunc` — 401 without caller, 403 without role; `workspaceIDParam` names the path value holding the workspace id for `"engineer"` (empty for global roles).
  - unexported `errNotSignedIn()` — the 401 error, used by `RequireRole` here and by `Author` in Task 9.

`Author` is not changed in this task (Task 9 switches it), so every existing test still passes.

- [ ] **Step 1: Write the failing test** `internal/api/caller_test.go`

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
)

// fakeCaller holds the roles named in has: "admin", or "engineer@<workspace>".
type fakeCaller struct {
	sig gitrepo.Signature
	has map[string]bool
}

func (c fakeCaller) Signature() gitrepo.Signature { return c.sig }
func (c fakeCaller) Has(role, workspaceID string) bool {
	return c.has[role] || c.has[role+"@"+workspaceID]
}

func TestCallerOf(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/workspaces", nil)
	if c, ok := CallerOf(req); ok {
		t.Errorf("caller %+v without WithCaller", c)
	}
	req = req.WithContext(WithCaller(req.Context(), nil))
	if _, ok := CallerOf(req); ok {
		t.Error("a nil caller counts as a caller")
	}
	req = req.WithContext(WithCaller(req.Context(), fakeCaller{sig: jane}))
	if c, ok := CallerOf(req); !ok || c.Signature() != jane {
		t.Errorf("caller %+v %v", c, ok)
	}
}

func TestRequireRole(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces", RequireRole("admin", "", ok))
	mux.HandleFunc("PUT /api/workspaces/{id}/answers/{task}", RequireRole("engineer", "id", ok))
	ws := fixture.WorkspaceID
	answer := "/api/workspaces/" + ws + "/answers/" + fixture.TaskA
	for _, tc := range []struct {
		name, method, path string
		caller             Caller // nil: not signed in
		want               int
		msg                string // part of the error message
	}{
		{"not signed in", "POST", "/api/workspaces", nil, http.StatusUnauthorized, "not signed in"},
		{"no role", "POST", "/api/workspaces", fakeCaller{sig: jane}, http.StatusForbidden, "needs the admin role"},
		{"admin", "POST", "/api/workspaces", fakeCaller{sig: jane, has: map[string]bool{"admin": true}}, http.StatusNoContent, ""},
		{"engineer of the workspace", "PUT", answer, fakeCaller{sig: jane, has: map[string]bool{"engineer@" + ws: true}}, http.StatusNoContent, ""},
		{"engineer of another workspace", "PUT", answer, fakeCaller{sig: jane, has: map[string]bool{"engineer@" + otherWorkspace: true}},
			http.StatusForbidden, "needs the engineer role for workspace " + ws},
		{"anonymous engineer request", "PUT", answer, nil, http.StatusUnauthorized, "not signed in"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.caller != nil {
			req = req.WithContext(WithCaller(req.Context(), tc.caller))
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if tc.want == http.StatusNoContent {
			if rec.Code != tc.want {
				t.Errorf("%s: status %d, body %s", tc.name, rec.Code, rec.Body)
			}
			continue
		}
		body := decode[errorJSON](t, rec, tc.want)
		if !strings.Contains(body.Error, tc.msg) {
			t.Errorf("%s: error %q, want it to contain %q", tc.name, body.Error, tc.msg)
		}
		if rec.Header().Get("WWW-Authenticate") != "" {
			t.Errorf("%s: WWW-Authenticate set; the API must not trigger a browser's password dialog", tc.name)
		}
	}
}
```

(`decode[T]`, `jane`, `otherWorkspace` and `fixture` are already in `internal/api`'s tests.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run 'TestCallerOf|TestRequireRole'`
Expected: FAIL — build error `undefined: CallerOf` (and `WithCaller`, `RequireRole`, `Caller`).

- [ ] **Step 3: Implement** `internal/api/caller.go`

```go
package api

import (
	"context"
	"net/http"

	"github.com/emeland-io/custos/internal/gitrepo"
)

// Caller is who a request is made by: the identity the authentication
// middleware (package auth) resolved from the request's session cookie or
// personal access token. Package api names it through this interface
// rather than auth.Principal because auth registers its endpoints on an
// *API and so imports this package.
type Caller interface {
	// Signature is the caller as the author of the commits their request
	// causes.
	Signature() gitrepo.Signature
	// Has reports whether the caller holds role ("admin", "task-author",
	// "processor-author" or "engineer"). workspaceID matters only for
	// "engineer"; an empty workspaceID asks whether the caller is engineer
	// of any workspace.
	Has(role, workspaceID string) bool
}

type callerKey struct{}

// WithCaller returns ctx carrying c. The authentication middleware calls
// it, and tests that serve the API without the middleware.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerOf returns the caller of r, or ok=false when the request carries
// no valid credential.
func CallerOf(r *http.Request) (Caller, bool) {
	c, ok := r.Context().Value(callerKey{}).(Caller)
	return c, ok && c != nil
}

// RequireRole wraps next so that it answers 401 when the request has no
// caller and 403 when the caller lacks role, before next runs. For
// "engineer", workspaceIDParam names the path value that holds the
// workspace id the role must cover (usually "id"); for the other roles it
// is empty. The answers carry no WWW-Authenticate header, so a browser
// never shows its own password dialog.
func RequireRole(role, workspaceIDParam string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := CallerOf(r)
		if !ok {
			WriteError(w, errNotSignedIn())
			return
		}
		ws := ""
		if workspaceIDParam != "" {
			ws = r.PathValue(workspaceIDParam)
		}
		if !c.Has(role, ws) {
			if ws != "" {
				WriteError(w, errorf(http.StatusForbidden, "this needs the %s role for workspace %s", role, ws))
				return
			}
			WriteError(w, errorf(http.StatusForbidden, "this needs the %s role", role))
			return
		}
		next(w, r)
	}
}

// errNotSignedIn is the 401 of a request without a caller.
func errNotSignedIn() error {
	return errorf(http.StatusUnauthorized,
		`not signed in: sign in at /auth/login, or send a personal access token as "Authorization: Bearer <token>"`)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/api/ -run 'TestCallerOf|TestRequireRole' -v`
Expected: PASS.

- [ ] **Step 5: Run the package**

Run: `go test ./internal/api/`
Expected: PASS (nothing else changed).

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/api   # prints nothing
go list -deps ./internal/api | grep -c internal/auth   # prints 0 (the package does not exist yet; keep checking this in Task 9)
git add internal/api/caller.go internal/api/caller_test.go
git commit -m "Let the API see the authenticated caller through a context value

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 2 — `internal/auth`: roles, users and tokens on disk

**Files:**
- Create: `internal/auth/auth.go`, `internal/auth/users.go`, `internal/auth/tokens.go`
- Test: `internal/auth/roles_test.go`, `internal/auth/users_test.go`, `internal/auth/tokens_test.go`

**Interfaces:**
- Consumes: `gitrepo.Signature`; `task.ValidID` (UUID v4 in canonical form); `github.com/google/uuid` (already a direct requirement).
- Produces (package `auth`):
  - `const RoleAdmin = "admin"`, `RoleTaskAuthor = "task-author"`, `RoleProcessorAuthor = "processor-author"`, `RoleEngineer = "engineer"`, `AllWorkspaces = "*"`
  - `type Roles struct{ Admin, TaskAuthor, ProcessorAuthor bool; Engineer []string }` with JSON names `admin`, `task_author`, `processor_author`, `engineer`; `func (r Roles) Has(role string, workspaceID string) bool`
  - `type Principal struct{ Subject, Name, Email string; Roles Roles }`; `func (p Principal) Signature() gitrepo.Signature`; `func (p Principal) Has(role, workspaceID string) bool` (so `Principal` is an `api.Caller`)
  - `type Config`, `type OIDCConfig` exactly as in the note; `type Manager`; `func NewManager(cfg Config) (*Manager, error)` — token-only in this task (Task 4 adds OIDC)
  - `type User struct{ Name, Subject string; Roles Roles }`; `var ErrInvalid`; `func (m *Manager) UpdateUser(email string, update func(u *User)) (User, error)`; `func (m *Manager) CreateToken(email, label string) (id, token string, err error)`
  - unexported: `loadUsers`, `saveUsers`, `principal`, `isAdminEmail`, `lookupToken`, `listTokens`, `revokeToken`, `tokenInfo`, `errNoToken`, `normalizeEmail`, `writeAtomic`

**File formats.** `<data-dir>/auth/users.json` (two-space indented, keys sorted, final newline):

```json
{
  "jane@example.org": {
    "name": "Jane Doe",
    "subject": "00u1a2b3c4",
    "roles": {
      "admin": false,
      "task_author": true,
      "processor_author": false,
      "engineer": [
        "5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9"
      ]
    }
  }
}
```

The key is the email address in lower case; `subject` (the OIDC `sub` of the last sign-in) is left out until the first sign-in. `<data-dir>/auth/tokens/<token-id>.json`:

```json
{
  "id": "0f8e2c4a-6b1d-4e3f-9a7c-5d2b8e1f0a63",
  "owner_email": "jane@example.org",
  "label": "laptop",
  "sha256": "<64 lowercase hex digits of sha256(bearer value)>",
  "created": "2026-10-09T12:00:00Z",
  "last_used": null
}
```

- [ ] **Step 1: Write the failing tests**

`internal/auth/roles_test.go`:

```go
package auth

import (
	"testing"

	"github.com/emeland-io/custos/internal/gitrepo"
)

const (
	ws1 = "5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9"
	ws2 = "9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d"
)

func TestRolesHas(t *testing.T) {
	for _, tc := range []struct {
		roles    Roles
		role, ws string
		want     bool
	}{
		{Roles{}, RoleAdmin, "", false},
		{Roles{}, RoleEngineer, ws1, false},
		{Roles{}, RoleEngineer, "", false},
		{Roles{Admin: true}, RoleAdmin, "", true},
		{Roles{Admin: true}, RoleTaskAuthor, "", true},
		{Roles{Admin: true}, RoleProcessorAuthor, "", true},
		{Roles{Admin: true}, RoleEngineer, ws1, true},
		{Roles{TaskAuthor: true}, RoleTaskAuthor, "", true},
		{Roles{TaskAuthor: true}, RoleProcessorAuthor, "", false},
		{Roles{TaskAuthor: true}, RoleAdmin, "", false},
		{Roles{ProcessorAuthor: true}, RoleProcessorAuthor, "", true},
		{Roles{ProcessorAuthor: true}, RoleTaskAuthor, "", false},
		{Roles{Engineer: []string{ws1}}, RoleEngineer, ws1, true},
		{Roles{Engineer: []string{ws1}}, RoleEngineer, ws2, false},
		{Roles{Engineer: []string{ws1}}, RoleEngineer, "", true}, // engineer of some workspace
		{Roles{Engineer: []string{ws1}}, RoleTaskAuthor, "", false},
		{Roles{Engineer: []string{AllWorkspaces}}, RoleEngineer, ws2, true},
		{Roles{Engineer: []string{ws1}}, "engineers", ws1, false}, // unknown role
	} {
		if got := tc.roles.Has(tc.role, tc.ws); got != tc.want {
			t.Errorf("%+v.Has(%q, %q) = %v, want %v", tc.roles, tc.role, tc.ws, got, tc.want)
		}
	}
}

func TestPrincipal(t *testing.T) {
	p := Principal{Subject: "s", Name: "Jane Doe", Email: "jane@example.org", Roles: Roles{Engineer: []string{ws1}}}
	if p.Signature() != (gitrepo.Signature{Name: "Jane Doe", Email: "jane@example.org"}) {
		t.Errorf("signature %+v", p.Signature())
	}
	if !p.Has(RoleEngineer, ws1) || p.Has(RoleEngineer, ws2) {
		t.Error("Principal.Has does not follow Roles.Has")
	}
}
```

`internal/auth/users_test.go`:

```go
package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// newManager returns a token-only manager over a new data directory.
func newManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNewManagerCreatesTheAuthDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewManager(Config{DataDir: dir}); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"auth", "auth/tokens"} {
		fi, err := os.Stat(filepath.Join(dir, d))
		if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, fi, err)
		}
	}
	if _, err := NewManager(Config{}); err == nil {
		t.Error("NewManager without a data directory succeeded")
	}
}

func TestNewManagerRejectsDamagedUsersFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "auth"), 0o700)
	os.WriteFile(filepath.Join(dir, "auth", "users.json"), []byte("{"), 0o600)
	if _, err := NewManager(Config{DataDir: dir}); err == nil || !strings.Contains(err.Error(), "users.json") {
		t.Errorf("err %v, want one naming users.json", err)
	}
}

func TestUpdateUser(t *testing.T) {
	m := newManager(t)
	u, err := m.UpdateUser(" Jane@Example.ORG ", func(u *User) {
		u.Name = "Jane Doe"
		u.Roles.Engineer = []string{ws2, ws1, ws2}
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Roles{Engineer: []string{ws1, ws2}}); !reflect.DeepEqual(u.Roles, want) {
		t.Errorf("roles %+v, want %+v (sorted, without duplicates)", u.Roles, want)
	}
	// A second update sees the first one.
	if _, err := m.UpdateUser("jane@example.org", func(u *User) { u.Roles.TaskAuthor = true }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(m.dir, "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]map[string]any
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	jane, ok := file["jane@example.org"]
	if !ok || len(file) != 1 || jane["name"] != "Jane Doe" {
		t.Fatalf("users.json:\n%s", data)
	}
	if _, ok := jane["subject"]; ok {
		t.Errorf("subject written before any sign-in:\n%s", data)
	}
	roles := jane["roles"].(map[string]any)
	if roles["task_author"] != true || roles["admin"] != false || roles["processor_author"] != false || len(roles["engineer"].([]any)) != 2 {
		t.Errorf("roles in users.json: %v", roles)
	}
	if fi, _ := os.Stat(filepath.Join(m.dir, "users.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("users.json mode %v", fi.Mode().Perm())
	}
}

func TestUpdateUserRejects(t *testing.T) {
	m := newManager(t)
	if _, err := m.UpdateUser("jane@example.org", func(u *User) { u.Name = "Jane Doe" }); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(m.dir, "users.json"))
	for name, tc := range map[string]struct {
		email  string
		update func(*User)
	}{
		"not an email":       {"jane", func(*User) {}},
		"empty email":        {"", func(*User) {}},
		"email with <":       {"<jane@example.org>", func(*User) {}},
		"email with space":   {"jane doe@example.org", func(*User) {}},
		"name with <":        {"jane@example.org", func(u *User) { u.Name = "Jane <x>" }},
		"name with newline":  {"jane@example.org", func(u *User) { u.Name = "Jane\nDoe" }},
		"engineer not uuid":  {"jane@example.org", func(u *User) { u.Roles.Engineer = []string{"ws-1"} }},
		"engineer uppercase": {"jane@example.org", func(u *User) { u.Roles.Engineer = []string{strings.ToUpper(ws1)} }},
	} {
		if _, err := m.UpdateUser(tc.email, tc.update); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err %v, want ErrInvalid", name, err)
		}
	}
	after, _ := os.ReadFile(filepath.Join(m.dir, "users.json"))
	if string(before) != string(after) {
		t.Errorf("a rejected update changed users.json:\n%s", after)
	}
}

func TestAdminEmails(t *testing.T) {
	m, err := NewManager(Config{DataDir: t.TempDir(), AdminEmails: []string{" JANE@example.org ", ""}})
	if err != nil {
		t.Fatal(err)
	}
	token := addUser(t, m, "jane@example.org", "Jane Doe", Roles{})
	p, ok := m.lookupToken(token)
	if !ok || !p.Roles.Admin {
		t.Errorf("principal %+v %v, want admin from AdminEmails", p, ok)
	}
	users, _ := m.loadUsers()
	if users["jane@example.org"].Roles.Admin {
		t.Error("AdminEmails was written into users.json; it must be applied at every check instead (ruling 4.6)")
	}
}
```

`internal/auth/tokens_test.go`:

```go
package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/task"
)

// addUser puts a user on file and returns a new token for them.
func addUser(t *testing.T, m *Manager, email, name string, roles Roles) string {
	t.Helper()
	if _, err := m.UpdateUser(email, func(u *User) { u.Name, u.Roles = name, roles }); err != nil {
		t.Fatal(err)
	}
	_, token, err := m.CreateToken(email, "test")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// flip changes the first character of a base64url string to another
// valid one.
func flip(s string) string {
	if s == "" {
		return "A"
	}
	if s[0] == 'A' {
		return "B" + s[1:]
	}
	return "A" + s[1:]
}

func TestTokenLookup(t *testing.T) {
	m := newManager(t)
	token := addUser(t, m, "Jane@Example.org", "Jane Doe", Roles{TaskAuthor: true})
	id, _, _ := strings.Cut(token, ".")
	p, ok := m.lookupToken(token)
	if !ok || p.Email != "jane@example.org" || p.Name != "Jane Doe" || !p.Roles.TaskAuthor || p.Subject != "token:"+id {
		t.Fatalf("principal %+v %v", p, ok)
	}
	// After an OIDC sign-in the subject on file is used.
	m.UpdateUser("jane@example.org", func(u *User) { u.Subject = "oidc-sub" })
	if p, _ := m.lookupToken(token); p.Subject != "oidc-sub" {
		t.Errorf("subject %q, want the OIDC subject on file", p.Subject)
	}
	// A user on file without a name signs commits with the email's local part.
	tok := addUser(t, m, "joe@example.org", "", Roles{})
	if p, _ := m.lookupToken(tok); p.Name != "joe" {
		t.Errorf("name %q, want joe", p.Name)
	}
}

func TestTokenFormat(t *testing.T) {
	m := newManager(t)
	token := addUser(t, m, "jane@example.org", "Jane Doe", Roles{})
	id, secret, ok := strings.Cut(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(secret)
	if !ok || !task.ValidID(id) || err != nil || len(raw) != 32 {
		t.Fatalf("token %q: want <uuid v4>.<base64url of 32 bytes>", token)
	}
	path := filepath.Join(m.dir, "tokens", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatal("the token file contains the secret")
	}
	var rec tokenRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(token))
	if rec.ID != id || rec.OwnerEmail != "jane@example.org" || rec.Label != "test" ||
		rec.SHA256 != hex.EncodeToString(sum[:]) || rec.LastUsed != nil || rec.Created.IsZero() {
		t.Errorf("record %+v", rec)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
}

func TestTokenLookupFails(t *testing.T) {
	m := newManager(t)
	token := addUser(t, m, "jane@example.org", "Jane Doe", Roles{Admin: true})
	id, secret, _ := strings.Cut(token, ".")

	_, revoked, err := m.CreateToken("jane@example.org", "revoked")
	if err != nil {
		t.Fatal(err)
	}
	revokedID, _, _ := strings.Cut(revoked, ".")
	if err := m.revokeToken("jane@example.org", revokedID); err != nil {
		t.Fatal(err)
	}

	orphan := addUser(t, m, "gone@example.org", "Gone", Roles{})
	users, _ := m.loadUsers()
	delete(users, "gone@example.org")
	if err := m.saveUsers(users); err != nil {
		t.Fatal(err)
	}

	copied := uuid.NewString()
	data, _ := os.ReadFile(filepath.Join(m.dir, "tokens", id+".json"))
	os.WriteFile(filepath.Join(m.dir, "tokens", copied+".json"), data, 0o600)

	for name, tok := range map[string]string{
		"empty":                    "",
		"id only":                  id,
		"secret only":              secret,
		"unknown id":               uuid.NewString() + "." + secret,
		"revoked":                  revoked,
		"wrong secret":             id + "." + flip(secret),
		"secret with extra data":   token + "x",
		"path in id":               "../users." + secret,
		"upper-case id":            strings.ToUpper(id) + "." + secret,
		"file copied to another id": copied + "." + secret,
		"owner not on file":        orphan,
	} {
		if p, ok := m.lookupToken(tok); ok {
			t.Errorf("%s: resolved to %+v", name, p)
		}
	}
	if _, ok := m.lookupToken(token); !ok {
		t.Error("the valid token stopped working")
	}
}

func TestTokenLastUsed(t *testing.T) {
	m := newManager(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	token := addUser(t, m, "jane@example.org", "Jane Doe", Roles{})
	id, _, _ := strings.Cut(token, ".")
	lastUsed := func() *time.Time {
		rec, err := m.readToken(id)
		if err != nil {
			t.Fatal(err)
		}
		return rec.LastUsed
	}
	m.lookupToken(token)
	if lu := lastUsed(); lu == nil || !lu.Equal(now) {
		t.Fatalf("last_used %v after the first use", lu)
	}
	first := now
	now = now.Add(30 * time.Second)
	m.lookupToken(token)
	if lu := lastUsed(); !lu.Equal(first) {
		t.Errorf("last_used rewritten %v after 30s; it is updated at most once a minute", lu)
	}
	now = now.Add(time.Minute)
	m.lookupToken(token)
	if lu := lastUsed(); !lu.Equal(now) {
		t.Errorf("last_used %v, want %v", lu, now)
	}
}

func TestListAndRevokeTokens(t *testing.T) {
	m := newManager(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	addUser(t, m, "jane@example.org", "Jane Doe", Roles{}) // token "test"
	now = now.Add(time.Second)
	id2, _, err := m.CreateToken("jane@example.org", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	joe := addUser(t, m, "joe@example.org", "Joe", Roles{})
	joeID, _, _ := strings.Cut(joe, ".")

	list, err := m.listTokens("jane@example.org")
	if err != nil || len(list) != 2 || list[0].Label != "test" || list[1].ID != id2 || list[1].Label != "laptop" {
		t.Fatalf("list %+v %v", list, err)
	}
	if empty, err := m.listTokens("nobody@example.org"); err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("list of a user without tokens: %#v %v, want an empty non-nil slice", empty, err)
	}
	for name, id := range map[string]string{"another user's token": joeID, "unknown": uuid.NewString(), "not an id": "../users"} {
		if err := m.revokeToken("jane@example.org", id); !errors.Is(err, errNoToken) {
			t.Errorf("%s: err %v, want errNoToken", name, err)
		}
	}
	if _, ok := m.lookupToken(joe); !ok {
		t.Error("jane revoked joe's token")
	}
	if err := m.revokeToken("jane@example.org", id2); err != nil {
		t.Fatal(err)
	}
	if list, _ := m.listTokens("jane@example.org"); len(list) != 1 {
		t.Errorf("list after revoke %+v", list)
	}
}

func TestCreateTokenRejects(t *testing.T) {
	m := newManager(t)
	addUser(t, m, "jane@example.org", "Jane Doe", Roles{})
	for name, tc := range map[string]struct{ email, label string }{
		"user not on file":     {"nobody@example.org", "x"},
		"not an email":         {"jane", "x"},
		"empty label":          {"jane@example.org", "  "},
		"label too long":       {"jane@example.org", strings.Repeat("x", 101)},
		"control character":    {"jane@example.org", "a\nb"},
	} {
		if _, _, err := m.CreateToken(tc.email, tc.label); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err %v, want ErrInvalid", name, err)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/auth/`
Expected: FAIL — the package has no non-test files yet (`no non-test Go files` / build errors `undefined: Roles`, …).

- [ ] **Step 3: Implement** `internal/auth/auth.go`

```go
// Package auth authenticates the people who use custos (spec §6.4): OIDC
// sign-in in the browser with a signed session cookie, and personal access
// tokens for Git over HTTP, the CLI and scripts. It keeps who has which
// role in files under <data-dir>/auth (ruling 4.8), resolves the caller of
// every request (Manager.Middleware, FromRequest), guards endpoints by role
// (Require, RequireWorkspace) and serves /auth/* and the admin endpoints
// (Register).
package auth

import (
	"bytes"
	"crypto/rand"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/emeland-io/custos/internal/gitrepo"
)

// The roles of §6.4.
const (
	RoleAdmin           = "admin"
	RoleTaskAuthor      = "task-author"
	RoleProcessorAuthor = "processor-author"
	RoleEngineer        = "engineer"
)

// AllWorkspaces as an entry of Roles.Engineer grants the engineer role for
// every workspace (ruling 4.9).
const AllWorkspaces = "*"

// Principal is the authenticated identity of a request: either a human who
// signed in through OIDC, or the owner of a personal access token. Name and
// Email make a gitrepo.Signature for commits the person's actions cause.
type Principal struct {
	Subject string // OIDC "sub", or "token:<token id>" for a PAT-only identity with no OIDC login on record
	Name    string
	Email   string
	Roles   Roles
}

// Signature is the principal as the author of commits.
func (p Principal) Signature() gitrepo.Signature {
	return gitrepo.Signature{Name: p.Name, Email: p.Email}
}

// Has reports whether p holds role (see Roles.Has). It makes Principal an
// api.Caller.
func (p Principal) Has(role, workspaceID string) bool { return p.Roles.Has(role, workspaceID) }

// Roles. Engineer is scoped per workspace; the others are global. Admin
// implies every other role everywhere (§6.4: "admin: create and fork
// workspaces, manage roles").
type Roles struct {
	Admin           bool     `json:"admin"`
	TaskAuthor      bool     `json:"task_author"`
	ProcessorAuthor bool     `json:"processor_author"`
	Engineer        []string `json:"engineer"` // workspace ids; "*" grants every workspace (ruling 4.9)
}

// Has reports whether r holds role, one of RoleAdmin, RoleTaskAuthor,
// RoleProcessorAuthor and RoleEngineer. workspaceID is ignored except for
// RoleEngineer, where an empty workspaceID asks for "engineer of any
// workspace". Admin always has every role; an unknown role nobody else has.
func (r Roles) Has(role string, workspaceID string) bool {
	if r.Admin {
		return true
	}
	switch role {
	case RoleTaskAuthor:
		return r.TaskAuthor
	case RoleProcessorAuthor:
		return r.ProcessorAuthor
	case RoleEngineer:
		for _, id := range r.Engineer {
			if workspaceID == "" || id == AllWorkspaces || id == workspaceID {
				return true
			}
		}
	}
	return false
}

// normalized returns r with Engineer sorted, without duplicates, and an
// empty list instead of nil, the way users.json and every JSON answer of
// this package list it.
func (r Roles) normalized() Roles {
	eng := slices.Clone(r.Engineer)
	slices.Sort(eng)
	eng = slices.Compact(eng)
	if eng == nil {
		eng = []string{}
	}
	r.Engineer = eng
	return r
}

// Config configures a Manager. One per process, created by cmd/custos.
type Config struct {
	DataDir       string
	SessionSecret []byte        // HMAC-SHA256 key for session cookies; random if empty (ruling 4.5: sessions do not survive a restart unless the operator sets one)
	SessionTTL    time.Duration // default 12h
	AdminEmails   []string      // users whose email is in this list get Admin on every login/token use, in addition to whatever is on file (ruling 4.6)
	OIDC          *OIDCConfig   // nil disables OIDC login entirely (token-only mode, e.g. CI/tests)
}

// OIDCConfig names the identity provider and how custos is registered there.
type OIDCConfig struct {
	IssuerURL, ClientID, ClientSecret, RedirectURL string
}

// defaultSessionTTL is the lifetime of a session when Config.SessionTTL is 0.
const defaultSessionTTL = 12 * time.Hour

// Manager owns the identity store under <data-dir>/auth and issues and
// verifies sessions and tokens. It caches nothing from the files: every
// request reads users.json and its token's file, so changes made by another
// process (custos token create) apply at once.
type Manager struct {
	dir         string        // <data-dir>/auth
	secret      []byte        // HMAC-SHA256 key of the session and sign-in cookies
	ttl         time.Duration // lifetime of a session
	adminEmails []string      // lower case
	issuer      string        // OIDC issuer URL; "" in token-only mode, which accepts no session
	secure      bool          // cookies carry the Secure attribute (the redirect URL is https)
	cop         *http.CrossOriginProtection
	now         func() time.Time // tests replace it

	mu sync.Mutex // serialises read-modify-write of users.json and of token files within this process
}

// NewManager loads or creates <data-dir>/auth/users.json and its token
// directory. With cfg.OIDC set it also discovers the identity provider and
// fails if the issuer is unreachable.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("auth: no data directory")
	}
	m := &Manager{
		dir:    filepath.Join(cfg.DataDir, "auth"),
		secret: bytes.Clone(cfg.SessionSecret),
		ttl:    cfg.SessionTTL,
		cop:    http.NewCrossOriginProtection(),
		now:    time.Now,
	}
	if err := os.MkdirAll(filepath.Join(m.dir, "tokens"), 0o700); err != nil {
		return nil, err
	}
	if len(m.secret) == 0 {
		m.secret = make([]byte, 32)
		rand.Read(m.secret) // never fails (crypto/rand since Go 1.24)
	}
	if m.ttl <= 0 {
		m.ttl = defaultSessionTTL
	}
	for _, e := range cfg.AdminEmails {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			m.adminEmails = append(m.adminEmails, e)
		}
	}
	if _, err := m.loadUsers(); err != nil { // a damaged users.json stops start-up
		return nil, err
	}
	if cfg.OIDC != nil {
		// Replaced in Task 4 by the OIDC client; nothing passes OIDC before then.
		return nil, errors.New("auth: OIDC sign-in is not available yet")
	}
	return m, nil
}
```

- [ ] **Step 4: Implement** `internal/auth/users.go`

```go
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/task"
)

// User is one person on file in <data-dir>/auth/users.json, keyed there by
// their email address in lower case.
type User struct {
	Name    string `json:"name"`              // from the last OIDC sign-in or custos token create; empty for someone an admin added before their first sign-in
	Subject string `json:"subject,omitempty"` // OIDC "sub" of the last sign-in
	Roles   Roles  `json:"roles"`
}

// ErrInvalid marks a malformed email address, name, label or role entry.
var ErrInvalid = errors.New("invalid")

// normalizeEmail returns s in lower case without surrounding space, or
// ErrInvalid when it cannot be an email address custos can sign commits
// with or put in a URL path.
func normalizeEmail(s string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(s))
	at := strings.LastIndex(e, "@")
	if at <= 0 || at == len(e)-1 || strings.ContainsAny(e, "<>\"\\/,; \t\r\n\x00") {
		return "", fmt.Errorf("%w: %q is not an email address", ErrInvalid, s)
	}
	return e, nil
}

// checkUser rejects names that cannot sign a commit and engineer entries
// that are neither a workspace UUID nor "*".
func checkUser(u User) error {
	if strings.ContainsAny(u.Name, "<>\r\n\x00") {
		return fmt.Errorf("%w: name %q contains <, > or a line break", ErrInvalid, u.Name)
	}
	for _, id := range u.Roles.Engineer {
		if id != AllWorkspaces && !task.ValidID(id) {
			return fmt.Errorf("%w: engineer entry %q is neither a workspace id (a lower-case UUID v4) nor %q", ErrInvalid, id, AllWorkspaces)
		}
	}
	return nil
}

func (m *Manager) usersFile() string { return filepath.Join(m.dir, "users.json") }

// loadUsers reads users.json; a missing file is an empty one.
func (m *Manager) loadUsers() (map[string]User, error) {
	data, err := os.ReadFile(m.usersFile())
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]User{}, nil
	}
	if err != nil {
		return nil, err
	}
	users := map[string]User{}
	if err := json.Unmarshal(data, &users); err != nil {
		return nil, fmt.Errorf("%s: %w", m.usersFile(), err)
	}
	return users, nil
}

// saveUsers replaces users.json. Callers hold m.mu.
func (m *Manager) saveUsers(users map[string]User) error {
	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(m.usersFile(), append(data, '\n'))
}

// UpdateUser applies update to the record of email — an empty record when
// the email is not on file yet — checks the result and saves it. Errors
// wrapping ErrInvalid name a malformed email, name or engineer entry; the
// file is then unchanged. Engineer lists are saved sorted and without
// duplicates.
func (m *Manager) UpdateUser(email string, update func(u *User)) (User, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	users, err := m.loadUsers()
	if err != nil {
		return User{}, err
	}
	u := users[email]
	update(&u)
	u.Roles = u.Roles.normalized()
	if err := checkUser(u); err != nil {
		return User{}, err
	}
	users[email] = u
	if err := m.saveUsers(users); err != nil {
		return User{}, err
	}
	return u, nil
}

// isAdminEmail reports whether email is in Config.AdminEmails.
func (m *Manager) isAdminEmail(email string) bool {
	return slices.Contains(m.adminEmails, strings.ToLower(email))
}

// principal is the Principal of the user on file as u under email:
// AdminEmails adds Admin, and a user without a name signs with the local
// part of their email address.
func (m *Manager) principal(email string, u User, subject string) Principal {
	roles := u.Roles.normalized()
	if m.isAdminEmail(email) {
		roles.Admin = true
	}
	name := u.Name
	if name == "" {
		name, _, _ = strings.Cut(email, "@")
	}
	return Principal{Subject: subject, Name: name, Email: email, Roles: roles}
}

// writeAtomic writes data to a temporary file (mode 0600) next to path and
// renames it over path.
func writeAtomic(path string, data []byte) error {
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

- [ ] **Step 5: Implement** `internal/auth/tokens.go`

```go
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/task"
)

// tokenRecord is the file <data-dir>/auth/tokens/<id>.json. It holds the
// SHA-256 of the whole bearer value, never the value itself (ruling 4.7).
type tokenRecord struct {
	ID         string     `json:"id"`
	OwnerEmail string     `json:"owner_email"`
	Label      string     `json:"label"`
	SHA256     string     `json:"sha256"`
	Created    time.Time  `json:"created"`
	LastUsed   *time.Time `json:"last_used"`
}

// tokenInfo is a token as GET /auth/tokens lists it.
type tokenInfo struct {
	ID       string     `json:"id"`
	Label    string     `json:"label"`
	Created  time.Time  `json:"created"`
	LastUsed *time.Time `json:"last_used"`
}

// maxLabelLen bounds a token's label, in bytes.
const maxLabelLen = 100

// lastUsedEvery is how often at most a token's last_used is rewritten, so
// that a clone (several requests) does not rewrite the file each time.
const lastUsedEvery = time.Minute

// errNoToken: the token does not exist or is not the caller's.
var errNoToken = errors.New("no such token")

func (m *Manager) tokenFile(id string) string { return filepath.Join(m.dir, "tokens", id+".json") }

// CreateToken mints a personal access token for the user on file under
// email, labelled label (1-100 bytes, no control characters). It returns
// the token's id and its bearer value "<id>.<32 random bytes, base64url>",
// which is not stored and cannot be shown again. A user not on file, a
// malformed email or a bad label is ErrInvalid.
func (m *Manager) CreateToken(email, label string) (id, token string, err error) {
	if email, err = normalizeEmail(email); err != nil {
		return "", "", err
	}
	if label, err = checkLabel(label); err != nil {
		return "", "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	users, err := m.loadUsers()
	if err != nil {
		return "", "", err
	}
	if _, ok := users[email]; !ok {
		return "", "", fmt.Errorf("%w: %s is not on file; add the user first", ErrInvalid, email)
	}
	secret := make([]byte, 32)
	rand.Read(secret) // never fails (crypto/rand since Go 1.24)
	id = uuid.NewString()
	token = id + "." + base64.RawURLEncoding.EncodeToString(secret)
	sum := sha256.Sum256([]byte(token))
	rec := tokenRecord{ID: id, OwnerEmail: email, Label: label, SHA256: hex.EncodeToString(sum[:]), Created: m.now().UTC()}
	if err := m.writeToken(rec); err != nil {
		return "", "", err
	}
	return id, token, nil
}

// checkLabel trims s and checks it as a token label.
func checkLabel(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", fmt.Errorf("%w: a token needs a label, such as the machine or script that uses it", ErrInvalid)
	case len(s) > maxLabelLen:
		return "", fmt.Errorf("%w: a token label has at most %d bytes", ErrInvalid, maxLabelLen)
	case strings.ContainsFunc(s, unicode.IsControl):
		return "", fmt.Errorf("%w: a token label must not contain control characters", ErrInvalid)
	}
	return s, nil
}

func (m *Manager) writeToken(rec tokenRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(m.tokenFile(rec.ID), append(data, '\n'))
}

// readToken reads the record of id; id must have passed task.ValidID. A
// file whose id field is not its name is an error, so a copied file does
// not work under another id.
func (m *Manager) readToken(id string) (tokenRecord, error) {
	data, err := os.ReadFile(m.tokenFile(id))
	if err != nil {
		return tokenRecord{}, err
	}
	var rec tokenRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return tokenRecord{}, fmt.Errorf("token %s: %w", id, err)
	}
	if rec.ID != id {
		return tokenRecord{}, fmt.Errorf("token %s: file holds token %q", id, rec.ID)
	}
	return rec, nil
}

// lookupToken returns the principal a bearer value belongs to, or
// ok=false when it names no live token: malformed, unknown id, revoked,
// wrong secret, or an owner who is no longer on file. The id is read off
// the value; the whole value is hashed and compared in constant time.
func (m *Manager) lookupToken(token string) (Principal, bool) {
	id, _, ok := strings.Cut(token, ".")
	if !ok || !task.ValidID(id) {
		return Principal{}, false
	}
	rec, err := m.readToken(id)
	if err != nil {
		return Principal{}, false
	}
	want, err := hex.DecodeString(rec.SHA256)
	sum := sha256.Sum256([]byte(token))
	if err != nil || subtle.ConstantTimeCompare(sum[:], want) != 1 {
		return Principal{}, false
	}
	users, err := m.loadUsers()
	if err != nil {
		return Principal{}, false
	}
	u, ok := users[rec.OwnerEmail]
	if !ok {
		return Principal{}, false
	}
	subject := u.Subject
	if subject == "" {
		subject = "token:" + id
	}
	if rec.LastUsed == nil || m.now().Sub(*rec.LastUsed) >= lastUsedEvery {
		m.touchToken(id)
	}
	return m.principal(rec.OwnerEmail, u, subject), true
}

// touchToken sets last_used to now, at most once per lastUsedEvery. It is
// best effort: a failure must not fail the request.
func (m *Manager) touchToken(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, err := m.readToken(id)
	if err != nil {
		return // revoked meanwhile
	}
	now := m.now().UTC()
	if rec.LastUsed != nil && now.Sub(*rec.LastUsed) < lastUsedEvery {
		return
	}
	rec.LastUsed = &now
	m.writeToken(rec)
}

// listTokens returns the tokens of email, oldest first, never nil.
// Damaged files are skipped, like damaged run records (ruling 3.52).
func (m *Manager) listTokens(email string) ([]tokenInfo, error) {
	entries, err := os.ReadDir(filepath.Join(m.dir, "tokens"))
	if err != nil {
		return nil, err
	}
	out := []tokenInfo{}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !task.ValidID(id) {
			continue // a temporary file of writeAtomic
		}
		rec, err := m.readToken(id)
		if err != nil || rec.OwnerEmail != email {
			continue
		}
		out = append(out, tokenInfo{ID: rec.ID, Label: rec.Label, Created: rec.Created, LastUsed: rec.LastUsed})
	}
	slices.SortFunc(out, func(a, b tokenInfo) int {
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// revokeToken deletes token id of email. errNoToken when there is no such
// token or it belongs to someone else (the two are not told apart).
func (m *Manager) revokeToken(email, id string) error {
	if !task.ValidID(id) {
		return errNoToken
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, err := m.readToken(id)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && rec.OwnerEmail != email) {
		return errNoToken
	}
	if err != nil {
		return err
	}
	return os.Remove(m.tokenFile(id))
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./internal/auth/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
gofmt -l internal/auth   # prints nothing
go vet ./internal/auth
git add internal/auth
git commit -m "Keep users, roles and personal access tokens under the data directory

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```


---

## Task 3 — `internal/auth`: session cookies, the sign-in middleware, `Require`/`RequireWorkspace`

**Files:**
- Create: `internal/auth/session.go`, `internal/auth/middleware.go`
- Test: `internal/auth/session_test.go`, `internal/auth/middleware_test.go`

**Interfaces:**
- Consumes: `api.WithCaller`, `api.CallerOf`, `api.RequireRole` (Task 1); `Manager.secret`, `Manager.ttl`, `Manager.issuer`, `Manager.cop`, `Manager.now` (Task 2).
- Produces (package `auth`):
  - `func FromRequest(r *http.Request) (Principal, bool)` — the note's `Principal(r)`, renamed per the Global Constraints exception.
  - `func (m *Manager) Middleware(excludedPrefixes ...string) func(http.Handler) http.Handler`
  - `func Require(role string, next http.HandlerFunc) http.HandlerFunc`
  - `func RequireWorkspace(workspaceIDParam string, next http.HandlerFunc) http.HandlerFunc`
  - unexported: `claims`, `(*Manager).sign`, `(*Manager).verify`, `(*Manager).setCookie`, `(*Manager).clearCookie`, `errBadCookie`, cookie name constants `sessionCookie`, `loginCookie`, `(*Manager).resolve`

`FromRequest` does not read a separate context key: it type-asserts the `api.Caller` that `api.CallerOf` already found back to `Principal`. `Middleware` is the only thing that ever puts one there (through `api.WithCaller`), so the two packages agree on one caller per request without a second context key.

**Cookie format.** `custos_session` and `custos_login` both hold `base64url(json) + "." + base64url(HMAC-SHA256(secret, json))`. The JSON (`claims`) carries a `kind` field — `"session"` or `"login"` — so that one cookie's bytes, copied into the other cookie's name, verify as neither (Review Focus 1's "really the sign-in state cookie"). A session's claims also carry the `issuer` the manager was built with, so a session minted under one OIDC configuration stops working after the operator points `--oidc-issuer-url` elsewhere, without needing to invalidate anything on disk.

- [ ] **Step 1: Write the failing tests**

`internal/auth/session_test.go`:

```go
package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	m := newManager(t)
	m.issuer = "https://idp.example.org"
	value := m.sign(claims{Kind: "session", Email: "jane@example.org", Subject: "sub-1", Issuer: m.issuer, Exp: m.now().Add(time.Hour).Unix()})
	c, err := m.verify(value, "session")
	if err != nil || c.Email != "jane@example.org" || c.Subject != "sub-1" {
		t.Fatalf("claims %+v, err %v", c, err)
	}
}

// TestSessionTampering is Review Focus 1: none of these variants of a
// signed cookie must verify.
func TestSessionTampering(t *testing.T) {
	m := newManager(t)
	m.issuer = "https://idp.example.org"
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	good := claims{Kind: "session", Email: "jane@example.org", Subject: "sub-1", Issuer: m.issuer, Exp: now.Add(time.Hour).Unix()}
	value := m.sign(good)
	payload, sig, _ := strings.Cut(value, ".")

	other := newManager(t) // a different random secret
	expired := m.sign(claims{Kind: "session", Email: "jane@example.org", Issuer: m.issuer, Exp: now.Add(-time.Second).Unix()})
	loginShaped := m.sign(claims{Kind: "login", Email: "jane@example.org", Issuer: m.issuer, Exp: now.Add(time.Hour).Unix()})
	// wrongIssuer is signed with m's own secret, not a copy of m with a
	// different issuer field: verify() does not itself check the issuer
	// (resolve(), one layer up in middleware.go, does that) — so the only
	// thing that actually needs to differ here is claims.Issuer, which
	// the test's own "want" computation checks below. Found while applying
	// this plan: `otherIssuer := *m; otherIssuer.issuer = "..."` both
	// copies a Manager containing a sync.Mutex (a go vet failure) and
	// signs `good`, whose Issuer field is already m.issuer and never
	// changes — so that case produced a token byte-identical to the
	// "good (control)" one, silently testing nothing.
	wrongIssuer := m.sign(claims{Kind: "session", Email: "jane@example.org", Subject: "sub-1", Issuer: "https://other-idp.example.org", Exp: now.Add(time.Hour).Unix()})

	for name, value := range map[string]string{
		"good (control)":             value,
		"no dot":                     payload + sig,
		"payload changed":            flip(payload) + "." + sig,
		"signed with another secret": other.sign(good),
		"wrong issuer in claims":     wrongIssuer,
		"expired":                    expired,
		"really the login cookie":    loginShaped,
		"empty":                      "",
		"garbage":                    "not-a-cookie",
	} {
		c, err := m.verify(value, "session")
		ok := err == nil && c.Issuer == m.issuer
		want := name == "good (control)"
		if ok != want {
			t.Errorf("%s: verify ok=%v err=%v, want ok=%v", name, ok, err, want)
		}
	}
}

func TestSetAndClearCookie(t *testing.T) {
	m := newManager(t)
	rec := httptest.NewRecorder()
	m.setCookie(rec, sessionCookie, "value", time.Hour)
	resp := rec.Result()
	if len(resp.Cookies()) != 1 {
		t.Fatalf("cookies %+v", resp.Cookies())
	}
	c := resp.Cookies()[0]
	if c.Name != sessionCookie || c.Value != "value" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie %+v", c)
	}
	rec = httptest.NewRecorder()
	m.clearCookie(rec, sessionCookie)
	c = rec.Result().Cookies()[0]
	if c.MaxAge >= 0 || c.Value != "" {
		t.Errorf("clearCookie: %+v", c)
	}
}
```

`internal/auth/middleware_test.go`:

```go
package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/api"
)

// sessionFor signs in email as a cookie header value ready for
// httptest.NewRequest + req.AddCookie, or req.Header.Set("Cookie", ...).
func sessionFor(t *testing.T, m *Manager, email string) *http.Cookie {
	t.Helper()
	return &http.Cookie{Name: sessionCookie, Value: m.sign(claims{
		Kind: "session", Email: email, Subject: "token:test", Issuer: m.issuer, Exp: m.now().Add(time.Hour).Unix(),
	})}
}

func TestMiddlewareResolvesSessionAndToken(t *testing.T) {
	m := newManager(t)
	addUser(t, m, "jane@example.org", "Jane Doe", Roles{TaskAuthor: true})
	tok := addUser(t, m, "joe@example.org", "Joe", Roles{Admin: true})

	ok := func(w http.ResponseWriter, r *http.Request) {
		p, signedIn := FromRequest(r)
		if !signedIn {
			w.WriteHeader(http.StatusTeapot) // distinguishable "no caller" marker
			return
		}
		w.Header().Set("X-Email", p.Email)
	}
	h := m.Middleware()(http.HandlerFunc(ok))

	req := httptest.NewRequest("GET", "/api/catalog", nil)
	req.AddCookie(sessionFor(t, m, "jane@example.org"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Email") != "jane@example.org" {
		t.Errorf("session: X-Email %q", rec.Header().Get("X-Email"))
	}

	req = httptest.NewRequest("GET", "/api/catalog", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Email") != "joe@example.org" {
		t.Errorf("bearer: X-Email %q", rec.Header().Get("X-Email"))
	}

	// resolve() passes the whole Basic password straight to lookupToken,
	// which expects the full "<id>.<secret>" bearer value, not the secret
	// half alone — found while applying this plan: an earlier version of
	// this line used cutToken to split tok and sent only the secret half,
	// which lookupToken then (correctly) rejected as malformed.
	req = httptest.NewRequest("GET", "/api/catalog", nil)
	req.SetBasicAuth("x", tok) // git sends the token as the Basic password; the username is ignored
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Email") != "joe@example.org" {
		t.Errorf("basic: X-Email %q", rec.Header().Get("X-Email"))
	}

	req = httptest.NewRequest("GET", "/api/catalog", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot {
		t.Errorf("no credential: code %d, want the handler's own 418 (no caller, not a 401 from Middleware itself)", rec.Code)
	}
}

func TestMiddlewareExcludedPrefixes(t *testing.T) {
	m := newManager(t)
	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if _, ok := FromRequest(r); ok {
			t.Error("excluded path resolved a caller from a cookie it was never asked to check")
		}
	})
	h := m.Middleware("/healthz", "/auth/login")(next)
	req := httptest.NewRequest("GET", "/healthz", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "garbage-that-would-otherwise-be-rejected-not-ignored"})
	httptest.NewRecorder()
	h.ServeHTTP(httptest.NewRecorder(), req)
	if calls != 1 {
		t.Fatalf("calls %d", calls)
	}
}

// TestMiddlewareCrossOrigin is Review Focus 2: an unsafe request whose only
// credential is the session cookie or Basic (both ambient: a browser
// attaches them on its own) must resolve to nobody when it is cross-origin;
// a Bearer token, which a cross-site page cannot attach by itself, is
// unaffected.
func TestMiddlewareCrossOrigin(t *testing.T) {
	m := newManager(t)
	tok := addUser(t, m, "jane@example.org", "Jane Doe", Roles{Admin: true})
	h := m.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := FromRequest(r); ok {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))

	post := func(withToken bool, sameOrigin bool) int {
		req := httptest.NewRequest("POST", "/api/workspaces", nil)
		req.Host = "custos.example.org"
		if withToken {
			req.Header.Set("Authorization", "Bearer "+tok)
		} else {
			req.AddCookie(sessionFor(t, m, "jane@example.org"))
		}
		if !sameOrigin {
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			req.Header.Set("Origin", "https://evil.example.org")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := post(false, true); got != http.StatusOK {
		t.Errorf("same-origin cookie write: %d", got)
	}
	if got := post(false, false); got != http.StatusUnauthorized {
		t.Errorf("cross-origin cookie write: %d, want 401 (resolves to nobody)", got)
	}
	if got := post(true, false); got != http.StatusOK {
		t.Errorf("cross-origin bearer-token write: %d, want 200 (a cross-site page cannot attach this header itself)", got)
	}
}

func TestRequireAndRequireWorkspace(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces", Require(RoleAdmin, ok))
	mux.HandleFunc("PUT /api/workspaces/{id}/answers/{task}", RequireWorkspace("id", ok))

	req := httptest.NewRequest("POST", "/api/workspaces", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no caller: %d", rec.Code)
	}

	req = httptest.NewRequest("POST", "/api/workspaces", nil)
	req = req.WithContext(api.WithCaller(req.Context(), Principal{Email: "jane@example.org", Roles: Roles{Admin: true}}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("admin: %d", rec.Code)
	}

	req = httptest.NewRequest("PUT", "/api/workspaces/"+ws1+"/answers/"+ws1, nil)
	req = req.WithContext(api.WithCaller(req.Context(), Principal{Email: "jane@example.org", Roles: Roles{Engineer: []string{ws1}}}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("engineer of %s: %d", ws1, rec.Code)
	}

	req = httptest.NewRequest("PUT", "/api/workspaces/"+ws2+"/answers/"+ws1, nil)
	req = req.WithContext(api.WithCaller(req.Context(), Principal{Email: "jane@example.org", Roles: Roles{Engineer: []string{ws1}}}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("engineer of another workspace: %d", rec.Code)
	}
}

// cutToken splits a bearer value into its id and secret, as the Basic
// password half of TestMiddlewareResolvesSessionAndToken needs.
func cutToken(token string) (id, secret string, ok bool) {
	return strings0Cut(token)
}
```

(`strings0Cut` is a one-line local alias the step below removes — see Step 3's note; it exists only so the test above compiles before `middleware.go`'s real helper is in place. In practice this file imports `strings` directly and calls `strings.Cut`; the stray helper is not part of the final file. Replace the body of `cutToken` with `return strings.Cut(token, ".")` — meaning `middleware_test.go` imports `"strings"`, not an alias.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/auth/ -run 'TestSign|TestSession|TestSetAndClear|TestMiddleware|TestRequire'`
Expected: FAIL — `undefined: claims`, `m.sign`, `m.verify`, `m.setCookie`, `FromRequest`, `Middleware`, `Require`, `RequireWorkspace`.

- [ ] **Step 3: Implement** `internal/auth/session.go`

```go
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Cookie names. custos_login never reaches a route handler: Middleware
// reads it only inside handleLogin/handleCallback.
const (
	sessionCookie = "custos_session"
	loginCookie   = "custos_login"
)

// claims is the signed payload of both cookies. Kind tells them apart so
// that one cookie's bytes, presented under the other cookie's name, verify
// as neither (Review Focus 1). The login fields are empty on a session and
// vice versa.
type claims struct {
	Kind     string `json:"kind"` // "session" or "login"
	Email    string `json:"email,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Issuer   string `json:"issuer,omitempty"`   // the Manager.issuer a session or login was started against
	State    string `json:"state,omitempty"`    // login only: the OIDC "state"
	Nonce    string `json:"nonce,omitempty"`    // login only: the OIDC nonce
	Verifier string `json:"verifier,omitempty"` // login only: the PKCE code verifier
	Return   string `json:"return,omitempty"`   // login only: where to send the browser after the callback
	Exp      int64  `json:"exp"`                // unix seconds
}

// errBadCookie covers every way a cookie can fail to verify — malformed,
// wrongly signed, expired, wrong kind — without telling the caller which,
// so a 400 cannot be used to probe the difference.
var errBadCookie = errors.New("auth: invalid, expired or tampered cookie")

// sign returns c as a cookie value: base64url(json) + "." +
// base64url(HMAC-SHA256(secret, json)).
func (m *Manager) sign(c claims) string {
	data, _ := json.Marshal(c) // claims never fails to marshal
	mac := hmac.New(sha256.New, m.secret)
	mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verify checks value's signature, expiry and kind (one of sessionCookie's
// "session" or loginCookie's "login", passed as want). Everything that can
// go wrong collapses to errBadCookie.
func (m *Manager) verify(value, want string) (claims, error) {
	payload, sig, ok := strings.Cut(value, ".")
	if !ok || payload == "" || sig == "" {
		return claims{}, errBadCookie
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return claims{}, errBadCookie
	}
	wantMAC, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return claims{}, errBadCookie
	}
	mac := hmac.New(sha256.New, m.secret)
	mac.Write(data)
	if subtle.ConstantTimeCompare(mac.Sum(nil), wantMAC) != 1 {
		return claims{}, errBadCookie
	}
	var c claims
	if err := json.Unmarshal(data, &c); err != nil {
		return claims{}, errBadCookie
	}
	if c.Kind != want || c.Exp == 0 || !m.now().Before(time.Unix(c.Exp, 0)) {
		return claims{}, errBadCookie
	}
	return c, nil
}

// setCookie sets name to value, valid for ttl, HttpOnly, SameSite=Lax (the
// OIDC redirect back from the IdP is a top-level GET navigation, which Lax
// still sends the cookie on) and Secure when m.secure.
func (m *Manager) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: true, Secure: m.secure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds()),
	})
}

// clearCookie deletes name from the browser.
func (m *Manager) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", HttpOnly: true, Secure: m.secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}
```

- [ ] **Step 4: Run the session tests**

Run: `go test ./internal/auth/ -run 'TestSign|TestSession|TestSetAndClear' -v`
Expected: PASS.

- [ ] **Step 5: Implement** `internal/auth/middleware.go`

```go
package auth

import (
	"net/http"
	"strings"

	"github.com/emeland-io/custos/internal/api"
)

// FromRequest returns the caller Middleware resolved for r. It is the
// architecture note's Principal(r), renamed because package auth cannot
// declare both a type and a function named Principal (Global Constraints).
// It does not read a context key of its own: it asks api.CallerOf — the
// only thing that ever puts a caller there is Middleware, via
// api.WithCaller — and type-asserts the result back to Principal.
func FromRequest(r *http.Request) (Principal, bool) {
	c, ok := api.CallerOf(r)
	if !ok {
		return Principal{}, false
	}
	p, ok := c.(Principal)
	return p, ok
}

// Require wraps next so it 401s (no caller) or 403s (caller present, role
// missing) before next runs. It is auth.Require(role, next) == api.
// RequireRole(role, "", next); the architecture note's name for roles that
// are not scoped to a workspace.
func Require(role string, next http.HandlerFunc) http.HandlerFunc {
	return api.RequireRole(role, "", next)
}

// RequireWorkspace is Require for the engineer role scoped to the
// workspace named by r.PathValue(workspaceIDParam) (usually "id").
func RequireWorkspace(workspaceIDParam string, next http.HandlerFunc) http.HandlerFunc {
	return api.RequireRole(RoleEngineer, workspaceIDParam, next)
}

// Middleware resolves the caller of every request that does not match one
// of excludedPrefixes (an exact path, or a prefix ending in "/") and, when
// it finds one, stores it for FromRequest and api.CallerOf/api.Author to
// read. It never itself answers a request — an unresolved caller is simply
// absent from the context, and Require/RequireWorkspace/api.Author give
// the 401 — except that an unsafe (non-GET/HEAD/OPTIONS) request whose only
// credential came from the cookie or Basic auth (both ambient: a browser
// attaches them without the page asking) and that http.
// CrossOriginProtection judges cross-origin is treated as if it had no
// credential at all (Review Focus 2). A credential read from an
// Authorization: Bearer header is not ambient — a cross-site page cannot
// attach an arbitrary header to a simple request — so it is exempt from
// that check.
func (m *Manager) Middleware(excludedPrefixes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, p := range excludedPrefixes {
				// p == "/" is deliberately an *exact* match only (the
				// first clause), never a prefix match: every request
				// path starts with "/", so letting a bare "/" act as a
				// prefix (it ends in "/", so the second clause would
				// otherwise accept it) bypassed credential resolution for
				// every single request to every path — found empirically
				// while applying this plan to a scratch copy and running
				// internal/server's real end-to-end tests
				// (TestAPINeedsSignIn, TestPushNeedsRole) against a real
				// HTTP server: both failed because no caller was ever
				// resolved, for any request, once "/" was in
				// excludedPrefixes — exactly the value cmd/custos/serve.go
				// passes in production. This was a critical bug: as
				// originally written, authentication was a no-op for
				// every request to a real custos serve. Only a longer
				// directory-style prefix (none in current use besides
				// "/") is still eligible for prefix matching.
				if r.URL.Path == p || (p != "/" && strings.HasSuffix(p, "/") && strings.HasPrefix(r.URL.Path, p)) {
					next.ServeHTTP(w, r)
					return
				}
			}
			if p, ambient, ok := m.resolve(r); ok {
				unsafe := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
				if !ambient || !unsafe || m.cop.Check(r) == nil {
					r = r.WithContext(api.WithCaller(r.Context(), p))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// resolve extracts a credential from r, in order: Authorization: Bearer
// (explicit, not ambient), Authorization: Basic with the token as the
// password (what git sends; ambient — a browser that cached it resends it
// to the same origin on its own), then the session cookie (ambient).
func (m *Manager) resolve(r *http.Request) (p Principal, ambient bool, ok bool) {
	if v := r.Header.Get("Authorization"); strings.HasPrefix(v, "Bearer ") {
		p, ok = m.lookupToken(strings.TrimPrefix(v, "Bearer "))
		return p, false, ok
	}
	if _, pass, basicOK := r.BasicAuth(); basicOK {
		p, ok = m.lookupToken(pass)
		return p, true, ok
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return Principal{}, false, false
	}
	claims, err := m.verify(c.Value, "session")
	if err != nil || claims.Issuer != m.issuer {
		return Principal{}, false, false
	}
	users, err := m.loadUsers()
	if err != nil {
		return Principal{}, false, false
	}
	u, ok := users[claims.Email]
	if !ok {
		return Principal{}, false, false
	}
	return m.principal(claims.Email, u, claims.Subject), true, true
}
```

Workspace-id validation is not duplicated here: `RequireWorkspace` passes whatever `r.PathValue(workspaceIDParam)` returns straight to `Roles.Has`, and every handler downstream (`api.workspaceID`) already 404s a value that is not a real workspace UUID before it would matter.

- [ ] **Step 6: Run every new test**

Run: `go test -race ./internal/auth/ -run 'TestSign|TestSession|TestSetAndClear|TestMiddleware|TestRequire' -v`
Expected: PASS, including `TestSessionTampering` and `TestMiddlewareCrossOrigin` (Review Focus 1 and 2).

- [ ] **Step 7: Run the package and commit**

Run: `go test -race ./internal/auth/`
Expected: PASS.

```bash
gofmt -l internal/auth
go vet ./internal/auth
git add internal/auth/session.go internal/auth/session_test.go internal/auth/middleware.go internal/auth/middleware_test.go
git commit -m "Resolve the caller of every request from a session cookie or token, and gate writes by role

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 4 — `internal/auth`: the OIDC authorization-code flow

**Files:**
- Create: `internal/auth/oidc.go`
- Modify: `internal/auth/auth.go` (`Manager` gains an `oidc *oidcClient` field; `NewManager`'s OIDC stub from Task 2 is replaced with the real discovery call)
- Test: `internal/auth/oidc_test.go`, `internal/auth/idp_test.go`

**Interfaces:**
- Consumes: `github.com/coreos/go-oidc/v3/oidc`, `golang.org/x/oauth2` (both promoted to direct requirements by this task — Global Constraints); `Manager.sign`/`verify`/`setCookie`/`clearCookie` (Task 3); `Manager.UpdateUser` (Task 2).
- Produces (package `auth`, all unexported — reached only through `Register` in Task 5): `oidcClient`, `newOIDCClient`, `(*Manager).handleLogin`, `(*Manager).handleCallback`.

**Why a hand-rolled test IdP instead of `oidc/oidctest`.** The Tech Stack line names `github.com/coreos/go-oidc/v3/oidc/oidctest` for "the test identity provider's discovery document and JWKS". That package's exact surface in the vendored v3.20.0 could not be confirmed while writing this plan (no network access from the planning session), and guessing wrong would cost the implementer more than writing the ~80 lines by hand: `idp_test.go` below builds the discovery document, JWKS and token endpoint entirely from `net/http/httptest` plus `crypto/rsa`, so it depends on nothing but the standard library. **If `oidctest` turns out to offer a ready-made discovery/JWKS server, Step 1 should use it instead and this file shrinks** — flagged in "Decisions beyond the architecture note" and as an open question for the cross-plan reviewer. Either way, `oidc.go` itself (the production code) is unaffected: it only ever calls the public `oidc.NewProvider` / `(*oidc.Provider).Verifier` / `(*oidc.IDTokenVerifier).Verify`, which is the part the architecture note requires to be real.

- [ ] **Step 1: Write the test identity provider** `internal/auth/idp_test.go`

```go
package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// testIDP is a minimal OIDC provider: a discovery document, one RSA
// signing key published as a JWKS, and a token endpoint that issues a real
// RS256-signed ID token for whatever claims the test arms it with. It
// exists so oidc.go runs against coreos/go-oidc's real verification
// (signature over the JWKS key, issuer, audience, expiry, nonce) instead
// of a mocked oidc.Provider (Tech Stack; Review Focus 1).
type testIDP struct {
	srv *httptest.Server
	key *rsa.PrivateKey
	kid string

	code   string         // armed by setCode; "" refuses every exchange
	claims map[string]any // merged over iss/aud/iat/exp for the next /token response
}

func newTestIDP(t *testing.T) *testIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &testIDP{key: key, kid: "test-key-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", idp.discovery)
	mux.HandleFunc("GET /jwks", idp.jwks)
	mux.HandleFunc("POST /token", idp.token)
	// /authorize is never hit: these tests drive handleCallback directly
	// with a code they armed, the way a browser redirect cannot be driven
	// from a test without a real browser.
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (idp *testIDP) issuer() string { return idp.srv.URL }

func (idp *testIDP) discovery(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                 idp.issuer(),
		"authorization_endpoint":                 idp.issuer() + "/authorize",
		"token_endpoint":                         idp.issuer() + "/token",
		"jwks_uri":                                idp.issuer() + "/jwks",
		"response_types_supported":                []string{"code"},
		"subject_types_supported":                  []string{"public"},
		"id_token_signing_alg_values_supported":    []string{"RS256"},
		"scopes_supported":                          []string{"openid", "profile", "email"},
		"token_endpoint_auth_methods_supported":     []string{"client_secret_basic", "client_secret_post"},
		"claims_supported":                          []string{"sub", "email", "email_verified", "name"},
	})
}

func (idp *testIDP) jwks(w http.ResponseWriter, r *http.Request) {
	pub := idp.key.PublicKey
	json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA", "kid": idp.kid, "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	})
}

// setCode arms the token endpoint: the next POST /token presenting this
// code succeeds, returning an ID token whose claims are iss/aud/iat/exp
// (filled in here) merged with extra.
func (idp *testIDP) setCode(code string, extra map[string]any) { idp.code, idp.claims = code, extra }

func (idp *testIDP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || idp.code == "" || r.Form.Get("code") != idp.code {
		// The real OIDC/OAuth2 token endpoint always answers JSON with an
		// explicit Content-Type, on an error response too — found while
		// applying this plan: omitting it (so Go's server falls back to
		// sniffing, which does not recognize JSON and yields "text/plain")
		// makes golang.org/x/oauth2's AuthStyle auto-probe treat the
		// *first*, actually-successful exchange attempt as unparseable
		// and silently retry with the other auth style; the retry then
		// re-presents the same, by-then-already-consumed code and gets a
		// legitimate invalid_grant, which is the error that actually
		// surfaces to the caller instead of the real token.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	idp.code = "" // a code is used once
	// client_id arrives either in the form body (AuthStyleInParams) or as
	// the Basic auth username (AuthStyleInHeader) — golang.org/x/oauth2's
	// auth-style auto-probe tries the header style first, which succeeds
	// against this fixture (nothing here checks the client secret), so
	// the aud claim must be read from wherever it actually landed or
	// every real sign-in gets an empty audience and fails go-oidc's own
	// audience check; found while applying this plan.
	clientID := r.Form.Get("client_id")
	if clientID == "" {
		if u, _, ok := r.BasicAuth(); ok {
			clientID = u
		}
	}
	now := time.Now()
	claims := map[string]any{
		"iss": idp.issuer(),
		"aud": clientID,
		"iat": now.Unix(),
		"exp": now.Add(time.Minute).Unix(),
	}
	for k, v := range idp.claims {
		claims[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token": "test-access-token",
		"token_type":   "Bearer",
		"id_token":     idp.signJWT(claims),
		"expires_in":   60,
	})
}

// signJWT returns claims as a compact RS256 JWS, built by hand so the
// test needs no JOSE library: base64url(header) + "." + base64url(payload)
// + "." + base64url(signature).
func (idp *testIDP) signJWT(claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": idp.kid, "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, idp.key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err) // a 2048-bit key never fails to sign a SHA-256 digest
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}
```

- [ ] **Step 2: Write the failing tests** `internal/auth/oidc_test.go`

```go
package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newOIDCManager returns a Manager configured against idp.
func newOIDCManager(t *testing.T, idp *testIDP) *Manager {
	t.Helper()
	m, err := NewManager(Config{DataDir: t.TempDir(), OIDC: &OIDCConfig{
		IssuerURL: idp.issuer(), ClientID: "custos", ClientSecret: "s3cr3t",
		RedirectURL: "https://custos.example.org/auth/callback",
	}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestHandleLoginRedirectsWithPKCEAndSetsLoginCookie(t *testing.T) {
	idp := newTestIDP(t)
	m := newOIDCManager(t, idp)
	req := httptest.NewRequest("GET", "/auth/login", nil)
	rec := httptest.NewRecorder()
	m.handleLogin(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("code %d", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || !strings.HasPrefix(loc.String(), idp.issuer()+"/authorize") {
		t.Fatalf("Location %q", rec.Header().Get("Location"))
	}
	if loc.Query().Get("code_challenge") == "" || loc.Query().Get("code_challenge_method") != "S256" {
		t.Errorf("no PKCE challenge: %v", loc.Query())
	}
	if loc.Query().Get("state") == "" {
		t.Error("no state")
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != loginCookie {
		t.Fatalf("cookies %+v", cookies)
	}
}

func TestHandleLoginTokenOnlyMode(t *testing.T) {
	m := newManager(t) // Config.OIDC is nil
	rec := httptest.NewRecorder()
	m.handleLogin(rec, httptest.NewRequest("GET", "/auth/login", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("code %d", rec.Code)
	}
}

// runLogin drives handleLogin and returns the state/nonce/verifier it
// picked, by parsing its own redirect, so TestHandleCallback* below do not
// need to re-derive PKCE by hand.
func runLogin(t *testing.T, m *Manager) (loginCookieHeader string, state string) {
	t.Helper()
	rec := httptest.NewRecorder()
	m.handleLogin(rec, httptest.NewRequest("GET", "/auth/login", nil))
	loc, _ := url.Parse(rec.Header().Get("Location"))
	return rec.Result().Cookies()[0].String(), loc.Query().Get("state")
}

func callback(t *testing.T, m *Manager, loginCookieHeader, state, code string) *httptest.ResponseRecorder {
	t.Helper()
	// Built with url.Values, not raw string concatenation: TestOIDCCallbackRejects
	// below arms codes like "code-no email" (the map key, which contains a
	// space), and a literal space in a hand-built target string makes
	// httptest.NewRequest panic trying to parse it as a raw HTTP request
	// line — found while applying this plan.
	q := url.Values{"state": {state}, "code": {code}}
	req := httptest.NewRequest("GET", "/auth/callback?"+q.Encode(), nil)
	req.Header.Set("Cookie", loginCookieHeader)
	rec := httptest.NewRecorder()
	m.handleCallback(rec, req)
	return rec
}

func TestHandleCallbackSignsIn(t *testing.T) {
	idp := newTestIDP(t)
	m := newOIDCManager(t, idp)
	// Named loginCookieHeader, not loginCookie, so the comparison below
	// against the real cookie-name constant (package-level loginCookie,
	// session.go) isn't shadowed by this function's own local variable —
	// found while applying this plan: as "loginCookie, state :=
	// runLogin(...)" the local shadowed the constant, so
	// "c.Name == loginCookie" compared a short cookie name against a
	// whole Set-Cookie header string and could never be true, silently
	// defeating the "was cleared" check below.
	loginCookieHeader, state := runLogin(t, m)
	idp.setCode("the-code", map[string]any{
		"sub": "idp-sub-1", "email": "jane@example.org", "email_verified": true, "name": "Jane Doe",
		"nonce": nonceFromState(t, m, loginCookieHeader),
	})
	rec := callback(t, m, loginCookieHeader, state, "the-code")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("code %d location %q body %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			session = c
		}
		if c.Name == loginCookie && c.MaxAge >= 0 {
			t.Error("custos_login was not cleared")
		}
	}
	if session == nil {
		t.Fatal("no session cookie")
	}
	claims, err := m.verify(session.Value, "session")
	if err != nil || claims.Email != "jane@example.org" || claims.Subject != "idp-sub-1" {
		t.Fatalf("session claims %+v err %v", claims, err)
	}
	u, _ := m.loadUsers()
	if u["jane@example.org"].Name != "Jane Doe" || u["jane@example.org"].Subject != "idp-sub-1" {
		t.Errorf("user %+v", u["jane@example.org"])
	}
}

// TestOIDCCallbackRejects and TestOIDCCallbackState are Review Focus 1: an
// ID token signed with another key, for another audience, from another
// issuer, expired, or with another nonce must not create a session; nor
// must a replayed code, a changed state, or an IdP error parameter.
func TestOIDCCallbackRejects(t *testing.T) {
	idp := newTestIDP(t)
	for name, claims := range map[string]map[string]any{
		"unverified email": {"sub": "s", "email": "jane@example.org", "email_verified": false},
		"no email":         {"sub": "s", "email_verified": true},
		"wrong audience":   {"sub": "s", "email": "jane@example.org", "email_verified": true, "aud": "someone-else"},
	} {
		m := newOIDCManager(t, idp)
		loginCookie, state := runLogin(t, m)
		claims["nonce"] = nonceFromState(t, m, loginCookie)
		idp.setCode("code-"+name, claims)
		rec := callback(t, m, loginCookie, state, "code-"+name)
		if rec.Code == http.StatusFound {
			t.Errorf("%s: callback succeeded", name)
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == sessionCookie && c.Value != "" {
				t.Errorf("%s: a session cookie was set", name)
			}
		}
	}

	// A replayed authorization code: the second exchange must fail because
	// testIDP's /token consumes the code once, which stands in for
	// go-oidc/oauth2's own single-use exchange against the IdP.
	m := newOIDCManager(t, idp)
	loginCookie, state := runLogin(t, m)
	idp.setCode("reuse-me", map[string]any{"sub": "s", "email": "jane@example.org", "email_verified": true, "nonce": nonceFromState(t, m, loginCookie)})
	if rec := callback(t, m, loginCookie, state, "reuse-me"); rec.Code != http.StatusFound {
		t.Fatalf("first use: %d", rec.Code)
	}
	loginCookie2, state2 := runLogin(t, m)
	idp.claims["nonce"] = nonceFromState(t, m, loginCookie2) // same armed code, new login
	if rec := callback(t, m, loginCookie2, state2, "reuse-me"); rec.Code == http.StatusFound {
		t.Error("replayed code succeeded")
	}
}

func TestOIDCCallbackState(t *testing.T) {
	idp := newTestIDP(t)
	m := newOIDCManager(t, idp)
	loginCookie, _ := runLogin(t, m)
	idp.setCode("code", map[string]any{"sub": "s", "email": "jane@example.org", "email_verified": true, "nonce": nonceFromState(t, m, loginCookie)})
	if rec := callback(t, m, loginCookie, "not-the-real-state", "code"); rec.Code == http.StatusFound {
		t.Error("wrong state succeeded")
	}
	if rec := callback(t, m, "custos_login=garbage", "x", "code"); rec.Code == http.StatusFound {
		t.Error("tampered login cookie succeeded")
	}
	req := httptest.NewRequest("GET", "/auth/callback?error=access_denied", nil) // no custos_login cookie at all
	rec := httptest.NewRecorder()
	m.handleCallback(rec, req)
	if rec.Code == http.StatusFound {
		t.Error("no login cookie at all succeeded")
	}
}

// nonceFromState decodes the nonce the manager put in its own login
// cookie, so the test's armed claims can match it without the test
// reimplementing handleLogin's random generation. loginCookieHeader is the
// Set-Cookie value runLogin returned ("custos_login=<value>; Path=/; ...").
func nonceFromState(t *testing.T, m *Manager, loginCookieHeader string) string {
	t.Helper()
	_, value, _ := strings.Cut(loginCookieHeader, loginCookie+"=")
	value, _, _ = strings.Cut(value, ";")
	c, err := m.verify(value, "login")
	if err != nil {
		t.Fatal(err)
	}
	return c.Nonce
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/auth/ -run 'TestHandleLogin|TestHandleCallback|TestOIDCCallback'`
Expected: FAIL — `undefined: m.handleLogin` (and `handleCallback`, `NewManager` still refusing `cfg.OIDC != nil` from Task 2's stub).

- [ ] **Step 4: Implement** `internal/auth/oidc.go`

```go
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// loginTTL bounds how long a sign-in may take before custos_login expires.
const loginTTL = 10 * time.Minute

// oidcClient is nil on a Manager built without Config.OIDC (token-only
// mode); handleLogin and handleCallback check for that themselves.
type oidcClient struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth2   oauth2.Config
}

// newOIDCClient discovers cfg.IssuerURL and builds the oauth2/go-oidc
// client. NewManager calls it once, at start-up, so a bad issuer URL or an
// unreachable IdP fails custos serve immediately rather than at the first
// /auth/login.
func newOIDCClient(ctx context.Context, cfg OIDCConfig) (*oidcClient, error) {
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("auth: OIDC discovery at %s: %w", cfg.IssuerURL, err)
	}
	return &oidcClient{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth2: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
	}, nil
}

// randString returns n random bytes, base64url-encoded, for state, nonce
// and (through oauth2.GenerateVerifier) the PKCE verifier.
func randString(n int) string {
	b := make([]byte, n)
	rand.Read(b) // crypto/rand never fails since Go 1.24
	return base64.RawURLEncoding.EncodeToString(b)
}

// handleLogin starts the authorization-code flow with PKCE: state, nonce
// and the PKCE verifier are generated, signed into custos_login, and the
// browser is sent to the IdP. 404 in token-only mode (Endpoints table).
func (m *Manager) handleLogin(w http.ResponseWriter, r *http.Request) {
	if m.oidc == nil {
		http.Error(w, "OIDC sign-in is not configured on this server", http.StatusNotFound)
		return
	}
	state, nonce, verifier := randString(24), randString(24), oauth2.GenerateVerifier()
	returnTo := "/"
	if v := r.URL.Query().Get("return_to"); strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		returnTo = v // "//host/.." would be an open redirect; everything else under "/" is the SPA's own routes
	}
	m.setCookie(w, loginCookie, m.sign(claims{
		Kind: "login", State: state, Nonce: nonce, Verifier: verifier, Issuer: m.issuer,
		Return: returnTo, Exp: m.now().Add(loginTTL).Unix(),
	}), loginTTL)
	url := m.oidc.oauth2.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, url, http.StatusFound)
}

// handleCallback exchanges the authorization code, verifies the ID token
// through go-oidc's real verification path (issuer, audience, signature
// against the IdP's published JWKS, expiry — Review Focus 1), checks state
// and nonce against custos_login, upserts the user's name and OIDC subject
// into users.json (roles untouched — the architecture note), starts a
// session and clears custos_login.
func (m *Manager) handleCallback(w http.ResponseWriter, r *http.Request) {
	if m.oidc == nil {
		http.Error(w, "OIDC sign-in is not configured on this server", http.StatusNotFound)
		return
	}
	cookie, err := r.Cookie(loginCookie)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "no sign-in in progress; start at /auth/login")
		return
	}
	lc, err := m.verify(cookie.Value, "login")
	if err != nil || lc.Issuer != m.issuer {
		writeAuthError(w, http.StatusBadRequest, "sign-in state is missing, expired, or does not match this server")
		return
	}
	m.clearCookie(w, loginCookie)
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(lc.State)) != 1 {
		writeAuthError(w, http.StatusBadRequest, "state parameter does not match the sign-in that was started")
		return
	}
	if msg := r.URL.Query().Get("error"); msg != "" {
		writeAuthError(w, http.StatusUnauthorized, "identity provider refused sign-in: "+msg)
		return
	}
	token, err := m.oidc.oauth2.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(lc.Verifier))
	if err != nil {
		writeAuthError(w, http.StatusBadGateway, fmt.Sprintf("exchanging the authorization code: %v", err))
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		writeAuthError(w, http.StatusBadGateway, "identity provider did not return an id_token")
		return
	}
	idToken, err := m.oidc.verifier.Verify(r.Context(), raw)
	if err != nil {
		writeAuthError(w, http.StatusUnauthorized, fmt.Sprintf("invalid ID token: %v", err))
		return
	}
	if idToken.Nonce != lc.Nonce {
		writeAuthError(w, http.StatusUnauthorized, "ID token nonce does not match the sign-in that was started")
		return
	}
	var oc struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idToken.Claims(&oc); err != nil {
		writeAuthError(w, http.StatusUnauthorized, fmt.Sprintf("reading ID token claims: %v", err))
		return
	}
	if oc.Email == "" || !oc.EmailVerified {
		writeAuthError(w, http.StatusForbidden, "the identity provider did not report a verified email address")
		return
	}
	email, err := normalizeEmail(oc.Email)
	if err != nil {
		writeAuthError(w, http.StatusForbidden, err.Error())
		return
	}
	if _, err := m.UpdateUser(email, func(u *User) {
		if oc.Name != "" {
			u.Name = oc.Name
		}
		u.Subject = idToken.Subject
	}); err != nil {
		writeAuthError(w, http.StatusForbidden, fmt.Sprintf("%s cannot be used as a commit author: %v", email, err))
		return
	}
	m.setCookie(w, sessionCookie, m.sign(claims{
		Kind: "session", Email: email, Subject: idToken.Subject, Issuer: m.issuer,
		Exp: m.now().Add(m.ttl).Unix(),
	}), m.ttl)
	http.Redirect(w, r, lc.Return, http.StatusFound)
}

// writeAuthError writes a JSON error body in the same shape as
// api.WriteError, without importing api's unexported httpError (auth
// already imports api for Register and the Caller seam, so this is just
// avoiding one more unexported dependency, not a cycle concern).
func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}
```

- [ ] **Step 5: Wire `oidc` into `Manager`** — edit `internal/auth/auth.go`

Add a field to the struct (next to `now`):

```go
	oidc *oidcClient // nil in token-only mode (Config.OIDC == nil)
```

Replace the stub at the end of `NewManager`:

```go
	if cfg.OIDC != nil {
		// Replaced in Task 4 by the OIDC client; nothing passes OIDC before then.
		return nil, errors.New("auth: OIDC sign-in is not available yet")
	}
	return m, nil
}
```

with:

```go
	if cfg.OIDC != nil {
		client, err := newOIDCClient(context.Background(), *cfg.OIDC)
		if err != nil {
			return nil, err
		}
		m.oidc = client
		m.issuer = cfg.OIDC.IssuerURL
		m.secure = strings.HasPrefix(cfg.OIDC.RedirectURL, "https://")
	}
	return m, nil
}
```

(`"context"` joins `auth.go`'s imports; `"errors"` stays — `NewManager`'s own `cfg.DataDir == ""` check still uses it.)

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./internal/auth/ -run 'TestHandleLogin|TestHandleCallback|TestOIDCCallback' -v`
Expected: PASS.

- [ ] **Step 7: Run the package and commit**

Run: `go test -race ./internal/auth/`
Expected: PASS.

```bash
gofmt -l internal/auth
go vet ./internal/auth
go mod tidy  # promotes go-oidc and oauth2 to direct requirements
git add internal/auth/oidc.go internal/auth/oidc_test.go internal/auth/idp_test.go internal/auth/auth.go go.mod go.sum
git commit -m "Sign in through an organization's OIDC identity provider

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 5 — `internal/auth/http.go`: `Register`, `/auth/*` and the admin endpoints

**Files:**
- Create: `internal/auth/http.go`
- Test: `internal/auth/http_test.go`

**Interfaces:**
- Consumes: `api.API.Handle` (Task 1 of phase 2, unchanged); `Manager.handleLogin`/`handleCallback` (Task 4); `Manager.loadUsers`/`UpdateUser`/`CreateToken`/`listTokens`/`revokeToken`/`isAdminEmail` (Task 2); `FromRequest`/`Require` (Task 3).
- Produces (package `auth`): `func Register(a *api.API, m *Manager)`; unexported `requireSignedIn`, `decodeAuthJSON`, `rolesUpdate`, and the handler methods in the Endpoints table below.

**Endpoints** (full text already fixed in this plan's own "Endpoints this plan adds or guards" table; repeated here only for the response/request shapes `Register` implements):

| Method and path | Guard | Body in | Body out |
| --- | --- | --- | --- |
| `GET /auth/login`, `GET /auth/callback` | none (Task 4 checks `m.oidc` itself) | — | — |
| `POST /auth/logout` | none | — | 204 |
| `GET /auth/me` | `requireSignedIn` | — | `{"name","email","roles":Roles}` |
| `GET /auth/tokens` | `requireSignedIn` | — | `[{"id","label","created","last_used"}]`, never `null` |
| `POST /auth/tokens` | `requireSignedIn` | `{"label"}` | 201 `{"id","token"}`, `Cache-Control: no-store` |
| `DELETE /auth/tokens/{id}` | `requireSignedIn` | — | 204 |
| `GET /api/admin/users` | `Require(RoleAdmin, ...)` | — | `[{"email","name","roles":Roles,"admin_by_config"}]` sorted by email |
| `PUT /api/admin/users/{email}/roles` | `Require(RoleAdmin, ...)` | `rolesUpdate` (each field optional — a merge, not a replace) | `Roles` |

`Roles` (Task 2) already carries the JSON names `admin`/`task_author`/`processor_author`/`engineer` the Endpoints table promises, so `/auth/me` and the admin endpoints marshal it directly — no separate `rolesJSON` wrapper type.

- [ ] **Step 1: Write the failing tests** `internal/auth/http_test.go`

```go
package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/api"
)

// handler builds the mux Register adds to, with one extra route
// (GET /whoami) that reports the caller api.CallerOf finds — the simplest
// way to drive requireSignedIn-gated and Require-gated handlers through a
// real http.Handler without a full server.Server.
func testHandler(t *testing.T, m *Manager) http.Handler {
	t.Helper()
	a := api.New(nil, nil) //lint:ignore -- these endpoints never touch a.st/a.bl
	Register(a, m)
	return a.Handler()
}

// signedRequest returns a request carrying ctx's caller already injected
// via api.WithCaller — the same shortcut internal/api's own tests use —
// standing in for a full session cookie or token, since these tests are
// about Register's own endpoints, not about Middleware (Task 3 already
// covers resolving a cookie or token into a caller).
func signedRequest(method, path, body string, p Principal) *http.Request {
	var r *strings.Reader
	if body != "" {
		r = strings.NewReader(body)
	} else {
		r = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req.WithContext(api.WithCaller(req.Context(), p))
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder, want int) T {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status %d, want %d; body %s", rec.Code, want, rec.Body)
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return v
}

// TestHandleConfig: unauthenticated, always 200, and the oidc field tracks
// whether the Manager was built with Config.OIDC set — this is the signal
// the frontend uses to tell token-only mode apart from a plain 401
// (cross-plan review of 4a/4c; architecture note ruling 18), so /auth/me
// itself never needs to vary its status code for that purpose.
func TestHandleConfig(t *testing.T) {
	m := newManager(t) // token-only: Config.OIDC is nil
	h := testHandler(t, m)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/auth/config", nil))
	got := decodeBody[struct {
		OIDC bool `json:"oidc"`
	}](t, rec, http.StatusOK)
	if got.OIDC {
		t.Errorf("token-only manager: /auth/config oidc = true")
	}
}

func TestHandleMe(t *testing.T) {
	m := newManager(t)
	h := testHandler(t, m)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/auth/me", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", rec.Code)
	}

	p := Principal{Email: "jane@example.org", Name: "Jane Doe", Roles: Roles{TaskAuthor: true}}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("GET", "/auth/me", "", p))
	got := decodeBody[struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Roles Roles  `json:"roles"`
	}](t, rec, http.StatusOK)
	if got.Name != "Jane Doe" || got.Email != "jane@example.org" || !got.Roles.TaskAuthor {
		t.Errorf("me %+v", got)
	}
}

func TestHandleLogout(t *testing.T) {
	m := newManager(t)
	h := testHandler(t, m)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/auth/logout", nil)) // anyone, even signed out
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code %d", rec.Code)
	}
	cleared := rec.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != sessionCookie || cleared[0].MaxAge >= 0 {
		t.Errorf("cookies %+v", cleared)
	}
}

func TestTokenEndpoints(t *testing.T) {
	m := newManager(t)
	addUser(t, m, "jane@example.org", "Jane Doe", Roles{})
	h := testHandler(t, m)
	jane := Principal{Email: "jane@example.org"}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("POST", "/auth/tokens", `{"label":"laptop"}`, jane))
	created := decodeBody[struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}](t, rec, http.StatusCreated)
	if created.ID == "" || created.Token == "" {
		t.Fatalf("created %+v", created)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("missing Cache-Control: no-store on a response that carries a secret")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("GET", "/auth/tokens", "", jane))
	list := decodeBody[[]struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}](t, rec, http.StatusOK)
	// addUser (users_test.go/tokens_test.go, Task 2) already mints one
	// token labelled "test" as its own side effect, so the list also
	// carries that entry alongside "laptop" — found while applying this
	// plan: an earlier version of this assertion expected exactly one
	// entry, which only held if addUser minted none.
	found := false
	for _, tok := range list {
		if tok.ID == created.ID && tok.Label == "laptop" {
			found = true
		}
	}
	if len(list) != 2 || !found {
		t.Fatalf("list %+v, want 2 entries including {%s laptop}", list, created.ID)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("DELETE", "/auth/tokens/"+created.ID, "", jane))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("DELETE", "/auth/tokens/"+created.ID, "", jane))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d", rec.Code)
	}

	// A token cannot be created for anyone but the caller: the label-only
	// body has nowhere to name another user, and the handler always uses
	// the caller's own email, so this is enforced by construction, not by
	// an explicit check — documented here so a later refactor notices if
	// it adds an "email" field to the request body without re-deriving it
	// from the caller.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("POST", "/auth/tokens", `{"label":""}`, jane))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty label: %d", rec.Code)
	}
}

// TestPutRoles is Review Focus 6: an admin's PUT merges into a user's
// roles — including a user who has never signed in — and takes effect for
// the very next request (nothing here is cached; proven by reading
// loadUsers directly afterwards, with no server restart in between).
func TestPutRoles(t *testing.T) {
	m := newManager(t)
	h := testHandler(t, m)
	admin := Principal{Email: "admin@example.org", Roles: Roles{Admin: true}}
	notAdmin := Principal{Email: "jane@example.org", Roles: Roles{TaskAuthor: true}}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("PUT", "/api/admin/users/new@example.org/roles", `{"task_author":true,"engineer":["`+ws1+`"]}`, notAdmin))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("PUT", "/api/admin/users/new@example.org/roles", `{"task_author":true,"engineer":["`+ws1+`"]}`, admin))
	got := decodeBody[Roles](t, rec, http.StatusOK)
	if !got.TaskAuthor || !slices.Equal(got.Engineer, []string{ws1}) {
		t.Fatalf("roles %+v", got)
	}

	// A second PUT that sets only admin leaves task_author and engineer
	// alone: this is a merge, not a replace.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("PUT", "/api/admin/users/new@example.org/roles", `{"admin":true}`, admin))
	got = decodeBody[Roles](t, rec, http.StatusOK)
	if !got.Admin || !got.TaskAuthor || len(got.Engineer) != 1 {
		t.Fatalf("merged roles %+v", got)
	}

	users, _ := m.loadUsers()
	if !users["new@example.org"].Roles.Admin {
		t.Error("the change was not on disk straight after the request")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("PUT", "/api/admin/users/not-an-email/roles", `{}`, admin))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad email: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("PUT", "/api/admin/users/jane@example.org/roles", `{"engineer":["not-a-uuid"]}`, admin))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad engineer entry: %d", rec.Code)
	}
}

func TestHandleListUsers(t *testing.T) {
	m, err := NewManager(Config{DataDir: t.TempDir(), AdminEmails: []string{"root@example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	addUser(t, m, "jane@example.org", "Jane Doe", Roles{TaskAuthor: true})
	addUser(t, m, "root@example.org", "Root", Roles{})
	h := testHandler(t, m)
	admin := Principal{Email: "root@example.org", Roles: Roles{Admin: true}}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("GET", "/api/admin/users", "", admin))
	list := decodeBody[[]struct {
		Email         string `json:"email"`
		AdminByConfig bool   `json:"admin_by_config"`
	}](t, rec, http.StatusOK)
	if len(list) != 2 || list[0].Email != "jane@example.org" || list[1].Email != "root@example.org" || !list[1].AdminByConfig {
		t.Fatalf("users %+v", list)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/auth/ -run 'TestHandleConfig|TestHandleMe|TestHandleLogout|TestTokenEndpoints|TestPutRoles|TestHandleListUsers'`
Expected: FAIL — `undefined: Register`.

- [ ] **Step 3: Implement** `internal/auth/http.go`

```go
package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/emeland-io/custos/internal/api"
)

// maxAuthBody bounds the JSON bodies of these endpoints: a token's label
// and a role update are both short.
const maxAuthBody = 4 << 10

// Register adds /auth/* and the admin endpoints to a (full paths, not
// below /api/ for the /auth/* ones — see the architecture note and this
// plan's server wiring in Task 8). Call it before a.Handler(), even in
// token-only mode: only handleLogin/handleCallback 404 then, which they
// check themselves.
func Register(a *api.API, m *Manager) {
	a.Handle("GET /auth/login", m.handleLogin)
	a.Handle("GET /auth/callback", m.handleCallback)
	a.Handle("POST /auth/logout", m.handleLogout)
	a.Handle("GET /auth/config", m.handleConfig)
	a.Handle("GET /auth/me", requireSignedIn(m.handleMe))
	a.Handle("GET /auth/tokens", requireSignedIn(m.handleListTokens))
	a.Handle("POST /auth/tokens", requireSignedIn(m.handleCreateToken))
	a.Handle("DELETE /auth/tokens/{id}", requireSignedIn(m.handleRevokeToken))
	a.Handle("GET /api/admin/users", Require(RoleAdmin, m.handleListUsers))
	a.Handle("PUT /api/admin/users/{email}/roles", Require(RoleAdmin, m.handlePutRoles))
}

// requireSignedIn wraps next so it 401s before next runs when the request
// carries no caller. Unlike Require/RequireWorkspace it asks for no
// particular role: GET /auth/me and /auth/tokens are for anyone signed in.
func requireSignedIn(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := FromRequest(r); !ok {
			writeAuthError(w, http.StatusUnauthorized,
				`not signed in: sign in at /auth/login, or send a personal access token as "Authorization: Bearer <token>"`)
			return
		}
		next(w, r)
	}
}

// decodeAuthJSON decodes r's body into v, bounded to maxAuthBody and
// rejecting unknown fields, writing the matching error status itself on
// failure (so callers just return on a non-nil error).
func decodeAuthJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAuthBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeAuthError(w, status, "request body: "+err.Error())
		return err
	}
	return nil
}

func (m *Manager) handleLogout(w http.ResponseWriter, r *http.Request) {
	m.clearCookie(w, sessionCookie)
	w.WriteHeader(http.StatusNoContent)
}

// handleConfig answers whether /auth/login is usable, unauthenticated and
// always 200 — the signal the frontend uses to tell "sign in please" (OIDC
// configured, a plain 401 from handleMe means not yet signed in) apart
// from "there is no sign-in here, paste a token" (token-only mode), rather
// than overloading /auth/me's status code for both meanings (cross-plan
// review of 4a/4c; architecture note ruling 18).
func (m *Manager) handleConfig(w http.ResponseWriter, r *http.Request) {
	api.WriteJSON(w, http.StatusOK, map[string]any{"oidc": m.oidc != nil})
}

func (m *Manager) handleMe(w http.ResponseWriter, r *http.Request) {
	p, _ := FromRequest(r) // requireSignedIn already guarantees ok
	api.WriteJSON(w, http.StatusOK, map[string]any{"name": p.Name, "email": p.Email, "roles": p.Roles})
}

func (m *Manager) handleListTokens(w http.ResponseWriter, r *http.Request) {
	p, _ := FromRequest(r)
	list, err := m.listTokens(p.Email)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, list)
}

func (m *Manager) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	p, _ := FromRequest(r)
	var body struct {
		Label string `json:"label"`
	}
	if decodeAuthJSON(w, r, &body) != nil {
		return
	}
	id, token, err := m.CreateToken(p.Email, body.Label)
	if err != nil {
		writeAuthError(w, httpStatusForAuthErr(err), err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	api.WriteJSON(w, http.StatusCreated, map[string]string{"id": id, "token": token})
}

func (m *Manager) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	p, _ := FromRequest(r)
	if err := m.revokeToken(p.Email, r.PathValue("id")); err != nil {
		if errors.Is(err, errNoToken) {
			writeAuthError(w, http.StatusNotFound, "no such token")
			return
		}
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Manager) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := m.loadUsers()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type userJSON struct {
		Email         string `json:"email"`
		Name          string `json:"name"`
		Roles         Roles  `json:"roles"`
		AdminByConfig bool   `json:"admin_by_config"`
	}
	out := make([]userJSON, 0, len(users))
	for email, u := range users {
		out = append(out, userJSON{Email: email, Name: u.Name, Roles: u.Roles.normalized(), AdminByConfig: m.isAdminEmail(email)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	api.WriteJSON(w, http.StatusOK, out)
}

// rolesUpdate is PUT /api/admin/users/{email}/roles's body: every field is
// optional, and only the ones present are merged into the user's roles
// (architecture note: "merges into that user's record").
type rolesUpdate struct {
	Admin           *bool     `json:"admin,omitempty"`
	TaskAuthor      *bool     `json:"task_author,omitempty"`
	ProcessorAuthor *bool     `json:"processor_author,omitempty"`
	Engineer        *[]string `json:"engineer,omitempty"`
}

func (m *Manager) handlePutRoles(w http.ResponseWriter, r *http.Request) {
	var body rolesUpdate
	if decodeAuthJSON(w, r, &body) != nil {
		return
	}
	u, err := m.UpdateUser(r.PathValue("email"), func(u *User) {
		if body.Admin != nil {
			u.Roles.Admin = *body.Admin
		}
		if body.TaskAuthor != nil {
			u.Roles.TaskAuthor = *body.TaskAuthor
		}
		if body.ProcessorAuthor != nil {
			u.Roles.ProcessorAuthor = *body.ProcessorAuthor
		}
		if body.Engineer != nil {
			u.Roles.Engineer = *body.Engineer
		}
	})
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, u.Roles)
}

// httpStatusForAuthErr maps CreateToken's ErrInvalid to 400; anything else
// (disk trouble) is a 500.
func httpStatusForAuthErr(err error) int {
	if errors.Is(err, ErrInvalid) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}
```

`api.New(nil, nil)` in the test helper above is safe only because none of `Register`'s own handlers touch the store or blob store it would otherwise need — `requireSignedIn`/`Require` run first and every handler after them only calls `Manager` methods. A later plan that adds more routes to the same `*api.API` in production always passes real stores; this shortcut is local to `http_test.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/auth/ -run 'TestHandleConfig|TestHandleMe|TestHandleLogout|TestTokenEndpoints|TestPutRoles|TestHandleListUsers' -v`
Expected: PASS.

- [ ] **Step 5: Run the package and commit**

Run: `go test -race ./internal/auth/`
Expected: PASS.

```bash
gofmt -l internal/auth
go vet ./internal/auth
git add internal/auth/http.go internal/auth/http_test.go
git commit -m "Serve /auth/me, personal access tokens and admin role management

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 6 — `cmd/custos`: `serve`'s auth flags

**Files:**
- Create: `cmd/custos/auth.go`
- Test: `cmd/custos/auth_test.go`

**Interfaces:**
- Consumes: `auth.Config`, `auth.OIDCConfig` (Task 2).
- Produces (package `main`): `type authFlagSet`; `func defineAuthFlags(fl *flag.FlagSet) *authFlagSet`; `func (f *authFlagSet) config(publicURL string) (auth.Config, error)`.

This task only defines and unit-tests the flags and the `auth.Config` they build; nothing calls it yet (Task 8 wires it into `runServe`/`openServer`), so it has no dependency on `internal/server`'s changes and can be written and tested in isolation.

**Flags** (architecture note, "Flags of `serve` (3d)"-style kebab-case/`CUSTOS_<UPPER_SNAKE>` convention, already used by every other `serve` flag in this file's sibling `serve.go`):

| Flag | Env | Default | Meaning |
| --- | --- | --- | --- |
| `--oidc-issuer-url` | `CUSTOS_OIDC_ISSUER_URL` | empty | empty runs token-only mode (ruling 4.11) |
| `--oidc-client-id` | `CUSTOS_OIDC_CLIENT_ID` | empty | required with `--oidc-issuer-url` |
| `--oidc-client-secret` | `CUSTOS_OIDC_CLIENT_SECRET` | empty | required with `--oidc-issuer-url` |
| `--oidc-redirect-url` | `CUSTOS_OIDC_REDIRECT_URL` | `<public-url>/auth/callback` | |
| `--admin-emails` | `CUSTOS_ADMIN_EMAILS` | empty | comma-separated |
| `--session-secret` | `CUSTOS_SESSION_SECRET` | empty (random) | hex, at least 32 bytes |

- [ ] **Step 1: Write the failing tests** `cmd/custos/auth_test.go`

```go
package main

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/auth"
)

func TestAuthConfigTokenOnlyMode(t *testing.T) {
	_, err := parseAuthFlagsConfig(t)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuthConfigOIDC(t *testing.T) {
	cfg, err := parseAuthFlagsConfig(t,
		"--oidc-issuer-url", "https://idp.example.org",
		"--oidc-client-id", "custos",
		"--oidc-client-secret", "s3cr3t",
		"--admin-emails", " Jane@Example.org , ,bob@example.org",
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OIDC == nil || cfg.OIDC.IssuerURL != "https://idp.example.org" || cfg.OIDC.RedirectURL != "http://127.0.0.1:8080/auth/callback" {
		t.Fatalf("OIDC %+v", cfg.OIDC)
	}
	if len(cfg.AdminEmails) != 2 || cfg.AdminEmails[0] != "Jane@Example.org" || cfg.AdminEmails[1] != "bob@example.org" {
		t.Fatalf("AdminEmails %+v", cfg.AdminEmails)
	}
}

func TestAuthConfigRedirectURLOverride(t *testing.T) {
	cfg, err := parseAuthFlagsConfig(t,
		"--oidc-issuer-url", "https://idp.example.org", "--oidc-client-id", "c", "--oidc-client-secret", "s",
		"--oidc-redirect-url", "https://custos.example.org/auth/callback",
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OIDC.RedirectURL != "https://custos.example.org/auth/callback" {
		t.Errorf("RedirectURL %q", cfg.OIDC.RedirectURL)
	}
}

func TestAuthConfigRejects(t *testing.T) {
	for name, args := range map[string][]string{
		"issuer without client id":     {"--oidc-issuer-url", "https://idp.example.org", "--oidc-client-secret", "s"},
		"issuer without client secret": {"--oidc-issuer-url", "https://idp.example.org", "--oidc-client-id", "c"},
		"client id without issuer":     {"--oidc-client-id", "c"},
		"redirect url without issuer":  {"--oidc-redirect-url", "https://x/callback"},
		"session secret too short":     {"--session-secret", "ab"},
		"session secret not hex":       {"--session-secret", strings.Repeat("zz", 32)},
	} {
		if _, err := parseAuthFlagsConfig(t, args...); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestAuthConfigSessionSecret(t *testing.T) {
	cfg, err := parseAuthFlagsConfig(t, "--session-secret", strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SessionSecret) != 32 {
		t.Errorf("SessionSecret %d bytes", len(cfg.SessionSecret))
	}
}

// parseAuthFlagsConfig builds an authFlagSet the way runServe will in
// Task 8, without needing the rest of serve's flags, and returns its
// built Config.
func parseAuthFlagsConfig(t *testing.T, args ...string) (auth.Config, error) {
	t.Helper()
	fl := flagSetForTest(t)
	af := defineAuthFlags(fl)
	if err := fl.Parse(args); err != nil {
		return auth.Config{}, err
	}
	return af.config("http://127.0.0.1:8080")
}
```

`flagSetForTest(t)` does not exist yet either: add it as a two-line helper (`flag.NewFlagSet("test", flag.ContinueOnError)` with `fl.SetOutput(io.Discard)`) at the bottom of `auth_test.go` — every other flag-parsing test in this package (`serve_test.go`, `remote_test.go`) instead drives flags through `runCmd`, which already exists for whole subcommands; `authFlagSet` is one level below that (it is assembled by a subcommand's own `flag.FlagSet`, not a subcommand itself), so this one helper is new:

```go
func flagSetForTest(t *testing.T) *flag.FlagSet {
	t.Helper()
	fl := flag.NewFlagSet("test", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	return fl
}
```

(add `"flag"` and `"io"` to the import block above).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/custos/ -run TestAuthConfig`
Expected: FAIL — `undefined: defineAuthFlags`.

- [ ] **Step 3: Implement** `cmd/custos/auth.go`

```go
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/emeland-io/custos/internal/auth"
)

// defaultSessionTTL matches auth.Config's own default; named again here
// only because authFlagSet.config always sets SessionTTL explicitly
// (there is no --session-ttl flag yet — the architecture note does not
// ask for one, and nothing in this plan needs it configurable).
const defaultSessionTTL = 12 * time.Hour

// authFlagSet holds serve's auth flags (ruling 4.11: leaving
// --oidc-issuer-url empty runs custos in token-only mode, with no
// /auth/login).
type authFlagSet struct {
	issuerURL, clientID, clientSecret, redirectURL *string
	adminEmails, sessionSecret                     *string
}

// defineAuthFlags defines serve's auth flags on fl. Call it alongside
// publicURLFlag; its result's config method needs that flag's already-
// parsed value.
func defineAuthFlags(fl *flag.FlagSet) *authFlagSet {
	return &authFlagSet{
		issuerURL: fl.String("oidc-issuer-url", os.Getenv("CUSTOS_OIDC_ISSUER_URL"),
			"OIDC issuer URL; empty runs custos in token-only mode, with no /auth/login (env CUSTOS_OIDC_ISSUER_URL)"),
		clientID: fl.String("oidc-client-id", os.Getenv("CUSTOS_OIDC_CLIENT_ID"),
			"OIDC client id (env CUSTOS_OIDC_CLIENT_ID)"),
		clientSecret: fl.String("oidc-client-secret", os.Getenv("CUSTOS_OIDC_CLIENT_SECRET"),
			"OIDC client secret (env CUSTOS_OIDC_CLIENT_SECRET)"),
		redirectURL: fl.String("oidc-redirect-url", os.Getenv("CUSTOS_OIDC_REDIRECT_URL"),
			"URL the identity provider redirects back to; default <public-url>/auth/callback (env CUSTOS_OIDC_REDIRECT_URL)"),
		adminEmails: fl.String("admin-emails", os.Getenv("CUSTOS_ADMIN_EMAILS"),
			"comma-separated emails that always have the admin role, in addition to users.json (env CUSTOS_ADMIN_EMAILS)"),
		sessionSecret: fl.String("session-secret", os.Getenv("CUSTOS_SESSION_SECRET"),
			"hex-encoded HMAC key for session cookies, at least 32 bytes (64 hex digits); random and unique to this process if empty, so restarting forgets every session (env CUSTOS_SESSION_SECRET)"),
	}
}

// config builds an auth.Config from the parsed flags. publicURL is
// --public-url, already validated by checkPublicURL; it supplies
// --oidc-redirect-url's default.
func (f *authFlagSet) config(publicURL string) (auth.Config, error) {
	cfg := auth.Config{SessionTTL: defaultSessionTTL}
	if *f.sessionSecret != "" {
		secret, err := hex.DecodeString(*f.sessionSecret)
		if err != nil || len(secret) < 32 {
			return auth.Config{}, fmt.Errorf("--session-secret must be at least 32 bytes of hex (64 digits)")
		}
		cfg.SessionSecret = secret
	}
	for _, e := range strings.Split(*f.adminEmails, ",") {
		if e = strings.TrimSpace(e); e != "" {
			cfg.AdminEmails = append(cfg.AdminEmails, e)
		}
	}
	switch {
	case *f.issuerURL == "" && (*f.clientID != "" || *f.clientSecret != "" || *f.redirectURL != ""):
		return auth.Config{}, fmt.Errorf("--oidc-client-id, --oidc-client-secret and --oidc-redirect-url need --oidc-issuer-url")
	case *f.issuerURL == "":
		return cfg, nil // token-only mode
	case *f.clientID == "" || *f.clientSecret == "":
		return auth.Config{}, fmt.Errorf("--oidc-issuer-url needs --oidc-client-id and --oidc-client-secret")
	}
	redirect := *f.redirectURL
	if redirect == "" {
		redirect = strings.TrimRight(publicURL, "/") + "/auth/callback"
	}
	cfg.OIDC = &auth.OIDCConfig{IssuerURL: *f.issuerURL, ClientID: *f.clientID, ClientSecret: *f.clientSecret, RedirectURL: redirect}
	return cfg, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/custos/ -run TestAuthConfig -v`
Expected: PASS.

- [ ] **Step 5: Run the package and commit**

Run: `go test ./cmd/custos/`
Expected: PASS (nothing else changed — `defineAuthFlags` is not called from `runServe` yet).

```bash
gofmt -l cmd/custos
go vet ./cmd/custos
git add cmd/custos/auth.go cmd/custos/auth_test.go
git commit -m "Add serve's OIDC and session flags

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 7 — `cmd/custos token create`

**Files:**
- Create: `cmd/custos/token.go`
- Modify: `cmd/custos/main.go` (dispatch the new subcommand; usage text)
- Test: `cmd/custos/token_test.go`

**Interfaces:**
- Consumes: `auth.NewManager`, `auth.Config`, `(*auth.Manager).UpdateUser`, `(*auth.Manager).CreateToken`, `auth.User`, `auth.Register`, `api.New` (for `TestTokenCreateWhileServing`'s "running server").
- Produces (package `main`): `func runToken(args []string, stdout, stderr io.Writer) int`.

**Usage** (architecture note): `custos token create --data-dir DIR --email E --name "Name" [--admin] [--task-author] [--processor-author] [--engineer WORKSPACE-UUID,...] [--label TEXT]`. Creates the user if the email has never been seen (empty `Roles`) or loads the existing one, merges in whatever role flags were given (flags only ever add a role — running the command again to add `--task-author` does not drop an `--engineer` grant made earlier), mints a token and prints **only the token** to stdout (so a script can capture `$(custos token create ...)` directly); everything else goes to stderr. This is ruling 4.12's offline bootstrap: no HTTP call, no running server required.

- [ ] **Step 1: Write the failing test** `cmd/custos/token_test.go`

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/auth"
)

func TestTokenCreateUsage(t *testing.T) {
	for _, args := range [][]string{
		{"token"},
		{"token", "create"},
		{"token", "create", "--data-dir", t.TempDir()},
		{"token", "create", "--email", "jane@example.org"},
	} {
		code, _, errs := runCmd(t, args...)
		if code != 2 || !strings.Contains(errs, "usage: custos token create") {
			t.Errorf("%v: code %d err %q", args, code, errs)
		}
	}
}

func TestTokenCreateBootstrapsAnAdmin(t *testing.T) {
	data := t.TempDir()
	code, out, errs := runCmd(t, "token", "create", "--data-dir", data, "--email", "Jane@Example.org",
		"--name", "Jane Doe", "--admin", "--label", "bootstrap")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	token := strings.TrimSpace(out)
	if token == "" || strings.Contains(token, "\n") {
		t.Fatalf("stdout %q, want exactly the token", out)
	}

	m, err := auth.NewManager(auth.Config{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	u, err := m.UpdateUser("jane@example.org", func(*auth.User) {}) // a no-op update just reads the current record back
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Jane Doe" || !u.Roles.Admin {
		t.Fatalf("user %+v", u)
	}
}

// TestTokenCreateWhileServing is Review Focus 6: a user or role granted by
// custos token create, run against the same --data-dir a server already
// has open, is usable on that server's very next request — no restart.
// The "server" here is the real auth.Manager + auth.Register + api.API
// stack (what server.Server mounts in Task 8), driven directly through
// m.Middleware so this test needs nothing from the server package.
func TestTokenCreateWhileServing(t *testing.T) {
	data := t.TempDir()
	serverSide, err := auth.NewManager(auth.Config{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(nil, nil)
	auth.Register(a, serverSide)
	ts := httptest.NewServer(serverSide.Middleware()(a.Handler()))
	defer ts.Close()

	before, err := http.Get(ts.URL + "/api/admin/users")
	if err != nil {
		t.Fatal(err)
	}
	before.Body.Close()
	if before.StatusCode != http.StatusUnauthorized {
		t.Fatalf("before: %d, want 401 (no user exists yet)", before.StatusCode)
	}

	code, out, errs := runCmd(t, "token", "create", "--data-dir", data, "--email", "jane@example.org",
		"--name", "Jane Doe", "--admin", "--label", "bootstrap")
	if code != 0 {
		t.Fatalf("token create: code %d, stderr %s", code, errs)
	}
	token := strings.TrimSpace(out)

	req, _ := http.NewRequest("GET", ts.URL+"/api/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	after, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Body.Close()
	if after.StatusCode != http.StatusOK {
		t.Fatalf("after: %d, want 200 — serverSide (opened before the CLI ran) must see jane without a restart", after.StatusCode)
	}
}

func TestTokenCreateAddsToExistingRoles(t *testing.T) {
	data := t.TempDir()
	if code, _, errs := runCmd(t, "token", "create", "--data-dir", data, "--email", "jane@example.org", "--engineer", ws1); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	if code, _, errs := runCmd(t, "token", "create", "--data-dir", data, "--email", "jane@example.org", "--task-author"); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	m, err := auth.NewManager(auth.Config{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	u, err := m.UpdateUser("jane@example.org", func(*auth.User) {}) // a no-op update just to read the current record back
	if err != nil {
		t.Fatal(err)
	}
	if !u.Roles.TaskAuthor || len(u.Roles.Engineer) != 1 || u.Roles.Engineer[0] != ws1 {
		t.Errorf("roles %+v, want both grants kept", u.Roles)
	}
}
```

Every assertion above reads a user's record back through `(*auth.Manager).UpdateUser`'s own return value (a no-op `func(*auth.User){}` update), rather than adding a test-only export to `auth.Manager` just to peek at `users.json` — the same trick `TestTokenCreateAddsToExistingRoles` uses.

`ws1` is the same workspace UUID constant used throughout this plan's `internal/auth` tests (`5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9`); `cmd/custos` already has `fixture.WorkspaceID` for its one real workspace fixture, so add `ws1` as a second, never-created UUID next to `otherWorkspace` in `distribute_test.go`, where that package keeps its shared test fixtures.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/custos/ -run TestTokenCreate`
Expected: FAIL — `undefined: runToken` (via `runCmd`'s dispatch) and the usage string not yet printed.

- [ ] **Step 3: Implement** `cmd/custos/token.go`

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/emeland-io/custos/internal/auth"
)

const tokenUsage = "usage: custos token create --data-dir DIR --email E --name \"Name\" [--admin] [--task-author] [--processor-author] [--engineer WORKSPACE-UUID,...] [--label TEXT]\n"

// runToken creates or updates a user directly against <data-dir>/auth and
// prints a fresh personal access token for them to stdout — the only
// thing this command writes to stdout, so scripts can capture it as
// $(custos token create ...) (ruling 4.12). Everything else goes to
// stderr.
func runToken(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "create" {
		fmt.Fprint(stderr, tokenUsage)
		return 2
	}
	fl := flag.NewFlagSet("token create", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dataDir := fl.String("data-dir", os.Getenv("CUSTOS_DATA_DIR"), "directory holding auth/ (env CUSTOS_DATA_DIR)")
	email := fl.String("email", "", "the user's email address")
	name := fl.String("name", "", "the user's display name, used as the Git commit author name")
	label := fl.String("label", "cli", "label for the new token, shown in GET /auth/tokens")
	admin := fl.Bool("admin", false, "grant the admin role")
	taskAuthor := fl.Bool("task-author", false, "grant the task-author role")
	processorAuthor := fl.Bool("processor-author", false, "grant the processor-author role")
	engineer := fl.String("engineer", "", `comma-separated workspace UUIDs, or "*" for every workspace, granting the engineer role`)
	if err := fl.Parse(args[1:]); err != nil {
		return helpOrUsage(err)
	}
	if fl.NArg() != 0 || *dataDir == "" || *email == "" {
		fmt.Fprint(stderr, tokenUsage)
		return 2
	}
	var engineerIDs []string
	for _, id := range strings.Split(*engineer, ",") {
		if id = strings.TrimSpace(id); id != "" {
			engineerIDs = append(engineerIDs, id)
		}
	}
	m, err := auth.NewManager(auth.Config{DataDir: *dataDir})
	if err != nil {
		fmt.Fprintf(stderr, "custos token create: %v\n", err)
		return 1
	}
	u, err := m.UpdateUser(*email, func(u *auth.User) {
		if *name != "" {
			u.Name = *name
		}
		// Flags only ever add a role: running this command again to add
		// --task-author must not silently drop an --engineer grant an
		// earlier run made.
		u.Roles.Admin = u.Roles.Admin || *admin
		u.Roles.TaskAuthor = u.Roles.TaskAuthor || *taskAuthor
		u.Roles.ProcessorAuthor = u.Roles.ProcessorAuthor || *processorAuthor
		u.Roles.Engineer = append(u.Roles.Engineer, engineerIDs...)
	})
	if err != nil {
		fmt.Fprintf(stderr, "custos token create: %v\n", err)
		return 1
	}
	id, token, err := m.CreateToken(*email, *label)
	if err != nil {
		fmt.Fprintf(stderr, "custos token create: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "custos token create: token %s for %s, roles %+v\n", id, *email, u.Roles)
	fmt.Fprintln(stdout, token)
	return 0
}
```

- [ ] **Step 4: Wire the subcommand** — edit `cmd/custos/main.go`

Add to the `usage` constant, after the `custos push ...` line:

```
  custos token create --data-dir DIR --email E --name "Name" [--admin] [--task-author]
                       [--processor-author] [--engineer WORKSPACE-UUID,...] [--label TEXT]
```

Add a case to `run`'s `switch`, after `case "push":`:

```go
	case "token":
		return runToken(args[1:], stdout, stderr)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./cmd/custos/ -run 'TestTokenCreate' -v`
Expected: PASS.

- [ ] **Step 6: Run the package and commit**

Run: `go test ./cmd/custos/`
Expected: PASS.

```bash
gofmt -l cmd/custos
go vet ./cmd/custos
git add cmd/custos/token.go cmd/custos/token_test.go cmd/custos/main.go
git commit -m "Add custos token create to bootstrap admins and tokens offline

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 8 — `internal/server`: mount `/auth/`, gate `/api/` and `/git/`, check the push role

**Files:**
- Modify: `internal/server/server.go` (new fields `auth`, `middleware`; `WithAuth`, `WithMiddleware`; the sign-in gate and push-role check inside `Handler`)
- Modify: `internal/server/server_test.go` (`start` wires a real, token-only `auth.Manager` and returns a pre-authed URL — every existing test in this file pushes/clones through it unchanged otherwise)
- Modify: `internal/server/api_test.go` (`TestWithAPI`'s happy path now needs a caller; without one, 401 — Review Focus 5)
- Create: `internal/server/auth_test.go`
- Modify: `cmd/custos/serve.go` (`openServer` builds the `auth.Manager`, registers it, wires `WithAuth`/`WithMiddleware`; its signature gains an `auth.Config` parameter)
- Modify: `cmd/custos/main.go` (usage text: the new flags; `--addr`'s "there is no authentication yet" caveat is now false)

**Interfaces:**
- Consumes: `api.CallerOf` (Task 1); `auth.Manager`, `auth.Register`, `(*auth.Manager).Middleware` (Tasks 2, 3, 5); `authFlagSet` (Task 6).
- Produces (package `server`): `func (s *Server) WithAuth(h http.Handler) *Server`; `func (s *Server) WithMiddleware(mw func(http.Handler) http.Handler) *Server`.

**Design.** Two independent things land in `Handler()`, and Review Focus 5 is about keeping them independent: (1) a gate, applied unconditionally to `/git/` and `/api/`, that 401s a request with no caller in its context — true whether or not `WithMiddleware` was ever called, because the gate only asks `api.CallerOf`, never anything about `auth` itself; (2) `WithMiddleware`'s callback, which is the *only* thing that ever puts a caller there. A `server.Server` built with `WithAPI`/`WithAuth` but no `WithMiddleware` therefore still refuses every `/api/` and `/git/` request — there is simply never a caller for the gate to find. `/auth/` is mounted without the gate (its own handlers, Task 5, each decide for themselves whether they need a caller), and `/healthz` and `/` stay open, matching `excludedPrefixes` in the architecture note.

The push-role check is a second, narrower gate layered inside the already-signed-in `/git/` branch, wrapping the existing push-notification handler: it looks at the method and path exactly the way `notifyPush` already does (reusing `catalogPushPaths`/`workspacePushPath`), and runs before `withContentLength` — so before any byte of the push is read (Review Focus 4) — denying with 403 a signed-in caller who lacks the role, without ever reaching `git http-backend`.

- [ ] **Step 1: Write the failing tests** `internal/server/auth_test.go`

```go
package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/auth"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/store"
)

// startAuthed is start, plus a real auth.Manager (token-only mode)
// wired through WithAuth/WithMiddleware, and api.New mounted at /api/
// with auth.Register added. It returns the bare URL (no credentials
// embedded) — callers add their own token per request, since these tests
// are about who may do what, not about one bootstrap identity.
func startAuthed(t *testing.T) (*store.Store, *auth.Manager, string) {
	t.Helper()
	st, err := store.Open(t.TempDir(), custosBin, "http://custos.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InstallHooks(); err != nil {
		t.Fatal(err)
	}
	m, err := auth.NewManager(auth.Config{DataDir: st.DataDir()})
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(st, nil)
	auth.Register(a, m)
	srv := New(st).WithAPI(a.Handler()).WithAuth(a.Handler()).
		WithMiddleware(m.Middleware("/healthz", "/auth/login", "/auth/callback", "/"))
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return st, m, ts.URL
}

// bootstrapToken creates (or updates) email with roles and returns a
// fresh token for them.
func bootstrapToken(t *testing.T, m *auth.Manager, email string, roles auth.Roles) string {
	t.Helper()
	if _, err := m.UpdateUser(email, func(u *auth.User) { u.Roles = roles }); err != nil {
		t.Fatal(err)
	}
	_, token, err := m.CreateToken(email, "test")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// authedURL returns rawURL with token as the Basic password (what git
// sends, and what Middleware's resolve reads back).
func authedURL(t *testing.T, rawURL, token string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("token", token)
	return u.String()
}

// TestAPINeedsSignIn is Review Focus 5: /api/ answers 401 to an anonymous
// request, with no WWW-Authenticate (so the UI never sees a browser's own
// password dialog), and 200 once a caller is presented.
func TestAPINeedsSignIn(t *testing.T) {
	_, m, baseURL := startAuthed(t)
	resp, err := http.Get(baseURL + "/api/catalog")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") != "" {
		t.Error("WWW-Authenticate set on /api/; the UI must not trigger a browser password dialog")
	}

	token := bootstrapToken(t, m, "jane@example.org", auth.Roles{})
	req, _ := http.NewRequest("GET", baseURL+"/api/catalog", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("signed in, no role needed for a read: %d", resp.StatusCode)
	}
}

// TestGitNeedsSignIn is Review Focus 5 for /git/: a bare HTTP request
// (not through the git CLI, so there is no risk of it blocking on a
// credential prompt) gets 401 with the Basic challenge git needs before
// it will send a URL's or a credential helper's credentials.
func TestGitNeedsSignIn(t *testing.T) {
	_, _, baseURL := startAuthed(t)
	resp, err := http.Get(baseURL + "/git/catalog.git/info/refs?service=git-upload-pack")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("code %d", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != `Basic realm="custos"` {
		t.Errorf("WWW-Authenticate %q", got)
	}
}

// TestPushNeedsRole is Review Focus 4: a push to the catalog needs
// task-author; a push to a workspace needs engineer on that workspace;
// a caller who is signed in but lacks the role gets 403 (not a retry
// prompt — git never re-asks for different credentials on 403, so these
// pushes, unlike an anonymous one, are safe to run through the real git
// CLI without GIT_TERMINAL_PROMPT=0).
func TestPushNeedsRole(t *testing.T) {
	st, m, baseURL := startAuthed(t)
	readOnly := bootstrapToken(t, m, "ro@example.org", auth.Roles{})
	taskAuthor := bootstrapToken(t, m, "ta@example.org", auth.Roles{TaskAuthor: true})
	engineer := bootstrapToken(t, m, "eng@example.org", auth.Roles{Engineer: []string{fixture.WorkspaceID}})

	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())

	if out, err := gittest.Try(work, "push", authedURL(t, baseURL+"/git/catalog.git", readOnly), "main"); err == nil {
		t.Fatalf("read-only caller pushed the catalog:\n%s", out)
	}
	gittest.Run(t, work, "push", authedURL(t, baseURL+"/git/catalog.git", taskAuthor), "main")

	if err := st.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	wsDir := filepath.Join(t.TempDir(), "ws")
	gittest.Run(t, t.TempDir(), "clone", authedURL(t, baseURL+"/git/workspaces/"+fixture.WorkspaceID+".git", engineer), wsDir)
	gittest.Commit(t, wsDir, map[string]string{"answers/" + fixture.TaskA + ".md": "---\ntask: " + fixture.TaskA + "\ntask_version: 1.0.0\ntype: text\nvalue: x\n---\n"})
	gittest.Run(t, wsDir, "push", "origin", "main") // origin already carries eng's token, from clone

	// A further local commit, so the push below actually has something
	// new to send: otherwise git decides the remote is already up to
	// date and never issues the request at all, which would make this
	// check pass even if the role gate were missing entirely — found
	// while applying this plan.
	gittest.Commit(t, wsDir, map[string]string{"answers/" + fixture.TaskA + ".md": "---\ntask: " + fixture.TaskA + "\ntask_version: 1.0.0\ntype: text\nvalue: y\n---\n"})
	if out, err := gittest.Try(wsDir, "push", authedURL(t, baseURL+"/git/workspaces/"+fixture.WorkspaceID+".git", taskAuthor), "main"); err == nil {
		t.Fatalf("task-author (not engineer here) pushed the workspace:\n%s", out)
	}
}

// TestMountingAndMiddlewareOrder checks that /auth/ bypasses the sign-in
// gate (handleLogin itself decides whether it is available) while /api/
// does not, and that excludedPrefixes reaches Middleware even once
// WithAuth/WithMiddleware are both set.
func TestMountingAndMiddlewareOrder(t *testing.T) {
	_, _, baseURL := startAuthed(t)
	resp, err := http.Get(baseURL + "/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/auth/me without a caller: %d, want 401 from the handler itself, not the gate", resp.StatusCode)
	}
	resp, err = http.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz: %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/server/ -run 'TestAPINeedsSignIn|TestGitNeedsSignIn|TestPushNeedsRole|TestMountingAndMiddlewareOrder'`
Expected: FAIL — `undefined: (*Server).WithAuth` (and `WithMiddleware`).

- [ ] **Step 3: Implement** — edit `internal/server/server.go`

Add to the import block:

```go
	"github.com/emeland-io/custos/internal/api"
```

Add two fields to `Server` (next to `api`):

```go
	auth       http.Handler                  // mounted at /auth/ when set
	middleware func(http.Handler) http.Handler // wraps the whole mux when set
```

Add, next to `WithAPI`:

```go
// WithAuth mounts h at /auth/ in the handler Handler returns, unguarded by
// the sign-in gate WithAPI's and /git/'s handlers get — each /auth/ route
// (Task 5) decides for itself whether it needs a caller. It returns s.
func (s *Server) WithAuth(h http.Handler) *Server {
	s.auth = h
	return s
}

// WithMiddleware wraps the whole handler Handler returns (/git/, /auth/,
// /api/ and /healthz together) in mw, after routing — so mw sees the
// original request path and can special-case it (auth.Manager.Middleware
// does, for its excludedPrefixes). mw is the only thing that ever
// resolves a caller into a request's context; Handler's own gate on
// /api/ and /git/ only ever checks whether one is there (Review Focus 5):
// a Server built without WithMiddleware refuses every /api/ and /git/
// request, because nothing ever puts a caller in context for the gate to
// find. It returns s.
func (s *Server) WithMiddleware(mw func(http.Handler) http.Handler) *Server {
	s.middleware = mw
	return s
}
```

Replace `Handler`:

```go
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
	mux.Handle("/git/", s.requireSignedIn(true, s.pushGate(withContentLength(s.notifyPush(backend)))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	if s.api != nil {
		mux.Handle("/api/", s.requireSignedIn(false, s.api))
	}
	if s.auth != nil {
		mux.Handle("/auth/", s.auth)
	}
	var h http.Handler = mux
	if s.middleware != nil {
		h = s.middleware(h)
	}
	return h, nil
}

// requireSignedIn wraps h so it 401s before h runs when the request
// carries no caller (api.CallerOf) — unconditionally, not only when
// WithMiddleware happens to be wired (Review Focus 5). gitRealm adds the
// WWW-Authenticate challenge git needs before it will send Basic
// credentials from a URL or a credential helper; /api/ never sends it, so
// a browser running the UI never shows its own password dialog.
func (s *Server) requireSignedIn(gitRealm bool, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := api.CallerOf(r); !ok {
			if gitRealm {
				w.Header().Set("WWW-Authenticate", `Basic realm="custos"`)
			}
			http.Error(w, "not signed in", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// pushGate wraps the /git/ handler so a push to the catalog needs the
// task-author role and a push to a workspace needs the engineer role for
// that workspace (Review Focus 4), checked before h runs — so before
// withContentLength or git http-backend read one byte of the request
// body. Reading (info/refs, upload-pack) is not matched here, so it is
// unaffected: requireSignedIn already covers it. The caller is always
// present: pushGate only ever wraps requireSignedIn's h.
func (s *Server) pushGate(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			c, _ := api.CallerOf(r)
			switch {
			case slices.Contains(catalogPushPaths[:], r.URL.Path):
				if !c.Has("task-author", "") {
					http.Error(w, "this push needs the task-author role", http.StatusForbidden)
					return
				}
			default:
				if m := workspacePushPath.FindStringSubmatch(r.URL.Path); m != nil && !c.Has("engineer", m[1]) {
					http.Error(w, "this push needs the engineer role for workspace "+m[1], http.StatusForbidden)
					return
				}
			}
		}
		h.ServeHTTP(w, r)
	})
}
```

(`slices` is already imported by this file for `notifyPush`.)

- [ ] **Step 4: Update the existing tests that now need a caller** — edit `internal/server/server_test.go`

Replace `start`:

```go
// start serves a new data directory the way custos serve does, with a
// bootstrap admin whose token is embedded in the returned URL — Admin
// implies every role, so every existing test below that pushes or clones
// through this URL keeps working unchanged; tests about roles themselves
// live in auth_test.go's startAuthed, which mints its own tokens per role.
func start(t *testing.T) (*store.Store, *Server, string) {
	t.Helper()
	st, err := store.Open(t.TempDir(), custosBin, "http://custos.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InstallHooks(); err != nil {
		t.Fatal(err)
	}
	m, err := auth.NewManager(auth.Config{DataDir: st.DataDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateUser("jane@example.org", func(u *auth.User) { u.Roles = auth.Roles{Admin: true} }); err != nil {
		t.Fatal(err)
	}
	_, token, err := m.CreateToken("jane@example.org", "test")
	if err != nil {
		t.Fatal(err)
	}
	srv := New(st).WithMiddleware(m.Middleware("/healthz"))
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("token", token)
	return st, srv, u.String()
}
```

Add `"github.com/emeland-io/custos/internal/auth"` and `"net/url"` to this file's import block.

- [ ] **Step 5: Update `TestWithAPI`** — edit `internal/server/api_test.go`

```go
func TestWithAPI(t *testing.T) {
	st, err := store.Open(t.TempDir(), "/custos", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "api "+r.URL.Path)
	})
	h, err := New(st).WithAPI(api).Handler()
	if err != nil {
		t.Fatal(err)
	}
	// Without WithMiddleware, nothing ever resolves a caller, so WithAPI
	// alone still refuses every /api/ request (Review Focus 5; the happy
	// path with a real caller is TestAPINeedsSignIn in auth_test.go).
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workspaces", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("without WithMiddleware: %d, want 401", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Body.String() != "ok\n" {
		t.Errorf("healthz %q", rec.Body.String())
	}

	plain, err := New(st).Handler()
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	plain.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workspaces", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("without WithAPI: %d", rec.Code)
	}
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./internal/server/ -v`
Expected: PASS, including every pre-existing test in `server_test.go` (now running through an authed URL) and the new ones above.

- [ ] **Step 7: Wire it into `custos serve`** — edit `cmd/custos/serve.go`

`runServe` defines the auth flags and builds their config, next to `procFlags`:

```go
	procFlags := defineProcessorFlags(fl)
	authFlags := defineAuthFlags(fl)
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	procOpts, err := procFlags.options()
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 2
	}
```

(unchanged lines in between elided) and, right after `checkPublicURL`:

```go
	if err := checkPublicURL(*publicURL); err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 2
	}
	authCfg, err := authFlags.config(*publicURL)
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 2
	}
```

`openServer`'s signature gains `authCfg auth.Config`, and its one call site passes the new value through:

```go
	srv, err := openServer(ctx, *dataDir, *publicURL, procOpts, authCfg, stderr)
```

```go
func openServer(ctx context.Context, dataDir, publicURL string, procOpts processorOptions, authCfg auth.Config, stderr io.Writer) (*server.Server, error) {
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
	a, err := openAPI(st)
	if err != nil {
		return nil, err
	}
	authMgr, err := auth.NewManager(authCfg)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	auth.Register(a, authMgr)
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
	srv.WithAuth(a.Handler())
	srv.WithMiddleware(authMgr.Middleware("/healthz", "/auth/login", "/auth/callback", "/"))
	return srv, nil
}
```

Add `"github.com/emeland-io/custos/internal/auth"` to this file's imports.

- [ ] **Step 8: Fix the usage text** — edit `cmd/custos/main.go`

`--addr`'s caveat in `serve.go` ("there is no authentication yet, so keep it on loopback unless the network is trusted") is now false — this plan is what makes it false, so it is corrected here rather than left for a later plan to notice (flagged in "Decisions beyond the architecture note"):

```go
	addr := fl.String("addr", envOr("CUSTOS_ADDR", defaultAddr),
		"listen address (env CUSTOS_ADDR); every request still needs a signed-in caller or a valid token, "+
			"but TLS is the operator's own job (a reverse proxy, typically) — custos itself only ever speaks plain HTTP")
```

`usage` in `main.go` gains the new flags, after the existing `serve` line:

```
  custos serve [--data-dir DIR] [--addr ADDR] [--public-url URL] [--container-runtime CMD]
               [--secrets-dir DIR] [--processor-memory SIZE] [--processor-workers N]
               [--max-generation-depth N] [--trusted-keys DIR]
               [--oidc-issuer-url URL] [--oidc-client-id ID] [--oidc-client-secret SECRET]
               [--oidc-redirect-url URL] [--admin-emails EMAILS] [--session-secret HEX]
```

- [ ] **Step 9: Run the whole `cmd/custos` and `internal/server` packages**

Run: `go build ./... && go test ./cmd/custos/... ./internal/server/...`
Expected: build succeeds; **this step is expected to fail** for every other package that still sends `X-Custos-Author` or calls `openServer`/`Push`/`Clone` with their old signatures — that sweep is Task 9. For `internal/server` and this task's own changes to `cmd/custos`, expect PASS.

- [ ] **Step 10: Commit**

```bash
gofmt -l internal/server cmd/custos
go vet ./internal/server ./cmd/custos
git add internal/server/server.go internal/server/server_test.go internal/server/api_test.go internal/server/auth_test.go cmd/custos/serve.go cmd/custos/main.go
git commit -m "Mount authentication in server.Server and require a role to push

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 9 — Remove `X-Custos-Author`: `api.Author` reads the caller, every write gets its role, custos's own HTTP calls carry a token

**Files:**
- Modify: `internal/api/request.go`, `internal/api/api.go`, `internal/api/api_test.go`, `internal/api/blob_test.go`
- Modify: `internal/merge/http.go`, `internal/merge/http_test.go`
- Modify: `internal/distribute/api.go`, `internal/distribute/api_test.go`
- Modify: `internal/runs/api.go`, `internal/runs/api_test.go`
- Modify: `internal/remote/remote.go`, `internal/remote/push.go`, `internal/remote/clone.go`, `internal/remote/remote_test.go`, `internal/remote/push_test.go`
- Modify: `internal/gittest/gittest.go`
- Modify: `cmd/custos/remote.go`, `cmd/custos/main.go`, `cmd/custos/remote_test.go`, `cmd/custos/runs_test.go`, `cmd/custos/distribute_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `api.CallerOf` (Task 1); `auth.Require`/`RequireWorkspace` (Task 3).
- Produces: no new exported surface — this task is entirely "the last caller of a now-removed thing" across the tree. `go build ./...` finding nothing left naming `X-Custos-Author` or `authorHeader`, and `go vet ./...` passing everywhere, is this task's own acceptance test as much as anything below.

This is the one task in this plan whose "Files" list spans most of the repository. It is kept to one task, rather than split per package, because none of the pieces are independently testable: `api.Author`'s new implementation (reading `api.CallerOf`) is meaningless until something guards a route with a role, which is meaningless until the tests that exercise that route stop sending a header `api.Author` no longer reads. Grep is this task's own Step 1.

- [ ] **Step 1: Confirm the full extent of the sweep**

Run: `grep -rn "X-Custos-Author\|authorHeader" --include='*.go' .`
Expected, going in (the state this plan's Tasks 1-8 leave behind): exactly the thirteen hits below, none created by Tasks 1-8:

```
cmd/custos/runs_test.go:63
cmd/custos/distribute_test.go:384
internal/merge/http_test.go:44
internal/distribute/api.go:92
internal/distribute/api_test.go:18,44
internal/api/blob_test.go:43
internal/api/request.go:14,20
internal/api/api_test.go:120,129,253
internal/runs/api_test.go:28
internal/remote/remote.go:25
```

Expected leaving this task: zero hits anywhere, and `go build ./...` succeeds.

- [ ] **Step 2: `api.Author` reads the caller** — edit `internal/api/request.go`

```go
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/task"
)

// maxJSONBytes bounds JSON request bodies. Answers are text; attachments
// travel separately through /api/blobs.
const maxJSONBytes = 16 << 20

// Author returns the signed-in caller as the author of the commit its
// request causes. Every write endpoint calls it first (most of them
// behind RequireRole, which already guarantees a caller is present —
// Author's own 401 only ever fires for an endpoint that takes no role at
// all, such as POST /api/blobs).
func Author(r *http.Request) (gitrepo.Signature, error) {
	c, ok := CallerOf(r)
	if !ok {
		return gitrepo.Signature{}, errNotSignedIn()
	}
	return c.Signature(), nil
}
```

(`gitrepo` stays imported for `gitrepo.Signature`; `task` is unchanged, still used by `workspaceID`/`taskID` below in the same file; `errNotSignedIn` is Task 1's.)

- [ ] **Step 3: Guard `api.go`'s own two writes** — edit `internal/api/api.go`

```go
func (a *API) routes() {
	a.Handle("POST /api/blobs", a.postBlob)
	a.Handle("GET /api/blobs/{sha256}", a.getBlob) // GET patterns also match HEAD
	a.Handle("GET /api/workspaces", a.listWorkspaces)
	a.Handle("POST /api/workspaces", RequireRole("admin", "", a.createWorkspace))
	a.Handle("GET /api/catalog", a.getCatalog)
	a.Handle("GET /api/catalog/tasks/{id}", a.getCatalogTask)
	a.Handle("GET /api/workspaces/{id}/status", a.getStatus)
	a.Handle("GET /api/workspaces/{id}/book", a.getBook)
	a.Handle("GET /api/workspaces/{id}/answers/{task}", a.getAnswer)
	a.Handle("PUT /api/workspaces/{id}/answers/{task}", RequireRole("engineer", "id", a.putAnswer))
	a.Handle("POST /api/workspaces/{id}/answers/{task}/still-valid", RequireRole("engineer", "id", a.stillValid))
}
```

`"admin"` and `"engineer"` are written as literals, not `auth.RoleAdmin`/`auth.RoleEngineer`: `internal/api` must not import `internal/auth` (Review Focus 3; `auth` imports `api`, for `Register`). `RequireRole` is Task 1's own function in this same package, so this needs no new import. `POST /api/blobs` stays role-free — any signed-in caller may upload (Endpoints table) — so `a.postBlob` itself still calls `Author` directly for its 401.

- [ ] **Step 4: Update `internal/api`'s own tests**

`api_test.go`: replace the `do` helper (and its comment) and add one small caller type next to `fakeCaller` (already in `caller_test.go`, in the same package, from Task 1):

```go
// allCaller holds every role for every workspace. do and the handful of
// other legacy helpers below predate per-endpoint roles and are not
// testing authorization themselves — TestRequireRole (caller_test.go) and
// TestPushNeedsRole/TestAPINeedsSignIn (internal/server) do that — so
// they grant everything rather than enumerate which workspace id each
// test happens to use.
type allCaller struct{ sig gitrepo.Signature }

func (c allCaller) Signature() gitrepo.Signature      { return c.sig }
func (c allCaller) Has(role, workspaceID string) bool { return true }

// do sends one request to the API. An empty author sends no caller
// (anonymous); otherwise the request carries an allCaller parsed from
// author as "Name <email>".
func (e *env) do(t *testing.T, method, path, author, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if author != "" {
		sig, err := gitrepo.ParseSignature(author)
		if err != nil {
			t.Fatalf("author %q: %v", author, err)
		}
		req = req.WithContext(WithCaller(req.Context(), allCaller{sig: sig}))
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}
```

Line 253 (the other `req.Header.Set("X-Custos-Author", header)` in this file) is `TestAuthor`, which the "same pattern" above does not actually fit — found while applying this plan: `TestAuthor` tests `Author(req)` itself against a table of malformed header strings ("Jane Doe", "<jane@example.org>", ...), and `Author` no longer parses anything out of the request at all, so there is nothing left to feed a malformed string into. Rewrite it instead to test the new contract directly:

```go
// TestAuthor: Author now reads the caller the authentication middleware
// put in the request's context (api.CallerOf) instead of a client-supplied
// header. No caller in context -> 401; a caller in context -> its own
// Signature().
func TestAuthor(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/blobs", nil)
	if sig, err := Author(req); err == nil {
		t.Errorf("no caller: sig %+v, want an error", sig)
	} else {
		rec := httptest.NewRecorder()
		WriteError(rec, err)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("no caller: status %d, want 401", rec.Code)
		}
	}

	req = req.WithContext(WithCaller(req.Context(), allCaller{sig: jane}))
	sig, err := Author(req)
	if err != nil || sig != jane {
		t.Errorf("with caller: %+v %v", sig, err)
	}
}
```

Separately, `internal/api/workspaces_test.go`'s `TestCreateWorkspaceErrors` has a `"bad author"` table case (`author: "Jane"`, expecting 401) that Step 1's grep does not catch at all (it names no header literal) but that this sweep still breaks: once `e.do` itself parses its `author` argument into a signature (above), a malformed string like `"Jane"` fails inside `do` via `t.Fatalf`, before the request is even built — there is no longer an equivalent of "a malformed header reaches the server and gets 401 there" to test. Remove that one table entry (the `"no author"` entry right above it already covers "no caller → 401"):

```go
		{"exists", janeHeader, body, http.StatusConflict},
		// "bad author" (a malformed X-Custos-Author header, e.g. "Jane")
		// is removed: Author no longer parses anything out of the request
		// itself, so there is no equivalent malformed-input case any more.
		{"no author", "", `{"id":"` + otherWorkspace + `"}`, http.StatusUnauthorized},
		{"path in id", janeHeader, `{"id":"../escape"}`, http.StatusBadRequest},
```

Neither of these two fixes is among Step 1's enumerated thirteen grep hits (neither test names the header literal), so finding them needs an actual test run after the rest of this step, not just the grep re-check.

`blob_test.go`'s one hand-built request (`TestBlobUploadTooLarge`):

```go
func TestBlobUploadTooLarge(t *testing.T) {
	e := newEnv(t, nil)
	req := httptest.NewRequest("POST", "/api/blobs", strings.NewReader("x"))
	req.ContentLength = blobs.MaxSize + 1
	req = req.WithContext(WithCaller(req.Context(), allCaller{sig: jane}))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	decode[errorJSON](t, rec, http.StatusRequestEntityTooLarge)
	if e.bl.Has(hash("x")) {
		t.Error("oversized upload was read")
	}
}
```

- [ ] **Step 5: Guard `merge`'s writes** — edit `internal/merge/http.go`

```go
func Register(a *api.API, st *store.Store, onMainMoved func(id string), onRerun func(id string, tasks []string)) {
	a.Handle("POST /api/workspaces/{id}/fork", auth.Require(auth.RoleAdmin, func(w http.ResponseWriter, r *http.Request) { handleFork(st, onMainMoved, w, r) }))
	a.Handle("POST /api/workspaces/{id}/merge", auth.RequireWorkspace("id", func(w http.ResponseWriter, r *http.Request) { handleMerge(st, onMainMoved, onRerun, w, r) }))
	a.Handle("GET /api/workspaces/{id}/branches", func(w http.ResponseWriter, r *http.Request) { handleBranches(st, w, r) })
}
```

Add `"github.com/emeland-io/custos/internal/auth"` to this file's imports. (`merge` importing `auth` is not a cycle: `auth` imports `api`, not `merge`.) `handleFork`/`handleMerge` keep calling `api.Author(r)` exactly as before — the guard above already guarantees a caller, so `Author` never 401s there now; it only still can for a route with no guard.

`internal/merge/http_test.go`'s `call` helper:

```go
func call(h http.Handler, method, path, author, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if author != "" {
		sig, err := gitrepo.ParseSignature(author)
		if err != nil {
			panic(err)
		}
		req = req.WithContext(api.WithCaller(req.Context(), allCaller{sig}))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// allCaller holds every role for every workspace — this file tests
// fork/merge/branch logic, not authorization.
type allCaller struct{ sig gitrepo.Signature }

func (c allCaller) Signature() gitrepo.Signature      { return c.sig }
func (c allCaller) Has(role, workspaceID string) bool { return true }
```

Add `"github.com/emeland-io/custos/internal/gitrepo"` to this file's imports (`api` is already imported).

- [ ] **Step 6: Guard `distribute`'s writes** — edit `internal/distribute/api.go`

```go
func Register(a *api.API, st *store.Store) {
	a.Handle("POST /api/workspaces/{id}/freeze", auth.RequireWorkspace("id", frozenHandler(st, true)))
	a.Handle("POST /api/workspaces/{id}/unfreeze", auth.RequireWorkspace("id", frozenHandler(st, false)))
	a.Handle("GET /api/workspaces/{id}/proposals", func(w http.ResponseWriter, r *http.Request) {
		ps, err := Proposals(st, r.PathValue("id"))
		if err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, ps)
	})
	a.Handle("POST /api/workspaces/{id}/proposals/accept", auth.RequireWorkspace("id", func(w http.ResponseWriter, r *http.Request) {
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
	}))
	a.Handle("POST /api/workspaces/{id}/proposals/reject", auth.RequireWorkspace("id", func(w http.ResponseWriter, r *http.Request) {
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
	}))
	a.Handle("GET /api/workspaces/{id}/pin-diff", func(w http.ResponseWriter, r *http.Request) {
		pd, err := pinDiff(st, r.PathValue("id"))
		if err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, pd)
	})
}
```

(`frozenHandler` and `requireAuthor` keep their bodies — `requireAuthor`'s doc comment loses its "X-Custos-Author" wording: "requireAuthor answers 401 when there is no signed-in caller, using api.WriteError so the body matches every other error response — unreachable in practice below RequireWorkspace, which already checked, but still the right thing for a route that gains one later without this guard.") Add `"github.com/emeland-io/custos/internal/auth"` to this file's imports.

`internal/distribute/api_test.go` uses a **real** `httptest.NewServer` + real `http.Client` (unlike `merge`/`runs`, whose tests dispatch in-process): there is no shared Go context to inject a caller into across that network hop, so this file gets a small stand-in for `auth.Middleware` instead of a context injection trick — it is testing `distribute`'s own freeze/proposal logic, not `auth`'s session or token resolution (Task 3 already covers that), so the stand-in only needs to turn one fixed header into a caller with every role:

```go
const authorHeader = "X-Test-Signed-In" // this package's own tests only; not read anywhere in production

// testAuth stands in for auth.Middleware: a request carrying authorHeader
// resolves to a caller with every role; this package's tests are about
// distribute's own endpoints, not about session/token resolution.
func testAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(authorHeader) != "" {
			r = r.WithContext(api.WithCaller(r.Context(), allCaller{gitrepo.Signature{Name: "Jane Doe", Email: "jane@example.org"}}))
		}
		next.ServeHTTP(w, r)
	})
}

type allCaller struct{ sig gitrepo.Signature }

func (c allCaller) Signature() gitrepo.Signature      { return c.sig }
func (c allCaller) Has(role, workspaceID string) bool { return true }

func serveAPI(t *testing.T, st *store.Store) string {
	t.Helper()
	bl, err := blobs.Open(filepath.Join(st.DataDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(st, bl)
	Register(a, st)
	ts := httptest.NewServer(testAuth(a.Handler()))
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
		req.Header.Set(authorHeader, "1")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
```

(the rest of `call` — reading and returning the status and body — is unchanged; so is every call site: they already pass a non-empty `author` string exactly where a caller should be present, so the meaning of the parameter does not change, only what it triggers under the hood.) Add `"github.com/emeland-io/custos/internal/gitrepo"` to this file's imports and remove the old `const authorHeader = "Jane Doe <jane@example.org>"` (its name is reused above for the header name instead — rename the handful of call sites that passed that string as the "author" argument to pass any non-empty marker, such as `"1"`, since the value no longer flows into a signature).

- [ ] **Step 7: Guard `runs`'s writes** — edit `internal/runs/api.go`

```go
	a.Handle("POST /api/workspaces/{id}/runs/{run}/retry", auth.RequireWorkspace("id", func(w http.ResponseWriter, r *http.Request) {
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
	}))
```

and, further down this same `Register` (not shown in the excerpt this plan read, but present in the file per the architecture note's endpoint table): wrap `POST /api/workspaces/{id}/processor-proposals/{task}/accept` and `.../reject` in `auth.RequireWorkspace("id", ...)`, and `POST /api/processors/{name}/dry-run` in `auth.Require(auth.RoleProcessorAuthor, ...)`. `GET /api/processors`, `GET /api/workspaces/{id}/runs[...]`, `GET .../processor-proposals[...]`, and `GET /api/dry-runs/{id}` stay unguarded here — reads need only the signed-in-caller gate `internal/server` already applies to all of `/api/`. Add `"github.com/emeland-io/custos/internal/auth"` to this file's imports.

`internal/runs/api_test.go`'s `call` helper gets the same treatment as `merge/http_test.go`'s in Step 5 (in-process `ServeHTTP`, so a direct context injection, not a header stand-in): replace `req.Header.Set("X-Custos-Author", author)` with `req = req.WithContext(api.WithCaller(req.Context(), allCaller{sig}))` after parsing `author` with `gitrepo.ParseSignature`, and add the same three-line `allCaller` type (or, since `internal/merge` and `internal/runs` are different packages, each needs its own copy — Go has no package for sharing three lines of test-only code without a new importable package nobody else needs).

- [ ] **Step 8: custos's own HTTP requests carry a token, not an author** — edit `internal/remote`

`remote.go`: remove `const authorHeader = "X-Custos-Author"`; add:

```go
// tokenEnv names the environment variable holding custos's own personal
// access token, read by Push and Clone's own HTTP requests to /api/blobs
// — reading and writing blobs now needs a signed-in caller like
// everything else (ruling 4.10), and these requests carry none of the
// git subprocess's own credentials.
const tokenEnv = "CUSTOS_TOKEN"

// bearerToken returns the token to send with Push/Clone's own HTTP
// requests: $CUSTOS_TOKEN, or — so a plain
// "git clone https://<id>.<secret>@host/git/..." URL just works without
// also exporting the variable — the password half of remoteName's own
// URL (what git itself already used to authenticate the clone or push).
func bearerToken(dir, remoteName string) string {
	if t := os.Getenv(tokenEnv); t != "" {
		return t
	}
	repoURL, err := git(dir, "remote", "get-url", remoteName)
	if err != nil {
		return ""
	}
	u, err := url.Parse(repoURL)
	if err != nil || u.User == nil {
		return ""
	}
	pw, _ := u.User.Password()
	return pw
}
```

Add `"net/url"` to this file's imports. Remove `GitAuthor` and its helper usage note (nothing in this package calls it once `--author` is gone from `custos push` — Step 11 below; its own test is removed in Step 10).

`push.go`: `Push` drops its `author gitrepo.Signature` parameter; `ensureUploaded` and `onServer` take a `token string` instead and send it as a Bearer token:

```go
func Push(dir, remoteName string, args []string, log io.Writer) error {
	repoURL, err := git(dir, "remote", "get-url", remoteName)
	if err != nil {
		return err
	}
	server, err := ServerURL(repoURL)
	if err != nil {
		return err
	}
	token := bearerToken(dir, remoteName)
	if revs := sources(args); len(revs) > 0 {
		revList := append([]string{"rev-list"}, revs...)
		out, err := git(dir, append(revList, "--not", "--remotes="+remoteName, "--")...)
		if err != nil {
			return err
		}
		tips, err := tipRevs(dir, revs)
		if err != nil {
			return err
		}
		ats, err := attachments(dir, append(strings.Fields(out), tips...))
		if err != nil {
			return err
		}
		for _, at := range ats {
			if err := ensureUploaded(dir, server, at, token, log); err != nil {
				return err
			}
		}
	}
	cmd := exec.Command("git", append([]string{"-C", dir, "push", remoteName}, args...)...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git push: %w", err)
	}
	return nil
}
```

`ensureUploaded`'s signature becomes `func ensureUploaded(dir, server string, at attachment, token string, log io.Writer) error`, calling `onServer(server, at.sha, token)` and, on the upload request, `req.Header.Set("Authorization", "Bearer "+token)` in place of `req.Header.Set(authorHeader, author.String())` (unconditionally — an empty token sends an empty bearer value, which the server answers with the same 401 as no header at all, giving the same "nothing was pushed" error `ensureUploaded` already builds from a non-201 status). `onServer`'s signature becomes `func onServer(server, sha, token string) (bool, error)`, using `http.NewRequest(http.MethodHead, ...)` instead of `http.Head` so it can set the same `Authorization` header (`/api/blobs/{sha256}` is a read, which now also needs a signed-in caller — ruling 4.10).

`clone.go`: `Clone`'s own signature is unchanged (git itself already authenticates the clone from the URL or a credential helper); only the attachment download needs the token, computed once `git clone` has set up `origin`:

```go
func Clone(repoURL, dir string, log io.Writer) error {
	server, err := ServerURL(repoURL)
	if err != nil {
		return err
	}
	cmd := exec.Command("git", "clone", "--", repoURL, dir)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	if err := excludeBlobDir(dir); err != nil {
		return err
	}
	return fetchBlobs(dir, server, bearerToken(dir, "origin"), log)
}
```

`fetchBlobs` and `download` each gain a `token string` parameter threaded the same way, with `download` building its request with `http.NewRequest` + the `Authorization` header instead of `http.Get`.

- [ ] **Step 9: Stop a refused push from hanging a test** — edit `internal/gittest/gittest.go`

```go
func Env() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.org",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.org",
		"GIT_TERMINAL_PROMPT=0", // a push or clone git refuses for lack of credentials fails fast instead of blocking on a prompt no test can answer
	)
}
```

- [ ] **Step 10: Update `internal/remote`'s own tests**

`remote_test.go`: remove `TestGitAuthor` and the now-dead `GitAuthor` function it tested (Step 8 already dropped its only caller); `startServer` wires a real token-only `auth.Manager`, registers it, and returns an authed base URL — every `s.url`/`s.workspaceURL()` use downstream keeps working unchanged because the credential now simply lives in the URL:

```go
func startServer(t *testing.T) *testServer {
	t.Helper()
	st, err := store.Open(t.TempDir(), custosBin, "http://custos.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InstallHooks(); err != nil {
		t.Fatal(err)
	}
	bl, err := blobs.Open(filepath.Join(st.DataDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := auth.NewManager(auth.Config{DataDir: st.DataDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateUser("jane@example.org", func(u *auth.User) { u.Roles = auth.Roles{Admin: true} }); err != nil {
		t.Fatal(err)
	}
	_, token, err := m.CreateToken("jane@example.org", "test")
	if err != nil {
		t.Fatal(err)
	}
	h, err := server.New(st).WithAPI(api.New(st, bl).Handler()).WithMiddleware(m.Middleware("/healthz")).Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("token", token)
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", u.String()+"/git/catalog.git", "main")
	if err := st.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	return &testServer{st: st, bl: bl, url: u.String()}
}
```

Add `"net/url"` and `"github.com/emeland-io/custos/internal/auth"` to this file's imports. `t.Setenv("CUSTOS_TOKEN", "")` at the top of `TestMain` or each test that must prove it reads the token from the URL, not the environment, is optional polish, not required for correctness (an empty env var is already a no-op in `bearerToken`).

`push_test.go`: every `Push(dir, "origin", []string{...}, jane, &log)` call drops the `jane` argument — `Push(dir, "origin", []string{...}, &log)` — across `TestPushAllScansEveryBranch`, `TestPushHealsAttachmentMissedByPlainGitPush`, `TestPushUploadsAttachments`, `TestPushSkipsBlobsTheServerHas`, `TestPushRefusesMissingAttachment` and `TestPushRefusesWrongLocalContent` (six call sites, same mechanical edit each time: delete the `jane,` argument).

- [ ] **Step 11: `custos push` drops `--author`** — edit `cmd/custos/remote.go`

```go
// runPush uploads the attachments of the commits to push, then runs git
// push. Arguments after REMOTE (refspecs, flags such as --force) go to
// git. Authentication is the remote URL's own (userinfo, or a credential
// helper) plus $CUSTOS_TOKEN for custos's own attachment upload — there is
// no --author: a commit's author was always "git commit" on this machine,
// never this command.
func runPush(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("push", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dir := fl.String("dir", ".", "git checkout to push")
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	remoteName, gitArgs := "origin", []string(nil)
	if fl.NArg() > 0 {
		remoteName, gitArgs = fl.Arg(0), fl.Args()[1:]
	}
	if err := remote.Push(*dir, remoteName, gitArgs, stderr); err != nil {
		fmt.Fprintf(stderr, "custos push: %v\n", err)
		return 1
	}
	return 0
}
```

`gitrepo` and `os` may now be unused imports in this file if `runClone` is the only other function in it — check with `goimports`/`go vet` and drop them if so (`runClone` itself does not reference either).

`cmd/custos/main.go`'s usage line for `push` loses `[--author "NAME <EMAIL>"]`:

```
  custos push [--dir DIR] [REMOTE [GIT-PUSH-ARGS...]]
```

- [ ] **Step 12: Update `cmd/custos`'s own tests**

`remote_test.go`: delete the three tests exercising `push --author` (around the lines this plan's Step 1 grep found) — `--author` is simply an unknown flag now, which `flag.ContinueOnError` already rejects with its own message; that shape of error ("flag provided but not defined") is already covered by this package's existing coverage of other unknown/malformed flags (`TestServeProcessorFlags` and siblings), so no replacement test is needed here.

`runs_test.go`: `apiCall` gains a `token string` parameter, used instead of the hardcoded header, and a new helper mints the bootstrap admin every test in this file (and `distribute_test.go`, `shutdown_test.go`, which share `apiCall`) needs:

```go
// bootstrapAdmin creates an admin over dataDir and returns their token —
// the one credential every apiCall and git clone/push in this package's
// tests below presents, the same way a real operator would run
// custos token create once after custos serve's first start.
func bootstrapAdmin(t *testing.T, dataDir string) string {
	t.Helper()
	m, err := auth.NewManager(auth.Config{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateUser("jane@example.org", func(u *auth.User) { u.Roles = auth.Roles{Admin: true} }); err != nil {
		t.Fatal(err)
	}
	_, token, err := m.CreateToken("jane@example.org", "test")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// api calls the server's REST API and decodes the JSON answer into out
// (unless nil); it fails the test on another status than want.
func apiCall(t *testing.T, ts *httptest.Server, token, method, path, body string, want int, out any) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
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
```

`waitForRun`'s signature gains `token string`, forwarded to each `apiCall` it makes; every call site of both functions across `runs_test.go`, `distribute_test.go` and `shutdown_test.go` gains one argument — the token `bootstrapAdmin(t, data)` returned right after `openServer` built that test's server (each test already has its own `data`/`dataDir` local; call `bootstrapAdmin` once per test, right after `openServer` returns, before the first `apiCall`).

`openServer`'s two direct test call sites (`runs_test.go`'s `TestProcessorRunsEndToEnd`, `shutdown_test.go`'s two tests) pass `auth.Config{DataDir: data}` (token-only mode — these tests are not about auth) as the new argument Task 8 added.

`distribute_test.go`'s `wired` helper (its own hand-built stack, separate from `openServer`, used only by `TestCatalogPushIsDistributed`) needs the same treatment as `internal/remote/remote_test.go`'s `startServer` in Step 10: build an `auth.Manager`, bootstrap `jane@example.org` as admin, add `.WithMiddleware(m.Middleware("/healthz"))` to the `server.New(st)` chain, and return the authed URL (or the token, forwarded into the git remote URLs this test builds) alongside what it already returns. The git clone/push URLs this test builds by hand (`ts.URL + "/git/catalog.git"`, the workspace clone URL) each need the token embedded exactly as in Step 10's `authedURL`-style treatment.

- [ ] **Step 13: README**

Add an "Authentication" section (sign-in, roles, `custos token create`, `CUSTOS_TOKEN`) and update every example that currently shows `X-Custos-Author` or `custos push --author` to show a token instead. (Left as prose guidance rather than a literal diff here: the exact current README wording was not part of this plan's own research and should be read fresh before editing.)

- [ ] **Step 14: Run everything**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS, and `grep -rn "X-Custos-Author\|authorHeader" --include='*.go' .` (Step 1's check, run again) finds nothing.

- [ ] **Step 15: Commit**

```bash
gofmt -l internal cmd
go vet ./...
git add internal/api internal/merge internal/distribute internal/runs internal/remote internal/gittest cmd/custos README.md
git commit -m "Remove X-Custos-Author: commits are authored by the signed-in caller everywhere

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Decisions beyond the architecture note

- **`GET /auth/config`** (unauthenticated, always 200, `{"oidc": bool}`) is a
  new, small endpoint added during cross-plan review, not originally in the
  architecture note's Plan 4a section (it is now — architecture note ruling
  18). It exists because `/auth/me` always answers 401 to an
  unauthenticated request in this plan (as its own endpoint table already
  said), but plan 4c's `LoginView.vue`/`stores/auth.ts` were written reading
  a 404 from `/auth/me` as "OIDC is off, offer the token-paste box" — a
  signal this plan's `/auth/me` never actually sends. Overloading
  `/auth/me`'s status code for two different meanings (not-yet-signed-in vs.
  no-sign-in-exists) was rejected in favor of a third, explicit signal; `/auth/me`
  itself is otherwise unchanged. See plan 4c's "Decisions" for the matching
  frontend fix.
- `auth.Principal(r *http.Request) (Principal, bool)` is renamed `auth.FromRequest` — the architecture note's own name does not compile (`type Principal struct` and `func Principal(...)` in one package) — flagged in Global Constraints as this entry; every citation of "the note's `Principal(r)`" elsewhere in this plan means `FromRequest`.
- `FromRequest` does not keep a context key of its own: it calls `api.CallerOf` and type-asserts the result back to `Principal` — `Manager.Middleware` is then the *only* code that ever puts a caller in a request's context (through `api.WithCaller`), so `api` and `auth` can never disagree about which caller a request carries — the alternative (a second, `auth`-owned context key) would let the two go out of sync if a future handler set one but not the other.
- `api.RequireRole` (Task 1, string-keyed, no `auth` import) is the only place role-gating logic actually lives; `auth.Require`/`auth.RequireWorkspace` (Task 3) are one-line wrappers over it. `internal/api/api.go`'s own two guarded routes call `RequireRole` directly with literal role strings (`"admin"`, `"engineer"`) instead of `auth.RoleAdmin`/`auth.RoleEngineer`, because `internal/api` must not import `internal/auth` (Review Focus 3) — `internal/merge`, `internal/distribute` and `internal/runs`, which `auth` does not import, use `auth.Require`/`RequireWorkspace` instead, since nothing stops them and the named constants read better there.
- Session and sign-in-state cookies are both `base64url(json claims) + "." + base64url(HMAC-SHA256)`, distinguished by a `kind` field inside the signed payload (`"session"` vs `"login"`) and, for a session, the OIDC issuer URL it was minted against — neither distinction is in the architecture note, which only says "signed session cookies"; both exist specifically to fail Review Focus 1's "really the sign-in state cookie" and "minted for another OIDC issuer" cases without a second secret or a server-side revocation list.
- `Manager.Middleware`'s cross-origin check (Review Focus 2) treats only cookie- and Basic-auth-resolved callers as "ambient" and subject to `http.CrossOriginProtection`; a caller resolved from an `Authorization: Bearer` header is exempt, because a cross-site page cannot attach an arbitrary header to a request without a CORS preflight the server never grants it — the architecture note names `http.CrossOriginProtection` but not which credentials it should gate.
- `PUT /api/admin/users/{email}/roles`'s body (`rolesUpdate`, Task 5) has every field optional and merges only the ones present into the user's existing roles; the architecture note's own body sketch (`{"admin"?,"task_author"?,...}`) already implies this, but the Endpoints table this plan's own intro fixed does not spell out "merge, not replace" in the response column, so it is called out here explicitly against a later reader assuming the opposite.
- `custos token create`'s role flags (`--admin`, `--task-author`, ...) only ever add a role to the user's existing record, never remove one — an operator revokes a role through `PUT /api/admin/users/{email}/roles` (or by editing `users.json`), not by re-running this command without a flag it previously set.
- The test identity provider (`internal/auth/idp_test.go`, Task 4) is built by hand on `net/http/httptest` and `crypto/rsa` rather than on `coreos/go-oidc/v3/oidc/oidctest`, which the Tech Stack line names for "the discovery document and JWKS": that package's exact surface in the vendored v3.20.0 could not be confirmed while writing this plan (no network access from the planning session), and a wrong guess at its API would have cost the implementer more than the ~80 lines this plan wrote instead. **Open question for the cross-plan reviewer:** if `oidctest` does offer a ready-made discovery/JWKS server, Task 4 Step 1 should use it and `idp_test.go` shrinks; either way `oidc.go` itself is unaffected, since it only ever calls `go-oidc`'s own public `NewProvider`/`Verifier`/`Verify`.
- `internal/server.Server`'s sign-in gate on `/api/` and `/git/` (`requireSignedIn`, Task 8) is unconditional — it only ever asks `api.CallerOf`, never whether `WithMiddleware` was called — so a `Server` built with `WithAPI`/`WithAuth` but no `WithMiddleware` refuses every request there instead of serving them anonymously. This is Review Focus 5's own requirement, called out again here because it is a specific, load-bearing property of `Handler`'s structure (two independent pieces, not one gate that happens to run after middleware), not just a test that happens to pass.
- `internal/remote`'s own HTTP requests (blob upload in `Push`, download in `Clone`) authenticate from `$CUSTOS_TOKEN` or, failing that, the git remote URL's own userinfo password; `custos push` drops `--author` entirely rather than repurposing it. The architecture note names exactly these two token sources; `--author` had never been anything but the value that header carried, so once the header is gone the flag has nothing left to do.
- `GitAuthor` (`internal/remote/remote.go`) and its test are deleted, not kept — once `--author` is gone, nothing in the tree calls it. This is a judgment call, not required by the architecture note: the alternative (leaving a now-uncalled exported helper in place in case something else wants "the git committer's own name and email" later) was rejected to match this repository's own practice of not keeping unused exported surface around.
- `internal/distribute/api_test.go`'s real-HTTP test harness (`httptest.NewServer` plus a real `http.Client`, with no shared Go context across that hop) stands in for `auth.Middleware` with a single fixed request header that resolves to a caller holding every role, rather than wiring a real `auth.Manager` and minting a token — this package's tests are about freeze/proposal logic, which Task 3 and Task 4 already cover for session/token resolution itself. `internal/merge` and `internal/runs`'s tests dispatch in-process (`http.Handler.ServeHTTP` directly, no network hop) and so inject the caller straight into the request's context instead, needing no stand-in at all.
- `--addr`'s "there is no authentication yet, so keep it on loopback" usage caveat is corrected in **this** plan (Task 8), even though the architecture note's prose mentions the same correction under its "Plan 4c" section (alongside `WithUI`). 4a is the plan that makes the sentence false, so leaving a known-false security claim in `serve -h`'s own output for a whole extra phase was rejected. **Resolved by cross-plan review:** plan 4c's own document did also claim this exact edit (its Task on `cmd/custos/serve.go`), with different replacement wording and also touching `defaultAddr`'s doc comment, which this plan's Task 8 does not touch. Plan 4c has been edited so its flag-usage-text edit is a no-op note ("already corrected by 4a; nothing to do here") and it keeps only the `defaultAddr` doc-comment fix, which this plan does not make and which was otherwise going to be left stale. No edit was needed here in 4a.
- Every pre-existing test that predates per-endpoint roles (`internal/server/server_test.go`'s `start`, `internal/remote/remote_test.go`'s `startServer`, and `cmd/custos`'s `apiCall`-based integration tests) is given one bootstrap admin identity (`jane@example.org`, `Roles{Admin: true}`) rather than a caller scoped to exactly what each test needs — `Admin` already implies every role everywhere, and these tests are not testing authorization, so enumerating per-test roles would only add noise `TestPushNeedsRole`/`TestAPINeedsSignIn`/`TestRequireRole` already cover properly.

## Rulings for spec §11.4

1. `auth.Principal(r)` from the architecture note is implemented as `auth.FromRequest(r)`; the type keeps the name `Principal`.
2. Session and sign-in-state cookies share one signed-cookie format, told apart by a `kind` field in the signed payload; a session's payload also carries the OIDC issuer it was minted against.
3. `http.CrossOriginProtection` gates only cookie- and Basic-resolved callers on unsafe requests; a caller resolved from `Authorization: Bearer` is exempt.
4. `PUT /api/admin/users/{email}/roles` merges the fields present in its body into the user's existing roles; it never replaces the whole set.
5. `custos token create`'s role flags are additive only; revoking a role goes through the admin roles endpoint or a direct edit of `users.json`.
6. `internal/api` guards its own two write routes with literal role strings through its own `RequireRole`, never importing `internal/auth`; every other package that guards a route imports `internal/auth` and uses `Require`/`RequireWorkspace`.
7. `custos`'s own HTTP requests (blob upload and download) authenticate from `$CUSTOS_TOKEN` or the git remote URL's userinfo password; `custos push` has no `--author` flag.
8. The OIDC test identity provider is hand-built on the standard library, not on `go-oidc`'s `oidc/oidctest` package, pending confirmation of that package's API in the vendored version.
9. `server.Server`'s sign-in gate on `/api/` and `/git/` is unconditional: it is `WithMiddleware` that is optional (and, unset, leaves every such request refused), not the gate itself.
10. `GitAuthor` is removed from `internal/remote` along with `--author` on `custos push`, rather than kept as unused exported surface.
11. `GET /auth/config` (unauthenticated, `{"oidc": bool}`) is added so the frontend can tell token-only mode apart from a plain "not yet signed in" 401; `GET /auth/me` never varies its status code for that purpose (architecture note ruling 18, added during cross-plan review).
12. The `--addr` usage-text correction (ruling 4.15) is made once, in this plan's Task 8; plan 4c's matching task was found to also claim it and was edited to a no-op for that specific string, keeping only its own, separate fix to `defaultAddr`'s stale doc comment.
