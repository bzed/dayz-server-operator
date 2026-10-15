// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/config"
)

// apiTarget are the flags every API-backed command shares: which
// installation to talk to and with which token.
type apiTarget struct {
	url, tokenFile, configPath string
}

func (a *apiTarget) flags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&a.url, "api", "", "API base URL (default: http://<serve.listen> from config.yaml)")
	cmd.PersistentFlags().StringVar(&a.tokenFile, "token-file", "", "file with the API token (default: $DZO_TOKEN)")
	cmd.PersistentFlags().StringVar(&a.configPath, "config", defaultConfigPath, "path to config.yaml")
}

func (a *apiTarget) client() (*apiclient.Client, error) {
	base := a.url
	if base == "" {
		cfg, err := config.Load(a.configPath)
		if err != nil {
			return nil, err
		}
		base = "http://" + cfg.Serve.Listen
	}
	token := strings.TrimSpace(os.Getenv("DZO_TOKEN"))
	if a.tokenFile != "" {
		b, err := os.ReadFile(a.tokenFile)
		if err != nil {
			return nil, err
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		return nil, fmt.Errorf("no API token: use --token-file or set DZO_TOKEN (create one with `dzo token create`)")
	}
	return apiclient.New(base, token).WithActor(os.Getenv("USER"), "cli"), nil
}

func printResult(cmd *cobra.Command, r api.ActionResult) error {
	switch {
	case r.Pending:
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "queued %s\n", r.ID)
	case r.OK:
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "ok")
	default:
		return fmt.Errorf("the game server refused: %s", r.Message)
	}
	return nil
}

func newPlayerCmd() *cobra.Command {
	var t apiTarget
	cmd := &cobra.Command{Use: "player", Short: "List players and act on them through dzo-admin (needs `dzo serve`)"}
	t.flags(cmd)

	cmd.AddCommand(&cobra.Command{
		Use: "list <instance>", Short: "List online players", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			ps, err := c.Players(ctxOf(cmd), args[0])
			if err != nil {
				return err
			}
			for _, p := range ps {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %-24s %6.0f %6.0f  ping %d\n", p.SteamID, p.Name, p.X, p.Z, p.Ping)
			}
			return nil
		},
	})

	var style string
	msg := &cobra.Command{
		Use: "msg <instance> <steamid|all> <text>", Short: "Send a message to a player, or to everyone", Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			id := args[1]
			if id == "all" {
				id = ""
			}
			r, err := c.Message(ctxOf(cmd), args[0], id, strings.Join(args[2:], " "), style)
			if err != nil {
				return err
			}
			return printResult(cmd, r)
		},
	}
	msg.Flags().StringVar(&style, "style", "chat", "chat, important or notification")

	var x, z float64
	var to string
	tp := &cobra.Command{
		Use: "tp <instance> <steamid>", Short: "Teleport a player to --x/--z or to another player (--to)", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			r, err := c.Teleport(ctxOf(cmd), args[0], args[1], x, z, to)
			if err != nil {
				return err
			}
			return printResult(cmd, r)
		},
	}
	tp.Flags().Float64Var(&x, "x", 0, "world x (east)")
	tp.Flags().Float64Var(&z, "z", 0, "world z (north)")
	tp.Flags().StringVar(&to, "to", "", "teleport to this player (Steam ID)")

	var qty, health float64
	var target string
	give := &cobra.Command{
		Use: "give <instance> <steamid> <class>", Short: "Spawn an item for a player", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			r, err := c.Give(ctxOf(cmd), args[0], args[1], args[2], qty, health, target)
			if err != nil {
				return err
			}
			return printResult(cmd, r)
		},
	}
	give.Flags().Float64Var(&qty, "qty", 0, "quantity (stack size, rounds, litres), 0 = default")
	give.Flags().Float64Var(&health, "health", 0, "health 0..1, 0 = default")
	give.Flags().StringVar(&target, "target", "inventory", "inventory, hands or ground")
	cmd.AddCommand(msg, tp, give)
	return cmd
}

func newVehicleCmd() *cobra.Command {
	var t apiTarget
	cmd := &cobra.Command{Use: "vehicle", Short: "List, repair, flip and delete vehicles through dzo-admin (needs `dzo serve`)"}
	t.flags(cmd)
	cmd.AddCommand(&cobra.Command{
		Use: "list <instance>", Short: "List vehicles", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			vs, err := c.Vehicles(ctxOf(cmd), args[0])
			if err != nil {
				return err
			}
			for _, v := range vs {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %-9s %-24s %6.0f %6.0f  health %.2f fuel %.2f crew %d\n", v.ID, v.Kind, v.Type, v.X, v.Z, v.Health, v.Fuel, len(v.Occupants))
			}
			return nil
		},
	})
	var scope string
	repair := &cobra.Command{
		Use: "repair <instance> <id>", Short: "Repair a vehicle", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			r, err := c.Repair(ctxOf(cmd), args[0], args[1], scope)
			if err != nil {
				return err
			}
			return printResult(cmd, r)
		},
	}
	repair.Flags().StringVar(&scope, "scope", "all", "all (repair, refuel, add missing parts), engine, parts, wheels, fluids or missing")
	flip := &cobra.Command{
		Use: "flip <instance> <id>", Short: "Stand a vehicle upright on its wheels", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			r, err := c.Flip(ctxOf(cmd), args[0], args[1])
			if err != nil {
				return err
			}
			return printResult(cmd, r)
		},
	}
	var force bool
	del := &cobra.Command{
		Use: "delete <instance> <id>", Short: "Delete a vehicle", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			r, err := c.DeleteVehicle(ctxOf(cmd), args[0], args[1], force)
			if err != nil {
				return err
			}
			return printResult(cmd, r)
		},
	}
	del.Flags().BoolVar(&force, "force", false, "delete even with players inside")
	cmd.AddCommand(repair, flip, del)
	return cmd
}
