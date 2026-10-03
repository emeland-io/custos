package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/semver"
)

const taskUsage = "usage: custos task new-version (--patch | --minor | --major) [--dir DIR] TASK-UUID\n"

func runTask(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "new-version" {
		fmt.Fprint(stderr, taskUsage)
		return 2
	}
	fl := flag.NewFlagSet("task new-version", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dir := fl.String("dir", ".", "catalog checkout")
	patch := fl.Bool("patch", false, "increase the patch version: wording or typo fixes")
	minor := fl.Bool("minor", false, "increase the minor version: clarifications")
	major := fl.Bool("major", false, "increase the major version: the task asks for something else")
	if err := fl.Parse(args[1:]); err != nil {
		return helpOrUsage(err)
	}
	var steps []semver.Step
	for step, set := range map[semver.Step]bool{semver.Patch: *patch, semver.Minor: *minor, semver.Major: *major} {
		if set {
			steps = append(steps, step)
		}
	}
	if len(steps) != 1 || fl.NArg() != 1 {
		fmt.Fprint(stderr, taskUsage)
		return 2
	}
	path, err := catalog.NewVersion(*dir, fl.Arg(0), steps[0])
	if err != nil {
		fmt.Fprintf(stderr, "custos task new-version: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}
