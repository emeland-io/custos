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
