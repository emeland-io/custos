package match

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

// newRun returns a run of host-scanner on the answer to fixture.TaskB in w;
// new ids are gid(100), gid(101), …
func newRun(t *testing.T, files map[string]string) Run {
	t.Helper()
	next := 100
	return Run{
		Workspace:    load(t, files),
		Catalog:      fixtureCatalog(t),
		Task:         task.Ref{ID: fixture.TaskB, Version: "1.0.0"},
		Processor:    "host-scanner",
		Digest:       digest,
		AnswerCommit: fixture.Commit,
		Verifier:     attest.Unverified,
		NewID: func() string {
			next++
			return gid(next - 1)
		},
	}
}

// tasksOnly is fixture.Workspace without its document: TaskA's answer and
// the generated task TaskC (key host:web-01) made from TaskB's answer.
func tasksOnly() map[string]string {
	files := fixture.Workspace()
	delete(files, "documents/"+fixture.DocID+".json")
	return files
}

// webTask is the output task that reproduces fixture.GeneratedFile.
func webTask() contract.OutputTask {
	return contract.OutputTask{
		MatchKey: "host:web-01", Title: "Host web-01", Body: "When was web-01 last patched?",
		AnswerType: task.AnswerTimestamp, Origin: &contract.Origin{ID: fixture.TaskB},
	}
}

// apply returns files with changes applied.
func apply(files map[string]string, changes []gitrepo.Change) map[string]string {
	out := with(files, nil)
	for _, c := range changes {
		if c.Delete {
			delete(out, c.Path)
		} else {
			out[c.Path] = string(c.Data)
		}
	}
	return out
}

func paths(changes []gitrepo.Change) []string {
	var ps []string
	for _, c := range changes {
		mark := "+"
		if c.Delete {
			mark = "-"
		}
		ps = append(ps, mark+c.Path)
	}
	return ps
}

func wantItems(t *testing.T, got []Item, want ...Item) {
	t.Helper()
	if !slices.EqualFunc(got, want, func(a, b Item) bool {
		return a.Kind == b.Kind && a.MatchKey == b.MatchKey && a.Action == b.Action && a.ID == b.ID &&
			a.From == b.From && a.To == b.To && a.Title == b.Title
	}) {
		t.Errorf("items:\n%s\nwant:\n%s", fmtItems(got), fmtItems(want))
	}
}

func fmtItems(items []Item) string {
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "  %s %s %s %s %s→%s %q\n", it.Kind, it.MatchKey, it.Action, it.ID, it.From, it.To, it.Title)
	}
	return b.String()
}

func TestPlanAddsTasks(t *testing.T) {
	files := with(emptyWorkspace(), nil)
	run := newRun(t, files)
	web := webTask()
	web.Processor = "host-scanner"
	db := contract.OutputTask{MatchKey: "host:db-01", Title: "Host db-01", Body: "Is db-01 patched?\n",
		AnswerType: task.AnswerChoice, Choices: []string{"yes", "no"}}
	ch, err := Plan(run, &contract.Output{Tasks: []contract.OutputTask{web, db}})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items,
		Item{Kind: KindTask, MatchKey: "host:db-01", Action: Added, ID: gid(101), To: "1.0.0", Title: "Host db-01"},
		Item{Kind: KindTask, MatchKey: "host:web-01", Action: Added, ID: gid(100), To: "1.0.0", Title: "Host web-01"},
	)
	if got, want := paths(ch.Files), []string{"+" + genPath(gid(100), "1.0.0"), "+" + genPath(gid(101), "1.0.0")}; !slices.Equal(got, want) {
		t.Fatalf("files %v, want %v", got, want)
	}
	wantFile := "---\nid: " + gid(100) + "\nversion: 1.0.0\ntitle: Host web-01\nanswer_type: timestamp\n" +
		"origin:\n  id: " + fixture.TaskB + "\nmatch_key: host:web-01\nprocessor: host-scanner\nproduced_by:\n" +
		"  task:\n    id: " + fixture.TaskB + "\n    version: 1.0.0\n  processor: host-scanner\n" +
		"  digest: " + digest + "\n  answer_commit: " + fixture.Commit + "\n---\n\nWhen was web-01 last patched?\n"
	if got := string(ch.Files[0].Data); got != wantFile {
		t.Errorf("file:\n%s\nwant:\n%s", got, wantFile)
	}
	after := apply(files, ch.Files)
	valid(t, after)
	g := Current(load(t, after), gid(101))
	if g == nil || !slices.Equal(g.Choices, []string{"yes", "no"}) || g.ProducedBy.Task.ID != fixture.TaskB || g.Body != "Is db-01 patched?\n" {
		t.Errorf("db-01 read back as %+v", g)
	}
}

