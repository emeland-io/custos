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
Forking and merging workspaces, processors and the web UI follow.

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
date. It does so right after each push to `catalog.git` and once when the
server starts.

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
it, merge it into `main` and push. custos deletes the branch once the pin
on `main` has reached the catalog's `main`.

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

Requires Go 1.26 and git 2.38 or later.

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

## Development

```sh
make test         # go vet and all tests; needs git on PATH
make dev-server   # serve ./tmp/data on 127.0.0.1:8080
```
