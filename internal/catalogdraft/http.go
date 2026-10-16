package catalogdraft

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"regexp"

	"go.yaml.in/yaml/v3"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/auth"
	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/merge"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

// maxBody bounds every request body this package decodes.
const maxBody = 4 << 20

// slugRE matches a valid group slug. Mirrors catalog's own (unexported)
// slugRE; kept as its own copy here rather than exported from catalog
// purely for this, since it is a one-line regular expression, not a
// behaviour worth sharing code for (see "Decisions beyond the architecture
// note"). This check runs before the slug ever reaches a file path, which
// catalog.Check's post-hoc validation does not protect against (ruling
// 4.13 lets a draft be invalid, but never lets a request write outside
// groups/<slug>/).
var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Register adds the nine draft endpoints (architecture note, Plan 4b) to
// the REST API, each gated by auth.Require("task-author", ...). Call it
// before a.Handler(), same convention as merge.Register. onPublish, which
// may be nil, is called synchronously after a successful Publish (never on
// a 409 or other error) — the catalog's analogue of merge.Register's
// onMainMoved, added during cross-plan review so a draft publish
// reconciles pinned workspaces immediately instead of waiting for an
// unrelated future catalog push (architecture note ruling 17; see
// "Decisions beyond the architecture note").
func Register(a *api.API, st *store.Store, onPublish func()) {
	a.Handle("GET /api/catalog/drafts", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleList(st, w, r) }))
	a.Handle("POST /api/catalog/drafts", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleCreate(st, w, r) }))
	a.Handle("GET /api/catalog/drafts/{id}", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleView(st, w, r) }))
	a.Handle("PUT /api/catalog/drafts/{id}/tasks/{task}", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleTaskEdit(st, w, r) }))
	a.Handle("PUT /api/catalog/drafts/{id}/groups/{slug}", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleGroupEdit(st, w, r) }))
	a.Handle("PUT /api/catalog/drafts/{id}/groups-index", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleGroupsIndexEdit(st, w, r) }))
	a.Handle("PUT /api/catalog/drafts/{id}/processors", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleProcessorsEdit(st, w, r) }))
	a.Handle("DELETE /api/catalog/drafts/{id}", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handleDelete(st, w, r) }))
	a.Handle("POST /api/catalog/drafts/{id}/publish", auth.Require("task-author", func(w http.ResponseWriter, r *http.Request) { handlePublish(st, onPublish, w, r) }))
}

// --- JSON shapes ---

type errorBody struct {
	Error string `json:"error"`
}

type authorJSON struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type draftJSON struct {
	ID     string     `json:"id"`
	Branch string     `json:"branch"`
	Title  string     `json:"title"`
	Author authorJSON `json:"author"`
	Base   string     `json:"base"`
	Commit string     `json:"commit"`
}

func draftToJSON(d *Draft) draftJSON {
	return draftJSON{
		ID: d.ID, Branch: d.Branch, Title: d.Title,
		Author: authorJSON{Name: d.Author.Name, Email: d.Author.Email},
		Base:   d.Base, Commit: d.Commit,
	}
}

type problemJSON struct {
	Path    string `json:"path"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

type draftViewJSON struct {
	Draft    draftJSON     `json:"draft"`
	Catalog  any           `json:"catalog"`
	Problems []problemJSON `json:"problems"`
}

type conflictJSON struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Ours   []byte `json:"ours"`
	Theirs []byte `json:"theirs"`
}

type conflictBody struct {
	Error     string         `json:"error"`
	Conflicts []conflictJSON `json:"conflicts"`
}

type resolutionJSON struct {
	Side    string `json:"side"`
	Content []byte `json:"content"`
}

type publishResultJSON struct {
	Commit string `json:"commit"`
}

// --- draft lifecycle ---

func handleList(st *store.Store, w http.ResponseWriter, r *http.Request) {
	drafts, err := List(st)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	out := make([]draftJSON, 0, len(drafts))
	for i := range drafts {
		out = append(out, draftToJSON(&drafts[i]))
	}
	api.WriteJSON(w, http.StatusOK, out)
}

func handleCreate(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	d, err := Create(st, author, body.Title)
	if err != nil {
		writeError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, draftToJSON(d))
}

func handleView(st *store.Store, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := Get(st, id)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	fsys, err := st.CatalogRepo().TreeFS(d.Commit)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	c, loadProblems := catalog.Load(fsys)
	problems := append(append([]problemJSON{}, toProblemJSON(loadProblems)...), toProblemJSON(c.Validate())...)
	api.WriteJSON(w, http.StatusOK, draftViewJSON{
		Draft: draftToJSON(d), Catalog: api.CatalogJSON(c, d.Commit), Problems: problems,
	})
}

func handleDelete(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	if err := Delete(st, r.PathValue("id"), author); err != nil {
		api.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- edits ---

func handleTaskEdit(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	taskID := r.PathValue("task")
	if !task.ValidID(taskID) {
		api.WriteJSON(w, http.StatusNotFound, errorBody{"unknown task " + taskID})
		return
	}
	var body struct {
		Step       string          `json:"step"`
		Title      string          `json:"title"`
		Body       string          `json:"body"`
		AnswerType task.AnswerType `json:"answer_type"`
		Choices    []string        `json:"choices"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	commit, err := Update(st, r.PathValue("id"), author, "Edit task "+taskID,
		func(tree fs.FS) ([]gitrepo.Change, error) {
			return catalog.NewVersionChanges(tree, taskID, semver.Step(body.Step), body.Title, body.Body, body.AnswerType, body.Choices)
		})
	if err != nil {
		writeError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]string{"commit": commit})
}

