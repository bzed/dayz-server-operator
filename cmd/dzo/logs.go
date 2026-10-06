// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/logs"
	"github.com/bzed/dayz-server-operator/internal/notify"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

func logArchive(cfg *config.Config, name string) string { return filepath.Join(cfg.Paths.Logs, name) }

func newLogsRotateCmd() *cobra.Command {
	var configPath string
	var dry, all bool
	c := &cobra.Command{
		Use:   "rotate [<name> | --all]",
		Short: "Move old profile logs into the log archive and apply the archive's retention",
		Long: "Per rule of logs.rotate, the newest files stay in the profiles directory and every older match is " +
			"archived (gzip) below paths.logs/<name>/<date>/. Files younger than min_age and files dzo or the game " +
			"need are never touched. It runs before every start and hourly (dzo-logs.timer) for all instances; " +
			"--dry-run lists what it would move and the large files no rule matches.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) == 1) {
				return fmt.Errorf("give an instance name or --all")
			}
			cfg, tree, err := loadSite(configPath)
			if err != nil {
				return err
			}
			names := args
			if all {
				for n := range tree.Instances {
					names = append(names, n)
				}
				sort.Strings(names)
			}
			var failed int
			for _, n := range names {
				inst, err := resolve.Resolve(cfg, tree, n)
				if err != nil {
					return err
				}
				if err := rotateInstance(cmd, cfg, inst, dry); err != nil {
					failed++
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", n, err)
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d instance(s) failed", failed)
			}
			return nil
		},
	}
	configFlag(c, &configPath)
	c.Flags().BoolVar(&dry, "dry-run", false, "only list what would be moved")
	c.Flags().BoolVar(&all, "all", false, "every instance")
	return c
}

func rotateInstance(cmd *cobra.Command, cfg *config.Config, inst *resolve.Instance, dry bool) error {
	out := cmd.OutOrStdout()
	archive := logArchive(cfg, inst.Name)
	now := time.Now()
	if dry {
		p, err := logs.MakePlan(inst.Paths.Profiles, inst.Logs, now)
		if err != nil {
			return err
		}
		for _, a := range p.Move {
			_, _ = fmt.Fprintf(out, "%s: move %s (%d bytes, rule %s)\n", inst.Name, a.Rel, a.Size, a.Rule)
		}
		for _, r := range p.Excluded {
			_, _ = fmt.Fprintf(out, "%s: %s matches a rule but is never moved\n", inst.Name, r)
		}
		for _, a := range p.Unmatched {
			_, _ = fmt.Fprintf(out, "%s: %s (%d bytes) is matched by no rule\n", inst.Name, a.Rel, a.Size)
		}
		return nil
	}
	moved, err := logs.Rotate(inst.Paths.Profiles, archive, inst.Logs, now)
	if err != nil {
		return err
	}
	pruned, err := logs.Prune(archive, inst.Logs, now)
	if err != nil {
		return err
	}
	if len(moved)+pruned > 0 {
		_, _ = fmt.Fprintf(out, "%s: archived %d log file(s), removed %d from the archive\n", inst.Name, len(moved), pruned)
	}
	return nil
}

func newLogsArchiveCmd() *cobra.Command {
	var configPath, since, match string
	c := &cobra.Command{
		Use:   "archive <name> [--since 7d] [--match <regexp>]",
		Short: "List the archived profile logs of an instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			var from time.Time
			if since != "" {
				var a site.Age
				if err := a.UnmarshalYAML(func(v any) error { *(v.(*string)) = since; return nil }); err != nil {
					return err
				}
				from = time.Now().Add(-a.Std())
			}
			var re *regexp.Regexp
			if match != "" {
				if re, err = regexp.Compile(match); err != nil {
					return err
				}
			}
			es, err := logs.List(logArchive(cfg, args[0]), from, re)
			if err != nil {
				return err
			}
			for _, e := range es {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %10d  %s\n", e.Mod.Format("2006-01-02 15:04"), e.Size, e.Path)
			}
			return nil
		},
	}
	configFlag(c, &configPath)
	c.Flags().StringVar(&since, "since", "", "only files archived within this age (7d, 12h)")
	c.Flags().StringVar(&match, "match", "", "only paths matching this regular expression")
	return c
}

func newLogsCrashSummaryCmd() *cobra.Command {
	var configPath string
	var lines int
	var force bool
	c := &cobra.Command{
		Use:   "crash-summary <name>",
		Short: "After an unclean exit: print the tails of the newest server logs and tell Discord",
		Long: "Runs as ExecStopPost of the unit. systemd tells it how the service ended in $SERVICE_RESULT; " +
			"after a clean stop it does nothing (--force prints anyway).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result := os.Getenv("SERVICE_RESULT")
			if !force && (result == "" || result == "success") {
				return nil
			}
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			summary := logs.CrashSummary(inst.Paths.Profiles, lines)
			if summary == "" {
				summary = "(no logs found)"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "crash summary of %s (service result %q):\n%s", inst.Name, result, summary)
			if n := newNotifier(cfg); n != nil {
				if len(summary) > 1200 {
					summary = summary[len(summary)-1200:]
				}
				_ = n(cmd.Context(), notify.Event{Kind: notify.KindCrash, Instance: inst.Name, Targets: inst.Notify.Discord,
					Data: map[string]any{"Result": result, "Summary": summary}})
			}
			return nil
		},
	}
	configFlag(c, &configPath)
	c.Flags().IntVar(&lines, "lines", 25, "lines per log file")
	c.Flags().BoolVar(&force, "force", false, "print even if the service ended cleanly")
	return c
}
