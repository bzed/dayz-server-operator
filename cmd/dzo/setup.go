// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/setup"
)

func newSetupCmd() *cobra.Command {
	var configPath, imagesDir, unitDir string
	var dryRun, imagesOnly bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "First-time setup as the service user: data directories, container images, site repo, image refresh timer",
		Long: "Run as the service user (sudo -iu dayz dzo setup) after installing the package. It creates and checks the " +
			"data directories, builds the two container images from the package's Containerfiles, reports what is left " +
			"for the Steam login, clones the site repo and enables a weekly timer that rebuilds the images. " +
			"It is safe to run again; --dry-run only reports what it would do.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			opts := setup.Options{
				Cfg: cfg, ImagesDir: imagesDir, UnitDir: unitDir, DryRun: dryRun, ImagesOnly: imagesOnly, Out: cmd.OutOrStdout(),
			}
			if configPath != defaultConfigPath {
				opts.ConfigPath = configPath
			}
			return setup.Run(ctxOf(cmd), opts)
		},
	}
	configFlag(cmd, &configPath)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "only report what would be done")
	cmd.Flags().BoolVar(&imagesOnly, "images-only", false, "only rebuild the images (pulling the base image again) and prune the old ones; the weekly timer runs this")
	cmd.Flags().StringVar(&imagesDir, "images-dir", setup.DefaultImagesDir, "directory with the images' Containerfiles")
	cmd.Flags().StringVar(&unitDir, "unit-dir", "", "systemd user unit directory for the refresh timer (default ~/.config/systemd/user)")
	return cmd
}
