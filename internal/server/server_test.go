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
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/store"
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

// start serves a new data directory the way custos serve does.
func start(t *testing.T) (*store.Store, *Server, string) {
	t.Helper()
	st, err := store.Open(t.TempDir(), custosBin, "http://custos.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InstallHooks(); err != nil {
		t.Fatal(err)
	}
	srv := New(st)
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return st, srv, ts.URL
}

var jane = gitrepo.Signature{Name: "Jane Doe", Email: "jane@example.org"}

func TestPushAndCloneCatalog(t *testing.T) {
	_, _, url := start(t)
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
	_, _, url := start(t)
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
	_, _, url := start(t)
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
	_, _, url := start(t)
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
	_, _, url := start(t)
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
	st, _, url := start(t)
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", url+"/git/catalog.git", "main")
	if err := st.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	remote := url + "/git/workspaces/" + fixture.WorkspaceID + ".git"
	clone := filepath.Join(t.TempDir(), "ws")
	gittest.Run(t, t.TempDir(), "clone", "--quiet", remote, clone)
	if _, err := os.Stat(filepath.Join(clone, "custos.yaml")); err != nil {
		t.Fatal(err)
	}

	gittest.Commit(t, clone, map[string]string{"answers/notes.txt": "x"})
	if out, err := gittest.Try(clone, "push", "origin", "main"); err == nil || !strings.Contains(out, "answers/notes.txt: path: unexpected file") {
		t.Fatalf("err %v, output:\n%s", err, out)
	}
	gittest.Run(t, clone, "reset", "--quiet", "--hard", "HEAD~1")
	gittest.Commit(t, clone, map[string]string{"answers/" + fixture.TaskA + ".md": fixture.AnswerFile})
	gittest.Run(t, clone, "push", "origin", "main")
}

// TestWorkspacePushesAreCheckedAgainstCatalog runs the hook with git's
// quarantine environment, in which it must still read the catalog.
func TestWorkspacePushesAreCheckedAgainstCatalog(t *testing.T) {
	st, _, url := start(t)
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", url+"/git/catalog.git", "main")
	if err := st.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(t.TempDir(), "ws")
	gittest.Run(t, t.TempDir(), "clone", "--quiet", url+"/git/workspaces/"+fixture.WorkspaceID+".git", clone)
	answer := "answers/" + fixture.TaskA + ".md"

	for _, bad := range []struct{ path, content, want string }{
		{answer, strings.Replace(fixture.AnswerFile, "1.0.0", "7.0.0", 1), answer + ": answer: task " + fixture.TaskA + " has no version 7.0.0"},
		{"custos.yaml", fixture.Config("0f0e0d0c-0b0a-4908-8706-050403020100", fixture.Commit), "custos.yaml: workspace-id:"},
		{"custos.yaml", fixture.Config(fixture.WorkspaceID, fixture.Commit), "custos.yaml: pin: catalog commit " + fixture.Commit + " does not exist"},
	} {
		gittest.Commit(t, clone, map[string]string{bad.path: bad.content})
		if out, err := gittest.Try(clone, "push", "origin", "main"); err == nil || !strings.Contains(out, bad.want) {
			t.Errorf("err %v, output:\n%s\nwant %q", err, out, bad.want)
		}
		gittest.Run(t, clone, "reset", "--quiet", "--hard", "origin/main")
	}
	gittest.Commit(t, clone, map[string]string{answer: fixture.AnswerFile})
	gittest.Run(t, clone, "push", "origin", "main")
}

func TestOnCatalogPush(t *testing.T) {
	_, srv, url := start(t)
	calls := 0
	srv.OnCatalogPush(func() { calls++ })
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", url+"/git/catalog.git", "main")
	if calls != 1 {
		t.Fatalf("%d calls after one push", calls)
	}
	gittest.Run(t, t.TempDir(), "ls-remote", url+"/git/catalog.git")
	f := fixture.Catalog()
	f["groups/index.yaml"] = "groups: [missing]\n"
	gittest.Commit(t, work, f)
	gittest.Try(work, "push", url+"/git/catalog.git", "main")
	if calls != 2 {
		t.Errorf("%d calls; want 2: a fetch does not count, a rejected push does", calls)
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
	h, err := New(store.New(t.TempDir(), custosBin, "http://custos.test")).Handler()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 || rec.Body.String() != "ok\n" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
}
