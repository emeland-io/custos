package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/task"
)

// authorHeader names the person a write is done for (ruling 2.6).
const authorHeader = "X-Custos-Author"

// maxJSONBytes bounds JSON request bodies. Answers are text; attachments
// travel separately through /api/blobs.
const maxJSONBytes = 16 << 20

// Author returns the person named by X-Custos-Author. Every write endpoint
// calls it first; its error makes WriteError answer 401.
func Author(r *http.Request) (gitrepo.Signature, error) {
	h := r.Header.Get(authorHeader)
	if h == "" {
		return gitrepo.Signature{}, errorf(http.StatusUnauthorized,
			"header %s is missing; send %s: Name <email>", authorHeader, authorHeader)
	}
	sig, err := gitrepo.ParseSignature(h)
	if err != nil {
		return gitrepo.Signature{}, errorf(http.StatusUnauthorized, "header %s: %v", authorHeader, err)
	}
	return sig, nil
}

// decodeJSON decodes the request body into v, rejecting unknown fields and
// data after the object. An empty body is allowed when optional is set.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any, optional bool) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBytes))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.Is(err, io.EOF) && optional:
		return nil
	case errors.Is(err, io.EOF):
		return errorf(http.StatusBadRequest, "request body is empty; send a JSON object")
	case errors.As(err, &tooLarge):
		return err
	case err != nil:
		return errorf(http.StatusBadRequest, "request body: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if errors.As(err, &tooLarge) {
			return err
		}
		return errorf(http.StatusBadRequest, "request body: unexpected data after the JSON object")
	}
	return nil
}

// workspaceID returns the path value "id". Anything but a workspace UUID is
// an unknown workspace, which also keeps paths such as ../x out of the store.
func workspaceID(r *http.Request) (string, error) {
	id := r.PathValue("id")
	if !task.ValidID(id) {
		return "", errorf(http.StatusNotFound, "unknown workspace %q", id)
	}
	return id, nil
}

// taskID returns the path value "task" when it is a task UUID.
func taskID(r *http.Request) (string, error) {
	id := r.PathValue("task")
	if !task.ValidID(id) {
		return "", errorf(http.StatusNotFound, "unknown task %q", id)
	}
	return id, nil
}

// answerPath is the path of the answer file of a task in a workspace.
func answerPath(taskID string) string { return "answers/" + taskID + ".md" }
