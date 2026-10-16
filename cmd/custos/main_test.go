package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
)

func TestHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, nil, &out, &errb); code != 0 {
		t.Fatalf("exit code %d, stderr %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "validate") {
		t.Errorf("usage does not mention validate:\n%s", out.String())
	}
}

func TestNoArguments(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, nil, &out, &errb); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"frobnicate"}, nil, &out, &errb); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	if !strings.Contains(errb.String(), `unknown command "frobnicate"`) {
		t.Errorf("stderr: %s", errb.String())
	}
}

func TestEveryCommandIsListed(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, nil, &out, &errb); code != 0 {
		t.Fatalf("exit code %d, stderr %s", code, errb.String())
	}
	for _, name := range []string{"serve", "validate", "task", "workspace", "clone", "push", "token", "processor"} {
		if !strings.Contains(out.String(), name) {
			t.Errorf("help does not list %q:\n%s", name, out.String())
		}
	}
}

func TestGlobalFlagsComeBeforeTheCommand(t *testing.T) {
	if legacyCLI {
		t.Skip("the old dispatcher has no global flags")
	}
	dir := t.TempDir()
	fixture.WriteDir(t, dir, fixture.Catalog())
	if code, out, errs := runCmd(t, "--config", filepath.Join(t.TempDir(), "none.yaml"), "validate", dir); code != 0 || out != "ok\n" {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
}
