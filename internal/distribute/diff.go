package distribute

import (
	"maps"
	"slices"
	"testing/fstest"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/task"
)

// Diff is what changes for a workspace when its pin moves from one catalog
// commit to another (spec §4.1 step 2). Lists are sorted by task id.
type Diff struct {
	// NewVersions are tasks with a current version at both commits that
	// differs; each entry is the version at the new commit.
	NewVersions []task.Ref `json:"new_versions"`
	// Added are tasks with a current version only at the new commit.
	Added []task.Ref `json:"added"`
	// Superseded are tasks with a current version only at the old commit
	// (merged into another task, or missing because the pin moves back);
	// each entry is the version at the old commit.
	Superseded []task.Ref `json:"superseded"`
	// GroupsChanged reports a change in groups/index.yaml or a group.yaml.
	GroupsChanged bool `json:"groups_changed"`
	// BindingsChanged reports a change in processors.yaml: a binding, or a
	// processor's image digest, timeout, network or secrets.
	BindingsChanged bool `json:"bindings_changed"`
}

// CatalogDiff compares the catalog at commit from with the catalog at commit
// to. from "" stands for an empty catalog. Files are compared by what they
// mean, not byte by byte. Problems in either tree are ignored: commits on
// the catalog's main were validated when they got there.
func CatalogDiff(cat *gitrepo.Repo, from, to string) (Diff, error) {
	a, err := loadCatalog(cat, from)
	if err != nil {
		return Diff{}, err
	}
	b, err := loadCatalog(cat, to)
	if err != nil {
		return Diff{}, err
	}
	d := Diff{NewVersions: []task.Ref{}, Added: []task.Ref{}, Superseded: []task.Ref{}}
	ids := append(a.Tasks.Tasks(), b.Tasks.Tasks()...)
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		old, hadOld := a.Tasks.Current(id)
		cur, hasCur := b.Tasks.Current(id)
		switch {
		case hasCur && !hadOld:
			d.Added = append(d.Added, cur.Ref())
		case hasCur && old.Ref() != cur.Ref():
			d.NewVersions = append(d.NewVersions, cur.Ref())
		case !hasCur && hadOld:
			d.Superseded = append(d.Superseded, old.Ref())
		}
	}
	d.GroupsChanged = !sameGroups(a, b)
	d.BindingsChanged = !sameRegistry(a.Registry, b.Registry)
	return d, nil
}

func loadCatalog(cat *gitrepo.Repo, rev string) (*catalog.Catalog, error) {
	if rev == "" {
		c, _ := catalog.Load(fstest.MapFS{})
		return c, nil
	}
	fsys, err := cat.TreeFS(rev)
	if err != nil {
		return nil, err
	}
	c, _ := catalog.Load(fsys)
	return c, nil
}

func sameGroups(a, b *catalog.Catalog) bool {
	return slices.Equal(a.Index, b.Index) && maps.EqualFunc(a.Groups, b.Groups, func(x, y *catalog.Group) bool {
		return x.Title == y.Title && slices.Equal(x.Children, y.Children)
	})
}

func sameRegistry(a, b catalog.Registry) bool {
	return maps.Equal(a.Bindings, b.Bindings) && maps.EqualFunc(a.Processors, b.Processors, func(x, y catalog.Processor) bool {
		return x.Image == y.Image && x.Timeout == y.Timeout && x.Network == y.Network && slices.Equal(x.Secrets, y.Secrets)
	})
}
