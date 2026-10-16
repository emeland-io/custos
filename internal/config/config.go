// Package config resolves the settings of the custos command line: each one
// comes from a flag, an environment variable or, for a few, the user's
// config file, and the layers win in that order.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.yaml.in/yaml/v3"
)

// KeyServerURL is the one setting the config file may hold.
const KeyServerURL = "server-url"

// EnvNames lists the flags that also have an environment variable, and the
// variable's name. It is a fixed list (ruling 5.4): a flag that is not here
// is never read from the environment, so a stray CUSTOS_EMAIL or
// CUSTOS_ADMIN cannot change what a command does.
var EnvNames = map[string]string{
	"server-url":           "CUSTOS_SERVER_URL",
	"data-dir":             "CUSTOS_DATA_DIR",
	"addr":                 "CUSTOS_ADDR",
	"public-url":           "CUSTOS_PUBLIC_URL",
	"container-runtime":    "CUSTOS_CONTAINER_RUNTIME",
	"secrets-dir":          "CUSTOS_SECRETS_DIR",
	"processor-memory":     "CUSTOS_PROCESSOR_MEMORY",
	"trusted-keys":         "CUSTOS_TRUSTED_KEYS",
	"processor-workers":    "CUSTOS_PROCESSOR_WORKERS",
	"max-generation-depth": "CUSTOS_MAX_GENERATION_DEPTH",
	"oidc-issuer-url":      "CUSTOS_OIDC_ISSUER_URL",
	"oidc-client-id":       "CUSTOS_OIDC_CLIENT_ID",
	"oidc-client-secret":   "CUSTOS_OIDC_CLIENT_SECRET",
	"oidc-redirect-url":    "CUSTOS_OIDC_REDIRECT_URL",
	"admin-emails":         "CUSTOS_ADMIN_EMAILS",
	"session-secret":       "CUSTOS_SESSION_SECRET",
	"author":               "CUSTOS_AUTHOR",
}

// fileKeys are the settings read from the config file. Everything else in
// the file is ignored with a warning.
var fileKeys = []string{KeyServerURL}

// Source names the layer a setting's value came from.
type Source string

const (
	SourceDefault Source = "default"
	SourceFile    Source = "file"
	SourceEnv     Source = "env"
	SourceFlag    Source = "flag"
)

// Options say whether and where to read the config file.
type Options struct {
	Path string    // config file; "" means DefaultPath()
	Warn io.Writer // receives the warnings about ignored keys
}

// Settings resolves the keys it was built for.
type Settings struct {
	v    *viper.Viper
	fs   *pflag.FlagSet
	env  map[string]string
	file map[string]bool
}

// New binds keys, flag names that must be in EnvNames, to their flags in
// fl (parsed already) and to their environment variables. A non-nil o also
// reads the config file for the keys in fileKeys.
func New(fl *pflag.FlagSet, keys []string, o *Options) (*Settings, error) {
	s := &Settings{v: viper.New(), fs: fl, env: map[string]string{}, file: map[string]bool{}}
	for _, k := range keys {
		env, ok := EnvNames[k]
		if !ok {
			return nil, fmt.Errorf("config: %q has no environment variable", k)
		}
		s.env[k] = env
		if err := s.v.BindEnv(k, env); err != nil {
			return nil, err
		}
		if f := fl.Lookup(k); f != nil {
			if err := s.v.BindPFlag(k, f); err != nil {
				return nil, err
			}
		}
	}
	if o == nil {
		return s, nil
	}
	path, err := ResolvePath(o.Path)
	if err != nil {
		return nil, err
	}
	vals, err := ReadFile(path, o.Warn)
	if err != nil {
		return nil, err
	}
	for _, k := range fileKeys {
		if val := vals[k]; val != "" {
			if err := s.v.MergeConfigMap(map[string]any{k: val}); err != nil {
				return nil, err
			}
			s.file[k] = true
		}
	}
	return s, nil
}

// String returns the value of key: its flag if given, else its environment
// variable if not empty, else its config file entry, else the flag's default.
func (s *Settings) String(key string) string { return s.v.GetString(key) }

// Int is String parsed as a whole number. A malformed environment variable
// is an error naming it; a malformed flag never gets this far.
func (s *Settings) Int(key string) (int, error) {
	raw := s.v.GetString(key)
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a whole number", s.env[key], raw)
	}
	return n, nil
}

// Source reports which layer String(key) came from.
func (s *Settings) Source(key string) Source {
	if f := s.fs.Lookup(key); f != nil && f.Changed {
		return SourceFlag
	}
	if env, ok := s.env[key]; ok && os.Getenv(env) != "" {
		return SourceEnv
	}
	if s.file[key] {
		return SourceFile
	}
	return SourceDefault
}

// ResolvePath returns path, or DefaultPath() when path is empty.
func ResolvePath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	return DefaultPath()
}

// DefaultPath is $XDG_CONFIG_HOME/custos/config.yaml, or
// ~/.config/custos/config.yaml; the XDG layout on every platform, macOS
// included.
func DefaultPath() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "custos", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "custos", "config.yaml"), nil
}

// ReadFile returns the entries of the config file at path that fileKeys
// allow. A missing file is empty. Any other key is left out, and warn (if
// not nil) is told, so a secret in a plain file is skipped visibly. The
// warning names the key and never prints its value.
func ReadFile(path string, warn io.Writer) (map[string]string, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("config file %s: %w", path, err)
	}
	vals := map[string]string{}
	for _, k := range v.AllKeys() {
		if k == KeyServerURL {
			vals[k] = v.GetString(k)
		} else if warn != nil {
			fmt.Fprintf(warn, "custos: config file %s: ignoring %q; only %q may be set there\n", path, k, KeyServerURL)
		}
	}
	return vals, nil
}

// CheckURL accepts absolute http and https URLs without user info, query
// or fragment.
func CheckURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%q is not an absolute http or https URL", s)
	}
	return nil
}

// Set writes key to the config file at path, keeping its other entries. The
// directory is created with mode 0700 and the file written with 0600
// through a temporary file and a rename, so a crash cannot leave half a file.
func Set(path, key, value string) error {
	if key != KeyServerURL {
		return fmt.Errorf("unknown key %q; only %q can be set", key, KeyServerURL)
	}
	if err := CheckURL(value); err != nil {
		return err
	}
	entries := map[string]any{}
	switch data, err := os.ReadFile(path); {
	case err == nil:
		if err := yaml.Unmarshal(data, &entries); err != nil {
			return fmt.Errorf("config file %s: %w", path, err)
		}
		if entries == nil {
			entries = map[string]any{}
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	entries[key] = value
	out, err := yaml.Marshal(entries)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp") // created with mode 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
