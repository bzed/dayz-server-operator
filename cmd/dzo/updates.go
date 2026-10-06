// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/backup"
	"github.com/bzed/dayz-server-operator/internal/battleye"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/exporter"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/notify"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/runfiles"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/bzed/dayz-server-operator/internal/steam"
	"github.com/bzed/dayz-server-operator/internal/units"
	"github.com/bzed/dayz-server-operator/internal/updates"
)

// updateInfo is what the exporter reports about updates, from the engine's state.
func updateInfo(cfg *config.Config) func() exporter.Updates {
	return func() exporter.Updates {
		st, err := updates.LoadState(updates.StatePath(cfg))
		if err != nil {
			return exporter.Updates{}
		}
		return exporter.Updates{PendingByInstance: st.Pending(), LastCheck: st.LastCheck.Unix()}
	}
}

func steamStatusPath(cfg *config.Config) string {
	return filepath.Join(cfg.Paths.Secrets, "steam-status.json")
}

func userDirs() (quadletDir, unitDir string) {
	c, err := os.UserConfigDir()
	if err != nil {
		c = "/root/.config"
	}
	return filepath.Join(c, "containers", "systemd"), filepath.Join(c, "systemd", "user")
}

// newNotifier builds the Discord notifier of the config; nil if no webhook is configured.
func newNotifier(cfg *config.Config) func(context.Context, notify.Event) error {
	if len(cfg.Notify.Discord) == 0 {
		return nil
	}
	ts, err := notify.NewTemplateSet()
	if err != nil {
		return nil
	}
	n := &notify.DiscordNotifier{Templates: ts, Default: cfg.Notify.Discord[0].Name}
	for _, w := range cfg.Notify.Discord {
		n.Webhooks = append(n.Webhooks, notify.Webhook{Name: w.Name, URL: w.URL})
		if w.Name == "default" {
			n.Default = "default"
		}
	}
	return n.Notify
}

// rconRestarter builds the graceful restarter of an instance: systemctl for the
// unit, and RCon with the instance's password for the announcements.
func rconRestarter(cmd *cobra.Command, cfg *config.Config, inst *resolve.Instance, lc instance.Lifecycle) instance.Restarter {
	cancel := filepath.Join(inst.Paths.Runtime, "restart-cancel")
	return instance.Restarter{
		Lifecycle: lc,
		Dial: func(ctx context.Context) (instance.Commander, error) {
			pw, err := runfiles.RConPassword(cfg.Paths.Secrets, inst.Name)
			if err != nil {
				return nil, err
			}
			// A session, not a single connection: a countdown lasts minutes, and a hiccup of RCon
			// (or the server) in between must not end the restart. Unreachable at the start still
			// fails fast, so the restart goes ahead without announcing.
			s := &battleye.Session{
				Addr: "127.0.0.1:" + fmt.Sprint(inst.Ports.RCon), Password: pw,
				Options:    []battleye.Option{battleye.WithLoginTimeout(5 * time.Second)},
				MinBackoff: time.Second, MaxBackoff: 10 * time.Second,
				Log: func(f string, a ...any) { _, _ = fmt.Fprintf(cmd.ErrOrStderr(), f+"\n", a...) },
			}
			first, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			if err := s.WaitConnected(first); err != nil {
				_ = s.Close()
				return nil, fmt.Errorf("RCon is not reachable: %w", err)
			}
			return s, nil
		},
		Log: func(f string, a ...any) { _, _ = fmt.Fprintf(cmd.ErrOrStderr(), f+"\n", a...) },
		Cancelled: func() bool {
			if _, err := os.Stat(cancel); err != nil {
				return false
			}
			_ = os.Remove(cancel)
			return true
		},
	}
}

