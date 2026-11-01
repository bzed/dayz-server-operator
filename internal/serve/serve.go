// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package serve runs `dzo serve`: the /api/v1 listener and the endpoint the
// dzo-admin mods call, over hubs built from the site config. It also derives
// what `dzo instance render` writes into the mod's config.json.
package serve

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/backup"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/geoip"
	"github.com/bzed/dayz-server-operator/internal/killfeed"
	"github.com/bzed/dayz-server-operator/internal/moderation"
	"github.com/bzed/dayz-server-operator/internal/notify"
	"github.com/bzed/dayz-server-operator/internal/presence"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/bzed/dayz-server-operator/internal/tlsfiles"
	"github.com/bzed/dayz-server-operator/internal/vehicles"
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

// FeedLogPath is the on-disk copy of an instance's event feed.
func FeedLogPath(cfg *config.Config, instance string) string {
	return filepath.Join(cfg.Paths.DB, "feed", instance+".jsonl")
}

// BanDB is the moderation database (bans, evidence, sightings).
func BanDB(cfg *config.Config) string { return filepath.Join(cfg.Paths.DB, "dzo.sqlite") }

// DBBackupDir is where the regular copies of the database are written: <paths.db>/backup.
func DBBackupDir(cfg *config.Config) string { return backup.DBBackupDir(cfg) }

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
		AllowSpawn: a.AllowSpawn == nil || *a.AllowSpawn, MoveOccupiedMax: moveOccupiedMax(a.MoveOccupiedMax), AllowClasses: a.AllowClasses, DenyClasses: a.DenyClasses, Watch: watchRules(inst),
	}
	return mc, nil
}

// vehicleKinds are the classes of an instance's bicycles and helicopters: the shipped ones and its own.
func vehicleKinds(inst *resolve.Instance) map[string][]string {
	kinds := admin.DefaultVehicleKinds()
	for k, classes := range inst.Admin.VehicleKinds {
		kinds[k] = append(kinds[k], classes...)
	}
	return kinds
}

// watchRules are the watch rules of an instance: its own, plus the shipped rules for the events of the
// Central Economy unless the instance turns them off. The mod gets them as its rules and the hub styles
// the markers with them (colour, and which role sees them), so both must use this list.
// moveOccupiedMax is admin.move_occupied_max: unset means 200 m.
func moveOccupiedMax(v *int) int {
	if v == nil {
		return 200
	}
	return *v
}

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
// after a site change: existing hubs keep their state, and the hubs of
// instances no longer in the site are dropped.
func AddHubs(reg *admin.Registry, cfg *config.Config, tree *site.Tree) error {
	all, err := resolve.ResolveAll(cfg, tree)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(all))
	for _, inst := range all {
		names = append(names, inst.Name)
		h := admin.NewHub(inst.Name)
		h.Allow, h.Deny, h.Kinds, h.MarkerDir = inst.Admin.AllowClasses, inst.Admin.DenyClasses, vehicleKinds(inst), admin.MarkerDir(inst.Paths.Profiles)
		if sched := inst.Restarts.Schedule; len(sched) > 0 {
			h.NextRestart = func(after time.Time) time.Time { return site.NextRestart(sched, after) }
		}
		h.Styles = map[string]admin.Layer{}
		for _, w := range watchRules(inst) {
			h.Styles[w.Layer] = admin.Layer{Name: w.Layer, Icon: w.Icon, Color: w.Color, Visibility: w.Visibility}
		}
		h = reg.Add(h)
		if err := h.UseFeedLog(&admin.FeedLog{Path: FeedLogPath(cfg, inst.Name)}); err != nil {
			return fmt.Errorf("instance %s: %w", inst.Name, err)
		}
	}
	reg.Retain(names)
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
	// Lookups asks BattlEye's master and Steam about the players of the player history (cmd/dzo sets it;
	// tests leave it off so that nothing talks to the internet).
	Lookups bool
	// Notify posts the killfeed to Discord (cmd/dzo passes the notifier of config.yaml); nil posts nothing.
	Notify func(context.Context, notify.Event) error
}

func newServer(h http.Handler) *http.Server {
	return &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute, IdleTimeout: 2 * time.Minute}
}

