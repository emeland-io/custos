package match

import (
	"maps"
	"math"
	"slices"

	"github.com/emeland-io/custos/internal/workspace"
)

// Current returns the current version of generated task id in w: the
// version no other version lists as previous, or the highest version when
// there is not exactly one such version (a broken history that validation
// reports). nil when w has no generated task id.
func Current(w *workspace.Workspace, id string) *workspace.Generated {
	if !w.Graph.Has(id) {
		return nil
	}
	v, ok := w.Graph.Current(id)
	if !ok {
		vs := w.Graph.Versions(id)
		v = vs[len(vs)-1]
	}
	return w.Generated[v.Path]
}

// producer returns produced_by.task.id of the current version of generated
// task id, or "" when id is not a generated task of w.
func producer(w *workspace.Workspace, id string) string {
	if g := Current(w, id); g != nil {
		return g.ProducedBy.Task.ID
	}
	return ""
}

// Depth returns the depth of task id in w: a generated task has the depth of
// the task whose answer produced it plus one, and a task that is not a
// generated task of w ends the chain with depth 0 (a catalog task; w does
// not hold the catalog). Depth returns -1 when id itself is not a generated
// task of w, so callers decide whether it is a catalog task, and
// math.MaxInt when the chain runs in a cycle, which counts as too deep.
func Depth(w *workspace.Workspace, id string) int {
	if !w.Graph.Has(id) {
		return -1
	}
	seen := map[string]bool{}
	d := 0
	for w.Graph.Has(id) {
		if seen[id] {
			return math.MaxInt
		}
		seen[id] = true
		d++
		id = producer(w, id)
	}
	return d
}

// Below returns the generated task ids whose produced_by.task.id is id,
// transitively, sorted (cascading removal). id itself is never part of the
// result, also when a cycle leads back to it.
func Below(w *workspace.Workspace, id string) []string {
	children := map[string][]string{}
	for _, t := range w.Graph.Tasks() {
		p := producer(w, t)
		children[p] = append(children[p], t)
	}
	found := map[string]bool{}
	queue := []string{id}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, c := range children[next] {
			if c != id && !found[c] {
				found[c] = true
				queue = append(queue, c)
			}
		}
	}
	return slices.Sorted(maps.Keys(found))
}
