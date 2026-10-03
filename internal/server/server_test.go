package server

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gittest"
)

// custosBin is a custos binary built for these tests, because the
// pre-receive hook runs it.
var custosBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "custos-bin-*")
	if err != nil {
		panic(err)
	}
	custosBin = filepath.Join(dir, "custos")
	if out, err := exec.Command("go", "build", "-o", custosBin, "github.com/emeland-io/custos/cmd/custos").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build custos: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func start(t *testing.T) (*Server, string) {
	t.Helper()
	srv, err := Open(t.TempDir(), custosBin)
	if err != nil {
		t.Fatal(err)
	}
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return srv, ts.URL
}

func TestPushAndCloneCatalog(t *testing.T) {
	_, url := start(t)
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", url+"/git/catalog.git", "main")

	clone := filepath.Join(t.TempDir(), "c")
	gittest.Run(t, t.TempDir(), "clone", "--quiet", url+"/git/catalog.git", clone)
	if _, err := os.Stat(filepath.Join(clone, "groups", "index.yaml")); err != nil {
		t.Error(err)
	}
}

func TestInvalidPushIsRejected(t *testing.T) {
	_, url := start(t)
	work := gittest.Init(t)
	f := fixture.Catalog()
	f["groups/index.yaml"] = "groups: [missing]\n"
	gittest.Commit(t, work, f)
	out, err := gittest.Try(work, "push", url+"/git/catalog.git", "main")
	if err == nil || !strings.Contains(out, "custos rejected the push") || !strings.Contains(out, "groups/index.yaml: groups: lists unknown group") {
		t.Fatalf("err %v, output:\n%s", err, out)
	}
}

func TestPublishedVersionsAreImmutableButDraftsAreNot(t *testing.T) {
	_, url := start(t)
	remote := url + "/git/catalog.git"
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", remote, "main")

	path := fixture.TaskPath(fixture.TaskA, "1.0.0")
	gittest.Commit(t, work, map[string]string{path: fixture.TaskFile(fixture.TaskA, "1.0.0") + "Edited.\n"})
	out, err := gittest.Try(work, "push", remote, "main")
	if err == nil || !strings.Contains(out, path+": immutable: was changed") {
		t.Fatalf("err %v, output:\n%s", err, out)
	}
	gittest.Run(t, work, "push", remote, "HEAD:refs/heads/draft")
	gittest.Run(t, work, "commit", "--quiet", "--amend", "-m", "reworded")
	gittest.Run(t, work, "push", "--force", remote, "HEAD:refs/heads/draft")
}

func TestForcePushToMainIsRejected(t *testing.T) {
	_, url := start(t)
	remote := url + "/git/catalog.git"
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Commit(t, work, map[string]string{"README.md": "hello"})
	gittest.Run(t, work, "push", remote, "main")
	gittest.Run(t, work, "reset", "--quiet", "--hard", "HEAD~1")
	out, err := gittest.Try(work, "push", "--force", remote, "main")
	if err == nil || !strings.Contains(out, "cannot be rewritten") {
		t.Fatalf("err %v, output:\n%s", err, out)
	}
}

func TestLargePush(t *testing.T) {
	_, url := start(t)
	work := gittest.Init(t)
	f := fixture.Catalog()
	big := make([]byte, 3<<20) // above git's 1 MiB http.postBuffer, so git sends it chunked
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	f["big.bin"] = string(big)
	gittest.Commit(t, work, f)
	gittest.Run(t, work, "push", url+"/git/catalog.git", "main")
}

