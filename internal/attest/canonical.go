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
