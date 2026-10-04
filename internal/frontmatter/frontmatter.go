// Package frontmatter reads and writes files that start with a YAML block
// between two "---" lines, followed by a Markdown body.
package frontmatter

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Split separates data into its frontmatter and its body. Windows line
// endings become "\n", and a missing final newline is added. One blank line
// after the closing "---" is not part of the body. One leading UTF-8
// byte-order mark, which some Windows editors write, is ignored.
func Split(data []byte) (meta []byte, body string, err error) {
	s := strings.TrimPrefix(string(data), "\uFEFF")
	s = strings.ReplaceAll(s, "\r\n", "\n")
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

// Encode writes v as frontmatter, followed by a blank line and body. The
// result always reads back as v: when the plain encoding does not
// round-trip (for example a string value whose first line is a tab, which
// yaml's literal block style cannot represent), the frontmatter is
// re-encoded with every string field forced to a double-quoted scalar.
func Encode(v any, body string) ([]byte, error) {
	meta, err := marshalYAML(v)
	if err != nil {
		return nil, err
	}
	if !roundTrips(meta, v) {
		if meta, err = marshalYAMLSafe(v); err != nil {
			return nil, err
		}
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(meta)
	b.WriteString("---\n")
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
	}
	return b.Bytes(), nil
}

func marshalYAML(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// roundTrips reports whether decoding meta and marshalling it again
// reproduces meta byte for byte, which is what Encode promises callers.
func roundTrips(meta []byte, v any) bool {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	decoded := reflect.New(rv.Type())
	if err := DecodeStrict(meta, decoded.Interface()); err != nil {
		return false
	}
	again, err := marshalYAML(decoded.Interface())
	if err != nil {
		return false
	}
	return bytes.Equal(meta, again)
}

// marshalYAMLSafe encodes v's exported, yaml-tagged fields one by one,
// writing every string field as a double-quoted scalar instead of letting
// the encoder pick a style it may not be able to read back. v must be a
// struct (or a pointer to one), which is what every Encode caller passes.
func marshalYAMLSafe(v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("frontmatter: cannot encode %T safely", v)
	}
	doc := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag, ok := f.Tag.Lookup("yaml")
		if !ok || tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		fv := rv.Field(i)
		if strings.Contains(opts, "omitempty") && fv.IsZero() {
			continue
		}
		var valueNode *yaml.Node
		if fv.Kind() == reflect.String {
			valueNode = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fv.String(), Style: yaml.DoubleQuotedStyle}
		} else {
			valueNode = &yaml.Node{}
			if err := valueNode.Encode(fv.Interface()); err != nil {
				return nil, err
			}
		}
		doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: name}, valueNode)
	}
	return marshalYAML(doc)
}
