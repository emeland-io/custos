package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// newFlags returns a parsed flag set with the flags the tests resolve.
func newFlags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	fl := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fl.String("server-url", "", "")
	fl.String("data-dir", "", "")
	fl.Int("processor-workers", 2, "")
	fl.String("dir", "", "") // not in EnvNames
	if err := fl.Parse(args); err != nil {
		t.Fatal(err)
	}
	return fl
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrecedence(t *testing.T) {
	const fileURL, envURL, flagURL = "https://file.example.org", "https://env.example.org", "https://flag.example.org"
	cases := []struct {
		name     string
		file     string // "" = no config file
		env      string
		args     []string
		want     string
		wantFrom Source
	}{
		{"nothing set", "", "", nil, "", SourceDefault},
		{"file only", fileURL, "", nil, fileURL, SourceFile},
		{"env beats file", fileURL, envURL, nil, envURL, SourceEnv},
		{"flag beats env", fileURL, envURL, []string{"--server-url", flagURL}, flagURL, SourceFlag},
		{"flag beats file", fileURL, "", []string{"--server-url", flagURL}, flagURL, SourceFlag},
		{"empty env is unset", fileURL, "", nil, fileURL, SourceFile},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("CUSTOS_SERVER_URL", c.env)
			path := filepath.Join(t.TempDir(), "absent.yaml")
			if c.file != "" {
				path = writeConfig(t, "server-url: "+c.file+"\n")
			}
			s, err := New(newFlags(t, c.args...), []string{"server-url"}, &Options{Path: path})
			if err != nil {
				t.Fatal(err)
			}
			if got := s.String("server-url"); got != c.want {
				t.Errorf("value %q, want %q", got, c.want)
			}
			if got := s.Source("server-url"); got != c.wantFrom {
				t.Errorf("source %q, want %q", got, c.wantFrom)
			}
		})
	}
}

func TestEnvBelowFlagWithoutFile(t *testing.T) {
	t.Setenv("CUSTOS_DATA_DIR", "/from/env")
	s, err := New(newFlags(t), []string{"data-dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.String("data-dir"); got != "/from/env" {
		t.Errorf("env: %q", got)
	}
	s, err = New(newFlags(t, "--data-dir", "/from/flag"), []string{"data-dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.String("data-dir"); got != "/from/flag" {
		t.Errorf("flag: %q", got)
	}
	// An explicit empty flag still wins over the environment, as before.
	s, err = New(newFlags(t, "--data-dir="), []string{"data-dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.String("data-dir"); got != "" {
		t.Errorf("empty flag: %q", got)
	}
}

func TestFileOnlyServerURL(t *testing.T) {
	t.Setenv("CUSTOS_DATA_DIR", "")
	path := writeConfig(t, "server-url: https://a.example.org\ndata-dir: /secret\noidc-client-secret: hunter2\n")
	var warn bytes.Buffer
	s, err := New(newFlags(t), []string{"server-url", "data-dir"}, &Options{Path: path, Warn: &warn})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.String("data-dir"); got != "" {
		t.Errorf("data-dir leaked from the file: %q", got)
	}
	for _, key := range []string{"data-dir", "oidc-client-secret"} {
		if !strings.Contains(warn.String(), `"`+key+`"`) {
			t.Errorf("no warning about %s:\n%s", key, warn.String())
		}
	}
	if strings.Contains(warn.String(), "hunter2") {
		t.Errorf("warning prints a secret value:\n%s", warn.String())
	}
}

func TestMalformedFile(t *testing.T) {
	path := writeConfig(t, "server-url: [unterminated\n")
	if _, err := New(newFlags(t), []string{"server-url"}, &Options{Path: path}); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("err %v", err)
	}
}

func TestUnlistedFlagHasNoEnv(t *testing.T) {
	if _, err := New(newFlags(t), []string{"dir"}, nil); err == nil {
		t.Error("binding a flag that is not in EnvNames must fail")
	}
}

func TestInt(t *testing.T) {
	t.Setenv("CUSTOS_PROCESSOR_WORKERS", "")
	s, _ := New(newFlags(t), []string{"processor-workers"}, nil)
	if n, err := s.Int("processor-workers"); err != nil || n != 2 {
		t.Errorf("default: %d %v", n, err)
	}
	t.Setenv("CUSTOS_PROCESSOR_WORKERS", "5")
	s, _ = New(newFlags(t), []string{"processor-workers"}, nil)
	if n, err := s.Int("processor-workers"); err != nil || n != 5 {
		t.Errorf("env: %d %v", n, err)
	}
	s, _ = New(newFlags(t, "--processor-workers", "7"), []string{"processor-workers"}, nil)
	if n, err := s.Int("processor-workers"); err != nil || n != 7 {
		t.Errorf("flag: %d %v", n, err)
	}
	t.Setenv("CUSTOS_PROCESSOR_WORKERS", "many")
	s, _ = New(newFlags(t), []string{"processor-workers"}, nil)
	if _, err := s.Int("processor-workers"); err == nil || !strings.Contains(err.Error(), "CUSTOS_PROCESSOR_WORKERS") {
		t.Errorf("malformed env: %v", err)
	}
	// A flag that overrides a malformed variable makes it irrelevant.
	s, _ = New(newFlags(t, "--processor-workers", "3"), []string{"processor-workers"}, nil)
	if n, err := s.Int("processor-workers"); err != nil || n != 3 {
		t.Errorf("flag over malformed env: %d %v", n, err)
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if p, _ := DefaultPath(); p != "/xdg/custos/config.yaml" {
		t.Errorf("xdg: %s", p)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative/is/ignored")
	t.Setenv("HOME", "/home/jane")
	if p, _ := DefaultPath(); p != "/home/jane/.config/custos/config.yaml" {
		t.Errorf("home: %s", p)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if _, err := DefaultPath(); err == nil {
		t.Error("no HOME and no XDG_CONFIG_HOME must be an error, not a guess")
	}
}

func TestSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	if err := Set(path, "data-dir", "/x"); err == nil {
		t.Error("only server-url may be set")
	}
	for _, bad := range []string{"", "ftp://x", "custos.example.org", "https://u:p@x", "https://x?q=1"} {
		if err := Set(path, KeyServerURL, bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if err := Set(path, KeyServerURL, "https://one.example.org"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", info, err)
	}
	if dir, _ := os.Stat(filepath.Dir(path)); dir.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", dir.Mode())
	}
	// Another entry survives a later set, and the old value is replaced.
	if err := os.WriteFile(path, []byte("server-url: https://one.example.org\nnote: keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Set(path, KeyServerURL, "https://two.example.org"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "https://two.example.org") || strings.Contains(string(data), "one.example.org") || !strings.Contains(string(data), "note: keep me") {
		t.Errorf("file:\n%s", data)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestSetUnderAFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "custos", "config.yaml")
	if err := Set(path, KeyServerURL, "https://one.example.org"); err == nil {
		t.Error("a parent that is a regular file must be an error")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(blocker), ".config-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}
