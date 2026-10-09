package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/attest/carabiner"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/runner"
	"github.com/emeland-io/custos/internal/runs"
	"github.com/emeland-io/custos/internal/server"
	"github.com/emeland-io/custos/internal/store"
)

// processorOptions are the processor flags of serve.
type processorOptions struct {
	runtime     string // docker, podman, or a path to the client binary
	secretsDir  string // "" = no secrets available
	memory      string
	trustedKeys string // "" = no trusted keys
	workers     int
	maxDepth    int
}

// defaultProcessorOptions are the defaults of the processor flags.
func defaultProcessorOptions() processorOptions {
	return processorOptions{runtime: "docker", memory: "512m", workers: 2, maxDepth: 8}
}

// processorFlags holds the processor flags while serve parses them.
type processorFlags struct {
	runtime, secretsDir, memory, trustedKeys *string
	workers, maxDepth                        *int
	envErr                                   error // malformed numbers in the environment
}

// defineProcessorFlags defines the processor flags of serve on fl, with
// defaults taken from the environment.
func defineProcessorFlags(fl *flag.FlagSet) *processorFlags {
	d := defaultProcessorOptions()
	workers, errW := envInt("CUSTOS_PROCESSOR_WORKERS", d.workers)
	depth, errD := envInt("CUSTOS_MAX_GENERATION_DEPTH", d.maxDepth)
	return &processorFlags{
		runtime:     fl.String("container-runtime", envOr("CUSTOS_CONTAINER_RUNTIME", d.runtime), "docker, podman, or the path of a compatible client that runs processors (env CUSTOS_CONTAINER_RUNTIME)"),
		secretsDir:  fl.String("secrets-dir", os.Getenv("CUSTOS_SECRETS_DIR"), "directory whose file <name> is mounted as processor secret <name>; empty: no secrets (env CUSTOS_SECRETS_DIR)"),
		memory:      fl.String("processor-memory", envOr("CUSTOS_PROCESSOR_MEMORY", d.memory), "memory limit of a processor container (env CUSTOS_PROCESSOR_MEMORY)"),
		trustedKeys: fl.String("trusted-keys", os.Getenv("CUSTOS_TRUSTED_KEYS"), "directory of public keys (*.pem, *.pub) that verify signed documents; empty: none (env CUSTOS_TRUSTED_KEYS)"),
		workers:     fl.Int("processor-workers", workers, "processor runs executed at the same time (env CUSTOS_PROCESSOR_WORKERS)"),
		maxDepth:    fl.Int("max-generation-depth", depth, "levels of generated tasks allowed below a catalog task (env CUSTOS_MAX_GENERATION_DEPTH)"),
		envErr:      errors.Join(errW, errD),
	}
}

// options checks the parsed flags.
func (f *processorFlags) options() (processorOptions, error) {
	o := processorOptions{runtime: *f.runtime, secretsDir: *f.secretsDir, memory: *f.memory,
		trustedKeys: *f.trustedKeys, workers: *f.workers, maxDepth: *f.maxDepth}
	switch {
	case f.envErr != nil:
		return o, f.envErr
	case o.runtime == "":
		return o, errors.New("--container-runtime must not be empty")
	case o.memory == "":
		return o, errors.New("--processor-memory must not be empty")
	case o.workers < 1:
		return o, fmt.Errorf("--processor-workers must be at least 1, got %d", o.workers)
	case o.maxDepth < 1:
		return o, fmt.Errorf("--max-generation-depth must be at least 1, got %d", o.maxDepth)
	}
	return o, nil
}

// envInt reads an integer from the environment; unset or empty gives
// fallback.
func envInt(name string, fallback int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback, fmt.Errorf("%s=%q is not a whole number", name, v)
	}
	return n, nil
}

// openRuns returns the run service of st, with attachments from
// <data-dir>/blobs, the container runtime and limits of o, and documents
// verified against the keys in o.trustedKeys.
func openRuns(st *store.Store, o processorOptions) (*runs.Service, error) {
	bl, err := blobs.Open(filepath.Join(st.DataDir(), "blobs"))
	if err != nil {
		return nil, err
	}
	v, err := carabiner.New(o.trustedKeys)
	if err != nil {
		return nil, fmt.Errorf("--trusted-keys: %w", err)
	}
	rn := runner.New(runner.Config{Runtime: o.runtime, SecretsDir: o.secretsDir, Memory: o.memory})
	return runs.New(st, bl, rn, v, runs.Config{Workers: o.workers, MaxDepth: o.maxDepth})
}

// startRuns adds the run endpoints to a, scans a workspace for answers to
// run after every move of its main through the store (answers, pin moves,
// accepted proposals, merges) and after every push to it, starts the
// workers, and scans every workspace once to catch up with changes made
// while the server was down. Call it after startDistribution, so the
// start-up scan sees the pins distribution moved.
//
// It also registers svc.Wait with srv.OnShutdown, so that whoever drives
// graceful shutdown (runServe's serveUntilDone) can wait for the workers
// to actually finish, not just be told to stop: a worker killed mid-run by
// ctx's cancellation still removes its container and finishes updating
// the record on disk before Wait returns, and the process must not exit
// before that happens or the container is left behind as orphaned.
func startRuns(ctx context.Context, st *store.Store, a *api.API, srv *server.Server, svc *runs.Service) {
	runs.Register(a, svc)
	st.OnMainMoved(svc.Scan)
	srv.OnWorkspacePush(svc.Scan)
	srv.OnShutdown(svc.Wait)
	svc.Start(ctx)
	svc.ScanAll()
}

// rerunAfterMerge is merge.Register's onRerun: it queues forced runs of the
// tasks whose generated output conflicted in a merge (§4.4).
func rerunAfterMerge(svc *runs.Service, stderr io.Writer) func(id string, tasks []string) {
	return func(id string, tasks []string) {
		if _, err := svc.Rerun(id, tasks, runs.ReasonMerge); err != nil {
			fmt.Fprintf(stderr, "custos serve: workspace %s: rerunning processors after a merge: %v\n", id, err)
		}
	}
}
