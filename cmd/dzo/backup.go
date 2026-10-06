// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/backup"
	"github.com/bzed/dayz-server-operator/internal/instance"
)

// backupFor resolves an instance and checks that it can be backed up.
func backupFor(configPath, name string) (*backup.Manager, error) {
	cfg, inst, err := loadInstance(configPath, name)
	if err != nil {
		return nil, err
	}
	if err := backup.Check(cfg, inst); err != nil {
		return nil, err
	}
	return backup.For(cfg, inst), nil
}

func printWarnings(w io.Writer, ws []string) {
	for _, x := range ws {
		_, _ = fmt.Fprintf(w, "warning: %s\n", x)
	}
}

func newBackupCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "backup", Short: "Snapshots of an instance: create, list, prune, pin, compare (§C20)"}
	cmd.AddCommand(newBackupCreateCmd(), newBackupListCmd(), newBackupPruneCmd(), newBackupPinCmd(true), newBackupPinCmd(false), newBackupDiffCmd())
	return cmd
}

func newBackupCreateCmd() *cobra.Command {
	var configPath, reason string
	cmd := &cobra.Command{
		Use:   "create <instance>",
		Short: "Take a read-only snapshot of an instance now",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := backupFor(configPath, args[0])
			if err != nil {
				return err
			}
			res, err := m.Create(cmd.Context(), backup.Reason(reason))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(out, res.Snapshot.ID)
			for _, p := range res.Pruned {
				_, _ = fmt.Fprintf(out, "pruned %s\n", p.ID)
			}
			printWarnings(cmd.ErrOrStderr(), res.Warnings)
			return nil
		},
	}
	configFlag(cmd, &configPath)
	cmd.Flags().StringVar(&reason, "reason", string(backup.Manual), "why: "+reasonList())
	return cmd
}

func reasonList() string {
	var rs []string
	for _, r := range backup.Reasons {
		rs = append(rs, string(r))
	}
	return strings.Join(rs, ", ")
}

func newBackupListCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "list [instance]",
		Short: "List the snapshots of an instance (or of every instance)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, tree, err := loadSite(configPath)
			if err != nil {
				return err
			}
			names := tree.InstanceNames()
			if len(args) == 1 {
				names = args
			}
			out := cmd.OutOrStdout()
			for _, n := range names {
				_, inst, err := loadInstance(configPath, n)
				if err != nil {
					return err
				}
				ss, err := backup.For(cfg, inst).List()
				if err != nil {
					return err
				}
				for _, s := range ss {
					pin := ""
					if s.Pinned {
						pin = " pinned"
					}
					_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s%s\n", n, s.ID, s.Reason, s.State, s.Created.Format("2006-01-02 15:04"), pin)
				}
			}
			return nil
		},
	}
	configFlag(cmd, &configPath)
	return cmd
}

