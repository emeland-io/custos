package main

import (
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/auth"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/runs"
	"github.com/emeland-io/custos/internal/store"
)

// bootstrapAdmin creates an admin over dataDir and returns their token —
// the one credential every apiCall and git clone/push in this package's
// tests below presents, the same way a real operator would run
// custos token create once after custos serve's first start.
func bootstrapAdmin(t *testing.T, dataDir string) string {
	t.Helper()
	m, err := auth.NewManager(auth.Config{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateUser("jane@example.org", func(u *auth.User) { u.Roles = auth.Roles{Admin: true} }); err != nil {
		t.Fatal(err)
	}
	_, token, err := m.CreateToken("jane@example.org", "test")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestServeProcessorFlags(t *testing.T) {
	for _, name := range []string{"CUSTOS_CONTAINER_RUNTIME", "CUSTOS_SECRETS_DIR", "CUSTOS_PROCESSOR_MEMORY",
		"CUSTOS_PROCESSOR_WORKERS", "CUSTOS_MAX_GENERATION_DEPTH", "CUSTOS_TRUSTED_KEYS"} {
		t.Setenv(name, "")
	}
	code, out, errs := runCmd(t, "serve", "-h")
	errs += out // the old dispatcher prints help to stderr, cobra to stdout
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{
		"-container-runtime", `(default "docker")`, "-secrets-dir", "-processor-memory", `(default "512m")`,
		"-processor-workers", "(default 2)", "-max-generation-depth", "(default 8)", "-trusted-keys",
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("help lacks %q:\n%s", want, errs)
		}
	}
	for _, args := range [][]string{
		{"--processor-workers", "0"},
		{"--max-generation-depth", "0"},
		{"--container-runtime", ""},
		{"--processor-memory", ""},
	} {
		code, _, errs := runCmd(t, append([]string{"serve", "--data-dir", t.TempDir()}, args...)...)
		if code != 2 || !strings.Contains(errs, args[0]) {
			t.Errorf("%v: code %d err %q", args, code, errs)
		}
	}
	t.Setenv("CUSTOS_PROCESSOR_WORKERS", "many")
	if code, _, errs := runCmd(t, "serve", "--data-dir", t.TempDir()); code != 2 || !strings.Contains(errs, "CUSTOS_PROCESSOR_WORKERS") {
		t.Errorf("bad env: code %d err %q", code, errs)
	}
}

// api calls the server's REST API and decodes the JSON answer into out
// (unless nil); it fails the test on another status than want.
func apiCall(t *testing.T, ts *httptest.Server, token, method, path, body string, want int, out any) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: %d, want %d: %s", method, path, resp.StatusCode, want, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, data)
		}
	}
}

