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
This version implements its first phase: file formats, validation, Git over
HTTP and the command line. Distributing catalog updates to workspaces,
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
custos validate [--against REV] [DIR]     # check a catalog or workspace checkout
custos task new-version --minor TASK-UUID # write the next version of a task
custos workspace create WORKSPACE-UUID    # create an empty workspace repository
custos serve                              # serve the repositories over HTTP
```

`custos validate --against origin/main` also reports task versions you
changed that are already published; it fits a pre-commit hook. Run it in the
repository root.

## Running

Requires Go 1.26 and git 2.28 or later.

```sh
make build
./custos serve --data-dir ./data
git push http://127.0.0.1:8080/git/catalog.git main
```

Repositories are served at `/git/catalog.git` and
`/git/workspaces/<uuid>.git`. **There is no authentication yet**, so custos
listens on `127.0.0.1:8080` by default.

| Flag | Environment | Default |
| --- | --- | --- |
| `--data-dir` | `CUSTOS_DATA_DIR` | required |
| `--addr` | `CUSTOS_ADDR` | `127.0.0.1:8080` |

Flags win over environment variables. Run the built binary rather than
`go run`: the repositories' hooks call the binary that started the server.

## Container image

```sh
make docker
docker run -p 127.0.0.1:9090:8080 -v custos-data:/data custos:dev
```

The image runs as UID 65532, keeps its repositories in the volume `/data`,
and listens on port 8080 inside the container.

## Development

```sh
make test         # go vet and all tests; needs git on PATH
make dev-server   # serve ./tmp/data on 127.0.0.1:8080
```
