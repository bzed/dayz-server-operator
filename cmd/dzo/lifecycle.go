// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/bzed/dayz-server-operator/internal/backup"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/mission"
	"github.com/bzed/dayz-server-operator/internal/moddeps"
	"github.com/bzed/dayz-server-operator/internal/product"
	"github.com/bzed/dayz-server-operator/internal/quadlet"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/bzed/dayz-server-operator/internal/units"
)

// confirm asks for an explicit "yes" on stdin before something destructive; yes (a --yes flag)
// answers for scripts.
func confirm(cmd *cobra.Command, yes bool, what string) error {
	if yes {
		return nil
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s\nType 'yes' to continue: ", what)
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if strings.TrimSpace(line) != "yes" {
		return errors.New("aborted")
	}
	return nil
}

// removeStorage deletes the persistence of an instance: everything below its storage/<map> directory.
func removeStorage(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return 0, err
		}
	}
	return len(entries), nil
}

// syncUnits is `dzo units sync` and `dzo instance apply`.
func syncUnits(cmd *cobra.Command, configPath, quadletDir, unitDir string, deploy []string, dry bool) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	tree, err := site.LoadTree(cfg.Paths.Site)
	if err != nil {
		return err
	}
	o := unitOptions(cfg, configPath, quadletDir, unitDir)
	o.Deploy = deploy
	res, err := units.Sync(cmd.Context(), o, tree, instance.Lifecycle{UserMode: true}, dry)
	out := cmd.OutOrStdout()
	verb := "wrote"
	if dry {
		verb = "would write"
	}
	for _, w := range res.Written {
		_, _ = fmt.Fprintf(out, "%s %s\n", verb, w)
	}
	for _, r := range res.Removed {
		_, _ = fmt.Fprintf(out, "removed %s\n", r)
	}
	printWarnings(cmd.ErrOrStderr(), res.Warnings)
	if err == nil && len(res.Written) == 0 && len(res.Removed) == 0 {
		_, _ = fmt.Fprintln(out, "units are up to date")
	}
	return err
}

func newInstanceCreateCmd() *cobra.Command {
	var configPath string
	c := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an instance from the site repository: its subvolume, the pristine mission, the first live mission",
		Long: "The first render of an instance. It fails if the instance exists already (dzo instance render updates " +
			"one that does). Nothing is started: `dzo instance apply <name>` writes the units, `dzo start <name>` starts it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			if _, err := os.Lstat(inst.Paths.Root); err == nil {
				return fmt.Errorf("instance %s exists (%s); dzo instance render updates it", inst.Name, inst.Paths.Root)
			}
			if len(inst.Missing) > 0 {
				return fmt.Errorf("instance %s is not installed completely: %s", inst.Name, strings.Join(inst.Missing, "; "))
			}
			if err := renderInstance(cmd, cfg, inst, false, false); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "created %s; next: dzo instance apply %s && dzo start %s\n", inst.Name, inst.Name, inst.Name)
			return nil
		},
	}
	configFlag(c, &configPath)
	return c
}

func newInstanceApplyCmd() *cobra.Command {
	var configPath string
	var dry bool
	c := &cobra.Command{
		Use:   "apply <name>",
		Short: "Write the units and timers of an instance (and of the host) with the current generations",
		Long: "dzo units sync with the instance's pending mod generations deployed. A running server keeps what it " +
			"runs until it is restarted. The server build is not changed (dzo instance upgrade does that).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, tree, err := loadSite(configPath)
			if err != nil {
				return err
			}
			if _, err := tree.Instance(args[0]); err != nil {
				return err
			}
			return syncUnits(cmd, configPath, "", "", []string{args[0]}, dry)
		},
	}
	configFlag(c, &configPath)
	c.Flags().BoolVar(&dry, "dry-run", false, "only say what would change")
	return c
}

// addonPatches reads the CfgPatches classes of every PBO below dir/addons (PBOs above maxPBO are
// skipped: the configs of the game are in the small ones).
func addonPatches(dir string) ([]moddeps.Patch, int, error) {
	const maxPBO = 128 << 20
	files, _ := filepath.Glob(filepath.Join(dir, "addons", "*.pbo"))
	sort.Strings(files)
	var out []moddeps.Patch
	skipped := 0
	for _, f := range files {
		if fi, err := os.Stat(f); err != nil || fi.Size() > maxPBO {
			skipped++
			continue
		}
		p, err := loadPatches(f)
		if err != nil {
			skipped++
			continue
		}
		out = append(out, p...)
	}
	return out, skipped, nil
}

