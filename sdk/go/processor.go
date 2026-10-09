// Package processor helps to write custos processors in Go.
//
// A processor is a container image. custos starts it with the contract
// input (custos.processor/v1) on stdin and reads the output from stdout;
// whatever the processor writes to stderr is kept as the run's log. With
// this package, a processor is one function:
//
//	func main() { processor.Main(process) }
//
//	func process(task processor.Task, answer processor.Answer) (processor.Output, error) {
//		…
//	}
//
// Main reads and checks the input, calls the function, checks the output
// with the rules custos applies, and writes it. The package uses the
// standard library only.
package processor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// ContractVersion is the contract this package speaks.
const ContractVersion = "custos.processor/v1"

// MaxOutputSize is the largest output custos accepts, in bytes.
const MaxOutputSize = 16 << 20

// AnswerType is the kind of answer a task asks for.
type AnswerType string

const (
	AnswerMarkdown  AnswerType = "markdown"
	AnswerText      AnswerType = "text"
	AnswerTimestamp AnswerType = "timestamp"
	AnswerPath      AnswerType = "path"
	AnswerURL       AnswerType = "url"
	AnswerChoice    AnswerType = "choice"
)

var answerTypes = []AnswerType{AnswerMarkdown, AnswerText, AnswerTimestamp, AnswerPath, AnswerURL, AnswerChoice}

// Bump is the part of a generated task's version that increases when the
// task changes.
type Bump string

const (
	Patch Bump = "patch"
	Minor Bump = "minor" // custos's default when Bump is empty
	Major Bump = "major"
)

// Input is what custos writes to the processor's stdin.
type Input struct {
	Contract  string    `json:"contract"`
	Workspace Workspace `json:"workspace"`
	Task      Task      `json:"task"`
	Answer    Answer    `json:"answer"`
}

// Workspace names the workspace the answer belongs to.
type Workspace struct {
	ID string `json:"id"`
}

// Task is the version of the task that was answered.
type Task struct {
	ID         string     `json:"id"`
	Version    string     `json:"version"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	AnswerType AnswerType `json:"answer_type"`
	Choices    []string   `json:"choices,omitempty"`
}

// Answer is the answer the processor works on.
type Answer struct {
	TaskVersion string       `json:"task_version"`
	Value       *string      `json:"value"` // nil for markdown answers
	Body        string       `json:"body"`
	Attachments []Attachment `json:"attachments"`
}

// Text returns the answer's value, or its body for markdown answers.
func (a Answer) Text() string {
	if a.Value != nil {
		return *a.Value
	}
	return a.Body
}

// Attachment is a file attached to the answer. It is mounted read-only at
// Path inside the container.
type Attachment struct {
	Name      string `json:"name"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type"`
	Path      string `json:"path"`
}

// Output is what the processor produces: follow-up tasks and documents.
type Output struct {
	Tasks     []OutputTask     `json:"tasks"`
	Documents []OutputDocument `json:"documents"`
}

// OutputTask is a generated task. MatchKey identifies it across runs of
// the processor on the same answer.
type OutputTask struct {
	MatchKey   string     `json:"match_key"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	AnswerType AnswerType `json:"answer_type"`
	Choices    []string   `json:"choices,omitempty"` // required for AnswerChoice, forbidden otherwise
	Origin     *Origin    `json:"origin,omitempty"`
	Processor  string     `json:"processor,omitempty"`
	Bump       Bump       `json:"bump,omitempty"`
}

// Origin places a generated task below another task in the book.
type Origin struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
}

// OutputDocument is a document such as an in-toto statement, a DSSE
// envelope or a Sigstore bundle. Content is any value that encodes to JSON
// other than null: a struct, a map, or a json.RawMessage.
type OutputDocument struct {
	MatchKey  string `json:"match_key"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Content   any    `json:"content"`
}

// Func turns one answer into an output. An error fails the run; custos then
// keeps the earlier output and shows the error in the run's log.
type Func func(task Task, answer Answer) (Output, error)

