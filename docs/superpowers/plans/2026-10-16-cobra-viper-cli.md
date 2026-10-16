# Phase 5: cobra/viper command line Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace custos's hand-rolled `flag` dispatcher with spf13/cobra, and resolve settings through spf13/viper with the precedence config file < environment variable < command-line flag, without changing what any existing command does.

**Architecture:** A new cobra command tree (`cmd/custos/root.go` and one file per command) runs next to the old dispatcher behind the unchanged `run(args, stdin, stdout, stderr) int` seam. Each command is ported by moving the body that follows flag parsing into a plain "core" function that both implementations call. A command is routed to cobra the moment it is ported. A test-only switch forces the old dispatcher, so `make test` proves both agree. Settings are resolved by the new `internal/config` package, which wraps one viper instance per command run.

**Tech Stack:** Go 1.26, `github.com/spf13/cobra` v1.10.2 (already an indirect dependency), `github.com/spf13/pflag` v1.0.10 (already indirect), `github.com/spf13/viper` v1.21.0 (new), `go.yaml.in/yaml/v3` (existing).

**Spec:** [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md) §6.2 (CLI). The rulings below become §11.5. They come from a design interview with the project owner on 2026-10-16.

## Rulings made while planning (spec §11.5 gets these in Task 13)

| # | Ruling |
| --- | --- |
| 5.1 | cobra does dispatch and flag parsing; viper layers flag, environment and config file. One viper instance per command run, built by `internal/config`; the global viper is never used. |
| 5.2 | Precedence, lowest to highest: flag default, config file, environment variable, command-line flag. An empty environment variable counts as unset (as `envOr` did). An explicitly empty flag (`--data-dir=`) still wins. |
| 5.3 | The personal config file is `$XDG_CONFIG_HOME/custos/config.yaml` (only when absolute) or `~/.config/custos/config.yaml` on every platform, YAML, overridable with the global `--config FILE`. A missing file is fine. Only `server-url` is read from it; any other key is ignored with a warning on stderr that names the key and never prints its value. The file is written with mode 0600 in a 0700 directory, atomically. |
| 5.4 | Environment binding is a fixed allowlist (`config.EnvNames`): the 16 variables that exist today plus `CUSTOS_SERVER_URL`. Each command binds only the ones it bound before. Per-invocation flags (`--dir`, `--against`, `--email`, `--name`, `--admin`, …) are flag-only, so a stray `CUSTOS_ADMIN` cannot change what `token create` does. |
| 5.5 | `--server-url` / `CUSTOS_SERVER_URL` is a new client-side setting. It is not merged with the server-side `--public-url` / `CUSTOS_PUBLIC_URL`, and there is no alias. In this phase nothing but `custos config get` reads it; `workspace create` does not fall back to it. `--server-url` is a global flag so the flag layer is testable end to end. |
| 5.6 | The token stays `CUSTOS_TOKEN`, read by `internal/remote`, environment only. No `CUSTOS_USER_TOKEN`, no `--token`, nothing secret in the config file. |
| 5.7 | Preserved: flag names, exit codes (0 success, 1 failure, 2 misuse), the `run(...)` signature, pass-through arguments of `push` and `processor test`, the internal `hook pre-receive` command, and the one-line `usage: …` messages. Changed: `-h` help is cobra's, printed to stdout (was stderr); single-dash long flags (`-data-dir`) are no longer accepted (nothing in the repository uses them); a malformed `CUSTOS_PROCESSOR_WORKERS` is no longer reported when `--processor-workers` overrides it. |
| 5.8 | The old dispatcher stays next to the new one until a later cleanup. Tests select it through the unexported variable `legacyCLI`, set only by a `_test.go` file from `CUSTOS_TEST_LEGACY_CLI`; there is no user-visible switch. `make test` runs the `cmd/custos` tests in both modes. |
| 5.9 | New command `custos config get server-url [--show-source]` and `custos config set server-url URL`. |
| 5.10 | Deferred, not part of this plan: (a) delete the old dispatcher, `legacyCLI`, the legacy `runX` parsing wrappers, `envOr`/`envInt`, and the Makefile's second test run; (b) let `custos clone` accept a bare workspace name or UUID, resolved against the server URL. |

## Global Constraints

- Go module path `github.com/emeland-io/custos`; Go 1.26.0 (`go.mod`).
- Exit codes: 0 success, 1 failure, 2 misuse. Usage text goes to stderr; `token create` prints only the token to stdout.
- The `run(args []string, stdin io.Reader, stdout, stderr io.Writer) int` signature in `cmd/custos/main.go` does not change: ~20 test files call it through `runCmd`.
- No new `CUSTOS_*` variable except `CUSTOS_SERVER_URL`. `CUSTOS_TOKEN` is untouched.
- The config file may hold `server-url` only; secrets never go in it.
- `make test` (`go vet ./...`, `go test ./... -race -count=1`, SDK tests, web type check) must pass after every task. For tasks that touch only `cmd/custos`, the fast check is `go vet ./cmd/custos && go test ./cmd/custos -race -count=1` and the same with `CUSTOS_TEST_LEGACY_CLI=1` (Task 2 introduces it).
- Do not `git add` the unrelated working-tree changes already present at plan time (`internal/webui/dist/*`, `.vscode/`, `5b6c7d8e-…/`, `data/`). Stage files by name.
- Commit messages end with the attribution line given by the harness for this session.

## Review Focus

Inputs and conditions the spec implies but no obvious task test covers; each has its test in the named task.

1. A stray environment variable that is not on the allowlist (`CUSTOS_ADMIN=1`, `CUSTOS_EMAIL=…`) must not grant a role or fill a required flag in `token create` (Task 7).
2. A broken or hostile config file (invalid YAML, a directory in its place, `oidc-client-secret:` in it) must not stop commands that never read it, and must never have its secret value echoed in a warning (Tasks 1, 3, 4).
3. `HOME` unset and no `XDG_CONFIG_HOME`: `config get` must fail with exit 1 and a message, not panic (Task 3).
4. `custos push origin --force main` and `custos push -- --weird` must reach git untouched, and `--dir` before the remote must still be honored (Task 8).
5. `config set` into a path whose parent is a regular file must exit 1 and leave no temporary files (Tasks 1, 3).
6. A malformed `CUSTOS_PROCESSOR_WORKERS` must still exit 2 naming the variable when no flag overrides it (Task 11).

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/config/config.go` (new) | Env allowlist, viper-backed `Settings`, config file read/write, URL check. No cobra dependency. |
| `internal/config/config_test.go` (new) | Precedence, file filtering, atomic write. |
| `cmd/custos/root.go` (new) | `cli` struct, `exitCode`/`usageError`, `newRootCmd`, `runCobra`, `exactArgs`. |
| `cmd/custos/configcmd.go` (new) | `custos config get|set`. |
| `cmd/custos/main.go` | `run` routes to `runCobra` or the old `runLegacy`; `legacyCLI`, `portedCommands`. |
| `cmd/custos/legacy_hook_test.go` (new) | Sets `legacyCLI` from `CUSTOS_TEST_LEGACY_CLI`. |
| `cmd/custos/<cmd>.go` (existing, one per command) | Gains a `doX` core, a `xCmd` cobra constructor. The legacy `runX` stays, calling the same core. |
| `Makefile` | `test` runs `cmd/custos` tests a second time in legacy mode. |
| `README.md`, spec, phases file | Documentation (Task 13). |

---

### Task 1: Dependencies and the `internal/config` package

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces (all in package `config`):
  - `const KeyServerURL = "server-url"`
  - `var EnvNames map[string]string` (flag name → variable)
  - `type Source string`; constants `SourceDefault`, `SourceFile`, `SourceEnv`, `SourceFlag`
  - `type Options struct { Path string; Warn io.Writer }`
  - `func New(fl *pflag.FlagSet, keys []string, o *Options) (*Settings, error)`
  - `func (s *Settings) String(key string) string`
  - `func (s *Settings) Int(key string) (int, error)`
  - `func (s *Settings) Source(key string) Source`
  - `func ResolvePath(path string) (string, error)`, `func DefaultPath() (string, error)`
  - `func ReadFile(path string, warn io.Writer) (map[string]string, error)`
  - `func CheckURL(s string) error`
  - `func Set(path, key, value string) error`

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/spf13/viper@v1.21.0
go get github.com/spf13/cobra@v1.10.2 github.com/spf13/pflag@v1.0.10
```

Expected: `go.mod` lists viper as a direct requirement and cobra and pflag lose their `// indirect` comment only after Task 2 imports them (so `go mod tidy` is run in Task 2, not here).

- [ ] **Step 2: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// newFlags returns a parsed flag set with the flags the tests resolve.
func newFlags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	fl := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fl.String("server-url", "", "")
	fl.String("data-dir", "", "")
	fl.Int("processor-workers", 2, "")
	fl.String("dir", "", "") // not in EnvNames
	if err := fl.Parse(args); err != nil {
		t.Fatal(err)
	}
	return fl
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrecedence(t *testing.T) {
	const fileURL, envURL, flagURL = "https://file.example.org", "https://env.example.org", "https://flag.example.org"
	cases := []struct {
		name     string
		file     string // "" = no config file
		env      string
		args     []string
		want     string
		wantFrom Source
	}{
		{"nothing set", "", "", nil, "", SourceDefault},
		{"file only", fileURL, "", nil, fileURL, SourceFile},
		{"env beats file", fileURL, envURL, nil, envURL, SourceEnv},
		{"flag beats env", fileURL, envURL, []string{"--server-url", flagURL}, flagURL, SourceFlag},
		{"flag beats file", fileURL, "", []string{"--server-url", flagURL}, flagURL, SourceFlag},
		{"empty env is unset", fileURL, "", nil, fileURL, SourceFile},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("CUSTOS_SERVER_URL", c.env)
			path := filepath.Join(t.TempDir(), "absent.yaml")
			if c.file != "" {
				path = writeConfig(t, "server-url: "+c.file+"\n")
			}
			s, err := New(newFlags(t, c.args...), []string{"server-url"}, &Options{Path: path})
			if err != nil {
				t.Fatal(err)
			}
			if got := s.String("server-url"); got != c.want {
				t.Errorf("value %q, want %q", got, c.want)
			}
			if got := s.Source("server-url"); got != c.wantFrom {
				t.Errorf("source %q, want %q", got, c.wantFrom)
			}
		})
	}
}

