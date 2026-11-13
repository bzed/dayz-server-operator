// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"sync"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// newStartCmd and newStopCmd are the short forms the documentation uses.
func newStartCmd() *cobra.Command {
	var configPath string
	var all bool
	c := newInstanceStartCmd()
	plain := c.RunE
	c.Use = "start [<name> | --all]"
	c.Short = "Start an instance, or every instance one after another (--all)"
	c.Long = "Without --all the instance is started at once (dzo instance start). --all starts every instance " +
		"of the site, one after another in name order: staggered starts are gentler on the disk and the I/O. " +
		"An instance that fails to start does not stop the others; the command fails with the number of the " +
		"instances that failed."
	c.Args = cobra.MaximumNArgs(1)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if all == (len(args) == 1) {
			return fmt.Errorf("give an instance name or --all")
		}
		if !all {
			return plain(cmd, args)
		}
		_, tree, err := loadSite(configPath)
		if err != nil {
			return err
		}
		lc := lifecycleOf(cmd)
		failed := 0
		for _, n := range tree.InstanceNames() {
			if err := lc.Start(cmd.Context(), n); err != nil {
				failed++
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", n, err)
			}
		}
		if failed > 0 {
			return fmt.Errorf("%d instance(s) failed", failed)
		}
		return nil
	}
	configFlag(c, &configPath)
	c.Flags().BoolVar(&all, "all", false, "every instance")
	return c
}

// lifecycleOf builds the systemctl runner from the --command and --user flags.
func lifecycleOf(cmd *cobra.Command) instance.Lifecycle {
	lc := instance.Lifecycle{UserMode: true}
	if u, err := cmd.Flags().GetBool("user"); err == nil {
		lc.UserMode = u
	}
	if s, err := cmd.Flags().GetString("command"); err == nil {
		lc.Command = s
	}
	return lc
}

// stopOptions are the countdown flags of dzo stop. counting is true when any of --graceful,
// --minutes, --lock, --kick and --text was given.
type stopOptions struct {
	counting, cancel bool

	minutes, lock, kick          int
	minutesSet, lockSet, kickSet bool
	text                         string
}

// stopWithOptions cancels a countdown, or counts down and stops one resolved instance.
func stopWithOptions(cmd *cobra.Command, cfg *config.Config, inst *resolve.Instance, lc instance.Lifecycle, o stopOptions) error {
	r := rconRestarter(cmd, cfg, inst, lc)
	if o.cancel {
		return cancelCountdown(cmd, inst, r)
	}
	a := resolve.AnnouncementOf(inst.Restarts.Announce, o.text)
	if o.minutesSet {
		a.Minutes = o.minutes
	}
	if o.lockSet {
		a.Lock = o.lock
	}
	if o.kickSet {
		a.Kick = o.kick
	}
	err := r.Stop(cmd.Context(), inst.Name, a)
	if errors.Is(err, instance.ErrCancelled) {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: the stop was cancelled\n", inst.Name)
		return nil
	}
	return err
}

// stopOne stops one instance: plainly without a countdown option (no resolution, a plain stop must
// work even when the instance does not resolve), otherwise after cancelling or counting down.
func stopOne(cmd *cobra.Command, cfg *config.Config, tree *site.Tree, name string, lc instance.Lifecycle, o stopOptions) error {
	if !o.counting && !o.cancel {
		return lc.Stop(cmd.Context(), name)
	}
	inst, err := resolve.Resolve(cfg, tree, name)
	if err != nil {
		return err
	}
	return stopWithOptions(cmd, cfg, inst, lc, o)
}

// stopAll stops every instance of the site at the same time: a systemctl stop blocks until the
// server is down, and with a countdown every server counts down in parallel. One instance that
// fails does not stop the others.
func stopAll(cmd *cobra.Command, cfg *config.Config, tree *site.Tree, lc instance.Lifecycle, o stopOptions) error {
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		failed int
	)
	for _, n := range tree.InstanceNames() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := stopOne(cmd, cfg, tree, n, lc, o); err != nil {
				mu.Lock()
				defer mu.Unlock()
				failed++
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", n, err)
			}
		}()
	}
	wg.Wait()
	if failed > 0 {
		return fmt.Errorf("%d instance(s) failed", failed)
	}
	return nil
}

// newStopCmd stops an instance and keeps it stopped. Without options it is dzo instance stop; with
// any of the countdown options it announces to the players first, like dzo restart.
func newStopCmd() *cobra.Command {
	var configPath, text string
	var minutes, lock, kick int
	var graceful, cancel, all bool
	c := newInstanceStopCmd()
	plain := c.RunE
	c.Use = "stop [<name> | --all] [--graceful | --minutes N --lock N --kick N --text T] [--cancel]"
	c.Short = "Stop an instance (or every instance) and keep it stopped; with a countdown option, announce, lock and kick the players first"
	c.Long = "Without options the server is stopped at once (dzo instance stop). --graceful, or any of --minutes, " +
		"--lock, --kick and --text, counts down in game like dzo restart: announcements, the lock against new " +
		"logins --lock minutes before the stop, the kick --kick minutes before it, then the stop. The values " +
		"that are not given come from the instance's restarts.announce. --cancel ends a countdown that is " +
		"running and unlocks the server. --all takes every instance of the site: the stops run at the same " +
		"time, so every server stops together, and each one counts down with its own restarts.announce values " +
		"(the flags override them for every instance). An instance that fails does not stop the others; the " +
		"command fails with the number of the instances that failed."
	c.Args = cobra.MaximumNArgs(1)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		f := cmd.Flags()
		if all == (len(args) == 1) {
			return fmt.Errorf("give an instance name or --all")
		}
		o := stopOptions{
			counting:   graceful || f.Changed("minutes") || f.Changed("lock") || f.Changed("kick") || text != "",
			cancel:     cancel,
			minutes:    minutes,
			lock:       lock,
			kick:       kick,
			minutesSet: f.Changed("minutes"),
			lockSet:    f.Changed("lock"),
			kickSet:    f.Changed("kick"),
			text:       text,
		}
		if !all {
			if !o.counting && !o.cancel {
				return plain(cmd, args)
			}
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			return stopWithOptions(cmd, cfg, inst, lifecycleOf(cmd), o)
		}
		cfg, tree, err := loadSite(configPath)
		if err != nil {
			return err
		}
		return stopAll(cmd, cfg, tree, lifecycleOf(cmd), o)
	}
	configFlag(c, &configPath)
	c.Flags().BoolVar(&all, "all", false, "every instance")
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
