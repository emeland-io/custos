package gitrepo

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/gittest"
)

func TestIsZero(t *testing.T) {
	if !IsZero(strings.Repeat("0", 40)) || !IsZero(strings.Repeat("0", 64)) || IsZero("") || IsZero("0a") {
		t.Error("IsZero is wrong")
	}
}

func TestInitBare(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x.git")
	if _, err := InitBare(dir); err != nil {
		t.Fatal(err)
	}
	if got := gittest.Run(t, dir, "config", "http.receivepack"); got != "true" {
		t.Errorf("http.receivepack = %q", got)
	}
	if got := gittest.Run(t, dir, "symbolic-ref", "HEAD"); got != "refs/heads/main" {
		t.Errorf("HEAD = %q", got)
	}
}

func TestInstallHookOverwrites(t *testing.T) {
	r, err := InitBare(filepath.Join(t.TempDir(), "x.git"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"#!/bin/sh\necho one\n", "#!/bin/sh\necho two\n"} {
		if err := r.InstallHook("pre-receive", s); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(r.Dir, "hooks", "pre-receive")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "two") {
		t.Fatalf("hook %q %v", data, err)
	}
	if info, _ := os.Stat(path); info.Mode()&0o100 == 0 {
		t.Error("hook is not executable")
	}
}

func TestTreeFS(t *testing.T) {
	dir := gittest.Init(t)
	first := gittest.Commit(t, dir, map[string]string{"a.txt": "one", "sub/b.txt": "two"})
	gittest.Commit(t, dir, map[string]string{"a.txt": "changed"})
	r := &Repo{Dir: dir}

	fsys, err := r.TreeFS(first)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := fs.ReadFile(fsys, "a.txt"); string(data) != "one" {
		t.Errorf("a.txt at first commit = %q", data)
	}
	if data, _ := fs.ReadFile(fsys, "sub/b.txt"); string(data) != "two" {
		t.Errorf("sub/b.txt = %q", data)
	}
	if _, err := r.TreeFS("--output=/tmp/x"); err == nil {
		t.Error("revisions starting with - must be rejected")
	}
	if _, err := r.TreeFS("does-not-exist"); err == nil {
		t.Error("unknown revision must fail")
	}
}

func TestTreeFSRejectsSymlinks(t *testing.T) {
	dir := gittest.Init(t)
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	rev := gittest.Commit(t, dir, nil)
	if _, err := (&Repo{Dir: dir}).TreeFS(rev); err == nil || !strings.Contains(err.Error(), "only regular files") {
		t.Errorf("err = %v", err)
	}
}

func TestIsAncestor(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, map[string]string{"a": "1"})
	c2 := gittest.Commit(t, dir, map[string]string{"a": "2"})
	r := &Repo{Dir: dir}
	if ok, err := r.IsAncestor(c1, c2); !ok || err != nil {
		t.Errorf("c1 before c2: %v %v", ok, err)
	}
	if ok, err := r.IsAncestor(c2, c1); ok || err != nil {
		t.Errorf("c2 before c1: %v %v", ok, err)
	}
}
