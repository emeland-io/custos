package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/emeland-io/custos/internal/remote"
)

const cloneUsage = "usage: custos clone URL DIR\n"

// remotePush is remote.Push; tests replace it.
var remotePush = remote.Push

// runClone clones a repository and downloads the attachments its answers
// reference.
func runClone(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("clone", flag.ContinueOnError)
	fl.SetOutput(stderr)
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	return doClone(fl.Args(), stderr)
}

// doClone clones args[0] into args[1].
func doClone(args []string, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprint(stderr, cloneUsage)
		return 2
	}
	if err := remote.Clone(args[0], args[1], stderr); err != nil {
		fmt.Fprintf(stderr, "custos clone: %v\n", err)
		return 1
	}
	return 0
}

// runPush uploads the attachments of the commits to push, then runs git
// push. Arguments after REMOTE (refspecs, flags such as --force) go to
// git. Authentication is the remote URL's own (userinfo, or a credential
// helper) plus $CUSTOS_TOKEN for custos's own attachment upload — there is
// no --author: a commit's author was always "git commit" on this machine,
// never this command.
func runPush(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("push", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dir := fl.String("dir", ".", "git checkout to push")
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	return doPush(*dir, fl.Args(), stderr)
}

// doPush pushes dir to args[0] (default origin); the arguments after it go to git.
func doPush(dir string, args []string, stderr io.Writer) int {
	remoteName, gitArgs := "origin", []string(nil)
	if len(args) > 0 {
		remoteName, gitArgs = args[0], args[1:]
	}
	if err := remotePush(dir, remoteName, gitArgs, stderr); err != nil {
		fmt.Fprintf(stderr, "custos push: %v\n", err)
		return 1
	}
	return 0
}

func (c *cli) cloneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clone URL DIR",
		Short: "git clone, then download the attachments the answers reference",
		Args:  exactArgs(2, cloneUsage),
		RunE:  func(_ *cobra.Command, args []string) error { return result(doClone(args, c.stderr)) },
	}
}

func (c *cli) pushCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "push [--dir DIR] [REMOTE [GIT-PUSH-ARGS...]]",
		Short: "upload the attachments of the commits to push, then git push",
		Args:  cobra.ArbitraryArgs,
		RunE:  func(_ *cobra.Command, args []string) error { return result(doPush(dir, args, c.stderr)) },
	}
	// Stop at the first argument, as the standard flag package did: what
	// follows REMOTE, flags included, belongs to git.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().StringVar(&dir, "dir", ".", "git checkout to push")
	return cmd
}
