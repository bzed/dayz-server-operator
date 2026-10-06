// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"github.com/spf13/cobra"
)

// newProductCmd groups the manual server-build downloads (§C7, D7).
func newProductCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "product",
		Short: "Manual server build downloads (§C7); never automatic (D7)",
	}
	cmd.AddCommand(newProductInstallCmd(), newProductUpdateCmd())
	return cmd
}

func newModCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mod",
		Short: "Workshop mod downloads and dependency checks (§C7)",
	}
	cmd.AddCommand(newModCfgPatchesCmd(), newModDepsCmd(),
		newModListCmd(), newModAddCmd(), newModUpdateCmd(), newModRefreshCmd(), newModRemoveCmd())
	return cmd
}
