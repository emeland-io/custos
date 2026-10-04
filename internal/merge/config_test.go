package merge

import (
	"errors"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/store"
)

// Valid catalog commits on top of fixture.Catalog().
var (
	catalogStep2 = map[string]string{fixture.TaskPath(fixture.TaskB, "1.1.0"): fixture.TaskFile(fixture.TaskB, "1.1.0", fixture.TaskB+"@1.0.0")}
	catalogStep3 = map[string]string{fixture.TaskPath(fixture.TaskA, "1.2.0"): fixture.TaskFile(fixture.TaskA, "1.2.0", fixture.TaskA+"@1.1.0")}
)

func TestMergePinMovesToDescendant(t *testing.T) {
	for _, tc := range []struct {
		name               string
		mainPin, branchPin int // index into e.cat
	}{{"branch newer", 1, 2}, {"main newer", 2, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			e.advanceCatalog(t, catalogStep2)
			e.advanceCatalog(t, catalogStep3)
			commitFiles(t, e.st, ws, whatIf, map[string]string{configPath: e.config(ws, e.cat[tc.branchPin], false)})
			commitFiles(t, e.st, ws, mainRef, map[string]string{configPath: e.config(ws, e.cat[tc.mainPin], false)})

			res, err := Merge(e.st, ws, "what-if", alice, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Conflicts) != 0 {
				t.Fatalf("conflicts %+v, want the pin to move to the descendant", res.Conflicts)
			}
			if cfg := readConfig(t, e, ws, res.Commit); cfg.Catalog.Commit != e.cat[2] || cfg.Workspace != ws {
				t.Errorf("custos.yaml = %+v, want pin %s", cfg, e.cat[2])
			}
		})
	}
}

