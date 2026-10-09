# custos

custos manages documentation made of small fragments. A central **catalog**
holds *tasks*: short descriptions of something an engineer has to do and
document. Each product or project has a **workspace** that holds the
*answers* to those tasks. Tasks are arranged in groups, so the answers of a
workspace read like the chapters of a book.

The catalog and the workspaces are plain Git repositories. custos checks
every change to their `main` branch and serves them over HTTP.

The design is in
[docs/superpowers/specs/2026-10-02-custos-design.md](docs/superpowers/specs/2026-10-02-custos-design.md).
This version implements the first phase (file formats, validation, Git over
HTTP and the command line) and the start of the second: workspaces are
created pinned to the catalog, and pushes to a workspace are checked against
that catalog. Answers can be written through a REST API, attachments are
kept in a blob store on the server, and `custos clone` / `custos push` move
them along with the Git history. Catalog updates reach the workspaces on
their own, as a new pin or, for frozen workspaces, as a pin proposal.
Workspaces can be forked, and branches merged into `main`. Processors run
on the answers of their tasks and propose generated tasks and documents
for review; SDKs for Go and Python and `custos processor test` help to
write them. The web UI follows.

## Catalog

```text
tasks/<uuid>/<semver>.md     one file per task version
groups/index.yaml            top-level groups, in book order
groups/<slug>/group.yaml     title and ordered children
processors.yaml              processors and the tasks they are bound to
```

A task version:

```markdown
---
id: 3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f
version: 1.1.0
title: Build the release binaries
answer_type: markdown        # markdown, text, timestamp, path, url or choice
previous:
  - id: 3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f
    version: 1.0.0
---

Describe how the release binaries are built and where they are stored.
```

`previous` links versions into a history graph that is separate from Git
history. A version that lists versions of two tasks merges them. The current
version of a task is the one no other version lists as previous.

A group:

```yaml
title: Build environment
children:
  - group: toolchain                               # a sub-group, by slug
  - task: 3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f      # a task, by UUID
```

`groups/index.yaml` lists the top-level groups: `groups: [build, release]`.

Processors are bound to tasks by UUID and pinned by digest:

```yaml
processors:
  host-scanner:
    image: registry.example.org/host-scanner@sha256:<64 hex digits>
    timeout: 60s
bindings:
  3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f: host-scanner
```

### Rules for `main`

A push to `main` is rejected when it breaks one of these rules. Other
branches are drafts and accept anything.

| Rule | Meaning |
| --- | --- |
| `immutable` | A task version file on `main` never changes or disappears. Create a new version instead. |
| `single-current` | Every task has one current version. Two versions that branch from the same version must be merged by a new version. |
| `previous` | Previous references exist, point to a lower version of the same task, and form no cycle. |
| `path` | Files are stored where their content says: `tasks/<id>/<version>.md`. |
| `groups` | Groups form one tree below `groups/index.yaml` and list each task at most once. A task merged into another can no longer be listed. |
| `bindings` | Bindings name existing, unmerged tasks and registered processors. |
| `format` | Files parse, IDs are lowercase UUID v4, versions are semver such as `1.2.0`, unknown fields are not allowed. |
| `history` | `main` cannot be deleted or force-pushed. |

Symlinks and submodules are rejected on `main` of the catalog and of
workspaces, because custos cannot validate what they point to. Files are
checked as stored in Git; `.gitattributes` does not change what is checked.

## Workspace

```text
custos.yaml                     workspace id and the catalog commit it uses
answers/<task-uuid>.md          the answer to a task
generated/<uuid>/<semver>.md    tasks made by processors
documents/<uuid>.json           documents made by processors
```

```yaml
# custos.yaml
workspace: 5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9
catalog:
  url: https://custos.example.org/git/catalog.git
  commit: <full commit hash>
frozen: true          # optional; see "Catalog updates and freezing"
```

