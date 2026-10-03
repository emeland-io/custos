# custos: design

- Status: draft, for review
- Date: 2026-10-02
- Replaces the Root/Node/Leaf/Seed/Shoot model described in the current README.

## 1. Purpose

custos manages documentation made of small fragments. A **task** asks for a
piece of documentation. An **answer** records how the task was done; it can be
long Markdown or a short value such as a timestamp or a filename. Tasks are
maintained centrally and arranged in hierarchical **groups**, so the answers
of a workspace can be assembled like the chapters of a book. Some tasks have a
**processor**, a program that reads the answer and generates more specific
follow-up tasks and documents such as in-toto attestations.

All state is revision-safe and can be forked and merged. custos uses Git for
this.

### Scale

A few hundred tasks, a few thousand at most. Nothing in this design needs
more than one server.

### Personas

| Persona | Does | Needs |
| --- | --- | --- |
| Engineer | Performs tasks and records the answers | Clear list of open work; review of task updates and processor output |
| Task-Author | Writes tasks and arranges them in groups | Revision-safe editing in the UI or in a plain text editor |
| Processor-Author | Writes processors | To work on subject matter only, without knowing how custos stores tasks |

Processor-Authors and Task-Authors are trusted by the organization.

## 2. Architecture

A single Go service (`custos serve`) owns plain Git repositories:

- one **catalog** repo with tasks, groups and the processor registry;
- one **workspace** repo per product or project, with answers, generated
  tasks and documents;
- a **blob store** per workspace for binary attachments.

Alongside them:

- an **index** (SQLite) built from the repos for queries; it is a cache and
  can be deleted and rebuilt at any time;
- a **processor runner** that runs processor containers from a queue;
- a **web UI** (Vite single-page app embedded in the binary), a REST API, and
  Git smart HTTP for clone and push.

Git is the only source of truth. Repos can be cloned, edited and pushed with
normal Git tools; pushes are validated like UI edits. A workspace can live on
another server and sync back, because it is just a Git repo.

## 3. Data model and file layout

All text files are YAML frontmatter with a Markdown body.

### 3.1 Catalog repo

```text
tasks/<uuid>/<semver>.md        one immutable file per task version
groups/<slug>/group.yaml        title, ordered children (sub-groups, task UUIDs)
processors.yaml                 processor registry and bindings
```

**Task version** frontmatter:

| Field | Meaning |
| --- | --- |
| `id` | UUID v4 of the task, the same for all its versions |
| `version` | semver string |
| `title` | short title |
| `answer_type` | `markdown`, `text`, `timestamp`, `path`, `url` or `choice` |
| `choices` | list of allowed values, for `choice` only |
| `previous` | list of `{id, version}`; empty for the first version |

The body is the task description in Markdown.

**Task-history graph.** The `previous` references form a directed graph that
is separate from Git history. A version may reference more than one previous
version:

- a new version of task A references A@1.2.0;
- merging tasks A and B: a new version of A references both A@1.2.0 and
  B@2.0.0; B is then superseded.

The **current version** of a task is the version of it that no other version
references in `previous`. A task whose versions have all been referenced by a
version of another task (B after the merge above) is **superseded** and has
no current version. It continues as part of the task that merged it: its
version files and history stay, but it is no longer a task of its own and
cannot be listed in a group. The publish that merges B must therefore also
remove B from its group (rule 5). How answers to a superseded task are
handled in workspaces is described in section 3.3.

**Rules enforced on `main`** (by `custos validate`, the UI, and the
pre-receive hook):

1. A task version file, once on `main`, never changes or disappears.
2. Every task that is not superseded has exactly one current version.
   Two current versions (a fork) must be merged by a new version first.
3. Every `previous` reference resolves to an existing version, and its
   version is lower than the referencing version when it is the same task.
4. `id` and `version` match the file path.
5. Groups reference only existing, non-superseded task UUIDs; each task
   appears in at most one group; group slugs are unique.
6. Bindings reference existing tasks and registered processor names.

