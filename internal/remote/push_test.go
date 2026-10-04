package remote

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/workspace"
)

// cloneWorkspace clones the workspace of s into a temporary directory.
func cloneWorkspace(t *testing.T, s *testServer) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ws")
	var log bytes.Buffer
	if err := Clone(s.workspaceURL(), dir, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	return dir
}

// writeLocalBlob stores content in the checkout's blob directory under name.
func writeLocalBlob(t *testing.T, dir, name, content string) {
	t.Helper()
	fixture.WriteDir(t, dir, map[string]string{BlobDir + "/" + name: content})
}

func remoteMain(t *testing.T, s *testServer) string {
	t.Helper()
	repo, err := s.st.WorkspaceRepo(fixture.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	oid, ok, err := repo.ResolveRef("refs/heads/main")
	if err != nil || !ok {
		t.Fatalf("remote main: %v %v", ok, err)
	}
	return oid
}

func commitAnswer(t *testing.T, dir, sha string) string {
	t.Helper()
	return gittest.Commit(t, dir, map[string]string{
		"answers/" + fixture.TaskA + ".md": answerWith("built",
			workspace.Attachment{Name: "build.log", SHA256: sha, MediaType: "text/plain"}),
	})
}

func TestPushUploadsAttachments(t *testing.T) {
	s := startServer(t)
	dir := cloneWorkspace(t, s)
	sha := hash("build log\n")
	writeLocalBlob(t, dir, sha, "build log\n")
	head := commitAnswer(t, dir, sha)

	var log bytes.Buffer
	if err := Push(dir, "origin", []string{"main"}, jane, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if !s.bl.Has(sha) {
		t.Error("attachment was not uploaded")
	}
	if got := remoteMain(t, s); got != head {
		t.Errorf("remote main %s, want %s", got, head)
	}
	if !strings.Contains(log.String(), `uploaded attachment "build.log"`) {
		t.Errorf("log:\n%s", log.String())
	}
}

func TestPushSkipsBlobsTheServerHas(t *testing.T) {
	s := startServer(t)
	dir := cloneWorkspace(t, s)
	sha, _, err := s.bl.Put(strings.NewReader("shared\n"))
	if err != nil {
		t.Fatal(err)
	}
	head := commitAnswer(t, dir, sha) // no local copy of the blob
	var log bytes.Buffer
	if err := Push(dir, "origin", []string{"main"}, jane, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if strings.Contains(log.String(), "uploaded") || remoteMain(t, s) != head {
		t.Errorf("log:\n%s", log.String())
	}
}

func TestPushRefusesMissingAttachment(t *testing.T) {
	s := startServer(t)
	dir := cloneWorkspace(t, s)
	before := remoteMain(t, s)
	commitAnswer(t, dir, hash("only on my laptop, deleted"))
	var log bytes.Buffer
	err := Push(dir, "origin", []string{"main"}, jane, &log)
	if err == nil || !strings.Contains(err.Error(), "neither") || !strings.Contains(err.Error(), "nothing was pushed") {
		t.Fatalf("err %v", err)
	}
	if remoteMain(t, s) != before {
		t.Error("main was pushed although an attachment is missing")
	}
}

func TestPushRefusesWrongLocalContent(t *testing.T) {
	s := startServer(t)
	dir := cloneWorkspace(t, s)
	before := remoteMain(t, s)
	sha := hash("the real log\n")
	writeLocalBlob(t, dir, sha, "something else\n")
	commitAnswer(t, dir, sha)
	err := Push(dir, "origin", []string{"main"}, jane, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "holds content with hash "+hash("something else\n")) {
		t.Fatalf("err %v", err)
	}
	if remoteMain(t, s) != before || s.bl.Has(sha) {
		t.Error("pushed or stored although the local blob does not match its name")
	}
}

func TestSources(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{nil, []string{"HEAD"}},
		{[]string{"main"}, []string{"main"}},
		{[]string{"+main:refs/heads/draft"}, []string{"main"}},
		{[]string{":refs/heads/old"}, nil},
		{[]string{"--force", "main", "draft:draft"}, []string{"main", "draft"}},
		{[]string{"--force"}, []string{"HEAD"}},
	}
	for _, tt := range tests {
		if got := sources(tt.args); !slices.Equal(got, tt.want) {
			t.Errorf("sources(%q) = %q, want %q", tt.args, got, tt.want)
		}
	}
}