```markdown
---
task: 3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f
task_version: 1.1.0
type: text
value: built by the release pipeline
---
```

Markdown answers keep their text in the body and have no `value`.

### Rules for a workspace's `main`

Besides the format rules above (`format`, `path`, `previous`, `history`), a
push to a workspace's `main` is checked against the server's catalog:

| Rule | Meaning |
| --- | --- |
| `workspace-id` | `workspace` in `custos.yaml` is the id of the repository pushed to. |
| `pin` | `catalog.commit` is a commit on the catalog's `main`, the current one or an older one. |
| `answer` | Each answer names a task of the pinned catalog or a generated task, a `task_version` that exists, the task's answer type, and for `choice` one of its choices. |

`custos validate` in a workspace checkout checks the workspace on its own;
these three rules are checked on push.

## Catalog updates and freezing

When the catalog's `main` advances, custos brings every workspace up to
date. It does so right after each push to `catalog.git`, right after each
push to a workspace (so a `frozen: false` pushed by hand, or a proposal
merged with plain Git, takes effect at once instead of waiting for the next
catalog push), and once when the server starts.

- An **unfrozen** workspace gets a commit by `custos-bot` on its `main`
  that moves `catalog.commit` to the new catalog commit. Existing answers
  stay in effect; tasks with a newer version show as *pending update*.
- A **frozen** workspace keeps its pin. custos opens a **pin proposal**
  instead: the branch `custos/pin/<catalog-commit>`, with one commit that
  changes the pin. A newer catalog commit replaces the open proposal.
  Accepting it merges it into `main` (and is checked like any change of
  `main`); rejecting it deletes it, and custos does not open it again until
  the catalog moves on.

Freeze a workspace when its documentation must not move, for example
during an audit:

```sh
A='X-Custos-Author: Jane Doe <jane@example.org>'
W=http://127.0.0.1:8080/api/workspaces/5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9
curl -X POST -H "$A" $W/freeze
curl $W/pin-diff        # pinned catalog commit compared with catalog main
curl $W/proposals       # open pin proposals and what each one changes
curl -X POST -H "$A" -H 'Content-Type: application/json' \
     -d '{"branch":"custos/pin/<catalog-commit>"}' $W/proposals/accept
curl -X POST -H "$A" $W/unfreeze   # the pin moves to catalog main right away
```

| Endpoint | Purpose |
| --- | --- |
| `POST /api/workspaces/{id}/freeze` | commit `frozen: true` |
| `POST /api/workspaces/{id}/unfreeze` | commit `frozen: false`, then move the pin and delete the proposals |
| `GET /api/workspaces/{id}/proposals` | open proposals: `branch`, `commit`, `from`, `to`, `changes` |
| `POST /api/workspaces/{id}/proposals/accept` | body `{"branch"}`; fast-forward or merge commit into `main` |
| `POST /api/workspaces/{id}/proposals/reject` | body `{"branch"}`; delete the proposal |
| `GET /api/workspaces/{id}/pin-diff` | `from` (pin), `to` (catalog main), `changes` |

`changes` lists `new_versions`, `added` and `superseded` tasks and tells
whether groups (`groups_changed`) or processors and bindings
(`bindings_changed`) differ. Write requests need the `X-Custos-Author`
header.

A proposal is a normal branch, so it can also be merged with Git: fetch
it, merge it into `main` and push. custos reconciles the workspace right
after that push and deletes the branch once the pin on `main` has reached
the catalog's `main`.

custos rewrites `custos.yaml` when it moves a pin or freezes a workspace;
comments in that file are not kept. A workspace whose `custos.yaml` cannot
be read is skipped and reported on the server's standard error; the other
workspaces are still updated.

## Command line