On branches other than `main`, version files can be edited freely; this is
how authors draft.

**Groups** reference task UUIDs, not versions. The assembled book uses the
current version of each task.

`group.yaml`:

```yaml
title: Build environment
children:
  - group: toolchain                       # sub-group, by slug
  - task: 3f1c…                            # task, by UUID
```

The top-level order is given by `groups/index.yaml`, which lists the
top-level group slugs.

### 3.2 Processor registry

```yaml
processors:
  host-scanner:
    image: registry.example.org/host-scanner@sha256:…
    timeout: 60s           # default 60s
    network: false         # default false
    secrets: [slsa-signing-key]
bindings:
  3f1c…: host-scanner      # catalog task UUID → processor name
```

Changing a binding or a digest is a catalog commit. It does not bump the
version of any task.

### 3.3 Workspace repo

```text
custos.yaml                     workspace id, catalog URL, pinned catalog commit, frozen flag
answers/<task-uuid>.md          the answer to a task
generated/<uuid>/<semver>.md    tasks made by processors
documents/<uuid>.json           documents made by processors
```

**Answer** frontmatter:

| Field | Meaning |
| --- | --- |
| `task` | task UUID |
| `task_version` | the task version this answer was written for |
| `type` | answer type, copied from the task version |
| `value` | the value, for all types except `markdown` |
| `attachments` | list of `{name, sha256, media_type}` |

For `markdown`, the body holds the answer.

**One answer file per task.** When the pinned catalog contains a newer
version of the task than `task_version`, the existing answer **stays in
effect** and appears in the book; the task is shown as *pending update*.
Answering the newer version overwrites the file. Older answers remain
reachable through Git history.

**Answers to merged tasks.** When A@2.0.0 merges B, the answer files of A and
B both stay in effect until A@2.0.0 is answered. Until then, the book shows
A's old answer and B's old answer together at A's position. Once A@2.0.0 is
answered, that answer replaces both in the book. B's answer file stays in
the workspace, so the reference view (section 4.2) can show it, and is kept
in Git history.

**Generated task** files use the task version format plus:

| Field | Meaning |
| --- | --- |
| `origin` | optional `{id, version?}`, set by the processor |
| `match_key` | the processor's key for this task (section 5.4) |
| `processor` | optional processor name for this task (section 5.5) |
| `produced_by` | `{task, processor, digest, answer_commit}` |

Generated task IDs are UUID v4, assigned by custos when the task first
appears.

**Document** files hold the document as emitted (statement, DSSE envelope or
Sigstore bundle) plus metadata: `match_key`, `name`, `media_type`,
`produced_by`, and `verification` (`verified`, `failed`, `unsigned`, with
signer identities).

### 3.4 Blob store

`blobs/sha256/<hash>` next to the workspace repo, outside Git. Answers
reference attachments by hash. Binary files of any type are allowed. A plain
`git clone` does not include blobs; `custos clone` and the API fetch them.

## 4. Change flows

### 4.1 Catalog change → workspaces

1. `main` of the catalog advances, by UI publish or by push. custos validates
   the new state (section 3.1); invalid changes are rejected.
2. For every workspace, custos diffs the pinned commit against the new
   `main`: new task versions, added and removed tasks, group changes,
   binding and digest changes.
3. **Unfrozen workspace:** custos moves the pin in a commit by `custos-bot`
   on the workspace's `main`.
   **Frozen workspace:** custos opens a pin proposal on branch
   `custos/pin/<catalog-commit>` listing the diff. A newer catalog commit
   replaces the open pin proposal. Accepting merges it.
4. After the pin moves, affected tasks show as *pending update*; existing
   answers stay in effect.
5. Binding and digest changes start processor reruns (section 4.3).

### 4.2 Answering

Saving an answer is one commit on the workspace's `main`, authored by the
engineer. **Still valid** writes the same content with the new
`task_version`. If the task has a processor, a run is queued and its result
is shown to the engineer as a proposal in the same session.