// deployFunc is what runs while an instance is down for a restart: a snapshot
// if updates are pending (a failed one aborts: nothing is changed and the
// server starts again as it was), then the unit is rewritten with the current
// generations.
func deployFunc(cmd *cobra.Command, cfg *config.Config, configPath, name string, lc instance.Lifecycle) func(context.Context) error {
	return func(ctx context.Context) error {
		tree, err := site.LoadTree(cfg.Paths.Site)
		if err != nil {
			return err
		}
		inst, err := resolve.Resolve(cfg, tree, name)
		if err != nil {
			return err
		}
		// A newer server build is not deployed by a restart: the instance is pinned to its build
		// until `dzo instance upgrade`, so it is not an update this restart applies.
		pending := slices.DeleteFunc(units.Pending(inst), func(k string) bool { return k == updates.BuildKey })
		if p := pending; len(p) > 0 {
			// pre_update: the instance is down and nothing has been changed yet; a failing hook
			// aborts, and the restart starts the server again as it was.
			if err := instanceHooks(ctx, cmd.OutOrStdout(), cfg, inst, hookPreUpdate, nil, map[string]any{"pending": p}); err != nil {
				return fmt.Errorf("%w, nothing was changed", err)
			}
			reason := backup.ModUpdate
			if err := backup.Check(cfg, inst); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: no snapshot before the update: %v\n", err)
			} else {
				res, took, err := backup.Before(ctx, backup.For(cfg, inst), inst.Backup, reason)
				if err != nil {
					return fmt.Errorf("the %s snapshot failed, nothing was changed: %w", reason, err)
				}
				if took {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "snapshot %s\n", res.Snapshot.ID)
				}
				printWarnings(cmd.ErrOrStderr(), res.Warnings)
			}
		}
		o := unitOptions(cfg, configPath, "", "")
		o.Deploy = []string{name}
		res, err := units.Sync(ctx, o, tree, lc, false)
		printWarnings(cmd.ErrOrStderr(), res.Warnings)
		if err == nil && len(pending) > 0 {
			err = instanceHooks(ctx, cmd.OutOrStdout(), cfg, inst, hookPostUpdate, nil, map[string]any{"applied": pending})
		}
		return err
	}
}

func unitOptions(cfg *config.Config, configPath, quadletDir, unitDir string) units.Options {
	q, u := userDirs()
	if quadletDir != "" {
		q = quadletDir
	}
	if unitDir != "" {
		u = unitDir
	}
	bin, err := os.Executable()
	if err != nil || strings.Contains(bin, "/go-build") {
		bin = "/usr/bin/dzo"
	}
	return units.Options{Cfg: cfg, DzoBin: bin, ConfigPath: configPath, QuadletDir: q, UnitDir: u}
}

// mergeSets is the union of two mod sets.
func mergeSets(a, b modSet) modSet {
	out := modSet{workshop: map[uint32][]uint64{}}
	for _, s := range []modSet{a, b} {
		for app, ids := range s.workshop {
			for _, id := range ids {
				if !slices.Contains(out.workshop[app], id) {
					out.workshop[app] = append(out.workshop[app], id)
				}
			}
		}
		for _, l := range s.local {
			if !slices.Contains(out.local, l) {
				out.local = append(out.local, l)
			}
		}
	}
	return out
}

func newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "update", Short: "The update engine: refresh mods, apply updates by policy, clean up old generations (§C7)"}
	cmd.AddCommand(newUpdateCheckCmd(), newUpdateStatusCmd(), newUpdateGCCmd())
	return cmd
}

