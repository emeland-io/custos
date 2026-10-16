package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/status"
)

// answeredEnv has fixture.WorkspaceID with an answer to TaskA 1.0.0 (the
// catalog's current version is 1.1.0) that has one attachment on the server
// and one that is missing.
func answeredEnv(t *testing.T) (*env, string) {
	t.Helper()
	e := newEnv(t, fixture.Catalog())
	e.createWorkspace(t)
	sha, _, err := e.bl.Put(strings.NewReader("build log\n"))
	if err != nil {
		t.Fatal(err)
	}
	e.writeFile(t, answerPath(fixture.TaskA), "---\ntask: "+fixture.TaskA+"\ntask_version: 1.0.0\ntype: text\nvalue: done with make\n"+
		"attachments:\n  - name: build.log\n    sha256: "+sha+"\n    media_type: text/plain\n"+
		"  - name: lost.bin\n    sha256: "+hash("lost")+"\n    media_type: application/octet-stream\n---\n")
	return e, sha
}

func TestStatus(t *testing.T) {
	e, sha := answeredEnv(t)
	s := decode[statusJSON](t, e.do(t, "GET", "/api/workspaces/"+fixture.WorkspaceID+"/status", "", ""), http.StatusOK)
	if s.Workspace != fixture.WorkspaceID || s.Pin != e.catalogCommit {
		t.Errorf("workspace %q pin %q", s.Workspace, s.Pin)
	}
	byID := map[string]taskStatusJSON{}
	for _, ts := range s.Tasks {
		byID[ts.ID] = ts
	}
	a := byID[fixture.TaskA]
	if a.State != status.PendingUpdate || a.Version != "1.1.0" || a.AnswerType != "text" || a.Generated {
		t.Errorf("task A %+v", a)
	}
	if a.Answer == nil || a.Answer.TaskVersion != "1.0.0" || a.Answer.Value != "done with make" {
		t.Fatalf("task A answer %+v", a.Answer)
	}
	want := []attachmentJSON{
		{Name: "build.log", SHA256: sha, MediaType: "text/plain", Available: true},
		{Name: "lost.bin", SHA256: hash("lost"), MediaType: "application/octet-stream", Available: false},
	}
	if !slices.Equal(a.Answer.Attachments, want) {
		t.Errorf("attachments %+v", a.Answer.Attachments)
	}
	if len(a.InEffect) != 1 || a.InEffect[0].TaskVersion != "1.0.0" || a.Merged == nil {
		t.Errorf("in effect %+v merged %+v", a.InEffect, a.Merged)
	}
	if b := byID[fixture.TaskB]; b.State != status.Unanswered || b.Answer != nil || b.InEffect == nil || len(b.InEffect) != 0 {
		t.Errorf("task B %+v", b)
	}
	if len(s.Book) == 0 || s.Book[0].Group == nil || s.Book[0].Group.Slug != "build" || s.Book[0].Group.Title != "Build" {
		t.Fatalf("book %+v", s.Book)
	}
	var order []string
	for _, entry := range s.Book {
		if entry.Task != "" {
			order = append(order, entry.Task)
		}
	}
	if !slices.Equal(order, []string{fixture.TaskA, fixture.TaskB}) {
		t.Errorf("book order %v", order)
	}
}

func TestBook(t *testing.T) {
	e, _ := answeredEnv(t)
	rec := e.do(t, "GET", "/api/workspaces/"+fixture.WorkspaceID+"/book", "", "")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if body := rec.Body.String(); !strings.Contains(body, "Build") || !strings.Contains(body, "done with make") {
		t.Errorf("book:\n%s", body)
	}
}

func TestGetBookHTML(t *testing.T) {
	e, _ := answeredEnv(t)
	rec := e.do(t, "GET", "/api/workspaces/"+fixture.WorkspaceID+"/book?format=html", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if body := rec.Body.String(); !strings.HasPrefix(body, "<!DOCTYPE html>") || !strings.Contains(body, "done with make") {
		t.Errorf("body is not an HTML document:\n%s", body)
	}

	rec = e.do(t, "GET", "/api/workspaces/"+fixture.WorkspaceID+"/book", "", "")
	if ct := rec.Header().Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Errorf("default format Content-Type = %q, want text/markdown", ct)
	}
}

// TestGetStatusChoices checks that the status endpoint carries a choice
// task's valid choices, so a frontend can render its answer form (plan 4c
// gap, closed by Task 8's cross-plan fix).
func TestGetStatusChoices(t *testing.T) {
	e := newEnv(t, testCatalog())
	e.createWorkspace(t)
	s := decode[statusJSON](t, e.do(t, "GET", "/api/workspaces/"+fixture.WorkspaceID+"/status", "", ""), http.StatusOK)
	var got *taskStatusJSON
	for i := range s.Tasks {
		if s.Tasks[i].ID == taskChoice {
			got = &s.Tasks[i]
		}
	}
	if got == nil {
		t.Fatalf("task %s not in status: %+v", taskChoice, s.Tasks)
	}
	if !slices.Equal(got.Choices, []string{"green", "red"}) {
		t.Errorf("choices = %v, want [green red]", got.Choices)
	}
}

func TestGetAnswer(t *testing.T) {
	e, sha := answeredEnv(t)
	got := decode[answerJSON](t, e.do(t, "GET", "/api/workspaces/"+fixture.WorkspaceID+"/answers/"+fixture.TaskA, "", ""), http.StatusOK)
	if got.Task != fixture.TaskA || got.TaskVersion != "1.0.0" || got.Type != "text" || got.Value != "done with make" ||
		got.Body != "" || len(got.Attachments) != 2 || got.Attachments[0].SHA256 != sha || !got.Attachments[0].Available {
		t.Errorf("answer %+v", got)
	}
	for _, path := range []string{
		"/api/workspaces/" + fixture.WorkspaceID + "/answers/" + fixture.TaskB, // not answered
		"/api/workspaces/" + fixture.WorkspaceID + "/answers/not-a-uuid",
		"/api/workspaces/" + otherWorkspace + "/answers/" + fixture.TaskA,
		"/api/workspaces/not-a-uuid/answers/" + fixture.TaskA,
		"/api/workspaces/" + otherWorkspace + "/status",
		"/api/workspaces/" + otherWorkspace + "/book",
	} {
		decode[errorJSON](t, e.do(t, "GET", path, "", ""), http.StatusNotFound)
	}
}
