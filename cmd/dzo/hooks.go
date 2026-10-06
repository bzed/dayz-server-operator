// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/hooks"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// hookTimeout bounds one hook script; a hook that hangs would hang the start or the restart.
const hookTimeout = 10 * time.Minute

// Hook points (§C11) and whether a failing script aborts the operation that fired them.
// A hook that runs after the fact (post_stop, post_update, post_download) can only be reported.
const (
	hookPreStart     = "pre_start"
	hookPostStop     = "post_stop"
	hookPreUpdate    = "pre_update"
	hookPostUpdate   = "post_update"
	hookPostDownload = "post_download"
	hookPostMerge    = "post_merge"
	hookPostRender   = "post_render"
)

var hookAborts = map[string]bool{
	hookPreStart: true, hookPreUpdate: true, hookPostMerge: true, hookPostRender: true,
}

// hookScripts returns the scripts an instance configured for a hook point.
func hookScripts(h site.HooksConfig, point string) ([]string, error) {
	switch point {
	case hookPreStart:
		return h.PreStart, nil
	case hookPostStop:
		return h.PostStop, nil
	case hookPreUpdate:
		return h.PreUpdate, nil
	case hookPostUpdate:
		return h.PostUpdate, nil
	case hookPostDownload:
		return h.PostDownload, nil
	case hookPostMerge:
		return h.PostMerge, nil
	case hookPostRender:
		return h.PostRender, nil
	case "post_backup":
		return h.PostBackup, nil
	}
	return nil, fmt.Errorf("unknown hook point %q", point)
}

// runHooks runs the scripts an instance configured for point, in the instance's directory of the
// site repository, with the environment of the hook contract (DZO_INSTANCE, DZO_MAP,
// DZO_LIVE_MISSION, plus env). Each script's output goes to out. It returns an error when a script
// fails and the hook point is one that aborts; for the others a failure is printed and returns nil.
func runHooks(ctx context.Context, out io.Writer, cfg *config.Config, h site.HooksConfig, name, mapName, point string, env map[string]string, extra map[string]any) error {
	scripts, err := hookScripts(h, point)
	if err != nil {
		return err
	}
	if len(scripts) == 0 {
		return nil
	}
	e := map[string]string{"DZO_MAP": mapName, "DZO_LIVE_MISSION": filepath.Join(cfg.Paths.Instances, name, "mpmissions", mapName)}
	for k, v := range env {
		e[k] = v
	}
	r := &hooks.Runner{Dir: filepath.Join(cfg.Paths.Site, "instances", name), Env: e, Timeout: hookTimeout}
	abort := hookAborts[point]
	var failed error
	for _, res := range hooks.RunAll(ctx, r, scripts, hooks.Context{Instance: name, Point: point, Extra: extra}, abort) {
		if res.Stdout != "" {
			_, _ = fmt.Fprint(out, res.Stdout)
		}
		if res.Err != nil {
			_, _ = fmt.Fprintf(out, "hook %s (%s): %v\n", point, res.Script, res.Err)
			if failed == nil {
				failed = fmt.Errorf("%s hook %s failed", point, res.Script)
			}
		}
	}
	if abort {
		return failed
	}
	return nil
}

// instanceHooks runs a hook point of a resolved instance.
func instanceHooks(ctx context.Context, out io.Writer, cfg *config.Config, inst *resolve.Instance, point string, env map[string]string, extra map[string]any) error {
	return runHooks(ctx, out, cfg, inst.Hooks, inst.Name, inst.Map, point, env, extra)
}

// newInstanceHookCmd is what the units run for pre_start (ExecStartPre) and post_stop (ExecStopPost).
func newInstanceHookCmd() *cobra.Command {
	var configPath string
	c := &cobra.Command{
		Use:   "hook <name> <point>",
		Short: "Run the hook scripts of an instance for a hook point (pre_start, post_stop, ...)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, inst, err := loadInstance(configPath, args[0])
			if err != nil {
				return err
			}
			return instanceHooks(cmd.Context(), cmd.OutOrStdout(), cfg, inst, args[1], nil, nil)
		},
	}
	configFlag(c, &configPath)
	return c
}

// postDownloadHooks runs the post_download hooks of every instance that uses the mod key (a workshop
// id or a local mod name) after a new generation of it was installed. The hook gets the mod and
// the generation in its context; a failure is printed, the installed generation stays.
func postDownloadHooks(cmd *cobra.Command, cfg *config.Config, t *site.Tree, key, generation string) {
	for _, name := range t.InstanceNames() {
		raw := t.Instances[name]
		uses := false
		for _, m := range raw.Mods {
			if m.Key() == key {
				uses = true
			}
		}
		if !uses {
			continue
		}
		_ = runHooks(cmd.Context(), cmd.OutOrStdout(), cfg, raw.Hooks, name, raw.Map, hookPostDownload, nil, map[string]any{"mod": key, "generation": generation})
	}
}
