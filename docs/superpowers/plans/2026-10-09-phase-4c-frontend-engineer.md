# Phase 4c (frontend scaffold and Engineer views) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** stand up the whole `web/` npm project (Vue 3 + Vite + vue-router + Pinia + Tailwind CSS), the `internal/webui` Go embedding package, and the server/Makefile wiring that wires them together, then build the Engineer-persona views: login, dashboard, answering, proposal review and the book. This is the first frontend code in the repository.

**Spec:** [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md) §1 (Personas), §3.3 (Answer/Verification file shapes), §4.2 (answering), §6.1 (web UI, Engineer bullet), §6.4 (authentication and roles), §7 (error handling), §8 (testing). Shared names and conventions: [2026-10-09-phase-4-architecture.md](2026-10-09-phase-4-architecture.md) — "Conventions" (all plans) and its "Plan 4c" section fix this plan's scope; "Plan 4a" and "Plan 4b" fix the backend contracts this plan is the first consumer of.

**Requires:** plans 4a and 4b executed first, exactly as the architecture note describes them (their code is not yet in this tree at the time this plan is written — only the note is the shared contract):

- 4a: `GET /auth/me` → `{"name","email","roles":{"admin","task_author","processor_author","engineer":[...]}}` or 401, or **404 when `--oidc-issuer-url` is empty** (token-only mode — this is the architecture note's own Plan-4c text, see "Decisions" §3 below for a tension with 4a's endpoint table); `GET /auth/login` (302 to the IdP, 404 when OIDC is off); `POST /auth/logout` (204); session cookie auth, `credentials: "include"`, no `X-Custos-Author` anywhere any more.
- 4b: `GET /api/workspaces/{id}/book?format=html|markdown` (default `markdown`) — the only change 4b makes to an endpoint this plan calls. Catalog draft editing (`/api/catalog/drafts/...`) is Task-Author surface and out of this plan's scope (plan 4d).

From phases 1-3, read directly from the code in this tree (not just the note) to get every JSON field name and type exactly right:

- `internal/api/read.go`: `statusJSON`, `taskStatusJSON`, `answerJSON`, `attachmentJSON`, `bookEntryJSON`, `bookGroupJSON`; `GET /api/workspaces/{id}/status`, `GET /api/workspaces/{id}/book`, `GET /api/workspaces/{id}/answers/{task}`.
- `internal/api/write.go`: `answerRequest`, `answerResponse`; `PUT /api/workspaces/{id}/answers/{task}`.
- `internal/api/stillvalid.go`: `POST /api/workspaces/{id}/answers/{task}/still-valid`.
- `internal/api/workspaces.go`: `workspaceJSON` (`id`,`pin`,`frozen`); `GET /api/workspaces`, `POST /api/workspaces`. There is no `GET /api/workspaces/{id}`: a single workspace's `frozen`/`pin` is read by filtering the list.
- `internal/api/blob.go`: `POST /api/blobs` takes the **raw request body** (no JSON envelope, no multipart) and returns `{"sha256","size"}`; `GET /api/blobs/{sha256}` serves it back as `application/octet-stream`.
- `internal/api/catalog.go`: `taskVersionJSON`, `taskHistoryJSON`; `GET /api/catalog/tasks/{id}` — **catalog tasks only**; there is no equivalent for a generated task (see "Decisions" §7, flagged for the cross-plan reviewer).
- `internal/api/response.go`: `errorJSON{"error","problems"?}`, `problemJSON{"path","rule","message"}` — the error body shape for every non-2xx response.
- `internal/status/status.go`: `status.State` (`unanswered`,`answered`,`pending-update`), `TaskStatus`, `Entry`.
- `internal/runs/json.go` + `internal/runs/api.go`: `ProposalJSON{"branch","commit","task","digest"?,"cascade","items"}`, `ItemJSON{"kind","match_key","action","id","from"?,"to"?,"title","verification"?}`; `GET /api/workspaces/{id}/processor-proposals`, `.../processor-proposals/{task}/accept` (body `{"items":[...]}`, selectors are `"<kind>:<match_key>"`, see `internal/proposal/diff.go`'s `selector`), `.../processor-proposals/{task}/reject`.
- `internal/distribute/proposals.go` + `internal/distribute/api.go`: `PinProposal{"branch","commit","from","to","changes"}`, `Diff{"new_versions","added","superseded","groups_changed","bindings_changed"}`; `GET /api/workspaces/{id}/proposals`, `.../proposals/accept` and `.../proposals/reject` (body `{"branch"}`), `POST /api/workspaces/{id}/freeze` / `/unfreeze` (→ `{"id","frozen","warning"?}`).
- `internal/runs/record.go`: `Record` (`state` one of `queued`,`running`,`succeeded`,`failed`).
- `internal/task/task.go`: `AnswerType` exact values `markdown`, `text`, `timestamp`, `path`, `url`, `choice`.
- `internal/server/server.go`: current `Server`, `WithAPI`, `Handler()` — this plan adds `WithUI` beside `WithAPI` and mounts it in `Handler()`.

If a signature or shape differs from what is written here once 4a/4b actually land, adapt the call and say so in the task's report — do not silently paper over a mismatch.

## Global Constraints

- Go 1.26, module `github.com/emeland-io/custos`. This plan adds **no new backend third-party module** (`goldmark` is 4b's; `coreos/go-oidc`/`golang.org/x/oauth2` are 4a's).
- Frontend toolchain: directory `web/`, a separate npm project — Vue 3 (`<script setup>`, TypeScript), Vite, vue-router, Pinia, Tailwind CSS. Node ≥ 20, npm as the package manager; `web/package-lock.json` is committed (generated by running `npm install` with network access — this plan does not fabricate a lockfile). `web/node_modules/` and `web/dist/*` (except `web/dist/.gitkeep`) are already gitignored; no `.gitignore` change is needed.
- **No frontend dependency beyond Vue/Vite/vue-router/Pinia/TypeScript/Tailwind (plus their own build-time tooling: `@vitejs/plugin-vue`, `vue-tsc`, `postcss`, `autoprefixer`) without flagging it as a decision.** In particular: no UI component library, no HTTP client library beyond the plain `fetch` wrapper this plan writes, no Markdown-rendering library, no diff library (see "Decisions" §4).
- `internal/webui/dist` must always contain *something* buildable even with zero npm installed (ruling 4.2): it is committed to Git with a placeholder `index.html` and `.gitkeep`; `make build`/`make test` overwrite it with the real build.
- `make test` needs Node.js and npm on `PATH` in addition to Docker and python3 (ruling 4.3 extends 3.80): it runs `cd web && npm ci && npm run type-check` before the Go tests. **No frontend component/unit test runner in this phase** (ruling 4.4): `vue-tsc --noEmit` is the only automated frontend check; every view task below instead specifies a manual click-through pass, which the executing engineer records in the task's report.
- Session auth is cookie-based (`credentials: "include"`); there is **no token entry in the normal web login flow** — personal access tokens are the CLI/Git/token-only-mode fallback (plan 4a), and this plan's only UI for one is `LoginView.vue`'s token-only-mode paste box (Task 8).
- `X-Custos-Author` no longer exists; `api/client.ts` never sends it.
- Follow this repo's existing Go doc-comment and test style for `internal/webui` (see `internal/status/status.go`'s package doc, and `internal/server/server_test.go`'s table-free, one-assertion-per-test style, as models).
- Commit messages: one imperative sentence, a blank line, `Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>` — the live executing session's own attribution, not necessarily this plan's (ruling 3.81 from phase 3 applies here too: use whatever the executing session actually is).

## File Structure

| File | Responsibility |
| --- | --- |
| `web/package.json`, `vite.config.ts`, `tsconfig.json`, `tailwind.config.js`, `postcss.config.js`, `index.html` | Frontend project scaffold |
| `web/src/style.css` | Tailwind entry point |
| `web/src/main.ts` | `createApp`, `pinia`, `router`, mounts `#app` |
| `web/src/App.vue` | Top nav (role-aware), `<router-view>` |
| `web/src/router/index.ts` | Routes and the auth guard |
| `web/src/stores/auth.ts` | Pinia store: principal, roles, `login`/`logout`/token fallback |
| `web/src/api/client.ts` | Typed `fetch` wrapper, `ApiError` |
| `web/src/api/token.ts` | Holds the token-only-mode bearer token (keeps `client.ts` and `stores/auth.ts` decoupled) |
| `web/src/lib/diff.ts` | Hand-rolled line diff (task-version diff) |
| `web/src/lib/markdown.ts` | Hand-rolled safe Markdown→HTML subset renderer |
| `web/src/types/*.ts` | Hand-written TypeScript mirrors of the Go JSON shapes |
| `web/src/components/RoleGate.vue`, `ProblemList.vue`, `MarkdownView.vue` | Shared widgets |
| `web/src/views/LoginView.vue` | OIDC button / token-only-mode fallback |
| `web/src/views/engineer/DashboardView.vue` | Workspace picker + status |
| `web/src/views/engineer/AnswerView.vue` | Answer form by type, attachments, diff, still-valid |
| `web/src/views/engineer/ProposalsView.vue` | Pin proposals (2c) + processor proposals (3d) |
| `web/src/views/engineer/BookView.vue` | Book export + freeze/unfreeze |
| `internal/webui/webui.go`, `webui_test.go` | `go:embed` handler for the built frontend |
| `internal/webui/dist/index.html`, `dist/.gitkeep` | Committed placeholder so `go build`/`go test` need no Node |
| `internal/server/server.go` | `+Server.ui`, `+WithUI`, `Handler()` mounts it |
| `internal/server/server_test.go` | `+TestWithUIServesTheUIAtRoot` |
| `cmd/custos/serve.go` | Wires `webui.Handler()`; corrects `--addr`'s usage text (ruling 4.15) |
| `Makefile` | `+web-build`, `+web-typecheck`; `build`/`test` depend on them |

---

## Task 1 — Frontend project scaffold (`web/`)

**Files:** create `web/package.json`, `web/vite.config.ts`, `web/tsconfig.json`, `web/tailwind.config.js`, `web/postcss.config.js`, `web/index.html`, `web/src/style.css`, `web/src/main.ts`, `web/src/App.vue`.

`web/package.json`:

```json
{
  "name": "custos-web",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "engines": { "node": ">=20" },
  "scripts": {
    "dev": "vite",
    "build": "vue-tsc --noEmit && vite build",
    "type-check": "vue-tsc --noEmit"
  },
  "dependencies": {
    "pinia": "^2.3.0",
    "vue": "^3.5.13",
    "vue-router": "^4.5.0"
  },
  "devDependencies": {
    "@types/node": "^22.10.2",
    "@vitejs/plugin-vue": "^5.2.1",
    "autoprefixer": "^10.4.20",
    "postcss": "^8.4.49",
    "tailwindcss": "^3.4.17",
    "typescript": "~5.7.3",
    "vite": "^6.0.7",
    "vue-tsc": "^2.2.0"
  }
}
```

`@types/node` (not in an earlier draft of this plan) is needed because
`vite.config.ts` calls `process.cwd()` — found while applying this plan
to a scratch copy: `npm run type-check` fails with "Cannot find name
'process'" without it, and `tsconfig.json`'s `"types"` array (above) must
also list `"node"`, since that array restricts which `@types/*` packages
TypeScript auto-includes.

`web/vite.config.ts` — the dev proxy to `CUSTOS_DEV_BACKEND` the architecture note's Conventions require:

```ts
import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

// For local development, `npm run dev` serves the SPA on Vite's own port
// and proxies /api, /auth and /git to a real `custos serve` (default
// http://127.0.0.1:8080, overridable with CUSTOS_DEV_BACKEND) so the SPA
// talks to the same origin in dev as it does once embedded.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const backend = env.CUSTOS_DEV_BACKEND || 'http://127.0.0.1:8080'
  return {
    plugins: [vue()],
    server: {
      proxy: {
        '/api': { target: backend, changeOrigin: true },
        '/auth': { target: backend, changeOrigin: true },
        '/git': { target: backend, changeOrigin: true },
      },
    },
    build: {
      outDir: 'dist',
    },
  }
})
```

`web/tsconfig.json` — one config for both `src/` and `vite.config.ts` (see "Decisions" §9):

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "useDefineForClassFields": true,
    "resolveJsonModule": true,
    "isolatedModules": true,
    "skipLibCheck": true,
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noEmit": true,
    "types": ["vite/client", "node"]
  },
  "include": ["src/**/*.ts", "src/**/*.vue", "vite.config.ts"]
}
```

`web/tailwind.config.js` (Tailwind v3's conventional config file — see "Decisions" §1 for why v3, not v4). Written as an ES module (`export default`, not `module.exports`): `web/package.json` declares `"type": "module"`, so Node and Vite load a `.js` file here as an ES module — found while applying this plan to a scratch copy, `vite build` failed with "module is not defined in ES module scope" against the original `module.exports` form:

```js
/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,ts}'],
  theme: {
    extend: {},
  },
  plugins: [],
}
```

`web/postcss.config.js` (same fix, same reason):

```js
export default {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
}
```

`web/index.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>custos</title>
  </head>
  <body>
    <div id="app"></div>
    <script type="module" src="/src/main.ts"></script>
  </body>
