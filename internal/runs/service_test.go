package runs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/proposal"
	"github.com/emeland-io/custos/internal/runner"
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

	// The queue must keep processing other work after a run fails because
	// its image cannot be found or pulled (Review Focus 3): bind TaskB to
	// the missing image and TaskA to a working one, with one worker so
	// processing is strictly sequential, and confirm the second answer's
	// run still succeeds.
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "bad", image: missing}, {name: "good", image: echo}},
		map[string]string{fixture.TaskB: "bad", fixture.TaskA: "good"}), Config{Workers: 1})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n"), answerA: textAnswer(fixture.TaskA, "1.1.0", "ok")})
	rs := e.runs(t, ws)
	if len(rs) != 2 {
		t.Fatalf("runs %+v, want one failed run and one that still succeeded", rs)
	}
	var sawFailed, sawSucceeded bool
	for _, r := range rs {
		switch {
		case r.AnswerPath == answerB:
			sawFailed = r.State == Failed && r.Error != ""
		case r.AnswerPath == answerA:
			sawSucceeded = r.State == Succeeded && r.Outcome == Proposed
		}
	}
	if !sawFailed || !sawSucceeded {
		t.Fatalf("runs %+v, want the bad image to fail and the queue to still process the good one", rs)
	}

	// A missing runtime binary must fail the run the same way, not hang
	// the worker or panic.
	t.Run("missing runtime", func(t *testing.T) {
		e := newStoppedEnv(t, registry([]proc{{name: "p", image: missing}}, map[string]string{fixture.TaskB: "p"}), Config{})
		svc, err := New(e.st, e.bl, runner.New(runner.Config{Runtime: "/nonexistent/docker"}), attest.Unverified, Config{})
		if err != nil {
			t.Fatal(err)
		}
		e.svc = svc
		e.st.OnMainMoved(svc.Scan)
		e.svc.Start(t.Context())
		e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n")})
		rs := e.runs(t, ws)
		if len(rs) != 1 || rs[0].State != Failed || rs[0].Error == "" {
			t.Fatalf("runs %+v, want one failed run", rs)
		}
	})
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
	// newStoppedEnv, not newEnv: the commit below must not itself produce a
	// completed run before the hand-written "running" record is loaded,
	// since that would give the key a terminal record of its own and the
	// restarted run would then (correctly, per I1) be skipped as
	// redundant rather than exercising the restart path this test is for.
	e := newStoppedEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	commit := e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n")})

	interrupted := Record{ID: runID, Workspace: ws, Task: task.Ref{ID: fixture.TaskB, Version: "1.0.0"}, AnswerPath: answerB,
		Reason: ReasonAnswer, State: Running, Queued: time.Now().UTC(), Started: time.Now().UTC()}
	data, _ := json.Marshal(interrupted)
	dir := filepath.Join(e.st.DataDir(), "runs", ws)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, runID+".json"), data, 0o644); err != nil {
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
	if r.Key == "" || r.Digest != digestOf(echo) || r.AnswerCommit != commit {
		t.Errorf("the restarted run must record what it actually used: %+v", r)
	}
}

