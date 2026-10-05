# ADR 0001: Use the carabiner-dev libraries for in-toto attestations

- Status: accepted
- Date: 2026-09-23

## Context

custos needs to parse in-toto attestations uploaded for a Node, verify their
signatures, and check them against the Node's requirement: predicate type,
subject name, and the identities that must have signed. We evaluated the Go
libraries from https://github.com/carabiner-dev against these needs, using
the offline test suite in `internal/attest/carabiner/eval_test.go`.

Modules used:

| Module | Version | Used for |
|---|---|---|
| `github.com/carabiner-dev/collector` | v0.3.17 | `envelope.Parsers` parses DSSE envelopes, Sigstore bundles and bare statements |
| `github.com/carabiner-dev/signer` | v0.6.2 | `Verifier.VerifyStatementBytes` verifies DSSE (keys) and Sigstore bundles; `key` loads and generates keys; `api/v1` identity specs and matching |
| `github.com/carabiner-dev/attestation` | v0.2.1 | interfaces (indirect) |

## Evaluation

All checks pass with network access blocked (`HTTPS_PROXY` pointed at a
closed port):

| Check | Result |
|---|---|
| Inspect a DSSE envelope: format, predicate type, subjects | pass |
| DSSE signed with ed25519 / ECDSA, trusted key | VERIFIED |
| DSSE signed with an untrusted key | FAILED |
| DSSE with no trusted keys configured | UNVERIFIABLE |
| DSSE with signatures removed, and bare statements | UNSIGNED |
| DSSE with a modified payload | FAILED |
| Wrong predicate type, subject name mismatch, missing required identity, invalid identity spec | requirement not met, with a readable message |
| Keyless Sigstore bundle (SLSA provenance of bnd v0.4.6) | VERIFIED offline using the embedded trust root; the signer identity `sigstore::https://token.actions.githubusercontent.com::https://github.com/carabiner-dev/bnd/.github/workflows/release.yaml@refs/tags/v0.4.6` matched a `sigstore(identityMatch=prefix)::…` spec, and a foreign identity did not |
| Sigstore bundle with a modified payload | FAILED |
| Loading PEM public keys from a directory | pass |
| Non-attestation JSON rejected | pass, after our own check (see below) |

Cost, measured with a probe binary that links only the verifier (Go 1.26.5,
darwin/arm64):

| Metric | Value |
|---|---|
| Binary size | 36.0 MiB (24.7 MiB with `-ldflags='-s -w'`) |
| Linked packages / modules | 837 / 134 |
| `go mod graph` edges | 4783 |
| Cold build (empty build cache) | 11 s wall clock |

## Findings

1. **No upstream changes were needed.** Everything custos needs is exposed
   by public APIs.
2. **Signer identities are matched after verification.** The bundle verifier
   insists on an expected issuer and SAN unless `WithSkipIdentityCheck` is
   set. We skip that check and match the Node's `requiredIdentities` against
   the returned identities with `Verification.MatchesIdentity`. This
   handles keys and Sigstore identities in the same way.
3. **The bare parser accepts any JSON.** A JSON document that is not a
   statement comes back wrapped in a synthetic statement. custos checks
   bare uploads for `_type` and `predicateType` itself.
4. **Trust roots refresh through TUF.** The embedded Sigstore trust root is
   used without network access only while it is less than 30 days old. After
   that, constructing a `signer.Verifier` tries a TUF refresh. Offline, this
   takes about 1.5 s and logs a warning, then falls back to the embedded
   copy. custos builds one verifier per process. Upgrading signer
   refreshes the embedded copy.
5. **Logging goes through logrus**, not `log/slog`.
6. **Maturity risk.** All modules are pre-1.0, come from one vendor and have
   little adoption, so expect API changes on upgrades.

## Decision

Use the carabiner-dev libraries (go criteria met: all checks pass without
patches, binary well under 80 MB). All use is confined to
`internal/attest/carabiner` behind the `attest.Verifier` interface, and the
eval tests run against that interface. If an upgrade breaks us, a backend
built on in-toto-golang, go-securesystemslib/dsse and sigstore-go can
replace it without changes elsewhere.

AMPEL policy evaluation (`ampel/pkg/verifier`) is a possible later addition
for requirements on predicate content. It is not used yet.

## Use in phase 3 (2026-10-04)

The phase-0 code was removed in `244f9de`. Phase 3 verifies the documents
that processors output and uses only `github.com/carabiner-dev/signer`
(v0.6.2): `signer.ParseArtifact` parses the content once into the
library's own artifact type, and `(*signer.Verifier).VerifyStatement` is
then called on that same parsed artifact — not `VerifyStatementBytes`,
which verifies from raw bytes but does not hand back the parsed envelope
it checked. `key.Parser` loads the trusted keys. The interface is now
`attest.Verifier` with `Verify(content) Result`; requirement checks against
predicate types, subjects and identities are gone, since custos no longer
has Nodes. The library's "unverifiable" is reported as `failed`.

The verified payload comes only from the artifact the library itself
checked; a second, independent parser must never supply it. An earlier
round of this plan took `Result.Payload` from custos's own separate parse
of `content`, which let a payload the library never saw (reached through
ambiguous or duplicate JSON keys) be reported as `verified` under the
signature covering a different payload — a signature-verification bypass.
The fix was to derive `Payload` from the DSSE envelope held by the very
`signer.SignedArtifact` that `VerifyStatement` checked (its
`EnvelopeArtifact.Envelope` or `BundleArtifact.Bundle`), which is why
`ParseArtifact`/`VerifyStatement` replaced `VerifyStatementBytes` here.
`TestVerifyNeverPairsVerifiedWithAnotherPayload` in
`internal/attest/carabiner/carabiner_test.go` is the regression test for
this rule.

Tests: `internal/attest/carabiner/carabiner_test.go`, which signs DSSE
envelopes with the standard library and keeps the bnd v0.4.6 bundle
fixture. Rulings: §11.3 of the design spec.
