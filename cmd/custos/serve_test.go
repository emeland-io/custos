package main

import (
	"net"
	"path/filepath"
	"strings"
	"testing"

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
	code, _, errs := runCmd(t, "serve", "-h")
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
	if code != 1 || strings.Contains(out, "listening") || !strings.Contains(errs, "custos serve:") {
		t.Errorf("code %d out %q err %q", code, out, errs)
	}
}

func TestServePublicURL(t *testing.T) {
	t.Setenv("CUSTOS_PUBLIC_URL", "")
	code, _, errs := runCmd(t, "serve", "-h")
	if code != 0 || !strings.Contains(errs, `(default "http://127.0.0.1:8080")`) {
		t.Errorf("code %d err %q", code, errs)
	}
	for _, bad := range []string{"127.0.0.1:8080", "ftp://x", "http://", "http://x/?a=b"} {
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
