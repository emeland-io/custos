package main

import (
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/store"
)

func TestServeNeedsDataDir(t *testing.T) {
	t.Setenv("CUSTOS_DATA_DIR", "")
	code, _, errs := runCmd(t, "serve")
	if code != 2 || !strings.Contains(errs, "--data-dir") {
		t.Errorf("code %d err %q", code, errs)
	}
}

func TestServeDefaultsToLoopback(t *testing.T) {
	t.Setenv("CUSTOS_ADDR", "")
	code, out, errs := runCmd(t, "serve", "-h")
	errs += out // the old dispatcher prints help to stderr, cobra to stdout
	if code != 0 || !strings.Contains(errs, `(default "127.0.0.1:8080")`) {
		t.Errorf("code %d err %q", code, errs)
	}
}

func TestServeAddressInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	code, out, errs := runCmd(t, "serve", "--data-dir", t.TempDir(), "--addr", ln.Addr().String())
	// The failure must specifically be the listener failing to bind
	// (address already in use), not any other startup error swallowed
	// under the same "custos serve:" prefix — such as openServer failing
	// before ever trying to bind, which is what masked the DataDir bug
	// this test exists to catch.
	if code != 1 || strings.Contains(out, "listening") || !strings.Contains(errs, "custos serve:") || !strings.Contains(errs, "address already in use") {
		t.Errorf("code %d out %q err %q", code, out, errs)
	}
}

// TestServeStartsAndServesHealthz calls the real CLI entry point (run, the
// same path os.Args[1:] takes) with a real --data-dir, the way an actual
// "custos serve" invocation builds its auth.Config via authFlagSet.config
// rather than constructing one by hand. It would have caught the bug where
// authFlags.config never set authCfg.DataDir, which made every real
// invocation fail at auth.NewManager ("auth: no data directory") before
// ever binding its listener.
func TestServeStartsAndServesHealthz(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	data := t.TempDir()
	done := make(chan int, 1)
	var out, errs lockedBuffer
	go func() {
		done <- run([]string{"serve", "--data-dir", data, "--addr", addr, "--public-url", "http://" + addr}, nil, &out, &errs)
	}()

	var resp *http.Response
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = http.Get("http://" + addr + "/healthz")
		if err == nil {
			break
		}
		select {
		case code := <-done:
			t.Fatalf("serve exited early with code %d before /healthz answered, out %q err %q", code, out.String(), errs.String())
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("server never became reachable: %v (out %q err %q)", err, out.String(), errs.String())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz: %d", resp.StatusCode)
	}

	// runServe only stops on SIGINT/SIGTERM or an HTTP server error, and
	// sending either from this test would reach the whole test binary, not
	// just this goroutine's server. There is nothing left to assert once
	// /healthz has answered, so leave it running; the test process exiting
	// cleans it up.
	_ = done
}

func TestServePublicURL(t *testing.T) {
	t.Setenv("CUSTOS_PUBLIC_URL", "")
	code, out, errs := runCmd(t, "serve", "-h")
	errs += out // the old dispatcher prints help to stderr, cobra to stdout
	if code != 0 || !strings.Contains(errs, `(default "http://127.0.0.1:8080")`) {
		t.Errorf("code %d err %q", code, errs)
	}
	for _, bad := range []string{"127.0.0.1:8080", "ftp://x", "http://", "http://x/?a=b", "http://user:pass@host"} {
		if code, _, errs := runCmd(t, "serve", "--data-dir", t.TempDir(), "--public-url", bad); code != 2 || !strings.Contains(errs, "--public-url") {
			t.Errorf("%q: code %d err %q", bad, code, errs)
		}
	}
}

