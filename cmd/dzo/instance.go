// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/bzed/dayz-server-operator/internal/backup"
	"github.com/bzed/dayz-server-operator/internal/battleye"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/mission"
	"github.com/bzed/dayz-server-operator/internal/quadlet"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/runfiles"
	"github.com/bzed/dayz-server-operator/internal/serve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// newInstanceCmd wires the lifecycle operations internal/instance
// implements (§C8). Resolving a real instance's mission/product config to
// build a full quadlet spec, and the announcement countdown before a
// graceful restart's lock/kick phase, are left to a future config-driven
// pass (see internal/instance's package doc comment); these commands
// operate directly on a unit/RCon target the operator names.
func newInstanceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instance",
		Short: "Instance lifecycle: start/stop/restart, the F3 failure gate (§C5/§C8)",
	}
	cmd.AddCommand(newInstanceCreateCmd(), newInstanceApplyCmd(), newInstanceRemoveCmd(), newInstanceCloneCmd(), newInstanceUpgradeCmd(), newInstanceModsCmd(), newInstanceShowCmd(), newInstanceRenderCmd(), newInstanceStartCmd(), newInstanceStopCmd(), newInstanceRestartCmd(), newInstanceShutdownCmd(), newInstanceHookCmd(), newInstanceAckFailureCmd())
	return cmd
}

func newInstanceShowCmd() *cobra.Command {
	var configPath string
	var showQuadlet bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Print an instance's resolved configuration as YAML",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			if showQuadlet {
				if len(inst.Missing) > 0 {
					return fmt.Errorf("instance %q is not installed completely: %s", inst.Name, strings.Join(inst.Missing, "; "))
				}
				unit, err := quadlet.RenderContainer(inst.Quadlet)
				if err != nil {
					return err
				}
				_, err = fmt.Fprint(cmd.OutOrStdout(), unit)
				return err
			}
			enc := yaml.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent(2)
			return enc.Encode(inst)
		},
	}
	configFlag(cmd, &configPath)
	cmd.Flags().BoolVar(&showQuadlet, "quadlet", false, "print the rendered quadlet unit instead")
	return cmd
}

func newInstanceRenderCmd() *cobra.Command {
	var configPath string
	var dryRun, updatePristine bool
	cmd := &cobra.Command{
		Use:   "render <name>",
		Short: "Update the live mission from the pristine one, in place (--dry-run: only print the plan)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			return renderInstance(cmd, cfg, inst, dryRun, updatePristine)
		},
	}
	configFlag(cmd, &configPath)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the apply plan and write nothing to the live mission")
	cmd.Flags().BoolVar(&updatePristine, "update-pristine", false, "fetch the pristine mission again from git before rendering")
	return cmd
}

// renderInstance is what `dzo instance render` does: everything the start needs besides the
// container, with the live mission updated in place. dryRun prints the plan and writes nothing.
func renderInstance(cmd *cobra.Command, cfg *config.Config, inst *resolve.Instance, dryRun, updatePristine bool) error {
	// Everything that can be wrong with the rest of the start is found
	// before the live mission is touched (§C5 F3), dry run included.
	rf, err := prepareRunfiles(cfg, inst)
	if err != nil {
		return err
	}
	if !dryRun {
		if err := prepareInstanceDir(cmd, cfg, inst, updatePristine); err != nil {
			return err
		}
	}
	src := inst.Mission.Source
	if _, err := os.Stat(inst.Paths.Pristine); updatePristine || os.IsNotExist(err) {
		if err := mission.FetchPristine(cmd.Context(), src.Git, src.Ref, src.Path, inst.Paths.Pristine); err != nil {
			return err
		}
	}
	tree, err := site.LoadTree(cfg.Paths.Site)
	if err != nil {
		return err
	}
	in := mission.RenderInput{
		PristineDir: inst.Paths.Pristine, LiveDir: inst.Paths.Live, ManifestPath: inst.Paths.Manifest,
		FileHistoryDir: inst.Paths.FileHistory, Unmanaged: inst.Mission.Unmanaged, DryRun: dryRun,
	}
	if err := addIntegrations(cmd, &in, cfg, tree, inst); err != nil {
		return err
	}
	if inst.Mission.Fallback != "" && inst.Product.Dir != "" {
		in.FallbackDir = filepath.Join(inst.Product.Dir, "mpmissions", inst.Mission.Fallback)
	}
	plan, report, err := mission.Render(in)
	if err != nil {
		return err
	}
	printPlan(cmd, plan)
	if dryRun {
		return nil
	}
	printReport(cmd, report)
	if err := runfiles.Write(rf); err != nil {
		return err
	}
	if inst.AdminEnabled() {
		// Every render rotates the mod's token (§C13). The mod reads its config once, at
		// start: rotating it under a running server cuts the server off (HTTP 401 until the
		// next restart), and a changed config would not apply anyway. The render that starts
		// the unit (ExecStartPre) runs while the unit is "activating", not "active".
		if active, _ := (instance.Lifecycle{UserMode: true}).IsActive(cmd.Context(), inst.Name); active {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "dzo-admin config kept (the server is running; it is written at the next start)")
		} else {
			if err := serve.WriteModConfig(cfg, inst); err != nil {
				return fmt.Errorf("writing the dzo-admin config: %w", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "dzo-admin config written")
		}
	}
	// post_render: the live mission is up to date; a failing hook fails the render, so the
	// unit does not start on a mission the hook rejected.
	if err := instanceHooks(cmd.Context(), cmd.OutOrStdout(), cfg, inst, hookPostRender, nil, nil); err != nil {
		return err
	}
	return nil
}

