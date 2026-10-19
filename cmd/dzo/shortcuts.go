// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// newStartCmd and newStopCmd are the short forms the documentation uses.
func newStartCmd() *cobra.Command {
	c := newInstanceStartCmd()
	c.Short = "Start an instance (same as dzo instance start)"
	return c
}

// newStopCmd stops an instance and keeps it stopped. Without options it is dzo instance stop; with
// any of the countdown options it announces to the players first, like dzo restart.
func newStopCmd() *cobra.Command {
	var configPath, text string
	var minutes, lock, kick int
	var graceful, cancel bool
	c := newInstanceStopCmd()
	plain := c.RunE
	c.Use = "stop <name> [--graceful | --minutes N --lock N --kick N --text T] [--cancel]"
	c.Short = "Stop an instance and keep it stopped; with a countdown option, announce, lock and kick the players first"
	c.Long = "Without options the server is stopped at once (dzo instance stop). --graceful, or any of --minutes, " +
		"--lock, --kick and --text, counts down in game like dzo restart: announcements, the lock against new " +
		"logins --lock minutes before the stop, the kick --kick minutes before it, then the stop. The values " +
		"that are not given come from the instance's restarts.announce. --cancel ends a countdown that is " +
		"running and unlocks the server."
	c.RunE = func(cmd *cobra.Command, args []string) error {
		f := cmd.Flags()
		counting := graceful || f.Changed("minutes") || f.Changed("lock") || f.Changed("kick") || text != ""
		if !counting && !cancel {
			return plain(cmd, args)
		}
		cfg, inst, err := loadInstance(configPath, args[0])
		if err != nil {
			return err
		}
		lc := instance.Lifecycle{UserMode: true}
		if u, err := f.GetBool("user"); err == nil {
			lc.UserMode = u
		}
		if s, err := f.GetString("command"); err == nil {
			lc.Command = s
		}
		r := rconRestarter(cmd, cfg, inst, lc)
		if cancel {
			return cancelCountdown(cmd, inst, r)
		}
		a := resolve.AnnouncementOf(inst.Restarts.Announce, text)
		if f.Changed("minutes") {
			a.Minutes = minutes
		}
		if f.Changed("lock") {
			a.Lock = lock
		}
		if f.Changed("kick") {
			a.Kick = kick
		}
		err = r.Stop(cmd.Context(), args[0], a)
		if errors.Is(err, instance.ErrCancelled) {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "the stop was cancelled")
			return nil
		}
		return err
	}
	configFlag(c, &configPath)
	c.Flags().BoolVar(&graceful, "graceful", false, "count down with restarts.announce before stopping")
	c.Flags().IntVar(&minutes, "minutes", 0, "start the countdown this many minutes before the stop (default: restarts.announce.minutes)")
	c.Flags().IntVar(&lock, "lock", 0, "lock the server this many minutes before the stop")
	c.Flags().IntVar(&kick, "kick", 0, "kick the players this many minutes before the stop")
	c.Flags().StringVar(&text, "text", "", "announcement text (default: Server shutdown)")
	c.Flags().BoolVar(&cancel, "cancel", false, "cancel a stop (or restart) that is counting down, and unlock the server")
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
			icons := admin.IconNames(cfg.Web.Assets)
			for _, name := range tree.InstanceNames() {
				inst, err := resolve.Resolve(cfg, tree, name)
				if err != nil {
					failed++
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-20s FAILED: %v\n", name, err)
					continue
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-20s ok\n", name)
				for _, w := range debugClientWarnings(name, tree.Instances[name].Mods) {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), w)
				}
				for _, ic := range admin.UnknownIcons(inst.AdminMap.Watch, icons) {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: admin_map icon %q is not in the Fork Awesome font installed here; the map shows a dot instead\n", name, ic)
				}
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