// checkBuildDependencies validates the mods' requiredAddons against a server build: what the build
// does not provide, another mod must. It prints the problems and returns their number.
func checkBuildDependencies(cmd *cobra.Command, inst *resolve.Instance, buildDir string) (int, error) {
	out := cmd.OutOrStdout()
	baseline, skipped, err := addonPatches(buildDir)
	if err != nil {
		return 0, err
	}
	// The DLC maps ship their addons next to the game's.
	dlc, _ := filepath.Glob(filepath.Join(buildDir, "*", "addons"))
	for _, d := range dlc {
		p, _, _ := addonPatches(filepath.Dir(d))
		baseline = append(baseline, p...)
	}
	mods := []moddeps.Mod{{ID: 0, Patches: baseline}}
	names := map[uint64]string{}
	for i, m := range inst.Mods {
		if m.Dir == "" {
			continue
		}
		p, _, _ := addonPatches(m.Dir)
		id := uint64(i + 1) //nolint:gosec // bounded by the mod list
		mods = append(mods, moddeps.Mod{ID: id, Patches: p})
		names[id] = m.Name
	}
	res := moddeps.Validate(mods)
	for _, m := range res.Missing {
		_, _ = fmt.Fprintf(out, "dependency: %s [%s]\n", m.String(), names[m.ModID])
	}
	if skipped > 0 {
		_, _ = fmt.Fprintf(out, "note: %d addon PBO(s) of the build were too large or unreadable to check\n", skipped)
	}
	if res.OK() {
		_, _ = fmt.Fprintln(out, "every requiredAddons entry of the mods is provided by the new build or another mod")
	}
	return len(res.Missing), nil
}

func newInstanceUpgradeCmd() *cobra.Command {
	var configPath, build, text string
	var dry, wipe, yes, now bool
	var minutes, lock, delay int
	c := &cobra.Command{
		Use:   "upgrade <name> --build <buildid> [--wipe] [--dry-run]",
		Short: "Switch an instance to another installed server build: announce, stop, snapshot, wipe, switch, render, start",
		Long: "An instance runs the server build it was deployed with; a newer build (dzo product update) waits until you " +
			"upgrade each instance explicitly. --dry-run shows the render plan for the new build and checks the mods' " +
			"dependencies against it. --wipe also deletes the world (the persistence) after a destructive snapshot.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			if len(inst.Missing) > 0 {
				return fmt.Errorf("instance %s is not installed completely: %s", inst.Name, strings.Join(inst.Missing, "; "))
			}
			store := product.ProductStore(cfg.Paths.Cache, inst.Product.Name)
			gens, _ := store.Generations()
			if !slices.Contains(gens, build) {
				return fmt.Errorf("build %q of %s is not installed (installed: %s); dzo product update downloads one", build, inst.Product.Name, strings.Join(gens, ", "))
			}
			if build == inst.Product.Build {
				return fmt.Errorf("instance %s runs build %s already", inst.Name, build)
			}
			out := cmd.OutOrStdout()
			newDir := filepath.Join(store.Root, build)
			_, _ = fmt.Fprintf(out, "upgrade %s: build %s -> %s%s\n", inst.Name, inst.Product.Build, build, map[bool]string{true: ", with a wipe", false: ""}[wipe])
			if dry {
				cp := *inst
				cp.Product.Build, cp.Product.Dir = build, newDir
				if n, err := checkBuildDependencies(cmd, &cp, newDir); err != nil {
					return err
				} else if n > 0 {
					_, _ = fmt.Fprintf(out, "%d dependency problem(s): the server would not start\n", n)
				}
				return renderInstance(cmd, cfg, &cp, true, false)
			}
			if wipe {
				if err := confirm(cmd, yes, "This deletes the world (persistence) of "+inst.Name+" after a snapshot."); err != nil {
					return err
				}
			}
			if n, err := checkBuildDependencies(cmd, inst, newDir); err != nil {
				return err
			} else if n > 0 {
				return fmt.Errorf("%d dependency problem(s) against build %s: not upgrading", n, build)
			}
			lc := instance.Lifecycle{UserMode: true}
			r := rconRestarter(cmd, cfg, inst, lc)
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
			return r.Restart(cmd.Context(), inst.Name, a, upgradeFunc(cmd, cfg, configPath, inst, build, wipe, lc))
		},
	}
	configFlag(c, &configPath)
	c.Flags().StringVar(&build, "build", "", "the installed build to switch to (required)")
	c.Flags().BoolVar(&dry, "dry-run", false, "show the render plan and the dependency check for the new build, change nothing")
	c.Flags().BoolVar(&wipe, "wipe", false, "also delete the world (persistence) after a destructive snapshot")
	c.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation (with --wipe)")
	c.Flags().BoolVar(&now, "now", false, "no countdown: lock, kick and switch at once")
	c.Flags().IntVar(&minutes, "minutes", 0, "announce for this many minutes (default: restarts.announce.minutes)")
	c.Flags().IntVar(&lock, "lock", 0, "lock the server this many minutes before the end")
	c.Flags().IntVar(&delay, "delay", 0, "seconds between the last kick and the stop")
	c.Flags().StringVar(&text, "text", "", "announcement text")
	_ = c.MarkFlagRequired("build")
	return c
}

