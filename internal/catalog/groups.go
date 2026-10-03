package catalog

import (
	"errors"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/repofile"
)

const indexPath = "groups/index.yaml"

// Group is one groups/<slug>/group.yaml file.
type Group struct {
	Slug     string  `yaml:"-"`
	Path     string  `yaml:"-"`
	Title    string  `yaml:"title"`
	Children []Child `yaml:"children"`
}

// Child is one entry of a group: a sub-group by slug or a task by UUID.
type Child struct {
	Group string `yaml:"group,omitempty"`
	Task  string `yaml:"task,omitempty"`
}

type index struct {
	Groups []string `yaml:"groups"`
}

func (c *Catalog) loadGroups(fsys fs.FS) []problem.Problem {
	var idx index
	_, ips := repofile.ReadYAML(fsys, indexPath, &idx)
	ps := problem.List(ips)
	c.Index = idx.Groups
	entries, err := fs.ReadDir(fsys, "groups")
	if errors.Is(err, fs.ErrNotExist) {
		return ps
	}
	if err != nil {
		ps.Add("groups", problem.RuleFormat, "%v", err)
		return ps
	}
	for _, e := range entries {
		path := "groups/" + e.Name()
		switch {
		case !e.IsDir():
			if path != indexPath {
				ps.Add(path, problem.RulePath, "unexpected file; expected groups/index.yaml or groups/<slug>/group.yaml")
			}
		case !slugRE.MatchString(e.Name()):
			ps.Add(path, problem.RuleGroups, "group directory name must match [a-z0-9][a-z0-9-]*")
		default:
			g := &Group{Slug: e.Name(), Path: path + "/group.yaml"}
			found, gps := repofile.ReadYAML(fsys, g.Path, g)
			ps = append(ps, gps...)
			switch {
			case !found:
				ps.Add(path, problem.RuleGroups, "group.yaml is missing")
			case len(gps) == 0:
				c.Groups[g.Slug] = g
			}
		}
	}
	return ps
}

// checkGroups applies rule 5: groups form one tree below groups/index.yaml
// and list each existing, non-superseded task at most once.
func (c *Catalog) checkGroups() []problem.Problem {
	var ps problem.List
	refs := map[string]int{}      // group slug → times listed
	taskIn := map[string]string{} // task id → path of the group listing it
	for _, slug := range c.Index {
		if _, ok := c.Groups[slug]; !ok {
			ps.Add(indexPath, problem.RuleGroups, "lists unknown group %q", slug)
			continue
		}
		refs[slug]++
	}
	slugs := slices.Sorted(maps.Keys(c.Groups))
	for _, slug := range slugs {
		g := c.Groups[slug]
		if strings.TrimSpace(g.Title) == "" {
			ps.Add(g.Path, problem.RuleGroups, "title is missing")
		}
		for _, ch := range g.Children {
			switch {
			case (ch.Group == "") == (ch.Task == ""):
				ps.Add(g.Path, problem.RuleGroups, "each child must set exactly one of group or task")
			case ch.Group != "":
				if _, ok := c.Groups[ch.Group]; !ok {
					ps.Add(g.Path, problem.RuleGroups, "lists unknown group %q", ch.Group)
					continue
				}
				refs[ch.Group]++
			case !c.Tasks.Has(ch.Task):
				ps.Add(g.Path, problem.RuleGroups, "lists unknown task %s", ch.Task)
			case c.Tasks.Superseded(ch.Task):
				ps.Add(g.Path, problem.RuleGroups, "lists task %s, which was merged into another task; remove it from this group", ch.Task)
			case taskIn[ch.Task] != "":
				ps.Add(g.Path, problem.RuleGroups, "lists task %s, which is already listed in %s", ch.Task, taskIn[ch.Task])
			default:
				taskIn[ch.Task] = g.Path
			}
		}
	}
	reach := c.reachable()
	for _, slug := range slugs {
		g := c.Groups[slug]
		if refs[slug] > 1 {
			ps.Add(g.Path, problem.RuleGroups, "group is listed %d times; each group must appear once", refs[slug])
		}
		if !reach[slug] {
			ps.Add(g.Path, problem.RuleGroups, "group is not reachable from %s", indexPath)
		}
	}
	return ps
}

func (c *Catalog) reachable() map[string]bool {
	seen := map[string]bool{}
	queue := slices.Clone(c.Index)
	for len(queue) > 0 {
		slug := queue[0]
		queue = queue[1:]
		g, ok := c.Groups[slug]
		if !ok || seen[slug] {
			continue
		}
		seen[slug] = true
		for _, ch := range g.Children {
			if ch.Group != "" {
				queue = append(queue, ch.Group)
			}
		}
	}
	return seen
}