func TestWorkspaces(t *testing.T) {
	srv, url := start(t)
	if err := srv.CreateWorkspace(fixture.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if err := srv.CreateWorkspace(fixture.WorkspaceID); err == nil {
		t.Error("creating a workspace twice must fail")
	}
	if err := srv.CreateWorkspace("../escape"); err == nil {
		t.Error("an invalid id must be rejected")
	}
	remote := url + "/git/workspaces/" + fixture.WorkspaceID + ".git"

	bad := gittest.Init(t)
	gittest.Commit(t, bad, map[string]string{"answers/notes.txt": "x"})
	if out, err := gittest.Try(bad, "push", remote, "main"); err == nil || !strings.Contains(out, "custos.yaml is missing") {
		t.Fatalf("err %v, output:\n%s", err, out)
	}

	good := gittest.Init(t)
	gittest.Commit(t, good, fixture.Workspace())
	gittest.Run(t, good, "push", remote, "main")
}

func TestOpenReinstallsHooks(t *testing.T) {
	data := t.TempDir()
	if _, err := Open(data, "/first/custos"); err != nil {
		t.Fatal(err)
	}
	srv, err := Open(data, "/second/custos")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.CreateWorkspace(fixture.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(data, "/third/custos"); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{"catalog.git", "workspaces/" + fixture.WorkspaceID + ".git"} {
		script, err := os.ReadFile(filepath.Join(data, "repos", repo, "hooks", "pre-receive"))
		if err != nil || !strings.Contains(string(script), "'/third/custos' hook pre-receive") {
			t.Errorf("%s hook: %q %v", repo, script, err)
		}
	}
}

func TestCreateWorkspaceLeavesOtherHooks(t *testing.T) {
	data := t.TempDir()
	if _, err := Open(data, "/first/custos"); err != nil {
		t.Fatal(err)
	}
	if err := New(data, "/second/custos").CreateWorkspace(fixture.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	for repo, want := range map[string]string{
		"catalog.git": "/first/custos",
		"workspaces/" + fixture.WorkspaceID + ".git": "/second/custos",
	} {
		script, err := os.ReadFile(filepath.Join(data, "repos", repo, "hooks", "pre-receive"))
		if err != nil || !strings.Contains(string(script), "'"+want+"' hook pre-receive") {
			t.Errorf("%s hook: %q %v, want %s", repo, script, err, want)
		}
	}
}

func TestCreateWorkspaceInEmptyDataDir(t *testing.T) {
	data := t.TempDir()
	if err := New(data, "/custos").CreateWorkspace(fixture.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "repos", "workspaces", fixture.WorkspaceID+".git", "hooks", "pre-receive")); err != nil {
		t.Error(err)
	}
}

func TestWithContentLengthLimitsBody(t *testing.T) {
	old := maxRequestBytes
	maxRequestBytes = 16
	t.Cleanup(func() { maxRequestBytes = old })

	var ran bool
	var gotLength int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran = true
		gotLength = r.ContentLength
	})
	h := withContentLength(inner)

	// A chunked body over the limit must be rejected before the inner
	// handler runs.
	ran = false
	req := httptest.NewRequest("POST", "/git/catalog.git/git-receive-pack", strings.NewReader(strings.Repeat("x", 64)))
	req.TransferEncoding = []string{"chunked"}
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("code = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if ran {
		t.Error("inner handler ran for an oversized chunked body")
	}

	// A body with a Content-Length over the limit must be rejected up front
	// too, not cut off inside the backend.
	ran = false
	req = httptest.NewRequest("POST", "/git/catalog.git/git-receive-pack", strings.NewReader(strings.Repeat("x", 64)))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "push too large") {
		t.Errorf("code = %d body %q, want %d", rec.Code, rec.Body.String(), http.StatusRequestEntityTooLarge)
	}
	if ran {
		t.Error("inner handler ran for an oversized body with Content-Length")
	}

	// A small body with Content-Length passes through unchanged.
	ran = false
	req = httptest.NewRequest("POST", "/git/catalog.git/git-receive-pack", strings.NewReader("small"))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !ran || gotLength != 5 {
		t.Errorf("ran %v, ContentLength = %d, want 5", ran, gotLength)
	}

	// A small chunked body must still reach the inner handler with the
	// right content length.
	ran = false
	body := "small"
	req = httptest.NewRequest("POST", "/git/catalog.git/git-receive-pack", strings.NewReader(body))
	req.TransferEncoding = []string{"chunked"}
	req.ContentLength = -1
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !ran {
		t.Error("inner handler did not run for a small chunked body")
	}
	if gotLength != int64(len(body)) {
		t.Errorf("ContentLength = %d, want %d", gotLength, len(body))
	}
}

func TestHealthz(t *testing.T) {
	srv, err := Open(t.TempDir(), custosBin)
	if err != nil {
		t.Fatal(err)
	}
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 || rec.Body.String() != "ok\n" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
}