// prepareInstanceDir creates the instance as a btrfs subvolume on its first
// render, and takes the snapshots the instance's backup policy asks for before
// this render changes anything. A snapshot that fails aborts the render.
func prepareInstanceDir(cmd *cobra.Command, cfg *config.Config, inst *resolve.Instance, updatePristine bool) error {
	_, statErr := os.Lstat(inst.Paths.Root)
	firstRender := os.IsNotExist(statErr)
	subvol, err := backup.EnsureInstanceDir(inst.Paths.Root)
	if err != nil {
		return err
	}
	if !subvol {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s is not a btrfs subvolume, so this instance cannot be backed up\n", inst.Paths.Root)
		return nil
	}
	if err := os.MkdirAll(cfg.Paths.Snapshots, 0o750); err != nil {
		return err
	}
	if firstRender {
		return nil // nothing to protect yet
	}
	m := backup.For(cfg, inst)
	reasons := []backup.Reason{backup.Render}
	if updatePristine {
		reasons = append(reasons, backup.MissionUpdate)
	}
	for _, r := range reasons {
		res, took, err := backup.Before(cmd.Context(), m, inst.Backup, r)
		if err != nil {
			return fmt.Errorf("the %s snapshot failed, nothing was changed: %w", r, err)
		}
		if took {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "snapshot %s\n", res.Snapshot.ID)
		}
		printWarnings(cmd.ErrOrStderr(), res.Warnings)
	}
	return nil
}

// prepareRunfiles gathers what the keys, serverDZ.cfg and BattlEye config of
// an instance are rendered from (§C6 steps 2, 6, 7) and checks that the
// site's serverDZ.cfg parses.
func prepareRunfiles(cfg *config.Config, inst *resolve.Instance) (runfiles.Input, error) {
	src, err := os.ReadFile(filepath.Join(cfg.Paths.Site, "instances", inst.Name, "serverDZ.cfg")) //nolint:gosec // the site checkout
	if err != nil {
		return runfiles.Input{}, fmt.Errorf("instances/%s/serverDZ.cfg is missing in the site repo: %w", inst.Name, err)
	}
	if _, err := runfiles.ServerCfg(src, inst.Map, inst.Ports.Query); err != nil {
		return runfiles.Input{}, fmt.Errorf("instances/%s/%w", inst.Name, err)
	}
	pw, err := runfiles.RConPassword(cfg.Paths.Secrets, inst.Name)
	if err != nil {
		return runfiles.Input{}, err
	}
	var clientMods []string
	for _, m := range inst.Mods {
		if !m.Server && m.Dir != "" {
			clientMods = append(clientMods, m.Dir)
		}
	}
	keys, err := runfiles.Keys(inst.Product.Dir, clientMods)
	if err != nil {
		return runfiles.Input{}, err
	}
	return runfiles.Input{
		RuntimeDir: inst.Paths.Runtime, ProfilesDir: inst.Paths.Profiles, StorageDir: inst.Paths.Storage, ServerCfg: src, Template: inst.Map,
		QueryPort: inst.Ports.Query, RConPort: inst.Ports.RCon, RConPass: pw, Keys: keys, RConIP: rconBind(inst),
	}, nil
}

func lifecycleFlags(cmd *cobra.Command, lc *instance.Lifecycle) {
	cmd.Flags().StringVar(&lc.Command, "command", "systemctl", "systemctl binary to run")
	cmd.Flags().BoolVar(&lc.UserMode, "user", true, "pass --user to systemctl (the normal case: a per-user systemd instance)")
}

func newInstanceStartCmd() *cobra.Command {
	var lc instance.Lifecycle
	cmd := &cobra.Command{
		Use:   "start <name>",
		Short: "systemctl start dzo-<name>.service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return lc.Start(cmd.Context(), args[0])
		},
	}
	lifecycleFlags(cmd, &lc)
	return cmd
}

func newInstanceStopCmd() *cobra.Command {
	var lc instance.Lifecycle
	cmd := &cobra.Command{
		Use:   "stop <name>",
		Short: "systemctl stop dzo-<name>.service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return lc.Stop(cmd.Context(), args[0])
		},
	}
	lifecycleFlags(cmd, &lc)
	return cmd
}

