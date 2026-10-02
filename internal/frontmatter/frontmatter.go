// Package frontmatter reads and writes files that start with a YAML block
// between two "---" lines, followed by a Markdown body.
package frontmatter

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Split separates data into its frontmatter and its body. Windows line
// endings become "\n", and a missing final newline is added. One blank line
// after the closing "---" is not part of the body.
func Split(data []byte) (meta []byte, body string, err error) {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, "", errors.New(`file must start with a "---" line`)
	}
	s = s[len("---\n"):]
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	if strings.HasPrefix(s, "---\n") {
		return nil, strings.TrimPrefix(s[len("---\n"):], "\n"), nil
	}
	i := strings.Index(s, "\n---\n")
	if i < 0 {
		return nil, "", errors.New(`frontmatter has no closing "---" line`)
	}
	return []byte(s[:i+1]), strings.TrimPrefix(s[i+len("\n---\n"):], "\n"), nil
}

// DecodeStrict decodes YAML into v and rejects fields v does not have.
// Empty input leaves v unchanged.
func DecodeStrict(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Decode decodes the frontmatter of data into v and returns the body.
func Decode(data []byte, v any) (string, error) {
	meta, body, err := Split(data)
	if err != nil {
		return "", err
	}
	if err := DecodeStrict(meta, v); err != nil {
		return "", fmt.Errorf("frontmatter: %w", err)
	}
	return body, nil
}

// Encode writes v as frontmatter, followed by a blank line and body.
func Encode(v any, body string) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("---\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	b.WriteString("---\n")
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
	}
	return b.Bytes(), nil
}
