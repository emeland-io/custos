package merge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/store"
)

// maxBody bounds fork and merge request bodies; resolutions carry file
// contents.
const maxBody = 64 << 20

// Register adds the fork, merge and branch endpoints (spec §4.4) to the
// REST API. Call it before a.Handler().
func Register(a *api.API, st *store.Store) {
	a.Handle("POST /api/workspaces/{id}/fork", func(w http.ResponseWriter, r *http.Request) { handleFork(st, w, r) })
	a.Handle("POST /api/workspaces/{id}/merge", func(w http.ResponseWriter, r *http.Request) { handleMerge(st, w, r) })
	a.Handle("GET /api/workspaces/{id}/branches", func(w http.ResponseWriter, r *http.Request) { handleBranches(st, w, r) })
}

type errorBody struct {
	Error string `json:"error"`
}

type conflictJSON struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Ours   []byte `json:"ours"`   // base64; null when main deleted the file
	Theirs []byte `json:"theirs"` // base64; null when the branch deleted the file
}

type conflictBody struct {
	Error     string         `json:"error"`
	Conflicts []conflictJSON `json:"conflicts"`
}

type resolutionJSON struct {
	Side    string `json:"side"`
	Content []byte `json:"content"` // base64
}

func handleFork(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.ID == "" {
		body.ID = uuid.NewString()
	}
	if err := Fork(st, r.PathValue("id"), body.ID, author); err != nil {
		writeError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, map[string]string{"id": body.ID})
}

func handleMerge(st *store.Store, w http.ResponseWriter, r *http.Request) {
	author, err := api.Author(r)
	if err != nil {
		api.WriteJSON(w, http.StatusUnauthorized, errorBody{err.Error()})
		return
	}
	var body struct {
		Branch      string                    `json:"branch"`
		Resolutions map[string]resolutionJSON `json:"resolutions"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	res := make(map[string]Resolution, len(body.Resolutions))
	for path, rj := range body.Resolutions {
		res[path] = Resolution{Side: rj.Side, Content: rj.Content}
	}
	result, err := Merge(st, r.PathValue("id"), body.Branch, author, res)
	if err != nil {
		writeError(w, err)
		return
	}
	if len(result.Conflicts) > 0 {
		cb := conflictBody{Error: fmt.Sprintf("merging %s into main needs a resolution for %d conflicting paths", body.Branch, len(result.Conflicts))}
		for _, c := range result.Conflicts {
			cb.Conflicts = append(cb.Conflicts, conflictJSON{Path: c.Path, Kind: c.Kind, Ours: c.Ours, Theirs: c.Theirs})
		}
		api.WriteJSON(w, http.StatusConflict, cb)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]string{"commit": result.Commit})
}

func handleBranches(st *store.Store, w http.ResponseWriter, r *http.Request) {
	bs, err := Branches(st, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, bs)
}

// decodeBody decodes a JSON object into v; an empty body leaves v unchanged.
// It writes the error response and returns false when the body is bad.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if errors.Is(err, io.EOF) {
		return true
	}
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
