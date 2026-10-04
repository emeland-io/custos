package merge

import (
	"bytes"
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/workspace"
)

// decodeConfig parses custos.yaml strictly, like workspace.Load. nil data
// (no file) is an error.
func decodeConfig(data []byte) (workspace.Config, error) {
	var c workspace.Config
	if data == nil {
		return c, errors.New("custos.yaml is missing")
	}
	if err := frontmatter.DecodeStrict(bytes.TrimPrefix(data, []byte("\ufeff")), &c); err != nil {
		return workspace.Config{}, fmt.Errorf("custos.yaml: %w", err)
	}
	return c, nil
}

// encodeConfig writes custos.yaml with two-space indentation.
func encodeConfig(c workspace.Config) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
