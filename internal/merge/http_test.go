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
	bl, err := blobs.Open(filepath.Join(e.st.DataDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(e.st, bl)
	Register(a, e.st)
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
