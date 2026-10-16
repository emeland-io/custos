# custos delivery phases

Status of the custos delivery phases (spec §9) and where their plans and rulings live. This file is updated whenever a phase or plan is completed.

- Spec: [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md)
- Rulings: spec §11 — §11.1 phase 1, §11.2 phase 2, and so on. Rulings made while a phase is executed are added to its subsection before its branch is finished.

## Overview

| Phase | Content | Status |
| --- | --- | --- |
| 1 Core | Catalog and workspace formats, validation, Git over HTTP with a validating pre-receive hook, CLI `validate`, `task new-version`, `workspace create`, `serve`, container image | Done, merged to `main` on 2026-10-03 (`d0af8ad`) |
| 2 Flows | Catalog updates reaching workspaces, workspace freeze and catalog-update proposals, answering, attachments, REST API, `custos clone`/`push`, workspace fork and merge | Done, merged to `main` on 2026-10-04 (`43ad0c9`) |
| 3 Processors | Processor registry and runner (containers), matching generated tasks across reruns, proposals for review, cascades and depth limit, signature checks, Go/Python SDKs, `processor test`, dry runs. Replaces ruling 2.10: merge conflicts in generated output get a processor rerun instead of a pick-one-side choice. | Done, not yet merged to `main` |
| 4 UI and authentication | Web UI for engineers, task authors and processor authors; HTML book; catalog editing through the API; OIDC login, roles and tokens. Replaces the `X-Custos-Author` header (ruling 2.6) and adds access control for the shared attachment store (ruling 2.7). | Done, not yet merged to `main` |

## Phase 1 — Core

- Plan: [2026-10-02-phase-1-core.md](2026-10-02-phase-1-core.md), 14 tasks.
- Executed with subagent-driven development: one implementer and one reviewer per task, then a whole-branch review on the most capable model.
- The final review found that a pushed `.gitattributes` file could hide files from the pre-receive hook. The hook now reads raw blobs (ruling 1.6); the fix wave also covered seven smaller findings.
- Rulings: spec §11.1 (1.1–1.11).

## Phase 2 — Flows

The plans run in order, and each needs the ones before it.

| Plan | Content | Tasks | Status |
| --- | --- | --- | --- |
| [2a](2026-10-03-phase-2a-store.md) | Server-side writes to the repositories, pushes checked against the pinned catalog, status and book | 10 | Done |
| [2b](2026-10-03-phase-2b-api.md) | Attachment store, REST API for workspaces, answers, attachments and the catalog, plus `custos clone`/`push` | 12 | Done |
| [2c](2026-10-03-phase-2c-distribution.md) | Catalog updates reaching workspaces, freeze, catalog-update proposals with accept/reject | 8 | Done |
| [2d](2026-10-03-phase-2d-fork-merge.md) | Workspace fork and merge with conflict resolution | 6 + 1 added | Done |

How the plans were made: four subagents wrote them in parallel from a shared architecture note, [2026-10-03-phase-2-architecture.md](2026-10-03-phase-2-architecture.md). A fifth reviewed them as a set. It found that the plans wired the API into `serve` incompatibly, that one code block didn't compile, and that the README steps overwrote each other, and it fixed all of these in the plan text. It then applied all four plans in order to a scratch copy of the repository; the result passes `gofmt`, `go vet` and `go test -race`.

Executed with subagent-driven development on branch `phase-2`: one implementer and one reviewer per task, then a whole-plan review on the most capable model after each plan, a single fix wave for its findings, and a scoped re-review of that wave.

What the whole-plan reviews found and what was fixed:

- **2a:** a tag named `refs/tags/refs/heads/main` could stand in for a missing `main`, so a workspace could be pinned to an unvalidated catalog commit (ruling 2.42); catalog pushes to `/git/catalog` without `.git` skipped distribution (2.45). Five smaller fixes in the same wave.
- **2b:** answer text whose first line starts with a tab was refused with a YAML error (2.48); `custos push --all`/`--tags`/`--mirror` could leave attachments unuploaded for good (2.50). Four smaller fixes.
- **2c:** distribution ignored `custos.yaml` changes made by push and never retried a lost race (2.53); freeze and unfreeze reported failure for a change that had been made (2.54).
- **2d:** an added task (5b) makes merges and forks through the API trigger distribution too (2.53). The review found only minor issues; a fork no longer deletes a push that won its race (2.46), and choosing a side for a `custos.yaml` conflict keeps `main`'s id (2.56).

