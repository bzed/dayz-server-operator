// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/product"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/servermods"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// steamDetails looks up workshop file details; tests replace it.
var steamDetails = (&product.FileDetailsClient{}).GetFileDetails

// installFlags are the flags every command that runs steamcmd shares.
type installFlags struct {
	config, steamcmd string
	ignoreCompat     bool
}

func (f *installFlags) add(cmd *cobra.Command) {
	configFlag(cmd, &f.config)
	cmd.Flags().StringVar(&f.steamcmd, "steamcmd", "steamcmd", "steamcmd binary to run")
	cmd.Flags().BoolVar(&f.ignoreCompat, "ignore-compat", false, "use a shipped servermod even if its PBOs differ from the tested set in compat.yaml")
}

func (f *installFlags) installer(cfg *config.Config) (*product.Installer, error) {
	if cfg.Steam.Account == "" {
		return nil, errors.New("steam.account is not set in config.yaml")
	}
	return &product.Installer{CacheRoot: cfg.Paths.Cache, Command: f.steamcmd, Account: cfg.Steam.Account, Details: steamDetails}, nil
}

// modSet is the mods to act on: workshop ids per workshop app, and local mods.
type modSet struct {
	workshop map[uint32][]uint64
	local    []string
}

// collectMods gathers the distinct mods of one instance (or of every
// instance when name is ""), optionally narrowed to refs ("<id>" or a local
// mod name); a ref no instance uses is an error.
func collectMods(cfg *config.Config, t *site.Tree, name string, refs []string) (modSet, error) {
	names := t.InstanceNames()
	if name != "" {
		if _, err := t.Instance(name); err != nil {
			return modSet{}, err
		}
		names = []string{name}
	}
	want := map[string]bool{}
	for _, r := range refs {
		want[r] = false
	}
	seen := map[string]bool{}
	set := modSet{workshop: map[uint32][]uint64{}}
	for _, n := range names {
		inst := t.Instances[n]
		app := cfg.Products[inst.Product].WorkshopAppID
		for _, m := range inst.Mods {
			key := m.Key()
			if _, filtered := want[key]; len(refs) > 0 && !filtered {
				continue
			}
			want[key] = true
			if id := fmt.Sprintf("%d/%s", app, key); !seen[id] {
				seen[id] = true
				if m.Local != "" {
					set.local = append(set.local, m.Local)
				} else {
					set.workshop[app] = append(set.workshop[app], m.ID)
				}
			}
		}
	}
	for r, used := range want {
		if !used {
			return modSet{}, fmt.Errorf("mod %s is not used by any instance", r)
		}
	}
	sort.Strings(set.local)
	return set, nil
}

// installMods runs the installer over set and prints one line per mod.
func installMods(cmd *cobra.Command, f *installFlags, cfg *config.Config, t *site.Tree, set modSet, force bool) error {
	in, err := f.installer(cfg)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	failed := 0
	report := func(key string, r product.InstallResult) {
		switch {
		case r.Err != nil:
			failed++
			_, _ = fmt.Fprintf(out, "%-14s FAILED: %v\n", key, r.Err)
		case r.Changed:
			_, _ = fmt.Fprintf(out, "%-14s installed %s\n", key, r.Generation)
			postDownloadHooks(cmd, cfg, t, key, r.Generation)
		default:
			_, _ = fmt.Fprintf(out, "%-14s up to date (%s)\n", key, r.Generation)
		}
	}
	for _, app := range slices.Sorted(maps.Keys(set.workshop)) {
		ids := set.workshop[app]
		results, err := in.InstallMods(cmd.Context(), app, ids, force)
		if err != nil {
			return err
		}
		for i, r := range results {
			report(strconv.FormatUint(ids[i], 10), r)
		}
	}
	for _, name := range set.local {
		src, err := resolve.LocalSource(t, name)
		if err != nil {
			return err
		}
		if !f.ignoreCompat {
			src.Check = servermods.CheckShipped
		}
		r, err := in.ImportLocal(cmd.Context(), name, src)
		r.Err = err
		report(name, r)
	}
	if failed > 0 {
		return fmt.Errorf("%d mod(s) failed", failed)
	}
	return nil
}

func newModListCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "list [instance]",
		Short: "List the mods of an instance (or of every instance) and their installed generation",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, tree, err := loadSite(configPath)
			if err != nil {
				return err
			}
			names := tree.InstanceNames()
			if len(args) == 1 {
				if _, err := tree.Instance(args[0]); err != nil {
					return err
				}
				names = args
			}
			for _, n := range names {
				inst, err := resolve.Resolve(cfg, tree, n)
				if err != nil {
					return err
				}
				for _, m := range inst.Mods {
					gen := m.Generation
					if gen == "" {
						gen = "not installed"
					}
					side := "client"
					if m.Server {
						side = "server"
					}
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", n, m.Name, side, gen)
				}
			}
			return nil
		},
	}
	configFlag(cmd, &configPath)
	return cmd
}

