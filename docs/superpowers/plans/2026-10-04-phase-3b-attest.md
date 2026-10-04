# Phase 3b (Attestation verification) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** custos can tell, for any document a processor outputs, whether it is `verified`, `failed` or `unsigned`, who signed it, and which canonical payload to compare it on — through the interface `attest.Verifier`, with the carabiner-dev signer library as the only backend.

**Architecture:** `internal/attest` (standard library only) defines the statuses, `Result`, the `Verifier` interface, `Canonical`, the no-check verifier `Unverified`, and `Unwrap`, which tells DSSE envelopes, Sigstore bundles and everything else apart and extracts the payload. `internal/attest/carabiner` is the only package that imports carabiner-dev: `New(keysDir)` loads the trusted public keys, and `Verify` hands envelopes and bundles to one process-wide `signer.Verifier` (offline: no Rekor, embedded Sigstore trust root) and maps its conclusion onto the three statuses. Plans 3c–3e use only `attest.Verifier`; plan 3d constructs `carabiner.New(--trusted-keys)`.

**Tech Stack:** Go 1.26, `github.com/carabiner-dev/signer` v0.6.2 (newest release on 2026-10-04); standard library otherwise.

**Spec:** [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md), §3.3 (document files: `verification` with `verified`, `failed`, `unsigned` and signer identities), §5.4 (documents compare on the payload, not on envelope or signature bytes), §5.6 (signing and verification), §11 (rulings override earlier sections). Shared names and conventions: [2026-10-04-phase-3-architecture.md](2026-10-04-phase-3-architecture.md), section "Plan 3b". Library choice: [ADR 0001](../../adr/0001-in-toto-library.md).

**Requires:** nothing from plan 3a. This plan's tests sign their DSSE envelopes in-process with the standard library instead of using `proctest.SigningKey` (see "Decisions beyond the architecture note"); they need no Docker. The working calls are taken from the phase-0 code removed in `244f9de` (`git show 244f9de^:internal/attest/carabiner/carabiner.go`).

## Global Constraints

