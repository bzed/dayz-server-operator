// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package monitor implements dzo-exporter's /metrics and /status endpoints
// and the Icinga (Monitoring Plugins API) check commands (§C9, FR-18a,
// D22): Icinga runs remotely, so everything here is reachable over the
// network rather than executed as a local plugin.
//
// Scope note: this package defines the status model, renders it as
// Prometheus exposition text and JSON, and evaluates it into Nagios-style
// check results. Populating a Snapshot from the real system (podman/
// systemd instance state, job history, disk usage) is Source's job and
// belongs to internal/instance once it exists; Source here is a small
// interface so the HTTP handlers and check logic are fully testable now
// against a fake one.
package monitor

// HealthState mirrors the podman/quadlet health states dzo tracks per
// instance (§C9).
type HealthState string

const (
	HealthStarting  HealthState = "starting"
	HealthHealthy   HealthState = "healthy"
	HealthUnhealthy HealthState = "unhealthy"
)

// InstanceStatus is one instance's current state, as reported by "dzo
// status <name>" / GET /status/<name> and rendered into /metrics.
type InstanceStatus struct {
	Name    string      `json:"name"`
	Up      bool        `json:"up"`
	Health  HealthState `json:"health"`
	Product string      `json:"product"`
	Build   string      `json:"build"`
	Map     string      `json:"map"`

	Players    int `json:"players"`
	MaxPlayers int `json:"max_players"`

	UptimeSeconds float64        `json:"uptime_seconds"`
	RestartsTotal map[string]int `json:"restarts_total"` // by reason: crash|health|scheduled|update|manual
	A2SRTTSeconds *float64       `json:"a2s_rtt_seconds,omitempty"`

	LastRenderSuccess   bool  `json:"last_render_success"`
	LastRenderTimestamp int64 `json:"last_render_timestamp"` // unix seconds, 0 = never
	MissionDriftFiles   int   `json:"mission_drift_files"`
	ModsPendingUpdate   int   `json:"mods_pending_update"`

	Message string `json:"message,omitempty"` // human-readable summary for Icinga/dashboards
}

// GlobalStatus is host-wide state, not tied to one instance.
type GlobalStatus struct {
	Version             string            `json:"version"`
	ProductBuilds       map[string]string `json:"product_builds"` // product -> buildid
	UpdatesPending      int               `json:"updates_pending"`
	UpdateLastCheckUnix int64             `json:"update_last_check_unix"`
	SteamSessionValid   bool              `json:"steam_session_valid"`
	SteamAuthRequired   bool              `json:"steam_auth_required"`
	JobsFailedTotal     map[string]int    `json:"jobs_failed_total"` // by job type
	CacheBytes          int64             `json:"cache_bytes"`
	DiskFreeBytes       int64             `json:"disk_free_bytes"`
}

// BackupStatus is the state of one instance's snapshots (§C20).
type BackupStatus struct {
	Instance    string           `json:"instance"`
	Count       int              `json:"count"`                  // complete snapshots
	LastSuccess map[string]int64 `json:"last_success_timestamp"` // by reason, unix seconds
}

// LogStatus is the state of one instance's profile logs (FR-20).
type LogStatus struct {
	Instance       string `json:"instance"`
	ArchiveBytes   int64  `json:"archive_bytes"`   // the archive of rotated logs
	UnmatchedBytes int64  `json:"unmatched_bytes"` // large files in profiles/ that no rotation rule matches
}

// Snapshot is the full payload behind /metrics and /status.
type Snapshot struct {
	Global    GlobalStatus     `json:"global"`
	Instances []InstanceStatus `json:"instances"`
	Backups   []BackupStatus   `json:"backups,omitempty"`
	Logs      []LogStatus      `json:"logs,omitempty"`
}

// Source supplies a fresh Snapshot at scrape time (pull-based, matching
// Prometheus semantics: a scrape never blocks on the game servers
// themselves, per §C9 - the real implementation caches per scrape
// interval).
type Source interface {
	Snapshot() (Snapshot, error)
}

// SourceFunc adapts a plain function to Source.
type SourceFunc func() (Snapshot, error)

func (f SourceFunc) Snapshot() (Snapshot, error) { return f() }

// Find returns the named instance's status, if present.
func (s Snapshot) Find(name string) (InstanceStatus, bool) {
	for _, i := range s.Instances {
		if i.Name == name {
			return i, true
		}
	}
	return InstanceStatus{}, false
}
