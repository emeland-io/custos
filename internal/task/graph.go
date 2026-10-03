package task

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/semver"
)

// Graph is the task-history graph of a set of task versions: each version
// points to the versions listed in its previous field. It is separate from
// git history.
type Graph struct {
	versions   map[Ref]*Version
	byTask     map[string][]*Version
	referenced map[Ref]bool
}

// NewGraph indexes vs. Every version must have a valid id and version.
// A second version with the same id and version is reported and ignored.
func NewGraph(vs []*Version) (*Graph, []problem.Problem) {
	g := &Graph{versions: map[Ref]*Version{}, byTask: map[string][]*Version{}, referenced: map[Ref]bool{}}
	var ps problem.List
	for _, v := range vs {
		r := v.Ref()
		if other, ok := g.versions[r]; ok {
			ps.Add(v.Path, problem.RulePath, "%s is also defined in %s", r, other.Path)
			continue
		}
		g.versions[r] = v
		g.byTask[r.ID] = append(g.byTask[r.ID], v)
		for _, p := range v.Previous {
			g.referenced[p] = true
		}
	}
	for _, list := range g.byTask {
		slices.SortFunc(list, func(x, y *Version) int { return semver.Compare(x.Version, y.Version) })
	}
	return g, ps
}

// Lookup returns the version r refers to.
func (g *Graph) Lookup(r Ref) (*Version, bool) {
	v, ok := g.versions[r]
	return v, ok
}

// Has reports whether the graph holds any version of task id.
func (g *Graph) Has(id string) bool { return len(g.byTask[id]) > 0 }

// Tasks returns the ids of all tasks, sorted.
func (g *Graph) Tasks() []string { return slices.Sorted(maps.Keys(g.byTask)) }

// Versions returns the versions of task id, lowest first.
func (g *Graph) Versions(id string) []*Version { return g.byTask[id] }

// Heads returns the versions of task id that no version lists as previous.
func (g *Graph) Heads(id string) []*Version {
	var hs []*Version
	for _, v := range g.byTask[id] {
		if !g.referenced[v.Ref()] {
			hs = append(hs, v)
		}
	}
	return hs
}

// Current returns the current version of task id. It fails when the task
// is superseded or has several heads.
func (g *Graph) Current(id string) (*Version, bool) {
	hs := g.Heads(id)
	if len(hs) != 1 {
		return nil, false
	}
	return hs[0], true
}

// Superseded reports whether all versions of task id are listed as previous
// by other versions, which happens when the task was merged into another.
func (g *Graph) Superseded(id string) bool { return g.Has(id) && len(g.Heads(id)) == 0 }

// Check reports broken previous references (rule 3), tasks with more than
// one current version (rule 2), and reference cycles.
func (g *Graph) Check() []problem.Problem {
	var ps problem.List
	for _, id := range g.Tasks() {
		for _, v := range g.byTask[id] {
			for _, p := range v.Previous {
				if _, ok := g.versions[p]; !ok {
					ps.Add(v.Path, problem.RulePrevious, "previous %s does not exist", p)
					continue
				}
				if p.ID == v.ID && semver.Compare(p.Version, v.Version) >= 0 {
					ps.Add(v.Path, problem.RulePrevious, "previous %s must be a lower version than %s", p, v.Version)
				}
			}
		}
		if hs := g.Heads(id); len(hs) > 1 {
			names := make([]string, len(hs))
			for i, h := range hs {
				names[i] = h.Version
			}
			ps.Add(hs[len(hs)-1].Path, problem.RuleSingleCurrent,
				"task %s has %d current versions (%s); publish a new version that lists all of them as previous",
				id, len(hs), strings.Join(names, ", "))
		}
	}
	return append(ps, g.cycles()...)
}

func (g *Graph) cycles() []problem.Problem {
	const (
		visiting = 1
		done     = 2
	)
	state := map[Ref]int{}
	var ps problem.List
	var visit func(v *Version)
	visit = func(v *Version) {
		state[v.Ref()] = visiting
		for _, p := range v.Previous {
			pv, ok := g.versions[p]
			if !ok {
				continue
			}
			switch state[p] {
			case visiting:
				ps.Add(v.Path, problem.RulePrevious, "previous %s forms a reference cycle", p)
			case 0:
				visit(pv)
			}
		}
		state[v.Ref()] = done
	}
	for _, r := range g.sortedRefs() {
		if state[r] == 0 {
			visit(g.versions[r])
		}
	}
	return ps
}

func (g *Graph) sortedRefs() []Ref {
	refs := slices.Collect(maps.Keys(g.versions))
	slices.SortFunc(refs, func(x, y Ref) int {
		return cmp.Or(cmp.Compare(x.ID, y.ID), semver.Compare(x.Version, y.Version))
	})
	return refs
}
