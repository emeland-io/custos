package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigPrecedenceEndToEnd(t *testing.T) {
	t.Setenv("CUSTOS_SERVER_URL", "")
	path := filepath.Join(t.TempDir(), "c.yaml")
	if code, _, errs := cobraCmd(t, "--config", path, "config", "get", "server-url"); code != 1 || !strings.Contains(errs, "not set") {
		t.Errorf("unset: %d %q", code, errs)
	}
	if code, _, errs := cobraCmd(t, "--config", path, "config", "set", "server-url", "nope"); code != 2 {
		t.Errorf("bad url: %d %q", code, errs)
	}
	if code, out, errs := cobraCmd(t, "--config", path, "config", "set", "server-url", "https://file.example.org"); code != 0 {
		t.Errorf("set: %d %q %q", code, out, errs)
	}
	code, out, _ := cobraCmd(t, "--config", path, "config", "get", "server-url", "--show-source")
	if code != 0 || out != "https://file.example.org (file "+path+")\n" {
		t.Errorf("file: %d %q", code, out)
	}
	t.Setenv("CUSTOS_SERVER_URL", "https://env.example.org")
	if _, out, _ := cobraCmd(t, "--config", path, "config", "get", "server-url", "--show-source"); out != "https://env.example.org (env CUSTOS_SERVER_URL)\n" {
		t.Errorf("env: %q", out)
	}
	if _, out, _ := cobraCmd(t, "--config", path, "config", "get", "server-url", "--server-url", "https://flag.example.org"); out != "https://flag.example.org\n" {
		t.Errorf("flag: %q", out)
	}
	if _, out, _ := cobraCmd(t, "config", "get", "server-url", "--server-url", "https://flag.example.org", "--show-source", "--config", path); out != "https://flag.example.org (flag)\n" {
		t.Errorf("flag source: %q", out)
	}
}

func TestConfigUsage(t *testing.T) {
	for _, args := range [][]string{
		{"config"},
		{"config", "get"},
		{"config", "get", "data-dir"}, // not a key
		{"config", "set", "server-url"},
		{"config", "set", "data-dir", "/x"},
	} {
		if code, _, errs := cobraCmd(t, args...); code != 2 || errs == "" {
			t.Errorf("%v: code %d err %q", args, code, errs)
		}
	}
}

func TestConfigGetWarnsAboutForeignKeys(t *testing.T) {
	t.Setenv("CUSTOS_SERVER_URL", "")
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("server-url: https://a.example.org\noidc-client-secret: hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := cobraCmd(t, "--config", path, "config", "get", "server-url")
	if code != 0 || out != "https://a.example.org\n" {
		t.Fatalf("code %d out %q", code, out)
	}
	if !strings.Contains(errs, `"oidc-client-secret"`) || strings.Contains(errs, "hunter2") {
		t.Errorf("warning: %q", errs)
	}
}

func TestConfigBrokenFileDoesNotStopOtherCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("server-url: [unterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(path))
	if code, _, errs := cobraCmd(t, "--config", path, "config", "get", "server-url"); code != 1 || !strings.Contains(errs, path) {
		t.Errorf("config get on a broken file: %d %q", code, errs)
	}
	// Commands that never read the file are unaffected: see TestValidate*
	// in Task 4, which run with XDG_CONFIG_HOME pointing at a broken file.
}

func TestConfigNoHome(t *testing.T) {
	t.Setenv("CUSTOS_SERVER_URL", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if code, _, errs := cobraCmd(t, "config", "get", "server-url"); code != 1 || errs == "" {
		t.Errorf("get: %d %q", code, errs)
	}
	if code, _, errs := cobraCmd(t, "config", "set", "server-url", "https://x.example.org"); code != 1 || errs == "" {
		t.Errorf("set: %d %q", code, errs)
	}
}

func TestConfigSetUnderAFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "custos", "config.yaml")
	if code, _, errs := cobraCmd(t, "--config", path, "config", "set", "server-url", "https://x.example.org"); code != 1 || errs == "" {
		t.Errorf("%d %q", code, errs)
	}
}
