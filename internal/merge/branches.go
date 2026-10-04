package merge

import (
	"maps"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/store"
)

// Branch is a branch of a workspace repository.
type Branch struct {
	Name   string `json:"name"` // short name, e.g. "main", "what-if"
	Commit string `json:"commit"`
}

// Branches lists the branches of a workspace, sorted by name.
func Branches(st *store.Store, id string) ([]Branch, error) {
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		return nil, err
	}
	refs, err := repo.Refs("refs/heads/")
	if err != nil {
		return nil, err
	}
	out := make([]Branch, 0, len(refs))
	for _, name := range slices.Sorted(maps.Keys(refs)) {
		out = append(out, Branch{Name: strings.TrimPrefix(name, "refs/heads/"), Commit: refs[name]})
	}
	return out, nil
}
