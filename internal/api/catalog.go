package api

import (
	"fmt"
	"net/http"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/task"
)

type catalogJSON struct {
	Commit string            `json:"commit"`
	Groups []*groupJSON      `json:"groups"`
	Tasks  []taskVersionJSON `json:"tasks"`
}

type groupJSON struct {
	Slug     string      `json:"slug"`
	Title    string      `json:"title"`
	Children []childJSON `json:"children"`
}

// childJSON is one child of a group: a sub-group or a task id.
type childJSON struct {
	Group *groupJSON `json:"group,omitempty"`
	Task  string     `json:"task,omitempty"`
}

type taskVersionJSON struct {
	ID         string          `json:"id"`
	Version    string          `json:"version"`
	Title      string          `json:"title"`
	AnswerType task.AnswerType `json:"answer_type"`
	Choices    []string        `json:"choices,omitempty"`
	Previous   []task.Ref      `json:"previous"`
	Body       string          `json:"body"`
	Processor  string          `json:"processor,omitempty"`
}

type taskHistoryJSON struct {
	ID         string            `json:"id"`
	Current    string            `json:"current"` // "" when superseded
	Superseded bool              `json:"superseded"`
	Processor  string            `json:"processor,omitempty"`
	Versions   []taskVersionJSON `json:"versions"` // lowest first
}

// catalogAt loads the catalog tree of a commit. Commits on the catalog's
// main were validated when main moved, so load problems are not repeated.
func (a *API) catalogAt(commit string) (*catalog.Catalog, error) {
	fsys, err := a.st.CatalogRepo().TreeFS(commit)
	if err != nil {
		return nil, fmt.Errorf("catalog commit %q: %w", commit, err)
	}
	c, _ := catalog.Load(fsys)
	return c, nil
}

// catalogMain loads the catalog's main; nil when the catalog is empty.
func (a *API) catalogMain() (*catalog.Catalog, string, error) {
	oid, ok, err := a.st.CatalogRepo().ResolveRef(mainRef)
	if err != nil || !ok {
		return nil, "", err
	}
	c, err := a.catalogAt(oid)
	return c, oid, err
}

func versionToJSON(v *task.Version, processor string) taskVersionJSON {
	return taskVersionJSON{
		ID: v.ID, Version: v.Version, Title: v.Title, AnswerType: v.AnswerType, Choices: v.Choices,
		Previous: append([]task.Ref{}, v.Previous...), Body: v.Body, Processor: processor,
	}
}

func catalogToJSON(c *catalog.Catalog, commit string) catalogJSON {
	out := catalogJSON{Commit: commit, Groups: []*groupJSON{}, Tasks: []taskVersionJSON{}}
	if c == nil {
		return out
	}
	seen := map[string]bool{}
	for _, slug := range c.Index {
		if g := groupToJSON(c, slug, seen); g != nil {
			out.Groups = append(out.Groups, g)
		}
	}
	for _, id := range c.Tasks.Tasks() {
		if v, ok := c.Tasks.Current(id); ok {
			out.Tasks = append(out.Tasks, versionToJSON(v, c.Registry.Bindings[id]))
		}
	}
	return out
}

// groupToJSON converts a group and its sub-groups. seen guards against
// cycles, which rule 5 already rejects on main.
func groupToJSON(c *catalog.Catalog, slug string, seen map[string]bool) *groupJSON {
	g, ok := c.Groups[slug]
	if !ok || seen[slug] {
		return nil
	}
	seen[slug] = true
	out := &groupJSON{Slug: slug, Title: g.Title, Children: []childJSON{}}
	for _, ch := range g.Children {
		switch {
		case ch.Group != "":
			if sub := groupToJSON(c, ch.Group, seen); sub != nil {
				out.Children = append(out.Children, childJSON{Group: sub})
			}
		case ch.Task != "":
			out.Children = append(out.Children, childJSON{Task: ch.Task})
		}
	}
	return out
}

func (a *API) getCatalog(w http.ResponseWriter, r *http.Request) {
	c, commit, err := a.catalogMain()
	if err != nil {
		WriteError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, catalogToJSON(c, commit))
}

func (a *API) getCatalogTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, _, err := a.catalogMain()
	if err != nil {
		WriteError(w, err)
		return
	}
	if c == nil || !task.ValidID(id) || !c.Tasks.Has(id) {
		WriteError(w, errorf(http.StatusNotFound, "the catalog has no task %q", id))
		return
	}
	h := taskHistoryJSON{ID: id, Superseded: c.Tasks.Superseded(id), Processor: c.Registry.Bindings[id], Versions: []taskVersionJSON{}}
	if cur, ok := c.Tasks.Current(id); ok {
		h.Current = cur.Version
	}
	for _, v := range c.Tasks.Versions(id) {
		h.Versions = append(h.Versions, versionToJSON(v, ""))
	}
	WriteJSON(w, http.StatusOK, h)
}
