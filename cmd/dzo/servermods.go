// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/servermods"
	"github.com/bzed/dayz-server-operator/internal/version"
)

// newServermodsCmd is the build-time tool behind `make servermods`.
func newServermodsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "servermods", Short: "Build dzo's own servermods (development and packaging)"}
	var src, out, serverBuild, lock string
	var updateLock bool
	build := &cobra.Command{
		Use:   "build",
		Short: "Pack servermods/*/src/* into PBOs, reproducibly",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := servermods.Build(ctxOf(cmd), src, out)
			if err != nil {
				return err
			}
			for _, r := range res {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  (prefix %s)\n", r.SHA256, r.PBO, r.Prefix)
			}
			if lock != "" {
				built, err := servermods.NewLock(ctxOf(cmd), src, out, res)
				if err != nil {
					return err
				}
				if updateLock {
					if err := built.Write(lock); err != nil {
						return err
					}
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", lock)
				} else {
					pinned, err := servermods.ReadLock(lock)
					if err != nil {
						return err
					}
					if err := pinned.Check(built); err != nil {
						return err
					}
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s matches the build\n", lock)
				}
			}
			return servermods.WriteCompat(ctxOf(cmd), src, out, version.Version, serverBuild, res)
		},
	}
	build.Flags().StringVar(&src, "src", "servermods", "directory holding the servermod checkouts")
	build.Flags().StringVar(&out, "out", "dist/servermods", "output directory")
	build.Flags().StringVar(&serverBuild, "server-build", "", "DayZServer build the set was boot-tested on, recorded in compat.yaml")
	build.Flags().StringVar(&lock, "lock", "", "servermods.lock: fail when the build differs from it")
	build.Flags().BoolVar(&updateLock, "update-lock", false, "write the --lock file from this build instead of checking it")
	cmd.AddCommand(build)
	return cmd
}