// TestRestartAfterRealCancellation strengthens the above: it starts a real
// run against an image that never finishes on its own, cancels the
// service's context while the container is actually running, and confirms
// the record is left "running" (not failed), its container is gone, and a
// fresh Service picks the run up and completes it exactly once (I2).
func TestRestartAfterRealCancellation(t *testing.T) {
	loop := proctest.Image(t, "loop")
	e := newStoppedEnv(t, registry([]proc{{name: "p", image: loop}}, map[string]string{fixture.TaskB: "p"}), Config{})

	// Snapshot what is running before this test starts anything, so the
	// check below is scoped to the container THIS run introduces rather
	// than the global "custos-run-*" namespace, which internal/runner's
	// own tests also use and which go test may be exercising in another
	// package at the same time.
	before := runningContainers(t)

	ctx, cancel := context.WithCancel(context.Background())
	e.svc.Start(ctx)
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n")})

	// Wait for both the record to say "running" and this run's own
	// container to be up: the record's state turns "running" slightly
	// before the container is actually started (resolving and pulling
	// the image come first), so either alone would race.
	var id string
	var mine []string
	waitUntil(t, 10*time.Second, func() bool {
		rs, err := e.svc.Runs(ws)
		if err != nil || len(rs) != 1 || rs[0].State != Running {
			return false
		}
		id = rs[0].ID
		mine = newContainers(before, runningContainers(t))
		return len(mine) > 0
	})

	cancel()
	waitUntil(t, 15*time.Second, func() bool { return len(newContainers(before, runningContainers(t))) == 0 })
	for _, name := range mine {
		if slices.Contains(runningContainers(t), name) {
			t.Fatalf("container %s is still running after cancel", name)
		}
	}

	r, _, err := e.svc.Get(ws, id)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != Running {
		t.Fatalf("cancelled run %+v, want it left running on disk", r)
	}

	// A fresh Service (against the same data dir and a working image for
	// this answer's processor) picks the interrupted run up and finishes
	// it exactly once.
	e.rebind(t, registry([]proc{{name: "p", image: proctest.Image(t, "echo")}}, map[string]string{fixture.TaskB: "p"}))
	e.svc = e.open(t, Config{})
	e.svc.Start(t.Context())
	e.svc.Wait()
	rs, err := e.svc.Runs(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("runs %+v, want the interrupted run completed exactly once, no duplicate", rs)
	}
	wantRun(t, rs[0], Succeeded, Proposed, ReasonAnswer)
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

// TestQueuedRunTakesNewestPin is TestQueuedRunTakesNewestAnswer's pin-move
// variant (I4): the pin moves (changing the processor/digest TaskB binds
// to) while the queued run for an unchanged answer still waits to start;
// one run is made, bound to the newest pin.
func TestQueuedRunTakesNewestPin(t *testing.T) {
	echo, gen := proctest.Image(t, "echo"), proctest.Image(t, "generate")
	e := newStoppedEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("doc out pin-moved\n")})
	if err := e.svc.scan(ws, false); err != nil {
		t.Fatal(err)
	}
	e.rebind(t, registry([]proc{{name: "gen", image: gen}}, map[string]string{fixture.TaskB: "gen"}))
	if err := e.svc.scan(ws, false); err != nil {
		t.Fatal(err)
	}
	e.svc.Start(t.Context())
	rs := e.runs(t, ws)
	if len(rs) != 1 {
		t.Fatalf("runs %+v, want one (coalesced to the new pin)", rs)
	}
	wantRun(t, rs[0], Succeeded, Proposed, ReasonAnswer)
	if rs[0].Processor != "gen" || rs[0].Digest != digestOf(gen) {
		t.Errorf("run %+v, want it bound to the newest pin (processor gen)", rs[0])
	}
}

// TestRevertAfterCoalescing is Critical finding C1's required test: an
// answer is saved, scanned (queuing a run for it) and then changed again
// before that run starts, so the queued run is coalesced into the newer
// content and the original content's key is abandoned without ever
// having actually run. Reverting back to the original content later must
// not be mistaken for "already covered" by that abandoned, stale key.
func TestRevertAfterCoalescing(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newStoppedEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})

	e.commit(t, ws, map[string]string{answerB: markdownAnswer("alpha\n")})
	if err := e.svc.scan(ws, false); err != nil {
		t.Fatal(err)
	}
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("beta\n")})
	if err := e.svc.scan(ws, false); err != nil {
		t.Fatal(err)
	}
	e.svc.Start(t.Context())
	rs := e.runs(t, ws)
	if len(rs) != 1 {
		t.Fatalf("runs %+v, want one run, coalesced to beta", rs)
	}
	wantRun(t, rs[0], Succeeded, Proposed, ReasonAnswer)

	// Revert to alpha: its key was never actually run (the queued run
	// above read beta, not alpha, when it started) and must not still
	// be considered covered.
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("alpha\n")})
	rs = e.runs(t, ws)
	if len(rs) != 2 {
		t.Fatalf("runs %+v, want a second run after reverting to alpha", rs)
	}
	wantRun(t, rs[0], Succeeded, Proposed, ReasonAnswer)

	// The open proposal must reflect what is actually on main (alpha),
	// not the stale beta content from the first run.
	p, err := proposal.Get(e.st, ws, fixture.TaskB)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := e.st.WorkspaceRepo(ws)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.TreeFS("refs/heads/" + p.Branch)
	if err != nil {
		t.Fatal(err)
	}
	if !treeContains(t, tree, "documents", "alpha") {
		t.Error("the open proposal must reflect the reverted answer (alpha)")
	}
	if treeContains(t, tree, "documents", "beta") {
		t.Error("the open proposal must not still reflect the superseded answer (beta)")
	}
}

