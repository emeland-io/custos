package merge

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/store"
)

func TestForkCopiesMainAndSetsID(t *testing.T) {
	e := setup(t)
	answer := textAnswer(fixture.TaskA, "1.1.0", "built by make")
	srcMain := commitFiles(t, e.st, ws, mainRef, map[string]string{answerA: answer})
	commitFiles(t, e.st, ws, whatIf, map[string]string{answerB: textAnswer(fixture.TaskB, "1.0.0", "draft")})

	if err := Fork(e.st, ws, forkID, alice); err != nil {
		t.Fatal(err)
	}

	forkMain := ref(t, e.st, forkID, mainRef)
	if got := gitOut(t, e.st, forkID, "rev-parse", forkMain+"^"); got != srcMain {
		t.Errorf("parent of the fork commit = %s, want the source's main %s", got, srcMain)
	}
	if got := gitOut(t, e.st, forkID, "log", "-1", "--format=%an <%ae>|%cn", forkMain); got != "Alice Example <alice@example.org>|custos-bot" {
		t.Errorf("author|committer = %q", got)
	}
	cfg, srcCfg := readConfig(t, e, forkID, forkMain), readConfig(t, e, ws, srcMain)
	if cfg.Workspace != forkID || cfg.Catalog != srcCfg.Catalog || cfg.Frozen != srcCfg.Frozen {
		t.Errorf("fork custos.yaml = %+v, want the source's %+v with workspace %s", cfg, srcCfg, forkID)
	}
	if got, ok := file(t, e.st, forkID, forkMain, answerA); !ok || got != answer {
		t.Errorf("answer in fork = %q (present %v), want %q", got, ok, answer)
	}
	if refs := gitOut(t, e.st, forkID, "for-each-ref", "--format=%(refname)"); refs != "refs/heads/main" {
		t.Errorf("fork refs = %q, want only refs/heads/main", refs)
	}
	if got := ref(t, e.st, ws, mainRef); got != srcMain {
		t.Errorf("source main moved to %s", got)
	}
}

func TestForkUnknownSource(t *testing.T) {
	e := setup(t)
	if err := Fork(e.st, unknownTask, forkID, alice); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Fork from unknown workspace: %v, want ErrNotFound", err)
	}
	if _, err := e.st.WorkspaceRepo(forkID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("fork repository exists after a failed fork: %v", err)
	}
}

func TestForkOntoExistingWorkspace(t *testing.T) {
	e := setup(t)
	if err := Fork(e.st, ws, forkID, alice); err != nil {
		t.Fatal(err)
	}
	forkMain, srcMain := ref(t, e.st, forkID, mainRef), ref(t, e.st, ws, mainRef)
	for _, target := range []string{forkID, ws} {
		if err := Fork(e.st, ws, target, alice); !errors.Is(err, store.ErrExists) {
			t.Errorf("Fork onto %s: %v, want ErrExists", target, err)
		}
	}
	if got := ref(t, e.st, forkID, mainRef); got != forkMain {
		t.Errorf("existing fork's main moved to %s", got)
	}
	if got := ref(t, e.st, ws, mainRef); got != srcMain {
		t.Errorf("source main moved to %s", got)
	}
}

func TestForkInvalidID(t *testing.T) {
	e := setup(t)
	for _, id := range []string{"", "not-a-uuid", strings.ToUpper(forkID), "../" + forkID} {
		if err := Fork(e.st, ws, id, alice); !errors.Is(err, ErrInvalid) {
			t.Errorf("Fork to %q: %v, want ErrInvalid", id, err)
		}
	}
}

func TestForkConflictKeepsPush(t *testing.T) {
	e := setup(t)
	var pushed string
	beforeForkUpdate = func() {
		repo, err := e.st.WorkspaceRepo(forkID)
		if err != nil {
			t.Fatal(err)
		}
		// Simulate a push landing in the window between CreateWorkspaceRepo
		// and forkInto's own UpdateRef.
		pushed = gittest.Run(t, repo.Dir, "commit-tree", "4b825dc642cb6eb9a060e54bf8d69288fbee4904", "-m", "pushed")
		if err := repo.UpdateRef(mainRef, pushed, ""); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { beforeForkUpdate = func() {} })

	err := Fork(e.st, ws, forkID, alice)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("Fork while the new main was pushed concurrently: %v, want ErrConflict", err)
	}
	repo, statErr := e.st.WorkspaceRepo(forkID)
	if statErr != nil {
		t.Fatalf("the repository with the accepted push must survive: %v", statErr)
	}
	if _, err := os.Stat(repo.Dir); err != nil {
		t.Errorf("the repository with the accepted push must survive: %v", err)
	}
	if got := ref(t, e.st, forkID, mainRef); got != pushed {
		t.Errorf("the push must not be overwritten, got %q", got)
	}
}

func TestForkOfInvalidMainLeavesNothingBehind(t *testing.T) {
	e := setup(t)
	src := repoOf(t, e.st, ws)
	old := ref(t, e.st, ws, mainRef)
	// A manual edit on disk broke the source's main (spec §7).
	bad, err := src.WriteCommit(gitrepo.CommitRequest{
		Base:    old,
		Parents: []string{old},
		Changes: []gitrepo.Change{{Path: "answers/" + unknownTask + ".md", Data: []byte(textAnswer(unknownTask, "1.0.0", "x"))}},
		Author:  alice,
		Message: "bypass validation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := src.UpdateRef(mainRef, bad, old); err != nil {
		t.Fatal(err)
	}

	err = Fork(e.st, ws, forkID, alice)
	var rej *store.RejectedError
	if !errors.As(err, &rej) || !hasRule(rej.Problems, problem.RuleAnswer) {
		t.Fatalf("Fork of an invalid main: %v, want rejection with rule %s", err, problem.RuleAnswer)
	}
	if _, err := e.st.WorkspaceRepo(forkID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("half-created fork left behind: %v", err)
	}
}
