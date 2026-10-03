package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/store"
)

// httpError is an error that carries its HTTP status.
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func errorf(status int, format string, args ...any) error {
	return &httpError{status: status, msg: fmt.Sprintf(format, args...)}
}

type problemJSON struct {
	Path    string `json:"path"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

type errorJSON struct {
	Error    string        `json:"error"`
	Problems []problemJSON `json:"problems,omitempty"`
}

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// WriteError writes err as an error body with the status the architecture
// note assigns to it: 422 with problems for validation, 404 for unknown
// things, 409 for conflicts, 413 for oversized bodies, 500 otherwise.
func WriteError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	body := errorJSON{Error: err.Error()}
	var he *httpError
	var rejected *store.RejectedError
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &he):
		code = he.status
	case errors.As(err, &rejected):
		code = http.StatusUnprocessableEntity
		for _, p := range rejected.Problems {
			body.Problems = append(body.Problems, problemJSON{Path: p.Path, Rule: p.Rule, Message: p.Message})
		}
	case errors.Is(err, store.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, store.ErrExists), errors.Is(err, store.ErrConflict):
		code = http.StatusConflict
	case errors.Is(err, blobs.ErrTooLarge), errors.As(err, &tooLarge):
		code = http.StatusRequestEntityTooLarge
	}
	WriteJSON(w, code, body)
}
