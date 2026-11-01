// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/auth"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/moderation"
	"github.com/bzed/dayz-server-operator/internal/serve"
)

// userTool is what the `dzo user` commands work on: the database and audit log of this host's dzo serve.
// It writes the database directly while dzo serve may be running (WAL and a busy timeout make that safe),
// like `dzo token` does with the token file.
type userTool struct {
	cmd   *cobra.Command
	cfg   *config.Config
	store *moderation.Store
	audit *admin.Audit
}

func openUserTool(cmd *cobra.Command, configPath string) (*userTool, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	st, err := moderation.Open(cmd.Context(), serve.BanDB(cfg))
	if err != nil {
		return nil, err
	}
	return &userTool{cmd: cmd, cfg: cfg, store: st, audit: admin.NewAudit(serve.AuditLog(cfg))}, nil
}

func (u *userTool) close() { _ = u.store.Close() }

// log writes the audit entry of a change: source cli, actor cli:$USER.
func (u *userTool) log(action, steamID, detail string) {
	who := os.Getenv("USER")
	if who == "" {
		who = "unknown"
	}
	if err := u.audit.Log(admin.AuditEntry{Actor: "cli:" + who, Source: "cli", Action: action, Target: steamID, Detail: detail, OK: true}); err != nil {
		_, _ = fmt.Fprintf(u.cmd.ErrOrStderr(), "warning: the audit log could not be written: %v\n", err)
	}
}

func (u *userTool) say(format string, a ...any) {
	_, _ = fmt.Fprintf(u.cmd.OutOrStdout(), format+"\n", a...)
}

func steamIDArg(id string) error {
	if !moderation.ValidSteamID(id) {
		return fmt.Errorf("%q is not a Steam ID (17 digits starting with 7656119)", id)
	}
	return nil
}

func grantsText(gs []moderation.Grant) string {
	if len(gs) == 0 {
		return "-"
	}
	parts := make([]string, len(gs))
	for i, g := range gs {
		parts[i] = g.Instance + "=" + g.Role
	}
	return strings.Join(parts, ",")
}

