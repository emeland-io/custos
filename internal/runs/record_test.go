package runs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/task"
)

const runID = "1f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9"

func TestRunKey(t *testing.T) {
	k := runKey(fixture.WorkspaceID, "answers/x.md", "abc", "sha256:"+fixture.SHA256)
	if len(k) != 64 {
		t.Fatalf("key %q is not 64 hex digits", k)
	}
	if k != runKey(fixture.WorkspaceID, "answers/x.md", "abc", "sha256:"+fixture.SHA256) {
		t.Error("the key is not stable")
	}
	for _, other := range []string{
		runKey(fixture.WorkspaceID, "answers/x.md", "abd", "sha256:"+fixture.SHA256),
		runKey(fixture.WorkspaceID, "answers/y.md", "abc", "sha256:"+fixture.SHA256),
		runKey(fixture.WorkspaceID, "answers/x.md", "abc", "sha256:0"+fixture.SHA256[1:]),
		runKey("6c7d8e9f-0a1b-4c2d-b3e4-f5a6b7c8d9e0", "answers/x.md", "abc", "sha256:"+fixture.SHA256),
	} {
		if other == k {
			t.Error("a different input gives the same key")
		}
	}
}

func TestFilesRoundTrip(t *testing.T) {
	f := files{dir: filepath.Join(t.TempDir(), "runs")}
	if rs, err := f.load(); err != nil || len(rs) != 0 {
		t.Fatalf("empty: %v %v", rs, err)
	}
	r := &Record{
		ID: runID, Workspace: fixture.WorkspaceID, Task: task.Ref{ID: fixture.TaskB, Version: "1.0.0"},
		AnswerPath: "answers/" + fixture.TaskB + ".md", Reason: ReasonAnswer, State: Queued,
		Queued: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC),
	}
	if err := f.save(r); err != nil {
		t.Fatal(err)
	}
	if err := f.saveLog(r, []byte("boom\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(f.dir, fixture.WorkspaceID, runID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{`"started"`, `"finished"`, `"outcome"`, `"retry_of"`} {
		if strings.Contains(string(data), absent) {
			t.Errorf("record file has %s although it is empty:\n%s", absent, data)
		}
	}
	rs, err := f.load()
	if err != nil || len(rs) != 1 {
		t.Fatalf("load: %v %v", rs, err)
	}
	if got := rs[0]; got.ID != r.ID || got.Task != r.Task || got.State != Queued || !got.Queued.Equal(r.Queued) || !got.Started.IsZero() {
		t.Errorf("loaded %+v", got)
	}
	if log, err := f.log(r); err != nil || string(log) != "boom\n" {
		t.Errorf("log %q %v", log, err)
	}
	other := *r
	other.ID = "2f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9"
	if log, err := f.log(&other); err != nil || len(log) != 0 {
		t.Errorf("missing log: %q %v", log, err)
	}
}

func TestFilesLoadSkipsDamagedRecords(t *testing.T) {
	f := files{dir: filepath.Join(t.TempDir(), "runs")}
	dir := filepath.Join(f.dir, fixture.WorkspaceID)
	fixture.WriteDir(t, dir, map[string]string{
		runID + ".json": "{not json",
		"2f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9.json": `{"id":"3f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9","workspace":"` + fixture.WorkspaceID + `"}`,
		"notes.txt": "x",
	})
	fixture.WriteDir(t, filepath.Join(f.dir, "not-a-workspace"), map[string]string{runID + ".json": "{}"})
	rs, err := f.load()
	if err != nil || len(rs) != 0 {
		t.Errorf("load: %+v %v", rs, err)
	}
}
