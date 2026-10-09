package runs

import (
	"errors"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/proposal"
	"github.com/emeland-io/custos/internal/store"
)

// dryEnv has processor "scan" (echo) bound to TaskB and an answer to TaskB
// in two workspaces, all runs done.
func dryEnv(t *testing.T) *env {
	t.Helper()
	echo := proctest.Image(t, "echo")
	e := newEnv(t, registry([]proc{{name: "scan", image: echo}}, map[string]string{fixture.TaskB: "scan"}), Config{})
	if err := e.st.CreateWorkspace(otherWS, alice); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ws, otherWS} {
		e.commit(t, id, map[string]string{answerB: markdownAnswer("task host Host one\n")})
	}
	e.svc.Wait()
	return e
}

func TestDryRun(t *testing.T) {
	e := dryEnv(t)
	gen := proctest.Image(t, "generate")
	id, err := e.svc.DryRun("scan", gen)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	rep, err := e.svc.DryRunStatus(id)
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != Succeeded || rep.Runs != 2 || rep.Failed != 0 || rep.Proposals != 2 || rep.Workspaces != 2 || len(rep.Samples) != 2 {
		t.Fatalf("report %+v", rep)
	}
	s := rep.Samples[0]
	if s.Task != fixture.TaskB || !strings.Contains(s.Workspace+otherWS+ws, s.Workspace) || len(s.Items) == 0 {
		t.Errorf("sample %+v", s)
	}
	// Nothing was written: the open proposals are still those of echo.
	for _, w := range []string{ws, otherWS} {
		p, err := proposal.Get(e.st, w, fixture.TaskB)
		if err != nil || p.Digest == digestOf(gen) {
			t.Errorf("workspace %s: proposal %+v, %v", w, p, err)
		}
		if rs, _ := e.svc.Runs(w); len(rs) != 1 {
			t.Errorf("workspace %s: a dry run must not create run records: %+v", w, rs)
		}
	}
}

func TestDryRunFailures(t *testing.T) {
	e := dryEnv(t)
	id, err := e.svc.DryRun("scan", proctest.Image(t, "fail"))
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	if rep, _ := e.svc.DryRunStatus(id); rep.State != Succeeded || rep.Runs != 2 || rep.Failed != 2 || rep.Proposals != 0 {
		t.Errorf("failing image: %+v", rep)
	}

	id, err = e.svc.DryRun("scan", "custos.test/missing@sha256:"+strings.Repeat("0", 64))
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	if rep, _ := e.svc.DryRunStatus(id); rep.State != Failed && rep.Failed != rep.Runs {
		t.Errorf("missing image: %+v", rep)
	}

	if _, err := e.svc.DryRun("scan", "custos.test/echo:latest"); !errors.Is(err, ErrInvalid) {
		t.Errorf("image without digest: %v", err)
	}
	if _, err := e.svc.DryRun("nope", proctest.Image(t, "echo")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown processor: %v", err)
	}
	if _, err := e.svc.DryRunStatus(noSuchID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown dry run: %v", err)
	}
}
