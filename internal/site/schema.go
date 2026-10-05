// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package site models the site config repo (§C3): shared mod integrations
// and overlays plus per-instance configuration, checked out from a
// configurable git remote (D16). It holds the schema types for
// instance.yaml, integration.yaml and overlay manifests, and the git
// plumbing to clone/pull/commit/push the repo.
package site

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
)

// Network selects how an instance's ports reach the host (D6), mirroring
// internal/quadlet.Network so callers can convert directly.
type Network string

const (
	NetworkHost    Network = "host"
	NetworkPublish Network = "publish"
)

// UpdatePolicy controls whether detected mod updates apply automatically.
type UpdatePolicy string

const (
	PolicyAuto   UpdatePolicy = "auto"
	PolicyNotify UpdatePolicy = "notify"
	PolicyManual UpdatePolicy = "manual"
)

// DriftPolicy controls what happens when a managed mission file has
// changed on disk since dzo last wrote it (Q11).
type DriftPolicy string

const (
	DriftWarnBackupOverwrite DriftPolicy = "warn-backup-overwrite"
)

// Ports is an instance's game/RCon/query port set.
type Ports struct {
	Game  int `yaml:"game"`
	RCon  int `yaml:"rcon"`
	Query int `yaml:"query"`
}

// Params are the DayZServer launch parameters not otherwise derived.
type Params struct {
	// CPUCount is either a positive integer as a string, or "auto".
	CPUCount string   `yaml:"cpuCount,omitempty"`
	LimitFPS *int     `yaml:"limitFPS,omitempty"`
	Extra    []string `yaml:"extra,omitempty"`
}

// The default mission source: the dayzOffline.<map> folders of Bohemia
// Interactive's Central Economy repository, at master. An instance that sets
// no git and no preset gets this, with the path taken from its map name.
const (
	CentralEconomyRepo = "https://github.com/BohemiaInteractive/DayZ-Central-Economy"
	CentralEconomyRef  = "master"
	// CentralEconomyPrefix starts the folder names of that repository.
	CentralEconomyPrefix = "dayzOffline."
)

// MissionSource describes where an instance's pristine mission comes from:
// a direct git repo, a named preset from site/integrations/maps/<name>.yaml,
// or (neither set) the Central Economy repository, see CentralEconomyRepo.
// ref and path override the preset's or the default's.
type MissionSource struct {
	Git    string `yaml:"git,omitempty"`
	Preset string `yaml:"preset,omitempty"`
	Ref    string `yaml:"ref,omitempty"`
	Path   string `yaml:"path,omitempty"`
}

// Validate checks that exactly one of Git/Preset is set, and that a direct
// git source has a ref and path.
func (m MissionSource) Validate() error {
	if (m.Git == "") == (m.Preset == "") {
		return fmt.Errorf("mission_source: exactly one of git or preset must be set")
	}
	if m.Git != "" {
		if m.Ref == "" {
			return fmt.Errorf("mission_source: ref is required with git")
		}
		if m.Path == "" {
			return fmt.Errorf("mission_source: path is required with git")
		}
	}
	return nil
}

// MissionConfig is the per-instance override of the render pipeline's file
// classification (§C6).
type MissionConfig struct {
	// Unmanaged lists glob patterns that stay foreign even if the mission
	// repo ships matching paths.
	Unmanaged []string    `yaml:"unmanaged,omitempty"`
	Drift     DriftPolicy `yaml:"drift,omitempty"`
}

// Announce is a restart/update announcement schedule (A4/legacy dayz_restart).
type Announce struct {
	Minutes int    `yaml:"minutes"`
	Lock    int    `yaml:"lock"`
	Delay   int    `yaml:"delay"`
	Text    string `yaml:"text,omitempty"`
}