func TestPlanUnchangedTask(t *testing.T) {
	run := newRun(t, tasksOnly())
	web := webTask()
	web.Body = "When was web-01 last patched?\r\n" // line endings do not count
	web.Bump = "major"                             // bump is not content
	ch, err := Plan(run, &contract.Output{Tasks: []contract.OutputTask{web}})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items, Item{Kind: KindTask, MatchKey: "host:web-01", Action: Unchanged, ID: fixture.TaskC, From: "1.0.0", To: "1.0.0", Title: "Host web-01"})
	if len(ch.Files) != 0 {
		t.Errorf("files %v, want none", paths(ch.Files))
	}
}

func TestPlanNewTaskVersion(t *testing.T) {
	for _, c := range []struct {
		bump string
		want string
	}{{"", "1.1.0"}, {"patch", "1.0.1"}, {"minor", "1.1.0"}, {"major", "2.0.0"}} {
		t.Run("bump="+c.bump, func(t *testing.T) {
			files := tasksOnly()
			web := webTask()
			web.Title = "Host web-01 (production)"
			web.Bump = semver.Step(c.bump)
			ch, err := Plan(newRun(t, files), &contract.Output{Tasks: []contract.OutputTask{web}})
			if err != nil {
				t.Fatal(err)
			}
			wantItems(t, ch.Items, Item{Kind: KindTask, MatchKey: "host:web-01", Action: NewVersion, ID: fixture.TaskC,
				From: "1.0.0", To: c.want, Title: "Host web-01 (production)"})
			if got := paths(ch.Files); !slices.Equal(got, []string{"+" + genPath(fixture.TaskC, c.want)}) {
				t.Fatalf("files %v", got)
			}
			after := apply(files, ch.Files)
			valid(t, after)
			g := Current(load(t, after), fixture.TaskC)
			if g.Version != c.want || !slices.Equal(g.Previous, []task.Ref{{ID: fixture.TaskC, Version: "1.0.0"}}) {
				t.Errorf("new version %s, previous %v", g.Version, g.Previous)
			}
		})
	}
}

func TestPlanEachFieldIsContent(t *testing.T) {
	for name, change := range map[string]func(*contract.OutputTask){
		"body":        func(o *contract.OutputTask) { o.Body = "Other body" },
		"answer_type": func(o *contract.OutputTask) { o.AnswerType = task.AnswerText },
		"choices": func(o *contract.OutputTask) {
			o.AnswerType, o.Choices = task.AnswerChoice, []string{"yes", "no"}
		},
		"origin":         func(o *contract.OutputTask) { o.Origin = &contract.Origin{ID: fixture.TaskA} },
		"origin version": func(o *contract.OutputTask) { o.Origin.Version = "1.0.0" },
		"no origin":      func(o *contract.OutputTask) { o.Origin = nil },
		"processor":      func(o *contract.OutputTask) { o.Processor = "host-scanner" },
	} {
		t.Run(name, func(t *testing.T) {
			web := webTask()
			change(&web)
			ch, err := Plan(newRun(t, tasksOnly()), &contract.Output{Tasks: []contract.OutputTask{web}})
			if err != nil {
				t.Fatal(err)
			}
			if len(ch.Items) != 1 || ch.Items[0].Action != NewVersion {
				t.Errorf("items:\n%s", fmtItems(ch.Items))
			}
		})
	}
}

