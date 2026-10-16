package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// exitCode is returned by a command's RunE when it has already printed
// its own message and only the process's exit status is left to report.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// usageError is a wrong invocation: runCobra prints it to stderr and exits 2.
type usageError string

func (e usageError) Error() string { return string(e) }

// result turns the int a command core returns into RunE's error.
func result(code int) error {
	if code == 0 {
		return nil
	}
	return exitCode(code)
}

// exactArgs is cobra.ExactArgs with the command's usage line as the message.
func exactArgs(n int, usage string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != n {
			return usageError(usage)
		}
		return nil
	}
}

// cli carries what the commands share: the process's streams and the
// global --config flag.
type cli struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	configPath     string
}

// newRootCmd builds the whole command tree. A fresh tree per run keeps flag
// values from one run out of the next (tests call run many times).
func newRootCmd(c *cli) *cobra.Command {
	root := &cobra.Command{
		Use:           "custos",
		Short:         "manage a task catalog and the workspaces that answer its tasks",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageError(fmt.Sprintf("custos: unknown command %q\n\n%s", args[0], cmd.UsageString()))
			}
			return usageError(cmd.UsageString())
		},
	}
	root.SetIn(c.stdin)
	root.SetOut(c.stdout)
	root.SetErr(c.stderr)
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageError(fmt.Sprintf("custos: %v\n\n%s", err, cmd.UsageString()))
	})
	root.PersistentFlags().StringVar(&c.configPath, "config", "", "personal config file (default ~/.config/custos/config.yaml)")
	root.PersistentFlags().String("server-url", "", "URL of the custos server, for the commands that talk to one (env CUSTOS_SERVER_URL; today only \"config get\" reads it)")
	// Commands are added here, one line each, in the task that ports them.
	root.AddCommand(c.configCmd(), c.validateCmd(), c.taskCmd())
	return root
}

// runCobra executes args on a fresh command tree and returns the exit code:
// 0 on success, 1 when a command failed, 2 when it was called incorrectly.
func runCobra(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c := &cli{stdin: stdin, stdout: stdout, stderr: stderr}
	root := newRootCmd(c)
	root.SetArgs(args)
	err := root.Execute()
	var code exitCode
	var usage usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &code):
		return int(code)
	case errors.As(err, &usage):
		fmt.Fprint(stderr, strings.TrimRight(string(usage), "\n")+"\n")
	default:
		// Only cobra itself returns other errors: an unknown flag or
		// subcommand it caught before any RunE ran.
		fmt.Fprintf(stderr, "custos: %v\n", err)
	}
	return 2
}
