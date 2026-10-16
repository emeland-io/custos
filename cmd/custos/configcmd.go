package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/emeland-io/custos/internal/config"
)

const (
	configUsage    = "usage: custos config get server-url [--show-source]\n       custos config set server-url URL\n"
	configGetUsage = "usage: custos config get server-url [--show-source]\n"
	configSetUsage = "usage: custos config set server-url URL\n"
)

// configCmd reads and writes the personal config file (ruling 5.9). Only
// server-url can live there (ruling 5.3).
func (c *cli) configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "read or write the personal config file",
		Args:  cobra.ArbitraryArgs,
		RunE:  func(*cobra.Command, []string) error { return usageError(configUsage) },
	}
	var showSource bool
	get := &cobra.Command{
		Use:   "get server-url",
		Short: "print a setting as the commands see it: flag, then environment, then config file",
		Args:  exactArgs(1, configGetUsage),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != config.KeyServerURL {
				return usageError(fmt.Sprintf("custos config get: unknown key %q; the only key is %q\n", args[0], config.KeyServerURL))
			}
			path, err := config.ResolvePath(c.configPath)
			if err != nil {
				fmt.Fprintf(c.stderr, "custos config get: %v\n", err)
				return exitCode(1)
			}
			s, err := config.New(cmd.Flags(), []string{config.KeyServerURL}, &config.Options{Path: path, Warn: c.stderr})
			if err != nil {
				fmt.Fprintf(c.stderr, "custos config get: %v\n", err)
				return exitCode(1)
			}
			val := s.String(config.KeyServerURL)
			if val == "" {
				fmt.Fprintf(c.stderr, "custos config get: %s is not set (--%s, $%s or %s)\n",
					config.KeyServerURL, config.KeyServerURL, config.EnvNames[config.KeyServerURL], path)
				return exitCode(1)
			}
			if !showSource {
				fmt.Fprintln(c.stdout, val)
				return nil
			}
			from := string(s.Source(config.KeyServerURL))
			switch s.Source(config.KeyServerURL) {
			case config.SourceEnv:
				from = "env " + config.EnvNames[config.KeyServerURL]
			case config.SourceFile:
				from = "file " + path
			}
			fmt.Fprintf(c.stdout, "%s (%s)\n", val, from)
			return nil
		},
	}
	get.Flags().BoolVar(&showSource, "show-source", false, "also say whether the value came from the flag, the environment or the config file")
	set := &cobra.Command{
		Use:   "set server-url URL",
		Short: "write a setting to the config file",
		Args:  exactArgs(2, configSetUsage),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != config.KeyServerURL {
				return usageError(fmt.Sprintf("custos config set: unknown key %q; the only key is %q\n", args[0], config.KeyServerURL))
			}
			if err := config.CheckURL(args[1]); err != nil {
				return usageError(fmt.Sprintf("custos config set: %v\n", err))
			}
			path, err := config.ResolvePath(c.configPath)
			if err == nil {
				err = config.Set(path, args[0], args[1])
			}
			if err != nil {
				fmt.Fprintf(c.stderr, "custos config set: %v\n", err)
				return exitCode(1)
			}
			fmt.Fprintf(c.stdout, "%s set in %s\n", args[0], path)
			return nil
		},
	}
	cmd.AddCommand(get, set)
	return cmd
}
