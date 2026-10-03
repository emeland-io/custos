package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/store"
)

// custosBin is a custos binary built for these tests, because the
// pre-receive hooks of the store's repositories run it.
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

const (
	janeHeader     = "Jane Doe <jane@example.org>"
	otherWorkspace = "9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d" // valid, never created
	taskMarkdown   = "b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e"
	taskChoice     = "e1f2a3b4-c5d6-4e7f-8a9b-0c1d2e3f4a5b"
	taskRetyped    = "f0e1d2c3-b4a5-4968-8776-655443322110" // 1.0.0 text, 2.0.0 timestamp
)

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// typedTask is a task version file with the given answer type; extra holds
// further frontmatter lines such as choices or previous.
func typedTask(id, version, answerType, extra string) string {
	return "---\nid: " + id + "\nversion: " + version + "\ntitle: Typed task\nanswer_type: " + answerType + "\n" + extra + "---\n\nDescribe it.\n"
}

// testCatalog is fixture.Catalog plus ungrouped tasks of other answer types.
func testCatalog() map[string]string {
	f := fixture.Catalog()
	f[fixture.TaskPath(taskMarkdown, "1.0.0")] = typedTask(taskMarkdown, "1.0.0", "markdown", "")
	f[fixture.TaskPath(taskChoice, "1.0.0")] = typedTask(taskChoice, "1.0.0", "choice", "choices: [green, red]\n")
	f[fixture.TaskPath(taskRetyped, "1.0.0")] = typedTask(taskRetyped, "1.0.0", "text", "")
	f[fixture.TaskPath(taskRetyped, "2.0.0")] = typedTask(taskRetyped, "2.0.0", "timestamp",
		"previous:\n  - id: "+taskRetyped+"\n    version: 1.0.0\n")
	return f
}

// mergedCatalog is fixture.Catalog where TaskA 2.0.0 merges TaskB, so TaskB
// is superseded, no longer listed in a group, and no longer bound.
func mergedCatalog() map[string]string {
	f := fixture.Catalog()
	f[fixture.TaskPath(fixture.TaskA, "2.0.0")] = fixture.TaskFile(fixture.TaskA, "2.0.0", fixture.TaskA+"@1.1.0", fixture.TaskB+"@1.0.0")
	f["groups/release/group.yaml"] = "title: Release\nchildren: []\n"
	f["processors.yaml"] = "processors:\n  host-scanner:\n    image: registry.example.org/host-scanner@sha256:" + fixture.SHA256 +
		"\nbindings:\n  " + fixture.TaskA + ": host-scanner\n"
	return f
}

type env struct {
	st            *store.Store
	bl            *blobs.Store
	h             http.Handler
	catalogCommit string // the catalog's main, "" when empty
}

// newEnv opens a store in a temporary data directory, pushes catalogFiles
// as the catalog's main (nothing when nil) and serves the API.
func newEnv(t *testing.T, catalogFiles map[string]string) *env {
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
	e := &env{st: st, bl: bl, h: New(st, bl).Handler()}
	if catalogFiles != nil {
		work := gittest.Init(t)
		e.catalogCommit = gittest.Commit(t, work, catalogFiles)
		gittest.Run(t, work, "push", st.CatalogRepo().Dir, "main")
	}
	return e
}

