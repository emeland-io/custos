package main

import (
	"path/filepath"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/store"
)

// openAPI returns the REST API of st, with attachments in <data-dir>/blobs
// (ruling 2.7). openServer adds the endpoints of other packages to it and
// then mounts it with srv.WithAPI(a.Handler()).
func openAPI(st *store.Store) (*api.API, error) {
	bl, err := blobs.Open(filepath.Join(st.DataDir(), "blobs"))
	if err != nil {
		return nil, err
	}
	return api.New(st, bl), nil
}
