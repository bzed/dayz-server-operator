// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/version"
)

// defaultConfigPath is where the Debian package installs config.yaml (§C2).
const defaultConfigPath = "/etc/dzo/config.yaml"

// allowRootEnv set to 1 lets dzo run as root: for the package build, which runs the tests as root.
const allowRootEnv = "DZO_ALLOW_ROOT"

// refuseRoot is the guard of every command. What root creates (cache generations, instance files,
// profiles) is owned by root, and the game servers run as the service user: a mod directory they
// cannot open is skipped without a word, so a mod just does not load. Only commands that run as
// uid 0 by design are let through: the container's health probes, the build steps of the package
// and the commands that only print.
func refuseRoot(cmd *cobra.Command, euid int, override string) error {
	if euid != 0 || override == "1" {
		return nil
	}
	top := cmd
	for top.Parent() != nil && top.Parent().Parent() != nil {
		top = top.Parent()
	}
	switch top.Name() {
	case "dzo", "version", "help", "completion", "__complete", "health", "gen-man", "servermods":
		return nil
	}
	return errors.New("dzo must not run as root: run it as the service user (sudo -iu dayz dzo ...). " +
		"What root creates is owned by root and the game servers, which run as the service user, cannot read it")
}

// newRootCmd builds the dzo command tree. Kept thin on purpose (C15): every
// subcommand delegates to an internal/ package that owns the real logic and
// its own tests, so cmd/ wiring doesn't dilute the coverage gate.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "dzo",
		Short:         "dzo manages DayZ dedicated server instances",
		SilenceUsage:  true,
		SilenceErrors: false,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return refuseRoot(cmd, os.Geteuid(), os.Getenv(allowRootEnv))
		},
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
		newPlayerCmd(), newBanCmd(), newEvidenceCmd(), newEventsCmd(),
		newVehicleCmd(),
		newFlagCmd(),
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
		newWipeCmd(),
		newShellCmd(),
		newExecCmd(),
		newManCmd(),
		newLegacyCmd(),
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
