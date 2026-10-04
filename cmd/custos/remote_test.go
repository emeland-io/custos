package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCloneUsage(t *testing.T) {
	for _, args := range [][]string{{"clone"}, {"clone", "http://127.0.0.1:8080/git/catalog.git"}, {"clone", "a", "b", "c"}} {
		if code, _, _ := runCmd(t, args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
}

func TestCloneRejectsNonCustosURL(t *testing.T) {
	code, _, errs := runCmd(t, "clone", "https://example.org/repo.git", filepath.Join(t.TempDir(), "x"))
	if code != 1 || !strings.Contains(errs, "custos clone:") || !strings.Contains(errs, "/git/") {
		t.Errorf("code %d err %q", code, errs)
	}
}

func TestPushBadAuthor(t *testing.T) {
	code, _, errs := runCmd(t, "push", "--author", "Jane")
	if code != 2 || !strings.Contains(errs, "--author") {
		t.Errorf("code %d err %q", code, errs)
	}
}

func TestPushOutsideCheckout(t *testing.T) {
	code, _, errs := runCmd(t, "push", "--dir", t.TempDir(), "--author", "Jane Doe <jane@example.org>")
	if code != 1 || !strings.Contains(errs, "custos push:") {
		t.Errorf("code %d err %q", code, errs)
	}
}

func TestPushAuthorFromEnvironment(t *testing.T) {
	t.Setenv("CUSTOS_AUTHOR", "Jane")
	code, _, errs := runCmd(t, "push", "--dir", t.TempDir())
	if code != 2 || !strings.Contains(errs, "--author") {
		t.Errorf("code %d err %q", code, errs)
	}
}
