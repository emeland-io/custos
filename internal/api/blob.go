package api

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/emeland-io/custos/internal/blobs"
)

type blobJSON struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// postBlob stores the raw request body as a blob (rulings 2.7, 2.8).
func (a *API) postBlob(w http.ResponseWriter, r *http.Request) {
	if _, err := Author(r); err != nil {
		WriteError(w, err)
		return
	}
	if r.ContentLength > blobs.MaxSize {
		WriteError(w, errorf(http.StatusRequestEntityTooLarge, "blob is larger than %d bytes", blobs.MaxSize))
		return
	}
	sha, size, err := a.bl.Put(r.Body)
	if err != nil {
		WriteError(w, err)
		return
	}
	WriteJSON(w, http.StatusCreated, blobJSON{SHA256: sha, Size: size})
}

// getBlob serves a blob. Blobs never change, so clients may cache them.
func (a *API) getBlob(w http.ResponseWriter, r *http.Request) {
	sha := r.PathValue("sha256")
	rc, size, err := a.bl.Open(sha)
	if errors.Is(err, fs.ErrNotExist) {
		WriteError(w, errorf(http.StatusNotFound, "unknown blob %q", sha))
		return
	}
	if err != nil {
		WriteError(w, err)
		return
	}
	defer rc.Close()
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.FormatInt(size, 10))
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		io.Copy(w, rc)
	}
}
