// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// banFlags are the fields shared by `ban add` and `ban edit`.
type banFlags struct {
	steam, reason, message, duration string
	guids, ips, instances            []string
	global                           bool
	evidence                         []int64
}

func (f *banFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.steam, "steam", "", "Steam64 ID (its BattlEye GUID is banned too)")
	cmd.Flags().StringSliceVar(&f.guids, "guid", nil, "BattlEye GUID (repeatable)")
	cmd.Flags().StringVar(&f.reason, "reason", "", "why (kept for admins)")
	cmd.Flags().StringVar(&f.message, "message", "", "what the player is shown (default: "+moderation.DefaultMessage+")")
	cmd.Flags().StringVar(&f.duration, "duration", "", "30m, 12h, 7d, 2w or permanent")
	cmd.Flags().StringSliceVar(&f.instances, "instance", nil, "instance the ban applies to (repeatable)")
	cmd.Flags().BoolVar(&f.global, "global", false, "apply to every instance of the installation")
	cmd.Flags().Int64SliceVar(&f.evidence, "evidence", nil, "evidence id to link (repeatable)")
}

func (f *banFlags) scope(in *moderation.BanInput) error {
	switch {
	case f.global && len(f.instances) > 0:
		return fmt.Errorf("give --global or --instance, not both")
	case f.global:
		in.Scope, in.Instances = moderation.ScopeGlobal, nil
	case len(f.instances) > 0:
		in.Scope, in.Instances = moderation.ScopeInstances, f.instances
	}
	return nil
}

func printSync(w io.Writer, sync map[string]string) {
	names := make([]string, 0, len(sync))
	for n := range sync {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		_, _ = fmt.Fprintf(w, "  %s: %s\n", n, sync[n])
	}
}

func targetList(b moderation.Ban) string {
	var v []string
	for _, t := range b.Targets {
		if !t.Derived {
			v = append(v, t.Value)
		}
	}
	return strings.Join(v, ",")
}

func scopeText(b moderation.Ban) string {
	if b.Scope == moderation.ScopeGlobal {
		return "global"
	}
	return strings.Join(b.Instances, ",")
}

func printBanResult(cmd *cobra.Command, verb string, r api.BanResult) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "ban #%d %s\n", r.Ban.ID, verb)
	printSync(cmd.OutOrStdout(), r.Sync)
}

func idArg(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid ban id %q", s)
	}
	return id, nil
}

func newBanCmd() *cobra.Command {
	var t apiTarget
	cmd := &cobra.Command{Use: "ban", Short: "Ban players, list, edit and revoke bans (needs `dzo serve`)"}
	t.flags(cmd)

	var add banFlags
	addCmd := &cobra.Command{
		Use: "add", Short: "Ban a player by Steam ID, GUID or IP", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			if add.duration == "" {
				return fmt.Errorf("give --duration (e.g. 7d or permanent)")
			}
			exp, err := moderation.ExpiryFrom(add.duration, time.Now())
			if err != nil {
				return err
			}
			in := moderation.BanInput{SteamID: add.steam, GUIDs: add.guids, IPs: add.ips, Reason: add.reason, Message: add.message, ExpiresAt: exp, Evidence: add.evidence}
			if err := add.scope(&in); err != nil {
				return err
			}
			r, err := c.CreateBan(ctxOf(cmd), in)
			if err != nil {
				return err
			}
			printBanResult(cmd, "created", r)
			return nil
		},
	}
	add.register(addCmd)
	addCmd.Flags().StringSliceVar(&add.ips, "ip", nil, "IPv4 address (repeatable; mind shared addresses behind carrier NAT)")

	var f apiclient.BanFilter
	listCmd := &cobra.Command{
		Use: "list", Short: "List bans, newest first", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			bans, err := c.Bans(ctxOf(cmd), f)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "ID\tSTATE\tEXPIRES\tSCOPE\tTARGETS\tREASON")
			now := time.Now()
			for _, b := range bans {
				_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n", b.ID, b.State(now), moderation.FormatExpiry(b.ExpiresAt), scopeText(b), targetList(b), b.Reason)
			}
			return tw.Flush()
		},
	}
	listCmd.Flags().StringVar(&f.State, "state", "", "active, expired or revoked")
	listCmd.Flags().StringVar(&f.Instance, "instance", "", "bans that apply to this instance")
	listCmd.Flags().StringVar(&f.Query, "query", "", "Steam ID, GUID, IP, or text in the reason or a player name")
	listCmd.Flags().IntVar(&f.Limit, "limit", 100, "at most this many")

	showCmd := &cobra.Command{
		Use: "show <id>", Short: "Show a ban with its history", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			id, err := idArg(args[0])
			if err != nil {
				return err
			}
			b, err := c.Ban(ctxOf(cmd), id)
			if err != nil {
				return err
			}
			h, err := c.BanHistory(ctxOf(cmd), id)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(w, "ban #%d (%s)\nreason:   %s\nmessage:  %s\nexpires:  %s\nscope:    %s\ncreated:  %s by %s\n",
				b.ID, b.State(time.Now()), b.Reason, b.Message, moderation.FormatExpiry(b.ExpiresAt), scopeText(b), b.CreatedAt.Format(time.RFC3339), b.CreatedBy)
			for _, tg := range b.Targets {
				d := ""
				if tg.Derived {
					d = " (from the Steam ID)"
				}
				_, _ = fmt.Fprintf(w, "target:   %s %s%s\n", tg.Kind, tg.Value, d)
			}
			for _, e := range b.Evidence {
				_, _ = fmt.Fprintf(w, "evidence: #%d\n", e)
			}
			if b.RevokedAt != nil {
				_, _ = fmt.Fprintf(w, "revoked:  %s by %s: %s\n", b.RevokedAt.Format(time.RFC3339), b.RevokedBy, b.RevokeReason)
			}
			for _, e := range h {
				_, _ = fmt.Fprintf(w, "history:  %s %s %s\n", e.At.Format(time.RFC3339), e.Actor, e.Action)
			}
			return nil
		},
	}

	var ed banFlags
	var addIPs, removeIPs []string
	editCmd := &cobra.Command{
		Use: "edit <id>", Short: "Change a ban (only the given fields)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			id, err := idArg(args[0])
			if err != nil {
				return err
			}
			b, err := c.Ban(ctxOf(cmd), id)
			if err != nil {
				return err
			}
			in := b.Input()
			fl := cmd.Flags()
			if fl.Changed("steam") {
				in.SteamID = ed.steam
			}
			if fl.Changed("guid") {
				in.GUIDs = ed.guids
			}
			if fl.Changed("reason") {
				in.Reason = ed.reason
			}
			if fl.Changed("message") {
				in.Message = ed.message
			}
			if fl.Changed("duration") {
				if in.ExpiresAt, err = moderation.ExpiryFrom(ed.duration, time.Now()); err != nil {
					return err
				}
			}
			if fl.Changed("evidence") {
				in.Evidence = ed.evidence
			}
			if err := ed.scope(&in); err != nil {
				return err
			}
			in.IPs = slices.DeleteFunc(append(in.IPs, addIPs...), func(ip string) bool { return slices.Contains(removeIPs, ip) })
			r, err := c.UpdateBan(ctxOf(cmd), id, in)
			if err != nil {
				return err
			}
			printBanResult(cmd, "changed", r)
			return nil
		},
	}
	ed.register(editCmd)
	editCmd.Flags().StringSliceVar(&addIPs, "add-ip", nil, "add an IPv4 address")
	editCmd.Flags().StringSliceVar(&removeIPs, "remove-ip", nil, "remove an IPv4 address")

	var why string
	revokeCmd := &cobra.Command{
		Use: "revoke <id>", Short: "Unban: end a ban now (it stays in the history)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			id, err := idArg(args[0])
			if err != nil {
				return err
			}
			r, err := c.RevokeBan(ctxOf(cmd), id, why)
			if err != nil {
				return err
			}
			printBanResult(cmd, "revoked", r)
			return nil
		},
	}
	revokeCmd.Flags().StringVar(&why, "reason", "", "why the ban ends (required)")

	cmd.AddCommand(addCmd, listCmd, showCmd, editCmd, revokeCmd)
	return cmd
}

