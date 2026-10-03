package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/hook"
)

// runHook is called by the pre-receive hook custos installs in its
// repositories. git runs it inside the repository with the ref updates on
// stdin, and shows its stderr to the person pushing.
func runHook(args []string, stdin io.Reader, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "pre-receive" {
		fmt.Fprintln(stderr, "usage: custos hook pre-receive --kind catalog|workspace")
		return 2
	}
	fl := flag.NewFlagSet("hook pre-receive", flag.ContinueOnError)
	fl.SetOutput(stderr)
	kindFlag := fl.String("kind", "", "catalog or workspace")
	if err := fl.Parse(args[1:]); err != nil {
		return helpOrUsage(err)
	}
	kind, err := hook.ParseKind(*kindFlag)
	if err != nil {
		fmt.Fprintf(stderr, "custos hook: %v\n", err)
		return 2
	}
	ps, err := hook.PreReceive(&gitrepo.Repo{Dir: ".", InheritGitEnv: true}, kind, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "custos hook: %v\n", err)
		return 1
	}
	if len(ps) > 0 {
		fmt.Fprintln(stderr, "custos rejected the push:")
		for _, p := range ps {
			fmt.Fprintln(stderr, "  "+p.String())
		}
		return 1
	}
	return 0
}
