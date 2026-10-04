package distribute

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

const authorHeader = "Jane Doe <jane@example.org>"

func serveAPI(t *testing.T, st *store.Store) string {
	t.Helper()
	bl, err := blobs.Open(filepath.Join(st.DataDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(st, bl)
	Register(a, st)
	ts := httptest.NewServer(a.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func call(t *testing.T, method, url, author, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if author != "" {
		req.Header.Set("X-Custos-Author", author)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

func TestAPIFreezeProposeAccept(t *testing.T) {
	st := newStore(t)
	c1 := commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	ws := serveAPI(t, st) + "/api/workspaces/" + wsA

	if code, body := call(t, "POST", ws+"/freeze", "", ""); code != http.StatusUnauthorized || !strings.Contains(body, `"error"`) {
		t.Fatalf("freeze without author: %d %s", code, body)
	}
	if code, body := call(t, "POST", ws+"/freeze", authorHeader, ""); code != http.StatusOK || !strings.Contains(body, `"frozen":true`) {
		t.Fatalf("freeze: %d %s", code, body)
	}
	c2 := newTaskAVersion(t, st, "1.2.0", "1.1.0")
	if err := Reconcile(st); err != nil {
		t.Fatal(err)
	}

	code, body := call(t, "GET", ws+"/pin-diff", "", "")
	var pd pinDiffResponse
	if code != http.StatusOK || json.Unmarshal([]byte(body), &pd) != nil {
		t.Fatalf("pin-diff: %d %s", code, body)
	}
	if pd.From != c1 || pd.To != c2 {
		t.Errorf("pin-diff %+v", pd)
	}
	wantRefs(t, "pin-diff new versions", pd.Changes.NewVersions, task.Ref{ID: fixture.TaskA, Version: "1.2.0"})

	code, body = call(t, "GET", ws+"/proposals", "", "")
	var ps []PinProposal
	if code != http.StatusOK || json.Unmarshal([]byte(body), &ps) != nil || len(ps) != 1 || ps[0].Branch != "custos/pin/"+c2 {
		t.Fatalf("proposals: %d %s", code, body)
	}

	if code, body := call(t, "POST", ws+"/proposals/accept", authorHeader, `{}`); code != http.StatusBadRequest {
		t.Errorf("accept without branch: %d %s", code, body)
	}
	if code, body := call(t, "POST", ws+"/proposals/accept", authorHeader, `{"branch":"custos/pin/`+strings.Repeat("a", 40)+`"}`); code != http.StatusNotFound {
		t.Errorf("accept unknown branch: %d %s", code, body)
	}
	if code, body := call(t, "POST", ws+"/proposals/accept", "", `{"branch":"custos/pin/`+c2+`"}`); code != http.StatusUnauthorized {
		t.Errorf("accept without author: %d %s", code, body)
	}
	if code, body := call(t, "POST", ws+"/proposals/accept", authorHeader, `{"branch":"custos/pin/`+c2+`"}`); code != http.StatusOK {
		t.Fatalf("accept: %d %s", code, body)
	}
	if c := configAt(t, wsRepo(t, st, wsA), "refs/heads/main"); c.Catalog.Commit != c2 {
		t.Errorf("pin after accept %s", c.Catalog.Commit)
	}

	if code, body := call(t, "POST", ws+"/unfreeze", authorHeader, ""); code != http.StatusOK || !strings.Contains(body, `"frozen":false`) {
		t.Errorf("unfreeze: %d %s", code, body)
	}
}

// TestAPIUnfreezeWarnsOnFailedReconcile checks that the unfreeze endpoint
// answers 200 with a "warning" field, not an error status, when the
// frozen:false commit succeeds but moving the pin fails: the client's
// request was honoured, so an error status would wrongly tell it otherwise.
func TestAPIUnfreezeWarnsOnFailedReconcile(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	createWorkspace(t, st, wsA)
	answerA(t, st, wsA)
	if err := Freeze(st, wsA, person); err != nil {
		t.Fatal(err)
	}
	commitCatalog(t, st, nil, fixture.TaskPath(fixture.TaskA, "1.0.0"), fixture.TaskPath(fixture.TaskA, "1.1.0"))
	ws := serveAPI(t, st) + "/api/workspaces/" + wsA

	code, body := call(t, "POST", ws+"/unfreeze", authorHeader, "")
	if code != http.StatusOK {
		t.Fatalf("unfreeze: %d %s, want 200 with a warning", code, body)
	}
	var resp struct {
		ID      string `json:"id"`
		Frozen  bool   `json:"frozen"`
		Warning string `json:"warning"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	if resp.Frozen {
		t.Errorf("response %+v, want frozen false", resp)
	}
	if resp.Warning == "" {
		t.Errorf("response %+v, want a non-empty warning", resp)
	}
	if c := configAt(t, wsRepo(t, st, wsA), "refs/heads/main"); c.Frozen {
		t.Errorf("config %+v, flag not committed despite the 200", c)
	}
}

func TestAPIReject(t *testing.T) {
	st, _, _, c2 := frozenWithProposal(t)
	ws := serveAPI(t, st) + "/api/workspaces/" + wsA
	if code, body := call(t, "POST", ws+"/proposals/reject", authorHeader, `{"branch":"custos/pin/`+c2+`"}`); code != http.StatusOK {
		t.Fatalf("reject: %d %s", code, body)
	}
	if code, body := call(t, "GET", ws+"/proposals", "", ""); code != http.StatusOK || strings.TrimSpace(body) != "[]" {
		t.Errorf("proposals after reject: %d %s", code, body)
	}
}

func TestAPIUnknownWorkspace(t *testing.T) {
	st := newStore(t)
	commitCatalog(t, st, fixture.Catalog())
	ws := serveAPI(t, st) + "/api/workspaces/" + wsB
	for _, c := range []struct{ method, path, author string }{
		{"GET", "/proposals", ""},
		{"GET", "/pin-diff", ""},
		{"POST", "/freeze", authorHeader},
		{"POST", "/unfreeze", authorHeader},
	} {
		if code, body := call(t, c.method, ws+c.path, c.author, ""); code != http.StatusNotFound {
			t.Errorf("%s %s: %d %s", c.method, c.path, code, body)
		}
	}
}