// UpdatesConfig controls the mod update pipeline for one instance (§C7).
type UpdatesConfig struct {
	Policy                    UpdatePolicy `yaml:"policy,omitempty"`
	CheckInterval             Duration     `yaml:"check_interval,omitempty"`
	Window                    []string     `yaml:"window,omitempty"`
	QuietHours                []string     `yaml:"quiet_hours,omitempty"`
	MinRestartInterval        Duration     `yaml:"min_restart_interval,omitempty"`
	BatchDelay                Duration     `yaml:"batch_delay,omitempty"`
	ApplyWithScheduledRestart bool         `yaml:"apply_with_scheduled_restart,omitempty"`
	MaxDelay                  Duration     `yaml:"max_delay,omitempty"`
	RestartAnnounce           Announce     `yaml:"restart_announce,omitempty"`
}

// RestartsConfig controls scheduled maintenance restarts (FR-16).
type RestartsConfig struct {
	Schedule []string `yaml:"schedule,omitempty"`
	Announce Announce `yaml:"announce,omitempty"`
}

// Age is a duration that also takes days: "30d", "12h", "90m".
type Age time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (a *Age) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	if s == "" {
		*a = 0
		return nil
	}
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days < 0 {
			return fmt.Errorf("site: invalid age %q", s)
		}
		*a = Age(time.Duration(days) * 24 * time.Hour)
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("site: invalid age %q: %w", s, err)
	}
	*a = Age(d)
	return nil
}

// Std returns the value as a plain time.Duration.
func (a Age) Std() time.Duration { return time.Duration(a) }

// Bytes is a size in bytes that takes units: "500MiB", "50GiB", "2TB", "1024".
type Bytes int64

var bytesRe = regexp.MustCompile(`^\s*(\d+)\s*([KMGT]?)(i?)B?\s*$`)

// UnmarshalYAML implements yaml.Unmarshaler.
func (b *Bytes) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	if s == "" {
		*b = 0
		return nil
	}
	m := bytesRe.FindStringSubmatch(strings.ToUpper(s))
	if m == nil {
		return fmt.Errorf("site: invalid size %q", s)
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	base := int64(1000)
	if m[3] == "I" {
		base = 1024
	}
	for _, u := range []string{"K", "M", "G", "T"} {
		if m[2] == "" {
			break
		}
		n *= base
		if m[2] == u {
			break
		}
	}
	*b = Bytes(n)
	return nil
}

// BackupConfig is the snapshot policy of an instance (§C20): when snapshots
// are taken, and how many are kept.
type BackupConfig struct {
	// Before lists the changes that take a snapshot first: update, mod_update,
	// mission_update, config_change, render. Unset means the first four; an
	// empty list means none.
	Before []string `yaml:"before,omitempty"`
	// Schedule is a systemd OnCalendar expression for periodic snapshots.
	Schedule     string         `yaml:"schedule,omitempty"`
	Keep         int            `yaml:"keep,omitempty"`
	KeepByReason map[string]int `yaml:"keep_by_reason,omitempty"`
	MaxAge       Age            `yaml:"max_age,omitempty"`
	MinKeep      int            `yaml:"min_keep,omitempty"`
	MinFreeBytes Bytes          `yaml:"min_free_bytes,omitempty"`
}

// HealthConfig drives HealthStartupRetries/HealthInterval (§C5/C9).
type HealthConfig struct {
	StartupTimeout Duration `yaml:"startup_timeout,omitempty"`
	Interval       Duration `yaml:"interval,omitempty"`
	Retries        int      `yaml:"retries,omitempty"`
}

// How an instance's server is brought down (stop, restart, updates, a reboot).
const (
	// StopRCon sends #shutdown over RCon, waits for the process to exit and kills
	// it when it does not within the timeout. The default.
	StopRCon = "rcon"
	// StopKill kills the server at once, without asking it to shut down.
	StopKill = "kill"
)

// StopConfig is how the server is stopped (`stop:` in instance.yaml or site.yaml).
type StopConfig struct {
	// Method is StopRCon (default) or StopKill.
	Method string `yaml:"method,omitempty"`
	// Timeout is how long StopRCon waits for the process to exit on its own
	// before it is killed. Default 30s.
	Timeout Duration `yaml:"timeout,omitempty"`
	// StdinQuit (default true) starts the server with a stdin that holds the line
	// "quit". The server ends every shutdown in a console loop that reads stdin and
	// leaves on "quit"; with stdin at end-of-file (a container, a service) it spins
	// forever instead (seen on 1.30 experimental; some modded maps do the same).
	StdinQuit *bool `yaml:"stdin_quit,omitempty"`
}