// TestNewerAnswerNeverOverwrittenByOlderRun is Critical finding C2's
// required test: an older run is still executing its (slow) container
// when a newer answer is saved; the newer run must not start executing
// until the older one has fully finished, and the proposal must end up
// reflecting the newer answer regardless of how slow the older run is.
func TestNewerAnswerNeverOverwrittenByOlderRun(t *testing.T) {
	gen := proctest.Image(t, "generate")
	e := newEnv(t, registry([]proc{{name: "p", image: gen}}, map[string]string{fixture.TaskB: "p"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("sleep 3s\ndoc out v1x\n")})

	// Wait until the slow (older) run is actually executing.
	waitUntil(t, 10*time.Second, func() bool {
		rs, err := e.svc.Runs(ws)
		return err == nil && len(rs) == 1 && rs[0].State == Running
	})

	// Save a newer answer while the older run is still asleep.
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("doc out v2x\n")})

	// The newer run must be queued but must NOT start executing while
	// the older one is still running: confirm one running and one queued
	// record while the older run is still asleep.
	waitUntil(t, 2*time.Second, func() bool {
		rs, err := e.svc.Runs(ws)
		return err == nil && len(rs) == 2
	})
	rs, err := e.svc.Runs(ws)
	if err != nil {
		t.Fatal(err)
	}
	var running, queued int
	for _, r := range rs {
		switch r.State {
		case Running:
			running++
		case Queued:
			queued++
		}
	}
	if running != 1 || queued != 1 {
		t.Fatalf("while the older run is executing, want exactly one running and one queued, got %+v", rs)
	}

	rs = e.runs(t, ws) // waits for both to finish
	if len(rs) != 2 {
		t.Fatalf("runs %+v", rs)
	}
	newer, older := rs[0], rs[1] // Runs returns newest first
	wantRun(t, older, Succeeded, Proposed, ReasonAnswer)
	wantRun(t, newer, Succeeded, Proposed, ReasonAnswer)
	if older.Finished.After(newer.Started) {
		t.Errorf("the older run (finished %s) must fully finish before the newer one starts (started %s) — "+
			"overlap means they could race on the proposal write", older.Finished, newer.Started)
	}

	p, err := proposal.Get(e.st, ws, fixture.TaskB)
	if err != nil {
		t.Fatal(err)
	}
	if p.Branch != newer.Branch {
		t.Errorf("open proposal branch %q, want the newer run's branch %q", p.Branch, newer.Branch)
	}
	repo, err := e.st.WorkspaceRepo(ws)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.TreeFS("refs/heads/" + p.Branch)
	if err != nil {
		t.Fatal(err)
	}
	if !treeContains(t, tree, "documents", "v2x") {
		t.Error("the open proposal must reflect the newer answer (v2x)")
	}
	if treeContains(t, tree, "documents", "v1x") {
		t.Error("the open proposal must not reflect the older, superseded answer (v1x)")
	}
}

