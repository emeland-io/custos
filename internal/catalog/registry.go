package catalog

import (
	"io/fs"

	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/repofile"
)

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