func TestEnvBelowFlagWithoutFile(t *testing.T) {
	t.Setenv("CUSTOS_DATA_DIR", "/from/env")
	s, err := New(newFlags(t), []string{"data-dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.String("data-dir"); got != "/from/env" {
		t.Errorf("env: %q", got)
	}
	s, err = New(newFlags(t, "--data-dir", "/from/flag"), []string{"data-dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.String("data-dir"); got != "/from/flag" {
		t.Errorf("flag: %q", got)
	}
	// An explicit empty flag still wins over the environment, as before.
	s, err = New(newFlags(t, "--data-dir="), []string{"data-dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.String("data-dir"); got != "" {
		t.Errorf("empty flag: %q", got)
	}
}

func TestFileOnlyServerURL(t *testing.T) {
	t.Setenv("CUSTOS_DATA_DIR", "")
	path := writeConfig(t, "server-url: https://a.example.org\ndata-dir: /secret\noidc-client-secret: hunter2\n")
	var warn bytes.Buffer
	s, err := New(newFlags(t), []string{"server-url", "data-dir"}, &Options{Path: path, Warn: &warn})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.String("data-dir"); got != "" {
		t.Errorf("data-dir leaked from the file: %q", got)
	}
	for _, key := range []string{"data-dir", "oidc-client-secret"} {
		if !strings.Contains(warn.String(), `"`+key+`"`) {
			t.Errorf("no warning about %s:\n%s", key, warn.String())
		}
	}
	if strings.Contains(warn.String(), "hunter2") {
		t.Errorf("warning prints a secret value:\n%s", warn.String())
	}
}

func TestMalformedFile(t *testing.T) {
	path := writeConfig(t, "server-url: [unterminated\n")
	if _, err := New(newFlags(t), []string{"server-url"}, &Options{Path: path}); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("err %v", err)
	}
}

func TestUnlistedFlagHasNoEnv(t *testing.T) {
	if _, err := New(newFlags(t), []string{"dir"}, nil); err == nil {
		t.Error("binding a flag that is not in EnvNames must fail")
	}
}

func TestInt(t *testing.T) {
	t.Setenv("CUSTOS_PROCESSOR_WORKERS", "")
	s, _ := New(newFlags(t), []string{"processor-workers"}, nil)
	if n, err := s.Int("processor-workers"); err != nil || n != 2 {
		t.Errorf("default: %d %v", n, err)
	}
	t.Setenv("CUSTOS_PROCESSOR_WORKERS", "5")
	s, _ = New(newFlags(t), []string{"processor-workers"}, nil)
	if n, err := s.Int("processor-workers"); err != nil || n != 5 {
		t.Errorf("env: %d %v", n, err)
	}
	s, _ = New(newFlags(t, "--processor-workers", "7"), []string{"processor-workers"}, nil)
	if n, err := s.Int("processor-workers"); err != nil || n != 7 {
		t.Errorf("flag: %d %v", n, err)
	}
	t.Setenv("CUSTOS_PROCESSOR_WORKERS", "many")
	s, _ = New(newFlags(t), []string{"processor-workers"}, nil)
	if _, err := s.Int("processor-workers"); err == nil || !strings.Contains(err.Error(), "CUSTOS_PROCESSOR_WORKERS") {
		t.Errorf("malformed env: %v", err)
	}
	// A flag that overrides a malformed variable makes it irrelevant.
	s, _ = New(newFlags(t, "--processor-workers", "3"), []string{"processor-workers"}, nil)
	if n, err := s.Int("processor-workers"); err != nil || n != 3 {
		t.Errorf("flag over malformed env: %d %v", n, err)
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if p, _ := DefaultPath(); p != "/xdg/custos/config.yaml" {
		t.Errorf("xdg: %s", p)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative/is/ignored")
	t.Setenv("HOME", "/home/jane")
	if p, _ := DefaultPath(); p != "/home/jane/.config/custos/config.yaml" {
		t.Errorf("home: %s", p)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if _, err := DefaultPath(); err == nil {
		t.Error("no HOME and no XDG_CONFIG_HOME must be an error, not a guess")
	}
}

func TestSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	if err := Set(path, "data-dir", "/x"); err == nil {
		t.Error("only server-url may be set")
	}
	for _, bad := range []string{"", "ftp://x", "custos.example.org", "https://u:p@x", "https://x?q=1"} {
		if err := Set(path, KeyServerURL, bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if err := Set(path, KeyServerURL, "https://one.example.org"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", info, err)
	}
	if dir, _ := os.Stat(filepath.Dir(path)); dir.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", dir.Mode())
	}
	// Another entry survives a later set, and the old value is replaced.
	if err := os.WriteFile(path, []byte("server-url: https://one.example.org\nnote: keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Set(path, KeyServerURL, "https://two.example.org"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "https://two.example.org") || strings.Contains(string(data), "one.example.org") || !strings.Contains(string(data), "note: keep me") {
		t.Errorf("file:\n%s", data)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestSetUnderAFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "custos", "config.yaml")
	if err := Set(path, KeyServerURL, "https://one.example.org"); err == nil {
		t.Error("a parent that is a regular file must be an error")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(blocker), ".config-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/config -count=1`
Expected: FAIL to build, `undefined: New` (and the other identifiers).

- [ ] **Step 4: Write the implementation**

Create `internal/config/config.go`:

```go
// Package config resolves the settings of the custos command line: each one
// comes from a flag, an environment variable or, for a few, the user's
// config file, and the layers win in that order.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.yaml.in/yaml/v3"
)

// KeyServerURL is the one setting the config file may hold.
const KeyServerURL = "server-url"

// EnvNames lists the flags that also have an environment variable, and the
// variable's name. It is a fixed list (ruling 5.4): a flag that is not here
// is never read from the environment, so a stray CUSTOS_EMAIL or
// CUSTOS_ADMIN cannot change what a command does.
var EnvNames = map[string]string{
	"server-url":           "CUSTOS_SERVER_URL",
	"data-dir":             "CUSTOS_DATA_DIR",
	"addr":                 "CUSTOS_ADDR",
	"public-url":           "CUSTOS_PUBLIC_URL",
	"container-runtime":    "CUSTOS_CONTAINER_RUNTIME",
	"secrets-dir":          "CUSTOS_SECRETS_DIR",
	"processor-memory":     "CUSTOS_PROCESSOR_MEMORY",
	"trusted-keys":         "CUSTOS_TRUSTED_KEYS",
	"processor-workers":    "CUSTOS_PROCESSOR_WORKERS",
	"max-generation-depth": "CUSTOS_MAX_GENERATION_DEPTH",
	"oidc-issuer-url":      "CUSTOS_OIDC_ISSUER_URL",
	"oidc-client-id":       "CUSTOS_OIDC_CLIENT_ID",
	"oidc-client-secret":   "CUSTOS_OIDC_CLIENT_SECRET",
	"oidc-redirect-url":    "CUSTOS_OIDC_REDIRECT_URL",
	"admin-emails":         "CUSTOS_ADMIN_EMAILS",
	"session-secret":       "CUSTOS_SESSION_SECRET",
	"author":               "CUSTOS_AUTHOR",
}

// fileKeys are the settings read from the config file. Everything else in
// the file is ignored with a warning.
var fileKeys = []string{KeyServerURL}

// Source names the layer a setting's value came from.
type Source string

const (
	SourceDefault Source = "default"
	SourceFile    Source = "file"
	SourceEnv     Source = "env"
	SourceFlag    Source = "flag"
)

// Options say whether and where to read the config file.
type Options struct {
	Path string    // config file; "" means DefaultPath()
	Warn io.Writer // receives the warnings about ignored keys
}

// Settings resolves the keys it was built for.
type Settings struct {
	v    *viper.Viper
	fs   *pflag.FlagSet
	env  map[string]string
	file map[string]bool
}

// New binds keys, flag names that must be in EnvNames, to their flags in
// fl (parsed already) and to their environment variables. A non-nil o also
// reads the config file for the keys in fileKeys.
func New(fl *pflag.FlagSet, keys []string, o *Options) (*Settings, error) {
	s := &Settings{v: viper.New(), fs: fl, env: map[string]string{}, file: map[string]bool{}}
	for _, k := range keys {
		env, ok := EnvNames[k]
		if !ok {
			return nil, fmt.Errorf("config: %q has no environment variable", k)
		}
		s.env[k] = env
		if err := s.v.BindEnv(k, env); err != nil {
			return nil, err
		}
		if f := fl.Lookup(k); f != nil {
			if err := s.v.BindPFlag(k, f); err != nil {
				return nil, err
			}
		}
	}
	if o == nil {
		return s, nil
	}
	path, err := ResolvePath(o.Path)
	if err != nil {
		return nil, err
	}
	vals, err := ReadFile(path, o.Warn)
	if err != nil {
		return nil, err
	}
	for _, k := range fileKeys {
		if val := vals[k]; val != "" {
			if err := s.v.MergeConfigMap(map[string]any{k: val}); err != nil {
				return nil, err
			}
			s.file[k] = true
		}
	}
	return s, nil
}

// String returns the value of key: its flag if given, else its environment
// variable if not empty, else its config file entry, else the flag's default.
func (s *Settings) String(key string) string { return s.v.GetString(key) }

// Int is String parsed as a whole number. A malformed environment variable
// is an error naming it; a malformed flag never gets this far.
func (s *Settings) Int(key string) (int, error) {
	raw := s.v.GetString(key)
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a whole number", s.env[key], raw)
	}
	return n, nil
}

// Source reports which layer String(key) came from.
func (s *Settings) Source(key string) Source {
	if f := s.fs.Lookup(key); f != nil && f.Changed {
		return SourceFlag
	}
	if env, ok := s.env[key]; ok && os.Getenv(env) != "" {
		return SourceEnv
	}
	if s.file[key] {
		return SourceFile
	}
	return SourceDefault
}

// ResolvePath returns path, or DefaultPath() when path is empty.
func ResolvePath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	return DefaultPath()
}

// DefaultPath is $XDG_CONFIG_HOME/custos/config.yaml, or
// ~/.config/custos/config.yaml; the XDG layout on every platform, macOS
// included.
func DefaultPath() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "custos", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "custos", "config.yaml"), nil
}

// ReadFile returns the entries of the config file at path that fileKeys
// allow. A missing file is empty. Any other key is left out, and warn (if
// not nil) is told, so a secret in a plain file is skipped visibly. The
// warning names the key and never prints its value.
func ReadFile(path string, warn io.Writer) (map[string]string, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("config file %s: %w", path, err)
	}
	vals := map[string]string{}
	for _, k := range v.AllKeys() {
		if k == KeyServerURL {
			vals[k] = v.GetString(k)
		} else if warn != nil {
			fmt.Fprintf(warn, "custos: config file %s: ignoring %q; only %q may be set there\n", path, k, KeyServerURL)
		}
	}
	return vals, nil
}

// CheckURL accepts absolute http and https URLs without user info, query
// or fragment.
func CheckURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%q is not an absolute http or https URL", s)
	}
	return nil
}

// Set writes key to the config file at path, keeping its other entries. The
// directory is created with mode 0700 and the file written with 0600
// through a temporary file and a rename, so a crash cannot leave half a file.
func Set(path, key, value string) error {
	if key != KeyServerURL {
		return fmt.Errorf("unknown key %q; only %q can be set", key, KeyServerURL)
	}
	if err := CheckURL(value); err != nil {
		return err
	}
	entries := map[string]any{}
	switch data, err := os.ReadFile(path); {
	case err == nil:
		if err := yaml.Unmarshal(data, &entries); err != nil {
			return fmt.Errorf("config file %s: %w", path, err)
		}
		if entries == nil {
			entries = map[string]any{}
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	entries[key] = value
	out, err := yaml.Marshal(entries)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp") // created with mode 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `gofmt -l internal/config && go vet ./internal/config && go test ./internal/config -race -count=1`
Expected: no `gofmt` output; `ok  github.com/emeland-io/custos/internal/config`.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/config
git commit -m "Add internal/config: flag, environment and config file layers on viper"
```

---

### Task 2: cobra root, the routing seam and the legacy switch

**Files:**
- Create: `cmd/custos/root.go`, `cmd/custos/legacy_hook_test.go`
- Modify: `cmd/custos/main.go`, `Makefile`, `go.mod` (via `go mod tidy`)
- Test: `cmd/custos/root_test.go`

**Interfaces:**
- Consumes: `internal/config` (Task 1) is not used yet.
- Produces (package `main`):
  - `type exitCode int` (an `error`); `type usageError string` (an `error`)
  - `func result(code int) error`
  - `func exactArgs(n int, usage string) cobra.PositionalArgs`
  - `type cli struct { stdin io.Reader; stdout, stderr io.Writer; configPath string }`
  - `func newRootCmd(c *cli) *cobra.Command`
  - `func runCobra(args []string, stdin io.Reader, stdout, stderr io.Writer) int`
  - `func runLegacy(args []string, stdin io.Reader, stdout, stderr io.Writer) int` (the old body of `run`)
  - `var legacyCLI bool`; `var portedCommands = map[string]bool{}`

- [ ] **Step 1: Write the failing test**

Create `cmd/custos/root_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

// cobraCmd runs args through the cobra tree directly, whatever legacyCLI says.
func cobraCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runCobra(args, nil, &out, &errb)
	return code, out.String(), errb.String()
}

func TestRunCobraRoot(t *testing.T) {
	if code, _, errs := cobraCmd(t); code != 2 || !strings.Contains(errs, "Usage:") {
		t.Errorf("no args: %d %q", code, errs)
	}
	if code, _, errs := cobraCmd(t, "frobnicate"); code != 2 || !strings.Contains(errs, `unknown command "frobnicate"`) {
		t.Errorf("unknown command: %d %q", code, errs)
	}
	if code, _, errs := cobraCmd(t, "--nope"); code != 2 || !strings.Contains(errs, "unknown flag: --nope") {
		t.Errorf("unknown flag: %d %q", code, errs)
	}
	if code, out, errs := cobraCmd(t, "-h"); code != 0 || !strings.Contains(out, "Usage:") || errs != "" {
		t.Errorf("-h: %d out %q err %q", code, out, errs)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/custos -run TestRunCobraRoot -count=1`
Expected: FAIL to build, `undefined: runCobra`.

- [ ] **Step 3: Write `root.go`**

Create `cmd/custos/root.go`:

```go
package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// exitCode is returned by a command's RunE when it has already printed
// its own message and only the process's exit status is left to report.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// usageError is a wrong invocation: runCobra prints it to stderr and exits 2.
type usageError string

func (e usageError) Error() string { return string(e) }

// result turns the int a command core returns into RunE's error.
func result(code int) error {
	if code == 0 {
		return nil
	}
	return exitCode(code)
}

// exactArgs is cobra.ExactArgs with the command's usage line as the message.
func exactArgs(n int, usage string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != n {
			return usageError(usage)
		}
		return nil
	}
}

// cli carries what the commands share: the process's streams and the
// global --config flag.
type cli struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	configPath     string
}

// newRootCmd builds the whole command tree. A fresh tree per run keeps flag
// values from one run out of the next (tests call run many times).
func newRootCmd(c *cli) *cobra.Command {
	root := &cobra.Command{
		Use:           "custos",
		Short:         "manage a task catalog and the workspaces that answer its tasks",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageError(fmt.Sprintf("custos: unknown command %q\n\n%s", args[0], cmd.UsageString()))
			}
			return usageError(cmd.UsageString())
		},
	}
	root.SetIn(c.stdin)
	root.SetOut(c.stdout)
	root.SetErr(c.stderr)
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageError(fmt.Sprintf("custos: %v\n\n%s", err, cmd.UsageString()))
	})
	root.PersistentFlags().StringVar(&c.configPath, "config", "", "personal config file (default ~/.config/custos/config.yaml)")
	// Commands are added here, one line each, in the task that ports them.
	return root
}

// runCobra executes args on a fresh command tree and returns the exit code:
// 0 on success, 1 when a command failed, 2 when it was called incorrectly.
func runCobra(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c := &cli{stdin: stdin, stdout: stdout, stderr: stderr}
	root := newRootCmd(c)
	root.SetArgs(args)
	err := root.Execute()
	var code exitCode
	var usage usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &code):
		return int(code)
	case errors.As(err, &usage):
		fmt.Fprint(stderr, strings.TrimRight(string(usage), "\n")+"\n")
	default:
		// Only cobra itself returns other errors: an unknown flag or
		// subcommand it caught before any RunE ran.
		fmt.Fprintf(stderr, "custos: %v\n", err)
	}
	return 2
}
```

- [ ] **Step 4: Route `run` and add the legacy switch**

In `cmd/custos/main.go`, rename the existing `run` to `runLegacy` (keep its doc comment, adjusted) and add the new `run` above it:

```go
// legacyCLI sends every command to the dispatcher that predates cobra
// (ruling 5.8). Only a _test.go file sets it, so that the tests can prove
// both implementations agree. Delete it with runLegacy (ruling 5.10).
var legacyCLI bool

// portedCommands are the commands cobra serves. A command joins it in the
// task that ports it.
var portedCommands = map[string]bool{}

// run executes one command and returns the process exit code: 0 on success,
// 1 when the command failed, 2 when it was called incorrectly.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if !legacyCLI && len(args) > 0 && portedCommands[args[0]] {
		return runCobra(args, stdin, stdout, stderr)
	}
	return runLegacy(args, stdin, stdout, stderr)
}

// runLegacy is the dispatcher that used the standard flag package.
func runLegacy(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
```

(the rest of the old `run` body is unchanged). `main()` still calls `run`.

Create `cmd/custos/legacy_hook_test.go`:

```go
package main

import "os"

// CUSTOS_TEST_LEGACY_CLI=1 runs the whole package's tests against the
// pre-cobra dispatcher (ruling 5.8); `make test` does both.
func init() { legacyCLI = os.Getenv("CUSTOS_TEST_LEGACY_CLI") != "" }
```

- [ ] **Step 5: Run both modes**

```bash
go mod tidy
go vet ./cmd/custos
go test ./cmd/custos -race -count=1
CUSTOS_TEST_LEGACY_CLI=1 go test ./cmd/custos -race -count=1
```

Expected: all PASS (`TestRunCobraRoot` calls `runCobra` directly, so it passes in both modes). `go mod tidy` drops the `// indirect` marker from cobra and pflag.

- [ ] **Step 6: Make `make test` run both modes**

In `Makefile`, in the `test` target, directly after the `go test ./... -race -count=1` line add:

```make
	CUSTOS_TEST_LEGACY_CLI=1 go test ./cmd/custos -race -count=1
```

(A tab, as the other recipe lines. The `## test:` comment line gains "; the cmd/custos tests also run against the pre-cobra dispatcher".)

- [ ] **Step 7: Commit**

```bash
git add cmd/custos/root.go cmd/custos/root_test.go cmd/custos/main.go cmd/custos/legacy_hook_test.go Makefile go.mod go.sum
git commit -m "Add the cobra root next to the old dispatcher, behind run()"
```

---

### Task 3: `custos config get|set` and the global `--server-url`

**Files:**
- Create: `cmd/custos/configcmd.go`
- Modify: `cmd/custos/root.go` (register the command and the persistent flag), `cmd/custos/main.go` (`portedCommands["config"] = true`)
- Test: `cmd/custos/configcmd_test.go`

**Interfaces:**
- Consumes: `config.New`, `config.Options`, `config.ResolvePath`, `config.CheckURL`, `config.Set`, `config.KeyServerURL`, `config.EnvNames`, `config.Source*` (Task 1); `cli`, `exitCode`, `usageError`, `exactArgs` (Task 2).
- Produces: `func (c *cli) configCmd() *cobra.Command`.

- [ ] **Step 1: Write the failing test**

Create `cmd/custos/configcmd_test.go` (these call `cobraCmd` from Task 2, because `config` is new and does not exist in the old dispatcher):

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigPrecedenceEndToEnd(t *testing.T) {
	t.Setenv("CUSTOS_SERVER_URL", "")
	path := filepath.Join(t.TempDir(), "c.yaml")
	if code, _, errs := cobraCmd(t, "--config", path, "config", "get", "server-url"); code != 1 || !strings.Contains(errs, "not set") {
		t.Errorf("unset: %d %q", code, errs)
	}
	if code, _, errs := cobraCmd(t, "--config", path, "config", "set", "server-url", "nope"); code != 2 {
		t.Errorf("bad url: %d %q", code, errs)
	}
	if code, out, errs := cobraCmd(t, "--config", path, "config", "set", "server-url", "https://file.example.org"); code != 0 {
		t.Errorf("set: %d %q %q", code, out, errs)
	}
	code, out, _ := cobraCmd(t, "--config", path, "config", "get", "server-url", "--show-source")
	if code != 0 || out != "https://file.example.org (file "+path+")\n" {
		t.Errorf("file: %d %q", code, out)
	}
	t.Setenv("CUSTOS_SERVER_URL", "https://env.example.org")
	if _, out, _ := cobraCmd(t, "--config", path, "config", "get", "server-url", "--show-source"); out != "https://env.example.org (env CUSTOS_SERVER_URL)\n" {
		t.Errorf("env: %q", out)
	}
	if _, out, _ := cobraCmd(t, "--config", path, "config", "get", "server-url", "--server-url", "https://flag.example.org"); out != "https://flag.example.org\n" {
		t.Errorf("flag: %q", out)
	}
	if _, out, _ := cobraCmd(t, "config", "get", "server-url", "--server-url", "https://flag.example.org", "--show-source", "--config", path); out != "https://flag.example.org (flag)\n" {
		t.Errorf("flag source: %q", out)
	}
}

func TestConfigUsage(t *testing.T) {
	for _, args := range [][]string{
		{"config"},
		{"config", "get"},
		{"config", "get", "data-dir"}, // not a key
		{"config", "set", "server-url"},
		{"config", "set", "data-dir", "/x"},
	} {
		if code, _, errs := cobraCmd(t, args...); code != 2 || errs == "" {
			t.Errorf("%v: code %d err %q", args, code, errs)
		}
	}
}

func TestConfigGetWarnsAboutForeignKeys(t *testing.T) {
	t.Setenv("CUSTOS_SERVER_URL", "")
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("server-url: https://a.example.org\noidc-client-secret: hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := cobraCmd(t, "--config", path, "config", "get", "server-url")
	if code != 0 || out != "https://a.example.org\n" {
		t.Fatalf("code %d out %q", code, out)
	}
	if !strings.Contains(errs, `"oidc-client-secret"`) || strings.Contains(errs, "hunter2") {
		t.Errorf("warning: %q", errs)
	}
}

func TestConfigBrokenFileDoesNotStopOtherCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("server-url: [unterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(path))
	if code, _, errs := cobraCmd(t, "--config", path, "config", "get", "server-url"); code != 1 || !strings.Contains(errs, path) {
		t.Errorf("config get on a broken file: %d %q", code, errs)
	}
	// Commands that never read the file are unaffected: see TestValidate*
	// in Task 4, which run with XDG_CONFIG_HOME pointing at a broken file.
}

func TestConfigNoHome(t *testing.T) {
	t.Setenv("CUSTOS_SERVER_URL", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if code, _, errs := cobraCmd(t, "config", "get", "server-url"); code != 1 || errs == "" {
		t.Errorf("get: %d %q", code, errs)
	}
	if code, _, errs := cobraCmd(t, "config", "set", "server-url", "https://x.example.org"); code != 1 || errs == "" {
		t.Errorf("set: %d %q", code, errs)
	}
}

func TestConfigSetUnderAFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "custos", "config.yaml")
	if code, _, errs := cobraCmd(t, "--config", path, "config", "set", "server-url", "https://x.example.org"); code != 1 || errs == "" {
		t.Errorf("%d %q", code, errs)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/custos -run 'TestConfig' -count=1`
Expected: FAIL: `config` is unknown (`code 2`, or `unknown flag: --server-url`).

- [ ] **Step 3: Write the command**

Create `cmd/custos/configcmd.go`:

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/emeland-io/custos/internal/config"
)

const (
	configUsage    = "usage: custos config get server-url [--show-source]\n       custos config set server-url URL\n"
	configGetUsage = "usage: custos config get server-url [--show-source]\n"
	configSetUsage = "usage: custos config set server-url URL\n"
)

// configCmd reads and writes the personal config file (ruling 5.9). Only
// server-url can live there (ruling 5.3).
func (c *cli) configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "read or write the personal config file",
		Args:  cobra.ArbitraryArgs,
		RunE:  func(*cobra.Command, []string) error { return usageError(configUsage) },
	}
	var showSource bool
	get := &cobra.Command{
		Use:   "get server-url",
		Short: "print a setting as the commands see it: flag, then environment, then config file",
		Args:  exactArgs(1, configGetUsage),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != config.KeyServerURL {
				return usageError(fmt.Sprintf("custos config get: unknown key %q; the only key is %q\n", args[0], config.KeyServerURL))
			}
			path, err := config.ResolvePath(c.configPath)
			if err != nil {
				fmt.Fprintf(c.stderr, "custos config get: %v\n", err)
				return exitCode(1)
			}
			s, err := config.New(cmd.Flags(), []string{config.KeyServerURL}, &config.Options{Path: path, Warn: c.stderr})
			if err != nil {
				fmt.Fprintf(c.stderr, "custos config get: %v\n", err)
				return exitCode(1)
			}
			val := s.String(config.KeyServerURL)
			if val == "" {
				fmt.Fprintf(c.stderr, "custos config get: %s is not set (--%s, $%s or %s)\n",
					config.KeyServerURL, config.KeyServerURL, config.EnvNames[config.KeyServerURL], path)
				return exitCode(1)
			}
			if !showSource {
				fmt.Fprintln(c.stdout, val)
				return nil
			}
			from := string(s.Source(config.KeyServerURL))
			switch s.Source(config.KeyServerURL) {
			case config.SourceEnv:
				from = "env " + config.EnvNames[config.KeyServerURL]
			case config.SourceFile:
				from = "file " + path
			}
			fmt.Fprintf(c.stdout, "%s (%s)\n", val, from)
			return nil
		},
	}
	get.Flags().BoolVar(&showSource, "show-source", false, "also say whether the value came from the flag, the environment or the config file")
	set := &cobra.Command{
		Use:   "set server-url URL",
		Short: "write a setting to the config file",
		Args:  exactArgs(2, configSetUsage),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != config.KeyServerURL {
				return usageError(fmt.Sprintf("custos config set: unknown key %q; the only key is %q\n", args[0], config.KeyServerURL))
			}
			if err := config.CheckURL(args[1]); err != nil {
				return usageError(fmt.Sprintf("custos config set: %v\n", err))
			}
			path, err := config.ResolvePath(c.configPath)
			if err == nil {
				err = config.Set(path, args[0], args[1])
			}
			if err != nil {
				fmt.Fprintf(c.stderr, "custos config set: %v\n", err)
				return exitCode(1)
			}
			fmt.Fprintf(c.stdout, "%s set in %s\n", args[0], path)
			return nil
		},
	}
	cmd.AddCommand(get, set)
	return cmd
}
```

In `cmd/custos/root.go`, replace the comment line `// Commands are added here…` with:

```go
	root.PersistentFlags().String("server-url", "", "URL of the custos server, for the commands that talk to one (env CUSTOS_SERVER_URL; today only \"config get\" reads it)")
	// Commands are added here, one line each, in the task that ports them.
	root.AddCommand(c.configCmd())
```

In `cmd/custos/main.go`, set `var portedCommands = map[string]bool{"config": true}`.

- [ ] **Step 4: Run both modes**

```bash
go vet ./cmd/custos
go test ./cmd/custos -race -count=1
CUSTOS_TEST_LEGACY_CLI=1 go test ./cmd/custos -race -count=1
```

Expected: PASS. (Under `CUSTOS_TEST_LEGACY_CLI=1` the config tests still pass because they call `runCobra` directly.)

- [ ] **Step 5: Commit**

```bash
git add cmd/custos/configcmd.go cmd/custos/configcmd_test.go cmd/custos/root.go cmd/custos/main.go
git commit -m "Add custos config get|set and the global --server-url flag"
```

---

### Task 4: Port `validate`

**Files:**
- Modify: `cmd/custos/validate.go`, `cmd/custos/root.go`, `cmd/custos/main.go`
- Test: existing `cmd/custos/validate_test.go` (runs in both modes), plus one new test appended to it

**Interfaces:**
- Consumes: `cli`, `result`, `usageError` (Task 2).
- Produces: `const validateUsage`; `func doValidate(against, dir string, stdout, stderr io.Writer) int`; `func (c *cli) validateCmd() *cobra.Command`.

- [ ] **Step 1: Write the failing test (and see the existing ones fail)**

Append to `cmd/custos/validate_test.go`:

```go
func TestValidateIgnoresABrokenConfigFile(t *testing.T) {
	cfgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "custos"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", cfgDir) // $XDG_CONFIG_HOME/custos/config.yaml cannot exist
	t.Setenv("HOME", cfgDir)
	dir := t.TempDir()
	fixture.WriteDir(t, dir, fixture.Catalog())
	if code, out, errs := runCmd(t, "validate", dir); code != 0 || out != "ok\n" {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
}
```

(Add `"os"` and `"path/filepath"` to the imports of that file if missing.)

In `cmd/custos/main.go`, set `portedCommands` to `{"config": true, "validate": true}`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/custos -run 'TestValidate' -count=1`
Expected: FAIL: `validate` is routed to cobra, which has no such command (`unknown command "validate"`, exit 2).

- [ ] **Step 3: Extract the core and write the command**

In `cmd/custos/validate.go`, replace the top of `runValidate` (through the `dir` computation) so that everything from `fsys, err := filesToValidate(dir)` on moves into `doValidate`:

```go
const validateUsage = "usage: custos validate [--against REV] [DIR]\n"

// runValidate parses the flags of validate with the pre-cobra dispatcher.
func runValidate(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("validate", flag.ContinueOnError)
	fl.SetOutput(stderr)
	against := fl.String("against", "", validateAgainstHelp)
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	if fl.NArg() > 1 {
		fmt.Fprint(stderr, validateUsage)
		return 2
	}
	dir := "."
	if fl.NArg() == 1 {
		dir = fl.Arg(0)
	}
	return doValidate(*against, dir, stdout, stderr)
}

const validateAgainstHelp = "also check that the task versions in this git revision are unchanged (catalogs only; DIR must be the repository root)"

// doValidate checks a catalog or workspace checkout: the staged content
// when dir is the root of a git work tree, the files on disk otherwise.
// Content holding custos.yaml is a workspace; anything else is a catalog.
func doValidate(against, dir string, stdout, stderr io.Writer) int {
	fsys, err := filesToValidate(dir)
	// … the unchanged remainder of the old runValidate, with *against replaced by against …
}
```

(The remainder is moved verbatim; the only edit is `*against` → `against` in the two places it occurs. Keep the doc comment that used to sit on `runValidate` on `doValidate`.)

Add to `validate.go`:

```go
func (c *cli) validateCmd() *cobra.Command {
	var against string
	cmd := &cobra.Command{
		Use:   "validate [flags] [DIR]",
		Short: "check a catalog or workspace checkout",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 1 {
				return usageError(validateUsage)
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return result(doValidate(against, dir, c.stdout, c.stderr))
		},
	}
	cmd.Flags().StringVar(&against, "against", "", validateAgainstHelp)
	return cmd
}
```

Add `"github.com/spf13/cobra"` to its imports. In `root.go`, register it: `root.AddCommand(c.configCmd(), c.validateCmd())`.

- [ ] **Step 4: Run both modes**

```bash
go vet ./cmd/custos
go test ./cmd/custos -race -count=1
CUSTOS_TEST_LEGACY_CLI=1 go test ./cmd/custos -race -count=1
```

Expected: PASS in both. `TestValidateTooManyArguments` proves exit 2 on both paths.

- [ ] **Step 5: Commit**

```bash
git add cmd/custos/validate.go cmd/custos/validate_test.go cmd/custos/root.go cmd/custos/main.go
git commit -m "Port validate to cobra"
```

---

### Task 5: Port `task new-version`

**Files:**
- Modify: `cmd/custos/task.go`, `cmd/custos/root.go`, `cmd/custos/main.go`
- Test: existing `cmd/custos/task_test.go` (both modes)

**Interfaces:**
- Produces: `func doTaskNewVersion(dir string, patch, minor, major bool, args []string, stdout, stderr io.Writer) int`; `func (c *cli) taskCmd() *cobra.Command`.

- [ ] **Step 1: Route it (red)**

In `main.go`, add `"task": true` to `portedCommands`. Run `go test ./cmd/custos -run TestTask -count=1`. Expected: FAIL (`unknown command "task"`).

- [ ] **Step 2: Extract the core**

In `cmd/custos/task.go`, change `runTask` so that after `fl.Parse` it calls the core, and move the rest of its body into the core:

```go
	if err := fl.Parse(args[1:]); err != nil {
		return helpOrUsage(err)
	}
	return doTaskNewVersion(*dir, *patch, *minor, *major, fl.Args(), stdout, stderr)
}

// doTaskNewVersion writes the next version file of the task args[0].
func doTaskNewVersion(dir string, patch, minor, major bool, args []string, stdout, stderr io.Writer) int {
	var steps []semver.Step
	for step, set := range map[semver.Step]bool{semver.Patch: patch, semver.Minor: minor, semver.Major: major} {
		if set {
			steps = append(steps, step)
		}
	}
	if len(steps) != 1 || len(args) != 1 {
		fmt.Fprint(stderr, taskUsage)
		return 2
	}
	path, err := catalog.NewVersion(dir, args[0], steps[0])
	if err != nil {
		fmt.Fprintf(stderr, "custos task new-version: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}
```

- [ ] **Step 3: Write the cobra command**

Add to `task.go` (plus the `cobra` import):

```go
func (c *cli) taskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "work on the tasks of a catalog checkout",
		Args:  cobra.ArbitraryArgs,
		RunE:  func(*cobra.Command, []string) error { return usageError(taskUsage) },
	}
	var dir string
	var patch, minor, major bool
	nv := &cobra.Command{
		Use:   "new-version (--patch | --minor | --major) [--dir DIR] TASK-UUID",
		Short: "write the next version file of a task",
		Args:  cobra.ArbitraryArgs, // doTaskNewVersion checks the count
		RunE: func(_ *cobra.Command, args []string) error {
			return result(doTaskNewVersion(dir, patch, minor, major, args, c.stdout, c.stderr))
		},
	}
	nv.Flags().StringVar(&dir, "dir", ".", "catalog checkout")
	nv.Flags().BoolVar(&patch, "patch", false, "increase the patch version: wording or typo fixes")
	nv.Flags().BoolVar(&minor, "minor", false, "increase the minor version: clarifications")
	nv.Flags().BoolVar(&major, "major", false, "increase the major version: the task asks for something else")
	cmd.AddCommand(nv)
	return cmd
}
```

Register: `root.AddCommand(c.configCmd(), c.validateCmd(), c.taskCmd())`.

- [ ] **Step 4: Run both modes**

```bash
go vet ./cmd/custos
go test ./cmd/custos -race -count=1
CUSTOS_TEST_LEGACY_CLI=1 go test ./cmd/custos -race -count=1
```

Expected: PASS. `TestTaskNewVersionUsage` covers `task` alone, no step, two steps.

- [ ] **Step 5: Commit**

```bash
git add cmd/custos/task.go cmd/custos/root.go cmd/custos/main.go
git commit -m "Port task new-version to cobra"
```

---

### Task 6: Port `workspace create`

**Files:**
- Modify: `cmd/custos/workspace.go`, `cmd/custos/root.go`, `cmd/custos/main.go`
- Test: existing `TestWorkspaceCreate*` in `cmd/custos/serve_test.go` (both modes) plus a precedence test

**Interfaces:**
- Consumes: `config.New` (Task 1).
- Produces: `func doWorkspaceCreate(dataDir, publicURL, author string, args []string, stdout, stderr io.Writer) int`; `func (c *cli) workspaceCmd() *cobra.Command`.

- [ ] **Step 1: Write the failing precedence test and route it**

Append to `cmd/custos/serve_test.go`:

```go
func TestWorkspaceCreateFlagBeatsEnv(t *testing.T) {
	data := t.TempDir()
	seedCatalog(t, data)
	t.Setenv("CUSTOS_DATA_DIR", t.TempDir()) // a different, empty data dir: the flag must win
	t.Setenv("CUSTOS_AUTHOR", "Env Author <env@example.org>")
	t.Setenv("CUSTOS_PUBLIC_URL", "https://env.example.org")
	code, out, errs := runCmd(t, "workspace", "create", "--data-dir", data, "--author", "Flag Author <flag@example.org>",
		"--public-url", "https://flag.example.org", fixture.WorkspaceID)
	if code != 0 || !strings.Contains(out, "https://flag.example.org/git/workspaces/") {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
	dir := filepath.Join(data, "repos", "workspaces", fixture.WorkspaceID+".git")
	if who := gittest.Run(t, dir, "log", "-1", "--format=%an", "main"); who != "Flag Author" {
		t.Errorf("author %q", who)
	}
}

func TestWorkspaceCreateIgnoresServerURL(t *testing.T) {
	// ruling 5.5: --server-url / CUSTOS_SERVER_URL are not the public URL.
	t.Setenv("CUSTOS_PUBLIC_URL", "")
	t.Setenv("CUSTOS_SERVER_URL", "https://server.example.org")
	data := t.TempDir()
	seedCatalog(t, data)
	_, out, _ := runCmd(t, "workspace", "create", "--data-dir", data, "--author", "Jane Doe <jane@example.org>", fixture.WorkspaceID)
	if !strings.Contains(out, defaultPublicURL) || strings.Contains(out, "server.example.org") {
		t.Errorf("out %q", out)
	}
}
```

In `main.go` add `"workspace": true` to `portedCommands`.

Run: `go test ./cmd/custos -run 'TestWorkspaceCreate' -count=1`. Expected: FAIL (`unknown command "workspace"`).

- [ ] **Step 2: Extract the core**

In `cmd/custos/workspace.go` split `runWorkspace`: after `fl.Parse(args[1:])`, call the core.

```go
	if err := fl.Parse(args[1:]); err != nil {
		return helpOrUsage(err)
	}
	return doWorkspaceCreate(*dataDir, *publicURL, *authorFlag, fl.Args(), stdout, stderr)
}

// doWorkspaceCreate creates the workspace args[0] in dataDir.
func doWorkspaceCreate(dataDir, publicURL, authorFlag string, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || dataDir == "" || authorFlag == "" {
		fmt.Fprint(stderr, workspaceUsage)
		return 2
	}
	// … the old body, with *dataDir → dataDir, *publicURL → publicURL,
	// *authorFlag → authorFlag, fl.Arg(0) → args[0] …
}
```

- [ ] **Step 3: Write the cobra command**

Add to `workspace.go` (imports: `cobra`, `internal/config`):

```go
func (c *cli) workspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "work with the workspaces of a custos data directory",
		Args:  cobra.ArbitraryArgs,
		RunE:  func(*cobra.Command, []string) error { return usageError(workspaceUsage) },
	}
	create := &cobra.Command{
		Use:   "create [flags] WORKSPACE-UUID",
		Short: "create a workspace pinned to the catalog",
		Args:  cobra.ArbitraryArgs, // doWorkspaceCreate checks the count
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := config.New(cmd.Flags(), []string{"data-dir", "public-url", "author"}, nil)
			if err != nil {
				fmt.Fprintf(c.stderr, "custos workspace create: %v\n", err)
				return exitCode(1)
			}
			return result(doWorkspaceCreate(s.String("data-dir"), s.String("public-url"), s.String("author"), args, c.stdout, c.stderr))
		},
	}
	f := create.Flags()
	f.String("data-dir", "", "directory holding the repositories (env CUSTOS_DATA_DIR)")
	f.String("public-url", defaultPublicURL, "base URL clients reach the server at, written into new workspaces (env CUSTOS_PUBLIC_URL)")
	f.String("author", "", "author of the initial commit, as \"Name <email>\" (env CUSTOS_AUTHOR)")
	cmd.AddCommand(create)
	return cmd
}
```

Register `c.workspaceCmd()` in `newRootCmd`.

- [ ] **Step 4: Run both modes**

```bash
go vet ./cmd/custos
go test ./cmd/custos -race -count=1
CUSTOS_TEST_LEGACY_CLI=1 go test ./cmd/custos -race -count=1
```

Expected: PASS. In legacy mode the two new tests also pass (the legacy flags give the same precedence).

- [ ] **Step 5: Commit**

```bash
git add cmd/custos/workspace.go cmd/custos/serve_test.go cmd/custos/root.go cmd/custos/main.go
git commit -m "Port workspace create to cobra"
```

---

### Task 7: Port `token create`

**Files:**
- Modify: `cmd/custos/token.go`, `cmd/custos/root.go`, `cmd/custos/main.go`
- Test: existing `cmd/custos/token_test.go` (both modes) plus an allowlist test

**Interfaces:**
- Produces: `type tokenArgs struct{ dataDir, email, name, label, engineer string; admin, taskAuthor, processorAuthor bool }`; `func doTokenCreate(a tokenArgs, nArgs int, stdout, stderr io.Writer) int`; `func (c *cli) tokenCmd() *cobra.Command`.

- [ ] **Step 1: Write the failing test and route it**

Append to `cmd/custos/token_test.go`:

```go
// Ruling 5.4: only the allowlisted variables are read. A stray
// CUSTOS_ADMIN or CUSTOS_EMAIL in someone's environment changes nothing.
func TestTokenCreateIgnoresStrayEnvironment(t *testing.T) {
	data := t.TempDir()
	t.Setenv("CUSTOS_ADMIN", "true")
	t.Setenv("CUSTOS_EMAIL", "env@example.org")
	t.Setenv("CUSTOS_NAME", "Env")
	if code, _, errs := runCmd(t, "token", "create", "--data-dir", data); code != 2 || !strings.Contains(errs, "usage: custos token create") {
		t.Fatalf("CUSTOS_EMAIL must not supply --email: code %d err %q", code, errs)
	}
	if code, _, errs := runCmd(t, "token", "create", "--data-dir", data, "--email", "jane@example.org"); code != 0 {
		t.Fatalf("code %d err %q", code, errs)
	}
	m, err := auth.NewManager(auth.Config{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	u, err := m.UpdateUser("jane@example.org", func(*auth.User) {})
	if err != nil {
		t.Fatal(err)
	}
	if u.Roles.Admin || u.Name == "Env" {
		t.Errorf("a stray environment variable took effect: %+v", u)
	}
}

func TestTokenCreateDataDirFromEnvironment(t *testing.T) {
	data := t.TempDir()
	t.Setenv("CUSTOS_DATA_DIR", data)
	if code, out, errs := runCmd(t, "token", "create", "--email", "jane@example.org"); code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
}
```

In `main.go` add `"token": true`. Run `go test ./cmd/custos -run TestToken -count=1`. Expected: FAIL (`unknown command "token"`).

- [ ] **Step 2: Extract the core**

In `token.go`, after `fl.Parse(args[1:])` in `runToken` build the struct and call the core:

```go
	if err := fl.Parse(args[1:]); err != nil {
		return helpOrUsage(err)
	}
	return doTokenCreate(tokenArgs{dataDir: *dataDir, email: *email, name: *name, label: *label, engineer: *engineer,
		admin: *admin, taskAuthor: *taskAuthor, processorAuthor: *processorAuthor}, fl.NArg(), stdout, stderr)
}

// tokenArgs are the parsed flags of token create.
type tokenArgs struct {
	dataDir, email, name, label, engineer   string
	admin, taskAuthor, processorAuthor bool
}

// doTokenCreate creates or updates the user and prints a new token to stdout.
func doTokenCreate(a tokenArgs, nArgs int, stdout, stderr io.Writer) int {
	if nArgs != 0 || a.dataDir == "" || a.email == "" {
		fmt.Fprint(stderr, tokenUsage)
		return 2
	}
	// … the old body from `var engineerIDs []string` on, with *dataDir → a.dataDir,
	// *email → a.email, *name → a.name, *label → a.label, *engineer → a.engineer,
	// *admin → a.admin, *taskAuthor → a.taskAuthor, *processorAuthor → a.processorAuthor …
}
```

(Run `gofmt -w cmd/custos/token.go` afterwards for the struct alignment.)

- [ ] **Step 3: Write the cobra command**

Add to `token.go` (imports: `cobra`, `internal/config`):

```go
func (c *cli) tokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "manage personal access tokens",
		Args:  cobra.ArbitraryArgs,
		RunE:  func(*cobra.Command, []string) error { return usageError(tokenUsage) },
	}
	var a tokenArgs
	create := &cobra.Command{
		Use:   "create --data-dir DIR --email E [flags]",
		Short: "create or update a user and print a fresh personal access token",
		Args:  cobra.ArbitraryArgs, // doTokenCreate checks the count
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := config.New(cmd.Flags(), []string{"data-dir"}, nil)
			if err != nil {
				fmt.Fprintf(c.stderr, "custos token create: %v\n", err)
				return exitCode(1)
			}
			a.dataDir = s.String("data-dir")
			return result(doTokenCreate(a, len(args), c.stdout, c.stderr))
		},
	}
	f := create.Flags()
	f.String("data-dir", "", "directory holding auth/ (env CUSTOS_DATA_DIR)")
	f.StringVar(&a.email, "email", "", "the user's email address")
	f.StringVar(&a.name, "name", "", "the user's display name, used as the Git commit author name")
	f.StringVar(&a.label, "label", "cli", "label for the new token, shown in GET /auth/tokens")
	f.BoolVar(&a.admin, "admin", false, "grant the admin role")
	f.BoolVar(&a.taskAuthor, "task-author", false, "grant the task-author role")
	f.BoolVar(&a.processorAuthor, "processor-author", false, "grant the processor-author role")
	f.StringVar(&a.engineer, "engineer", "", `comma-separated workspace UUIDs, or "*" for every workspace, granting the engineer role`)
	cmd.AddCommand(create)
	return cmd
}
```

Register `c.tokenCmd()`.

- [ ] **Step 4: Run both modes**

```bash
go vet ./cmd/custos
go test ./cmd/custos -race -count=1
CUSTOS_TEST_LEGACY_CLI=1 go test ./cmd/custos -race -count=1
```

Expected: PASS. `TestTokenCreateBootstrapsAnAdmin` proves stdout is exactly the token.

- [ ] **Step 5: Commit**

```bash
git add cmd/custos/token.go cmd/custos/token_test.go cmd/custos/root.go cmd/custos/main.go
git commit -m "Port token create to cobra"
```

---

### Task 8: Port `clone` and `push`

**Files:**
- Modify: `cmd/custos/remote.go`, `cmd/custos/root.go`, `cmd/custos/main.go`
- Test: existing `cmd/custos/remote_test.go` (both modes) plus pass-through tests

**Interfaces:**
- Produces: `func doClone(args []string, stderr io.Writer) int`; `func doPush(dir string, args []string, stderr io.Writer) int`; `var remotePush = remote.Push` (a test seam); `func (c *cli) cloneCmd() *cobra.Command`; `func (c *cli) pushCmd() *cobra.Command`.

- [ ] **Step 1: Write the failing tests and route**

Append to `cmd/custos/remote_test.go`:

```go
func TestPushPassesArgumentsToGit(t *testing.T) {
	type call struct {
		dir, remote string
		args        []string
	}
	var got call
	old := remotePush
	remotePush = func(dir, remoteName string, args []string, _ io.Writer) error {
		got = call{dir, remoteName, args}
		return nil
	}
	t.Cleanup(func() { remotePush = old })

	for _, tc := range []struct {
		name string
		args []string
		want call
	}{
		{"defaults", []string{"push"}, call{".", "origin", nil}},
		{"remote only", []string{"push", "upstream"}, call{".", "upstream", nil}},
		{"dir before the remote", []string{"push", "--dir", "x", "origin", "--force", "main"}, call{"x", "origin", []string{"--force", "main"}}},
		{"flags after the remote belong to git", []string{"push", "origin", "--dir", "y"}, call{".", "origin", []string{"--dir", "y"}}},
		{"double dash", []string{"push", "origin", "--", "--weird"}, call{".", "origin", []string{"--", "--weird"}}},
	} {
		got = call{}
		if code, _, errs := runCmd(t, tc.args...); code != 0 {
			t.Fatalf("%s: code %d err %q", tc.name, code, errs)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
```

(Add `"io"` and `"reflect"` to the imports.)

> **Note for the implementer:** the last case pins what the old dispatcher does today: stdlib `flag` stops at `origin`, so `--` after the remote is an ordinary git argument. If the legacy run of this test disagrees with a case's `want`, fix the expectation to what legacy does (legacy is the reference), then make cobra match with `SetInterspersed(false)`.

In `main.go` add `"clone": true, "push": true`. Run `go test ./cmd/custos -run 'TestPush|TestClone' -count=1`. Expected: FAIL (build error: `undefined: remotePush`).

- [ ] **Step 2: Extract the cores**

In `cmd/custos/remote.go`:

```go
// remotePush is remote.Push; tests replace it.
var remotePush = remote.Push

func runClone(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("clone", flag.ContinueOnError)
	fl.SetOutput(stderr)
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	return doClone(fl.Args(), stderr)
}

// doClone clones args[0] into args[1].
func doClone(args []string, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprint(stderr, cloneUsage)
		return 2
	}
	if err := remote.Clone(args[0], args[1], stderr); err != nil {
		fmt.Fprintf(stderr, "custos clone: %v\n", err)
		return 1
	}
	return 0
}
```

and in `runPush`, after `fl.Parse`, `return doPush(*dir, fl.Args(), stderr)`, with:

```go
// doPush pushes dir to args[0] (default origin); the arguments after it go to git.
func doPush(dir string, args []string, stderr io.Writer) int {
	remoteName, gitArgs := "origin", []string(nil)
	if len(args) > 0 {
		remoteName, gitArgs = args[0], args[1:]
	}
	if err := remotePush(dir, remoteName, gitArgs, stderr); err != nil {
		fmt.Fprintf(stderr, "custos push: %v\n", err)
		return 1
	}
	return 0
}
```

- [ ] **Step 3: Write the cobra commands**

Add to `remote.go` (import `cobra`):

```go
func (c *cli) cloneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clone URL DIR",
		Short: "git clone, then download the attachments the answers reference",
		Args:  exactArgs(2, cloneUsage),
		RunE:  func(_ *cobra.Command, args []string) error { return result(doClone(args, c.stderr)) },
	}
}

func (c *cli) pushCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "push [--dir DIR] [REMOTE [GIT-PUSH-ARGS...]]",
		Short: "upload the attachments of the commits to push, then git push",
		Args:  cobra.ArbitraryArgs,
		RunE:  func(_ *cobra.Command, args []string) error { return result(doPush(dir, args, c.stderr)) },
	}
	// Stop at the first argument, as the standard flag package did: what
	// follows REMOTE, flags included, belongs to git.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().StringVar(&dir, "dir", ".", "git checkout to push")
	return cmd
}
```

Register `c.cloneCmd(), c.pushCmd()`.

- [ ] **Step 4: Run both modes**

```bash
go vet ./cmd/custos
go test ./cmd/custos -race -count=1
CUSTOS_TEST_LEGACY_CLI=1 go test ./cmd/custos -race -count=1
```

Expected: PASS in both.

- [ ] **Step 5: Commit**

```bash
git add cmd/custos/remote.go cmd/custos/remote_test.go cmd/custos/root.go cmd/custos/main.go
git commit -m "Port clone and push to cobra"
```

---

### Task 9: Port `hook pre-receive`

**Files:**
- Modify: `cmd/custos/hook.go`, `cmd/custos/root.go`, `cmd/custos/main.go`
- Test: there is no existing `cmd` test for `hook`; the hook is exercised by the push tests in `distribute_test.go`/`serve_test.go` that go through a real `git` and the installed script (`hook.Script` runs `custos hook pre-receive --kind K` from the built binary). Add a direct test.

**Interfaces:**
- Produces: `func doHookPreReceive(kindFlag, catalogDir, workspaceID string, stdin io.Reader, stderr io.Writer) int`; `func (c *cli) hookCmd() *cobra.Command`.

- [ ] **Step 1: Write the failing test and route**

Create `cmd/custos/hook_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestHookUsage(t *testing.T) {
	for _, args := range [][]string{{"hook"}, {"hook", "other"}, {"hook", "pre-receive"}, {"hook", "pre-receive", "--kind", "nope"}} {
		code, _, errs := runCmd(t, args...)
		if code != 2 || errs == "" {
			t.Errorf("%v: code %d err %q", args, code, errs)
		}
	}
}

func TestHookIsNotListedInHelp(t *testing.T) {
	_, out, errs := cobraCmd(t, "-h")
	if strings.Contains(out+errs, "pre-receive") {
		t.Errorf("the internal hook command is listed:\n%s", out+errs)
	}
}
```

In `main.go` add `"hook": true`. Run `go test ./cmd/custos -run TestHook -count=1`. Expected: FAIL.

- [ ] **Step 2: Extract the core**

In `hook.go`, `runHook` keeps its usage check and flag parsing, then calls `doHookPreReceive(*kindFlag, *catalogDir, *workspaceID, stdin, stderr)`. The core holds everything from `kind, err := hook.ParseKind(*kindFlag)` on (with `*kindFlag` → `kindFlag` etc.).

- [ ] **Step 3: Write the cobra command**

```go
func (c *cli) hookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "hook",
		Short:  "git hooks custos installs in its repositories",
		Hidden: true, // internal: git runs it, not people
		Args:   cobra.ArbitraryArgs,
		RunE:   func(*cobra.Command, []string) error { return usageError(hookUsage) },
	}
	var kind, catalogDir, workspaceID string
	pre := &cobra.Command{
		Use:    "pre-receive --kind catalog|workspace [--catalog DIR --workspace UUID]",
		Short:  "check the ref updates of a push (reads them from stdin)",
		Hidden: true,
		Args:   cobra.ArbitraryArgs,
		// No environment, no config file: git runs this in a hook, and its
		// behavior must not depend on whose environment that is.
		RunE: func(*cobra.Command, []string) error {
			return result(doHookPreReceive(kind, catalogDir, workspaceID, c.stdin, c.stderr))
		},
	}
	pre.Flags().StringVar(&kind, "kind", "", "catalog or workspace")
	pre.Flags().StringVar(&catalogDir, "catalog", "", "absolute path of catalog.git (workspace hooks)")
	pre.Flags().StringVar(&workspaceID, "workspace", "", "id of the workspace the repository belongs to (workspace hooks)")
	cmd.AddCommand(pre)
	return cmd
}
```

with `const hookUsage = "usage: custos hook pre-receive --kind catalog|workspace [--catalog DIR --workspace UUID]\n"` replacing the inline string in `runHook`. Register `c.hookCmd()`.

- [ ] **Step 4: Run both modes, including the end-to-end push tests**

```bash
go vet ./cmd/custos
go test ./cmd/custos -race -count=1
CUSTOS_TEST_LEGACY_CLI=1 go test ./cmd/custos -race -count=1
go test ./internal/hook ./internal/store -race -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/custos/hook.go cmd/custos/hook_test.go cmd/custos/root.go cmd/custos/main.go
git commit -m "Port hook pre-receive to cobra"
```

---

### Task 10: Port `processor test`

**Files:**
- Modify: `cmd/custos/processor.go`, `cmd/custos/root.go`, `cmd/custos/main.go`
- Test: existing `cmd/custos/processor_test.go` and `processor_sdk_test.go` (both modes; they need Docker, as `make test` does)

**Interfaces:**
- Consumes: `config.New` (Task 1).
- Produces: `func doProcessorTest(pt processorTest, extraArgs int, stdout, stderr io.Writer) int`; `func (c *cli) processorCmd() *cobra.Command`.

- [ ] **Step 1: Route it (red)**

In `main.go` add `"processor": true`. Run `go test ./cmd/custos -run 'TestProcessorTest' -count=1`. Expected: FAIL (`unknown command "processor"`).

- [ ] **Step 2: Extract the core**

In `runProcessor`, after the two-phase parse, replace everything from `if pt.image == "" || fl.NArg() != 0 …` to the end with:

```go
	return doProcessorTest(pt, fl.NArg(), stdout, stderr)
}

