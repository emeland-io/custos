package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/emeland-io/custos/internal/server"
)

const workspaceUsage = "usage: custos workspace create [--data-dir DIR] WORKSPACE-UUID\n"

func runWorkspace(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "create" {
		fmt.Fprint(stderr, workspaceUsage)
		return 2
	}
	fl := flag.NewFlagSet("workspace create", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dataDir := fl.String("data-dir", os.Getenv("CUSTOS_DATA_DIR"), "directory holding the repositories (env CUSTOS_DATA_DIR)")
	if err := fl.Parse(args[1:]); err != nil {
		return helpOrUsage(err)
	}
	if fl.NArg() != 1 || *dataDir == "" {
		fmt.Fprint(stderr, workspaceUsage)
		return 2
	}
	// Only the new repository gets a hook; the hooks of existing ones stay
	// as custos serve installed them.
	exe, err := os.Executable()
	if err == nil {
		err = server.New(*dataDir, exe).CreateWorkspace(fl.Arg(0))
	}
	if err != nil {
		fmt.Fprintf(stderr, "custos workspace create: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "created workspace %s; push its main branch to /git/workspaces/%s.git\n", fl.Arg(0), fl.Arg(0))
	return 0
}
