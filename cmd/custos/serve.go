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

	"github.com/emeland-io/custos/internal/auth"
	"github.com/emeland-io/custos/internal/catalogdraft"
	"github.com/emeland-io/custos/internal/config"
	"github.com/emeland-io/custos/internal/distribute"
	"github.com/emeland-io/custos/internal/merge"
	"github.com/emeland-io/custos/internal/server"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/webui"
	"github.com/spf13/cobra"
)

// defaultAddr listens on loopback only, the conservative default for a
// server that is not deliberately placed behind a reverse proxy.
const defaultAddr = "127.0.0.1:8080"

// defaultPublicURL is where clients reach a server on defaultAddr.
const defaultPublicURL = "http://127.0.0.1:8080"

func runServe(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dataDir := fl.String("data-dir", os.Getenv("CUSTOS_DATA_DIR"), "directory holding the repositories (env CUSTOS_DATA_DIR)")
	addr := fl.String("addr", envOr("CUSTOS_ADDR", defaultAddr),
		"listen address (env CUSTOS_ADDR); every request still needs a signed-in caller or a valid token, "+
			"but TLS is the operator's own job (a reverse proxy, typically) — custos itself only ever speaks plain HTTP")
	publicURL := publicURLFlag(fl)
	procFlags := defineProcessorFlags(fl)
	authFlags := defineAuthFlags(fl)
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	procOpts, err := procFlags.options()
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 2
	}
	return serveWith(*dataDir, *addr, *publicURL, procOpts, authFlags.values(), stdout, stderr)
}

// serveWith checks the remaining settings, opens the data directory and serves it.
func serveWith(dataDir, addr, publicURL string, procOpts processorOptions, av authValues, stdout, stderr io.Writer) int {
	if dataDir == "" {
		fmt.Fprintln(stderr, "custos serve: --data-dir or CUSTOS_DATA_DIR is required")
		return 2
	}
	if err := checkPublicURL(publicURL); err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 2
	}
	authCfg, err := av.config(publicURL)
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 2
	}
	// The context also stops the processor workers; runs it interrupts are
	// queued again at the next start.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv, err := openServer(ctx, dataDir, publicURL, procOpts, authCfg, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 1
	}
	h, err := srv.Handler()
	if err != nil {
		fmt.Fprintf(stderr, "custos serve: %v\n", err)
		return 1
	}
	return serveUntilDone(ctx, srv, h, addr, dataDir, stdout, stderr)
}

// shutdownTimeout bounds how long serveUntilDone waits, once ctx is done,
// for in-flight HTTP requests to finish and for background work the
// server registered with OnShutdown (the run service's workers) to
// actually stop.
const shutdownTimeout = 10 * time.Second