func newUpdateCheckCmd() *cobra.Command {
	var f installFlags
	var apply bool
	cmd := &cobra.Command{
		Use:   "check [--apply]",
		Short: "Refresh the mods and find pending updates; with --apply, update the instances whose policy and window allow it now",
		Long: "Run hourly by dzo-update-check.timer. For every instance whose check_interval is due it brings the cache up to " +
			"date (workshop mods and local servermods), then looks at what its running unit lags behind: policy auto updates " +
			"it in one announced cycle (stop, snapshot, deploy, start) inside its update window; notify tells the admins once; " +
			"manual only records. A new server build is only reported. While Steam wants a login nothing is downloaded and " +
			"nothing is restarted.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(f.config)
			if err != nil {
				return err
			}
			lc := instance.Lifecycle{UserMode: true}
			e := &updates.Engine{
				Cfg: cfg, Load: func() (*site.Tree, error) { return site.LoadTree(cfg.Paths.Site) },
				Notify: newNotifier(cfg),
				Log:    func(f string, a ...any) { _, _ = fmt.Fprintf(cmd.ErrOrStderr(), f+"\n", a...) },
				SteamAuthRequired: func() bool {
					st, err := steam.LoadStatus(steamStatusPath(cfg))
					return err == nil && st.AuthRequired
				},
				MarkAuthRequired: func() {
					if st, err := steam.LoadStatus(steamStatusPath(cfg)); err == nil {
						st.AuthRequired = true
						_ = st.Save(steamStatusPath(cfg))
					}
				},
				NextScheduledRestart: nextScheduledRestart,
			}
			e.Refresh = func(ctx context.Context, names []string) error {
				tree, err := site.LoadTree(cfg.Paths.Site)
				if err != nil {
					return err
				}
				var set modSet
				for _, n := range names {
					s, err := collectMods(cfg, tree, n, nil)
					if err != nil {
						return err
					}
					set = mergeSets(set, s)
				}
				return installMods(cmd, &f, cfg, tree, set, false)
			}
			e.Apply = func(ctx context.Context, inst *resolve.Instance, a instance.Announcement) error {
				return rconRestarter(cmd, cfg, inst, lc).Restart(ctx, inst.Name, a, deployFunc(cmd, cfg, f.config, inst.Name, lc))
			}
			rep, err := e.Check(cmd.Context(), apply)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if rep.Paused {
				_, _ = fmt.Fprintln(out, "paused: Steam wants a login (dzo steam login); nothing was downloaded or restarted")
			}
			failed := 0
			for _, o := range rep.Outcomes {
				line := fmt.Sprintf("%s: %s", o.Instance, o.Action)
				if len(o.Pending) > 0 {
					line += " [" + strings.Join(o.Pending, ", ") + "]"
				}
				if o.Reason != "" {
					line += " (" + o.Reason + ")"
				}
				if o.Err != nil {
					line += ": " + o.Err.Error()
					failed++
				}
				_, _ = fmt.Fprintln(out, line)
			}
			if failed > 0 {
				return fmt.Errorf("%d instance(s) failed", failed)
			}
			return nil
		},
	}
	f.add(cmd)
	cmd.Flags().BoolVar(&apply, "apply", false, "also update the instances that are due by their policy")
	return cmd
}

// nextScheduledRestart returns the next maintenance restart of an instance by
// asking systemd to evaluate its calendar expressions.
func nextScheduledRestart(inst *resolve.Instance) time.Time {
	var next time.Time
	for _, expr := range inst.Restarts.Schedule {
		out, err := exec.Command("systemd-analyze", "calendar", "--iterations=1", expr).Output() //nolint:gosec // an expression from the site config, as one argument
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(out), "\n") {
			l = strings.TrimSpace(l)
			rest, ok := strings.CutPrefix(l, "Next elapse:")
			if !ok {
				continue
			}
			// "Fri 2026-10-02 16:00:00 UTC"
			f := strings.Fields(rest)
			if len(f) >= 3 {
				if t, err := time.Parse("2006-01-02 15:04:05", f[1]+" "+f[2]); err == nil && (next.IsZero() || t.Before(next)) {
					next = t
				}
			}
		}
	}
	return next
}

func newUpdateStatusCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show pending updates and what the engine decided last",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			st, err := updates.LoadState(updates.StatePath(cfg))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if s, err := steam.LoadStatus(steamStatusPath(cfg)); err == nil && s.AuthRequired {
				_, _ = fmt.Fprintln(out, "steam: login required (dzo steam login); updates are paused")
			}
			if st.LastCheck.IsZero() {
				_, _ = fmt.Fprintln(out, "no update check has run yet")
				return nil
			}
			_, _ = fmt.Fprintf(out, "last check: %s\n", st.LastCheck.Format(time.RFC3339))
			names := make([]string, 0, len(st.Instances))
			for n := range st.Instances {
				names = append(names, n)
			}
			slices.Sort(names)
			for _, n := range names {
				i := st.Instances[n]
				line := fmt.Sprintf("%s: ", n)
				if len(i.Pending) == 0 {
					line += "up to date"
				} else {
					line += "pending " + strings.Join(i.Pending, ", ") + " since " + i.PendingSince.Format(time.RFC3339)
				}
				if i.Decision != "" {
					line += " (" + i.Decision + ")"
				}
				if i.LastError != "" {
					line += " last error: " + i.LastError
				}
				_, _ = fmt.Fprintln(out, line)
			}
			return nil
		},
	}
	configFlag(cmd, &configPath)
	return cmd
}

func newUpdateGCCmd() *cobra.Command {
	var configPath string
	var minAge time.Duration
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Remove generations of the server build and mods that no instance uses and that are older than --min-age",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			e := &updates.Engine{Cfg: cfg, Load: func() (*site.Tree, error) { return site.LoadTree(cfg.Paths.Site) }}
			removed, err := e.GC(minAge)
			for _, r := range removed {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", r)
			}
			return err
		},
	}
	configFlag(cmd, &configPath)
	cmd.Flags().DurationVar(&minAge, "min-age", 14*24*time.Hour, "only remove generations older than this")
	return cmd
}