// upgradeFunc is what runs while the instance is down for an upgrade: pre_update hooks, the
// snapshots, the wipe, the pin to the new build and the unit that runs it, post_update hooks. A
// failure puts the pin back, and the restart starts the server again as it was.
func upgradeFunc(cmd *cobra.Command, cfg *config.Config, configPath string, inst *resolve.Instance, build string, wipe bool, lc instance.Lifecycle) func(context.Context) error {
	return func(ctx context.Context) (err error) {
		out := cmd.OutOrStdout()
		if err := instanceHooks(ctx, out, cfg, inst, hookPreUpdate, nil, map[string]any{"build": build}); err != nil {
			return fmt.Errorf("%w, nothing was changed", err)
		}
		if berr := backup.Check(cfg, inst); berr != nil {
			if wipe {
				return fmt.Errorf("no wipe without a snapshot: %w", berr)
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: no snapshot before the upgrade: %v\n", berr)
		} else {
			m := backup.For(cfg, inst)
			if res, took, err := backup.Before(ctx, m, inst.Backup, backup.Update); err != nil {
				return fmt.Errorf("the update snapshot failed, nothing was changed: %w", err)
			} else if took {
				_, _ = fmt.Fprintf(out, "snapshot %s\n", res.Snapshot.ID)
			}
			if wipe {
				res, err := m.Create(ctx, backup.Destructive)
				if err != nil {
					return fmt.Errorf("the destructive snapshot failed, nothing was wiped: %w", err)
				}
				_, _ = fmt.Fprintf(out, "snapshot %s (destructive)\n", res.Snapshot.ID)
			}
		}
		if wipe {
			n, err := removeStorage(inst.Paths.Storage)
			if err != nil {
				return fmt.Errorf("wiping %s: %w", inst.Paths.Storage, err)
			}
			_, _ = fmt.Fprintf(out, "wiped %s (%d entries)\n", inst.Paths.Storage, n)
		}
		prev, _ := os.ReadFile(resolve.BuildPinFile(inst.Paths.Runtime))
		if err := resolve.PinBuild(inst.Paths.Runtime, build); err != nil {
			return err
		}
		defer func() {
			if err != nil { // the unit was not switched: keep the instance on its build
				_ = resolve.PinBuild(inst.Paths.Runtime, strings.TrimSpace(string(prev)))
			}
		}()
		tree, err := site.LoadTree(cfg.Paths.Site)
		if err != nil {
			return err
		}
		o := unitOptions(cfg, configPath, "", "")
		o.Deploy = []string{inst.Name}
		res, err := units.Sync(ctx, o, tree, lc, false)
		printWarnings(cmd.ErrOrStderr(), res.Warnings)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "%s now runs build %s\n", inst.Name, build)
		return instanceHooks(ctx, out, cfg, inst, hookPostUpdate, nil, map[string]any{"build": build})
	}
}