// doProcessorTest checks pt and runs it. extraArgs counts the positional
// arguments left over after the image.
func doProcessorTest(pt processorTest, extraArgs int, stdout, stderr io.Writer) int {
	if pt.image == "" || extraArgs != 0 || pt.answerPath == "" || pt.timeout <= 0 {
		fmt.Fprint(stderr, processorUsage)
		return 2
	}
	// … the old tail unchanged: workspace-id check, signal.NotifyContext, pt.run …
}
```

- [ ] **Step 3: Write the cobra command**

Add to `processor.go`:

```go
func (c *cli) processorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "processor",
		Short: "work with processors",
		Args:  cobra.ArbitraryArgs,
		RunE:  func(*cobra.Command, []string) error { return usageError(processorUsage) },
	}
	var pt processorTest
	test := &cobra.Command{
		Use:   "test IMAGE --answer FILE [flags]",
		Short: "run a processor image on an answer and print its proposal",
		// IMAGE may come before or after the flags, as before.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Only --container-runtime has an environment variable here; the
			// processor test's --secrets-dir and --trusted-keys never did.
			s, err := config.New(cmd.Flags(), []string{"container-runtime"}, nil)
			if err != nil {
				fmt.Fprintf(c.stderr, "custos processor test: %v\n", err)
				return exitCode(1)
			}
			pt.runtime = s.String("container-runtime")
			extra := 0
			if len(args) > 0 {
				pt.image = args[0]
				extra = len(args) - 1
			}
			return result(doProcessorTest(pt, extra, c.stdout, c.stderr))
		},
	}
	f := test.Flags()
	f.StringVar(&pt.answerPath, "answer", "", "answer file in the workspace format (answers/<task-uuid>.md)")
	f.StringVar(&pt.taskPath, "task", "", "task version file (default: a task made up from the answer)")
	f.StringVar(&pt.previousDir, "previous", "", "directory laid out like a workspace whose generated/ and documents/ hold earlier output")
	f.StringVar(&pt.blobsDir, "blobs", "", "directory with the answer's attachments named by their SHA-256 (default: .custos/blobs/sha256 of the checkout holding the answer)")
	f.StringVar(&pt.secretsDir, "secrets-dir", "", "every file in this directory is mounted as a secret at /run/secrets/<name>")
	f.BoolVar(&pt.network, "network", false, "give the processor network access")
	f.DurationVar(&pt.timeout, "timeout", 60*time.Second, "kill the processor after this time")
	f.StringVar(&pt.trustedKeys, "trusted-keys", "", "directory with trusted public keys (*.pem, *.pub); without it signatures are not checked")
	f.StringVar(&pt.workspaceID, "workspace-id", testWorkspaceID, "workspace id sent to the processor")
	f.String("container-runtime", "docker", "docker, podman, or the path of either (env CUSTOS_CONTAINER_RUNTIME)")
	cmd.AddCommand(test)
	return cmd
}
```

Add `"github.com/spf13/cobra"` and `"github.com/emeland-io/custos/internal/config"` to the imports. Register `c.processorCmd()`.

- [ ] **Step 4: Run both modes**

```bash
go vet ./cmd/custos
go test ./cmd/custos -race -count=1
CUSTOS_TEST_LEGACY_CLI=1 go test ./cmd/custos -race -count=1
```

Expected: PASS (needs Docker for the golden and SDK tests, as before). `TestProcessorTestUsage` and `TestProcessorTestRejectsBadInput` cover the exit-2 paths.

- [ ] **Step 5: Commit**

```bash
git add cmd/custos/processor.go cmd/custos/root.go cmd/custos/main.go
git commit -m "Port processor test to cobra"
```

---

### Task 11: Port `serve`

**Files:**
- Modify: `cmd/custos/serve.go`, `cmd/custos/auth.go`, `cmd/custos/runs.go`, `cmd/custos/root.go`, `cmd/custos/main.go`
- Test: existing `serve_test.go`, `runs_test.go`, `auth_test.go`, `shutdown_test.go`, `api_test.go`, `distribute_test.go` (both modes); one existing assertion changes (see Step 3)

**Interfaces:**
- Consumes: `config.New`, `(*Settings).String`, `(*Settings).Int` (Task 1).
- Produces:
  - `type authValues struct{ issuerURL, clientID, clientSecret, redirectURL, adminEmails, sessionSecret string }` with `func (v authValues) config(publicURL string) (auth.Config, error)`; `func (f *authFlagSet) values() authValues`
  - `func (o processorOptions) check() error`
  - `func processorOptionsFrom(s *config.Settings) (processorOptions, error)`
  - `func serveWith(dataDir, addr, publicURL string, procOpts processorOptions, av authValues, stdout, stderr io.Writer) int`
  - `func (c *cli) serveCmd() *cobra.Command`

- [ ] **Step 1: Route it (red)**

In `main.go` add `"serve": true`. Run `go test ./cmd/custos -run 'TestServe|TestProcessorRuns|TestShutdown' -count=1`. Expected: FAIL (`unknown command "serve"`).

- [ ] **Step 2: Refactor the shared pieces**

`cmd/custos/auth.go`: change the method receiver. Keep `authFlagSet` and `defineAuthFlags` (legacy and `auth_test.go` use them); add:

```go
// authValues are the resolved auth settings of serve.
type authValues struct {
	issuerURL, clientID, clientSecret, redirectURL string
	adminEmails, sessionSecret                     string
}