For a merged task, the answers of all its `previous` tasks are shown as
reference while the engineer writes the merged answer. Until it is
answered, those answers stay in effect in the book (section 3.3).

### 4.3 Processor runs and proposals

A run takes one answer, the task version, and the processor digest. Its
output is matched against the current generated tasks and documents of that
answer (section 5.4) and written to branch
`custos/proposal/<task-uuid>/<digest>`. A run whose output matches the
current state creates no proposal. A newer proposal for the same task
replaces an open one.

Runs are triggered when:

- an answer to a bound task is saved or accepted;
- the digest of a processor name changes in the pinned catalog: custos
  reruns it on every answer of every task bound to that name, in catalog
  tasks and generated tasks, in every workspace;
- a binding is added or changed;
- an engineer retries a failed run.

The engineer reviews a proposal as a diff (added / new version / unchanged /
removed, with document verification status) and accepts all, some or none.
Accepting merges the selected changes into `main`. Accepted changes may
trigger further runs (section 5.5).

### 4.4 Fork and merge

- **Fork a workspace:** a new repo from the history of an existing one, with
  a new workspace id and its own blob store (blobs copied by reference).
- **Branches** inside a workspace are for parallel or what-if work.
- **Merge** uses Git, with custos-aware conflict handling:
  - conflicting answer files are shown side by side for the engineer to
    choose;
  - conflicting pins resolve to the descendant commit when one is an
    ancestor of the other; otherwise the merge is blocked;
  - conflicting generated tasks or documents are resolved by rerunning the
    processor on the merged answer.
- **Fork the catalog:** produces another catalog; a workspace pins one
  catalog by URL.

### 4.5 Concurrency

Writes to each repo go through one queue in the server. Pushes from outside
pass through a pre-receive hook that applies the same validation.

## 5. Processors

### 5.1 Packaging

A processor is a container image. The image digest is its version.

### 5.2 Running

custos runs the image through Docker or Podman on the central host, with a
read-only root filesystem, the configured timeout, a memory limit, and no
network unless `network: true`. Named secrets are mounted read-only at
`/run/secrets/<name>` from a secrets directory on the host. Attachments are
mounted read-only at `/input/blobs/<sha256>`.

### 5.3 Contract `custos.processor/v1`

Input on stdin:

```json
{ "contract": "custos.processor/v1",
  "workspace": {"id": "…"},
  "task":   {"id": "…", "version": "1.3.0", "title": "…", "body": "…", "answer_type": "markdown"},
  "answer": {"task_version": "1.3.0", "value": null, "body": "…",
             "attachments": [{"name": "scan.pdf", "sha256": "…", "media_type": "application/pdf",
                              "path": "/input/blobs/…"}]} }
```

Output on stdout:

```json
{ "tasks": [{"match_key": "host:web-01", "title": "…", "body": "…",
             "answer_type": "text", "origin": {"id": "…"},
             "processor": "host-scanner", "bump": "minor"}],
  "documents": [{"match_key": "slsa:web-01", "name": "…",
                 "media_type": "application/vnd.in-toto+json", "content": {}}] }
```

- `origin`, `processor` and `bump` are optional. `bump` is `patch`, `minor`
  or `major` and defaults to `minor`.
- `match_key` is required and unique within one output, separately for tasks
  and documents.
- stderr is kept as the run log.
- A non-zero exit, a timeout, or output that fails validation marks the run
  as failed. Failed runs create no proposal and are shown on the task with
  their log.

### 5.4 Matching

For each output item, custos looks up the item with the same `match_key`
produced earlier for the same answer:

| Case | Result |
| --- | --- |
| key found, content unchanged | unchanged |
| key found, content changed | task: new version, semver step from `bump`, `previous` → old version; document: replaced |
| key not found | new task with a fresh UUID v4 / new document |
| existing key missing from output | proposed for removal |

Documents are compared on their statement payload, not on envelope or
signature bytes, so re-signing alone is not a change.

