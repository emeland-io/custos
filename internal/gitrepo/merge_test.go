package gitrepo

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/gittest"
)

// sameFile compares file contents; nil (no file) differs from empty.
func sameFile(a, b []byte) bool {
	return (a == nil) == (b == nil) && bytes.Equal(a, b)
}

// mergeFixture makes a work repository where branch theirs and main both
// changed a.txt and added c.txt with different content, and theirs deleted
// d.txt while main changed it.
func mergeFixture(t *testing.T) (r *Repo, base, ours, theirs string) {
	t.Helper()
	dir := gittest.Init(t)
	base = gittest.Commit(t, dir, map[string]string{"a.txt": "base\n", "d.txt": "d\n", "keep.txt": "k\n"})
	gittest.Run(t, dir, "checkout", "-q", "-b", "theirs")
	if err := os.Remove(filepath.Join(dir, "d.txt")); err != nil {
		t.Fatal(err)
	}
	theirs = gittest.Commit(t, dir, map[string]string{"a.txt": "theirs\n", "c.txt": "c theirs\n"})
	gittest.Run(t, dir, "checkout", "-q", "main")
	ours = gittest.Commit(t, dir, map[string]string{"a.txt": "ours\n", "c.txt": "c ours\n", "d.txt": "d changed\n"})
	return &Repo{Dir: dir}, base, ours, theirs
}

// orphan commits a root commit on a new branch solo and returns it.
func orphan(t *testing.T, dir string) string {
	t.Helper()
	gittest.Run(t, dir, "checkout", "-q", "--orphan", "solo")
	c := gittest.Commit(t, dir, map[string]string{"solo.txt": "solo\n"})
	gittest.Run(t, dir, "checkout", "-q", "main")
	return c
}

