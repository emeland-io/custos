package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gittest"
)

func TestHookUsage(t *testing.T) {
	const usageText = "usage: custos hook pre-receive"
	for _, args := range [][]string{{"hook"}, {"hook", "other"}} {
		code, _, errs := runCmd(t, args...)
		if code != 2 || !strings.Contains(errs, usageText) {
			t.Errorf("%v: code %d err %q", args, code, errs)
		}
	}
	for _, args := range [][]string{{"hook", "pre-receive"}, {"hook", "pre-receive", "--kind", "nope"}} {
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

// runHookIn runs `hook pre-receive --kind catalog` inside dir with the given
// ref updates on stdin, the way git runs the installed hook.
func runHookIn(t *testing.T, dir, stdin string) (int, string) {
	t.Helper()
	t.Chdir(dir)
	var out, errb bytes.Buffer
	code := run([]string{"hook", "pre-receive", "--kind", "catalog"}, strings.NewReader(stdin), &out, &errb)
	return code, errb.String()
}

func TestHookPreReceiveAccepts(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Catalog())
	zero := strings.Repeat("0", 40)
	code, errs := runHookIn(t, dir, zero+" "+c1+" refs/heads/main\n")
	if code != 0 || errs != "" {
		t.Errorf("code %d err %q", code, errs)
	}
	code, errs = runHookIn(t, dir, "")
	if code != 0 || errs != "" {
		t.Errorf("empty stdin: code %d err %q", code, errs)
	}
}

func TestHookPreReceiveRejects(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, fixture.Catalog())
	zero := strings.Repeat("0", 40)
	// Deleting main is rejected; only the stdin tells the hook so.
	code, errs := runHookIn(t, dir, c1+" "+zero+" refs/heads/main\n")
	if code != 1 || !strings.Contains(errs, "custos rejected the push:") || !strings.Contains(errs, "cannot be deleted") {
		t.Errorf("code %d err %q", code, errs)
	}
}
