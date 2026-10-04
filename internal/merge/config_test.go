package merge

import (
	"errors"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
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
