// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package exporter is `dzo exporter` (§C9): the collector that fills
// internal/monitor's Snapshot from systemd, podman, the Steam query, the
// instance's state files and the snapshot index, and the HTTP server that
// serves it with optional TLS (reloaded without a restart), a bearer token and
// an IP allow-list. Scrapes are answered from a snapshot refreshed in the
// background, so they never wait for a game server.
package exporter

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/bzed/dayz-server-operator/internal/a2s"
	"github.com/bzed/dayz-server-operator/internal/backup"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/logs"
	"github.com/bzed/dayz-server-operator/internal/mission"
	"github.com/bzed/dayz-server-operator/internal/monitor"
	"github.com/bzed/dayz-server-operator/internal/product"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/bzed/dayz-server-operator/internal/steam"
)

// Runner runs a command and returns its output; tests replace it.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Updates is what the update engine knows (internal/updates).
type Updates struct {
	PendingByInstance map[string]int
	LastCheck         int64
}

// Collector builds monitor snapshots of one installation.
type Collector struct {
	Cfg     *config.Config
	Version string
	Load    func() (*site.Tree, error)
	Run     Runner
	Query   func(addr string, timeout time.Duration) (a2s.InfoResponse, error)
	Now     func() time.Time
	// BootUptime returns the seconds since the host booted (systemd reports an
	// instance's start on that clock).
	BootUptime func() (float64, error)
	Updates    func() Updates
	// Interval is how often the snapshot is refreshed; SlowInterval how often
	// the expensive parts (cache size, mission drift) are.
	Interval, SlowInterval time.Duration

	mu     sync.Mutex
	snap   monitor.Snapshot
	have   bool
	slowAt time.Time
	slow   slowData
}

type slowData struct {
	cacheBytes int64
	drift      map[string]int
}

func (c *Collector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Collector) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if c.Run != nil {
		return c.Run(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // systemctl and podman with arguments built here
}

// Snapshot returns the latest snapshot; the first call collects one.
func (c *Collector) Snapshot() (monitor.Snapshot, error) {
	c.mu.Lock()
	have := c.have
	c.mu.Unlock()
	if !have {
		c.Refresh(context.Background())
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snap, nil
}

// Start refreshes the snapshot every Interval until ctx ends.
func (c *Collector) Start(ctx context.Context) {
	iv := c.Interval
	if iv == 0 {
		iv = 15 * time.Second
	}
	c.Refresh(ctx)
	go func() {
		t := time.NewTicker(iv)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c.Refresh(ctx)
			}
		}
	}()
}

// Refresh collects a new snapshot now.
func (c *Collector) Refresh(ctx context.Context) {
	snap := c.collect(ctx)
	c.mu.Lock()
	c.snap, c.have = snap, true
	c.mu.Unlock()
}

func (c *Collector) collect(ctx context.Context) monitor.Snapshot {
	snap := monitor.Snapshot{Global: c.global()}
	tree, err := c.Load()
	if err != nil {
		snap.Global.JobsFailedTotal = map[string]int{"site_load": 1}
		return snap
	}
	var up Updates
	if c.Updates != nil {
		up = c.Updates()
	}
	snap.Global.UpdateLastCheckUnix = up.LastCheck
	slow := c.slowData(tree)

	names := tree.InstanceNames()
	results := make([]monitor.InstanceStatus, len(names))
	var wg sync.WaitGroup
	for i, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = c.instance(ctx, tree, n, up.PendingByInstance[n], slow.drift[n])
		}()
	}
	wg.Wait()
	snap.Instances = results
	for _, n := range names {
		snap.Global.UpdatesPending += up.PendingByInstance[n]
		if bs, ok := c.backups(tree, n); ok {
			snap.Backups = append(snap.Backups, bs)
		}
		if ls, ok := c.logStatus(tree, n); ok {
			snap.Logs = append(snap.Logs, ls)
		}
	}
	snap.Global.CacheBytes = slow.cacheBytes
	return snap
}

