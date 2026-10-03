package status

import (
	"fmt"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/workspace"
)

const (
	a = fixture.TaskA
	b = fixture.TaskB
	c = fixture.TaskC
	d = "0f0e0d0c-0b0a-4908-8706-050403020100"
	e = "1e2d3c4b-5a69-4788-9766-554433221100"
)

func compute(t *testing.T, cat, ws map[string]string) *Status {
	t.Helper()
	cl, ps := catalog.Load(fixture.MapFS(cat))
	fixture.WantNone(t, append(ps, cl.Validate()...))
	w, ps := workspace.Load(fixture.MapFS(ws))
	fixture.WantNone(t, ps)
	return Compute(w, cl)
}

// book renders the book as "<depth>:<group slug or task id>" items, with
// the state after task ids.
func book(s *Status) string {
	var items []string
	for _, en := range s.Book {
		switch {
		case en.Group != nil:
			items = append(items, fmt.Sprintf("%d:group %s", en.Depth, en.Group.Title))
		case en.Task != nil:
			items = append(items, fmt.Sprintf("%d:%s %s", en.Depth, en.Task.ID[:4], en.Task.State))
		}
	}
	return strings.Join(items, ", ")
}

func answerFile(id, version string) string {
	return "---\ntask: " + id + "\ntask_version: " + version + "\ntype: text\nvalue: answer to " + id[:4] + "@" + version + "\n---\n"
}

func answerPath(id string) string { return "answers/" + id + ".md" }

// generated returns a generated task file for id below origin; an empty
// origin leaves only produced_by, which names TaskB.
func generated(id, origin string) (string, string) {
	f := strings.ReplaceAll(fixture.GeneratedFile, "id: "+c+"\n", "id: "+id+"\n")
	if origin == "" {
		f = strings.Replace(f, "origin:\n  id: "+b+"\n", "", 1)
	} else {
		f = strings.Replace(f, "origin:\n  id: "+b+"\n", "origin:\n  id: "+origin+"\n", 1)
	}
	return "generated/" + id + "/1.0.0.md", f
}

func values(as []*workspace.Answer) string {
	var vs []string
	for _, x := range as {
		vs = append(vs, x.Value)
	}
	return strings.Join(vs, " + ")
}

func TestComputeFixture(t *testing.T) {
	s := compute(t, fixture.Catalog(), fixture.Workspace())
	if s.Workspace != fixture.WorkspaceID || s.Pin != fixture.Commit {
		t.Errorf("workspace %q pin %q", s.Workspace, s.Pin)
	}
	want := "0:group Build, 1:3f1c pending-update, 1:group Release, 2:7d9e unanswered, 3:a1b2 unanswered"
	if got := book(s); got != want {
		t.Errorf("book\n got %s\nwant %s", got, want)
	}
	if len(s.Tasks) != 3 || s.Tasks[0].ID != a || s.Tasks[1].ID != b || s.Tasks[2].ID != c {
		t.Fatalf("tasks %v", s.Tasks)
	}
	ta, tc := s.Tasks[0], s.Tasks[2]
	if ta.Current.Version != "1.1.0" || ta.Title != "Task 1.1.0" || ta.Generated || ta.Answer == nil || values(ta.InEffect) != "done with make" {
		t.Errorf("TaskA %+v", ta)
	}
	if !tc.Generated || tc.Title != "Host web-01" || tc.AnswerType != "timestamp" || tc.Answer != nil || tc.InEffect != nil {
		t.Errorf("TaskC %+v", tc)
	}
}

func TestComputeAnswered(t *testing.T) {
	ws := fixture.Workspace()
	ws[answerPath(a)] = answerFile(a, "1.1.0")
	ta := compute(t, fixture.Catalog(), ws).Tasks[0]
	if ta.State != Answered || values(ta.InEffect) != "answer to 3f1c@1.1.0" {
		t.Errorf("TaskA %+v", ta)
	}
}

// mergedCatalog is the fixture catalog after TaskA 2.0.0 merged TaskA 1.1.0
// and TaskB 1.0.0.
func mergedCatalog() map[string]string {
	cat := fixture.Catalog()
	cat[fixture.TaskPath(a, "2.0.0")] = fixture.TaskFile(a, "2.0.0", a+"@1.1.0", b+"@1.0.0")
	cat["groups/release/group.yaml"] = "title: Release\nchildren: []\n"
	cat["processors.yaml"] = "processors: {}\nbindings: {}\n"
	return cat
}

