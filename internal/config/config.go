// Package config reads the server configuration from command line flags
// and environment variables. A flag wins over its environment variable,
// which wins over the default.
package config

import (
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strconv"
)

// Config is the server configuration.
type Config struct {
	// RootDir holds Roots, Nodes and Leaves.
	RootDir string
	// WorkDir holds Seeds, Shoots and Attestations.
	WorkDir string
	// KeysDir holds the trusted public keys.
	KeysDir string
	// Addr is the listen address.
	Addr string
	// SigstoreOnline allows Rekor lookups for keyless DSSE envelopes.
	SigstoreOnline bool
}

// Parse reads the configuration from args (without the program name) and
// the environment, as returned by getenv.
func Parse(args []string, getenv func(string) string) (Config, error) {
	var c Config
	fs := flag.NewFlagSet("custos", flag.ContinueOnError)
	fs.StringVar(&c.RootDir, "root-dir", getenv("CUSTOS_ROOT_DIR"), "directory for roots, nodes and leaves (env CUSTOS_ROOT_DIR)")
	fs.StringVar(&c.WorkDir, "work-dir", getenv("CUSTOS_WORK_DIR"), "directory for seeds, shoots and attestations (env CUSTOS_WORK_DIR)")
	fs.StringVar(&c.KeysDir, "keys-dir", getenv("CUSTOS_KEYS_DIR"), "directory of trusted public keys (env CUSTOS_KEYS_DIR, default <work-dir>/keys)")
	fs.StringVar(&c.Addr, "addr", withDefault(getenv("CUSTOS_ADDR"), ":8080"), "listen address (env CUSTOS_ADDR)")
	online, err := parseBool(getenv("CUSTOS_SIGSTORE_ONLINE"))
	if err != nil {
		return c, fmt.Errorf("CUSTOS_SIGSTORE_ONLINE: %w", err)
	}
	fs.BoolVar(&c.SigstoreOnline, "sigstore-online", online, "allow Rekor lookups to verify keyless DSSE envelopes (env CUSTOS_SIGSTORE_ONLINE)")
	if err := fs.Parse(args); err != nil {
		return c, err
	}

	if c.RootDir == "" {
		return c, errors.New("root dir is required (--root-dir or CUSTOS_ROOT_DIR)")
	}
	if c.WorkDir == "" {
		return c, errors.New("work dir is required (--work-dir or CUSTOS_WORK_DIR)")
	}
	if c.KeysDir == "" {
		c.KeysDir = filepath.Join(c.WorkDir, "keys")
	}
	return c, nil
}

func withDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func parseBool(v string) (bool, error) {
	if v == "" {
		return false, nil
	}
	return strconv.ParseBool(v)
}
