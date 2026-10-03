package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
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

func TestWorkspaceCreate(t *testing.T) {
	data := t.TempDir()
	code, out, errs := runCmd(t, "workspace", "create", "--data-dir", data, fixture.WorkspaceID)
	if code != 0 || !strings.Contains(out, "/git/workspaces/"+fixture.WorkspaceID+".git") {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(data, "repos", "workspaces", fixture.WorkspaceID+".git", "HEAD")); err != nil {
		t.Error(err)
	}
	if code, _, _ := runCmd(t, "workspace", "create", "--data-dir", data, "not-a-uuid"); code != 1 {
		t.Errorf("invalid id: code %d", code)
	}
}