```sh
custos validate [--against REV] [DIR]                    # check a catalog or workspace checkout
custos task new-version --minor TASK-UUID                # write the next version (or --patch, --major)
custos workspace create --author "Jane Doe <jane@example.org>" WORKSPACE-UUID
                                                         # create a workspace pinned to the catalog
custos serve [--data-dir DIR] [--addr ADDR] [--public-url URL]
                                                         # serve the repositories over HTTP
custos clone URL DIR                                     # git clone, then download the attachments
custos push [--dir DIR] [--author "NAME <EMAIL>"] [REMOTE [GIT-PUSH-ARGS...]]
                                                         # upload attachments, then git push
custos processor test IMAGE --answer FILE [--previous DIR] [...]
                                                         # run a processor locally, print its proposal
```

`workspace create` and `serve` need `--data-dir` or `CUSTOS_DATA_DIR`.
`workspace create` also needs `--author` or `CUSTOS_AUTHOR`, the author of
the workspace's first commit.

In the root of a git checkout, `custos validate` checks the staged content
(the git index), which is what the next commit will hold: untracked and
ignored files such as `.DS_Store` and unstaged edits are not checked. In any
other directory it checks the files on disk.

`custos validate --against origin/main` also reports task versions you
changed that are already published; it fits a pre-commit hook. Run it in the
repository root.

## Running

Requires Go 1.26 and git 2.38 or later, and Docker or Podman on the
server's host to run processors.

```sh
make build
./custos serve --data-dir ./data
git push http://127.0.0.1:8080/git/catalog.git main
```

Repositories are served at `/git/catalog.git` and
`/git/workspaces/<uuid>.git`; `GET /healthz` answers `ok` while the server
runs. **There is no authentication yet**, so custos listens on
`127.0.0.1:8080` by default.

Push the catalog first; a workspace can only be created once the catalog
has a `main` branch. Creating a workspace makes its first commit, a
`custos.yaml` pinned to the catalog's current `main`, so clone it and push
your answers on top:

```sh
./custos workspace create --data-dir ./data --author "Jane Doe <jane@example.org>" \
    5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9
git clone http://127.0.0.1:8080/git/workspaces/5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9.git
```

| Flag | Environment | Default |
| --- | --- | --- |
| `--data-dir` | `CUSTOS_DATA_DIR` | required |
| `--addr` | `CUSTOS_ADDR` | `127.0.0.1:8080` |
| `--public-url` | `CUSTOS_PUBLIC_URL` | `http://127.0.0.1:8080` |
| `--author` (`workspace create`) | `CUSTOS_AUTHOR` | required |

