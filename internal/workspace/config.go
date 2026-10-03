package workspace

import (
	"bytes"

	"go.yaml.in/yaml/v3"
)

// MarshalConfig encodes c as the content of custos.yaml.
func MarshalConfig(c Config) ([]byte, error) {
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
