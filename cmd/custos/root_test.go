package main

import (
	"bytes"
	"strings"
	"testing"
)

// cobraCmd runs args through the cobra tree directly, whatever legacyCLI says.
func cobraCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runCobra(args, nil, &out, &errb)
	return code, out.String(), errb.String()
}

func TestRunCobraRoot(t *testing.T) {
	if code, _, errs := cobraCmd(t); code != 2 || !strings.Contains(errs, "Usage:") {
		t.Errorf("no args: %d %q", code, errs)
	}
	if code, _, errs := cobraCmd(t, "frobnicate"); code != 2 || !strings.Contains(errs, `unknown command "frobnicate"`) {
		t.Errorf("unknown command: %d %q", code, errs)
	}
	if code, _, errs := cobraCmd(t, "--nope"); code != 2 || !strings.Contains(errs, "unknown flag: --nope") {
		t.Errorf("unknown flag: %d %q", code, errs)
	}
	if code, out, errs := cobraCmd(t, "-h"); code != 0 || !strings.Contains(out, "Usage:") || errs != "" {
		t.Errorf("-h: %d out %q err %q", code, out, errs)
	}
}

// Shell completion is cobra's own command and stays enabled (ruling 5.16).
func TestCompletion(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		code, out, errs := cobraCmd(t, "completion", shell)
		if code != 0 || !strings.Contains(out, "custos") || errs != "" {
			t.Errorf("completion %s: %d out %.80q err %q", shell, code, out, errs)
		}
	}
	// Misuse exits 2 and shows usage, like the other commands that only group subcommands.
	for _, args := range [][]string{{"completion"}, {"completion", "bogus"}} {
		if code, out, errs := cobraCmd(t, args...); code != 2 || out != "" || !strings.Contains(errs, "Usage:") {
			t.Errorf("%v: %d out %.80q err %.120q", args, code, out, errs)
		}
	}
	if code, out, errs := cobraCmd(t, "completion", "bash", "extra"); code != 2 || out != "" || !strings.Contains(errs, `unknown command "extra"`) {
		t.Errorf("completion bash extra: %d out %.80q err %.120q", code, out, errs)
	}
	if code, out, _ := cobraCmd(t, "completion", "-h"); code != 0 || !strings.Contains(out, "bash") {
		t.Errorf("completion -h: %d out %.120q", code, out)
	}
	if code, out, _ := cobraCmd(t, "-h"); code != 0 || !strings.Contains(out, "completion") {
		t.Errorf("completion is not listed in help:\n%s", out)
	}
}
