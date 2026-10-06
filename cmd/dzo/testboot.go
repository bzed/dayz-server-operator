// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/boottest"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/mission"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/runfiles"
	"github.com/bzed/dayz-server-operator/internal/serve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test", Short: "Development tests that need a real game server"}
	cmd.AddCommand(newTestBootCmd())
	return cmd
}

type bootFlags struct {
	configPath, server, modsFrom, out string
	timeout, settle                   time.Duration
	keepTree, vanilla, asJSON         bool
	expect                            []string
}

func newTestBootCmd() *cobra.Command {
	var f bootFlags
	cmd := &cobra.Command{
		Use:   "boot <instance>",
		Short: "Boot a real DayZServer on what a render of the instance produces, and check the logs (development only)",
		Long: "Builds a disposable server tree from the instance's staged mission, serverDZ.cfg, keys and mods, boots the " +
			"native Linux DayZServer on free ports until it answers the Steam query, stops it, and checks the logs: " +
			"script compile errors, script modules that did not load (against a vanilla baseline), engine errors, " +
			"--expect patterns and dzo-admin's first contact. Exit code 0 passed, 1 failed, 2 the test itself could not run.\n\n" +
			"It never runs on a host where dzo manages instances, and has no override. The server is the DayZ Server tool " +
			"installed by the Steam client (--server steam) or a directory (--server <dir>); nothing is ever written into it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runBoot(cmd, args[0], f)
			if err == nil {
				return nil
			}
			if _, ok := err.(*checkExitErr); ok { //nolint:errorlint // our own type, never wrapped
				return err
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "error: %v\n", err)
			return &checkExitErr{code: 2}
		},
	}
	configFlag(cmd, &f.configPath)
	cmd.Flags().StringVar(&f.server, "server", "steam", "the server install: steam (the DayZ Server tool of the Steam client) or a directory")
	cmd.Flags().StringVar(&f.modsFrom, "mods-from", "", "where the mods come from: cache (dzo's cache), steam-client (the client's workshop directory), or a directory of @Mod folders (default steam-client with --server steam, else cache)")
	cmd.Flags().DurationVar(&f.timeout, "timeout", 3*time.Minute, "how long the server may take to answer the Steam query")
	cmd.Flags().DurationVar(&f.settle, "settle", 20*time.Second, "how long to let it run after the mission has loaded (with dzo-admin, it then waits for the mod to make contact)")
	cmd.Flags().BoolVar(&f.keepTree, "keep-tree", false, "keep the server tree afterwards")
	cmd.Flags().BoolVar(&f.vanilla, "vanilla", false, "boot the unmodded server mission and record the baseline for this server build")
	cmd.Flags().StringArrayVar(&f.expect, "expect", nil, "a regular expression that must appear in the script log or the RPT (repeatable)")
	cmd.Flags().StringVar(&f.out, "out", "", "copy the logs here (default ./boottest-<instance>-<time>)")
	cmd.Flags().BoolVar(&f.asJSON, "json", false, "print the report as JSON")
	return cmd
}

func runBoot(cmd *cobra.Command, name string, f bootFlags) error {
	cfg, inst, err := loadInstance(f.configPath, name)
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	cfgDir, _ := os.UserConfigDir()
	if err := boottest.Guard(cfg.Paths.Instances, filepath.Join(cfgDir, "containers", "systemd")); err != nil {
		return err
	}
	libs := boottest.Libraries(boottest.SteamRoots(home))
	serverDir := f.server
	if f.server == "steam" {
		s, err := boottest.FindSteamServer(libs, inst.Product.AppID)
		if err != nil {
			return err
		}
		serverDir = s.Dir
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "server: %s (build %s)\n", s.Dir, s.Build)
	}
	if f.modsFrom == "" {
		f.modsFrom = "cache"
		if f.server == "steam" {
			f.modsFrom = "steam-client"
		}
	}

	cacheHome, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	run := filepath.Join(cacheHome, "dzo", "boottest", time.Now().Format("20060102-150405")+"-"+runfiles.Hex(3))
	if err := os.MkdirAll(run, 0o750); err != nil {
		return err
	}
	if !f.keepTree {
		defer os.RemoveAll(run) //nolint:errcheck // scratch
	} else {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "tree kept in %s\n", run)
	}

	bc := boottest.Config{
		ServerDir: serverDir, TreeDir: filepath.Join(run, "tree"), Template: inst.Map,
		Timeout: f.timeout, Settle: f.settle, Expect: f.expect, Log: cmd.ErrOrStderr(),
	}
	blPath, err := boottest.BaselinePath(cfg.Paths.Cache, inst.Product.Name, serverDir)
	if err != nil {
		return err
	}
	if f.vanilla {
		return bootVanilla(cmd, bc, serverDir, blPath)
	}
	if err := prepareBoot(cmd, &bc, cfg, inst, serverDir, libs, run, f.modsFrom); err != nil {
		return err
	}
	if bl, err := boottest.LoadBaseline(blPath); err == nil {
		bc.Baseline = bl
	}
	res, err := boottest.Run(cmd.Context(), bc)
	if err != nil {
		return err
	}
	return report(cmd, res, f, name)
}

