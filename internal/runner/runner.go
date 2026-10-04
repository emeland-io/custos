// Package runner runs processor images through the docker or podman
// command-line client (spec §5.2): read-only root filesystem, no network
// unless asked for, a memory limit, no capabilities, secrets and attachments
// mounted read-only, and a timeout.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/contract"
)

// ErrUnavailable means the container could not be started; it is wrapped
// with the reason.
var ErrUnavailable = errors.New("container could not be started")

// MaxLogSize is how much of stderr a Result keeps.
const MaxLogSize = 1 << 20

const truncatedNote = "\n[log truncated]\n"

// killGrace is how long Run waits for the client to return after it killed
// the container, before it kills the client too.
const killGrace = 10 * time.Second

var (
	secretRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	shaRE    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Config configures a Runner.
type Config struct {
	Runtime        string        // "docker" (default) or "podman", or a path to the binary
	SecretsDir     string        // secret <name> is the file <SecretsDir>/<name>
	Memory         string        // container memory limit, default "512m"
	DefaultTimeout time.Duration // default 60s; used when Job.Timeout == 0
}

// Job is one processor run.
type Job struct {
	Image   string // "<ref>@sha256:<hex>" as in processors.yaml, or a local image reference (processor test)
	Timeout time.Duration
	Network bool
	Secrets []string          // mounted read-only at /run/secrets/<name>
	Blobs   map[string]string // sha256 → host file; mounted read-only at /input/blobs/<sha256>
	Input   []byte            // stdin
}

// Result is what a finished container left behind.
type Result struct {
	Stdout   []byte // at most contract.MaxOutputSize+1 bytes are kept
	Log      []byte // stderr, at most 1 MiB kept, then "\n[log truncated]\n"
	ExitCode int    // -1 when TimedOut
	TimedOut bool
	Duration time.Duration
}

// Runner starts processor containers.
type Runner struct {
	cfg Config
}

// New returns a Runner; empty fields of cfg get their defaults.
func New(cfg Config) *Runner {
	if cfg.Runtime == "" {
		cfg.Runtime = "docker"
	}
	if cfg.Memory == "" {
		cfg.Memory = "512m"
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = 60 * time.Second
	}
	return &Runner{cfg: cfg}
}

// Run starts the container with a read-only root filesystem, no network
// unless Job.Network, the memory limit, no capabilities, no new privileges,
// a small tmpfs at /tmp, and the mounts above; kills it when the timeout
// expires. A missing secret, an image that cannot be found or pulled, or a
// missing runtime are ErrUnavailable; a non-zero exit or timeout is reported
// in Result, not as an error. A cancelled ctx kills the container and
// returns ctx's error.
func (r *Runner) Run(ctx context.Context, job Job) (*Result, error) {
	mounts, err := r.mounts(job)
	if err != nil {
		return nil, err
	}
	ref, _, err := r.Resolve(ctx, job.Image)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("processor run cancelled: %w", ctx.Err())
		}
		return nil, err
	}
	if err := r.ensureImage(ctx, ref); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("processor run cancelled: %w", ctx.Err())
		}
		return nil, err
	}
	name := "custos-run-" + uuid.NewString()
	args := []string{"run", "--name", name, "--interactive", "--quiet", "--pull", "never",
		"--read-only",
		"--memory", r.cfg.Memory, "--memory-swap", r.cfg.Memory,
		"--pids-limit", "256",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--tmpfs", "/tmp:rw,noexec,nosuid,size=64m",
	}
	if !job.Network {
		args = append(args, "--network", "none")
	}
	for _, m := range mounts {
		args = append(args, "--mount", m)
	}
	args = append(args, ref)

	timeout := job.Timeout
	if timeout <= 0 {
		timeout = r.cfg.DefaultTimeout
	}
	stdout := &capped{limit: contract.MaxOutputSize + 1}
	stderr := &capped{limit: MaxLogSize, note: truncatedNote}
	cmd := exec.Command(r.cfg.Runtime, args...)
	cmd.Stdin = bytes.NewReader(job.Input)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer r.remove(name)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var stopped error // why Run killed the container, nil when it ended by itself
	select {
	case <-done:
	case <-timer.C:
		stopped = context.DeadlineExceeded
	case <-ctx.Done():
		stopped = ctx.Err()
	}
	if stopped != nil {
		r.kill(name)
		select {
		case <-done:
		case <-time.After(killGrace):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	res := &Result{Stdout: stdout.buf.Bytes(), Log: stderr.bytes(), Duration: time.Since(start)}
	if stopped == context.DeadlineExceeded {
		res.TimedOut, res.ExitCode = true, -1
		return res, nil
	}
	if stopped != nil {
		return nil, fmt.Errorf("processor run cancelled: %w", stopped)
	}
	state, err := r.state(name)
	if err != nil {
		// No container: the client could not create it (bad mount, image gone).
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, reason(stderr.bytes(), err))
	}
	if state.Error != "" {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, state.Error)
	}
	if state.Running {
		// cmd.Wait returned (done closed) but the container itself is still
		// running: the client process died or lost its daemon connection
		// without the container actually exiting. Reporting state.ExitCode
		// here (typically 0) would misreport this as a successful empty run.
		return nil, fmt.Errorf("%w: client exited but the container is still running", ErrUnavailable)
	}
	res.ExitCode = state.ExitCode
	if state.OOMKilled {
		res.Log = append(res.Log, []byte("\n[killed: out of memory, limit "+r.cfg.Memory+"]\n")...)
	}
	return res, nil
}

