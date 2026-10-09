package contract

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

func TestParseOutputValid(t *testing.T) {
	out, err := ParseOutput([]byte(`
	{"tasks": [
	  {"match_key": "host:web-01", "title": "Host web-01", "body": "Patch it.", "answer_type": "timestamp",
	   "origin": {"id": "` + fixture.TaskA + `", "version": "1.1.0"}, "processor": "host-scanner", "bump": "major"},
	  {"match_key": "host:web-02", "title": "Host web-02", "answer_type": "choice", "choices": ["yes", "no"]}],
	 "documents": [
	  {"match_key": "slsa:web-01", "name": "provenance", "media_type": "application/vnd.in-toto+json",
	   "content": {"_type": "https://in-toto.io/Statement/v1"}}]}
	`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tasks) != 2 || len(out.Documents) != 1 {
		t.Fatalf("output %+v", out)
	}
	t0, t1 := out.Tasks[0], out.Tasks[1]
	if t0.MatchKey != "host:web-01" || t0.AnswerType != task.AnswerTimestamp || t0.Origin == nil || t0.Origin.ID != fixture.TaskA ||
		t0.Origin.Version != "1.1.0" || t0.Processor != "host-scanner" || t0.Bump != semver.Major || t0.Body != "Patch it." {
		t.Errorf("task 0 %+v", t0)
	}
	if t1.Bump != semver.Minor {
		t.Errorf("bump %q, want the default minor", t1.Bump)
	}
	if len(t1.Choices) != 2 || t1.Origin != nil {
		t.Errorf("task 1 %+v", t1)
	}
	if d := out.Documents[0]; d.Name != "provenance" || string(d.Content) != `{"_type": "https://in-toto.io/Statement/v1"}` {
		t.Errorf("document %+v content %s", d, d.Content)
	}
}

func TestParseOutputEmptyListsAreNotNull(t *testing.T) {
	for _, in := range []string{`{}`, `{"tasks": null, "documents": null}`, `{"tasks": [], "documents": []}`} {
		out, err := ParseOutput([]byte(in))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if out.Tasks == nil || out.Documents == nil || len(out.Tasks)+len(out.Documents) != 0 {
			t.Errorf("%s: %+v", in, out)
		}
	}
}

func TestParseOutputRejectsMalformedOutput(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"empty", "", "output is empty"},
		{"blank", " \n\t", "output is empty"},
		{"not json", "not json", "not a JSON object"},
		{"array", `[]`, "not a JSON object"},
		{"null", `null`, "not a JSON object"},
		{"unknown top-level field", `{"tasks": [], "warnings": []}`, `unknown field "warnings"`},
		{"unknown task field", `{"tasks": [{"match_key": "a", "title": "A", "answer_type": "text", "priority": 1}]}`, `unknown field "priority"`},
		{"trailing object", `{"tasks": []} {"tasks": []}`, "data after the JSON object"},
		{"trailing garbage", `{"tasks": []} x`, "data after the JSON object"},
		{"truncated", `{"tasks": [`, "output is not valid"},
		{"wrong type", `{"tasks": {}}`, "output is not valid"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseOutput([]byte(c.in))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestParseOutputSizeLimit(t *testing.T) {
	pad := strings.Repeat(" ", MaxOutputSize-len(`{}`))
	if _, err := ParseOutput([]byte(`{}` + pad)); err != nil {
		t.Errorf("exactly MaxOutputSize bytes: %v", err)
	}
	_, err := ParseOutput([]byte(`{}` + pad + " "))
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err %v, want a size error", err)
	}
}

func TestParseOutputReportsAllProblems(t *testing.T) {
	_, err := ParseOutput([]byte(`{
	 "tasks": [
	  {"match_key": "", "title": "No key", "answer_type": "text"},
	  {"match_key": "a", "title": " ", "answer_type": "essay"},
	  {"match_key": "a", "title": "Dup", "answer_type": "text", "choices": ["x"]},
	  {"match_key": "c", "title": "C", "answer_type": "choice"},
	  {"match_key": "d", "title": "D", "answer_type": "choice", "choices": ["x", "x", ""]},
	  {"match_key": "e", "title": "E", "answer_type": "text", "origin": {"id": "not-a-uuid", "version": "v1"}},
	  {"match_key": "f", "title": "F", "answer_type": "text", "processor": "Host_Scanner", "bump": "huge"}],
	 "documents": [
	  {"match_key": "x", "name": "", "media_type": "", "content": null},
	  {"match_key": "x", "name": "X", "media_type": "text/plain"},
	  {"name": "Y", "media_type": "text/plain", "content": "plain text is a JSON value"}]}`))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{
		"tasks[0]: match_key is missing",
		`tasks[1] (match_key "a"): title is missing`,
		`tasks[1] (match_key "a"): answer_type "essay" is not one of`,
		`tasks[2] (match_key "a"): match_key is used by an earlier task`,
		`tasks[2] (match_key "a"): choices are only allowed with answer_type choice`,
		`tasks[3] (match_key "c"): answer_type choice needs a non-empty choices list`,
		`tasks[4] (match_key "d"): duplicate choice "x"`,
		`tasks[4] (match_key "d"): choices must not be empty`,
		`tasks[5] (match_key "e"): origin.id "not-a-uuid" is not a lowercase UUID v4`,
		`tasks[5] (match_key "e"): origin.version "v1" is not a semantic version`,
		`tasks[6] (match_key "f"): processor "Host_Scanner" must match`,
		`tasks[6] (match_key "f"): bump "huge" is not one of patch, minor, major`,
		`documents[0] (match_key "x"): name is missing`,
		`documents[0] (match_key "x"): media_type is missing`,
		`documents[0] (match_key "x"): content is missing`,
		`documents[1] (match_key "x"): match_key is used by an earlier document`,
		`documents[1] (match_key "x"): content is missing`,
		"documents[2]: match_key is missing",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not contain %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "documents[2]: content") {
		t.Errorf("a JSON string is valid content:\n%v", err)
	}
}

func TestParseOutputSameKeyForTaskAndDocument(t *testing.T) {
	_, err := ParseOutput([]byte(`{"tasks": [{"match_key": "k", "title": "T", "answer_type": "text"}],
	 "documents": [{"match_key": "k", "name": "D", "media_type": "text/plain", "content": 1}]}`))
	if err != nil {
		t.Errorf("match keys are unique per kind, so a task and a document may share one: %v", err)
	}
}