func newInstanceModsCmd() *cobra.Command {
	c := &cobra.Command{Use: "mods", Short: "The mod list of an instance and its order"}
	var configPath string
	list := &cobra.Command{
		Use:   "list <name>",
		Short: "List the mods in load (and merge precedence) order",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			for i, m := range inst.Mods {
				kind := "-mod"
				if m.Server {
					kind = "-servermod"
				}
				gen := m.Generation
				if gen == "" {
					gen = "not installed"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%2d  %-12s %-11s %s\n", i+1, m.Name, kind, gen)
			}
			return nil
		},
	}
	var before, after string
	move := &cobra.Command{
		Use:   "move <name> <id | local name> (--before <other> | --after <other>)",
		Short: "Move a mod in the list of instance.yaml; the order is the load order",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (before == "") == (after == "") {
				return errors.New("give --before or --after, one of them")
			}
			_, tree, err := loadSite(configPath)
			if err != nil {
				return err
			}
			if _, err := tree.Instance(args[0]); err != nil {
				return err
			}
			other, isAfter := before, false
			if after != "" {
				other, isAfter = after, true
			}
			return site.MoveMod(filepath.Join(tree.Dir, "instances", args[0], "instance.yaml"), args[1], other, isAfter)
		},
	}
	move.Flags().StringVar(&before, "before", "", "put the mod right before this one")
	move.Flags().StringVar(&after, "after", "", "put the mod right after this one")
	configFlag(list, &configPath)
	configFlag(move, &configPath)
	c.AddCommand(list, move)
	return c
}

func newModRemoveCmd() *cobra.Command {
	var configPath, instName string
	c := &cobra.Command{
		Use:   "remove <workshop id | local name> --instance <name>",
		Short: "Remove a mod from an instance's mod list in the site repo",
		Long: "Edits instance.yaml (its comments are kept, the change is left uncommitted). The mod stays in the cache " +
			"until nothing uses it (dzo update gc); the server drops it at its next restart.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, tree, err := loadSite(configPath)
			if err != nil {
				return err
			}
			if _, err := tree.Instance(instName); err != nil {
				return err
			}
			return site.RemoveMod(filepath.Join(tree.Dir, "instances", instName, "instance.yaml"), args[0])
		},
	}
	configFlag(c, &configPath)
	c.Flags().StringVar(&instName, "instance", "", "instance to remove the mod from (required)")
	_ = c.MarkFlagRequired("instance")
	return c
}

// --- mission ---------------------------------------------------------------------------------

func newMissionUpdateCmd() *cobra.Command {
	var configPath string
	var dry bool
	c := &cobra.Command{
		Use:   "update <name>",
		Short: "Fetch a new pristine mission from its git source and show what the next render would change",
		Long: "Only servermpmissions/ changes: the live mission follows on the next render. With --dry-run the pristine mission " +
			"of the instance is left alone and the plan is computed against the freshly fetched one.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			src := inst.Mission.Source
			cp := *inst
			dest := inst.Paths.Pristine
			if dry {
				tmp, err := os.MkdirTemp("", "dzo-pristine-")
				if err != nil {
					return err
				}
				defer os.RemoveAll(tmp) //nolint:errcheck // scratch
				dest = filepath.Join(tmp, "pristine")
				cp.Paths.Pristine = dest
			}
			if err := mission.FetchPristine(cmd.Context(), src.Git, src.Ref, src.Path, dest); err != nil {
				return err
			}
			return renderInstance(cmd, cfg, &cp, true, false)
		},
	}
	configFlag(c, &configPath)
	c.Flags().BoolVar(&dry, "dry-run", false, "fetch to a scratch directory and only show the plan")
	return c
}

func newMissionInitCmd() *cobra.Command {
	var configPath string
	c := &cobra.Command{
		Use:   "init <name>",
		Short: "Create the live mission from the pristine one (once; an existing live mission is never touched)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			if _, err := os.Stat(inst.Paths.Live); err == nil {
				return fmt.Errorf("the live mission %s exists; dzo instance render updates it, dzo mission reinit recreates it", inst.Paths.Live)
			}
			return renderInstance(cmd, cfg, inst, false, false)
		},
	}
	configFlag(c, &configPath)
	return c
}

