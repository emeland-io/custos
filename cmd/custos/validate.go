package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/workspace"
)

// runValidate checks a catalog or workspace checkout. A directory holding
// custos.yaml is a workspace; anything else is a catalog.
func runValidate(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("validate", flag.ContinueOnError)
	fl.SetOutput(stderr)
	against := fl.String("against", "", "also check that the task versions in this git revision are unchanged (catalogs only; DIR must be the repository root)")
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	if fl.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: custos validate [--against REV] [DIR]")
		return 2
	}
	dir := "."
	if fl.NArg() == 1 {
		dir = fl.Arg(0)
	}
	fsys := os.DirFS(dir)
	var ps []problem.Problem
	if _, err := os.Stat(filepath.Join(dir, "custos.yaml")); err == nil {
		if *against != "" {
			fmt.Fprintln(stderr, "custos validate: --against only applies to catalogs")
			return 2
		}
		ps = workspace.Check(fsys)
	} else {
		ps = catalog.Check(fsys)
		if *against != "" {
			old, err := (&gitrepo.Repo{Dir: dir}).TreeFS(*against)
			if err != nil {
				fmt.Fprintf(stderr, "custos validate: %v\n", err)
				return 1
			}
			ps = append(ps, catalog.CheckImmutable(old, fsys)...)
			problem.Sort(ps)
		}
	}
	for _, p := range ps {
		fmt.Fprintln(stdout, p)
	}
	if len(ps) > 0 {
		fmt.Fprintf(stderr, "%d problem(s) found\n", len(ps))
		return 1
	}
	fmt.Fprintln(stdout, "ok")
	return 0
}

// helpOrUsage maps a flag parsing error to an exit code: -h is not an error.
func helpOrUsage(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}
