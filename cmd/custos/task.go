package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/spf13/cobra"
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
	return doTaskNewVersion(*dir, *patch, *minor, *major, fl.Args(), stdout, stderr)
}

// doTaskNewVersion writes the next version file of the task args[0].
func doTaskNewVersion(dir string, patch, minor, major bool, args []string, stdout, stderr io.Writer) int {
	var steps []semver.Step
	for step, set := range map[semver.Step]bool{semver.Patch: patch, semver.Minor: minor, semver.Major: major} {
		if set {
			steps = append(steps, step)
		}
	}
	if len(steps) != 1 || len(args) != 1 {
		fmt.Fprint(stderr, taskUsage)
		return 2
	}
	path, err := catalog.NewVersion(dir, args[0], steps[0])
	if err != nil {
		fmt.Fprintf(stderr, "custos task new-version: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}

func (c *cli) taskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "work on the tasks of a catalog checkout",
		Args:  cobra.ArbitraryArgs,
		RunE:  func(*cobra.Command, []string) error { return usageError(taskUsage) },
	}
	var dir string
	var patch, minor, major bool
	nv := &cobra.Command{
		Use:   "new-version (--patch | --minor | --major) [--dir DIR] TASK-UUID",
		Short: "write the next version file of a task",
		Args:  cobra.ArbitraryArgs, // doTaskNewVersion checks the count
		RunE: func(_ *cobra.Command, args []string) error {
			return result(doTaskNewVersion(dir, patch, minor, major, args, c.stdout, c.stderr))
		},
	}
	nv.Flags().StringVar(&dir, "dir", ".", "catalog checkout")
	nv.Flags().BoolVar(&patch, "patch", false, "increase the patch version: wording or typo fixes")
	nv.Flags().BoolVar(&minor, "minor", false, "increase the minor version: clarifications")
	nv.Flags().BoolVar(&major, "major", false, "increase the major version: the task asks for something else")
	cmd.AddCommand(nv)
	return cmd
}
