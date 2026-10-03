package gitrepo

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/gittest"
)

func TestResolveRef(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, map[string]string{"a.txt": "one"})
	r := &Repo{Dir: dir}
	if oid, ok, err := r.ResolveRef("refs/heads/main"); oid != c1 || !ok || err != nil {
		t.Errorf("main: %q %v %v", oid, ok, err)
	}
	if oid, ok, err := r.ResolveRef("refs/heads/missing"); oid != "" || ok || err != nil {
		t.Errorf("missing: %q %v %v", oid, ok, err)
	}
	if _, ok, err := r.ResolveRef(strings.Repeat("1", 40) + "^{commit}"); ok || err != nil {
		t.Errorf("unknown commit: %v %v", ok, err)
	}
	if _, _, err := r.ResolveRef("--all"); err == nil {
		t.Error("options must be rejected")
	}
	if _, _, err := (&Repo{Dir: filepath.Join(t.TempDir(), "none")}).ResolveRef("main"); err == nil {
		t.Error("a missing repository must be an error, not a missing ref")
	}
}

// TestResolveRefExactRefName checks that a fully qualified ref name
// (refs/…) is resolved exactly, not through rev-parse's DWIM lookup, which
// would otherwise let a tag named refs/tags/refs/heads/main stand in for a
// missing refs/heads/main.
func TestResolveRefExactRefName(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, map[string]string{"a.txt": "one"})
	gittest.Run(t, dir, "update-ref", "refs/tags/refs/heads/main", c1)
	gittest.Run(t, dir, "update-ref", "-d", "refs/heads/main")
	r := &Repo{Dir: dir}
	if oid, ok, err := r.ResolveRef("refs/heads/main"); ok || err != nil {
		t.Errorf("DWIM must not let the tag stand in for main: %q %v %v", oid, ok, err)
	}
	if oid, ok, err := r.ResolveRef("refs/heads/main^{commit}"); ok || err != nil {
		t.Errorf("DWIM must not let the tag stand in for main (commit form): %q %v %v", oid, ok, err)
	}
	if oid, ok, err := r.ResolveRef("refs/tags/refs/heads/main"); oid != c1 || !ok || err != nil {
		t.Errorf("the tag itself must still resolve by its real name: %q %v %v", oid, ok, err)
	}
	if oid, ok, err := r.ResolveRef("refs/tags/refs/heads/main^{commit}"); oid != c1 || !ok || err != nil {
		t.Errorf("the tag must still peel to its commit: %q %v %v", oid, ok, err)
	}
}

// TestHookEnvironment checks that a Repo ignores the variables git sets for
// a hook, which point at another repository's objects, unless it is the
// repository the hook runs in.
func TestHookEnvironment(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, map[string]string{"a.txt": "one"})
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "other.git"))
	t.Setenv("GIT_OBJECT_DIRECTORY", t.TempDir())
	if oid, ok, err := (&Repo{Dir: dir}).ResolveRef(c1 + "^{commit}"); oid != c1 || !ok || err != nil {
		t.Errorf("clean environment: %q %v %v", oid, ok, err)
	}
	if _, ok, _ := (&Repo{Dir: dir, InheritGitEnv: true}).ResolveRef(c1 + "^{commit}"); ok {
		t.Error("InheritGitEnv must keep GIT_DIR and GIT_OBJECT_DIRECTORY")
	}
}

func TestReadFile(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, map[string]string{"a.txt": "one", "sub/b.txt": "two"})
	r := &Repo{Dir: dir}
	if data, ok, err := r.ReadFile(c1, "sub/b.txt"); string(data) != "two" || !ok || err != nil {
		t.Errorf("sub/b.txt: %q %v %v", data, ok, err)
	}
	if data, ok, err := r.ReadFile("main", "a.txt"); string(data) != "one" || !ok || err != nil {
		t.Errorf("a.txt at main: %q %v %v", data, ok, err)
	}
	for _, path := range []string{"missing.txt", "sub", "b.txt"} {
		if _, ok, err := r.ReadFile(c1, path); ok || err != nil {
			t.Errorf("%s: %v %v", path, ok, err)
		}
	}
	for _, path := range []string{"", "/a.txt", "../a.txt", ".git/config", "sub/"} {
		if _, _, err := r.ReadFile(c1, path); err == nil {
			t.Errorf("path %q must be rejected", path)
		}
	}
	if _, _, err := r.ReadFile("missing", "a.txt"); err == nil {
		t.Error("an unknown revision must fail")
	}
	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	c2 := gittest.Commit(t, dir, nil)
	if _, _, err := r.ReadFile(c2, "link"); err == nil || !strings.Contains(err.Error(), "only regular files") {
		t.Errorf("symlink: %v", err)
	}
}

func TestRefs(t *testing.T) {
	dir := gittest.Init(t)
	c1 := gittest.Commit(t, dir, map[string]string{"a.txt": "one"})
	gittest.Run(t, dir, "branch", "custos/pin/x")
	gittest.Run(t, dir, "branch", "custos/pinned")
	gittest.Run(t, dir, "tag", "v1")
	r := &Repo{Dir: dir}
	refs, err := r.Refs("refs/heads/custos/pin/")
	if err != nil || !maps.Equal(refs, map[string]string{"refs/heads/custos/pin/x": c1}) {
		t.Errorf("pin branches: %v %v", refs, err)
	}
	if all, err := r.Refs(""); err != nil || len(all) != 4 || all["refs/tags/v1"] != c1 {
		t.Errorf("all refs: %v %v", all, err)
	}
	empty, err := InitBare(filepath.Join(t.TempDir(), "e.git"))
	if err != nil {
		t.Fatal(err)
	}
	if refs, err := empty.Refs(""); err != nil || len(refs) != 0 {
		t.Errorf("empty repository: %v %v", refs, err)
	}
}