func newMissionRollbackCmd() *cobra.Command {
	var configPath string
	var list bool
	c := &cobra.Command{
		Use:   "rollback <name> [<time>]",
		Short: "Restore the managed mission files that an earlier render overwrote (from filehistory/)",
		Long: "<time> is the start of a filehistory set name (see --list); without it the newest set is used. A rollback holds " +
			"until the next render: make lasting changes in the pristine mission or an overlay.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if list {
				sets, err := mission.HistorySets(inst.Paths.FileHistory)
				for _, s := range sets {
					_, _ = fmt.Fprintln(out, s)
				}
				return err
			}
			ts := ""
			if len(args) == 2 {
				ts = args[1]
			}
			m, err := mission.LoadManifest(inst.Paths.Manifest)
			if err != nil {
				return err
			}
			set, restored, err := mission.Rollback(inst.Paths.FileHistory, inst.Paths.Live, m, ts)
			if err != nil {
				return err
			}
			if err := m.Save(inst.Paths.Manifest); err != nil {
				return err
			}
			for _, r := range restored {
				_, _ = fmt.Fprintf(out, "restored %s\n", r)
			}
			_, _ = fmt.Fprintf(out, "rolled back to %s (%d files); the next render replaces them again\n", set, len(restored))
			return nil
		},
	}
	configFlag(c, &configPath)
	c.Flags().BoolVar(&list, "list", false, "list the filehistory sets and stop")
	return c
}

func newMissionReinitCmd() *cobra.Command {
	var configPath string
	var yes bool
	c := &cobra.Command{
		Use:   "reinit <name>",
		Short: "Recreate the live mission from the pristine one (destructive: asks, and snapshots first)",
		Long: "The only mission command that deletes the live mission. The server must be stopped. A destructive snapshot is " +
			"taken first and the command refuses to go on without one. The world (persistence) is not part of the mission " +
			"and stays.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			if active, _ := (instance.Lifecycle{UserMode: true}).IsActive(cmd.Context(), inst.Name); active {
				return fmt.Errorf("instance %s is running: stop it first", inst.Name)
			}
			if _, err := os.Stat(inst.Paths.Live); err != nil {
				return fmt.Errorf("there is no live mission to recreate (dzo mission init creates one)")
			}
			if err := backup.Check(cfg, inst); err != nil {
				return fmt.Errorf("no reinit without a snapshot: %w", err)
			}
			if err := confirm(cmd, yes, "This deletes the live mission of "+inst.Name+" ("+inst.Paths.Live+") and recreates it from the pristine mission."); err != nil {
				return err
			}
			res, err := backup.For(cfg, inst).Create(cmd.Context(), backup.Destructive)
			if err != nil {
				return fmt.Errorf("the snapshot failed, nothing was deleted: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "snapshot %s (destructive)\n", res.Snapshot.ID)
			if err := os.RemoveAll(inst.Paths.Live); err != nil {
				return err
			}
			if err := os.Remove(inst.Paths.Manifest); err != nil && !os.IsNotExist(err) {
				return err
			}
			return renderInstance(cmd, cfg, inst, false, false)
		},
	}
	configFlag(c, &configPath)
	c.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
	return c
}

// --- wipe, shell, exec -----------------------------------------------------------------------