</html>
```

`web/src/style.css`:

```css
@tailwind base;
@tailwind components;
@tailwind utilities;
```

`web/src/main.ts` — minimal for now; Task 6 adds `pinia` and `router`:

```ts
import { createApp } from 'vue'
import App from './App.vue'
import './style.css'

createApp(App).mount('#app')
```

`web/src/App.vue` — a placeholder; Task 7 replaces it with the real nav:

```vue
<script setup lang="ts"></script>

<template>
  <div class="p-6 text-gray-900">
    <h1 class="text-xl font-semibold">custos</h1>
    <p class="text-sm text-gray-500">Frontend scaffold OK — later tasks add the router, stores and views.</p>
  </div>
</template>
```

**Test:**

```
cd web && npm install && npm run type-check
```

Expected: no errors (generates `web/package-lock.json`; commit it).

```
cd web && npm run build
```

Expected: `web/dist/index.html` plus hashed asset files.

**Commit:**

```bash
git add web/package.json web/package-lock.json web/vite.config.ts web/tsconfig.json \
  web/tailwind.config.js web/postcss.config.js web/index.html web/src/style.css \
  web/src/main.ts web/src/App.vue
git commit -m "Scaffold the web/ frontend project (Vue 3, Vite, Tailwind, TypeScript)

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 2 — `internal/webui`: embed the built frontend

**Files:** create `internal/webui/webui.go`, `internal/webui/webui_test.go`, `internal/webui/dist/index.html`, `internal/webui/dist/.gitkeep`.

`internal/webui/dist/index.html` — the committed placeholder (ruling 4.2):

```html
<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <title>custos</title>
  </head>
  <body>
    custos UI not built — run `make build` (needs Node.js and npm on PATH) to embed the real frontend.
  </body>
</html>
```

`internal/webui/dist/.gitkeep` — empty file, so the directory exists before `go:embed` runs even if nothing else is tracked under it yet.

`internal/webui/webui.go`:

```go
// Package webui serves the built frontend (web/dist, copied into
// internal/webui/dist by `make build`'s web-build target — go:embed
// cannot reach outside its own module directory with "..") as the custos
// binary's single-page application.
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed dist
var distFS embed.FS

// Handler serves dist/ at the root of the mux it is mounted on
// (server.Server.WithUI, at "/"): a real file as itself, with the
// Content-Type net/http infers from its extension, and dist/index.html
// for any other path, so that a full page load of a client-side route
// such as /w/<id> still gets the single-page application's shell. A path
// whose last segment contains a "." but that dist does not have 404s
// instead of falling back to index.html, so a mistyped or stale asset URL
// is visible as a 404 rather than a blank page trying to parse HTML as
// JavaScript or CSS.
func Handler() (http.Handler, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// path.Clean("/") is "/", and TrimPrefix(..., "/") of that is ""
		// (not "."): an earlier version of this handler checked only for
		// "." and so never matched a bare "/" at all, 404ing the root —
		// found while applying this plan to a scratch copy
		// (TestHandlerServesIndexAtRoot failed). "" and "." both mean
		// "serve index.html".
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || p == "." {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			if strings.Contains(path.Base(p), ".") {
				http.NotFound(w, r)
				return
			}
			serveIndex(w, r, sub)
			return
		}
		files.ServeHTTP(w, r)
	}), nil
}

// serveIndex writes dist/index.html directly, rather than routing a
// crafted "/index.html" request through http.FileServer: FileServer's own
// serveFile has a built-in redirect for any request whose path ends in
// "/index.html" (a 301 to the canonical directory URL, meant for a real
// browser following links) — found while applying this plan to a scratch
// copy (TestHandlerFallsBackToIndexForAClientRoute got a 301 instead of
// the SPA shell): that redirect fired for every client-side route (e.g.
// /w/<id>) instead of this handler's intended "serve the SPA shell"
// fallback. An earlier version of this function built a shell *http.Request
// with its URL.Path set to "/index.html" and routed it through files
// (http.FileServer); this version reads and serves the bytes directly
// instead.
func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(data))
}
```

`internal/webui/webui_test.go`:

```go
package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndexAtRoot(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "custos") {
		t.Errorf("body = %q, want it to mention custos", rr.Body.String())
	}
}

func TestHandlerFallsBackToIndexForAClientRoute(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/w/00000000-0000-4000-8000-000000000000", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "custos") {
		t.Errorf("body = %q, want the index.html shell", rr.Body.String())
	}
}

func TestHandler404sAnUnknownAsset(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/assets/does-not-exist.js", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}
```

**Test:**

```
go test ./internal/webui/...
```

Expected: `ok` — these pass against the committed placeholder alone, no Node needed.

**Commit:**

```bash
git add internal/webui/webui.go internal/webui/webui_test.go \
  internal/webui/dist/index.html internal/webui/dist/.gitkeep
git commit -m "Add internal/webui, embedding the built frontend with a committed placeholder

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 3 — Wire the UI into `server.Server`, `cmd/custos`, and the `Makefile`

**Files:** modify `internal/server/server.go`, `internal/server/server_test.go`, `cmd/custos/serve.go`, `Makefile`.

### `internal/server/server.go`

Add a field and a chain method mirroring `WithAPI` (shown against today's file; plan 4a is expected to have already added an `auth` field, `WithAuth`, an `mw` field and `WithMiddleware` by the time this lands for real — splice this in alongside those, the two sets of additions do not interact):

```go
// Server serves the repositories of a store. Repository layout, hooks and
// writes belong to the store (ruling 2.17).
type Server struct {
	st  *store.Store
	api http.Handler // mounted at /api/ when set
	ui  http.Handler // mounted at / when set

	mu              sync.Mutex
	onCatalogPush   []func()
	onWorkspacePush []func(id string)
	onShutdown      []func()
}
```

```go
// WithUI mounts h at "/" in the handler that Handler returns, so that any
// request none of the more specific prefixes (/git/, /api/, /auth/,
// /healthz) claims falls through to the embedded single-page application
// (internal/webui.Handler()), which serves its own index.html for every
// client-side route. It returns s.
func (s *Server) WithUI(h http.Handler) *Server {
	s.ui = h
	return s
}
```

In `Handler()`, add the mount after the existing `if s.api != nil` block (`http.ServeMux` resolves by longest matching pattern, not registration order, so this is safe wherever it is added relative to `/auth/`):

```go
	if s.api != nil {
		mux.Handle("/api/", s.api)
	}
	if s.ui != nil {
		mux.Handle("/", s.ui)
	}
	return mux, nil
}
```

### `internal/server/server_test.go`

Add, next to `TestHealthz`:

```go
func TestWithUIServesTheUIAtRoot(t *testing.T) {
	ui := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ui: %s", r.URL.Path)
	})
	h, err := New(store.New(t.TempDir(), custosBin, "http://custos.test")).WithUI(ui).Handler()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/w/some-workspace", nil))
	if rec.Code != 200 || rec.Body.String() != "ui: /w/some-workspace" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
	// /healthz still takes priority over the catch-all UI mount.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 || rec.Body.String() != "ok\n" {
		t.Errorf("%d %q, want /healthz unaffected by WithUI", rec.Code, rec.Body.String())
	}
}
```

(`fmt` and `net/http` are already imported by this file.)

### `cmd/custos/serve.go`

Add the import and wire `webui.Handler()` right after `srv.WithAPI(a.Handler())` in `openServer`:

```go
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/webui"
)
```

```go
	srv := server.New(st)
	startDistribution(st, a, srv, stderr)
	startRuns(ctx, st, a, srv, svc)
	srv.WithAPI(a.Handler())
	ui, err := webui.Handler()
	if err != nil {
		return nil, err
	}
	srv.WithUI(ui)
	return srv, nil
}
```

**Cross-plan note (resolved during review):** the architecture note's prose
originally assigned the `--addr` usage-text correction to this plan, but
plan 4a's own Task 8 makes the sentence false first (real auth lands in
4a) and already corrects the flag's usage string there, with its own
wording. By the time this plan's implementer reaches this step, that
string will already have been changed by 4a's commit, so re-applying the
block below as a second, conflicting edit is a no-op for the flag text —
**do not re-edit `addr := fl.String(...)`'s usage string; it is already
done.** What 4a's Task 8 does *not* touch is `defaultAddr`'s doc comment,
which is `cmd/custos/serve.go`'s other stale "there is no authentication
yet" claim; fixing only that comment is this plan's real, remaining piece
of ruling 4.15:

```go
// defaultAddr listens on loopback only, the conservative default for a
// server that is not deliberately placed behind a reverse proxy.
const defaultAddr = "127.0.0.1:8080"
```

### `Makefile`

```make
BINARY := custos