// BanInstances are the instances whose bans serve enforces. An instance without an RCon password yet
// (never rendered) is skipped: the start path renders its bans.txt, and serve picks it up after the
// next site reload.
func BanInstances(cfg *config.Config, tree *site.Tree, logf func(string, ...any)) ([]moderation.Instance, error) {
	all, err := resolve.ResolveAll(cfg, tree)
	if err != nil {
		return nil, err
	}
	var out []moderation.Instance
	for _, inst := range all {
		b, err := os.ReadFile(filepath.Join(cfg.Paths.Secrets, "rcon", inst.Name)) //nolint:gosec // dzo's own secrets directory
		if errors.Is(err, os.ErrNotExist) {
			logf("bans: %s has no RCon password yet; enforcing after its first render and a reload", inst.Name)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("bans: %s: reading the RCon password: %w", inst.Name, err)
		}
		out = append(out, moderation.Instance{Name: inst.Name, BEDir: filepath.Join(inst.Paths.Profiles, "battleye"),
			RConAddr: inst.RConAddr(), RConPass: strings.TrimSpace(string(b))})
	}
	return out, nil
}

// KillfeedConfigs are the killfeeds of the instances that have one.
func KillfeedConfigs(cfg *config.Config, tree *site.Tree) ([]killfeed.Config, error) {
	all, err := resolve.ResolveAll(cfg, tree)
	if err != nil {
		return nil, err
	}
	var out []killfeed.Config
	for _, inst := range all {
		k := inst.Killfeed
		if len(k.Discord) == 0 {
			continue
		}
		out = append(out, killfeed.Config{Instance: inst.Name, Targets: k.Discord, Delay: k.Delay.Std(),
			AIKillers: k.AIKillers == nil || *k.AIKillers, AIVictims: k.AIVictims, Deaths: k.Deaths})
	}
	return out, nil
}

// forgetDays holds admin.vehicle_forget_days of every instance, replaced on a reload.
type forgetDays struct {
	mu sync.Mutex
	by map[string]time.Duration
}

func (f *forgetDays) set(cfg *config.Config, tree *site.Tree) {
	all, err := resolve.ResolveAll(cfg, tree)
	if err != nil {
		return // keep the old values; the reload reports the error itself
	}
	by := map[string]time.Duration{}
	for _, inst := range all {
		by[inst.Name] = vehicles.ForgetAfter(inst.Admin)
	}
	f.mu.Lock()
	f.by = by
	f.mu.Unlock()
}

func (f *forgetDays) get(name string) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.by[name]; ok {
		return d
	}
	return vehicles.DefaultForgetDays * 24 * time.Hour
}