func TestMergeTreeConflicts(t *testing.T) {
	r, _, ours, theirs := mergeFixture(t)
	res, err := r.MergeTree(ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if !mergeOIDRE.MatchString(res.Tree) {
		t.Errorf("tree = %q", res.Tree)
	}
	want := []ConflictedFile{
		{Path: "a.txt", Base: []byte("base\n"), Ours: []byte("ours\n"), Theirs: []byte("theirs\n")},
		{Path: "c.txt", Ours: []byte("c ours\n"), Theirs: []byte("c theirs\n")},
		{Path: "d.txt", Base: []byte("d\n"), Ours: []byte("d changed\n")},
	}
	if len(res.Conflicts) != len(want) {
		t.Fatalf("conflicts = %+v, want %d", res.Conflicts, len(want))
	}
	for i, w := range want {
		g := res.Conflicts[i]
		if g.Path != w.Path || !sameFile(g.Base, w.Base) || !sameFile(g.Ours, w.Ours) || !sameFile(g.Theirs, w.Theirs) {
			t.Errorf("conflict %d = %s base %q ours %q theirs %q, want %s base %q ours %q theirs %q",
				i, g.Path, g.Base, g.Ours, g.Theirs, w.Path, w.Base, w.Ours, w.Theirs)
		}
	}
}

func TestMergeTreeClean(t *testing.T) {
	dir := gittest.Init(t)
	gittest.Commit(t, dir, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	gittest.Run(t, dir, "checkout", "-q", "-b", "side")
	theirs := gittest.Commit(t, dir, map[string]string{"c.txt": "c\n"})
	gittest.Run(t, dir, "checkout", "-q", "main")
	ours := gittest.Commit(t, dir, map[string]string{"a.txt": "a2\n"})
	r := &Repo{Dir: dir}

	res, err := r.MergeTree(ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts %+v", res.Conflicts)
	}
	fsys, err := r.TreeFS(res.Tree)
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{"a.txt": "a2\n", "b.txt": "b\n", "c.txt": "c\n"} {
		if data, err := fs.ReadFile(fsys, p); err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", p, data, err, want)
		}
	}
}

func TestMergeTreeErrors(t *testing.T) {
	r, _, ours, _ := mergeFixture(t)
	if _, err := r.MergeTree("-x", ours); err == nil {
		t.Error("revisions starting with - must be rejected")
	}
	if _, err := r.MergeTree(ours, "does-not-exist"); err == nil {
		t.Error("unknown revision must fail")
	}
	solo := orphan(t, r.Dir)
	if _, err := r.MergeTree(ours, solo); err == nil {
		t.Error("unrelated histories must fail")
	}
}

func TestMergeBase(t *testing.T) {
	r, base, ours, theirs := mergeFixture(t)
	got, ok, err := r.MergeBase(ours, theirs)
	if err != nil || !ok || got != base {
		t.Errorf("MergeBase = %q, %v, %v; want %s", got, ok, err, base)
	}
	solo := orphan(t, r.Dir)
	if got, ok, err := r.MergeBase(ours, solo); err != nil || ok {
		t.Errorf("MergeBase of unrelated commits = %q, %v, %v; want no base, no error", got, ok, err)
	}
	if _, _, err := r.MergeBase("-x", ours); err == nil {
		t.Error("revisions starting with - must be rejected")
	}
}

func TestHasCommit(t *testing.T) {
	r, base, _, _ := mergeFixture(t)
	blob := gittest.Run(t, r.Dir, "rev-parse", base+":a.txt")
	for _, tc := range []struct {
		oid  string
		want bool
	}{{base, true}, {blob, false}, {strings.Repeat("1", 40), false}} {
		if got, err := r.HasCommit(tc.oid); err != nil || got != tc.want {
			t.Errorf("HasCommit(%s) = %v, %v; want %v", tc.oid, got, err, tc.want)
		}
	}
	if _, err := r.HasCommit("-x"); err == nil {
		t.Error("revisions starting with - must be rejected")
	}
}

func TestFetch(t *testing.T) {
	work := gittest.Init(t)
	first := gittest.Commit(t, work, map[string]string{"a.txt": "one"})
	second := gittest.Commit(t, work, map[string]string{"a.txt": "two"})
	src, err := InitBare(filepath.Join(t.TempDir(), "src.git"))
	if err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, work, "push", "--quiet", src.Dir, "main")
	dst, err := InitBare(filepath.Join(t.TempDir(), "dst.git"))
	if err != nil {
		t.Fatal(err)
	}

	oid, err := dst.Fetch(src, "refs/heads/main", "refs/custos/tmp")
	if err != nil || oid != second {
		t.Fatalf("Fetch = %q, %v; want %s", oid, err, second)
	}
	if got := gittest.Run(t, dst.Dir, "rev-parse", "refs/custos/tmp~1"); got != first {
		t.Errorf("history not copied: refs/custos/tmp~1 = %s, want %s", got, first)
	}
	if got := gittest.Run(t, dst.Dir, "for-each-ref", "--format=%(refname)"); got != "refs/custos/tmp" {
		t.Errorf("refs after fetch = %q, want only refs/custos/tmp", got)
	}
	if _, err := dst.Fetch(src, "refs/heads/nope", "refs/custos/x"); err == nil {
		t.Error("fetching a missing ref must fail")
	}
	if _, err := dst.Fetch(src, "main", "refs/custos/x"); err == nil {
		t.Error("short ref names must be rejected")
	}
}

func TestEditTree(t *testing.T) {
	dir := gittest.Init(t)
	c := gittest.Commit(t, dir, map[string]string{"a.txt": "one", "b.txt": "two"})
	tree := gittest.Run(t, dir, "rev-parse", c+"^{tree}")
	indexPath := filepath.Join(dir, ".git", "index")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(t.TempDir(), "inherited-index")
	t.Setenv("GIT_INDEX_FILE", stray)
	r := &Repo{Dir: dir}

	got, err := r.EditTree(tree, []Change{
		{Path: "a.txt", Data: []byte("changed")},
		{Path: "b.txt", Delete: true},
		{Path: "sub/c.txt", Data: []byte("three")},
		{Path: "empty.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	fsys, err := r.TreeFS(got)
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{"a.txt": "changed", "sub/c.txt": "three", "empty.txt": ""} {
		if data, err := fs.ReadFile(fsys, p); err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", p, data, err, want)
		}
	}
	if _, err := fs.Stat(fsys, "b.txt"); err == nil {
		t.Error("b.txt was not deleted")
	}
	old, err := r.TreeFS(tree)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := fs.ReadFile(old, "a.txt"); string(data) != "one" {
		t.Errorf("original tree changed: a.txt = %q", data)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Error("EditTree used the inherited GIT_INDEX_FILE")
	}
	if indexAfter, _ := os.ReadFile(indexPath); !bytes.Equal(indexBefore, indexAfter) {
		t.Error("EditTree changed the repository's index")
	}
	if _, err := r.EditTree("HEAD", nil); err == nil {
		t.Error("EditTree must require a full tree id")
	}
}
