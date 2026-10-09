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
