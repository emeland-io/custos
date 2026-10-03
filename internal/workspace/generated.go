package workspace

import (
	"strings"

	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

// GeneratedMeta is the frontmatter of a task version made by a processor.
type GeneratedMeta struct {
	task.Meta  `yaml:",inline"`
	Origin     *Origin    `yaml:"origin,omitempty"`
	MatchKey   string     `yaml:"match_key"`
	Processor  string     `yaml:"processor,omitempty"`
	ProducedBy ProducedBy `yaml:"produced_by"`
}

// Origin optionally points to the task a generated task belongs to.
type Origin struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version,omitempty"`
}

// Generated is one generated/<uuid>/<semver>.md file.
type Generated struct {
	GeneratedMeta
	Body string
	Path string
}

func (w *Workspace) loadGenerated(path string, data []byte) []problem.Problem {
	var m GeneratedMeta
	body, err := frontmatter.Decode(data, &m)
	if err != nil {
		return []problem.Problem{{Path: path, Rule: problem.RuleFormat, Message: err.Error()}}
	}
	ps := problem.List(task.CheckMeta(m.Meta, path, "generated"))
	if strings.TrimSpace(m.MatchKey) == "" {
		ps.Add(path, problem.RuleFormat, "match_key is missing")
	}
	if m.Origin != nil {
		if !task.ValidID(m.Origin.ID) {
			ps.Add(path, problem.RuleFormat, "origin.id %q is not a lowercase UUID v4", m.Origin.ID)
		}
		if m.Origin.Version != "" && !semver.Valid(m.Origin.Version) {
			ps.Add(path, problem.RuleFormat, "origin.version %q is not a semantic version", m.Origin.Version)
		}
	}
	ps = append(ps, checkProducedBy(path, m.ProducedBy)...)
	if task.ValidID(m.ID) && semver.Valid(m.Version) {
		w.Generated[path] = &Generated{GeneratedMeta: m, Body: body, Path: path}
	}
	return ps
}
