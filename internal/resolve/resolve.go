// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package resolve turns the operator config (internal/config), the site
// checkout (internal/site) and the download cache (internal/cache) into one
// already-resolved Instance: the single value the mission, product, quadlet
// and instance packages take their input from (§C3, §C6, §C7).
//
// Resolving is read-only. Anything not downloaded yet (server build, mod
// generations) is recorded in Instance.Missing instead of being an error, so
// `dzo config validate` works before the first install; the quadlet spec is
// only built once nothing is missing.
//
// The runtime image must not set an ENTRYPOINT: the quadlet Exec= line
// starts with ./DayZServer and runs in /dayz.
package resolve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/cache"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/product"
	"github.com/bzed/dayz-server-operator/internal/quadlet"
	"github.com/bzed/dayz-server-operator/internal/site"
)

const (
	defaultImage          = "localhost/dzo-runtime:latest"
	defaultStartupTimeout = 45 * time.Minute
	startupProbeInterval  = 30 * time.Second
	defaultHealthInterval = 60 * time.Second
	defaultHealthRetries  = 5
	defaultRestartBurst   = 5
	defaultBackupKeep     = 20
	defaultBackupMinKeep  = 3
	defaultRestartWindow  = 30 * time.Minute
	stopTimeout           = 120 * time.Second
	defaultStopTimeout    = 30 * time.Second
	killStopTimeout       = 1 * time.Second
	restartSec            = 15 * time.Second //nolint:staticcheck // mirrors RestartSec=
	startTimeoutSlack     = 15 * time.Minute
	dzoBinary             = "/usr/bin/dzo"
)

// Product is the instance's server product and the build it is pinned to.
type Product struct {
	Name           string `yaml:"name"`
	config.Product `yaml:",inline"`
	// Build is the build the instance runs: the one it is pinned to (runtime/build, written when
	// the unit is first deployed and by `dzo instance upgrade`), else the newest installed. ""
	// if none is installed. Latest is the newest installed build; when it differs from Build, a
	// new build is waiting for an explicit upgrade (§C7: a server build is never applied by itself).
	Build  string `yaml:"build,omitempty"`
	Latest string `yaml:"latest,omitempty"`
	Dir    string `yaml:"dir,omitempty"`
}

// pinnedBuild reads the build an instance is pinned to: runtime/build, else (an instance deployed
// before the pin existed) the build in runtime/generations.json.
func pinnedBuild(runtimeDir string) string {
	if b, err := os.ReadFile(BuildPinFile(runtimeDir)); err == nil { //nolint:gosec // dzo's own runtime dir
		return strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(runtimeDir, "generations.json")); err == nil { //nolint:gosec // dzo's own runtime dir
		var g struct {
			Build string `json:"build"`
		}
		if json.Unmarshal(b, &g) == nil {
			return g.Build
		}
	}
	return ""
}