When a generated task gets a new version, its answer stays in effect and the
task is *pending update*, exactly like a catalog task.

### 5.5 Generated tasks behave like catalog tasks

Generated tasks have versions, answers and pending updates, and they appear
in the book under their `origin`, or under the task whose answer produced
them when `origin` is empty. A generated task with a `processor` name runs
that processor when answered and is rerun when that name's digest changes.

- **Cascading removal:** accepting the removal of a generated task proposes
  removal of the tasks generated below it. Answers stay in Git history.
- **Depth limit:** at most 8 levels of generated tasks below a catalog task
  (configurable). Output that would go deeper fails the run.

### 5.6 Signing and verification

Processors sign their own documents, using mounted secrets or keyless
Sigstore (with `network: true`). Documents may be DSSE envelopes, Sigstore
bundles or bare statements. custos verifies them on ingest against the
trusted keys, using the carabiner-dev libraries chosen in
[ADR 0001](../../adr/0001-in-toto-library.md), and stores the result with
the document.

### 5.7 Tooling for Processor-Authors

- SDKs for Go and Python: implement `process(task, answer) -> Output`;
  the SDK reads the input, validates the output, and writes JSON.
- `custos processor test <image> --answer sample.md [--previous dir]` runs a
  processor locally and prints the proposal it would produce. No server or
  workspace is needed.
- **Dry run** in the UI before changing a digest: runs the new image over all
  bound answers and reports how many proposals in how many workspaces it
  would create, with samples. Nothing is committed.

## 6. Interfaces

### 6.1 Web UI

- **Engineer:** workspace dashboard (unanswered, pending update, open
  proposals, failed runs); answer forms per type with attachment upload;
  task-version diff next to the current answer with **still valid**;
  proposal review with per-item accept/reject; book view with Markdown and
  HTML export; freeze/unfreeze.
- **Task-Author:** group tree with drag-to-reorder; task editor with
  **new version** (copies the current version, asks for the semver step,
  sets `previous`); merge tool; version-graph view. Edits go to a draft
  branch; **publish** validates and merges into `main`.
- **Processor-Author:** registry list, run history and logs, dry run.

### 6.2 CLI

| Command | Purpose |
| --- | --- |
| `custos serve` | run the server |
| `custos clone` / `custos push` | repo plus blobs |
| `custos validate` | check a catalog checkout; usable as pre-commit hook |
| `custos task new-version <uuid> --patch\|--minor\|--major` | create the next version file |
| `custos processor test` | section 5.7 |

### 6.3 API

- REST and JSON under `/api`.
- Git smart HTTP under `/git/<repo>.git`, with the pre-receive validation.

### 6.4 Authentication and roles

- OIDC login against the organization's identity provider; personal access
  tokens for Git over HTTP and the CLI.
- Roles: `engineer` (per workspace), `task-author` (catalog),
  `processor-author` (registry), `admin` (create and fork workspaces,
  manage roles).
- Commits are authored by the acting user; automated commits by
  `custos-bot`. Optionally, custos signs its own commits with an SSH key.

## 7. Error handling

- Invalid catalog changes are rejected at publish or push, naming the file
  and the violated rule.
- A workspace found inconsistent at startup (for example after a manual
  edit on disk) is reported on its dashboard; custos does not crash and does
  not repair it silently.
- Failed processor runs are stored with logs and can be retried.
- If the index is missing or does not match Git `HEAD`, it is rebuilt from
  the repos.
- A missing blob is shown as "attachment unavailable" on its answer.

## 8. Testing

- Unit tests for domain rules: current-version detection, merge validation,
  matching, cascades, depth limit.
- Repo-level tests on temporary Git repos for every flow in section 4.
- Processor contract tests with small test images (echo, generate, sign,
  fail, loop) and golden-file tests for `custos processor test`.
- API tests and frontend type checks (`make test`).

## 9. Delivery

One implementation plan per phase:

