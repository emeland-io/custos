package remote

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/workspace"
)

func TestClone(t *testing.T) {
	s := startServer(t)
	sha, _, err := s.bl.Put(strings.NewReader("scan results\n"))
	if err != nil {
		t.Fatal(err)
	}
	answer := answerWith("scanned",
		workspace.Attachment{Name: "scan.txt", SHA256: sha, MediaType: "text/plain"},
		workspace.Attachment{Name: "lost.bin", SHA256: hash("lost"), MediaType: "application/octet-stream"})
	if _, err := s.st.UpdateWorkspace(fixture.WorkspaceID, "refs/heads/main", jane, "answer",
		func(fs.FS) ([]gitrepo.Change, error) {
			return []gitrepo.Change{{Path: "answers/" + fixture.TaskA + ".md", Data: []byte(answer)}}, nil
		}); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(t.TempDir(), "ws")
	var log bytes.Buffer
	if err := Clone(s.workspaceURL(), dir, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if data, err := os.ReadFile(filepath.Join(dir, ".custos", "blobs", "sha256", sha)); err != nil || string(data) != "scan results\n" {
		t.Errorf("downloaded blob: %q %v", data, err)
	}
	if !strings.Contains(log.String(), `attachment "lost.bin" in answers/`+fixture.TaskA+`.md is unavailable`) {
		t.Errorf("no warning about the missing blob:\n%s", log.String())
	}
	if st := gittest.Run(t, dir, "status", "--porcelain"); st != "" {
		t.Errorf("checkout not clean after clone: %q", st)
	}
}

func TestCloneCatalog(t *testing.T) {
	s := startServer(t)
	dir := filepath.Join(t.TempDir(), "catalog")
	if err := Clone(s.url+"/git/catalog.git", dir, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "groups", "index.yaml")); err != nil {
		t.Error(err)
	}
}

func TestCloneRejectsNonCustosURL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x")
	err := Clone("https://example.org/repo.git", dir, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "/git/") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("clone directory was created: %v", err)
	}
}