Rulings: spec §11.2 holds 41 rulings made while planning (2.1–2.41) and 15 made during execution (2.42–2.56). These six change the spec and deserve a look first:

| Ruling | What changes |
| --- | --- |
| 2.2 | No SQLite index. Status and the book are computed from Git on each request. |
| 2.6 | Until login exists in phase 4, write requests name their author in an `X-Custos-Author` header. The API is unauthenticated, like Git over HTTP. |
| 2.7 | One shared attachment store for all workspaces instead of one per workspace, so forks copy nothing. |
| 2.9 | The server needs Git 2.38 or newer, because merges run without a working copy. |
| 2.11 | The book is exported as Markdown only; HTML export comes with the web UI. |
| 2.12 | The catalog API is read-only for now; editing through the API comes with the web UI. |

## Phase 3 — Processors

The plans run in order, and each needs the ones before it.

| Plan | Content | Tasks | Status |
| --- | --- | --- | --- |
| [3a](2026-10-04-phase-3a-runner.md) | Processor contract (input and output JSON), container runner through the Docker or Podman CLI, and the test images echo, generate, sign, fail and loop | 5 | Done |
| [3b](2026-10-04-phase-3b-attest.md) | Verifying documents: DSSE envelopes, Sigstore bundles and bare statements, with the carabiner-dev libraries (ADR 0001) | 4 | Done |
| [3c](2026-10-04-phase-3c-proposals.md) | Matching output against earlier output, the depth limit, proposal branches, accepting all or some items, rejecting, cascading removal | 5 | Done |
| [3d](2026-10-04-phase-3d-runs.md) | Run records, queue and workers, finding the runs needed after answers, pushes, pin moves and merges; REST endpoints, `serve` flags, merge reruns (replacing ruling 2.10), dry runs | 8 | Done |
| [3e](2026-10-04-phase-3e-sdk.md) | Go and Python SDKs, `custos processor test` with golden files, Makefile and README | 6 | Done |

How the plans were made: the controller wrote a shared architecture note, [2026-10-04-phase-3-architecture.md](2026-10-04-phase-3-architecture.md), and five subagents wrote the plans from it in parallel. A sixth reviewed them as a set and applied all five in order to a scratch copy of the repository; with real Docker, `gofmt`, `go vet`, `go test` after each plan, `go test -race` on the concurrent packages and `make test` with the SDK tests all passed. It fixed plan text where the plans did not fit together: the accept endpoint now reports a commit that landed together with a warning, a test runs the real `sign` image against the verifier, and the plans no longer edit the spec themselves.

Choices the project owner made for this phase: plans written like phase 2, run records as files in the data directory, and tests always against real Docker.

Executed with subagent-driven development on branch `worktree-phase-3`: one implementer and one reviewer per task, then a whole-plan review on the most capable model after each plan, fix waves for its findings, and scoped re-reviews of each wave.

**What the whole-plan reviews found and fixed, by plan:**

- **3a (contract, runner, test images):** one fix wave after the final review — test-image builds embedded VCS metadata, so every commit produced a new image ID and risked a cold-cache race once later plans added more test packages (fixed with `-buildvcs=false`); a container still `Running` after its client exited was misreported as a normal finish; added a DSSE spec-vector regression test.
- **3b (attestation verification):** a **critical, confirmed signature-verification bypass** — a correctly-signed document could be reported `verified` while its payload was silently swapped for a forged statement, via a JSON key trick exploiting a mismatch between custos's own envelope parser and the carabiner-dev signer library's parser. It took two fix rounds: round 1 closed the exact reported exploit but opened a variant of the same bug; round 2 redesigned the verifier to parse content exactly once through the library itself, removing the second parser entirely (ruling 3.82). The final design was adversarially tested with roughly 20,000 constructed attack documents and confirmed clean.
- **3c (matching and output proposals):** **three more critical concurrency bugs**, all the same class as each other — a compound git-write sequence (delete-then-write, or commit-then-close) split across two separate lock acquisitions, letting a concurrent caller act in the gap. Found and fixed in `proposal.Write` (two open proposals for one task could coexist), in `proposal.Accept` (two concurrent accepts, or an accept racing a reject, could both land), and — found only by the *final* whole-plan review, after both of those were already fixed — in accepted removals and the cascades they should open (an accepted removal and the removed task's own in-flight accept could land content under an already-removed task that no cascade would ever clean up). Each was reproduced empirically before its fix and reproduced clean afterward, with increasing rigor each time (the last fix was checked against roughly 280 adversarial test iterations across four attack shapes).
- **3d (runs, triggers, REST, `serve`, merge reruns, dry run):** the queue built in this plan's largest task repeated the same bug class **twice more** — a stale queue-time key could permanently suppress a rerun on an answer or pin revert, and a missing re-check before writing a proposal let an older, slower run permanently overwrite a newer run's output (both fixed with a proper refcounted key scheme and per-answer-path execution serialization). The whole-plan review then found that `serve`'s graceful shutdown waited on the wrong condition — a full queue drain instead of just in-flight work — so an ordinary queue backlog made every real shutdown wait the full timeout and log a false warning (ruling 3.86); it also closed a test-coverage gap on the accept-with-warning contract that had been carried over, untested, from plan 3c.
- **3e (SDKs, `custos processor test`, README):** no correctness issues of comparable severity — this plan's defining achievement is independently, repeatedly verified byte-exact agreement across three implementations (the Go SDK, the Python SDK, and the server's own `contract.ParseOutput`) on a shared 38-case validation fixture, and byte-identical output from the Go and Python `hostlist` example processors on the same input, confirmed via direct `cmp`/SHA-256 comparison more than once.

