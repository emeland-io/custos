package main

import (
	"strings"
	"testing"
)

func TestHookUsage(t *testing.T) {
	for _, args := range [][]string{{"hook"}, {"hook", "other"}, {"hook", "pre-receive"}, {"hook", "pre-receive", "--kind", "nope"}} {
		code, _, errs := runCmd(t, args...)
		if code != 2 || errs == "" {
			t.Errorf("%v: code %d err %q", args, code, errs)
		}
	}
}

func TestHookIsNotListedInHelp(t *testing.T) {
	_, out, errs := cobraCmd(t, "-h")
	if strings.Contains(out+errs, "pre-receive") {
		t.Errorf("the internal hook command is listed:\n%s", out+errs)
	}
}
