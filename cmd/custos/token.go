package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/emeland-io/custos/internal/auth"
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
	if fl.NArg() != 0 || *dataDir == "" || *email == "" {
		fmt.Fprint(stderr, tokenUsage)
		return 2
	}
	var engineerIDs []string
	for _, id := range strings.Split(*engineer, ",") {
		if id = strings.TrimSpace(id); id != "" {
			engineerIDs = append(engineerIDs, id)
		}
	}
	m, err := auth.NewManager(auth.Config{DataDir: *dataDir})
	if err != nil {
		fmt.Fprintf(stderr, "custos token create: %v\n", err)
		return 1
	}
	u, err := m.UpdateUser(*email, func(u *auth.User) {
		if *name != "" {
			u.Name = *name
		}
		// Flags only ever add a role: running this command again to add
		// --task-author must not silently drop an --engineer grant an
		// earlier run made.
		u.Roles.Admin = u.Roles.Admin || *admin
		u.Roles.TaskAuthor = u.Roles.TaskAuthor || *taskAuthor
		u.Roles.ProcessorAuthor = u.Roles.ProcessorAuthor || *processorAuthor
		u.Roles.Engineer = append(u.Roles.Engineer, engineerIDs...)
	})
	if err != nil {
		fmt.Fprintf(stderr, "custos token create: %v\n", err)
		return 1
	}
	id, token, err := m.CreateToken(*email, *label)
	if err != nil {
		fmt.Fprintf(stderr, "custos token create: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "custos token create: token %s for %s, roles %+v\n", id, *email, u.Roles)
	fmt.Fprintln(stdout, token)
	return 0
}
