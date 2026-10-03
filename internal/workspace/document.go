package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/task"
)

var verificationStates = []string{"verified", "failed", "unsigned"}

// Document is one documents/<uuid>.json file: a processor output such as an
// in-toto attestation, with its metadata.
type Document struct {
	MatchKey     string          `json:"match_key"`
	Name         string          `json:"name"`
	MediaType    string          `json:"media_type"`
	ProducedBy   ProducedBy      `json:"produced_by"`
	Verification *Verification   `json:"verification,omitempty"`
	Content      json.RawMessage `json:"content"`
	Path         string          `json:"-"`
}

// Verification is the result of checking a document's signatures.
type Verification struct {
	Status  string   `json:"status"`
	Signers []string `json:"signers,omitempty"`
}

func (w *Workspace) loadDocument(path string, data []byte) []problem.Problem {
	var ps problem.List
	id := strings.TrimSuffix(strings.TrimPrefix(path, "documents/"), ".json")
	if !task.ValidID(id) {
		ps.Add(path, problem.RulePath, "file name must be a lowercase UUID v4, as in documents/<uuid>.json")
	}
	var d Document
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		ps.Add(path, problem.RuleFormat, "%v", err)
		return ps
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		ps.Add(path, problem.RuleFormat, "unexpected data after the JSON object")
		return ps
	}
	d.Path = path
	w.Documents[path] = &d
	if strings.TrimSpace(d.MatchKey) == "" {
		ps.Add(path, problem.RuleFormat, "match_key is missing")
	}
	if strings.TrimSpace(d.Name) == "" {
		ps.Add(path, problem.RuleFormat, "name is missing")
	}
	if strings.TrimSpace(d.MediaType) == "" {
		ps.Add(path, problem.RuleFormat, "media_type is missing")
	}
	if len(d.Content) == 0 || string(d.Content) == "null" {
		ps.Add(path, problem.RuleFormat, "content is missing")
	}
	if d.Verification != nil && !slices.Contains(verificationStates, d.Verification.Status) {
		ps.Add(path, problem.RuleFormat, "verification.status %q is not one of %s", d.Verification.Status, strings.Join(verificationStates, ", "))
	}
	return append(ps, checkProducedBy(path, d.ProducedBy)...)
}
