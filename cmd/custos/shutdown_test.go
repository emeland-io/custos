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
