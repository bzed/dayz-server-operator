// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/moddeps"
)

// loadPatches reads path as a config.cpp source or a rapified config.bin,
// or - if it ends in ".pbo" - opens it as a PBO archive and reads its
// config.bin (falling back to config.cpp).
func loadPatches(path string) ([]moddeps.Patch, error) {
	if !strings.HasSuffix(path, ".pbo") {
		data, err := os.ReadFile(path) //nolint:gosec // path is an operator-supplied CLI argument, not external input
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		return moddeps.ExtractPatchesFromConfig(data)
	}
	f, err := os.Open(path) //nolint:gosec // path is an operator-supplied CLI argument, not external input
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	pbo, err := moddeps.OpenPBOFile(f)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return moddeps.ExtractPatchesFromPBO(pbo)
}

func newModCfgPatchesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cfgpatches <path> [path...]",
		Short: "Print each file's CfgPatches classes and requiredAddons (diagnostic, §C7)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, path := range args {
				patches, err := loadPatches(path)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s:\n", path)
				for _, p := range patches {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s requires %v\n", p.Name, p.RequiredAddons)
				}
			}
			return nil
		},
	}
}

func newModDepsCmd() *cobra.Command {
	var provides []string
	var showOrder bool
	cmd := &cobra.Command{
		Use:   "deps <path> [path...]",
		Short: "Validate CfgPatches requiredAddons across mod files and print the load order (FR-06a)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mods := make([]moddeps.Mod, 0, len(args)+1)
			if len(provides) > 0 {
				baseline := make([]moddeps.Patch, len(provides))
				for i, name := range provides {
					baseline[i] = moddeps.Patch{Name: name}
				}
				mods = append(mods, moddeps.Mod{ID: 0, Patches: baseline})
			}
			for i, path := range args {
				patches, err := loadPatches(path)
				if err != nil {
					return err
				}
				mods = append(mods, moddeps.Mod{ID: uint64(i + 1), Patches: patches}) //nolint:gosec // i+1 is bounded by len(args), never near uint64 overflow
			}

			result := moddeps.Validate(mods)
			for _, m := range result.Missing {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), m.String())
			}
			if !result.OK() {
				return fmt.Errorf("moddeps: %d missing dependencies", len(result.Missing))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "all requiredAddons entries are satisfied")

			if showOrder {
				sorted, err := moddeps.TopoSort(mods)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "load order (index into the path arguments, 0 = --provides baseline):")
				for _, m := range sorted {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %d\n", m.ID)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&provides, "provides", nil, "CfgPatches class names already provided (e.g. by the product build), comma-separated")
	cmd.Flags().BoolVar(&showOrder, "order", false, "also print a dependency-sorted load order")
	return cmd
}
