// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/legacy"
	"github.com/bzed/dayz-server-operator/internal/site"
)

func newLegacyCmd() *cobra.Command {
	c := &cobra.Command{Use: "legacy", Short: "Help to leave the legacy dayzdockerserver behind"}
	c.AddCommand(newLegacyConvertCmd())
	return c
}

func newLegacyConvertCmd() *cobra.Command {
	var repo, out, image string
	var refs []string
	var dry bool
	var offset int
	c := &cobra.Command{
		Use:   "convert-config --repo <dayzdockerserver> --ref <branch>... --out <site dir>",
		Short: "Make an example site configuration from the branches of the legacy repository",
		Long: "Reads the branches with git (no checkout, no volumes, nothing from a running server) and writes a site " +
			"tree: instances/<branch>/ with instance.yaml and serverDZ.cfg, integrations/mods/<id>/ from xml.env, map.env and " +
			"start.sh, overlays/ from files/custom, and CONVERSION_REPORT.md with what needs a decision. What is the same " +
			"on every branch is shared, what differs becomes an override of the instance. Run it again after a change: " +
			"files are rewritten, nothing is deleted. The result is an example to review, not a finished site.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if repo == "" || len(refs) == 0 || out == "" {
				return fmt.Errorf("--repo, --ref and --out are required")
			}
			res, err := legacy.Convert(legacy.Git{Repo: repo}, legacy.Options{Refs: refs, PortOffset: offset, Image: image})
			if err != nil {
				return err
			}
			res.Files["CONVERSION_REPORT.md"] = []byte(res.Report)
			names := make([]string, 0, len(res.Files))
			for n := range res.Files {
				names = append(names, n)
			}
			sort.Strings(names)
			w := cmd.OutOrStdout()
			changed := 0
			for _, n := range names {
				p := filepath.Join(out, filepath.FromSlash(n))
				if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, res.Files[n]) { //nolint:gosec // inside --out
					continue
				}
				changed++
				if dry {
					_, _ = fmt.Fprintf(w, "would write %s\n", n)
					continue
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
					return err
				}
				if err := os.WriteFile(p, res.Files[n], 0o644); err != nil { //nolint:gosec // an example site, meant to be committed
					return err
				}
				_, _ = fmt.Fprintf(w, "wrote %s\n", n)
			}
			_, _ = fmt.Fprintf(w, "%d of %d file(s) %s\n", changed, len(names), map[bool]string{true: "would change", false: "written"}[dry])
			if dry {
				return nil
			}
			if _, err := site.LoadTree(out); err != nil {
				return fmt.Errorf("the converted site does not validate: %w", err)
			}
			_, _ = fmt.Fprintln(w, "the site validates; read CONVERSION_REPORT.md next")
			return nil
		},
	}
	c.Flags().StringVar(&repo, "repo", "", "the legacy repository (a git checkout with the server branches)")
	c.Flags().StringArrayVar(&refs, "ref", nil, "a branch to convert (repeatable); the instance is named like it")
	c.Flags().StringVar(&out, "out", "", "the site directory to write")
	c.Flags().BoolVar(&dry, "dry-run", false, "only list the files that would change")
	c.Flags().IntVar(&offset, "port-offset", 0, "add this to every port, to run next to the legacy servers")
	c.Flags().StringVar(&image, "image", "", "runtime image for site.yaml (default localhost/dzo-runtime:latest)")
	return c
}