.PHONY: all build test docker dev-server clean web-build web-typecheck

all: build

## build: build the binary (builds the frontend first, embedding it)
build: web-build
	go build -o $(BINARY) ./cmd/custos

## web-build: build the frontend and copy it into internal/webui/dist for go:embed
web-build:
	cd web && npm ci && npm run build
	rm -rf internal/webui/dist && cp -r web/dist internal/webui/dist

## test: vet and run all tests, including both SDKs and the frontend type check (needs git, docker, python3 and node/npm on PATH)
test: web-typecheck
	go vet ./...
	go test ./...
	cd sdk/go && go vet ./... && go test ./...
	python3 -m unittest discover -s sdk/python

## web-typecheck: type-check the frontend with vue-tsc (no build output)
web-typecheck:
	cd web && npm ci && npm run type-check

## docker: build the container image custos:dev
docker:
	docker build -t $(BINARY):dev .

## dev-server: build and serve repositories from ./tmp/data on 127.0.0.1:8080
## (run `make build` first so the embedded UI is current; for frontend
## development, run `npm run dev` in web/ against this server instead,
## which proxies /api, /auth and /git and hot-reloads)
dev-server: build
	./$(BINARY) serve --data-dir ./tmp/data

clean:
	rm -f $(BINARY)
```

**Test:**

```
go build ./... && go vet ./... && go test ./internal/server/... ./cmd/custos/...
```

Expected: builds and passes without Node (the committed placeholder is embedded).

```
make build
```

Expected (needs Node/npm): builds the real frontend into `internal/webui/dist`, then the Go binary; `./custos serve --data-dir ./tmp/data` and loading `http://127.0.0.1:8080/` shows the real scaffold page (Task 1's placeholder `App.vue`), not the "not built" placeholder.

**Commit:**

```bash
git add internal/server/server.go internal/server/server_test.go cmd/custos/serve.go Makefile
git commit -m "Wire the embedded frontend into server.Server and cmd/custos, and teach make build/test about it

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 4 — TypeScript types mirroring the Go JSON shapes

**Files:** create `web/src/types/error.ts`, `auth.ts`, `workspace.ts`, `status.ts`, `catalog.ts`, `proposals.ts`, `runs.ts`, `blob.ts`.

`web/src/types/error.ts` — mirrors `internal/api/response.go`'s `errorJSON`/`problemJSON`:

```ts
export interface Problem {
  path: string
  rule: string
  message: string
}

export interface ErrorBody {
  error: string
  problems?: Problem[]
}
```

`web/src/types/auth.ts` — mirrors plan 4a's `GET /auth/me` body, plus
`GET /auth/config` (architecture note ruling 18, added during cross-plan
review as the token-only-mode signal `stores/auth.ts` needs):

```ts
export interface Roles {
  admin: boolean
  task_author: boolean
  processor_author: boolean
  engineer: string[] // workspace ids, or "*" for every workspace
}

export interface Principal {
  name: string
  email: string
  roles: Roles
}

export interface AuthConfig {
  oidc: boolean
}
```

`web/src/types/workspace.ts` — mirrors `internal/api/workspaces.go`'s `workspaceJSON`:

```ts
export interface Workspace {
  id: string
  pin: string
  frozen: boolean
}
```

`web/src/types/status.ts` — mirrors `internal/api/read.go`'s `statusJSON` family and `internal/task.AnswerType`:

```ts
export type AnswerType = 'markdown' | 'text' | 'timestamp' | 'path' | 'url' | 'choice'

export interface Attachment {
  name: string
  sha256: string
  media_type: string
  available: boolean
}

export interface Answer {
  task: string
  task_version: string
  type: AnswerType
  value: string
  body: string
  attachments: Attachment[]
}

export type TaskState = 'unanswered' | 'answered' | 'pending-update'

export interface TaskStatus {
  id: string
  version: string // current version
  title: string
  answer_type: AnswerType
  choices?: string[] // present when answer_type is 'choice'; plan 4b, Task 8
  generated: boolean
  state: TaskState
  answer: Answer | null
  merged: Answer[]
  in_effect: Answer[]
}

export interface BookGroup {
  slug: string
  title: string
}

export interface BookEntry {
  depth: number
  group?: BookGroup
  task?: string
}

export interface Status {
  workspace: string
  pin: string
  tasks: TaskStatus[] // book order
  book: BookEntry[]
}
```

`web/src/types/catalog.ts` — mirrors `internal/api/catalog.go`'s `taskVersionJSON`/`taskHistoryJSON`:

```ts
import type { AnswerType } from './status'

export interface Ref {
  id: string
  version: string
}

export interface TaskVersion {
  id: string
  version: string
  title: string
  answer_type: AnswerType
  choices?: string[]
  previous: Ref[]
  body: string
  processor?: string
}

export interface TaskHistory {
  id: string
  current: string // "" when superseded
  superseded: boolean
  processor?: string
  versions: TaskVersion[] // lowest first
}
```

`web/src/types/proposals.ts` — mirrors `internal/runs/json.go` (`ProposalJSON`/`ItemJSON`) and `internal/distribute/proposals.go`+`diff.go` (`PinProposal`/`Diff`). These are two genuinely different shapes from two different endpoint families — see "Decisions" §5:

```ts
import type { Ref } from './catalog'

export interface Verification {
  status: 'verified' | 'failed' | 'unsigned'
  signers?: string[]
}

export type ItemAction = 'added' | 'new-version' | 'unchanged' | 'removed'

export interface ProposalItem {
  kind: 'task' | 'document'
  match_key: string
  action: ItemAction
  id: string
  from?: string
  to?: string
  title: string
  verification?: Verification
}

// ProcessorProposal is a processor output proposal (phase 3d), on branch
// custos/proposal/<task-uuid>/<digest>.
export interface ProcessorProposal {
  branch: string
  commit: string
  task: string
  digest?: string
  cascade: boolean
  items: ProposalItem[]
}

export interface Diff {
  new_versions: Ref[]
  added: Ref[]
  superseded: Ref[]
  groups_changed: boolean
  bindings_changed: boolean
}

// PinProposal is a catalog pin-update proposal (phase 2c), on branch
// custos/pin/<catalog-commit>.
export interface PinProposal {
  branch: string
  commit: string
  from: string
  to: string
  changes: Diff
}
```

`web/src/types/runs.ts` — mirrors `internal/runs/record.go`'s `Record`:

```ts
export type RunState = 'queued' | 'running' | 'succeeded' | 'failed'
export type RunOutcome = 'proposed' | 'unchanged' | ''

export interface RunRecord {
  id: string
  workspace: string
  task: { id: string; version: string }
  answer_path: string
  answer_blob: string
  answer_commit: string
  processor: string
  image: string
  digest: string
  key: string
  reason: string
  retry_of?: string
  state: RunState
  outcome?: RunOutcome
  branch?: string
  error?: string
  queued: string
  started?: string
  finished?: string
}
```

(Named `RunRecord`, not `Record`, so it never shadows TypeScript's built-in `Record<K, V>` utility type in a file that uses both.)

`web/src/types/blob.ts` — mirrors `internal/api/blob.go`'s `blobJSON`:

```ts
export interface Blob {
  sha256: string
  size: number
}
```

**Test:**

```
cd web && npm run type-check
```

Expected: no errors (these files are not imported by anything yet, but must still be syntactically valid TypeScript).

**Commit:**

```bash
git add web/src/types
git commit -m "Add hand-written TypeScript mirrors of the backend's JSON shapes

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 5 — API client and small hand-rolled helpers

**Files:** create `web/src/api/token.ts`, `web/src/api/client.ts`, `web/src/lib/diff.ts`, `web/src/lib/markdown.ts`.

`web/src/api/token.ts` — holds the token-only-mode bearer token in memory (and `sessionStorage`, so a reload does not force re-pasting it), kept separate from `stores/auth.ts` so `client.ts` does not have to import Pinia (see "Decisions" §6):

```ts
// api/token.ts holds the personal access token LoginView's token-only-mode
// fallback pastes in. Normal web sessions never use this (architecture
// note: "there is no token entry in the normal web login flow"). It is a
// plain module, not a Pinia store, so api/client.ts can read it without
// importing stores/auth.ts, which itself imports client.ts.
const STORAGE_KEY = 'custos-token'

let token: string | null = (() => {
  try {
    return sessionStorage.getItem(STORAGE_KEY)
  } catch {
    return null // a private window or blocked storage: re-paste after reload
  }
})()

export function getToken(): string | null {
  return token
}

export function setToken(t: string): void {
  token = t
  try {
    sessionStorage.setItem(STORAGE_KEY, t)
  } catch {
    // ignore: the token still works for this page's lifetime
  }
}

export function clearToken(): void {
  token = null
  try {
    sessionStorage.removeItem(STORAGE_KEY)
  } catch {
    // ignore
  }
}
```

`web/src/api/client.ts`:

```ts
// api/client.ts is the only HTTP client this project uses: a thin, typed
// wrapper around fetch. Same-origin, session-cookie authenticated
// (credentials: "include"); in token-only mode it also attaches a bearer
// token from api/token.ts. No HTTP client library is added (Global
// Constraints).
import type { ErrorBody } from '../types/error'
import { getToken } from './token'

export class ApiError extends Error {
  readonly status: number
  readonly body: ErrorBody

  constructor(status: number, body: ErrorBody) {
    super(body.error || `request failed with status ${status}`)
    this.status = status
    this.body = body
  }
}

function authHeaders(): Record<string, string> {
  const token = getToken()
  return token ? { Authorization: `Bearer ${token}` } : {}
}

async function parseBody(res: Response): Promise<unknown> {
  const text = await res.text()
  return text ? JSON.parse(text) : undefined
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { ...authHeaders() }
  let requestBody: BodyInit | undefined
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    requestBody = JSON.stringify(body)
  }
  const res = await fetch(path, { method, headers, body: requestBody, credentials: 'include' })
  if (res.status === 204) return undefined as T
  const data = await parseBody(res)
  if (!res.ok) {
    throw new ApiError(res.status, (data as ErrorBody) ?? { error: res.statusText })
  }
  return data as T
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
  del: <T>(path: string, body?: unknown) => request<T>('DELETE', path, body),

  // putBlob uploads raw bytes to POST /api/blobs: the server stores the
  // request body verbatim and hashes it (internal/api/blob.go's
  // postBlob) — there is no JSON envelope or multipart form here.
  async putBlob(file: Blob): Promise<{ sha256: string; size: number }> {
    const res = await fetch('/api/blobs', {
      method: 'POST',
      headers: authHeaders(),
      body: file,
      credentials: 'include',
    })
    const data = await parseBody(res)
    if (!res.ok) {
      throw new ApiError(res.status, (data as ErrorBody) ?? { error: res.statusText })
    }
    return data as { sha256: string; size: number }
  },
}
```

