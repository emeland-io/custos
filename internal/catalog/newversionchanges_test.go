package catalog

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

func TestNewVersionChanges(t *testing.T) {
	fsys := fixture.MapFS(fixture.Catalog())
	changes, err := NewVersionChanges(fsys, a, semver.Minor, "New title", "New body.\n", task.AnswerText, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want exactly one", changes)
	}
	ch := changes[0]
	if want := fixture.TaskPath(a, "1.2.0"); ch.Path != want || ch.Delete {
		t.Fatalf("change = %+v, want an add at %s", ch, want)
	}
	want := "---\nid: " + a + "\nversion: 1.2.0\ntitle: New title\nanswer_type: text\nprevious:\n  - id: " + a +
		"\n    version: 1.1.0\n---\n\nNew body.\n"
	if string(ch.Data) != want {
		t.Errorf("got\n%s\nwant\n%s", ch.Data, want)
	}
	// Applying the change to the tree must still validate.
	after := with(fixture.Catalog(), map[string]string{ch.Path: string(ch.Data)})
	fixture.WantNone(t, Check(fixture.MapFS(after)))
}

// with returns files plus more, more winning on overlap.
func with(files, more map[string]string) map[string]string {
	out := map[string]string{}
	for p, c := range files {
		out[p] = c
	}
	for p, c := range more {
		out[p] = c
	}
	return out
}

func TestNewVersionChangesErrors(t *testing.T) {
	tests := []struct {
		name, id, contains string
		change             func(f map[string]string)
	}{
		{"unknown task", c, "not found", func(map[string]string) {}},
		{"forked task", a, "several current versions", func(f map[string]string) {
			f[fixture.TaskPath(a, "1.2.0")] = fixture.TaskFile(a, "1.2.0", a+"@1.0.0")
		}},
		{"merged task", b, "merged into another task", merge},
		{"version exists", a, "already exists", func(f map[string]string) {
			f[fixture.TaskPath(a, "1.2.0")] = fixture.TaskFile(a, "1.2.0", a+"@1.0.0")
			f[fixture.TaskPath(a, "1.1.0")] = fixture.TaskFile(a, "1.1.0", a+"@1.0.0", a+"@1.2.0")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := fixture.Catalog()
			tt.change(f)
			_, err := NewVersionChanges(fixture.MapFS(f), tt.id, semver.Minor, "T", "B\n", task.AnswerText, nil)
			if err == nil || !strings.Contains(err.Error(), tt.contains) {
				t.Errorf("err = %v, want it to contain %q", err, tt.contains)
			}
		})
	}
}