func newRestartCmd() *cobra.Command {
	var configPath, text string
	var minutes, lock, delay int
	var now, cancel bool
	cmd := &cobra.Command{
		Use:   "restart <instance> [--minutes N --lock N --delay N --text T] [--now] [--cancel]",
		Short: "Restart an instance gracefully: announce, lock, kick, stop, deploy pending updates, start",
		Long: "Announces the restart every minute, locks the server --lock minutes before the end, kicks the remaining " +
			"players, stops it, takes the snapshot the backup policy asks for if updates are pending, rewrites the unit " +
			"with the current generations and starts it again. The defaults come from the instance's restarts.announce. " +
			"--now skips the countdown. --cancel ends a countdown that is running and unlocks the server.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			lc := instance.Lifecycle{UserMode: true}
			r := rconRestarter(cmd, cfg, inst, lc)
			if cancel {
				if err := os.MkdirAll(inst.Paths.Runtime, 0o750); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(inst.Paths.Runtime, "restart-cancel"), nil, 0o600); err != nil {
					return err
				}
				if c, err := r.Dial(cmd.Context()); err == nil {
					_ = instance.Unlock(cmd.Context(), c)
					if cl, ok := c.(interface{ Close() error }); ok {
						_ = cl.Close()
					}
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "cancelled")
				return nil
			}
			a := instance.Announcement{Minutes: inst.Restarts.Announce.Minutes, Lock: inst.Restarts.Announce.Lock, Delay: inst.Restarts.Announce.Delay, Text: inst.Restarts.Announce.Text}
			if cmd.Flags().Changed("minutes") {
				a.Minutes = minutes
			}
			if cmd.Flags().Changed("lock") {
				a.Lock = lock
			}
			if cmd.Flags().Changed("delay") {
				a.Delay = delay
			}
			if text != "" {
				a.Text = text
			}
			if now {
				a.Minutes, a.Lock = 0, 0
			}
			err = r.Restart(cmd.Context(), args[0], a, deployFunc(cmd, cfg, configPath, args[0], lc))
			if errors.Is(err, instance.ErrCancelled) {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "the restart was cancelled")
				return nil
			}
			return err
		},
	}
	configFlag(cmd, &configPath)
	cmd.Flags().IntVar(&minutes, "minutes", 0, "announce for this many minutes (default: restarts.announce.minutes)")
	cmd.Flags().IntVar(&lock, "lock", 0, "lock the server this many minutes before the end")
	cmd.Flags().IntVar(&delay, "delay", 0, "seconds between the last kick and the stop")
	cmd.Flags().StringVar(&text, "text", "", "announcement text")
	cmd.Flags().BoolVar(&now, "now", false, "no countdown: lock, kick and restart at once")
	cmd.Flags().BoolVar(&cancel, "cancel", false, "cancel a restart that is counting down, and unlock the server")
	return cmd
}

func newUnitsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "units", Short: "The systemd units dzo generates (§C5)"}
	var configPath, quadletDir, unitDir string
	var dry bool
	var deploy []string
	sync := &cobra.Command{
		Use:   "sync",
		Short: "Write the quadlets and timers of every instance and the host, remove the ones that no longer apply",
		Long: "Writes dzo-<name>.container for every instance that is installed completely, the restart and backup " +
			"timers of its schedules, the update check, the daily snapshot prune, the status snapshot and the exporter " +
			"service, tells systemd, and removes generated units that no longer apply (only files with dzo's header). " +
			"It never starts or stops a game server. An instance with updates pending keeps the unit it runs " +
			"until it is restarted (dzo restart); --deploy <name> (or *) writes the new generations now.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return syncUnits(cmd, configPath, quadletDir, unitDir, deploy, dry)
		},
	}
	configFlag(sync, &configPath)
	sync.Flags().BoolVar(&dry, "dry-run", false, "only say what would change")
	sync.Flags().StringSliceVar(&deploy, "deploy", nil, "instances (or *) whose pending updates are deployed now")
	sync.Flags().StringVar(&quadletDir, "quadlet-dir", "", "quadlet directory (default ~/.config/containers/systemd)")
	sync.Flags().StringVar(&unitDir, "unit-dir", "", "systemd user unit directory (default ~/.config/systemd/user)")
	cmd.AddCommand(sync)
	return cmd
}
