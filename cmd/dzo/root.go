// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/version"
)

// defaultConfigPath is where the Debian package installs config.yaml (§C2).
const defaultConfigPath = "/etc/dzo/config.yaml"

// newRootCmd builds the dzo command tree. Kept thin on purpose (C15): every
// subcommand delegates to an internal/ package that owns the real logic and
// its own tests, so cmd/ wiring doesn't dilute the coverage gate.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "dzo",
		Short:         "dzo manages DayZ dedicated server instances",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.AddCommand(
		newVersionCmd(),
		newConfigCmd(),
		newServerCfgCmd(),
		newRconCmd(),
		newCacheCmd(),
		newHealthCmd(),
		newNotifyCmd(),
		newMissionCmd(),
		newCheckCmd(),
		newSteamCmd(),
		newProductCmd(),
		newModCmd(),
		newInstanceCmd(),
		newServeCmd(),
		newWebCmd(),
		newTokenCmd(),
		newPlayerCmd(),
		newVehicleCmd(),
		newServermodsCmd(),
		newMapCmd(),
		newSetupCmd(),
		newTestCmd(),
		newBackupCmd(),
		newExporterCmd(),
		newUpdateCmd(),
		newRestartCmd(),
		newUnitsCmd(),
		newStatusCmd(),
		newRestoreCmd(),
		newStartCmd(),
		newStopCmd(),
		newLogsCmd(),
		newSiteCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the dzo version",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), version.String())
			return nil
		},
	}
}
