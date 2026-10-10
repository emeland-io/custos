# Phase 4d (Task-Author and Processor-Author views) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A task-author can open a catalog draft, reorder the group tree by dragging, write a new version of a task (choosing patch/minor/major, reviewing a diff against the current version), see the task's version history as a list of generations, and publish the draft — resolving conflicts with the published catalog inline when `main` moved meanwhile. A processor-author can list the registry, browse run history and logs of one processor across all workspaces, and dry-run a candidate image, watching the report until it finishes.

**Architecture:** Pure frontend, inside the `web/` project plan 4c creates. Nine new Vue single-file components under `web/src/views/taskauthor/` and `web/src/views/processorauthor/`, two new hand-written type mirror files under `web/src/types/`, one endpoint module `web/src/api/authors.ts` that is the **only** place this plan touches plan 4c's `api/client.ts`, and one route module `web/src/router/authors.ts` that plan 4c's `router/index.ts` spreads into its route list. Every backend endpoint called here already exists (phase 2/3 code, or plans 4a/4b). **This plan adds no Go code and therefore no Go tests.**

**Tech Stack:** Vue 3 (`<script setup lang="ts">`, including one generic component), vue-router 4, Pinia, Tailwind CSS utility classes, TypeScript checked by `vue-tsc --noEmit` — all exactly as plan 4c's scaffold installs them. No new npm package.

**Spec:** [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md) — §3.1 (task versions, `previous`, groups, `groups/index.yaml`), §3.2 (registry), §5.1–5.4 and §5.7 (processors, matching, dry run), §6.1 (Task-Author and Processor-Author bullets), §6.4 (roles), §7, §8, and §11 (rulings override earlier sections). Shared names and formats: [2026-10-09-phase-4-architecture.md](2026-10-09-phase-4-architecture.md), sections "Plan 4b", "Plan 4c" and "Plan 4d"; processor/run/dry-run endpoint shapes: [2026-10-04-phase-3-architecture.md](2026-10-04-phase-3-architecture.md), section "Plan 3d" (authoritative), checked against `internal/runs/{record,dryrun,json,api}.go`.

**Requires:** plans 4a (auth, `GET /auth/me`, `custos token create`), 4b (`/api/catalog/drafts…`) and 4c (`web/` scaffold) executed first. Where the architecture note does not fix a name this plan needs from 4b or 4c, the plan states the assumption once, in the section "Assumed surface of plans 4b and 4c" below, and confines it to as few lines as possible so the cross-plan reviewer can adapt it in one place.

## Global Constraints

- **No new npm dependency of any kind** — not for drag-and-drop, not for graph drawing (ruling 4.16), not for diffing, not for anything else. `web/package.json` and `web/package-lock.json` are not modified by this plan. Drag-and-drop is the browser's native HTML5 DnD API; the version graph is an ordered list; the diff is a 40-line LCS function in this plan.
- **Every view fetches through the shared client.** All JSON requests go through plan 4c's actual `web/src/api/client.ts` export, the `api` object (`api.get/post/put/del`; an earlier draft of this plan assumed a standalone `request()` function, corrected during cross-plan review — see "Decisions"), wrapped by this plan's `web/src/api/authors.ts`. Views never call `fetch` themselves. The single exception is `getRunLog` in `authors.ts` (the log endpoint answers `text/plain`, which a JSON client cannot return); it uses the same same-origin, `credentials: 'include'` settings, plus the same `Authorization: Bearer` header `client.ts` attaches in token-only mode (also fixed during cross-plan review).
- **Types are hand-written mirrors** of the Go JSON shapes (ruling 4.4), added as new files beside plan 4c's in `web/src/types/` (`taskAuthor.ts`, `processorAuthor.ts`), each field commented with its Go source where the name is not obvious. No generated types, no parallel data-fetching layer.
- **`vue-tsc --noEmit` (`npm run type-check`) is the only automated frontend check** (ruling 4.4). Do not add a component test runner, Vitest, Playwright, or any test file. Each task's behaviour is verified by the manual click-through it lists, recorded in the task's report the same way phase 3 tasks recorded real-Docker runs.
- **No Go code, no Go tests.** If a task seems to need a backend change, stop and report it; do not add an endpoint.
- Layout fixed by the architecture note: views in `web/src/views/taskauthor/` and `web/src/views/processorauthor/`; shared widgets `RoleGate.vue`, `ProblemList.vue`, `MarkdownView.vue` from `web/src/components/` (plan 4c) are reused, not re-implemented.
- Routes exactly as the architecture note lists them: `/catalog/drafts`, `/catalog/drafts/:draftId`, `/catalog/drafts/:draftId/groups/:slug`, `/catalog/drafts/:draftId/tasks/:taskId`, `/processors`, `/processors/:name/runs`, `/processors/:name/dry-run`. No other route is added.
- Roles: the Task-Author section needs `task_author`, the Processor-Author section `processor_author`, as `GET /auth/me` names them; `admin` implies both (§6.4, architecture note `Roles.Has`).
- Imports are relative (`../../api/authors`), not an `@/` alias, so this plan does not depend on whether plan 4c configures one. Type-only imports use `import type` (safe under `verbatimModuleSyntax`).
- Commit messages: one imperative sentence without prefix, a blank line, then the `Co-Authored-By:` line the architecture note's Conventions section gives.
- Edits to files that already exist in this plan's own output are given as `replace … with …` blocks; each quoted block occurs exactly once in the file at that point of the plan. Edits to plan 4c's files (`router/index.ts`, `App.vue`) are given as insertions with a precise anchor, since their exact text is plan 4c's.

## Assumed surface of plans 4b and 4c

The architecture note fixes file locations but not every exported name. This plan assumes the following; each is used in exactly the places listed, so the cross-plan reviewer adapts it there and nowhere else.