func newEvidenceCmd() *cobra.Command {
	var t apiTarget
	cmd := &cobra.Command{Use: "evidence", Short: "File and list evidence against players (needs `dzo serve`)"}
	t.flags(cmd)
	var who moderation.Who
	whoFlags := func(c *cobra.Command) {
		c.Flags().StringVar(&who.SteamID, "steam", "", "Steam64 ID")
		c.Flags().StringVar(&who.GUID, "guid", "", "BattlEye GUID")
		c.Flags().StringVar(&who.IP, "ip", "", "IPv4 address")
	}

	var instance, logText, note, file, image, caption string
	addCmd := &cobra.Command{
		Use: "add", Short: "File log lines (--log, or --file with - for stdin), a note or an image", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			in := moderation.EvidenceInput{SteamID: who.SteamID, GUID: who.GUID, IP: who.IP, Instance: instance}
			var ev moderation.Evidence
			switch {
			case image != "":
				data, err := os.ReadFile(image) //nolint:gosec // a file the operator names
				if err != nil {
					return err
				}
				in.Body = caption
				ev, err = c.AddEvidenceImage(ctxOf(cmd), in, data)
				if err != nil {
					return err
				}
			default:
				switch {
				case logText != "":
					in.Kind, in.Body = moderation.EvidenceLog, logText
				case note != "":
					in.Kind, in.Body = moderation.EvidenceNote, note
				case file != "":
					var data []byte
					if file == "-" {
						data, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), moderation.MaxEvidenceBody+1))
					} else {
						data, err = os.ReadFile(file) //nolint:gosec // a file the operator names
					}
					if err != nil {
						return err
					}
					in.Kind, in.Body = moderation.EvidenceLog, string(data)
				default:
					return fmt.Errorf("give --log, --note, --file or --image")
				}
				if ev, err = c.AddEvidence(ctxOf(cmd), in); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "evidence #%d filed\n", ev.ID)
			return nil
		},
	}
	whoFlags(addCmd)
	addCmd.Flags().StringVar(&instance, "instance", "", "instance the evidence is from (required)")
	addCmd.Flags().StringVar(&logText, "log", "", "log lines")
	addCmd.Flags().StringVar(&note, "note", "", "a note")
	addCmd.Flags().StringVar(&file, "file", "", "file with log lines, - for stdin")
	addCmd.Flags().StringVar(&image, "image", "", "PNG, JPEG or WebP screenshot")
	addCmd.Flags().StringVar(&caption, "caption", "", "caption of the image")

	listCmd := &cobra.Command{
		Use: "list", Short: "List the evidence filed against a player", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := t.client()
			if err != nil {
				return err
			}
			list, err := c.Evidence(ctxOf(cmd), who)
			if err != nil {
				return err
			}
			for _, e := range list {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "#%d %s %s %s by %s\n", e.ID, e.CreatedAt.Format(time.RFC3339), e.Instance, e.Kind, e.Source)
				if e.Body != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "    %s\n", strings.ReplaceAll(e.Body, "\n", "\n    "))
				}
			}
			return nil
		},
	}
	whoFlags(listCmd)
	cmd.AddCommand(addCmd, listCmd)
	return cmd
}
