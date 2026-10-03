package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
)

const workspaceUsage = "usage: custos workspace create [--data-dir DIR] [--public-url URL] --author \"Name <email>\" WORKSPACE-UUID\n"

func runWorkspace(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "create" {
		fmt.Fprint(stderr, workspaceUsage)
		return 2
	}
	fl := flag.NewFlagSet("workspace create", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dataDir := fl.String("data-dir", os.Getenv("CUSTOS_DATA_DIR"), "directory holding the repositories (env CUSTOS_DATA_DIR)")
	publicURL := publicURLFlag(fl)
	authorFlag := fl.String("author", os.Getenv("CUSTOS_AUTHOR"), "author of the initial commit, as \"Name <email>\" (env CUSTOS_AUTHOR)")
	if err := fl.Parse(args[1:]); err != nil {
		return helpOrUsage(err)
	}
	if fl.NArg() != 1 || *dataDir == "" || *authorFlag == "" {
		fmt.Fprint(stderr, workspaceUsage)
		return 2
	}
	author, err := gitrepo.ParseSignature(*authorFlag)
	if err != nil {
		fmt.Fprintf(stderr, "custos workspace create: --author: %v\n", err)
		return 2
	}
	if err := checkPublicURL(*publicURL); err != nil {
		fmt.Fprintf(stderr, "custos workspace create: %v\n", err)
		return 2
	}
	// Only the new repository gets a hook; the hooks of existing ones stay
	// as custos serve installed them.
	exe, err := os.Executable()
	if err == nil {
		err = store.New(*dataDir, exe, *publicURL).CreateWorkspace(fl.Arg(0), author)
	}
	if err != nil {
		fmt.Fprintf(stderr, "custos workspace create: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "created workspace %s; clone it from %s/git/workspaces/%s.git\n", fl.Arg(0), strings.TrimRight(*publicURL, "/"), fl.Arg(0))
	return 0
}