| Assumption | Used only in |
| --- | --- |
| **Confirmed against 4c's actual code** (cross-plan review): `web/src/api/client.ts` exports `class ApiError extends Error { status: number; body: unknown }` and an `api` object with `get<T>(path)`, `post<T>(path, body?)`, `put<T>(path, body?)`, `del<T>(path, body?)` methods (JSON-encodes `body`, `credentials: 'include'`, resolves `undefined` for 204/empty bodies, rejects with `ApiError` for non-2xx) — **not** a standalone `request<T>(method, path, body?)` function as this row originally assumed. `web/src/api/authors.ts` (Task 1) has been rewritten to call `api.get/post/put/del` throughout. | `web/src/api/authors.ts` (top import + `errorMessage`/`problemsOf`/`conflictsOf`/`statusOf`) |
| **Confirmed against 4c's actual code:** the SPA authenticates `fetch` by the session cookie *and*, in token-only mode, an `Authorization: Bearer` header read from `api/token.ts`'s `getToken()` (`client.ts`'s `authHeaders()`) — not purely the cookie, as this row originally assumed. `getRunLog` has been fixed to attach the same header via the same `getToken()`, so a log request is not left unauthenticated under a pasted token. | `getRunLog` in `web/src/api/authors.ts` |
| `web/src/stores/auth.ts` exports `useAuthStore()`, whose `principal` is `null` or `{ name, email, roles: { admin: boolean, task_author: boolean, processor_author: boolean, engineer: string[] } }` (the `GET /auth/me` body), and plan 4c's global `beforeEach` has awaited the `/auth/me` load before any route's component is created. | `useAuthorRole` in `web/src/router/authors.ts` |
| **Confirmed against 4c's actual code** (cross-plan review): `web/src/router/index.ts` builds its router from a `routes` array literal, and its global guard requires auth by default, opting a route *out* via `meta: { public: true }` — not opting a route *in* via `meta: { requiresAuth: true }` as this row originally assumed. `authorRoutes`' routes need no `meta` at all (the default already covers them); the dead `requiresAuth` key has been removed from every route this plan adds. | `web/src/router/authors.ts`, one insertion in `router/index.ts` |
| **Corrected by cross-plan review:** `RoleGate.vue`'s prop `role` takes the *hyphenated* role strings mirroring `internal/auth.Require`'s role names — `'admin' \| 'task-author' \| 'processor-author' \| 'engineer'` — not the `/auth/me` JSON field names (`task_author`, `processor_author`) this row originally assumed. It renders its default slot only when `stores/auth.ts`'s `has(role, workspaceId)` (which does use the hyphenated names) returns true, and `has()` already treats `admin` as having every role. The two `App.vue` insertions use `role="task-author"` / `role="processor-author"`. | two insertions in `App.vue` |
| `ProblemList.vue` has a prop `problems: { path: string; rule: string; message: string }[]`. | `DraftsView.vue`, `GroupTreeView.vue`, `TaskEditorView.vue` |
| `MarkdownView.vue` has a prop `source: string` (the Markdown text to render). | `TaskEditorView.vue` (two uses) |
| 4b's `Draft` is encoded with lower-case snake_case JSON names: `{"id","branch","title","author":{"name","email"},"base","commit"}`. | `Draft` in `web/src/types/taskAuthor.ts` |
| 4b's `GET /api/catalog/drafts/{id}` answers `{"draft": <Draft>, "catalog": <same shape as GET /api/catalog: {"commit","groups","tasks"}>, "problems": [{"path","rule","message"}]}`. | `DraftDetail` in `web/src/types/taskAuthor.ts` |
| 4b's `POST …/publish` answers 200 `{"commit"}`, 409 with merge's `conflictBody` (`{"error","conflicts":[{"path","kind","ours","theirs"}]}`, `ours`/`theirs` base64 or null) when paths need a resolution, 409 `{"error"}` (no `conflicts`) when `main` moved during the compare-and-swap, 422 `{"error","problems"}` when `catalog.Check` rejects the merged tree, and 400 when a resolution names a path that has no conflict — the same contract as `merge.Register`'s merge endpoint. | `publish` in `DraftsView.vue` |
| 4b's PUT endpoints answer 2xx with any body (this plan ignores it and re-reads the draft). | `putTaskVersion`, `putGroup`, `putGroupsIndex` |

## Review Focus

1. **Two drafts change the same task; the first is published; the second is published.** Expected: the second publish shows the merge tool inline with the conflicting path(s), ours = published, theirs = draft; choosing a side (or editing) and pressing "Publish with these resolutions" publishes; a third concurrent publish between the two rounds makes the tool show only the new paths while the earlier choices are re-sent (manual step 3.6–3.9, Task 3).
2. **A drop lands on the wrong list.** Dragging a top-level group into the children list of the selected group (or a file from the desktop onto either list) does nothing: each `ReorderList` only accepts drags it started (Task 4, step 4.4).
3. **A dry run outlives the page.** Leaving the dry-run page stops polling (no requests in the Network tab afterwards); reloading the page with `?job=<id>` resumes polling the same job; restarting the server makes the page say the job was forgotten instead of polling forever (Task 9, steps 9.4–9.7).
4. **An admin who is not explicitly a task-author or processor-author** sees both nav sections and can use every view; an engineer-only user sees neither section and gets a "needs the … role" message on a typed-in URL, without any 401/403 request being made (Task 1, step 1.6).
5. **Version history across a draft.** A task with a version in the draft that is not yet on `main` shows the draft version as a later generation marked "not published"; after publishing, the same task shows it as "current (published)" (Task 6).

## File Structure

| File | Responsibility |
| --- | --- |
| `web/src/types/taskAuthor.ts` | Mirrors of catalog JSON (`internal/api/catalog.go`), problems, merge conflicts/resolutions (`internal/merge/http.go`), drafts and draft request bodies (plan 4b) |
| `web/src/types/processorAuthor.ts` | Mirrors of `runs.ProcessorInfo`, `runs.Record`, `runs.DryRunReport`/`Sample`, `runs.ItemJSON`, `api.workspaceJSON` |
| `web/src/api/authors.ts` | Endpoint functions for every call of this plan; base64 text helpers; `errorMessage`, `problemsOf`, `conflictsOf`, `statusOf` |
| `web/src/router/authors.ts` | `authorRoutes`, `useAuthorRole`, `routeParam` |
| `web/src/router/index.ts` (4c) | + spread `authorRoutes` |
| `web/src/App.vue` (4c) | + two role-gated nav links |
| `web/src/views/taskauthor/lineDiff.ts` | Line diff (LCS) |
| `web/src/views/taskauthor/LineDiff.vue` | Renders a line diff |
| `web/src/views/taskauthor/MergeToolView.vue` | Per-path ours/theirs/edit choice; emits resolutions |
| `web/src/views/taskauthor/DraftsView.vue` | Draft list, create, discard, draft detail (problems, tasks), publish, inline merge tool |
| `web/src/views/taskauthor/ReorderList.vue` | Generic HTML5 drag-to-reorder list with up/down buttons |
| `web/src/views/taskauthor/GroupTreeView.vue` | Top-level order, one group's title/children, new top-level group |
| `web/src/views/taskauthor/semver.ts` | `bumpVersion` (mirror of `semver.Bump`), `compareVersions` |
| `web/src/views/taskauthor/TaskEditorView.vue` | New-version wizard |
| `web/src/views/taskauthor/VersionGraphView.vue` | A task's versions as a list of generations |
| `web/src/views/processorauthor/RegistryView.vue` | Registered processors |
| `web/src/views/processorauthor/RunsView.vue` | Runs of one processor in every workspace, log, retry |
| `web/src/views/processorauthor/DryRunView.vue` | Start a dry run, poll its report |

## Manual-pass setup (used by every task)

Every task ends with a manual click-through against a real server. Do this setup once per session (and again after Task 9's restart step). Run from the repository root; it needs Docker, Node ≥ 20 and the binaries plans 4a–4c produce.

```sh
# 1. Fresh data directory and four users (plan 4a's offline CLI; each prints one token).
rm -rf ./tmp/data && mkdir -p ./tmp/data
go build -o custos ./cmd/custos
TA=$(./custos token create --data-dir ./tmp/data --email ta@example.org --name "Tara Author" --task-author)
PA=$(./custos token create --data-dir ./tmp/data --email pa@example.org --name "Paul Processor" --processor-author)
EN=$(./custos token create --data-dir ./tmp/data --email en@example.org --name "Erin Engineer" --engineer '*')
AD=$(./custos token create --data-dir ./tmp/data --email ad@example.org --name "Ada Admin" --admin)

# 2. Two local processor images, built the way internal/proctest builds them.
img() {
  d=$(mktemp -d)
  (cd internal/proctest/images/$1 && CGO_ENABLED=0 GOOS=linux GOARCH=$(go env GOARCH) \
     go build -trimpath -buildvcs=false -o "$d/processor" .)
  printf 'FROM scratch\nCOPY processor /processor\nENTRYPOINT ["/processor"]\n' > "$d/Dockerfile"
  echo "custos.manual/$1@$(docker build --quiet --tag custos.manual/$1:latest "$d")"
}
IMG_ECHO=$(img echo)
IMG_FAIL=$(img fail)

# 3. Server in token-only mode (no --oidc-issuer-url), in its own terminal:
#      ./custos serve --data-dir ./tmp/data
#    and the Vite dev server in a third terminal:
#      cd web && npm run dev          # http://127.0.0.1:5173, proxies /api, /auth, /git

# 4. Seed the catalog: three tasks, two top-level groups, two processors.
T1=11111111-1111-4111-8111-111111111111
T2=22222222-2222-4222-8222-222222222222
T3=33333333-3333-4333-8333-333333333333
C=$(mktemp -d) && git -C "$C" init -q -b main
mkdir -p "$C/tasks/$T1" "$C/tasks/$T2" "$C/tasks/$T3" "$C/groups/build" "$C/groups/release"
printf -- '---\nid: %s\nversion: 1.0.0\ntitle: Build the release binaries\nanswer_type: markdown\nprevious: []\n---\n\nDescribe how the release binaries are built.\n\nName the build host.\n' "$T1" > "$C/tasks/$T1/1.0.0.md"
printf -- '---\nid: %s\nversion: 1.0.0\ntitle: Name the release manager\nanswer_type: text\nprevious: []\n---\n\nWho signs off a release?\n' "$T2" > "$C/tasks/$T2/1.0.0.md"
printf -- '---\nid: %s\nversion: 1.0.0\ntitle: Pick the support tier\nanswer_type: choice\nchoices: [basic, extended]\nprevious: []\n---\n\nWhich support tier applies?\n' "$T3" > "$C/tasks/$T3/1.0.0.md"
printf 'groups: [build, release]\n' > "$C/groups/index.yaml"
printf 'title: Build\nchildren:\n  - task: %s\n' "$T1" > "$C/groups/build/group.yaml"
printf 'title: Release\nchildren:\n  - task: %s\n  - task: %s\n' "$T2" "$T3" > "$C/groups/release/group.yaml"
printf 'processors:\n  echo:\n    image: %s\n  fail:\n    image: %s\nbindings:\n  %s: echo\n  %s: fail\n' "$IMG_ECHO" "$IMG_FAIL" "$T1" "$T2" > "$C/processors.yaml"
git -C "$C" add -A && git -C "$C" -c user.name=Tara -c user.email=ta@example.org commit -qm "Seed the catalog"
git -C "$C" push -q "http://ta:$TA@127.0.0.1:8080/git/catalog.git" main

# 5. Two workspaces with answers to the bound tasks: T1 runs echo (succeeds,
#    proposes a document), T2 runs fail (fails with a log).
for n in 1 2; do
  WS=$(curl -s -X POST -H "Authorization: Bearer $AD" http://127.0.0.1:8080/api/workspaces | sed 's/.*"id":"\([^"]*\)".*/\1/')
  curl -s -X PUT -H "Authorization: Bearer $EN" -H 'Content-Type: application/json' \
    -d '{"body": "Built by the release pipeline."}' http://127.0.0.1:8080/api/workspaces/$WS/answers/$T1 >/dev/null
  curl -s -X PUT -H "Authorization: Bearer $EN" -H 'Content-Type: application/json' \
    -d '{"value": "Jane Doe"}' http://127.0.0.1:8080/api/workspaces/$WS/answers/$T2 >/dev/null
  echo "workspace $n: $WS"
done
echo "TA=$TA PA=$PA EN=$EN AD=$AD IMG_ECHO=$IMG_ECHO IMG_FAIL=$IMG_FAIL"
```

"Sign in as X" below means: open `http://127.0.0.1:5173/login`, sign out first if signed in, paste X's token into plan 4c's token box. Keep the browser's developer tools open on the Network and Console tabs throughout; **any Vue warning or uncaught error in the Console fails the step it appears in.**

---

## Task 1 — Shared plumbing: types, endpoint module, route module, nav

**Files:**
- Create: `web/src/types/taskAuthor.ts`, `web/src/types/processorAuthor.ts`, `web/src/api/authors.ts`, `web/src/router/authors.ts`
- Modify: `web/src/router/index.ts` (plan 4c), `web/src/App.vue` (plan 4c)

**Interfaces:**
- Consumes: `request`, `ApiError` (4c `api/client.ts`); `useAuthStore` (4c `stores/auth.ts`); `RoleGate.vue` (4c); the endpoint shapes listed in "Assumed surface" and in `internal/api/catalog.go`, `internal/merge/http.go`, `internal/runs/{record,dryrun,json}.go`, `internal/api/workspaces.go`.
- Produces: every type and endpoint function Tasks 2–9 import; `authorRoutes` (empty until Task 3); `useAuthorRole(role)`; `routeParam(v)`; nav links "Catalog drafts" → `/catalog/drafts`, "Processors" → `/processors`.

- [ ] **Step 1: Create `web/src/types/taskAuthor.ts`**

```ts
// Hand-written mirrors (ruling 4.4) of the catalog and catalog-draft JSON
// used by the Task-Author views (plan 4d). Sources of truth:
// internal/api/catalog.go (catalogJSON, groupJSON, childJSON,
// taskVersionJSON, taskHistoryJSON), internal/api/response.go (problemJSON),
// internal/merge/http.go (conflictJSON, conflictBody, resolutionJSON) and
// internal/catalogdraft (plan 4b: Draft and the draft endpoints' bodies).

/** task.Ref */
export interface TaskRef {
  id: string
  version: string
}

/** task.AnswerType */
export type AnswerType = 'markdown' | 'text' | 'timestamp' | 'path' | 'url' | 'choice'

/** The values of AnswerType, in the order of internal/task/task.go's answerTypes. */
export const ANSWER_TYPES: readonly AnswerType[] = ['markdown', 'text', 'timestamp', 'path', 'url', 'choice']

/** api.taskVersionJSON */
export interface CatalogTaskVersion {
  id: string
  version: string
  title: string
  answer_type: AnswerType
  choices?: string[]
  previous: TaskRef[]
  body: string
  processor?: string // bound processor name; only set in GET /api/catalog's task list
}

/** api.childJSON: exactly one of group or task is set. */
export interface CatalogChild {
  group?: CatalogGroup
  task?: string
}

/** api.groupJSON (sub-groups are nested, not referenced by slug) */
export interface CatalogGroup {
  slug: string
  title: string
  children: CatalogChild[]
}

/** api.catalogJSON: GET /api/catalog, and the "catalog" of a draft (plan 4b). */
export interface CatalogJSON {
  commit: string
  groups: CatalogGroup[] // top-level groups in groups/index.yaml order
  tasks: CatalogTaskVersion[] // the current version of every task that has one
}

/** api.taskHistoryJSON: GET /api/catalog/tasks/{id} */
export interface TaskHistory {
  id: string
  current: string // "" when superseded
  superseded: boolean
  processor?: string
  versions: CatalogTaskVersion[] // lowest first
}

/** api.problemJSON */
export interface CatalogProblem {
  path: string
  rule: string
  message: string
}

/** gitrepo.Signature as plan 4b encodes it. */
export interface Signature {
  name: string
  email: string
}

/** catalogdraft.Draft (plan 4b) */
export interface Draft {
  id: string
  branch: string
  title: string
  author: Signature
  base: string // catalog main's commit when the draft was created
  commit: string // current head of the draft branch
}

/** GET /api/catalog/drafts/{id} (plan 4b) */
export interface DraftDetail {
  draft: Draft
  catalog: CatalogJSON
  problems: CatalogProblem[]
}

/** semver.Step */
export type SemverStep = 'patch' | 'minor' | 'major'

/** Body of PUT /api/catalog/drafts/{id}/tasks/{task-id}; choices only for answer_type "choice". */
export interface NewVersionRequest {
  step: SemverStep
  title: string
  body: string
  answer_type: AnswerType
  choices?: string[]
}

/** One child in PUT …/groups/{slug}: exactly one of group (a slug) or task (a UUID). */
export interface GroupChildRef {
  group?: string
  task?: string
}

/** Body of PUT /api/catalog/drafts/{id}/groups/{slug} */
export interface GroupRequest {
  title: string
  children: GroupChildRef[]
}

/** Body of PUT /api/catalog/drafts/{id}/groups-index */
export interface GroupsIndexRequest {
  groups: string[]
}

/** merge.SideOurs/SideTheirs/SideContent */
export type ResolutionSide = 'ours' | 'theirs' | 'content'

/** merge.resolutionJSON: content is base64 and is sent only with side "content". */
export interface ResolutionJSON {
  side: ResolutionSide
  content?: string
}

/** merge.conflictJSON: ours = catalog main, theirs = the draft; base64, null when that side deleted the file. */
export interface ConflictJSON {
  path: string
  kind: string
  ours: string | null
  theirs: string | null
}

/** merge.conflictBody: the 409 body of a publish that needs resolutions. */
export interface ConflictBody {
  error: string
  conflicts: ConflictJSON[]
}

/** A ConflictJSON with ours/theirs decoded as UTF-8 text: MergeToolView's input. */
export interface DecodedConflict {
  path: string
  kind: string
  ours: string | null
  theirs: string | null
}

/** 200 body of POST /api/catalog/drafts/{id}/publish */
export interface PublishResponse {
  commit: string
}
```

- [ ] **Step 2: Create `web/src/types/processorAuthor.ts`**

```ts
// Hand-written mirrors (ruling 4.4) of the processor, run and dry-run JSON
// used by the Processor-Author views (plan 4d). Sources of truth:
// internal/runs/record.go (Record, State, Outcome), internal/runs/dryrun.go
// (ProcessorInfo, DryRunReport, Sample), internal/runs/json.go (ItemJSON),
// internal/workspace/document.go (Verification), internal/api/workspaces.go
// (workspaceJSON).
import type { TaskRef } from './taskAuthor'

/** runs.ProcessorInfo: GET /api/processors */
export interface ProcessorInfo {
  name: string
  image: string // digest-pinned reference, <name>@sha256:<hex>
  digest: string
  timeout: string // "60s" when processors.yaml leaves it out
  network: boolean
  secrets: string[]
  bound_tasks: string[] // catalog task UUIDs
}

/** runs.State */
export type RunState = 'queued' | 'running' | 'succeeded' | 'failed'

/** runs.Outcome; absent while queued/running and for failed runs */
export type RunOutcome = 'proposed' | 'unchanged'

/** runs.Record: GET /api/workspaces/{id}/runs (newest first) */
export interface RunRecord {
  id: string
  workspace: string
  task: TaskRef // answered task and the answer's task_version
  answer_path: string
  answer_blob: string
  answer_commit: string
  processor: string // processor name
  image: string
  digest: string
  key: string
  reason: string // "answer", "digest", "binding", "retry", "merge", "start-up"
  retry_of?: string
  state: RunState
  outcome?: RunOutcome
  branch?: string
  error?: string
  queued: string // RFC 3339
  started?: string
  finished?: string
}

/** api.workspaceJSON: GET /api/workspaces */
export interface WorkspaceSummary {
  id: string
  pin: string
  frozen: boolean
}

/** workspace.Verification */
export interface Verification {
  status: string
  signers?: string[]
}

/** match.Action */
export type ItemAction = 'added' | 'new-version' | 'unchanged' | 'removed'

/** runs.ItemJSON */
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

/** runs.Sample */
export interface DryRunSample {
  workspace: string
  task: string
  items: ProposalItem[]
}

/** The states a dry run reports (runs.Running, Succeeded, Failed). */
export type DryRunState = 'running' | 'succeeded' | 'failed'

/** runs.DryRunReport: GET /api/dry-runs/{id} */
export interface DryRunReport {
  state: DryRunState
  runs: number
  failed: number
  proposals: number
  workspaces: number
  samples: DryRunSample[] // at most 5
  error?: string // why the dry run itself failed
}

/** 202 body of POST /api/processors/{name}/dry-run */
export interface DryRunStarted {
  id: string
}
```

- [ ] **Step 3: Create `web/src/api/authors.ts`**

```ts
// Endpoint functions for the Task-Author and Processor-Author views
// (plan 4d). Every JSON call goes through the shared client's `api`
// object (plan 4c's actual export: api.get/post/put/del, not a standalone
// request() function as an earlier draft of this plan assumed — fixed
// during cross-plan review once 4c's api/client.ts was readable); this
// file is the only place plan 4d touches that client. getRunLog alone
// calls fetch itself, because its endpoint answers text/plain, with the
// client's same-origin, credentials: 'include' setup — and, since 4c's
// client also attaches an Authorization: Bearer header in token-only mode
// (see api/token.ts), getRunLog attaches the same header itself so the
// log request is not left unauthenticated in that mode (also found during
// cross-plan review; flagged by this plan's own "Decisions" as an open
// question, now resolved).
import { ApiError, api } from './client'
import { getToken } from './token'
import type {
  CatalogJSON,
  CatalogProblem,
  DecodedConflict,
  Draft,
  DraftDetail,
  GroupRequest,
  GroupsIndexRequest,
  NewVersionRequest,
  PublishResponse,
  ResolutionJSON,
  TaskHistory,
} from '../types/taskAuthor'
import type {
  DryRunReport,
  DryRunStarted,
  ProcessorInfo,
  RunRecord,
  WorkspaceSummary,
} from '../types/processorAuthor'

const seg = encodeURIComponent

/* ---------- catalog main (read-only; internal/api/catalog.go) ---------- */

export function getCatalog(): Promise<CatalogJSON> {
  return api.get<CatalogJSON>('/api/catalog')
}

export function getCatalogTask(id: string): Promise<TaskHistory> {
  return api.get<TaskHistory>(`/api/catalog/tasks/${seg(id)}`)
}

/* ---------- catalog drafts (plan 4b) ---------- */

export async function listDrafts(): Promise<Draft[]> {
  return (await api.get<Draft[] | null>('/api/catalog/drafts')) ?? []
}

export function createDraft(title: string): Promise<Draft> {
  return api.post<Draft>('/api/catalog/drafts', { title })
}

export async function getDraft(id: string): Promise<DraftDetail> {
  const d = await api.get<DraftDetail>(`/api/catalog/drafts/${seg(id)}`)
  // Go encodes empty slices as null unless the handler says otherwise.
  return {
    draft: d.draft,
    catalog: { commit: d.catalog.commit, groups: d.catalog.groups ?? [], tasks: d.catalog.tasks ?? [] },
    problems: d.problems ?? [],
  }
}

export async function deleteDraft(id: string): Promise<void> {
  await api.del<unknown>(`/api/catalog/drafts/${seg(id)}`)
}

export async function putTaskVersion(draftId: string, taskId: string, body: NewVersionRequest): Promise<void> {
  await api.put<unknown>(`/api/catalog/drafts/${seg(draftId)}/tasks/${seg(taskId)}`, body)
}

export async function putGroup(draftId: string, slug: string, body: GroupRequest): Promise<void> {
  await api.put<unknown>(`/api/catalog/drafts/${seg(draftId)}/groups/${seg(slug)}`, body)
}

export async function putGroupsIndex(draftId: string, groups: string[]): Promise<void> {
  const body: GroupsIndexRequest = { groups }
  await api.put<unknown>(`/api/catalog/drafts/${seg(draftId)}/groups-index`, body)
}

/** Publishes a draft; resolutions is empty for the first attempt. */
export function publishDraft(draftId: string, resolutions: Record<string, ResolutionJSON>): Promise<PublishResponse> {
  const body = Object.keys(resolutions).length > 0 ? { resolutions } : {}
  return api.post<PublishResponse>(`/api/catalog/drafts/${seg(draftId)}/publish`, body)
}

/* ---------- processors, runs, dry runs (internal/runs/api.go) ---------- */

export async function listProcessors(): Promise<ProcessorInfo[]> {
  return (await api.get<ProcessorInfo[] | null>('/api/processors')) ?? []
}

export async function listWorkspaces(): Promise<WorkspaceSummary[]> {
  return (await api.get<WorkspaceSummary[] | null>('/api/workspaces')) ?? []
}

export async function listRuns(workspace: string): Promise<RunRecord[]> {
  return (await api.get<RunRecord[] | null>(`/api/workspaces/${seg(workspace)}/runs`)) ?? []
}

export function retryRun(workspace: string, run: string): Promise<RunRecord> {
  return api.post<RunRecord>(`/api/workspaces/${seg(workspace)}/runs/${seg(run)}/retry`)
}

/**
 * The run's log (the processor's stderr), as text. Calls fetch directly
 * (the endpoint answers text/plain, which the JSON client cannot return),
 * but still attaches the same Authorization: Bearer header client.ts uses
 * in token-only mode (api/token.ts's getToken()), so the log request is
 * not left unauthenticated under a pasted token — found during cross-plan
 * review once 4c's client.ts (which does attach that header) was readable.
 */
export async function getRunLog(workspace: string, run: string): Promise<string> {
  const token = getToken()
  const headers: Record<string, string> = { Accept: 'text/plain' }
  if (token) headers.Authorization = `Bearer ${token}`
  const res = await fetch(`/api/workspaces/${seg(workspace)}/runs/${seg(run)}/log`, {
    credentials: 'include',
    headers,
  })
  const text = await res.text()
  if (!res.ok) {
    let msg = text
    try {
      const b: unknown = JSON.parse(text)
      if (isRecord(b) && typeof b.error === 'string') msg = b.error
    } catch {
      // not JSON: keep the text
    }
    throw new Error(`Loading the log failed (HTTP ${res.status}): ${msg}`)
  }
  return text
}

export function startDryRun(name: string, image: string): Promise<DryRunStarted> {
  return api.post<DryRunStarted>(`/api/processors/${seg(name)}/dry-run`, { image })
}

export function getDryRun(id: string): Promise<DryRunReport> {
  return api.get<DryRunReport>(`/api/dry-runs/${seg(id)}`)
}

/* ---------- base64 text (merge conflicts and resolutions carry []byte) ---------- */

export function decodeBase64Text(b64: string): string {
  const bin = atob(b64)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return new TextDecoder().decode(bytes)
}

export function encodeBase64Text(text: string): string {
  const bytes = new TextEncoder().encode(text)
  let bin = ''
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]!)
  return btoa(bin)
}

/* ---------- errors ---------- */

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/** The HTTP status of a failed request, or null when it never got one. */
export function statusOf(e: unknown): number | null {
  return e instanceof ApiError ? e.status : null
}

/** A sentence to show for a failed request. */
export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.status === 401) return 'You are not signed in, or your session has expired. Sign in again.'
    if (e.status === 403) return 'Your account does not have the role this action needs.'
    const body: unknown = e.body
    if (isRecord(body) && typeof body.error === 'string' && body.error !== '') return body.error
    if (typeof body === 'string' && body !== '') return `HTTP ${e.status}: ${body}`
    return `HTTP ${e.status}`
  }
  if (e instanceof Error) return e.message
  return String(e)
}

/** The validation problems of a 422 (or any error body that has them). */
export function problemsOf(e: unknown): CatalogProblem[] {
  if (!(e instanceof ApiError)) return []
  const body: unknown = e.body
  if (!isRecord(body) || !Array.isArray(body.problems)) return []
  const out: CatalogProblem[] = []
  for (const p of body.problems) {
    if (isRecord(p) && typeof p.path === 'string' && typeof p.rule === 'string' && typeof p.message === 'string') {
      out.push({ path: p.path, rule: p.rule, message: p.message })
    }
  }
  return out
}

/**
 * The decoded conflicts of a 409 conflictBody, or null when e is anything
 * else — including a 409 without conflicts (main moved during the
 * compare-and-swap), which the caller treats as a plain error.
 */
export function conflictsOf(e: unknown): DecodedConflict[] | null {
  if (!(e instanceof ApiError) || e.status !== 409) return null
  const body: unknown = e.body
  if (!isRecord(body) || !Array.isArray(body.conflicts)) return null
  const out: DecodedConflict[] = []
  for (const c of body.conflicts) {
    if (!isRecord(c) || typeof c.path !== 'string') continue
    out.push({
      path: c.path,
      kind: typeof c.kind === 'string' ? c.kind : 'other',
      ours: typeof c.ours === 'string' ? decodeBase64Text(c.ours) : null,
      theirs: typeof c.theirs === 'string' ? decodeBase64Text(c.theirs) : null,
    })
  }
  return out.length > 0 ? out : null
}
```

- [ ] **Step 4: Create `web/src/router/authors.ts`**

```ts
// Routes and role helper of the Task-Author and Processor-Author views
// (plan 4d). router/index.ts (plan 4c) spreads authorRoutes into its route
// list; App.vue links to /catalog/drafts and /processors.
import { computed, type ComputedRef } from 'vue'
import type { RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '../stores/auth'

export type AuthorRole = 'task_author' | 'processor_author'

/**
 * Whether the signed-in principal has role; admin implies every role
 * (§6.4). Views render a "needs the … role" message and make no request
 * when this is false, so a typed-in URL never produces a 403.
 */
export function useAuthorRole(role: AuthorRole): ComputedRef<boolean> {
  const auth = useAuthStore()
  return computed(() => {
    const p = auth.principal
    return p != null && (p.roles.admin || p.roles[role])
  })
}

/** A route param or query value as one string; '' when absent. */
export function routeParam(v: string | null | (string | null)[] | undefined): string {
  if (typeof v === 'string') return v
  if (Array.isArray(v)) return v[0] ?? ''
  return ''
}

export const authorRoutes: RouteRecordRaw[] = [
  // plan 4d: later tasks insert their routes above this line
]
```

- [ ] **Step 5: Spread `authorRoutes` into plan 4c's router**

In `web/src/router/index.ts`, add after its last `import` line:

```ts
import { authorRoutes } from './authors'
```

and add, as the **last element of the `routes` array literal, but before a catch-all route** (`/:pathMatch(.*)*`) if 4c has one:

```ts
  ...authorRoutes,
```

Nothing else in the file changes.

- [ ] **Step 6: Add the role-gated nav links to plan 4c's `App.vue`**

In `web/src/App.vue`, inside the top `<nav>` element, after the last engineer link (or the last `RouterLink` in the nav, if 4c groups them differently), insert:

```vue
      <RoleGate role="task-author">
        <RouterLink to="/catalog/drafts" class="rounded px-3 py-2 hover:bg-gray-100" active-class="font-semibold">
          Catalog drafts
        </RouterLink>
      </RoleGate>
      <RoleGate role="processor-author">
        <RouterLink to="/processors" class="rounded px-3 py-2 hover:bg-gray-100" active-class="font-semibold">
          Processors
        </RouterLink>
      </RoleGate>
```

If `App.vue`'s `<script setup>` does not already import `RoleGate`, add `import RoleGate from './components/RoleGate.vue'`; `RouterLink` is registered globally by vue-router.

- [ ] **Step 7: Type-check**

Run: `cd web && npm run type-check`
Expected: exits 0 with no output from `vue-tsc`.

- [ ] **Step 8: Manual pass** (setup above; the two links lead nowhere yet — vue-router logs "No match found" for them, which is expected **only in this task**)

1.1 Sign in as `$TA`. The nav shows "Catalog drafts" and not "Processors".
1.2 Sign in as `$PA`. The nav shows "Processors" and not "Catalog drafts".
1.3 Sign in as `$AD` (admin only, no explicit author role). The nav shows both.
1.4 Sign in as `$EN`. The nav shows neither.
1.5 Plan 4c's engineer links still work for `$EN` (open the dashboard of a workspace).
1.6 Record in the task report which of these held. (Step 1.6's "no 403 on a typed-in URL" check is repeated in Tasks 3 and 7 once routes exist.)

- [ ] **Step 9: Commit**

```bash
git add web/src/types/taskAuthor.ts web/src/types/processorAuthor.ts web/src/api/authors.ts web/src/router/authors.ts web/src/router/index.ts web/src/App.vue
git commit -m "Add the types, endpoints, routes module and nav links for the author views"
```

---

## Task 2 — Line diff and `MergeToolView`

**Files:**
- Create: `web/src/views/taskauthor/lineDiff.ts`, `web/src/views/taskauthor/LineDiff.vue`, `web/src/views/taskauthor/MergeToolView.vue`

**Interfaces:**
- Consumes: `DecodedConflict`, `ResolutionJSON` (Task 1); `encodeBase64Text` (Task 1).
- Produces:
  - `lineDiff(before: string, after: string): DiffLine[]` with `DiffLine = { op: 'same' | 'add' | 'del'; text: string }`.
  - `<LineDiff :before="string" :after="string" />`.
  - **The `MergeToolView` contract** (DraftsView, Task 3, is its only user):
    - props: `conflicts: DecodedConflict[]` — the paths still without a resolution, exactly as the last 409 returned them, decoded; `busy: boolean` — a publish with resolutions is in flight (all inputs disabled).
    - emits: `resolve(resolutions: Record<string, ResolutionJSON>)` — one entry per path in `conflicts`, never fewer, never others: `{side: 'ours'}`, `{side: 'theirs'}` or `{side: 'content', content: <base64 of the edited UTF-8 text>}`; emitted only when every path has a choice. `cancel()` — the user abandons publishing for now.
    - it keeps no state across a change of the `conflicts` prop: a new array (the next round's 409) resets every choice.
    - it never calls the API.

The merge tool is a component, not a routed view; the architecture note names it `MergeToolView.vue` and places it in `views/taskauthor/`, which this plan keeps.

- [ ] **Step 1: Create `web/src/views/taskauthor/lineDiff.ts`**

```ts
// A line diff by longest common subsequence: small, dependency-free
// (ruling 4.16), and plenty for task descriptions and catalog YAML files.

export type DiffOp = 'same' | 'add' | 'del'

export interface DiffLine {
  op: DiffOp
  text: string
}

/** Above this many table cells (lines before × lines after), show a full replace instead. */
const MAX_CELLS = 4_000_000

function lines(s: string): string[] {
  if (s === '') return []
  const ls = s.split('\n')
  if (ls[ls.length - 1] === '') ls.pop() // a final newline ends the last line; it does not start an empty one
  return ls
}

export function lineDiff(before: string, after: string): DiffLine[] {
  const a = lines(before)
  const b = lines(after)
  const n = a.length
  const m = b.length
  if (n * m > MAX_CELLS) {
    return [
      ...a.map((text): DiffLine => ({ op: 'del', text })),
      ...b.map((text): DiffLine => ({ op: 'add', text })),
    ]
  }
  // lcs[i][j] is the length of the longest common subsequence of a[i:] and b[j:].
  const lcs: Uint32Array[] = []
  for (let i = 0; i <= n; i++) lcs.push(new Uint32Array(m + 1))
  for (let i = n - 1; i >= 0; i--) {
    const row = lcs[i]!
    const next = lcs[i + 1]!
    for (let j = m - 1; j >= 0; j--) {
      row[j] = a[i] === b[j] ? next[j + 1]! + 1 : Math.max(next[j]!, row[j + 1]!)
    }
  }
  const out: DiffLine[] = []
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push({ op: 'same', text: a[i]! })
      i++
      j++
    } else if (lcs[i + 1]![j]! >= lcs[i]![j + 1]!) {
      out.push({ op: 'del', text: a[i]! })
      i++
    } else {
      out.push({ op: 'add', text: b[j]! })
      j++
    }
  }
  while (i < n) out.push({ op: 'del', text: a[i++]! })
  while (j < m) out.push({ op: 'add', text: b[j++]! })
  return out
}
```

- [ ] **Step 2: Create `web/src/views/taskauthor/LineDiff.vue`**

```vue
<script setup lang="ts">
import { computed } from 'vue'
import { lineDiff } from './lineDiff'

const props = defineProps<{ before: string; after: string }>()

const lines = computed(() => lineDiff(props.before, props.after))
const changed = computed(() => lines.value.some((l) => l.op !== 'same'))
</script>

<template>
  <div class="overflow-x-auto rounded border border-gray-200 text-sm">
    <p v-if="!changed" class="px-3 py-2 text-gray-500">No differences.</p>
    <div v-else class="py-1 font-mono leading-5">
      <div
        v-for="(l, i) in lines"
        :key="i"
        class="whitespace-pre-wrap px-3"
        :class="l.op === 'add' ? 'bg-green-50 text-green-900' : l.op === 'del' ? 'bg-red-50 text-red-900' : 'text-gray-700'"
      >{{ l.op === 'add' ? '+ ' : l.op === 'del' ? '- ' : '  ' }}{{ l.text }}</div>
    </div>
  </div>
</template>
```

- [ ] **Step 3: Create `web/src/views/taskauthor/MergeToolView.vue`**

```vue
<script setup lang="ts">
// The merge tool (§6.1): for each path the publish could not merge, the
// author keeps the published version (ours = catalog main), takes the
// draft's (theirs), or edits the file by hand (side "content"). It only
// collects choices; DraftsView sends them.
import { computed, ref, watch } from 'vue'
import LineDiff from './LineDiff.vue'
import { encodeBase64Text } from '../../api/authors'
import type { DecodedConflict, ResolutionJSON } from '../../types/taskAuthor'

type Choice = 'ours' | 'theirs' | 'edit'

const props = defineProps<{
  conflicts: DecodedConflict[]
  busy: boolean
}>()

const emit = defineEmits<{
  resolve: [resolutions: Record<string, ResolutionJSON>]
  cancel: []
}>()

const choices = ref<Record<string, Choice | undefined>>({})
const edits = ref<Record<string, string>>({})

// A new conflicts array is a new round: forget every earlier choice.
watch(
  () => props.conflicts,
  (cs) => {
    const c: Record<string, Choice | undefined> = {}
    const e: Record<string, string> = {}
    for (const x of cs) {
      c[x.path] = undefined
      e[x.path] = x.theirs ?? x.ours ?? '' // editing starts from the draft's text
    }
    choices.value = c
    edits.value = e
  },
  { immediate: true },
)

const unresolved = computed(() => props.conflicts.filter((c) => choices.value[c.path] === undefined).length)

function submit() {
  if (props.busy || unresolved.value > 0) return
  const out: Record<string, ResolutionJSON> = {}
  for (const c of props.conflicts) {
    const choice = choices.value[c.path]
    if (choice === 'ours') out[c.path] = { side: 'ours' }
    else if (choice === 'theirs') out[c.path] = { side: 'theirs' }
    else if (choice === 'edit') out[c.path] = { side: 'content', content: encodeBase64Text(edits.value[c.path] ?? '') }
  }
  emit('resolve', out)
}
</script>

<template>
  <section class="space-y-4 rounded border border-amber-300 bg-amber-50 p-4">
    <header class="space-y-1">
      <h3 class="text-lg font-semibold">Resolve conflicts with the published catalog</h3>
      <p class="text-sm text-gray-700">
        The published catalog changed since this draft was created, and {{ conflicts.length }}
        {{ conflicts.length === 1 ? 'file was' : 'files were' }} changed on both sides. Choose which version of each
        file to publish. The diff shows the published version (−) against this draft's (+).
      </p>
    </header>

    <article v-for="c in conflicts" :key="c.path" class="space-y-2 rounded border border-gray-200 bg-white p-3">
      <h4 class="font-mono text-sm">
        {{ c.path }} <span class="text-xs text-gray-500">({{ c.kind }})</span>
      </h4>
      <fieldset class="flex flex-wrap gap-4 text-sm" :disabled="busy">
        <legend class="sr-only">Resolution for {{ c.path }}</legend>
        <label class="flex items-center gap-1">
          <input v-model="choices[c.path]" type="radio" :name="'resolution-' + c.path" value="ours" />
          Keep the published version{{ c.ours === null ? ' (the file stays deleted)' : '' }}
        </label>
        <label class="flex items-center gap-1">
          <input v-model="choices[c.path]" type="radio" :name="'resolution-' + c.path" value="theirs" />
          Take this draft's version{{ c.theirs === null ? ' (deletes the file)' : '' }}
        </label>
        <label class="flex items-center gap-1">
          <input v-model="choices[c.path]" type="radio" :name="'resolution-' + c.path" value="edit" />
          Edit by hand
        </label>
      </fieldset>
      <LineDiff :before="c.ours ?? ''" :after="c.theirs ?? ''" />
      <textarea
        v-if="choices[c.path] === 'edit'"
        v-model="edits[c.path]"
        rows="12"
        class="w-full rounded border border-gray-300 p-2 font-mono text-sm"
        :disabled="busy"
      />
    </article>

    <footer class="flex flex-wrap items-center gap-3">
      <button
        type="button"
        class="rounded bg-blue-600 px-4 py-2 text-white disabled:opacity-50"
        :disabled="busy || unresolved > 0"
        @click="submit"
      >
        {{ busy ? 'Publishing…' : 'Publish with these resolutions' }}
      </button>
      <button type="button" class="rounded px-4 py-2 text-gray-700 hover:bg-gray-100" :disabled="busy" @click="emit('cancel')">
        Cancel
      </button>
      <span v-if="unresolved > 0" class="text-sm text-gray-600">
        {{ unresolved }} {{ unresolved === 1 ? 'file still needs' : 'files still need' }} a choice.
      </span>
    </footer>
  </section>
</template>
```

- [ ] **Step 4: Type-check**

Run: `cd web && npm run type-check`
Expected: exits 0.

- [ ] **Step 5: Manual pass**

None in this task: `MergeToolView` has no route and no caller yet. Its click-through is steps 3.6–3.9 of Task 3. Record "type-check only; exercised in Task 3" in the report.

- [ ] **Step 6: Commit**

```bash
git add web/src/views/taskauthor/lineDiff.ts web/src/views/taskauthor/LineDiff.vue web/src/views/taskauthor/MergeToolView.vue
git commit -m "Add the line diff and the merge tool for publishing catalog drafts"
```

---

## Task 3 — `DraftsView`: list, create, discard, detail, publish

**Files:**
- Create: `web/src/views/taskauthor/DraftsView.vue`
- Modify: `web/src/router/authors.ts`

**Interfaces:**
- Consumes: `listDrafts`, `createDraft`, `deleteDraft`, `getDraft`, `publishDraft`, `errorMessage`, `problemsOf`, `conflictsOf`, `statusOf` (Task 1); `MergeToolView` (Task 2); `ProblemList` (4c).
- Produces: routes `catalog-drafts` (`/catalog/drafts`) and `catalog-draft` (`/catalog/drafts/:draftId`), both rendering `DraftsView` (list on top, the selected draft below). Query `?published=<commit>` on `/catalog/drafts` shows a "published" notice. Links to `/catalog/drafts/:draftId/groups/:slug` (Task 4) and `/catalog/drafts/:draftId/tasks/:taskId` (Task 5) as path strings.

**Publish state machine** (all inside `DraftsView`):

| State | Entered when | Shows |
| --- | --- | --- |
| `idle` | draft loaded; Cancel in the merge tool | Publish button |
| `publishing` | Publish pressed, or the merge tool emitted `resolve` | button disabled ("Publishing…"); merge tool (if shown) `busy` |
| `conflicts` | 409 with `conflicts` | merge tool with those paths; `sentResolutions` := everything just sent |
| `failed` | any other error | message and `ProblemList` of 422 problems; `sentResolutions` cleared |
| — | 200 | navigates to `/catalog/drafts?published=<commit>` (4b deletes the draft branch) |

Every request sends `{...sentResolutions, ...newChoices}`: the server only reports paths that *still* lack a resolution, so earlier choices must be re-sent (merge.Merge's contract). A 400 after sending resolutions means a resolved path no longer conflicts (main moved again); the view clears them and says so.

- [ ] **Step 1: Create `web/src/views/taskauthor/DraftsView.vue`**

```vue
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import ProblemList from '../../components/ProblemList.vue'
import MergeToolView from './MergeToolView.vue'
import {
  conflictsOf,
  createDraft,
  deleteDraft,
  errorMessage,
  getDraft,
  listDrafts,
  problemsOf,
  publishDraft,
  statusOf,
} from '../../api/authors'
import { routeParam, useAuthorRole } from '../../router/authors'
import type { CatalogProblem, DecodedConflict, Draft, DraftDetail, ResolutionJSON } from '../../types/taskAuthor'

type PublishState = 'idle' | 'publishing' | 'conflicts' | 'failed'

const route = useRoute()
const router = useRouter()
const allowed = useAuthorRole('task_author')

const drafts = ref<Draft[]>([])
const listError = ref('')
const newTitle = ref('')
const creating = ref(false)

const draftId = computed(() => routeParam(route.params.draftId))
const publishedCommit = computed(() => routeParam(route.query.published))
const detail = ref<DraftDetail | null>(null)
const detailError = ref('')
const loadingDetail = ref(false)

const publishState = ref<PublishState>('idle')
const publishError = ref('')
const publishProblems = ref<CatalogProblem[]>([])
const conflicts = ref<DecodedConflict[]>([])
const sentResolutions = ref<Record<string, ResolutionJSON>>({})

const tasks = computed(() => [...(detail.value?.catalog.tasks ?? [])].sort((a, b) => a.title.localeCompare(b.title)))
// "-" is never a group slug (slugs start with [a-z0-9]); GroupTreeView reads it as "no group selected".
const firstGroupSlug = computed(() => detail.value?.catalog.groups[0]?.slug ?? '-')

function resetPublish() {
  publishState.value = 'idle'
  publishError.value = ''
  publishProblems.value = []
  conflicts.value = []
  sentResolutions.value = {}
}

async function loadList() {
  listError.value = ''
  try {
    drafts.value = await listDrafts()
  } catch (e) {
    listError.value = errorMessage(e)
  }
}

async function loadDetail() {
  resetPublish()
  detail.value = null
  detailError.value = ''
  if (draftId.value === '') return
  loadingDetail.value = true
  try {
    detail.value = await getDraft(draftId.value)
  } catch (e) {
    detailError.value =
      statusOf(e) === 404 ? 'This draft does not exist; it may have been published or discarded.' : errorMessage(e)
  } finally {
    loadingDetail.value = false
  }
}

async function create() {
  const title = newTitle.value.trim()
  if (title === '' || creating.value) return
  creating.value = true
  listError.value = ''
  try {
    const d = await createDraft(title)
    newTitle.value = ''
    await loadList()
    await router.push({ name: 'catalog-draft', params: { draftId: d.id } })
  } catch (e) {
    listError.value = errorMessage(e)
  } finally {
    creating.value = false
  }
}

async function discard(d: Draft) {
  if (!window.confirm(`Discard the draft "${d.title}"? Its unpublished changes are lost.`)) return
  listError.value = ''
  try {
    await deleteDraft(d.id)
  } catch (e) {
    listError.value = errorMessage(e)
    return
  }
  await loadList()
  if (draftId.value === d.id) await router.push({ name: 'catalog-drafts' })
}

/** Publishes the open draft, re-sending earlier resolutions together with extra. */
async function publish(extra: Record<string, ResolutionJSON> = {}) {
  const d = detail.value
  if (d === null || publishState.value === 'publishing') return
  const resolutions = { ...sentResolutions.value, ...extra }
  publishState.value = 'publishing'
  publishError.value = ''
  publishProblems.value = []
  try {
    const res = await publishDraft(d.draft.id, resolutions)
    resetPublish()
    await loadList()
    await router.push({ name: 'catalog-drafts', query: { published: res.commit } })
  } catch (e) {
    const cs = conflictsOf(e)
    if (cs !== null) {
      sentResolutions.value = resolutions
      conflicts.value = cs
      publishState.value = 'conflicts'
      return
    }
    const hadResolutions = Object.keys(resolutions).length > 0
    sentResolutions.value = {}
    conflicts.value = []
    publishState.value = 'failed'
    publishProblems.value = problemsOf(e)
    publishError.value = errorMessage(e)
    if (hadResolutions && statusOf(e) === 400) {
      publishError.value +=
        ' The published catalog changed again while you were resolving, so your choices were discarded. Press Publish to start over.'
    } else if (statusOf(e) === 409) {
      publishError.value += ' Press Publish to try again.'
    }
  }
}

watch(allowed, (ok) => {
  if (ok) void loadList()
}, { immediate: true })

watch([allowed, draftId], () => {
  if (allowed.value) void loadDetail()
}, { immediate: true })
</script>

<template>
  <section class="mx-auto max-w-5xl space-y-8 p-6">
    <p v-if="!allowed" class="text-gray-700">Catalog drafts need the task-author role.</p>
    <template v-else>
      <header class="space-y-1">
        <h1 class="text-2xl font-semibold">Catalog drafts</h1>
        <p class="text-sm text-gray-600">
          Changes to tasks and groups are made in a draft. Publishing validates the draft and merges it into the
          catalog's main.
        </p>
      </header>

      <p v-if="publishedCommit !== ''" class="rounded bg-green-50 p-3 text-green-900">
        Published as catalog commit <code>{{ publishedCommit.slice(0, 12) }}</code>.
      </p>

      <form class="flex gap-2" @submit.prevent="create">
        <input
          v-model="newTitle"
          type="text"
          placeholder="Title of a new draft"
          class="flex-1 rounded border border-gray-300 px-3 py-2"
          :disabled="creating"
        />
        <button
          type="submit"
          class="rounded bg-blue-600 px-4 py-2 text-white disabled:opacity-50"
          :disabled="creating || newTitle.trim() === ''"
        >
          Create draft
        </button>
      </form>
      <p v-if="listError" class="text-red-700">{{ listError }}</p>

      <table v-if="drafts.length > 0" class="w-full text-left text-sm">
        <thead>
          <tr class="border-b">
            <th class="py-2">Title</th>
            <th>Author</th>
            <th>Based on</th>
            <th>Head</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in drafts" :key="d.id" class="border-b" :class="{ 'bg-blue-50': d.id === draftId }">
            <td class="py-2">
              <RouterLink :to="`/catalog/drafts/${d.id}`" class="text-blue-700 hover:underline">{{ d.title }}</RouterLink>
            </td>
            <td>{{ d.author.name }}</td>
            <td><code>{{ d.base.slice(0, 8) }}</code></td>
            <td><code>{{ d.commit.slice(0, 8) }}</code></td>
            <td class="text-right">
              <button type="button" class="text-red-700 hover:underline" @click="discard(d)">Discard</button>
            </td>
          </tr>
        </tbody>
      </table>
      <p v-else-if="listError === ''" class="text-gray-600">No open drafts.</p>

      <section v-if="draftId !== ''" class="space-y-6 border-t pt-6">
        <p v-if="loadingDetail" class="text-gray-600">Loading the draft…</p>
        <p v-else-if="detailError" class="text-red-700">{{ detailError }}</p>
        <template v-else-if="detail">
          <header class="flex flex-wrap items-baseline justify-between gap-2">
            <h2 class="text-xl font-semibold">{{ detail.draft.title }}</h2>
            <span class="text-sm text-gray-600">
              branch <code>{{ detail.draft.branch }}</code> by {{ detail.draft.author.name }}
            </span>
          </header>

          <nav class="text-sm">
            <RouterLink
              :to="`/catalog/drafts/${detail.draft.id}/groups/${firstGroupSlug}`"
              class="text-blue-700 hover:underline"
            >
              Edit the group tree
            </RouterLink>
          </nav>

          <div>
            <h3 class="mb-2 font-semibold">Problems</h3>
            <ProblemList v-if="detail.problems.length > 0" :problems="detail.problems" />
            <p v-else class="text-sm text-green-800">None: this draft passes validation as it stands.</p>
          </div>

          <div>
            <h3 class="mb-2 font-semibold">Tasks</h3>
            <table class="w-full text-left text-sm">
              <thead>
                <tr class="border-b">
                  <th class="py-2">Title</th>
                  <th>Current version</th>
                  <th>Answer type</th>
                  <th>Processor</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="t in tasks" :key="t.id" class="border-b">
                  <td class="py-2">{{ t.title }}</td>
                  <td>{{ t.version }}</td>
                  <td>{{ t.answer_type }}</td>
                  <td>{{ t.processor ?? '' }}</td>
                  <td class="text-right">
                    <RouterLink
                      :to="`/catalog/drafts/${detail.draft.id}/tasks/${t.id}`"
                      class="text-blue-700 hover:underline"
                    >
                      New version…
                    </RouterLink>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <div class="space-y-3">
            <button
              type="button"
              class="rounded bg-green-700 px-4 py-2 text-white disabled:opacity-50"
              :disabled="publishState === 'publishing' || conflicts.length > 0"
              @click="publish()"
            >
              {{ publishState === 'publishing' && conflicts.length === 0 ? 'Publishing…' : 'Publish' }}
            </button>
            <p v-if="detail.problems.length > 0" class="text-sm text-amber-800">
              This draft has problems; publishing is rejected until they are fixed.
            </p>
            <p v-if="publishError" class="text-red-700">{{ publishError }}</p>
            <ProblemList v-if="publishProblems.length > 0" :problems="publishProblems" />
            <MergeToolView
              v-if="conflicts.length > 0"
              :conflicts="conflicts"
              :busy="publishState === 'publishing'"
              @resolve="publish"
              @cancel="resetPublish"
            />
          </div>
        </template>
      </section>
    </template>
  </section>
</template>
```

Note `@click="publish()"` with parentheses: without them Vue would pass the click event as `extra`.

- [ ] **Step 2: Register the two routes**

In `web/src/router/authors.ts`, replace

```ts
  // plan 4d: later tasks insert their routes above this line
```

with

```ts
  {
    path: '/catalog/drafts',
    name: 'catalog-drafts',
    component: () => import('../views/taskauthor/DraftsView.vue'),
  },
  {
    path: '/catalog/drafts/:draftId',
    name: 'catalog-draft',
    component: () => import('../views/taskauthor/DraftsView.vue'),
  },
  // plan 4d: later tasks insert their routes above this line
```

- [ ] **Step 3: Type-check**

Run: `cd web && npm run type-check`
Expected: exits 0.

- [ ] **Step 4: Manual pass** (setup above; links to groups and tasks lead nowhere until Tasks 4–5 — vue-router's "No match found" warning for those two links only is expected in this task)

3.1 Sign in as `$TA`, click "Catalog drafts". "No open drafts." is shown; Network shows `GET /api/catalog/drafts` 200.
3.2 Create a draft "Rename release tasks". The URL becomes `/catalog/drafts/<uuid>`, the draft is listed and highlighted, the detail shows branch `custos/draft/<uuid>`, author "Tara Author", "Problems: None", and the three seed tasks with version 1.0.0; T1's processor column says `echo`, T2's `fail`.
3.3 Create a second draft "Throwaway", then Discard it and confirm. It disappears; the URL goes back to `/catalog/drafts`.
3.4 Publish "Rename release tasks" unchanged. Expected: either 200 (navigates to `/catalog/drafts?published=<commit>` with the green notice, draft gone from the list) — record which `commit` 4b returned (main's own, since nothing changed) — or the error 4b gives for an empty draft; record it.
3.5 Prepare a conflict from the shell (4b endpoints, two drafts both bumping T2 to 1.1.0 with different bodies):

```sh
H=(-H "Authorization: Bearer $TA" -H 'Content-Type: application/json')
A=$(curl -s "${H[@]}" -X POST -d '{"title":"Draft A"}' http://127.0.0.1:8080/api/catalog/drafts | sed 's/.*"id":"\([^"]*\)".*/\1/')
B=$(curl -s "${H[@]}" -X POST -d '{"title":"Draft B"}' http://127.0.0.1:8080/api/catalog/drafts | sed 's/.*"id":"\([^"]*\)".*/\1/')
curl -s "${H[@]}" -X PUT -d '{"step":"minor","title":"Name the release manager","body":"Who signs off a release? (A)\n","answer_type":"text"}' http://127.0.0.1:8080/api/catalog/drafts/$A/tasks/$T2
curl -s "${H[@]}" -X PUT -d '{"step":"minor","title":"Name the release manager","body":"Who signs off a release? (B)\n","answer_type":"text"}' http://127.0.0.1:8080/api/catalog/drafts/$B/tasks/$T2
curl -s "${H[@]}" -X POST -d '{}' http://127.0.0.1:8080/api/catalog/drafts/$A/publish
```

3.6 Reload the UI, open "Draft B", press Publish. Expected: a 409 in Network; the merge tool appears inline with `tasks/2222…/1.1.0.md`, a diff with `- …(A)` and `+ …(B)`, three radio buttons, "1 file still needs a choice", and the Publish button above is disabled.
3.7 Choose "Edit by hand", change the textarea's body line to `… (A and B)`, press "Publish with these resolutions". Expected: one POST whose request body is `{"resolutions":{"tasks/…/1.1.0.md":{"side":"content","content":"<base64>"}}}`; 200; navigation to the published notice.
3.8 Repeat 3.5 with two fresh drafts C and D (bump T3 instead, `"answer_type":"choice","choices":["basic","extended"]`), publish C from the shell, open D, press Publish, choose "Take this draft's version", then **before** pressing "Publish with these resolutions" publish a third draft E from the shell that also bumps T3 (to make `main` move again, creating a new conflict). Press the button. Expected: either a fresh 409 listing only new paths (the merge tool resets; the next request body contains the earlier `theirs` choice *and* the new ones), or a 400 with the "changed again … choices were discarded" sentence; record which.
3.9 In the merge tool press Cancel. Expected: the tool disappears; Publish is enabled again; no request is sent.
3.10 Sign in as `$EN` and type `/catalog/drafts` into the address bar. Expected: "Catalog drafts need the task-author role." and **no** `/api/catalog/drafts` request in Network.
3.11 Sign in as `$AD`: the drafts list loads.

- [ ] **Step 5: Commit**

```bash
git add web/src/views/taskauthor/DraftsView.vue web/src/router/authors.ts
git commit -m "Add the catalog drafts view with publish and the inline merge tool"
```

---

## Task 4 — `ReorderList` and `GroupTreeView`

**Files:**
- Create: `web/src/views/taskauthor/ReorderList.vue`, `web/src/views/taskauthor/GroupTreeView.vue`
- Modify: `web/src/router/authors.ts`

**Interfaces:**
- Consumes: `getDraft`, `putGroup`, `putGroupsIndex`, `errorMessage`, `problemsOf`, `statusOf` (Task 1); `ProblemList` (4c).
- Produces: route `catalog-draft-group` (`/catalog/drafts/:draftId/groups/:slug`). `:slug` = `-` means "no group selected" (DraftsView links there when the catalog has no group). `ReorderList` contract:
  - generic over `T`; props `items: T[]`, `itemKey: (item: T) => string` (unique per item), `disabled?: boolean`;
  - emits `update:items(next: T[])` with a **new** array — it never mutates `items`;
  - slot `item` with `{ item: T, index: number }`.

**Drag-and-drop, precisely** (native HTML5 DnD on each `<li>`, no library):

| Event (on item `i`) | Handler |
| --- | --- |
| `dragstart` | `dragIndex := i`; `dataTransfer.effectAllowed = 'move'`; `dataTransfer.setData('text/plain', String(i))` (Firefox starts no drag without data) |
| `dragover` | if `dragIndex === null` → return (not this list's drag: a file, a link, or another `ReorderList`), so the browser shows "not allowed"; else `preventDefault()` (this makes the element a drop target), `dropEffect = 'move'`, `overIndex := i` |
| `dragleave` | if `overIndex === i` → `overIndex := null` |
| `drop` | if `dragIndex === null` → return; else `preventDefault()`; emit the array with the item at `dragIndex` removed and re-inserted at index `i` (so it ends up exactly where it was dropped; the items in between shift by one); `dragIndex := overIndex := null` |
| `dragend` (fires on the source after drop or cancel) | `dragIndex := overIndex := null` |

The dragged item is dimmed (`opacity-50`); the item under the pointer gets a blue top border. Each item also has ↑/↓ buttons that do the same move by one, so reordering works by keyboard.

**Edit model:** edits are local until **Save**; each Save is exactly one PUT (one draft commit). The top-level order and the selected group are saved separately ("Save order" → `PUT …/groups-index`; "Save group" → `PUT …/groups/{slug}`); saving one keeps the other's unsaved edits. Leaving the page or switching groups with unsaved edits asks for confirmation. Supported edits: reorder the top-level groups, rename the selected group, reorder its children, remove a child, add a task that is in no group, create a new top-level group (two PUTs: the group, then the index). Not supported (use Git on a draft branch): creating sub-groups, deleting group files, dragging between lists (remove from one group, save, add in the other).

- [ ] **Step 1: Create `web/src/views/taskauthor/ReorderList.vue`**

```vue
<script setup lang="ts" generic="T">
// A list the user reorders by dragging (native HTML5 drag-and-drop, no
// library — ruling 4.16) or with the ↑/↓ buttons. It only accepts drags
// it started itself.
import { ref } from 'vue'

const props = defineProps<{
  items: T[]
  itemKey: (item: T) => string
  disabled?: boolean
}>()

const emit = defineEmits<{
  'update:items': [items: T[]]
}>()

defineSlots<{
  item(props: { item: T; index: number }): any
}>()

const dragIndex = ref<number | null>(null)
const overIndex = ref<number | null>(null)

function move(from: number, to: number) {
  if (from === to || to < 0 || to >= props.items.length) return
  const next = props.items.slice()
  next.splice(to, 0, ...next.splice(from, 1))
  emit('update:items', next)
}

function onDragStart(e: DragEvent, i: number) {
  if (props.disabled) {
    e.preventDefault()
    return
  }
  dragIndex.value = i
  if (e.dataTransfer) {
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', String(i))
  }
}

function onDragOver(e: DragEvent, i: number) {
  if (dragIndex.value === null) return
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  overIndex.value = i
}

function onDragLeave(i: number) {
  if (overIndex.value === i) overIndex.value = null
}

function onDrop(e: DragEvent, i: number) {
  if (dragIndex.value === null) return
  e.preventDefault()
  move(dragIndex.value, i)
  dragIndex.value = null
  overIndex.value = null
}

function onDragEnd() {
  dragIndex.value = null
  overIndex.value = null
}
</script>

<template>
  <ol class="space-y-1">
    <li
      v-for="(item, index) in items"
      :key="itemKey(item)"
      :draggable="!disabled"
      class="flex items-center gap-2 rounded border border-gray-200 bg-white px-2 py-1"
      :class="{
        'opacity-50': dragIndex === index,
        'border-t-2 border-t-blue-500': overIndex === index && dragIndex !== index,
      }"
      @dragstart="onDragStart($event, index)"
      @dragover="onDragOver($event, index)"
      @dragleave="onDragLeave(index)"
      @drop="onDrop($event, index)"
      @dragend="onDragEnd"
    >
      <span aria-hidden="true" class="cursor-grab select-none text-gray-400">⋮⋮</span>
      <div class="min-w-0 flex-1">
        <slot name="item" :item="item" :index="index" />
      </div>
      <button
        type="button"
        class="px-1 text-gray-600 disabled:opacity-30"
        :disabled="disabled || index === 0"
        aria-label="Move up"
        @click="move(index, index - 1)"
      >
        ↑
      </button>
      <button
        type="button"
        class="px-1 text-gray-600 disabled:opacity-30"
        :disabled="disabled || index === items.length - 1"
        aria-label="Move down"
        @click="move(index, index + 1)"
      >
        ↓
      </button>
    </li>
  </ol>
</template>
```

- [ ] **Step 2: Create `web/src/views/taskauthor/GroupTreeView.vue`**

```vue
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import ProblemList from '../../components/ProblemList.vue'
import ReorderList from './ReorderList.vue'
import { errorMessage, getDraft, problemsOf, putGroup, putGroupsIndex, statusOf } from '../../api/authors'
import { routeParam, useAuthorRole } from '../../router/authors'
import type { CatalogGroup, CatalogProblem, DraftDetail, GroupChildRef } from '../../types/taskAuthor'

/** A top-level group in the index list; key is unique even if a draft lists a slug twice. */
interface IndexItem {
  key: string
  slug: string
}

/** A child of the selected group: exactly one of group (a slug) or task (a UUID). */
interface ChildItem {
  key: string
  group?: string
  task?: string
}

const SLUG_RE = /^[a-z0-9][a-z0-9-]*$/ // internal/catalog/catalog.go slugRE

const route = useRoute()
const router = useRouter()
const allowed = useAuthorRole('task_author')
const draftId = computed(() => routeParam(route.params.draftId))
const slug = computed(() => routeParam(route.params.slug))

const detail = ref<DraftDetail | null>(null)
const loadError = ref('')
const indexItems = ref<IndexItem[]>([])
const indexDirty = ref(false)
const title = ref('')
const children = ref<ChildItem[]>([])
const groupDirty = ref(false)
const saving = ref(false)
const saveError = ref('')
const saveProblems = ref<CatalogProblem[]>([])
const notice = ref('')
const addTaskId = ref('')
const newSlug = ref('')
const newTitle = ref('')
let keySeq = 0

const indexKey = (i: IndexItem) => i.key
const childKey = (c: ChildItem) => c.key

function collect(gs: CatalogGroup[], out: Map<string, CatalogGroup>): Map<string, CatalogGroup> {
  for (const g of gs) {
    if (out.has(g.slug)) continue
    out.set(g.slug, g)
    collect(g.children.flatMap((c) => (c.group ? [c.group] : [])), out)
  }
  return out
}

/** Every group reachable from the index, by slug. */
const groups = computed(() => collect(detail.value?.catalog.groups ?? [], new Map<string, CatalogGroup>()))
const selected = computed(() => groups.value.get(slug.value) ?? null)
const taskLabel = computed(
  () => new Map((detail.value?.catalog.tasks ?? []).map((t) => [t.id, `${t.title} (${t.version})`] as const)),
)
const groupTitle = (s: string) => groups.value.get(s)?.title ?? s

/** Slugs from a top-level group down to the selected one. */
const breadcrumb = computed(() => {
  const path: string[] = []
  const walk = (gs: CatalogGroup[]): boolean => {
    for (const g of gs) {
      path.push(g.slug)
      if (g.slug === slug.value) return true
      if (walk(g.children.flatMap((c) => (c.group ? [c.group] : [])))) return true
      path.pop()
    }
    return false
  }
  return walk(detail.value?.catalog.groups ?? []) ? path : []
})

/** Tasks in no group, counting the selected group's unsaved children instead of its saved ones. */
const ungroupedTasks = computed(() => {
  const used = new Set<string>()
  for (const g of groups.value.values()) {
    if (g.slug === slug.value) continue
    for (const c of g.children) if (c.task) used.add(c.task)
  }
  for (const c of children.value) if (c.task) used.add(c.task)
  return (detail.value?.catalog.tasks ?? [])
    .filter((t) => !used.has(t.id))
    .sort((a, b) => a.title.localeCompare(b.title))
})

const newSlugError = computed(() => {
  const s = newSlug.value.trim()
  if (s === '') return ''
  if (!SLUG_RE.test(s)) return 'A slug has lowercase letters, digits and dashes, and starts with a letter or digit.'
  if (groups.value.has(s)) return `There is already a group "${s}".`
  return ''
})

function resetIndex() {
  indexItems.value = (detail.value?.catalog.groups ?? []).map((g) => ({ key: `i${keySeq++}`, slug: g.slug }))
  indexDirty.value = false
}

function resetGroup() {
  const g = selected.value
  title.value = g?.title ?? ''
  children.value = (g?.children ?? []).map(
    (c): ChildItem => (c.group ? { key: `c${keySeq++}`, group: c.group.slug } : { key: `c${keySeq++}`, task: c.task ?? '' }),
  )
  addTaskId.value = ''
  groupDirty.value = false
}

/** Re-reads the draft; scope says which local edits to replace with what was read. */
async function load(scope: 'all' | 'index' | 'group' = 'all') {
  loadError.value = ''
  if (!allowed.value || draftId.value === '') return
  try {
    detail.value = await getDraft(draftId.value)
    if (scope !== 'group') resetIndex()
    if (scope !== 'index') resetGroup()
  } catch (e) {
    detail.value = null
    loadError.value =
      statusOf(e) === 404 ? 'This draft does not exist; it may have been published or discarded.' : errorMessage(e)
  }
}

watch([allowed, draftId, slug], () => {
  notice.value = ''
  saveError.value = ''
  saveProblems.value = []
  void load()
}, { immediate: true })

const dirty = computed(() => indexDirty.value || groupDirty.value)
const confirmLeave = () => !dirty.value || window.confirm('Discard your unsaved changes to the group tree?')
onBeforeRouteLeave(confirmLeave)
onBeforeRouteUpdate(confirmLeave)

function onIndexReordered(next: IndexItem[]) {
  indexItems.value = next
  indexDirty.value = true
}

function onChildrenReordered(next: ChildItem[]) {
  children.value = next
  groupDirty.value = true
}

function removeChild(key: string) {
  children.value = children.value.filter((c) => c.key !== key)
  groupDirty.value = true
}

function addTask() {
  if (addTaskId.value === '') return
  children.value = [...children.value, { key: `c${keySeq++}`, task: addTaskId.value }]
  addTaskId.value = ''
  groupDirty.value = true
}

async function save(done: string, action: () => Promise<void>) {
  saving.value = true
  saveError.value = ''
  saveProblems.value = []
  notice.value = ''
  try {
    await action()
    notice.value = done
  } catch (e) {
    saveError.value = errorMessage(e)
    saveProblems.value = problemsOf(e)
  } finally {
    saving.value = false
  }
}

function saveIndex() {
  return save('The top-level order is saved in this draft.', async () => {
    await putGroupsIndex(draftId.value, indexItems.value.map((i) => i.slug))
    await load('index')
  })
}

function saveGroup() {
  const t = title.value.trim()
  const body = {
    title: t,
    children: children.value.map((c): GroupChildRef => (c.group !== undefined ? { group: c.group } : { task: c.task })),
  }
  return save(`The group "${t}" is saved in this draft.`, async () => {
    await putGroup(draftId.value, slug.value, body)
    await load('group')
  })
}

function createGroup() {
  const s = newSlug.value.trim()
  const t = newTitle.value.trim()
  if (s === '' || t === '' || newSlugError.value !== '' || indexDirty.value) return
  return save(`The group "${t}" is created in this draft.`, async () => {
    await putGroup(draftId.value, s, { title: t, children: [] })
    await putGroupsIndex(draftId.value, [...indexItems.value.map((i) => i.slug), s])
    newSlug.value = ''
    newTitle.value = ''
    const failure = await router.push(`/catalog/drafts/${draftId.value}/groups/${s}`)
    if (failure) await load('index') // the user kept unsaved edits of the current group
  })
}
</script>

<template>
  <section class="mx-auto max-w-6xl space-y-6 p-6">
    <p v-if="!allowed" class="text-gray-700">Editing the group tree needs the task-author role.</p>
    <template v-else>
      <nav class="text-sm">
        <RouterLink :to="`/catalog/drafts/${draftId}`" class="text-blue-700 hover:underline">← Back to the draft</RouterLink>
      </nav>
      <p v-if="loadError" class="text-red-700">{{ loadError }}</p>
      <template v-else-if="detail">
        <h1 class="text-2xl font-semibold">Group tree of "{{ detail.draft.title }}"</h1>
        <p v-if="notice" class="rounded bg-green-50 p-2 text-green-900">{{ notice }}</p>
        <p v-if="saveError" class="text-red-700">{{ saveError }}</p>
        <ProblemList v-if="saveProblems.length > 0" :problems="saveProblems" />

        <div class="grid gap-6 md:grid-cols-[1fr_2fr]">
          <section class="space-y-3">
            <h2 class="font-semibold">Top-level order</h2>
            <p class="text-xs text-gray-600">Drag a group, or use the arrows, then save.</p>
            <ReorderList :items="indexItems" :item-key="indexKey" :disabled="saving" @update:items="onIndexReordered">
              <template #item="{ item }">
                <RouterLink
                  :to="`/catalog/drafts/${draftId}/groups/${item.slug}`"
                  :class="item.slug === slug ? 'font-semibold' : 'text-blue-700 hover:underline'"
                >
                  {{ groupTitle(item.slug) }}
                </RouterLink>
                <span class="ml-1 font-mono text-xs text-gray-500">{{ item.slug }}</span>
              </template>
            </ReorderList>
            <p v-if="indexItems.length === 0" class="text-sm text-gray-600">This catalog has no groups yet.</p>
            <div class="flex gap-2">
              <button
                type="button"
                class="rounded bg-blue-600 px-3 py-1 text-white disabled:opacity-50"
                :disabled="saving || !indexDirty"
                @click="saveIndex"
              >
                Save order
              </button>
              <button
                type="button"
                class="rounded px-3 py-1 text-gray-700 hover:bg-gray-100 disabled:opacity-50"
                :disabled="saving || !indexDirty"
                @click="resetIndex"
              >
                Discard
              </button>
            </div>

            <form class="space-y-2 border-t pt-3" @submit.prevent="createGroup">
              <h3 class="text-sm font-semibold">New top-level group</h3>
              <input v-model="newSlug" type="text" placeholder="slug, e.g. operations" class="w-full rounded border border-gray-300 px-2 py-1 font-mono text-sm" />
              <input v-model="newTitle" type="text" placeholder="Title" class="w-full rounded border border-gray-300 px-2 py-1 text-sm" />
              <p v-if="newSlugError" class="text-xs text-red-700">{{ newSlugError }}</p>
              <p v-if="indexDirty" class="text-xs text-amber-800">Save or discard the new order first.</p>
              <button
                type="submit"
                class="rounded bg-blue-600 px-3 py-1 text-sm text-white disabled:opacity-50"
                :disabled="saving || indexDirty || newSlug.trim() === '' || newTitle.trim() === '' || newSlugError !== ''"
              >
                Create group
              </button>
            </form>
          </section>

          <section class="space-y-3">
            <p v-if="!selected" class="text-gray-600">
              {{ slug === '-' ? 'Select a group on the left, or create one.' : `There is no group "${slug}" in this draft's tree.` }}
            </p>
            <template v-else>
              <nav class="text-xs text-gray-600">
                <template v-for="(s, i) in breadcrumb" :key="s">
                  <span v-if="i > 0"> / </span>
                  <RouterLink :to="`/catalog/drafts/${draftId}/groups/${s}`" class="hover:underline">{{ groupTitle(s) }}</RouterLink>
                </template>
              </nav>
              <label class="block text-sm font-semibold">
                Title
                <input
                  v-model="title"
                  type="text"
                  class="mt-1 w-full rounded border border-gray-300 px-3 py-2 font-normal"
                  @input="groupDirty = true"
                />
              </label>
              <h2 class="font-semibold">Children</h2>
              <ReorderList :items="children" :item-key="childKey" :disabled="saving" @update:items="onChildrenReordered">
                <template #item="{ item }">
                  <div class="flex items-center justify-between gap-2 text-sm">
                    <span v-if="item.group !== undefined">
                      Group:
                      <RouterLink :to="`/catalog/drafts/${draftId}/groups/${item.group}`" class="text-blue-700 hover:underline">
                        {{ groupTitle(item.group) }}
                      </RouterLink>
                    </span>
                    <span v-else>
                      Task:
                      <RouterLink :to="`/catalog/drafts/${draftId}/tasks/${item.task}`" class="text-blue-700 hover:underline">
                        {{ taskLabel.get(item.task ?? '') ?? item.task }}
                      </RouterLink>
                    </span>
                    <button type="button" class="text-xs text-red-700 hover:underline" :disabled="saving" @click="removeChild(item.key)">
                      Remove
                    </button>
                  </div>
                </template>
              </ReorderList>
              <p v-if="children.length === 0" class="text-sm text-gray-600">This group has no children.</p>
              <div class="flex gap-2">
                <select v-model="addTaskId" class="flex-1 rounded border border-gray-300 px-2 py-1 text-sm">
                  <option value="">Add a task that is in no group…</option>
                  <option v-for="t in ungroupedTasks" :key="t.id" :value="t.id">{{ t.title }} ({{ t.version }})</option>
                </select>
                <button
                  type="button"
                  class="rounded px-3 py-1 text-sm text-blue-700 hover:bg-gray-100 disabled:opacity-50"
                  :disabled="saving || addTaskId === ''"
                  @click="addTask"
                >
                  Add
                </button>
              </div>
              <div class="flex gap-2">
                <button
                  type="button"
                  class="rounded bg-blue-600 px-3 py-1 text-white disabled:opacity-50"
                  :disabled="saving || !groupDirty || title.trim() === ''"
                  @click="saveGroup"
                >
                  Save group
                </button>
                <button
                  type="button"
                  class="rounded px-3 py-1 text-gray-700 hover:bg-gray-100 disabled:opacity-50"
                  :disabled="saving || !groupDirty"
                  @click="resetGroup"
                >
                  Discard
                </button>
              </div>
            </template>
          </section>
        </div>

        <section>
          <h2 class="mb-2 font-semibold">Problems in this draft</h2>
          <ProblemList v-if="detail.problems.length > 0" :problems="detail.problems" />
          <p v-else class="text-sm text-green-800">None.</p>
        </section>
      </template>
    </template>
  </section>
</template>
```

- [ ] **Step 3: Register the route**

In `web/src/router/authors.ts`, replace

```ts
  // plan 4d: later tasks insert their routes above this line
```

with

```ts
  {
    path: '/catalog/drafts/:draftId/groups/:slug',
    name: 'catalog-draft-group',
    component: () => import('../views/taskauthor/GroupTreeView.vue'),
  },
  // plan 4d: later tasks insert their routes above this line
```

- [ ] **Step 4: Type-check**

Run: `cd web && npm run type-check`
Expected: exits 0. (If `vue-tsc` reports that `generic` is unknown, plan 4c installed Vue < 3.3; that contradicts the note's "Vue 3" scaffold being current — stop and report rather than rewriting `ReorderList` without generics.)

- [ ] **Step 5: Manual pass** (sign in as `$TA`, create a fresh draft "Groups" from the drafts view)

4.1 Click "Edit the group tree". URL `/catalog/drafts/<id>/groups/build`; left list "Build", "Release"; right: title "Build", one child task "Build the release binaries (1.0.0)".
4.2 Drag "Release" above "Build" with the mouse. While dragging, "Build" shows a blue top border under the pointer and "Release" is dimmed. After the drop the order is Release, Build; "Save order" is enabled; no request was sent.
4.3 Press "Save order". Exactly one `PUT …/groups-index` with body `{"groups":["release","build"]}`; the green notice appears; the order stays.
4.4 Click "Release". Drag "Build" from the **left** list onto a child in the **right** list: the cursor shows "not allowed" and dropping changes nothing. Drag a file from the desktop onto either list: nothing happens, the browser does not navigate to the file.
4.5 In "Release", use ↓ on "Name the release manager" (keyboard: Tab to the button, Enter). Order becomes T3, T2. Rename the title to "Release management". Press "Save group": one `PUT …/groups/release` with `{"title":"Release management","children":[{"task":"3333…"},{"task":"2222…"}]}`.
4.6 Remove "Pick the support tier", press "Save group". The "Problems in this draft" section is unchanged (a task in no group is allowed). Go to "Build", pick "Pick the support tier" in the "Add a task that is in no group…" select, Add, Save group. It is now under Build.
4.7 Change Build's title without saving, click "Release" on the left: a confirm dialog appears; Cancel keeps you on Build with the edit; OK discards it.
4.8 Create a top-level group with slug `Bad Slug`: the slug error shows and "Create group" is disabled. Use `operations` / "Operations": two PUTs (`groups/operations`, then `groups-index` with `operations` last), then the URL is `/groups/operations` and the group has no children.
4.9 Back on the draft, Publish. It succeeds (or, if 4b's `catalog.Check` rejects an empty group, shows the 422 problems — record which); the catalog's `GET /api/catalog` now shows the new order.
4.10 Open `/catalog/drafts/<id>/groups/nope` for an open draft: "There is no group "nope" in this draft's tree."; the left list still works.

- [ ] **Step 6: Commit**

```bash
git add web/src/views/taskauthor/ReorderList.vue web/src/views/taskauthor/GroupTreeView.vue web/src/router/authors.ts
git commit -m "Add the group tree editor with drag-to-reorder"
```

---

## Task 5 — `TaskEditorView`: the new-version wizard

**Files:**
- Create: `web/src/views/taskauthor/semver.ts`, `web/src/views/taskauthor/TaskEditorView.vue`
- Modify: `web/src/router/authors.ts`

**Interfaces:**
- Consumes: `getDraft`, `putTaskVersion`, `errorMessage`, `problemsOf`, `statusOf` (Task 1); `ANSWER_TYPES` (Task 1); `LineDiff` (Task 2); `MarkdownView`, `ProblemList` (4c).
- Produces: route `catalog-draft-task` (`/catalog/drafts/:draftId/tasks/:taskId`); `bumpVersion(version, step)` (mirror of `internal/semver.Bump`, for display only — the server computes the real next version) and `compareVersions(a, b)` (used by Task 6).

**Wizard states:** `1` choose the step (patch/minor/major; each option shows `current → next`), `2` edit title, answer type, choices (one per line, only for `choice`) and the Markdown body — all prefilled from the draft's current version ("copies the current version", §6.1) — `3` review: list of metadata changes, `LineDiff` of the body, the current and new body side by side through `MarkdownView`, and "Save". Save sends `PUT …/tasks/{task-id}` with `{step,title,body,answer_type}` plus `choices` only when the answer type is `choice` (rule: choices are only allowed with `choice`), re-reads the draft, shows "Version X is saved in this draft", and returns to state 1 prefilled from the new current version. Leaving the page in state 2 or 3 asks for confirmation.

- [ ] **Step 1: Create `web/src/views/taskauthor/semver.ts`**

```ts
// Display helpers mirroring internal/semver. The server computes the real
// next version; these only preview it and order versions on screen.
import type { SemverStep } from '../../types/taskAuthor'

function core(v: string): [number, number, number] {
  const [c = ''] = v.split('-', 1)
  const p = c.split('.').map((s) => Number.parseInt(s, 10))
  return [p[0] ?? 0, p[1] ?? 0, p[2] ?? 0]
}

/** semver.Bump: a pre-release suffix is dropped before increasing, so 1.3.0-rc.1 patches to 1.3.1. */
export function bumpVersion(v: string, step: SemverStep): string {
  const [major, minor, patch] = core(v)
  switch (step) {
    case 'major':
      return `${major + 1}.0.0`
    case 'minor':
      return `${major}.${minor + 1}.0`
    case 'patch':
      return `${major}.${minor}.${patch + 1}`
  }
}

/** Negative, zero or positive as a is lower than, equal to or higher than b. */
export function compareVersions(a: string, b: string): number {
  const ca = core(a)
  const cb = core(b)
  const d = ca[0] - cb[0] || ca[1] - cb[1] || ca[2] - cb[2]
  if (d !== 0) return d
  const pa = a.includes('-')
  const pb = b.includes('-')
  if (pa !== pb) return pa ? -1 : 1 // a pre-release is lower than its release
  return a < b ? -1 : a > b ? 1 : 0 // pre-release identifiers compared as text: close enough for display
}
```

- [ ] **Step 2: Create `web/src/views/taskauthor/TaskEditorView.vue`**

```vue
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, onBeforeRouteLeave, useRoute } from 'vue-router'
import MarkdownView from '../../components/MarkdownView.vue'
import ProblemList from '../../components/ProblemList.vue'
import LineDiff from './LineDiff.vue'
import { bumpVersion } from './semver'
import { errorMessage, getDraft, problemsOf, putTaskVersion, statusOf } from '../../api/authors'
import { routeParam, useAuthorRole } from '../../router/authors'
import { ANSWER_TYPES } from '../../types/taskAuthor'
import type { AnswerType, CatalogProblem, DraftDetail, NewVersionRequest, SemverStep } from '../../types/taskAuthor'

const STEPS: { value: SemverStep; label: string }[] = [
  { value: 'patch', label: 'Patch' },
  { value: 'minor', label: 'Minor' },
  { value: 'major', label: 'Major' },
]
const WIZARD = ['1. Version step', '2. Edit', '3. Review'] as const

const route = useRoute()
const allowed = useAuthorRole('task_author')
const draftId = computed(() => routeParam(route.params.draftId))
const taskId = computed(() => routeParam(route.params.taskId))

const detail = ref<DraftDetail | null>(null)
const loadError = ref('')
const current = computed(() => detail.value?.catalog.tasks.find((t) => t.id === taskId.value) ?? null)

const wizardStep = ref<1 | 2 | 3>(1)
const step = ref<SemverStep>('minor')
const title = ref('')
const answerType = ref<AnswerType>('markdown')
const choicesText = ref('')
const body = ref('')
const saving = ref(false)
const saveError = ref('')
const saveProblems = ref<CatalogProblem[]>([])
const notice = ref('')

const nextVersion = computed(() => (current.value ? bumpVersion(current.value.version, step.value) : ''))
const choices = computed(() =>
  choicesText.value
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s !== ''),
)

const formError = computed(() => {
  if (title.value.trim() === '') return 'The title must not be empty.'
  if (answerType.value === 'choice' && choices.value.length === 0) return 'A choice task needs at least one choice, one per line.'
  return ''
})

const metaChanges = computed(() => {
  const c = current.value
  if (!c) return []
  const out: string[] = []
  const t = title.value.trim()
  if (t !== c.title) out.push(`Title: "${c.title}" → "${t}"`)
  if (answerType.value !== c.answer_type) out.push(`Answer type: ${c.answer_type} → ${answerType.value}`)
  const before = (c.choices ?? []).join(', ')
  const after = answerType.value === 'choice' ? choices.value.join(', ') : ''
  if (before !== after) out.push(`Choices: ${before || '(none)'} → ${after || '(none)'}`)
  return out
})

const unchanged = computed(() => metaChanges.value.length === 0 && current.value !== null && body.value === current.value.body)

function resetForm() {
  const c = current.value
  wizardStep.value = 1
  step.value = 'minor'
  title.value = c?.title ?? ''
  answerType.value = c?.answer_type ?? 'markdown'
  choicesText.value = (c?.choices ?? []).join('\n')
  body.value = c?.body ?? ''
}

async function load() {
  loadError.value = ''
  saveError.value = ''
  saveProblems.value = []
  if (!allowed.value || draftId.value === '') return
  try {
    detail.value = await getDraft(draftId.value)
    resetForm()
  } catch (e) {
    detail.value = null
    loadError.value =
      statusOf(e) === 404 ? 'This draft does not exist; it may have been published or discarded.' : errorMessage(e)
  }
}

watch([allowed, draftId, taskId], () => {
  notice.value = ''
  void load()
}, { immediate: true })

async function save() {
  const c = current.value
  if (c === null || formError.value !== '' || saving.value) return
  const req: NewVersionRequest = {
    step: step.value,
    title: title.value.trim(),
    body: body.value,
    answer_type: answerType.value,
  }
  if (answerType.value === 'choice') req.choices = choices.value
  const made = nextVersion.value
  saving.value = true
  saveError.value = ''
  saveProblems.value = []
  try {
    await putTaskVersion(draftId.value, c.id, req)
    await load()
    notice.value = `Version ${made} is saved in this draft. Publish the draft to make it the current version in the catalog.`
  } catch (e) {
    saveError.value = errorMessage(e)
    saveProblems.value = problemsOf(e)
  } finally {
    saving.value = false
  }
}

onBeforeRouteLeave(() => wizardStep.value === 1 || saving.value || window.confirm('Discard this unsaved new version?'))
</script>

<template>
  <section class="mx-auto max-w-5xl space-y-6 p-6">
    <p v-if="!allowed" class="text-gray-700">Editing tasks needs the task-author role.</p>
    <template v-else>
      <nav class="text-sm">
        <RouterLink :to="`/catalog/drafts/${draftId}`" class="text-blue-700 hover:underline">← Back to the draft</RouterLink>
      </nav>
      <p v-if="loadError" class="text-red-700">{{ loadError }}</p>
      <template v-else-if="detail">
        <p v-if="!current" class="text-gray-700">
          Task <code>{{ taskId }}</code> has no current version in this draft: it is unknown, was merged into another
          task, or has two current versions that a new version must merge first.
        </p>
        <template v-else>
          <header class="space-y-1">
            <h1 class="text-2xl font-semibold">{{ current.title }}</h1>
            <p class="text-sm text-gray-600">
              Task <code>{{ current.id }}</code> · current version in this draft {{ current.version }} ·
              {{ current.answer_type }}
            </p>
          </header>
          <p v-if="notice" class="rounded bg-green-50 p-2 text-green-900">{{ notice }}</p>

          <ol class="flex gap-4 text-sm">
            <li v-for="(label, i) in WIZARD" :key="label" :class="wizardStep === i + 1 ? 'font-semibold text-blue-700' : 'text-gray-500'">
              {{ label }}
            </li>
          </ol>

          <section v-if="wizardStep === 1" class="space-y-3">
            <p class="text-sm">
              A new version copies version {{ current.version }}, lists it as its previous version, and increases the
              version number by the step you choose.
            </p>
            <fieldset class="space-y-2">
              <legend class="sr-only">Version step</legend>
              <label v-for="s in STEPS" :key="s.value" class="flex items-center gap-2">
                <input v-model="step" type="radio" name="step" :value="s.value" />
                <span class="w-12 font-medium">{{ s.label }}</span>
                <span class="font-mono text-sm text-gray-600">{{ current.version }} → {{ bumpVersion(current.version, s.value) }}</span>
              </label>
            </fieldset>
            <button type="button" class="rounded bg-blue-600 px-4 py-2 text-white" @click="wizardStep = 2">Next: edit</button>
          </section>

          <section v-else-if="wizardStep === 2" class="space-y-3">
            <label class="block text-sm font-semibold">
              Title
              <input v-model="title" type="text" class="mt-1 w-full rounded border border-gray-300 px-3 py-2 font-normal" />
            </label>
            <label class="block text-sm font-semibold">
              Answer type
              <select v-model="answerType" class="mt-1 block rounded border border-gray-300 px-3 py-2 font-normal">
                <option v-for="t in ANSWER_TYPES" :key="t" :value="t">{{ t }}</option>
              </select>
            </label>
            <label v-if="answerType === 'choice'" class="block text-sm font-semibold">
              Choices, one per line
              <textarea v-model="choicesText" rows="4" class="mt-1 w-full rounded border border-gray-300 p-2 font-mono font-normal" />
            </label>
            <label class="block text-sm font-semibold">
              Description (Markdown)
              <textarea v-model="body" rows="16" class="mt-1 w-full rounded border border-gray-300 p-2 font-mono font-normal" />
            </label>
            <p v-if="formError" class="text-sm text-red-700">{{ formError }}</p>
            <div class="flex gap-2">
              <button type="button" class="rounded px-4 py-2 text-gray-700 hover:bg-gray-100" @click="wizardStep = 1">Back</button>
              <button
                type="button"
                class="rounded bg-blue-600 px-4 py-2 text-white disabled:opacity-50"
                :disabled="formError !== ''"
                @click="wizardStep = 3"
              >
                Next: review
              </button>
            </div>
          </section>

          <section v-else class="space-y-4">
            <h2 class="text-lg font-semibold">Version {{ nextVersion }} (previous: {{ current.version }})</h2>
            <ul v-if="metaChanges.length > 0" class="list-disc pl-6 text-sm">
              <li v-for="m in metaChanges" :key="m">{{ m }}</li>
            </ul>
            <p v-else class="text-sm text-gray-600">Title, answer type and choices are unchanged.</p>
            <p v-if="unchanged" class="text-sm text-amber-800">Nothing changed: the new version would equal {{ current.version }}.</p>
            <h3 class="font-semibold">Description changes</h3>
            <LineDiff :before="current.body" :after="body" />
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <h3 class="mb-1 text-sm font-semibold">Current ({{ current.version }})</h3>
                <MarkdownView :source="current.body" />
              </div>
              <div>
                <h3 class="mb-1 text-sm font-semibold">New ({{ nextVersion }})</h3>
                <MarkdownView :source="body" />
              </div>
            </div>
            <p v-if="saveError" class="text-red-700">{{ saveError }}</p>
            <ProblemList v-if="saveProblems.length > 0" :problems="saveProblems" />
            <div class="flex gap-2">
              <button type="button" class="rounded px-4 py-2 text-gray-700 hover:bg-gray-100" :disabled="saving" @click="wizardStep = 2">
                Back
              </button>
              <button
                type="button"
                class="rounded bg-green-700 px-4 py-2 text-white disabled:opacity-50"
                :disabled="saving || formError !== ''"
                @click="save"
              >
                {{ saving ? 'Saving…' : `Save version ${nextVersion} in the draft` }}
              </button>
            </div>
          </section>
        </template>
      </template>
    </template>
  </section>
</template>
```

- [ ] **Step 3: Register the route**

In `web/src/router/authors.ts`, replace

```ts
  // plan 4d: later tasks insert their routes above this line
```

with

```ts
  {
    path: '/catalog/drafts/:draftId/tasks/:taskId',
    name: 'catalog-draft-task',
    component: () => import('../views/taskauthor/TaskEditorView.vue'),
  },
  // plan 4d: later tasks insert their routes above this line
```

- [ ] **Step 4: Type-check**

Run: `cd web && npm run type-check`
Expected: exits 0.

- [ ] **Step 5: Manual pass** (sign in as `$TA`, create a draft "Wizard")

5.1 In the draft's task list click "New version…" next to "Build the release binaries". Step 1 shows Patch `1.0.0 → 1.0.1`, Minor `1.0.0 → 1.1.0` (selected), Major `1.0.0 → 2.0.0`. (If Task 3's step 3.7 already published a T2 1.1.0, T2 shows `1.1.0 → …` instead.)
5.2 Choose Major, Next. Step 2 is prefilled with the current title, `markdown`, and the body.
5.3 Clear the title: "The title must not be empty." and Next is disabled. Restore it. Switch answer type to `choice` with empty choices: the choices error shows. Switch back to `markdown`.
5.4 Change the body's second paragraph to "Name the build host and its OS." Next. Step 3: heading "Version 2.0.0 (previous: 1.0.0)", "Title, answer type and choices are unchanged.", the diff has one `-` and one `+` line, and both Markdown previews render (headings/paragraphs, not raw text).
5.5 Click "← Back to the draft": a confirm dialog appears; Cancel stays.
5.6 Save. Exactly one `PUT …/tasks/1111…` with body `{"step":"major","title":"Build the release binaries","body":"…","answer_type":"markdown"}` (no `choices` key). The notice says "Version 2.0.0 is saved…"; the header now says current version 2.0.0; the wizard is back on step 1 showing `2.0.0 → 3.0.0` for Major.
5.7 Open "Pick the support tier", Minor, add a line `premium` to the choices, Next, Save. The PUT body contains `"choices":["basic","extended","premium"]`; step 3 listed "Choices: basic, extended → basic, extended, premium".
5.8 Back on the draft, Publish; it succeeds. `GET /api/catalog/tasks/$T1` (Network or curl) lists 1.0.0 and 2.0.0 with current 2.0.0.
5.9 Open `/catalog/drafts/<open draft>/tasks/00000000-0000-4000-8000-000000000000`: the "has no current version in this draft" message, no error in the Console.

- [ ] **Step 6: Commit**

```bash
git add web/src/views/taskauthor/semver.ts web/src/views/taskauthor/TaskEditorView.vue web/src/router/authors.ts
git commit -m "Add the new-version wizard for catalog tasks"
```

---

## Task 6 — `VersionGraphView` in the task editor

**Files:**
- Create: `web/src/views/taskauthor/VersionGraphView.vue`
- Modify: `web/src/views/taskauthor/TaskEditorView.vue`

**Interfaces:**
- Consumes: `getCatalogTask`, `errorMessage`, `statusOf` (Task 1); `compareVersions` (Task 5).
- Produces: `<VersionGraphView :task-id="string" :draft-version="CatalogTaskVersion | null" />` — a component, not a route (the architecture note's router list gives it none). It fetches the task's published history (`GET /api/catalog/tasks/{id}`, all versions on `main`, lowest first) when `taskId` changes, and adds `draftVersion` when that version is not on `main`.

**Rendering rule ("list of generations", ruling 4.16 — no graph library):** generation of a version = 0 when its `previous` is empty or names only versions not shown, else 1 + the highest generation among the shown versions it names (longest path from a root; a reference cycle, which `main` rejects anyway, counts as unknown). A draft-only version that names an earlier version of the same task that is not on `main` (an intermediate version made earlier in the same draft) is placed one generation after the highest published one. Generations are listed in ascending order, labelled "Generation 1, 2, …"; within one, versions ascend by semver. Each version shows its title, badges "current (published)" / "current in this draft" / "not published", and one line per `previous` reference: `← 1.2.0` for the same task, `← 1.2.0 (not published; an earlier version in this draft)` for a same-task reference not on `main`, and `← 3f1c2a4e…@2.0.0 (another task, merged into this one)` for a merge (§3.1). A 404 (task not on `main`) shows only the draft version.

- [ ] **Step 1: Create `web/src/views/taskauthor/VersionGraphView.vue`**

```vue
<script setup lang="ts">
// A task's version history (§3.1 "task-history graph") as an ordered list
// of generations — plain HTML, no graph-drawing library (ruling 4.16).
import { computed, ref, watch } from 'vue'
import { compareVersions } from './semver'
import { errorMessage, getCatalogTask, statusOf } from '../../api/authors'
import type { CatalogTaskVersion, TaskHistory, TaskRef } from '../../types/taskAuthor'

const props = defineProps<{
  taskId: string
  draftVersion: CatalogTaskVersion | null
}>()

interface GraphNode {
  version: CatalogTaskVersion
  generation: number
  mainCurrent: boolean
  draftCurrent: boolean
  draftOnly: boolean
}

const history = ref<TaskHistory | null>(null)
const notOnMain = ref(false)
const error = ref('')
const loading = ref(false)

const refKey = (r: TaskRef) => `${r.id}@${r.version}`

async function load() {
  history.value = null
  notOnMain.value = false
  error.value = ''
  if (props.taskId === '') return
  loading.value = true
  try {
    history.value = await getCatalogTask(props.taskId)
  } catch (e) {
    if (statusOf(e) === 404) notOnMain.value = true
    else error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}

watch(() => props.taskId, load, { immediate: true })

/** Published versions of this task, by "id@version". */
const published = computed(() => {
  const m = new Map<string, CatalogTaskVersion>()
  for (const v of history.value?.versions ?? []) m.set(refKey(v), v)
  return m
})

const nodes = computed<GraphNode[]>(() => {
  const shown = new Map(published.value)
  const d = props.draftVersion
  const draftOnly = d !== null && !shown.has(refKey(d))
  if (d !== null && draftOnly) shown.set(refKey(d), d)

  const gen = new Map<string, number>()
  const visiting = new Set<string>()
  const generation = (k: string): number => {
    const cached = gen.get(k)
    if (cached !== undefined) return cached
    const v = shown.get(k)
    if (v === undefined || visiting.has(k)) return -1 // not shown, or a cycle
    visiting.add(k)
    let g = 0
    for (const p of v.previous) g = Math.max(g, generation(refKey(p)) + 1)
    visiting.delete(k)
    gen.set(k, g)
    return g
  }

  const out: GraphNode[] = []
  let maxPublished = -1
  for (const [k, v] of published.value) {
    const g = generation(k)
    maxPublished = Math.max(maxPublished, g)
    out.push({
      version: v,
      generation: g,
      mainCurrent: v.version === history.value?.current,
      draftCurrent: d !== null && refKey(d) === k,
      draftOnly: false,
    })
  }
  if (d !== null && draftOnly) {
    let g = generation(refKey(d))
    if (d.previous.some((p) => p.id === d.id && !shown.has(refKey(p)))) g = Math.max(g, maxPublished + 1)
    out.push({ version: d, generation: g, mainCurrent: false, draftCurrent: true, draftOnly: true })
  }
  return out
})

const generations = computed(() => {
  const by = new Map<number, GraphNode[]>()
  for (const n of nodes.value) {
    const list = by.get(n.generation) ?? []
    list.push(n)
    by.set(n.generation, list)
  }
  return [...by.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([generation, list]) => ({
      generation,
      nodes: list.sort((x, y) => compareVersions(x.version.version, y.version.version)),
    }))
})

function previousLabel(p: TaskRef): string {
  if (p.id === props.taskId) {
    return published.value.has(refKey(p)) ? p.version : `${p.version} (not published; an earlier version in this draft)`
  }
  return `${p.id.slice(0, 8)}…@${p.version} (another task, merged into this one)`
}
</script>

<template>
  <section class="space-y-3">
    <h2 class="text-lg font-semibold">Version history</h2>
    <p v-if="loading" class="text-sm text-gray-600">Loading…</p>
    <p v-else-if="error" class="text-sm text-red-700">{{ error }}</p>
    <template v-else>
      <p v-if="notOnMain" class="text-sm text-gray-600">
        This task is not in the published catalog yet; only this draft's version is shown.
      </p>
      <p v-if="history?.superseded" class="text-sm text-amber-800">
        In the published catalog this task was merged into another task and has no current version.
      </p>
      <ol class="space-y-3">
        <li v-for="g in generations" :key="g.generation" class="rounded border border-gray-200 p-3">
          <h3 class="text-xs font-semibold uppercase text-gray-500">Generation {{ g.generation + 1 }}</h3>
          <ul class="mt-2 space-y-2">
            <li v-for="n in g.nodes" :key="n.version.version" class="text-sm">
              <span class="font-mono font-semibold">{{ n.version.version }}</span>
              <span class="ml-2">{{ n.version.title }}</span>
              <span v-if="n.mainCurrent" class="ml-2 rounded bg-green-100 px-1.5 text-xs text-green-900">current (published)</span>
              <span v-if="n.draftCurrent" class="ml-2 rounded bg-blue-100 px-1.5 text-xs text-blue-900">current in this draft</span>
              <span v-if="n.draftOnly" class="ml-2 rounded bg-amber-100 px-1.5 text-xs text-amber-900">not published</span>
              <ul v-if="n.version.previous.length > 0" class="ml-6 text-xs text-gray-600">
                <li v-for="p in n.version.previous" :key="refKey(p)">← {{ previousLabel(p) }}</li>
              </ul>
            </li>
          </ul>
        </li>
      </ol>
    </template>
  </section>
</template>
```

- [ ] **Step 2: Embed it in `TaskEditorView.vue`**

Replace

```ts
import LineDiff from './LineDiff.vue'
import { bumpVersion } from './semver'
```

with

```ts
import LineDiff from './LineDiff.vue'
import VersionGraphView from './VersionGraphView.vue'
import { bumpVersion } from './semver'
```

and replace

```vue
          <p v-if="notice" class="rounded bg-green-50 p-2 text-green-900">{{ notice }}</p>

          <ol class="flex gap-4 text-sm">
```

with

```vue
          <p v-if="notice" class="rounded bg-green-50 p-2 text-green-900">{{ notice }}</p>

          <VersionGraphView :task-id="current.id" :draft-version="current" />

          <ol class="flex gap-4 text-sm">
```

(`current` is a new object after every save's re-read, so the generations recompute; the published history is fetched again only when the task id changes.)

- [ ] **Step 3: Type-check**

Run: `cd web && npm run type-check`
Expected: exits 0.

- [ ] **Step 4: Manual pass** (sign in as `$TA`; T1 has 1.0.0 and 2.0.0 on `main` after Task 5)

6.1 New draft "History", open T1's editor. "Version history" shows Generation 1: `1.0.0`; Generation 2: `2.0.0` with "current (published)" and "current in this draft", and `← 1.0.0`.
6.2 Save a Patch version (2.0.1). The list gains Generation 3: `2.0.1` "current in this draft", "not published", `← 2.0.0`; `2.0.0` keeps only "current (published)". Only one `GET /api/catalog/tasks/…` was made since the page opened.
6.3 Save another Patch (2.0.2) in the same draft. Generation 3 now shows `2.0.2` with `← 2.0.1 (not published; an earlier version in this draft)`; `2.0.1` is no longer listed (the draft response carries current versions only — see the open question in "Decisions").
6.4 Publish the draft and reopen T1's editor from a new draft: `2.0.2` is "current (published)" in Generation 3. (Generation numbering skips nothing: 1.0.0, 2.0.0, 2.0.1, 2.0.2 are generations 1–4 now that 2.0.1 is on `main`.)
6.5 Merge check, via Git on a draft branch (no UI for merging tasks): in a clone of the catalog, add `tasks/$T2/2.0.0.md` with `previous: [{id: $T2, version: <T2's current>}, {id: $T3, version: <T3's current>}]`, remove T3 from its group, push to `main` as `$TA`. T2's history shows `← 33333333…@… (another task, merged into this one)`; opening T3's editor from a draft says it has no current version.

- [ ] **Step 5: Commit**

```bash
git add web/src/views/taskauthor/VersionGraphView.vue web/src/views/taskauthor/TaskEditorView.vue
git commit -m "Show a task's version history as generations in the task editor"
```

---

## Task 7 — `RegistryView`

**Files:**
- Create: `web/src/views/processorauthor/RegistryView.vue`
- Modify: `web/src/router/authors.ts`

**Interfaces:**
- Consumes: `listProcessors`, `getCatalog`, `errorMessage` (Task 1).
- Produces: route `processors` (`/processors`), linking to `/processors/:name/runs` (Task 8) and `/processors/:name/dry-run` (Task 9).

`GET /api/processors` is the registry at catalog `main` (`timeout` defaults to `"60s"`, lists are never null — 3d's decisions). Bound tasks are shown by title from `GET /api/catalog`; if that call fails, by id — the registry still renders.

- [ ] **Step 1: Create `web/src/views/processorauthor/RegistryView.vue`**

```vue
<script setup lang="ts">
import { ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { errorMessage, getCatalog, listProcessors } from '../../api/authors'
import { useAuthorRole } from '../../router/authors'
import type { ProcessorInfo } from '../../types/processorAuthor'

const allowed = useAuthorRole('processor_author')
const processors = ref<ProcessorInfo[]>([])
const taskTitles = ref(new Map<string, string>())
const loading = ref(false)
const error = ref('')

async function load() {
  if (!allowed.value) return
  loading.value = true
  error.value = ''
  try {
    processors.value = await listProcessors()
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
  try {
    const c = await getCatalog()
    taskTitles.value = new Map(c.tasks.map((t) => [t.id, t.title] as const))
  } catch {
    // Titles are a convenience; ids are shown instead.
  }
}

watch(allowed, () => void load(), { immediate: true })

const imageName = (p: ProcessorInfo) => p.image.split('@')[0] ?? p.image
</script>

<template>
  <section class="mx-auto max-w-6xl space-y-6 p-6">
    <p v-if="!allowed" class="text-gray-700">The processor registry needs the processor-author role.</p>
    <template v-else>
      <header class="space-y-1">
        <h1 class="text-2xl font-semibold">Processors</h1>
        <p class="text-sm text-gray-600">
          The registry on the catalog's main. Changing an image or a binding is a catalog change made in a draft.
        </p>
      </header>
      <p v-if="loading" class="text-gray-600">Loading…</p>
      <p v-else-if="error" class="text-red-700">{{ error }}</p>
      <p v-else-if="processors.length === 0" class="text-gray-600">No processors are registered.</p>
      <table v-else class="w-full text-left text-sm">
        <thead>
          <tr class="border-b">
            <th class="py-2">Name</th>
            <th>Image</th>
            <th>Digest</th>
            <th>Timeout</th>
            <th>Network</th>
            <th>Secrets</th>
            <th>Bound tasks</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in processors" :key="p.name" class="border-b align-top">
            <td class="py-2 font-mono">{{ p.name }}</td>
            <td class="font-mono text-xs">{{ imageName(p) }}</td>
            <td class="font-mono text-xs" :title="p.digest">{{ p.digest.slice(0, 19) }}…</td>
            <td>{{ p.timeout }}</td>
            <td>{{ p.network ? 'yes' : 'no' }}</td>
            <td>{{ p.secrets.length > 0 ? p.secrets.join(', ') : '—' }}</td>
            <td>
              <ul v-if="p.bound_tasks.length > 0">
                <li v-for="id in p.bound_tasks" :key="id">{{ taskTitles.get(id) ?? id }}</li>
              </ul>
              <span v-else class="text-gray-500">none</span>
            </td>
            <td class="space-x-3 whitespace-nowrap text-right">
              <RouterLink :to="`/processors/${p.name}/runs`" class="text-blue-700 hover:underline">Runs</RouterLink>
              <RouterLink :to="`/processors/${p.name}/dry-run`" class="text-blue-700 hover:underline">Dry run</RouterLink>
            </td>
          </tr>
        </tbody>
      </table>
    </template>
  </section>
</template>
```

- [ ] **Step 2: Register the route**

In `web/src/router/authors.ts`, replace

```ts
  // plan 4d: later tasks insert their routes above this line
```

with

```ts
  {
    path: '/processors',
    name: 'processors',
    component: () => import('../views/processorauthor/RegistryView.vue'),
  },
  // plan 4d: later tasks insert their routes above this line
```

- [ ] **Step 3: Type-check**

Run: `cd web && npm run type-check`
Expected: exits 0.

- [ ] **Step 4: Manual pass** (links to runs/dry-run lead nowhere until Tasks 8–9; vue-router's "No match found" for those two only is expected here)

7.1 Sign in as `$PA`, click "Processors". Two rows: `echo` (bound to "Build the release binaries"), `fail` (bound to "Name the release manager"); timeout `60s`, network `no`, secrets `—`; the image column shows `custos.manual/echo`, the digest `sha256:…` truncated with the full value as tooltip.
7.2 Block `GET /api/catalog` in the browser's Network tab ("Block request URL") and reload: the table still renders, bound tasks shown by UUID. Unblock.
7.3 Sign in as `$TA` and type `/processors`: "The processor registry needs the processor-author role." and no `/api/processors` request.

- [ ] **Step 5: Commit**

```bash
git add web/src/views/processorauthor/RegistryView.vue web/src/router/authors.ts
git commit -m "Add the processor registry view"
```

---

## Task 8 — `RunsView`: run history and logs of one processor

**Files:**
- Create: `web/src/views/processorauthor/RunsView.vue`
- Modify: `web/src/router/authors.ts`

**Interfaces:**
- Consumes: `listWorkspaces`, `listRuns`, `getRunLog`, `retryRun`, `errorMessage` (Task 1).
- Produces: route `processor-runs` (`/processors/:name/runs`).

**Data:** run records are per workspace (`GET /api/workspaces/{id}/runs`, 3d); there is no per-processor endpoint. The view lists workspaces (`GET /api/workspaces`), fetches every workspace's runs in parallel (`Promise.allSettled`, so one broken workspace does not hide the others — it is listed under "Could not load runs of"), keeps records whose `processor` equals `:name`, and sorts them by `queued`, newest first. Filters (state, workspace) are client-side.

**Polling:** after each refresh, if any kept record is `queued` or `running`, the next refresh is scheduled with `setTimeout(refresh, 5000)` (never `setInterval`, so requests never overlap); otherwise polling stops. Unmounting clears the timer and drops any in-flight response; a newer refresh supersedes an older one's response (sequence counter). When the selected run's state changes during a refresh, its log is fetched again (the log is complete only once the run finished).

**Retry:** shown for `failed` runs only (3d: 409 otherwise). `POST …/retry` → 202 new record; the view refreshes and selects it. The server may answer 403 if plan 4a gates retry by workspace role; the message is shown.

- [ ] **Step 1: Create `web/src/views/processorauthor/RunsView.vue`**

```vue
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { errorMessage, getRunLog, listRuns, listWorkspaces, retryRun } from '../../api/authors'
import { routeParam, useAuthorRole } from '../../router/authors'
import type { RunRecord, RunState } from '../../types/processorAuthor'

const POLL_MS = 5000
const STATES: RunState[] = ['queued', 'running', 'succeeded', 'failed']

const route = useRoute()
const allowed = useAuthorRole('processor_author')
const name = computed(() => routeParam(route.params.name))

const runs = ref<RunRecord[]>([])
const workspaceErrors = ref<string[]>([])
const error = ref('')
const loading = ref(false)
const polling = ref(false)
const stateFilter = ref<RunState | ''>('')
const workspaceFilter = ref('')
const selected = ref<RunRecord | null>(null)
const log = ref('')
const logError = ref('')
const logLoading = ref(false)
const retrying = ref(false)
const actionError = ref('')

let timer: ReturnType<typeof setTimeout> | undefined
let alive = true
let refreshSeq = 0

const active = (r: RunRecord) => r.state === 'queued' || r.state === 'running'
const workspaces = computed(() => [...new Set(runs.value.map((r) => r.workspace))].sort())
const shown = computed(() =>
  runs.value.filter(
    (r) =>
      (stateFilter.value === '' || r.state === stateFilter.value) &&
      (workspaceFilter.value === '' || r.workspace === workspaceFilter.value),
  ),
)
const fmt = (s: string | undefined) => (s ? new Date(s).toLocaleString() : '—')
const stateClass = (s: RunState) =>
  ({
    queued: 'bg-gray-100 text-gray-800',
    running: 'bg-blue-100 text-blue-900',
    succeeded: 'bg-green-100 text-green-900',
    failed: 'bg-red-100 text-red-900',
  })[s]

async function loadLog(r: RunRecord) {
  logLoading.value = true
  logError.value = ''
  try {
    const text = await getRunLog(r.workspace, r.id)
    if (selected.value?.id === r.id) log.value = text
  } catch (e) {
    if (selected.value?.id === r.id) logError.value = errorMessage(e)
  } finally {
    logLoading.value = false
  }
}

function select(r: RunRecord) {
  selected.value = r
  log.value = ''
  actionError.value = ''
  void loadLog(r)
}

async function refresh() {
  clearTimeout(timer)
  timer = undefined
  polling.value = false
  if (!allowed.value || name.value === '') return
  const seq = ++refreshSeq
  loading.value = runs.value.length === 0
  error.value = ''
  try {
    const wss = await listWorkspaces()
    const results = await Promise.allSettled(wss.map((w) => listRuns(w.id)))
    if (!alive || seq !== refreshSeq) return
    const all: RunRecord[] = []
    const errs: string[] = []
    results.forEach((r, i) => {
      if (r.status === 'fulfilled') all.push(...r.value.filter((x) => x.processor === name.value))
      else errs.push(`${wss[i]?.id ?? '?'}: ${errorMessage(r.reason)}`)
    })
    all.sort((a, b) => Date.parse(b.queued) - Date.parse(a.queued))
    runs.value = all
    workspaceErrors.value = errs
    const sel = selected.value
    if (sel !== null) {
      const now = all.find((r) => r.id === sel.id && r.workspace === sel.workspace) ?? null
      selected.value = now
      if (now === null) log.value = ''
      else if (now.state !== sel.state) void loadLog(now)
    }
    if (all.some(active)) {
      polling.value = true
      timer = setTimeout(() => void refresh(), POLL_MS)
    }
  } catch (e) {
    if (alive && seq === refreshSeq) error.value = errorMessage(e)
  } finally {
    if (seq === refreshSeq) loading.value = false
  }
}

async function retry(r: RunRecord) {
  retrying.value = true
  actionError.value = ''
  try {
    const rec = await retryRun(r.workspace, r.id)
    await refresh()
    const now = runs.value.find((x) => x.id === rec.id && x.workspace === rec.workspace)
    if (now) select(now)
  } catch (e) {
    actionError.value = errorMessage(e)
  } finally {
    retrying.value = false
  }
}

watch([allowed, name], () => {
  selected.value = null
  runs.value = []
  void refresh()
}, { immediate: true })

onBeforeUnmount(() => {
  alive = false
  clearTimeout(timer)
})
</script>

<template>
  <section class="mx-auto max-w-7xl space-y-6 p-6">
    <p v-if="!allowed" class="text-gray-700">Run history needs the processor-author role.</p>
    <template v-else>
      <nav class="space-x-4 text-sm">
        <RouterLink to="/processors" class="text-blue-700 hover:underline">← Processors</RouterLink>
        <RouterLink :to="`/processors/${name}/dry-run`" class="text-blue-700 hover:underline">Dry run</RouterLink>
      </nav>
      <header class="flex flex-wrap items-baseline justify-between gap-2">
        <h1 class="text-2xl font-semibold">Runs of <span class="font-mono">{{ name }}</span></h1>
        <span v-if="polling" class="text-xs text-gray-600">Refreshing every {{ POLL_MS / 1000 }} s while runs are queued or running.</span>
      </header>

      <div class="flex flex-wrap gap-4 text-sm">
        <label>
          State
          <select v-model="stateFilter" class="ml-1 rounded border border-gray-300 px-2 py-1">
            <option value="">all</option>
            <option v-for="s in STATES" :key="s" :value="s">{{ s }}</option>
          </select>
        </label>
        <label>
          Workspace
          <select v-model="workspaceFilter" class="ml-1 rounded border border-gray-300 px-2 py-1 font-mono">
            <option value="">all</option>
            <option v-for="w in workspaces" :key="w" :value="w">{{ w.slice(0, 8) }}</option>
          </select>
        </label>
        <button type="button" class="rounded px-3 py-1 text-blue-700 hover:bg-gray-100" @click="refresh">Refresh</button>
      </div>

      <p v-if="error" class="text-red-700">{{ error }}</p>
      <div v-if="workspaceErrors.length > 0" class="text-sm text-amber-800">
        Could not load runs of:
        <ul class="list-disc pl-6 font-mono">
          <li v-for="w in workspaceErrors" :key="w">{{ w }}</li>
        </ul>
      </div>

      <div class="grid gap-6 lg:grid-cols-[3fr_2fr]">
        <div>
          <p v-if="loading" class="text-gray-600">Loading…</p>
          <p v-else-if="shown.length === 0" class="text-gray-600">No runs.</p>
          <table v-else class="w-full text-left text-sm">
            <thead>
              <tr class="border-b">
                <th class="py-2">Queued</th>
                <th>Workspace</th>
                <th>Task</th>
                <th>Reason</th>
                <th>State</th>
                <th>Outcome</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="r in shown"
                :key="r.workspace + '/' + r.id"
                tabindex="0"
                class="cursor-pointer border-b hover:bg-gray-50"
                :class="{ 'bg-blue-50': selected?.id === r.id && selected?.workspace === r.workspace }"
                @click="select(r)"
                @keydown.enter="select(r)"
              >
                <td class="py-2">{{ fmt(r.queued) }}</td>
                <td class="font-mono">{{ r.workspace.slice(0, 8) }}</td>
                <td class="font-mono">{{ r.task.id.slice(0, 8) }}@{{ r.task.version }}</td>
                <td>{{ r.reason }}</td>
                <td><span class="rounded px-1.5 text-xs" :class="stateClass(r.state)">{{ r.state }}</span></td>
                <td>{{ r.outcome ?? '' }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <aside v-if="selected" class="space-y-3 rounded border border-gray-200 p-4 text-sm">
          <h2 class="font-semibold">Run <span class="font-mono">{{ selected.id.slice(0, 8) }}</span></h2>
          <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
            <dt class="text-gray-600">Workspace</dt><dd class="break-all font-mono">{{ selected.workspace }}</dd>
            <dt class="text-gray-600">Task</dt><dd class="break-all font-mono">{{ selected.task.id }}@{{ selected.task.version }}</dd>
            <dt class="text-gray-600">Answer</dt><dd class="break-all font-mono">{{ selected.answer_path }}</dd>
            <dt class="text-gray-600">Image</dt><dd class="break-all font-mono">{{ selected.image }}</dd>
            <dt class="text-gray-600">Reason</dt><dd>{{ selected.reason }}<span v-if="selected.retry_of"> (retry of {{ selected.retry_of.slice(0, 8) }})</span></dd>
            <dt class="text-gray-600">State</dt><dd>{{ selected.state }}<span v-if="selected.outcome">, {{ selected.outcome }}</span></dd>
            <dt v-if="selected.branch" class="text-gray-600">Proposal</dt><dd v-if="selected.branch" class="break-all font-mono">{{ selected.branch }}</dd>
            <dt class="text-gray-600">Queued</dt><dd>{{ fmt(selected.queued) }}</dd>
            <dt class="text-gray-600">Started</dt><dd>{{ fmt(selected.started) }}</dd>
            <dt class="text-gray-600">Finished</dt><dd>{{ fmt(selected.finished) }}</dd>
          </dl>
          <p v-if="selected.error" class="whitespace-pre-wrap rounded bg-red-50 p-2 text-red-900">{{ selected.error }}</p>
          <button
            v-if="selected.state === 'failed'"
            type="button"
            class="rounded bg-blue-600 px-3 py-1 text-white disabled:opacity-50"
            :disabled="retrying"
            @click="retry(selected)"
          >
            {{ retrying ? 'Retrying…' : 'Retry' }}
          </button>
          <p v-if="actionError" class="text-red-700">{{ actionError }}</p>
          <h3 class="font-semibold">Log</h3>
          <p v-if="logLoading" class="text-gray-600">Loading the log…</p>
          <p v-else-if="logError" class="text-red-700">{{ logError }}</p>
          <pre v-else-if="log !== ''" class="max-h-96 overflow-auto whitespace-pre-wrap rounded bg-gray-900 p-3 text-xs text-gray-100">{{ log }}</pre>
          <p v-else class="text-gray-600">(empty log)</p>
        </aside>
      </div>
    </template>
  </section>
</template>
```

- [ ] **Step 2: Register the route**

In `web/src/router/authors.ts`, replace

```ts
  // plan 4d: later tasks insert their routes above this line
```

with

```ts
  {
    path: '/processors/:name/runs',
    name: 'processor-runs',
    component: () => import('../views/processorauthor/RunsView.vue'),
  },
  // plan 4d: later tasks insert their routes above this line
```

- [ ] **Step 3: Type-check**

Run: `cd web && npm run type-check`
Expected: exits 0.

- [ ] **Step 4: Manual pass** (sign in as `$PA`; the setup's two workspaces each have an echo run and a fail run)

8.1 Processors → "Runs" of `echo`. Two rows (one per workspace), state `succeeded`, outcome `proposed`, reason `answer` (or `start-up`); Network shows one `GET /api/workspaces` and one `…/runs` per workspace. No polling note (nothing active).
8.2 Click a row: the side panel shows the record; "Proposal" shows `custos/proposal/1111…/<64 hex>`; the log panel says "(empty log)" (echo writes nothing to stderr).
8.3 Runs of `fail`: two `failed` rows; selecting one shows the red error (`processor fail exited with code 3`) and a log containing `boom`.
8.4 Press Retry (as `$PA`; if plan 4a requires the engineer role for retry, the 403 message appears instead — record which, then repeat as `$AD`). A new row `queued`/`running` appears, the polling note shows, and within ~10 s it turns `failed` with reason `retry` and "retry of …"; the Network tab shows refreshes 5 s apart that stop once nothing is active.
8.5 Filter State = `failed`, then Workspace = the first one: the table narrows accordingly without requests.
8.6 While a retry is running, navigate to Processors: the Network tab shows no further `…/runs` requests afterwards.
8.7 Open `/processors/nonexistent/runs`: "No runs.", no Console error.

- [ ] **Step 5: Commit**

```bash
git add web/src/views/processorauthor/RunsView.vue web/src/router/authors.ts
git commit -m "Add the run history and log view for a processor"
```

---

## Task 9 — `DryRunView`: start a dry run and poll its report

**Files:**
- Create: `web/src/views/processorauthor/DryRunView.vue`
- Modify: `web/src/router/authors.ts`

**Interfaces:**
- Consumes: `listProcessors`, `startDryRun`, `getDryRun`, `errorMessage`, `statusOf` (Task 1).
- Produces: route `processor-dry-run` (`/processors/:name/dry-run`, optional query `?job=<dry-run id>`).

**Polling state machine** (`phase`):

| Phase | Entered when | Behaviour |
| --- | --- | --- |
| `idle` | page opened without `?job`; "Stop watching" | form enabled |
| `starting` | "Start dry run" pressed with a valid image | `POST /api/processors/{name}/dry-run {"image"}`; 202 → `begin(id)` and `router.replace({query: {job: id}})`; error → `idle` with the message (400 not pinned, 404 unknown processor) |
| `polling` | `begin(id)` (also on load with `?job=<id>`) | `GET /api/dry-runs/{id}` immediately, then **2000 ms after each response arrives** (`setTimeout` chain, never overlapping); report shown live |
| `done` | a response with `state` `succeeded` or `failed` (the terminal states; `running` is the only other one) | no more requests |
| `forgotten` | a 404 (jobs live in memory only, ruling 3.10: the server restarted) | no more requests; says so |
| `stalled` | **3 consecutive** failed polls of any other kind (network, 5xx) | no more requests; "Resume polling" → `begin(id)` (resets the count) |

Every stop path — leaving the page (`onBeforeUnmount`), a new `begin`, "Stop watching" — increments a token and clears the timer; a response arriving for an old token is dropped. The image is checked client-side against the same pattern as `runs.pinnedImageRE` (`^[^\s@]+@sha256:[0-9a-f]{64}$`) and prefilled with the processor's current image. The job id lives in the URL so a reload resumes the same job.

- [ ] **Step 1: Create `web/src/views/processorauthor/DryRunView.vue`**

```vue
<script setup lang="ts">
// Dry run (§5.7): run a candidate image over every answer bound to this
// processor and report what it would propose. Nothing is committed.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { errorMessage, getDryRun, listProcessors, startDryRun, statusOf } from '../../api/authors'
import { routeParam, useAuthorRole } from '../../router/authors'
import type { DryRunReport, ProcessorInfo } from '../../types/processorAuthor'

const POLL_MS = 2000
const MAX_POLL_ERRORS = 3
const PINNED_IMAGE_RE = /^[^\s@]+@sha256:[0-9a-f]{64}$/ // internal/runs/dryrun.go pinnedImageRE

type Phase = 'idle' | 'starting' | 'polling' | 'done' | 'forgotten' | 'stalled'

const route = useRoute()
const router = useRouter()
const allowed = useAuthorRole('processor_author')
const name = computed(() => routeParam(route.params.name))

const processor = ref<ProcessorInfo | null>(null)
const processorError = ref('')
const image = ref('')
const jobId = ref('')
const report = ref<DryRunReport | null>(null)
const phase = ref<Phase>('idle')
const startError = ref('')
const pollError = ref('')

const imageError = computed(() => {
  const s = image.value.trim()
  if (s === '' || PINNED_IMAGE_RE.test(s)) return ''
  return 'The image must be pinned by digest, as in registry.example.org/name@sha256:<64 hex digits>.'
})
const busy = computed(() => phase.value === 'starting' || phase.value === 'polling')

let timer: ReturnType<typeof setTimeout> | undefined
let token = 0
let pollErrors = 0

function stopPolling() {
  token++
  clearTimeout(timer)
  timer = undefined
}

function begin(id: string) {
  stopPolling()
  const mine = token
  jobId.value = id
  report.value = null
  pollError.value = ''
  pollErrors = 0
  phase.value = 'polling'
  void poll(id, mine)
}

async function poll(id: string, mine: number) {
  try {
    const r = await getDryRun(id)
    if (mine !== token) return
    pollErrors = 0
    pollError.value = ''
    report.value = r
    if (r.state === 'succeeded' || r.state === 'failed') {
      phase.value = 'done'
      return
    }
    timer = setTimeout(() => void poll(id, mine), POLL_MS)
  } catch (e) {
    if (mine !== token) return
    if (statusOf(e) === 404) {
      phase.value = 'forgotten'
      return
    }
    pollErrors++
    pollError.value = errorMessage(e)
    if (pollErrors >= MAX_POLL_ERRORS) {
      phase.value = 'stalled'
      return
    }
    timer = setTimeout(() => void poll(id, mine), POLL_MS)
  }
}

function stopWatching() {
  stopPolling()
  phase.value = 'idle'
}

async function start() {
  const img = image.value.trim()
  if (img === '' || imageError.value !== '' || busy.value) return
  phase.value = 'starting'
  startError.value = ''
  try {
    const { id } = await startDryRun(name.value, img)
    begin(id)
    await router.replace({ query: { ...route.query, job: id } })
  } catch (e) {
    phase.value = 'idle'
    startError.value = errorMessage(e)
  }
}

async function loadProcessor() {
  processor.value = null
  processorError.value = ''
  try {
    const p = (await listProcessors()).find((x) => x.name === name.value) ?? null
    processor.value = p
    if (p === null) processorError.value = `No processor "${name.value}" is registered on the catalog's main.`
    else if (image.value === '') image.value = p.image
  } catch (e) {
    processorError.value = errorMessage(e)
  }
}

watch([allowed, name], () => {
  stopPolling()
  phase.value = 'idle'
  report.value = null
  jobId.value = ''
  image.value = ''
  startError.value = ''
  if (!allowed.value) return
  void loadProcessor()
  const job = routeParam(route.query.job)
  if (job !== '') begin(job)
}, { immediate: true })

onBeforeUnmount(stopPolling)
</script>

<template>
  <section class="mx-auto max-w-5xl space-y-6 p-6">
    <p v-if="!allowed" class="text-gray-700">Dry runs need the processor-author role.</p>
    <template v-else>
      <nav class="space-x-4 text-sm">
        <RouterLink to="/processors" class="text-blue-700 hover:underline">← Processors</RouterLink>
        <RouterLink :to="`/processors/${name}/runs`" class="text-blue-700 hover:underline">Runs</RouterLink>
      </nav>
      <header class="space-y-1">
        <h1 class="text-2xl font-semibold">Dry run of <span class="font-mono">{{ name }}</span></h1>
        <p class="text-sm text-gray-600">
          Runs a candidate image over every answer bound to this processor, in every workspace, and reports how many
          proposals it would open. Nothing is committed.
        </p>
      </header>

      <p v-if="processorError" class="text-red-700">{{ processorError }}</p>
      <p v-else-if="processor" class="text-sm text-gray-700">
        Registered image: <code class="break-all">{{ processor.image }}</code> ·
        {{ processor.bound_tasks.length }} bound {{ processor.bound_tasks.length === 1 ? 'task' : 'tasks' }}
      </p>

      <form class="space-y-2" @submit.prevent="start">
        <label class="block text-sm font-semibold">
          Candidate image
          <input
            v-model="image"
            type="text"
            class="mt-1 w-full rounded border border-gray-300 px-3 py-2 font-mono text-sm font-normal"
            :disabled="busy"
          />
        </label>
        <p v-if="imageError" class="text-sm text-red-700">{{ imageError }}</p>
        <div class="flex gap-2">
          <button
            type="submit"
            class="rounded bg-blue-600 px-4 py-2 text-white disabled:opacity-50"
            :disabled="busy || image.trim() === '' || imageError !== ''"
          >
            {{ phase === 'starting' ? 'Starting…' : 'Start dry run' }}
          </button>
          <button
            v-if="phase === 'polling'"
            type="button"
            class="rounded px-4 py-2 text-gray-700 hover:bg-gray-100"
            @click="stopWatching"
          >
            Stop watching
          </button>
        </div>
        <p v-if="startError" class="text-red-700">{{ startError }}</p>
      </form>

      <section v-if="jobId !== ''" class="space-y-3 border-t pt-4">
        <h2 class="font-semibold">Job <span class="font-mono">{{ jobId.slice(0, 8) }}</span></h2>
        <p v-if="phase === 'forgotten'" class="text-amber-800">
          The server no longer knows this dry run; dry runs are kept in memory only and are lost when the server
          restarts. Start it again.
        </p>
        <p v-if="phase === 'stalled'" class="text-red-700">
          Polling stopped after {{ MAX_POLL_ERRORS }} failed attempts: {{ pollError }}
          <button type="button" class="ml-2 text-blue-700 hover:underline" @click="begin(jobId)">Resume polling</button>
        </p>
        <p v-else-if="pollError" class="text-sm text-amber-800">Retrying after an error: {{ pollError }}</p>

        <template v-if="report">
          <p>
            <span
              class="rounded px-1.5 text-xs"
              :class="report.state === 'running' ? 'bg-blue-100 text-blue-900' : report.state === 'succeeded' ? 'bg-green-100 text-green-900' : 'bg-red-100 text-red-900'"
            >{{ report.state }}</span>
            <span v-if="phase === 'polling'" class="ml-2 text-xs text-gray-600">refreshing every {{ POLL_MS / 1000 }} s</span>
          </p>
          <p v-if="report.error" class="whitespace-pre-wrap rounded bg-red-50 p-2 text-red-900">{{ report.error }}</p>
          <p>
            Ran {{ report.runs }} bound {{ report.runs === 1 ? 'answer' : 'answers' }} ({{ report.failed }} failed); would open
            {{ report.proposals }} {{ report.proposals === 1 ? 'proposal' : 'proposals' }} in {{ report.workspaces }}
            {{ report.workspaces === 1 ? 'workspace' : 'workspaces' }}.
          </p>
          <div v-if="report.samples.length > 0" class="space-y-3">
            <h3 class="font-semibold">Samples</h3>
            <article v-for="s in report.samples" :key="s.workspace + '/' + s.task" class="rounded border border-gray-200 p-3 text-sm">
              <p class="mb-2 text-gray-700">
                Workspace <span class="font-mono">{{ s.workspace.slice(0, 8) }}</span>, task
                <span class="font-mono">{{ s.task.slice(0, 8) }}</span>
              </p>
              <table class="w-full text-left">
                <thead>
                  <tr class="border-b">
                    <th class="py-1">Kind</th>
                    <th>Change</th>
                    <th>Title</th>
                    <th>Versions</th>
                    <th>Match key</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="it in s.items" :key="it.kind + ':' + it.match_key" class="border-b">
                    <td class="py-1">{{ it.kind }}</td>
                    <td>{{ it.action }}</td>
                    <td>{{ it.title }}</td>
                    <td class="font-mono">{{ it.from ?? '' }}{{ it.from && it.to ? ' → ' : '' }}{{ it.to ?? '' }}</td>
                    <td class="font-mono">{{ it.match_key }}</td>
                  </tr>
                </tbody>
              </table>
            </article>
          </div>
        </template>
        <p v-else-if="phase === 'polling'" class="text-gray-600">Waiting for the first report…</p>
      </section>
    </template>
  </section>
</template>
```

- [ ] **Step 2: Register the route**

In `web/src/router/authors.ts`, replace

```ts
  // plan 4d: later tasks insert their routes above this line
```

with

```ts
  {
    path: '/processors/:name/dry-run',
    name: 'processor-dry-run',
    component: () => import('../views/processorauthor/DryRunView.vue'),
  },
  // plan 4d: later tasks insert their routes above this line
```

- [ ] **Step 3: Type-check**

Run: `cd web && npm run type-check`
Expected: exits 0.

- [ ] **Step 4: Manual pass** (sign in as `$PA`)

9.1 Processors → "Dry run" of `echo`. The registered image and "1 bound task" show; the input is prefilled with `$IMG_ECHO`.
9.2 Change the image to `custos.manual/echo:latest`: the pin error shows and Start is disabled.
9.3 Restore `$IMG_ECHO`, Start. One `POST …/dry-run` (202), the URL gains `?job=<id>`, then `GET /api/dry-runs/<id>` requests 2 s apart (check the Network timing column) until `succeeded`: "Ran 2 bound answers (0 failed); would open 0 proposals in 0 workspaces." (the same image already produced these proposals — record the actual numbers), then **no further requests**.
9.4 Start with `$IMG_FAIL` as the candidate image for `echo`: it ends `succeeded` with "Ran 2 … (2 failed)" (individual run failures do not fail the dry run — 3d's decisions).
9.5 Start another dry run and, while it is `running`, click "← Processors": no `dry-runs` request appears afterwards.
9.6 Press Back: the page reloads with `?job=<id>` and resumes polling the same job (same id in the requests), ending in `done`.
9.7 With a finished job in the URL, restart `custos serve` and reload the page: one 404 and the "no longer knows this dry run" message; no further requests.
9.8 Stop `custos serve` entirely, Start (fails: message, phase back to idle). Restart the server, Start a dry run, then stop the server while it is running: three failed polls 2 s apart, then the "Polling stopped after 3 failed attempts" message. Restart the server and press "Resume polling": a 404 → "no longer knows" (the restart forgot it).
9.9 Open `/processors/nope/dry-run`: "No processor "nope" is registered…"; pressing Start with any pinned image shows the server's 404 message.

- [ ] **Step 5: Final checks for the whole plan**

Run: `make test` (runs `npm ci && npm run type-check` before the Go tests per plan 4c's Makefile).
Expected: passes; `git status` shows no change to `web/package.json` or `web/package-lock.json` and no new file outside the paths in "File Structure".

- [ ] **Step 6: Commit**

```bash
git add web/src/views/processorauthor/DryRunView.vue web/src/router/authors.ts
git commit -m "Add the dry-run view that polls a dry run until it finishes"
```

---

## Self-Review Notes

- **Spec coverage:**
  - §6.1 Task-Author: group tree with drag-to-reorder — Task 4; task editor with new version (copies the current version, asks for the semver step, sets `previous` — server side via 4b's `NewVersionChanges`) — Task 5; merge tool — Tasks 2–3; version-graph view — Task 6; edits go to a draft branch, publish validates and merges — Task 3.
  - §6.1 Processor-Author: registry list — Task 7; run history and logs — Task 8; dry run — Task 9.
  - §5.7 dry run "reports how many proposals in how many workspaces it would create, with samples. Nothing is committed" — Task 9's report section.
  - §6.4 roles: `task-author`, `processor-author`, admin implies both — Task 1 (`useAuthorRole`, nav).
  - §7 "invalid catalog changes are rejected at publish … naming the file and the violated rule" — `ProblemList` of 422 problems in Task 3; failed runs shown with logs and retried — Task 8.
  - §8 "frontend type checks" — every task's type-check step; no new Go tests (no Go code).
- **Placeholder scan:** none; every new file is given in full. The two edits to plan 4c's files are anchored insertions because their exact text belongs to 4c.
- **Type consistency:** every type imported by a view is defined once in `types/taskAuthor.ts` or `types/processorAuthor.ts` (Task 1); every endpoint function once in `api/authors.ts` (Task 1); `routeParam`/`useAuthorRole` once in `router/authors.ts`; `lineDiff`/`LineDiff` once (Task 2); `bumpVersion`/`compareVersions` once (Task 5).
- **What was not verified:** this plan was written without plans 4a–4c's code; the "Assumed surface" table lists everything taken on trust. Nothing here was run.

## Decisions beyond the architecture note

Each line: decision — reason — cost if wrong.

- **One endpoint module, `web/src/api/authors.ts`**, is the only file importing plan 4c's client. **Resolved by cross-plan review:** 4c's `client.ts` actually exports an `api` object (`api.get/post/put/del`), not the standalone `request<T>(method, path, body?)` function this plan originally assumed; `authors.ts`'s calls have been rewritten accordingly (Task 1, Step 3) — exactly the five-line adaptation this decision's own reasoning anticipated.
- **`getRunLog` calls `fetch` directly** (same-origin, `credentials: 'include'`). **Resolved by cross-plan review:** 4c's `client.ts` does authenticate the SPA by an `Authorization: Bearer` header in token-only mode (`api/token.ts`'s `getToken()`), so `getRunLog` now attaches the same header itself; without this it would 401 under a pasted token exactly as this decision's "cost if wrong" predicted.
- **Assumed 4b response shapes** (see "Assumed surface"): `Draft` in snake_case lower-case JSON with `author: {name, email}`, and `GET /api/catalog/drafts/{id}` = `{draft, catalog: <GET /api/catalog shape>, problems}`. **Confirmed correct by cross-plan review** against 4b's actual `internal/catalogdraft/http.go` (`draftJSON`, `draftViewJSON`): the guess matches byte-for-byte, including that `catalog` is the same shape `GET /api/catalog` returns (4b exports `api.CatalogJSON` for exactly this reuse). No change needed to `DraftDetail` or the views reading `detail.catalog`.
- **`/catalog/drafts/:draftId` renders `DraftsView`** (list plus the selected draft's detail, problems, tasks and publish) — the note lists the route but no view for it; a separate DraftView would duplicate the list — the list is always visible above the detail.
- **`:slug` = `-` means "no group selected"** in `GroupTreeView` — `DraftsView` must link to the group editor even when the catalog has no group, and the note's route requires a slug; `-` can never be a slug (`slugRE` requires `[a-z0-9]` first) — none, beyond an odd-looking URL.
- **`GroupTreeView` edits both the top-level order (`groups-index`) and one group** (`:slug`) on one page — the note says the view calls both endpoints and gives it one route — the page has two save buttons.
- **Group edits are explicit saves, one PUT per save** (no save-on-drop) — every PUT is a draft commit (4b `Update`); a save per drop would make dozens of commits — the user must remember to save; leaving with unsaved edits asks for confirmation.
- **Group-tree feature scope:** reorder, rename, remove a child, add a task that is in no group, create a top-level group (two PUTs: group, then index — a draft may be transiently invalid, ruling 4.13, so a failure between them only leaves an unreachable group that `Check` reports). Not included: creating sub-groups, deleting group files, dragging between lists — keeps one sitting's scope; §6.1 asks only for drag-to-reorder — those edits need Git on the draft branch.
- **Native HTML5 drag-and-drop with exact handlers, plus ↑/↓ buttons** in a generic `ReorderList.vue` placed in `views/taskauthor/` (not `components/`, since only 4d uses it) — ruling 4.16; buttons make reordering keyboard-accessible; native DnD does not work on touch screens — touch users use the buttons.
- **`ReorderList` uses Vue's `generic` script attribute** (Vue ≥ 3.3) — typed slots for two item types without `any` — if 4c pinned Vue < 3.3 the component needs `items: unknown[]` and casts.
- **Line diff is this plan's own 40-line LCS** (`lineDiff.ts`, capped at 4 M cells, above which it shows a full replace) and `LineDiff.vue`, reused by the merge tool and the task editor — no npm diff package (ruling 4.16) — plan 4c's `AnswerView` "task-version diff" may have its own; the reviewer may merge them.
- **The task editor shows both a line diff and two `MarkdownView` renderings** — the note says "diff … shown with MarkdownView"; a rendered diff of Markdown is not meaningful, so the textual diff shows *what* changed and the renderings show *how it reads* — two panels instead of one.
- **The next version is previewed client-side** (`bumpVersion`, a mirror of `semver.Bump` including dropping a pre-release suffix) — the PUT does not return the new version in any shape the note fixes — a divergence would only mislabel the preview; the server's file is authoritative and the view re-reads the draft after saving.
- **`choices` is omitted from the PUT body unless the answer type is `choice`** — `task.CheckMeta` rejects choices on other types — none.
- **`MergeToolView` is a component rendered inline by `DraftsView`, with the props/emits contract in Task 2**; "edit" maps to merge's side `content` (base64 of UTF-8 text), prefilled with the draft's (theirs) text — the note's "ours/theirs/edit" vs merge's `ours/theirs/content` — binary files would be mangled by the UTF-8 round trip; catalog files are all text.
- **Resolutions accumulate across 409 rounds** (each request re-sends earlier choices) and are **discarded on a 400 or a 409 without conflicts** — `merge.Merge` reports only paths still lacking a resolution and rejects a resolution for a path with no conflict; 4b's `Publish` is "identical in method" — if 4b's `Publish` differed (e.g. tolerated stale resolutions), the 400 branch would simply never trigger.
- **`VersionGraphView` is a component embedded in `TaskEditorView`**, not routed — the note's router list gives it no route — the history is only visible from a draft's task editor.
- **Version history = main's history + the draft's current version.** Intermediate versions created earlier in the same draft are not listed (only referenced, "not published; an earlier version in this draft") — the draft endpoint (as assumed) only carries current versions and there is no per-task draft history endpoint — **open question for the reviewer:** 4b could add all versions to the draft response; `VersionGraphView` would then take a `versions` list instead of `draftVersion`.
- **Generation = longest path from a root** (unknown or cyclic references count as nothing; a draft-only version with a missing same-task predecessor goes after the highest published generation) — gives a stable "list of generations" for merges (§3.1) without layout — siblings from a fork (rule 2 violation in a draft) appear in the same generation.
- **`RunsView` aggregates runs across workspaces client-side** (`GET /api/workspaces`, then one `…/runs` per workspace in parallel, filtered by `record.processor`) — 3d has no per-processor run endpoint and this plan adds no Go code — N+1 requests every 5 s while runs are active; fine at §1's scale (tens of workspaces).
- **Polling intervals:** runs 5000 ms while any shown run is `queued`/`running`; dry runs 2000 ms until `succeeded`/`failed`; both by chained `setTimeout`, stopped on unmount; dry-run polling also stops on 404 ("forgotten", ruling 3.10) and after 3 consecutive errors ("Resume polling") — a dry run's progress is what the user is watching, runs are background — up to 2 s / 5 s of staleness.
- **The dry-run job id is kept in the URL (`?job=`)** — reload and Back resume the same job — the URL can outlive the in-memory job; that case is shown as "forgotten".
- **The candidate image is validated client-side** with `runs.pinnedImageRE`'s pattern and prefilled with the registered image — immediate feedback; the server re-checks — if the server pattern changes the client is stricter or looser until updated (the server's 400 is still shown).
- **Retry is offered in `RunsView` for failed runs** — §7 "failed runs … can be retried"; 3d's endpoint exists — plan 4a may gate it by workspace engineer role; the 403 message is then shown to a processor-author.
- **Role gating is per view (`useAuthorRole`, admin implies the role) and per nav entry (`RoleGate`)**, not a route guard — the note's router guard handles only "signed in?"; a view-level check renders a clear message and makes no request (no 403 noise) — the role logic exists twice (RoleGate in 4c, `useAuthorRole` here); they must agree on "admin implies".
- **Routes carry `meta: { requiresAuth: true }`** — assumed to be 4c's guard key. **Resolved by cross-plan review:** 4c's actual router guard (`router/index.ts`) works the other way around — it checks `to.meta.public` as an *opt-out* of a default that already requires auth (`if (to.meta.public) return true; if (auth.mode === 'authenticated') return true; ...`), and has no `requiresAuth` key at all. A route with no `meta` already requires auth under 4c's guard, so `meta: { requiresAuth: true }` was inert (harmless, but documented a false assumption); it has been removed from every route in `authorRoutes` (Task 1, Step 4, and the route-adding steps of Tasks 3, 6 and 8) rather than left as dead, misleading configuration.
- **Route names** `catalog-drafts`, `catalog-draft`, `catalog-draft-group`, `catalog-draft-task`, `processors`, `processor-runs`, `processor-dry-run`; links between views use path strings — names are needed for `router.push` within `DraftsView`; path-string links let earlier tasks link to routes later tasks add without `router.resolve` throwing — none.
- **Type files are named `taskAuthor.ts` and `processorAuthor.ts`** — avoids colliding with whatever names 4c uses (`catalog.ts`, `proposals.ts`, …) when the plans are applied together — `ProposalItem` may duplicate 4c's mirror of `runs.ItemJSON` (used by its `ProposalsView`); the reviewer may dedupe.
- **No UI for editing `processors.yaml`** (`PUT /api/catalog/drafts/{id}/processors`) is a **deliberate, decided gap, not an open question.** Spec §6.1's Processor-Author bullet (and §5.7, which it mirrors) lists only "registry list, run history and logs, dry run" — no registry editor. No plan in 4a-4d assigns that endpoint a view: 4b exposes it (correctly task-author-gated, since binding a task to a processor is a catalog-content edit, not a processor-author action), but the only UI this phase builds for processors is `RegistryView`'s read-only `GET /api/processors` list. Confirmed during cross-plan review by reading all four plans together: this is phase 4's explicit scope boundary, not a dropped ball — changing a digest or a task/processor binding still works today, through Git on a draft branch (or a plain commit to `catalog.git`), exactly as before this phase existed.

## Rulings for spec §11.4

Rulings this plan makes (numbers assigned by the phase-4 controller when recording them):

1. (4d) The Task-Author and Processor-Author views add no npm dependency at all — drag-and-drop is the native HTML5 API (with ↑/↓ buttons for keyboard use), the version graph is an ordered list of generations, the diff is an in-repo LCS line diff.
2. (4d) Group-tree and task edits are saved explicitly; each save is exactly one draft commit. Leaving with unsaved edits asks for confirmation.
3. (4d) A task's version history shows `main`'s versions plus the draft's current version, grouped by generation = longest `previous` path from a root; intermediate versions made earlier in the same draft are referenced but not listed.
4. (4d) The merge tool re-sends every earlier resolution with each publish attempt and discards them all when the server answers 400 (a resolved path no longer conflicts) or a 409 without conflicts.
5. (4d) Dry-run reports are polled every 2 s until `succeeded` or `failed`; a 404 means the job was forgotten by a restart (ruling 3.10) and stops polling; three consecutive errors stop polling until the user resumes. The job id is kept in the page URL.
6. (4d) A processor's run history is assembled in the browser from every workspace's run list (no per-processor endpoint), refreshed every 5 s while any of its runs is queued or running.
7. (4d) Author views check the role themselves (admin implies it) and render a message instead of calling the API when it is missing; the navigation hides sections the principal cannot use.
8. (4d) `api/authors.ts` calls plan 4c's client through `api.get/post/put/del`, not a standalone `request()` function; `getRunLog` attaches the same `Authorization: Bearer` header `client.ts` uses in token-only mode. Both fixed during cross-plan review against 4c's actual code.
9. (4d) `RoleGate`'s `role` prop and the nav links in `App.vue` use the hyphenated role strings (`task-author`, `processor-author`) that mirror `internal/auth`'s own role names, not the `/auth/me` JSON field names; `useAuthorRole` (a separate, view-level check) is unaffected, since it reads the JSON field names directly. Fixed during cross-plan review against 4c's actual `RoleGate.vue`.
10. (4d) `authorRoutes`' routes carry no router `meta` at all; 4c's guard already requires auth by default and opts a route out via `meta: { public: true }`, so the `meta: { requiresAuth: true }` this plan originally wrote was inert and has been removed. Fixed during cross-plan review against 4c's actual `router/index.ts`.
11. (4d) Editing `processors.yaml` through the web UI is out of scope for phase 4, confirmed against spec §6.1/§5.7's Processor-Author bullet (registry list, run history/logs, dry run — no registry editor); `PUT /api/catalog/drafts/{id}/processors` remains reachable only through Git on a draft branch.