`web/src/lib/diff.ts` — a small line diff for the task-version diff in `AnswerView.vue`. No diff library is added (Global Constraints); this is plain application code, in the spirit of the hand-rolled SDKs phase 3e wrote for the same reason:

```ts
// lib/diff.ts — a small line-based diff. Good enough to show what changed
// between two task-version bodies; not meant to compete with a real diff
// algorithm, and no diff library is added for it (Global Constraints).
export interface DiffLine {
  kind: 'same' | 'added' | 'removed'
  text: string
}

export function diffLines(a: string, b: string): DiffLine[] {
  const linesA = a.split('\n')
  const linesB = b.split('\n')
  const n = linesA.length
  const m = linesB.length
  // lcs[i][j] = length of the longest common subsequence of linesA[i:] and linesB[j:]
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0))
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] = linesA[i] === linesB[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1])
    }
  }
  const out: DiffLine[] = []
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (linesA[i] === linesB[j]) {
      out.push({ kind: 'same', text: linesA[i] })
      i++
      j++
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      out.push({ kind: 'removed', text: linesA[i] })
      i++
    } else {
      out.push({ kind: 'added', text: linesB[j] })
      j++
    }
  }
  while (i < n) {
    out.push({ kind: 'removed', text: linesA[i] })
    i++
  }
  while (j < m) {
    out.push({ kind: 'added', text: linesB[j] })
    j++
  }
  return out
}
```

`web/src/lib/markdown.ts` — a small, safe subset-of-Markdown renderer. No Markdown-rendering library is added (`goldmark`, added in plan 4b, is confined to the Go book HTML export):

```ts
// lib/markdown.ts renders a small, safe subset of Markdown for previewing
// a single answer or task body in the browser. Untrusted content is
// HTML-escaped before any tag is introduced, so a task or answer body
// cannot inject markup. This is a readability aid, not a preview of the
// assembled book (that comes from the server's own HTML export, plan 4b).
function escapeHTML(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

function inline(s: string): string {
  let out = escapeHTML(s)
  out = out.replace(/`([^`]+)`/g, '<code>$1</code>')
  out = out.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
  out = out.replace(/\*([^*]+)\*/g, '<em>$1</em>')
  out = out.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>')
  return out
}

