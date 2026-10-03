package catalog

import (
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"time"

	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/repofile"
)

var digestRE = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)

const registryPath = "processors.yaml"

// Registry is processors.yaml: named processors and the catalog tasks they
// are bound to.
type Registry struct {
	Processors map[string]Processor `yaml:"processors"`
	Bindings   map[string]string    `yaml:"bindings"` // task UUID → processor name
}

// Processor is one registered processor image.
type Processor struct {
	Image   string   `yaml:"image"`
	Timeout string   `yaml:"timeout,omitempty"`
	Network bool     `yaml:"network,omitempty"`
	Secrets []string `yaml:"secrets,omitempty"`
}

func (c *Catalog) loadRegistry(fsys fs.FS) []problem.Problem {
	_, ps := repofile.ReadYAML(fsys, registryPath, &c.Registry)
	return ps
}

// checkRegistry applies rule 6 and checks the processor entries.
func (c *Catalog) checkRegistry() []problem.Problem {
	var ps problem.List
	for _, name := range slices.Sorted(maps.Keys(c.Registry.Processors)) {
		p := c.Registry.Processors[name]
		if !slugRE.MatchString(name) {
			ps.Add(registryPath, problem.RuleFormat, "processor name %q must match [a-z0-9][a-z0-9-]*", name)
		}
		if !digestRE.MatchString(p.Image) {
			ps.Add(registryPath, problem.RuleFormat, "processor %s: image must be pinned by digest, as in registry.example.org/name@sha256:<64 hex digits>", name)
		}
		if p.Timeout != "" {
			if d, err := time.ParseDuration(p.Timeout); err != nil || d <= 0 {
				ps.Add(registryPath, problem.RuleFormat, "processor %s: timeout %q is not a positive duration such as 60s", name, p.Timeout)
			}
		}
		for _, s := range p.Secrets {
			if !slugRE.MatchString(s) {
				ps.Add(registryPath, problem.RuleFormat, "processor %s: secret name %q must match [a-z0-9][a-z0-9-]*", name, s)
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(c.Registry.Bindings)) {
		name := c.Registry.Bindings[id]
		switch {
		case !c.Tasks.Has(id):
			ps.Add(registryPath, problem.RuleBindings, "binding for unknown task %s", id)
		case c.Tasks.Superseded(id):
			ps.Add(registryPath, problem.RuleBindings, "binding for task %s, which was merged into another task", id)
		}
		if _, ok := c.Registry.Processors[name]; !ok {
			ps.Add(registryPath, problem.RuleBindings, "binding for task %s names unknown processor %q", id, name)
		}
	}
	return ps
}
