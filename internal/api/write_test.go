package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/status"
)

func put(t *testing.T, e *env, taskID, author, body string) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(t, "PUT", "/api/workspaces/"+fixture.WorkspaceID+"/answers/"+taskID, author, body)
}

func taskState(t *testing.T, e *env, taskID string) status.State {
	t.Helper()
	s := decode[statusJSON](t, e.do(t, "GET", "/api/workspaces/"+fixture.WorkspaceID+"/status", "", ""), http.StatusOK)
	for _, ts := range s.Tasks {
		if ts.ID == taskID {
			return ts.State
		}
	}
	t.Fatalf("task %s not in status", taskID)
	return ""
}

func jsonBody(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPutAnswer(t *testing.T) {
	e := newEnv(t, testCatalog())
	e.createWorkspace(t)
	got := decode[answerResponse](t, put(t, e, fixture.TaskA, janeHeader, `{"value":"built with make"}`), http.StatusOK)
	if got.Task != fixture.TaskA || got.TaskVersion != "1.1.0" || got.Type != "text" || got.Value != "built with make" {
		t.Errorf("answer %+v", got)
	}
	if got.Commit != e.mainOID(t) {
		t.Errorf("commit %q, main %q", got.Commit, e.mainOID(t))
	}
	want := "---\ntask: " + fixture.TaskA + "\ntask_version: 1.1.0\ntype: text\nvalue: built with make\n---\n"
	if file := e.readMain(t, answerPath(fixture.TaskA)); file != want {
		t.Errorf("answer file:\n%s\nwant:\n%s", file, want)
	}
	who := gittest.Run(t, e.repo(t).Dir, "log", "-1", "--format=%an <%ae>|%cn <%ce>", "main")
	if who != janeHeader+"|custos-bot <custos-bot@localhost>" {
		t.Errorf("author|committer %q", who)
	}
	if st := taskState(t, e, fixture.TaskA); st != status.Answered {
		t.Errorf("state %q", st)
	}
}

func TestPutAnswerForOlderVersion(t *testing.T) {
	e := newEnv(t, testCatalog())
	e.createWorkspace(t)
	got := decode[answerResponse](t, put(t, e, fixture.TaskA, janeHeader, `{"task_version":"1.0.0","value":"x"}`), http.StatusOK)
	if got.TaskVersion != "1.0.0" {
		t.Errorf("task_version %q", got.TaskVersion)
	}
	if st := taskState(t, e, fixture.TaskA); st != status.PendingUpdate {
		t.Errorf("state %q", st)
	}
	wantProblem(t, put(t, e, fixture.TaskA, janeHeader, `{"task_version":"9.9.9","value":"x"}`),
		answerPath(fixture.TaskA), problem.RuleAnswer, "no version")
}

func TestPutAnswerRejected(t *testing.T) {
	e := newEnv(t, testCatalog())
	e.createWorkspace(t)
	before := e.mainOID(t)
	attachment := func(sha string) string {
		return `{"value":"x","attachments":[{"name":"a.txt","sha256":"` + sha + `","media_type":"text/plain"}]}`
	}
	tests := []struct {
		name, task, body, rule, contains string
	}{
		{"text without value", fixture.TaskA, `{}`, problem.RuleFormat, "value is missing"},
		{"choice outside the list", taskChoice, `{"value":"blue"}`, problem.RuleAnswer, ""},
		{"markdown with value", taskMarkdown, `{"value":"x"}`, problem.RuleFormat, "value must be empty"},
		{"bad timestamp", taskRetyped, `{"value":"yesterday"}`, problem.RuleFormat, "RFC 3339"},
		{"attachment not uploaded", fixture.TaskA, attachment(hash("nope")), problem.RuleAnswer, "not on the server"},
		{"malformed attachment hash", fixture.TaskA, attachment("abc"), problem.RuleFormat, "sha256"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantProblem(t, put(t, e, tt.task, janeHeader, tt.body), answerPath(tt.task), tt.rule, tt.contains)
		})
	}
	if e.mainOID(t) != before {
		t.Error("a rejected answer moved main")
	}
}

