// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"os/exec"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// newStartCmd and newStopCmd are the short forms the documentation uses.
func newStartCmd() *cobra.Command {
	c := newInstanceStartCmd()
	c.Short = "Start an instance (same as dzo instance start)"
	return c
}

func newStopCmd() *cobra.Command {
	c := newInstanceStopCmd()
	c.Short = "Stop an instance and keep it stopped (same as dzo instance stop)"
	return c
}

// newLogsCmd shows the server console of an instance from the user's journal.
func newLogsCmd() *cobra.Command {
	var follow bool
	var lines int
	var journalctl string
	c := &cobra.Command{
		Use:   "logs <name>",
		Short: "Show the server console of an instance (the unit's journal)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := []string{"--user", "-u", "dzo-" + args[0] + ".service", "-n", strconv.Itoa(lines), "--no-pager"}
			if follow {
				a = append(a, "-f")
			}
			j := exec.CommandContext(cmd.Context(), journalctl, a...) //nolint:gosec // our own unit name, the binary is a flag for tests
			j.Stdout, j.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
			return j.Run()
		},
	}
	c.Flags().BoolVarP(&follow, "follow", "f", false, "keep following the console")
	c.Flags().IntVarP(&lines, "lines", "n", 100, "how many lines to show first")
	c.Flags().StringVar(&journalctl, "journalctl", "journalctl", "journalctl binary to run")
	return c
}

func newSiteCmd() *cobra.Command {
	c := &cobra.Command{Use: "site", Short: "The site repository: pull it, check it"}
	c.AddCommand(newSitePullCmd(), newSiteValidateCmd())
	return c
}

func newSitePullCmd() *cobra.Command {
	var configPath string
	c := &cobra.Command{
		Use:   "pull",
		Short: "Clone the site repository, or fast-forward it to the remote branch",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			branch := cfg.Site.Branch
			if branch == "" {
				branch = "main"
			}
			r := &site.Repo{Dir: cfg.Paths.Site, RemoteURL: cfg.Site.Remote, Branch: branch, DeployKeyPath: cfg.Site.DeployKeyPath, AutoPush: cfg.Site.AutoPush}
			if r.Cloned() {
				err = r.Pull(cmd.Context())
			} else {
				err = r.Clone(cmd.Context())
			}
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "site %s is up to date (%s)\n", cfg.Paths.Site, branch)
			return nil
		},
	}
	configFlag(c, &configPath)
	return c
}

func newSiteValidateCmd() *cobra.Command {
	var configPath string
	c := &cobra.Command{
		Use:   "validate",
		Short: "Load the site repository and resolve every instance",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, tree, err := loadSite(configPath)
			if err != nil {
				return err
			}
			failed := 0
			for _, name := range tree.InstanceNames() {
				if _, err := resolve.Resolve(cfg, tree, name); err != nil {
					failed++
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-20s FAILED: %v\n", name, err)
					continue
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-20s ok\n", name)
			}
			if failed > 0 {
				return fmt.Errorf("%d instance(s) do not resolve", failed)
			}
			return nil
		},
	}
	configFlag(c, &configPath)
	return c
}
