package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/emeland-io/custos/internal/auth"
	"github.com/emeland-io/custos/internal/config"
)

const tokenUsage = "usage: custos token create --data-dir DIR --email E --name \"Name\" [--admin] [--task-author] [--processor-author] [--engineer WORKSPACE-UUID,...] [--label TEXT]\n"

// runToken creates or updates a user directly against <data-dir>/auth and
// prints a fresh personal access token for them to stdout — the only
// thing this command writes to stdout, so scripts can capture it as
// $(custos token create ...) (ruling 4.12). Everything else goes to
// stderr.
func runToken(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "create" {
		fmt.Fprint(stderr, tokenUsage)
		return 2
	}
	fl := flag.NewFlagSet("token create", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dataDir := fl.String("data-dir", os.Getenv("CUSTOS_DATA_DIR"), "directory holding auth/ (env CUSTOS_DATA_DIR)")
	email := fl.String("email", "", "the user's email address")
	name := fl.String("name", "", "the user's display name, used as the Git commit author name")
	label := fl.String("label", "cli", "label for the new token, shown in GET /auth/tokens")
	admin := fl.Bool("admin", false, "grant the admin role")
	taskAuthor := fl.Bool("task-author", false, "grant the task-author role")
	processorAuthor := fl.Bool("processor-author", false, "grant the processor-author role")
	engineer := fl.String("engineer", "", `comma-separated workspace UUIDs, or "*" for every workspace, granting the engineer role`)
	if err := fl.Parse(args[1:]); err != nil {
		return helpOrUsage(err)
	}
	return doTokenCreate(tokenArgs{dataDir: *dataDir, email: *email, name: *name, label: *label, engineer: *engineer,
		admin: *admin, taskAuthor: *taskAuthor, processorAuthor: *processorAuthor}, fl.NArg(), stdout, stderr)
}

// tokenArgs are the parsed flags of token create.
type tokenArgs struct {
	dataDir, email, name, label, engineer string
	admin, taskAuthor, processorAuthor    bool
}

// doTokenCreate creates or updates the user and prints a new token to stdout.
func doTokenCreate(a tokenArgs, nArgs int, stdout, stderr io.Writer) int {
	if nArgs != 0 || a.dataDir == "" || a.email == "" {
		fmt.Fprint(stderr, tokenUsage)
		return 2
	}
	var engineerIDs []string
	for _, id := range strings.Split(a.engineer, ",") {
		if id = strings.TrimSpace(id); id != "" {
			engineerIDs = append(engineerIDs, id)
		}
	}
	m, err := auth.NewManager(auth.Config{DataDir: a.dataDir})
	if err != nil {
		fmt.Fprintf(stderr, "custos token create: %v\n", err)
		return 1
	}
	u, err := m.UpdateUser(a.email, func(u *auth.User) {
		if a.name != "" {
			u.Name = a.name
		}
		// Flags only ever add a role: running this command again to add
		// --task-author must not silently drop an --engineer grant an
		// earlier run made.
		u.Roles.Admin = u.Roles.Admin || a.admin
		u.Roles.TaskAuthor = u.Roles.TaskAuthor || a.taskAuthor
		u.Roles.ProcessorAuthor = u.Roles.ProcessorAuthor || a.processorAuthor
		u.Roles.Engineer = append(u.Roles.Engineer, engineerIDs...)
	})
	if err != nil {
		fmt.Fprintf(stderr, "custos token create: %v\n", err)
		return 1
	}
	id, token, err := m.CreateToken(a.email, a.label)
	if err != nil {
		fmt.Fprintf(stderr, "custos token create: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "custos token create: token %s for %s, roles %+v\n", id, a.email, u.Roles)
	fmt.Fprintln(stdout, token)
	return 0
}

func (c *cli) tokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "manage personal access tokens",
		Args:  cobra.ArbitraryArgs,
		RunE:  func(*cobra.Command, []string) error { return usageError(tokenUsage) },
	}
	var a tokenArgs
	create := &cobra.Command{
		Use:   "create --data-dir DIR --email E [flags]",
		Short: "create or update a user and print a fresh personal access token",
		Args:  cobra.ArbitraryArgs, // doTokenCreate checks the count
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := config.New(cmd.Flags(), []string{"data-dir"}, nil)
			if err != nil {
				fmt.Fprintf(c.stderr, "custos token create: %v\n", err)
				return exitCode(1)
			}
			a.dataDir = s.String("data-dir")
			return result(doTokenCreate(a, len(args), c.stdout, c.stderr))
		},
	}
	f := create.Flags()
	f.String("data-dir", "", "directory holding auth/ (env CUSTOS_DATA_DIR)")
	f.StringVar(&a.email, "email", "", "the user's email address")
	f.StringVar(&a.name, "name", "", "the user's display name, used as the Git commit author name")
	f.StringVar(&a.label, "label", "cli", "label for the new token, shown in GET /auth/tokens")
	f.BoolVar(&a.admin, "admin", false, "grant the admin role")
	f.BoolVar(&a.taskAuthor, "task-author", false, "grant the task-author role")
	f.BoolVar(&a.processorAuthor, "processor-author", false, "grant the processor-author role")
	f.StringVar(&a.engineer, "engineer", "", `comma-separated workspace UUIDs, or "*" for every workspace, granting the engineer role`)
	cmd.AddCommand(create)
	return cmd
}