1. **Core:** catalog and workspace repo formats, validation, Git smart HTTP
   with pre-receive validation, CLI `validate` and `task new-version`.
   Usable with a text editor.
2. **Flows:** pin updates and freeze, answering, blob store, workspace fork
   and merge, index, REST API.
3. **Processors:** registry, runner, matching, proposals, cascades, signing
   verification, SDKs, `processor test`, dry run.
4. **UI and authentication:** the persona views, OIDC, roles.

## 10. Out of scope

- PDF export of the book.
- Running processors on Kubernetes or other remote runners.
- Mirroring repos to an external Git forge.
- Editing generated tasks by hand; they change only through processors.

## 11. Implementation rulings

Implementing a phase sometimes needs a decision that this spec does not
settle, or one that departs from it. Each such decision is recorded here,
with its reason and what it costs if it turns out wrong. A ruling that
contradicts an earlier section takes precedence over it, until that section
is revised.

### 11.1 Phase 1 (Core)

| # | Ruling | Reason | Cost if wrong |
| --- | --- | --- | --- |
| 1.1 | `go.mod` says `go 1.26.0` instead of `go 1.26`. | Both mean the same minimum version; `go mod tidy` writes the patch form because a dependency requires it. | A one-line edit. |
| 1.2 | `Heads`, `Current` and `Superseded` of the task graph assume a graph without reference cycles; versions on a cycle are never current, so their tasks look merged. | Every validation path runs `Check` first, which rejects cycles. | A catalog with a cycle can show "merged" messages next to the cycle error, including from `task new-version`. |
| 1.3 | The immutability check (rule 1) ignores layout problems in the previous `main`. | The previous `main` was accepted already; layout problems of the new tree are reported by the normal check. | An unreadable old tree would skip the immutability check. |
| 1.4 | Request bodies below `/git/` are limited to 256 MiB (HTTP 413 above). | Prevents a client from filling the disk; attachments live outside Git, so pushes stay small. | Larger legitimate pushes are refused; the limit is one variable. |
| 1.5 | The container image is based on Alpine with the packages `git` and `git-daemon`. | The server runs `git http-backend`, which Alpine ships in `git-daemon`. | A larger image than distroless. |
| 1.6 | The pre-receive hook reads pushed files as raw blobs (`git ls-tree` + `git cat-file --batch`), not through `git archive`; symlinks and submodules are rejected on `main`. | `git archive` honours `.gitattributes`, which let a push hide files from validation. | None known. |
| 1.7 | `custos validate DIR` checks the staged content (the Git index) when DIR is the root of a Git checkout, and the files on disk otherwise. | It is meant as a pre-commit hook, and untracked files such as `.DS_Store` must not count. | Unstaged edits are only checked after `git add`. |
| 1.8 | `custos workspace create` only installs the hook of the new repository; `serve` rewrites all hooks at start. | Running the CLI from another path must not re-point the hooks of all repositories. | None known. |
| 1.9 | A UTF-8 byte-order mark before the frontmatter is ignored. | Windows editors write one. | None known. |
| 1.10 | The hook keeps a pushed tree in memory. | Catalogs and workspaces are small; the push limit bounds it. | Hook memory grows with the push size. |
| 1.11 | Deferred to phase 2: checking that the `workspace` id in `custos.yaml` matches the repository it is pushed to. | Phase 2 owns the link between workspace repositories and their content. | Until then a workspace repository can carry another workspace's id. |

### 11.2 Phase 2 (Flows)

Rulings made while planning phase 2. Rulings made during its execution are
added below them.

