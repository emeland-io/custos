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

// Unwrap tells whether content is a DSSE envelope, a Sigstore bundle
// wrapping one, or anything else, and extracts the payload documents are
// compared on: the canonical JSON of the envelope's payload when that is
// JSON, else the canonical JSON of content, else content itself (content
// that is not JSON at all). It does not check signatures.
//
// Every field consulted here (mediaType, dsseEnvelope, payloadType,
// payload, signatures) is looked up by its exact-case JSON key, so that
// duplicate keys differing only in case are never matched.
//
// Unwrap is not how a Verifier learns which payload a signature covers:
// signature libraries parse envelopes with their own rules (case-folding,
// protojson field aliases, the last of duplicate keys winning, or
// rejecting them), and no second parser can be relied on to agree with
// them on crafted content. A Verifier takes a verified payload from the
// envelope its library checked, and reports content on which that
// disagrees with Unwrap as failed.
func Unwrap(content []byte) Unwrapped {
	u := Unwrapped{Kind: KindBare, Payload: canonicalOrSelf(content)}

	top, ok := decodeObject(content)
	if !ok {
		return u
	}
	mediaType, mediaTypePresent, ok := stringField(top, "mediaType")
	if !ok {
		return u
	}
	dsseEnvelope, dssePresent, ok := objectField(top, "dsseEnvelope")
	if !ok {
		return u
	}
	payloadTypePresent, envPayload, payloadPresent, signatures, ok := envelopeFields(top)
	if !ok {
		return u
	}

	switch {
	case mediaTypePresent && strings.HasPrefix(mediaType, bundleMediaTypePrefix) && dssePresent:
		u.Kind = KindBundle
		_, envPayload, payloadPresent, signatures, ok = envelopeFields(dsseEnvelope)
		if !ok {
			return u
		}
	case payloadTypePresent && payloadPresent:
		u.Kind = KindDSSE
	default:
		return u
	}

	u.Signatures = len(signatures)
	if !payloadPresent {
		return u
	}
	decoded, ok := decodeBase64(envPayload)
	if !ok {
		return u
	}
	c, err := Canonical(decoded)
	if err != nil {
		return u
	}
	u.Payload = c
	return u
}

// decodeObject decodes a top-level JSON object into a map keyed by its
// exact (case-sensitive) field names. Anything that is not a JSON object
// — including malformed JSON, and well-formed JSON whose top-level value
// is an array, string, number, bool or null — reports ok = false.
func decodeObject(content []byte) (m map[string]json.RawMessage, ok bool) {
	if err := json.Unmarshal(content, &m); err != nil {
		return nil, false
	}
	return m, true
}

// stringField reads key from m by its exact-case name. present reports
// whether the key exists at all; ok is false only when the key is
// present but its value is not a JSON string, mirroring how a single
// struct-based json.Unmarshal would have failed outright on a type
// mismatch rather than silently coercing it.
func stringField(m map[string]json.RawMessage, key string) (value string, present, ok bool) {
	raw, exists := m[key]
	if !exists {
		return "", false, true
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, false
	}
	return value, true, true
}

// objectField is stringField for a nested JSON object.
func objectField(m map[string]json.RawMessage, key string) (value map[string]json.RawMessage, present, ok bool) {
	raw, exists := m[key]
	if !exists {
		return nil, false, true
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, true, false
	}
	return value, true, true
}

// arrayField is stringField for a JSON array.
func arrayField(m map[string]json.RawMessage, key string) (value []json.RawMessage, present, ok bool) {
	raw, exists := m[key]
	if !exists {
		return nil, false, true
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, true, false
	}
	return value, true, true
}

// envelopeFields extracts a DSSE envelope's fields from m by their
// exact-case keys: payloadType (presence only — its value is never used
// beyond that), payload and signatures. ok is false when a present field
// does not have the JSON type the DSSE spec gives it, in which case the
// whole object must be treated as unparseable, the same way one combined
// struct decode of all of an envelope's fields would have failed outright
// on any single type mismatch.
func envelopeFields(m map[string]json.RawMessage) (payloadTypePresent bool, payload string, payloadPresent bool, signatures []json.RawMessage, ok bool) {
	_, payloadTypePresent, ok = stringField(m, "payloadType")
	if !ok {
		return
	}
	payload, payloadPresent, ok = stringField(m, "payload")
	if !ok {
		return
	}
	signatures, _, ok = arrayField(m, "signatures")
	return
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

// canonicalOrSelf returns the canonical JSON of content when content is
// valid JSON, otherwise content itself (cloned).
func canonicalOrSelf(content []byte) []byte {
	if c, err := Canonical(content); err == nil {
		return c
	}
	return bytes.Clone(content)
}
