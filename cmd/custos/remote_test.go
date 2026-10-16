package main

import (
	"io"
	"path/filepath"
	"reflect"
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

func TestPushPassesArgumentsToGit(t *testing.T) {
	type call struct {
		dir, remote string
		args        []string
	}
	var got call
	old := remotePush
	remotePush = func(dir, remoteName string, args []string, _ io.Writer) error {
		got = call{dir, remoteName, args}
		return nil
	}
	t.Cleanup(func() { remotePush = old })

	for _, tc := range []struct {
		name string
		args []string
		want call
	}{
		{"defaults", []string{"push"}, call{".", "origin", nil}},
		{"remote only", []string{"push", "upstream"}, call{".", "upstream", []string{}}},
		{"dir before the remote", []string{"push", "--dir", "x", "origin", "--force", "main"}, call{"x", "origin", []string{"--force", "main"}}},
		{"flags after the remote belong to git", []string{"push", "origin", "--dir", "y"}, call{".", "origin", []string{"--dir", "y"}}},
		{"double dash", []string{"push", "origin", "--", "--weird"}, call{".", "origin", []string{"--", "--weird"}}},
	} {
		got = call{}
		if code, _, errs := runCmd(t, tc.args...); code != 0 {
			t.Fatalf("%s: code %d err %q", tc.name, code, errs)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