**Running total for the phase: five confirmed critical/important concurrency or correctness bugs, every one found through adversarial empirical testing (not code reading alone) and every fix independently re-verified the same way**, across plans 3b through 3d.

Rulings: spec §11.3 holds 80 rulings made while planning (3.1–3.80; 3.1–3.11 come from the architecture note) plus 6 made during execution (3.81–3.86). These change the spec or the way custos is run and deserve a look first:

| Ruling | What changes |
| --- | --- |
| 3.2 | Run records are files under `<data-dir>/runs/`, not Git and not an index. |
| 3.3 | Runs are found by a key (workspace, answer path, answer blob, processor digest), so a rebinding to an image that already ran on an answer does not rerun. |
| 3.4 | Processors run through the `docker`/`podman` CLI; the test suite needs Docker. |
| 3.7 | Replaces 2.10: merge conflicts in generated output take `main`'s side and trigger a rerun. |
| 3.8 | Removing a generated task removes its answer from `main`; cascades are separate proposals. |
| 3.80 | `make test` needs Docker and python3. |
| 3.82 | The verified payload for a `verified` attestation result must come only from the artifact the signer library itself parsed and checked, never from a second independent parse — closes the critical signature bypass above. |
| 3.84 | Accepting a proposal that was replaced while the call was in flight returns 409, not a silent retry-and-apply under the caller's stale selection; narrows 3.40. |
| 3.86 | `serve`'s shutdown waits only for in-flight runs, not the whole queue, before exiting. |

Also note: the container image gets no Docker client in phase 3, so processors run only when `custos serve` runs on a host with Docker or Podman (ruling 3.79). A document signed with a key custos does not trust shows as `failed` on the server, but as `unsigned` in `custos processor test` without `--trusted-keys`.

## Phase 4 — UI and authentication

The plans run in order, and each needs the ones before it.

| Plan | Content | Tasks | Status |
| --- | --- | --- | --- |
| [4a](2026-10-09-phase-4a-auth.md) | Authentication, roles, personal access tokens, OIDC login | 9 | Done |
| [4b](2026-10-09-phase-4b-catalog-editing.md) | Catalog editing API (drafts, publish, conflict resolution) and the book's HTML export | 8 | Done |
| [4c](2026-10-09-phase-4c-frontend-engineer.md) | Frontend scaffold (`internal/webui`, the Vue app shell) and the Engineer's views | 12 | Done |
| [4d](2026-10-09-phase-4d-frontend-authors.md) | Task-Author and Processor-Author views | 9 | Done |

How the plans were made: a shared architecture note,
[2026-10-09-phase-4-architecture.md](2026-10-09-phase-4-architecture.md),
fixed the packages, exported names and conventions the four plans share,
then four subagents wrote the plans from it in parallel. A cross-plan
reviewer applied all four, in order, to a scratch copy of the repository
before any plan was executed, and found two near-miss bugs: the auth
middleware's `excludedPrefixes` treated a bare `"/"` as a prefix match
(every path starts with `/`, and `"/"` itself ends in `/`), which bypassed
credential resolution for every single request to every path — exactly
the value `cmd/custos/serve.go` passes in production, so authentication
would have been a no-op for a real `custos serve`; and the webui package's
SPA-shell fallback routed a crafted `/index.html` request through
`http.FileServer`, whose own built-in redirect for any path ending in
`/index.html` fired on every client-side route, so the embedded
single-page application would never actually have loaded. Both were fixed
in the plan text before execution started. The same review added two
rulings to the architecture note's list (4.17, 4.18): a
`catalogdraft.Register` `onPublish` callback so a draft publish reconciles
pinned workspaces immediately instead of waiting for an unrelated future
catalog push, and `GET /auth/config` as the signal the frontend uses to
tell token-only mode apart from "sign in please."

