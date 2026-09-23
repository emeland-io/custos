package carabiner

// These tests are the evaluation of the carabiner-dev libraries for
// custos (see docs/adr/0001-in-toto-library.md). They run offline and
// generate every key and envelope they need, except the Sigstore bundle
// in testdata, which is the SLSA provenance of the bnd v0.4.6 release.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carabiner-dev/signer"
	"github.com/carabiner-dev/signer/key"
	"github.com/carabiner-dev/signer/options"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/model"
)

const testPredicateType = "https://example.com/test/v1"

func statement(t *testing.T, predicateType, subject string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"_type":         "https://in-toto.io/Statement/v1",
		"predicateType": predicateType,
		"subject": []map[string]any{{
			"name":   subject,
			"digest": map[string]string{"sha256": strings.Repeat("ab", 32)},
		}},
		"predicate": map[string]any{"result": "done"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func genKey(t *testing.T, typ key.Type) *key.Private {
	t.Helper()
	k, err := key.NewGenerator().GenerateKeyPair(key.WithKeyType(typ))
	if err != nil {
		t.Fatalf("generating %s key: %v", typ, err)
	}
	return k
}

func trusted(t *testing.T, k *key.Private) TrustedKey {
	t.Helper()
	tk, err := NewTrustedKey(string(k.Type), k)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func signDSSE(t *testing.T, stmt []byte, k *key.Private) []byte {
	t.Helper()
	s := signer.NewSigner()
	env, err := s.SignStatementToDSSE(stmt, options.WithKey(k))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	var buf bytes.Buffer
	if err := s.WriteDSSEEnvelope(env, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInspectDSSE(t *testing.T) {
	raw := signDSSE(t, statement(t, testPredicateType, "artifact.tgz"), genKey(t, key.ED25519))
	p, err := New().Inspect(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Format != attest.FormatDSSE || p.PredicateType != testPredicateType {
		t.Errorf("got format %q predicate %q", p.Format, p.PredicateType)
	}
	if len(p.Subjects) != 1 || p.Subjects[0].Name != "artifact.tgz" || p.Subjects[0].Digest["sha256"] == "" {
		t.Errorf("subjects: %+v", p.Subjects)
	}
}

func TestVerifyDSSE(t *testing.T) {
	ed := genKey(t, key.ED25519)
	ec := genKey(t, key.ECDSA)
	other := genKey(t, key.ED25519)
	stmt := statement(t, testPredicateType, "artifact.tgz")
	req := model.RequiredAttestation{PredicateType: testPredicateType}

	for _, tc := range []struct {
		name     string
		raw      []byte
		keys     []TrustedKey
		req      model.RequiredAttestation
		status   model.VerificationStatus
		met      bool
		errMatch string
	}{
		{name: "ed25519", raw: signDSSE(t, stmt, ed), keys: []TrustedKey{trusted(t, ed)}, req: req, status: model.StatusVerified, met: true},
		{name: "ecdsa", raw: signDSSE(t, stmt, ec), keys: []TrustedKey{trusted(t, ec)}, req: req, status: model.StatusVerified, met: true},
		{name: "one of several keys", raw: signDSSE(t, stmt, ec), keys: []TrustedKey{trusted(t, other), trusted(t, ec)}, req: req, status: model.StatusVerified, met: true},
		{name: "wrong key", raw: signDSSE(t, stmt, ed), keys: []TrustedKey{trusted(t, other)}, req: req, status: model.StatusFailed},
		{name: "no trusted keys", raw: signDSSE(t, stmt, ed), req: req, status: model.StatusUnverifiable},
		{name: "unsigned envelope", raw: stripSignatures(t, signDSSE(t, stmt, ed)), keys: []TrustedKey{trusted(t, ed)}, req: req, status: model.StatusUnsigned},
		{name: "bare statement", raw: stmt, keys: []TrustedKey{trusted(t, ed)}, req: req, status: model.StatusUnsigned},
		{
			name: "tampered payload", raw: tamperPayload(t, signDSSE(t, stmt, ed)), keys: []TrustedKey{trusted(t, ed)},
			req: req, status: model.StatusFailed,
		},
		{
			name: "wrong predicate type", raw: signDSSE(t, stmt, ed), keys: []TrustedKey{trusted(t, ed)},
			req:    model.RequiredAttestation{PredicateType: "https://slsa.dev/provenance/v1"},
			status: model.StatusVerified, errMatch: "predicate type",
		},
		{
			name: "subject name matches", raw: signDSSE(t, stmt, ed), keys: []TrustedKey{trusted(t, ed)},
			req:    model.RequiredAttestation{PredicateType: testPredicateType, SubjectName: "artifact.tgz"},
			status: model.StatusVerified, met: true,
		},
		{
			name: "subject name mismatch", raw: signDSSE(t, stmt, ed), keys: []TrustedKey{trusted(t, ed)},
			req:    model.RequiredAttestation{PredicateType: testPredicateType, SubjectName: "other.tgz"},
			status: model.StatusVerified, errMatch: "no subject named",
		},
		{
			name: "required identity signed", raw: signDSSE(t, stmt, ed), keys: []TrustedKey{trusted(t, ed), trusted(t, other)},
			req:    model.RequiredAttestation{PredicateType: testPredicateType, RequiredIdentities: []string{trusted(t, ed).Identity}},
			status: model.StatusVerified, met: true,
		},
		{
			name: "required identity missing", raw: signDSSE(t, stmt, ed), keys: []TrustedKey{trusted(t, ed), trusted(t, other)},
			req:    model.RequiredAttestation{PredicateType: testPredicateType, RequiredIdentities: []string{trusted(t, other).Identity}},
			status: model.StatusVerified, errMatch: "not signed by",
		},
		{
			name: "invalid identity spec", raw: signDSSE(t, stmt, ed), keys: []TrustedKey{trusted(t, ed)},
			req:    model.RequiredAttestation{PredicateType: testPredicateType, RequiredIdentities: []string{"nonsense"}},
			status: model.StatusVerified, errMatch: "invalid identity",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, res, err := New(WithKeys(tc.keys...)).Verify(tc.raw, tc.req)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if res.Status != tc.status {
				t.Errorf("status %s, want %s (error %q)", res.Status, tc.status, res.Error)
			}
			if res.RequirementMet != tc.met {
				t.Errorf("requirementMet %v, want %v (errors %q)", res.RequirementMet, tc.met, res.RequirementErrors)
			}
			if tc.errMatch != "" && !strings.Contains(strings.Join(res.RequirementErrors, "\n"), tc.errMatch) {
				t.Errorf("requirement errors %q do not mention %q", res.RequirementErrors, tc.errMatch)
			}
			if tc.status == model.StatusVerified && len(res.Identities) == 0 {
				t.Error("verified without identities")
			}
		})
	}
}

func TestVerifySigstoreBundleOffline(t *testing.T) {
	raw, err := os.ReadFile("testdata/bnd-v0.4.6-provenance.bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	const issuer = "https://token.actions.githubusercontent.com"
	req := model.RequiredAttestation{PredicateType: "https://slsa.dev/provenance/v1", SubjectName: "bnd-v0.4.6-windows-amd64.exe"}

	p, res, err := New().Verify(raw, req)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if p.Format != attest.FormatBundle {
		t.Errorf("format %q", p.Format)
	}
	if res.Status != model.StatusVerified || !res.RequirementMet {
		t.Fatalf("status %s met %v: %s %q", res.Status, res.RequirementMet, res.Error, res.RequirementErrors)
	}
	t.Logf("signer identities: %q", res.Identities)

	req.RequiredIdentities = []string{"sigstore(identityMatch=prefix)::" + issuer + "::https://github.com/carabiner-dev/bnd/"}
	if _, res, _ = New().Verify(raw, req); !res.RequirementMet {
		t.Errorf("bnd release identity not matched: %q", res.RequirementErrors)
	}
	req.RequiredIdentities = []string{"sigstore(identityMatch=prefix)::" + issuer + "::https://github.com/someone/else/"}
	if _, res, _ = New().Verify(raw, req); res.RequirementMet {
		t.Error("foreign identity matched")
	}

	_, res, err = New().Verify(tamperBundle(t, raw), model.RequiredAttestation{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != model.StatusFailed {
		t.Errorf("tampered bundle: status %s", res.Status)
	}
}

func TestInspectRejectsGarbage(t *testing.T) {
	for _, raw := range []string{`not json`, `{"hello":"world"}`, `[]`, `{"_type":"https://in-toto.io/Statement/v1"}`} {
		if _, err := New().Inspect([]byte(raw)); !errors.Is(err, attest.ErrNotAttestation) {
			t.Errorf("%s: got %v", raw, err)
		}
	}
}

func TestLoadKeys(t *testing.T) {
	dir := t.TempDir()
	ed, ec := genKey(t, key.ED25519), genKey(t, key.ECDSA)
	for name, k := range map[string]*key.Private{"ed.pub": ed, "ec.pem": ec} {
		pub, err := k.PublicKey()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(pub.Data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := LoadKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("loaded %d keys", len(keys))
	}
	raw := signDSSE(t, statement(t, testPredicateType, "a"), ec)
	if _, res, _ := New(WithKeys(keys...)).Verify(raw, model.RequiredAttestation{}); res.Status != model.StatusVerified {
		t.Errorf("status %s: %s", res.Status, res.Error)
	}
	if keys, err := LoadKeys(filepath.Join(dir, "missing")); err != nil || keys != nil {
		t.Errorf("missing dir: %v %v", keys, err)
	}
}

func editJSON(t *testing.T, raw []byte, edit func(map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func stripSignatures(t *testing.T, raw []byte) []byte {
	return editJSON(t, raw, func(m map[string]any) { m["signatures"] = []any{} })
}

// tamper renames the first subject in the payload of a DSSE envelope.
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

func tamperPayload(t *testing.T, raw []byte) []byte {
	return editJSON(t, raw, func(m map[string]any) { tamper(t, m) })
}

func tamperBundle(t *testing.T, raw []byte) []byte {
	return editJSON(t, raw, func(m map[string]any) { tamper(t, m["dsseEnvelope"].(map[string]any)) })
}