// waitForRun polls the runs of a workspace until one with the given reason
// has finished, and returns it.
func waitForRun(t *testing.T, ts *httptest.Server, token, id, reason string) runs.Record {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		var rs []runs.Record
		apiCall(t, ts, token, "GET", "/api/workspaces/"+id+"/runs", "", http.StatusOK, &rs)
		for _, r := range rs {
			if r.Reason == reason && (r.State == runs.Succeeded || r.State == runs.Failed) {
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no finished %s run; runs %+v", reason, rs)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestProcessorRunsEndToEnd runs the wiring of serve: an answer saved
// through the API runs the bound processor in a real container, the
// proposal is accepted through the API, and a merge whose generated output
// conflicts reruns the processor.
func TestProcessorRunsEndToEnd(t *testing.T) {
	gen := proctest.Image(t, "generate")
	data := t.TempDir()
	cat := fixture.Catalog()
	cat["processors.yaml"] = "processors:\n  scan:\n    image: " + gen + "\nbindings:\n  " + fixture.TaskB + ": scan\n"
	seedCatalogFiles(t, data, cat)

	var log lockedBuffer
	srv, err := openServer(t.Context(), data, "http://127.0.0.1:8080", defaultProcessorOptions(), auth.Config{DataDir: data}, &log)
	if err != nil {
		t.Fatal(err)
	}
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	token := bootstrapAdmin(t, data)

	var procs []runs.ProcessorInfo
	apiCall(t, ts, token, "GET", "/api/processors", "", http.StatusOK, &procs)
	if len(procs) != 1 || procs[0].Name != "scan" || procs[0].BoundTasks[0] != fixture.TaskB {
		t.Fatalf("processors %+v", procs)
	}

	ws := fixture.WorkspaceID
	apiCall(t, ts, token, "POST", "/api/workspaces", `{"id":"`+ws+`"}`, http.StatusCreated, nil)
	apiCall(t, ts, token, "PUT", "/api/workspaces/"+ws+"/answers/"+fixture.TaskB, `{"value":"task host Host one"}`, http.StatusOK, nil)
	r := waitForRun(t, ts, token, ws, runs.ReasonAnswer)
	if r.State != runs.Succeeded || r.Outcome != runs.Proposed {
		t.Fatalf("run %+v", r)
	}
	var accepted map[string]string
	apiCall(t, ts, token, "POST", "/api/workspaces/"+ws+"/processor-proposals/"+fixture.TaskB+"/accept", "", http.StatusOK, &accepted)

	// Change the generated task differently on main and on a branch.
	st := store.New(data, "/custos", "http://127.0.0.1:8080")
	w, _, err := st.Load(ws, "")
	if err != nil {
		t.Fatal(err)
	}
	var path, content string
	repo, err := st.WorkspaceRepo(ws)
	if err != nil {
		t.Fatal(err)
	}
	for p := range w.Generated {
		raw, _, err := repo.ReadFile("refs/heads/main", p)
		if err != nil {
			t.Fatal(err)
		}
		path, content = p, string(raw)
	}
	if path == "" {
		t.Fatal("the accepted proposal added no generated task")
	}
	edit := func(ref, title string) {
		if _, err := st.UpdateWorkspace(ws, ref, gitrepo.Bot, "edit", func(fs.FS) ([]gitrepo.Change, error) {
			return []gitrepo.Change{{Path: path, Data: []byte(strings.Replace(content, "title: Host one", "title: "+title, 1))}}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	edit("refs/heads/what-if", "Host one (branch)")
	edit("refs/heads/main", "Host one (main)")

	apiCall(t, ts, token, "POST", "/api/workspaces/"+ws+"/merge", `{"branch":"what-if"}`, http.StatusOK, nil)
	r = waitForRun(t, ts, token, ws, runs.ReasonMerge)
	if r.State != runs.Succeeded || r.Outcome != runs.Proposed {
		t.Errorf("merge rerun %+v", r)
	}
	if s := log.String(); s != "" {
		t.Errorf("unexpected log:\n%s", s)
	}
}

// seedCatalogFiles puts files on main of the data directory's catalog,
// without hooks.
func seedCatalogFiles(t *testing.T, data string, files map[string]string) {
	t.Helper()
	st, err := store.Open(data, "/custos", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	work := gittest.Init(t)
	gittest.Commit(t, work, files)
	gittest.Run(t, work, "push", "--quiet", st.CatalogRepo().Dir, "main")
}

func TestServeEnvironmentAndFlagPrecedence(t *testing.T) {
	t.Setenv("CUSTOS_ADDR", "")
	t.Setenv("CUSTOS_PROCESSOR_WORKERS", "many")
	if code, _, errs := runCmd(t, "serve", "--data-dir", t.TempDir()); code != 2 || !strings.Contains(errs, "CUSTOS_PROCESSOR_WORKERS") {
		t.Fatalf("malformed variable: code %d err %q", code, errs)
	}
	if legacyCLI {
		return // ruling 5.7: the old dispatcher reports a malformed variable even when a flag overrides it
	}
	// Ruling 5.7: a flag that overrides the variable makes its malformed value irrelevant.
	// An address that is already in use makes serve stop with exit 1
	// *after* every setting was accepted, deterministically and without a listener left behind.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	code, _, errs := runCmd(t, "serve", "--data-dir", t.TempDir(), "--processor-workers", "3", "--addr", ln.Addr().String())
	if code != 1 || strings.Contains(errs, "CUSTOS_PROCESSOR_WORKERS") || strings.Contains(errs, "whole number") {
		t.Errorf("flag over malformed variable: code %d err %q", code, errs)
	}
}
