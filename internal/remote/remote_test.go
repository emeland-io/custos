package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/server"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// custosBin is a custos binary built for these tests, because the
// pre-receive hooks of the served repositories run it.
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

var jane = gitrepo.Signature{Name: "Jane Doe", Email: "jane@example.org"}

type testServer struct {
	st  *store.Store
	bl  *blobs.Store
	url string
}

// startServer serves Git and the API of a fresh store over HTTP, with
// fixture.Catalog pushed and the workspace fixture.WorkspaceID created.
func startServer(t *testing.T) *testServer {
	t.Helper()
	st, err := store.Open(t.TempDir(), custosBin, "http://custos.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InstallHooks(); err != nil {
		t.Fatal(err)
	}
	bl, err := blobs.Open(filepath.Join(st.DataDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := server.New(st).WithAPI(api.New(st, bl).Handler()).Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", ts.URL+"/git/catalog.git", "main")
	if err := st.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	return &testServer{st: st, bl: bl, url: ts.URL}
}

func (s *testServer) workspaceURL() string {
	return s.url + "/git/workspaces/" + fixture.WorkspaceID + ".git"
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// answerWith is a text answer to TaskA 1.1.0 with the given attachments.
func answerWith(value string, ats ...workspace.Attachment) string {
	data, err := frontmatter.Encode(&workspace.Answer{
		Task: fixture.TaskA, TaskVersion: "1.1.0", Type: task.AnswerText, Value: value, Attachments: ats,
	}, "")
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestServerURL(t *testing.T) {
	good := map[string]string{
		"http://127.0.0.1:8080/git/workspaces/" + fixture.WorkspaceID + ".git": "http://127.0.0.1:8080",
		"https://custos.example.org/git/catalog.git":                           "https://custos.example.org",
		"https://custos.example.org/prefix/git/catalog.git?x=1":                "https://custos.example.org/prefix",
		"https://user:token@custos.example.org/git/catalog.git":                "https://user:token@custos.example.org",
	}
	for in, want := range good {
		if got, err := ServerURL(in); err != nil || got != want {
			t.Errorf("ServerURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"git@example.org:repo.git", "https://example.org/repo.git", "file:///srv/git/catalog.git", "/srv/git/catalog.git"} {
		if got, err := ServerURL(in); err == nil {
			t.Errorf("ServerURL(%q) = %q, want an error", in, got)
		}
	}
}

func TestGitAuthor(t *testing.T) {
	dir := gittest.Init(t)
	gittest.Run(t, dir, "config", "user.name", "Jane Doe")
	gittest.Run(t, dir, "config", "user.email", "jane@example.org")
	if got, err := GitAuthor(dir); err != nil || got != jane {
		t.Errorf("GitAuthor = %+v, %v", got, err)
	}
	if _, err := GitAuthor(t.TempDir()); err == nil || !strings.Contains(err.Error(), "user.name") {
		t.Errorf("outside a repository: %v", err)
	}
}
