// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/bzed/dayz-server-operator/internal/backup"
	"github.com/bzed/dayz-server-operator/internal/battleye"
	"github.com/bzed/dayz-server-operator/internal/btrfs"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/mission"
	"github.com/bzed/dayz-server-operator/internal/quadlet"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/runfiles"
	"github.com/bzed/dayz-server-operator/internal/serve"
	"github.com/bzed/dayz-server-operator/internal/servermods"
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
				return startFailure(cmd, "render", args[0], err)
			}
			return startFailure(cmd, "render", args[0], renderInstance(cmd, cfg, inst, dryRun, updatePristine))
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
	if err := checkModsUsable(inst); err != nil {
		return err
	}
	if !dryRun {
		if err := prepareInstanceDir(cmd, cfg, inst, updatePristine); err != nil {
			return err
		}
	}
	src := inst.Mission.Source
	pristine := inst.Paths.Pristine
	if _, err := os.Stat(pristine); updatePristine || os.IsNotExist(err) {
		if dryRun {
			// A dry run writes nothing in the instance: the fetch would create the instance
			// directory as a plain directory, not the subvolume the first render makes.
			scratch, err := os.MkdirTemp("", "dzo-pristine-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(scratch) //nolint:errcheck // scratch dir
			pristine = filepath.Join(scratch, "pristine")
		}
		if err := mission.FetchPristine(cmd.Context(), src.Git, src.Ref, src.Path, pristine); err != nil {
			return err
		}
	}
	tree, err := site.LoadTree(cfg.Paths.Site)
	if err != nil {
		return err
	}
	in := mission.RenderInput{
		PristineDir: pristine, LiveDir: inst.Paths.Live, ManifestPath: inst.Paths.Manifest,
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
		warnStaleAdminMod(cmd.ErrOrStderr(), tree, inst)
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

// checkModsUsable fails when an installed mod, or one of its files, cannot be read by this user, or
// holds nothing to load (Steam delivers an empty folder now and then). The game server skips such a
// mod and says nothing, so the instance would run without it: it must not start. The usual cause of
// an unreadable mod is a `dzo mod` command that was run as root.
func checkModsUsable(inst *resolve.Instance) error {
	var bad []string
	for _, m := range inst.Mods {
		if m.Dir == "" {
			continue
		}
		if err := modUsable(m.Dir); err != nil {
			bad = append(bad, fmt.Sprintf("the installed mod %s is not usable: %v\n  directory: %s (if root installed it, run: sudo chown -R dayz:dayz it; if it is empty, run: dzo mod refresh %s --force)",
				m.Name, err, m.Dir, strings.TrimPrefix(m.Name, "@")))
		}
	}
	if len(bad) > 0 {
		return errors.New(strings.Join(bad, "\n"))
	}
	return nil
}

// modUsable opens the mod directory and every file in its addons/, and wants some content: a PBO or
// EBO in addons/ that is not empty. A mod with a meta.cpp and no addons/ is let through (a workshop
// mod may be that); a directory with neither is an empty download.
func modUsable(dir string) error {
	top, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(top) == 0 {
		return errors.New("the directory is empty")
	}
	addons := filepath.Join(dir, "addons")
	entries, err := os.ReadDir(addons)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if _, err := os.Stat(filepath.Join(dir, "meta.cpp")); err != nil {
			return errors.New("it has neither addons/ nor meta.cpp (an empty download?)")
		}
		return nil
	case err != nil:
		return err
	}
	n := 0
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		f, err := os.Open(filepath.Join(addons, e.Name())) //nolint:gosec // inside the mod's generation in the cache
		if err != nil {
			return err
		}
		fi, err := f.Stat()
		_ = f.Close()
		if err != nil {
			return err
		}
		if fi.Size() == 0 {
			return fmt.Errorf("addons/%s is empty", e.Name())
		}
		if ext := strings.ToLower(filepath.Ext(e.Name())); ext == ".pbo" || ext == ".ebo" {
			n++
		}
	}
	if n == 0 {
		return errors.New("addons/ holds no .pbo or .ebo")
	}
	return nil
}

// warnStaleAdminMod warns when the installed dzo-admin is not the build shipped with this dzo: a mod
// of another build can read its config from another place or speak another protocol, and then sits
// idle without a word, so the instance never shows "dzo-admin connected". It only warns: an
// unpackaged mod of the operator's own is none of its business.
func warnStaleAdminMod(w io.Writer, tree *site.Tree, inst *resolve.Instance) {
	for _, m := range inst.Mods {
		if m.Local != resolve.AdminName || m.Dir == "" {
			continue
		}
		src, err := resolve.LocalSource(tree, m.Local)
		if err != nil || src.Dir == "" {
			return
		}
		if err := servermods.CheckInstalled(m.Local, m.Dir, src.Dir); err != nil {
			_, _ = fmt.Fprintf(w, "warning: the installed %s is not the build shipped with this dzo, so it may not connect: %v\n"+
				"  run: dzo mod update %s\n", m.Local, err, inst.Name)
		}
		return
	}
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
		why := "it is not on a btrfs filesystem"
		if onBtrfs, _ := btrfs.IsBtrfs(inst.Paths.Root); onBtrfs {
			why = "the directory existed before and is a plain directory (move its contents aside, remove it and render again to get a subvolume)"
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s is not a btrfs subvolume, so this instance cannot be backed up: %s\n", inst.Paths.Root, why)
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
	extra, err := runfiles.CustomKeys(cfg.Paths.Site, inst.Name, inst.Overlays)
	if err != nil {
		return runfiles.Input{}, err
	}
	keys = append(keys, extra...)
	return runfiles.Input{
		RuntimeDir: inst.Paths.Runtime, ProfilesDir: inst.Paths.Profiles, StorageDir: inst.Paths.Storage, ServerCfg: src, Priority: inst.Priority, Template: inst.Map,
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
			client, err := battleye.Dial(inst.RConAddr(), pw, battleye.WithLoginTimeout(5*time.Second))
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

// rconBind is the RConIP of the BattlEye config (see Instance.RConListen): rcon_bind, which is
// 127.0.0.1 unless the operator opens RCon to the network.
func rconBind(inst *resolve.Instance) string { return inst.RConListen() }
