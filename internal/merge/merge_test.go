package merge

import (
	"errors"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/store"
)

// diverge commits base to main (unless nil), then theirs to branch what-if
// (which starts at main) and ours to main. It returns main before the merge.
func diverge(t *testing.T, e *env, base, ours, theirs map[string]string) string {
	t.Helper()
	if base != nil {
		commitFiles(t, e.st, ws, mainRef, base)
	}
	commitFiles(t, e.st, ws, whatIf, theirs)
	return commitFiles(t, e.st, ws, mainRef, ours)
}

func TestMergeClean(t *testing.T) {
	e := setup(t)
	ours, theirs := textAnswer(fixture.TaskA, "1.1.0", "ours"), textAnswer(fixture.TaskB, "1.0.0", "theirs")
	before := diverge(t, e, nil, map[string]string{answerA: ours}, map[string]string{answerB: theirs})
	branch := ref(t, e.st, ws, whatIf)

	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 || res.Commit == "" {
		t.Fatalf("result %+v, want a merge commit", res)
	}
	if got := ref(t, e.st, ws, mainRef); got != res.Commit {
		t.Errorf("main = %s, want the merge commit %s", got, res.Commit)
	}
	if got := gitOut(t, e.st, ws, "rev-parse", res.Commit+"^1", res.Commit+"^2"); got != before+"\n"+branch {
		t.Errorf("parents = %q, want main then branch", got)
	}
	if got := gitOut(t, e.st, ws, "log", "-1", "--format=%an <%ae>|%cn|%s", res.Commit); got != "Alice Example <alice@example.org>|custos-bot|Merge branch 'what-if'" {
		t.Errorf("author|committer|subject = %q", got)
	}
	for path, want := range map[string]string{answerA: ours, answerB: theirs} {
		if got, ok := file(t, e.st, ws, res.Commit, path); !ok || got != want {
			t.Errorf("%s = %q (present %v), want %q", path, got, ok, want)
		}
	}
}

func TestMergeBranchAheadAndUpToDate(t *testing.T) {
	e := setup(t)
	commitFiles(t, e.st, ws, whatIf, map[string]string{answerB: textAnswer(fixture.TaskB, "1.0.0", "theirs")})

	first, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if parents := strings.Fields(gitOut(t, e.st, ws, "rev-list", "--parents", "-n", "1", first.Commit)); len(parents) != 3 {
		t.Errorf("a branch ahead of main gets a merge commit with two parents; got %v", parents)
	}
	second, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Commit != first.Commit || ref(t, e.st, ws, mainRef) != first.Commit {
		t.Errorf("merging a merged branch again = %+v, want main unchanged at %s", second, first.Commit)
	}
}

func TestMergeAnswerConflict(t *testing.T) {
	e := setup(t)
	ours, theirs := textAnswer(fixture.TaskA, "1.1.0", "ours"), textAnswer(fixture.TaskA, "1.1.0", "theirs")
	before := diverge(t, e,
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "base")},
		map[string]string{answerA: ours},
		map[string]string{answerA: theirs})

	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != "" || len(res.Conflicts) != 1 {
		t.Fatalf("result %+v, want one conflict and no commit", res)
	}
	c := res.Conflicts[0]
	if c.Path != answerA || c.Kind != KindAnswer || string(c.Ours) != ours || string(c.Theirs) != theirs {
		t.Errorf("conflict = %s %s ours %q theirs %q", c.Path, c.Kind, c.Ours, c.Theirs)
	}
	if got := ref(t, e.st, ws, mainRef); got != before {
		t.Errorf("main moved to %s despite conflicts", got)
	}
}

