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
- a **web UI** (Vue.js and tailwindcss single-page app embedded in the binary), a REST API, and
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
| 2.10 | Until phase 3, merge conflicts in generated tasks and documents are resolved by choosing one side, like answer conflicts. | Rerunning processors (§4.4) needs the processor runner of phase 3. | Replaced in phase 3 by ruling 3.7. |
| 2.11 | The book is exported as Markdown, and status as JSON; HTML export comes with the web UI in phase 4. | HTML needs a Markdown renderer, which the UI brings anyway. | No HTML export until phase 4. |
| 2.12 | The catalog API of phase 2 is read-only; drafting and publishing through the API comes with the Task-Author UI in phase 4. | Authors can work with Git and a text editor meanwhile. | Phase 4 grows by the catalog write API. |
| 2.13 | Creating a workspace (CLI or API) makes an initial commit with `custos.yaml` pinned to the current catalog `main`, and fails while the catalog is empty. New flag `--public-url` (`CUSTOS_PUBLIC_URL`, default `http://127.0.0.1:8080`). | A workspace without a pin cannot show tasks. | The catalog must be pushed before the first workspace is created. |
| 2.14 | Pushes to a workspace's `main` are also checked against the server: the `workspace` id must match the repository (resolves 1.11), the pin must be a commit on the catalog's `main`, and each answer must name an existing task version of the pinned catalog (or a generated task), with the same answer type and, for `choice`, one of its choices. New rule names `workspace-id`, `pin` and `answer`. | These checks need the catalog, which only the server holds. | None known. |
| 2.15 | `custos validate` on a workspace checkout still checks the workspace on its own, without the catalog. | The catalog is not part of a workspace checkout. | Answer/task mismatches surface only on push. |
| 2.16 | A pin may move to any commit on the catalog's `main`, also an older one. | The spec is silent; going back is useful to undo a pin move. | None known. |
| 2.17 | Repository layout, hook installation and server-side writes move from package `server` into a new package `store`; `server` keeps HTTP only. | Fork, distribution and the API all create or write repositories. | None. |
| 2.18 | A rejected pin proposal is remembered as the ref `refs/custos/rejected-pin/<catalog-commit>` in the workspace; reconcile does not reopen it, and deletes the mark once the catalog's `main` moves on. | Reconcile runs again after every push and at start-up and would otherwise reopen what the engineer rejected. | Hidden refs in workspace repositories. |
| 2.19 | Accepting a pin proposal after `main` moved on writes a merge commit whose tree is `main`'s tree with the proposal's pin, instead of a three-way merge. | Proposal branches are written by custos and change only the pin. | Commits pushed onto a `custos/pin/*` branch by hand are dropped on such an accept (a fast-forward keeps them). |
| 2.20 | Freezing a workspace whose pin lags behind the catalog opens the pin proposal at once; unfreezing moves the pin at once. | The workspace should not wait for the next catalog push. | None known. |
| 2.21 | custos re-encodes `custos.yaml` when it changes it; unknown fields are an error, comments are lost. | The file is small and machine-owned. | Hand-written comments in `custos.yaml` disappear on the next bot commit. |
| 2.22 | Git commands custos runs for a repository other than the one a hook guards drop the hook's Git environment (`GIT_DIR`, `GIT_OBJECT_DIRECTORY`, quarantine paths, `GIT_INDEX_FILE`); only the guarded repository keeps it. | A workspace hook reading the catalog would otherwise look into the workspace's quarantine. | A hook-side repository that forgets the flag cannot see pushed objects; the push tests catch it. |
| 2.23 | Catalog distribution runs synchronously after every push request to `catalog.git`, accepted or not, before the response ends; at start-up it runs before the server accepts requests. | Deterministic, and reconcile is idempotent. | A slow reconcile delays the catalog pusher and start-up. |
| 2.24 | Only one distribution operation (reconcile, freeze, unfreeze, accept, reject) runs at a time per server. | Overlapping reconciles could move a pin back or delete a newer proposal. | A slow reconcile delays freeze and accept requests. |
| 2.25 | An existing pin proposal branch is not rewritten when the workspace's `main` moves on. | Rewriting it after every answer would churn commits. | Accepting is more often a merge (2.19) than a fast-forward. |
| 2.26 | Distribution errors and workspace problems found at start-up are printed on `serve`'s standard error and never stop the server. | §7; there is no dashboard until phase 4. | Problems go unnoticed when nobody reads the log. |
| 2.27 | `--public-url` must be an absolute http(s) URL; its default does not follow `--addr`. | The public URL can differ from the bind address (proxies, containers). | Users who change `--addr` must set `--public-url` too. |
| 2.28 | Only directories named `<uuid>.git` under `repos/workspaces/` are workspaces; others get no hook. | Keeps the layout unambiguous. | A repository created there by hand under another name is served without validation. |
| 2.29 | Server-side commits are never GPG-signed, whatever the host's Git configuration says. | A host `commit.gpgSign` setting would break server writes. | None until custos signs its own commits (§6.4). |
| 2.30 | Status leaves out tasks without exactly one current version and answer files whose `task` field contradicts their path; such trees fail validation. Generated tasks whose parent is unknown are listed under "Ungrouped". Markdown book headings stop at level 6. | Status shows valid content; validation reports the rest. | Broken entries vanish from status instead of showing an error. |
| 2.31 | Uploading a blob is a write and needs `X-Custos-Author`. | Convention 2.6 for all writes. | Upload-only clients must send the header. |
| 2.32 | The API refuses answers whose attachments are not in the blob store; Git pushes are not checked against it. | The API must not create dangling references; `custos push` uploads first. | A plain `git push` can reference missing blobs, which then show as unavailable (§7). |
| 2.33 | Saving an answer with unchanged content makes no commit; answer bodies are stored with `\n` line endings and a final newline. | No empty history; exact round trips. | Clients tell a no-op apart only by the unchanged commit; CRLF is not kept. |
| 2.34 | An answer may name any existing version of its task (default: the current one); answers to superseded (merged) tasks cannot be written through the API. | 2.14 accepts any existing version; §3.1 makes a superseded task no task of its own. | A client can write a stale answer on purpose; old answers of merged tasks change only by Git push. |
| 2.35 | Request bodies are limited: JSON to 16 MiB, fork and merge requests to 64 MiB. | Answers are text; attachments go through the blob store. | Very large Markdown answers are refused; the limits are constants. |
| 2.36 | Blobs are served as `application/octet-stream` with `nosniff`. | Serving the declared media type could let an upload run as HTML. | Browsers download attachments instead of showing them. |
| 2.37 | A checkout keeps attachments in `.custos/blobs/sha256/<hash>`; `custos clone` warns about attachments the server lacks and still succeeds; `custos push` pushes nothing when a referenced attachment is neither local nor on the server. There is no `attach` command yet. | §7 treats missing blobs as unavailable, but a push must not create references the server cannot serve. | Attaching a file in a checkout is a manual copy. |
| 2.38 | A merge always creates a merge commit, also when `main` could fast-forward. | The `custos.yaml` rules and validation then apply to every merge. | One extra commit per merge. |
| 2.39 | When the two sides' `custos.yaml` differ, it is merged field by field: the workspace id is always `main`'s, the pin follows the descendant rule (§4.4), `url` and `frozen` are merged three-way with `main` winning. A pin conflict without ancestry can be settled by an explicit resolution, which is validated like any pin. | Merging a fork's branch back would otherwise bring the fork's id. | Formatting of `custos.yaml` is rewritten (2.21); §4.4's "blocked" becomes "blocked until resolved". |
| 2.40 | Merge resolutions may only name open conflicts; a resolution for any other path, or a malformed one, is rejected (400). Resolving only some conflicts changes nothing and returns the ones still open. Conflicts on symlinks or submodules are errors, not resolvable conflicts. | Silent ignoring would hide mistakes; `main` rejects such files anyway (1.6). | Clients resend all resolutions once the last conflict is settled. |
| 2.41 | A fork copies only `main`, not other branches or pin proposals. | The source's proposals do not apply to the fork. | What-if branches must be pushed to the fork again. |