func (f *authFlagSet) values() authValues {
	return authValues{*f.issuerURL, *f.clientID, *f.clientSecret, *f.redirectURL, *f.adminEmails, *f.sessionSecret}
}

// config builds an auth.Config from the parsed flags (see authValues.config).
func (f *authFlagSet) config(publicURL string) (auth.Config, error) {
	return f.values().config(publicURL)
}

// config builds an auth.Config. publicURL is --public-url, already validated
// by checkPublicURL; it supplies --oidc-redirect-url's default.
func (v authValues) config(publicURL string) (auth.Config, error) {
	// … the old body of authFlagSet.config, with every *f.x replaced by v.x …
}
```

`cmd/custos/runs.go`: split `options()`:

```go
func (f *processorFlags) options() (processorOptions, error) {
	o := processorOptions{runtime: *f.runtime, secretsDir: *f.secretsDir, memory: *f.memory,
		trustedKeys: *f.trustedKeys, workers: *f.workers, maxDepth: *f.maxDepth}
	if f.envErr != nil {
		return o, f.envErr
	}
	return o, o.check()
}

// check rejects values serve cannot work with.
func (o processorOptions) check() error {
	switch {
	case o.runtime == "":
		return errors.New("--container-runtime must not be empty")
	case o.memory == "":
		return errors.New("--processor-memory must not be empty")
	case o.workers < 1:
		return fmt.Errorf("--processor-workers must be at least 1, got %d", o.workers)
	case o.maxDepth < 1:
		return fmt.Errorf("--max-generation-depth must be at least 1, got %d", o.maxDepth)
	}
	return nil
}

