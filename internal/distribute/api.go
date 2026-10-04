package distribute

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
)

// Register adds the freeze and pin proposal endpoints to a. Call it before
// a.Handler().
func Register(a *api.API, st *store.Store) {
	a.Handle("POST /api/workspaces/{id}/freeze", frozenHandler(st, true))
	a.Handle("POST /api/workspaces/{id}/unfreeze", frozenHandler(st, false))
	a.Handle("GET /api/workspaces/{id}/proposals", func(w http.ResponseWriter, r *http.Request) {
		ps, err := Proposals(st, r.PathValue("id"))
		if err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, ps)
	})
	a.Handle("POST /api/workspaces/{id}/proposals/accept", func(w http.ResponseWriter, r *http.Request) {
		author, ok := requireAuthor(w, r)
		if !ok {
			return
		}
		branch, ok := readBranch(w, r)
		if !ok {
			return
		}
		if err := Accept(st, r.PathValue("id"), branch, author); err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]string{"branch": branch})
	})
	a.Handle("POST /api/workspaces/{id}/proposals/reject", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAuthor(w, r); !ok {
			return
		}
		branch, ok := readBranch(w, r)
		if !ok {
			return
		}
		if err := Reject(st, r.PathValue("id"), branch); err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]string{"branch": branch})
	})
	a.Handle("GET /api/workspaces/{id}/pin-diff", func(w http.ResponseWriter, r *http.Request) {
		pd, err := pinDiff(st, r.PathValue("id"))
		if err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, pd)
	})
}

func frozenHandler(st *store.Store, frozen bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		author, ok := requireAuthor(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		set := Unfreeze
		if frozen {
			set = Freeze
		}
		resp := map[string]any{"id": id, "frozen": frozen}
		if err := set(st, id, author); err != nil {
			var de *DistributeError
			if !errors.As(err, &de) {
				api.WriteError(w, err)
				return
			}
			// The flag was already committed; tell the client it worked,
			// with a warning, instead of answering as if it had not.
			resp["warning"] = de.Cause.Error()
		}
		api.WriteJSON(w, http.StatusOK, resp)
	}
}

// requireAuthor answers 401 when the X-Custos-Author header is missing or
// malformed (ruling 2.6), using api.WriteError so the body matches every
// other error response.
func requireAuthor(w http.ResponseWriter, r *http.Request) (gitrepo.Signature, bool) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteError(w, err)
		return gitrepo.Signature{}, false
	}
	return author, true
}

// readBranch reads the body {"branch": "custos/pin/<catalog commit>"} and
// answers 400 when it is malformed.
func readBranch(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Branch string `json:"branch"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || body.Branch == "" {
		api.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": `body must be {"branch": "custos/pin/<catalog commit>"}`})
		return "", false
	}
	return body.Branch, true
}

// pinDiffResponse compares a workspace's pin with the catalog's main.
type pinDiffResponse struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Changes Diff   `json:"changes"`
}

func pinDiff(st *store.Store, id string) (pinDiffResponse, error) {
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		return pinDiffResponse{}, err
	}
	main, ok, err := repo.ResolveRef(mainRef)
	if err != nil {
		return pinDiffResponse{}, err
	}
	if !ok {
		return pinDiffResponse{}, fmt.Errorf("workspace %s has no main branch: %w", id, store.ErrNotFound)
	}
	cfg, err := readConfig(repo, main)
	if err != nil {
		return pinDiffResponse{}, err
	}
	head, ok, err := st.CatalogRepo().ResolveRef(mainRef)
	if err != nil {
		return pinDiffResponse{}, err
	}
	if !ok {
		return pinDiffResponse{}, fmt.Errorf("the catalog has no main branch: %w", store.ErrNotFound)
	}
	d, err := CatalogDiff(st.CatalogRepo(), cfg.Catalog.Commit, head)
	if err != nil {
		return pinDiffResponse{}, err
	}
	return pinDiffResponse{From: cfg.Catalog.Commit, To: head, Changes: d}, nil
}
