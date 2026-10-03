// Package hook implements the pre-receive hook that keeps the main branch
// of every custos repository valid.
package hook

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/workspace"
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

// PreReceive checks the ref updates git passes to a pre-receive hook, one
// "<old> <new> <ref>" line each. Only main is checked; draft branches may
// hold anything and may be rewritten.
func PreReceive(repo *gitrepo.Repo, kind Kind, updates io.Reader) ([]problem.Problem, error) {
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
		mps, err := checkMain(repo, kind, f[0], f[1])
		if err != nil {
			return nil, err
		}
		ps = append(ps, mps...)
	}
	return ps, sc.Err()
}

func checkMain(repo *gitrepo.Repo, kind Kind, oldOID, newOID string) ([]problem.Problem, error) {
	var ps problem.List
	if gitrepo.IsZero(newOID) {
		ps.Add("", problem.RuleHistory, "main cannot be deleted")
		return ps, nil
	}
	if !gitrepo.IsZero(oldOID) {
		ok, err := repo.IsAncestor(oldOID, newOID)
		if err != nil {
			return nil, err
		}
		if !ok {
			ps.Add("", problem.RuleHistory, "main cannot be rewritten; add commits on top of it instead of force-pushing")
			return ps, nil
		}
	}
	newFS, err := repo.TreeFS(newOID)
	if err != nil {
		return nil, err
	}
	if kind == Workspace {
		return workspace.Check(newFS), nil
	}
	all := catalog.Check(newFS)
	if !gitrepo.IsZero(oldOID) {
		oldFS, err := repo.TreeFS(oldOID)
		if err != nil {
			return nil, err
		}
		all = append(all, catalog.CheckImmutable(oldFS, newFS)...)
	}
	problem.Sort(all)
	return all, nil
}

// Script returns a pre-receive hook that runs the custos binary at exe.
func Script(exe string, kind Kind) (string, error) {
	if strings.ContainsAny(exe, "'\n") {
		return "", fmt.Errorf("cannot use %q in a hook script", exe)
	}
	return fmt.Sprintf("#!/bin/sh\nexec '%s' hook pre-receive --kind %s\n", exe, kind), nil
}
