# custos delivery phases

Status of the custos delivery phases (spec §9) and where their plans and rulings live. This file is updated whenever a phase or plan is completed.

- Spec: [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md)
- Rulings: spec §11 — §11.1 phase 1, §11.2 phase 2, and so on. Rulings made while a phase is executed are added to its subsection before its branch is finished.

## Overview

| Phase | Content | Status |
| --- | --- | --- |
| 1 Core | Catalog and workspace formats, validation, Git over HTTP with a validating pre-receive hook, CLI `validate`, `task new-version`, `workspace create`, `serve`, container image | Done, merged to `main` on 2026-10-03 (`d0af8ad`) |
| 2 Flows | Catalog updates reaching workspaces, workspace freeze and catalog-update proposals, answering, attachments, REST API, `custos clone`/`push`, workspace fork and merge | 4 plans written, not started |
| 3 Processors | Processor registry and runner (containers), matching generated tasks across reruns, proposals for review, cascades and depth limit, signature checks, Go/Python SDKs, `processor test`, dry runs. Replaces ruling 2.10: merge conflicts in generated output get a processor rerun instead of a pick-one-side choice. | Not planned |
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
| [2a](2026-10-03-phase-2a-store.md) | Server-side writes to the repositories, pushes checked against the pinned catalog, status and book | 10 | Not started |
| [2b](2026-10-03-phase-2b-api.md) | Attachment store, REST API for workspaces, answers, attachments and the catalog, plus `custos clone`/`push` | 12 | Not started |
| [2c](2026-10-03-phase-2c-distribution.md) | Catalog updates reaching workspaces, freeze, catalog-update proposals with accept/reject | 8 | Not started |
| [2d](2026-10-03-phase-2d-fork-merge.md) | Workspace fork and merge with conflict resolution | 6 | Not started |

How the plans were made: four subagents wrote them in parallel from a shared architecture note, [2026-10-03-phase-2-architecture.md](2026-10-03-phase-2-architecture.md). A fifth reviewed them as a set. It found that the plans wired the API into `serve` incompatibly, that one code block didn't compile, and that the README steps overwrote each other, and it fixed all of these in the plan text. It then applied all four plans in order to a scratch copy of the repository; the result passes `gofmt`, `go vet` and `go test -race`.

Rulings: spec §11.2 holds 41 rulings made while planning (2.1–2.41). These six change the spec and deserve a look first:

| Ruling | What changes |
| --- | --- |
| 2.2 | No SQLite index. Status and the book are computed from Git on each request. |
| 2.6 | Until login exists in phase 4, write requests name their author in an `X-Custos-Author` header. The API is unauthenticated, like Git over HTTP. |
| 2.7 | One shared attachment store for all workspaces instead of one per workspace, so forks copy nothing. |
| 2.9 | The server needs Git 2.38 or newer, because merges run without a working copy. |
| 2.11 | The book is exported as Markdown only; HTML export comes with the web UI. |
| 2.12 | The catalog API is read-only for now; editing through the API comes with the web UI. |

## Phase 3 — Processors

Not planned yet.

## Phase 4 — UI and authentication

Not planned yet.