// mounts checks the secrets and blobs of job and returns the --mount values.
func (r *Runner) mounts(job Job) ([]string, error) {
	var out []string
	for _, s := range job.Secrets {
		if !secretRE.MatchString(s) {
			return nil, fmt.Errorf("%w: secret name %q must match [a-z0-9][a-z0-9-]*", ErrUnavailable, s)
		}
		if r.cfg.SecretsDir == "" {
			return nil, fmt.Errorf("%w: secret %s is not available: no secrets directory is configured", ErrUnavailable, s)
		}
		m, err := bind(filepath.Join(r.cfg.SecretsDir, s), "/run/secrets/"+s)
		if err != nil {
			return nil, fmt.Errorf("%w: secret %s: %v", ErrUnavailable, s, err)
		}
		out = append(out, m)
	}
	for sha, path := range job.Blobs {
		if !shaRE.MatchString(sha) {
			return nil, fmt.Errorf("%w: blob %q is not 64 lowercase hex digits", ErrUnavailable, sha)
		}
		m, err := bind(path, contract.BlobPath(sha))
		if err != nil {
			return nil, fmt.Errorf("%w: attachment %s: %v", ErrUnavailable, sha, err)
		}
		out = append(out, m)
	}
	return out, nil
}

// bind returns a read-only bind mount of the regular file src at dst.
func bind(src, dst string) (string, error) {
	abs, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", abs)
	}
	if strings.ContainsAny(abs, ",\"\n") {
		return "", fmt.Errorf("path %q contains a comma, quote or newline, which a mount cannot name", abs)
	}
	return "type=bind,source=" + abs + ",target=" + dst + ",readonly", nil
}

type containerState struct {
	ExitCode  int    `json:"ExitCode"`
	Error     string `json:"Error"`
	OOMKilled bool   `json:"OOMKilled"`
	Running   bool   `json:"Running"`
}

func (r *Runner) state(name string) (containerState, error) {
	var st containerState
	out, err := r.output(context.Background(), "container", "inspect", "--format", "{{json .State}}", name)
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return st, fmt.Errorf("reading container state: %v", err)
	}
	return st, nil
}

func (r *Runner) kill(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), killGrace)
	defer cancel()
	_, _ = r.output(ctx, "kill", name)
}

func (r *Runner) remove(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = r.output(ctx, "rm", "--force", name)
}

// output runs the runtime client and returns its stdout; the error carries
// its stderr.
func (r *Runner) output(ctx context.Context, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, r.cfg.Runtime, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.New(reason(stderr.Bytes(), err))
	}
	return stdout.Bytes(), nil
}

// reason is the client's error message, or err when it printed none.
func reason(stderr []byte, err error) string {
	if msg := strings.TrimSpace(string(stderr)); msg != "" {
		return msg
	}
	return err.Error()
}

// capped is an io.Writer that keeps the first limit bytes and drops the
// rest, so a chatty container cannot exhaust memory and never blocks.
type capped struct {
	buf       bytes.Buffer
	limit     int
	note      string
	truncated bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room < len(p) {
		c.buf.Write(p[:max(room, 0)])
		c.truncated = true
	} else {
		c.buf.Write(p)
	}
	return len(p), nil
}

func (c *capped) bytes() []byte {
	if c.truncated && c.note != "" {
		return append(bytes.Clone(c.buf.Bytes()), c.note...)
	}
	return c.buf.Bytes()
}
