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
