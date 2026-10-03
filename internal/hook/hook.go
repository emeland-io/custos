// Package hook implements the pre-receive hook that keeps the main branch
// of every custos repository valid. The rules themselves live in package
// rules, which the store applies to its own writes too.
package hook

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/rules"
	"github.com/emeland-io/custos/internal/task"
)

// Kind is the kind of repository a hook guards.
type Kind string

const (
	Catalog   Kind = "catalog"
	Workspace Kind = "workspace"
)

const mainRef = "refs/heads/main"

// ParseKind parses the --kind flag of the hook command.
func ParseKind(s string) (Kind, error) {
	switch k := Kind(s); k {
	case Catalog, Workspace:
		return k, nil
	}
	return "", fmt.Errorf("unknown repository kind %q, want catalog or workspace", s)
}

// ScriptOptions describe the repository a hook guards.
type ScriptOptions struct {
	Kind        Kind
	CatalogDir  string // absolute path of catalog.git; workspace hooks only
	WorkspaceID string // workspace hooks only
}

func (o ScriptOptions) check() error {
	switch o.Kind {
	case Catalog:
		return nil
	case Workspace:
		if !filepath.IsAbs(o.CatalogDir) || strings.ContainsAny(o.CatalogDir, "'\n") {
			return fmt.Errorf("workspace hook: catalog directory %q must be an absolute path without quotes or line breaks", o.CatalogDir)
		}
		if !task.ValidID(o.WorkspaceID) {
			return fmt.Errorf("workspace hook: workspace id %q is not a lowercase UUID v4", o.WorkspaceID)
		}
		return nil
	}
	_, err := ParseKind(string(o.Kind))
	return err
}

// PreReceive checks the ref updates git passes to a pre-receive hook, one
// "<old> <new> <ref>" line each. Only main is checked; draft branches may
// hold anything and may be rewritten.
func PreReceive(repo *gitrepo.Repo, opts ScriptOptions, updates io.Reader) ([]problem.Problem, error) {
	if err := opts.check(); err != nil {
		return nil, err
	}
	var ps []problem.Problem
	sc := bufio.NewScanner(updates)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 {
			return nil, fmt.Errorf("unexpected hook input %q", sc.Text())
		}
		if f[2] != mainRef {
			continue
		}
		var mps []problem.Problem
		var err error
		if opts.Kind == Catalog {
			mps, err = rules.CheckCatalogUpdate(repo, f[0], f[1])
		} else {
			mps, err = rules.CheckWorkspaceUpdate(repo, &gitrepo.Repo{Dir: opts.CatalogDir}, opts.WorkspaceID, f[0], f[1])
		}
		if err != nil {
			return nil, err
		}
		ps = append(ps, mps...)
	}
	return ps, sc.Err()
}

// Script returns a pre-receive hook that runs the custos binary at exe.
func Script(exe string, opts ScriptOptions) (string, error) {
	if strings.ContainsAny(exe, "'\n") {
		return "", fmt.Errorf("cannot use %q in a hook script", exe)
	}
	if err := opts.check(); err != nil {
		return "", err
	}
	cmd := fmt.Sprintf("exec '%s' hook pre-receive --kind %s", exe, opts.Kind)
	if opts.Kind == Workspace {
		cmd += fmt.Sprintf(" --catalog '%s' --workspace %s", opts.CatalogDir, opts.WorkspaceID)
	}
	return "#!/bin/sh\n" + cmd + "\n", nil
}