// serveUntilDone binds addr, serves h, and blocks until either the HTTP
// server fails or ctx is cancelled (see the comment on runServe's ctx).
// On cancellation it shuts the HTTP server down and waits for srv's
// OnShutdown callbacks — in particular the run service's WaitInFlight,
// registered by startRuns — concurrently, against one shared deadline: a
// run interrupted by ctx's cancellation still needs to kill and remove its
// container and finish updating its record before the process may exit,
// or the container is left behind as orphaned and the run, left "running"
// on disk, is executed again at the next start regardless. WaitInFlight
// only waits for work actually in progress, not for the whole queue to
// drain, so an ordinary backlog beyond the worker count does not by
// itself hold shutdown to the full deadline below. If that deadline does
// pass before the background work finishes, serveUntilDone logs a
// warning and returns anyway, rather than hang the process forever over a
// stuck worker or container runtime.
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
func openServer(ctx context.Context, dataDir, publicURL string, procOpts processorOptions, authCfg auth.Config, stderr io.Writer) (*server.Server, error) {
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
	// authFlags.config builds authCfg without knowing dataDir (it has no
	// --data-dir flag of its own; that flag lives in runServe), so fill it
	// in here from openServer's own dataDir before auth.NewManager, which
	// requires it.
	authCfg.DataDir = dataDir
	authMgr, err := auth.NewManager(authCfg)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	auth.Register(a, authMgr)
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
	reconcile := startDistribution(st, a, srv, stderr)
	catalogdraft.Register(a, st, reconcile)
	startRuns(ctx, st, a, srv, svc)
	srv.WithAPI(a.Handler())
	ui, err := webui.Handler()
	if err != nil {
		return nil, err
	}
	srv.WithUI(ui)
	srv.WithAuth(a.Handler())
	srv.WithMiddleware(authMgr.Middleware("/healthz", "/auth/login", "/auth/callback", "/"))
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

// serveKeys are the flags of serve that also have an environment variable.
var serveKeys = []string{"data-dir", "addr", "public-url", "container-runtime", "secrets-dir", "processor-memory",
	"trusted-keys", "processor-workers", "max-generation-depth", "oidc-issuer-url", "oidc-client-id",
	"oidc-client-secret", "oidc-redirect-url", "admin-emails", "session-secret"}

func (c *cli) serveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve [flags]",
		Short: "serve the repositories over HTTP",
		// The old dispatcher ignored stray arguments; so does this.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := config.New(cmd.Flags(), serveKeys, nil)
			if err != nil {
				fmt.Fprintf(c.stderr, "custos serve: %v\n", err)
				return exitCode(1)
			}
			procOpts, err := processorOptionsFrom(s)
			if err != nil {
				fmt.Fprintf(c.stderr, "custos serve: %v\n", err)
				return exitCode(2)
			}
			av := authValues{issuerURL: s.String("oidc-issuer-url"), clientID: s.String("oidc-client-id"),
				clientSecret: s.String("oidc-client-secret"), redirectURL: s.String("oidc-redirect-url"),
				adminEmails: s.String("admin-emails"), sessionSecret: s.String("session-secret")}
			return result(serveWith(s.String("data-dir"), s.String("addr"), s.String("public-url"), procOpts, av, c.stdout, c.stderr))
		},
	}
	d := defaultProcessorOptions()
	f := cmd.Flags()
	f.String("data-dir", "", "directory holding the repositories (env CUSTOS_DATA_DIR)")
	f.String("addr", defaultAddr, "listen address (env CUSTOS_ADDR); every request still needs a signed-in caller or a valid token, "+
		"but TLS is the operator's own job (a reverse proxy, typically) — custos itself only ever speaks plain HTTP")
	f.String("public-url", defaultPublicURL, "base URL clients reach the server at, written into new workspaces (env CUSTOS_PUBLIC_URL)")
	f.String("container-runtime", d.runtime, "docker, podman, or the path of a compatible client that runs processors (env CUSTOS_CONTAINER_RUNTIME)")
	f.String("secrets-dir", "", "directory whose file <name> is mounted as processor secret <name>; empty: no secrets (env CUSTOS_SECRETS_DIR)")
	f.String("processor-memory", d.memory, "memory limit of a processor container (env CUSTOS_PROCESSOR_MEMORY)")
	f.String("trusted-keys", "", "directory of public keys (*.pem, *.pub) that verify signed documents; empty: none (env CUSTOS_TRUSTED_KEYS)")
	f.Int("processor-workers", d.workers, "processor runs executed at the same time (env CUSTOS_PROCESSOR_WORKERS)")
	f.Int("max-generation-depth", d.maxDepth, "levels of generated tasks allowed below a catalog task (env CUSTOS_MAX_GENERATION_DEPTH)")
	f.String("oidc-issuer-url", "", "OIDC issuer URL; empty runs custos in token-only mode, with no /auth/login (env CUSTOS_OIDC_ISSUER_URL)")
	f.String("oidc-client-id", "", "OIDC client id (env CUSTOS_OIDC_CLIENT_ID)")
	f.String("oidc-client-secret", "", "OIDC client secret (env CUSTOS_OIDC_CLIENT_SECRET)")
	f.String("oidc-redirect-url", "", "URL the identity provider redirects back to; default <public-url>/auth/callback (env CUSTOS_OIDC_REDIRECT_URL)")
	f.String("admin-emails", "", "comma-separated emails that always have the admin role, in addition to users.json (env CUSTOS_ADMIN_EMAILS)")
	f.String("session-secret", "", "hex-encoded HMAC key for session cookies, at least 32 bytes (64 hex digits); random and unique to this process if empty, so restarting forgets every session (env CUSTOS_SESSION_SECRET)")
	return cmd
}