// RestartLimit is the crash/render-loop brake (F3).
type RestartLimit struct {
	Burst    int       `yaml:"burst,omitempty"`
	Interval Duration  `yaml:"interval,omitempty"`
	Cooldown *Duration `yaml:"cooldown,omitempty"`
}

// NotifyConfig selects which Discord webhooks (by name, from
// /etc/dzo/config.yaml's notify.discord) an instance uses. Discord being
// nil means "use the default webhook"; a non-nil empty slice means "send
// nothing" (Q14).
type NotifyConfig struct {
	Discord *[]string `yaml:"discord,omitempty"`
}

// ContainerConfig covers the generic per-instance container escape hatches
// (D9): extra env, extra mounts, resource limits.
type ContainerConfig struct {
	Env     map[string]string `yaml:"env,omitempty"`
	Mounts  []string          `yaml:"mounts,omitempty"`
	LogzDir string            `yaml:"logz_dir,omitempty"`
	CPUs    *string           `yaml:"cpus,omitempty"`
	Memory  *string           `yaml:"memory,omitempty"`
}

// HooksConfig lists the hook scripts for each hook point (§C11).
type HooksConfig struct {
	PreStart     []string `yaml:"pre_start,omitempty"`
	PostStop     []string `yaml:"post_stop,omitempty"`
	PreUpdate    []string `yaml:"pre_update,omitempty"`
	PostUpdate   []string `yaml:"post_update,omitempty"`
	PostDownload []string `yaml:"post_download,omitempty"`
	PostMerge    []string `yaml:"post_merge,omitempty"`
	PostRender   []string `yaml:"post_render,omitempty"`
	PostBackup   []string `yaml:"post_backup,omitempty"`
}

// ModRef is one entry in an instance's mod list: a workshop mod (ID) or a
// local servermod (Local, §C7/D37). List order is the dzo merge/CE
// precedence order (later wins), independent of any in-game -mod= load
// order.
type ModRef struct {
	ID     uint64 `yaml:"id,omitempty"`
	Local  string `yaml:"local,omitempty"`
	Server bool   `yaml:"server,omitempty"`
}

// localModName is the name rule for local mods (§C7): it is not purely
// numeric (checked separately), so it cannot collide with a workshop id.
var localModName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// Key identifies the mod within one instance's list.
func (m ModRef) Key() string {
	if m.Local != "" {
		return m.Local
	}
	return strconv.FormatUint(m.ID, 10)
}

// Validate checks that exactly one of ID/Local is set and that a local mod
// is a servermod with a legal name.
func (m ModRef) Validate() error {
	if (m.ID == 0) == (m.Local == "") {
		return fmt.Errorf("mods: exactly one of id or local must be set")
	}
	if m.Local == "" {
		return nil
	}
	if !localModName.MatchString(m.Local) {
		return fmt.Errorf("mods: local name %q must match %s", m.Local, localModName)
	}
	if _, err := strconv.ParseUint(m.Local, 10, 64); err == nil {
		return fmt.Errorf("mods: local name %q must not be purely numeric", m.Local)
	}
	if !m.Server {
		return fmt.Errorf("mods: local mod %q must set server: true (clients cannot fetch local mods)", m.Local)
	}
	return nil
}

// AdminConfig tunes the dzo-admin mod of one instance (§C16). The mod is
// enabled by listing {local: dzo-admin, server: true} in mods; zero values
// mean the mod's defaults (sync every second, players every 5 s, vehicles
// every 60 s, markers every 10 s, events every 30 s).
type AdminConfig struct {
	Disabled     bool     `yaml:"disabled,omitempty"`
	SyncMS       int      `yaml:"sync_ms,omitempty"`
	PlayersS     int      `yaml:"players_s,omitempty"`
	VehiclesS    int      `yaml:"vehicles_s,omitempty"`
	MarkersS     int      `yaml:"markers_s,omitempty"`
	EventsS      int      `yaml:"events_s,omitempty"`
	AllowSpawn   *bool    `yaml:"allow_spawn,omitempty"`
	AllowClasses []string `yaml:"allow_classes,omitempty"`
	DenyClasses  []string `yaml:"deny_classes,omitempty"`
}