// processorOptionsFrom reads the processor settings of serve.
func processorOptionsFrom(s *config.Settings) (processorOptions, error) {
	workers, errW := s.Int("processor-workers")
	depth, errD := s.Int("max-generation-depth")
	o := processorOptions{runtime: s.String("container-runtime"), secretsDir: s.String("secrets-dir"),
		memory: s.String("processor-memory"), trustedKeys: s.String("trusted-keys"), workers: workers, maxDepth: depth}
	if err := errors.Join(errW, errD); err != nil {
		return o, err
	}
	return o, o.check()
}
```

(import `internal/config`.)

`cmd/custos/serve.go`: split `runServe` after the flag parse and `procFlags.options()`:

```go
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
	// … the old tail unchanged (ctx, openServer, Handler, serveUntilDone), with
	// *dataDir → dataDir, *publicURL → publicURL, *addr → addr …
}
```

- [ ] **Step 3: Write the cobra command**

Add to `serve.go` (imports `cobra`, `internal/config`):

```go
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
```

Register `c.serveCmd()`.

Cobra prints `-h` help to stdout, the old dispatcher to stderr. In `cmd/custos/runs_test.go`, `TestServeProcessorFlags`: change

```go
	code, _, errs := runCmd(t, "serve", "-h")
```
to
```go
	code, out, errs := runCmd(t, "serve", "-h")
	errs += out // the old dispatcher prints help to stderr, cobra to stdout