// Main runs f as a processor: it reads the input from stdin, writes the
// output to stdout and exits. On any error it writes the error to stderr
// and exits with status 1.
func Main(f Func) {
	if err := Run(f, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

// Run is Main without the process around it: it reads the input from in,
// calls f, checks the output and writes it to out. Nothing is written when
// it returns an error. Tests of a processor call it with a sample input.
func Run(f Func, in io.Reader, out io.Writer) error {
	input, err := ReadInput(in)
	if err != nil {
		return err
	}
	output, err := f(input.Task, input.Answer)
	if err != nil {
		return err
	}
	data, err := Encode(output)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}

// ReadInput decodes the contract input and checks its contract version.
// Fields this version does not know are ignored.
func ReadInput(r io.Reader) (Input, error) {
	var in Input
	dec := json.NewDecoder(r)
	if err := dec.Decode(&in); err != nil {
		return Input{}, fmt.Errorf("reading the input: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Input{}, errors.New("reading the input: unexpected data after the JSON object")
	}
	if in.Contract != ContractVersion {
		return Input{}, fmt.Errorf("input has contract %q; this SDK speaks %s", in.Contract, ContractVersion)
	}
	return in, nil
}

// Encode checks o and returns it as JSON, followed by a newline. Empty task
// and document lists are written as [].
func Encode(o Output) ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if o.Tasks == nil {
		o.Tasks = []OutputTask{}
	}
	if o.Documents == nil {
		o.Documents = []OutputDocument{}
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(o); err != nil {
		return nil, fmt.Errorf("encoding the output: %w", err)
	}
	if b.Len() > MaxOutputSize {
		return nil, fmt.Errorf("output is %d bytes; custos accepts at most %d", b.Len(), MaxOutputSize)
	}
	return b.Bytes(), nil
}

// ValidationError lists everything wrong with an output.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "invalid output:\n  " + strings.Join(e.Problems, "\n  ")
}

var (
	uuidV4RE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	semverRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)` +
		`(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$`)
	nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// Validate applies the rules custos checks on every output, except whether
// a named processor is registered (only the catalog knows that). It
// returns a *ValidationError listing every problem, or nil.
func (o Output) Validate() error {
	var ps []string
	add := func(format string, args ...any) { ps = append(ps, fmt.Sprintf(format, args...)) }
	keys := map[string]bool{}
	for i, t := range o.Tasks {
		at := fmt.Sprintf("tasks[%d]", i)
		switch {
		case strings.TrimSpace(t.MatchKey) == "":
			add("%s: match_key is missing", at)
		case keys[t.MatchKey]:
			add("%s: match_key %q is used by an earlier task", at, t.MatchKey)
		}
		keys[t.MatchKey] = true
		if strings.TrimSpace(t.Title) == "" {
			add("%s: title is missing", at)
		}
		if !validAnswerType(t.AnswerType) {
			add("%s: answer_type %q is not one of markdown, text, timestamp, path, url, choice", at, t.AnswerType)
		}
		if t.AnswerType == AnswerChoice {
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
			if !uuidV4RE.MatchString(t.Origin.ID) {
				add("%s: origin.id %q is not a lowercase UUID v4", at, t.Origin.ID)
			}
			if t.Origin.Version != "" && !semverRE.MatchString(t.Origin.Version) {
				add("%s: origin.version %q is not a semantic version such as 1.2.0", at, t.Origin.Version)
			}
		}
		if t.Processor != "" && !nameRE.MatchString(t.Processor) {
			add("%s: processor %q must match [a-z0-9][a-z0-9-]*", at, t.Processor)
		}
		switch t.Bump {
		case "", Patch, Minor, Major:
		default:
			add("%s: bump %q is not one of patch, minor, major", at, t.Bump)
		}
	}
	keys = map[string]bool{}
	for i, d := range o.Documents {
		at := fmt.Sprintf("documents[%d]", i)
		switch {
		case strings.TrimSpace(d.MatchKey) == "":
			add("%s: match_key is missing", at)
		case keys[d.MatchKey]:
			add("%s: match_key %q is used by an earlier document", at, d.MatchKey)
		}
		keys[d.MatchKey] = true
		if strings.TrimSpace(d.Name) == "" {
			add("%s: name is missing", at)
		}
		if strings.TrimSpace(d.MediaType) == "" {
			add("%s: media_type is missing", at)
		}
		content, err := json.Marshal(d.Content)
		switch {
		case err != nil:
			add("%s: content cannot be encoded as JSON: %v", at, err)
		case string(content) == "null":
			add("%s: content is missing", at)
		}
	}
	if len(ps) > 0 {
		return &ValidationError{Problems: ps}
	}
	return nil
}

func validAnswerType(t AnswerType) bool {
	for _, a := range answerTypes {
		if t == a {
			return true
		}
	}
	return false
}