The flags for processors are listed under [Processors](#processors).

`--public-url` is the address clients reach the server at. custos writes it
into the `catalog.url` of new workspaces, so set it whenever the server is
reached under another address than the default, for example behind a proxy
or with another `--addr`.

At start, `serve` reports workspaces whose `main` breaks the rules, for
example after an edit on disk, and serves them anyway.

Flags win over environment variables. Run the built binary rather than
`go run`: the repositories' hooks call the binary that started the server.

## REST API

The server answers JSON below `/api`. Every request that changes something
names its author in the header `X-Custos-Author: Name <email>`; that person
becomes the author of the commit, `custos-bot` its committer. There is no
authentication yet (see Running).

| Method and path | Purpose |
| --- | --- |
| `GET /api/workspaces` | workspaces with their pinned catalog commit and frozen flag |
| `POST /api/workspaces` | create a workspace pinned to the catalog's `main`; body `{"id": "<uuid>"}`, or none for a new id |
| `GET /api/workspaces/{id}/status` | every task with its state (`unanswered`, `answered`, `pending-update`), its answer and the answers in effect |
| `GET /api/workspaces/{id}/book` | the book as Markdown |
| `GET /api/workspaces/{id}/answers/{task}` | the answer to a task |
| `PUT /api/workspaces/{id}/answers/{task}` | write the answer, as one commit on `main` |
| `POST /api/workspaces/{id}/answers/{task}/still-valid` | keep the answer for the task's current version |
| `POST /api/blobs` | upload an attachment (raw body, at most 1 GiB); returns `{"sha256", "size"}` |
| `GET /api/blobs/{sha256}` | download an attachment |
| `GET /api/catalog` | groups and current task versions of the catalog's `main` |
| `GET /api/catalog/tasks/{id}` | all versions of a task |

An answer is written for the task's current version unless `task_version`
names another one. Markdown answers use `body`, all other types `value`:

```sh
curl -X PUT -H 'X-Custos-Author: Jane Doe <jane@example.org>' \
  -H 'Content-Type: application/json' \
  -d '{"value": "built by the release pipeline"}' \
  http://127.0.0.1:8080/api/workspaces/<workspace-uuid>/answers/<task-uuid>
```

Attachments are uploaded first and then listed in the answer as
`"attachments": [{"name": "scan.pdf", "sha256": "<hash>", "media_type": "application/pdf"}]`.
Answers show `"available": false` for an attachment whose file the server
does not have.

Errors come as `{"error": "...", "problems": [{"path", "rule", "message"}]}`
with 400 for a malformed request, 401 without a valid `X-Custos-Author`, 404
for unknown workspaces, tasks and blobs, 409 when the workspace already
exists or `main` moved meanwhile, 413 for oversized bodies, and 422 when the
answer breaks a rule, the same rules as for a push.

## Attachments, clone and push

Attachments are stored on the server by their SHA-256 hash, outside Git, in
`<data-dir>/blobs`. A plain `git clone` does not include them;
`custos clone` does:

```sh
custos clone http://127.0.0.1:8080/git/workspaces/<workspace-uuid>.git ws
```

It downloads the attachments of the answers on the checked-out branch into
`ws/.custos/blobs/sha256/`, which Git ignores, and warns about attachments
the server does not have.

To add an attachment in a checkout, copy the file there under its hash and
reference it in the answer:

```sh
cd ws
sha=$(shasum -a 256 scan.pdf | cut -d' ' -f1)
mkdir -p .custos/blobs/sha256 && cp scan.pdf .custos/blobs/sha256/$sha
# add to answers/<task-uuid>.md:
#   attachments:
#     - name: scan.pdf
#       sha256: <the hash>
#       media_type: application/pdf
git commit -am "Attach the scan"
custos push
```

`custos push` uploads the attachments referenced in the commits the remote
does not have yet, then runs `git push`. It pushes nothing when an attachment
is neither in `.custos/blobs/sha256/` nor on the server. Uploads are made in
the name of `--author`, `CUSTOS_AUTHOR`, or else the checkout's
`user.name` and `user.email`.

## Fork and merge

**Fork** a workspace to start a new one from its history, for example for
the next release of a product. The fork's `main` is the source's `main`
plus one commit that sets the new id in `custos.yaml`. Branches are not
copied. Attachments live in one blob store shared by all workspaces, so
nothing is copied there.

```sh
curl -X POST -H 'X-Custos-Author: Jane Doe <jane@example.org>' \
  -d '{"id": "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"}' \
  http://127.0.0.1:8080/api/workspaces/5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9/fork
```

Leave out `id` to get a generated one; the answer is `201 {"id": "…"}`.

**Branches** of a workspace are for parallel or what-if work. Push any
branch other than `main` with Git; only `main` is validated.
`GET /api/workspaces/<id>/branches` lists them as `[{"name", "commit"}]`.
To bring a fork's work back, push its `main` to a branch of the original
workspace:

```sh
git push http://127.0.0.1:8080/git/workspaces/<original-id>.git main:from-fork
```

**Merge** a branch into `main`:

```sh
curl -X POST -H 'X-Custos-Author: Jane Doe <jane@example.org>' \
  -d '{"branch": "what-if"}' \
  http://127.0.0.1:8080/api/workspaces/<id>/merge
```

custos always records a merge commit, authored by you, and validates the
result like a push to `main` before `main` moves (`200 {"commit": "…"}`).
When a file changed on both sides, nothing changes and the answer is `409`
with the conflicts. `ours` is the file on `main`, `theirs` the file on the
branch, both base64-encoded, `null` when that side deleted the file:

```json
{"error": "…", "conflicts": [{"path": "answers/<task-uuid>.md", "kind": "answer", "ours": "LS0t…", "theirs": "LS0t…"}]}
```

Send the merge again with a resolution for every conflicting path: `ours`
keeps `main`'s version, `theirs` takes the branch's, `content` writes the
given base64 content.

```json
{"branch": "what-if", "resolutions": {"answers/<task-uuid>.md": {"side": "theirs"}}}
```

- `custos.yaml` is merged field by field. `main` keeps its workspace id, so
  a fork's work merges back. When both sides moved the pin, it moves to the
  newer catalog commit; when neither commit descends from the other,
  `custos.yaml` is a conflict like any other file.
- Generated tasks and documents in conflict keep `main`'s side, and custos
  runs the producing processors again on the merged answers; their output
  arrives as proposals (see [Processors](#processors)).
- `400` means a bad branch name or resolution (including one for a path
  without a conflict), `404` an unknown workspace or branch, `422` a merge
  result that breaks a rule. `409` without conflicts means `main` moved
  while the merge ran, or the branch shares no history with `main`; send
  the merge again after checking.
- A successful merge or fork through the API is followed at once by
  reconciling the new or changed workspace with the catalog, the same way
  a push is: the pin moves, or a pin proposal opens, without waiting for
  the next catalog update.

Merging needs git 2.38 or later (see [Running](#running)).

## Processors

A **processor** is a container image that reads an answer and produces
follow-up tasks (*generated tasks*) and documents such as in-toto
attestations. Processors are registered in the catalog's `processors.yaml`
by name, pinned by digest, and bound to catalog tasks:

```yaml
processors:
  host-scanner:
    image: registry.example.org/host-scanner@sha256:<64 hex digits>
    timeout: 60s          # default 60s
    network: false        # default false
    secrets: [slsa-signing-key]
bindings:
  3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f: host-scanner
```

### Runs

custos runs a processor on every answer of a bound task: a catalog task
with a binding at the workspace's pin, or a generated task that names a
registered processor. It looks for work after every answer saved through
the API, every push to a workspace, every pin move, every accepted
proposal, every merge, and once at start-up. An answer is run once per
processor digest: a run is identified by the workspace, the answer file's
content and the image digest, so changing the digest in the catalog (or a
binding) runs the processor again on every answer it applies to, while an
unchanged answer is not run twice.

Each run starts the image through `docker` or `podman` with a read-only
root filesystem, no capabilities, a memory limit, the processor's timeout,
no network unless `network: true`, the answer's attachments read-only at
`/input/blobs/<sha256>`, and its secrets read-only at `/run/secrets/<name>`
from the server's secrets directory. A run fails when the processor exits
with a non-zero status, runs out of time, or writes output that breaks the
contract; failed runs keep their log and can be retried.

Run records and logs are files below `<data-dir>/runs/<workspace-id>/`, not
Git. Deleting them makes custos run every bound answer once more.

### Output proposals

A run never changes `main`. custos matches the output against what the
same answer produced before (by `match_key`) and, when something differs,
writes a **proposal**: the branch
`custos/proposal/<task-uuid>/<digest-hex>`, one commit by `custos-bot`.
A newer run on the same task replaces the open proposal. Each item of a
proposal is one of

| Action | Meaning |
| --- | --- |
| `added` | a new generated task (version `1.0.0`, fresh UUID) or a new document |
| `new-version` | a generated task changed: next version by the item's `bump` (default `minor`), `previous` pointing to the old one; a document changed and is replaced |
| `unchanged` | nothing to do; documents compare on their statement, so re-signing alone is no change |
| `removed` | the processor no longer produces it: the generated task and its answer, or the document, are deleted |

Documents are verified when they are read from the output: `verified`
(signed by a key in `--trusted-keys` or a valid Sigstore bundle), `failed`,
or `unsigned`. The result is stored with the document.

Accepting a proposal applies all its items, or the ones you select, to
`main` in one commit authored by you. When the removal of a generated task
is accepted and other tasks or documents were generated from its answer,
custos opens a second proposal, `custos/proposal/<removed-task-uuid>/cascade`,
to remove those as well. A generated task with a `processor` runs that processor when
it is answered; at most 8 levels of generated tasks are allowed below a
catalog task (`--max-generation-depth`), and output that would go deeper
fails the run.

```sh
A='X-Custos-Author: Jane Doe <jane@example.org>'
W=http://127.0.0.1:8080/api/workspaces/5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9
curl $W/processor-proposals                       # open proposals with their items
curl -X POST -H "$A" -H 'Content-Type: application/json' \
     -d '{"items": ["task:host:web-01", "document:inventory"]}' \
     $W/processor-proposals/<task-uuid>/accept      # leave out "items" to accept everything
curl -X POST -H "$A" $W/processor-proposals/<task-uuid>/reject
curl $W/runs                                      # run history, newest first
curl $W/runs/<run-id>/log
curl -X POST -H "$A" $W/runs/<run-id>/retry      # failed runs only
```

| Method and path | Purpose |
| --- | --- |
| `GET /api/processors` | the registry at the catalog's `main`: `name`, `image`, `digest`, `timeout`, `network`, `secrets`, `bound_tasks` |
| `GET /api/workspaces/{id}/runs` | run records, newest first: `state` (`queued`, `running`, `succeeded`, `failed`), `outcome` (`proposed`, `unchanged`), `reason`, `branch`, `error` |
| `GET /api/workspaces/{id}/runs/{run}` | one run record |
| `GET /api/workspaces/{id}/runs/{run}/log` | the run's log (`text/plain`) |
| `POST /api/workspaces/{id}/runs/{run}/retry` | run a failed run again; `202` with the new record, `409` for runs that did not fail |
| `GET /api/workspaces/{id}/processor-proposals` | open output proposals with their items |
| `GET /api/workspaces/{id}/processor-proposals/{task}` | the open proposal of one task |
| `POST /api/workspaces/{id}/processor-proposals/{task}/accept` | body `{"items"?: ["task:<match_key>", "document:<match_key>"]}`; answers `{"commit"}`, plus `warning` when the commit was made but the proposal branch could not be deleted or a cascade proposal not opened |
| `POST /api/workspaces/{id}/processor-proposals/{task}/reject` | delete the proposal; `204` |
| `POST /api/processors/{name}/dry-run` | body `{"image": "<ref>@sha256:<hex>"}`; `202 {"id"}` |
| `GET /api/dry-runs/{id}` | `state`, `runs`, `failed`, `proposals`, `workspaces` and up to five `samples` |

A **dry run** runs a new image over every answer bound to the processor
name, in every workspace, before you change the digest in the catalog. It
reports how many proposals in how many workspaces the new image would
create, with samples, and commits nothing. Dry runs are kept in memory and
are gone after a restart.

**Merging** a branch whose generated tasks or documents conflict with
`main` no longer asks you to choose: the merge keeps `main`'s side of
those files and then runs the producing processors again on the merged
answers, so their output arrives as proposals.

`serve` flags for processors:

| Flag | Environment | Default |
| --- | --- | --- |
| `--container-runtime` | `CUSTOS_CONTAINER_RUNTIME` | `docker` (or `podman`, or a path to either) |
| `--secrets-dir` | `CUSTOS_SECRETS_DIR` | none: processors that need secrets fail |
| `--processor-memory` | `CUSTOS_PROCESSOR_MEMORY` | `512m` |
| `--processor-workers` | `CUSTOS_PROCESSOR_WORKERS` | `2` |
| `--max-generation-depth` | `CUSTOS_MAX_GENERATION_DEPTH` | `8` |
| `--trusted-keys` | `CUSTOS_TRUSTED_KEYS` | none: signed documents show as `failed` unless they carry a valid Sigstore bundle |

The secret `<name>` is the file `<secrets-dir>/<name>`. Containers run
without capabilities as the image's user, so a secret file must be
readable by that user (for example mode `0644` in a directory only the
server can enter); a secret the processor cannot read fails its run.
Trusted keys are the `*.pem` and `*.pub` public keys in the
`--trusted-keys` directory.

## Writing processors

A processor reads one JSON object on stdin and writes one on stdout
(contract `custos.processor/v1`); what it writes to stderr is the run's
log. It does not need to know how custos stores anything.

Input:

```json
{"contract": "custos.processor/v1",
 "workspace": {"id": "5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9"},
 "task": {"id": "3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f", "version": "1.1.0",
          "title": "List the hosts", "body": "…", "answer_type": "markdown"},
 "answer": {"task_version": "1.1.0", "value": null, "body": "- web-01\n- db-01\n",
            "attachments": [{"name": "scan.pdf", "sha256": "<hash>", "media_type": "application/pdf",
                             "path": "/input/blobs/<hash>"}]}}
```

`value` is `null` for markdown answers, whose text is in `body`; `choices`
appears in `task` for choice tasks.

Output:

```json
{"tasks": [{"match_key": "host:web-01", "title": "Patch level of web-01",
            "body": "When was web-01 last patched?", "answer_type": "timestamp",
            "origin": {"id": "<task uuid>", "version": "1.0.0"},
            "processor": "host-scanner", "bump": "minor"}],
 "documents": [{"match_key": "inventory", "name": "Host inventory",
                "media_type": "application/vnd.in-toto+json", "content": {}}]}
```

- `match_key` is required and unique among the tasks, and separately among
  the documents. It is how custos recognises an item in the next run on
  the same answer, so derive it from the subject (`host:web-01`), not from
  the position.
- Tasks need `title` and `answer_type` (`markdown`, `text`, `timestamp`,
  `path`, `url`, `choice`); `choices` is required for `choice` and not
  allowed otherwise. `origin` (a task UUID and optional version) places the
  task in the book; `processor` names a registered processor to run on its
  answers; `bump` (`patch`, `minor`, `major`, default `minor`) is how the
  version grows when the task changes.
- Documents need `name`, `media_type` and `content`, any JSON value but
  `null`: a bare in-toto statement, a DSSE envelope or a Sigstore bundle.
  Sign with a mounted secret, or keyless with `network: true`.
- Unknown fields, more than one JSON value, or more than 16 MiB of output
  fail the run, as does a non-zero exit status.

### SDKs

The SDKs read and check the input, call your function, check the output
with the same rules as the server (except whether a named processor is
registered), and write it. An error from your function, or invalid
output, ends the process with status 1 and the message on stderr.

**Go** — module `github.com/emeland-io/custos/sdk/go`, package
`processor`, standard library only:

```go
package main

import processor "github.com/emeland-io/custos/sdk/go"

func main() { processor.Main(process) }

func process(task processor.Task, answer processor.Answer) (processor.Output, error) {
	return processor.Output{Tasks: []processor.OutputTask{{
		MatchKey: "host:web-01", Title: "Patch level of web-01", AnswerType: processor.AnswerTimestamp,
	}}}, nil
}
```

`processor.Run(f, stdin, stdout)` does the same without exiting, for tests;
`Output.Validate` checks an output on its own. A complete example is
[sdk/go/examples/hostlist](sdk/go/examples/hostlist/main.go). Build it into
an image without a base:

```dockerfile
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /hostlist ./examples/hostlist

FROM scratch
COPY --from=build /hostlist /hostlist
USER 65532:65532
ENTRYPOINT ["/hostlist"]
```

Save these lines as `hostlist.Dockerfile` and build with
`docker build -t hostlist:dev -f hostlist.Dockerfile sdk/go`.

**Python** — the package `sdk/python/custos_processor`, standard library
only, Python 3.10 or later. Copy it next to your processor:

```python
from custos_processor import Output, OutputTask, run

def process(task, answer):
    return Output(tasks=[OutputTask(match_key="host:web-01", title="Patch level of web-01",
                                    answer_type="timestamp")])

if __name__ == "__main__":
    run(process)
```

`answer.text` is the value, or the body of a markdown answer.
`process_stream(process, stdin, stdout)` and `validate(output)` serve
tests. The Python twin of the Go example is
[sdk/python/examples/hostlist.py](sdk/python/examples/hostlist.py).

### Trying a processor: `custos processor test`

```sh
custos processor test IMAGE --answer FILE [--task FILE] [--previous DIR] [--blobs DIR]
    [--secrets-dir DIR] [--network] [--timeout D] [--trusted-keys DIR]
    [--workspace-id UUID] [--container-runtime NAME]
```

runs an image on one answer with the same container settings as the server
and prints the proposal it would produce, followed by the processor's log.
It needs Docker or Podman, but no server and no workspace.

- `IMAGE` is a local image such as `hostlist:dev`, or a reference pinned by
  digest as in `processors.yaml`.
- `--answer` is an answer file as in a workspace (`answers/<task>.md`).
- `--task` is the catalog's task version file. Without it, custos makes up
  a task from the answer: same id and version, title `Test task`, the
  answer's type.
- `--previous` is a directory laid out like a workspace, for example a
  workspace checkout; its `generated/` and `documents/` are the earlier
  output to match against, so you see `new-version`, `unchanged` and
  `removed` items as the server would.
- Attachments are taken from `--blobs`, a directory of files named by their
  SHA-256, or else from `.custos/blobs/sha256` of the checkout the answer
  file is in.
- Every file in `--secrets-dir` is mounted as a secret. With
  `--trusted-keys` documents are verified as on the server; without it they
  show as `unsigned`.

```text
$ custos processor test hostlist:dev --answer ws/answers/3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f.md --previous ws
Image:  hostlist:dev
Digest: sha256:<image id>
Task:   3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f@1.0.0

Proposal: 1 added, 0 new version, 0 removed, 2 unchanged
  unchanged  document  inventory           Host inventory (unsigned)
  added      task      host:db-01   1.0.0  Patch level of db-01
  unchanged  task      host:web-01  1.0.0  Patch level of web-01

Log: (empty)
```

Processor names and origins in the output cannot be checked without the
catalog; `processor test` lists them under "Assumed without the catalog".
The exit status is 0 when the run succeeded, 1 when it failed (the reason
is printed before the log) and 2 for wrong flags.

## Container image

```sh
make docker
docker run -d --name custos -p 127.0.0.1:9090:8080 -v custos-data:/data \
    -e CUSTOS_PUBLIC_URL=http://127.0.0.1:9090 custos:dev
git push http://127.0.0.1:9090/git/catalog.git main
docker exec custos custos workspace create --author "Jane Doe <jane@example.org>" \
    5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9
```

The image runs as UID 65532, keeps its repositories in the volume `/data`
(`CUSTOS_DATA_DIR=/data`), and listens on port 8080 inside the container.

To run processors, `custos serve` calls the `docker` or `podman` client.
The image does not contain one, so serve from the image only when no
processors are registered, or run `custos serve` on a host that has Docker
or Podman.

## Development

```sh
make test         # go vet and all tests, both SDKs included; needs git, Docker and python3
make dev-server   # serve ./tmp/data on 127.0.0.1:8080
```