```

(the `-container-runtime` substring checks still match cobra's `--container-runtime`.)

- [ ] **Step 4: Add the precedence tests**

Append to `cmd/custos/runs_test.go`:

```go
func TestServeEnvironmentAndFlagPrecedence(t *testing.T) {
	t.Setenv("CUSTOS_PROCESSOR_WORKERS", "many")
	if code, _, errs := runCmd(t, "serve", "--data-dir", t.TempDir()); code != 2 || !strings.Contains(errs, "CUSTOS_PROCESSOR_WORKERS") {
		t.Fatalf("malformed variable: code %d err %q", code, errs)
	}
	// Ruling 5.7: a flag that overrides the variable makes its malformed value irrelevant.
	// --addr 127.0.0.1:1 cannot be bound by a normal user, so serve stops with exit 1
	// *after* every setting was accepted.
	t.Setenv("CUSTOS_ADDR", "")
	code, _, errs := runCmd(t, "serve", "--data-dir", t.TempDir(), "--processor-workers", "3", "--addr", "127.0.0.1:1")
	if code == 2 {
		t.Errorf("flag over malformed variable was rejected: %q", errs)
	}
}
```

> **Note for the implementer:** in legacy mode this test fails at its second half (the old dispatcher reports the malformed variable even when the flag overrides it, ruling 5.7). Guard that half with `if legacyCLI { return }`, with a comment pointing at ruling 5.7.

- [ ] **Step 5: Run both modes**

```bash
go vet ./cmd/custos
go test ./cmd/custos -race -count=1
CUSTOS_TEST_LEGACY_CLI=1 go test ./cmd/custos -race -count=1
```

Expected: PASS in both.

- [ ] **Step 6: Commit**

```bash
git add cmd/custos/serve.go cmd/custos/auth.go cmd/custos/runs.go cmd/custos/runs_test.go cmd/custos/root.go cmd/custos/main.go
git commit -m "Port serve to cobra"
```

---

### Task 12: Cut over: cobra answers everything

**Files:**
- Modify: `cmd/custos/main.go`, `cmd/custos/main_test.go`, `cmd/custos/root.go`
- Test: `cmd/custos/main_test.go`, plus a global-flag test

**Interfaces:**
- Consumes: every `xCmd` from Tasks 3–11.
- Produces: the final `run`: `if legacyCLI { return runLegacy(...) }; return runCobra(...)`.

- [ ] **Step 1: Update and add the tests (red)**

In `cmd/custos/main_test.go`, change `TestHelp`'s assertion to `strings.Contains(out.String(), "validate")` (cobra's generated help lists `validate` under "Available Commands", not as `custos validate`). Append:

```go
func TestEveryCommandIsListed(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, nil, &out, &errb); code != 0 {
		t.Fatalf("exit code %d, stderr %s", code, errb.String())
	}
	for _, name := range []string{"serve", "validate", "task", "workspace", "clone", "push", "token", "processor"} {
		if !strings.Contains(out.String(), name) {
			t.Errorf("help does not list %q:\n%s", name, out.String())
		}
	}
}

