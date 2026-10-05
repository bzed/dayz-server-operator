// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/battleye"
	"github.com/bzed/dayz-server-operator/internal/runfiles"
)

func newRconCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rcon",
		Short: "Talk to an instance's built-in BattlEye RCon client (D30)",
	}
	cmd.AddCommand(newRconExecCmd())
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