// prepareBoot fills the config from the instance: the staged mission, the
// site's serverDZ.cfg, the keys and the mods.
func prepareBoot(cmd *cobra.Command, bc *boottest.Config, cfg *config.Config, inst *resolve.Instance, serverDir string, libs []string, run, modsFrom string) error {
	src := inst.Mission.Source
	pristine := filepath.Join(run, "pristine")
	if err := mission.FetchPristine(cmd.Context(), src.Git, src.Ref, src.Path, pristine); err != nil {
		return err
	}
	in := mission.RenderInput{PristineDir: pristine}
	if inst.Mission.Fallback != "" {
		in.FallbackDir = filepath.Join(serverDir, "mpmissions", inst.Mission.Fallback)
	}
	tree, err := site.LoadTree(cfg.Paths.Site)
	if err != nil {
		return err
	}
	if err := addIntegrations(cmd, &in, cfg, tree, inst); err != nil {
		return err
	}
	staging, err := mission.BuildStaging(in)
	if err != nil {
		return err
	}
	bc.MissionDir = staging
	scfg, err := os.ReadFile(filepath.Join(cfg.Paths.Site, "instances", inst.Name, "serverDZ.cfg")) //nolint:gosec // the site checkout
	if err != nil {
		return fmt.Errorf("instances/%s/serverDZ.cfg is missing in the site repo: %w", inst.Name, err)
	}
	bc.ServerCfg = scfg
	bc.Params = inst.Params.Extra

	var clientDirs []string
	bc.ModScripts = map[string]int{}
	for _, m := range inst.Mods {
		dir, err := modDir(m, libs, inst.Product.WorkshopAppID, modsFrom)
		if err != nil {
			return err
		}
		bc.Mods = append(bc.Mods, boottest.Mod{Name: m.Name, Dir: dir, Server: m.Server})
		if !m.Server {
			clientDirs = append(clientDirs, dir)
		}
		counts, err := boottest.ModScripts(dir)
		if err != nil {
			return err
		}
		for k, v := range counts {
			bc.ModScripts[k] += v
		}
	}
	if bc.Keys, err = runfiles.Keys(serverDir, clientDirs); err != nil {
		return err
	}
	if inst.AdminEnabled() {
		mc, err := serve.ModConfig(cfg, inst)
		if err != nil {
			return err
		}
		bc.Admin = &mc
	}
	return nil
}

// modDir finds a mod's directory. Local servermods (dzo-admin) always come
// from dzo's cache unless --mods-from names a directory.
func modDir(m resolve.Mod, libs []string, workshopApp uint32, from string) (string, error) {
	switch {
	case from == "cache" || (from == "steam-client" && m.Local != ""):
		if m.Dir == "" {
			return "", fmt.Errorf("mod %s is not in dzo's cache: run dzo mod add/update first", m.Name)
		}
		return m.Dir, nil
	case from == "steam-client":
		return boottest.WorkshopMod(libs, workshopApp, m.ID)
	}
	key := m.Name
	for _, cand := range []string{m.Name, strconv.FormatUint(m.ID, 10), m.Local} {
		if cand == "" {
			continue
		}
		if fi, err := os.Stat(filepath.Join(from, cand)); err == nil && fi.IsDir() {
			return filepath.Join(from, cand), nil
		}
	}
	return "", fmt.Errorf("mod %s: no directory %s in %s", key, m.Name, from)
}

func bootVanilla(cmd *cobra.Command, bc boottest.Config, serverDir, blPath string) error {
	bc.MissionDir = filepath.Join(serverDir, "mpmissions", bc.Template)
	scfg, err := os.ReadFile(filepath.Join(serverDir, "serverDZ.cfg")) //nolint:gosec // the server install, read only
	if err != nil {
		return err
	}
	bc.ServerCfg = scfg
	var kerr error
	if bc.Keys, kerr = runfiles.Keys(serverDir, nil); kerr != nil {
		return kerr
	}
	res, err := boottest.Run(cmd.Context(), bc)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprint(cmd.OutOrStdout(), boottest.Text(res.Checks))
	if boottest.Failed(res.Checks) {
		return &checkExitErr{code: 1}
	}
	bl := boottest.RecordBaseline(res.Profiles)
	if err := bl.Save(blPath); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "baseline recorded in %s (Game %d, World %d, Mission %d, %d noise lines)\n", blPath, bl.Modules["Game"], bl.Modules["World"], bl.Modules["Mission"], len(bl.Noise))
	return nil
}

func report(cmd *cobra.Command, res *boottest.Result, f bootFlags, name string) error {
	out := f.out
	if out == "" {
		out = "boottest-" + name + "-" + time.Now().Format("20060102-150405")
	}
	if err := copyLogs(res.Profiles, out); err != nil {
		return err
	}
	if f.asJSON {
		b, _ := json.MarshalIndent(map[string]any{"checks": res.Checks, "ports": res.Ports, "failed": boottest.Failed(res.Checks), "logs": out}, "", "  ")
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(b))
	} else {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), boottest.Text(res.Checks))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "logs: %s\n", out)
	}
	if boottest.Failed(res.Checks) {
		return &checkExitErr{code: 1}
	}
	return nil
}

// copyLogs copies the profiles' logs (not the mod data) to out.
func copyLogs(profiles, out string) error {
	if err := os.MkdirAll(out, 0o750); err != nil {
		return err
	}
	for _, pat := range []string{"*.log", "*.RPT", "*.ADM", "*.mdmp"} {
		files, _ := filepath.Glob(filepath.Join(profiles, pat))
		for _, f := range files {
			b, err := os.ReadFile(f) //nolint:gosec // the test tree
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(out, filepath.Base(f)), b, 0o600); err != nil { //nolint:gosec // out is the operator's own --out directory, the name comes from a glob of the test tree
				return err
			}
		}
	}
	return nil
}
