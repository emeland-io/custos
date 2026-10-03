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
that catalog. Distributing catalog updates to workspaces, the REST API,
processors and the web UI follow.

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

## Command line

```sh
custos validate [--against REV] [DIR]                    # check a catalog or workspace checkout
custos task new-version --minor TASK-UUID                # write the next version (or --patch, --major)
custos workspace create --author "Jane Doe <jane@example.org>" WORKSPACE-UUID
                                                         # create a workspace pinned to the catalog
custos serve [--data-dir DIR] [--addr ADDR] [--public-url URL]
                                                         # serve the repositories over HTTP
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