// renderMarkdown understands fenced code blocks, ATX headings (# through
// ######), unordered (-/*) and ordered (1.) lists, blank-line-separated
// paragraphs, and the inline forms above. Anything else renders as a
// plain paragraph with its Markdown syntax still visible, which is honest
// about what this renderer does not understand.
export function renderMarkdown(src: string): string {
  const lines = src.replace(/\r\n/g, '\n').split('\n')
  const html: string[] = []
  let inCode = false
  let codeLines: string[] = []
  let paragraph: string[] = []
  let listItems: string[] = []
  let listTag: 'ul' | 'ol' | null = null

  const flushParagraph = () => {
    if (paragraph.length) {
      html.push('<p>' + paragraph.map(inline).join('<br>') + '</p>')
      paragraph = []
    }
  }
  const flushList = () => {
    if (listTag) {
      html.push(`<${listTag}>` + listItems.map((it) => `<li>${inline(it)}</li>`).join('') + `</${listTag}>`)
      listItems = []
      listTag = null
    }
  }

  for (const line of lines) {
    if (line.trim().startsWith('```')) {
      if (inCode) {
        html.push('<pre><code>' + escapeHTML(codeLines.join('\n')) + '</code></pre>')
        codeLines = []
        inCode = false
      } else {
        flushParagraph()
        flushList()
        inCode = true
      }
      continue
    }
    if (inCode) {
      codeLines.push(line)
      continue
    }
    const heading = /^(#{1,6})\s+(.*)$/.exec(line)
    if (heading) {
      flushParagraph()
      flushList()
      const level = heading[1].length
      html.push(`<h${level}>${inline(heading[2])}</h${level}>`)
      continue
    }
    const unordered = /^[-*]\s+(.*)$/.exec(line)
    const ordered = /^\d+\.\s+(.*)$/.exec(line)
    if (unordered || ordered) {
      flushParagraph()
      const tag = unordered ? 'ul' : 'ol'
      if (listTag && listTag !== tag) flushList()
      listTag = tag
      listItems.push((unordered ?? ordered)![1])
      continue
    }
    if (line.trim() === '') {
      flushParagraph()
      flushList()
      continue
    }
    flushList()
    paragraph.push(line)
  }
  flushParagraph()
  flushList()
  if (inCode) {
    html.push('<pre><code>' + escapeHTML(codeLines.join('\n')) + '</code></pre>')
  }
  return html.join('\n')
}
```

**Test:**

```
cd web && npm run type-check
```

Expected: no errors.

**Commit:**

```bash
git add web/src/api web/src/lib
git commit -m "Add the typed fetch client and the hand-rolled diff/Markdown helpers

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 6 — Auth store and router

**Files:** create `web/src/stores/auth.ts`, `web/src/router/index.ts`; modify `web/src/main.ts`.

`web/src/stores/auth.ts`:

```ts
import { defineStore } from 'pinia'
import { api, ApiError } from '../api/client'
import { setToken, clearToken } from '../api/token'
import type { Principal, AuthConfig } from '../types/auth'

export type AuthMode = 'loading' | 'authenticated' | 'unauthenticated' | 'token-only'

let initPromise: Promise<void> | null = null

export const useAuthStore = defineStore('auth', {
  state: () => ({
    principal: null as Principal | null,
    mode: 'loading' as AuthMode,
  }),
  actions: {
    // init loads GET /auth/config first (always 200, unauthenticated) to
    // learn whether OIDC is configured, then GET /auth/me for the caller's
    // identity. /auth/me always answers 401 when there is no caller,
    // whether or not OIDC is on — it never signals token-only mode by
    // status code (architecture note ruling 18, added during cross-plan
    // review: 4a's own endpoint table only ever documented 401 here, which
    // disagreed with an earlier draft of this plan that read a 404 as the
    // token-only signal; /auth/config is the fix, not a change to this
    // endpoint's semantics).
    async init(): Promise<void> {
      let oidc = true
      try {
        oidc = (await api.get<AuthConfig>('/auth/config')).oidc
      } catch {
        // /auth/config itself failing is unexpected; default to assuming
        // OIDC is configured, which sends an unauthenticated user to the
        // real /auth/login redirect (the safer of the two failure modes).
      }
      try {
        this.principal = await api.get<Principal>('/auth/me')
        this.mode = 'authenticated'
      } catch (err) {
        this.principal = null
        if (!(err instanceof ApiError)) throw err
        this.mode = oidc ? 'unauthenticated' : 'token-only'
      }
    },

    // ensureInit runs init() exactly once per page load, however many
    // callers (App.vue's onMounted, the router guard) ask for it.
    ensureInit(): Promise<void> {
      if (!initPromise) initPromise = this.init()
      return initPromise
    },

    // login starts a real OIDC redirect: a full page navigation, not a
    // client-side route (GET /auth/login 302s to the IdP).
    login(): void {
      window.location.href = '/auth/login'
    },

    // loginWithToken is the token-only-mode fallback: it stores the
    // pasted personal access token and re-runs init() so it is sent as
    // Authorization: Bearer on the next /auth/me.
    async loginWithToken(token: string): Promise<void> {
      setToken(token)
      await this.init()
      if (this.mode !== 'authenticated') {
        clearToken()
        throw new Error('that token was not accepted')
      }
    },

    async logout(): Promise<void> {
      try {
        await api.post('/auth/logout')
      } finally {
        clearToken()
        this.principal = null
        this.mode = 'unauthenticated'
        window.location.href = '/login'
      }
    },

    // has mirrors internal/auth.Roles.Has (admin implies every role
    // everywhere; engineer is scoped to workspaceId or "*") for UI
    // gating only — the server remains the actual authority on every
    // request regardless of what this returns.
    has(role: 'admin' | 'task-author' | 'processor-author' | 'engineer', workspaceId?: string): boolean {
      const roles = this.principal?.roles
      if (!roles) return false
      if (roles.admin) return true
      switch (role) {
        case 'admin':
          return false
        case 'task-author':
          return roles.task_author
        case 'processor-author':
          return roles.processor_author
        case 'engineer':
          return !!workspaceId && (roles.engineer.includes('*') || roles.engineer.includes(workspaceId))
      }
    },
  },
})
```

`web/src/router/index.ts`:

```ts
import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('../views/LoginView.vue'), meta: { public: true } },
    // "/" and "/w/:id" share DashboardView.vue: without an id it renders
    // a workspace picker, with one it renders that workspace's dashboard
    // (the architecture note's route table does not list a root route;
    // see this plan's "Decisions" §2).
    { path: '/', name: 'dashboard-root', component: () => import('../views/engineer/DashboardView.vue') },
    { path: '/w/:id', name: 'dashboard', component: () => import('../views/engineer/DashboardView.vue') },
    { path: '/w/:id/tasks/:taskId', name: 'answer', component: () => import('../views/engineer/AnswerView.vue') },
    { path: '/w/:id/proposals', name: 'proposals', component: () => import('../views/engineer/ProposalsView.vue') },
    { path: '/w/:id/book', name: 'book', component: () => import('../views/engineer/BookView.vue') },
  ],
})

// beforeEach ensures the auth store is initialised exactly once, then
// redirects: an unauthenticated principal with OIDC configured goes to
// the real backend login (a full page navigation, not a client route —
// architecture note, Plan 4c); in token-only mode (no /auth/login to
// redirect to — it 404s per Plan 4a) it goes to the SPA's own /login
// route instead, which shows the paste-a-token fallback.
router.beforeEach(async (to) => {
  const auth = useAuthStore()
  await auth.ensureInit()
  if (to.meta.public) return true
  if (auth.mode === 'authenticated') return true
  if (auth.mode === 'token-only') {
    return to.name === 'login' ? true : { name: 'login' }
  }
  window.location.href = '/auth/login'
  return false
})

export default router
```

`web/src/main.ts` (replaces Task 1's version):

```ts
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import './style.css'

createApp(App).use(createPinia()).use(router).mount('#app')
```

**Test:**

```
cd web && npm run type-check
```

Expected: no errors. `App.vue` still being Task 1's placeholder, `npm run dev` (against a running `custos serve --oidc-issuer-url= ... ` i.e. token-only mode, or a real OIDC backend) should redirect `/` to either `/auth/login` (hard nav) or render the still-placeholder page once authenticated — full behaviour is only visible once Task 7-9 land; this task's own check is the type-check plus a glance at the browser console for a thrown error.

**Commit:**

```bash
git add web/src/stores web/src/router web/src/main.ts
git commit -m "Add the auth store and router, with the token-only-mode redirect guard

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 7 — App shell and shared components

**Files:** create `web/src/components/RoleGate.vue`, `ProblemList.vue`, `MarkdownView.vue`; modify `web/src/App.vue`.

`web/src/components/RoleGate.vue` — mirrors `auth.Require`'s role/workspace-id signature for UI gating:

```vue
<script setup lang="ts">
import { computed } from 'vue'
import { useAuthStore } from '../stores/auth'

const props = defineProps<{
  role: 'admin' | 'task-author' | 'processor-author' | 'engineer'
  workspaceId?: string
}>()

const auth = useAuthStore()
const allowed = computed(() => auth.has(props.role, props.workspaceId))
</script>

<template>
  <slot v-if="allowed" />
</template>
```

`web/src/components/ProblemList.vue` — renders `[]{path,rule,message}` the same way everywhere:

```vue
<script setup lang="ts">
import type { Problem } from '../types/error'

defineProps<{ problems: Problem[] }>()
</script>

<template>
  <ul v-if="problems.length" class="list-disc space-y-1 pl-5 text-sm text-red-700">
    <li v-for="(p, i) in problems" :key="i">
      <span class="font-mono text-xs text-red-500">{{ p.path }}</span>
      <span class="text-red-400">({{ p.rule }})</span>: {{ p.message }}
    </li>
  </ul>
</template>
```

`web/src/components/MarkdownView.vue`:

```vue
<script setup lang="ts">
import { computed } from 'vue'
import { renderMarkdown } from '../lib/markdown'

const props = defineProps<{ source: string }>()
const html = computed(() => renderMarkdown(props.source))
</script>

<template>
  <!-- eslint-disable-next-line vue/no-v-html -->
  <div class="markdown-view" v-html="html" />
</template>

<style scoped>
.markdown-view :deep(h1),
.markdown-view :deep(h2),
.markdown-view :deep(h3) {
  font-weight: 600;
  margin: 0.75em 0 0.25em;
}
.markdown-view :deep(p) {
  margin: 0.5em 0;
}
.markdown-view :deep(ul),
.markdown-view :deep(ol) {
  margin: 0.5em 0 0.5em 1.5em;
}
.markdown-view :deep(code) {
  background: #f3f4f6;
  padding: 0 0.25em;
  border-radius: 0.25em;
  font-size: 0.9em;
}
.markdown-view :deep(pre) {
  background: #f3f4f6;
  padding: 0.75em;
  border-radius: 0.375em;
  overflow-x: auto;
}
</style>
```

`web/src/App.vue` (replaces Task 1's placeholder — note the marked extension point for plan 4d's nav sections):

```vue
<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from './stores/auth'
import RoleGate from './components/RoleGate.vue'

const auth = useAuthStore()
const route = useRoute()

onMounted(() => {
  auth.ensureInit()
})

const workspaceId = computed(() => (typeof route.params.id === 'string' ? route.params.id : undefined))
</script>

<template>
  <div class="min-h-screen bg-gray-50 text-gray-900">
    <header class="border-b bg-white">
      <nav class="mx-auto flex max-w-5xl items-center gap-4 px-4 py-3">
        <router-link to="/" class="font-semibold">custos</router-link>
        <RoleGate role="engineer" :workspace-id="workspaceId">
          <router-link :to="`/w/${workspaceId}`" class="text-sm text-gray-600 hover:text-gray-900">
            Dashboard
          </router-link>
          <router-link :to="`/w/${workspaceId}/proposals`" class="text-sm text-gray-600 hover:text-gray-900">
            Proposals
          </router-link>
          <router-link :to="`/w/${workspaceId}/book`" class="text-sm text-gray-600 hover:text-gray-900">
            Book
          </router-link>
        </RoleGate>
        <!--
          Extension point for plan 4d: RoleGate role="task-author" and
          role="processor-author" sections linking into /catalog/drafts
          and /processors go here.
        -->
        <div class="ml-auto flex items-center gap-3 text-sm">
          <template v-if="auth.principal">
            <span class="text-gray-500">{{ auth.principal.name }}</span>
            <button class="text-gray-600 hover:text-gray-900" @click="auth.logout()">Sign out</button>
          </template>
        </div>
      </nav>
    </header>
    <main class="mx-auto max-w-5xl px-4 py-6">
      <router-view />
    </main>
  </div>
</template>
```

**Test:**

```
cd web && npm run type-check
```

Expected: no errors.

**Manual verification** (needs `make build && ./custos serve --data-dir ./tmp/data` in token-only mode, i.e. no `--oidc-issuer-url`): load `http://127.0.0.1:8080/`; it redirects to `/login`, which (per Task 8, not yet written) 404s gracefully or shows nothing meaningful yet — record that the nav shell itself renders without a console error once a principal exists (fake one by running `custos token create` and pasting it once Task 8 lands; this task's own pass can stop at "no console error, nav bar renders").

**Commit:**

```bash
git add web/src/components web/src/App.vue
git commit -m "Add the app shell with a role-aware nav, RoleGate, ProblemList and MarkdownView

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 8 — `LoginView.vue`

**Files:** create `web/src/views/LoginView.vue`.

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { useAuthStore } from '../stores/auth'

const auth = useAuthStore()
const token = ref('')
const error = ref('')
const submitting = ref(false)

async function submitToken() {
  error.value = ''
  submitting.value = true
  try {
    await auth.loginWithToken(token.value.trim())
    window.location.href = '/'
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'sign-in failed'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="mx-auto mt-16 max-w-sm space-y-6 rounded-lg border bg-white p-6 shadow-sm">
    <h1 class="text-lg font-semibold">Sign in to custos</h1>

    <div v-if="auth.mode === 'token-only'" class="space-y-3">
      <p class="text-sm text-gray-600">
        This server has no identity provider configured (token-only mode). Paste a personal access token
        created with <code>custos token create</code>.
      </p>
      <input
        v-model="token"
        type="password"
        placeholder="personal access token"
        class="w-full rounded border px-3 py-2 text-sm"
        @keyup.enter="submitToken"
      />
      <button
        class="w-full rounded bg-gray-900 px-3 py-2 text-sm font-medium text-white disabled:opacity-50"
        :disabled="!token || submitting"
        @click="submitToken"
      >
        Sign in with token
      </button>
      <p v-if="error" class="text-sm text-red-600">{{ error }}</p>
    </div>

    <div v-else class="space-y-3">
      <p class="text-sm text-gray-600">Sign in with your organization's identity provider.</p>
      <button class="w-full rounded bg-gray-900 px-3 py-2 text-sm font-medium text-white" @click="auth.login()">
        Sign in
      </button>
    </div>
  </div>
</template>
```

**Test:**

```
cd web && npm run type-check
```

**Manual verification:**

1. Token-only mode: start `custos serve` with no `--oidc-issuer-url`. Run `custos token create --data-dir ... --email you@example.org --name "You" --admin` to get a token. Load `/`; it should land on `/login` showing the paste box (not the OIDC button). Paste the token, submit; expect a redirect to `/` with the nav bar now showing your name.
2. OIDC mode (if an IdP is available, e.g. the test provider plan 4a's own tests spin up): start `custos serve` with `--oidc-issuer-url` set. Load `/`; it should hard-navigate straight to `/auth/login` (never rendering the SPA's `/login` route) and come back signed in after the IdP round trip.
3. Record both passes (or note which was skipped and why, e.g. no IdP available) in the task's report.

**Commit:**

```bash
git add web/src/views/LoginView.vue
git commit -m "Add LoginView: the OIDC sign-in button and the token-only-mode fallback

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 9 — `DashboardView.vue`

**Files:** create `web/src/views/engineer/DashboardView.vue`.

```vue
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../../api/client'
import { useAuthStore } from '../../stores/auth'
import type { Workspace } from '../../types/workspace'
import type { Status } from '../../types/status'
import type { PinProposal, ProcessorProposal } from '../../types/proposals'
import type { RunRecord } from '../../types/runs'

const route = useRoute()
const auth = useAuthStore()

const workspaces = ref<Workspace[]>([])
const status = ref<Status | null>(null)
const pinProposals = ref<PinProposal[]>([])
const processorProposals = ref<ProcessorProposal[]>([])
const failedRuns = ref<RunRecord[]>([])
const loadError = ref('')

const workspaceId = computed(() => (typeof route.params.id === 'string' ? route.params.id : null))

const visibleWorkspaces = computed(() => workspaces.value.filter((w) => auth.has('engineer', w.id)))

async function loadWorkspaces() {
  workspaces.value = await api.get<Workspace[]>('/api/workspaces')
}

async function loadDashboard(id: string) {
  loadError.value = ''
  try {
    const [s, pins, procs, runs] = await Promise.all([
      api.get<Status>(`/api/workspaces/${id}/status`),
      api.get<PinProposal[]>(`/api/workspaces/${id}/proposals`),
      api.get<ProcessorProposal[]>(`/api/workspaces/${id}/processor-proposals`),
      api.get<RunRecord[]>(`/api/workspaces/${id}/runs`),
    ])
    status.value = s
    pinProposals.value = pins
    processorProposals.value = procs
    failedRuns.value = runs.filter((r) => r.state === 'failed')
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err)
  }
}

const unanswered = computed(() => status.value?.tasks.filter((t) => t.state === 'unanswered') ?? [])
const pendingUpdate = computed(() => status.value?.tasks.filter((t) => t.state === 'pending-update') ?? [])
const answered = computed(() => status.value?.tasks.filter((t) => t.state === 'answered') ?? [])
const openProposalCount = computed(() => pinProposals.value.length + processorProposals.value.length)

onMounted(() => {
  loadWorkspaces()
  if (workspaceId.value) loadDashboard(workspaceId.value)
})

watch(workspaceId, (id) => {
  if (id) loadDashboard(id)
})
</script>

<template>
  <div v-if="!workspaceId" class="space-y-4">
    <h1 class="text-xl font-semibold">Workspaces</h1>
    <ul class="divide-y rounded border bg-white">
      <li v-for="w in visibleWorkspaces" :key="w.id">
        <router-link :to="`/w/${w.id}`" class="flex items-center justify-between px-4 py-3 hover:bg-gray-50">
          <span class="font-mono text-sm">{{ w.id }}</span>
          <span v-if="w.frozen" class="rounded bg-blue-100 px-2 py-0.5 text-xs text-blue-800">frozen</span>
        </router-link>
      </li>
      <li v-if="!visibleWorkspaces.length" class="px-4 py-3 text-sm text-gray-500">
        No workspace is assigned to you yet.
      </li>
    </ul>
  </div>

  <div v-else class="space-y-6">
    <h1 class="font-mono text-xl font-semibold">{{ workspaceId }}</h1>
    <p v-if="loadError" class="text-sm text-red-600">{{ loadError }}</p>

    <div class="grid grid-cols-2 gap-4 sm:grid-cols-4">
      <div class="rounded border bg-white p-4">
        <div class="text-2xl font-semibold">{{ unanswered.length }}</div>
        <div class="text-sm text-gray-500">unanswered</div>
      </div>
      <div class="rounded border bg-white p-4">
        <div class="text-2xl font-semibold">{{ pendingUpdate.length }}</div>
        <div class="text-sm text-gray-500">pending update</div>
      </div>
      <div class="rounded border bg-white p-4">
        <div class="text-2xl font-semibold">{{ openProposalCount }}</div>
        <div class="text-sm text-gray-500">
          <router-link :to="`/w/${workspaceId}/proposals`" class="hover:underline">open proposals</router-link>
        </div>
      </div>
      <div class="rounded border bg-white p-4">
        <div class="text-2xl font-semibold">{{ failedRuns.length }}</div>
        <div class="text-sm text-gray-500">failed runs</div>
      </div>
    </div>

    <section>
      <h2 class="mb-2 font-medium text-gray-700">Unanswered ({{ unanswered.length }})</h2>
      <ul class="divide-y rounded border bg-white">
        <li v-for="t in unanswered" :key="t.id">
          <router-link :to="`/w/${workspaceId}/tasks/${t.id}`" class="flex items-center justify-between px-4 py-2 hover:bg-gray-50">
            <span>{{ t.title }}</span>
            <span class="text-xs text-gray-400">{{ t.generated ? 'generated' : 'catalog' }}</span>
          </router-link>
        </li>
        <li v-if="!unanswered.length" class="px-4 py-2 text-sm text-gray-400">none</li>
      </ul>
    </section>

    <section>
      <h2 class="mb-2 font-medium text-gray-700">Pending update ({{ pendingUpdate.length }})</h2>
      <ul class="divide-y rounded border bg-white">
        <li v-for="t in pendingUpdate" :key="t.id">
          <router-link :to="`/w/${workspaceId}/tasks/${t.id}`" class="flex items-center justify-between px-4 py-2 hover:bg-gray-50">
            <span>{{ t.title }}</span>
            <span class="text-xs text-gray-400">{{ t.generated ? 'generated' : 'catalog' }}</span>
          </router-link>
        </li>
        <li v-if="!pendingUpdate.length" class="px-4 py-2 text-sm text-gray-400">none</li>
      </ul>
    </section>

    <section>
      <h2 class="mb-2 font-medium text-gray-700">Answered ({{ answered.length }})</h2>
      <ul class="divide-y rounded border bg-white">
        <li v-for="t in answered" :key="t.id">
          <router-link :to="`/w/${workspaceId}/tasks/${t.id}`" class="flex items-center justify-between px-4 py-2 hover:bg-gray-50">
            <span>{{ t.title }}</span>
            <span class="text-xs text-gray-400">{{ t.generated ? 'generated' : 'catalog' }}</span>
          </router-link>
        </li>
        <li v-if="!answered.length" class="px-4 py-2 text-sm text-gray-400">none</li>
      </ul>
    </section>
  </div>
</template>
```

**Test:**

```
cd web && npm run type-check
```

**Manual verification:** sign in (Task 8), land on `/`; confirm the workspace picker lists only workspaces your token's `engineer` role covers. Create a workspace (`custos workspace create` or `POST /api/workspaces`), answer no tasks yet, open it; confirm the four tiles and the three sections render with correct counts, and clicking a task navigates to `/w/<id>/tasks/<taskId>` (Task 10, 404s gracefully until it lands — fine to note as expected at this point in the sequence).

**Commit:**

```bash
git add web/src/views/engineer/DashboardView.vue
git commit -m "Add DashboardView: workspace picker and per-workspace status

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 10 — `AnswerView.vue`

**Files:** create `web/src/views/engineer/AnswerView.vue`.

```vue
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, ApiError } from '../../api/client'
import ProblemList from '../../components/ProblemList.vue'
import MarkdownView from '../../components/MarkdownView.vue'
import { diffLines } from '../../lib/diff'
import type { Status, TaskStatus, Attachment } from '../../types/status'
import type { TaskHistory, TaskVersion } from '../../types/catalog'
import type { Problem } from '../../types/error'

const route = useRoute()
const router = useRouter()

const workspaceId = computed(() => route.params.id as string)
const taskId = computed(() => route.params.taskId as string)

const task = ref<TaskStatus | null>(null)
const history = ref<TaskHistory | null>(null)
// historyUnavailable is also true for a generated task: there is still no
// endpoint exposing a generated task's body (title/answer_type/choices are
// now carried by GET /api/workspaces/{id}/status itself — plan 4b, Task 8
// — which is enough to render and answer a choice-type generated task; the
// task's descriptive body text remains catalog-tasks-only).
const historyUnavailable = ref(false)

const value = ref('')
const body = ref('')
const attachments = ref<Attachment[]>([])
const uploading = ref(false)
const saving = ref(false)
const confirming = ref(false)
const commit = ref('')
const errorMessage = ref('')
const problems = ref<Problem[]>([])

async function load() {
  errorMessage.value = ''
  const status = await api.get<Status>(`/api/workspaces/${workspaceId.value}/status`)
  const found = status.tasks.find((t) => t.id === taskId.value)
  if (!found) {
    errorMessage.value = `workspace ${workspaceId.value} has no task ${taskId.value}`
    return
  }
  task.value = found
  value.value = found.answer?.value ?? ''
  body.value = found.answer?.body ?? ''
  attachments.value = found.answer?.attachments ?? []

  if (found.generated) {
    historyUnavailable.value = true
    return
  }
  try {
    history.value = await api.get<TaskHistory>(`/api/catalog/tasks/${taskId.value}`)
  } catch {
    historyUnavailable.value = true
  }
}

onMounted(load)

const currentVersion = computed<TaskVersion | undefined>(() =>
  history.value?.versions.find((v) => v.version === history.value!.current),
)
const answeredVersion = computed<TaskVersion | undefined>(() =>
  history.value?.versions.find((v) => v.version === task.value?.answer?.task_version),
)
// currentVersion.choices covers a catalog task; task.choices (status.ts,
// plan 4b Task 8) covers a generated task, which has no TaskHistory.
const choices = computed(() => currentVersion.value?.choices ?? task.value?.choices ?? [])

const showDiff = computed(
  () => !!currentVersion.value && !!answeredVersion.value && currentVersion.value.version !== answeredVersion.value.version,
)
const bodyDiff = computed(() => (showDiff.value ? diffLines(answeredVersion.value!.body, currentVersion.value!.body) : []))

async function onFileChosen(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  uploading.value = true
  errorMessage.value = ''
  try {
    const { sha256 } = await api.putBlob(file)
    attachments.value.push({ name: file.name, sha256, media_type: file.type || 'application/octet-stream', available: true })
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  } finally {
    uploading.value = false
    input.value = ''
  }
}

function removeAttachment(i: number) {
  attachments.value.splice(i, 1)
}

async function save() {
  if (!task.value) return
  saving.value = true
  errorMessage.value = ''
  problems.value = []
  try {
    const res = await api.put<{ commit: string }>(`/api/workspaces/${workspaceId.value}/answers/${taskId.value}`, {
      value: task.value.answer_type === 'markdown' ? '' : value.value,
      body: task.value.answer_type === 'markdown' ? body.value : '',
      attachments: attachments.value.map((a) => ({ name: a.name, sha256: a.sha256, media_type: a.media_type })),
    })
    commit.value = res.commit
    await load()
  } catch (err) {
    handleError(err)
  } finally {
    saving.value = false
  }
}

async function confirmStillValid() {
  confirming.value = true
  errorMessage.value = ''
  problems.value = []
  try {
    const res = await api.post<{ commit: string }>(`/api/workspaces/${workspaceId.value}/answers/${taskId.value}/still-valid`)
    commit.value = res.commit
    await load()
  } catch (err) {
    handleError(err)
  } finally {
    confirming.value = false
  }
}

function handleError(err: unknown) {
  if (err instanceof ApiError) {
    errorMessage.value = err.body.error
    problems.value = err.body.problems ?? []
  } else {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  }
}

function goBack() {
  router.push(`/w/${workspaceId.value}`)
}
</script>

<template>
  <div v-if="task" class="space-y-6">
    <div>
      <h1 class="text-xl font-semibold">{{ task.title }}</h1>
      <p class="text-sm text-gray-500">
        {{ task.generated ? 'generated task' : 'catalog task' }} · state: {{ task.state }} · answer type:
        {{ task.answer_type }}
      </p>
    </div>

    <section v-if="currentVersion" class="rounded border bg-white p-4">
      <h2 class="mb-2 font-medium text-gray-700">Task description (version {{ currentVersion.version }})</h2>
      <MarkdownView :source="currentVersion.body" />
    </section>
    <p v-else-if="historyUnavailable" class="text-sm text-gray-500">
      No task description is available for a generated task yet (see this plan's open question about a missing
      generated-task detail endpoint).
    </p>

    <section v-if="showDiff" class="rounded border bg-white p-4">
      <h2 class="mb-2 font-medium text-gray-700">What changed since you answered version {{ answeredVersion!.version }}</h2>
      <pre class="overflow-x-auto rounded bg-gray-50 p-3 text-xs"><span
        v-for="(l, i) in bodyDiff"
        :key="i"
        :class="{ 'bg-green-100': l.kind === 'added', 'bg-red-100 line-through': l.kind === 'removed' }"
      >{{ l.text }}<br /></span></pre>
      <button
        class="mt-3 rounded bg-gray-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
        :disabled="confirming"
        @click="confirmStillValid"
      >
        Still valid — keep this answer
      </button>
    </section>

    <section class="space-y-3 rounded border bg-white p-4">
      <h2 class="font-medium text-gray-700">Your answer</h2>

      <textarea
        v-if="task.answer_type === 'markdown'"
        v-model="body"
        rows="10"
        class="w-full rounded border px-3 py-2 font-mono text-sm"
        placeholder="Write the answer in Markdown"
      />
      <input
        v-else-if="task.answer_type === 'text'"
        v-model="value"
        type="text"
        class="w-full rounded border px-3 py-2 text-sm"
      />
      <div v-else-if="task.answer_type === 'timestamp'" class="flex gap-2">
        <input
          v-model="value"
          type="text"
          placeholder="2026-10-09T12:00:00Z"
          class="w-full rounded border px-3 py-2 font-mono text-sm"
        />
        <button type="button" class="rounded border px-3 py-2 text-sm" @click="value = new Date().toISOString()">
          now
        </button>
      </div>
      <input
        v-else-if="task.answer_type === 'path'"
        v-model="value"
        type="text"
        placeholder="/path/to/file"
        class="w-full rounded border px-3 py-2 text-sm"
      />
      <input
        v-else-if="task.answer_type === 'url'"
        v-model="value"
        type="url"
        class="w-full rounded border px-3 py-2 text-sm"
      />
      <template v-else-if="task.answer_type === 'choice'">
        <select v-model="value" class="w-full rounded border px-3 py-2 text-sm">
          <option value="" disabled>choose one</option>
          <option v-for="c in choices" :key="c" :value="c">{{ c }}</option>
        </select>
        <p v-if="!choices.length" class="text-sm text-amber-700">
          No choices are recorded for this task. Saving is disabled.
        </p>
      </template>

      <div>
        <h3 class="mb-1 text-sm font-medium text-gray-600">Attachments</h3>
        <ul class="mb-2 space-y-1">
          <li v-for="(a, i) in attachments" :key="a.sha256" class="flex items-center justify-between text-sm">
            <span>
              {{ a.name }}
              <span class="font-mono text-xs text-gray-400">{{ a.sha256.slice(0, 12) }}…</span>
              <span v-if="!a.available" class="ml-1 text-xs text-red-600">attachment unavailable</span>
            </span>
            <button class="text-xs text-gray-500 hover:text-red-600" @click="removeAttachment(i)">remove</button>
          </li>
        </ul>
        <input type="file" :disabled="uploading" @change="onFileChosen" />
      </div>

      <ProblemList :problems="problems" />
      <p v-if="errorMessage" class="text-sm text-red-600">{{ errorMessage }}</p>
      <p v-if="commit" class="text-sm text-green-700">Saved as commit {{ commit.slice(0, 12) }}…</p>

      <div>
        <button
          class="rounded bg-gray-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
          :disabled="saving || (task.answer_type === 'choice' && !choices.length)"
          @click="save"
        >
          Save answer
        </button>
        <button class="ml-2 text-sm text-gray-600 hover:text-gray-900" @click="goBack">Back to dashboard</button>
      </div>
    </section>
  </div>
  <p v-else-if="errorMessage" class="text-sm text-red-600">{{ errorMessage }}</p>
</template>
```

**Test:**

```
cd web && npm run type-check
```

**Manual verification:** for a catalog task of each `answer_type`, open its `AnswerView`, confirm the right form control appears, save an answer, confirm the dashboard now lists it under "Answered". Bump a task's version in the catalog (new minor version via `custos task new-version`), re-open its `AnswerView`; confirm the diff section appears and "Still valid" moves it back to answered without changing the content; then edit and save a genuinely new answer instead and confirm that works too. Attach a file, confirm it uploads and appears in the list with a truncated hash; remove it before saving and confirm it is not in the saved answer. For a `choice` task, confirm the dropdown lists the catalog's `choices`. For a generated task (produce one via a processor run in a test workspace, or skip and note it in the report if none is handy), confirm the "no task description available" / "no choices available" messages appear instead of a crash.

**Commit:**

```bash
git add web/src/views/engineer/AnswerView.vue
git commit -m "Add AnswerView: per-type answer form, attachments, version diff and still-valid

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 11 — `ProposalsView.vue`

**Files:** create `web/src/views/engineer/ProposalsView.vue`.

```vue
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api, ApiError } from '../../api/client'
import type { PinProposal, ProcessorProposal, ProposalItem } from '../../types/proposals'

