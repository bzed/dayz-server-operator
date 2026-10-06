// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/mission"
)

func newMissionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mission",
		Short: "Live mission render/apply pipeline primitives (§C6)",
	}
	cmd.AddCommand(newMissionDiffCmd(), newMissionApplyCmd(), newMissionUpdateCmd(), newMissionInitCmd(), newMissionRollbackCmd(), newMissionReinitCmd())
	return cmd
}

func splitCommaList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func printPlan(cmd *cobra.Command, plan mission.Plan) {
	out := cmd.OutOrStdout()
	symbol := map[mission.Action]string{
		mission.ActionNew:       "+",
		mission.ActionUpdate:    "~",
		mission.ActionDrift:     "!",
		mission.ActionObsolete:  "-",
		mission.ActionUnchanged: " ",
	}
	for _, e := range plan.Entries {
		if e.Action == mission.ActionUnchanged {
			continue
		}
		_, _ = fmt.Fprintf(out, "%s %s (%s)\n", symbol[e.Action], e.Path, e.Action)
	}
	if plan.Empty() {
		_, _ = fmt.Fprintln(out, "no changes")
	}
}

func printReport(cmd *cobra.Command, report mission.Report) {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "written: %d, drifted: %d, obsolete: %d\n", len(report.Written), len(report.Drifted), len(report.Obsolete))
	for _, e := range report.Drifted {
		_, _ = fmt.Fprintf(out, "warning: drift on %s (backed up before overwrite)\n", e.Path)
	}
}

func newMissionDiffCmd() *cobra.Command {
	var manifestPath, unmanaged string
	cmd := &cobra.Command{
		Use:   "diff <staging-dir> <live-dir>",
		Short: "Show the apply plan without touching the live mission (dry-run)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := mission.LoadManifest(manifestPath)
			if err != nil {
				return err
			}
			plan, err := mission.Classify(args[0], args[1], m, splitCommaList(unmanaged))
			if err != nil {
				return err
			}
			printPlan(cmd, plan)
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "path to .dzo-manifest.json (required)")
	cmd.Flags().StringVar(&unmanaged, "unmanaged", "", "comma-separated unmanaged glob patterns")
	_ = cmd.MarkFlagRequired("manifest")
	return cmd
}

func newMissionApplyCmd() *cobra.Command {
	var manifestPath, unmanaged, filehistoryDir string
	var keepHistory int
	cmd := &cobra.Command{
		Use:   "apply <staging-dir> <live-dir>",
		Short: "Classify and apply staging onto the live mission, in place",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := mission.LoadManifest(manifestPath)
			if err != nil {
				return err
			}
			plan, err := mission.Classify(args[0], args[1], m, splitCommaList(unmanaged))
			if err != nil {
				return err
			}
			report, err := mission.Apply(plan, args[0], args[1], filehistoryDir, m, mission.Options{KeepFileHistory: keepHistory})
			if err != nil {
				return err
			}
			if err := m.Save(manifestPath); err != nil {
				return err
			}
			printReport(cmd, report)
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "path to .dzo-manifest.json (required)")
	cmd.Flags().StringVar(&unmanaged, "unmanaged", "", "comma-separated unmanaged glob patterns")
	cmd.Flags().StringVar(&filehistoryDir, "filehistory", "", "directory for pre-overwrite file backups (required)")
	cmd.Flags().IntVar(&keepHistory, "keep-history", mission.DefaultKeepFileHistory, "number of filehistory generations to keep")
	_ = cmd.MarkFlagRequired("manifest")
	_ = cmd.MarkFlagRequired("filehistory")
	return cmd
}
