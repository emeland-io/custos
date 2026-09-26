# custos

custos manages small documentation fragments that describe tasks, and
collects the answers to those tasks together with the in-toto attestations
they require.

## Concepts

| Element | Stored in | Purpose |
| --- | --- | --- |
| **Root** | root dir | Named entry point to a tree; points to its entry Node. |
| **Node** | root dir | Inner element of the tree. Requires an in-toto attestation (predicate type, optional subject name and signer identities) and explains in Markdown why it is missing. Versioned. |
| **Leaf** | root dir | A task, described in Markdown, below a Node. Versioned. |
| **Seed** | work dir | One run over the tree of a Root. |
| **Shoot** | work dir | The answer to a Leaf within a Seed. |
| **Attestation** | work dir | An in-toto attestation uploaded for a Node within a Seed, with its verification result. |

A Seed reports which Leaves still lack an answer and which Nodes still lack a
verified attestation that meets their requirement.

Creating a **new version** of a Node or Leaf copies it under a new ID and
marks the old one superseded. Answers and attestations stay attached to the
version they were made for, so they show up as *stale* until they are
redone for the new version. Editing an element in place keeps its version.

Every element is one JSON file named `<uuid>.json`:

```text
<root-dir>/roots/  <root-dir>/nodes/  <root-dir>/leaves/
<work-dir>/seeds/  <work-dir>/shoots/  <work-dir>/attestations/
```

The root dir can be kept in git and edited outside custos; references that
no longer resolve are logged at startup and reported in the Seed status.

## Attestations

Uploads can be DSSE envelopes, Sigstore bundles, or bare in-toto statements
(stored as unsigned). Signatures are verified with the
[carabiner-dev](https://github.com/carabiner-dev) libraries, see
[docs/adr/0001-in-toto-library.md](docs/adr/0001-in-toto-library.md).

- Put trusted public keys (PEM or GPG) into the keys dir. The Keys page shows
  the identity spec of each key, such as `key::ed25519::<id>`.
- Sigstore bundles are verified offline against the embedded Sigstore trust
  root. Require a keyless signer with a spec such as
  `sigstore(identityMatch=prefix)::https://token.actions.githubusercontent.com::https://github.com/org/repo/`.

## Running

Requires Go 1.26 and Node.js 22.

```sh
make build
./custos --root-dir ./data/root --work-dir ./data/work
```

Then open <http://localhost:8080>.

| Flag | Environment | Default |
| --- | --- | --- |
| `--root-dir` | `CUSTOS_ROOT_DIR` | required |
| `--work-dir` | `CUSTOS_WORK_DIR` | required |
| `--keys-dir` | `CUSTOS_KEYS_DIR` | `<work-dir>/keys` |
| `--addr` | `CUSTOS_ADDR` | `:8080` |
| `--port` | `CUSTOS_PORT` | port of `--addr` (replaces only the port) |
| `--no-banner` | `CUSTOS_NO_BANNER` | `false` (hide the container startup message) |
| `--sigstore-online` | `CUSTOS_SIGSTORE_ONLINE` | `false` (allow Rekor lookups for keyless DSSE envelopes) |

Flags win over environment variables.

## Container image

Images for linux/amd64 and linux/arm64 are published to
`ghcr.io/emeland-io/custos` for every push to `main` (`main`, `latest`,
`sha-<commit>`) and every `v*` tag (`1.2.3`, `1.2`). They carry SLSA
provenance and an SBOM.

```sh
docker run -p 9090:8080 \
  -v custos-root:/data/root \
  -v custos-work:/data/work \
  ghcr.io/emeland-io/custos:latest
```

Then open <http://localhost:9090>. On startup the container prints these
instructions; hide them with `--no-banner`.

The image runs as a non-root user and keeps its data in two volumes: the
root dir `/data/root` and the work dir `/data/work`; trusted keys go into
`/data/work/keys`. To keep the root dir in a git checkout, bind-mount it
instead, for example `-v "$PWD/tree:/data/root"`; it must be writable by
UID 65532. To listen on another port inside the container, pass
`--port <port>` and adjust the right side of `-p`. All settings can also be
changed through the `CUSTOS_*` variables.

`make docker` builds the image locally.

## Development

```sh
make dev-server   # API on :8080, data in ./tmp
make dev-web      # Vite on :5173, proxies /api
make test         # go vet, go test, frontend type check
```

The API lives below `/api`; see [internal/api/api.go](internal/api/api.go)
for the routes.
