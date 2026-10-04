package main

import (
	"fmt"
	"io"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/distribute"
	"github.com/emeland-io/custos/internal/server"
	"github.com/emeland-io/custos/internal/store"
)

// startDistribution adds the freeze and pin proposal endpoints to a,
// distributes catalog changes after every push to catalog.git, and
// distributes once now to catch up with commits made while the server was
// down (ruling 2.5). Call it before a.Handler() and srv.Handler(). Failures
// are written to stderr and never stop the server (spec §7).
func startDistribution(st *store.Store, a *api.API, srv *server.Server, stderr io.Writer) {
	distribute.Register(a, st)
	reconcile := func() {
		if err := distribute.Reconcile(st); err != nil {
			fmt.Fprintf(stderr, "custos serve: distributing the catalog: %v\n", err)
		}
	}
	srv.OnCatalogPush(reconcile)
	reconcile()
}