- Module `github.com/emeland-io/custos`, Go 1.26 (`go 1.26.0` in `go.mod`; it stays unchanged — signer v0.6.2 requires `go 1.26.0`).
- Third-party modules: the existing `go.yaml.in/yaml/v3`, `golang.org/x/mod`, `github.com/google/uuid`, plus — only imported in `internal/attest/carabiner` — `github.com/carabiner-dev/signer` (ADR 0001). Nothing else is imported directly. `internal/attest` itself uses the standard library only.
- Exported names exactly as in the architecture note: `attest.StatusVerified = "verified"`, `attest.StatusFailed = "failed"`, `attest.StatusUnsigned = "unsigned"`, `attest.Result{Status string; Signers []string; Payload []byte}`, `attest.Verifier` with `Verify(content []byte) Result`, `attest.Canonical(data []byte) ([]byte, error)`, `attest.Unverified Verifier`, `carabiner.New(keysDir string) (*Verifier, error)`, `(*carabiner.Verifier).Verify(content []byte) attest.Result`.
- `Verify` never fails and never panics: unreadable input is `unsigned` with `Payload` = canonical JSON of content.
- `Signers` holds identities of verified signers, sorted; empty unless `verified`.
- `Payload` is the canonical JSON of the statement (or of the content when it is not an attestation); documents compare on it (§5.4).
- Sigstore bundles verify offline with the embedded trust root (ADR 0001 findings 2 and 4); one `signer.Verifier` per process.
- Commit messages: one imperative sentence without prefix, then a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **A processor signs the same statement again on every run** (ECDSA signatures differ each time; a bundle gets a new certificate). The document must count as unchanged: the payload is the canonical statement, equal for bare, signed and re-signed forms (tests `TestSamePayloadWhetherSignedOrNot` in Task 2 and `TestPayloadIsTheStatement` in Task 3).
2. **custos runs without `--trusted-keys` and a processor signs with a key.** The document must not look `verified`, and must not look `unsigned` either (it is signed): it is `failed` (test case "no trusted keys" in `TestVerifyDSSE`, Task 3).
3. **The trusted-keys directory is a mounted Kubernetes secret or holds other files** (symlinked key files, a README, subdirectories). Keys must load, other files must be ignored, and a broken key must stop start-up with the file name in the error (tests `TestNewLoadsKeys` and `TestNewErrors`, Task 3).
4. **Two run workers verify at the same time** (plan 3d runs two workers by default). No data race, same results (test `TestConcurrentVerify` under `-race`, Task 3).
5. **A processor outputs a broken envelope** — a signature that is not base64 or not a valid signature, a payload that is not base64 or not JSON. `Verify` must return `failed` (or `unsigned` when there are no signatures) and a payload, never panic or error (cases "garbage signature" and "signature that is not base64" in Task 3; "payload that is not JSON/base64" in Task 2).

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/attest/canonical.go` | `Canonical` |
| `internal/attest/attest.go` | Package doc, statuses, `Result`, `Verifier`, `Unverified` |
| `internal/attest/unwrap.go` | `Unwrap`: kind of content, signature count, payload |
| `internal/attest/*_test.go` | Tests of the above (standard library only) |
| `internal/attest/carabiner/carabiner.go` | `New` (trusted keys), `Verify` (signer library, status mapping, signers) |
| `internal/attest/carabiner/carabiner_test.go` | DSSE with ed25519/ECDSA keys signed in-process, bare statements, tampering, key loading, the Sigstore bundle, concurrency |
| `internal/attest/carabiner/testdata/bnd-v0.4.6-provenance.bundle.json` | Sigstore bundle fixture, restored from `244f9de^` |
| `go.mod`, `go.sum` | `github.com/carabiner-dev/signer v0.6.2` and its dependencies |
| `docs/adr/0001-in-toto-library.md` | Note on how phase 3 uses the library |

---

### Task 1: Canonical JSON

**Files:**
- Create: `internal/attest/canonical.go`
- Test: `internal/attest/canonical_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func Canonical(data []byte) ([]byte, error)` in package `attest` — sorted object keys, no insignificant whitespace, numbers as written (`json.Number`), `<`, `>`, `&` not escaped, last duplicate key wins; error on invalid JSON or trailing data.

- [ ] **Step 1: Write the failing test** `internal/attest/canonical_test.go`

```go
package attest

import "testing"

func TestCanonical(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"b":1,"a":2}`, `{"a":2,"b":1}`},
		{" {\n  \"z\": [ 3, {\"y\":true,\"x\":null} ],\n  \"a\": \"s\"\n}\n", `{"a":"s","z":[3,{"x":null,"y":true}]}`},
		{`{"n":1.50,"m":1e2,"big":123456789012345678901234567890}`, `{"big":123456789012345678901234567890,"m":1e2,"n":1.50}`},
		{`{"html":"<a&b>","esc":"é\n"}`, `{"esc":"é\n","html":"<a&b>"}`},
		{`"text"`, `"text"`},
		{`[]`, `[]`},
		{`{"k":1,"k":2}`, `{"k":2}`},
	} {
		got, err := Canonical([]byte(tc.in))
		if err != nil {
			t.Errorf("Canonical(%s): %v", tc.in, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("Canonical(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestCanonicalRejectsInvalidJSON(t *testing.T) {
	for _, in := range []string{``, `not json`, `{"a":1} {"b":2}`, `{"a":1`, `{"a":1}x`} {
		if got, err := Canonical([]byte(in)); err == nil {
			t.Errorf("Canonical(%q) = %s, want an error", in, got)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/attest/`
Expected: FAIL — build error `undefined: Canonical`.

- [ ] **Step 3: Implement** `internal/attest/canonical.go`

```go
package attest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Canonical re-encodes JSON with sorted object keys and no insignificant
// whitespace (numbers kept as written). Error on invalid JSON.
//
// Strings are re-escaped the way encoding/json writes them, except that
// <, > and & stay as they are; of duplicate object keys the last one wins.
func Canonical(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("canonical JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("canonical JSON: unexpected data after the JSON value")
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("canonical JSON: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/attest/`
Expected: PASS (`ok  github.com/emeland-io/custos/internal/attest`).

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/attest   # prints nothing
git add internal/attest/canonical.go internal/attest/canonical_test.go
git commit -m "Add canonical JSON encoding for comparing processor documents

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Verification results, `Unwrap` and `Unverified`

**Files:**
- Create: `internal/attest/attest.go`
- Create: `internal/attest/unwrap.go`
- Test: `internal/attest/unwrap_test.go`

**Interfaces:**
- Consumes: `Canonical` (Task 1).
- Produces (package `attest`):
  - `const StatusVerified = "verified"`, `StatusFailed = "failed"`, `StatusUnsigned = "unsigned"`
  - `type Result struct { Status string; Signers []string; Payload []byte }`
  - `type Verifier interface { Verify(content []byte) Result }`
  - `var Unverified Verifier` — `Status` always `"unsigned"`, `Signers` nil, `Payload` = `Unwrap(content).Payload`
  - `const KindBare = "bare"`, `KindDSSE = "dsse"`, `KindBundle = "sigstore-bundle"`
  - `type Unwrapped struct { Kind string; Signatures int; Payload []byte }`
  - `func Unwrap(content []byte) Unwrapped` — never fails. DSSE envelope = JSON object with string fields `payloadType` and `payload`; Sigstore bundle = JSON object whose `mediaType` starts with `application/vnd.dev.sigstore.bundle` and that has an object `dsseEnvelope`; anything else is bare. `Payload` = canonical JSON of the base64-decoded envelope payload (standard or URL-safe alphabet, padded or not) when that is JSON; else canonical JSON of content; else content itself.

- [ ] **Step 1: Write the failing test** `internal/attest/unwrap_test.go`

```go
package attest

import (
	"encoding/base64"
	"fmt"
	"testing"
)

// statement is an in-toto statement written with unsorted keys and
// whitespace; statementCanonical is its canonical form.
const (
	statement          = `{ "predicateType": "https://example.org/custos-test/v1", "_type": "https://in-toto.io/Statement/v1", "subject": [{"name": "a", "digest": {"sha256": "00"}}], "predicate": {} }`
	statementCanonical = `{"_type":"https://in-toto.io/Statement/v1","predicate":{},"predicateType":"https://example.org/custos-test/v1","subject":[{"digest":{"sha256":"00"},"name":"a"}]}`
)

func dsse(payload string, enc *base64.Encoding, signatures string) string {
	return fmt.Sprintf(`{"payloadType":"application/vnd.in-toto+json","payload":%q,"signatures":%s}`,
		enc.EncodeToString([]byte(payload)), signatures)
}

func TestUnwrap(t *testing.T) {
	sigs := `[{"keyid":"","sig":"c2ln"}]`
	for _, tc := range []struct {
		name, content string
		kind          string
		signatures    int
		payload       string
	}{
		{"bare statement", statement, KindBare, 0, statementCanonical},
		{"not an attestation", `{"hello": "world"}`, KindBare, 0, `{"hello":"world"}`},
		{"JSON string", `"just text"`, KindBare, 0, `"just text"`},
		{"not JSON", `not json`, KindBare, 0, `not json`},
		{"signed envelope", dsse(statement, base64.StdEncoding, sigs), KindDSSE, 1, statementCanonical},
		{"URL-safe unpadded payload", dsse(statement, base64.RawURLEncoding, sigs), KindDSSE, 1, statementCanonical},
		{"envelope without signatures", dsse(statement, base64.StdEncoding, `[]`), KindDSSE, 0, statementCanonical},
		{"envelope with null signatures", dsse(statement, base64.StdEncoding, `null`), KindDSSE, 0, statementCanonical},
		{
			"envelope with a payload that is not JSON",
			dsse("plain text", base64.StdEncoding, sigs), KindDSSE, 1,
			`{"payload":"cGxhaW4gdGV4dA==","payloadType":"application/vnd.in-toto+json","signatures":[{"keyid":"","sig":"c2ln"}]}`,
		},
		{
			"envelope with a payload that is not base64",
			`{"payloadType":"application/vnd.in-toto+json","payload":"%%%","signatures":[]}`, KindDSSE, 0,
			`{"payload":"%%%","payloadType":"application/vnd.in-toto+json","signatures":[]}`,
		},
		{
			"bundle",
			`{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json","verificationMaterial":{},"dsseEnvelope":` + dsse(statement, base64.StdEncoding, sigs) + `}`,
			KindBundle, 1, statementCanonical,
		},
		{
			"mediaType that is not a bundle",
			`{"mediaType":"application/json","dsseEnvelope":` + dsse(statement, base64.StdEncoding, sigs) + `}`,
			KindBare, 0, "",
		},
		{"payloadType without payload", `{"payloadType":"x","signatures":[]}`, KindBare, 0, `{"payloadType":"x","signatures":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := Unwrap([]byte(tc.content))
			if u.Kind != tc.kind || u.Signatures != tc.signatures {
				t.Errorf("kind %q signatures %d, want %q %d", u.Kind, u.Signatures, tc.kind, tc.signatures)
			}
			want := tc.payload
			if want == "" {
				c, err := Canonical([]byte(tc.content))
				if err != nil {
					t.Fatal(err)
				}
				want = string(c)
			}
			if string(u.Payload) != want {
				t.Errorf("payload\n got %s\nwant %s", u.Payload, want)
			}
		})
	}
}

func TestSamePayloadWhetherSignedOrNot(t *testing.T) {
	bare := Unwrap([]byte(statement)).Payload
	signed := Unwrap([]byte(dsse(statement, base64.StdEncoding, `[{"sig":"c2ln"}]`))).Payload
	resigned := Unwrap([]byte(dsse(statement, base64.StdEncoding, `[{"sig":"b3RoZXI="}]`))).Payload
	if string(bare) != string(signed) || string(signed) != string(resigned) {
		t.Errorf("payloads differ:\n%s\n%s\n%s", bare, signed, resigned)
	}
}

func TestUnverified(t *testing.T) {
	for _, content := range []string{statement, dsse(statement, base64.StdEncoding, `[{"sig":"c2ln"}]`), `not json`} {
		r := Unverified.Verify([]byte(content))
		if r.Status != StatusUnsigned || len(r.Signers) != 0 {
			t.Errorf("%s: status %q signers %q", content, r.Status, r.Signers)
		}
		if want := Unwrap([]byte(content)).Payload; string(r.Payload) != string(want) {
			t.Errorf("%s: payload %s, want %s", content, r.Payload, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/attest/`
Expected: FAIL — build errors `undefined: Unwrap`, `undefined: KindBare`, `undefined: Unverified`, `undefined: StatusUnsigned`.

- [ ] **Step 3: Write** `internal/attest/attest.go`

```go
// Package attest defines how custos verifies the documents processors
// output (in-toto statements, DSSE envelopes, Sigstore bundles) and how it
// compares them. The signature checks live in the backend subpackage
// carabiner, behind the Verifier interface (ADR 0001); everything here uses
// the standard library only.
package attest

// Verification states, as stored in a document's verification.status.
const (
	StatusVerified = "verified" // signed by a trusted key or a valid Sigstore bundle
	StatusFailed   = "failed"   // signatures present but none verifies, or payload tampered
	StatusUnsigned = "unsigned" // bare statement, envelope without signatures, or not an attestation
)

// Result is the outcome of verifying one document's content.
type Result struct {
	Status  string
	Signers []string // identities of verified signers, sorted; empty unless verified
	Payload []byte   // canonical JSON of the statement (or of the content when it is not an attestation); documents compare on it (§5.4)
}

// Verifier checks the signatures of a document's content.
type Verifier interface {
	Verify(content []byte) Result // never fails: unreadable input is Unsigned with Payload = canonical JSON of content
}

// Unverified is a Verifier that never checks signatures: Status is
// "unsigned" for everything, Payload as above. processor test uses it when no
// trusted keys are given.
var Unverified Verifier = unverified{}

type unverified struct{}

func (unverified) Verify(content []byte) Result {
	return Result{Status: StatusUnsigned, Payload: Unwrap(content).Payload}
}
```

- [ ] **Step 4: Write** `internal/attest/unwrap.go`

```go
package attest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// Kinds of content, as reported by Unwrap.
const (
	KindBare   = "bare"            // anything that is neither a DSSE envelope nor a Sigstore bundle
	KindDSSE   = "dsse"            // a DSSE envelope
	KindBundle = "sigstore-bundle" // a Sigstore bundle that wraps a DSSE envelope
)

// bundleMediaTypePrefix starts the mediaType of every Sigstore bundle
// version (v0.1 to v0.3 and later).
const bundleMediaTypePrefix = "application/vnd.dev.sigstore.bundle"

// Unwrapped is what Unwrap found in a document's content.
type Unwrapped struct {
	Kind       string // KindBare, KindDSSE or KindBundle
	Signatures int    // number of signatures of the envelope; 0 for KindBare
	Payload    []byte // as Result.Payload
}

// envelope is a DSSE envelope as JSON (the protocol's field names).
type envelope struct {
	PayloadType *string           `json:"payloadType"`
	Payload     *string           `json:"payload"`
	Signatures  []json.RawMessage `json:"signatures"`
}

// Unwrap tells whether content is a DSSE envelope, a Sigstore bundle
// wrapping one, or anything else, and extracts the payload documents are
// compared on: the canonical JSON of the envelope's payload when that is
// JSON, else the canonical JSON of content, else content itself (content
// that is not JSON at all). It does not check signatures.
func Unwrap(content []byte) Unwrapped {
	u := Unwrapped{Kind: KindBare, Payload: canonicalOrSelf(content)}
	var probe struct {
		envelope
		MediaType    *string   `json:"mediaType"`
		DSSEEnvelope *envelope `json:"dsseEnvelope"`
	}
	if err := json.Unmarshal(content, &probe); err != nil {
		return u
	}
	env := &probe.envelope
	switch {
	case probe.MediaType != nil && strings.HasPrefix(*probe.MediaType, bundleMediaTypePrefix) && probe.DSSEEnvelope != nil:
		u.Kind = KindBundle
		env = probe.DSSEEnvelope
	case probe.PayloadType != nil && probe.Payload != nil:
		u.Kind = KindDSSE
	default:
		return u
	}
	u.Signatures = len(env.Signatures)
	if env.Payload == nil {
		return u
	}
	if payload, ok := decodeBase64(*env.Payload); ok {
		if c, err := Canonical(payload); err == nil {
			u.Payload = c
		}
	}
	return u
}

// decodeBase64 accepts the standard and the URL-safe alphabet, with or
// without padding, as DSSE implementations differ.
func decodeBase64(s string) ([]byte, bool) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, true
		}
	}
	return nil, false
}

func canonicalOrSelf(content []byte) []byte {
	if c, err := Canonical(content); err == nil {
		return c
	}
	return bytes.Clone(content)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/attest/ && go vet ./internal/attest/`
Expected: PASS, no vet findings.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/attest   # prints nothing
git add internal/attest/attest.go internal/attest/unwrap.go internal/attest/unwrap_test.go
git commit -m "Define document verification results and extract the payload documents compare on

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Verify signatures with the carabiner-dev signer

**Files:**
- Create: `internal/attest/carabiner/carabiner.go`
- Create: `internal/attest/carabiner/testdata/bnd-v0.4.6-provenance.bundle.json` (restored from Git history)
- Modify: `go.mod`, `go.sum`
- Test: `internal/attest/carabiner/carabiner_test.go`

**Interfaces:**
- Consumes: `attest.Result`, `attest.Verifier`, `attest.Status*`, `attest.Unwrap`, `attest.KindBare`, `attest.Canonical` (Tasks 1–2). From `github.com/carabiner-dev/signer` v0.6.2: `signer.NewVerifier()`, `(*signer.Verifier).VerifyStatementBytes(data []byte, fnOpts ...options.VerificationOptFunc) (*api.Verification, error)`, `options.WithRekorVerification(bool)`, `options.WithSkipIdentityCheck(bool)`, `options.WithPublicKeys(...key.PublicKeyProvider)`, `key.NewParser().ParsePublicKeyProvider([]byte)`, `api.VerificationStatus_VERIFIED/_UNSIGNED`, `(*api.Identity).Spec()`.
- Produces (package `carabiner`, used by plan 3d's `serve` and plan 3e's `processor test --trusted-keys`):
  - `type Verifier struct{ /* unexported */ }` — implements `attest.Verifier`, safe for concurrent use
  - `func New(keysDir string) (*Verifier, error)` — `""` = no keys; loads regular files (symlinks followed) named `*.pem` or `*.pub`; a missing directory or an unparsable key is an error naming the path
  - `func (v *Verifier) Verify(content []byte) attest.Result` — bare content and envelopes/bundles without signatures → `unsigned`; signer library VERIFIED → `verified` with `Signers` = sorted, deduplicated identity specs (`key::<type>::<key id>` for keys — `key::::<key id>` for PKIX keys —, `sigstore::<issuer>::<identity>` for bundles); UNSIGNED → `unsigned`; FAILED, UNVERIFIABLE or a library error → `failed`. `Payload` always `attest.Unwrap(content).Payload`.

- [ ] **Step 1: Restore the Sigstore bundle fixture** (the SLSA provenance of the bnd v0.4.6 release, used by ADR 0001's evaluation)

```bash
mkdir -p internal/attest/carabiner/testdata
git show 244f9de^:internal/attest/carabiner/testdata/bnd-v0.4.6-provenance.bundle.json \
  > internal/attest/carabiner/testdata/bnd-v0.4.6-provenance.bundle.json
