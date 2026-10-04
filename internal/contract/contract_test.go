package contract

import (
	"encoding/json"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

func taskVersion(answerType task.AnswerType, choices ...string) *task.Version {
	return &task.Version{
		Meta: task.Meta{ID: fixture.TaskB, Version: "1.0.0", Title: "Hosts", AnswerType: answerType, Choices: choices},
		Body: "List the hosts.\n",
		Path: fixture.TaskPath(fixture.TaskB, "1.0.0"),
	}
}

func TestNewInputText(t *testing.T) {
	a := &workspace.Answer{
		Task: fixture.TaskB, TaskVersion: "1.0.0", Type: task.AnswerText, Value: "web-01",
		Attachments: []workspace.Attachment{{Name: "scan.pdf", SHA256: fixture.SHA256, MediaType: "application/pdf"}},
		Path:        "answers/" + fixture.TaskB + ".md",
	}
	got, err := json.Marshal(NewInput(fixture.WorkspaceID, taskVersion(task.AnswerText), a))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"contract":"custos.processor/v1","workspace":{"id":"` + fixture.WorkspaceID + `"},` +
		`"task":{"id":"` + fixture.TaskB + `","version":"1.0.0","title":"Hosts","body":"List the hosts.\n","answer_type":"text"},` +
		`"answer":{"task_version":"1.0.0","value":"web-01","body":"",` +
		`"attachments":[{"name":"scan.pdf","sha256":"` + fixture.SHA256 + `","media_type":"application/pdf","path":"/input/blobs/` + fixture.SHA256 + `"}]}}`
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestNewInputMarkdownHasNullValueAndEmptyAttachments(t *testing.T) {
	a := &workspace.Answer{Task: fixture.TaskB, TaskVersion: "1.0.0", Type: task.AnswerMarkdown, Body: "# Hosts\n\n- web-01\n"}
	got, err := json.Marshal(NewInput(fixture.WorkspaceID, taskVersion(task.AnswerMarkdown), a))
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Answer map[string]json.RawMessage `json:"answer"`
	}
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatal(err)
	}
	if v := string(back.Answer["value"]); v != "null" {
		t.Errorf("value %s, want null", v)
	}
	if v := string(back.Answer["attachments"]); v != "[]" {
		t.Errorf("attachments %s, want []", v)
	}
	if v := string(back.Answer["body"]); v != `"# Hosts\n\n- web-01\n"` {
		t.Errorf("body %s", v)
	}
}

func TestNewInputChoiceKeepsChoices(t *testing.T) {
	a := &workspace.Answer{Task: fixture.TaskB, TaskVersion: "1.0.0", Type: task.AnswerChoice, Value: "yes"}
	in := NewInput(fixture.WorkspaceID, taskVersion(task.AnswerChoice, "yes", "no"), a)
	if len(in.Task.Choices) != 2 || in.Task.Choices[0] != "yes" || in.Answer.Value == nil || *in.Answer.Value != "yes" {
		t.Errorf("input %+v", in)
	}
}
