package directives

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// PayloadType is the DSSE payload type of the envelopes sign writes.
const PayloadType = "application/vnd.in-toto+json"

// PAE is the DSSE v1 pre-authentication encoding that is signed.
func PAE(payloadType string, payload []byte) []byte {
	return fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(payloadType), payloadType, len(payload), payload)
}

// Envelope wraps statement in a DSSE envelope with one signature by key.
func Envelope(statement []byte, key ed25519.PrivateKey) (json.RawMessage, error) {
	sig := ed25519.Sign(key, PAE(PayloadType, statement))
	return json.Marshal(map[string]any{
		"payloadType": PayloadType,
		"payload":     base64.StdEncoding.EncodeToString(statement),
		"signatures":  []any{map[string]string{"sig": base64.StdEncoding.EncodeToString(sig)}},
	})
}
