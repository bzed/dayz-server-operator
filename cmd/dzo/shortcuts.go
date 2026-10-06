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
		Short: "Show the server console of an instance (the unit's journal); rotate, archive and crash-summary handle the profile logs",
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
	c.AddCommand(newLogsRotateCmd(), newLogsArchiveCmd(), newLogsCrashSummaryCmd())
	return c
}

func newSiteCmd() *cobra.Command {
	c := &cobra.Command{Use: "site", Short: "The site repository: pull, validate, status, commit"}
	c.AddCommand(newSitePullCmd(), newSiteValidateCmd(), newSiteStatusCmd(), newSiteCommitCmd())
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

func siteRepo(configPath string) (*site.Repo, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	branch := cfg.Site.Branch
	if branch == "" {
		branch = "main"
	}
	r := &site.Repo{Dir: cfg.Paths.Site, RemoteURL: cfg.Site.Remote, Branch: branch, DeployKeyPath: cfg.Site.DeployKeyPath, AutoPush: cfg.Site.AutoPush}
	if !r.Cloned() {
		return nil, fmt.Errorf("the site checkout %s does not exist yet: dzo site pull", cfg.Paths.Site)
	}
	return r, nil
}

func newSiteStatusCmd() *cobra.Command {
	var configPath string
	c := &cobra.Command{
		Use:   "status",
		Short: "Uncommitted changes in the site checkout (dzo mod add and friends edit it) and its place on the branch",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := siteRepo(configPath)
			if err != nil {
				return err
			}
			out, err := r.Status(cmd.Context())
			if err != nil {
				return err
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
	configFlag(c, &configPath)
	return c
}

func newSiteCommitCmd() *cobra.Command {
	var configPath, message string
	var push bool
	c := &cobra.Command{
		Use:   "commit -m <message> [--push]",
		Short: "Commit the changes in the site checkout, and push them with --push (or site.auto_push)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := siteRepo(configPath)
			if err != nil {
				return err
			}
			if push {
				r.AutoPush = true
			}
			done, err := r.Commit(cmd.Context(), message, "")
			if err != nil {
				return err
			}
			if !done {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "nothing to commit")
				return nil
			}
			if err := r.Push(cmd.Context()); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "committed")
			return nil
		},
	}
	configFlag(c, &configPath)
	c.Flags().StringVarP(&message, "message", "m", "", "commit message")
	c.Flags().BoolVar(&push, "push", false, "push the branch to the remote")
	_ = c.MarkFlagRequired("message")
	return c
}
