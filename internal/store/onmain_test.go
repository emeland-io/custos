package store

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
)

// moves records the ids OnMainMoved reports.
type moves struct {
	mu  sync.Mutex
	ids []string
}

func (m *moves) add(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ids = append(m.ids, id)
}

func (m *moves) take() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := m.ids
	m.ids = nil
	return ids
}

func TestOnMainMoved(t *testing.T) {
	s, _ := open(t)
	var m moves
	s.OnMainMoved(m.add)
	id := fixture.WorkspaceID

	if err := s.CreateWorkspace(id, jane); err != nil {
		t.Fatal(err)
	}
	if got := m.take(); !slices.Equal(got, []string{id}) {
		t.Errorf("CreateWorkspace: %v", got)
	}

	first := mainOf(t, s, mainRef)
	commit, err := s.UpdateWorkspace(id, mainRef, jane, "answer", put(map[string]string{answerA: answerFile("1.1.0")}))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.take(); !slices.Equal(got, []string{id}) {
		t.Errorf("UpdateWorkspace on main: %v", got)
	}

	// No change, another branch, a rejected write: no call.
	if _, err := s.UpdateWorkspace(id, mainRef, jane, "again", put(map[string]string{answerA: answerFile("1.1.0")})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateWorkspace(id, "refs/heads/draft", jane, "draft", put(map[string]string{answerA: answerFile("1.0.0")})); err != nil {
		t.Fatal(err)
	}
	var rejected *RejectedError
	if _, err := s.UpdateWorkspace(id, mainRef, jane, "bad", put(map[string]string{answerA: "garbage"})); !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a rejection", err)
	}
	if got := m.take(); len(got) != 0 {
		t.Errorf("unexpected calls %v", got)
	}

	// SetRef on main calls, a lost compare-and-swap does not.
	repo, _ := s.WorkspaceRepo(id)
	next, err := repo.WriteCommit(gitrepo.CommitRequest{Base: commit, Parents: []string{commit}, Author: jane, Message: "next",
		Changes: []gitrepo.Change{{Path: answerA, Data: []byte(answerFile("1.0.0"))}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRef(id, mainRef, next, first); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale SetRef: %v", err)
	}
	if got := m.take(); len(got) != 0 {
		t.Errorf("lost compare-and-swap called %v", got)
	}
	if err := s.SetRef(id, mainRef, next, commit); err != nil {
		t.Fatal(err)
	}
	if got := m.take(); !slices.Equal(got, []string{id}) {
		t.Errorf("SetRef on main: %v", got)
	}
}

// TestOnMainMovedOutsideLock writes from inside the callback; if the
// callback ran under the workspace's lock, this would deadlock.
func TestOnMainMovedOutsideLock(t *testing.T) {
	s, _ := created(t)
	id := fixture.WorkspaceID
	var once sync.Once
	s.OnMainMoved(func(got string) {
		once.Do(func() {
			if _, err := s.UpdateWorkspace(got, "refs/heads/notes", gitrepo.Bot, "note", put(map[string]string{"notes/x.txt": "x"})); err != nil {
				t.Error(err)
			}
		})
	})
	if _, err := s.UpdateWorkspace(id, mainRef, jane, "answer", put(map[string]string{answerA: answerFile("1.1.0")})); err != nil {
		t.Fatal(err)
	}
	if mainOf(t, s, "refs/heads/notes") == "" {
		t.Error("the callback's write is missing")
	}
}
