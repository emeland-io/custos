// Package catalog loads a catalog tree (tasks, groups, processors) and checks
// the rules that every commit on its main branch must follow.
package catalog

import (
	"io/fs"
	"regexp"

	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/repofile"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

var (
	taskLayout = regexp.MustCompile(`^tasks/[^/]+/[^/]+\.md$`)
	slugRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// Catalog is the content of one catalog tree. Files at the top level other
// than those below are ignored, so a catalog can carry a README.
type Catalog struct {
	Tasks    *task.Graph
	Groups   map[string]*Group // by slug
	Index    []string          // top-level group slugs, in book order
	Registry Registry
}

// Load reads a catalog tree. Problems found while reading are returned;
// the catalog holds everything that could be read.
func Load(fsys fs.FS) (*Catalog, []problem.Problem) {
	c := &Catalog{Groups: map[string]*Group{}}
	var versions []*task.Version
	ps := repofile.Walk(fsys, "tasks", taskLayout, "tasks/<uuid>/<semver>.md", func(path string, data []byte) []problem.Problem {
		v, vps := task.ParseFile(path, data)
		if v != nil && task.ValidID(v.ID) && semver.Valid(v.Version) {
			versions = append(versions, v)
		}
		return vps
	})
	g, gps := task.NewGraph(versions)
	c.Tasks = g
	ps = append(ps, gps...)
	ps = append(ps, c.loadGroups(fsys)...)
	ps = append(ps, c.loadRegistry(fsys)...)
	return c, ps
}

// Validate checks the rules that span files.
func (c *Catalog) Validate() []problem.Problem {
	ps := c.Tasks.Check()
	ps = append(ps, c.checkGroups()...)
	return append(ps, c.checkRegistry()...)
}

// Check loads and validates the catalog in fsys and returns all problems,
// sorted.
func Check(fsys fs.FS) []problem.Problem {
	c, ps := Load(fsys)
	ps = append(ps, c.Validate()...)
	problem.Sort(ps)
	return ps
}