// AdminMap is the zero-code marker integration (§C16 path 1).
type AdminMap struct {
	Watch []admin.WatchRule `yaml:"watch,omitempty"`
}

// Validate checks the intervals and watch rules.
func (a AdminConfig) Validate() error {
	for name, v := range map[string]int{"sync_ms": a.SyncMS, "players_s": a.PlayersS, "vehicles_s": a.VehiclesS, "markers_s": a.MarkersS, "events_s": a.EventsS} {
		if v < 0 {
			return fmt.Errorf("admin.%s must not be negative", name)
		}
	}
	return nil
}

// Instance is one instances/<name>/instance.yaml (§C3 example).
type Instance struct {
	Name            string          `yaml:"name"`
	Product         string          `yaml:"product"`
	Map             string          `yaml:"map"`
	MissionSource   MissionSource   `yaml:"mission_source"`
	FallbackMission string          `yaml:"fallback_mission,omitempty"`
	Mission         MissionConfig   `yaml:"mission,omitempty"`
	Ports           Ports           `yaml:"ports"`
	Network         Network         `yaml:"network"`
	Params          Params          `yaml:"params,omitempty"`
	Mods            []ModRef        `yaml:"mods,omitempty"`
	Overlays        []string        `yaml:"overlays,omitempty"`
	Updates         UpdatesConfig   `yaml:"updates,omitempty"`
	Restarts        RestartsConfig  `yaml:"restarts,omitempty"`
	Health          HealthConfig    `yaml:"health,omitempty"`
	RestartLimit    RestartLimit    `yaml:"restart_limit,omitempty"`
	Stop            StopConfig      `yaml:"stop,omitempty"`
	Notify          NotifyConfig    `yaml:"notify,omitempty"`
	Container       ContainerConfig `yaml:"container,omitempty"`
	Hooks           HooksConfig     `yaml:"hooks,omitempty"`
	Backup          BackupConfig    `yaml:"backup,omitempty"`
	Admin           AdminConfig     `yaml:"admin,omitempty"`
	AdminMap        AdminMap        `yaml:"admin_map,omitempty"`
}

// Validate checks the invariants the rest of dzo relies on. It does not
// check cross-references (product exists in config.yaml, overlay/mod
// files exist) - that needs the loaded site tree and belongs to a later
// "site validate" pass.
func (i Instance) Validate() error {
	var errs []string
	if i.Name == "" {
		errs = append(errs, "name is required")
	}
	if i.Product == "" {
		errs = append(errs, "product is required")
	}
	if i.Map == "" {
		errs = append(errs, "map is required")
	}
	if i.MissionSource.Git == "" && i.MissionSource.Preset == "" {
		// The default source: Bohemia's repository has no folder for a map it does not ship.
		if i.MissionSource.Path == "" && i.Map != "" && !strings.HasPrefix(i.Map, CentralEconomyPrefix) {
			errs = append(errs, fmt.Sprintf("mission_source is required for map %q: only %s<map> folders of %s are the default", i.Map, CentralEconomyPrefix, CentralEconomyRepo))
		}
	} else if err := i.MissionSource.Validate(); err != nil {
		errs = append(errs, err.Error())
	}
	if m := i.Stop.Method; m != "" && m != StopRCon && m != StopKill {
		errs = append(errs, fmt.Sprintf("stop.method must be %q or %q, got %q", StopRCon, StopKill, m))
	}
	if i.Network != NetworkHost && i.Network != NetworkPublish {
		errs = append(errs, fmt.Sprintf("network must be %q or %q, got %q", NetworkHost, NetworkPublish, i.Network))
	}
	if i.Ports.Game == 0 {
		errs = append(errs, "ports.game is required")
	}
	seen := map[string]bool{}
	for _, m := range i.Mods {
		if err := m.Validate(); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if seen[m.Key()] {
			errs = append(errs, fmt.Sprintf("mods: duplicate entry %s", m.Key()))
		}
		seen[m.Key()] = true
	}
	if err := i.Admin.Validate(); err != nil {
		errs = append(errs, err.Error())
	}
	for _, w := range i.AdminMap.Watch {
		if err := w.Validate(); err != nil {
			errs = append(errs, "admin_map: "+err.Error())
		}
	}
	switch i.Updates.Policy {
	case "", PolicyAuto, PolicyNotify, PolicyManual:
	default:
		errs = append(errs, fmt.Sprintf("updates.policy: invalid value %q", i.Updates.Policy))
	}
	if len(errs) > 0 {
		return fmt.Errorf("instance %q: %s", i.Name, joinErrs(errs))
	}
	return nil
}