// Run serves until ctx is done.
func Run(ctx context.Context, cfg *config.Config, o Options) error {
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	reg := admin.NewRegistry(TokenDir(cfg))
	reg.Logf = logf
	tree, err := o.Load()
	if err != nil {
		return err
	}
	if err := AddHubs(reg, cfg, tree); err != nil {
		return err
	}
	reg.LogTokens()
	go reg.Watch(ctx)
	kctx, kcancel := context.WithCancel(ctx)
	kf := &killfeed.Manager{Notify: o.Notify, Logf: logf}
	defer func() { kcancel(); kf.Wait() }()
	if cfgs, err := KillfeedConfigs(cfg, tree); err != nil {
		logf("killfeed: %v", err)
	} else {
		kf.Set(kctx, reg, cfgs)
	}
	msgs, err := startMessengers(ctx, cfg, tree, reg, logf)
	if err != nil {
		return err
	}
	defer func() { msgs.stop() }()
	audit := admin.NewAudit(AuditLog(cfg))
	store, err := moderation.Open(ctx, BanDB(cfg))
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }() // after the Service has stopped (deferred below, runs first)
	mctx, mcancel := context.WithCancel(ctx)
	svc := moderation.NewService(store, audit, logf)
	svc.AddressRetention, svc.VacuumEvery, svc.SessionRetention = cfg.Serve.IPRetention(), cfg.Serve.VacuumEvery(), cfg.Serve.SessionRetention()
	svc.SessionIdle, svc.SessionMax = cfg.Serve.SessionIdle(), cfg.Serve.SessionMax()
	svc.AuditClientRetention = cfg.Serve.AuditIPRetention()
	svc.BackupDir, svc.BackupEvery, svc.BackupKeep = DBBackupDir(cfg), cfg.Serve.DBBackupEvery(), cfg.Serve.DBBackupCount()
	svc.OnEvent = feedFromRCon(reg)
	audit.OnLog = feedFromAudit(reg)
	svc.Start(mctx)
	defer func() { mcancel(); svc.Wait() }()
	if insts, err := BanInstances(cfg, tree, logf); err != nil {
		logf("bans: %v", err)
	} else {
		svc.SetInstances(insts)
	}
	// The player history: who is on which instance, from the live player lists. Stopped before the store closes.
	pctx, pcancel := context.WithCancel(ctx)
	rec := &presence.Recorder{Store: store, Logf: logf}
	defer func() { pcancel(); rec.Wait() }()
	var lookupAPI api.Lookups
	if o.Lookups {
		ref, err := newLookups(cfg, o.Version, store, logf)
		if err != nil {
			return err
		}
		rec.OnJoin = ref.Nudge
		lookupAPI = ref
		var lwg sync.WaitGroup
		lwg.Add(1)
		go func() { defer lwg.Done(); ref.Run(pctx) }()
		defer func() { pcancel(); lwg.Wait() }()
	}
	rec.Set(pctx, presence.FromRegistry(reg), reg.Names())
	// The vehicle records: the last driver of each vehicle, from the vehicle snapshots. Stopped before the store closes.
	vctx, vcancel := context.WithCancel(ctx)
	forget := &forgetDays{}
	forget.set(cfg, tree)
	vrec := &vehicles.Recorder{Store: store, Logf: logf, Forget: forget.get}
	defer func() { vcancel(); vrec.Wait() }()
	vrec.Set(vctx, vehicles.FromRegistry(reg), reg.Names())
	apiSrv := api.New(cfg.Serve.Installation, o.Version, reg, api.NewTokenStore(APITokens(cfg)), audit)
	apiSrv.Moderation = svc
	apiSrv.Vehicles = store
	apiSrv.Logf = logf
	if cfg.Serve.WebURL != "" { // user logins need to know where the web interface lives
		apiSrv.Accounts = store
		apiSrv.Login = api.LoginSettings{WebURL: cfg.Serve.WebURL, SessionIdle: cfg.Serve.SessionIdle(), SessionMax: cfg.Serve.SessionMax()}
	}
	apiSrv.Lookups = lookupAPI
	apiSrv.TilesDir = TilesDir(cfg)
	apiSrv.Say = newRConSay(rconDialer(cfg, tree))
	apiSrv.GeoIP = geoip.Open(cfg.Serve.GeoIPDir, cfg.Serve.GeoIPLegacyDir)
	defer apiSrv.GeoIP.Close()
	// The API serves with write timeouts off: the stream stays open.
	apiHTTP, modHTTP := newServer(apiSrv.Handler()), newServer(reg.ModHandler())
	modHTTP.WriteTimeout = time.Minute
	// Open streams end when ctx does, so a shutdown does not wait for them.
	apiHTTP.BaseContext = func(net.Listener) context.Context { return ctx }

	// TLS on the API listener; the files reload on SIGHUP and when they change.
	var reloadTLS []func()
	var apiTLS *tls.Config
	if t := cfg.Serve.TLS; t.CertFile != "" {
		pair, err := tlsfiles.NewPair(t.CertFile, t.KeyFile, logf)
		if err != nil {
			return fmt.Errorf("serve.tls: %w", err)
		}
		reloadTLS = append(reloadTLS, pair.Reload)
		var pool *tlsfiles.Pool
		if t.ClientCAFile != "" {
			if pool, err = tlsfiles.NewPool(t.ClientCAFile, logf); err != nil {
				return fmt.Errorf("serve.tls.client_ca_file: %w", err)
			}
			reloadTLS = append(reloadTLS, pool.Reload)
		}
		apiTLS = tlsfiles.ServerConfig(pair, pool)
	}
	apiLn, err := net.Listen("tcp", cfg.Serve.Listen)
	if err != nil {
		return err
	}
	if apiTLS != nil {
		apiLn = tls.NewListener(apiLn, apiTLS)
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
			for _, r := range reloadTLS {
				r()
			}
			t, err := o.Load()
			if err == nil {
				err = AddHubs(reg, cfg, t)
			}
			if err != nil {
				logf("reload failed, keeping the old instances: %v", err)
				continue
			}
			msgs.stop()
			if msgs, err = startMessengers(ctx, cfg, t, reg, logf); err != nil {
				logf("server messages not restarted: %v", err)
				msgs = &messengers{cancel: func() {}}
			}
			if insts, err := BanInstances(cfg, t, logf); err != nil {
				logf("bans: %v", err)
			} else {
				svc.SetInstances(insts)
			}
			if cfgs, err := KillfeedConfigs(cfg, t); err != nil {
				logf("killfeed: %v", err)
			} else {
				kf.Set(kctx, reg, cfgs)
			}
			rec.Set(pctx, presence.FromRegistry(reg), reg.Names())
			forget.set(cfg, t)
			vrec.Set(vctx, vehicles.FromRegistry(reg), reg.Names())
			reg.LogTokens()
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