Executed with subagent-driven development on branch
`worktree-worktree-phase-4`: one implementer and one reviewer per task,
then a whole-plan review on the most capable model after each plan, fix
waves for its findings and scoped re-reviews of each wave; finished with a
final whole-branch review and one more fix wave for its findings (this
one).

**What each plan's own task- and whole-plan reviews found and fixed:**

- **4a (authentication, roles, tokens):** an open redirect in `return_to`
  validation, reproduced byte-for-byte from the plan's own listing (fixed
  by rejecting any `return_to` containing a backslash in addition to the
  existing checks, ruling 4.19); `handlePutRoles` hardcoded 400 for every
  `UpdateUser` error instead of using `httpStatusForAuthErr` the way
  `handleCreateToken` correctly does (ruling 4.20). The whole-plan review
  then found a **Critical** bug that would have stopped `custos serve`
  from starting at all: `authFlagSet.config()` never set
  `auth.Config.DataDir`, so `auth.NewManager` always failed "no data
  directory" on real CLI startup — masked by every existing test
  hand-building `auth.Config` directly instead of going through the real
  CLI path. One fix wave, all green.
- **4b (catalog editing API and book HTML export):** Task 5's review
  found a **Critical** fast-forward immutability bypass — `allowedPaths`
  was built from a publish request's raw keys before `Publish` knew
  whether it was taking the fast-forward or the merge path, and was passed
  unconditionally into `UpdateCatalog`'s immutability exemption; on the
  fast-forward path, the validation that a key actually names a real
  conflict never ran, so a caller could rewrite an already-published task
  version just by naming its path in the request with any resolution,
  even though nothing had conflicted. Confirmed exploitable with a
  throwaway test before the fix (ruling 4.22 records the exemption
  mechanism that bug lived in). The whole-plan review was otherwise clean,
  verified end-to-end against a real binary and server (draft lifecycle,
  two-drafts conflict, auth matrix, book export).
- **4c (frontend scaffold and Engineer views):** three in-task fixes.
  Task 2: a direct `GET /index.html` request still routed through
  `http.FileServer`, which 301-redirects any path ending in
  `/index.html` — the original fix only covered the SPA-fallback case
  (synthesizing `index.html` for an unknown route), not a direct hit on
  the real file. Task 5: `markdown.ts`'s `inline()` ran its
  backtick/bold/italic/link regexes sequentially over the same string, so
  a tag inserted by an earlier pass could be swallowed or re-processed by
  a later one (e.g. a code span containing a link) — a real
  rendering-correctness bug, confirmed not an XSS escape, fixed by
  tokenizing instead of chaining global regex replacements. Task 12:
  `BookView.vue`'s plain `<a href>`/`<iframe :src>` to the book endpoint
  made passive browser loads that cannot carry the `Authorization:
  Bearer` header token-only mode needs — it worked under an OIDC
  session's ambient cookie but 401'd under a pasted token; fixed with the
  same manual-`fetch`-plus-Blob-URL pattern sibling plan 4d had already
  applied to `getRunLog`. The whole-plan review was otherwise clean: a
  full real end-to-end HTTP-level exercise (`make build`, a real binary,
  roughly 30 curl requests reproducing the exact SPA JS sequence) with
  plans 4a/4b/4c all wired together, with the explicit, named gap that no
  browser automation tool was available in that environment, so no actual
  rendered-DOM/click/OIDC-redirect pass had ever been observed — flagged
  for a human click-through before shipping (the final whole-branch
  review's Critical finding below was found exactly that way, with a real
  headless browser).
- **4d (Task-Author and Processor-Author views):** all 9 tasks reviewed
  clean on the first pass, no fix rounds. The whole-plan review was a full
  real end-to-end exercise (`make build`, a real binary and server,
  roughly 30 curl requests) covering all 5 Review Focus items, including
  the merge-conflict round trip, role-boundary enforcement, and the
  dry-run-forgotten-after-restart case via an actual server restart.

**What the final whole-branch review found, now fixed:**

