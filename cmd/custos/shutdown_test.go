package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/runs"
	"github.com/emeland-io/custos/internal/store"
)

// runningRunContainers returns the names of the running "custos-run-*"
// containers the runner starts (see internal/runner.Run), using the docker
// CLI directly so the test can assert a container was actually started,
// and later actually removed, independent of the run service's own
// bookkeeping.
func runningRunContainers(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("docker", "ps", "--filter", "name=custos-run-", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names
}

// newRunContainers returns the names in after that are not in before, so
// the test can scope a "my container is gone" check to the one it
// actually introduced rather than the whole "custos-run-*" namespace,
// which internal/runner's and internal/runs's own tests also use and
// which go test may be exercising concurrently in a sibling package.
func newRunContainers(before, after []string) []string {
	var out []string
	for _, name := range after {
		if !slices.Contains(before, name) {
			out = append(out, name)
		}
	}
	return out
}

// waitForCondition polls cond (which must not block) until it reports
// true or timeout elapses, in which case the test fails.
func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestServeShutdownWaitsForProcessorWorkers checks that serveUntilDone,
// which runServe calls once ctx is cancelled (a SIGINT/SIGTERM in the real
// binary), does not return until the run service's workers have actually
// stopped — not merely been signalled to — so a run killed mid-execution
// by the shutdown has its container actually removed, and its record's
// "running" state settled on disk, before the process may exit. This
// mirrors internal/runs's own TestRestartAfterRealCancellation, which
// checks the same real-container property one level down, by cancelling
// a bare context directly rather than going through an OS signal.
func TestServeShutdownWaitsForProcessorWorkers(t *testing.T) {
	loop := proctest.Image(t, "loop") // never finishes on its own
	data := t.TempDir()
	cat := fixture.Catalog()
	cat["processors.yaml"] = "processors:\n  scan:\n    image: " + loop + "\nbindings:\n  " + fixture.TaskB + ": scan\n"
	seedCatalogFiles(t, data, cat)

	// Snapshot what is running before this test starts anything, so the
	// check below is scoped to the container this run introduces.
	before := runningRunContainers(t)

	ctx, cancel := context.WithCancel(context.Background())
	srv, err := openServer(ctx, data, "http://127.0.0.1:8080", defaultProcessorOptions(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	ws := fixture.WorkspaceID
	apiCall(t, ts, "POST", "/api/workspaces", `{"id":"`+ws+`"}`, http.StatusCreated, nil)
	apiCall(t, ts, "PUT", "/api/workspaces/"+ws+"/answers/"+fixture.TaskB, `{"value":"task host Host one"}`, http.StatusOK, nil)

	// Wait for the loop processor's own container to actually be up
	// before triggering shutdown, so the test exercises a run that is
	// genuinely mid-execution, not one still queued.
	var mine []string
	waitForCondition(t, 15*time.Second, func() bool {
		mine = newRunContainers(before, runningRunContainers(t))
		return len(mine) > 0
	})

	// Drive the exact shutdown path runServe uses (serveUntilDone), on its
	// own listener, cancelling ctx the way a SIGINT/SIGTERM would.
	var stderr lockedBuffer
	done := make(chan int, 1)
	go func() { done <- serveUntilDone(ctx, srv, h, "127.0.0.1:0", data, io.Discard, &stderr) }()
	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("serveUntilDone returned %d, stderr %q", code, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serveUntilDone did not return within 30s of shutdown being triggered")
	}

	// The whole point of this test: by the time serveUntilDone has
	// returned, the container it interrupted must actually be gone, not
	// merely asked to stop.
	still := runningRunContainers(t)
	for _, name := range mine {
		if slices.Contains(still, name) {
			t.Fatalf("container %s is still running after shutdown returned", name)
		}
	}
	if s := stderr.String(); strings.Contains(s, "shutdown timeout") {
		t.Errorf("shutdown reported a timeout, want the worker to finish within it:\n%s", s)
	}
}

// TestServeShutdownDoesNotWaitForQueuedBacklog is the regression test for
// the Important finding that shutdown, with --processor-workers set low
// and a real backlog (more answers bound to a never-finishing processor
// than there are workers to run them), waited the full shutdownTimeout and
// logged a false "did not stop" warning — even though the actually
// running container was gone in well under a second. The queue exists
// precisely to absorb bursts like this, so the backlog here is ordinary,
// not a corner case: once ctx is cancelled, worker's loop stops pulling
// new work from the queue (it checks ctx.Err() before every nextReady),
// so the still-queued, never-started answer can never finish on its own
// and waiting for it (the old Wait semantics, which svc.WaitInFlight,
// registered with srv.OnShutdown in its place, deliberately does not) was
// always going to time out.
//
// It asserts everything the fix promises: (a) serveUntilDone returns
// quickly, not after the full timeout; (b) it logs no misleading timeout
// warning; (c) the one container actually running is confirmed gone; (d)
// the still-queued record is left Queued on disk — not lost, not marked
// failed — and a fresh Service picks it up again (TestRestartAfterRealCancellation
// and TestRestartQueuesInterruptedRuns check the same "left for the next
// start" property one level down, for the interrupted run itself).
func TestServeShutdownDoesNotWaitForQueuedBacklog(t *testing.T) {
	loop := proctest.Image(t, "loop") // never finishes on its own
	data := t.TempDir()
	cat := fixture.Catalog()
	// Two answers bound to the same never-finishing processor, with a
	// single worker below: one can be executing while the other has
	// nothing to run it, which is the backlog this test needs.
	cat["processors.yaml"] = "processors:\n  scan:\n    image: " + loop +
		"\nbindings:\n  " + fixture.TaskA + ": scan\n  " + fixture.TaskB + ": scan\n"
	seedCatalogFiles(t, data, cat)

	before := runningRunContainers(t)

	ctx, cancel := context.WithCancel(context.Background())
	procOpts := defaultProcessorOptions()
	procOpts.workers = 1
	srv, err := openServer(ctx, data, "http://127.0.0.1:8080", procOpts, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	ws := fixture.WorkspaceID
	apiCall(t, ts, "POST", "/api/workspaces", `{"id":"`+ws+`"}`, http.StatusCreated, nil)
	apiCall(t, ts, "PUT", "/api/workspaces/"+ws+"/answers/"+fixture.TaskA, `{"value":"a"}`, http.StatusOK, nil)
	apiCall(t, ts, "PUT", "/api/workspaces/"+ws+"/answers/"+fixture.TaskB, `{"value":"task host Host one"}`, http.StatusOK, nil)

	// Wait until the backlog this test needs actually exists: one run's
	// container is up (the single worker took it) and the other answer's
	// run is still sitting in the queue, never started.
	var mine []string
	waitForCondition(t, 15*time.Second, func() bool {
		mine = newRunContainers(before, runningRunContainers(t))
		if len(mine) == 0 {
			return false
		}
		var rs []runs.Record
		apiCall(t, ts, "GET", "/api/workspaces/"+ws+"/runs", "", http.StatusOK, &rs)
		queued, running := 0, 0
		for _, r := range rs {
			switch r.State {
			case runs.Queued:
				queued++
			case runs.Running:
				running++
			}
		}
		return queued == 1 && running == 1
	})

	var stderr lockedBuffer
	done := make(chan int, 1)
	start := time.Now()
	go func() { done <- serveUntilDone(ctx, srv, h, "127.0.0.1:0", data, io.Discard, &stderr) }()
	cancel()

	var elapsed time.Duration
	select {
	case code := <-done:
		elapsed = time.Since(start)
		if code != 0 {
			t.Errorf("serveUntilDone returned %d, stderr %q", code, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serveUntilDone did not return within 30s of shutdown being triggered")
	}

	// (a) The whole point of this test: shutdown must not be held to the
	// full shutdownTimeout (10s) by the still-queued, never-started
	// answer — only the one run actually in flight should matter, and
	// that one's container is killed and removed in well under a second.
	if elapsed >= 3*time.Second {
		t.Errorf("serveUntilDone took %s, want well under the %s shutdown timeout: a queued-but-not-started "+
			"backlog must not hold shutdown up", elapsed, shutdownTimeout)
	}
	// (b) No misleading "did not stop" warning.
	if s := stderr.String(); strings.Contains(s, "did not stop before the shutdown timeout") {
		t.Errorf("shutdown reported a timeout, want it to have returned quickly once the in-flight run stopped:\n%s", s)
	}
	// (c) The container actually running is confirmed gone, not merely
	// asked to stop. docker rm --force's CLI return and docker ps's view
	// of container state are not perfectly synchronized under host/Docker
	// contention, so this polls with a short, bounded timeout rather than
	// checking once immediately; the removal itself is already synchronous
	// (runner.remove runs "docker rm --force" before Run returns), so this
	// is confirming something that should already be done, not waiting for
	// new work, hence the short timeout.
	waitForCondition(t, 5*time.Second, func() bool {
		still := runningRunContainers(t)
		for _, name := range mine {
			if slices.Contains(still, name) {
				return false
			}
		}
		return true
	})

	// (d) The still-queued (never started) record is left Queued on disk,
	// and the interrupted (actually running) one is left Running, exactly
	// as TestRestartAfterRealCancellation checks for a single interrupted
	// run; neither is lost or marked failed.
	var rs []runs.Record
	apiCall(t, ts, "GET", "/api/workspaces/"+ws+"/runs", "", http.StatusOK, &rs)
	var sawQueued, sawRunning bool
	for _, r := range rs {
		switch r.State {
		case runs.Queued:
			sawQueued = true
		case runs.Running:
			sawRunning = true
		default:
			t.Errorf("run %s is %s after shutdown, want it left Queued or Running", r.ID, r.State)
		}
	}
	if !sawQueued || !sawRunning {
		t.Fatalf("runs after shutdown %+v, want exactly one left Queued (never started) and one left Running (interrupted)", rs)
	}

	// A fresh Service, loading the same on-disk records, picks both back
	// up: New() requeues anything Queued or Running (see service.go),
	// exactly as TestRestartQueuesInterruptedRuns and
	// TestRestartAfterRealCancellation check one level down for the
	// interrupted run alone.
	st2, err := store.Open(data, "/custos", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := openRuns(st2, defaultProcessorOptions())
	if err != nil {
		t.Fatal(err)
	}
	rs2, err := fresh.Runs(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs2) != len(rs) {
		t.Fatalf("fresh Service runs %+v, want the same %d records reloaded", rs2, len(rs))
	}
	for _, r := range rs2 {
		if r.State != runs.Queued {
			t.Errorf("after a fresh Service loads, run %s is %s, want it queued again so it is picked up and retried", r.ID, r.State)
		}
	}
}
