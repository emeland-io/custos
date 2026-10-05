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

// TestUnwrapMalformed is a regression guard for the "never panics"
// guarantee: it exercises nil/empty input, truncated garbage, a top-level
// JSON null or array, and fields present with the wrong JSON type (a
// dsseEnvelope that is a string or number, a mediaType that is a number, a
// payloadType/payload that are numbers). All of these must fall back to
// KindBare without panicking.
func TestUnwrapMalformed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		content    []byte
		kind       string
		signatures int
		validJSON  bool // true: Payload == Canonical(content); false: Payload == content itself
	}{
		{"nil content", nil, KindBare, 0, false},
		{"empty content", []byte{}, KindBare, 0, false},
		{"truncated garbage", []byte(`{{{{not even close to json`), KindBare, 0, false},
		{"top-level null", []byte(`null`), KindBare, 0, true},
		{"top-level array", []byte(`[1,2,3]`), KindBare, 0, true},
		{
			"dsseEnvelope as a string",
			[]byte(`{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json","dsseEnvelope":"not an object"}`),
			KindBare, 0, true,
		},
		{
			"dsseEnvelope as a number",
			[]byte(`{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json","dsseEnvelope":123}`),
			KindBare, 0, true,
		},
		{
			"mediaType as a number",
			[]byte(`{"mediaType":123,"dsseEnvelope":{}}`),
			KindBare, 0, true,
		},
		{
			"payloadType and payload as numbers",
			[]byte(`{"payloadType":123,"payload":456,"signatures":[]}`),
			KindBare, 0, true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Unwrap panicked on %q: %v", tc.content, r)
				}
			}()
			u := Unwrap(tc.content)
			if u.Kind != tc.kind || u.Signatures != tc.signatures {
				t.Errorf("kind %q signatures %d, want %q %d", u.Kind, u.Signatures, tc.kind, tc.signatures)
			}
			want := tc.content
			if tc.validJSON {
				c, err := Canonical(tc.content)
				if err != nil {
					t.Fatal(err)
				}
				want = c
			}
			if string(u.Payload) != string(want) {
				t.Errorf("payload\n got %s\nwant %s", u.Payload, want)
			}
		})
	}
}

// TestUnwrapIgnoresCaseVariantDuplicateKeys: of a "payload" key and a
// duplicate differing only in case, Unwrap takes the exact-case one,
// wherever the duplicate stands. The content is concatenated, not
// re-marshaled, so the key order is as written. (Verifiers do not rely on
// this: see carabiner's TestVerifyNeverPairsVerifiedWithAnotherPayload.)
func TestUnwrapIgnoresCaseVariantDuplicateKeys(t *testing.T) {
	forged := base64.StdEncoding.EncodeToString([]byte(`{"forged":true}`))
	env := dsse(statement, base64.StdEncoding, `[{"keyid":"","sig":"c2ln"}]`)
	for name, content := range map[string]string{
		"duplicate last":  env[:len(env)-1] + fmt.Sprintf(`,"Payload":%q}`, forged),
		"duplicate first": fmt.Sprintf(`{"Payload":%q,`, forged) + env[1:],
	} {
		u := Unwrap([]byte(content))
		if u.Kind != KindDSSE || string(u.Payload) != statementCanonical {
			t.Errorf("%s: kind %q payload %s, want %q %s", name, u.Kind, u.Payload, KindDSSE, statementCanonical)
		}
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