func TestMergeResolutions(t *testing.T) {
	ours, theirs := textAnswer(fixture.TaskA, "1.1.0", "ours"), textAnswer(fixture.TaskA, "1.1.0", "theirs")
	resolved := textAnswer(fixture.TaskA, "1.1.0", "both")
	for _, tc := range []struct {
		name string
		res  Resolution
		want string
	}{
		{"ours", Resolution{Side: SideOurs}, ours},
		{"theirs", Resolution{Side: SideTheirs}, theirs},
		{"content", Resolution{Side: SideContent, Content: []byte(resolved)}, resolved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			diverge(t, e,
				map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "base")},
				map[string]string{answerA: ours},
				map[string]string{answerA: theirs})
			res, err := Merge(e.st, ws, "what-if", alice, map[string]Resolution{answerA: tc.res})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Conflicts) != 0 || ref(t, e.st, ws, mainRef) != res.Commit {
				t.Fatalf("result %+v, want merged", res)
			}
			if got, _ := file(t, e.st, ws, res.Commit, answerA); got != tc.want {
				t.Errorf("answer = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMergeModifyDeleteConflict(t *testing.T) {
	e := setup(t)
	theirs := textAnswer(fixture.TaskA, "1.1.0", "theirs")
	diverge(t, e,
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "base")},
		map[string]string{answerA: ""}, // main deletes the answer
		map[string]string{answerA: theirs})

	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Ours != nil || string(res.Conflicts[0].Theirs) != theirs {
		t.Fatalf("result %+v, want one conflict with no file on main", res)
	}
	res, err = Merge(e.st, ws, "what-if", alice, map[string]Resolution{answerA: {Side: SideOurs}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := file(t, e.st, ws, res.Commit, answerA); ok {
		t.Error("choosing the deleting side must delete the answer")
	}
}

func TestMergeGeneratedAndDocumentConflicts(t *testing.T) {
	// §4.4, replacing ruling 2.10: main's side is kept and the producing
	// task is reported for a rerun.
	e := setup(t)
	gen := "generated/" + fixture.TaskC + "/1.0.0.md"
	doc := "documents/" + fixture.DocID + ".json"
	genOn := func(side string) string {
		return strings.Replace(fixture.GeneratedFile, "title: Host web-01", "title: Host web-01 ("+side+")", 1)
	}
	docOn := func(side string) string {
		return strings.Replace(fixture.DocumentFile, `"name":"provenance"`, `"name":"provenance-`+side+`"`, 1)
	}
	diverge(t, e,
		map[string]string{gen: fixture.GeneratedFile, doc: fixture.DocumentFile},
		map[string]string{gen: genOn("main"), doc: docOn("main")},
		map[string]string{gen: genOn("branch"), doc: docOn("branch")})

	if _, err := Merge(e.st, ws, "what-if", alice, map[string]Resolution{gen: {Side: SideTheirs}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a resolution for generated output must be refused, got %v", err)
	}
	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 || res.Commit == "" {
		t.Fatalf("result %+v, want a merge without open conflicts", res)
	}
	if len(res.Rerun) != 1 || res.Rerun[0] != fixture.TaskB {
		t.Errorf("rerun %v, want the producing task %s", res.Rerun, fixture.TaskB)
	}
	if got, _ := file(t, e.st, ws, res.Commit, gen); got != genOn("main") {
		t.Errorf("generated task = %q, want main's", got)
	}
	if got, _ := file(t, e.st, ws, res.Commit, doc); got != docOn("main") {
		t.Errorf("document = %q, want main's", got)
	}
}

// TestMergeGeneratedDeletedOnMain keeps main's deletion of a generated
// file the branch changed, and still reruns its producer.
func TestMergeGeneratedDeletedOnMain(t *testing.T) {
	e := setup(t)
	doc := "documents/" + fixture.DocID + ".json"
	diverge(t, e,
		map[string]string{doc: fixture.DocumentFile},
		map[string]string{doc: ""},
		map[string]string{doc: strings.Replace(fixture.DocumentFile, `"name":"provenance"`, `"name":"changed"`, 1)})
	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := file(t, e.st, ws, res.Commit, doc); ok {
		t.Error("main deleted the document; the merge must keep it deleted")
	}
	if len(res.Rerun) != 1 || res.Rerun[0] != fixture.TaskB {
		t.Errorf("rerun %v", res.Rerun)
	}
}

// TestMergeAnswerConflictWithGeneratedOutput reports only the answer as
// a conflict and the rerun only once the merge is made.
func TestMergeAnswerConflictWithGeneratedOutput(t *testing.T) {
	e := setup(t)
	doc := "documents/" + fixture.DocID + ".json"
	docOn := func(side string) string {
		return strings.Replace(fixture.DocumentFile, `"name":"provenance"`, `"name":"provenance-`+side+`"`, 1)
	}
	b := func(v string) string { return textAnswer(fixture.TaskB, "1.0.0", v) }
	diverge(t, e,
		map[string]string{doc: fixture.DocumentFile, answerB: b("base")},
		map[string]string{doc: docOn("main"), answerB: b("main")},
		map[string]string{doc: docOn("branch"), answerB: b("branch")})
	res, err := Merge(e.st, ws, "what-if", alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Path != answerB || res.Rerun != nil || res.Commit != "" {
		t.Fatalf("result %+v, want only the answer conflict", res)
	}
	res, err = Merge(e.st, ws, "what-if", alice, map[string]Resolution{answerB: {Side: SideTheirs}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit == "" || len(res.Rerun) != 1 || res.Rerun[0] != fixture.TaskB {
		t.Errorf("result %+v", res)
	}
}

func TestMergePartialResolution(t *testing.T) {
	e := setup(t)
	a := func(v string) string { return textAnswer(fixture.TaskA, "1.1.0", v) }
	b := func(v string) string { return textAnswer(fixture.TaskB, "1.0.0", v) }
	before := diverge(t, e,
		map[string]string{answerA: a("base"), answerB: b("base")},
		map[string]string{answerA: a("ours"), answerB: b("ours")},
		map[string]string{answerA: a("theirs"), answerB: b("theirs")})

	res, err := Merge(e.st, ws, "what-if", alice, map[string]Resolution{answerA: {Side: SideTheirs}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != "" || len(res.Conflicts) != 1 || res.Conflicts[0].Path != answerB {
		t.Fatalf("result %+v, want only the unresolved %s", res, answerB)
	}
	if got := ref(t, e.st, ws, mainRef); got != before {
		t.Errorf("main moved to %s", got)
	}
}

func TestMergeRejectsInvalidResult(t *testing.T) {
	e := setup(t)
	commitFiles(t, e.st, ws, whatIf, map[string]string{"answers/" + unknownTask + ".md": textAnswer(unknownTask, "1.0.0", "x")})
	before := ref(t, e.st, ws, mainRef)

	_, err := Merge(e.st, ws, "what-if", alice, nil)
	var rej *store.RejectedError
	if !errors.As(err, &rej) || !hasRule(rej.Problems, problem.RuleAnswer) {
		t.Fatalf("Merge: %v, want rejection with rule %s", err, problem.RuleAnswer)
	}
	if got := ref(t, e.st, ws, mainRef); got != before {
		t.Errorf("main moved to %s", got)
	}
}

func TestMergeBadRequests(t *testing.T) {
	e := setup(t)
	before := diverge(t, e,
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "base")},
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "ours")},
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "theirs")})
	for name, tc := range map[string]struct {
		branch string
		res    map[string]Resolution
	}{
		"main":                {"main", nil},
		"empty":               {"", nil},
		"dot-dot":             {"a/../main", nil},
		"option":              {"-x", nil},
		"full ref":            {"refs/heads/what-if", nil},
		"unknown side":        {"what-if", map[string]Resolution{answerA: {Side: "mine"}}},
		"content missing":     {"what-if", map[string]Resolution{answerA: {Side: SideContent}}},
		"content with ours":   {"what-if", map[string]Resolution{answerA: {Side: SideOurs, Content: []byte("x")}}},
		"no conflict at path": {"what-if", map[string]Resolution{answerA: {Side: SideOurs}, answerB: {Side: SideOurs}}},
	} {
		if _, err := Merge(e.st, ws, tc.branch, alice, tc.res); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if got := ref(t, e.st, ws, mainRef); got != before {
		t.Errorf("main moved to %s", got)
	}
}

func TestMergeNotFound(t *testing.T) {
	e := setup(t)
	if _, err := Merge(e.st, ws, "nope", alice, nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown branch: %v, want ErrNotFound", err)
	}
	if _, err := Merge(e.st, unknownTask, "what-if", alice, nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown workspace: %v, want ErrNotFound", err)
	}
}

func TestMergeUnrelatedHistories(t *testing.T) {
	e := setup(t)
	repo := repoOf(t, e.st, ws)
	solo, err := repo.WriteCommit(gitrepo.CommitRequest{
		Changes: []gitrepo.Change{{Path: configPath, Data: []byte(e.config(ws, e.cat[0], false))}},
		Author:  alice,
		Message: "orphan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRef("refs/heads/orphan", solo, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(e.st, ws, "orphan", alice, nil); !errors.Is(err, store.ErrConflict) {
		t.Errorf("merging unrelated history: %v, want ErrConflict", err)
	}
}

func TestMergeMainMovedConcurrently(t *testing.T) {
	e := setup(t)
	diverge(t, e, nil,
		map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "ours")},
		map[string]string{answerB: textAnswer(fixture.TaskB, "1.0.0", "theirs")})
	var moved string
	beforeUpdate = func() {
		moved = commitFiles(t, e.st, ws, mainRef, map[string]string{answerA: textAnswer(fixture.TaskA, "1.1.0", "concurrent")})
	}
	t.Cleanup(func() { beforeUpdate = func() {} })

	if _, err := Merge(e.st, ws, "what-if", alice, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("Merge while main moved: %v, want ErrConflict", err)
	}
	if got := ref(t, e.st, ws, mainRef); got != moved {
		t.Errorf("main = %s, want the concurrent commit %s", got, moved)
	}
}
