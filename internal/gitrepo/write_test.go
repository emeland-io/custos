package gitrepo

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/gittest"
)

var jane = Signature{Name: "Jane Doe", Email: "jane@example.org"}

func bare(t *testing.T) *Repo {
	t.Helper()
	r, err := InitBare(filepath.Join(t.TempDir(), "x.git"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func readAll(t *testing.T, r *Repo, rev string) map[string]string {
	t.Helper()
	fsys, err := r.TreeFS(rev)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := fs.ReadFile(fsys, p)
			files[p] = string(data)
		}
		return err
	})
	return files
}

func TestWriteCommit(t *testing.T) {
	r := bare(t)
	c1, err := r.WriteCommit(CommitRequest{
		Changes: []Change{{Path: "a.txt", Data: []byte("one")}, {Path: "sub/b.txt", Data: []byte("two")}, {Path: "gone.txt", Delete: true}},
		Author:  jane,
		Message: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r, c1); len(got) != 2 || got["a.txt"] != "one" || got["sub/b.txt"] != "two" {
		t.Errorf("first tree: %v", got)
	}
	c2, err := r.WriteCommit(CommitRequest{
		Base:    c1,
		Parents: []string{c1},
		Changes: []Change{{Path: "a.txt", Delete: true}, {Path: "sub/c.txt", Data: []byte("three")}},
		Author:  jane,
		Message: "second",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r, c2); len(got) != 2 || got["sub/b.txt"] != "two" || got["sub/c.txt"] != "three" {
		t.Errorf("second tree: %v", got)
	}
	if parent := gittest.Run(t, r.Dir, "rev-parse", c2+"^"); parent != c1 {
		t.Errorf("parent = %s, want %s", parent, c1)
	}
	if who := gittest.Run(t, r.Dir, "log", "-1", "--format=%an <%ae>|%cn <%ce>|%B", c2); who != "Jane Doe <jane@example.org>|custos-bot <custos-bot@localhost>|second" {
		t.Errorf("commit: %q", who)
	}
	if refs, _ := r.Refs(""); len(refs) != 0 {
		t.Errorf("WriteCommit must not move refs: %v", refs)
	}
}

func TestWriteCommitLeavesIndexAlone(t *testing.T) {
	dir := gittest.Init(t)
	base := gittest.Commit(t, dir, map[string]string{"a.txt": "one"})
	before := gittest.Run(t, dir, "ls-files", "-s")
	outside := filepath.Join(t.TempDir(), "index")
	t.Setenv("GIT_INDEX_FILE", outside)
	r := &Repo{Dir: dir}
	if _, err := r.WriteCommit(CommitRequest{Base: base, Parents: []string{base}, Changes: []Change{{Path: "b.txt", Data: []byte("x")}}, Author: jane, Message: "m"}); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("GIT_INDEX_FILE") // t.Setenv restores it after the test
	if after := gittest.Run(t, dir, "ls-files", "-s"); after != before {
		t.Errorf("index changed:\n%s\nwas\n%s", after, before)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Error("an inherited GIT_INDEX_FILE was written")
	}
}

func TestWriteCommitRejects(t *testing.T) {
	r := bare(t)
	for _, req := range []CommitRequest{
		{Changes: []Change{{Path: ".git/config", Data: []byte("x")}}, Author: jane, Message: "m"},
		{Changes: []Change{{Path: "../x", Data: []byte("x")}}, Author: jane, Message: "m"},
		{Changes: []Change{{Path: "/abs", Data: []byte("x")}}, Author: jane, Message: "m"},
		{Changes: []Change{{Path: "a.txt", Data: []byte("x")}}, Author: Signature{Name: "No Email"}, Message: "m"},
		{Base: strings.Repeat("1", 40), Author: jane, Message: "m"},
		{Base: "--all", Author: jane, Message: "m"},
	} {
		if _, err := r.WriteCommit(req); err == nil {
			t.Errorf("accepted %+v", req)
		}
	}
}

func TestWriteCommitIgnoresSigningConfig(t *testing.T) {
	r := bare(t)
	gittest.Run(t, r.Dir, "config", "commit.gpgsign", "true")
	gittest.Run(t, r.Dir, "config", "gpg.program", "false")
	if _, err := r.WriteCommit(CommitRequest{Author: jane, Message: "m"}); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateAndDeleteRef(t *testing.T) {
	r := bare(t)
	c1, err := r.WriteCommit(CommitRequest{Changes: []Change{{Path: "a", Data: []byte("1")}}, Author: jane, Message: "1"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := r.WriteCommit(CommitRequest{Base: c1, Parents: []string{c1}, Changes: []Change{{Path: "a", Data: []byte("2")}}, Author: jane, Message: "2"})
	if err != nil {
		t.Fatal(err)
	}
	const main = "refs/heads/main"
	if err := r.UpdateRef(main, c1, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef(main, c2, ""); !errors.Is(err, ErrRefMoved) {
		t.Errorf("create over existing ref: %v", err)
	}
	if err := r.UpdateRef(main, c2, c2); !errors.Is(err, ErrRefMoved) {
		t.Errorf("wrong expected value: %v", err)
	}
	if err := r.UpdateRef(main, c2, c1); err != nil {
		t.Fatal(err)
	}
	if oid, _, _ := r.ResolveRef(main); oid != c2 {
		t.Errorf("main = %s, want %s", oid, c2)
	}
	if err := r.UpdateRef("refs/heads/bad..name", c1, ""); err == nil || errors.Is(err, ErrRefMoved) {
		t.Errorf("bad name: %v", err)
	}
	if err := r.UpdateRef("main", c1, ""); err == nil {
		t.Error("a ref outside refs/ must be rejected")
	}
	if err := r.UpdateRef(main, "HEAD", c2); err == nil {
		t.Error("an abbreviated new value must be rejected")
	}
	if err := r.DeleteRef(main, c1); !errors.Is(err, ErrRefMoved) {
		t.Errorf("delete with wrong value: %v", err)
	}
	if err := r.DeleteRef(main, c2); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := r.ResolveRef(main); ok {
		t.Error("main still exists")
	}
	if err := r.DeleteRef(main, c2); !errors.Is(err, ErrRefMoved) {
		t.Errorf("delete of a missing ref: %v", err)
	}
}
