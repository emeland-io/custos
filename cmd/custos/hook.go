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
		fmt.Fprintln(stderr, "usage: custos hook pre-receive --kind catalog|workspace [--catalog DIR --workspace UUID]")
		return 2
	}
	fl := flag.NewFlagSet("hook pre-receive", flag.ContinueOnError)
	fl.SetOutput(stderr)
	kindFlag := fl.String("kind", "", "catalog or workspace")
	catalogDir := fl.String("catalog", "", "absolute path of catalog.git (workspace hooks)")
	workspaceID := fl.String("workspace", "", "id of the workspace the repository belongs to (workspace hooks)")
	if err := fl.Parse(args[1:]); err != nil {
		return helpOrUsage(err)
	}
	kind, err := hook.ParseKind(*kindFlag)
	if err != nil {
		fmt.Fprintf(stderr, "custos hook: %v\n", err)
		return 2
	}
	// The repository the hook runs in sees the quarantined objects of the
	// push through git's environment; the catalog must not.
	opts := hook.ScriptOptions{Kind: kind, CatalogDir: *catalogDir, WorkspaceID: *workspaceID}
	ps, err := hook.PreReceive(&gitrepo.Repo{Dir: ".", InheritGitEnv: true}, opts, stdin)
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