func TestPutAnswerBadRequests(t *testing.T) {
	e := newEnv(t, testCatalog())
	e.createWorkspace(t)
	before := e.mainOID(t)
	tests := []struct {
		name, workspace, task, author, body string
		code                                int
	}{
		{"no author", fixture.WorkspaceID, fixture.TaskA, "", `{"value":"x"}`, http.StatusUnauthorized},
		{"unknown field", fixture.WorkspaceID, fixture.TaskA, janeHeader, `{"valu":"x"}`, http.StatusBadRequest},
		{"not json", fixture.WorkspaceID, fixture.TaskA, janeHeader, `value=x`, http.StatusBadRequest},
		{"empty body", fixture.WorkspaceID, fixture.TaskA, janeHeader, ``, http.StatusBadRequest},
		{"two objects", fixture.WorkspaceID, fixture.TaskA, janeHeader, `{"value":"x"} {"value":"y"}`, http.StatusBadRequest},
		{"unknown task", fixture.WorkspaceID, fixture.TaskC, janeHeader, `{"value":"x"}`, http.StatusNotFound},
		{"task not a uuid", fixture.WorkspaceID, "not-a-uuid", janeHeader, `{"value":"x"}`, http.StatusNotFound},
		{"unknown workspace", otherWorkspace, fixture.TaskA, janeHeader, `{"value":"x"}`, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do(t, "PUT", "/api/workspaces/"+tt.workspace+"/answers/"+tt.task, tt.author, tt.body)
			decode[errorJSON](t, rec, tt.code)
		})
	}
	if e.mainOID(t) != before {
		t.Error("a bad request moved main")
	}
}

func TestPutAnswerRoundTrip(t *testing.T) {
	e := newEnv(t, testCatalog())
	e.createWorkspace(t)
	path := "/api/workspaces/" + fixture.WorkspaceID + "/answers/"

	body := "Intro\r\n---\nnot: frontmatter\r\n\r\n```\n---\n```"
	wantBody := "Intro\n---\nnot: frontmatter\n\n```\n---\n```\n"
	first := decode[answerResponse](t, put(t, e, taskMarkdown, janeHeader, jsonBody(t, map[string]string{"body": body})), http.StatusOK)
	if first.Body != wantBody {
		t.Errorf("body %q, want %q", first.Body, wantBody)
	}
	if got := decode[answerJSON](t, e.do(t, "GET", path+taskMarkdown, "", ""), http.StatusOK); got.Body != wantBody {
		t.Errorf("read back %q, want %q", got.Body, wantBody)
	}
	again := decode[answerResponse](t, put(t, e, taskMarkdown, janeHeader, jsonBody(t, map[string]string{"body": body})), http.StatusOK)
	if again.Commit != first.Commit {
		t.Errorf("saving the same answer again made commit %s after %s", again.Commit, first.Commit)
	}

	value := "key: value\n---\n- item # not a comment"
	decode[answerResponse](t, put(t, e, fixture.TaskA, janeHeader, jsonBody(t, map[string]string{"value": value})), http.StatusOK)
	if got := decode[answerJSON](t, e.do(t, "GET", path+fixture.TaskA, "", ""), http.StatusOK); got.Value != value {
		t.Errorf("value %q, want %q", got.Value, value)
	}
}

func TestPutAnswerWithAttachment(t *testing.T) {
	e := newEnv(t, testCatalog())
	e.createWorkspace(t)
	sha, _, err := e.bl.Put(strings.NewReader("build log\n"))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"value":"x","attachments":[{"name":"build.log","sha256":"` + sha + `","media_type":"text/plain","available":false}]}`
	got := decode[answerResponse](t, put(t, e, fixture.TaskA, janeHeader, body), http.StatusOK)
	if len(got.Attachments) != 1 || got.Attachments[0].SHA256 != sha || !got.Attachments[0].Available {
		t.Errorf("attachments %+v", got.Attachments)
	}
	if file := e.readMain(t, answerPath(fixture.TaskA)); !strings.Contains(file, "sha256: "+sha) || strings.Contains(file, "available") {
		t.Errorf("answer file:\n%s", file)
	}
}

func TestPutAnswerToGeneratedTask(t *testing.T) {
	e := newEnv(t, testCatalog())
	e.createWorkspace(t)
	e.writeFile(t, "generated/"+fixture.TaskC+"/1.0.0.md", fixture.GeneratedFile)
	got := decode[answerResponse](t, put(t, e, fixture.TaskC, janeHeader, `{"value":"2026-10-01T12:00:00Z"}`), http.StatusOK)
	if got.TaskVersion != "1.0.0" || got.Type != "timestamp" {
		t.Errorf("answer %+v", got)
	}
}

func TestPutAnswerToMergedTask(t *testing.T) {
	e := newEnv(t, mergedCatalog())
	e.createWorkspace(t)
	wantProblem(t, put(t, e, fixture.TaskB, janeHeader, `{"value":"x"}`), answerPath(fixture.TaskB), problem.RuleAnswer, "merged")
	got := decode[answerResponse](t, put(t, e, fixture.TaskA, janeHeader, `{"value":"x"}`), http.StatusOK)
	if got.TaskVersion != "2.0.0" {
		t.Errorf("task_version %q", got.TaskVersion)
	}
}
