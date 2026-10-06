// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/battleye"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/runfiles"
)

func newRconCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rcon",
		Short: "Talk to an instance's built-in BattlEye RCon client (D30)",
	}
	cmd.AddCommand(newRconExecCmd(), newRconConsoleCmd(), newRconRotateCmd())
	return cmd
}

func newRconExecCmd() *cobra.Command {
	var addr, password, instanceName, configPath string
	var timeout time.Duration
	c := &cobra.Command{
		Use:   "exec <command...>",
		Short: "Send one RCon command and print its response",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if instanceName != "" {
				// The address and password of a dzo instance: its RCon port on localhost and the
				// password dzo generated for it.
				cfg, inst, err := loadInstance(configPath, instanceName)
				if err != nil {
					return err
				}
				if password, err = runfiles.RConPassword(cfg.Paths.Secrets, inst.Name); err != nil {
					return err
				}
				addr = "127.0.0.1:" + strconv.Itoa(inst.Ports.RCon)
			} else if addr == "" || password == "" {
				return fmt.Errorf("give --instance <name>, or --addr and --password")
			}
			client, err := battleye.Dial(addr, password)
			if err != nil {
				return fmt.Errorf("rcon: connect: %w", err)
			}
			defer func() { _ = client.Close() }()

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			command := args[0]
			for _, a := range args[1:] {
				command += " " + a
			}
			resp, err := client.Command(ctx, command)
			if err != nil {
				return fmt.Errorf("rcon: command: %w", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), resp)
			return nil
		},
	}
	c.Flags().StringVar(&instanceName, "instance", "", "a dzo instance: its RCon port and generated password are used")
	configFlag(c, &configPath)
	c.Flags().StringVar(&addr, "addr", "", "RCon host:port (without --instance)")
	c.Flags().StringVar(&password, "password", "", "RCon password (without --instance)")
	c.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "command timeout")
	return c
}

func newRconConsoleCmd() *cobra.Command {
	var configPath string
	var timeout time.Duration
	c := &cobra.Command{
		Use:   "console <instance>",
		Short: "An interactive RCon console: one command per line, server messages as they arrive",
		Long: "Reads commands from standard input, prints the answers and the server's own messages (connects, chat, " +
			"kicks). It reconnects when the server restarts. Leave with exit, quit or end of input.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			pw, err := runfiles.RConPassword(cfg.Paths.Secrets, inst.Name)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			s := &battleye.Session{Addr: "127.0.0.1:" + strconv.Itoa(inst.Ports.RCon), Password: pw}
			s.Start()
			defer func() { _ = s.Close() }()
			go func() {
				for ev := range s.Events() {
					_, _ = fmt.Fprintf(out, "< %s\n", ev.Raw)
				}
			}()
			wctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if err := s.WaitConnected(wctx); err != nil {
				return fmt.Errorf("rcon: %s is not reachable: %w", inst.Name, err)
			}
			_, _ = fmt.Fprintf(out, "connected to %s; exit to leave\n", inst.Name)
			sc := bufio.NewScanner(cmd.InOrStdin())
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				switch line {
				case "":
					continue
				case "exit", "quit":
					return nil
				}
				cctx, ccancel := context.WithTimeout(cmd.Context(), 60*time.Second)
				resp, err := s.Command(cctx, line)
				ccancel()
				if err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "error: %v\n", err)
					continue
				}
				_, _ = fmt.Fprintln(out, resp)
			}
			return sc.Err()
		},
	}
	configFlag(c, &configPath)
	c.Flags().DurationVar(&timeout, "timeout", 15*time.Second, "how long to wait for the first connection")
	return c
}

func newRconRotateCmd() *cobra.Command {
	var configPath string
	var restart bool
	c := &cobra.Command{
		Use:   "rotate <instance> [--restart]",
		Short: "Generate a new RCon password for an instance",
		Long: "Writes a new password to <paths.secrets>/rcon/<instance>. The server reads it at its start, so a running " +
			"server has to be restarted: until then dzo cannot log in. Without --restart a running instance is refused; " +
			"with it the instance is restarted through its unit (the start renders the new BattlEye config).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			lc := instance.Lifecycle{UserMode: true}
			active, _ := lc.IsActive(cmd.Context(), inst.Name)
			if active && !restart {
				return fmt.Errorf("instance %s is running: its RCon password changes at the next start; use --restart to restart it now", inst.Name)
			}
			pw, err := runfiles.RandomPassword(24)
			if err != nil {
				return err
			}
			path := filepath.Join(cfg.Paths.Secrets, "rcon", inst.Name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(pw+"\n"), 0o600); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "new RCon password for %s written to %s\n", inst.Name, path)
			if active {
				return lc.Restart(cmd.Context(), inst.Name)
			}
			return nil
		},
	}
	configFlag(c, &configPath)
	c.Flags().BoolVar(&restart, "restart", false, "restart a running instance to apply it")
	return c
}