// seedCatalog puts the fixture catalog on main of the data directory's
// catalog, without hooks.
func seedCatalog(t *testing.T, data string) {
	t.Helper()
	st, err := store.Open(data, "/custos", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", "--quiet", st.CatalogRepo().Dir, "main")
}

func TestWorkspaceCreate(t *testing.T) {
	t.Setenv("CUSTOS_AUTHOR", "")
	t.Setenv("CUSTOS_PUBLIC_URL", "")
	data := t.TempDir()
	if code, _, errs := runCmd(t, "workspace", "create", "--data-dir", data, "--author", "Jane Doe <jane@example.org>", fixture.WorkspaceID); code != 1 || !strings.Contains(errs, "push the catalog first") {
		t.Fatalf("empty catalog: code %d err %q", code, errs)
	}
	seedCatalog(t, data)
	code, out, errs := runCmd(t, "workspace", "create", "--data-dir", data, "--public-url", "https://custos.example.org/",
		"--author", "Jane Doe <jane@example.org>", fixture.WorkspaceID)
	if code != 0 || !strings.Contains(out, "clone it from https://custos.example.org/git/workspaces/"+fixture.WorkspaceID+".git") {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
	dir := filepath.Join(data, "repos", "workspaces", fixture.WorkspaceID+".git")
	if who := gittest.Run(t, dir, "log", "-1", "--format=%an <%ae>", "main"); who != "Jane Doe <jane@example.org>" {
		t.Errorf("author %q", who)
	}
	if config := gittest.Run(t, dir, "show", "main:custos.yaml"); !strings.Contains(config, "url: https://custos.example.org/git/catalog.git") {
		t.Errorf("custos.yaml %q", config)
	}
	if code, _, _ := runCmd(t, "workspace", "create", "--data-dir", data, "--author", "Jane Doe <jane@example.org>", "not-a-uuid"); code != 1 {
		t.Errorf("invalid id: code %d", code)
	}
}

func TestWorkspaceCreateAuthor(t *testing.T) {
	t.Setenv("CUSTOS_AUTHOR", "")
	data := t.TempDir()
	if code, _, errs := runCmd(t, "workspace", "create", "--data-dir", data, fixture.WorkspaceID); code != 2 || !strings.Contains(errs, "--author") {
		t.Errorf("no author: code %d err %q", code, errs)
	}
	if code, _, errs := runCmd(t, "workspace", "create", "--data-dir", data, "--author", "jane", fixture.WorkspaceID); code != 2 || !strings.Contains(errs, "Name <email>") {
		t.Errorf("bad author: code %d err %q", code, errs)
	}
	seedCatalog(t, data)
	t.Setenv("CUSTOS_AUTHOR", "Env Author <env@example.org>")
	if code, _, errs := runCmd(t, "workspace", "create", "--data-dir", data, fixture.WorkspaceID); code != 0 {
		t.Fatalf("code %d err %q", code, errs)
	}
	dir := filepath.Join(data, "repos", "workspaces", fixture.WorkspaceID+".git")
	if who := gittest.Run(t, dir, "log", "-1", "--format=%an", "main"); who != "Env Author" {
		t.Errorf("author %q", who)
	}
}

func TestWorkspaceCreateFlagBeatsEnv(t *testing.T) {
	data := t.TempDir()
	seedCatalog(t, data)
	t.Setenv("CUSTOS_DATA_DIR", t.TempDir()) // a different, empty data dir: the flag must win
	t.Setenv("CUSTOS_AUTHOR", "Env Author <env@example.org>")
	t.Setenv("CUSTOS_PUBLIC_URL", "https://env.example.org")
	code, out, errs := runCmd(t, "workspace", "create", "--data-dir", data, "--author", "Flag Author <flag@example.org>",
		"--public-url", "https://flag.example.org", fixture.WorkspaceID)
	if code != 0 || !strings.Contains(out, "https://flag.example.org/git/workspaces/") {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
	dir := filepath.Join(data, "repos", "workspaces", fixture.WorkspaceID+".git")
	if who := gittest.Run(t, dir, "log", "-1", "--format=%an", "main"); who != "Flag Author" {
		t.Errorf("author %q", who)
	}
}

func TestWorkspaceCreateIgnoresServerURL(t *testing.T) {
	// ruling 5.5: --server-url / CUSTOS_SERVER_URL are not the public URL.
	t.Setenv("CUSTOS_PUBLIC_URL", "")
	t.Setenv("CUSTOS_SERVER_URL", "https://server.example.org")
	data := t.TempDir()
	seedCatalog(t, data)
	_, out, _ := runCmd(t, "workspace", "create", "--data-dir", data, "--author", "Jane Doe <jane@example.org>", fixture.WorkspaceID)
	if !strings.Contains(out, defaultPublicURL) || strings.Contains(out, "server.example.org") {
		t.Errorf("out %q", out)
	}
}
