// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/serve"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/bzed/dayz-server-operator/internal/version"
)

func newServeCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the JSON API (/api/v1) and the endpoint the dzo-admin mods call",
		Long: "Serves /api/v1 on serve.listen and /mod/v1/sync on serve.mod_listen. " +
			"SIGHUP re-reads the site config (new instances appear, live state is kept).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			reload, stopReload := reloadOnSIGHUP(ctx)
			defer stopReload()
			return serve.Run(ctx, cfg, serve.Options{
				Version: version.String(), Reload: reload,
				Load: func() (*site.Tree, error) { return site.LoadTree(cfg.Paths.Site) },
				Ready: func(apiAddr, modAddr string) {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "api on %s, mod endpoint on %s\n", apiAddr, modAddr)
				},
				Logf:   func(f string, a ...any) { _, _ = fmt.Fprintf(cmd.ErrOrStderr(), f+"\n", a...) },
				Notify: newNotifier(cfg), Lookups: true,
			})
		},
	}
	configFlag(cmd, &configPath)
	return cmd
}

func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "token", Short: "Manage API tokens (bots, scripts, the web interface)"}
	var configPath string
	store := func() (*api.TokenStore, error) {
		cfg, err := config.Load(configPath)
		if err != nil {
			return nil, err
		}
		return api.NewTokenStore(serve.APITokens(cfg)), nil
	}

	var role string
	var instances []string
	var delegate bool
	var ttl time.Duration
	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a token; the secret is printed once",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store()
			if err != nil {
				return err
			}
			secret, tok, err := s.Create(args[0], api.Role(role), instances, delegate, ttl)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "token %s (%s) expires %s\n", tok.ID, tok.Role, tok.Expires.Format(time.RFC3339))
			_, err = fmt.Fprintln(cmd.OutOrStdout(), secret)
			return err
		},
	}
	create.Flags().StringVar(&role, "role", "viewer", "detector, viewer, moderator, operator or admin")
	create.Flags().StringSliceVar(&instances, "instance", nil, "limit the token to these instances (default: all)")
	create.Flags().BoolVar(&delegate, "delegate", false, "may name the acting user (for the web interface)")
	create.Flags().DurationVar(&ttl, "ttl", 365*24*time.Hour, "lifetime (at most one year)")

	list := &cobra.Command{
		Use:   "list",
		Short: "List tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store()
			if err != nil {
				return err
			}
			toks, err := s.List()
			if err != nil {
				return err
			}
			for _, t := range toks {
				scope := "all instances"
				if len(t.Instances) > 0 {
					scope = fmt.Sprint(t.Instances)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %-16s %-9s delegate=%-5t %s  expires %s\n", t.ID, t.Name, t.Role, t.Delegate, scope, t.Expires.Format("2006-01-02"))
			}
			return nil
		},
	}
	revoke := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Delete a token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store()
			if err != nil {
				return err
			}
			return s.Revoke(args[0])
		},
	}
	cmd.PersistentFlags().StringVar(&configPath, "config", defaultConfigPath, "path to config.yaml")
	cmd.AddCommand(create, list, revoke)
	return cmd
}

// ctxOf is the command's context, never nil.
func ctxOf(cmd *cobra.Command) context.Context {
	if cmd.Context() != nil {
		return cmd.Context()
	}
	return context.Background()
}