// PinBuild pins an instance to a server build (`dzo instance upgrade` does this).
func PinBuild(runtimeDir, build string) error {
	if err := os.MkdirAll(runtimeDir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(BuildPinFile(runtimeDir), []byte(build+"\n"), 0o600)
}

// BuildPinFile is where an instance's pinned server build is recorded, below its runtime dir.
func BuildPinFile(runtimeDir string) string { return filepath.Join(runtimeDir, "build") }

// Mod is one mod-list entry, in list (= merge precedence) order.
type Mod struct {
	ID         uint64 `yaml:"id,omitempty"`
	Local      string `yaml:"local,omitempty"`
	Server     bool   `yaml:"server,omitempty"`
	Name       string `yaml:"name"` // directory name in the container: @<id> or @<local>
	Generation string `yaml:"generation,omitempty"`
	Dir        string `yaml:"dir,omitempty"`
}

// Paths are the instance's host locations (§C2).
type Paths struct {
	Root        string `yaml:"root"`
	Pristine    string `yaml:"pristine"` // servermpmissions/<map>
	Live        string `yaml:"live"`     // mpmissions/<map>
	Manifest    string `yaml:"manifest"`
	FileHistory string `yaml:"filehistory"`
	Profiles    string `yaml:"profiles"`
	Storage     string `yaml:"storage"` // storage/<map>, the server's persistence (-storage)
	Runtime     string `yaml:"runtime"`
}

// Mission is the mission source and classification overrides.
type Mission struct {
	Source    site.MissionSource `yaml:"source"`
	Fallback  string             `yaml:"fallback,omitempty"`
	Unmanaged []string           `yaml:"unmanaged,omitempty"`
	Drift     site.DriftPolicy   `yaml:"drift,omitempty"`
}

// Instance is one fully resolved instance.
type Instance struct {
	Name         string               `yaml:"name"`
	Map          string               `yaml:"map"`
	Product      Product              `yaml:"product"`
	Mission      Mission              `yaml:"mission"`
	Paths        Paths                `yaml:"paths"`
	Ports        site.Ports           `yaml:"ports"`
	Network      site.Network         `yaml:"network"`
	Image        string               `yaml:"image"`
	Params       site.Params          `yaml:"params"`
	Mods         []Mod                `yaml:"mods,omitempty"`
	Overlays     []string             `yaml:"overlays,omitempty"`
	Updates      site.UpdatesConfig   `yaml:"updates"`
	Restarts     site.RestartsConfig  `yaml:"restarts"`
	Health       site.HealthConfig    `yaml:"health"`
	RestartLimit site.RestartLimit    `yaml:"restart_limit"`
	Stop         site.StopConfig      `yaml:"stop"`
	Notify       site.NotifyConfig    `yaml:"notify"`
	Container    site.ContainerConfig `yaml:"container"`
	Hooks        site.HooksConfig     `yaml:"hooks"`
	Backup       site.BackupConfig    `yaml:"backup"`
	Admin        site.AdminConfig     `yaml:"admin,omitempty"`
	AdminMap     site.AdminMap        `yaml:"admin_map,omitempty"`

	// Missing lists what is not downloaded yet; Quadlet is only populated
	// when it is empty.
	Missing []string              `yaml:"missing,omitempty"`
	Quadlet quadlet.ContainerSpec `yaml:"-"`
}

// AdminName is the local servermod that enables the admin integration.
const AdminName = "dzo-admin"

// AdminEnabled reports whether the instance runs the dzo-admin mod and has
// not switched the integration off.
func (i *Instance) AdminEnabled() bool {
	if i.Admin.Disabled {
		return false
	}
	for _, m := range i.Mods {
		if m.Local == AdminName {
			return true
		}
	}
	return false
}

// Resolve builds the named instance. Errors are config errors (unknown
// product, preset or local mod, missing overlay); what is merely not
// downloaded yet is reported in Instance.Missing.
func Resolve(cfg *config.Config, t *site.Tree, name string) (*Instance, error) {
	raw, err := t.Instance(name)
	if err != nil {
		return nil, err
	}
	// Site defaults fill every field the instance leaves unset.
	def := reflect.ValueOf(t.Site.Defaults)
	for _, f := range []string{"Params", "Updates", "Restarts", "Health", "RestartLimit", "Stop", "Notify", "Container", "Backup"} {
		fillZero(reflect.ValueOf(&raw).Elem().FieldByName(f), def.FieldByName(f))
	}

	prod, ok := cfg.Products[raw.Product]
	if !ok {
		return nil, fmt.Errorf("instance %q: unknown product %q (not in config.yaml products)", name, raw.Product)
	}
	src, err := missionSource(t, raw)
	if err != nil {
		return nil, fmt.Errorf("instance %q: %w", name, err)
	}

	root := filepath.Join(cfg.Paths.Instances, name)
	inst := &Instance{
		Name: name, Map: raw.Map, Ports: raw.Ports, Network: raw.Network, Image: t.Site.Image,
		Params: raw.Params, Overlays: raw.Overlays, Updates: raw.Updates, Restarts: raw.Restarts,
		Health: raw.Health, RestartLimit: raw.RestartLimit, Stop: raw.Stop, Notify: raw.Notify, Container: raw.Container, Hooks: raw.Hooks, Backup: raw.Backup, Admin: raw.Admin, AdminMap: raw.AdminMap,
		Mission: Mission{Source: src, Fallback: raw.FallbackMission, Unmanaged: raw.Mission.Unmanaged, Drift: raw.Mission.Drift},
		Product: Product{Name: raw.Product, Product: prod},
		Paths: Paths{
			Root:        root,
			Pristine:    filepath.Join(root, "servermpmissions", raw.Map),
			Live:        filepath.Join(root, "mpmissions", raw.Map),
			Manifest:    filepath.Join(root, "mpmissions", ".dzo-manifest.json"),
			FileHistory: filepath.Join(root, "filehistory"),
			Profiles:    filepath.Join(root, "profiles"),
			Storage:     filepath.Join(root, "storage", raw.Map),
			Runtime:     filepath.Join(root, "runtime"),
		},
	}
	applyDefaults(inst)

	for _, mnt := range inst.Container.Mounts {
		if strings.Count(mnt, ":") < 1 {
			return nil, fmt.Errorf("instance %q: container.mounts entry %q must be src:dst[:ro]", name, mnt)
		}
	}
	for _, o := range inst.Overlays {
		if !isDir(filepath.Join(t.Dir, "overlays", o)) && !isDir(filepath.Join(t.Dir, "instances", name, "overlays", o)) {
			return nil, fmt.Errorf("instance %q: overlay %q not found in overlays/ or instances/%s/overlays/", name, o, name)
		}
	}

	store := product.ProductStore(cfg.Paths.Cache, raw.Product)
	build, dir, err := current(store)
	if err != nil {
		return nil, err
	}
	inst.Product.Latest = build
	if pin := pinnedBuild(inst.Paths.Runtime); pin != "" && isDir(filepath.Join(store.Root, pin)) {
		build, dir = pin, filepath.Join(store.Root, pin)
	}
	inst.Product.Build, inst.Product.Dir = build, dir
	if build == "" {
		inst.Missing = append(inst.Missing, fmt.Sprintf("product %s: no server build installed", raw.Product))
	}

	for _, ref := range raw.Mods {
		m, err := resolveMod(cfg, t, prod, ref)
		if err != nil {
			return nil, fmt.Errorf("instance %q: %w", name, err)
		}
		if m.Generation == "" {
			inst.Missing = append(inst.Missing, fmt.Sprintf("mod %s: not downloaded", ref.Key()))
		}
		inst.Mods = append(inst.Mods, m)
	}

	if len(inst.Missing) == 0 {
		inst.Quadlet = buildQuadlet(inst)
	}
	return inst, nil
}

// ResolveAll resolves every instance (sorted by name) and checks that no two
// of them claim the same port.
func ResolveAll(cfg *config.Config, t *site.Tree) ([]*Instance, error) {
	var out []*Instance
	owner := map[int]string{}
	for _, name := range t.InstanceNames() {
		inst, err := Resolve(cfg, t, name)
		if err != nil {
			return nil, err
		}
		for _, p := range []int{inst.Ports.Game, inst.Ports.RCon, inst.Ports.Query} {
			if other, dup := owner[p]; dup && p != 0 {
				return nil, fmt.Errorf("port %d is used by both instance %q and %q", p, other, name)
			}
			owner[p] = name
		}
		out = append(out, inst)
	}
	return out, nil
}

// fillZero sets every zero leaf of dst from src, recursing into structs.
func fillZero(dst, src reflect.Value) {
	if dst.Kind() == reflect.Struct {
		for i := 0; i < dst.NumField(); i++ {
			fillZero(dst.Field(i), src.Field(i))
		}
		return
	}
	if dst.IsZero() {
		dst.Set(src)
	}
}

func missionSource(t *site.Tree, raw site.Instance) (site.MissionSource, error) {
	src := raw.MissionSource
	if src.Git == "" && src.Preset == "" {
		// The default: Bohemia's Central Economy repository, the folder named like the map.
		out := site.MissionSource{Git: site.CentralEconomyRepo, Ref: site.CentralEconomyRef, Path: raw.Map}
		if src.Ref != "" {
			out.Ref = src.Ref
		}
		if src.Path != "" {
			out.Path = src.Path
		}
		return out, nil
	}
	if src.Preset == "" {
		return src, nil
	}
	p, ok := t.Maps[src.Preset]
	if !ok {
		return src, fmt.Errorf("unknown mission preset %q (no integrations/maps/%s.yaml)", src.Preset, src.Preset)
	}
	out := site.MissionSource{Git: p.Git, Ref: p.Ref, Path: p.Path}
	if src.Ref != "" {
		out.Ref = src.Ref
	}
	if src.Path != "" {
		out.Path = src.Path
	}
	return out, nil
}

func applyDefaults(inst *Instance) {
	if inst.Stop.Method == "" {
		inst.Stop.Method = site.StopRCon
	}
	if inst.Stop.Timeout == 0 {
		inst.Stop.Timeout = site.Duration(defaultStopTimeout)
	}
	if inst.Stop.IgnoreAsserts == nil {
		t := true
		inst.Stop.IgnoreAsserts = &t
	}
	if inst.Image == "" {
		inst.Image = defaultImage
	}
	if inst.Backup.Before == nil {
		inst.Backup.Before = []string{"update", "mod_update", "mission_update", "config_change"}
	}
	if inst.Backup.Keep == 0 {
		inst.Backup.Keep = defaultBackupKeep
	}
	if inst.Backup.MinKeep == 0 {
		inst.Backup.MinKeep = defaultBackupMinKeep
	}
	if inst.Mission.Drift == "" {
		inst.Mission.Drift = site.DriftWarnBackupOverwrite
	}
	if inst.Health.StartupTimeout == 0 {
		inst.Health.StartupTimeout = site.Duration(defaultStartupTimeout)
	}
	if inst.Health.Interval == 0 {
		inst.Health.Interval = site.Duration(defaultHealthInterval)
	}
	if inst.Health.Retries == 0 {
		inst.Health.Retries = defaultHealthRetries
	}
	if inst.RestartLimit.Burst == 0 {
		inst.RestartLimit.Burst = defaultRestartBurst
	}
	if inst.RestartLimit.Interval == 0 {
		inst.RestartLimit.Interval = site.Duration(defaultRestartWindow)
	}
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// current returns the store's current generation id and directory, or empty
// strings when nothing is installed.
func current(s *cache.Store) (id, dir string, err error) {
	id, err = s.Current()
	if err != nil || id == "" {
		return "", "", err
	}
	return id, filepath.Join(s.Root, id), nil
}

func resolveMod(cfg *config.Config, t *site.Tree, prod config.Product, ref site.ModRef) (Mod, error) {
	m := Mod{ID: ref.ID, Local: ref.Local, Server: ref.Server}
	var store *cache.Store
	if ref.Local != "" {
		if _, err := LocalSource(t, ref.Local); err != nil {
			return m, err
		}
		m.Name = "@" + ref.Local
		store = product.LocalModStore(cfg.Paths.Cache, ref.Local)
	} else {
		m.Name = "@" + strconv.FormatUint(ref.ID, 10)
		store = product.ModStore(cfg.Paths.Cache, prod.WorkshopAppID, ref.ID)
	}
	var err error
	m.Generation, m.Dir, err = current(store)
	return m, err
}

// LocalSource finds where a local servermod is imported from (§C7): its
// site.yaml `local_mods` entry, else localmods/<name>/ in the site repo.
func LocalSource(t *site.Tree, name string) (product.LocalSource, error) {
	if l, ok := t.Site.LocalMods[name]; ok {
		if l.URL != "" {
			return product.LocalSource{URL: l.URL, SHA256: l.SHA256}, nil
		}
		return product.LocalSource{Dir: l.Path}, nil
	}
	if dir := filepath.Join(t.Dir, "localmods", name); isDir(dir) {
		return product.LocalSource{Dir: dir}, nil
	}
	return product.LocalSource{}, fmt.Errorf("local mod %q: no local_mods entry in site.yaml and no localmods/%s/ in the site repo", name, name)
}

// buildQuadlet composes the instance's container: the shared build mounted
// read-only with per-instance keys/, mpmissions/ and mod mounts nested on
// top (§C5), plus the DayZServer command line (§C6 step 8).
func buildQuadlet(inst *Instance) quadlet.ContainerSpec {
	rt := inst.Paths.Runtime
	vols := []quadlet.Volume{
		{Source: dzoBinary, Destination: "/usr/local/bin/dzo", ReadOnly: true},
		// An overlay, not ":ro": a server that finds the build or a mod on a read-only
		// mount silently skips its extra addon directories (DLC such as sakhal, and every
		// -servermod/-mod), with no error and no script from them (verified on 1.29). The
		// overlay is writable for the server and leaves the immutable generation untouched.
		{Source: inst.Product.Dir, Destination: "/dayz", Overlay: true},
		{Source: filepath.Join(rt, "keys"), Destination: "/dayz/keys", ReadOnly: true},
		{Source: filepath.Dir(inst.Paths.Manifest), Destination: "/dayz/mpmissions"},
		{Source: inst.Paths.Profiles, Destination: "/profiles"},
		{Source: inst.Paths.Storage, Destination: "/storage"},
		{Source: filepath.Join(rt, "serverDZ.cfg"), Destination: "/profiles/serverDZ.cfg", ReadOnly: true},
	}
	var clientMods, serverMods []string
	for _, m := range inst.Mods {
		vols = append(vols, quadlet.Volume{Source: m.Dir, Destination: "/dayz/" + m.Name, Overlay: true})
		if m.Server {
			serverMods = append(serverMods, m.Name)
		} else {
			clientMods = append(clientMods, m.Name)
		}
	}
	for _, mnt := range inst.Container.Mounts {
		vols = append(vols, parseMount(mnt))
	}
	if d := inst.Container.LogzDir; d != "" {
		vols = append(vols, quadlet.Volume{Source: d, Destination: d})
	}

	exec := []string{
		"./DayZServer",
		"-config=/profiles/serverDZ.cfg",
		"-port=" + strconv.Itoa(inst.Ports.Game),
		"-profiles=/profiles",
		"-storage=/storage",
		"-BEpath=/profiles/battleye",
	}
	if len(clientMods) > 0 {
		exec = append(exec, "-mod="+strings.Join(clientMods, ";"))
	}
	if len(serverMods) > 0 {
		exec = append(exec, "-servermod="+strings.Join(serverMods, ";"))
	}
	if c := inst.Params.CPUCount; c != "" && c != "auto" {
		exec = append(exec, "-cpuCount="+c)
	}
	if inst.Params.LimitFPS != nil {
		exec = append(exec, "-limitFPS="+strconv.Itoa(*inst.Params.LimitFPS))
	}
	exec = append(exec, inst.Params.Extra...)

	query := "127.0.0.1:" + strconv.Itoa(inst.Ports.Query)
	startup := inst.Health.StartupTimeout.Std()
	spec := quadlet.ContainerSpec{
		Name:        "dzo-" + inst.Name,
		Description: "DayZ server " + inst.Name + " (dzo)",
		Image:       inst.Image,
		Network:     quadlet.Network(inst.Network),
		Volumes:     vols,
		Environment: inst.Container.Env,
		Exec:        exec,
		WorkingDir:  "/dayz",
		Health: quadlet.Health{
			Cmd:             "/usr/local/bin/dzo health live --query " + query,
			Interval:        inst.Health.Interval.Std(),
			Retries:         inst.Health.Retries,
			OnFailure:       "kill",
			StartupCmd:      "/usr/local/bin/dzo health startup --query " + query,
			StartupInterval: startupProbeInterval,
			StartupRetries:  int((startup + startupProbeInterval - 1) / startupProbeInterval),
		},
		NotifyHealthy:        true,
		PreStart:             []string{dzoBinary + " instance render " + inst.Name},
		StopTimeout:          stopTimeout,
		RestartSec:           restartSec,
		TimeoutStartSec:      startup + startTimeoutSlack,
		RestartLimitBurst:    inst.RestartLimit.Burst,
		RestartLimitInterval: inst.RestartLimit.Interval.Std(),
	}
	if inst.Stop.IgnoreAsserts == nil || *inst.Stop.IgnoreAsserts {
		// The experimental builds stop at "Assertion failed ... (A)bort (R)etry (I)gnore" when
		// they shut down with leaked scripts and read the answer from stdin; a stdin at EOF makes
		// them spin forever (see site.StopConfig). A file holding the answer does the job and
		// keeps the server PID 1, so signals reach it.
		spec.Volumes = append(spec.Volumes, quadlet.Volume{Source: filepath.Join(rt, "stdin"), Destination: "/stdin", ReadOnly: true})
		spec.Exec = []string{"/bin/sh", "-c", "exec " + shellJoin(exec) + " </stdin"}
	}
	if len(inst.Hooks.PreStart) > 0 {
		spec.PreStart = append(spec.PreStart, dzoBinary+" instance hook "+inst.Name+" pre_start")
	}
	if len(inst.Hooks.PostStop) > 0 {
		spec.PostStop = []string{dzoBinary + " instance hook " + inst.Name + " post_stop"}
	}
	switch inst.Stop.Method {
	case site.StopKill:
		// No shutdown request: podman stops the container with a one-second grace.
		spec.StopTimeout = killStopTimeout
		spec.TimeoutStopSec = 30 * time.Second
	default:
		// Ask over RCon first (dzo instance shutdown), then podman's own stop as the fallback.
		to := inst.Stop.Timeout.Std()
		spec.ExecStop = []string{dzoBinary + " instance shutdown " + inst.Name + " --timeout " + to.String()}
		spec.TimeoutStopSec = to + stopTimeout + 30*time.Second
	}
	if inst.Container.Memory != nil {
		spec.Memory = *inst.Container.Memory
	}
	if inst.Container.CPUs != nil {
		spec.CPUs = *inst.Container.CPUs
	}
	if inst.Network == site.NetworkPublish {
		for _, p := range []int{inst.Ports.Game, inst.Ports.RCon, inst.Ports.Query} {
			if p != 0 {
				spec.Ports = append(spec.Ports, quadlet.Port{HostPort: p, ContainerPort: p, Protocol: "udp"})
			}
		}
	}
	return spec
}

// shellJoin quotes every word for /bin/sh, so that -mod=@a;@b stays one word.
func shellJoin(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

// parseMount parses a container.mounts entry "src:dst[:ro]".
func parseMount(s string) quadlet.Volume {
	parts := strings.Split(s, ":")
	v := quadlet.Volume{Source: parts[0]}
	if len(parts) > 1 {
		v.Destination = parts[1]
	}
	v.ReadOnly = len(parts) > 2 && parts[2] == "ro"
	return v
}