| # | Ruling | Reason | Cost if wrong |
| --- | --- | --- | --- |
| 2.1 | Phase 2 is delivered as four plans, run in order: 2a store and status, 2b answers, blobs and REST API, 2c catalog distribution and freeze, 2d fork and merge. | One plan for all of §9 item 2 would be too large to review. | None. |
| 2.2 | No SQLite index in phase 2 (§2, §7): status and the book are computed on demand from the Git trees. | A few thousand small files parse in milliseconds; an index adds a dependency and a cache to keep consistent. | If it gets slow, an index or cache has to be added later behind the same functions. |
| 2.3 | Server-side changes are written with Git plumbing on the bare repositories (temporary index, `commit-tree`, `update-ref` with the expected old value), one lock per repository, and validated with the same rules as the pre-receive hook before the ref moves. | `update-ref` does not run hooks, and pushes can race with server writes. | A write that loses the race fails with a conflict and must be retried. |
| 2.4 | A workspace's pin is resolved in the catalog repository of the same server; `catalog.url` in `custos.yaml` is informational and set from the server's public URL. | Workspaces hosted elsewhere sync back by push, so the server always holds the catalog they pin. | Workspaces pinned to a foreign catalog are not supported. |
| 2.5 | Catalog changes are distributed in-process after a successful push to `catalog.git` and once at start-up, by an idempotent reconcile; Git hooks never write. | Hooks run inside `git receive-pack` and cannot take the server's repository locks. | A crash between the push and the reconcile delays distribution until the next push or restart. |
| 2.6 | Until phase 4 adds authentication, write requests name their author in the header `X-Custos-Author: Name <email>`; the REST API is unauthenticated like Git over HTTP and listens on loopback by default. | Commits must be authored by a person (§6.4) before OIDC exists. | Anyone who can reach the port can write as anyone; same exposure as phase 1. |
| 2.7 | One content-addressed blob store `<data-dir>/blobs/sha256/<hash>` serves all workspaces (§3.4 and §4.4 said one per workspace). | Forks then copy nothing, and identical files are stored once. | Per-workspace access control for blobs has to be enforced by the API in phase 4. |
| 2.8 | A single uploaded blob is limited to 1 GiB. | Bounds disk use per request. | Larger attachments are refused; the limit is one variable. |
| 2.9 | Workspace merges use `git merge-tree --write-tree`, which raises the Git requirement from 2.28 to 2.38. | It merges inside a bare repository without a work tree. | Hosts with an older Git cannot run the server. |
| 2.10 | Until phase 3, merge conflicts in generated tasks and documents are resolved by choosing one side, like answer conflicts. | Rerunning processors (§4.4) needs the processor runner of phase 3. | Phase 3 must replace the choice with a rerun. |
| 2.11 | The book is exported as Markdown, and status as JSON; HTML export comes with the web UI in phase 4. | HTML needs a Markdown renderer, which the UI brings anyway. | No HTML export until phase 4. |
| 2.12 | The catalog API of phase 2 is read-only; drafting and publishing through the API comes with the Task-Author UI in phase 4. | Authors can work with Git and a text editor meanwhile. | Phase 4 grows by the catalog write API. |
| 2.13 | Creating a workspace (CLI or API) makes an initial commit with `custos.yaml` pinned to the current catalog `main`, and fails while the catalog is empty. New flag `--public-url` (`CUSTOS_PUBLIC_URL`, default `http://127.0.0.1:8080`). | A workspace without a pin cannot show tasks. | The catalog must be pushed before the first workspace is created. |
| 2.14 | Pushes to a workspace's `main` are also checked against the server: the `workspace` id must match the repository (resolves 1.11), the pin must be a commit on the catalog's `main`, and each answer must name an existing task version of the pinned catalog (or a generated task), with the same answer type and, for `choice`, one of its choices. New rule names `workspace-id`, `pin` and `answer`. | These checks need the catalog, which only the server holds. | None known. |
| 2.15 | `custos validate` on a workspace checkout still checks the workspace on its own, without the catalog. | The catalog is not part of a workspace checkout. | Answer/task mismatches surface only on push. |
| 2.16 | A pin may move to any commit on the catalog's `main`, also an older one. | The spec is silent; going back is useful to undo a pin move. | None known. |
| 2.17 | Repository layout, hook installation and server-side writes move from package `server` into a new package `store`; `server` keeps HTTP only. | Fork, distribution and the API all create or write repositories. | None. |
