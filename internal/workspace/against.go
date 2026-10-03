package workspace

import (
	"maps"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

// CheckAgainstCatalog applies ruling 2.14 to answers: each answer names a task
// of the catalog or a generated task of w, a task_version that exists for it,
// the same answer type, and for choice tasks one of its choices. c is the
// catalog at the workspace's pin. Answers whose task or task_version is
// malformed are skipped; Check reports them.
func CheckAgainstCatalog(w *Workspace, c *catalog.Catalog) []problem.Problem {
	var ps problem.List
	for _, path := range slices.Sorted(maps.Keys(w.Answers)) {
		a := w.Answers[path]
		if !task.ValidID(a.Task) || !semver.Valid(a.TaskVersion) {
			continue
		}
		graph, where := c.Tasks, "the pinned catalog"
		if !graph.Has(a.Task) {
			graph, where = w.Graph, "the generated tasks"
		}
		if !graph.Has(a.Task) {
			ps.Add(path, problem.RuleAnswer, "task %s is neither in the pinned catalog nor a generated task", a.Task)
			continue
		}
		v, ok := graph.Lookup(task.Ref{ID: a.Task, Version: a.TaskVersion})
		if !ok {
			ps.Add(path, problem.RuleAnswer, "task %s has no version %s in %s", a.Task, a.TaskVersion, where)
			continue
		}
		if a.Type != v.AnswerType {
			ps.Add(path, problem.RuleAnswer, "type %s does not match answer_type %s of task %s", a.Type, v.AnswerType, v.Ref())
			continue
		}
		if v.AnswerType == task.AnswerChoice && !slices.Contains(v.Choices, a.Value) {
			ps.Add(path, problem.RuleAnswer, "value %q is not one of the choices of task %s: %s", a.Value, v.Ref(), strings.Join(v.Choices, ", "))
		}
	}
	return ps
}
