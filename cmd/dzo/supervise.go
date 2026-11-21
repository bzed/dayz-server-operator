// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/supervise"
)

// exitCodeErr makes the process exit with an arbitrary code instead of
// cobra's generic 1-on-any-error, without printing an error line: the
// supervised server's exit code is its answer, not a dzo failure.
type exitCodeErr struct{ code int }

func (e *exitCodeErr) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// newInstanceSuperviseCmd is the ExecStart of a supervised instance unit
// (podman.mode: remote). It is hidden: the unit calls it, no operator does.
// The podman run arguments follow a `--` and are used verbatim, so a held unit
// keeps the generations it was rendered for across a restart.
func newInstanceSuperviseCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "supervise <name> -- <podman run arguments>",
		Short:  "Run or re-attach to an instance's container and report readiness to systemd (the unit's ExecStart)",
		Hidden: true,
		Args:   cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.ArgsLenAtDash() != 1 || len(args) < 2 {
				return errors.New("usage: dzo instance supervise <name> -- <podman run arguments>")
			}
			o, err := supervise.ParseUnitArgs(args[0], args[1:])
			if err != nil {
				return err
			}
			o.Stdout, o.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
			// systemd stops the unit with SIGTERM (SIGINT is for a run by
			// hand); the supervisor hands both to podman as a container stop.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			code, err := supervise.Run(ctx, o)
			if err != nil {
				return err
			}
			if code != 0 {
				// The server's exit code is the unit's answer, not a dzo
				// failure: no "Error:" line for it in the journal. A real
				// supervisor error (returned above) still prints.
				cmd.SilenceErrors = true
				return &exitCodeErr{code: code}
			}
			return nil
		},
	}
}