func TestGlobalFlagsComeBeforeTheCommand(t *testing.T) {
	if legacyCLI {
		t.Skip("the old dispatcher has no global flags")
	}
	dir := t.TempDir()
	fixture.WriteDir(t, dir, fixture.Catalog())
	if code, out, errs := runCmd(t, "--config", filepath.Join(t.TempDir(), "none.yaml"), "validate", dir); code != 0 || out != "ok\n" {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
}
```

(imports: `path/filepath`, and the `fixture` package as `validate_test.go` imports it.)

Run: `go test ./cmd/custos -run 'TestHelp|TestEveryCommandIsListed|TestGlobalFlags' -count=1`
Expected: FAIL on `TestGlobalFlagsComeBeforeTheCommand` (`--config` is not a ported command, so `run` falls through to the old dispatcher: `unknown command "--config"`).

- [ ] **Step 2: Cut over**

In `main.go` replace `run` and delete `portedCommands`:

```go
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if legacyCLI {
		return runLegacy(args, stdin, stdout, stderr)
	}
	return runCobra(args, stdin, stdout, stderr)
}
```

In the `usage` constant's neighbor comment, say the constant is now used only by `runLegacy`.

- [ ] **Step 3: Run both modes and the whole suite**

```bash
make test
```

Expected: PASS (`make test` runs the `cmd/custos` tests in both modes since Task 2). `TestNoArguments` (exit 2) and `TestUnknownCommand` (`unknown command "frobnicate"`) now run against cobra.

- [ ] **Step 4: Commit**

```bash
git add cmd/custos/main.go cmd/custos/main_test.go cmd/custos/root.go
git commit -m "Cut the command line over to cobra"
```

---

### Task 13: Documentation and bookkeeping

**Files:**
- Modify: `README.md` ("Command line" section), `docs/superpowers/specs/2026-10-02-custos-design.md` (§6.2 table, new §11.5), `docs/superpowers/plans/2026-10-03-custos-phases.md` (Phase 5 row and section)

- [ ] **Step 1: README**

In the "Command line" section of `README.md` add `custos config` to the list:

```sh
custos config get server-url [--show-source]             # print the server URL custos would use
custos config set server-url URL                         # remember it in ~/.config/custos/config.yaml
```

and, after the paragraph that starts "`workspace create` and `serve` need `--data-dir`", add a subsection:

```markdown
### Settings: file, environment, flag

