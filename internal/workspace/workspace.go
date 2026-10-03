// Package workspace loads a workspace tree (answers, generated tasks,
// documents) and checks the rules every commit on its main branch must
// follow.
package workspace

import (
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/repofile"
	"github.com/emeland-io/custos/internal/task"
)

// ConfigPath is the path of the workspace configuration file.
const ConfigPath = "custos.yaml"

var (
	answerLayout    = regexp.MustCompile(`^answers/[^/]+\.md$`)
	generatedLayout = regexp.MustCompile(`^generated/[^/]+/[^/]+\.md$`)
	documentLayout  = regexp.MustCompile(`^documents/[^/]+\.json$`)
	commitRE        = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
	sha256RE        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	digestRE        = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Config is custos.yaml.
type Config struct {
	Workspace string     `yaml:"workspace"`
	Catalog   CatalogPin `yaml:"catalog"`
	Frozen    bool       `yaml:"frozen,omitempty"`
}

// CatalogPin names the catalog commit the workspace uses.
type CatalogPin struct {
	URL    string `yaml:"url"`
	Commit string `yaml:"commit"`
}

// ProducedBy records which processor run created a generated task or
// document.
type ProducedBy struct {
	Task         task.Ref `yaml:"task" json:"task"`
	Processor    string   `yaml:"processor" json:"processor"`
	Digest       string   `yaml:"digest" json:"digest"`
	AnswerCommit string   `yaml:"answer_commit" json:"answer_commit"`
}

// Workspace is the content of one workspace tree. Maps are keyed by file
// path.
type Workspace struct {
	Config    Config
	Answers   map[string]*Answer
	Generated map[string]*Generated
	Graph     *task.Graph // history graph of the generated tasks
	Documents map[string]*Document
}

// Load reads a workspace tree. Problems found while reading are returned;
// the workspace holds everything that could be read.
func Load(fsys fs.FS) (*Workspace, []problem.Problem) {
	w := &Workspace{Answers: map[string]*Answer{}, Generated: map[string]*Generated{}, Documents: map[string]*Document{}}
	ps := w.loadConfig(fsys)
	ps = append(ps, repofile.Walk(fsys, "answers", answerLayout, "answers/<task-uuid>.md", w.loadAnswer)...)
	ps = append(ps, repofile.Walk(fsys, "generated", generatedLayout, "generated/<uuid>/<semver>.md", w.loadGenerated)...)
	ps = append(ps, repofile.Walk(fsys, "documents", documentLayout, "documents/<uuid>.json", w.loadDocument)...)
	var vs []*task.Version
	for _, path := range slices.Sorted(maps.Keys(w.Generated)) {
		g := w.Generated[path]
		vs = append(vs, &task.Version{Meta: g.Meta, Body: g.Body, Path: g.Path})
	}
	graph, gps := task.NewGraph(vs)
	w.Graph = graph
	return w, append(ps, gps...)
}

// Check loads and validates the workspace in fsys and returns all problems,
// sorted.
func Check(fsys fs.FS) []problem.Problem {
	w, ps := Load(fsys)
	ps = append(ps, w.Graph.Check()...)
	problem.Sort(ps)
	return ps
}

func (w *Workspace) loadConfig(fsys fs.FS) []problem.Problem {
	found, rps := repofile.ReadYAML(fsys, ConfigPath, &w.Config)
	ps := problem.List(rps)
	if !found {
		ps.Add(ConfigPath, problem.RuleFormat, "custos.yaml is missing; a workspace needs it to name its catalog")
	}
	if !found || len(rps) > 0 {
		return ps
	}
	if !task.ValidID(w.Config.Workspace) {
		ps.Add(ConfigPath, problem.RuleFormat, "workspace %q is not a lowercase UUID v4", w.Config.Workspace)
	}
	if strings.TrimSpace(w.Config.Catalog.URL) == "" {
		ps.Add(ConfigPath, problem.RuleFormat, "catalog.url is missing")
	}
	if !commitRE.MatchString(w.Config.Catalog.Commit) {
		ps.Add(ConfigPath, problem.RuleFormat, "catalog.commit %q is not a full commit hash", w.Config.Catalog.Commit)
	}
	return ps
}

func checkProducedBy(path string, pb ProducedBy) []problem.Problem {
	var ps problem.List
	if !task.ValidID(pb.Task.ID) {
		ps.Add(path, problem.RuleFormat, "produced_by.task.id %q is not a lowercase UUID v4", pb.Task.ID)
	}
	if strings.TrimSpace(pb.Processor) == "" {
		ps.Add(path, problem.RuleFormat, "produced_by.processor is missing")
	}
	if !digestRE.MatchString(pb.Digest) {
		ps.Add(path, problem.RuleFormat, "produced_by.digest %q is not an image digest such as sha256:<64 hex digits>", pb.Digest)
	}
	if !commitRE.MatchString(pb.AnswerCommit) {
		ps.Add(path, problem.RuleFormat, "produced_by.answer_commit %q is not a full commit hash", pb.AnswerCommit)
	}
	return ps
}