func joinErrs(errs []string) string {
	out := errs[0]
	for _, e := range errs[1:] {
		out += "; " + e
	}
	return out
}

// FileSource is one entry in an Integration's Files map: where to fetch a
// mod integration file from.
type FileSource struct {
	Source string   `yaml:"source"` // local | url | mod
	Path   string   `yaml:"path,omitempty"`
	URL    string   `yaml:"url,omitempty"`
	SHA256 string   `yaml:"sha256,omitempty"`
	Maps   []string `yaml:"maps,omitempty"` // restrict this variant to specific maps
}

// Validate checks that Source is a known value and its required field is set.
func (f FileSource) Validate(key string) error {
	switch f.Source {
	case "local":
		if f.Path == "" {
			return fmt.Errorf("files.%s: source local requires path", key)
		}
	case "url":
		if f.URL == "" {
			return fmt.Errorf("files.%s: source url requires url", key)
		}
	case "mod":
		if f.Path == "" {
			return fmt.Errorf("files.%s: source mod requires path", key)
		}
	default:
		return fmt.Errorf("files.%s: unknown source %q (want local, url or mod)", key, f.Source)
	}
	return nil
}

// IntegrationHooks lists post-merge scripts for one mod integration.
type IntegrationHooks struct {
	PostMerge []string `yaml:"post_merge,omitempty"`
}

// Integration is one integrations/mods/<modid>/integration.yaml (§C3
// example): replaces the legacy xml.env/map.env.
type Integration struct {
	Mod       uint64                `yaml:"mod"`
	Name      string                `yaml:"name"`
	Files     map[string]FileSource `yaml:"files,omitempty"`
	Normalize []string              `yaml:"normalize,omitempty"`
	Hooks     IntegrationHooks      `yaml:"hooks,omitempty"`
	Aliases   []uint64              `yaml:"aliases,omitempty"`
}

// Validate checks the invariants Integration relies on.
func (i Integration) Validate() error {
	var errs []string
	if i.Mod == 0 {
		errs = append(errs, "mod is required")
	}
	if i.Name == "" {
		errs = append(errs, "name is required")
	}
	for key, f := range i.Files {
		if err := f.Validate(key); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("integration %d: %s", i.Mod, joinErrs(errs))
	}
	return nil
}

// OverlayManifest is a site/overlays/<name>/overlay.yaml: it declares which
// of the overlay's own files should be referenced from generated
// cfggameplay.json list keys (§C6 pipeline step 4).
type OverlayManifest struct {
	ObjectSpawners   []string `yaml:"object_spawners,omitempty"`
	SpawnGearPresets []string `yaml:"spawn_gear_presets,omitempty"`
	RestrictedAreas  []string `yaml:"restricted_areas,omitempty"`
}

// MapPreset is one integrations/maps/<name>.yaml: a reusable mission
// source, referenced from Instance.MissionSource.Preset.
type MapPreset struct {
	Git  string `yaml:"git"`
	Ref  string `yaml:"ref"`
	Path string `yaml:"path"`
}