func newInstanceRestartCmd() *cobra.Command {
	var lc instance.Lifecycle
	var graceful bool
	var rconAddr, rconPassword, reason string
	var delay, rconTimeout time.Duration
	var kickPasses int
	cmd := &cobra.Command{
		Use:   "restart <name>",
		Short: "Restart an instance, plainly or via the lock/kick/#kick sequence (--graceful)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !graceful {
				return lc.Restart(cmd.Context(), args[0])
			}
			if rconAddr == "" {
				return fmt.Errorf("--rcon-addr is required with --graceful")
			}
			client, err := battleye.Dial(rconAddr, rconPassword, battleye.WithLoginTimeout(rconTimeout))
			if err != nil {
				// §C8: if RCon is unreachable, log it and restart via
				// systemd immediately rather than blocking the restart on it.
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "RCon unreachable (%v), restarting immediately\n", err)
				return lc.Restart(cmd.Context(), args[0])
			}
			defer func() { _ = client.Close() }()
			return instance.GracefulRestart(cmd.Context(), client, lc, args[0], instance.GracefulRestartOptions{
				Reason: reason, KickPasses: kickPasses, Delay: delay,
			})
		},
	}
	lifecycleFlags(cmd, &lc)
	cmd.Flags().BoolVar(&graceful, "graceful", false, "lock, kick players, then restart (§C8) instead of an immediate restart")
	cmd.Flags().StringVar(&rconAddr, "rcon-addr", "", "RCon host:port (required with --graceful)")
	cmd.Flags().StringVar(&rconPassword, "rcon-password", "", "RCon password (with --graceful)")
	cmd.Flags().StringVar(&reason, "reason", "server restart", "kick reason shown to players")
	cmd.Flags().DurationVar(&delay, "delay", 3*time.Second, "wait after the final kick before restarting")
	cmd.Flags().IntVar(&kickPasses, "kick-passes", 3, "number of players+kick rounds before the final #kick -1")
	cmd.Flags().DurationVar(&rconTimeout, "rcon-timeout", 5*time.Second, "RCon login handshake timeout")
	return cmd
}

// newInstanceShutdownCmd is what the unit's ExecStop runs for stop.method rcon: ask the
// server to shut down over RCon and wait for its container to stop. It never fails the stop.
func newInstanceShutdownCmd() *cobra.Command {
	var configPath string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "shutdown <name>",
		Short: "Ask the server to shut down over RCon and wait for it to exit (the unit's ExecStop)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			logf := func(format string, a ...any) {
				_, _ = fmt.Fprintf(out, "dzo shutdown "+inst.Name+": "+format+"\n", a...)
			}
			pw, err := runfiles.RConPassword(cfg.Paths.Secrets, inst.Name)
			if err != nil {
				logf("%v", err)
				return nil
			}
			running := func(ctx context.Context) (bool, error) {
				b, err := exec.CommandContext(ctx, "podman", "inspect", "-f", "{{.State.Running}}", "dzo-"+inst.Name).Output() //nolint:gosec // our container's name
				if err != nil {
					return false, nil // no such container: nothing runs
				}
				return strings.TrimSpace(string(b)) == "true", nil
			}
			client, err := battleye.Dial("127.0.0.1:"+strconv.Itoa(inst.Ports.RCon), pw, battleye.WithLoginTimeout(5*time.Second))
			if err != nil {
				logf("RCon unreachable (%v), leaving it to the hard stop", err)
				return nil
			}
			defer func() { _ = client.Close() }()
			_, err = instance.Shutdown(cmd.Context(), client, instance.ShutdownOptions{Timeout: timeout, Running: running, Log: logf})
			return err
		},
	}
	configFlag(cmd, &configPath)
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "how long to wait for the server to exit before the hard stop")
	return cmd
}

func newInstanceAckFailureCmd() *cobra.Command {
	var lc instance.Lifecycle
	var gateFile string
	cmd := &cobra.Command{
		Use:   "ack-failure <name>",
		Short: "Clear the F3 failed-render gate and systemd's reset-failed state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := lc.AckFailure(cmd.Context(), args[0]); err != nil {
				return err
			}
			if gateFile == "" {
				return nil
			}
			gate := instance.FailureGate{}
			gate.Ack()
			return gate.State.Save(gateFile)
		},
	}
	lifecycleFlags(cmd, &lc)
	cmd.Flags().StringVar(&gateFile, "gate-file", "", "path to the instance's persisted failed-render gate state (cleared if given)")
	return cmd
}

// rconBind is the address RCon listens on. dzo reaches it on 127.0.0.1, so with host networking
// nothing else on the network needs to: the game's own ports are the only ones that should be
// open. In a container network of its own the server must listen on every address of it.
func rconBind(inst *resolve.Instance) string {
	if inst.Network == site.NetworkHost {
		return "127.0.0.1"
	}
	return ""
}
