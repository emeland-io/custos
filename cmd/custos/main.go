// Command custos manages a central task catalog and the workspaces that
// answer its tasks.
package main

import (
	"fmt"
	"io"
	"os"
)

const usage = `custos manages a task catalog and the workspaces that answer its tasks.

Usage:
  custos serve [--data-dir DIR] [--addr ADDR] [--public-url URL] [--container-runtime CMD]
               [--secrets-dir DIR] [--processor-memory SIZE] [--processor-workers N]
               [--max-generation-depth N] [--trusted-keys DIR]
               [--oidc-issuer-url URL] [--oidc-client-id ID] [--oidc-client-secret SECRET]
               [--oidc-redirect-url URL] [--admin-emails EMAILS] [--session-secret HEX]
  custos validate [--against REV] [DIR]
  custos task new-version (--patch | --minor | --major) [--dir DIR] TASK-UUID
  custos workspace create [--data-dir DIR] [--public-url URL] --author "Name <email>" WORKSPACE-UUID
  custos clone URL DIR
  custos push [--dir DIR] [REMOTE [GIT-PUSH-ARGS...]]
  custos processor test IMAGE --answer FILE [--task FILE] [--previous DIR] [flags]
  custos token create --data-dir DIR --email E --name "Name" [--admin] [--task-author]
                       [--processor-author] [--engineer WORKSPACE-UUID,...] [--label TEXT]

Run "custos COMMAND -h" for the flags of a command.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// legacyCLI sends every command to the dispatcher that predates cobra
// (ruling 5.8). Only a _test.go file sets it, so that the tests can prove
// both implementations agree. Delete it with runLegacy (ruling 5.10).
var legacyCLI bool

// portedCommands are the commands cobra serves. A command joins it in the
// task that ports it.
var portedCommands = map[string]bool{"config": true, "validate": true, "task": true, "workspace": true, "token": true, "clone": true, "push": true, "hook": true}

// run executes one command and returns the process exit code: 0 on success,
// 1 when the command failed, 2 when it was called incorrectly.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if !legacyCLI && len(args) > 0 && portedCommands[args[0]] {
		return runCobra(args, stdin, stdout, stderr)
	}
	return runLegacy(args, stdin, stdout, stderr)
}

// runLegacy is the dispatcher that used the standard flag package.
func runLegacy(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "task":
		return runTask(args[1:], stdout, stderr)
	case "workspace":
		return runWorkspace(args[1:], stdout, stderr)
	case "hook":
		return runHook(args[1:], stdin, stderr)
	case "clone":
		return runClone(args[1:], stdout, stderr)
	case "push":
		return runPush(args[1:], stdout, stderr)
	case "token":
		return runToken(args[1:], stdout, stderr)
	case "processor":
		return runProcessor(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "custos: unknown command %q\n\n%s", args[0], usage)
	return 2
}
