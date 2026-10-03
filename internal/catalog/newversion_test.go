package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/semver"
)

func TestNewVersion(t *testing.T) {
	dir := t.TempDir()
	fixture.WriteDir(t, dir, fixture.Catalog())
	path, err := NewVersion(dir, a, semver.Minor)
	if err != nil {
		t.Fatal(err)
	}
	if want := fixture.TaskPath(a, "1.2.0"); path != want {
		t.Errorf("path %s, want %s", path, want)
	}
	data, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nid: " + a + "\nversion: 1.2.0\ntitle: Task 1.1.0\nanswer_type: text\nprevious:\n  - id: " + a +
		"\n    version: 1.1.0\n---\n\nDescribe how the task was done.\n"
	if string(data) != want {
		t.Errorf("got\n%s\nwant\n%s", data, want)
	}
	fixture.WantNone(t, Check(os.DirFS(dir)))
}

func TestNewVersionErrors(t *testing.T) {
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
			// 1.1.0 stays the only current version, but 1.2.0 already exists:
			// it was branched from 1.0.0 and merged back by 1.1.0.
			f[fixture.TaskPath(a, "1.2.0")] = fixture.TaskFile(a, "1.2.0", a+"@1.0.0")
			f[fixture.TaskPath(a, "1.1.0")] = fixture.TaskFile(a, "1.1.0", a+"@1.0.0", a+"@1.2.0")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := fixture.Catalog()
			tt.change(f)
			dir := t.TempDir()
			fixture.WriteDir(t, dir, f)
			_, err := NewVersion(dir, tt.id, semver.Minor)
			if err == nil || !strings.Contains(err.Error(), tt.contains) {
				t.Errorf("err = %v, want it to contain %q", err, tt.contains)
			}
		})
	}
}
