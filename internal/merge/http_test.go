package merge

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/task"
)

const aliceHeader = "Alice Example <alice@example.org>"

func handler(t *testing.T, e *env) http.Handler {
	t.Helper()
	return handlerWithCallback(t, e, nil)
}

// handlerWithCallback is handler, but Register is given onMainMoved.
func handlerWithCallback(t *testing.T, e *env, onMainMoved func(id string)) http.Handler {
	t.Helper()
	bl, err := blobs.Open(filepath.Join(e.st.DataDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(e.st, bl)
	Register(a, e.st, onMainMoved, nil)
	return a.Handler()
}

func call(h http.Handler, method, path, author, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if author != "" {
		req.Header.Set("X-Custos-Author", author)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
}

func TestForkEndpoint(t *testing.T) {
	e := setup(t)
	h := handler(t, e)
	path := "/api/workspaces/" + ws + "/fork"

	rec := call(h, "POST", path, aliceHeader, `{"id":"`+forkID+`"}`)
	var got struct {
		ID string `json:"id"`
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("fork: %d %s", rec.Code, rec.Body)
	}
	if decodeJSON(t, rec, &got); got.ID != forkID {
		t.Errorf("fork id = %q, want %s", got.ID, forkID)
	}
	for _, body := range []string{`{}`, ``} {
		rec = call(h, "POST", path, aliceHeader, body)
		got.ID = ""
		if rec.Code != http.StatusCreated {
			t.Fatalf("fork with body %q: %d %s", body, rec.Code, rec.Body)
		}
		if decodeJSON(t, rec, &got); !task.ValidID(got.ID) || got.ID == forkID {
			t.Errorf("generated id = %q", got.ID)
		}
	}
	for _, tc := range []struct {
		name, path, author, body string
		want                     int
	}{
		{"no author", path, "", `{}`, http.StatusUnauthorized},
		{"existing id", path, aliceHeader, `{"id":"` + forkID + `"}`, http.StatusConflict},
		{"unknown source", "/api/workspaces/" + unknownTask + "/fork", aliceHeader, `{}`, http.StatusNotFound},
		{"invalid id", path, aliceHeader, `{"id":"nope"}`, http.StatusBadRequest},
		{"malformed", path, aliceHeader, `{"id":`, http.StatusBadRequest},
		{"unknown field", path, aliceHeader, `{"id":"` + forkID + `","x":1}`, http.StatusBadRequest},
	} {
		if rec := call(h, "POST", tc.path, tc.author, tc.body); rec.Code != tc.want {
			t.Errorf("%s: %d %s, want %d", tc.name, rec.Code, rec.Body, tc.want)
		}
	}
}

func TestMergeEndpoint(t *testing.T) {
	e := setup(t)
	ours, theirs := textAnswer(fixture.TaskA, "1.1.0", "ours"), textAnswer(fixture.TaskA, "1.1.0", "theirs")
	before := diverge(t, e,
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "base")},
		map[string]string{answerA: ours},
		map[string]string{answerA: theirs})
	h := handler(t, e)
	path := "/api/workspaces/" + ws + "/merge"

	rec := call(h, "POST", path, aliceHeader, `{"branch":"what-if"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("merge with conflicts: %d %s", rec.Code, rec.Body)
	}
	var conflicts struct {
		Error     string `json:"error"`
		Conflicts []struct {
			Path   string `json:"path"`
			Kind   string `json:"kind"`
			Ours   []byte `json:"ours"`
			Theirs []byte `json:"theirs"`
		} `json:"conflicts"`
	}
	decodeJSON(t, rec, &conflicts)
	if conflicts.Error == "" || len(conflicts.Conflicts) != 1 {
		t.Fatalf("conflict body %s", rec.Body)
	}
	if c := conflicts.Conflicts[0]; c.Path != answerA || c.Kind != KindAnswer || string(c.Ours) != ours || string(c.Theirs) != theirs {
		t.Errorf("conflict = %+v", c)
	}
	if got := ref(t, e.st, ws, mainRef); got != before {
		t.Errorf("main moved to %s", got)
	}

	resolved := textAnswer(fixture.TaskA, "1.1.0", "both")
	body := `{"branch":"what-if","resolutions":{"` + answerA + `":{"side":"content","content":"` +
		base64.StdEncoding.EncodeToString([]byte(resolved)) + `"}}}`
	rec = call(h, "POST", path, aliceHeader, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge with resolution: %d %s", rec.Code, rec.Body)
	}
	var merged struct {
		Commit string `json:"commit"`
	}
	if decodeJSON(t, rec, &merged); merged.Commit != ref(t, e.st, ws, mainRef) {
		t.Errorf("commit %q is not main", merged.Commit)
	}
	if got, _ := file(t, e.st, ws, merged.Commit, answerA); got != resolved {
		t.Errorf("answer = %q, want %q", got, resolved)
	}

	commitFiles(t, e.st, ws, "refs/heads/bad", map[string]string{"answers/" + unknownTask + ".md": textAnswer(unknownTask, "1.0.0", "x")})
	for _, tc := range []struct {
		name, author, body string
		want               int
	}{
		{"no author", "", `{"branch":"what-if"}`, http.StatusUnauthorized},
		{"unknown branch", aliceHeader, `{"branch":"nope"}`, http.StatusNotFound},
		{"main", aliceHeader, `{"branch":"main"}`, http.StatusBadRequest},
		{"no branch", aliceHeader, ``, http.StatusBadRequest},
		{"bad side", aliceHeader, `{"branch":"bad","resolutions":{"x":{"side":"mine"}}}`, http.StatusBadRequest},
		{"invalid result", aliceHeader, `{"branch":"bad"}`, http.StatusUnprocessableEntity},
	} {
		if rec := call(h, "POST", path, tc.author, tc.body); rec.Code != tc.want {
			t.Errorf("%s: %d %s, want %d", tc.name, rec.Code, rec.Body, tc.want)
		}
	}
}

func TestBranchesEndpoint(t *testing.T) {
	e := setup(t)
	tip := commitFiles(t, e.st, ws, whatIf, map[string]string{answerB: textAnswer(fixture.TaskB, "1.0.0", "draft")})
	h := handler(t, e)

	rec := call(h, "GET", "/api/workspaces/"+ws+"/branches", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("branches: %d %s", rec.Code, rec.Body)
	}
	var got []Branch
	decodeJSON(t, rec, &got)
	want := []Branch{{Name: "main", Commit: ref(t, e.st, ws, mainRef)}, {Name: "what-if", Commit: tip}}
	if !slices.Equal(got, want) {
		t.Errorf("branches = %+v, want %+v", got, want)
	}
	if rec := call(h, "GET", "/api/workspaces/"+unknownTask+"/branches", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown workspace: %d, want 404", rec.Code)
	}
}

// TestOnMainMovedCallback checks that Register's onMainMoved callback fires
// once with the target id after a merge that moves main, once with the new
// id after a successful fork, and not at all for a merge that answers 409
// with conflicts, a merge that is a no-op (branch already merged), or a
// failed fork.
func TestOnMainMovedCallback(t *testing.T) {
	e := setup(t)
	ours, theirs := textAnswer(fixture.TaskA, "1.1.0", "ours"), textAnswer(fixture.TaskA, "1.1.0", "theirs")
	diverge(t, e,
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "base")},
		map[string]string{answerA: ours},
		map[string]string{answerA: theirs})

	var calls []string
	h := handlerWithCallback(t, e, func(id string) { calls = append(calls, id) })
	mergePath := "/api/workspaces/" + ws + "/merge"

	// Conflicting merge: 409, no callback.
	rec := call(h, "POST", mergePath, aliceHeader, `{"branch":"what-if"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("merge with conflicts: %d %s", rec.Code, rec.Body)
	}

	// Resolve the conflict: main moves, the callback fires once with ws.
	resolved := textAnswer(fixture.TaskA, "1.1.0", "both")
	body := `{"branch":"what-if","resolutions":{"` + answerA + `":{"side":"content","content":"` +
		base64.StdEncoding.EncodeToString([]byte(resolved)) + `"}}}`
	rec = call(h, "POST", mergePath, aliceHeader, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge with resolution: %d %s", rec.Code, rec.Body)
	}

	// Merging the already-merged branch again is a no-op: no extra callback.
	rec = call(h, "POST", mergePath, aliceHeader, `{"branch":"what-if"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge again: %d %s", rec.Code, rec.Body)
	}

	// Failed fork (no author): no callback.
	rec = call(h, "POST", "/api/workspaces/"+ws+"/fork", "", `{}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("fork without author: %d %s", rec.Code, rec.Body)
	}

	// Successful fork: the callback fires once with the new id.
	rec = call(h, "POST", "/api/workspaces/"+ws+"/fork", aliceHeader, `{"id":"`+forkID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("fork: %d %s", rec.Code, rec.Body)
	}

	if want := []string{ws, forkID}; !slices.Equal(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

// TestOnRerunCallback checks that a merge that kept main's side of
// conflicting generated output calls onRerun with the producing tasks, and
// that a merge without such conflicts does not.
func TestOnRerunCallback(t *testing.T) {
	e := setup(t)
	doc := "documents/" + fixture.DocID + ".json"
	docOn := func(side string) string {
		return strings.Replace(fixture.DocumentFile, `"name":"provenance"`, `"name":"provenance-`+side+`"`, 1)
	}
	diverge(t, e,
		map[string]string{doc: fixture.DocumentFile},
		map[string]string{doc: docOn("main")},
		map[string]string{doc: docOn("branch")})
	commitFiles(t, e.st, ws, "refs/heads/clean", map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "clean")})

	bl, err := blobs.Open(filepath.Join(e.st.DataDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(e.st, bl)
	var order []string
	var reruns [][]string
	Register(a, e.st, func(id string) { order = append(order, "moved "+id) }, func(id string, tasks []string) {
		order = append(order, "rerun "+id)
		reruns = append(reruns, tasks)
	})
	h := a.Handler()

	rec := call(h, "POST", "/api/workspaces/"+ws+"/merge", aliceHeader, `{"branch":"what-if"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge: %d %s", rec.Code, rec.Body)
	}
	if want := []string{"moved " + ws, "rerun " + ws}; !slices.Equal(order, want) {
		t.Errorf("calls %v, want %v", order, want)
	}
	if len(reruns) != 1 || !slices.Equal(reruns[0], []string{fixture.TaskB}) {
		t.Errorf("reruns %v", reruns)
	}

	rec = call(h, "POST", "/api/workspaces/"+ws+"/merge", aliceHeader, `{"branch":"clean"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clean merge: %d %s", rec.Code, rec.Body)
	}
	if len(reruns) != 1 {
		t.Errorf("a merge without generated conflicts called onRerun: %v", reruns)
	}
}
