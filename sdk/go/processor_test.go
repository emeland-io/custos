package processor

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

const sampleInput = `{"contract": "custos.processor/v1",
 "workspace": {"id": "5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9"},
 "task": {"id": "3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f", "version": "1.1.0", "title": "Hosts", "body": "List the hosts.", "answer_type": "markdown"},
 "answer": {"task_version": "1.1.0", "value": null, "body": "web-01\n",
            "attachments": [{"name": "scan.pdf", "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
                             "media_type": "application/pdf", "path": "/input/blobs/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}]},
 "added_in_a_later_version": true}`

func TestRunPassesTaskAndAnswer(t *testing.T) {
	var gotTask Task
	var gotAnswer Answer
	f := func(task Task, answer Answer) (Output, error) {
		gotTask, gotAnswer = task, answer
		return Output{Tasks: []OutputTask{{MatchKey: "host:web-01", Title: "Patch web-01", AnswerType: AnswerTimestamp}}}, nil
	}
	var out bytes.Buffer
	if err := Run(f, strings.NewReader(sampleInput), &out); err != nil {
		t.Fatal(err)
	}
	if gotTask.ID != "3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f" || gotTask.Version != "1.1.0" || gotTask.AnswerType != AnswerMarkdown || gotTask.Body != "List the hosts." {
		t.Errorf("task %+v", gotTask)
	}
	if gotAnswer.Value != nil || gotAnswer.Text() != "web-01\n" || len(gotAnswer.Attachments) != 1 || gotAnswer.Attachments[0].Path != "/input/blobs/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("answer %+v", gotAnswer)
	}
	want := `{"tasks":[{"match_key":"host:web-01","title":"Patch web-01","body":"","answer_type":"timestamp"}],"documents":[]}` + "\n"
	if out.String() != want {
		t.Errorf("output\n%s\nwant\n%s", out.String(), want)
	}
}

func TestAnswerTextPrefersValue(t *testing.T) {
	v := "2026-10-04T12:00:00Z"
	if got := (Answer{Value: &v, Body: "ignored"}).Text(); got != v {
		t.Errorf("Text() = %q", got)
	}
}

func TestRunRejectsOtherContract(t *testing.T) {
	in := strings.Replace(sampleInput, "custos.processor/v1", "custos.processor/v2", 1)
	err := Run(func(Task, Answer) (Output, error) { t.Fatal("f called"); return Output{}, nil }, strings.NewReader(in), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `contract "custos.processor/v2"`) {
		t.Errorf("err %v", err)
	}
}

func TestRunRejectsBrokenInput(t *testing.T) {
	for name, in := range map[string]string{"not json": "not json", "trailing data": sampleInput + " {}", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			err := Run(func(Task, Answer) (Output, error) { t.Fatal("f called"); return Output{}, nil }, strings.NewReader(in), &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "input") {
				t.Errorf("err %v", err)
			}
		})
	}
}

func TestRunReturnsProcessError(t *testing.T) {
	boom := errors.New("no hosts found")
	var out bytes.Buffer
	err := Run(func(Task, Answer) (Output, error) { return Output{}, boom }, strings.NewReader(sampleInput), &out)
	if !errors.Is(err, boom) || out.Len() != 0 {
		t.Errorf("err %v, output %q", err, out.String())
	}
}

func TestRunWritesNothingForInvalidOutput(t *testing.T) {
	var out bytes.Buffer
	err := Run(func(Task, Answer) (Output, error) {
		return Output{Tasks: []OutputTask{{MatchKey: "a", AnswerType: AnswerText}}}, nil
	}, strings.NewReader(sampleInput), &out)
	var verr *ValidationError
	if !errors.As(err, &verr) || len(verr.Problems) != 1 || verr.Problems[0] != "tasks[0]: title is missing" || out.Len() != 0 {
		t.Errorf("err %v, output %q", err, out.String())
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	err := Output{
		Tasks:     []OutputTask{{MatchKey: "a", Title: "A", AnswerType: "number"}, {MatchKey: "a", Bump: "huge", AnswerType: AnswerText}},
		Documents: []OutputDocument{{MatchKey: "d", Name: "n"}},
	}.Validate()
	want := []string{
		`tasks[0]: answer_type "number" is not one of markdown, text, timestamp, path, url, choice`,
		`tasks[1]: match_key "a" is used by an earlier task`,
		`tasks[1]: title is missing`,
		`tasks[1]: bump "huge" is not one of patch, minor, major`,
		`documents[0]: media_type is missing`,
		`documents[0]: content is missing`,
	}
	var verr *ValidationError
	if !errors.As(err, &verr) || strings.Join(verr.Problems, "\n") != strings.Join(want, "\n") {
		t.Errorf("got %v\nwant %q", err, want)
	}
}

func TestEncodeDocumentContentKinds(t *testing.T) {
	type statement struct {
		Type string `json:"_type"`
	}
	data, err := Encode(Output{Documents: []OutputDocument{
		{MatchKey: "a", Name: "a", MediaType: "application/json", Content: statement{Type: "https://in-toto.io/Statement/v1"}},
		{MatchKey: "b", Name: "b", MediaType: "application/json", Content: json.RawMessage(`{"x":[1,2]}`)},
		{MatchKey: "c", Name: "c", MediaType: "application/json", Content: map[string]any{"<": "&"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"tasks":[],"documents":[` +
		`{"match_key":"a","name":"a","media_type":"application/json","content":{"_type":"https://in-toto.io/Statement/v1"}},` +
		`{"match_key":"b","name":"b","media_type":"application/json","content":{"x":[1,2]}},` +
		`{"match_key":"c","name":"c","media_type":"application/json","content":{"<":"&"}}]}` + "\n"
	if string(data) != want {
		t.Errorf("got\n%s\nwant\n%s", data, want)
	}
}

func TestEncodeRejectsBrokenRawContent(t *testing.T) {
	_, err := Encode(Output{Documents: []OutputDocument{{MatchKey: "a", Name: "a", MediaType: "application/json", Content: json.RawMessage(`{`)}}})
	if err == nil || !strings.Contains(err.Error(), "documents[0]: content cannot be encoded as JSON") {
		t.Errorf("err %v", err)
	}
}

func TestEncodeRejectsOversizedOutput(t *testing.T) {
	big := strings.Repeat("x", MaxOutputSize)
	_, err := Encode(Output{Documents: []OutputDocument{{MatchKey: "a", Name: "a", MediaType: "text/plain", Content: big}}})
	if err == nil || !strings.Contains(err.Error(), "custos accepts at most 16777216") {
		t.Errorf("err %v", err)
	}
}

// outputCase is one entry of sdk/testdata/output-cases.json, which the
// custos server's parser and both SDKs are checked against.
type outputCase struct {
	Name       string `json:"name"`
	Valid      bool   `json:"valid"`
	ParserOnly bool   `json:"parser_only"`
	Stdout     string `json:"stdout"`
}

func TestSharedOutputCases(t *testing.T) {
	data, err := os.ReadFile("../testdata/output-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []outputCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range cases {
		if c.ParserOnly {
			continue // malformed JSON cannot be built as an Output value
		}
		t.Run(c.Name, func(t *testing.T) {
			var o Output
			if err := json.Unmarshal([]byte(c.Stdout), &o); err != nil {
				t.Fatalf("case does not decode: %v", err)
			}
			err := o.Validate()
			if c.Valid && err != nil {
				t.Errorf("want valid, got %v", err)
			}
			if !c.Valid && err == nil {
				t.Error("want invalid, got valid")
			}
		})
	}
}
