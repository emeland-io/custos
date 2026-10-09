package runs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/proctest"
)

const aliceHeader = "Alice Example <alice@example.org>"

func handler(e *env) http.Handler {
	a := api.New(e.st, e.bl)
	Register(a, e.svc)
	return a.Handler()
}

func call(h http.Handler, method, path, author, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if author != "" {
		req.Header.Set("X-Custos-Author", author)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, code int) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("status %d, want %d; body %s", rec.Code, code, rec.Body.String())
	}
}

func TestProcessorsEndpoint(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}, {name: "slow", image: echo, timeout: "5m"}},
		map[string]string{fixture.TaskB: "scan"}), Config{})
	rec := call(handler(e), "GET", "/api/processors", "", "")
	wantStatus(t, rec, http.StatusOK)
	var got []ProcessorInfo
	decode(t, rec, &got)
	if len(got) != 2 || got[0].Name != "scan" || got[0].Digest != digestOf(echo) || got[0].Timeout != "60s" ||
		len(got[0].BoundTasks) != 1 || got[0].BoundTasks[0] != fixture.TaskB || got[1].Timeout != "5m" || len(got[1].BoundTasks) != 0 {
		t.Errorf("processors %+v", got)
	}
	if !strings.Contains(rec.Body.String(), `"secrets":[]`) {
		t.Errorf("secrets must be a list: %s", rec.Body.String())
	}
}

