# Phase 3a (Processor contract, runner, test images) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** custos can build the input of the processor contract `custos.processor/v1` for an answer, run a processor image in a locked-down container through the `docker`/`podman` CLI, and strictly parse and validate what it writes; five small test images (echo, generate, sign, fail, loop) exist for the tests of plans 3a–3e.

**Architecture:** Three new packages. `internal/contract` holds the JSON types of §5.3, `NewInput` and `ParseOutput` (pure, no I/O). `internal/runner` shells out to the container client: `Resolve` maps a digest-pinned reference to a local image or a pull, `Run` starts `docker run --read-only --network none --cap-drop ALL …`, feeds stdin, caps stdout and stderr, kills the container on timeout or cancellation, and reads the container state afterwards to tell "could not start" (`ErrUnavailable`) from "ran and failed" (`Result`). `internal/proctest` builds the test images `FROM scratch` with one static Go binary each (`internal/proctest/images/<name>`), once per test process.

**Tech Stack:** Go 1.26, the `docker` CLI (Docker ≥ 27; `podman` is supported by the same flags), `github.com/google/uuid`; standard library otherwise.

**Spec:** [docs/superpowers/specs/2026-10-02-custos-design.md](../specs/2026-10-02-custos-design.md), §5.1–§5.3 (packaging, running, contract), §7 (failed runs keep their log), §8 (contract tests with small test images) and §11 (rulings). Shared names and formats: [docs/superpowers/plans/2026-10-04-phase-3-architecture.md](2026-10-04-phase-3-architecture.md), section "Plan 3a". This is the first of the five phase-3 plans; it needs only phases 1 and 2 on `main`.

## Global Constraints