// TestRedundantKeyNotReexecuted is Important finding I1's required test:
// a run whose key, by the time it actually executes, already has a
// terminal record from another run (and whose own reason is not an
// explicit retry or merge, which are allowed to redo the work) must not
// run the processor again.
func TestRedundantKeyNotReexecuted(t *testing.T) {
	gen := proctest.Image(t, "generate")
	e := newEnv(t, registry([]proc{{name: "p", image: gen}}, map[string]string{fixture.TaskB: "p"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("stderr marker\ndoc out v1\n")})
	first := e.runs(t, ws)[0]
	wantRun(t, first, Succeeded, Proposed, ReasonAnswer)

	// Force a second run of the exact same, unchanged answer (Rerun does
	// not check whether the key already has a record — merge reruns
	// explicitly want that). Its reason is not retry/merge, so prepare
	// must recognize the key already has a terminal result and skip
	// running the processor again.
	got, err := e.svc.Rerun(ws, []string{fixture.TaskB}, ReasonAnswer)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Rerun = %+v", got)
	}
	rs := e.runs(t, ws)
	if len(rs) != 2 {
		t.Fatalf("runs %+v", rs)
	}
	second := rs[0]
	wantRun(t, second, Succeeded, Unchanged, ReasonAnswer)
	if second.Key != first.Key {
		t.Errorf("the forced rerun of unchanged content must resolve to the same key: %+v, want %s", second, first.Key)
	}
	_, log, err := e.svc.Get(ws, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 0 {
		t.Errorf("a redundant run must not execute the processor again; log = %q", log)
	}
}

// TestDeletedAnswerReleasesKeyOnFailure is round 2's required test (a): an
// answer is queued, then deleted before the run starts, so prepare fails
// before it ever binds to a confirmed key. That failure must release the
// record's queue-time key (same bug class as C1) so re-adding the exact
// same answer later triggers a fresh run instead of being blocked by a
// stale key from a run that never actually bound to it.
func TestDeletedAnswerReleasesKeyOnFailure(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newStoppedEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n")})
	if err := e.svc.scan(ws, false); err != nil {
		t.Fatal(err)
	}

	// Delete the answer before the queued run starts.
	e.commit(t, ws, map[string]string{answerB: ""})
	e.svc.Start(t.Context())
	rs := e.runs(t, ws)
	if len(rs) != 1 {
		t.Fatalf("runs %+v, want one failed run", rs)
	}
	wantRun(t, rs[0], Failed, None, ReasonAnswer)
	if !strings.Contains(rs[0].Error, "no longer on main") {
		t.Errorf("error %q, want it to name the missing answer", rs[0].Error)
	}
	if rs[0].Key != "" {
		t.Errorf("a run that failed before binding to a key must not still carry one: %+v", rs[0])
	}

	// Re-adding the exact same answer must trigger a fresh run: the
	// failed attempt's key must not still be blocking it.
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n")})
	rs = e.runs(t, ws)
	if len(rs) != 2 {
		t.Fatalf("runs %+v, want a second run after re-adding the deleted answer", rs)
	}
	wantRun(t, rs[0], Succeeded, Proposed, ReasonAnswer)
}

// TestUnboundTaskReleasesKeyOnFailure is round 2's required test (b): an
// answer is queued, then its task is unbound (the processor stays
// registered, but the binding naming it is removed) before the run
// starts, so prepare fails before it ever binds to a confirmed key. That
// failure must release the record's queue-time key so rebinding the same
// processor (and so the same digest) later triggers a fresh run instead
// of being blocked by a stale key.
func TestUnboundTaskReleasesKeyOnFailure(t *testing.T) {
	echo := proctest.Image(t, "echo")
	e := newStoppedEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	e.commit(t, ws, map[string]string{answerB: markdownAnswer("x\n")})
	if err := e.svc.scan(ws, false); err != nil {
		t.Fatal(err)
	}

	// Unbind TaskB before the queued run starts: the processor stays
	// registered, but nothing names it for TaskB any more.
	e.rebind(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{}))
	e.svc.Start(t.Context())
	rs := e.runs(t, ws)
	if len(rs) != 1 {
		t.Fatalf("runs %+v, want one failed run", rs)
	}
	wantRun(t, rs[0], Failed, None, ReasonAnswer)
	if !strings.Contains(rs[0].Error, "no longer bound") {
		t.Errorf("error %q, want it to name the task as unbound", rs[0].Error)
	}
	if rs[0].Key != "" {
		t.Errorf("a run that failed before binding to a key must not still carry one: %+v", rs[0])
	}

	// Rebinding the same processor (and so the same digest) must trigger
	// a fresh run: the failed attempt's key must not still block it.
	e.rebind(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}))
	rs = e.runs(t, ws)
	if len(rs) != 2 {
		t.Fatalf("runs %+v, want a second run after rebinding the task", rs)
	}
	wantRun(t, rs[0], Succeeded, Proposed, ReasonAnswer)
}
