package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/emeland-io/custos/internal/gitrepo"
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

// runPush uploads the attachments of the commits to push, then runs git
// push. Arguments after REMOTE (refspecs, flags such as --force) go to git.
func runPush(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("push", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dir := fl.String("dir", ".", "git checkout to push")
	author := fl.String("author", os.Getenv("CUSTOS_AUTHOR"),
		`uploader of attachments, "Name <email>" (env CUSTOS_AUTHOR; default: git config user.name and user.email)`)
	if err := fl.Parse(args); err != nil {
		return helpOrUsage(err)
	}
	remoteName, gitArgs := "origin", []string(nil)
	if fl.NArg() > 0 {
		remoteName, gitArgs = fl.Arg(0), fl.Args()[1:]
	}
	var sig gitrepo.Signature
	var err error
	if *author != "" {
		if sig, err = gitrepo.ParseSignature(*author); err != nil {
			fmt.Fprintf(stderr, "custos push: --author: %v\n", err)
			return 2
		}
	} else if sig, err = remote.GitAuthor(*dir); err != nil {
		fmt.Fprintf(stderr, "custos push: %v\n", err)
		return 1
	}
	if err := remote.Push(*dir, remoteName, gitArgs, sig, stderr); err != nil {
		fmt.Fprintf(stderr, "custos push: %v\n", err)
		return 1
	}
	return 0
}