Rulings made while executing phase 2:

| # | Ruling | Reason | Cost if wrong |
| --- | --- | --- | --- |
| 2.42 | A full ref name such as `refs/heads/main` is resolved exactly; Git's guessing of similar names (e.g. a tag `refs/tags/refs/heads/main`) is not used. A missing repository is an error, not a missing ref. | Branches other than `main` are not validated, so a look-alike tag let a workspace be pinned to an unvalidated catalog commit. | None known. |
| 2.43 | When one server-side edit changes the same file more than once, the last change wins; deleting a path that is a directory is a no-op. | Matches the order in which the changes are applied, and "no changes" must never create an empty commit. | Callers that expected duplicates to be rejected get last-wins silently. |
| 2.44 | `--public-url` must not contain a user name or password. | It is written into `custos.yaml` and so into Git history. | None known. |
| 2.45 | Catalog pushes are recognised at `/git/catalog.git/` and at `/git/catalog/`; workspace pushes likewise with and without `.git`. | `git http-backend` serves both forms, and distribution hangs on these notifications. | None known. |
| 2.46 | Creating or forking a workspace keeps the new repository when its final compare-and-swap on `main` loses to a push; the push wins and the request answers 409. | Removing the repository would erase an accepted push. | A failed creation can leave a repository behind that holds only the pushed history. |
| 2.47 | `GET /api/workspaces` ignores a malformed `custos.yaml` and also a repository-level error of one workspace; that workspace is listed with an empty pin. | One broken workspace must not break the list; status and the start-up report of `serve` name the problem (§7). | A broken workspace shows an empty pin in the list with no hint until its status is opened. |
| 2.48 | Answer values that YAML cannot read back as written (e.g. text whose first line starts with a tab) make the encoder write that file's top-level strings double-quoted; every encoded file is checked to read back as written. | Such answers were refused with a YAML parser error. | Some answer files contain quoted strings that are less pleasant to edit by hand. |
| 2.49 | Answering a workspace that has no `main` yet answers 409; freeze, unfreeze and listing proposals on it answer 404. | Such a workspace exists while it is being created or forked; a 500 would hide that. | None known. |
| 2.50 | `custos push` checks the attachments of every new commit and also those in the trees of the refs being pushed; `--all`/`--branches`, `--tags` and `--mirror` select refs as in `git push`, and the values of `-o`, `--repo`, `--receive-pack` and `--exec` are not taken for refs. A local attachment whose content does not match its name is refused before anything is uploaded. | An attachment that escaped once (e.g. by a plain `git push`) must be uploaded by the next `custos push`. | One extra existence check per distinct attachment on every push. |
| 2.51 | Known limits of `clone` and `push` that stay for now: `push --dry-run` still uploads attachments, neither command sets HTTP timeouts, downloaded attachments are readable only by their owner, and `clone` trusts an attachment file that already exists. | No effect on correctness; uploads are idempotent and content-addressed. | A dry run changes the server's blob store; a stalled server hangs `clone` or `push`. |
| 2.52 | Unknown paths and wrong methods below `/api/` answer with Go's plain-text 404/405, not the JSON error body. | A catch-all route would get in the way of the endpoints later plans register on the same mux. | Clients see non-JSON bodies on wrong paths. |
| 2.53 | Distribution also runs after every push to a workspace and after every merge or fork through the API, for that one workspace; a pin move or proposal that loses its compare-and-swap is retried up to three times. New names: `server.OnWorkspacePush`, `distribute.ReconcileWorkspace`, and a callback parameter of `merge.Register`. | Ruling 2.3 requires the loser of a race to retry, and 2.20 promises that unfreezing moves the pin at once, however `frozen` is changed. | Every workspace push and API merge waits for one workspace reconcile. |
| 2.54 | Freeze and unfreeze succeed once the flag is committed; if the following pin move fails, the response is 200 with a `warning`. | Reporting an error for a change that was made would mislead the client. | Clients that only check the status code miss that the pin did not move. |
| 2.55 | A branch that shares no history with `main` cannot be merged (409). Only short branch names that match a conservative pattern can be merged through the API. | A missing common history is a state of the repository, not a malformed request; the API body names a branch. | Unusual but valid branch names (e.g. with `@` or `+`) cannot be merged through the API. |
| 2.56 | Resolving a `custos.yaml` conflict by choosing a side keeps `main`'s workspace id; content given explicitly is used as is. | Choosing the fork's side would otherwise bring the fork's id and fail validation. | None known. |

