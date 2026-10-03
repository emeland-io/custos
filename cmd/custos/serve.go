package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/emeland-io/custos/internal/server"
)

// defaultAddr listens on loopback only: phase 1 has no authentication.
const defaultAddr = "127.0.0.1:8080"

func runServe(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dataDir := fl.String("data-dir", os.Getenv("CUSTOS_DATA_DIR"), "directory holding the repositories (env CUSTOS_DATA_DIR)")
	addr := fl.String("addr", envOr("CUSTOS_ADDR", defaultAddr), "listen address (env CUSTOS_ADDR); there is no authentication yet, so keep it on loopback unless the network is trusted")
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	if *dataDir == "" {
		fmt.Fprintln(stderr, "custos serve: --data-dir or CUSTOS_DATA_DIR is required")
		return 2
	}
	srv, err := openServer(*dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 1
	}
	h, err := srv.Handler()
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hs := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	fmt.Fprintf(stdout, "custos listening on %s, repositories in %s\n", *addr, *dataDir)
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "custos serve: %v\n", err)
			return 1
		}
		return 0
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := hs.Shutdown(shutdown); err != nil {
			fmt.Fprintf(stderr, "custos serve: %v\n", err)
			return 1
		}
		return 0
	}
}

// openServer opens the data directory with hooks that run this binary.
func openServer(dataDir string) (*server.Server, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return server.Open(dataDir, exe)
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
