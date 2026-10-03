package workspace

import (
	"net/url"
	"strings"
	"time"

	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

// Answer is one answers/<task-uuid>.md file. Markdown answers keep their
// text in Body; all other types use Value.
type Answer struct {
	Task        string          `yaml:"task"`
	TaskVersion string          `yaml:"task_version"`
	Type        task.AnswerType `yaml:"type"`
	Value       string          `yaml:"value,omitempty"`
	Attachments []Attachment    `yaml:"attachments,omitempty"`
	Body        string          `yaml:"-"`
	Path        string          `yaml:"-"`
}

// Attachment references a file in the workspace's blob store.
type Attachment struct {
	Name      string `yaml:"name"`
	SHA256    string `yaml:"sha256"`
	MediaType string `yaml:"media_type"`
}

func (w *Workspace) loadAnswer(path string, data []byte) []problem.Problem {
	var a Answer
	body, err := frontmatter.Decode(data, &a)
	if err != nil {
		return []problem.Problem{{Path: path, Rule: problem.RuleFormat, Message: err.Error()}}
	}
	a.Body, a.Path = body, path
	w.Answers[path] = &a
	return checkAnswer(&a)
}

func checkAnswer(a *Answer) []problem.Problem {
	var ps problem.List
	if !task.ValidID(a.Task) {
		ps.Add(a.Path, problem.RuleFormat, "task %q is not a lowercase UUID v4", a.Task)
	} else if want := "answers/" + a.Task + ".md"; a.Path != want {
		ps.Add(a.Path, problem.RulePath, "file must be stored as %s", want)
	}
	if !semver.Valid(a.TaskVersion) {
		ps.Add(a.Path, problem.RuleFormat, "task_version %q is not a semantic version such as 1.2.0", a.TaskVersion)
	}
	switch a.Type {
	case task.AnswerMarkdown:
		if a.Value != "" {
			ps.Add(a.Path, problem.RuleFormat, "value must be empty for markdown answers; the answer goes in the body")
		}
	case task.AnswerTimestamp:
		if _, err := time.Parse(time.RFC3339, a.Value); err != nil {
			ps.Add(a.Path, problem.RuleFormat, "value %q is not an RFC 3339 timestamp such as 2026-10-02T14:00:00Z", a.Value)
		}
	case task.AnswerURL:
		if u, err := url.Parse(a.Value); err != nil || u.Scheme == "" || u.Host == "" {
			ps.Add(a.Path, problem.RuleFormat, "value %q is not an absolute URL", a.Value)
		}
	case task.AnswerText, task.AnswerPath, task.AnswerChoice:
		if strings.TrimSpace(a.Value) == "" {
			ps.Add(a.Path, problem.RuleFormat, "value is missing")
		}
	default:
		ps.Add(a.Path, problem.RuleFormat, "type %q is not a known answer type", a.Type)
	}
	for _, at := range a.Attachments {
		if strings.TrimSpace(at.Name) == "" {
			ps.Add(a.Path, problem.RuleFormat, "attachment without name")
		}
		if !sha256RE.MatchString(at.SHA256) {
			ps.Add(a.Path, problem.RuleFormat, "attachment %q: sha256 %q is not 64 lowercase hex digits", at.Name, at.SHA256)
		}
		if strings.TrimSpace(at.MediaType) == "" {
			ps.Add(a.Path, problem.RuleFormat, "attachment %q: media_type is missing", at.Name)
		}
	}
	return ps
}
