package proctest_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/runner"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// run runs image on a text answer (or a markdown answer when markdown is
// true) to fixture.TaskB with the given answer text.
func run(t *testing.T, cfg runner.Config, image, text string, markdown bool) *runner.Result {
	t.Helper()
	v := &task.Version{Meta: task.Meta{ID: fixture.TaskB, Version: "1.0.0", Title: "Hosts", AnswerType: task.AnswerText}}
	a := &workspace.Answer{Task: fixture.TaskB, TaskVersion: "1.0.0", Type: task.AnswerText, Value: text}
	if markdown {
		v.AnswerType, a.Type, a.Value, a.Body = task.AnswerMarkdown, task.AnswerMarkdown, "", text
	}
	in, err := json.Marshal(contract.NewInput(fixture.WorkspaceID, v, a))
	if err != nil {
		t.Fatal(err)
	}
	res, err := runner.New(cfg).Run(context.Background(), runner.Job{Image: proctest.Image(t, image), Input: in})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// parse checks that the run succeeded and returns its parsed output.
func parse(t *testing.T, res *runner.Result) *contract.Output {
	t.Helper()
	if res.ExitCode != 0 || res.TimedOut {
		t.Fatalf("exit %d, timed out %v, log:\n%s", res.ExitCode, res.TimedOut, res.Log)
	}
	out, err := contract.ParseOutput(res.Stdout)
	if err != nil {
		t.Fatalf("%v\nstdout: %s", err, res.Stdout)
	}
	return out
}

func TestGenerateTasks(t *testing.T) {
	out := parse(t, run(t, runner.Config{}, "generate", strings.Join([]string{
		"task host:web-01 Host web-01",
		"",
		"choice host:web-02 Is   web-02 patched?",
		"bump host:web-01 major",
		"processor host:web-01 host-scanner",
		"origin host:web-02 " + fixture.TaskA,
	}, "\n"), false))
	if len(out.Tasks) != 2 || len(out.Documents) != 0 {
		t.Fatalf("output %+v", out)
	}
	t0, t1 := out.Tasks[0], out.Tasks[1]
	if t0.MatchKey != "host:web-01" || t0.Title != "Host web-01" || t0.Body != "Generated for "+fixture.TaskB ||
		t0.AnswerType != task.AnswerText || t0.Bump != semver.Major || t0.Processor != "host-scanner" || t0.Origin != nil || t0.Choices != nil {
		t.Errorf("task 0 %+v", t0)
	}
	if t1.MatchKey != "host:web-02" || t1.Title != "Is web-02 patched?" || t1.AnswerType != task.AnswerChoice ||
		strings.Join(t1.Choices, ",") != "yes,no" || t1.Origin == nil || t1.Origin.ID != fixture.TaskA || t1.Origin.Version != "" || t1.Bump != semver.Minor {
		t.Errorf("task 1 %+v", t1)
	}
}

func TestGenerateReadsMarkdownBody(t *testing.T) {
	out := parse(t, run(t, runner.Config{}, "generate", "task a From the body\n", true))
	if len(out.Tasks) != 1 || out.Tasks[0].Title != "From the body" {
		t.Errorf("output %+v", out)
	}
}

func TestGenerateDocument(t *testing.T) {
	out := parse(t, run(t, runner.Config{}, "generate", "doc slsa:web-01 web-01", false))
	if len(out.Documents) != 1 {
		t.Fatalf("output %+v", out)
	}
	d := out.Documents[0]
	if d.MatchKey != "slsa:web-01" || d.Name != "slsa:web-01" || d.MediaType != "application/vnd.in-toto+json" {
		t.Errorf("document %+v", d)
	}
	sum := sha256.Sum256([]byte("web-01"))
	want := `{"_type":"https://in-toto.io/Statement/v1","predicate":{},"predicateType":"https://example.org/custos-test/v1",` +
		`"subject":[{"digest":{"sha256":"` + hex.EncodeToString(sum[:]) + `"},"name":"web-01"}]}`
	if string(d.Content) != want {
		t.Errorf("content:\n%s\nwant:\n%s", d.Content, want)
	}
}

func TestGenerateStderrGarbageAndExit(t *testing.T) {
	res := run(t, runner.Config{}, "generate", "stderr scanning  two hosts\ntask a A", false)
	if out := parse(t, res); len(out.Tasks) != 1 || string(res.Log) != "scanning two hosts\n" {
		t.Errorf("output %+v, log %q", out, res.Log)
	}

	res = run(t, runner.Config{}, "generate", "task a A\ngarbage\ntask b B", false)
	if res.ExitCode != 0 || string(res.Stdout) != "not json\n" {
		t.Errorf("garbage: exit %d, stdout %q", res.ExitCode, res.Stdout)
	}
	if _, err := contract.ParseOutput(res.Stdout); err == nil {
		t.Error("garbage must not parse")
	}

	res = run(t, runner.Config{}, "generate", "stderr about to fail\ntask a A\nexit 7", false)
	if res.ExitCode != 7 || len(res.Stdout) != 0 || string(res.Log) != "about to fail\n" {
		t.Errorf("exit: code %d, stdout %q, log %q", res.ExitCode, res.Stdout, res.Log)
	}
}

func TestGenerateBadDirective(t *testing.T) {
	for _, text := range []string{"frobnicate x", "bump a minor", "task a", "exit x"} {
		res := run(t, runner.Config{}, "generate", text, false)
		if res.ExitCode != 1 || len(res.Stdout) != 0 || !strings.Contains(string(res.Log), "line 1") {
			t.Errorf("%q: exit %d, stdout %q, log %q", text, res.ExitCode, res.Stdout, res.Log)
		}
	}
}

func TestLongLogIsTruncated(t *testing.T) {
	res := run(t, runner.Config{}, "generate", "stderr "+strings.Repeat("x", 2*runner.MaxLogSize), false)
	parse(t, res)
	if len(res.Log) != runner.MaxLogSize+len("\n[log truncated]\n") || !strings.HasSuffix(string(res.Log), "x\n[log truncated]\n") {
		t.Errorf("log of %d bytes ending %q", len(res.Log), res.Log[max(0, len(res.Log)-40):])
	}
}
