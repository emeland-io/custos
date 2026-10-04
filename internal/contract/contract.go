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