const route = useRoute()
const workspaceId = computed(() => route.params.id as string)

const pinProposals = ref<PinProposal[]>([])
const processorProposals = ref<ProcessorProposal[]>([])
const errorMessage = ref('')
const selected = ref<Record<string, Set<string>>>({}) // branch -> set of "kind:match_key" selectors
const busy = ref<Record<string, boolean>>({})

// selector matches internal/proposal/diff.go's selector: "<kind>:<match_key>".
function selector(item: ProposalItem): string {
  return `${item.kind}:${item.match_key}`
}

function selectAll(p: ProcessorProposal) {
  selected.value[p.branch] = new Set(p.items.map(selector))
}

function toggle(p: ProcessorProposal, item: ProposalItem) {
  const set = selected.value[p.branch] ?? new Set<string>()
  const key = selector(item)
  if (set.has(key)) set.delete(key)
  else set.add(key)
  selected.value[p.branch] = set
}

function noneSelected(p: ProcessorProposal): boolean {
  return !selected.value[p.branch]?.size
}

async function load() {
  errorMessage.value = ''
  try {
    const [pins, procs] = await Promise.all([
      api.get<PinProposal[]>(`/api/workspaces/${workspaceId.value}/proposals`),
      api.get<ProcessorProposal[]>(`/api/workspaces/${workspaceId.value}/processor-proposals`),
    ])
    pinProposals.value = pins
    processorProposals.value = procs
    for (const p of procs) selectAll(p)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  }
}

