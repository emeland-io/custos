package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/emeland-io/custos/internal/config"
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
	return doWorkspaceCreate(*dataDir, *publicURL, *authorFlag, fl.Args(), stdout, stderr)
}

// doWorkspaceCreate creates the workspace args[0] in dataDir.
func doWorkspaceCreate(dataDir, publicURL, authorFlag string, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || dataDir == "" || authorFlag == "" {
		fmt.Fprint(stderr, workspaceUsage)
		return 2
	}
	author, err := gitrepo.ParseSignature(authorFlag)
	if err != nil {
		fmt.Fprintf(stderr, "custos workspace create: --author: %v\n", err)
		return 2
	}
	if err := checkPublicURL(publicURL); err != nil {
		fmt.Fprintf(stderr, "custos workspace create: %v\n", err)
		return 2
	}
	// Only the new repository gets a hook; the hooks of existing ones stay
	// as custos serve installed them.
	exe, err := os.Executable()
	if err == nil {
		err = store.New(dataDir, exe, publicURL).CreateWorkspace(args[0], author)
	}
	if err != nil {
		fmt.Fprintf(stderr, "custos workspace create: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "created workspace %s; clone it from %s/git/workspaces/%s.git\n", args[0], strings.TrimRight(publicURL, "/"), args[0])
	return 0
}

func (c *cli) workspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "work with the workspaces of a custos data directory",
		Args:  cobra.ArbitraryArgs,
		RunE:  func(*cobra.Command, []string) error { return usageError(workspaceUsage) },
	}
	create := &cobra.Command{
		Use:   "create [flags] WORKSPACE-UUID",
		Short: "create a workspace pinned to the catalog",
		Args:  cobra.ArbitraryArgs, // doWorkspaceCreate checks the count
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := config.New(cmd.Flags(), []string{"data-dir", "public-url", "author"}, nil)
			if err != nil {
				fmt.Fprintf(c.stderr, "custos workspace create: %v\n", err)
				return exitCode(1)
			}
			return result(doWorkspaceCreate(s.String("data-dir"), s.String("public-url"), s.String("author"), args, c.stdout, c.stderr))
		},
	}
	f := create.Flags()
	f.String("data-dir", "", "directory holding the repositories (env CUSTOS_DATA_DIR)")
	f.String("public-url", defaultPublicURL, "base URL clients reach the server at, written into new workspaces (env CUSTOS_PUBLIC_URL)")
	f.String("author", "", "author of the initial commit, as \"Name <email>\" (env CUSTOS_AUTHOR)")
	cmd.AddCommand(create)
	return cmd
}