### 11.3 Phase 3 (Processors)

Rulings made while planning phase 3. Rulings 3.1–3.11 come from the shared architecture note ([2026-10-04-phase-3-architecture.md](../plans/2026-10-04-phase-3-architecture.md)); the others from the plans' decisions, marked with their plan. Rulings made during execution are added below them.

| # | Ruling | Reason | Cost if wrong |
| --- | --- | --- | --- |
| 3.1 | Phase 3 is delivered as five plans run in order: 3a contract, runner and test images; 3b attestation verification; 3c matching and output proposals; 3d runs, triggers, API, `serve` wiring, merge reruns and dry runs; 3e SDKs, `custos processor test` and README. | One plan for all of §9 item 3 would be too large to review. | None. |
| 3.2 | Run records (state, log, timestamps) are files under `<data-dir>/runs/<workspace>/<run-id>.json` and `.log`, not in Git and not in an index. | Project owner's choice; consistent with 2.2. Proposals themselves live in Git. | Deleting the records makes custos run every bound answer once more. |
| 3.3 | A run counts as done for the key (workspace, answer path, answer blob, processor digest); an idempotent scan of each workspace finds the runs still needed. | Covers answers pushed with plain Git, digest changes and binding changes (§4.3) without tracking events. | Rebinding an answer to an image that already ran on it does not rerun (see the 3d rulings). |
| 3.4 | Processors run through the `docker` or `podman` command-line client (`--container-runtime`, default `docker`); tests always use real Docker, with no fake runner and no skips. | No Docker SDK dependency; the project owner wants tests against real containers. | The test suite needs Docker; podman and Linux hosts are not covered by tests. |
| 3.5 | `<ref>@sha256:<hex>` runs a local image whose ID is `sha256:<hex>` when one exists, otherwise the reference is pulled. | Locally built images (tests, air-gapped hosts) have no registry digest. | An image ID is the config hash, not the manifest digest; both identify the same content. |
| 3.6 | Output proposals live on `custos/proposal/<task-uuid>/<digest-hex>`, with the digest as 64 hex digits; at most one per task. | `:` is not allowed in Git ref names. | None. |
| 3.7 | Merge conflicts on generated tasks and documents take `main`'s side and trigger a forced rerun of the producing tasks. Replaces 2.10. | §4.4 asks for a rerun on the merged answer; the runner exists now. | A merge's generated output is briefly `main`'s until the rerun's proposal is accepted. |
| 3.8 | Removing a generated task also removes its answer file from `main`; removals below it are proposed separately on `custos/proposal/<task>/cascade`. | §5.5 cascading removal; answers stay in Git history. | Several cascade branches can be open at once. |
| 3.9 | Output task items may carry `choices`. | A `choice` task needs its choices; §5.3 does not list the field. | None. |
| 3.10 | Dry-run jobs are kept in memory only. | They report and write nothing. | A restart forgets running and finished dry runs. |
| 3.11 | The Go SDK is its own module `github.com/emeland-io/custos/sdk/go`; the Python SDK uses the standard library only. | Processors must not depend on custos's dependencies. | Two modules to version. |
| 3.12 | (3a) A missing image is pulled with `docker pull` before `docker run --pull never`; the job's timeout covers only the run, not the pull. | A large first pull must not use up a 60s processor timeout. | A pull that hangs is limited only by ctx. |
| 3.13 | (3a) On timeout `ExitCode` is `-1` | The container was killed, so its exit code means nothing. | Clients that print the exit code show -1. |
| 3.14 | (3a) A cancelled ctx kills and removes the container and returns an error wrapping `ctx.Err()`, not `ErrUnavailable` | Shutdown is neither a processor failure nor an unavailable runtime; 3d can requeue such runs. | 3d must check `errors.Is(err, context.Canceled)` to tell it apart. |
| 3.15 | (3a) Extra sandbox flags not named in §5.2: `--memory-swap` equal to the memory limit (no swap), `--pids-limit 256`, tmpfs `/tmp:rw,noexec,nosuid,size=64m`, `--quiet` | A fork bomb or swapping would hurt the shared host. | A processor that needs more processes or more `/tmp` cannot get it without a code change. |
| 3.16 | (3a) The container runs as the image's user (no `--user`) | Scratch images have no passwd file, and secrets must be readable. | With `--cap-drop ALL` even root in the container cannot bypass file permissions, so secret files must be readable by the container's user (blobs are 0444 already); `SigningKey` writes its key 0644 for this reason. Operators with 0600 secrets on Linux will see the processor fail to read them. |
| 3.17 | (3a) Secret names must match `[a-z0-9][a-z0-9-]*` and blob keys 64 lowercase hex digits, and mount sources must be regular files without `,`, `"` or newline; otherwise `ErrUnavailable` | Prevents path traversal and `--mount` injection. | Unusual data-dir paths (with a comma) cannot be used. |
| 3.18 | (3a) When the container was OOM-killed, `Run` appends `[killed: out of memory, limit <m>]` to the log. | The engineer otherwise sees only exit code 137. | None known. |
| 3.19 | (3a) `ParseOutput` treats missing or `null` `tasks`/`documents` as empty and returns non-nil empty slices; it rejects empty stdout, top-level non-objects (also `null`) and anything after the object; a document `content` may be any JSON value except `null` | Friendly to hand-written processors, strict where data could be lost. | A processor that prints a log line after its JSON fails. |
| 3.20 | (3b) `carabiner-dev/collector` is not added (only `signer`) | Custos detects the format itself; an unused requirement would be removed by `go mod tidy` | None; the note's module list is an upper bound. |
| 3.21 | (3b) `go.yaml.in/yaml/v3` moves from v3.0.4 to v3.0.5. | Required by signer's module graph. | A patch-level change of the YAML encoder; the full test suite passes with it. |
| 3.22 | (3b) The library's UNVERIFIABLE (no trusted keys, keyless DSSE without Rekor) and any library error map to `failed` | The note defines `failed` as "signatures present but none verifies", and there is no fourth status. | Operators who run without trusted keys see key-signed documents as failed. |
| 3.23 | (3b) Rekor lookups are disabled (`WithRekorVerification(false)`) | Offline, deterministic verification. | Keyless DSSE envelopes with an attached certificate (slsa-github-generator style, not bundles) show as failed; keyless signing must produce a Sigstore bundle, which verifies offline with the embedded trust root (ADR 0001 finding 4). |
| 3.24 | (3b) A VERIFIED conclusion with no identity is reported as `failed` | `Signers` must not be empty for `verified` (note: "empty unless verified" implies non-empty when verified for consumers that show signers) | A library change that drops identities would turn verified documents into failed ones, which the tests catch. |
| 3.25 | (3b) Envelope detection is custos's own: DSSE = object with string `payloadType` and `payload`; bundle = `mediaType` starting with `application/vnd.dev.sigstore.bundle` plus an object `dsseEnvelope`; zero signatures → `unsigned` without calling the library. | The library's bare parser accepts any JSON and its bundle check fires on any `mediaType` field (ADR 0001 finding 3) | A bundle that signs a message instead of a DSSE envelope counts as bare and `unsigned`. |
| 3.26 | (3b) Payload of an envelope whose payload is not base64 or not JSON, and of non-JSON content, falls back to canonical content, then raw content. | `Verify` must always return a payload. | Such documents compare on the whole envelope, so re-signing one counts as a change. |
| 3.27 | (3b) `Canonical` keeps `<`, `>`, `&` unescaped, normalises string escapes, and keeps the last of duplicate keys. | The note fixes only key order, whitespace and numbers. | Two statements that differ only in escaping compare equal; one with duplicate keys compares on the last value, which may differ from what another JSON parser reads. |
| 3.28 | (3b) `New` ignores entries other than regular files (after following symbolic links) named `*.pem`/`*.pub`; a missing directory or an unparsable key is an error naming the path. | Fail loudly on wrong trust configuration, work with Kubernetes secret mounts. | A key file with another extension is ignored silently. |
| 3.29 | (3b) Signers are the library's identity spec strings (`key::<type>::<id>`, `sigstore::<issuer>::<san>`), sorted and deduplicated. | ADR 0001 finding 2. | Strings may change on a library upgrade (documents compare on the payload, so this causes no proposals). |
| 3.30 | (3c) `match.Depth(w, id)` returns -1 for every id that is not a generated task of w (also catalog tasks), and a producer that is not a generated task ends the chain at depth 0; `Plan` treats an answered task found in the catalog as depth 0 and fails a run on a task that is neither. | `Depth` has no catalog parameter. | A generated task whose producer was a hand-made unknown id counts as depth 1. |
| 3.31 | (3c) A run on a task on a producer cycle fails with `ErrTooDeep` when it outputs tasks; the depth check is `depth >= max`, so `math.MaxInt` cannot overflow. | The note says cycles count as too deep. | None known. |
| 3.32 | (3c) An origin id must name a catalog task (at the pin) or an existing generated task; its `version` is not checked, and a task created by the same output cannot be an origin. | Processors do not know the ids custos assigns. | A processor cannot nest new tasks under each other in one run. |
| 3.33 | (3c) A document is unchanged only when payload, `name` and `media_type` are all unchanged; a changed verification status alone (e.g. new trusted keys) is not a change. | Otherwise a rename would never reach `main`, while re-verification would churn proposals. | Stored verification of an unchanged document can go stale until its payload changes. |
| 3.34 | (3c) Generated task bodies are stored normalised (`\r\n` → `\n`, final newline) and compared normalised; files are encoded from a flat struct, not `workspace.GeneratedMeta`, because `frontmatter.Encode`'s quoting fallback cannot encode an inlined struct. | Titles like `\tx` must round-trip. | A later field added to `GeneratedMeta` must be added to `generatedFile` too. |
| 3.35 | (3c) Document files are indented JSON (two spaces, no HTML escaping, final newline) in the field order of `workspace.Document` | Readable Git diffs. | Content is re-indented, not kept byte for byte (signatures are in base64 payloads, so verification is unaffected). |
| 3.36 | (3c) When two earlier items of one answer share a match key (only after hand edits), the smallest id/path is matched and the others are proposed for removal. | Deterministic. | A hand-made duplicate is removed. |
| 3.37 | (3c) Unchanged items are never rewritten, so their `produced_by` keeps the digest and answer commit of the run that last changed them. | §5.4 makes unchanged a no-op. | `produced_by.digest` does not tell which image last confirmed an item. |
| 3.38 | (3c) `Write` deletes the task's open proposals first and then creates the branch with `store.UpdateWorkspace` (base = current `main`, changes that leave `main` as it is are dropped); if nothing remains, no branch is created and `""` is returned. | `UpdateWorkspace` bases an existing branch on itself, and a run whose output `main` already holds must not open a proposal. | For a moment the task has no open proposal; a concurrent writer of the same task makes one retry. |
| 3.39 | (3c) Items are recomputed by comparing the branch tip with the current `main` within a scope (see "How a proposal's items are recomputed"); tasks by id, documents by path. | The branch holds the full proposed state, so unchanged items and items accepted meanwhile show correctly. | A generated task of the same answer that reaches `main` by hand while a proposal is open is listed as `removed`. |
| 3.40 | (3c) `Accept` recomputes inside the `UpdateWorkspace` edit (so against the `main` it commits on), writes a normal commit on `main` (no merge commit with the branch), and deletes the branch only if it still points at the accepted commit; a newer proposal written meanwhile is kept. | The proposal is a set of item changes, not a history to merge. | Commits pushed onto a proposal branch by hand are applied only as far as they change in-scope files. |
| 3.41 | (3c) Accept with an empty, non-nil selection is `ErrInvalid` ("reject the proposal instead"); selecting an unchanged item is allowed and does nothing; nothing effective to apply returns `main`'s commit without a new commit and still closes the proposal. | `{"items": []}` is more likely a client bug than a wish to drop everything. | Clients must call reject to drop a proposal. |
| 3.42 | (3c) Errors after the accepting commit landed (deleting the branch, opening a cascade) are returned together with the commit. | The change was made and must not be reported as not made. | 3d must answer such errors with the commit, not as plain failure. |
| 3.43 | (3c) A cascade also removes the documents produced by the removed task and by the tasks below it, and is opened when a removed task has tasks **or** such documents below it. | Nothing would ever remove those documents otherwise, since their answer is gone. | Documents disappear that the note's wording ("tasks below") did not mention. |
| 3.44 | (3c) Cascades recurse through acceptance: accepting the removal of a task listed in a cascade opens that task's own cascade for what is still below it. | Partial accepts must not leave orphans unproposed. | Several cascade branches can be open at once (one per removed task). |
| 3.45 | (3c) `Get` (and `Accept`, `Reject`) use the first branch by name when a hand push left several below `custos/proposal/<task>/`; `Reject` deletes all of them; branches with names custos never writes are ignored by `List`/`Get` | Robustness against pushes. | Such branches are deleted by the next `Write` or `Reject` of that task. |
| 3.46 | (3d) `store.OnMainMoved` callbacks run synchronously after the lock is released, and not for no-op, failed or other-branch writes. | They may write to the store themselves; `Scan` is non-blocking. | A slow callback would delay the writer. |
| 3.47 | (3d) `CreateWorkspace` fires `OnMainMoved`; `merge.Fork` does not move `main` through the store, so `serve`'s merge `onMainMoved` also calls `Scan` | A forked workspace is scanned at once, so its bound answers run once more (the key includes the workspace id), usually with unchanged outcome — the alternative is to scan the fork later at an arbitrary time. | One run per bound answer per fork. |
| 3.48 | (3d) Reason constants `ReasonAnswer` … `ReasonStartUp` exported; classification from the newest earlier record of the answer, other blob → `answer`, other processor → `binding`, other digest → `digest`; without an earlier record `answer`, or `start-up` when the start-up scan (`ScanAll`) found it. | Scans keep no event history (note ruling 3) | Reasons are approximate after deleted records; after deleting the run records every answer shows `start-up` once. |
| 3.49 | (3d) A queued run reads `main` when it starts and updates its record (blob, commit, processor, image, digest, key); a scan skips answers with a queued or starting run; `snapMu` orders scans and run starts. | Answers saved in quick succession cause one run, not one per save, and no duplicate keys. | A run's record shows the answer it ran, not the one that triggered it. |
| 3.50 | (3d) A run whose answer disappeared or lost its binding before it started fails with that message. | The record must end in a final state. | Such runs show as failed although nothing went wrong. |
| 3.51 | (3d) Shutdown (context cancelled) leaves running records `running`; `New` re-queues `queued` and `running` records oldest first, keeping their reason. | An interrupted run must not be lost. | A run that crashed the container runtime is retried at every start. |
| 3.52 | (3d) Damaged record files are skipped at start-up. | A corrupt file must not stop `serve` | That answer may run once more. |
| 3.53 | (3d) `Retry` creates a new record with `retry_of` and reason `retry` and runs the answer as it is on `main` then. | The note allows retry for failed runs only (409 otherwise) | A retry after the answer changed runs the new answer. |
| 3.54 | (3d) `Rerun` returns the existing queued record of an answer instead of queuing a second one and skips tasks without answer or processor. | Merges and scans can trigger the same answer at once. | A forced rerun can be absorbed by a queued non-forced run (same effect). |
| 3.55 | (3d) Attachments missing from the blob store fail the run with a message naming the attachment; invalid registry timeouts fail the run (the catalog validation rejects them anyway) | A processor must not see an empty mount (§7: the attachment is unavailable) | The engineer must re-upload the attachment and retry. |
| 3.56 | (3d) The log file holds the processor's stderr only (empty when it never ran) | `error` carries custos's own message. | Two places to look. |
| 3.57 | (3d) Dry runs: one goroutine per job, one container at a time, each workspace's pinned bindings and registry settings, outputs compared with `len(Changes.Files) > 0`, samples carry `ItemJSON` items, state `failed` with `error` only when the image cannot be resolved or the server stops; nothing is written; `POST …/dry-run` requires `X-Custos-Author` like every POST; the report gains `error` | §5.7 asks for the effect of the new digest on what would actually rerun. | A big dry run is slow and cannot be cancelled except by a restart. |
| 3.58 | (3d) `Processors()` lists the registry at the catalog's `main` with `timeout` defaulting to `"60s"`, `secrets` and `bound_tasks` as `[]` when empty, sorted. | JSON lists are never null in this API. | None known. |
| 3.59 | (3d) `ErrInvalid` of `runs` (dry-run image, malformed bodies) and `proposal.ErrInvalid` (unknown selector) both answer 400; bodies of these endpoints are limited to 64 KiB with unknown fields rejected. | Consistent with 2b. | Clients sending extra fields get 400. |
| 3.60 | (3d) `POST …/accept` answers 200 `{"commit", "warning"}` when `proposal.Accept` returns a commit together with an error (the commit landed; deleting the branch or opening a cascade failed) | The change was made and must not be reported as failed or lose its commit. | Clients that ignore `warning` miss a proposal branch left open (removed by the next write or reject of that task) or a cascade proposal that was not opened (the tasks below stay until removed by hand). |
| 3.61 | (3d) `merge.Merge` sets `Result.Rerun` only when the merge was made, and a resolution sent for a generated or document path is now rejected as "no conflict" (ruling 2.40) | Main's side is final. | Clients that sent such resolutions get 400. |
| 3.62 | (3d) `merge.Register`'s `onRerun` runs after `onMainMoved`; the merge response body is unchanged. | Clients do not need the rerun list (it is visible in the run records) | None known. |
| 3.63 | (3d) A run's key holds the processor digest, not its name: rebinding an answer to another processor name whose image already ran on the same answer does not run it again; a forced rerun (merge) or a retry still does. | The note fixes the key (workspace, answer path, answer blob, digest), and the output of the same image on the same answer differs only in `produced_by.processor` | A rebinding to an identical image keeps the old `produced_by.processor` until the answer changes. |
| 3.64 | (3d) A generated task is bound by the `processor` field of the version its answer names (`task_version`), looked up in the registry of the workspace's pin. | The answer was written for that version. | A new version that changes `processor` takes effect when the answer is updated. |
| 3.65 | (3e) The Go SDK exports, besides the note's types, `Main` and `Func`: `Input`, `Workspace`, `AnswerType` and `Bump` with constants, `ContractVersion`, `MaxOutputSize`, `Run`, `ReadInput`, `Encode`, `Output.Validate`, `ValidationError`, `Answer.Text` | Authors must be able to test a processor without `os.Exit` and without spelling strings. | More exported surface to keep stable. |
| 3.66 | (3e) `OutputDocument.Content` is `any` in the Go SDK (a `json.RawMessage` works too) | Authors usually build statements as structs. | `null`-checks need a marshal during validation. |
| 3.67 | (3e) Both SDKs decode input leniently (unknown fields ignored, contract version checked) and write output with `[]` for empty lists and without empty optional fields. | Forward compatibility within v1; the server's strict parser accepts both. | A server field renamed by mistake reaches the processor as an empty value instead of an error. |
| 3.68 | (3e) `sdk/go/go.mod` says `go 1.22` | The SDK uses nothing newer, and processor builds should not need custos's toolchain. | None known. |
| 3.69 | (3e) One shared case file `sdk/testdata/output-cases.json`, with `parser_only` for malformed JSON. | The note demands "the same rules"; a test is the only way to keep three implementations aligned. | The root test reaches outside its package directory (`../../sdk/testdata`). |
| 3.70 | (3e) The SDKs exit 1 for every failure (input, process error, invalid output) and print one message (validation: `invalid output:` plus one indented line per problem) | The note says "exits 1 with the error on stderr". | Authors cannot tell failures apart by status. |
| 3.71 | (3e) `processor test` flags beyond the note: `--blobs DIR` and `--container-runtime` (`CUSTOS_CONTAINER_RUNTIME`) | Attachments and the client binary have to be found — two more flags. Without `--task`, the task is made up from the answer (title `Test task`; for `choice`, the answer's value is the only choice) — the catalog is not available locally. | A processor that behaves differently with the real task text is tested with the wrong input. |
| 3.72 | (3e) `processor test` accepts the image before or after the flags (`flag` stops at the first argument, so it parses twice) | `custos processor test img --answer f` reads naturally and is what §5.7 shows. | `--flag` values that look like the image are not possible after it. |
| 3.73 | (3e) `processor test` reads the answer, `--task` and `--previous` into one in-memory workspace tree (`custos.yaml` synthesized, answer at `answers/<task>.md`, `generated/` and `documents/` copied) and validates it with `workspace.Load` plus `Graph.Check`; problems print to stdout with the user's file paths and exit 1 before any container runs. | The same rules as a push, so a bad sample answer is found locally. | A `--previous` checkout with a broken file outside `generated/`/`documents/` is ignored. |
| 3.74 | (3e) `--task` must match the answer's task id, `task_version` and type (and contain the value for `choice`); it is read as a catalog task file. | Matches how the server pairs an answer with a task version (ruling 2.34) | A generated task file cannot be used as `--task` (its extra fields are rejected). |
| 3.75 | (3e) `processor test` checks the output against a stand-in catalog that holds the tested task, registers the tested image as processor `test`, and assumes that every other processor name and origin id the output uses exists, listing those assumptions under "Assumed without the catalog"; its workspace id defaults to `00000000-0000-4000-8000-000000000000` and its answer commit is forty zeros. | No server or workspace is needed (§5.7), and the spec's own example output names a processor and an origin. | A wrong processor name or origin is only found by the server's run. |
| 3.76 | (3e) The answered task is treated as a catalog task (depth 0) and `MaxDepth` is fixed at 8. | No catalog locally. | Depth-limit failures of processors on deep generated tasks are only seen on the server. |
| 3.77 | (3e) Secrets: every regular file in `--secrets-dir` whose name matches `[a-z0-9][a-z0-9-]*` is mounted. | There is no registry entry listing them. | A processor can see secrets it would not get on the server. |
| 3.78 | (3e) Output format as fixed by the golden files (header, `Run failed: …` or proposal lines, assumptions, `Log:`); a failed run prints to stdout and exits 1, while errors before the run (bad files, unknown image, runtime missing) go to stderr. | The run report is one readable unit for authors. | Scripts must read both streams. |
| 3.79 | (3e) The container image (`Dockerfile`) gets no container client in phase 3, and the README says so: serve from the image only without processors, or run `custos serve` on a host with Docker or Podman. | A client in the image would also need the host's Docker socket (root-equivalent) or rootless Podman-in-container, a security decision the spec does not make. | Running processors from the official image needs a follow-up. |
| 3.80 | (3e) `make test` needs Docker and python3 besides git. | Processor tests run real containers (note), and the Python SDK is tested with `unittest` | Machines without them cannot run the full suite. |