func TestRunEndpoints(t *testing.T) {
	fail := proctest.Image(t, "fail")
	e := newEnv(t, registry([]proc{{name: "p", image: fail}}, map[string]string{fixture.TaskB: "p"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n")})
	e.svc.Wait()
	h := handler(e)

	rec := call(h, "GET", "/api/workspaces/"+ws+"/runs", "", "")
	wantStatus(t, rec, http.StatusOK)
	var list []Record
	decode(t, rec, &list)
	if len(list) != 1 || list[0].State != Failed {
		t.Fatalf("runs %+v", list)
	}
	id := list[0].ID

	rec = call(h, "GET", "/api/workspaces/"+ws+"/runs/"+id, "", "")
	wantStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"answer_path":"`+answerB+`"`) {
		t.Errorf("record %s", rec.Body.String())
	}
	rec = call(h, "GET", "/api/workspaces/"+ws+"/runs/"+id+"/log", "", "")
	wantStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") || !strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("log %q (%s)", rec.Body.String(), ct)
	}

	wantStatus(t, call(h, "GET", "/api/workspaces/"+ws+"/runs/"+noSuchID, "", ""), http.StatusNotFound)
	wantStatus(t, call(h, "GET", "/api/workspaces/"+ws+"/runs/..%2f..%2fx/log", "", ""), http.StatusNotFound)
	wantStatus(t, call(h, "GET", "/api/workspaces/"+noSuchID+"/runs", "", ""), http.StatusNotFound)
	wantStatus(t, call(h, "POST", "/api/workspaces/"+ws+"/runs/"+id+"/retry", "", ""), http.StatusUnauthorized)

	rec = call(h, "POST", "/api/workspaces/"+ws+"/runs/"+id+"/retry", aliceHeader, "")
	wantStatus(t, rec, http.StatusAccepted)
	var retry Record
	decode(t, rec, &retry)
	if retry.RetryOf != id || retry.Reason != ReasonRetry {
		t.Errorf("retry %+v", retry)
	}
	e.svc.Wait()
	e.rebind(t, registry([]proc{{name: "p", image: proctest.Image(t, "echo")}}, map[string]string{fixture.TaskB: "p"}))
	e.svc.Wait()
	rs, _ := e.svc.Runs(ws)
	wantStatus(t, call(h, "POST", "/api/workspaces/"+ws+"/runs/"+rs[0].ID+"/retry", aliceHeader, ""), http.StatusConflict)
}

func TestProcessorProposalEndpoints(t *testing.T) {
	gen := proctest.Image(t, "generate")
	e := newEnv(t, registry([]proc{{name: "gen", image: gen}}, map[string]string{fixture.TaskB: "gen"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("task host Host one\ndoc slsa web-01\n")})
	e.svc.Wait()
	h := handler(e)
	base := "/api/workspaces/" + ws + "/processor-proposals"

	rec := call(h, "GET", base, "", "")
	wantStatus(t, rec, http.StatusOK)
	var list []ProposalJSON
	decode(t, rec, &list)
	if len(list) != 1 || list[0].Task != fixture.TaskB || list[0].Digest != digestOf(gen) || len(list[0].Items) != 2 {
		t.Fatalf("proposals %+v", list)
	}
	if !strings.Contains(rec.Body.String(), `"kind":"document"`) || !strings.Contains(rec.Body.String(), `"match_key":"host"`) {
		t.Errorf("items are not in snake_case JSON: %s", rec.Body.String())
	}
	wantStatus(t, call(h, "GET", base+"/"+fixture.TaskB, "", ""), http.StatusOK)
	wantStatus(t, call(h, "GET", base+"/"+fixture.TaskA, "", ""), http.StatusNotFound)
	wantStatus(t, call(h, "GET", base+"/not-a-task", "", ""), http.StatusNotFound)

	wantStatus(t, call(h, "POST", base+"/"+fixture.TaskB+"/accept", "", ""), http.StatusUnauthorized)
	wantStatus(t, call(h, "POST", base+"/"+fixture.TaskB+"/accept", aliceHeader, `{"items":["task:nope"]}`), http.StatusBadRequest)
	wantStatus(t, call(h, "POST", base+"/"+fixture.TaskB+"/accept", aliceHeader, `{"colour":"red"}`), http.StatusBadRequest)
	rec = call(h, "POST", base+"/"+fixture.TaskB+"/accept", aliceHeader, `{"items":["task:host"]}`)
	wantStatus(t, rec, http.StatusOK)
	var acc map[string]string
	decode(t, rec, &acc)
	repo, _ := e.st.WorkspaceRepo(ws)
	if main, _, _ := repo.ResolveRef("refs/heads/main"); acc["commit"] == "" || acc["commit"] != main {
		t.Errorf("accept %v, main %s", acc, main)
	}
	wantStatus(t, call(h, "POST", base+"/"+fixture.TaskB+"/reject", aliceHeader, ""), http.StatusNotFound)

	// Without a selection, accepting takes everything; reject closes.
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("task host Host one\ndoc slsa web-02\n")})
	e.svc.Wait()
	wantStatus(t, call(h, "POST", base+"/"+fixture.TaskB+"/reject", "", ""), http.StatusUnauthorized)
	wantStatus(t, call(h, "POST", base+"/"+fixture.TaskB+"/reject", aliceHeader, ""), http.StatusNoContent)
	wantStatus(t, call(h, "GET", base+"/"+fixture.TaskB, "", ""), http.StatusNotFound)
}

func TestDryRunEndpoints(t *testing.T) {
	e := dryEnv(t)
	h := handler(e)
	gen := proctest.Image(t, "generate")
	wantStatus(t, call(h, "POST", "/api/processors/scan/dry-run", "", `{"image":"`+gen+`"}`), http.StatusUnauthorized)
	wantStatus(t, call(h, "POST", "/api/processors/scan/dry-run", aliceHeader, `{"image":"custos.test/echo:latest"}`), http.StatusBadRequest)
	wantStatus(t, call(h, "POST", "/api/processors/scan/dry-run", aliceHeader, ``), http.StatusBadRequest)
	wantStatus(t, call(h, "POST", "/api/processors/nope/dry-run", aliceHeader, `{"image":"`+gen+`"}`), http.StatusNotFound)
	rec := call(h, "POST", "/api/processors/scan/dry-run", aliceHeader, `{"image":"`+gen+`"}`)
	wantStatus(t, rec, http.StatusAccepted)
	var started map[string]string
	decode(t, rec, &started)
	e.svc.Wait()
	rec = call(h, "GET", "/api/dry-runs/"+started["id"], "", "")
	wantStatus(t, rec, http.StatusOK)
	var rep struct {
		State      string `json:"state"`
		Runs       int    `json:"runs"`
		Failed     int    `json:"failed"`
		Proposals  int    `json:"proposals"`
		Workspaces int    `json:"workspaces"`
		Samples    []struct {
			Workspace string     `json:"workspace"`
			Task      string     `json:"task"`
			Items     []ItemJSON `json:"items"`
		} `json:"samples"`
	}
	decode(t, rec, &rep)
	if rep.State != "succeeded" || rep.Runs != 2 || rep.Proposals != 2 || rep.Workspaces != 2 || len(rep.Samples) != 2 || len(rep.Samples[0].Items) == 0 {
		t.Errorf("report %s", rec.Body.String())
	}
	wantStatus(t, call(h, "GET", "/api/dry-runs/"+noSuchID, "", ""), http.StatusNotFound)
}