- Module `github.com/emeland-io/custos`, Go 1.26. Third-party modules stay `go.yaml.in/yaml/v3`, `golang.org/x/mod`, `github.com/google/uuid`; nothing is added by this plan.
- Processors run through the `docker` or `podman` command-line client via `os/exec`; no Docker SDK. `runner.Config.Runtime` defaults to `docker`.
- **Tests use real Docker:** every test that runs a processor runs a real container. No fake runner, no `t.Skip` when Docker is missing — such tests fail.
- Test images are `FROM scratch` plus a static Linux binary built with `CGO_ENABLED=0 GOOS=linux GOARCH=<runtime.GOARCH>`; building them needs no network.
- Contract version string `custos.processor/v1`; `contract.MaxOutputSize = 16 << 20`; stdout above it fails the run; at most 1 MiB of stderr is kept, then `\n[log truncated]\n`.
- Container sandbox (§5.2): read-only root filesystem, no network unless `Job.Network`, memory limit (default `512m`), no capabilities, no new privileges, a small tmpfs at `/tmp`, secrets read-only at `/run/secrets/<name>` from `<SecretsDir>/<name>`, attachments read-only at `/input/blobs/<sha256>`; default timeout 60s.
- Exported names and JSON field names exactly as in the architecture note, section "Plan 3a".
- Commit messages: one imperative sentence without prefix, a blank line, and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **A processor exits 0 but writes nothing, or writes something that is not JSON.** The run must fail with a message that says so (not a decoder message about EOF), so the engineer reading the run knows what to fix (tests `TestParseOutputRejectsMalformedOutput` in Task 1 and `TestGenerateStderrGarbageAndExit` in Task 4).
2. **A processor hangs, or ignores signals.** It must be killed when the timeout expires, the run reported as timed out, and no container left behind (test `TestRunTimeoutKillsAndRemovesContainer` in Task 3).
3. **A processor floods stdout or stderr.** custos must keep at most `MaxOutputSize+1` bytes of stdout and 1 MiB of log, never block the container, and fail the run on oversize output (tests `TestRunOutputIsCapped` in Task 3 and `TestLongLogIsTruncated` in Task 4).
4. **The run cannot start: missing secret, image not pullable, Docker not installed, attachment file missing.** That is `ErrUnavailable` naming the cause, not a failed processor with exit code 125 (test `TestRunUnavailable` in Task 3).
5. **custos shuts down while a processor runs.** The container must be killed and removed, and `Run` must return the context's error rather than a result that looks like a processor failure (test `TestRunCancelled` in Task 3).

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/contract/contract.go` | Package doc; `Version`, `MaxOutputSize`, input and output types, `BlobPath`, `NewInput` |
| `internal/contract/output.go` | `ParseOutput`: strict decoding, defaults, validation |
| `internal/contract/*_test.go` | Unit tests |
| `internal/proctest/proctest.go` | `Image` (build and cache test images), `SigningKey`, `SigningKeySecret`, `Names` |
| `internal/proctest/images/{echo,fail,loop,generate,sign}/main.go` | The five test processors |
| `internal/proctest/images/directives/` | Shared core of generate and sign: directives, in-toto statement, DSSE envelope |
| `internal/proctest/proctest_test.go` | Image building and caching |
| `internal/proctest/images_test.go` | Behaviour of generate and sign, run through `runner` |
| `internal/runner/runner.go` | `Config`, `Job`, `Result`, `ErrUnavailable`, `MaxLogSize`, `New`, `Run` |
| `internal/runner/resolve.go` | `Resolve`, local image lookup, pulling |
| `internal/runner/runner_test.go` | Real containers: echo, fail, timeout, cancellation, sandbox flags, unavailability, caps |

---
### Task 1: Contract types, `NewInput` and `ParseOutput`

**Files:**
- Create: `internal/contract/contract.go`
- Create: `internal/contract/output.go`
- Test: `internal/contract/contract_test.go`, `internal/contract/output_test.go`

**Interfaces:**
- Consumes: `task.Version` (embeds `task.Meta{ID, Version, Title, AnswerType, Choices, Previous}`, plus `Body`, `Path`), `task.AnswerType` with `Valid()`, `task.AnswerMarkdown`, `task.AnswerChoice`, `task.ValidID(string) bool`, `workspace.Answer{Task, TaskVersion, Type, Value, Attachments, Body, Path}`, `workspace.Attachment{Name, SHA256, MediaType}`, `semver.Step`, `semver.Patch/Minor/Major`, `semver.Valid`; in tests `fixture.TaskA`, `fixture.TaskB`, `fixture.WorkspaceID`, `fixture.SHA256`, `fixture.TaskPath`.
- Produces:
  - `const Version = "custos.processor/v1"`, `const MaxOutputSize = 16 << 20`
  - types `Input`, `InputWorkspace`, `InputTask`, `InputAnswer`, `InputAttachment`, `Output`, `OutputTask`, `Origin`, `OutputDocument` exactly as in the architecture note
  - `func BlobPath(sha256 string) string` — `"/input/blobs/<sha256>"` (used by `runner`)
  - `func NewInput(workspaceID string, v *task.Version, a *workspace.Answer) Input` — `Answer.Value` nil for markdown answers, `Attachments` never nil
  - `func ParseOutput(data []byte) (*Output, error)` — on success `Tasks` and `Documents` are non-nil and every `Bump` is set; on failure one error `output is not valid: <problem>; <problem>…` (or a single reason for empty, oversize, non-object or trailing data)

- [ ] **Step 1: Write the failing tests** `internal/contract/contract_test.go`

```go
package contract

import (
	"encoding/json"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

func taskVersion(answerType task.AnswerType, choices ...string) *task.Version {
	return &task.Version{
		Meta: task.Meta{ID: fixture.TaskB, Version: "1.0.0", Title: "Hosts", AnswerType: answerType, Choices: choices},
		Body: "List the hosts.\n",
		Path: fixture.TaskPath(fixture.TaskB, "1.0.0"),
	}
}

func TestNewInputText(t *testing.T) {
	a := &workspace.Answer{
		Task: fixture.TaskB, TaskVersion: "1.0.0", Type: task.AnswerText, Value: "web-01",
		Attachments: []workspace.Attachment{{Name: "scan.pdf", SHA256: fixture.SHA256, MediaType: "application/pdf"}},
		Path:        "answers/" + fixture.TaskB + ".md",
	}
	got, err := json.Marshal(NewInput(fixture.WorkspaceID, taskVersion(task.AnswerText), a))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"contract":"custos.processor/v1","workspace":{"id":"` + fixture.WorkspaceID + `"},` +
		`"task":{"id":"` + fixture.TaskB + `","version":"1.0.0","title":"Hosts","body":"List the hosts.\n","answer_type":"text"},` +
		`"answer":{"task_version":"1.0.0","value":"web-01","body":"",` +
		`"attachments":[{"name":"scan.pdf","sha256":"` + fixture.SHA256 + `","media_type":"application/pdf","path":"/input/blobs/` + fixture.SHA256 + `"}]}}`
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestNewInputMarkdownHasNullValueAndEmptyAttachments(t *testing.T) {
	a := &workspace.Answer{Task: fixture.TaskB, TaskVersion: "1.0.0", Type: task.AnswerMarkdown, Body: "# Hosts\n\n- web-01\n"}
	got, err := json.Marshal(NewInput(fixture.WorkspaceID, taskVersion(task.AnswerMarkdown), a))
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Answer map[string]json.RawMessage `json:"answer"`
	}
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatal(err)
	}
	if v := string(back.Answer["value"]); v != "null" {
		t.Errorf("value %s, want null", v)
	}
	if v := string(back.Answer["attachments"]); v != "[]" {
		t.Errorf("attachments %s, want []", v)
	}
	if v := string(back.Answer["body"]); v != `"# Hosts\n\n- web-01\n"` {
		t.Errorf("body %s", v)
	}
}

func TestNewInputChoiceKeepsChoices(t *testing.T) {
	a := &workspace.Answer{Task: fixture.TaskB, TaskVersion: "1.0.0", Type: task.AnswerChoice, Value: "yes"}
	in := NewInput(fixture.WorkspaceID, taskVersion(task.AnswerChoice, "yes", "no"), a)
	if len(in.Task.Choices) != 2 || in.Task.Choices[0] != "yes" || in.Answer.Value == nil || *in.Answer.Value != "yes" {
		t.Errorf("input %+v", in)
	}
}
```

and `internal/contract/output_test.go`

```go
package contract

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

func TestParseOutputValid(t *testing.T) {
	out, err := ParseOutput([]byte(`
	{"tasks": [
	  {"match_key": "host:web-01", "title": "Host web-01", "body": "Patch it.", "answer_type": "timestamp",
	   "origin": {"id": "` + fixture.TaskA + `", "version": "1.1.0"}, "processor": "host-scanner", "bump": "major"},
	  {"match_key": "host:web-02", "title": "Host web-02", "answer_type": "choice", "choices": ["yes", "no"]}],
	 "documents": [
	  {"match_key": "slsa:web-01", "name": "provenance", "media_type": "application/vnd.in-toto+json",
	   "content": {"_type": "https://in-toto.io/Statement/v1"}}]}
	`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tasks) != 2 || len(out.Documents) != 1 {
		t.Fatalf("output %+v", out)
	}
	t0, t1 := out.Tasks[0], out.Tasks[1]
	if t0.MatchKey != "host:web-01" || t0.AnswerType != task.AnswerTimestamp || t0.Origin == nil || t0.Origin.ID != fixture.TaskA ||
		t0.Origin.Version != "1.1.0" || t0.Processor != "host-scanner" || t0.Bump != semver.Major || t0.Body != "Patch it." {
		t.Errorf("task 0 %+v", t0)
	}
	if t1.Bump != semver.Minor {
		t.Errorf("bump %q, want the default minor", t1.Bump)
	}
	if len(t1.Choices) != 2 || t1.Origin != nil {
		t.Errorf("task 1 %+v", t1)
	}
	if d := out.Documents[0]; d.Name != "provenance" || string(d.Content) != `{"_type": "https://in-toto.io/Statement/v1"}` {
		t.Errorf("document %+v content %s", d, d.Content)
	}
}

func TestParseOutputEmptyListsAreNotNull(t *testing.T) {
	for _, in := range []string{`{}`, `{"tasks": null, "documents": null}`, `{"tasks": [], "documents": []}`} {
		out, err := ParseOutput([]byte(in))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if out.Tasks == nil || out.Documents == nil || len(out.Tasks)+len(out.Documents) != 0 {
			t.Errorf("%s: %+v", in, out)
		}
	}
}

func TestParseOutputRejectsMalformedOutput(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"empty", "", "output is empty"},
		{"blank", " \n\t", "output is empty"},
		{"not json", "not json", "not a JSON object"},
		{"array", `[]`, "not a JSON object"},
		{"null", `null`, "not a JSON object"},
		{"unknown top-level field", `{"tasks": [], "warnings": []}`, `unknown field "warnings"`},
		{"unknown task field", `{"tasks": [{"match_key": "a", "title": "A", "answer_type": "text", "priority": 1}]}`, `unknown field "priority"`},
		{"trailing object", `{"tasks": []} {"tasks": []}`, "data after the JSON object"},
		{"trailing garbage", `{"tasks": []} x`, "data after the JSON object"},
		{"truncated", `{"tasks": [`, "output is not valid"},
		{"wrong type", `{"tasks": {}}`, "output is not valid"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseOutput([]byte(c.in))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestParseOutputSizeLimit(t *testing.T) {
	pad := strings.Repeat(" ", MaxOutputSize-len(`{}`))
	if _, err := ParseOutput([]byte(`{}` + pad)); err != nil {
		t.Errorf("exactly MaxOutputSize bytes: %v", err)
	}
	_, err := ParseOutput([]byte(`{}` + pad + " "))
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err %v, want a size error", err)
	}
}

func TestParseOutputReportsAllProblems(t *testing.T) {
	_, err := ParseOutput([]byte(`{
	 "tasks": [
	  {"match_key": "", "title": "No key", "answer_type": "text"},
	  {"match_key": "a", "title": " ", "answer_type": "essay"},
	  {"match_key": "a", "title": "Dup", "answer_type": "text", "choices": ["x"]},
	  {"match_key": "c", "title": "C", "answer_type": "choice"},
	  {"match_key": "d", "title": "D", "answer_type": "choice", "choices": ["x", "x", ""]},
	  {"match_key": "e", "title": "E", "answer_type": "text", "origin": {"id": "not-a-uuid", "version": "v1"}},
	  {"match_key": "f", "title": "F", "answer_type": "text", "processor": "Host_Scanner", "bump": "huge"}],
	 "documents": [
	  {"match_key": "x", "name": "", "media_type": "", "content": null},
	  {"match_key": "x", "name": "X", "media_type": "text/plain"},
	  {"name": "Y", "media_type": "text/plain", "content": "plain text is a JSON value"}]}`))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{
		"tasks[0]: match_key is missing",
		`tasks[1] (match_key "a"): title is missing`,
		`tasks[1] (match_key "a"): answer_type "essay" is not one of`,
		`tasks[2] (match_key "a"): match_key is used by an earlier task`,
		`tasks[2] (match_key "a"): choices are only allowed with answer_type choice`,
		`tasks[3] (match_key "c"): answer_type choice needs a non-empty choices list`,
		`tasks[4] (match_key "d"): duplicate choice "x"`,
		`tasks[4] (match_key "d"): choices must not be empty`,
		`tasks[5] (match_key "e"): origin.id "not-a-uuid" is not a lowercase UUID v4`,
		`tasks[5] (match_key "e"): origin.version "v1" is not a semantic version`,
		`tasks[6] (match_key "f"): processor "Host_Scanner" must match`,
		`tasks[6] (match_key "f"): bump "huge" is not one of patch, minor, major`,
		`documents[0] (match_key "x"): name is missing`,
		`documents[0] (match_key "x"): media_type is missing`,
		`documents[0] (match_key "x"): content is missing`,
		`documents[1] (match_key "x"): match_key is used by an earlier document`,
		`documents[1] (match_key "x"): content is missing`,
		"documents[2]: match_key is missing",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not contain %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "documents[2]: content") {
		t.Errorf("a JSON string is valid content:\n%v", err)
	}
}

func TestParseOutputSameKeyForTaskAndDocument(t *testing.T) {
	_, err := ParseOutput([]byte(`{"tasks": [{"match_key": "k", "title": "T", "answer_type": "text"}],
	 "documents": [{"match_key": "k", "name": "D", "media_type": "text/plain", "content": 1}]}`))
	if err != nil {
		t.Errorf("match keys are unique per kind, so a task and a document may share one: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/contract/`
Expected: FAIL, `undefined: NewInput` and `undefined: ParseOutput`

- [ ] **Step 3: Write `internal/contract/contract.go`**

```go
// Package contract defines the processor contract custos.processor/v1
// (spec §5.3): the JSON input a processor reads on stdin and the JSON output
// it writes on stdout.
package contract

import (
	"encoding/json"

	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// Version names the contract in Input.Contract.
const Version = "custos.processor/v1"

// MaxOutputSize is the largest stdout a run may produce; more fails the run.
const MaxOutputSize = 16 << 20

// Input is what a processor reads on stdin.
type Input struct {
	Contract  string         `json:"contract"`
	Workspace InputWorkspace `json:"workspace"`
	Task      InputTask      `json:"task"`
	Answer    InputAnswer    `json:"answer"`
}

// InputWorkspace names the workspace the answer belongs to.
type InputWorkspace struct {
	ID string `json:"id"`
}

// InputTask is the task version the answer was written for.
type InputTask struct {
	ID         string          `json:"id"`
	Version    string          `json:"version"`
	Title      string          `json:"title"`
	Body       string          `json:"body"`
	AnswerType task.AnswerType `json:"answer_type"`
	Choices    []string        `json:"choices,omitempty"`
}

// InputAnswer is the answer the processor works on.
type InputAnswer struct {
	TaskVersion string            `json:"task_version"`
	Value       *string           `json:"value"` // null for markdown answers
	Body        string            `json:"body"`
	Attachments []InputAttachment `json:"attachments"` // never null
}

// InputAttachment is one attachment of the answer, mounted read-only at Path.
type InputAttachment struct {
	Name      string `json:"name"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type"`
	Path      string `json:"path"` // "/input/blobs/<sha256>"
}

// Output is what a processor writes on stdout.
type Output struct {
	Tasks     []OutputTask     `json:"tasks"`
	Documents []OutputDocument `json:"documents"`
}

// OutputTask is one generated task the processor wants to exist.
type OutputTask struct {
	MatchKey   string          `json:"match_key"`
	Title      string          `json:"title"`
	Body       string          `json:"body"`
	AnswerType task.AnswerType `json:"answer_type"`
	Choices    []string        `json:"choices,omitempty"` // required for choice, forbidden otherwise
	Origin     *Origin         `json:"origin,omitempty"`
	Processor  string          `json:"processor,omitempty"`
	Bump       semver.Step     `json:"bump,omitempty"` // ParseOutput sets "minor" when empty
}

// Origin places a generated task below another task in the book.
type Origin struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
}

// OutputDocument is one document the processor wants to exist.
type OutputDocument struct {
	MatchKey  string          `json:"match_key"`
	Name      string          `json:"name"`
	MediaType string          `json:"media_type"`
	Content   json.RawMessage `json:"content"` // any JSON value except null
}

// BlobPath is where the attachment with the given hash is mounted.
func BlobPath(sha256 string) string { return "/input/blobs/" + sha256 }

// NewInput builds the input for one answer. path of attachment i is
// "/input/blobs/<sha256>".
func NewInput(workspaceID string, v *task.Version, a *workspace.Answer) Input {
	in := Input{
		Contract:  Version,
		Workspace: InputWorkspace{ID: workspaceID},
		Task: InputTask{
			ID:         v.ID,
			Version:    v.Version,
			Title:      v.Title,
			Body:       v.Body,
			AnswerType: v.AnswerType,
			Choices:    v.Choices,
		},
		Answer: InputAnswer{
			TaskVersion: a.TaskVersion,
			Body:        a.Body,
			Attachments: make([]InputAttachment, 0, len(a.Attachments)),
		},
	}
	if a.Type != task.AnswerMarkdown {
		value := a.Value
		in.Answer.Value = &value
	}
	for _, at := range a.Attachments {
		in.Answer.Attachments = append(in.Answer.Attachments, InputAttachment{
			Name:      at.Name,
			SHA256:    at.SHA256,
			MediaType: at.MediaType,
			Path:      BlobPath(at.SHA256),
		})
	}
	return in
}
```

- [ ] **Step 4: Write `internal/contract/output.go`**

```go
package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

var processorRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ParseOutput decodes stdout strictly (unknown fields, trailing data and
// sizes above MaxOutputSize are errors), applies defaults, and validates:
// match_key present and unique per kind, title present, answer_type valid,
// choices rules, origin id a UUID v4 and version a semver, processor name
// matching [a-z0-9][a-z0-9-]*, bump one of patch/minor/major, document
// name/media_type present, content present. All problems are reported in
// one error. Whether a named processor exists is checked by 3c, which has
// the catalog.
func ParseOutput(data []byte) (*Output, error) {
	if len(data) > MaxOutputSize {
		return nil, fmt.Errorf("output is larger than %d bytes", MaxOutputSize)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("output is empty; a processor must write a JSON object to stdout")
	}
	if trimmed[0] != '{' {
		return nil, errors.New("output is not a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var out Output
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("output is not valid: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("output has data after the JSON object")
	}
	if out.Tasks == nil {
		out.Tasks = []OutputTask{}
	}
	if out.Documents == nil {
		out.Documents = []OutputDocument{}
	}
	var ps []string
	add := func(format string, args ...any) { ps = append(ps, fmt.Sprintf(format, args...)) }
	keys := map[string]bool{}
	for i := range out.Tasks {
		t := &out.Tasks[i]
		at := fmt.Sprintf("tasks[%d]", i)
		if strings.TrimSpace(t.MatchKey) == "" {
			add("%s: match_key is missing", at)
		} else {
			at = fmt.Sprintf("tasks[%d] (match_key %q)", i, t.MatchKey)
			if keys[t.MatchKey] {
				add("%s: match_key is used by an earlier task", at)
			}
			keys[t.MatchKey] = true
		}
		if strings.TrimSpace(t.Title) == "" {
			add("%s: title is missing", at)
		}
		if !t.AnswerType.Valid() {
			add("%s: answer_type %q is not one of markdown, text, timestamp, path, url, choice", at, t.AnswerType)
		}
		if t.AnswerType == task.AnswerChoice {
			if len(t.Choices) == 0 {
				add("%s: answer_type choice needs a non-empty choices list", at)
			}
			seen := map[string]bool{}
			for _, c := range t.Choices {
				if strings.TrimSpace(c) == "" {
					add("%s: choices must not be empty", at)
				} else if seen[c] {
					add("%s: duplicate choice %q", at, c)
				}
				seen[c] = true
			}
		} else if len(t.Choices) > 0 {
			add("%s: choices are only allowed with answer_type choice", at)
		}
		if t.Origin != nil {
			if !task.ValidID(t.Origin.ID) {
				add("%s: origin.id %q is not a lowercase UUID v4", at, t.Origin.ID)
			}
			if t.Origin.Version != "" && !semver.Valid(t.Origin.Version) {
				add("%s: origin.version %q is not a semantic version such as 1.2.0", at, t.Origin.Version)
			}
		}
		if t.Processor != "" && !processorRE.MatchString(t.Processor) {
			add("%s: processor %q must match [a-z0-9][a-z0-9-]*", at, t.Processor)
		}
		switch t.Bump {
		case "":
			t.Bump = semver.Minor
		case semver.Patch, semver.Minor, semver.Major:
		default:
			add("%s: bump %q is not one of patch, minor, major", at, t.Bump)
		}
	}
	keys = map[string]bool{}
	for i, d := range out.Documents {
		at := fmt.Sprintf("documents[%d]", i)
		if strings.TrimSpace(d.MatchKey) == "" {
			add("%s: match_key is missing", at)
		} else {
			at = fmt.Sprintf("documents[%d] (match_key %q)", i, d.MatchKey)
			if keys[d.MatchKey] {
				add("%s: match_key is used by an earlier document", at)
			}
			keys[d.MatchKey] = true
		}
		if strings.TrimSpace(d.Name) == "" {
			add("%s: name is missing", at)
		}
		if strings.TrimSpace(d.MediaType) == "" {
			add("%s: media_type is missing", at)
		}
		if len(d.Content) == 0 || string(d.Content) == "null" {
			add("%s: content is missing", at)
		}
	}
	if len(ps) > 0 {
		return nil, fmt.Errorf("output is not valid: %s", strings.Join(ps, "; "))
	}
	return &out, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go vet ./internal/contract/ && go test ./internal/contract/ -v`
Expected: PASS (9 tests). `json.RawMessage` receives the literal `null` for `"content": null`, which is why `ParseOutput` compares against the string `"null"`.

- [ ] **Step 6: Commit**

```bash
git add internal/contract
git commit -m "Define the processor contract and parse processor output strictly

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Build test images once per process

**Files:**
- Create: `internal/proctest/proctest.go`
- Create: `internal/proctest/images/echo/main.go`, `internal/proctest/images/fail/main.go`, `internal/proctest/images/loop/main.go`
- Test: `internal/proctest/proctest_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks; needs `go` and `docker` on `PATH`.
- Produces:
  - `var Names = []string{"echo", "generate", "sign", "fail", "loop"}`
  - `func Image(t testing.TB, name string) string` — `"custos.test/<name>@sha256:<image-id-hex>"`; the image is also tagged `custos.test/<name>:latest`; fails the test when the build fails (Docker unavailable)
  - (unexported) `func build(name string) (string, error)`, `var idRE`
  - Images: **echo** (no tasks, one document `input` whose content is the compacted input JSON), **fail** (reads stdin, writes `boom\n` to stderr, exits 3), **loop** (sleeps until killed). `generate` and `sign` follow in Tasks 4 and 5; `Image(t, "generate")` fails until then with a `go build` error.

- [ ] **Step 1: Write the failing test** `internal/proctest/proctest_test.go`

```go
package proctest

import (
	"os/exec"
	"strings"
	"testing"
)

func TestImageBuildsOncePerProcess(t *testing.T) {
	ref := Image(t, "echo")
	name, id, ok := strings.Cut(ref, "@")
	if !ok || name != "custos.test/echo" || !idRE.MatchString(id) {
		t.Fatalf("ref %q, want custos.test/echo@sha256:<64 hex>", ref)
	}
	out, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", id).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != id {
		t.Fatalf("docker image inspect %s: %v\n%s", id, err, out)
	}
	if again := Image(t, "echo"); again != ref {
		t.Errorf("second call returned %q, want the cached %q", again, ref)
	}
}

func TestImageUnknownName(t *testing.T) {
	if _, err := build("nope"); err == nil || !strings.Contains(err.Error(), "unknown test image") {
		t.Errorf("err %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/proctest/`
Expected: FAIL, `undefined: Image` (and `idRE`, `build`)

- [ ] **Step 3: Write `internal/proctest/proctest.go`**

```go
// Package proctest builds the container images that tests of processor runs
// use (spec §8): echo, generate, sign, fail and loop. Each image is
// FROM scratch plus one static Linux binary built from
// internal/proctest/images/<name>, so building needs Docker but no network.
// Only test code imports this package.
package proctest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Names lists the test images Image can build.
var Names = []string{"echo", "generate", "sign", "fail", "loop"}

var idRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type built struct {
	ref string
	err error
}

var (
	mu     sync.Mutex
	images = map[string]*built{}
)

// Image builds the named test image once per process and returns
// "custos.test/<name>@sha256:<image-id-hex>". Names: echo, generate, sign,
// fail, loop. Fails the test when Docker is unavailable.
func Image(t testing.TB, name string) string {
	t.Helper()
	mu.Lock()
	b, ok := images[name]
	if !ok {
		b = &built{}
		b.ref, b.err = build(name)
		images[name] = b
	}
	mu.Unlock()
	if b.err != nil {
		t.Fatalf("test image %s: %v", name, b.err)
	}
	return b.ref
}

func build(name string) (string, error) {
	known := false
	for _, n := range Names {
		known = known || n == name
	}
	if !known {
		return "", fmt.Errorf("unknown test image, want one of %s", strings.Join(Names, ", "))
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate the sources of package proctest")
	}
	src := filepath.Join(filepath.Dir(file), "images", name)
	dir, err := os.MkdirTemp("", "custos-proctest-"+name+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	gobuild := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, "processor"), ".")
	gobuild.Dir = src
	gobuild.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := gobuild.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %v\n%s", err, out)
	}
	dockerfile := "FROM scratch\nCOPY processor /processor\nENTRYPOINT [\"/processor\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	docker := exec.Command("docker", "build", "--quiet", "--tag", "custos.test/"+name+":latest", dir)
	docker.Stdout, docker.Stderr = &stdout, &stderr
	if err := docker.Run(); err != nil {
		return "", fmt.Errorf("docker build (is Docker running?): %v\n%s", err, stderr.Bytes())
	}
	id := strings.TrimSpace(stdout.String())
	if !idRE.MatchString(id) {
		return "", fmt.Errorf("docker build printed %q, want an image id sha256:<64 hex digits>", id)
	}
	return "custos.test/" + name + "@" + id, nil
}
```

- [ ] **Step 4: Write the three simple images**

`internal/proctest/images/echo/main.go`:

```go
// Command echo is the test processor that returns its input: no tasks and
// one document "input" whose content is the input JSON object.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func main() {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, in); err != nil {
		fail(fmt.Errorf("input is not JSON: %w", err))
	}
	out := map[string]any{
		"tasks": []any{},
		"documents": []any{map[string]any{
			"match_key":  "input",
			"name":       "input",
			"media_type": "application/json",
			"content":    json.RawMessage(compact.Bytes()),
		}},
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "echo:", err)
	os.Exit(1)
}
```

`internal/proctest/images/fail/main.go`:

```go
// Command fail is the test processor that always fails: it writes "boom" to
// stderr and exits with code 3.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	_, _ = io.Copy(io.Discard, os.Stdin)
	fmt.Fprintln(os.Stderr, "boom")
	os.Exit(3)
}
```

`internal/proctest/images/loop/main.go` (a `select {}` would make the Go runtime exit with "all goroutines are asleep"):

```go
// Command loop is the test processor that never finishes: it sleeps until
// it is killed.
package main

import "time"

func main() {
	for {
		time.Sleep(time.Hour)
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go vet ./internal/proctest/... && go test ./internal/proctest/... -v`
Expected: PASS (2 tests). The first build takes a few seconds. If it fails with `docker build (is Docker running?)`, start Docker; the test must not be skipped.

- [ ] **Step 6: Commit**

```bash
git add internal/proctest
git commit -m "Build the echo, fail and loop test processor images from scratch

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Run processor containers

**Files:**
- Create: `internal/runner/runner.go`
- Create: `internal/runner/resolve.go`
- Test: `internal/runner/runner_test.go`

**Interfaces:**
- Consumes: `contract.MaxOutputSize`, `contract.BlobPath`, `contract.ParseOutput` (Task 1); `proctest.Image` with images echo, fail, loop (Task 2); `uuid.NewString`.
- Produces:
  - `type Config`, `type Job`, `type Result`, `var ErrUnavailable`, `func New(Config) *Runner`, `func (*Runner) Run(ctx, Job) (*Result, error)`, `func (*Runner) Resolve(ctx, image string) (ref, digest string, err error)` exactly as in the architecture note
  - `const MaxLogSize = 1 << 20`
  - `Result.ExitCode` is `-1` when `TimedOut`; when the container was killed for memory, the log ends with `\n[killed: out of memory, limit <memory>]\n`
  - `Run` returns an error wrapping `ctx.Err()` (not `ErrUnavailable`) when ctx is cancelled before the container ends
  - `Run` calls `Resolve` itself, so `Job.Image` may be a `processors.yaml` reference, a local tag, or a `ref` returned by `Resolve`

How `Run` tells the cases apart: it checks secrets and attachment files before anything else (missing → `ErrUnavailable`), resolves and, if the image is not local, pulls it (`docker pull`; failure → `ErrUnavailable`), then runs `docker run --name custos-run-<uuid> --pull never …` without `--rm`. After the client returns, `docker container inspect` reads the container state: no container means the client could not create it (`ErrUnavailable` with the client's message); a non-empty `State.Error` means the runtime could not start it, e.g. a missing entrypoint (`ErrUnavailable`); otherwise `State.ExitCode` is the processor's exit code. The container is always removed with `docker rm --force` at the end. On timeout or cancellation `Run` sends `docker kill`, waits up to 10s for the client, then kills the client process.

- [ ] **Step 1: Write the failing tests** `internal/runner/runner_test.go`

```go
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/proctest"
)

const input = `{"contract":"custos.processor/v1","workspace":{"id":"5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9"}}`

func TestRunEcho(t *testing.T) {
	r := New(Config{})
	res, err := r.Run(context.Background(), Job{Image: proctest.Image(t, "echo"), Input: []byte(input)})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.TimedOut || res.Duration <= 0 {
		t.Fatalf("result %+v, log %s", res, res.Log)
	}
	out, err := contract.ParseOutput(res.Stdout)
	if err != nil {
		t.Fatalf("%v\nstdout: %s", err, res.Stdout)
	}
	if len(out.Tasks) != 0 || len(out.Documents) != 1 {
		t.Fatalf("output %+v", out)
	}
	if d := out.Documents[0]; d.MatchKey != "input" || d.Name != "input" || d.MediaType != "application/json" || string(d.Content) != input {
		t.Errorf("document %+v, content %s", d, d.Content)
	}
}

func TestRunFailKeepsLogAndExitCode(t *testing.T) {
	res, err := New(Config{}).Run(context.Background(), Job{Image: proctest.Image(t, "fail"), Input: []byte(input)})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.TimedOut || string(res.Log) != "boom\n" || len(res.Stdout) != 0 {
		t.Errorf("result %+v, log %q", res, res.Log)
	}
}

func TestRunTimeoutKillsAndRemovesContainer(t *testing.T) {
	img := proctest.Image(t, "loop")
	marker := filepath.Join(t.TempDir(), "marker") // tells this test's container from others
	writeFile(t, marker, "x")
	start := time.Now()
	res, err := New(Config{}).Run(context.Background(), Job{
		Image: img, Timeout: 2 * time.Second, Blobs: map[string]string{strings.Repeat("ef", 32): marker},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.ExitCode != -1 {
		t.Errorf("result %+v", res)
	}
	if d := time.Since(start); d < 2*time.Second || d > 20*time.Second {
		t.Errorf("run took %v, want about 2s", d)
	}
	if left := containersOf(t, img, marker); len(left) != 0 {
		t.Errorf("containers left behind: %v", left)
	}
}

func TestRunDefaultTimeout(t *testing.T) {
	res, err := New(Config{DefaultTimeout: time.Second}).Run(context.Background(), Job{Image: proctest.Image(t, "loop")})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Errorf("result %+v, want a timeout from Config.DefaultTimeout", res)
	}
}

func TestRunCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(time.Second, cancel)
	_, err := New(Config{}).Run(ctx, Job{Image: proctest.Image(t, "loop"), Timeout: time.Minute})
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnavailable) {
		t.Errorf("err %v, want context.Canceled", err)
	}
}

// TestRunSandbox inspects a running container: the flags of §5.2 and the
// read-only mounts of secrets and attachments.
func TestRunSandbox(t *testing.T) {
	img := proctest.Image(t, "loop")
	secrets := t.TempDir()
	writeFile(t, filepath.Join(secrets, "api-token"), "s3cret")
	blob := filepath.Join(t.TempDir(), "blob")
	writeFile(t, blob, "attachment")
	sha := strings.Repeat("ab", 32)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := New(Config{SecretsDir: secrets, Memory: "256m"}).Run(ctx, Job{
			Image: img, Timeout: time.Minute, Secrets: []string{"api-token"}, Blobs: map[string]string{sha: blob},
		})
		done <- err
	}()
	var ids []string
	for deadline := time.Now().Add(30 * time.Second); len(ids) == 0 && time.Now().Before(deadline); {
		time.Sleep(200 * time.Millisecond)
		ids = containersOf(t, img, blob)
	}
	if len(ids) != 1 {
		t.Fatalf("running containers with the blob mounted: %v", ids)
	}
	var info []struct {
		HostConfig struct {
			ReadonlyRootfs bool
			NetworkMode    string
			CapDrop        []string
			SecurityOpt    []string
			Memory         int64
			MemorySwap     int64
			Tmpfs          map[string]string
		}
		Mounts []struct {
			Source, Destination string
			RW                  bool
		}
	}
	out, err := exec.Command("docker", "container", "inspect", ids[0]).Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out, &info); err != nil || len(info) != 1 {
		t.Fatalf("inspect: %v\n%s", err, out)
	}
	hc := info[0].HostConfig
	if !hc.ReadonlyRootfs || hc.NetworkMode != "none" || strings.Join(hc.CapDrop, ",") != "ALL" ||
		strings.Join(hc.SecurityOpt, ",") != "no-new-privileges" || hc.Memory != 256<<20 || hc.MemorySwap != 256<<20 {
		t.Errorf("host config %+v", hc)
	}
	if _, ok := hc.Tmpfs["/tmp"]; !ok {
		t.Errorf("no tmpfs at /tmp: %v", hc.Tmpfs)
	}
	mounts := map[string]bool{}
	for _, m := range info[0].Mounts {
		if m.RW {
			t.Errorf("mount %s is writable", m.Destination)
		}
		mounts[m.Destination] = true
	}
	if !mounts["/run/secrets/api-token"] || !mounts["/input/blobs/"+sha] {
		t.Errorf("mounts %+v", info[0].Mounts)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("err %v", err)
	}
}

func TestRunNetworkAllowed(t *testing.T) {
	img := proctest.Image(t, "loop")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	marker := filepath.Join(t.TempDir(), "marker")
	writeFile(t, marker, "x")
	done := make(chan error, 1)
	go func() {
		_, err := New(Config{}).Run(ctx, Job{Image: img, Timeout: time.Minute, Network: true, Blobs: map[string]string{strings.Repeat("cd", 32): marker}})
		done <- err
	}()
	var ids []string
	for deadline := time.Now().Add(30 * time.Second); len(ids) == 0 && time.Now().Before(deadline); {
		time.Sleep(200 * time.Millisecond)
		ids = containersOf(t, img, marker)
	}
	if len(ids) != 1 {
		t.Fatalf("containers %v", ids)
	}
	out, err := exec.Command("docker", "container", "inspect", "--format", "{{.HostConfig.NetworkMode}}", ids[0]).Output()
	if err != nil || strings.TrimSpace(string(out)) == "none" {
		t.Errorf("network mode %q, err %v", out, err)
	}
	cancel()
	<-done
}

func TestRunUnavailable(t *testing.T) {
	echo := proctest.Image(t, "echo")
	secrets := t.TempDir()
	for _, c := range []struct {
		name string
		cfg  Config
		job  Job
		want string
	}{
		{"missing runtime", Config{Runtime: "/nonexistent/docker"}, Job{Image: echo}, "/nonexistent/docker"},
		{"no secrets dir", Config{}, Job{Image: echo, Secrets: []string{"api-token"}}, "no secrets directory"},
		{"missing secret", Config{SecretsDir: secrets}, Job{Image: echo, Secrets: []string{"api-token"}}, "secret api-token"},
		{"bad secret name", Config{SecretsDir: secrets}, Job{Image: echo, Secrets: []string{"../etc/passwd"}}, "must match"},
		{"missing blob", Config{}, Job{Image: echo, Blobs: map[string]string{strings.Repeat("0", 64): filepath.Join(secrets, "nope")}}, "attachment"},
		{"bad blob hash", Config{}, Job{Image: echo, Blobs: map[string]string{"x": "/etc/hosts"}}, "64 lowercase hex"},
		{"unknown local image", Config{}, Job{Image: "custos.test/does-not-exist:latest"}, "does-not-exist"},
		{"unpullable image", Config{}, Job{Image: "custos.invalid/none@sha256:" + strings.Repeat("0", 64)}, "pulling custos.invalid/none"},
		{"empty image", Config{}, Job{Image: ""}, "invalid image reference"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, err := New(c.cfg).Run(context.Background(), c.job)
			if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("result %+v, err %v; want ErrUnavailable naming %q", res, err, c.want)
			}
		})
	}
}

func TestRunOutputIsCapped(t *testing.T) {
	big := `{"pad":"` + strings.Repeat("x", contract.MaxOutputSize) + `"}`
	res, err := New(Config{}).Run(context.Background(), Job{Image: proctest.Image(t, "echo"), Input: []byte(big)})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || len(res.Stdout) != contract.MaxOutputSize+1 {
		t.Fatalf("exit %d, stdout %d bytes, want %d", res.ExitCode, len(res.Stdout), contract.MaxOutputSize+1)
	}
	if _, err := contract.ParseOutput(res.Stdout); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err %v", err)
	}
}

func TestCapped(t *testing.T) {
	c := &capped{limit: 5, note: "[cut]"}
	for _, p := range []string{"abc", "def", "ghi"} {
		if n, err := c.Write([]byte(p)); n != 3 || err != nil {
			t.Fatalf("Write %q: %d, %v", p, n, err)
		}
	}
	if got := string(c.bytes()); got != "abcde[cut]" {
		t.Errorf("got %q", got)
	}
	c = &capped{limit: 5, note: "[cut]"}
	c.Write([]byte("abcde"))
	if got := string(c.bytes()); got != "abcde" {
		t.Errorf("exactly at the limit: got %q", got)
	}
}

func TestResolve(t *testing.T) {
	r := New(Config{})
	ctx := context.Background()
	pinned := proctest.Image(t, "echo")
	id := pinned[strings.Index(pinned, "@")+1:]

	ref, digest, err := r.Resolve(ctx, pinned)
	if err != nil || ref != id || digest != id {
		t.Errorf("pinned local image: ref %q, digest %q, err %v; want %q twice", ref, digest, err, id)
	}
	ref, digest, err = r.Resolve(ctx, "custos.test/echo:latest")
	if err != nil || ref != "custos.test/echo:latest" || digest != id {
		t.Errorf("local tag: ref %q, digest %q, err %v", ref, digest, err)
	}
	remote := "registry.example.org/host-scanner@sha256:" + strings.Repeat("1", 64)
	ref, digest, err = r.Resolve(ctx, remote)
	if err != nil || ref != remote || digest != "sha256:"+strings.Repeat("1", 64) {
		t.Errorf("remote: ref %q, digest %q, err %v", ref, digest, err)
	}
	if _, _, err := r.Resolve(ctx, "custos.test/does-not-exist:latest"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("unknown local image: err %v", err)
	}
}

// containersOf lists the IDs of containers of image img (running or not)
// that mount the host file source.
func containersOf(t *testing.T, img, source string) []string {
	t.Helper()
	id := img[strings.Index(img, "@")+1:]
	out, err := exec.Command("docker", "ps", "--all", "--quiet", "--no-trunc", "--filter", "ancestor="+id).Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	var ids []string
	for _, c := range strings.Fields(string(out)) {
		m, err := exec.Command("docker", "container", "inspect", "--format", "{{json .Mounts}}", c).Output()
		if err != nil || !bytes.Contains(m, []byte(source)) {
			continue
		}
		ids = append(ids, c)
	}
	return ids
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/runner/`
Expected: FAIL, `undefined: New` (and `Config`, `Job`, `ErrUnavailable`, `capped`, `MaxLogSize`)

- [ ] **Step 3: Write `internal/runner/runner.go`**

```go
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
		return nil, err
	}
	if err := r.ensureImage(ctx, ref); err != nil {
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
```

- [ ] **Step 4: Write `internal/runner/resolve.go`**

```go
package runner

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var pinnedRE = regexp.MustCompile(`^(.+)@(sha256:[0-9a-f]{64})$`)

// Resolve returns the reference to run and the image digest
// "sha256:<hex>". For "<ref>@sha256:<hex>" it uses a local image whose ID is
// sha256:<hex> if one exists (locally built images have no registry
// digest), else the reference itself (pulled on first use), and the digest
// from the reference. For any other reference (processor test) it inspects
// the local image and returns its ID as digest.
func (r *Runner) Resolve(ctx context.Context, image string) (ref, digest string, err error) {
	if _, err := exec.LookPath(r.cfg.Runtime); err != nil {
		return "", "", fmt.Errorf("%w: container runtime %q: %v", ErrUnavailable, r.cfg.Runtime, err)
	}
	if strings.TrimSpace(image) == "" || strings.HasPrefix(image, "-") {
		return "", "", fmt.Errorf("%w: invalid image reference %q", ErrUnavailable, image)
	}
	if m := pinnedRE.FindStringSubmatch(image); m != nil {
		digest = m[2]
		if id, err := r.imageID(ctx, digest); err == nil && id == digest {
			return digest, digest, nil
		}
		return image, digest, nil
	}
	id, err := r.imageID(ctx, image)
	if err != nil {
		return "", "", fmt.Errorf("%w: image %s: %v", ErrUnavailable, image, err)
	}
	return image, id, nil
}

// imageID returns the ID of a local image as "sha256:<hex>".
func (r *Runner) imageID(ctx context.Context, ref string) (string, error) {
	out, err := r.output(ctx, "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out))
	if !strings.HasPrefix(id, "sha256:") {
		id = "sha256:" + id // podman prints the bare hex
	}
	if !pinnedRE.MatchString("x@" + id) {
		return "", fmt.Errorf("unexpected image id %q", id)
	}
	return id, nil
}

// ensureImage pulls ref unless it is present locally.
func (r *Runner) ensureImage(ctx context.Context, ref string) error {
	if _, err := r.imageID(ctx, ref); err == nil {
		return nil
	}
	if _, err := r.output(ctx, "pull", "--quiet", ref); err != nil {
		return fmt.Errorf("%w: pulling %s: %v", ErrUnavailable, ref, err)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go vet ./internal/runner/ && go test ./internal/runner/ -v && go test -race ./internal/runner/`
Expected: PASS (11 tests, 9 subtests), about 7–10 seconds. `TestRunUnavailable/unpullable_image` uses the reserved `.invalid` top-level domain, so the pull fails at once without network access. Afterwards `docker ps -a --filter name=custos-run- -q` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/runner
git commit -m "Run processor images in locked-down containers through the docker or podman client

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The generate test image

**Files:**
- Create: `internal/proctest/images/directives/directives.go`
- Create: `internal/proctest/images/generate/main.go`
- Test: `internal/proctest/images_test.go`

**Interfaces:**
- Consumes: `proctest.Image` (Task 2), `runner.New/Run/Config/Job/Result/MaxLogSize` (Task 3), `contract.NewInput/ParseOutput` (Task 1), `fixture.TaskA/TaskB/WorkspaceID`.
- Produces:
  - package `directives` (only the test images import it): `const StatementType`, `const PredicateType`, `func Statement(subject string) []byte`, `func Main(name string, wrap func(statement []byte) (json.RawMessage, error))`
  - the **generate** image with the directives of the architecture note: `task`, `choice`, `bump`, `processor`, `origin`, `doc`, `stderr`, `garbage`, `exit`. Answer text is `value` when not null, else `body`; blank lines are ignored; words are split on white space, so titles have single spaces. An unknown directive, a missing argument, a reference to a key no earlier `task`/`choice` line defined, or an `exit` argument outside 0–255 writes `<name>: line <n>: …` to stderr and exits 1 with no stdout. `bump`, `processor` and `origin` values are copied unchecked, so tests of later plans can provoke validation errors.
  - test helpers in `images_test.go` (package `proctest_test`): `run(t, cfg, image, text string, markdown bool) *runner.Result`, `parse(t, res) *contract.Output`

The `doc` content is the bare statement `{"_type":"https://in-toto.io/Statement/v1","predicate":{},"predicateType":"https://example.org/custos-test/v1","subject":[{"digest":{"sha256":"<hex>"},"name":"<subject>"}]}` (keys sorted, as `encoding/json` writes maps).

- [ ] **Step 1: Write the failing tests** `internal/proctest/images_test.go`

```go
package proctest_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/runner"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// run runs image on a text answer (or a markdown answer when markdown is
// true) to fixture.TaskB with the given answer text.
func run(t *testing.T, cfg runner.Config, image, text string, markdown bool) *runner.Result {
	t.Helper()
	v := &task.Version{Meta: task.Meta{ID: fixture.TaskB, Version: "1.0.0", Title: "Hosts", AnswerType: task.AnswerText}}
	a := &workspace.Answer{Task: fixture.TaskB, TaskVersion: "1.0.0", Type: task.AnswerText, Value: text}
	if markdown {
		v.AnswerType, a.Type, a.Value, a.Body = task.AnswerMarkdown, task.AnswerMarkdown, "", text
	}
	in, err := json.Marshal(contract.NewInput(fixture.WorkspaceID, v, a))
	if err != nil {
		t.Fatal(err)
	}
	res, err := runner.New(cfg).Run(context.Background(), runner.Job{Image: proctest.Image(t, image), Input: in})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// parse checks that the run succeeded and returns its parsed output.
func parse(t *testing.T, res *runner.Result) *contract.Output {
	t.Helper()
	if res.ExitCode != 0 || res.TimedOut {
		t.Fatalf("exit %d, timed out %v, log:\n%s", res.ExitCode, res.TimedOut, res.Log)
	}
	out, err := contract.ParseOutput(res.Stdout)
	if err != nil {
		t.Fatalf("%v\nstdout: %s", err, res.Stdout)
	}
	return out
}

func TestGenerateTasks(t *testing.T) {
	out := parse(t, run(t, runner.Config{}, "generate", strings.Join([]string{
		"task host:web-01 Host web-01",
		"",
		"choice host:web-02 Is   web-02 patched?",
		"bump host:web-01 major",
		"processor host:web-01 host-scanner",
		"origin host:web-02 " + fixture.TaskA,
	}, "\n"), false))
	if len(out.Tasks) != 2 || len(out.Documents) != 0 {
		t.Fatalf("output %+v", out)
	}
	t0, t1 := out.Tasks[0], out.Tasks[1]
	if t0.MatchKey != "host:web-01" || t0.Title != "Host web-01" || t0.Body != "Generated for "+fixture.TaskB ||
		t0.AnswerType != task.AnswerText || t0.Bump != semver.Major || t0.Processor != "host-scanner" || t0.Origin != nil || t0.Choices != nil {
		t.Errorf("task 0 %+v", t0)
	}
	if t1.MatchKey != "host:web-02" || t1.Title != "Is web-02 patched?" || t1.AnswerType != task.AnswerChoice ||
		strings.Join(t1.Choices, ",") != "yes,no" || t1.Origin == nil || t1.Origin.ID != fixture.TaskA || t1.Origin.Version != "" || t1.Bump != semver.Minor {
		t.Errorf("task 1 %+v", t1)
	}
}

func TestGenerateReadsMarkdownBody(t *testing.T) {
	out := parse(t, run(t, runner.Config{}, "generate", "task a From the body\n", true))
	if len(out.Tasks) != 1 || out.Tasks[0].Title != "From the body" {
		t.Errorf("output %+v", out)
	}
}

func TestGenerateDocument(t *testing.T) {
	out := parse(t, run(t, runner.Config{}, "generate", "doc slsa:web-01 web-01", false))
	if len(out.Documents) != 1 {
		t.Fatalf("output %+v", out)
	}
	d := out.Documents[0]
	if d.MatchKey != "slsa:web-01" || d.Name != "slsa:web-01" || d.MediaType != "application/vnd.in-toto+json" {
		t.Errorf("document %+v", d)
	}
	sum := sha256.Sum256([]byte("web-01"))
	want := `{"_type":"https://in-toto.io/Statement/v1","predicate":{},"predicateType":"https://example.org/custos-test/v1",` +
		`"subject":[{"digest":{"sha256":"` + hex.EncodeToString(sum[:]) + `"},"name":"web-01"}]}`
	if string(d.Content) != want {
		t.Errorf("content:\n%s\nwant:\n%s", d.Content, want)
	}
}

func TestGenerateStderrGarbageAndExit(t *testing.T) {
	res := run(t, runner.Config{}, "generate", "stderr scanning  two hosts\ntask a A", false)
	if out := parse(t, res); len(out.Tasks) != 1 || string(res.Log) != "scanning two hosts\n" {
		t.Errorf("output %+v, log %q", out, res.Log)
	}

	res = run(t, runner.Config{}, "generate", "task a A\ngarbage\ntask b B", false)
	if res.ExitCode != 0 || string(res.Stdout) != "not json\n" {
		t.Errorf("garbage: exit %d, stdout %q", res.ExitCode, res.Stdout)
	}
	if _, err := contract.ParseOutput(res.Stdout); err == nil {
		t.Error("garbage must not parse")
	}

	res = run(t, runner.Config{}, "generate", "stderr about to fail\ntask a A\nexit 7", false)
	if res.ExitCode != 7 || len(res.Stdout) != 0 || string(res.Log) != "about to fail\n" {
		t.Errorf("exit: code %d, stdout %q, log %q", res.ExitCode, res.Stdout, res.Log)
	}
}

func TestGenerateBadDirective(t *testing.T) {
	for _, text := range []string{"frobnicate x", "bump a minor", "task a", "exit x"} {
		res := run(t, runner.Config{}, "generate", text, false)
		if res.ExitCode != 1 || len(res.Stdout) != 0 || !strings.Contains(string(res.Log), "line 1") {
			t.Errorf("%q: exit %d, stdout %q, log %q", text, res.ExitCode, res.Stdout, res.Log)
		}
	}
}

func TestLongLogIsTruncated(t *testing.T) {
	res := run(t, runner.Config{}, "generate", "stderr "+strings.Repeat("x", 2*runner.MaxLogSize), false)
	parse(t, res)
	if len(res.Log) != runner.MaxLogSize+len("\n[log truncated]\n") || !strings.HasSuffix(string(res.Log), "x\n[log truncated]\n") {
		t.Errorf("log of %d bytes ending %q", len(res.Log), res.Log[max(0, len(res.Log)-40):])
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/proctest/`
Expected: FAIL, `test image generate: go build: … directory not found` (or similar) in every `TestGenerate…` test

- [ ] **Step 3: Write `internal/proctest/images/directives/directives.go`**

```go
// Package directives is the shared core of the generate and sign test
// processors: it reads the contract input, follows the directives in the
// answer text, and writes the output. It uses only the standard library and
// its own copies of the contract's JSON names, like a third-party processor.
package directives

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// StatementType and PredicateType are the in-toto fields of a doc.
const (
	StatementType = "https://in-toto.io/Statement/v1"
	PredicateType = "https://example.org/custos-test/v1"
)

type input struct {
	Task struct {
		ID string `json:"id"`
	} `json:"task"`
	Answer struct {
		Value *string `json:"value"`
		Body  string  `json:"body"`
	} `json:"answer"`
}

type origin struct {
	ID string `json:"id"`
}

type outTask struct {
	MatchKey   string   `json:"match_key"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	AnswerType string   `json:"answer_type"`
	Choices    []string `json:"choices,omitempty"`
	Origin     *origin  `json:"origin,omitempty"`
	Processor  string   `json:"processor,omitempty"`
	Bump       string   `json:"bump,omitempty"`
}

type outDocument struct {
	MatchKey  string          `json:"match_key"`
	Name      string          `json:"name"`
	MediaType string          `json:"media_type"`
	Content   json.RawMessage `json:"content"`
}

type output struct {
	Tasks     []*outTask    `json:"tasks"`
	Documents []outDocument `json:"documents"`
}

// Statement returns the bare in-toto v1 statement a "doc <key> <subject>"
// directive produces.
func Statement(subject string) []byte {
	sum := sha256.Sum256([]byte(subject))
	st := map[string]any{
		"_type":         StatementType,
		"subject":       []any{map[string]any{"name": subject, "digest": map[string]string{"sha256": hex.EncodeToString(sum[:])}}},
		"predicateType": PredicateType,
		"predicate":     map[string]any{},
	}
	data, err := json.Marshal(st)
	if err != nil {
		panic(err)
	}
	return data
}

// Main runs the processor. wrap turns the statement of each doc directive
// into the document content (identity for generate, a DSSE envelope for
// sign). It exits 1 on malformed input or directives.
func Main(name string, wrap func(statement []byte) (json.RawMessage, error)) {
	fail := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, name+": "+format+"\n", args...)
		os.Exit(1)
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail("reading stdin: %v", err)
	}
	var in input
	if err := json.Unmarshal(data, &in); err != nil {
		fail("input is not valid: %v", err)
	}
	text := in.Answer.Body
	if in.Answer.Value != nil {
		text = *in.Answer.Value
	}
	out := output{Tasks: []*outTask{}, Documents: []outDocument{}}
	tasks := map[string]*outTask{}
	for n, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		at := fmt.Sprintf("line %d: %s", n+1, f[0])
		need := func(min int) {
			if len(f) < min {
				fail("%s needs at least %d arguments", at, min-1)
			}
		}
		lookup := func() *outTask {
			t, ok := tasks[f[1]]
			if !ok {
				fail("%s: no task with key %q before this line", at, f[1])
			}
			return t
		}
		switch f[0] {
		case "task", "choice":
			need(3)
			t := &outTask{MatchKey: f[1], Title: strings.Join(f[2:], " "), Body: "Generated for " + in.Task.ID, AnswerType: "text"}
			if f[0] == "choice" {
				t.AnswerType, t.Choices = "choice", []string{"yes", "no"}
			}
			tasks[t.MatchKey] = t
			out.Tasks = append(out.Tasks, t)
		case "bump":
			need(3)
			lookup().Bump = f[2]
		case "processor":
			need(3)
			lookup().Processor = f[2]
		case "origin":
			need(3)
			lookup().Origin = &origin{ID: f[2]}
		case "doc":
			need(3)
			content, err := wrap(Statement(f[2]))
			if err != nil {
				fail("%s: %v", at, err)
			}
			out.Documents = append(out.Documents, outDocument{MatchKey: f[1], Name: f[1], MediaType: "application/vnd.in-toto+json", Content: content})
		case "stderr":
			fmt.Fprintln(os.Stderr, strings.Join(f[1:], " "))
		case "garbage":
			fmt.Println("not json")
			os.Exit(0)
		case "exit":
			need(2)
			code, err := strconv.Atoi(f[1])
			if err != nil || code < 0 || code > 255 {
				fail("%s: %q is not an exit code", at, f[1])
			}
			os.Exit(code)
		default:
			fail("%s: unknown directive", at)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fail("writing output: %v", err)
	}
}
```

- [ ] **Step 4: Write `internal/proctest/images/generate/main.go`**

```go
// Command generate is the test processor that turns the directives in the
// answer text into output tasks and bare in-toto statements (see package
// directives and the architecture note of phase 3).
package main

import (
	"encoding/json"

	"github.com/emeland-io/custos/internal/proctest/images/directives"
)

func main() {
	directives.Main("generate", func(statement []byte) (json.RawMessage, error) {
		return statement, nil
	})
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go vet ./internal/proctest/... && go test ./internal/proctest/... -v`
Expected: PASS (8 tests)

- [ ] **Step 6: Commit**

```bash
git add internal/proctest
git commit -m "Add the generate test image that turns answer directives into tasks and statements

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The sign test image and `SigningKey`

**Files:**
- Create: `internal/proctest/images/directives/dsse.go`
- Create: `internal/proctest/images/sign/main.go`
- Modify: `internal/proctest/proctest.go` (whole file below)
- Modify: `internal/proctest/images_test.go` (import block, three tests appended)

**Interfaces:**
- Consumes: `directives.Main`, `directives.Statement` (Task 4), `runner` (Task 3), the helpers `run` and `parse` of `images_test.go` (Task 4).
- Produces:
  - `func SigningKey(t testing.TB) (secretsDir string, publicKeyPEM []byte)` and `const SigningKeySecret = "test-signing-key"` in `proctest`
  - `directives.PayloadType = "application/vnd.in-toto+json"`, `func PAE(payloadType string, payload []byte) []byte`, `func Envelope(statement []byte, key ed25519.PrivateKey) (json.RawMessage, error)`
  - the **sign** image: like generate, but each `doc` content is `{"payload":"<base64 of the statement>","payloadType":"application/vnd.in-toto+json","signatures":[{"sig":"<base64 ed25519 signature over the DSSE v1 PAE>"}]}` (no `keyid`); without a readable PKCS#8 ed25519 key at `/run/secrets/test-signing-key` it writes the reason to stderr and exits 2 before reading directives. Plan 3b verifies these envelopes with the public key from `SigningKey`.

- [ ] **Step 1: Write the failing tests** — in `internal/proctest/images_test.go`, replace the import block

```go
package proctest_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/runner"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)
```

with

```go
package proctest_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/runner"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)
```

and append

```go
func TestSignWrapsDocumentsInSignedEnvelopes(t *testing.T) {
	secrets, pubPEM := proctest.SigningKey(t)
	block, _ := pem.Decode(pubPEM)
	if block == nil || block.Type != "PUBLIC KEY" {
		t.Fatalf("public key %s", pubPEM)
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	cfg := runner.Config{SecretsDir: secrets}
	in, err := json.Marshal(contract.NewInput(fixture.WorkspaceID,
		&task.Version{Meta: task.Meta{ID: fixture.TaskB, Version: "1.0.0", Title: "Hosts", AnswerType: task.AnswerText}},
		&workspace.Answer{Task: fixture.TaskB, TaskVersion: "1.0.0", Type: task.AnswerText, Value: "task a A\ndoc slsa:web-01 web-01"}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := runner.New(cfg).Run(context.Background(), runner.Job{
		Image: proctest.Image(t, "sign"), Input: in, Secrets: []string{proctest.SigningKeySecret},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := parse(t, res)
	if len(out.Tasks) != 1 || len(out.Documents) != 1 || out.Documents[0].MediaType != "application/vnd.in-toto+json" {
		t.Fatalf("output %+v", out)
	}
	var env struct {
		PayloadType string `json:"payloadType"`
		Payload     []byte `json:"payload"` // base64 in JSON
		Signatures  []struct {
			Sig []byte `json:"sig"`
		} `json:"signatures"`
	}
	if err := json.Unmarshal(out.Documents[0].Content, &env); err != nil {
		t.Fatal(err)
	}
	if env.PayloadType != "application/vnd.in-toto+json" || len(env.Signatures) != 1 {
		t.Fatalf("envelope %s", out.Documents[0].Content)
	}
	sum := sha256.Sum256([]byte("web-01"))
	if !strings.Contains(string(env.Payload), `"name":"web-01"`) || !strings.Contains(string(env.Payload), hex.EncodeToString(sum[:])) {
		t.Errorf("payload %s", env.Payload)
	}
	pae := fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(env.PayloadType), env.PayloadType, len(env.Payload), env.Payload)
	if !ed25519.Verify(pub.(ed25519.PublicKey), pae, env.Signatures[0].Sig) {
		t.Error("signature does not verify with the public key from SigningKey")
	}
}

func TestSignWithoutKeyExits2(t *testing.T) {
	res := run(t, runner.Config{}, "sign", "doc a b", false)
	if res.ExitCode != 2 || len(res.Stdout) != 0 || !strings.Contains(string(res.Log), "test-signing-key") {
		t.Errorf("exit %d, stdout %q, log %q", res.ExitCode, res.Stdout, res.Log)
	}
}

func TestSigningKeysDiffer(t *testing.T) {
	_, a := proctest.SigningKey(t)
	_, b := proctest.SigningKey(t)
	if string(a) == string(b) {
		t.Error("two calls returned the same key")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/proctest/`
Expected: FAIL, `undefined: proctest.SigningKey` and `undefined: proctest.SigningKeySecret`

- [ ] **Step 3: Replace `internal/proctest/proctest.go`** with

```go
// Package proctest builds the container images that tests of processor runs
// use (spec §8): echo, generate, sign, fail and loop. Each image is
// FROM scratch plus one static Linux binary built from
// internal/proctest/images/<name>, so building needs Docker but no network.
// Only test code imports this package.
package proctest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// SigningKeySecret is the secret name SigningKey writes and the sign image
// reads.
const SigningKeySecret = "test-signing-key"

// Names lists the test images Image can build.
var Names = []string{"echo", "generate", "sign", "fail", "loop"}

var idRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type built struct {
	ref string
	err error
}

var (
	mu     sync.Mutex
	images = map[string]*built{}
)

// Image builds the named test image once per process and returns
// "custos.test/<name>@sha256:<image-id-hex>". Names: echo, generate, sign,
// fail, loop. Fails the test when Docker is unavailable.
func Image(t testing.TB, name string) string {
	t.Helper()
	mu.Lock()
	b, ok := images[name]
	if !ok {
		b = &built{}
		b.ref, b.err = build(name)
		images[name] = b
	}
	mu.Unlock()
	if b.err != nil {
		t.Fatalf("test image %s: %v", name, b.err)
	}
	return b.ref
}

func build(name string) (string, error) {
	known := false
	for _, n := range Names {
		known = known || n == name
	}
	if !known {
		return "", fmt.Errorf("unknown test image, want one of %s", strings.Join(Names, ", "))
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate the sources of package proctest")
	}
	src := filepath.Join(filepath.Dir(file), "images", name)
	dir, err := os.MkdirTemp("", "custos-proctest-"+name+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	gobuild := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, "processor"), ".")
	gobuild.Dir = src
	gobuild.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := gobuild.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %v\n%s", err, out)
	}
	dockerfile := "FROM scratch\nCOPY processor /processor\nENTRYPOINT [\"/processor\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	docker := exec.Command("docker", "build", "--quiet", "--tag", "custos.test/"+name+":latest", dir)
	docker.Stdout, docker.Stderr = &stdout, &stderr
	if err := docker.Run(); err != nil {
		return "", fmt.Errorf("docker build (is Docker running?): %v\n%s", err, stderr.Bytes())
	}
	id := strings.TrimSpace(stdout.String())
	if !idRE.MatchString(id) {
		return "", fmt.Errorf("docker build printed %q, want an image id sha256:<64 hex digits>", id)
	}
	return "custos.test/" + name + "@" + id, nil
}

// SigningKey writes a fresh ed25519 private key (PKCS#8 PEM) as secret
// "test-signing-key" into a new temp directory and returns that secrets
// directory and the matching public key (PKIX PEM).
func SigningKey(t testing.TB) (secretsDir string, publicKeyPEM []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	secretsDir = t.TempDir()
	// 0644: the container drops all capabilities, so even its root user
	// can read only files the permissions allow.
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
	if err := os.WriteFile(filepath.Join(secretsDir, SigningKeySecret), keyPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	return secretsDir, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
}
```

- [ ] **Step 4: Write `internal/proctest/images/directives/dsse.go`**

```go
package directives

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// PayloadType is the DSSE payload type of the envelopes sign writes.
const PayloadType = "application/vnd.in-toto+json"

// PAE is the DSSE v1 pre-authentication encoding that is signed.
func PAE(payloadType string, payload []byte) []byte {
	return fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(payloadType), payloadType, len(payload), payload)
}

// Envelope wraps statement in a DSSE envelope with one signature by key.
func Envelope(statement []byte, key ed25519.PrivateKey) (json.RawMessage, error) {
	sig := ed25519.Sign(key, PAE(PayloadType, statement))
	return json.Marshal(map[string]any{
		"payloadType": PayloadType,
		"payload":     base64.StdEncoding.EncodeToString(statement),
		"signatures":  []any{map[string]string{"sig": base64.StdEncoding.EncodeToString(sig)}},
	})
}
```

- [ ] **Step 5: Write `internal/proctest/images/sign/main.go`**

```go
// Command sign is the test processor that works like generate, but wraps
// each doc in a DSSE envelope signed with the ed25519 key mounted as secret
// test-signing-key. Without that secret it exits with code 2.
package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/emeland-io/custos/internal/proctest/images/directives"
)

const keyPath = "/run/secrets/test-signing-key"

func main() {
	key, err := loadKey(keyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sign:", err)
		os.Exit(2)
	}
	directives.Main("sign", func(statement []byte) (json.RawMessage, error) {
		return directives.Envelope(statement, key)
	})
}

func loadKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s holds no PEM private key", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	ed, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s is not an ed25519 key", path)
	}
	return ed, nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go vet ./internal/proctest/... && go test ./internal/proctest/... -v`
Expected: PASS (11 tests)

- [ ] **Step 7: Run the whole suite**

Run: `gofmt -l . && make test`
Expected: no files listed by `gofmt`; `go vet` clean; all packages PASS (Docker must be running).

- [ ] **Step 8: Commit**

```bash
git add internal/proctest
git commit -m "Add the sign test image and a helper that provides its signing key as a secret

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-Review Notes

- **Spec coverage:**
  - §5.1 (image digest is the version) → `Resolve` returns `sha256:<hex>` from the pinned reference, or the local image ID (Task 3).
  - §5.2 (read-only root, timeout, memory limit, no network unless `network: true`, secrets at `/run/secrets/<name>`, attachments at `/input/blobs/<sha256>`) → `Run` (Task 3), checked on a live container by `TestRunSandbox` and `TestRunNetworkAllowed`.
  - §5.3 input → `NewInput` (Task 1); output, defaults (`bump` minor), `match_key` uniqueness per kind, stderr as log, non-zero exit / timeout / invalid output fail → `ParseOutput` (Task 1) and `Result` (Task 3); the failure decision itself is 3d's.
  - §7 (failed runs keep logs) → `Result.Log` is returned for every finished run, also on timeout.
  - §8 (contract tests with echo, generate, sign, fail, loop) → Tasks 2, 4, 5.
  - Ruling 5 of the note (local image by ID) → `Resolve` and `TestResolve`.
- **What was run:** every file of this plan was applied to a clone of `main` (8d0c5ef) and checked with `gofmt -l .` (clean), `go vet ./...` (clean), `go test -count=1 ./...` (all packages pass, including the existing ones), and `go test -race` on `internal/runner` and `internal/proctest`, on macOS arm64 with Docker server 27.4 (client 29.8, overlay2 storage). No containers were left behind. Not run: `podman`; Linux hosts (file permissions of bind-mounted secrets, see decisions); a real registry pull.
- **Placeholder scan:** none; every code step holds the whole file or quotes the replaced text.
- **Set review (2026-10-04):** plans 3a–3e were applied in order, as written, to one clone of `main`; `gofmt -l .`, `go vet ./...` and `go test ./...` passed after each plan, `make test` after 3e, and `go test -race` on runner, runs, store, proposal, merge and cmd/custos at the end (Docker 27.4, Go 1.27.1, Python 3.14).
- **Type consistency:** `capped`, `bind`, `reason`, `containerState`, `MaxLogSize`, `BlobPath`, `SigningKeySecret`, `run`, `parse`, `containersOf`, `writeFile` are each defined once and used with the same signatures; exported names match the architecture note.
- **Review Focus:** each of the five items has its test in the owning task (Tasks 1, 3, 4).
- **Not covered by a test:** the `State.Error` path (an image whose entrypoint cannot start) and the out-of-memory note; both were checked by hand with `docker run` while writing the plan (exit 127 with `State.Error` set, container in state `created`).

## Decisions beyond the architecture note

- `Run` uses `docker run` without `--rm` and then `docker container inspect` for the exit code and `State.Error`, then `docker rm --force` — the client's own exit code mixes daemon errors (125–127) with processor exit codes — two extra client calls per run (about 50 ms).
- A missing image is pulled with `docker pull` before `docker run --pull never`; the job's timeout covers only the run, not the pull — a large first pull must not use up a 60s processor timeout — a pull that hangs is limited only by ctx.
- On timeout `ExitCode` is `-1` — the container was killed, so its exit code means nothing — clients that print the exit code show -1.
- A cancelled ctx kills and removes the container and returns an error wrapping `ctx.Err()`, not `ErrUnavailable` — shutdown is neither a processor failure nor an unavailable runtime; 3d can requeue such runs — 3d must check `errors.Is(err, context.Canceled)` to tell it apart.
- After `docker kill`, `Run` waits 10 s for the client, then kills the client process — a stuck client must not block a worker forever — a container created in that window may outlive the client briefly until `rm --force`.
- Extra sandbox flags not named in §5.2: `--memory-swap` equal to the memory limit (no swap), `--pids-limit 256`, tmpfs `/tmp:rw,noexec,nosuid,size=64m`, `--quiet` — a fork bomb or swapping would hurt the shared host — a processor that needs more processes or more `/tmp` cannot get it without a code change.
- The container runs as the image's user (no `--user`) — scratch images have no passwd file, and secrets must be readable — with `--cap-drop ALL` even root in the container cannot bypass file permissions, so secret files must be readable by the container's user (blobs are 0444 already); `SigningKey` writes its key 0644 for this reason. Operators with 0600 secrets on Linux will see the processor fail to read them.
- Secret names must match `[a-z0-9][a-z0-9-]*` and blob keys 64 lowercase hex digits, and mount sources must be regular files without `,`, `"` or newline; otherwise `ErrUnavailable` — prevents path traversal and `--mount` injection — unusual data-dir paths (with a comma) cannot be used.
- When the container was OOM-killed, `Run` appends `[killed: out of memory, limit <m>]` to the log — the engineer otherwise sees only exit code 137 — none known.
- `Resolve` checks that the runtime binary exists (`exec.LookPath`) and treats an empty reference or one starting with `-` as `ErrUnavailable` — a reference must never become a client flag — none known.
- `Resolve` returns `sha256:<hex>` (the image ID) as `ref` for a local image found by ID; podman's bare-hex IDs are normalised — Docker accepts the prefixed ID as a reference — untested on podman.
- New exported names: `contract.BlobPath`, `runner.MaxLogSize`, `proctest.Names`, `proctest.SigningKeySecret`, package `internal/proctest/images/directives` (`Main`, `Statement`, `Envelope`, `PAE`, `StatementType`, `PredicateType`, `PayloadType`) — shared by runner, tests and the images — a few more names to keep stable.
- `ParseOutput` treats missing or `null` `tasks`/`documents` as empty and returns non-nil empty slices; it rejects empty stdout, top-level non-objects (also `null`) and anything after the object; a document `content` may be any JSON value except `null` — friendly to hand-written processors, strict where data could be lost — a processor that prints a log line after its JSON fails.
- `ParseOutput` does not limit `match_key` characters or title length, and keeps `content` bytes as written — matching (3c) and canonicalisation (3b) own those — odd keys reach Git frontmatter unchanged.
- The test images import nothing from custos except `directives`; generate/sign use their own JSON structs — they behave like third-party processors, so a contract change in custos does not silently change them — the images must be updated by hand when the contract grows.
- The sign image's DSSE signature has no `keyid` — DSSE makes it optional; the verifier tries every trusted key (3c's `TestPlanVerifiesSignedDocumentFromRealProcessor` and 3d's `TestSignedDocumentIsVerified` verify these envelopes with 3b's verifier) — none known.
- `proctest.Image` tags each build `custos.test/<name>:latest` and leaves earlier builds as dangling images — tags make the images findable — repeated test runs accumulate dangling images until `docker image prune`.
- Rulings of this plan for spec §11.3 are not written by this plan — five plans editing the same table would conflict; the note's ruling list and these decisions are the source — the controller of phase 3 records them.