onMounted(load)

function reportError(err: unknown) {
  errorMessage.value = err instanceof ApiError ? err.body.error : err instanceof Error ? err.message : String(err)
}

async function acceptPin(p: PinProposal) {
  busy.value[p.branch] = true
  try {
    await api.post(`/api/workspaces/${workspaceId.value}/proposals/accept`, { branch: p.branch })
    await load()
  } catch (err) {
    reportError(err)
  } finally {
    busy.value[p.branch] = false
  }
}

async function rejectPin(p: PinProposal) {
  busy.value[p.branch] = true
  try {
    await api.post(`/api/workspaces/${workspaceId.value}/proposals/reject`, { branch: p.branch })
    await load()
  } catch (err) {
    reportError(err)
  } finally {
    busy.value[p.branch] = false
  }
}

async function acceptProcessor(p: ProcessorProposal) {
  busy.value[p.branch] = true
  try {
    const items = Array.from(selected.value[p.branch] ?? [])
    await api.post(`/api/workspaces/${workspaceId.value}/processor-proposals/${p.task}/accept`, { items })
    await load()
  } catch (err) {
    reportError(err)
  } finally {
    busy.value[p.branch] = false
  }
}

async function rejectProcessor(p: ProcessorProposal) {
  busy.value[p.branch] = true
  try {
    await api.post(`/api/workspaces/${workspaceId.value}/processor-proposals/${p.task}/reject`)
    await load()
  } catch (err) {
    reportError(err)
  } finally {
    busy.value[p.branch] = false
  }
}
</script>

<template>
  <div class="space-y-8">
    <p v-if="errorMessage" class="text-sm text-red-600">{{ errorMessage }}</p>

    <section>
      <h1 class="mb-3 text-xl font-semibold">Catalog pin updates</h1>
      <div v-if="!pinProposals.length" class="text-sm text-gray-500">No open pin proposals.</div>
      <div v-for="p in pinProposals" :key="p.branch" class="mb-3 rounded border bg-white p-4">
        <p class="font-mono text-xs text-gray-500">{{ p.branch }}</p>
        <ul class="my-2 text-sm">
          <li>{{ p.changes.added.length }} added</li>
          <li>{{ p.changes.new_versions.length }} new versions</li>
          <li>{{ p.changes.superseded.length }} superseded</li>
          <li v-if="p.changes.groups_changed">groups changed</li>
          <li v-if="p.changes.bindings_changed">processor bindings changed</li>
        </ul>
        <button
          class="rounded bg-gray-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
          :disabled="busy[p.branch]"
          @click="acceptPin(p)"
        >
          Accept
        </button>
        <button class="ml-2 text-sm text-gray-600 hover:text-red-600" :disabled="busy[p.branch]" @click="rejectPin(p)">
          Reject
        </button>
      </div>
    </section>

    <section>
      <h1 class="mb-3 text-xl font-semibold">Processor output proposals</h1>
      <div v-if="!processorProposals.length" class="text-sm text-gray-500">No open processor proposals.</div>
      <div v-for="p in processorProposals" :key="p.branch" class="mb-3 rounded border bg-white p-4">
        <p class="font-mono text-xs text-gray-500">task {{ p.task }} · {{ p.cascade ? 'cascade removal' : 'output' }}</p>
        <ul class="my-2 divide-y text-sm">
          <li v-for="item in p.items" :key="selector(item)" class="flex items-center gap-2 py-1">
            <input type="checkbox" :checked="selected[p.branch]?.has(selector(item))" @change="toggle(p, item)" />
            <span class="w-20 shrink-0 text-xs uppercase text-gray-400">{{ item.action }}</span>
            <span class="w-16 shrink-0 text-xs text-gray-400">{{ item.kind }}</span>
            <span class="flex-1">{{ item.title }}</span>
            <span
              v-if="item.verification"
              class="text-xs"
              :class="{
                'text-green-700': item.verification.status === 'verified',
                'text-red-700': item.verification.status === 'failed',
                'text-gray-500': item.verification.status === 'unsigned',
              }"
            >
              {{ item.verification.status }}
            </span>
          </li>
        </ul>
        <p v-if="noneSelected(p)" class="mb-2 text-xs text-amber-700">
          Select at least one item, or reject the whole proposal.
        </p>
        <button
          class="rounded bg-gray-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
          :disabled="busy[p.branch] || noneSelected(p)"
          @click="acceptProcessor(p)"
        >
          Accept selected
        </button>
        <button
          class="ml-2 text-sm text-gray-600 hover:text-red-600"
          :disabled="busy[p.branch]"
          @click="rejectProcessor(p)"
        >
          Reject
        </button>
      </div>
    </section>
  </div>
</template>
```

**Test:**

```
cd web && npm run type-check
```

**Manual verification:** with a workspace that has both an open pin proposal (freeze it, push a catalog change, confirm it appears) and an open processor proposal (bind a processor to an answered task and save/retry an answer), open `/w/:id/proposals`; confirm both sections list correctly, accepting/rejecting a pin proposal removes it and moves the pin (or not, on reject), and accepting a subset of a processor proposal's items only applies those (verify via `GET /api/workspaces/:id/status` or the book afterwards) while deselecting everything disables "Accept selected" with the hint shown.

**Commit:**

```bash
git add web/src/views/engineer/ProposalsView.vue
git commit -m "Add ProposalsView: pin-update and processor-output proposals, per-item accept/reject

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Task 12 — `BookView.vue`

**Files:** create `web/src/views/engineer/BookView.vue`.

```vue
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../../api/client'
import type { Workspace } from '../../types/workspace'

const route = useRoute()
const workspaceId = computed(() => route.params.id as string)

const frozen = ref(false)
const warning = ref('')
const errorMessage = ref('')
const toggling = ref(false)

async function load() {
  try {
    const list = await api.get<Workspace[]>('/api/workspaces')
    const w = list.find((x) => x.id === workspaceId.value)
    frozen.value = w?.frozen ?? false
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  }
}

onMounted(load)

async function toggleFreeze() {
  toggling.value = true
  warning.value = ''
  errorMessage.value = ''
  try {
    const path = frozen.value ? 'unfreeze' : 'freeze'
    const res = await api.post<{ frozen: boolean; warning?: string }>(`/api/workspaces/${workspaceId.value}/${path}`)
    frozen.value = res.frozen
    if (res.warning) warning.value = res.warning
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  } finally {
    toggling.value = false
  }
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-xl font-semibold">Book</h1>
      <div class="flex items-center gap-3">
        <a :href="`/api/workspaces/${workspaceId}/book?format=markdown`" class="text-sm text-gray-600 hover:text-gray-900" download>
          Download Markdown
        </a>
        <button
          class="rounded border px-3 py-1.5 text-sm font-medium disabled:opacity-50"
          :disabled="toggling"
          @click="toggleFreeze"
        >
          {{ frozen ? 'Unfreeze' : 'Freeze' }}
        </button>
      </div>
    </div>
    <p v-if="warning" class="text-sm text-amber-700">{{ warning }}</p>
    <p v-if="errorMessage" class="text-sm text-red-600">{{ errorMessage }}</p>
    <iframe :src="`/api/workspaces/${workspaceId}/book?format=html`" class="h-[80vh] w-full rounded border bg-white" title="Workspace book" />
  </div>
</template>
```

