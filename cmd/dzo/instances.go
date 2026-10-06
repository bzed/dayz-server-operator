// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/btrfs"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/mission"
)

// instanceUnitFiles are the generated unit files of one instance.
func instanceUnitFiles(quadletDir, unitDir, name string) []string {
	var out []string
	for _, p := range []string{
		filepath.Join(quadletDir, "dzo-"+name+".container"),
		filepath.Join(unitDir, "dzo-restart-"+name+".*"),
		filepath.Join(unitDir, "dzo-backup-"+name+".*"),
	} {
		m, _ := filepath.Glob(p)
		out = append(out, m...)
	}
	return out
}

func newInstanceRemoveCmd() *cobra.Command {
	var configPath string
	var yes, keepSnapshots, deleteLogs bool
	c := &cobra.Command{
		Use:   "remove <name> [--keep-snapshots] [--delete-logs] [--yes]",
		Short: "Delete an instance: stop it, remove its units and its data directory, and (unless kept) its snapshots",
		Long: "Stops the instance, removes its units and timers, deletes instances/<name> (the whole subvolume: mission, " +
			"profiles, persistence) and, unless --keep-snapshots, its snapshots. The log archive is kept unless " +
			"--delete-logs. Mods and server builds in the cache are not touched. Afterwards delete " +
			"instances/<name>/ from the site repository and commit it, or the instance comes back with `dzo instance create`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			root := filepath.Join(cfg.Paths.Instances, name)
			if filepath.Base(root) != name || name == "" || name == "." || name == ".." {
				return fmt.Errorf("invalid instance name %q", name)
			}
			snaps := filepath.Join(cfg.Paths.Snapshots, name)
			if _, err := os.Lstat(root); err != nil {
				return fmt.Errorf("instance %s has no data directory %s", name, root)
			}
			what := "This deletes " + root
			if !keepSnapshots {
				what += " and the snapshots in " + snaps
			}
			if err := confirm(cmd, yes, what+"."); err != nil {
				return err
			}
			lc := instance.Lifecycle{UserMode: true}
			if active, _ := lc.IsActive(cmd.Context(), name); active {
				if err := lc.Stop(cmd.Context(), name); err != nil {
					return err
				}
			}
			q, u := userDirs()
			files := instanceUnitFiles(q, u, name)
			var timers []string
			for _, f := range files {
				if ext := filepath.Ext(f); ext == ".timer" {
					timers = append(timers, filepath.Base(f))
				}
			}
			if len(timers) > 0 {
				_ = lc.Disable(cmd.Context(), timers...)
			}
			for _, f := range files {
				_ = os.Remove(f)
			}
			if len(files) > 0 {
				_ = lc.DaemonReload(cmd.Context())
			}
			out := cmd.OutOrStdout()
			if err := deleteTree(root); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "removed %s and %d unit file(s)\n", root, len(files))
			if !keepSnapshots {
				n, err := deleteSnapshots(snaps)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(out, "removed %d snapshot(s)\n", n)
			}
			if deleteLogs {
				_ = os.RemoveAll(logArchive(cfg, name))
			}
			_, _ = fmt.Fprintf(out, "now delete instances/%s from the site repository and commit it\n", name)
			return nil
		},
	}
	configFlag(c, &configPath)
	c.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
	c.Flags().BoolVar(&keepSnapshots, "keep-snapshots", false, "keep the snapshots of the instance")
	c.Flags().BoolVar(&deleteLogs, "delete-logs", false, "also delete the archived profile logs")
	return c
}

// deleteTree removes a directory that may be a btrfs subvolume.
func deleteTree(dir string) error {
	if sub, _ := btrfs.IsSubvolume(dir); sub {
		return btrfs.Delete(dir)
	}
	return os.RemoveAll(dir)
}

// deleteSnapshots removes every snapshot below dir, then dir.
func deleteSnapshots(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := deleteTree(filepath.Join(dir, e.Name())); err != nil {
			return n, err
		}
		n++
	}
	return n, os.RemoveAll(dir)
}

