package main

import (
	"fmt"
	"io"

	"github.com/emeland-io/custos/internal/config"
)

const (
	image = "ghcr.io/emeland-io/custos:latest"
	// hostPort is the port the message suggests publishing the web server
	// on.
	hostPort = "9090"
)

// writeBanner explains how to run the container image: how to keep the
// data directories in volumes and how to reach the web server.
func writeBanner(w io.Writer, cfg config.Config) {
	port := cfg.ListenPort()
	fmt.Fprintf(w, `
custos is running in a container and listens on port %[1]s.

Keep your data by mapping both data directories to volumes:
  root dir  %[2]s  roots, nodes and leaves (can be a git checkout)
  work dir  %[3]s  seeds, shoots, attestations and trusted keys

Publish the web server on port %[4]s of the host:

  docker run -p %[4]s:%[1]s \
    -v custos-root:%[2]s \
    -v custos-work:%[3]s \
    %[5]s

Then open http://localhost:%[4]s. To listen on another port inside the
container, add --port <port> and change the right side of -p to match.
Hide this message with --no-banner or CUSTOS_NO_BANNER=true.

`, port, cfg.RootDir, cfg.WorkDir, hostPort, image)
}
