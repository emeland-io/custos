package catalog

import (
	"maps"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/problem"
)

func TestCheckImmutable(t *testing.T) {
	old := fixture.MapFS(fixture.Catalog())
	path := fixture.TaskPath(a, "1.0.0")

	t.Run("new versions and other changes are fine", func(t *testing.T) {
		f := maps.Clone(fixture.Catalog())
		f[fixture.TaskPath(a, "1.2.0")] = fixture.TaskFile(a, "1.2.0", a+"@1.1.0")
		f["groups/index.yaml"] = "groups: [build]\n# reordered\n"
		fixture.WantNone(t, CheckImmutable(old, fixture.MapFS(f)))
	})
	t.Run("changed version", func(t *testing.T) {
		f := maps.Clone(fixture.Catalog())
		f[path] += "One more sentence.\n"
		fixture.WantProblem(t, CheckImmutable(old, fixture.MapFS(f)), path, problem.RuleImmutable, "was changed")
	})
	t.Run("removed version", func(t *testing.T) {
		f := maps.Clone(fixture.Catalog())
		delete(f, path)
		fixture.WantProblem(t, CheckImmutable(old, fixture.MapFS(f)), path, problem.RuleImmutable, "was removed")
	})
}