func (c *Collector) global() monitor.GlobalStatus {
	g := monitor.GlobalStatus{Version: c.Version, ProductBuilds: map[string]string{}, JobsFailedTotal: map[string]int{}}
	for name := range c.Cfg.Products {
		if id, err := product.ProductStore(c.Cfg.Paths.Cache, name).Current(); err == nil && id != "" {
			g.ProductBuilds[name] = id
		}
	}
	if st, err := steam.LoadStatus(filepath.Join(c.Cfg.Paths.Secrets, "steam-status.json")); err == nil {
		g.SteamAuthRequired = st.AuthRequired
		g.SteamSessionValid = st.LastSuccessAt != nil && !st.AuthRequired
	}
	var fsst unix.Statfs_t
	if err := unix.Statfs(existingDir(c.Cfg.Paths.Data), &fsst); err == nil {
		g.DiskFreeBytes = int64(fsst.Bavail) * int64(fsst.Bsize) //nolint:gosec // block counts of a real filesystem
	}
	return g
}

func existingDir(p string) string {
	for {
		if _, err := os.Stat(p); err == nil || filepath.Dir(p) == p {
			return p
		}
		p = filepath.Dir(p)
	}
}

// slowData returns the expensive values, recomputed every SlowInterval.
func (c *Collector) slowData(tree *site.Tree) slowData {
	iv := c.SlowInterval
	if iv == 0 {
		iv = 10 * time.Minute
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.slow.drift != nil && c.now().Sub(c.slowAt) < iv {
		return c.slow
	}
	d := slowData{drift: map[string]int{}}
	_ = filepath.WalkDir(c.Cfg.Paths.Cache, func(_ string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil //nolint:nilerr // a vanished file is not an error of the total
		}
		if info, err := e.Info(); err == nil {
			d.cacheBytes += info.Size()
		}
		return nil
	})
	for _, n := range tree.InstanceNames() {
		d.drift[n] = c.driftFiles(tree, n)
	}
	c.slow, c.slowAt = d, c.now()
	return d
}

// driftFiles counts the managed mission files that changed outside dzo, by a
// dry-run render against the pristine mission that is already there.
func (c *Collector) driftFiles(tree *site.Tree, name string) int {
	inst, err := resolve.Resolve(c.Cfg, tree, name)
	if err != nil {
		return 0
	}
	if _, err := os.Stat(inst.Paths.Pristine); err != nil {
		return 0
	}
	in := mission.RenderInput{
		PristineDir: inst.Paths.Pristine, LiveDir: inst.Paths.Live, ManifestPath: inst.Paths.Manifest,
		FileHistoryDir: inst.Paths.FileHistory, Unmanaged: inst.Mission.Unmanaged, DryRun: true,
	}
	if inst.Mission.Fallback != "" && inst.Product.Dir != "" {
		in.FallbackDir = filepath.Join(inst.Product.Dir, "mpmissions", inst.Mission.Fallback)
	}
	plan, _, err := mission.Render(in)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range plan.Entries {
		if e.Action == mission.ActionDrift {
			n++
		}
	}
	return n
}

func props(out []byte) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			m[k] = v
		}
	}
	return m
}

func (c *Collector) instance(ctx context.Context, tree *site.Tree, name string, pending, drift int) monitor.InstanceStatus {
	st := monitor.InstanceStatus{Name: name, Health: monitor.HealthUnhealthy, RestartsTotal: map[string]int{}, ModsPendingUpdate: pending, MissionDriftFiles: drift}
	inst, err := resolve.Resolve(c.Cfg, tree, name)
	if err != nil {
		st.Message = "cannot resolve: " + err.Error()
		return st
	}
	st.Product, st.Build, st.Map = inst.Product.Name, inst.Product.Build, inst.Map

	unit := "dzo-" + name + ".service"
	out, err := c.run(ctx, "systemctl", "--user", "show", "-p", "ActiveState,SubState,ActiveEnterTimestampMonotonic,NRestarts", unit)
	p := props(out)
	var systemdErr error
	if err != nil && len(p) == 0 {
		systemdErr = err
	}
	active := p["ActiveState"] == "active"
	if n, err := strconv.Atoi(p["NRestarts"]); err == nil {
		st.RestartsTotal["all"] = n
	}
	if active && c.BootUptime != nil {
		if mono, err := strconv.ParseFloat(p["ActiveEnterTimestampMonotonic"], 64); err == nil {
			if boot, err := c.BootUptime(); err == nil && boot*1e6 >= mono {
				st.UptimeSeconds = boot - mono/1e6
			}
		}
	}

	health := ""
	if hb, err := c.run(ctx, "podman", "inspect", "--format", "{{.State.Health.Status}}", "dzo-"+name); err == nil {
		health = strings.TrimSpace(string(hb))
	}
	switch monitor.HealthState(health) {
	case monitor.HealthHealthy, monitor.HealthStarting, monitor.HealthUnhealthy:
		st.Health = monitor.HealthState(health)
	default:
		if p["ActiveState"] == "activating" {
			st.Health = monitor.HealthStarting
		} else if active {
			st.Health = monitor.HealthHealthy // running without a health check to say otherwise
		}
	}

	if active && inst.Ports.Query != 0 {
		query := c.Query
		if query == nil {
			query = a2s.Query
		}
		start := time.Now()
		if info, err := query("127.0.0.1:"+strconv.Itoa(inst.Ports.Query), 2*time.Second); err == nil {
			rtt := time.Since(start).Seconds()
			st.A2SRTTSeconds = &rtt
			st.Players, st.MaxPlayers, st.Up = int(info.Players), int(info.MaxPlayers), true
		}
	}

	st.LastRenderTimestamp, st.LastRenderSuccess = c.render(inst)
	st.Message = message(st, p["ActiveState"])
	if systemdErr != nil && !active {
		st.Message = "systemd: " + systemdErr.Error()
	}
	return st
}

