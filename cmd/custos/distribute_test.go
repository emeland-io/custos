package main

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/distribute"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/merge"
	"github.com/emeland-io/custos/internal/server"
	"github.com/emeland-io/custos/internal/store"
)

const otherWorkspace = "6c7d8e9f-0a1b-4c2d-b3e4-f5a6b7c8d9e0"

var jane = gitrepo.Signature{Name: "Jane Doe", Email: "jane@example.org"}

// lockedBuffer is a bytes.Buffer that the server goroutines and the test
// can use at the same time.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// wired opens a store in a fresh data directory with hooks that run exe and
// wires distribution and the merge/fork endpoints the way serve does,
// including reconciling a workspace right after an API merge or fork moves
// its main (openServer's onMainMoved).
func wired(t *testing.T, exe string, log *lockedBuffer) (*store.Store, *server.Server) {
	t.Helper()
	data := t.TempDir()
	st, err := store.Open(data, exe, "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InstallHooks(); err != nil {
		t.Fatal(err)
	}
	bl, err := blobs.Open(filepath.Join(data, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(st, bl)
	merge.Register(a, st, func(id string) {
		if err := distribute.ReconcileWorkspace(st, id); err != nil {
			fmt.Fprintf(log, "custos serve: workspace %s: %v\n", id, err)
		}
	}, nil)
	srv := server.New(st)
	startDistribution(st, a, srv, log)
	srv.WithAPI(a.Handler())
	return st, srv
}

func pinOf(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	w, _, err := st.Load(id, "")
	if err != nil {
		t.Fatal(err)
	}
	return w.Config.Catalog.Commit
}

func TestCatalogPushIsDistributed(t *testing.T) {
	// The catalog's pre-receive hook runs the custos binary.
	bin := filepath.Join(t.TempDir(), "custos")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/emeland-io/custos/cmd/custos").CombinedOutput(); err != nil {
		t.Fatalf("build custos: %v\n%s", err, out)
	}
	var log lockedBuffer
	st, srv := wired(t, bin, &log)
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	remote := ts.URL + "/git/catalog.git"

	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", "--quiet", remote, "main")
	for _, id := range []string{fixture.WorkspaceID, otherWorkspace} {
		if err := st.CreateWorkspace(id, jane); err != nil {
			t.Fatal(err)
		}
	}
	if err := distribute.Freeze(st, otherWorkspace, jane); err != nil {
		t.Fatal(err)
	}

	c2 := gittest.Commit(t, work, map[string]string{
		fixture.TaskPath(fixture.TaskA, "1.2.0"): fixture.TaskFile(fixture.TaskA, "1.2.0", fixture.TaskA+"@1.1.0"),
	})
	gittest.Run(t, work, "push", "--quiet", remote, "main")

	// OnCatalogPush may run after the response is sent, so poll.
	deadline := time.Now().Add(10 * time.Second)
	for {
		ps, err := distribute.Proposals(st, otherWorkspace)
		if err != nil {
			t.Fatal(err)
		}
		if pinOf(t, st, fixture.WorkspaceID) == c2 && len(ps) == 1 && ps[0].Branch == "custos/pin/"+c2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("catalog commit %s was not distributed; pin %s, proposals %+v; log:\n%s",
				c2, pinOf(t, st, fixture.WorkspaceID), ps, log.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pinOf(t, st, otherWorkspace) == c2 {
		t.Error("the frozen workspace's pin moved")
	}
	if s := log.String(); s != "" {
		t.Errorf("unexpected log:\n%s", s)
	}
}

// TestWorkspacePushIsDistributed checks that a push to a workspace's main
// that unfreezes it reconciles that workspace right away, through
// OnWorkspacePush, without waiting for the next catalog push: the pin moves
// to the catalog's current main and the proposal opened for it is pruned.
func TestWorkspacePushIsDistributed(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "custos")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/emeland-io/custos/cmd/custos").CombinedOutput(); err != nil {
		t.Fatalf("build custos: %v\n%s", err, out)
	}
	var log lockedBuffer
	st, srv := wired(t, bin, &log)
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	catalogRemote := ts.URL + "/git/catalog.git"

	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", "--quiet", catalogRemote, "main")
	if err := st.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	if err := distribute.Freeze(st, fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	c1 := pinOf(t, st, fixture.WorkspaceID)

	c2 := gittest.Commit(t, work, map[string]string{
		fixture.TaskPath(fixture.TaskA, "1.2.0"): fixture.TaskFile(fixture.TaskA, "1.2.0", fixture.TaskA+"@1.1.0"),
	})
	gittest.Run(t, work, "push", "--quiet", catalogRemote, "main")

	// Wait for the catalog push's reconcile to open the proposal.
	deadline := time.Now().Add(10 * time.Second)
	for {
		ps, err := distribute.Proposals(st, fixture.WorkspaceID)
		if err != nil {
			t.Fatal(err)
		}
		if len(ps) == 1 && ps[0].Branch == "custos/pin/"+c2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("proposal for %s was not opened; proposals %+v; log:\n%s", c2, ps, log.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	// An engineer unfreezes by pushing custos.yaml directly, not through the
	// API, and with no further catalog push: ReconcileWorkspace must still
	// move the pin and prune the now-unneeded proposal.
	clone := filepath.Join(t.TempDir(), "ws")
	gittest.Run(t, t.TempDir(), "clone", "--quiet", ts.URL+"/git/workspaces/"+fixture.WorkspaceID+".git", clone)
	gittest.Commit(t, clone, map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, c1)})
	gittest.Run(t, clone, "push", "--quiet", "origin", "main")

	deadline = time.Now().Add(10 * time.Second)
	for {
		ps, err := distribute.Proposals(st, fixture.WorkspaceID)
		if err != nil {
			t.Fatal(err)
		}
		if pinOf(t, st, fixture.WorkspaceID) == c2 && len(ps) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("workspace push was not distributed; pin %s, proposals %+v; log:\n%s",
				pinOf(t, st, fixture.WorkspaceID), ps, log.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s := log.String(); s != "" {
		t.Errorf("unexpected log:\n%s", s)
	}
}

func TestStartDistributionCatchesUp(t *testing.T) {
	// Catalog commits made while the server was down are distributed when it
	// starts (ruling 2.5); a broken workspace is logged and the others are
	// still updated (spec §7). No push runs, so the hook binary is never used.
	exe := filepath.Join(t.TempDir(), "custos")
	data := t.TempDir()
	st, err := store.Open(data, exe, "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	cat := st.CatalogRepo()
	commit := func(base string, files map[string]string) string {
		req := gitrepo.CommitRequest{Author: gitrepo.Bot, Message: "Change the catalog"}
		if base != "" {
			req.Base, req.Parents = base, []string{base}
		}
		for _, p := range slices.Sorted(maps.Keys(files)) {
			req.Changes = append(req.Changes, gitrepo.Change{Path: p, Data: []byte(files[p])})
		}
		oid, err := cat.WriteCommit(req)
		if err != nil {
			t.Fatal(err)
		}
		if err := cat.UpdateRef("refs/heads/main", oid, base); err != nil {
			t.Fatal(err)
		}
		return oid
	}
	c1 := commit("", fixture.Catalog())
	for _, id := range []string{fixture.WorkspaceID, otherWorkspace} {
		if err := st.CreateWorkspace(id, jane); err != nil {
			t.Fatal(err)
		}
	}
	// Break the first workspace's custos.yaml behind the store's back.
	repo, err := st.WorkspaceRepo(fixture.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	main, _, err := repo.ResolveRef("refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	broken, err := repo.WriteCommit(gitrepo.CommitRequest{
		Base: main, Parents: []string{main}, Author: gitrepo.Bot, Message: "Break custos.yaml",
		Changes: []gitrepo.Change{{Path: "custos.yaml", Data: []byte("workspace: [\n")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRef("refs/heads/main", broken, main); err != nil {
		t.Fatal(err)
	}
	c2 := commit(c1, map[string]string{
		fixture.TaskPath(fixture.TaskA, "1.2.0"): fixture.TaskFile(fixture.TaskA, "1.2.0", fixture.TaskA+"@1.1.0"),
	})

	var log lockedBuffer
	bl, err := blobs.Open(filepath.Join(data, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	startDistribution(st, api.New(st, bl), server.New(st), &log)

	if got := pinOf(t, st, otherWorkspace); got != c2 {
		t.Errorf("pin %s, want %s", got, c2)
	}
	if s := log.String(); !bytes.Contains([]byte(s), []byte("workspace "+fixture.WorkspaceID)) {
		t.Errorf("log does not name the broken workspace:\n%s", s)
	}
}

// unfrozenConfig is a custos.yaml for workspace id, pinned to pin, with
// frozen left at its default (false).
func unfrozenConfig(st *store.Store, id, pin string) string {
	return "workspace: " + id + "\ncatalog:\n  url: " + st.CatalogURL() + "\n  commit: " + pin + "\n"
}

// TestMergeThroughAPIIsDistributed checks that a merge made through the
// REST API reconciles the merged-into workspace right away, through
// merge.Register's onMainMoved, without waiting for a catalog push or a
// further push to the workspace: merging a branch that unfreezes a
// workspace whose pin lags the catalog moves the pin to the catalog's main
// and prunes the pin proposal in the same request (ruling 2.20).
func TestMergeThroughAPIIsDistributed(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "custos")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/emeland-io/custos/cmd/custos").CombinedOutput(); err != nil {
		t.Fatalf("build custos: %v\n%s", out, err)
	}
	var log lockedBuffer
	st, srv := wired(t, bin, &log)
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	catalogRemote := ts.URL + "/git/catalog.git"

	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())
	gittest.Run(t, work, "push", "--quiet", catalogRemote, "main")
	if err := st.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	if err := distribute.Freeze(st, fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	c1 := pinOf(t, st, fixture.WorkspaceID)

	c2 := gittest.Commit(t, work, map[string]string{
		fixture.TaskPath(fixture.TaskA, "1.2.0"): fixture.TaskFile(fixture.TaskA, "1.2.0", fixture.TaskA+"@1.1.0"),
	})
	gittest.Run(t, work, "push", "--quiet", catalogRemote, "main")

	// Wait for the catalog push's reconcile to open the proposal, so the
	// frozen workspace's pin lags the catalog before the merge.
	deadline := time.Now().Add(10 * time.Second)
	for {
		ps, err := distribute.Proposals(st, fixture.WorkspaceID)
		if err != nil {
			t.Fatal(err)
		}
		if len(ps) == 1 && ps[0].Branch == "custos/pin/"+c2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("proposal for %s was not opened; proposals %+v; log:\n%s", c2, ps, log.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Push a plain branch, not main, that sets frozen: false: OnWorkspacePush
	// reconciles main, which is still frozen and unchanged, so this alone
	// moves nothing.
	clone := filepath.Join(t.TempDir(), "ws")
	gittest.Run(t, t.TempDir(), "clone", "--quiet", ts.URL+"/git/workspaces/"+fixture.WorkspaceID+".git", clone)
	gittest.Run(t, clone, "checkout", "-q", "-b", "unfreeze")
	gittest.Commit(t, clone, map[string]string{"custos.yaml": unfrozenConfig(st, fixture.WorkspaceID, c1)})
	gittest.Run(t, clone, "push", "--quiet", "origin", "unfreeze")
	if got := pinOf(t, st, fixture.WorkspaceID); got != c1 {
		t.Fatalf("pushing the branch alone moved the pin to %s", got)
	}
	if ps, err := distribute.Proposals(st, fixture.WorkspaceID); err != nil {
		t.Fatal(err)
	} else if len(ps) != 1 || ps[0].Branch != "custos/pin/"+c2 {
		t.Fatalf("pushing the branch alone changed the proposals: %+v", ps)
	}

	// Merge the branch through the REST API: main is no longer frozen, but
	// the merge itself does not touch the pin.
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/workspaces/"+fixture.WorkspaceID+"/merge",
		strings.NewReader(`{"branch":"unfreeze"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custos-Author", "Jane Doe <jane@example.org>")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("merge: %d %s", resp.StatusCode, body)
	}

	// Without any further push, the merge's onMainMoved callback must have
	// already reconciled the workspace: the pin now follows the catalog's
	// main and the proposal is gone.
	if got := pinOf(t, st, fixture.WorkspaceID); got != c2 {
		t.Errorf("pin = %s, want the catalog's main %s", got, c2)
	}
	if ps, err := distribute.Proposals(st, fixture.WorkspaceID); err != nil {
		t.Fatal(err)
	} else if len(ps) != 0 {
		t.Errorf("proposals = %+v, want none", ps)
	}
	if s := log.String(); s != "" {
		t.Errorf("unexpected log:\n%s", s)
	}
}