A setting can come from three places. A later one wins:

1. the personal config file, `~/.config/custos/config.yaml` (`$XDG_CONFIG_HOME/custos/config.yaml` when that is set to an absolute path; `--config FILE` picks another file). Only `server-url` is read from it; other keys are ignored with a warning, so a secret in that file is never used. `custos config set server-url URL` writes it with mode 0600.
2. an environment variable
3. a command-line flag

| Flag | Variable |
| --- | --- |
| `--server-url` | `CUSTOS_SERVER_URL` |
| `--data-dir` | `CUSTOS_DATA_DIR` |
| `--addr` | `CUSTOS_ADDR` |
| `--public-url` | `CUSTOS_PUBLIC_URL` |
| `--author` | `CUSTOS_AUTHOR` |
| `--container-runtime`, `--secrets-dir`, `--processor-memory`, `--processor-workers`, `--max-generation-depth`, `--trusted-keys` | `CUSTOS_` plus the flag name in capitals, `_` for `-` (`serve`; `processor test` reads only `CUSTOS_CONTAINER_RUNTIME`) |
| `--oidc-issuer-url`, `--oidc-client-id`, `--oidc-client-secret`, `--oidc-redirect-url`, `--admin-emails`, `--session-secret` | likewise (`serve`) |

An empty variable counts as unset. Flags that are not in this table (`--dir`, `--email`, `--admin`, …) are never read from the environment. `custos push` and `custos clone` send the token in `CUSTOS_TOKEN`. `--server-url` is a client-side setting, separate from the server's `--public-url`; today only `custos config get` uses it.
```

- [ ] **Step 2: Spec**

In §6.2 of the spec add the row `` | `custos config get|set` | the personal config file (§11.5, ruling 5.9) | ``. Add §11.5 "Phase 5 (cobra/viper command line)" after §11.4, containing the ten rulings from the table at the top of this plan, numbered 5.1–5.10, and add rulings made during execution (5.11 and on). Say there that 5.7's three changes are the only deliberate behavior changes.

- [ ] **Step 3: Phases file**

In `docs/superpowers/plans/2026-10-03-custos-phases.md`, add a row to the overview table:

```
| 5 cobra/viper command line | Replace the `flag` dispatcher with cobra; flag, environment and personal config file layered with viper; `custos config get|set` | Done, not yet merged to `main` |
```

and a "Phase 5 — cobra/viper command line" section linking this plan, listing the 13 tasks, naming the deferred items (ruling 5.10), and pointing to spec §11.5. Fill in the real status when the branch is finished (the plan is only written when this task runs, so write what actually happened).

- [ ] **Step 4: Run the full suite and commit**

```bash
make test
git add README.md docs/superpowers/specs/2026-10-02-custos-design.md docs/superpowers/plans/2026-10-03-custos-phases.md docs/superpowers/plans/2026-10-16-cobra-viper-cli.md
git commit -m "Document the cobra/viper command line and record the phase 5 rulings"
```

---

## Deferred (not in this plan)

1. **Delete the old dispatcher** (ruling 5.10a): `runLegacy`, `legacyCLI`, `legacy_hook_test.go`, the `runX` parsing wrappers, `defineAuthFlags`/`defineProcessorFlags`/`publicURLFlag`/`authFlagSet`/`processorFlags`, `envOr`/`envInt`, `helpOrUsage`, the `usage` constant, and the second `go test` line in the Makefile. Keep the `doX` cores. Re-run `make test` after each deletion; the `auth_test.go` tests that use `defineAuthFlags` move to `authValues`.
2. **`custos clone` by name** (ruling 5.10b): accept `workspaces/<uuid>` (or a bare UUID) and resolve it against `server-url`, which is the first real consumer of the setting. Decide then whether `workspace create` should default `--public-url` from `server-url` (rejected for now, ruling 5.5).

## Self-review

**Spec coverage.** Every point of the brief maps to a task:

| Requirement | Task |
| --- | --- |
| cobra for argument parsing | 2 (root), 4–11 (every command), 12 (cut-over) |
| viper; custos URL in a config file in the home directory | 1 (`internal/config`), 3 (`config get|set`) |
| env vars `CUSTOS_SERVER_URL`, `CUSTOS_DATA_DIR` | 1 (`EnvNames`), 3, 6, 7, 11 |
| `CUSTOS_USER_TOKEN` | Dropped by the project owner (ruling 5.6); `CUSTOS_TOKEN` untouched |
| precedence file < env < flag | 1 (`TestPrecedence`), 3 (end to end through the CLI) |
| Only `server-url` from the user's file | 1 (`TestFileOnlyServerURL`), 3 |
| Behavior preserved, duplicates removed later | 2 (legacy switch), 4–12 (both modes), Deferred item 1 |
| Note for `clone` by name | Deferred item 2 |

**Placeholder scan.** The ellipses inside Tasks 4–11 ("… the unchanged remainder …", "… the old tail unchanged …") each stand for a code block that is moved without edit and is named precisely, with the exact substitutions to make; no step says "add error handling" or leaves a behavior unspecified. The two "Note for the implementer" blocks (Tasks 8 and 11) are instructions to resolve a known legacy-versus-cobra difference, with the rule for resolving it.

**Type consistency.** `exitCode`, `usageError`, `result`, `exactArgs`, `cli`, `cobraCmd` (Task 2) are used under the same names in Tasks 3–12. `config.New(fl, keys, opts)` has the same signature in every caller. `tokenArgs`, `authValues`, `processorOptions.check` and `serveWith` are each defined once and used consistently. The spike behind this plan (a scratch module with the Task 1 package, the Task 2 root and Task 3 command) compiled and passed its tests with cobra v1.10.2, pflag v1.0.10 and viper v1.21.0.

**Review Focus coverage.** (1) Task 7; (2) Tasks 1, 3, 4; (3) Task 3 (`TestConfigNoHome`) and Task 1 (`TestDefaultPath`); (4) Task 8; (5) Tasks 1 and 3; (6) Task 11.
