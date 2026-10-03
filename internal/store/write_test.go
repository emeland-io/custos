package store

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/status"
)

var answerA = "answers/" + fixture.TaskA + ".md"

func answerFile(version string) string {
	return "---\ntask: " + fixture.TaskA + "\ntask_version: " + version + "\ntype: text\nvalue: built with make\n---\n"
}

// put returns an edit that writes files.
func put(files map[string]string) func(fs.FS) ([]gitrepo.Change, error) {
	return func(fs.FS) ([]gitrepo.Change, error) {
		var cs []gitrepo.Change
		for p, c := range files {
			cs = append(cs, gitrepo.Change{Path: p, Data: []byte(c)})
		}
		return cs, nil
	}
}

// created returns a store with the fixture catalog and the workspace
// fixture.WorkspaceID, and the workspace's main.
func created(t *testing.T) (*Store, string) {
	t.Helper()
	s, _ := open(t)
	if err := s.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	return s, mainOf(t, s, mainRef)
}

func mainOf(t *testing.T, s *Store, ref string) string {
	t.Helper()
	repo, err := s.WorkspaceRepo(fixture.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	oid, _, err := repo.ResolveRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func TestUpdateWorkspaceMain(t *testing.T) {
	s, first := created(t)
	commit, err := s.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "Answer TaskA", put(map[string]string{answerA: answerFile("1.1.0")}))
	if err != nil {
		t.Fatal(err)
	}
	if commit == first || mainOf(t, s, mainRef) != commit {
		t.Fatalf("main %s, commit %s, first %s", mainOf(t, s, mainRef), commit, first)
	}
	repo, _ := s.WorkspaceRepo(fixture.WorkspaceID)
	if data, ok, _ := repo.ReadFile(commit, answerA); !ok || string(data) != answerFile("1.1.0") {
		t.Errorf("answer %q", data)
	}
	// The same content again makes no commit.
	again, err := s.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "again", put(map[string]string{answerA: answerFile("1.1.0")}))
	if err != nil || again != commit {
		t.Errorf("no-op update: %s %v, want %s", again, err, commit)
	}
	// The edit sees the current tree.
	_, err = s.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "check", func(tree fs.FS) ([]gitrepo.Change, error) {
		if _, err := fs.Stat(tree, answerA); err != nil {
			t.Errorf("edit does not see the answer: %v", err)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpdateWorkspaceRejected(t *testing.T) {
	s, first := created(t)
	_, err := s.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "bad", put(map[string]string{answerA: answerFile("9.0.0")}))
	var rej *RejectedError
	if !errors.As(err, &rej) {
		t.Fatalf("err = %v", err)
	}
	fixture.WantProblem(t, rej.Problems, answerA, problem.RuleAnswer, "has no version 9.0.0")
	if mainOf(t, s, mainRef) != first {
		t.Error("main moved")
	}
}

func TestUpdateWorkspaceBranch(t *testing.T) {
	s, first := created(t)
	const draft = "refs/heads/what-if"
	// Branches are not validated, and start at main.
	commit, err := s.UpdateWorkspace(fixture.WorkspaceID, draft, jane, "draft", put(map[string]string{"answers/notes.txt": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	if mainOf(t, s, draft) != commit || mainOf(t, s, mainRef) != first {
		t.Errorf("draft %s main %s", mainOf(t, s, draft), mainOf(t, s, mainRef))
	}
	repo, _ := s.WorkspaceRepo(fixture.WorkspaceID)
	if parent, _, _ := repo.ResolveRef(commit + "^"); parent != first {
		t.Errorf("parent %s, want %s", parent, first)
	}
	if oid, err := s.UpdateWorkspace(fixture.WorkspaceID, "refs/heads/empty", jane, "nothing", put(nil)); oid != "" || err != nil {
		t.Errorf("no-op on a new branch: %q %v", oid, err)
	}
	if _, ok, _ := repo.ResolveRef("refs/heads/empty"); ok {
		t.Error("a no-op created a branch")
	}
	if _, err := s.UpdateWorkspace(fixture.WorkspaceID, "refs/tags/v1", jane, "m", put(nil)); err == nil {
		t.Error("a tag must be rejected")
	}
}

// TestUpdateWorkspaceConflict moves main while the edit runs, as a push
// would, and expects the write to fail instead of overwriting the push.
func TestUpdateWorkspaceConflict(t *testing.T) {
	s, first := created(t)
	repo, _ := s.WorkspaceRepo(fixture.WorkspaceID)
	pushed, err := repo.WriteCommit(gitrepo.CommitRequest{Base: first, Parents: []string{first}, Author: jane, Message: "pushed",
		Changes: []gitrepo.Change{{Path: "README.md", Data: []byte("pushed")}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "answer", func(fs.FS) ([]gitrepo.Change, error) {
		if err := repo.UpdateRef(mainRef, pushed, first); err != nil {
			t.Fatal(err)
		}
		return put(map[string]string{answerA: answerFile("1.1.0")})(nil)
	})
	if !errors.Is(err, ErrConflict) || !errors.Is(err, gitrepo.ErrRefMoved) {
		t.Errorf("err = %v", err)
	}
	if mainOf(t, s, mainRef) != pushed {
		t.Error("the push was overwritten")
	}
}

func TestUpdateWorkspaceErrors(t *testing.T) {
	s, _ := created(t)
	boom := errors.New("boom")
	if _, err := s.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "m", func(fs.FS) ([]gitrepo.Change, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Errorf("edit error: %v", err)
	}
	if _, err := s.UpdateWorkspace("0f0e0d0c-0b0a-4908-8706-050403020100", mainRef, jane, "m", put(nil)); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown workspace: %v", err)
	}
}

// TestUpdateWorkspaceConcurrent runs writes in parallel; the lock must
// serialise them so that none is lost or fails.
func TestUpdateWorkspaceConcurrent(t *testing.T) {
	s, _ := created(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := fmt.Sprintf("notes/%d.txt", i)
			if _, err := s.UpdateWorkspace(fixture.WorkspaceID, "refs/heads/draft", jane, path, put(map[string]string{path: "x"})); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	repo, _ := s.WorkspaceRepo(fixture.WorkspaceID)
	tree, err := repo.TreeFS("refs/heads/draft")
	if err != nil {
		t.Fatal(err)
	}
	if entries, _ := fs.ReadDir(tree, "notes"); len(entries) != 8 {
		t.Errorf("%d notes, want 8", len(entries))
	}
}

func TestSetRef(t *testing.T) {
	s, first := created(t)
	repo, _ := s.WorkspaceRepo(fixture.WorkspaceID)
	commit := func(base, path, content string) string {
		c, err := repo.WriteCommit(gitrepo.CommitRequest{Base: base, Parents: []string{base}, Author: jane, Message: path,
			Changes: []gitrepo.Change{{Path: path, Data: []byte(content)}}})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	next := commit(first, answerA, answerFile("1.1.0"))
	if err := s.SetRef(fixture.WorkspaceID, mainRef, next, first); err != nil {
		t.Fatal(err)
	}
	var rej *RejectedError
	if err := s.SetRef(fixture.WorkspaceID, mainRef, first, next); !errors.As(err, &rej) || rej.Problems[0].Rule != problem.RuleHistory {
		t.Errorf("rewind: %v", err)
	}
	bad := commit(next, answerA, answerFile("9.0.0"))
	if err := s.SetRef(fixture.WorkspaceID, mainRef, bad, next); !errors.As(err, &rej) {
		t.Errorf("invalid content: %v", err)
	}
	ok := commit(next, "README.md", "x")
	if err := s.SetRef(fixture.WorkspaceID, mainRef, ok, first); !errors.Is(err, ErrConflict) {
		t.Errorf("stale old value: %v", err)
	}
	if err := s.SetRef(fixture.WorkspaceID, "refs/heads/draft", bad, ""); err != nil {
		t.Errorf("new branch: %v", err)
	}
	if err := s.SetRef(fixture.WorkspaceID, "refs/heads/draft", bad, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("branch exists: %v", err)
	}
}

func TestLoadAndStatus(t *testing.T) {
	s, _ := created(t)
	pin := mainOf(t, s, mainRef)
	if _, err := s.UpdateWorkspace(fixture.WorkspaceID, mainRef, jane, "answer", put(map[string]string{answerA: answerFile("1.1.0")})); err != nil {
		t.Fatal(err)
	}
	w, c, err := s.Load(fixture.WorkspaceID, "")
	if err != nil {
		t.Fatal(err)
	}
	if w.Config.Workspace != fixture.WorkspaceID || w.Answers[answerA] == nil || !c.Tasks.Has(fixture.TaskA) {
		t.Errorf("loaded %+v", w.Config)
	}
	if w, _, err := s.Load(fixture.WorkspaceID, pin); err != nil || w.Answers[answerA] != nil {
		t.Errorf("load at the first commit: %v", err)
	}
	if _, _, err := s.Load(fixture.WorkspaceID, "refs/heads/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing branch: %v", err)
	}
	st, err := s.Status(fixture.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Workspace != fixture.WorkspaceID || len(st.Tasks) != 2 || st.Tasks[0].State != status.Answered || st.Tasks[1].State != status.Unanswered {
		t.Errorf("status %+v", st)
	}
}

// TestCheckWorkspace finds a main that was changed behind custos's back.
func TestCheckWorkspace(t *testing.T) {
	s, first := created(t)
	if ps, err := s.CheckWorkspace(fixture.WorkspaceID); err != nil || len(ps) != 0 {
		t.Fatalf("fresh workspace: %v %v", ps, err)
	}
	repo, _ := s.WorkspaceRepo(fixture.WorkspaceID)
	bad, err := repo.WriteCommit(gitrepo.CommitRequest{Base: first, Parents: []string{first}, Author: jane, Message: "on disk",
		Changes: []gitrepo.Change{{Path: answerA, Data: []byte(answerFile("9.0.0"))}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRef(mainRef, bad, first); err != nil {
		t.Fatal(err)
	}
	ps, err := s.CheckWorkspace(fixture.WorkspaceID)
	if err != nil || len(ps) != 1 || !strings.Contains(ps[0].Message, "9.0.0") {
		t.Errorf("%v %v", ps, err)
	}
	if _, err := s.Status(fixture.WorkspaceID); err != nil {
		t.Errorf("status of an inconsistent workspace: %v", err)
	}
}