func TestMergePinWithoutCommonLineIsAConflict(t *testing.T) {
	e := setup(t)
	draft := e.draftCatalog(t, e.cat[0], map[string]string{
		fixture.TaskPath(fixture.TaskB, "2.0.0"): fixture.TaskFile(fixture.TaskB, "2.0.0", fixture.TaskB+"@1.0.0"),
	})
	c2 := e.advanceCatalog(t, catalogStep2)
	ours, theirs := e.config(ws, c2, false), e.config(ws, draft, false)
	commitFiles(t, e.st, ws, whatIf, map[string]string{configPath: theirs})
	before := commitFiles(t, e.st, ws, mainRef, map[string]string{configPath: ours})

	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("conflicts %+v, want one on custos.yaml", res.Conflicts)
	}
	if c := res.Conflicts[0]; c.Path != configPath || c.Kind != KindConfig || string(c.Ours) != ours || string(c.Theirs) != theirs {
		t.Errorf("conflict = %s %s ours %q theirs %q", c.Path, c.Kind, c.Ours, c.Theirs)
	}
	if got := ref(t, e.st, ws, mainRef); got != before {
		t.Errorf("main moved to %s", got)
	}

	// The draft commit is not on the catalog's main, so taking it is rejected.
	_, err = Merge(e.st, ws, "what-if", alice, map[string]Resolution{configPath: {Side: SideTheirs}})
	var rej *store.RejectedError
	if !errors.As(err, &rej) || !hasRule(rej.Problems, problem.RulePin) {
		t.Fatalf("choosing the draft pin: %v, want rejection with rule %s", err, problem.RulePin)
	}
	res, err = Merge(e.st, ws, "what-if", alice, map[string]Resolution{configPath: {Side: SideOurs}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg := readConfig(t, e, ws, res.Commit); cfg.Catalog.Commit != c2 {
		t.Errorf("pin = %s, want %s", cfg.Catalog.Commit, c2)
	}
}

// TestMergeResolvedConfigConflictKeepsMainWorkspaceID checks that resolving
// a textual custos.yaml conflict with side theirs keeps main's workspace id
// even though the branch's file (as a fork's would) names another
// workspace: the README promises main keeps its id, and taking the
// branch's file whole must not relitigate that through rule workspace-id.
func TestMergeResolvedConfigConflictKeepsMainWorkspaceID(t *testing.T) {
	e := setup(t)
	repo := repoOf(t, e.st, ws)
	old := ref(t, e.st, ws, mainRef)
	c2 := e.advanceCatalog(t, catalogStep2)
	c3 := e.advanceCatalog(t, catalogStep3)

	// A historical commit whose custos.yaml has a since-removed field (a
	// manual edit on disk, as in TestForkOfInvalidMainLeavesNothingBehind):
	// this is the merge base, and merge's own field-by-field config merge
	// cannot read it, so git's own line-level merge of custos.yaml decides
	// whether there is a conflict.
	base, err := repo.WriteCommit(gitrepo.CommitRequest{
		Base: old, Parents: []string{old},
		Changes: []gitrepo.Change{{Path: configPath, Data: []byte(e.config(ws, e.cat[0], false) + "weird: true\n")}},
		Author:  alice, Message: "a historical commit with a since-removed field",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRef(mainRef, base, old); err != nil {
		t.Fatal(err)
	}

	// main moves the pin to c2 (dropping the unknown field); this changes
	// the same "commit:" line the branch below changes, so git cannot
	// auto-merge custos.yaml.
	ours, err := repo.WriteCommit(gitrepo.CommitRequest{
		Base: base, Parents: []string{base},
		Changes: []gitrepo.Change{{Path: configPath, Data: []byte(e.config(ws, c2, false))}},
		Author:  alice, Message: "move the pin and drop the unknown field",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRef(mainRef, ours, base); err != nil {
		t.Fatal(err)
	}

	// The branch is a fork's: its custos.yaml names forkID and moves the
	// pin to c3, as a git push of such a branch would bring it.
	theirs, err := repo.WriteCommit(gitrepo.CommitRequest{
		Base: base, Parents: []string{base},
		Changes: []gitrepo.Change{{Path: configPath, Data: []byte(e.config(forkID, c3, false))}},
		Author:  alice, Message: "the fork's branch",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRef("refs/heads/from-fork", theirs, ""); err != nil {
		t.Fatal(err)
	}

	res, err := Merge(e.st, ws, "from-fork", alice, map[string]Resolution{configPath: {Side: SideTheirs}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("conflicts %+v, want the custos.yaml conflict resolved by side theirs", res.Conflicts)
	}
	if cfg := readConfig(t, e, ws, res.Commit); cfg.Workspace != ws || cfg.Catalog.Commit != c3 {
		t.Errorf("custos.yaml = %+v, want main's workspace %s pinned to the fork's %s", cfg, ws, c3)
	}
}

func TestMergeKeepsMainWorkspaceID(t *testing.T) {
	e := setup(t)
	if err := Fork(e.st, ws, forkID, alice); err != nil {
		t.Fatal(err)
	}
	fromFork := textAnswer(fixture.TaskB, "1.0.0", "answered in the fork")
	commitFiles(t, e.st, forkID, mainRef, map[string]string{answerB: fromFork})
	// The fork's main arrives as a branch, as a git push of it would bring it.
	if _, err := repoOf(t, e.st, ws).Fetch(repoOf(t, e.st, forkID), mainRef, "refs/heads/from-fork"); err != nil {
		t.Fatal(err)
	}

	res, err := Merge(e.st, ws, "from-fork", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
	if cfg := readConfig(t, e, ws, res.Commit); cfg.Workspace != ws {
		t.Errorf("workspace id after merging the fork = %s, want %s", cfg.Workspace, ws)
	}
	if got, _ := file(t, e.st, ws, res.Commit, answerB); got != fromFork {
		t.Errorf("answer from the fork = %q", got)
	}
}

func TestMergeConfigFieldByField(t *testing.T) {
	e := setup(t)
	c2 := e.advanceCatalog(t, catalogStep2)
	commitFiles(t, e.st, ws, whatIf, map[string]string{configPath: e.config(ws, e.cat[0], true)}) // branch freezes
	commitFiles(t, e.st, ws, mainRef, map[string]string{configPath: e.config(ws, c2, false)})     // main moves the pin

	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
	if cfg := readConfig(t, e, ws, res.Commit); !cfg.Frozen || cfg.Catalog.Commit != c2 || cfg.Workspace != ws {
		t.Errorf("custos.yaml = %+v, want frozen and pinned to %s", cfg, c2)
	}
}

func TestMergeUnreadableConfigIsAConflict(t *testing.T) {
	e := setup(t)
	c2 := e.advanceCatalog(t, catalogStep2)
	commitFiles(t, e.st, ws, whatIf, map[string]string{configPath: "workspace: [\n"})
	before := commitFiles(t, e.st, ws, mainRef, map[string]string{configPath: e.config(ws, c2, false)})

	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Kind != KindConfig || string(res.Conflicts[0].Theirs) != "workspace: [\n" {
		t.Fatalf("conflicts %+v, want one on custos.yaml", res.Conflicts)
	}
	if got := ref(t, e.st, ws, mainRef); got != before {
		t.Errorf("main moved to %s", got)
	}
}
