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

	"github.com/emeland-io/custos/internal/distribute"
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
	procFlags := defineProcessorFlags(fl)
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	procOpts, err := procFlags.options()
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 2
	}
	if *dataDir == "" {
		fmt.Fprintln(stderr, "custos serve: --data-dir or CUSTOS_DATA_DIR is required")
		return 2
	}
	if err := checkPublicURL(*publicURL); err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 2
	}
	// The context also stops the processor workers; runs it interrupts are
	// queued again at the next start.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv, err := openServer(ctx, *dataDir, *publicURL, procOpts, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 1
	}
	h, err := srv.Handler()
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 1
	}
	return serveUntilDone(ctx, srv, h, *addr, *dataDir, stdout, stderr)
}

// shutdownTimeout bounds how long serveUntilDone waits, once ctx is done,
// for in-flight HTTP requests to finish and for background work the
// server registered with OnShutdown (the run service's workers) to
// actually stop.
const shutdownTimeout = 10 * time.Second

// serveUntilDone binds addr, serves h, and blocks until either the HTTP
// server fails or ctx is cancelled (see the comment on runServe's ctx).
// On cancellation it shuts the HTTP server down and waits for srv's
// OnShutdown callbacks — in particular the run service's Wait, registered
// by startRuns — concurrently, against one shared deadline: a run
// interrupted by ctx's cancellation still needs to kill and remove its
// container and finish updating its record before the process may exit,
// or the container is left behind as orphaned and the run, left "running"
// on disk, is executed again at the next start regardless. If that
// deadline passes before the background work finishes, serveUntilDone
// logs a warning and returns anyway, rather than hang the process
// forever over a stuck worker or container runtime.
func serveUntilDone(ctx context.Context, srv *server.Server, h http.Handler, addr, dataDir string, stdout, stderr io.Writer) int {
	// Bind before announcing, so "listening" is only printed when it is true.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 1
	}
	hs := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	fmt.Fprintf(stdout, "custos listening on %s, repositories in %s\n", addr, dataDir)
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "custos serve: %v\n", err)
			return 1
		}
		return 0
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		waited := make(chan struct{})
		go func() {
			srv.WaitForShutdown(shutdown)
			close(waited)
		}()
		httpErr := hs.Shutdown(shutdown)
		<-waited
		if httpErr != nil {
			fmt.Fprintf(stderr, "custos serve: %v\n", httpErr)
			return 1
		}
		if shutdown.Err() != nil {
			fmt.Fprintln(stderr, "custos serve: background work (such as a processor run) did not stop before the shutdown timeout; exiting anyway")
		}
		return 0
	}
}

// openServer opens the data directory, points all hooks at this binary and
// reports workspaces whose main breaks the rules, for example after an edit
// on disk (spec section 7); it serves them anyway. It starts the processor
// workers, which stop when ctx is cancelled.
func openServer(ctx context.Context, dataDir, publicURL string, procOpts processorOptions, stderr io.Writer) (*server.Server, error) {
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
	svc, err := openRuns(st, procOpts)
	if err != nil {
		return nil, err
	}
	merge.Register(a, st, func(id string) {
		if err := distribute.ReconcileWorkspace(st, id); err != nil {
			fmt.Fprintf(stderr, "custos serve: workspace %s: %v\n", id, err)
		}
		svc.Scan(id) // a fork's main does not move through the store
	}, rerunAfterMerge(svc, stderr))
	srv := server.New(st)
	startDistribution(st, a, srv, stderr)
	startRuns(ctx, st, a, srv, svc)
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
