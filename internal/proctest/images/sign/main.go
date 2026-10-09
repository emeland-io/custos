// Command sign is the test processor that works like generate, but wraps
// each doc in a DSSE envelope signed with the ed25519 key mounted as secret
// test-signing-key. Without that secret it exits with code 2.
package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/emeland-io/custos/internal/proctest/images/directives"
)

const keyPath = "/run/secrets/test-signing-key"

func main() {
	key, err := loadKey(keyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sign:", err)
		os.Exit(2)
	}
	directives.Main("sign", func(statement []byte) (json.RawMessage, error) {
		return directives.Envelope(statement, key)
	})
}

func loadKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s holds no PEM private key", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	ed, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s is not an ed25519 key", path)
	}
	return ed, nil
}
