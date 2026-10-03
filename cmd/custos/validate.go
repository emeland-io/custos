package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/workspace"
)

// runValidate checks a catalog or workspace checkout: the staged content
// when DIR is the root of a git work tree, the files on disk otherwise.
// Content holding custos.yaml is a workspace; anything else is a catalog.
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
	fsys, err := filesToValidate(dir)
	if err != nil {
		fmt.Fprintf(stderr, "custos validate: %v\n", err)
		return 1
	}
	var ps []problem.Problem
	if _, err := fs.Stat(fsys, "custos.yaml"); err == nil {
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

// filesToValidate returns what the next commit would hold when dir is the
// root of a git work tree: the index, so that untracked and ignored files
// (.DS_Store, editor swap files) and unstaged edits are not checked, which
// also makes validate usable as a pre-commit hook. Any other directory is
// checked as it is on disk.
func filesToValidate(dir string) (fs.FS, error) {
	if isWorkTreeRoot(dir) {
		return (&gitrepo.Repo{Dir: dir}).IndexFS()
	}
	return os.DirFS(dir), nil
}

func isWorkTreeRoot(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return false
	}
	top, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	if err != nil {
		return false
	}
	abs, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	abs, err = filepath.Abs(abs)
	return err == nil && abs == top
}

// helpOrUsage maps a flag parsing error to an exit code: -h is not an error.
func helpOrUsage(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}
