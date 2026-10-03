package api

import (
	"net/http"
	"slices"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/task"
)

func TestCatalog(t *testing.T) {
	e := newEnv(t, fixture.Catalog())
	got := decode[catalogJSON](t, e.do(t, "GET", "/api/catalog", "", ""), http.StatusOK)
	if got.Commit != e.catalogCommit {
		t.Errorf("commit %q, want %q", got.Commit, e.catalogCommit)
	}
	if len(got.Groups) != 1 || got.Groups[0].Slug != "build" || got.Groups[0].Title != "Build" {
		t.Fatalf("groups %+v", got.Groups)
	}
	build := got.Groups[0].Children
	if len(build) != 2 || build[0].Task != fixture.TaskA || build[1].Group == nil || build[1].Group.Slug != "release" {
		t.Fatalf("build children %+v", build)
	}
	if release := build[1].Group.Children; len(release) != 1 || release[0].Task != fixture.TaskB {
		t.Errorf("release children %+v", release)
	}
	if len(got.Tasks) != 2 {
		t.Fatalf("tasks %+v", got.Tasks)
	}
	a, b := got.Tasks[0], got.Tasks[1]
	if a.ID != fixture.TaskA || a.Version != "1.1.0" || a.Title != "Task 1.1.0" || a.AnswerType != task.AnswerText ||
		a.Body != "Describe how the task was done.\n" ||
		!slices.Equal(a.Previous, []task.Ref{{ID: fixture.TaskA, Version: "1.0.0"}}) || a.Processor != "" {
		t.Errorf("task A %+v", a)
	}
	if b.ID != fixture.TaskB || b.Version != "1.0.0" || b.Processor != "host-scanner" || b.Previous == nil {
		t.Errorf("task B %+v", b)
	}
}

func TestCatalogTask(t *testing.T) {
	e := newEnv(t, fixture.Catalog())
	got := decode[taskHistoryJSON](t, e.do(t, "GET", "/api/catalog/tasks/"+fixture.TaskA, "", ""), http.StatusOK)
	if got.ID != fixture.TaskA || got.Current != "1.1.0" || got.Superseded || len(got.Versions) != 2 ||
		got.Versions[0].Version != "1.0.0" || got.Versions[1].Version != "1.1.0" {
		t.Errorf("history %+v", got)
	}
	for _, id := range []string{fixture.TaskC, "not-a-uuid"} {
		decode[errorJSON](t, e.do(t, "GET", "/api/catalog/tasks/"+id, "", ""), http.StatusNotFound)
	}
}

func TestCatalogMergedTask(t *testing.T) {
	e := newEnv(t, mergedCatalog())
	b := decode[taskHistoryJSON](t, e.do(t, "GET", "/api/catalog/tasks/"+fixture.TaskB, "", ""), http.StatusOK)
	if !b.Superseded || b.Current != "" || len(b.Versions) != 1 {
		t.Errorf("merged task %+v", b)
	}
	got := decode[catalogJSON](t, e.do(t, "GET", "/api/catalog", "", ""), http.StatusOK)
	if len(got.Tasks) != 1 || got.Tasks[0].ID != fixture.TaskA || got.Tasks[0].Version != "2.0.0" ||
		len(got.Tasks[0].Previous) != 2 || got.Tasks[0].Processor != "host-scanner" {
		t.Errorf("tasks %+v", got.Tasks)
	}
}

func TestEmptyCatalog(t *testing.T) {
	e := newEnv(t, nil)
	got := decode[catalogJSON](t, e.do(t, "GET", "/api/catalog", "", ""), http.StatusOK)
	if got.Commit != "" || got.Groups == nil || len(got.Groups) != 0 || got.Tasks == nil || len(got.Tasks) != 0 {
		t.Errorf("empty catalog %+v", got)
	}
	decode[errorJSON](t, e.do(t, "GET", "/api/catalog/tasks/"+fixture.TaskA, "", ""), http.StatusNotFound)
}
