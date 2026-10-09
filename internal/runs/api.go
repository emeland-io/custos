package runs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/proposal"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

// maxBody bounds the JSON bodies of these endpoints.
const maxBody = 64 << 10

// Register adds the processor, run, output proposal and dry-run endpoints
// to a. Call it before a.Handler().
func Register(a *api.API, s *Service) {
	a.Handle("GET /api/processors", func(w http.ResponseWriter, r *http.Request) {
		ps, err := s.Processors()
		if err != nil {
			writeError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, ps)
	})
	a.Handle("GET /api/workspaces/{id}/runs", func(w http.ResponseWriter, r *http.Request) {
		rs, err := s.Runs(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, rs)
	})
	a.Handle("GET /api/workspaces/{id}/runs/{run}", func(w http.ResponseWriter, r *http.Request) {
		rec, _, err := s.Get(r.PathValue("id"), r.PathValue("run"))
		if err != nil {
			writeError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, rec)
	})
	a.Handle("GET /api/workspaces/{id}/runs/{run}/log", func(w http.ResponseWriter, r *http.Request) {
		_, log, err := s.Get(r.PathValue("id"), r.PathValue("run"))
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(log)
	})
	a.Handle("POST /api/workspaces/{id}/runs/{run}/retry", func(w http.ResponseWriter, r *http.Request) {
		if _, err := api.Author(r); err != nil {
			writeError(w, err)
			return
		}
		rec, err := s.Retry(r.PathValue("id"), r.PathValue("run"))
		if err != nil {
			writeError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusAccepted, rec)
	})
	a.Handle("GET /api/workspaces/{id}/processor-proposals", func(w http.ResponseWriter, r *http.Request) {
		ps, err := proposal.List(s.st, r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := make([]ProposalJSON, 0, len(ps))
		for _, p := range ps {
			out = append(out, proposalJSON(p))
		}
		api.WriteJSON(w, http.StatusOK, out)
	})
	a.Handle("GET /api/workspaces/{id}/processor-proposals/{task}", func(w http.ResponseWriter, r *http.Request) {
		tid, err := taskParam(r)
		if err != nil {
			writeError(w, err)
			return
		}
		p, err := proposal.Get(s.st, r.PathValue("id"), tid)
		if err != nil {
			writeError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, proposalJSON(*p))
	})
	a.Handle("POST /api/workspaces/{id}/processor-proposals/{task}/accept", func(w http.ResponseWriter, r *http.Request) {
		author, err := api.Author(r)
		if err != nil {
			writeError(w, err)
			return
		}
		tid, err := taskParam(r)
		if err != nil {
			writeError(w, err)
			return
		}
		var body struct {
			Items []string `json:"items"`
		}
		if err := decodeBody(w, r, &body, true); err != nil {
			writeError(w, err)
			return
		}
		commit, err := proposal.Accept(s.st, r.PathValue("id"), tid, body.Items, author)
		if err != nil && commit == "" {
			writeError(w, err)
			return
		}
		res := map[string]string{"commit": commit}
		if err != nil {
			// The commit landed on main; only deleting the proposal branch or
			// opening a cascade proposal failed. Report it without hiding the commit.
			res["warning"] = err.Error()
		}
		api.WriteJSON(w, http.StatusOK, res)
	})
	a.Handle("POST /api/workspaces/{id}/processor-proposals/{task}/reject", func(w http.ResponseWriter, r *http.Request) {
		if _, err := api.Author(r); err != nil {
			writeError(w, err)
			return
		}
		tid, err := taskParam(r)
		if err != nil {
			writeError(w, err)
			return
		}
		if err := proposal.Reject(s.st, r.PathValue("id"), tid); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	a.Handle("POST /api/processors/{name}/dry-run", func(w http.ResponseWriter, r *http.Request) {
		if _, err := api.Author(r); err != nil {
			writeError(w, err)
			return
		}
		var body struct {
			Image string `json:"image"`
		}
		if err := decodeBody(w, r, &body, false); err != nil {
			writeError(w, err)
			return
		}
		id, err := s.DryRun(r.PathValue("name"), body.Image)
		if err != nil {
			writeError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusAccepted, map[string]string{"id": id})
	})
	a.Handle("GET /api/dry-runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		rep, err := s.DryRunStatus(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, rep)
	})
}

// taskParam returns the path value "task"; anything but a task UUID is an
// unknown proposal.
func taskParam(r *http.Request) (string, error) {
	id := r.PathValue("task")
	if !task.ValidID(id) {
		return "", fmt.Errorf("no open proposal for task %q: %w", id, store.ErrNotFound)
	}
	return id, nil
}

// decodeBody decodes a JSON object into v, rejecting unknown fields and
// trailing data; an empty body is allowed when optional is set. Errors
// wrap ErrInvalid, or are *http.MaxBytesError.
func decodeBody(w http.ResponseWriter, r *http.Request, v any, optional bool) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.Is(err, io.EOF) && optional:
		return nil
	case errors.Is(err, io.EOF):
		return fmt.Errorf("%w: request body is empty; send a JSON object", ErrInvalid)
	case errors.As(err, &tooLarge):
		return err
	case err != nil:
		return fmt.Errorf("%w: request body: %v", ErrInvalid, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: request body: unexpected data after the JSON object", ErrInvalid)
	}
	return nil
}

// writeError answers ErrInvalid and proposal.ErrInvalid with 400 and
// leaves the rest to api.WriteError.
func writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrInvalid) || errors.Is(err, proposal.ErrInvalid) {
		api.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	api.WriteError(w, err)
}
