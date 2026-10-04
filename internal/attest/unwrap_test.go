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