func TestPlanRemovesTasks(t *testing.T) {
	files := with(tasksOnly(), map[string]string{
		"answers/" + fixture.TaskC + ".md": "---\ntask: " + fixture.TaskC + "\ntask_version: 1.0.0\ntype: timestamp\nvalue: 2026-10-01T10:00:00Z\n---\n",
		genPath(gid(1), "1.0.0"):           genFile(gid(1), "1.0.0", "host:db-01", fixture.TaskB),
		genPath(gid(1), "1.1.0"):           genFile(gid(1), "1.1.0", "host:db-01", fixture.TaskB, gid(1)+"@1.0.0"),
		genPath(gid(2), "1.0.0"):           genFile(gid(2), "1.0.0", "other", fixture.TaskA), // another answer's output
	})
	valid(t, files)
	ch, err := Plan(newRun(t, files), &contract.Output{})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items,
		Item{Kind: KindTask, MatchKey: "host:db-01", Action: Removed, ID: gid(1), From: "1.1.0", Title: "Generated host:db-01"},
		Item{Kind: KindTask, MatchKey: "host:web-01", Action: Removed, ID: fixture.TaskC, From: "1.0.0", Title: "Host web-01"},
	)
	want := []string{"-" + genPath(gid(1), "1.0.0"), "-" + genPath(gid(1), "1.1.0"),
		"-answers/" + fixture.TaskC + ".md", "-" + genPath(fixture.TaskC, "1.0.0")}
	slices.Sort(want)
	if got := paths(ch.Files); !slices.Equal(got, want) {
		t.Errorf("files %v, want %v", got, want)
	}
	valid(t, apply(files, ch.Files))
}

func TestPlanDuplicateEarlierKeys(t *testing.T) {
	// Two generated tasks of TaskB's answer share a key, as only a hand
	// edit can do: the smaller id is matched, the other removed.
	files := with(emptyWorkspace(), map[string]string{
		genPath(gid(1), "1.0.0"): genFile(gid(1), "1.0.0", "k", fixture.TaskB),
		genPath(gid(2), "1.0.0"): genFile(gid(2), "1.0.0", "k", fixture.TaskB),
	})
	out := contract.OutputTask{MatchKey: "k", Title: "Generated k", Body: "Body of k.", AnswerType: task.AnswerText}
	ch, err := Plan(newRun(t, files), &contract.Output{Tasks: []contract.OutputTask{out}})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items,
		Item{Kind: KindTask, MatchKey: "k", Action: Unchanged, ID: gid(1), From: "1.0.0", To: "1.0.0", Title: "Generated k"},
		Item{Kind: KindTask, MatchKey: "k", Action: Removed, ID: gid(2), From: "1.0.0", Title: "Generated k"},
	)
}

func TestPlanChecksOutput(t *testing.T) {
	run := newRun(t, tasksOnly())
	ok := []contract.OutputTask{
		{MatchKey: "a", Title: "A", AnswerType: task.AnswerText, Origin: &contract.Origin{ID: fixture.TaskA}}, // catalog task
		{MatchKey: "b", Title: "B", AnswerType: task.AnswerText, Origin: &contract.Origin{ID: fixture.TaskC}}, // generated task
		{MatchKey: "c", Title: "C", AnswerType: task.AnswerText, Processor: "host-scanner"},
	}
	if _, err := Plan(run, &contract.Output{Tasks: ok}); err != nil {
		t.Fatalf("valid output: %v", err)
	}
	bad := &contract.Output{
		Tasks: []contract.OutputTask{
			{MatchKey: "a", Title: "A", AnswerType: task.AnswerText, Processor: "nope"},
			{MatchKey: "b", Title: "B", AnswerType: task.AnswerText, Origin: &contract.Origin{ID: gid(77)}},
			{MatchKey: "b", Title: "B again", AnswerType: task.AnswerText},
		},
	}
	_, err := Plan(run, bad)
	if !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("err %v, want ErrInvalidOutput", err)
	}
	for _, s := range []string{`processor "nope" is not registered`, "origin " + gid(77), `task "b": match_key appears more than once`} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q lacks %q", err, s)
		}
	}
}