**Test:**

```
cd web && npm run type-check
```

**Manual verification:** open `/w/:id/book` for a workspace with at least one answered task; confirm the iframe shows the rendered HTML book (this needs plan 4b's `?format=html` landed — until then it shows whatever the markdown-only endpoint currently returns through the iframe, which is acceptable as a placeholder for this task's own check), "Download Markdown" downloads the plain-text book, and the Freeze/Unfreeze button toggles and relabels itself, showing a warning banner if the server returns one (e.g. pin move failed per ruling 2.54).

**Commit:**

```bash
git add web/src/views/engineer/BookView.vue
git commit -m "Add BookView: HTML/Markdown export links and the freeze/unfreeze toggle

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

## Decisions beyond the architecture note

1. **npm package versions.** `vue@^3.5.13`, `vue-router@^4.5.0`, `pinia@^2.3.0`, `vite@^6.0.7`, `@vitejs/plugin-vue@^5.2.1`, `typescript@~5.7.3`, `vue-tsc@^2.2.0`, `tailwindcss@^3.4.17`, `postcss@^8.4.49`, `autoprefixer@^10.4.20`. These are mutually compatible, actively maintained lines as of this plan's writing. **Tailwind is pinned to v3, not v4**, specifically because the architecture note's own file tree names `tailwind.config.js` as a fixed file for plan 4c — that is the conventional v3 (and PostCSS-plugin) setup; Tailwind v4's idiomatic flow is CSS-first (`@import "tailwindcss"` plus `@theme`, no JS config file by default), which would make a committed `tailwind.config.js` either dead or require the unusual `@config` import directive. Pinning v3 keeps the note's named file meaningful without inventing a nonstandard v4 setup. The executing engineer should run `npm install` with network access to generate the real `package-lock.json`; the exact resolved versions it pins may be slightly newer patches, which is fine.
2. **Root route "/".** The architecture note's route table starts at `/login` and `/w/:id` and does not list a bare `/`; but `DashboardView.vue`'s own comment ("workspace picker + GET status") implies a picker must exist somewhere before a workspace is chosen. Rather than add an undocumented view file, `/` and `/w/:id` share `DashboardView.vue`: without an `:id` param it renders the picker, with one it renders that workspace's dashboard.
3. **`GET /auth/me` 404 vs. 401 — resolved by cross-plan review.** An
   earlier draft of this plan read a 404 from `/auth/me` as the
   token-only-mode signal, which plan 4a's endpoint table never actually
   sent (4a's `/auth/me` always 401s when there is no caller, OIDC
   configured or not). The fix is a new, unauthenticated `GET /auth/config`
   → `{"oidc": bool}` endpoint (architecture note ruling 18, added to plan
   4a), which `stores/auth.ts`'s `init()` (Task 6, above) now calls first
   to learn whether OIDC is configured, before ever looking at `/auth/me`'s
   status. `LoginView.vue`'s token-only-mode fallback (Task 8, below) is
   unchanged: it still renders whenever `auth.mode === 'token-only'`, which
   `init()` now sets correctly from `/auth/config` rather than from a 404
   that was never actually sent.
4. **Two small hand-rolled helpers beyond the fixed file list**: `src/lib/diff.ts` (line diff for the task-version diff) and `src/lib/markdown.ts` (a safe Markdown-subset renderer). No diff or Markdown-rendering npm package is added; this generalizes the spirit of ruling 4.16 (no casual new frontend dependency) beyond its literal drag-and-drop/graph wording.
5. **`ProposalsView.vue` renders two distinct kinds of proposal in one file.** `GET /api/workspaces/{id}/proposals` (pin proposals, `PinProposal`/`Diff`) and `GET /api/workspaces/{id}/processor-proposals` (`ProposalJSON`/`ItemJSON`) are genuinely different endpoints with different bodies and different accept/reject contracts (`{"branch"}` vs. `{"items":[...]}` with `"<kind>:<match_key>"` selectors, confirmed against `internal/proposal/diff.go`'s `selector` function). The architecture note names one view file for both; this plan keeps that one file but renders them as two independent sections rather than forcing a shared list component over incompatible shapes.
6. **`src/api/token.ts` as a standalone module.** `api/client.ts` needs to read the token-only-mode bearer token; `stores/auth.ts` needs to set/clear it. Making `client.ts` depend on the Pinia store (or vice versa) would create a circular import, so the token itself lives in a tiny non-reactive module both import.
7. **No endpoint exposes a generated task's title/body/choices — resolved by cross-plan review for `choices` specifically.** Only `GET /api/catalog/tasks/{id}` exists, and only for catalog tasks (`internal/api/catalog.go`); a generated task still has no detail endpoint for its descriptive body text. But `choices` was a real, blocking gap (an engineer could not answer a generated `choice`-type task at all), fixed by adding `Choices` to `status.TaskStatus`/`taskStatusJSON` directly (plan 4b, Task 8) — every task, generated or catalog, is already in that response. `AnswerView.vue`'s `choices` computed now falls back to `task.choices` from the status response when there is no `TaskHistory` (see Task 10, above); the save-disabled message is adjusted to a plain "no choices are recorded" rather than citing a missing endpoint that no longer applies. The task's descriptive *body* text for a generated task remains unavailable, which is a smaller, non-blocking gap (the form still renders and saves) left for a follow-up.
8. **No single-workspace `GET` endpoint exists** (`internal/api/workspaces.go` only has list and create). `BookView.vue` and `DashboardView.vue`'s picker both read a workspace's `frozen`/`pin` by filtering `GET /api/workspaces`'s list client-side, rather than assuming a detail endpoint.
9. **One `tsconfig.json`**, not the common Vite-template split into an app config and a `tsconfig.node.json` for `vite.config.ts`. At this project's scale (a "few hundred tasks" per spec §1, a small frontend to match) the extra file bought little; `vite.config.ts` is included directly in the one config's `include` list.
10. **`timestamp` answer type uses a plain text input plus a "now" button**, not `<input type="datetime-local">`. The native control's value format (`YYYY-MM-DDTHH:mm`, no timezone, no seconds) does not match the ISO-8601-with-`Z` style the rest of the system seems to expect, and converting between the two formats client-side risked subtly corrupting a saved timestamp. A plain text field keeps exactly what the engineer types.
11. **`RoleGate.vue` and `stores/auth.ts`'s `has()` mirror `internal/auth.Roles.Has`'s role/workspace-id semantics purely for UI gating** (hiding nav links and disabling buttons); the server remains the sole authority on every request (401/403), consistent with defense in depth.
12. **`App.vue`'s nav is Engineer-only in this plan**, with a marked HTML-comment extension point for plan 4d's Task-Author/Processor-Author `RoleGate` sections, since those routes and views do not exist yet when this plan runs.
13. **The `server.Server` diff in Task 3 is written against the pre-4a `server.go`** that is actually in this tree at the time this plan was written (plans 4a's own `auth`/`WithAuth`/`mw`/`WithMiddleware` additions are described in the architecture note but not yet code here); the diff adds only the `ui` field, `WithUI` method and one `mux.Handle("/", s.ui)` line, which composes with 4a's additions however they land, since `http.ServeMux` resolves by longest matching pattern rather than registration order.

## Rulings for spec §11.4

1. `internal/webui.Handler` 404s a request whose last path segment contains a "." when `dist` lacks that file, instead of falling back to `index.html`, so a stale or mistyped asset URL is visible as a 404 rather than a blank page trying to parse HTML as JavaScript or CSS.
2. The SPA's `/` and `/w/:id` routes share one component, `DashboardView.vue`: without an `:id` it renders a workspace picker, with one it renders that workspace's dashboard — no separate picker view file was introduced, since the architecture note's route table did not list a root route.
3. The frontend takes on two small hand-rolled helpers outside the architecture note's named file list — `src/lib/markdown.ts` (a safe Markdown subset renderer) and `src/lib/diff.ts` (a line diff) — instead of a Markdown or diff npm dependency, extending the no-casual-new-frontend-dependency posture of ruling 4.16 beyond its literal drag-and-drop/graph wording.
4. Token-only mode is signalled to the frontend by `GET /auth/config`'s `{"oidc": false}`, not by a 404 from `GET /auth/me` (which always 401s when there is no caller); resolved during cross-plan review (architecture note ruling 18).
5. `taskStatusJSON`/`status.TaskStatus` gain a `Choices` field (plan 4b, Task 8), so `AnswerView.vue` renders a generated `choice`-type task's options from `GET /api/workspaces/{id}/status` directly, without needing a new catalog-task-detail endpoint for generated tasks.
6. `ProposalsView.vue` renders both processor-output proposals (phase 3d) and catalog pin-update proposals (phase 2c) as two sections of one view and route, since they come from distinct endpoints with distinct accept/reject bodies, rather than one shared list component forced over two incompatible shapes.
7. A generated task's descriptive body text is still unavailable through the UI (only `GET /api/catalog/tasks/{id}`, catalog tasks only, exposes one) — a smaller, non-blocking gap than the `choices` one ruling 5 closes, since the form still renders and saves; left for a follow-up.
8. `web/package.json` takes `@types/node` as a devDependency, and `web/tsconfig.json`'s `"types"` array lists `"node"` alongside `"vite/client"`, because `vite.config.ts` calls `process.cwd()`; found missing while applying this plan to a scratch copy (`npm run type-check` failed with "Cannot find name 'process'" without it).
9. `web/tailwind.config.js` and `web/postcss.config.js` are written as ES modules (`export default`), not CommonJS (`module.exports`): `web/package.json` declares `"type": "module"`, so Node and Vite load a `.js` file here as an ES module, and the CommonJS form this plan originally wrote failed `vite build` outright ("module is not defined in ES module scope") — found while applying this plan to a scratch copy.
10. `internal/webui.Handler`'s SPA-shell fallback writes `dist/index.html`'s bytes directly (`http.ServeContent`) instead of routing a crafted `/index.html` request through `http.FileServer`, and its root-path check is `p == "" || p == "."`, not `p == "."` alone — both found while applying this plan to a scratch copy, where the original code 404d the literal root path and 301-redirected every other client-side route instead of serving the SPA shell (`http.FileServer` has its own built-in redirect for any request path ending in "/index.html"). This was a critical functional bug: as originally written, the embedded single-page application never actually loaded.
