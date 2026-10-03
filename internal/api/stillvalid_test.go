package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/status"
)

func stillValid(t *testing.T, e *env, workspaceID, taskID, author string) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(t, "POST", "/api/workspaces/"+workspaceID+"/answers/"+taskID+"/still-valid", author, "")
}

func TestStillValid(t *testing.T) {
	e := newEnv(t, testCatalog())
	e.createWorkspace(t)
	sha, _, err := e.bl.Put(strings.NewReader("log\n"))
	if err != nil {
		t.Fatal(err)
	}
	old := `{"task_version":"1.0.0","value":"done","body":"Notes.\n","attachments":[{"name":"log.txt","sha256":"` + sha + `","media_type":"text/plain"}]}`
	decode[answerResponse](t, put(t, e, fixture.TaskA, janeHeader, old), http.StatusOK)
	if st := taskState(t, e, fixture.TaskA); st != status.PendingUpdate {
		t.Fatalf("state before %q", st)
	}

	got := decode[answerResponse](t, stillValid(t, e, fixture.WorkspaceID, fixture.TaskA, janeHeader), http.StatusOK)
	if got.TaskVersion != "1.1.0" || got.Value != "done" || got.Body != "Notes.\n" ||
		len(got.Attachments) != 1 || got.Attachments[0].SHA256 != sha || got.Commit != e.mainOID(t) {
		t.Errorf("answer %+v", got)
	}
	want := "---\ntask: " + fixture.TaskA + "\ntask_version: 1.1.0\ntype: text\nvalue: done\nattachments:\n" +
		"  - name: log.txt\n    sha256: " + sha + "\n    media_type: text/plain\n---\n\nNotes.\n"
	if file := e.readMain(t, answerPath(fixture.TaskA)); file != want {
		t.Errorf("answer file:\n%s\nwant:\n%s", file, want)
	}
	if st := taskState(t, e, fixture.TaskA); st != status.Answered {
		t.Errorf("state after %q", st)
	}
	if who := gittest.Run(t, e.repo(t).Dir, "log", "-1", "--format=%an <%ae>", "main"); who != janeHeader {
		t.Errorf("author %q", who)
	}

	again := decode[answerResponse](t, stillValid(t, e, fixture.WorkspaceID, fixture.TaskA, janeHeader), http.StatusOK)
	if again.Commit != got.Commit {
		t.Errorf("still valid on a current answer made commit %s after %s", again.Commit, got.Commit)
	}
}

func TestStillValidErrors(t *testing.T) {
	e := newEnv(t, testCatalog())
	e.createWorkspace(t)
	decode[answerResponse](t, put(t, e, taskRetyped, janeHeader, `{"task_version":"1.0.0","value":"x"}`), http.StatusOK)
	before := e.mainOID(t)

	wantProblem(t, stillValid(t, e, fixture.WorkspaceID, taskRetyped, janeHeader),
		answerPath(taskRetyped), problem.RuleAnswer, "write a new answer")
	decode[errorJSON](t, stillValid(t, e, fixture.WorkspaceID, fixture.TaskB, janeHeader), http.StatusNotFound)
	decode[errorJSON](t, stillValid(t, e, fixture.WorkspaceID, taskRetyped, ""), http.StatusUnauthorized)
	decode[errorJSON](t, stillValid(t, e, otherWorkspace, taskRetyped, janeHeader), http.StatusNotFound)
	if e.mainOID(t) != before {
		t.Error("a refused still-valid moved main")
	}
}

func TestStillValidMergedTask(t *testing.T) {
	e := newEnv(t, mergedCatalog())
	e.createWorkspace(t)
	e.writeFile(t, answerPath(fixture.TaskB), "---\ntask: "+fixture.TaskB+"\ntask_version: 1.0.0\ntype: text\nvalue: released\n---\n")
	wantProblem(t, stillValid(t, e, fixture.WorkspaceID, fixture.TaskB, janeHeader),
		answerPath(fixture.TaskB), problem.RuleAnswer, "merged")
}
