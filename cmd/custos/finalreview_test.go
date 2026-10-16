package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A nil args slice must mean "no arguments", not "the process arguments".
func TestRunCobraNilArgsIgnoresOSArgs(t *testing.T) {
	old := os.Args
	os.Args = []string{"custos", "validate", "/nonexistent-dir-for-test"}
	t.Cleanup(func() { os.Args = old })
	var out, errb bytes.Buffer
	code := runCobra(nil, nil, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "Usage:") || strings.Contains(errb.String(), "nonexistent-dir-for-test") {
		t.Errorf("nil args: %d %q", code, errb.String())
	}
}

func TestConfigGetRejectsInvalidValues(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(good, []byte("server-url: https://file.example.org\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("server-url: \"ftp://evil?x#y\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CUSTOS_SERVER_URL", "")
	if code, out, errs := cobraCmd(t, "--config", good, "config", "get", "server-url", "--server-url", "ftp://x"); code != 1 || out != "" || !strings.Contains(errs, "flag") || !strings.Contains(errs, "ftp://x") {
		t.Errorf("flag: %d %q %q", code, out, errs)
	}
	if code, out, errs := cobraCmd(t, "--config", bad, "config", "get", "server-url"); code != 1 || out != "" || !strings.Contains(errs, "file "+bad) {
		t.Errorf("file: %d %q %q", code, out, errs)
	}
	t.Setenv("CUSTOS_SERVER_URL", "12345")
	if code, out, errs := cobraCmd(t, "--config", good, "config", "get", "server-url"); code != 1 || out != "" || !strings.Contains(errs, "env CUSTOS_SERVER_URL") {
		t.Errorf("env: %d %q %q", code, out, errs)
	}
	t.Setenv("CUSTOS_SERVER_URL", "")
	if code, out, errs := cobraCmd(t, "--config", good, "config", "get", "server-url"); code != 0 || out != "https://file.example.org\n" {
		t.Errorf("valid: %d %q %q", code, out, errs)
	}
}

func TestPushUsageLine(t *testing.T) {
	code, out, _ := cobraCmd(t, "push", "-h")
	if code != 0 || !strings.Contains(out, "push [--dir DIR] [REMOTE [GIT-PUSH-ARGS...]]\n") {
		t.Errorf("push -h: %d %q", code, out)
	}
}