// chainTo returns a workspace whose generated tasks gid(1) … gid(n) form a
// chain below TaskB, so gid(n) has depth n.
func chainTo(n int) map[string]string {
	files := emptyWorkspace()
	parent := fixture.TaskB
	for i := 1; i <= n; i++ {
		files[genPath(gid(i), "1.0.0")] = genFile(gid(i), "1.0.0", fmt.Sprintf("level-%d", i), parent)
		parent = gid(i)
	}
	return files
}

func TestPlanDepthLimit(t *testing.T) {
	one := &contract.Output{Tasks: []contract.OutputTask{{MatchKey: "deeper", Title: "Deeper", AnswerType: task.AnswerText}}}
	for _, c := range []struct {
		name    string
		depth   int // depth of the answered task
		max     int
		out     *contract.Output
		tooDeep bool
	}{
		{"catalog task", 0, 8, one, false},
		{"below the limit", 7, 8, one, false},
		{"at the limit", 8, 8, one, true},
		{"default limit", 8, 0, one, true},
		{"default limit not reached", 7, 0, one, false},
		{"small limit", 3, 3, one, true},
		{"no tasks in the output", 8, 8, &contract.Output{}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			run := newRun(t, chainTo(c.depth))
			if c.depth > 0 {
				run.Task = task.Ref{ID: gid(c.depth), Version: "1.0.0"}
			}
			run.MaxDepth = c.max
			_, err := Plan(run, c.out)
			if got := errors.Is(err, ErrTooDeep); got != c.tooDeep || (!c.tooDeep && err != nil) {
				t.Errorf("err %v, want too deep: %v", err, c.tooDeep)
			}
		})
	}
}

func TestPlanCycleIsTooDeep(t *testing.T) {
	files := with(emptyWorkspace(), map[string]string{
		genPath(gid(5), "1.0.0"): genFile(gid(5), "1.0.0", "five", gid(6)),
		genPath(gid(6), "1.0.0"): genFile(gid(6), "1.0.0", "six", gid(5)),
	})
	run := newRun(t, files)
	run.Task = task.Ref{ID: gid(5), Version: "1.0.0"}
	out := &contract.Output{Tasks: []contract.OutputTask{{MatchKey: "x", Title: "X", AnswerType: task.AnswerText}}}
	if _, err := Plan(run, out); !errors.Is(err, ErrTooDeep) || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("err %v, want ErrTooDeep naming the cycle", err)
	}
}

func TestPlanUnknownTask(t *testing.T) {
	run := newRun(t, emptyWorkspace())
	run.Task = task.Ref{ID: gid(42), Version: "1.0.0"}
	_, err := Plan(run, &contract.Output{})
	if err == nil || errors.Is(err, ErrTooDeep) || !strings.Contains(err.Error(), gid(42)) {
		t.Errorf("err %v, want an error naming the unknown task", err)
	}
}

func TestPlanWritesTextYAMLCannotWritePlainly(t *testing.T) {
	files := emptyWorkspace()
	out := &contract.Output{Tasks: []contract.OutputTask{{
		MatchKey: "\tkey: with colon", Title: "\tIndented: title #1", Body: "line one\r\nline two",
		AnswerType: task.AnswerText,
	}}}
	ch, err := Plan(newRun(t, files), out)
	if err != nil {
		t.Fatal(err)
	}
	after := apply(files, ch.Files)
	valid(t, after)
	g := Current(load(t, after), gid(100))
	if g == nil || g.Title != "\tIndented: title #1" || g.MatchKey != "\tkey: with colon" || g.Body != "line one\nline two\n" {
		t.Fatalf("read back as %+v", g)
	}
	// Running again with the same output changes nothing.
	again, err := Plan(newRun(t, after), out)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Files) != 0 || again.Items[0].Action != Unchanged {
		t.Errorf("second run: files %v, items:\n%s", paths(again.Files), fmtItems(again.Items))
	}
}
