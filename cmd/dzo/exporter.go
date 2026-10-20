// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/exporter"
	"github.com/bzed/dayz-server-operator/internal/monitor"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/bzed/dayz-server-operator/internal/version"
)

func newCollector(cfg *config.Config) *exporter.Collector {
	return &exporter.Collector{
		Cfg: cfg, Version: version.Version, BootUptime: exporter.ProcUptime,
		Load:    func() (*site.Tree, error) { return site.LoadTree(cfg.Paths.Site) },
		Updates: updateInfo(cfg),
	}
}

func newExporterCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "exporter",
		Short: "Serve /metrics (Prometheus) and /status (Icinga) for this installation",
		Long: "Serves GET /metrics and GET /status[/<instance>] on exporter.listen (default :9464), over plain HTTP or, " +
			"with exporter.tls, TLS (client certificates with client_ca_file). A bearer token and an IP allow-list are " +
			"optional. SIGHUP (systemctl --user reload dzo-exporter) re-reads the certificate; changed certificate " +
			"files are also picked up by themselves. Game servers are never involved in a certificate change.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			col := newCollector(cfg)
			col.Start(ctx)
			reload, stopReload := reloadOnSIGHUP(ctx)
			defer stopReload()
			errw := cmd.ErrOrStderr()
			return exporter.Serve(ctx, cfg.Exporter, monitor.Handler(col), reload,
				func(addr string) { _, _ = fmt.Fprintf(errw, "exporter on %s\n", addr) },
				func(f string, a ...any) { _, _ = fmt.Fprintf(errw, f+"\n", a...) })
		},
	}
	configFlag(cmd, &configPath)
	return cmd
}

func newStatusCmd() *cobra.Command {
	var configPath, write string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status [instance]",
		Short: "Show what the exporter would report: instance state, players, health, restarts",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			col := newCollector(cfg)
			col.Refresh(context.Background())
			snap, _ := col.Snapshot()
			if write != "" {
				if err := writeStatusFiles(write, snap); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			if len(args) == 1 {
				i, ok := snap.Find(args[0])
				if !ok {
					return fmt.Errorf("no instance %q", args[0])
				}
				if asJSON {
					return json.NewEncoder(out).Encode(i)
				}
				_, _ = fmt.Fprintf(out, "%s: %s\n", i.Name, i.Message)
				return nil
			}
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(snap)
			}
			for _, i := range snap.Instances {
				_, _ = fmt.Fprintf(out, "%s: %s\n", i.Name, i.Message)
			}
			return nil
		},
	}
	configFlag(cmd, &configPath)
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.Flags().StringVar(&write, "write", "", "also write <dir>/<instance>.json and <dir>/global.json (the local status snapshot, /run/dzo/status)")
	return cmd
}

// writeStatusFiles writes the local status snapshot: one JSON file per
// instance and one for the host, each replaced atomically.
func writeStatusFiles(dir string, snap monitor.Snapshot) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	put := func(name string, v any) error {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(dir, ".status-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name()) //nolint:errcheck // gone after the rename
		if _, err := tmp.Write(append(b, '\n')); err != nil {
			_ = tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := os.Chmod(tmp.Name(), 0o644); err != nil { //nolint:gosec // a status file for local readers
			return err
		}
		return os.Rename(tmp.Name(), filepath.Join(dir, name))
	}
	if err := put("global.json", snap.Global); err != nil {
		return err
	}
	for _, i := range snap.Instances {
		if err := put(i.Name+".json", i); err != nil {
			return err
		}
	}
	return nil
}
