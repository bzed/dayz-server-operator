// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package serve runs `dzo serve`: the /api/v1 listener and the endpoint the
// dzo-admin mods call, over hubs built from the site config. It also derives
// what `dzo instance render` writes into the mod's config.json.
package serve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// gatewayHost is how a container with published (pasta) networking reaches
// the host; unverified against the target podman (see the README).
const gatewayHost = "host.containers.internal"

// TokenDir is where the per-instance mod tokens live.
func TokenDir(cfg *config.Config) string { return filepath.Join(cfg.Paths.Secrets, "admin") }

// APITokens is the API token file.
func APITokens(cfg *config.Config) string { return filepath.Join(cfg.Paths.Secrets, "api-tokens.json") }

// AuditLog is the audit log file.
func AuditLog(cfg *config.Config) string { return filepath.Join(cfg.Paths.DB, "audit.jsonl") }

// TilesDir is where the built map tile sets live.
func TilesDir(cfg *config.Config) string { return filepath.Join(cfg.Paths.Cache, "maptiles") }

// ModEndpoint is the URL an instance's mod posts to (with a trailing slash,
// as RestApi wants it).
func ModEndpoint(cfg *config.Config, inst *resolve.Instance) (string, error) {
	_, port, err := net.SplitHostPort(cfg.Serve.ModListen)
	if err != nil {
		return "", err
	}
	host := "127.0.0.1"
	if inst.Network == site.NetworkPublish {
		host = gatewayHost
	}
	return fmt.Sprintf("http://%s/", net.JoinHostPort(host, port)), nil
}

// ModConfig builds the mod's config.json content (without the token, which
// WriteModConfig generates) for an instance.
func ModConfig(cfg *config.Config, inst *resolve.Instance) (admin.ModConfig, error) {
	endpoint, err := ModEndpoint(cfg, inst)
	if err != nil {
		return admin.ModConfig{}, err
	}
	a := inst.Admin
	mc := admin.ModConfig{
		Endpoint: endpoint, SyncMS: a.SyncMS, PlayersS: a.PlayersS, VehiclesS: a.VehiclesS, MarkersS: a.MarkersS, EventsS: a.EventsS,
		AllowSpawn: a.AllowSpawn == nil || *a.AllowSpawn, AllowClasses: a.AllowClasses, DenyClasses: a.DenyClasses, Watch: watchRules(inst),
	}
	return mc, nil
}

// watchRules are the watch rules of an instance: its own, plus the shipped rules for the events of the
// Central Economy unless the instance turns them off. The mod gets them as its rules and the hub styles
// the markers with them (colour, and which role sees them), so both must use this list.
func watchRules(inst *resolve.Instance) []admin.WatchRule {
	if inst.AdminMap.VanillaEvents == nil || *inst.AdminMap.VanillaEvents {
		return site.MergeWatch(admin.VanillaEventWatch(), inst.AdminMap.Watch)
	}
	return inst.AdminMap.Watch
}

// WriteModConfig writes the mod's config.json and rotates its token.
func WriteModConfig(cfg *config.Config, inst *resolve.Instance) error {
	mc, err := ModConfig(cfg, inst)
	if err != nil {
		return err
	}
	return admin.WriteModConfig(inst.Paths.Profiles, TokenDir(cfg), inst.Name, mc)
}

// AddHubs registers a hub for every instance of the site. It can run again
// after a site change: existing hubs keep their state.
func AddHubs(reg *admin.Registry, cfg *config.Config, tree *site.Tree) error {
	all, err := resolve.ResolveAll(cfg, tree)
	if err != nil {
		return err
	}
	for _, inst := range all {
		h := admin.NewHub(inst.Name)
		h.Allow, h.Deny, h.MarkerDir = inst.Admin.AllowClasses, inst.Admin.DenyClasses, admin.MarkerDir(inst.Paths.Profiles)
		if sched := inst.Restarts.Schedule; len(sched) > 0 {
			h.NextRestart = func(after time.Time) time.Time { return site.NextRestart(sched, after) }
		}
		h.Styles = map[string]admin.Layer{}
		for _, w := range watchRules(inst) {
			h.Styles[w.Layer] = admin.Layer{Name: w.Layer, Icon: w.Icon, Color: w.Color, Visibility: w.Visibility}
		}
		reg.Add(h)
	}
	return nil
}

// Options are the parts of Run the caller can replace in tests.
type Options struct {
	Version string
	// Load returns the current site tree (called at start and on every
	// signal on Reload).
	Load   func() (*site.Tree, error)
	Reload <-chan struct{}
	// Ready is called once both listeners are bound.
	Ready func(apiAddr, modAddr string)
	Logf  func(format string, args ...any)
}

func newServer(h http.Handler) *http.Server {
	return &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute, IdleTimeout: 2 * time.Minute}
}

// Run serves until ctx is done.
func Run(ctx context.Context, cfg *config.Config, o Options) error {
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	reg := admin.NewRegistry(TokenDir(cfg))
	tree, err := o.Load()
	if err != nil {
		return err
	}
	if err := AddHubs(reg, cfg, tree); err != nil {
		return err
	}
	apiSrv := api.New(cfg.Serve.Installation, o.Version, reg, api.NewTokenStore(APITokens(cfg)), admin.NewAudit(AuditLog(cfg)))
	apiSrv.TilesDir = TilesDir(cfg)
	// The API serves with write timeouts off: the stream stays open.
	apiHTTP, modHTTP := newServer(apiSrv.Handler()), newServer(reg.ModHandler())
	modHTTP.WriteTimeout = time.Minute
	// Open streams end when ctx does, so a shutdown does not wait for them.
	apiHTTP.BaseContext = func(net.Listener) context.Context { return ctx }

	apiLn, err := net.Listen("tcp", cfg.Serve.Listen)
	if err != nil {
		return err
	}
	modLn, err := net.Listen("tcp", cfg.Serve.ModListen)
	if err != nil {
		_ = apiLn.Close()
		return err
	}
	if o.Ready != nil {
		o.Ready(apiLn.Addr().String(), modLn.Addr().String())
	}

	errc := make(chan error, 2)
	go func() { errc <- apiHTTP.Serve(apiLn) }()
	go func() { errc <- modHTTP.Serve(modLn) }()
	for {
		select {
		case <-o.Reload:
			t, err := o.Load()
			if err == nil {
				err = AddHubs(reg, cfg, t)
			}
			if err != nil {
				logf("reload failed, keeping the old instances: %v", err)
				continue
			}
			logf("site reloaded")
		case err := <-errc:
			_ = apiHTTP.Close()
			_ = modHTTP.Close()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ctx.Done():
			sd, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = apiHTTP.Shutdown(sd)
			_ = modHTTP.Shutdown(sd)
			return nil
		}
	}
}
