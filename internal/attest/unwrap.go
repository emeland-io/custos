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
