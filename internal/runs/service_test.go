package runs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/proposal"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

func TestScanRunsBoundAnswers(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	answer := e.commit(t, ws, map[string]string{
		answerB: markdownAnswer("hello\n"),
		answerA: textAnswer(fixture.TaskA, "1.1.0", "unbound task"),
	})

	rs := e.runs(t, ws)
	if len(rs) != 1 {
		t.Fatalf("runs %+v, want one run for the bound answer only", rs)
	}
	r := rs[0]
	wantRun(t, r, Succeeded, Proposed, ReasonAnswer)
	if r.Task != (task.Ref{ID: fixture.TaskB, Version: "1.0.0"}) || r.AnswerPath != answerB || r.AnswerCommit != answer ||
		r.Processor != "scan" || r.Image != echo || r.Digest != digestOf(echo) || r.Started.IsZero() || r.Finished.IsZero() {
		t.Errorf("record %+v", r)
	}
	if want := proposal.Branch(fixture.TaskB, r.Digest); r.Branch != want {
		t.Errorf("branch %q, want %q", r.Branch, want)
	}
	p, err := proposal.Get(e.st, ws, fixture.TaskB)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].Kind != "document" || p.Items[0].MatchKey != "input" {
		t.Errorf("items %+v, want echo's input document", p.Items)
	}

	// Scanning again finds nothing new; neither does an unrelated commit.
	e.svc.Scan(ws)
	e.commit(t, ws, map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "changed")})
	if rs := e.runs(t, ws); len(rs) != 1 {
		t.Errorf("runs %+v, want still one", rs)
	}
}

func TestChangedAnswerRunsAgain(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("one\n")})
	first := e.runs(t, ws)[0]
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("two\n")})
	rs := e.runs(t, ws)
	if len(rs) != 2 || rs[1].ID != first.ID {
		t.Fatalf("runs %+v, want the new run first", rs)
	}
	wantRun(t, rs[0], Succeeded, Proposed, ReasonAnswer)
	if rs[0].AnswerBlob == first.AnswerBlob || rs[0].Key == first.Key {
		t.Error("the new run must record the new answer")
	}
}

func TestDigestAndBindingChangesRerun(t *testing.T) {
	echo, gen, fail := proctest.Image(t, "echo"), proctest.Image(t, "generate"), proctest.Image(t, "fail")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("task host Host one\n")})
	wantRun(t, e.runs(t, ws)[0], Succeeded, Proposed, ReasonAnswer)

	e.rebind(t, registry([]proc{{name: "scan", image: gen}}, map[string]string{fixture.TaskB: "scan"}))
	rs := e.runs(t, ws)
	if len(rs) != 2 {
		t.Fatalf("runs %+v, want a rerun after the digest change", rs)
	}
	wantRun(t, rs[0], Succeeded, Proposed, ReasonDigest)
	if rs[0].Digest != digestOf(gen) {
		t.Errorf("digest %s, want %s", rs[0].Digest, digestOf(gen))
	}
	p, err := proposal.Get(e.st, ws, fixture.TaskB)
	if err != nil {
		t.Fatal(err)
	}
	if p.Digest != digestOf(gen) {
		t.Errorf("open proposal %s, want the one of the new digest", p.Branch)
	}

	// The key holds the digest, not the name: the new binding uses an
	// image this answer has not run with yet.
	e.rebind(t, registry([]proc{{name: "scan", image: gen}, {name: "other", image: fail}}, map[string]string{fixture.TaskB: "other"}))
	rs = e.runs(t, ws)
	if len(rs) != 3 {
		t.Fatalf("runs %+v, want a rerun after the binding change", rs)
	}
	wantRun(t, rs[0], Failed, None, ReasonBinding)
	if rs[0].Processor != "other" {
		t.Errorf("processor %s", rs[0].Processor)
	}
}

func TestFailedRuns(t *testing.T) {
	tests := []struct {
		name, image, timeout, body string
		wantErr, wantLog           string
	}{
		{"non-zero exit", "fail", "", "x\n", "exited with code 3", "boom"},
		{"invalid output", "generate", "", "stderr about to fail\ngarbage\n", "invalid output", "about to fail"},
		{"timeout", "loop", "2s", "x\n", "timed out", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			image := proctest.Image(t, tt.image)
			e := newEnv(t, registry([]proc{{name: "p", image: image, timeout: tt.timeout}}, map[string]string{fixture.TaskB: "p"}), Config{})
			e.commit(t, ws, map[string]string{answerB: markdownAnswer(tt.body)})
			rs := e.runs(t, ws)
			if len(rs) != 1 {
				t.Fatalf("runs %+v", rs)
			}
			wantRun(t, rs[0], Failed, None, ReasonAnswer)
			if !strings.Contains(rs[0].Error, tt.wantErr) {
				t.Errorf("error %q, want %q", rs[0].Error, tt.wantErr)
			}
			_, log, err := e.svc.Get(ws, rs[0].ID)
			if err != nil || !strings.Contains(string(log), tt.wantLog) {
				t.Errorf("log %q, %v; want %q", log, err, tt.wantLog)
			}
			if _, err := proposal.Get(e.st, ws, fixture.TaskB); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("a failed run must not open a proposal: %v", err)
			}
		})
	}
}