func newWipeCmd() *cobra.Command {
	var configPath string
	var yes, restart, noSnapshot bool
	c := &cobra.Command{
		Use:   "wipe <name>",
		Short: "Delete the world (the persistence) of an instance: asks, snapshots first",
		Long: "Removes everything below the instance's storage/<map> directory. The server must be stopped, or pass --restart " +
			"to stop it for the wipe and start it again. A destructive snapshot is taken first (--no-snapshot skips it, " +
			"which is the only way to wipe an instance that is not on btrfs).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			lc := instance.Lifecycle{UserMode: true}
			active, _ := lc.IsActive(cmd.Context(), inst.Name)
			if active && !restart {
				return fmt.Errorf("instance %s is running: stop it first, or use --restart", inst.Name)
			}
			if !noSnapshot {
				if err := backup.Check(cfg, inst); err != nil {
					return fmt.Errorf("no wipe without a snapshot (--no-snapshot skips it): %w", err)
				}
			}
			if err := confirm(cmd, yes, "This deletes the world (persistence) of "+inst.Name+" in "+inst.Paths.Storage+"."); err != nil {
				return err
			}
			if active {
				if err := lc.Stop(cmd.Context(), inst.Name); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			if !noSnapshot {
				res, err := backup.For(cfg, inst).Create(cmd.Context(), backup.Destructive)
				if err != nil {
					return fmt.Errorf("the snapshot failed, nothing was deleted: %w", err)
				}
				_, _ = fmt.Fprintf(out, "snapshot %s (destructive)\n", res.Snapshot.ID)
			}
			n, err := removeStorage(inst.Paths.Storage)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "wiped %s (%d entries)\n", inst.Paths.Storage, n)
			if active {
				return lc.Start(cmd.Context(), inst.Name)
			}
			return nil
		},
	}
	configFlag(c, &configPath)
	c.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
	c.Flags().BoolVar(&restart, "restart", false, "stop a running instance for the wipe and start it again")
	c.Flags().BoolVar(&noSnapshot, "no-snapshot", false, "wipe without a snapshot first")
	return c
}

// podmanVolume renders a quadlet volume as the argument of podman run -v.
func podmanVolume(v quadlet.Volume) string {
	s := v.Source + ":" + v.Destination
	switch {
	case v.Overlay:
		s += ":O"
	case v.ReadOnly:
		s += ":ro"
	}
	return s
}

func newShellCmd() *cobra.Command {
	var configPath, podman, network string
	c := &cobra.Command{
		Use:   "shell <name> [-- command...]",
		Short: "A debug container with the mounts of the instance and a shell, without starting the server",
		Long: "Runs the instance's image with the same mounts (build, mods, mission, profiles, storage) and /bin/bash, or the " +
			"command you give. The network is off unless you pass --network host: the server is not started, and a debug " +
			"container must not take its ports.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			if len(inst.Missing) > 0 {
				return fmt.Errorf("instance %s is not installed completely: %s", inst.Name, strings.Join(inst.Missing, "; "))
			}
			q := inst.Quadlet
			a := []string{"run", "--rm", "-it", "--network", network, "--workdir", q.WorkingDir}
			for _, v := range q.Volumes {
				a = append(a, "-v", podmanVolume(v))
			}
			envs := make([]string, 0, len(q.Environment))
			for k, v := range q.Environment {
				envs = append(envs, k+"="+v)
			}
			sort.Strings(envs)
			for _, e := range envs {
				a = append(a, "-e", e)
			}
			a = append(a, q.Image)
			if len(args) > 1 {
				a = append(a, args[1:]...)
			} else {
				a = append(a, "/bin/bash")
			}
			return runInteractive(cmd, podman, a)
		},
	}
	configFlag(c, &configPath)
	c.Flags().StringVar(&podman, "podman", "podman", "podman binary to run")
	c.Flags().StringVar(&network, "network", "none", "podman network of the debug container (host to reach the server's ports)")
	c.Flags().SetInterspersed(false) // flags of the command to run are not ours
	return c
}

func newExecCmd() *cobra.Command {
	var podman string
	c := &cobra.Command{
		Use:   "exec <name> <command...>",
		Short: "Run a command in the running container of an instance",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := []string{"exec"}
			if isTerminal(os.Stdin) {
				a = append(a, "-it")
			}
			a = append(a, "dzo-"+args[0])
			a = append(a, args[1:]...)
			return runInteractive(cmd, podman, a)
		},
	}
	c.Flags().StringVar(&podman, "podman", "podman", "podman binary to run")
	c.Flags().SetInterspersed(false) // flags of the command to run are not ours
	return c
}

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) } //nolint:gosec // a file descriptor fits an int

// runInteractive runs a program with the terminal of dzo attached and passes its exit status on.
func runInteractive(cmd *cobra.Command, bin string, args []string) error {
	p := exec.CommandContext(cmd.Context(), bin, args...) //nolint:gosec // podman and arguments built from the instance's own configuration
	p.Stdin, p.Stdout, p.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	return p.Run()
}
