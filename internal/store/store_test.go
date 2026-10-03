package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/problem"
)

const publicURL = "https://custos.example.org"

var jane = gitrepo.Signature{Name: "Jane Doe", Email: "jane@example.org"}

// seedCatalog commits files to the catalog's main without running hooks and
// returns the commit.
func seedCatalog(t *testing.T, s *Store, files map[string]string) string {
	t.Helper()
	cat := s.CatalogRepo()
	old, _, err := cat.ResolveRef(mainRef)
	if err != nil {
		t.Fatal(err)
	}
	var changes []gitrepo.Change
	for p, c := range files {
		changes = append(changes, gitrepo.Change{Path: p, Data: []byte(c)})
	}
	req := gitrepo.CommitRequest{Base: old, Changes: changes, Author: jane, Message: "catalog"}
	if old != "" {
		req.Parents = []string{old}
	}
	commit, err := cat.WriteCommit(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.UpdateRef(mainRef, commit, old); err != nil {
		t.Fatal(err)
	}
	return commit
}

// open returns a store with the fixture catalog on main.
func open(t *testing.T) (*Store, string) {
	t.Helper()
	s, err := Open(t.TempDir(), "/custos", publicURL)
	if err != nil {
		t.Fatal(err)
	}
	return s, seedCatalog(t, s, fixture.Catalog())
}

func hookOf(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "hooks", "pre-receive"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestOpen(t *testing.T) {
	data := t.TempDir()
	s, err := Open(data, "/custos", publicURL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if s.DataDir() != data {
		t.Errorf("DataDir = %s", s.DataDir())
	}
	if s.CatalogRepo().Dir != filepath.Join(data, "repos", "catalog.git") {
		t.Errorf("catalog at %s", s.CatalogRepo().Dir)
	}
	if got := gittest.Run(t, s.CatalogRepo().Dir, "config", "http.receivepack"); got != "true" {
		t.Errorf("http.receivepack = %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.CatalogRepo().Dir, "hooks", "pre-receive")); err == nil {
		t.Error("Open must not install hooks")
	}
	if s.CatalogURL() != publicURL+"/git/catalog.git" {
		t.Errorf("CatalogURL = %s", s.CatalogURL())
	}
	if ids, err := s.WorkspaceIDs(); err != nil || len(ids) != 0 {
		t.Errorf("ids %v %v", ids, err)
	}
}

func TestNewMakesDataDirAbsolute(t *testing.T) {
	t.Chdir(t.TempDir())
	s := New("data", "/custos", publicURL)
	if !filepath.IsAbs(s.DataDir()) {
		t.Errorf("DataDir = %s", s.DataDir())
	}
}

func TestCreateWorkspace(t *testing.T) {
	s, pin := open(t)
	if err := s.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	repo, err := s.WorkspaceRepo(fixture.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	data, ok, err := repo.ReadFile(mainRef, "custos.yaml")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	want := "workspace: " + fixture.WorkspaceID + "\ncatalog:\n  url: " + publicURL + "/git/catalog.git\n  commit: " + pin + "\n"
	if string(data) != want {
		t.Errorf("custos.yaml:\n%s\nwant:\n%s", data, want)
	}
	if who := gittest.Run(t, repo.Dir, "log", "-1", "--format=%an <%ae>|%cn <%ce>|%s", "main"); who != "Jane Doe <jane@example.org>|custos-bot <custos-bot@localhost>|Create workspace "+fixture.WorkspaceID {
		t.Errorf("commit %q", who)
	}
	if want := "'/custos' hook pre-receive --kind workspace --catalog '" + s.CatalogRepo().Dir + "' --workspace " + fixture.WorkspaceID; !strings.Contains(hookOf(t, repo.Dir), want) {
		t.Errorf("hook %q", hookOf(t, repo.Dir))
	}
	if ids, err := s.WorkspaceIDs(); err != nil || !slices.Equal(ids, []string{fixture.WorkspaceID}) {
		t.Errorf("ids %v %v", ids, err)
	}
	if err := s.CreateWorkspace(fixture.WorkspaceID, jane); !errors.Is(err, ErrExists) {
		t.Errorf("second create: %v", err)
	}
}

func TestCreateWorkspaceInvalidID(t *testing.T) {
	s, _ := open(t)
	for _, id := range []string{"../escape", "ws-1", strings.ToUpper(fixture.WorkspaceID)} {
		err := s.CreateWorkspace(id, jane)
		var rej *RejectedError
		if !errors.As(err, &rej) || rej.Problems[0].Rule != problem.RuleWorkspaceID {
			t.Errorf("%q: %v", id, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(s.DataDir(), "repos", "workspaces"))
	if len(entries) != 0 {
		t.Errorf("left %v behind", entries)
	}
}

func TestCreateWorkspaceNeedsCatalog(t *testing.T) {
	s, err := Open(t.TempDir(), "/custos", publicURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkspace(fixture.WorkspaceID, jane); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "push the catalog first") {
		t.Errorf("empty catalog: %v", err)
	}
	if ids, _ := s.WorkspaceIDs(); len(ids) != 0 {
		t.Errorf("ids %v", ids)
	}
	// A data directory that was never opened has no catalog at all.
	if err := New(t.TempDir(), "/custos", publicURL).CreateWorkspace(fixture.WorkspaceID, jane); !errors.Is(err, ErrConflict) {
		t.Errorf("no catalog: %v", err)
	}
}

func TestCreateWorkspaceRepo(t *testing.T) {
	s := New(t.TempDir(), "/custos", publicURL)
	repo, err := s.CreateWorkspaceRepo(fixture.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if refs, err := repo.Refs(""); err != nil || len(refs) != 0 {
		t.Errorf("refs %v %v", refs, err)
	}
	if !strings.Contains(hookOf(t, repo.Dir), "--kind workspace") {
		t.Error("hook missing")
	}
	if _, err := s.CreateWorkspaceRepo(fixture.WorkspaceID); !errors.Is(err, ErrExists) {
		t.Errorf("second create: %v", err)
	}
}

func TestWorkspaceRepoNotFound(t *testing.T) {
	s, _ := open(t)
	for _, id := range []string{fixture.WorkspaceID, "../catalog", ""} {
		if _, err := s.WorkspaceRepo(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v", id, err)
		}
	}
}

func TestWorkspaceIDsIgnoresOtherDirectories(t *testing.T) {
	s, _ := open(t)
	if err := s.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(s.DataDir(), "repos", "workspaces")
	os.Mkdir(filepath.Join(ws, "notes"), 0o755)
	os.Mkdir(filepath.Join(ws, "scratch.git"), 0o755)
	os.WriteFile(filepath.Join(ws, "0f0e0d0c-0b0a-4908-8706-050403020100.git"), nil, 0o644)
	if ids, err := s.WorkspaceIDs(); err != nil || !slices.Equal(ids, []string{fixture.WorkspaceID}) {
		t.Errorf("ids %v %v", ids, err)
	}
}

func TestInstallHooks(t *testing.T) {
	data := t.TempDir()
	first, err := Open(data, "/first/custos", publicURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.InstallHooks(); err != nil {
		t.Fatal(err)
	}
	seedCatalog(t, first, fixture.Catalog())
	// workspace create runs from another binary path and leaves the other
	// hooks alone (ruling 1.8).
	if err := New(data, "/second/custos", publicURL).CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	catDir := first.CatalogRepo().Dir
	wsDir := filepath.Join(data, "repos", "workspaces", fixture.WorkspaceID+".git")
	if !strings.Contains(hookOf(t, catDir), "'/first/custos'") || !strings.Contains(hookOf(t, wsDir), "'/second/custos'") {
		t.Errorf("hooks %q %q", hookOf(t, catDir), hookOf(t, wsDir))
	}
	third, err := Open(data, "/third/custos", publicURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.InstallHooks(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hookOf(t, catDir), "'/third/custos' hook pre-receive --kind catalog") ||
		!strings.Contains(hookOf(t, wsDir), "'/third/custos' hook pre-receive --kind workspace") {
		t.Errorf("hooks %q %q", hookOf(t, catDir), hookOf(t, wsDir))
	}
}

func TestLock(t *testing.T) {
	s := New(t.TempDir(), "/custos", publicURL)
	unlock := s.Lock("a")
	got := make(chan bool)
	go func() {
		u := s.Lock("a")
		got <- true
		u()
	}()
	s.Lock("b")() // another repository is not blocked
	select {
	case <-got:
		t.Fatal("second Lock of a returned while a was locked")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("Lock of a did not return after unlock")
	}
}

func TestRejectedError(t *testing.T) {
	err := &RejectedError{Problems: []problem.Problem{
		{Path: "a.md", Rule: problem.RuleAnswer, Message: "one"},
		{Rule: problem.RuleHistory, Message: "two"},
	}}
	if err.Error() != "rejected: a.md: answer: one; history: two" {
		t.Errorf("%q", err.Error())
	}
}