// do sends one request to the API. An empty author sends no X-Custos-Author.
func (e *env) do(t *testing.T, method, path, author, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if author != "" {
		req.Header.Set("X-Custos-Author", author)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// createWorkspace creates fixture.WorkspaceID, pinned to the catalog's main.
func (e *env) createWorkspace(t *testing.T) {
	t.Helper()
	if err := e.st.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
}

// writeFile commits one file to main of fixture.WorkspaceID through the store.
func (e *env) writeFile(t *testing.T, path, content string) string {
	t.Helper()
	oid, err := e.st.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "test",
		func(fs.FS) ([]gitrepo.Change, error) {
			return []gitrepo.Change{{Path: path, Data: []byte(content)}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func (e *env) repo(t *testing.T) *gitrepo.Repo {
	t.Helper()
	r, err := e.st.WorkspaceRepo(fixture.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (e *env) mainOID(t *testing.T) string {
	t.Helper()
	oid, ok, err := e.repo(t).ResolveRef(mainRef)
	if err != nil || !ok {
		t.Fatalf("main: ok %v err %v", ok, err)
	}
	return oid
}

func (e *env) readMain(t *testing.T, path string) string {
	t.Helper()
	data, ok, err := e.repo(t).ReadFile(e.mainOID(t), path)
	if err != nil || !ok {
		t.Fatalf("%s on main: ok %v err %v", path, ok, err)
	}
	return string(data)
}

// decode checks the status of rec and decodes its JSON body.
func decode[T any](t *testing.T, rec *httptest.ResponseRecorder, want int) T {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status %d, want %d; body %s", rec.Code, want, rec.Body.String())
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return v
}

// wantProblem fails unless rec is a 422 that lists a problem for path and
// rule whose message contains the given text.
func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, path, rule, contains string) {
	t.Helper()
	body := decode[errorJSON](t, rec, http.StatusUnprocessableEntity)
	for _, p := range body.Problems {
		if p.Path == path && p.Rule == rule && strings.Contains(p.Message, contains) {
			return
		}
	}
	t.Errorf("want problem %s: %s: ...%s...; got %+v", path, rule, contains, body.Problems)
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		err  error
		code int
	}{
		{errorf(http.StatusBadRequest, "bad request"), http.StatusBadRequest},
		{&store.RejectedError{Problems: []problem.Problem{{Path: "answers/x.md", Rule: "answer", Message: "no such task"}}}, http.StatusUnprocessableEntity},
		{fmt.Errorf("workspace x: %w", store.ErrNotFound), http.StatusNotFound},
		{store.ErrExists, http.StatusConflict},
		{fmt.Errorf("update main: %w", store.ErrConflict), http.StatusConflict},
		{blobs.ErrTooLarge, http.StatusRequestEntityTooLarge},
		{&http.MaxBytesError{Limit: 1}, http.StatusRequestEntityTooLarge},
		{errors.New("disk on fire"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		WriteError(rec, tt.err)
		body := decode[errorJSON](t, rec, tt.code)
		if body.Error != tt.err.Error() {
			t.Errorf("%v: error %q", tt.err, body.Error)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%v: Content-Type %q", tt.err, ct)
		}
	}
	rec := httptest.NewRecorder()
	WriteError(rec, tests[1].err)
	body := decode[errorJSON](t, rec, http.StatusUnprocessableEntity)
	want := []problemJSON{{Path: "answers/x.md", Rule: "answer", Message: "no such task"}}
	if len(body.Problems) != 1 || body.Problems[0] != want[0] {
		t.Errorf("problems %+v", body.Problems)
	}
}

func TestAuthor(t *testing.T) {
	for header, ok := range map[string]bool{
		"":                            false,
		"Jane Doe":                    false,
		"<jane@example.org>":          false,
		"Jane Doe <jane@example.org>": true,
	} {
		req := httptest.NewRequest("POST", "/api/blobs", nil)
		if header != "" {
			req.Header.Set("X-Custos-Author", header)
		}
		sig, err := Author(req)
		if ok {
			if err != nil || sig != jane {
				t.Errorf("%q: %+v %v", header, sig, err)
			}
			continue
		}
		rec := httptest.NewRecorder()
		WriteError(rec, err)
		if err == nil || rec.Code != http.StatusUnauthorized {
			t.Errorf("%q: err %v, status %d", header, err, rec.Code)
		}
	}
}

func TestDecodeJSON(t *testing.T) {
	type request struct {
		Value string `json:"value"`
	}
	tests := []struct {
		name, body string
		optional   bool
		code       int
	}{
		{"object", `{"value":"x"}`, false, http.StatusOK},
		{"empty optional body", "", true, http.StatusOK},
		{"empty required body", "", false, http.StatusBadRequest},
		{"unknown field", `{"valu":"x"}`, false, http.StatusBadRequest},
		{"wrong type", `{"value":5}`, false, http.StatusBadRequest},
		{"trailing data", `{"value":"x"} {}`, false, http.StatusBadRequest},
		{"not json", `value=x`, false, http.StatusBadRequest},
		{"too large", `{"value":"` + strings.Repeat("x", maxJSONBytes) + `"}`, false, http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/", strings.NewReader(tt.body))
			var v request
			if err := decodeJSON(rec, req, &v, tt.optional); err != nil {
				WriteError(rec, err)
			} else {
				WriteJSON(rec, http.StatusOK, v)
			}
			if rec.Code != tt.code {
				t.Errorf("status %d, want %d; body %.200s", rec.Code, tt.code, rec.Body.String())
			}
		})
	}
}

func TestHandle(t *testing.T) {
	e := newEnv(t, nil)
	a := New(e.st, e.bl)
	a.Handle("GET /api/echo/{word}", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, r.PathValue("word"))
	})
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/echo/hello", nil))
	if got := decode[string](t, rec, http.StatusOK); got != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestPathIDs(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.SetPathValue("id", "../escape")
	req.SetPathValue("task", fixture.TaskA)
	if _, err := workspaceID(req); err == nil {
		t.Error("workspace id ../escape accepted")
	}
	if id, err := taskID(req); err != nil || id != fixture.TaskA {
		t.Errorf("task id %q %v", id, err)
	}
	rec := httptest.NewRecorder()
	_, err := workspaceID(req)
	WriteError(rec, err)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
	if got := answerPath(fixture.TaskA); got != "answers/"+fixture.TaskA+".md" {
		t.Errorf("answerPath %q", got)
	}
}