func newModAddCmd() *cobra.Command {
	var f installFlags
	var instName string
	var server bool
	cmd := &cobra.Command{
		Use:   "add <workshop id | local name> --instance <name>",
		Short: "Install a mod and add it to an instance's mod list in the site repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, tree, err := loadSite(f.config)
			if err != nil {
				return err
			}
			if _, err := tree.Instance(instName); err != nil {
				return err
			}
			ref := site.ModRef{Server: server}
			if id, err := strconv.ParseUint(args[0], 10, 64); err == nil {
				ref.ID = id
			} else {
				ref.Local = args[0]
			}
			if err := ref.Validate(); err != nil {
				return err
			}
			// Install first, so a failed download never leaves the config pointing at a missing mod.
			app := cfg.Products[tree.Instances[instName].Product].WorkshopAppID
			set := modSet{workshop: map[uint32][]uint64{}}
			if ref.Local != "" {
				set.local = []string{ref.Local}
			} else {
				set.workshop[app] = []uint64{ref.ID}
			}
			if err := installMods(cmd, &f, cfg, tree, set, false); err != nil {
				return err
			}
			return site.AddMod(filepath.Join(tree.Dir, "instances", instName, "instance.yaml"), ref)
		},
	}
	f.add(cmd)
	cmd.Flags().StringVar(&instName, "instance", "", "instance to add the mod to (required)")
	cmd.Flags().BoolVar(&server, "server", false, "load it with -servermod (always on for local mods)")
	_ = cmd.MarkFlagRequired("instance")
	return cmd
}

func newModUpdateCmd() *cobra.Command {
	var f installFlags
	cmd := &cobra.Command{
		Use:   "update [instance]",
		Short: "Download new mod versions as new generations (running servers are not touched)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, tree, err := loadSite(f.config)
			if err != nil {
				return err
			}
			var name string
			if len(args) == 1 {
				name = args[0]
			}
			set, err := collectMods(cfg, tree, name, nil)
			if err != nil {
				return err
			}
			return installMods(cmd, &f, cfg, tree, set, false)
		},
	}
	f.add(cmd)
	return cmd
}

func newModRefreshCmd() *cobra.Command {
	var f installFlags
	var instName string
	var all, force bool
	cmd := &cobra.Command{
		Use:   "refresh <workshop id | local name>... | --all [--instance <name>] --force",
		Short: "Wipe steamcmd's state for mods and download them again as new generations",
		Long: "For a broken or incomplete download, or a bad steamcmd cache. Downloads again even when " +
			"Steam's time_updated is unchanged and stores the result as a new generation; the old one stays " +
			"until nothing uses it. Servers switch at their next restart.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !force {
				return errors.New("refresh downloads again regardless of Steam's version: pass --force")
			}
			if all == (len(args) > 0) {
				return errors.New("give mod ids/names, or --all")
			}
			cfg, tree, err := loadSite(f.config)
			if err != nil {
				return err
			}
			set, err := collectMods(cfg, tree, instName, args)
			if err != nil {
				return err
			}
			return installMods(cmd, &f, cfg, tree, set, true)
		},
	}
	f.add(cmd)
	cmd.Flags().StringVar(&instName, "instance", "", "only mods of this instance")
	cmd.Flags().BoolVar(&all, "all", false, "refresh every mod (of --instance, or of all instances)")
	cmd.Flags().BoolVar(&force, "force", false, "confirm the forced download")
	return cmd
}

func newProductInstallCmd() *cobra.Command {
	return newProductCmdFor("install", "First download of a product's server build", false)
}

func newProductUpdateCmd() *cobra.Command {
	return newProductCmdFor("update", "Download a new server build (manual only, never automatic)", true)
}

func newProductCmdFor(verb, short string, update bool) *cobra.Command {
	var f installFlags
	var force bool
	cmd := &cobra.Command{
		Use:   verb + " <product>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(f.config)
			if err != nil {
				return err
			}
			p, ok := cfg.Products[args[0]]
			if !ok {
				return fmt.Errorf("unknown product %q", args[0])
			}
			in, err := f.installer(cfg)
			if err != nil {
				return err
			}
			cur, err := product.ProductStore(cfg.Paths.Cache, args[0]).Current()
			if err != nil {
				return err
			}
			switch {
			case !update && cur != "":
				return fmt.Errorf("product %s is installed (build %s): use `dzo product update`", args[0], cur)
			case update && cur == "":
				return fmt.Errorf("product %s is not installed: use `dzo product install`", args[0])
			}
			r, err := in.InstallProduct(cmd.Context(), args[0], p, force)
			if err != nil {
				return err
			}
			msg := "installed"
			if !r.Changed {
				msg = "already stored"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: build %s %s (now current)\n", args[0], r.Generation, msg)
			return nil
		},
	}
	f.add(cmd)
	if update {
		cmd.Flags().BoolVar(&force, "force", false, "store the current build again as <build>-r<n> after re-validating it")
	}
	return cmd
}