type childJSON struct {
	Group string `json:"group,omitempty"`
	Task  string `json:"task,omitempty"`
}

func (c childJSON) toCatalog() catalog.Child { return catalog.Child{Group: c.Group, Task: c.Task} }

type groupBodyJSON struct {
	Title    string      `json:"title"`
	Children []childJSON `json:"children"`
}

func handleGroupEdit(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	slug := r.PathValue("slug")
	if !slugRE.MatchString(slug) {
		api.WriteJSON(w, http.StatusBadRequest, errorBody{fmt.Sprintf("invalid group slug %q", slug)})
		return
	}
	var body groupBodyJSON
	if !decodeBody(w, r, &body) {
		return
	}
	g := catalog.Group{Title: body.Title}
	for _, c := range body.Children {
		g.Children = append(g.Children, c.toCatalog())
	}
	data, err := marshalYAML(struct {
		Title    string          `yaml:"title"`
		Children []catalog.Child `yaml:"children"`
	}{g.Title, g.Children})
	if err != nil {
		api.WriteError(w, err)
		return
	}
	commit, err := Update(st, r.PathValue("id"), author, "Edit group "+slug,
		func(fs.FS) ([]gitrepo.Change, error) {
			return []gitrepo.Change{{Path: "groups/" + slug + "/group.yaml", Data: data}}, nil
		})
	if err != nil {
		writeError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]string{"commit": commit})
}

func handleGroupsIndexEdit(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	var body struct {
		Groups []string `json:"groups"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	for _, slug := range body.Groups {
		if !slugRE.MatchString(slug) {
			api.WriteJSON(w, http.StatusBadRequest, errorBody{fmt.Sprintf("invalid group slug %q", slug)})
			return
		}
	}
	data, err := marshalYAML(struct {
		Groups []string `yaml:"groups"`
	}{body.Groups})
	if err != nil {
		api.WriteError(w, err)
		return
	}
	commit, err := Update(st, r.PathValue("id"), author, "Reorder top-level groups",
		func(fs.FS) ([]gitrepo.Change, error) {
			return []gitrepo.Change{{Path: "groups/index.yaml", Data: data}}, nil
		})
	if err != nil {
		writeError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]string{"commit": commit})
}

func handleProcessorsEdit(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	var registry catalog.Registry
	if !decodeBody(w, r, &registry) {
		return
	}
	data, err := marshalYAML(registry)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	commit, err := Update(st, r.PathValue("id"), author, "Edit the processor registry",
		func(fs.FS) ([]gitrepo.Change, error) {
			return []gitrepo.Change{{Path: "processors.yaml", Data: data}}, nil
		})
	if err != nil {
		writeError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]string{"commit": commit})
}

// --- publish ---

func handlePublish(st *store.Store, onPublish func(), w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	var body struct {
		Resolutions map[string]resolutionJSON `json:"resolutions"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	res := make(map[string]merge.Resolution, len(body.Resolutions))
	for path, rj := range body.Resolutions {
		res[path] = merge.Resolution{Side: rj.Side, Content: rj.Content}
	}
	result, err := Publish(st, r.PathValue("id"), author, res)
	if err != nil {
		writeError(w, err)
		return
	}
	if len(result.Conflicts) > 0 {
		cb := conflictBody{Error: fmt.Sprintf("publishing needs a resolution for %d conflicting path(s)", len(result.Conflicts))}
		for _, c := range result.Conflicts {
			cb.Conflicts = append(cb.Conflicts, conflictJSON{Path: c.Path, Kind: c.Kind, Ours: c.Ours, Theirs: c.Theirs})
		}
		api.WriteJSON(w, http.StatusConflict, cb)
		return
	}
	if onPublish != nil {
		onPublish()
	}
	api.WriteJSON(w, http.StatusOK, publishResultJSON{Commit: result.Commit})
}

// --- helpers ---

func toProblemJSON(ps []problem.Problem) []problemJSON {
	out := make([]problemJSON, 0, len(ps))
	for _, p := range ps {
		out = append(out, problemJSON{Path: p.Path, Rule: p.Rule, Message: p.Message})
	}
	return out
}

// marshalYAML writes v with two-space indentation, the same convention
// internal/workspace/config.go's MarshalConfig and
// internal/frontmatter/frontmatter.go's own marshalYAML use.
func marshalYAML(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// decodeBody decodes a JSON object into v. It writes the error response and
// returns false when the body is bad.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil && dec.Decode(&struct{}{}) != io.EOF {
		err = errors.New("unexpected data after the JSON object")
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		api.WriteJSON(w, http.StatusRequestEntityTooLarge, errorBody{fmt.Sprintf("request body is larger than %d bytes", maxBody)})
		return false
	case err != nil:
		api.WriteJSON(w, http.StatusBadRequest, errorBody{"malformed request body: " + err.Error()})
		return false
	}
	return true
}

// writeError answers ErrInvalid with 400 and leaves the rest to api.WriteError.
func writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrInvalid) {
		api.WriteJSON(w, http.StatusBadRequest, errorBody{err.Error()})
		return
	}
	api.WriteError(w, err)
}