wc -c internal/attest/carabiner/testdata/bnd-v0.4.6-provenance.bundle.json   # 12655 bytes
```

- [ ] **Step 2: Write the failing test** `internal/attest/carabiner/carabiner_test.go`

```go
package carabiner

// The DSSE envelopes in these tests are signed in-process with the
// standard library, in the formats of internal/proctest's SigningKey
// (PKCS#8 private key, PKIX public key, both PEM) and of the "sign" test
// image (DSSE over the PAE, payloadType application/vnd.in-toto+json,
// standard base64). The Sigstore bundle in testdata is the SLSA provenance
// of the bnd v0.4.6 release; it verifies offline with the embedded trust
// root.

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/emeland-io/custos/internal/attest"
)

const payloadType = "application/vnd.in-toto+json"

// statement returns a bare in-toto v1 statement about subject, encoded
// with sorted keys like the "generate" test image.
func statement(t *testing.T, subject string) []byte {
	t.Helper()
	sum := sha256.Sum256([]byte(subject))
	data, err := json.Marshal(map[string]any{
		"_type":         "https://in-toto.io/Statement/v1",
		"subject":       []map[string]any{{"name": subject, "digest": map[string]string{"sha256": fmt.Sprintf("%x", sum)}}},
		"predicateType": "https://example.org/custos-test/v1",
		"predicate":     map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// testKey is a key pair; public is its PKIX PEM encoding.
type testKey struct {
	signer crypto.Signer
	public []byte
}

func newKey(t *testing.T, kind string) testKey {
	t.Helper()
	var s crypto.Signer
	var err error
	switch kind {
	case "ed25519":
		_, s, err = ed25519.GenerateKey(rand.Reader)
	case "ecdsa":
		s, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	default:
		t.Fatalf("unknown key kind %q", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(s.Public())
	if err != nil {
		t.Fatal(err)
	}
	return testKey{signer: s, public: pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})}
}

// sign returns payload wrapped in a DSSE envelope with one signature per
// key over the PAE (ed25519 signs it as is, ECDSA its SHA-256 digest).
func sign(t *testing.T, payload []byte, keys ...testKey) []byte {
	t.Helper()
	pae := fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(payloadType), payloadType, len(payload), payload)
	sigs := []map[string]string{}
	for _, k := range keys {
		msg, opts := pae, crypto.SignerOpts(crypto.Hash(0))
		if _, ok := k.signer.(*ecdsa.PrivateKey); ok {
			sum := sha256.Sum256(pae)
			msg, opts = sum[:], crypto.SHA256
		}
		sig, err := k.signer.Sign(rand.Reader, msg, opts)
		if err != nil {
			t.Fatal(err)
		}
		sigs = append(sigs, map[string]string{"keyid": "", "sig": base64.StdEncoding.EncodeToString(sig)})
	}
	data, err := json.Marshal(map[string]any{
		"payloadType": payloadType,
		"payload":     base64.StdEncoding.EncodeToString(payload),
		"signatures":  sigs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// keysDir writes each key's public PEM as <name> into a new directory.
func keysDir(t *testing.T, keys map[string]testKey) string {
	t.Helper()
	dir := t.TempDir()
	for name, k := range keys {
		if err := os.WriteFile(filepath.Join(dir, name), k.public, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func newVerifier(t *testing.T, keys map[string]testKey) *Verifier {
	t.Helper()
	dir := ""
	if keys != nil {
		dir = keysDir(t, keys)
	}
	v, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

func editJSON(t *testing.T, data []byte, edit func(map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// tamper renames the first subject in the payload of the DSSE envelope env.
func tamper(t *testing.T, env map[string]any) {
	t.Helper()
	payload, err := base64.StdEncoding.DecodeString(env["payload"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if err := json.Unmarshal(payload, &st); err != nil {
		t.Fatal(err)
	}
	st["subject"].([]any)[0].(map[string]any)["name"] = "tampered"
	if payload, err = json.Marshal(st); err != nil {
		t.Fatal(err)
	}
	env["payload"] = base64.StdEncoding.EncodeToString(payload)
}

func canonical(t *testing.T, data []byte) string {
	t.Helper()
	c, err := attest.Canonical(data)
	if err != nil {
		t.Fatal(err)
	}
	return string(c)
}

func TestVerifyDSSE(t *testing.T) {
	ed, ec, other := newKey(t, "ed25519"), newKey(t, "ecdsa"), newKey(t, "ed25519")
	stmt := statement(t, "artifact.tgz")
	signed := sign(t, stmt, ed)
	trusted := map[string]testKey{"ed.pub": ed}

	for _, tc := range []struct {
		name    string
		content []byte
		keys    map[string]testKey // nil: New("")
		status  string
		signers int
	}{
		{"ed25519 key", signed, trusted, attest.StatusVerified, 1},
		{"ecdsa key", sign(t, stmt, ec), map[string]testKey{"ec.pem": ec}, attest.StatusVerified, 1},
		{"one of several trusted keys", signed, map[string]testKey{"other.pub": other, "ed.pub": ed}, attest.StatusVerified, 1},
		{"two trusted signers", sign(t, stmt, ed, ec), map[string]testKey{"ed.pub": ed, "ec.pem": ec}, attest.StatusVerified, 2},
		{"signed by an untrusted key", signed, map[string]testKey{"other.pub": other}, attest.StatusFailed, 0},
		{"no trusted keys", signed, nil, attest.StatusFailed, 0},
		{"no signatures", editJSON(t, signed, func(m map[string]any) { m["signatures"] = []any{} }), trusted, attest.StatusUnsigned, 0},
		{"bare statement", stmt, trusted, attest.StatusUnsigned, 0},
		{"not an attestation", []byte(`{"hello":"world"}`), trusted, attest.StatusUnsigned, 0},
		{"not JSON", []byte(`not json`), trusted, attest.StatusUnsigned, 0},
		{"tampered payload", editJSON(t, signed, func(m map[string]any) { tamper(t, m) }), trusted, attest.StatusFailed, 0},
		{"garbage signature", editJSON(t, signed, func(m map[string]any) {
			m["signatures"] = []any{map[string]any{"sig": "AAAA"}}
		}), trusted, attest.StatusFailed, 0},
		{"signature that is not base64", editJSON(t, signed, func(m map[string]any) {
			m["signatures"] = []any{map[string]any{"sig": "%%%"}}
		}), trusted, attest.StatusFailed, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newVerifier(t, tc.keys).Verify(tc.content)
			if r.Status != tc.status {
				t.Errorf("status %q, want %q", r.Status, tc.status)
			}
			if len(r.Signers) != tc.signers {
				t.Errorf("signers %q, want %d", r.Signers, tc.signers)
			}
			if want := attest.Unwrap(tc.content).Payload; string(r.Payload) != string(want) {
				t.Errorf("payload %s, want %s", r.Payload, want)
			}
		})
	}
}

func TestPayloadIsTheStatement(t *testing.T) {
	ed := newKey(t, "ed25519")
	stmt := statement(t, "a")
	v := newVerifier(t, map[string]testKey{"ed.pub": ed})
	want := canonical(t, stmt)
	for name, content := range map[string][]byte{
		"bare":                       stmt,
		"signed by a trusted key":    sign(t, stmt, ed),
		"signed by an untrusted key": sign(t, stmt, newKey(t, "ecdsa")),
	} {
		if got := string(v.Verify(content).Payload); got != want {
			t.Errorf("%s: payload %s, want %s", name, got, want)
		}
	}
}

func TestSignersNameTheKeys(t *testing.T) {
	ed, ec := newKey(t, "ed25519"), newKey(t, "ecdsa")
	v := newVerifier(t, map[string]testKey{"ed.pub": ed, "ec.pem": ec})
	edOnly := v.Verify(sign(t, statement(t, "a"), ed)).Signers
	ecOnly := v.Verify(sign(t, statement(t, "a"), ec)).Signers
	both := v.Verify(sign(t, statement(t, "a"), ec, ed)).Signers
	if len(edOnly) != 1 || len(ecOnly) != 1 || edOnly[0] == ecOnly[0] {
		t.Fatalf("signers %q and %q", edOnly, ecOnly)
	}
	want := []string{edOnly[0], ecOnly[0]}
	if want[0] > want[1] {
		want[0], want[1] = want[1], want[0]
	}
	if len(both) != 2 || both[0] != want[0] || both[1] != want[1] {
		t.Errorf("signers %q, want %q (sorted)", both, want)
	}
	t.Logf("key identity: %s", edOnly[0])
}

func TestNewLoadsKeys(t *testing.T) {
	ed, ec := newKey(t, "ed25519"), newKey(t, "ecdsa")
	dir := keysDir(t, map[string]testKey{"ed.pub": ed, "ec.pem": ec})
	// Files with other names and subdirectories are ignored.
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "old.pem"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Symbolic links are followed (mounted Kubernetes secrets).
	linked := newKey(t, "ed25519")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, linked.public, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "linked.pub")); err != nil {
		t.Fatal(err)
	}
	v, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for name, k := range map[string]testKey{"ed": ed, "ec": ec, "linked": linked} {
		if r := v.Verify(sign(t, statement(t, "a"), k)); r.Status != attest.StatusVerified {
			t.Errorf("%s: status %q", name, r.Status)
		}
	}
}

func TestNewErrors(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing directory: no error")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.pem"), []byte("-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(dir)
	if err == nil || !strings.Contains(err.Error(), "broken.pem") {
		t.Errorf("broken key: error %v does not name the file", err)
	}
	empty, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("empty directory: %v", err)
	}
	if r := empty.Verify(statement(t, "a")); r.Status != attest.StatusUnsigned {
		t.Errorf("empty directory: status %q", r.Status)
	}
}

func TestVerifySigstoreBundle(t *testing.T) {
	bundle, err := os.ReadFile("testdata/bnd-v0.4.6-provenance.bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	// Trusted keys play no part for bundles.
	v := newVerifier(t, map[string]testKey{"ed.pub": newKey(t, "ed25519")})
	r := v.Verify(bundle)
	if r.Status != attest.StatusVerified {
		t.Fatalf("status %q", r.Status)
	}
	const identity = "sigstore::https://token.actions.githubusercontent.com::https://github.com/carabiner-dev/bnd/.github/workflows/release.yaml@refs/tags/v0.4.6"
	if len(r.Signers) != 1 || r.Signers[0] != identity {
		t.Errorf("signers %q, want [%q]", r.Signers, identity)
	}
	var b struct {
		DSSEEnvelope struct {
			Payload string `json:"payload"`
		} `json:"dsseEnvelope"`
	}
	if err := json.Unmarshal(bundle, &b); err != nil {
		t.Fatal(err)
	}
	stmt, err := base64.StdEncoding.DecodeString(b.DSSEEnvelope.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Payload) != canonical(t, stmt) {
		t.Errorf("payload is not the canonical statement")
	}

	tampered := editJSON(t, bundle, func(m map[string]any) { tamper(t, m["dsseEnvelope"].(map[string]any)) })
	if r := v.Verify(tampered); r.Status != attest.StatusFailed || len(r.Signers) != 0 {
		t.Errorf("tampered bundle: status %q signers %q", r.Status, r.Signers)
	}
	unsigned := editJSON(t, bundle, func(m map[string]any) { m["dsseEnvelope"].(map[string]any)["signatures"] = []any{} })
	if r := v.Verify(unsigned); r.Status != attest.StatusUnsigned {
		t.Errorf("bundle without signatures: status %q", r.Status)
	}
}

// TestConcurrentVerify runs verifications from several goroutines, as the
// run workers of plan 3d do; run it with -race.
func TestConcurrentVerify(t *testing.T) {
	ed := newKey(t, "ed25519")
	v := newVerifier(t, map[string]testKey{"ed.pub": ed})
	bundle, err := os.ReadFile("testdata/bnd-v0.4.6-provenance.bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	signed := sign(t, statement(t, "a"), ed)
	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for i := range 8 {
		wg.Go(func() {
			content := signed
			if i%2 == 1 {
				content = bundle
			}
			if r := v.Verify(content); r.Status != attest.StatusVerified {
				errs <- fmt.Sprintf("goroutine %d: status %q", i, r.Status)
			}
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/attest/carabiner/`
Expected: FAIL — build errors `undefined: Verifier` and `undefined: New`.

- [ ] **Step 4: Implement** `internal/attest/carabiner/carabiner.go`

```go
// Package carabiner implements attest.Verifier with the carabiner-dev
// signer library (ADR 0001). It is the only package of custos that imports
// carabiner-dev modules.
package carabiner

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/carabiner-dev/signer"
	api "github.com/carabiner-dev/signer/api/v1"
	"github.com/carabiner-dev/signer/key"
	"github.com/carabiner-dev/signer/options"

	"github.com/emeland-io/custos/internal/attest"
)

// sharedSigner is created once per process: constructing a signer
// verifier resolves the Sigstore trust root, which tries a TUF refresh
// (about 1.5 s offline) when the embedded copy is older than 30 days
// (ADR 0001 finding 4).
var sharedSigner = sync.OnceValue(func() *signer.Verifier { return signer.NewVerifier() })

// Verifier verifies documents against the trusted public keys and, for
// Sigstore bundles, the embedded Sigstore trust root. It is safe for
// concurrent use.
type Verifier struct {
	keys []key.PublicKeyProvider
}

var _ attest.Verifier = (*Verifier)(nil)

// New loads every *.pem / *.pub public key in keysDir ("" = none) and
// returns a Verifier. Sigstore bundles verify offline with the embedded
// trust root (ADR 0001 findings 2 and 4); one signer.Verifier per process.
//
// Other files and subdirectories of keysDir are ignored. A keysDir that
// does not exist, or a key file that cannot be read or parsed, is an error
// naming the file.
func New(keysDir string) (*Verifier, error) {
	v := &Verifier{}
	if keysDir == "" {
		return v, nil
	}
	entries, err := os.ReadDir(keysDir)
	if err != nil {
		return nil, fmt.Errorf("reading trusted keys: %w", err)
	}
	parser := key.NewParser()
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if ext != ".pem" && ext != ".pub" {
			continue
		}
		path := filepath.Join(keysDir, e.Name())
		// Stat follows symbolic links, as in mounted Kubernetes secrets.
		if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading trusted key: %w", err)
		}
		p, err := parser.ParsePublicKeyProvider(data)
		if err != nil {
			return nil, fmt.Errorf("trusted key %s: %w", path, err)
		}
		if _, err := p.PublicKey(); err != nil {
			return nil, fmt.Errorf("trusted key %s: %w", path, err)
		}
		v.keys = append(v.keys, p)
	}
	return v, nil
}

// Verify implements attest.Verifier. Bare content and envelopes without
// signatures are unsigned; an envelope or bundle whose signatures cannot
// be verified for any reason (no matching trusted key, no trusted keys at
// all, a modified payload, a malformed envelope) is failed.
func (v *Verifier) Verify(content []byte) attest.Result {
	u := attest.Unwrap(content)
	res := attest.Result{Status: attest.StatusUnsigned, Payload: u.Payload}
	if u.Kind == attest.KindBare || u.Signatures == 0 {
		return res
	}
	opts := []options.VerificationOptFunc{
		options.WithRekorVerification(false),
		// custos records who signed; it does not require particular
		// signers, so the bundle verifier must not insist on one.
		options.WithSkipIdentityCheck(true),
	}
	if len(v.keys) > 0 {
		opts = append(opts, options.WithPublicKeys(v.keys...))
	}
	ver, err := sharedSigner().VerifyStatementBytes(content, opts...)
	if err != nil {
		res.Status = attest.StatusFailed
		return res
	}
	sig := ver.GetSignature()
	switch sig.GetStatus() {
	case api.VerificationStatus_VERIFIED:
		res.Status = attest.StatusVerified
		res.Signers = signers(sig.GetIdentities())
		if len(res.Signers) == 0 {
			res.Status = attest.StatusFailed
		}
	case api.VerificationStatus_UNSIGNED:
		res.Status = attest.StatusUnsigned
	default:
		res.Status = attest.StatusFailed
	}
	return res
}

// signers returns the identity specs of ids, sorted and without
// duplicates.
func signers(ids []*api.Identity) []string {
	var out []string
	for _, id := range ids {
		if s := strings.TrimSpace(id.Spec()); s != "" {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
```

- [ ] **Step 5: Add the module**

```bash
go get github.com/carabiner-dev/signer@v0.6.2
go mod tidy
grep -n 'carabiner-dev/signer v0.6.2$' go.mod   # a direct requirement, no "// indirect"
grep -n '^go ' go.mod                          # still "go 1.26.0"
```

`go mod tidy` also raises `go.yaml.in/yaml/v3` from v3.0.4 to v3.0.5 (minimum version selection; signer requires it) and adds about 85 indirect requirements. If a newer signer release exists when you run this, keep v0.6.2 unless this plan's tests pass with the newer one unchanged; record a change of version in your report.

- [ ] **Step 6: Run the tests to verify they pass, with the race detector**

Run: `go test -race -v ./internal/attest/...`
Expected: PASS for every test. The first verification takes up to about 2 s (the signer resolves its Sigstore trust root once, ADR 0001 finding 4); `TestSignersNameTheKeys` logs an identity like `key::::24c302e60f66544c`.

- [ ] **Step 7: Run the full suite**

Run: `gofmt -l . && make test`
Expected: `gofmt` prints nothing; `go vet ./...` clean; all packages PASS.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/attest/carabiner
git commit -m "Verify processor documents against trusted keys and Sigstore bundles with the carabiner-dev signer

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Update ADR 0001

**Files:**
- Modify: `docs/adr/0001-in-toto-library.md` (new section at the end)

The rulings of this plan are recorded in spec §11.3 by the controller of phase 3, not by this plan; they are listed in "Decisions beyond the architecture note".

- [ ] **Step 1: Update ADR 0001** — append to `docs/adr/0001-in-toto-library.md`:

```markdown

## Use in phase 3 (2026-10-04)

The phase-0 code was removed in `244f9de`. Phase 3 verifies the documents
that processors output and uses only `github.com/carabiner-dev/signer`
(v0.6.2): `Verifier.VerifyStatementBytes` for DSSE envelopes and Sigstore
bundles, `key.Parser` for the trusted keys. The interface is now
`attest.Verifier` with `Verify(content) Result`; requirement checks against
predicate types, subjects and identities are gone, since custos no longer
has Nodes. The library's "unverifiable" is reported as `failed`. Tests:
`internal/attest/carabiner/carabiner_test.go`, which signs DSSE envelopes
with the standard library and keeps the bnd v0.4.6 bundle fixture. Rulings:
§11.3 of the design spec.
```

- [ ] **Step 2: Check**

Run: `grep -c 'carabiner-dev/signer' docs/adr/0001-in-toto-library.md && make test`
Expected: a count ≥ 2; all tests PASS.

- [ ] **Step 3: Commit**

```bash
git add docs/adr/0001-in-toto-library.md
git commit -m "Describe how phase 3 uses the carabiner-dev signer in ADR 0001

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-Review Notes

- **Spec and note coverage:**
  - §3.3 `verification` (`verified`, `failed`, `unsigned`, signer identities) → `attest.Status*`, `Result.Signers` (Tasks 2, 3); `workspace.Verification{Status, Signers}` already exists and takes these values unchanged (3c copies them).
  - §5.4 documents compare on the payload → `Result.Payload` = `Unwrap(...).Payload`, canonical (Tasks 1–3); 3c compares `Payload` bytes.
  - §5.6 DSSE envelopes, Sigstore bundles and bare statements, verified on ingest against trusted keys with carabiner-dev → Task 3; ingest wiring is 3c (`match.Plan` calls `Run.Verifier`), flag `--trusted-keys` is 3d.
  - Note "3b's tests include": DSSE envelope verified with its key, failed with another, unsigned without signatures, bare statement, tampered payload, the Sigstore bundle fixture → `TestVerifyDSSE` and `TestVerifySigstoreBundle`. The envelopes are signed in-process instead of with `proctest.SigningKey` (decision below), in the same key formats.
  - Note "one signer.Verifier per process" → `sharedSigner` (`sync.OnceValue`).
- **What was run** (on a fresh scratch clone of `main` at `8d0c5ef`, with the Go code blocks extracted from this plan file, the fixture restored and Step 5's commands run): `gofmt -l .` (no output), `go vet ./...` (clean), `make test` (vet clean, all packages pass), `go test -race -count=1 ./internal/attest/...` (pass; the carabiner tests take about 3 s, most of it the one-time trust-root resolution). Go toolchain 1.27.1 with `go 1.26.0` in `go.mod`. The failing-test steps were checked only by reasoning (the test files reference names that do not exist before the implementation step).
- **Not run in this plan:** verification of envelopes produced by 3a's `sign` image; 3c's `internal/match/sign_test.go` and 3d's `internal/runs/sign_test.go` cover it with the real image. The bundle test was not run with the network blocked; ADR 0001 measured that case (falls back to the embedded trust root after about 1.5 s).
- **Placeholder scan:** no TBD/TODO.
- **Set review (2026-10-04):** plans 3a–3e were applied in order, as written, to one clone of `main`; `gofmt -l .`, `go vet ./...` and `go test ./...` passed after each plan, `make test` after 3e, and `go test -race` on runner, runs, store, proposal, merge and cmd/custos at the end (Docker 27.4, Go 1.27.1, Python 3.14).
- **Type consistency:** `Unwrap`, `Unwrapped`, `KindBare`, `KindDSSE`, `KindBundle` are defined in Task 2 and used with the same names in Task 3; exported names of the note are unchanged.

## Decisions beyond the architecture note

- 3b's tests sign DSSE envelopes in-process with `crypto/ed25519` and `crypto/ecdsa` and write PKIX PEM public keys, instead of using `proctest.SigningKey` — keeps 3b independent of 3a and of Docker, and lets the tests cover ECDSA and several signers — if 3a's `sign` image writes envelopes differently (e.g. URL-safe base64, a different payloadType, ECDSA instead of ed25519) nothing here notices; 3c/3d should have one test that runs the `sign` image with `proctest.SigningKey` through `carabiner.New` and expects `verified`.
- New exported names in `attest`: `Unwrap`, `Unwrapped`, `KindBare`, `KindDSSE`, `KindBundle` — both `Unverified` and the carabiner backend need the same payload extraction, and only `attest` can hold it without carabiner-dev — three more exported names others could start depending on.
- `carabiner-dev/collector` is not added (only `signer`) — custos detects the format itself; an unused requirement would be removed by `go mod tidy` — none; the note's module list is an upper bound.
- `go.yaml.in/yaml/v3` moves from v3.0.4 to v3.0.5 — required by signer's module graph — a patch-level change of the YAML encoder; the full test suite passes with it.
- The library's UNVERIFIABLE (no trusted keys, keyless DSSE without Rekor) and any library error map to `failed` — the note defines `failed` as "signatures present but none verifies", and there is no fourth status — operators who run without trusted keys see key-signed documents as failed.
- Rekor lookups are disabled (`WithRekorVerification(false)`) — offline, deterministic verification — keyless DSSE envelopes with an attached certificate (slsa-github-generator style, not bundles) show as failed; keyless signing must produce a Sigstore bundle, which verifies offline with the embedded trust root (ADR 0001 finding 4).
- A VERIFIED conclusion with no identity is reported as `failed` — `Signers` must not be empty for `verified` (note: "empty unless verified" implies non-empty when verified for consumers that show signers) — a library change that drops identities would turn verified documents into failed ones, which the tests catch.
- Envelope detection is custos's own: DSSE = object with string `payloadType` and `payload`; bundle = `mediaType` starting with `application/vnd.dev.sigstore.bundle` plus an object `dsseEnvelope`; zero signatures → `unsigned` without calling the library — the library's bare parser accepts any JSON and its bundle check fires on any `mediaType` field (ADR 0001 finding 3) — a bundle that signs a message instead of a DSSE envelope counts as bare and `unsigned`.
- Payload of an envelope whose payload is not base64 or not JSON, and of non-JSON content, falls back to canonical content, then raw content — `Verify` must always return a payload — such documents compare on the whole envelope, so re-signing one counts as a change.
- `Canonical` keeps `<`, `>`, `&` unescaped, normalises string escapes, and keeps the last of duplicate keys — the note fixes only key order, whitespace and numbers — two statements that differ only in escaping compare equal; one with duplicate keys compares on the last value, which may differ from what another JSON parser reads.
- `New` ignores entries other than regular files (after following symbolic links) named `*.pem`/`*.pub`; a missing directory or an unparsable key is an error naming the path — fail loudly on wrong trust configuration, work with Kubernetes secret mounts — a key file with another extension is ignored silently.
- Signers are the library's identity spec strings (`key::<type>::<id>`, `sigstore::<issuer>::<san>`), sorted and deduplicated — ADR 0001 finding 2 — strings may change on a library upgrade (documents compare on the payload, so this causes no proposals).
- This plan writes only the ADR 0001 addendum (Task 4); the decisions above are recorded in spec §11.3 by the controller of phase 3 — five plans editing one table would conflict — none.