// render says when the instance's mission was last rendered, and whether the
// last render succeeded: the manifest is written by every successful render,
// and the failed-render gate holds a failure.
func (c *Collector) render(inst *resolve.Instance) (ts int64, ok bool) {
	if fi, err := os.Stat(inst.Paths.Manifest); err == nil {
		ts, ok = fi.ModTime().Unix(), true
	}
	if g, err := instance.LoadGateState(instance.GateFile(inst.Paths.Runtime)); err == nil && g.Failed {
		ok = false
		if g.At.Unix() > ts {
			ts = g.At.Unix()
		}
	}
	return ts, ok
}

func message(st monitor.InstanceStatus, active string) string {
	switch {
	case st.Up:
		return fmt.Sprintf("running, %d/%d players, %s, %d restarts", st.Players, st.MaxPlayers, st.Health, st.RestartsTotal["all"])
	case active == "active":
		return fmt.Sprintf("the unit is active but the server does not answer the Steam query (%s)", st.Health)
	case active == "activating":
		return "starting"
	case active == "":
		return "not installed as a unit"
	}
	return "stopped (" + active + ")"
}

// logStatus sizes the log archive of an instance and the profile files no rotation rule matches.
func (c *Collector) logStatus(tree *site.Tree, name string) (monitor.LogStatus, bool) {
	inst, err := resolve.Resolve(c.Cfg, tree, name)
	if err != nil {
		return monitor.LogStatus{}, false
	}
	ls := monitor.LogStatus{Instance: name, ArchiveBytes: logs.ArchiveSize(filepath.Join(c.Cfg.Paths.Logs, name))}
	if p, err := logs.MakePlan(inst.Paths.Profiles, inst.Logs, time.Now()); err == nil {
		for _, a := range p.Unmatched {
			ls.UnmatchedBytes += a.Size
		}
	}
	return ls, true
}

func (c *Collector) backups(tree *site.Tree, name string) (monitor.BackupStatus, bool) {
	inst, err := resolve.Resolve(c.Cfg, tree, name)
	if err != nil {
		return monitor.BackupStatus{}, false
	}
	ss, err := backup.ReadIndex(filepath.Join(c.Cfg.Paths.Snapshots, inst.Name))
	if err != nil || len(ss) == 0 {
		return monitor.BackupStatus{}, false
	}
	bs := monitor.BackupStatus{Instance: name, LastSuccess: map[string]int64{}}
	for _, s := range ss {
		if s.State != backup.Complete {
			continue
		}
		bs.Count++
		if t := s.Created.Unix(); t > bs.LastSuccess[string(s.Reason)] {
			bs.LastSuccess[string(s.Reason)] = t
		}
	}
	return bs, true
}

// ProcUptime reads the seconds since boot from /proc/uptime.
func ProcUptime() (float64, error) {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, fmt.Errorf("exporter: empty /proc/uptime")
	}
	return strconv.ParseFloat(f[0], 64)
}
