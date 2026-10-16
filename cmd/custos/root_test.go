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
