package main

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
)

func TestTaskNewVersion(t *testing.T) {
	dir := t.TempDir()
	fixture.WriteDir(t, dir, fixture.Catalog())
	code, out, errs := runCmd(t, "task", "new-version", "--major", "--dir", dir, fixture.TaskA)
	if code != 0 || out != fixture.TaskPath(fixture.TaskA, "2.0.0")+"\n" {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
	if code, out, _ := runCmd(t, "validate", dir); code != 0 {
		t.Errorf("catalog invalid after new-version: %s", out)
	}
}

func TestTaskNewVersionUsage(t *testing.T) {
	for _, args := range [][]string{
		{"task"},
		{"task", "new-version", fixture.TaskA},
		{"task", "new-version", "--minor", "--major", fixture.TaskA},
		{"task", "rename"},
	} {
		if code, _, _ := runCmd(t, args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
	code, _, errs := runCmd(t, "task", "new-version", "--minor", "--dir", t.TempDir(), fixture.TaskA)
	if code != 1 || !strings.Contains(errs, "not found") {
		t.Errorf("code %d err %q", code, errs)
	}
}