func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage the people who may use the web interface and what they may do (run on the host of dzo serve)",
		Long: "People are identified by their Steam ID and have a role per instance. `dzo user grant` is how the first admin is made. " +
			"The commands write the database of this host's dzo serve directly, so they work while it runs and when " +
			"nobody can log in.",
	}
	var configPath string

	with := func(fn func(u *userTool, args []string) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			u, err := openUserTool(cmd, configPath)
			if err != nil {
				return err
			}
			defer u.close()
			return fn(u, args)
		}
	}

	list := &cobra.Command{
		Use: "list", Short: "List the people with their grants", Args: cobra.NoArgs,
		RunE: with(func(u *userTool, _ []string) error {
			people, err := u.store.People(u.cmd.Context())
			if err != nil {
				return err
			}
			if len(people) == 0 {
				u.say("no people yet; make the first admin with: dzo user grant <steamid> --role admin")
				return nil
			}
			tw := tabwriter.NewWriter(u.cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "STEAM ID\tNAME\tUSERNAME\tGRANTS\tSTATE")
			for _, p := range people {
				state := ""
				if p.Disabled {
					state = "disabled"
				}
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.SteamID, orDash(p.Name), orDash(p.Username), grantsText(p.Grants), state)
			}
			return tw.Flush()
		}),
	}

	var role, name, username string
	var instances []string
	grant := &cobra.Command{
		Use:   "grant <steamid> --role <role> --username <name> [--instance <name>…] [--name <text>]",
		Short: "Give a role on instances (default: all, *); creates the person, replaces the role on listed instances",
		Args:  cobra.ExactArgs(1),
		RunE: with(func(u *userTool, args []string) error {
			id := args[0]
			if err := steamIDArg(id); err != nil {
				return err
			}
			if len(instances) == 0 {
				instances = []string{"*"}
			}
			p, err := u.store.Person(u.cmd.Context(), id)
			if err != nil && !errors.Is(err, moderation.ErrNotFound) {
				return err
			}
			if name != "" {
				p.Name = name
			}
			username = strings.ToLower(strings.TrimSpace(username))
			switch {
			case username == "" && p.Username == "":
				return errors.New("a new person needs a username: --username <name>")
			case username != "" && !api.ValidUsername(username):
				return fmt.Errorf("a username has 3 to 32 characters: letters, digits, dot, dash and underscore (got %q)", username)
			}
			p.Username = username // empty keeps the one they have
			p.SteamID = id
			byInstance := map[string]string{}
			for _, g := range p.Grants {
				byInstance[g.Instance] = g.Role
			}
			for _, i := range instances {
				byInstance[i] = role
			}
			p.Grants = p.Grants[:0:0]
			for i, r := range byInstance {
				p.Grants = append(p.Grants, moderation.Grant{Instance: i, Role: r})
			}
			sort.Slice(p.Grants, func(i, j int) bool { return p.Grants[i].Instance < p.Grants[j].Instance })
			if err := u.store.PutPerson(u.cmd.Context(), p, time.Now()); err != nil {
				if errors.Is(err, moderation.ErrUsernameTaken) {
					return fmt.Errorf("the username %q is taken", username)
				}
				return err
			}
			u.log("users.grant", id, "grants: "+grantsText(p.Grants))
			u.say("%s (%s): %s on %s; now %s", id, p.Username, role, strings.Join(instances, ", "), grantsText(p.Grants))
			return nil
		}),
	}
	grant.Flags().StringVar(&role, "role", "", "viewer, moderator, operator or admin (required)")
	grant.Flags().StringSliceVar(&instances, "instance", nil, "instance name (repeatable; default: * = every instance)")
	grant.Flags().StringVar(&username, "username", "", "the login name and what people see: 3 to 32 of a-z 0-9 . _ - (needed for a new person; given here or changed only by an administrator)")
	grant.Flags().StringVar(&name, "name", "", "display name (optional)")
	_ = grant.MarkFlagRequired("role")

	var revokeInstances []string
	revoke := &cobra.Command{
		Use:   "revoke <steamid> [--instance <name>…]",
		Short: "Take roles away: on the listed instances, or every grant",
		Args:  cobra.ExactArgs(1),
		RunE: with(func(u *userTool, args []string) error {
			id := args[0]
			if err := steamIDArg(id); err != nil {
				return err
			}
			p, err := u.store.Person(u.cmd.Context(), id)
			if err != nil {
				return personErr(id, err)
			}
			if len(revokeInstances) == 0 {
				p.Grants = nil
			} else {
				for _, i := range revokeInstances {
					found := false
					keep := p.Grants[:0:0]
					for _, g := range p.Grants {
						if g.Instance == i {
							found = true
							continue
						}
						keep = append(keep, g)
					}
					if !found {
						return fmt.Errorf("%s has no grant on %q", id, i)
					}
					p.Grants = keep
				}
			}
			if err := u.store.PutPerson(u.cmd.Context(), p, time.Now()); err != nil {
				return err
			}
			u.log("users.revoke", id, "grants: "+grantsText(p.Grants))
			u.say("%s: now %s", id, grantsText(p.Grants))
			return nil
		}),
	}
	revoke.Flags().StringSliceVar(&revokeInstances, "instance", nil, "instance name (repeatable; default: every grant)")

	setDisabled := func(use, short string, disabled bool) *cobra.Command {
		return &cobra.Command{
			Use: use + " <steamid>", Short: short, Args: cobra.ExactArgs(1),
			RunE: with(func(u *userTool, args []string) error {
				id := args[0]
				if err := steamIDArg(id); err != nil {
					return err
				}
				p, err := u.store.Person(u.cmd.Context(), id)
				if err != nil {
					return personErr(id, err)
				}
				p.Disabled = disabled
				if err := u.store.PutPerson(u.cmd.Context(), p, time.Now()); err != nil {
					return err
				}
				u.log("users."+use, id, "")
				u.say("%s: %sd", id, use)
				return nil
			}),
		}
	}

	del := &cobra.Command{
		Use: "delete <steamid>", Short: "Delete a person with their grants, credentials and sessions", Args: cobra.ExactArgs(1),
		RunE: with(func(u *userTool, args []string) error {
			id := args[0]
			if err := steamIDArg(id); err != nil {
				return err
			}
			if err := u.store.DeletePerson(u.cmd.Context(), id); err != nil {
				return personErr(id, err)
			}
			u.log("users.delete", id, "")
			u.say("%s: deleted", id)
			return nil
		}),
	}

	reset := &cobra.Command{
		Use:   "reset <steamid|username>",
		Short: "Remove password, TOTP, passkeys, recovery codes and sessions; the person enrols again (the username stays)",
		Args:  cobra.ExactArgs(1),
		RunE: with(func(u *userTool, args []string) error {
			id := args[0]
			if !moderation.ValidSteamID(id) {
				p, err := u.store.PersonByUsername(u.cmd.Context(), id)
				if err != nil {
					return personErr(id, err)
				}
				id = p.SteamID
			}
			if err := u.store.ResetCredentials(u.cmd.Context(), id); err != nil {
				return personErr(id, err)
			}
			u.log("users.reset", id, "password, second factors and sessions removed")
			u.say("%s: credentials removed; they sign in through Steam and set up a second factor again", id)
			return nil
		}),
	}

	password := &cobra.Command{
		Use:   "password <steamid|username>",
		Short: "Set the password of a person, for when signing in through Steam does not work yet",
		Long: "Sets the password of a person who already has a role and a username (dzo user grant). The password is read " +
			"from the terminal without echo, twice; piped in, one line is read. It is the first factor only: the person " +
			"still sets up an authenticator app or a passkey at the first login (they get recovery codes), and logs in " +
			"with it every time after. Until they have, whoever knows the password can set up that second factor, so " +
			"hand the password over safely. Passwords are 12 to 256 bytes long. `dzo user reset` removes it again.",
		Args: cobra.ExactArgs(1),
		RunE: with(func(u *userTool, args []string) error {
			ctx := u.cmd.Context()
			var p moderation.Person
			var err error
			if moderation.ValidSteamID(args[0]) {
				p, err = u.store.Person(ctx, args[0])
			} else {
				p, err = u.store.PersonByUsername(ctx, args[0])
			}
			if errors.Is(err, moderation.ErrNotFound) {
				return fmt.Errorf("no such person: %s (give them a role first: dzo user grant <steamid> --role admin)", args[0])
			}
			if err != nil {
				return err
			}
			if p.Username == "" {
				return fmt.Errorf("%s has no username yet: dzo user grant %s --role <role> --username <name>", p.SteamID, p.SteamID)
			}
			pw, err := readNewPassword(u.cmd)
			if err != nil {
				return err
			}
			hash, err := auth.HashPassword(pw)
			if err != nil {
				return err
			}
			if err := u.store.SetPassword(ctx, p.SteamID, "", hash); err != nil {
				return err
			}
			if err := u.store.DeleteSessions(ctx, p.SteamID, ""); err != nil {
				return err
			}
			u.log("users.password", p.SteamID, "set from the command line")
			u.say("%s: password set, their sessions ended, login name %s; at the first login they set up a second factor", p.SteamID, p.Username)
			return nil
		}),
	}

	cmd.AddCommand(list, grant, revoke, password,
		setDisabled("disable", "Block a person from logging in and end their sessions", true),
		setDisabled("enable", "Let a disabled person log in again", false),
		del, reset)
	for _, c := range cmd.Commands() {
		configFlag(c, &configPath)
	}
	return cmd
}

// readNewPassword asks for a password twice on a terminal (no echo), or reads one line from a pipe.
func readNewPassword(cmd *cobra.Command) (string, error) {
	in := cmd.InOrStdin()
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		read := func(prompt string) (string, error) {
			_, _ = fmt.Fprint(cmd.ErrOrStderr(), prompt)
			b, err := term.ReadPassword(int(f.Fd()))
			_, _ = fmt.Fprintln(cmd.ErrOrStderr())
			return string(b), err
		}
		first, err := read("New password: ")
		if err != nil {
			return "", fmt.Errorf("read from terminal: %w", err)
		}
		second, err := read("Again: ")
		if err != nil {
			return "", fmt.Errorf("read from terminal: %w", err)
		}
		if first != second {
			return "", errors.New("the two passwords differ")
		}
		return first, nil
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read input: %w", err)
	}
	return trimNewline(line), nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func personErr(who string, err error) error {
	if errors.Is(err, moderation.ErrNotFound) {
		return fmt.Errorf("no such person: %s", who)
	}
	return err
}
