package catalog

import (
	"maps"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/problem"
)

const pinned = "registry.example.org/host-scanner@sha256:" + fixture.SHA256

func registry(image, timeout, task, name string) string {
	return "processors:\n  host-scanner:\n    image: " + image + "\n    timeout: " + timeout +
		"\nbindings:\n  " + task + ": " + name + "\n"
}

func TestRegistryProblems(t *testing.T) {
	tests := []struct {
		name, file, rule, contains string
	}{
		{"image not pinned", registry("registry.example.org/host-scanner:latest", "30s", b, "host-scanner"), problem.RuleFormat, "pinned by digest"},
		{"bad timeout", registry(pinned, "soon", b, "host-scanner"), problem.RuleFormat, `timeout "soon"`},
		{"negative timeout", registry(pinned, "-5s", b, "host-scanner"), problem.RuleFormat, "positive duration"},
		{"unknown processor", registry(pinned, "30s", b, "scanner"), problem.RuleBindings, `unknown processor "scanner"`},
		{"unknown task", registry(pinned, "30s", c, "host-scanner"), problem.RuleBindings, "unknown task " + c},
		{"bad processor name", "processors:\n  Host_Scanner:\n    image: " + pinned + "\n", problem.RuleFormat, "must match"},
		{"bad secret name", "processors:\n  host-scanner:\n    image: " + pinned + "\n    secrets: [Signing Key]\n", problem.RuleFormat, "secret name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := maps.Clone(fixture.Catalog())
			f["processors.yaml"] = tt.file
			fixture.WantProblem(t, Check(fixture.MapFS(f)), "processors.yaml", tt.rule, tt.contains)
		})
	}
}

func TestMergedTaskStillBound(t *testing.T) {
	f := fixture.Catalog()
	merge(f)
	f["groups/release/group.yaml"] = "title: Release\nchildren: []\n"
	fixture.WantProblem(t, Check(fixture.MapFS(f)), "processors.yaml", problem.RuleBindings, "merged into another task")
}