func newInstanceCloneCmd() *cobra.Command {
	var configPath string
	var force bool
	c := &cobra.Command{
		Use:   "clone <old> <new>",
		Short: "Copy an instance's data into a new instance defined in the site repository",
		Long: "The new instance must exist in the site repository (its own ports, name and settings) and must have the same " +
			"map. Its directory is a copy of the old one made as a btrfs snapshot (a copy with reflinks elsewhere), " +
			"then rendered for the new instance, which replaces everything dzo generates (keys, config, RCon password). " +
			"A running source is refused unless --force: the copy is then crash-consistent. Nothing is started.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, from, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			_, to, err := loadInstance(configPath, args[1])
			if err != nil {
				return err
			}
			switch {
			case from.Name == to.Name:
				return errors.New("source and target are the same instance")
			case from.Map != to.Map:
				return fmt.Errorf("the maps differ (%s, %s): a clone keeps the map", from.Map, to.Map)
			case len(to.Missing) > 0:
				return fmt.Errorf("instance %s is not installed completely: %s", to.Name, to.Missing)
			}
			if _, err := os.Lstat(from.Paths.Root); err != nil {
				return fmt.Errorf("instance %s has no data directory", from.Name)
			}
			if _, err := os.Lstat(to.Paths.Root); err == nil {
				return fmt.Errorf("instance %s exists (%s)", to.Name, to.Paths.Root)
			}
			lc := instance.Lifecycle{UserMode: true}
			if active, _ := lc.IsActive(cmd.Context(), from.Name); active && !force {
				return fmt.Errorf("instance %s is running: stop it, or use --force for a crash-consistent copy", from.Name)
			}
			if sub, _ := btrfs.IsSubvolume(from.Paths.Root); sub {
				if err := btrfs.Snapshot(from.Paths.Root, to.Paths.Root, false); err != nil {
					return err
				}
			} else if out, err := exec.CommandContext(cmd.Context(), "cp", "-a", "--reflink=auto", from.Paths.Root, to.Paths.Root).CombinedOutput(); err != nil { //nolint:gosec // two paths from the config
				return fmt.Errorf("copy: %w\n%s", err, out)
			}
			// The new instance gets its own profiles, keys and passwords; it keeps the server build pin.
			_ = os.RemoveAll(filepath.Join(to.Paths.Root, "profiles"))
			pin, _ := os.ReadFile(filepath.Join(to.Paths.Root, "runtime", "build")) //nolint:gosec // inside the new instance
			_ = os.RemoveAll(filepath.Join(to.Paths.Root, "runtime"))
			if len(pin) > 0 {
				_ = os.MkdirAll(filepath.Join(to.Paths.Root, "runtime"), 0o750)
				if err := os.WriteFile(filepath.Join(to.Paths.Root, "runtime", "build"), pin, 0o600); err != nil { //nolint:gosec // inside the new instance, from the config
					return err
				}
			}
			if err := renderInstance(cmd, cfg, to, false, false); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "cloned %s to %s; next: dzo instance apply %s && dzo start %s\n", from.Name, to.Name, to.Name, to.Name)
			return nil
		},
	}
	configFlag(c, &configPath)
	c.Flags().BoolVar(&force, "force", false, "copy a running instance (crash-consistent)")
	return c
}

func newMissionStatusCmd() *cobra.Command {
	var configPath string
	c := &cobra.Command{
		Use:   "status <name>",
		Short: "Where the mission of an instance comes from, how old the pristine copy is, and what the next render would change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			src := inst.Mission.Source
			_, _ = fmt.Fprintf(out, "source: %s@%s %s\n", src.Git, src.Ref, src.Path)
			if fi, err := os.Stat(inst.Paths.Pristine); err == nil {
				_, _ = fmt.Fprintf(out, "pristine: %s (fetched %s)\n", inst.Paths.Pristine, fi.ModTime().Format("2006-01-02 15:04"))
			} else {
				_, _ = fmt.Fprintln(out, "pristine: not fetched yet")
			}
			if _, err := os.Stat(inst.Paths.Live); err != nil {
				_, _ = fmt.Fprintln(out, "live mission: not created (dzo instance create / dzo mission init)")
				return nil
			}
			if m, err := mission.LoadManifest(inst.Paths.Manifest); err == nil {
				_, _ = fmt.Fprintf(out, "live mission: %s, %d managed file(s)\n", inst.Paths.Live, len(m.Paths()))
			}
			_, _ = fmt.Fprintln(out, "next render:")
			return renderInstance(cmd, cfg, inst, true, false)
		},
	}
	configFlag(c, &configPath)
	return c
}
