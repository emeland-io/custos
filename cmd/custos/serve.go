package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/emeland-io/custos/internal/merge"
	"github.com/emeland-io/custos/internal/server"
	"github.com/emeland-io/custos/internal/store"
)

// defaultAddr listens on loopback only: there is no authentication yet.
const defaultAddr = "127.0.0.1:8080"

// defaultPublicURL is where clients reach a server on defaultAddr.
const defaultPublicURL = "http://127.0.0.1:8080"

func runServe(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dataDir := fl.String("data-dir", os.Getenv("CUSTOS_DATA_DIR"), "directory holding the repositories (env CUSTOS_DATA_DIR)")
	addr := fl.String("addr", envOr("CUSTOS_ADDR", defaultAddr), "listen address (env CUSTOS_ADDR); there is no authentication yet, so keep it on loopback unless the network is trusted")
	publicURL := publicURLFlag(fl)
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	if *dataDir == "" {
		fmt.Fprintln(stderr, "custos serve: --data-dir or CUSTOS_DATA_DIR is required")
		return 2
	}
	if err := checkPublicURL(*publicURL); err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 2
	}
	srv, err := openServer(*dataDir, *publicURL, stderr)
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
	// Bind before announcing, so "listening" is only printed when it is true.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 1
	}
	hs := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
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

// openServer opens the data directory, points all hooks at this binary and
// reports workspaces whose main breaks the rules, for example after an edit
// on disk (spec section 7); it serves them anyway.
func openServer(dataDir, publicURL string, stderr io.Writer) (*server.Server, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(dataDir, exe, publicURL)
	if err != nil {
		return nil, err
	}
	if err := st.InstallHooks(); err != nil {
		return nil, err
	}
	ids, err := st.WorkspaceIDs()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		ps, err := st.CheckWorkspace(id)
		if err != nil {
			fmt.Fprintf(stderr, "custos serve: workspace %s: %v\n", id, err)
		}
		for _, p := range ps {
			fmt.Fprintf(stderr, "custos serve: workspace %s is inconsistent: %s\n", id, p)
		}
	}
	a, err := openAPI(st)
	if err != nil {
		return nil, err
	}
	merge.Register(a, st)
	srv := server.New(st)
	startDistribution(st, a, srv, stderr)
	srv.WithAPI(a.Handler())
	return srv, nil
}

// publicURLFlag defines --public-url.
func publicURLFlag(fl *flag.FlagSet) *string {
	return fl.String("public-url", envOr("CUSTOS_PUBLIC_URL", defaultPublicURL), "base URL clients reach the server at, written into new workspaces (env CUSTOS_PUBLIC_URL)")
}

// checkPublicURL accepts absolute http and https URLs without query.
func checkPublicURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(s, "'") {
		return fmt.Errorf("--public-url %q is not an absolute http or https URL such as %s", s, defaultPublicURL)
	}
	return nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
