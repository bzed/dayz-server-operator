// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/admin"
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

	var style, notifyType, title string
	var seconds int
	msg := &cobra.Command{
		Use: "msg <instance> <steamid|all> <text>", Short: "Send a message to a player, or to everyone", Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			if notifyType != "" && style != "expansion" {
				return fmt.Errorf("--type needs --style expansion")
			}
			if style == "expansion" && notifyType != "" && !slices.Contains(admin.NotifyTypes, notifyType) {
				return fmt.Errorf("unknown --type %q (want %s)", notifyType, strings.Join(admin.NotifyTypes, ", "))
			}
			id := args[1]
			if id == "all" {
				id = ""
			}
			r, err := c.Message(ctxOf(cmd), args[0], id, api.MessageBody{Text: strings.Join(args[2:], " "), Style: style, NotifyType: notifyType, Title: title, Seconds: seconds})
			if err != nil {
				return err
			}
			return printResult(cmd, r)
		},
	}
	msg.Flags().StringVar(&style, "style", "chat", "how it reaches the players: chat (yellow), important (red), friendly (green), blue, notification (on screen), expansion (Expansion notification, see --type), rcon (BattlEye say, everyone only)")
	msg.Flags().StringVar(&notifyType, "type", "", "Expansion notification type with --style expansion: "+strings.Join(admin.NotifyTypes, ", ")+" (default toast)")
	msg.Flags().StringVar(&title, "title", "", "heading of a notification (default Server)")
	msg.Flags().IntVar(&seconds, "seconds", 0, "how long a notification stays on screen (default 10, at most 60)")

	kick := &cobra.Command{
		Use: "kick <instance> <steamid> <reason...>", Short: "Kick a player; the reason (at most 64 bytes) is shown to them", Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			r, err := c.Kick(ctxOf(cmd), args[0], args[1], strings.Join(args[2:], " "))
			if err != nil {
				return err
			}
			return printResult(cmd, r)
		},
	}

	simple := func(use, short string, f func(*apiclient.Client, context.Context, string, string) (api.ActionResult, error), confirm bool) *cobra.Command {
		var yes bool
		c := &cobra.Command{
			Use: use + " <instance> <steamid>", Short: short, Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				if confirm && !yes {
					return fmt.Errorf("%s needs --yes: it cannot be undone", use)
				}
				c, err := t.client()
				if err != nil {
					return err
				}
				r, err := f(c, ctxOf(cmd), args[0], args[1])
				if err != nil {
					return err
				}
				return printResult(cmd, r)
			},
		}
		if confirm {
			c.Flags().BoolVar(&yes, "yes", false, "confirm")
		}
		return c
	}
	heal := simple("heal", "Heal a player: health, blood, shock, bones, bleeding and diseases", (*apiclient.Client).Heal, false)
	fixgear := simple("fixgear", "Repair everything a player carries and wears", (*apiclient.Client).FixGear, false)
	kill := simple("kill", "Kill a player", (*apiclient.Client).Kill, true)

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

	var qtyText string
	var health float64
	var target string
	var attachments bool
	give := &cobra.Command{
		Use: "give <instance> <steamid> <class>", Short: "Spawn an item for a player", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			qty, err := admin.ParseQuantity(qtyText)
			if err != nil {
				return err
			}
			r, err := c.Give(ctxOf(cmd), args[0], args[1], args[2], qty, health, target, attachments)
			if err != nil {
				return err
			}
			return printResult(cmd, r)
		},
	}
	give.Flags().StringVar(&qtyText, "qty", "default", "quantity (stack size, rounds, millilitres): a number (0 = empty), default or max (a full stack)")
	give.Flags().Float64Var(&health, "health", 0, "health 0..1, 0 = default")
	give.Flags().BoolVar(&attachments, "attachments", false, "with the kit the game's debug spawn gives it (a flashlight with its battery, a weapon with its parts)")
	give.Flags().StringVar(&target, "target", "inventory", "inventory, hands or ground")
	var sx, sz float64
	spawn := &cobra.Command{
		Use: "spawn <instance> <class>", Short: "Spawn an item on the ground at a position", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			qty, err := admin.ParseQuantity(qtyText)
			if err != nil {
				return err
			}
			r, err := c.Spawn(ctxOf(cmd), args[0], args[1], qty, health, sx, sz, attachments)
			if err != nil {
				return err
			}
			return printResult(cmd, r)
		},
	}
	spawn.Flags().StringVar(&qtyText, "qty", "default", "quantity: a number (0 = empty; more than a stack holds makes more stacks), default or max (a full stack)")
	spawn.Flags().Float64Var(&health, "health", 0, "health 0..1, 0 = default")
	spawn.Flags().BoolVar(&attachments, "attachments", false, "with the kit the game's debug spawn gives it")
	spawn.Flags().Float64Var(&sx, "x", 0, "world x (east)")
	spawn.Flags().Float64Var(&sz, "z", 0, "world z (north)")
	cmd.AddCommand(msg, kick, heal, fixgear, kill, tp, give, spawn)
	return cmd
}

func newVehicleCmd() *cobra.Command {
	var t apiTarget
	cmd := &cobra.Command{Use: "vehicle", Short: "List, spawn, repair, flip, move and delete vehicles through dzo-admin (needs `dzo serve`)"}
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
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %-9s %-24s %6.0f %6.0f  health %.2f fuel %.2f crew %d%s\n", v.ID, v.Kind, v.Type, v.X, v.Z, v.Health, v.Fuel, len(v.Occupants), vehicleNote(v))
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
	var mx, mz float64
	move := &cobra.Command{
		Use: "move <instance> <id> --x <x> --z <z>", Short: "Put a vehicle, with the players inside, on the ground at a position", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			r, err := c.MoveVehicle(ctxOf(cmd), args[0], args[1], mx, mz)
			if err != nil {
				return err
			}
			return printResult(cmd, r)
		},
	}
	move.Flags().Float64Var(&mx, "x", 0, "world x (east)")
	move.Flags().Float64Var(&mz, "z", 0, "world z (north)")
	var vx, vz float64
	spawn := &cobra.Command{
		Use: "spawn <instance> <class>", Short: "Spawn a vehicle on the ground at a position", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			r, err := c.SpawnVehicle(ctxOf(cmd), args[0], args[1], vx, vz)
			if err != nil {
				return err
			}
			return printResult(cmd, r)
		},
	}
	spawn.Flags().Float64Var(&vx, "x", 0, "world x (east)")
	spawn.Flags().Float64Var(&vz, "z", 0, "world z (north)")
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
	cmd.AddCommand(spawn, repair, flip, move, del)
	return cmd
}

// newFlagCmd is `dzo flag`: actions on the territory flags of the map.
func newFlagCmd() *cobra.Command {
	var t apiTarget
	cmd := &cobra.Command{Use: "flag", Short: "Territory flags through dzo-admin (needs `dzo serve`)"}
	t.flags(cmd)
	raise := &cobra.Command{
		Use: "raise <instance> <id>", Short: "Raise a territory flag to the top of its pole and fill its timer (the id is the marker id on the Flags layer)", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			r, err := c.RaiseFlag(ctxOf(cmd), args[0], args[1])
			if err != nil {
				return err
			}
			return printResult(cmd, r)
		},
	}
	cmd.AddCommand(raise)
	return cmd
}

// vehicleNote is the owner and cover state a DayZ Expansion server adds to a vehicle line.
func vehicleNote(v admin.Vehicle) string {
	s := ""
	if v.Owner != "" {
		s += "  owner " + v.Owner
	}
	if v.Covered {
		s += "  (covered)"
	}
	return s
}
