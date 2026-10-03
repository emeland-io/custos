package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

type workspaceJSON struct {
	ID     string `json:"id"`
	Pin    string `json:"pin"`
	Frozen bool   `json:"frozen"`
}

type workspaceIDJSON struct {
	ID string `json:"id"`
}

func (a *API) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	ids, err := a.st.WorkspaceIDs()
	if err != nil {
		WriteError(w, err)
		return
	}
	out := []workspaceJSON{}
	for _, id := range ids {
		cfg, err := a.config(id)
		if err != nil {
			WriteError(w, err)
			return
		}
		out = append(out, workspaceJSON{ID: id, Pin: cfg.Catalog.Commit, Frozen: cfg.Frozen})
	}
	WriteJSON(w, http.StatusOK, out)
}

// config reads custos.yaml from main of a workspace without loading the
// catalog, so one broken workspace does not break the list. A workspace
// without main yet (a fork in progress) has the zero config.
func (a *API) config(id string) (workspace.Config, error) {
	var cfg workspace.Config
	repo, err := a.st.WorkspaceRepo(id)
	if err != nil {
		return cfg, err
	}
	oid, ok, err := repo.ResolveRef(mainRef)
	if err != nil || !ok {
		return cfg, err
	}
	data, ok, err := repo.ReadFile(oid, "custos.yaml")
	if err != nil || !ok {
		return cfg, err
	}
	// main is validated on every update; should custos.yaml be broken anyway,
	// the list shows what could be read and the status endpoint reports it.
	_ = frontmatter.DecodeStrict(data, &cfg)
	return cfg, nil
}

// createWorkspace creates a workspace pinned to the catalog's main
// (ruling 2.13). Without an id in the body, a UUID v4 is generated.
func (a *API) createWorkspace(w http.ResponseWriter, r *http.Request) {
	author, err := Author(r)
	if err != nil {
		WriteError(w, err)
		return
	}
	var req workspaceIDJSON
	if err := decodeJSON(w, r, &req, true); err != nil {
		WriteError(w, err)
		return
	}
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	if !task.ValidID(req.ID) {
		WriteError(w, errorf(http.StatusBadRequest, "workspace id %q is not a lowercase UUID v4", req.ID))
		return
	}
	if _, ok, err := a.st.CatalogRepo().ResolveRef(mainRef); err != nil {
		WriteError(w, err)
		return
	} else if !ok {
		WriteError(w, errorf(http.StatusConflict, "the catalog is empty; push its main branch before creating workspaces"))
		return
	}
	if err := a.st.CreateWorkspace(req.ID, author); err != nil {
		WriteError(w, err)
		return
	}
	WriteJSON(w, http.StatusCreated, workspaceIDJSON{ID: req.ID})
}