// TestUnavailableImage covers an image that is neither local nor
// pullable: the run fails instead of hanging the queue.
func TestUnavailableImage(t *testing.T) {
	missing := "custos.test/missing@sha256:" + strings.Repeat("0", 64)
	e := newEnv(t, registry([]proc{{name: "p", image: missing}}, map[string]string{fixture.TaskB: "p"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n")})
	rs := e.runs(t, ws)
	if len(rs) != 1 || rs[0].State != Failed || rs[0].Error == "" {
		t.Fatalf("runs %+v, want one failed run", rs)
	}
}

func TestRetry(t *testing.T) {
	fail, echo := proctest.Image(t, "fail"), proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "p", image: fail}}, map[string]string{fixture.TaskB: "p"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n")})
	failed := e.runs(t, ws)[0]

	// Nothing changed, so scanning finds nothing; only a retry runs again.
	e.svc.Scan(ws)
	if rs := e.runs(t, ws); len(rs) != 1 {
		t.Fatalf("runs %+v", rs)
	}
	r, err := e.svc.Retry(ws, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.RetryOf != failed.ID || r.Reason != ReasonRetry || r.State != Queued || r.ID == failed.ID {
		t.Errorf("retry record %+v", r)
	}
	rs := e.runs(t, ws)
	if len(rs) != 2 || rs[0].ID != r.ID {
		t.Fatalf("runs %+v", rs)
	}
	wantRun(t, rs[0], Failed, None, ReasonRetry)

	// Fixing the processor reruns the answer on its own (new digest).
	e.rebind(t, registry([]proc{{name: "p", image: echo}}, map[string]string{fixture.TaskB: "p"}))
	ok := e.runs(t, ws)[0]
	wantRun(t, ok, Succeeded, Proposed, ReasonDigest)
	if _, err := e.svc.Retry(ws, ok.ID); !errors.Is(err, store.ErrConflict) {
		t.Errorf("retrying a succeeded run: %v, want a conflict", err)
	}
	if _, err := e.svc.Retry(ws, noSuchID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown run: %v", err)
	}
	if _, err := e.svc.Retry(otherWS, failed.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("run of another workspace: %v", err)
	}
}

// TestRestartQueuesInterruptedRuns writes the record of a run that was
// running when the previous process stopped; the next Service runs it.
func TestRestartQueuesInterruptedRuns(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n")})
	first := e.runs(t, ws)[0]

	interrupted := Record{ID: runID, Workspace: ws, Task: first.Task, AnswerPath: answerB, Reason: ReasonAnswer,
		State: Running, Queued: time.Now().UTC(), Started: time.Now().UTC()}
	data, _ := json.Marshal(interrupted)
	if err := os.WriteFile(filepath.Join(e.st.DataDir(), "runs", ws, runID+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	e.svc = e.open(t, Config{})
	e.svc.Start(t.Context())
	e.svc.Wait()
	r, _, err := e.svc.Get(ws, runID)
	if err != nil {
		t.Fatal(err)
	}
	wantRun(t, *r, Succeeded, Proposed, ReasonAnswer)
	if r.Key != first.Key || r.Digest != first.Digest {
		t.Errorf("the restarted run must record what it used: %+v", r)
	}
}

func TestRerunIgnoresKey(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n"), answerA: textAnswer(fixture.TaskA, "1.1.0", "a")})
	first := e.runs(t, ws)[0]
	if _, err := proposal.Accept(e.st, ws, fixture.TaskB, nil, alice); err != nil {
		t.Fatal(err)
	}
	if rs := e.runs(t, ws); len(rs) != 1 {
		t.Fatalf("accepting the output must not run the unchanged answer again: %+v", rs)
	}

	got, err := e.svc.Rerun(ws, []string{fixture.TaskB, fixture.TaskA, noSuchID, fixture.TaskB}, ReasonMerge)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Task.ID != fixture.TaskB || got[0].Reason != ReasonMerge || got[0].Key != first.Key {
		t.Fatalf("Rerun = %+v, want one forced run of TaskB", got)
	}
	rs := e.runs(t, ws)
	if len(rs) != 2 {
		t.Fatalf("runs %+v", rs)
	}
	wantRun(t, rs[0], Succeeded, Unchanged, ReasonMerge)
	if rs[0].Branch != "" {
		t.Errorf("an unchanged run has no branch: %q", rs[0].Branch)
	}
	if _, err := e.svc.Rerun(noSuchID, []string{fixture.TaskB}, ReasonMerge); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown workspace: %v", err)
	}
}

// TestGeneratedTaskWithProcessor follows §5.5: a generated task that names
// a processor runs it when answered, and output below the depth limit
// fails the run.
func TestGeneratedTaskWithProcessor(t *testing.T) {
	gen := proctest.Image(t, "generate")
	e := newEnv(t, registry([]proc{{name: "gen", image: gen}}, map[string]string{fixture.TaskB: "gen"}), Config{MaxDepth: 1})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("task host Host one\nprocessor host gen\n")})
	wantRun(t, e.runs(t, ws)[0], Succeeded, Proposed, ReasonAnswer)
	if _, err := proposal.Accept(e.st, ws, fixture.TaskB, nil, alice); err != nil {
		t.Fatal(err)
	}
	w, _, err := e.st.Load(ws, "")
	if err != nil {
		t.Fatal(err)
	}
	var generated string
	for _, g := range w.Generated {
		generated = g.ID
	}
	if generated == "" || len(e.runs(t, ws)) != 1 {
		t.Fatalf("generated %q, runs %+v", generated, e.runs(t, ws))
	}

	e.commit(t, ws, map[string]string{"answers/" + generated + ".md": textAnswer(generated, "1.0.0", "task sub Sub task")})
	rs := e.runs(t, ws)
	if len(rs) != 2 || rs[0].Task.ID != generated || rs[0].Processor != "gen" {
		t.Fatalf("runs %+v, want a run for the generated task", rs)
	}
	wantRun(t, rs[0], Failed, None, ReasonAnswer)
	if !strings.Contains(rs[0].Error, "depth") {
		t.Errorf("error %q, want the depth limit", rs[0].Error)
	}
}

func TestAttachments(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	sha, _, err := e.bl.Put(strings.NewReader("scan report"))
	if err != nil {
		t.Fatal(err)
	}
	withAttachment := func(sha string) string {
		return "---\ntask: " + fixture.TaskB + "\ntask_version: 1.0.0\ntype: markdown\nattachments:\n  - name: scan.txt\n    sha256: " +
			sha + "\n    media_type: text/plain\n---\nsee attachment\n"
	}
	e.commit(t, ws, map[string]string{answerB: withAttachment(sha)})
	r := e.runs(t, ws)[0]
	wantRun(t, r, Succeeded, Proposed, ReasonAnswer)
	repo, _ := e.st.WorkspaceRepo(ws)
	tree, err := repo.TreeFS("refs/heads/" + r.Branch)
	if err != nil {
		t.Fatal(err)
	}
	if !treeContains(t, tree, "documents", `/input/blobs/`+sha) {
		t.Error("the echoed input does not name the mounted attachment")
	}

	missing := strings.Repeat("ab", 32)
	e.commit(t, ws, map[string]string{answerB: withAttachment(missing)})
	r = e.runs(t, ws)[0]
	wantRun(t, r, Failed, None, ReasonAnswer)
	if !strings.Contains(r.Error, "not in the blob store") {
		t.Errorf("error %q", r.Error)
	}
}

// TestQueuedRunTakesNewestAnswer changes an answer while its run waits in
// the queue: one run is made, with the newest answer.
func TestQueuedRunTakesNewestAnswer(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newStoppedEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("one\n")})
	if err := e.svc.scan(ws, false); err != nil {
		t.Fatal(err)
	}
	second := e.commit(t, ws, map[string]string{answerB: markdownAnswer("two\n")})
	if err := e.svc.scan(ws, false); err != nil {
		t.Fatal(err)
	}
	e.svc.Start(t.Context())
	rs := e.runs(t, ws)
	if len(rs) != 1 {
		t.Fatalf("runs %+v, want one", rs)
	}
	wantRun(t, rs[0], Succeeded, Proposed, ReasonAnswer)
	if rs[0].AnswerCommit != second {
		t.Errorf("the run read %s, want the newest main %s", rs[0].AnswerCommit, second)
	}
	e.svc.Scan(ws)
	if rs := e.runs(t, ws); len(rs) != 1 {
		t.Errorf("the newest answer was run already; runs %+v", rs)
	}
}
