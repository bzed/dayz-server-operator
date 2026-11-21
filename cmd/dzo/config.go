// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// validateSite resolves every instance of the site checkout, if there is
// one. A checkout that is not there yet is not an error: config.yaml is
// valid on its own before `dzo setup` has cloned the site repo.
func validateSite(cmd *cobra.Command, c *config.Config) error {
	out := cmd.OutOrStdout()
	if _, err := os.Stat(c.Paths.Site); err != nil {
		_, _ = fmt.Fprintf(out, "  site:      %s not present, instances not checked\n", c.Paths.Site)
		return nil
	}
	tree, err := site.LoadTree(c.Paths.Site)
	if err != nil {
		return err
	}
	all, err := resolve.ResolveAll(c, tree)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "  site:      %d instances resolve\n", len(all))
	for _, inst := range all {
		for _, m := range inst.Missing {
			_, _ = fmt.Fprintf(out, "  note: %s: %s\n", inst.Name, m)
		}
	}
	return nil
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and validate the operator configuration",
	}
	cmd.AddCommand(newConfigGetCmd(), newConfigValidateCmd())
	return cmd
}

// configPathKeys are the keys `dzo config get` prints: the resolved directory
// of every paths field. The scripts around dzo read them instead of parsing
// config.yaml themselves (the steamcmd container wrapper mounts paths.secrets
// and paths.cache).
var configPathKeys = []string{
	"paths.cache", "paths.data", "paths.db", "paths.instances",
	"paths.logs", "paths.secrets", "paths.site", "paths.snapshots",
}

// configPath is the resolved value of one key of configPathKeys.
func configPath(c *config.Config, key string) (string, bool) {
	switch key {
	case "paths.cache":
		return c.Paths.Cache, true
	case "paths.data":
		return c.Paths.Data, true
	case "paths.db":
		return c.Paths.DB, true
	case "paths.instances":
		return c.Paths.Instances, true
	case "paths.logs":
		return c.Paths.Logs, true
	case "paths.secrets":
		return c.Paths.Secrets, true
	case "paths.site":
		return c.Paths.Site, true
	case "paths.snapshots":
		return c.Paths.Snapshots, true
	}
	return "", false
}

func newConfigGetCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Print one resolved path of config.yaml",
		Long: "Print the resolved absolute path of one paths.* key, as dzo uses it after expanding ${data}: " +
			strings.Join(configPathKeys, ", ") + ". The steamcmd container wrapper reads paths.secrets and paths.cache with it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := config.Load(path)
			if err != nil {
				return err
			}
			p, ok := configPath(c, args[0])
			if !ok {
				return fmt.Errorf("unknown config key %q: use one of %s", args[0], strings.Join(configPathKeys, ", "))
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), p)
			return err
		},
	}
	configFlag(cmd, &path)
	return cmd
}

func newConfigValidateCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Load and validate config.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := config.Load(path)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "config %s is valid\n", path)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  data:      %s\n", c.Paths.Data)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  instances: %s\n", c.Paths.Instances)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  products:  %d configured\n", len(c.Products))
			return validateSite(cmd, c)
		},
	}
	cmd.Flags().StringVar(&path, "config", defaultConfigPath, "path to config.yaml")
	return cmd
}
