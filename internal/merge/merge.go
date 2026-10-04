package merge

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
)

// Sides of a Resolution.
const (
	SideOurs    = "ours"    // keep main's version
	SideTheirs  = "theirs"  // take the branch's version
	SideContent = "content" // write Content
)

// Kinds of a Conflict, derived from its path.
const (
	KindAnswer    = "answer"
	KindGenerated = "generated"
	KindDocument  = "document"
	KindConfig    = "custos.yaml"
	KindOther     = "other"
)

// Resolution settles the conflict at one path.
type Resolution struct {
	Side    string // "ours" | "theirs" | "content"
	Content []byte // when Side == "content"
}

// Conflict is a path the merge cannot settle on its own.
type Conflict struct {
	Path         string
	Ours, Theirs []byte // nil when the side deleted the file
	Kind         string // "answer" | "generated" | "document" | "custos.yaml" | "other"
}

// Result is the outcome of Merge.
type Result struct {
	Commit    string     // merge commit on main when merged
	Conflicts []Conflict // non-empty → nothing changed
}

var branchRE = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// beforeUpdate runs after the merge commit is written and before main
// moves. Tests replace it to move main concurrently.
var beforeUpdate = func() {}

// Merge merges branch (a short name such as "what-if") into the workspace's
// main with git merge-tree --write-tree and always records a merge commit,
// authored by author, with main and the branch as parents.
//
// Every path git cannot merge needs a resolution in res; until all have
// one, Merge changes nothing and Result.Conflicts lists the paths still
// without one. A resolution for a path that has no conflict, or a malformed
// one, is ErrInvalid. The merged main is validated like any main update
// (*store.RejectedError) and moved only if it is still where the merge
// started (store.ErrConflict otherwise). A branch already contained in main
// returns main's commit and ignores res.
func Merge(st *store.Store, id, branch string, author gitrepo.Signature, res map[string]Resolution) (*Result, error) {
	if branch == "main" || !branchRE.MatchString(branch) || strings.Contains(branch, "..") ||
		strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, ".") || strings.HasPrefix(branch, "refs/") {
		return nil, fmt.Errorf("%w: %q is not a branch that can be merged into main", ErrInvalid, branch)
	}
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		return nil, err
	}
	ours, ok, err := repo.ResolveRef(mainRef)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("workspace %s has no main branch: %w", id, store.ErrNotFound)
	}
	theirs, ok, err := repo.ResolveRef("refs/heads/" + branch)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("branch %s: %w", branch, store.ErrNotFound)
	}
	if merged, err := repo.IsAncestor(theirs, ours); err != nil {
		return nil, err
	} else if merged {
		return &Result{Commit: ours}, nil
	}
	base, ok, err := repo.MergeBase(ours, theirs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: branch %s shares no history with main", store.ErrConflict, branch)
	}
	mt, err := repo.MergeTree(ours, theirs)
	if err != nil {
		return nil, err
	}
	p, err := planMerge(st, repo, base, ours, theirs, mt, res)
	if err != nil {
		return nil, err
	}
	if len(p.conflicts) > 0 {
		return &Result{Conflicts: p.conflicts}, nil
	}
	tree, err := repo.EditTree(mt.Tree, p.changes)
	if err != nil {
		return nil, err
	}
	commit, err := repo.CommitTree(tree, []string{ours, theirs}, author, fmt.Sprintf("Merge branch '%s'", branch))
	if err != nil {
		return nil, err
	}
	beforeUpdate()
	if err := st.SetRef(id, mainRef, commit, ours); err != nil {
		return nil, err
	}
	return &Result{Commit: commit}, nil
}

// plan is how a merge settles its conflicts.
type plan struct {
	changes   []gitrepo.Change // applied to the tree git merged
	conflicts []Conflict       // paths still without a resolution
	used      map[string]bool  // paths whose resolution was applied
}

// planMerge settles every path git could not merge, by its resolution or
// as an open conflict.
func planMerge(st *store.Store, repo *gitrepo.Repo, base, ours, theirs string, mt *gitrepo.MergeTreeResult, res map[string]Resolution) (*plan, error) {
	p := &plan{used: map[string]bool{}}
	for _, c := range mt.Conflicts {
		if err := p.settle(c.Path, c.Ours, c.Theirs, res); err != nil {
			return nil, err
		}
	}
	if err := p.finish(res); err != nil {
		return nil, err
	}
	return p, nil
}

// settle applies the resolution for path, or records an open conflict.
// ours and theirs are nil when that side has no file.
func (p *plan) settle(path string, ours, theirs []byte, res map[string]Resolution) error {
	r, ok := res[path]
	if !ok {
		p.conflicts = append(p.conflicts, Conflict{Path: path, Ours: ours, Theirs: theirs, Kind: kindOf(path)})
		return nil
	}
	p.used[path] = true
	switch r.Side {
	case SideOurs, SideTheirs:
		if r.Content != nil {
			return fmt.Errorf("%w: %s: content is only allowed with side %q", ErrInvalid, path, SideContent)
		}
		data := ours
		if r.Side == SideTheirs {
			data = theirs
		}
		if data == nil {
			p.changes = append(p.changes, gitrepo.Change{Path: path, Delete: true})
		} else {
			p.changes = append(p.changes, gitrepo.Change{Path: path, Data: data})
		}
	case SideContent:
		if r.Content == nil {
			return fmt.Errorf("%w: %s: side %q needs content", ErrInvalid, path, SideContent)
		}
		p.changes = append(p.changes, gitrepo.Change{Path: path, Data: r.Content})
	default:
		return fmt.Errorf("%w: %s: side %q is not %s, %s or %s", ErrInvalid, path, r.Side, SideOurs, SideTheirs, SideContent)
	}
	return nil
}

// finish rejects resolutions for paths without a conflict and sorts the
// open conflicts by path.
func (p *plan) finish(res map[string]Resolution) error {
	for _, path := range slices.Sorted(maps.Keys(res)) {
		if !p.used[path] {
			return fmt.Errorf("%w: %s has no conflict to resolve", ErrInvalid, path)
		}
	}
	slices.SortFunc(p.conflicts, func(a, b Conflict) int { return strings.Compare(a.Path, b.Path) })
	return nil
}

func kindOf(path string) string {
	switch {
	case path == configPath:
		return KindConfig
	case strings.HasPrefix(path, "answers/"):
		return KindAnswer
	case strings.HasPrefix(path, "generated/"):
		return KindGenerated
	case strings.HasPrefix(path, "documents/"):
		return KindDocument
	}
	return KindOther
}
