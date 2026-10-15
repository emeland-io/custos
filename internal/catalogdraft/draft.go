package catalogdraft

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

// Draft is one catalog draft: a branch of the catalog repository.
type Draft struct {
	ID     string // UUID v4
	Branch string // custos/draft/<ID>, a short branch name (no refs/heads/ prefix)
	Title  string
	Author gitrepo.Signature // who created it
	Base   string            // main's commit when the draft was created
	Commit string            // current head of the branch
}

// ref returns the draft's full ref name.
func (d Draft) ref() string { return "refs/heads/" + d.Branch }

// draftMeta is the content of metaPath.
type draftMeta struct {
	ID     string            `json:"id"`
	Title  string            `json:"title"`
	Author gitrepo.Signature `json:"author"`
	Base   string            `json:"base"`
}

// Create starts a new draft from the catalog's current main.
func Create(st *store.Store, author gitrepo.Signature, title string) (*Draft, error) {
	title = strings.TrimSpace(strings.SplitN(title, "\n", 2)[0])
	if title == "" {
		return nil, fmt.Errorf("%w: title must not be empty", ErrInvalid)
	}
	cat := st.CatalogRepo()
	// Read once, only to fill in the metadata Create returns; the commit
	// store.UpdateCatalog actually writes resolves main again, fresh,
	// under the lock, so a push landing in between affects at worst which
	// commit this Draft.Base names, never the branch's real parent — and a
	// stale Base only makes Publish choose the three-way-merge path
	// instead of the (also correct) fast-forward one.
	base, ok, err := cat.ResolveRef(mainRef)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("the catalog has no main branch yet; push the catalog first: %w", store.ErrConflict)
	}
	id := uuid.NewString()
	branch := BranchPrefix + id
	data, err := json.Marshal(draftMeta{ID: id, Title: title, Author: author, Base: base})
	if err != nil {
		return nil, err
	}
	commit, err := st.UpdateCatalog("refs/heads/"+branch, author, "Start catalog draft: "+title,
		func(fs.FS) ([]gitrepo.Change, error) {
			return []gitrepo.Change{{Path: metaPath, Data: data}}, nil
		})
	if err != nil {
		return nil, err
	}
	return &Draft{ID: id, Branch: branch, Title: title, Author: author, Base: base, Commit: commit}, nil
}

// Get returns draft id, read from its branch's own metadata marker.
func Get(st *store.Store, id string) (*Draft, error) {
	if !task.ValidID(id) {
		return nil, fmt.Errorf("%w: draft %q", store.ErrNotFound, id)
	}
	branch := BranchPrefix + id
	cat := st.CatalogRepo()
	tip, ok, err := cat.ResolveRef("refs/heads/" + branch)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: draft %s", store.ErrNotFound, id)
	}
	data, ok, err := cat.ReadFile(tip, metaPath)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("draft %s: %s is missing", id, metaPath)
	}
	var meta draftMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("draft %s: %s: %w", id, metaPath, err)
	}
	return &Draft{ID: id, Branch: branch, Title: meta.Title, Author: meta.Author, Base: meta.Base, Commit: tip}, nil
}

// List returns every open draft, sorted by id.
func List(st *store.Store) ([]Draft, error) {
	refs, err := st.CatalogRepo().Refs("refs/heads/" + BranchPrefix)
	if err != nil {
		return nil, err
	}
	var ids []string
	for name := range refs {
		id := strings.TrimPrefix(name, "refs/heads/"+BranchPrefix)
		if task.ValidID(id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	drafts := make([]Draft, 0, len(ids))
	for _, id := range ids {
		d, err := Get(st, id)
		if errors.Is(err, store.ErrNotFound) {
			continue // raced with a concurrent Delete
		}
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, *d)
	}
	return drafts, nil
}

// Delete discards draft id. author is accepted for symmetry with every
// other mutating draft endpoint, which calls api.Author first, but is not
// recorded anywhere: deleting a ref writes no commit.
func Delete(st *store.Store, id string, author gitrepo.Signature) error {
	d, err := Get(st, id)
	if err != nil {
		return err
	}
	unlock := st.Lock(catalogLock)
	defer unlock()
	cat := st.CatalogRepo()
	tip, ok, err := cat.ResolveRef(d.ref())
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: draft %s", store.ErrNotFound, id)
	}
	if err := cat.DeleteRef(d.ref(), tip); err != nil {
		if errors.Is(err, gitrepo.ErrRefMoved) {
			return fmt.Errorf("%w: %v", store.ErrConflict, err)
		}
		return err
	}
	return nil
}