func newBackupPruneCmd() *cobra.Command {
	var configPath string
	var dry, adopt, deleteOrphans bool
	cmd := &cobra.Command{
		Use:   "prune [instance]",
		Short: "Apply the retention policy (--dry-run: only say what would go)",
		Long: "Deletes the snapshots the retention policy no longer keeps. Only snapshots in dzo's index are touched; " +
			"directories in the snapshot folder that dzo does not know are listed, and deleted or adopted only with " +
			"--delete-orphans or --adopt-orphans.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, tree, err := loadSite(configPath)
			if err != nil {
				return err
			}
			names := tree.InstanceNames()
			if len(args) == 1 {
				names = args
			}
			out := cmd.OutOrStdout()
			var failed []string
			for _, n := range names {
				m, err := backupFor(configPath, n)
				if err != nil {
					if len(args) == 0 {
						// the timer prunes every instance: one that cannot be backed up (not on btrfs, created
						// before it was a subvolume) has no snapshots to prune and must not fail the run
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "skipping %s: %v\n", n, err)
						continue
					}
					failed = append(failed, err.Error())
					continue
				}
				del, err := m.Prune(dry)
				verb := "pruned"
				if dry {
					verb = "would prune"
				}
				for _, s := range del {
					_, _ = fmt.Fprintf(out, "%s: %s %s (%s)\n", n, verb, s.ID, s.Reason)
				}
				if err != nil {
					failed = append(failed, fmt.Sprintf("%s: %v", n, err))
				}
				orph, oerr := m.Orphans()
				if oerr != nil {
					failed = append(failed, oerr.Error())
					continue
				}
				for _, o := range orph {
					_, _ = fmt.Fprintf(out, "%s: unknown directory %s in the snapshot folder\n", n, o)
				}
				switch {
				case deleteOrphans && len(orph) > 0 && !dry:
					err = m.DeleteOrphans(orph)
				case adopt && len(orph) > 0 && !dry:
					err = m.AdoptOrphans(orph)
				}
				if err != nil {
					failed = append(failed, fmt.Sprintf("%s: %v", n, err))
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("%s", strings.Join(failed, "; "))
			}
			return nil
		},
	}
	configFlag(cmd, &configPath)
	cmd.Flags().BoolVar(&dry, "dry-run", false, "only say what would be pruned")
	cmd.Flags().BoolVar(&adopt, "adopt-orphans", false, "enter unknown directories into the index")
	cmd.Flags().BoolVar(&deleteOrphans, "delete-orphans", false, "delete unknown directories in the snapshot folder")
	return cmd
}

func newBackupPinCmd(pin bool) *cobra.Command {
	var configPath string
	use, short := "pin <instance> <id>", "Protect a snapshot from automatic pruning"
	if !pin {
		use, short = "unpin <instance> <id>", "Allow a snapshot to be pruned again"
	}
	cmd := &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := backupFor(configPath, args[0])
			if err != nil {
				return err
			}
			return m.Pin(args[1], pin)
		},
	}
	configFlag(cmd, &configPath)
	return cmd
}

func newBackupDiffCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "diff <instance> <id> [<id2>|live]",
		Short: "Show the files that differ between a snapshot and another snapshot or the live instance",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := backupFor(configPath, args[0])
			if err != nil {
				return err
			}
			other := "live"
			if len(args) == 3 {
				other = args[2]
			}
			changes, err := m.DiffWith(args[1], other)
			if err != nil {
				return err
			}
			for _, c := range changes {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", c.Kind, c.Path)
			}
			return nil
		},
	}
	configFlag(cmd, &configPath)
	return cmd
}

func newRestoreCmd() *cobra.Command {
	var configPath, path string
	var lc instance.Lifecycle
	cmd := &cobra.Command{
		Use:   "restore <instance> <id> [--path <path below the instance>]",
		Short: "Put a snapshot back: the whole instance, or one path of it",
		Long: "Stops the instance if it runs, takes a pre_restore snapshot, and restores. A full restore moves the " +
			"live subvolume aside (it is kept as a snapshot of reason \"replaced\"), makes a writable snapshot of the " +
			"backup at the instance path and starts the instance again. With --path only that path is replaced, " +
			"for example mpmissions/<map>/storage_1.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := backupFor(configPath, args[0])
			if err != nil {
				return err
			}
			wasActive := false
			o := backup.RestoreOptions{
				Path: path,
				Stop: func(ctx context.Context) error {
					wasActive, _ = lc.IsActive(ctx, args[0])
					if !wasActive {
						return nil
					}
					return lc.Stop(ctx, args[0])
				},
				Start: func(ctx context.Context) error {
					if !wasActive {
						return nil // it was stopped: leave it stopped
					}
					return lc.Start(ctx, args[0])
				},
			}
			res, err := m.Restore(cmd.Context(), args[1], o)
			printWarnings(cmd.ErrOrStderr(), res.Warnings)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "restored %s of %s; the state before is in %s\n", args[1], args[0], res.Snapshot.ID)
			return nil
		},
	}
	configFlag(cmd, &configPath)
	lifecycleFlags(cmd, &lc)
	cmd.Flags().StringVar(&path, "path", "", "restore only this path below the instance")
	return cmd
}
