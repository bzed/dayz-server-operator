// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Lifecycle drives one instance's systemd unit (`systemctl --user
// start/stop/restart dzo-<name>.service`, §C8). Command is overridden in
// tests to point at a fake systemctl script.
type Lifecycle struct {
	Command  string // defaults to "systemctl"
	UserMode bool   // true adds --user (the normal case: dzo runs as the "dayz" user's systemd instance)
}

func (l Lifecycle) command() string {
	if l.Command == "" {
		return "systemctl"
	}
	return l.Command
}

func (l Lifecycle) run(ctx context.Context, args ...string) (string, error) {
	full := args
	if l.UserMode {
		full = append([]string{"--user"}, args...)
	}
	cmd := exec.CommandContext(ctx, l.command(), full...) //nolint:gosec // command/args are operator-configured or built from a validated instance name, not external input
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("instance: systemctl %s: %w: %s", strings.Join(full, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// unitName returns the systemd unit name for instance name.
func unitName(name string) string { return "dzo-" + name + ".service" }

// Start runs `systemctl start dzo-<name>.service`.
func (l Lifecycle) Start(ctx context.Context, name string) error {
	_, err := l.run(ctx, "start", unitName(name))
	return err
}

// Stop runs `systemctl stop dzo-<name>.service` ("stop and stay stopped").
func (l Lifecycle) Stop(ctx context.Context, name string) error {
	_, err := l.run(ctx, "stop", unitName(name))
	return err
}

// Restart runs `systemctl restart dzo-<name>.service`. Callers wanting
// the graceful announce/lock/kick sequence first use GracefulRestart
// instead; this is a plain, immediate restart.
func (l Lifecycle) Restart(ctx context.Context, name string) error {
	_, err := l.run(ctx, "restart", unitName(name))
	return err
}

// IsActive reports whether the unit is currently active, mirroring
// `systemctl is-active`'s own semantics (a non-active unit is not an
// error to ask about).
func (l Lifecycle) IsActive(ctx context.Context, name string) (bool, error) {
	out, err := l.run(ctx, "is-active", unitName(name))
	state := strings.TrimSpace(out)
	if err != nil {
		// is-active exits non-zero for every state other than "active"
		// (inactive, failed, activating, ...) - that is its normal,
		// successful answer, not a command failure.
		return false, nil
	}
	return state == "active", nil
}

// AckFailure runs `systemctl reset-failed dzo-<name>.service`, the
// systemd half of `dzo instance ack-failure` (F3 layer 2/3): it lets the
// unit's own start-limit counter accept new attempts again. Clearing the
// FailureGate itself is the caller's job.
func (l Lifecycle) AckFailure(ctx context.Context, name string) error {
	_, err := l.run(ctx, "reset-failed", unitName(name))
	return err
}

// DaemonReload runs `systemctl daemon-reload`, needed after materializing
// new/changed unit files before systemd will notice them.
func (l Lifecycle) DaemonReload(ctx context.Context) error {
	_, err := l.run(ctx, "daemon-reload")
	return err
}

// EnableNow runs `systemctl --user enable --now <units>`: timers and the
// exporter, never a game server.
func (l Lifecycle) EnableNow(ctx context.Context, units ...string) error {
	_, err := l.run(ctx, append([]string{"enable", "--now"}, units...)...)
	return err
}

// Enable runs `systemctl --user enable <units>`: it only links the units into
// the default.target wants directory, it never starts one. `dzo units sync`
// uses it for a supervised instance unit (podman.mode: remote), which must
// come up with the user manager after a reboot but must never be started by
// the sync itself.
func (l Lifecycle) Enable(ctx context.Context, units ...string) error {
	_, err := l.run(ctx, append([]string{"enable"}, units...)...)
	return err
}

// Disable runs `systemctl --user disable --now <units>`, for units dzo no
// longer generates. A unit that is already gone is not an error.
func (l Lifecycle) Disable(ctx context.Context, units ...string) error {
	_, err := l.run(ctx, append([]string{"disable", "--now"}, units...)...)
	return err
}
