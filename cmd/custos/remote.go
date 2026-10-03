package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/emeland-io/custos/internal/remote"
)

const cloneUsage = "usage: custos clone URL DIR\n"

// runClone clones a repository and downloads the attachments its answers
// reference.
func runClone(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("clone", flag.ContinueOnError)
	fl.SetOutput(stderr)
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	if fl.NArg() != 2 {
		fmt.Fprint(stderr, cloneUsage)
		return 2
	}
	if err := remote.Clone(fl.Arg(0), fl.Arg(1), stderr); err != nil {
		fmt.Fprintf(stderr, "custos clone: %v\n", err)
		return 1
	}
	return 0
}
