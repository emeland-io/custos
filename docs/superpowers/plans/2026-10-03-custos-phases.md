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
| 4 UI and authentication | Web UI for engineers, task authors and processor authors; HTML book; catalog editing through the API; OIDC login, roles and tokens. Replaces the `X-Custos-Author` header (ruling 2.6) and adds access control for the shared attachment store (ruling 2.7). | Not planned |

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

Not planned yet.