func TestComputeMergedTasks(t *testing.T) {
	ws := fixture.Workspace()
	ws[answerPath(b)] = answerFile(b, "1.0.0")
	s := compute(t, mergedCatalog(), ws)
	// TaskB is gone; TaskC, generated from TaskB's answer, follows TaskA.
	if got, want := book(s), "0:group Build, 1:3f1c pending-update, 2:a1b2 unanswered, 1:group Release"; got != want {
		t.Errorf("book\n got %s\nwant %s", got, want)
	}
	ta := s.Tasks[0]
	if values(ta.InEffect) != "done with make + answer to 7d9e@1.0.0" || values(ta.Merged) != "answer to 7d9e@1.0.0" {
		t.Errorf("before answering the merge: in effect %q, merged %q", values(ta.InEffect), values(ta.Merged))
	}

	// Answering the merged version replaces both answers in the book.
	ws[answerPath(a)] = answerFile(a, "2.0.0")
	ta = compute(t, mergedCatalog(), ws).Tasks[0]
	if ta.State != Answered || values(ta.InEffect) != "answer to 3f1c@2.0.0" || values(ta.Merged) != "answer to 7d9e@1.0.0" {
		t.Errorf("after answering: %s, in effect %q, merged %q", ta.State, values(ta.InEffect), values(ta.Merged))
	}

	// A later version keeps the merged answer out of the book: the answer
	// to 2.0.0 already covers TaskB.
	cat := mergedCatalog()
	cat[fixture.TaskPath(a, "3.0.0")] = fixture.TaskFile(a, "3.0.0", a+"@2.0.0")
	ta = compute(t, cat, ws).Tasks[0]
	if ta.State != PendingUpdate || values(ta.InEffect) != "answer to 3f1c@2.0.0" {
		t.Errorf("after a new version: %s, in effect %q", ta.State, values(ta.InEffect))
	}

	// Only the merged task was answered.
	delete(ws, answerPath(a))
	ta = compute(t, mergedCatalog(), ws).Tasks[0]
	if ta.State != PendingUpdate || ta.Answer != nil || values(ta.InEffect) != "answer to 7d9e@1.0.0" {
		t.Errorf("only TaskB answered: %s, in effect %q", ta.State, values(ta.InEffect))
	}
}

func TestComputeUngrouped(t *testing.T) {
	cat := fixture.Catalog()
	cat[fixture.TaskPath(d, "1.0.0")] = fixture.TaskFile(d, "1.0.0")
	ws := fixture.Workspace()
	path, file := generated(e, "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d") // origin is no task
	ws[path] = file
	got := book(compute(t, cat, ws))
	want := "0:group Build, 1:3f1c pending-update, 1:group Release, 2:7d9e unanswered, 3:a1b2 unanswered, " +
		"0:group Ungrouped, 1:0f0e unanswered, 1:1e2d unanswered"
	if got != want {
		t.Errorf("book\n got %s\nwant %s", got, want)
	}
}

func TestComputeGeneratedWithoutOrigin(t *testing.T) {
	ws := fixture.Workspace()
	path, file := generated(c, "")
	ws[path] = file
	want := "0:group Build, 1:3f1c pending-update, 1:group Release, 2:7d9e unanswered, 3:a1b2 unanswered"
	if got := book(compute(t, fixture.Catalog(), ws)); got != want {
		t.Errorf("book\n got %s\nwant %s", got, want)
	}
}

// TestComputeOriginCycle must terminate and list each task once.
func TestComputeOriginCycle(t *testing.T) {
	ws := fixture.Workspace()
	p1, f1 := generated(d, e)
	p2, f2 := generated(e, d)
	ws[p1], ws[p2] = f1, f2
	s := compute(t, fixture.Catalog(), ws)
	if got, want := book(s), "0:group Build, 1:3f1c pending-update, 1:group Release, 2:7d9e unanswered, 3:a1b2 unanswered, 0:group Ungrouped, 1:0f0e unanswered, 2:1e2d unanswered"; got != want {
		t.Errorf("book\n got %s\nwant %s", got, want)
	}
	if len(s.Tasks) != 5 {
		t.Errorf("%d tasks", len(s.Tasks))
	}
}

// TestComputeBrokenTrees must not panic on trees that never passed
// validation, such as a repository edited on disk (spec section 7).
func TestComputeBrokenTrees(t *testing.T) {
	cat := fixture.Catalog()
	cat["groups/index.yaml"] = "groups: [build, build, missing]\n"
	cat["groups/release/group.yaml"] = "title: Release\nchildren:\n  - group: build\n  - task: " + d + "\n"
	cat[fixture.TaskPath(a, "1.2.0")] = fixture.TaskFile(a, "1.2.0", a+"@1.0.0") // two current versions
	cl, _ := catalog.Load(fixture.MapFS(cat))
	ws := fixture.Workspace()
	ws["custos.yaml"] = "- garbage\n"
	ws[answerPath(b)] = answerFile(c, "1.0.0") // stored under the wrong task
	w, _ := workspace.Load(fixture.MapFS(ws))
	s := Compute(w, cl)
	// Release lists build again (a cycle) and an unknown task instead of
	// TaskB; TaskA has two current versions and is left out.
	if got, want := book(s), "0:group Build, 1:group Release, 0:group Ungrouped, 1:7d9e unanswered, 2:a1b2 unanswered"; got != want {
		t.Errorf("book\n got %s\nwant %s", got, want)
	}
}