- **[Critical]** A stored script in an engineer's Markdown answer ran in
  custos's own origin when an admin opened the workspace's Book view
  (`BookView.vue`'s `<iframe :srcdoc>` had no `sandbox` attribute) and
  could read the admin's token-only-mode bearer token out of
  `sessionStorage` and self-promote to admin via `PUT
  /api/admin/users/{email}/roles`. Reproduced end to end with a real
  headless browser: the attack succeeded against the unfixed code (a test
  account's `admin` flag went from `false` to `true`) and was confirmed
  blocked after the fix (`sandbox=""` on the iframe, plus
  `Content-Security-Policy: sandbox` and `X-Content-Type-Options:
  nosniff` on the HTML response) — ruling 4.23.
- **[Important]** On a case-insensitive filesystem (macOS/Windows, the
  dev-server default), `internal/server`'s push-path role gate matched
  the catalog/workspace path case-sensitively while git http-backend's
  own repository lookup folds case, so `/git/Catalog.git`/
  `/git/catalog.GIT` reached the backend with no role check at all.
  Reproduced live: a read-only token's push to `/git/Catalog.git`
  succeeded and catalog `main` actually advanced. Fixed by matching a
  lower-cased copy of the path; regression test added — ruling 4.24.
- **[Important]** The `Dockerfile` had no Node build stage, so `make
  docker`/the published image embedded whatever UI happened to already be
  on disk (the committed placeholder, or stale local build output)
  instead of a real build. Fixed with a `node:22-slim` build stage that
  runs the frontend build and copies its output into the Go build stage
  before compiling; verified by building and running the real image and
  confirming it serves the real UI, not the placeholder.
- **[Important]** `make build`'s `web-build` step deleted the tracked
  `.gitkeep` and overwrote the tracked placeholder `index.html`, then
  left the new `assets/` directory untracked — a routine `git add -A`
  after a build would stage large, frequently-changing build artifacts
  and could permanently lose the "works with zero npm" placeholder
  invariant (ruling 4.2). Fixed by extending `.gitignore` to keep
  `.gitkeep` and `index.html` tracked while ignoring everything else
  `make build` generates under `internal/webui/dist/`.
- **[Important]** This documentation gap: spec §11.4 was missing despite
  dozens of citations of "ruling 4.1" through "ruling 4.18" across the
  codebase, this file still said phase 4 was "Not planned," and the
  README had seven separate inaccuracies (base64 instead of hex session
  secrets, unauthenticated push examples that now 401, a missing Node.js
  prerequisite, a stale "the web UI follows" placeholder sentence, missing
  endpoint rows, and wrong descriptions of `--admin-emails` and
  `--oidc-redirect-url`). All fixed in this dispatch.

Rulings: spec §11.4 holds 18 rulings from the architecture note and its
cross-plan review (4.1–4.18), 4 made while executing the plans
(4.19–4.22), 2 made by this final whole-branch review (4.23–4.24), and 2
Minor findings recorded as deliberate non-fixes rather than acted-on
rulings (4.25–4.26). These change the spec or the way custos is run and
deserve a look first:

| Ruling | What changes |
| --- | --- |
| 2.6 | Replaced: `X-Custos-Author` is gone; every write is authored by the signed-in caller (OIDC session or personal access token). |
| 2.7 | The shared attachment store now sits behind sign-in: `POST`/`GET /api/blobs` are gated only on the caller being signed in, not on any specific role (confirmed in the final re-review). |
| 2.11 | HTML export now exists: `GET /api/workspaces/{id}/book?format=html`. |
| 2.12 | The catalog API is no longer read-only: drafts, task/group/processor edits and publish now go through `internal/catalogdraft`. |
| 4.6 | `--admin-emails` is checked fresh on every request, not cached at start-up — granting admin takes effect on that person's next request, not just at first sign-in. |
| 4.21 | goldmark's `WithUnsafe()` (raw HTML passthrough in the book) is accepted as a deliberate, reversible, small-org-scale decision — now contained by ruling 4.23's sandboxed iframe and CSP headers. |
| 4.23 | `BookView`'s HTML preview is sandboxed (`sandbox=""`) and CSP-restricted, closing the privilege-escalation path `WithUnsafe()` otherwise left open. |
| 4.24 | The catalog/workspace push-path role gate matches case-insensitively, closing a macOS/Windows-only role-gate bypass. |

Also note: the container image now really does embed a built frontend
(the `Dockerfile` gained a Node build stage in the final review; it
previously embedded whatever happened to already be on disk at build
time). `make test` now runs `go test ./... -race -count=1` (previously no
`-race`).

Phase 4 is complete on branch `worktree-worktree-phase-4` — not yet merged
to `main` — and ready for the finishing step.
