package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gittest"
)

func runCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, nil, &out, &errb)
	return code, out.String(), errb.String()
}

func TestValidateCatalog(t *testing.T) {
	dir := t.TempDir()
	fixture.WriteDir(t, dir, fixture.Catalog())
	if code, out, errs := runCmd(t, "validate", dir); code != 0 || out != "ok\n" {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
	fixture.WriteDir(t, dir, map[string]string{"groups/index.yaml": "groups: [missing]\n"})
	code, out, errs := runCmd(t, "validate", dir)
	if code != 1 || !strings.Contains(out, "groups/index.yaml: groups: lists unknown group") || !strings.Contains(errs, "problem(s) found") {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
}

func TestValidateWorkspace(t *testing.T) {
	dir := t.TempDir()
	fixture.WriteDir(t, dir, fixture.Workspace())
	if code, out, errs := runCmd(t, "validate", dir); code != 0 {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
	if code, _, errs := runCmd(t, "validate", "--against", "HEAD", dir); code != 2 || !strings.Contains(errs, "only applies to catalogs") {
		t.Fatalf("code %d err %q", code, errs)
	}
}

func TestValidateAgainst(t *testing.T) {
	dir := gittest.Init(t)
	gittest.Commit(t, dir, fixture.Catalog())
	path := fixture.TaskPath(fixture.TaskA, "1.0.0")
	fixture.WriteDir(t, dir, map[string]string{path: fixture.TaskFile(fixture.TaskA, "1.0.0") + "Edited.\n"})
	code, out, errs := runCmd(t, "validate", "--against", "HEAD", dir)
	if code != 1 || !strings.Contains(out, path+": immutable: was changed") {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
}

func TestValidateTooManyArguments(t *testing.T) {
	if code, _, _ := runCmd(t, "validate", "a", "b"); code != 2 {
		t.Errorf("code %d", code)
	}
}
