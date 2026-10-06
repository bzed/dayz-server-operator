// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package monitor

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// metric is one already-formatted Prometheus sample line's pieces, kept
// only to sort output deterministically (repeated scrapes of the same
// Snapshot must render byte-identical text).
type metric struct {
	name   string
	labels string
	value  string
}

func (m metric) line() string {
	if m.labels == "" {
		return fmt.Sprintf("%s %s\n", m.name, m.value)
	}
	return fmt.Sprintf("%s{%s} %s\n", m.name, m.labels, m.value)
}

type builder struct {
	help    map[string]string
	typ     map[string]string
	metrics []metric
}

func newBuilder() *builder {
	return &builder{help: map[string]string{}, typ: map[string]string{}}
}

func (b *builder) declare(name, help, typ string) {
	b.help[name] = help
	b.typ[name] = typ
}

func (b *builder) add(name string, labels map[string]string, value float64) {
	b.metrics = append(b.metrics, metric{name: name, labels: formatLabels(labels), value: formatValue(value)})
}

func (b *builder) addInt(name string, labels map[string]string, value int) {
	b.add(name, labels, float64(value))
}

func (b *builder) addBool(name string, labels map[string]string, value bool) {
	v := 0.0
	if value {
		v = 1
	}
	b.add(name, labels, v)
}

func formatValue(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%q", k, escapeLabelValue(labels[k]))
	}
	return strings.Join(parts, ",")
}

func escapeLabelValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}

// WriteTo renders every declared metric in name order, each preceded by
// its HELP/TYPE comment pair exactly once, then its samples in the order
// they were added.
func (b *builder) render(w io.Writer) error {
	// Group metrics by name, preserving each name's first-seen order but
	// iterating names in a fixed (sorted) order for deterministic output.
	byName := map[string][]metric{}
	var names []string
	for _, m := range b.metrics {
		if _, ok := byName[m.name]; !ok {
			names = append(names, m.name)
		}
		byName[m.name] = append(byName[m.name], m)
	}
	sort.Strings(names)

	for _, name := range names {
		if help, ok := b.help[name]; ok {
			if _, err := fmt.Fprintf(w, "# HELP %s %s\n", name, help); err != nil {
				return err
			}
		}
		if typ, ok := b.typ[name]; ok {
			if _, err := fmt.Fprintf(w, "# TYPE %s %s\n", name, typ); err != nil {
				return err
			}
		}
		for _, m := range byName[name] {
			if _, err := io.WriteString(w, m.line()); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteMetrics renders s in Prometheus text exposition format (§C9's
// documented metric set).
func WriteMetrics(w io.Writer, s Snapshot) error {
	b := newBuilder()

	b.declare("dzo_build_info", "dzo build version.", "gauge")
	b.add("dzo_build_info", map[string]string{"version": s.Global.Version}, 1)

	b.declare("dzo_product_build_info", "Currently downloaded build id per product.", "gauge")
	for _, product := range sortedKeys(s.Global.ProductBuilds) {
		b.add("dzo_product_build_info", map[string]string{"product": product, "buildid": s.Global.ProductBuilds[product]}, 1)
	}

	b.declare("dzo_updates_pending", "Number of mod updates detected but not yet applied.", "gauge")
	b.addInt("dzo_updates_pending", nil, s.Global.UpdatesPending)

	b.declare("dzo_update_last_check_timestamp", "Unix time of the last update check.", "gauge")
	b.add("dzo_update_last_check_timestamp", nil, float64(s.Global.UpdateLastCheckUnix))

	b.declare("dzo_steam_session_valid", "Whether the cached Steam session is currently valid.", "gauge")
	b.addBool("dzo_steam_session_valid", nil, s.Global.SteamSessionValid)

	b.declare("dzo_steam_auth_required", "Whether a human needs to re-authenticate with Steam.", "gauge")
	b.addBool("dzo_steam_auth_required", nil, s.Global.SteamAuthRequired)

	b.declare("dzo_jobs_failed_total", "Failed operator jobs by type.", "counter")
	for _, jobType := range sortedKeys(s.Global.JobsFailedTotal) {
		b.addInt("dzo_jobs_failed_total", map[string]string{"type": jobType}, s.Global.JobsFailedTotal[jobType])
	}

	b.declare("dzo_cache_bytes", "Total size of the download cache.", "gauge")
	b.add("dzo_cache_bytes", nil, float64(s.Global.CacheBytes))

	b.declare("dzo_disk_free_bytes", "Free space on the data filesystem.", "gauge")
	b.add("dzo_disk_free_bytes", nil, float64(s.Global.DiskFreeBytes))

	b.declare("dzo_instance_up", "Whether the instance's container is running.", "gauge")
	b.declare("dzo_instance_health", "Instance health state (1 for the current state, 0 for the others).", "gauge")
	b.declare("dzo_instance_info", "Instance product/build/map, value always 1.", "gauge")
	b.declare("dzo_instance_players", "Current player count.", "gauge")
	b.declare("dzo_instance_max_players", "Configured max player count.", "gauge")
	b.declare("dzo_instance_uptime_seconds", "Seconds since the server became healthy.", "gauge")
	b.declare("dzo_instance_restarts_total", "Restarts by reason.", "counter")
	b.declare("dzo_instance_last_render_success", "Whether the last render/apply succeeded.", "gauge")
	b.declare("dzo_instance_last_render_timestamp", "Unix time of the last render/apply.", "gauge")
	b.declare("dzo_instance_mission_drift_files", "Number of managed mission files that drifted since last written.", "gauge")
	b.declare("dzo_instance_a2s_rtt_seconds", "Round-trip time of the last A2S probe.", "gauge")
	b.declare("dzo_instance_mods_pending_update", "Number of mods with a pending update for this instance.", "gauge")

	for _, inst := range s.Instances {
		labels := map[string]string{"instance": inst.Name}
		b.addBool("dzo_instance_up", labels, inst.Up)

		for _, state := range []HealthState{HealthStarting, HealthHealthy, HealthUnhealthy} {
			stateLabels := map[string]string{"instance": inst.Name, "state": string(state)}
			b.addBool("dzo_instance_health", stateLabels, inst.Health == state)
		}

		b.add("dzo_instance_info", map[string]string{
			"instance": inst.Name, "product": inst.Product, "build": inst.Build, "map": inst.Map,
		}, 1)

		b.addInt("dzo_instance_players", labels, inst.Players)
		b.addInt("dzo_instance_max_players", labels, inst.MaxPlayers)
		b.add("dzo_instance_uptime_seconds", labels, inst.UptimeSeconds)

		for _, reason := range sortedKeys(inst.RestartsTotal) {
			b.addInt("dzo_instance_restarts_total", map[string]string{"instance": inst.Name, "reason": reason}, inst.RestartsTotal[reason])
		}

		b.addBool("dzo_instance_last_render_success", labels, inst.LastRenderSuccess)
		b.add("dzo_instance_last_render_timestamp", labels, float64(inst.LastRenderTimestamp))
		b.addInt("dzo_instance_mission_drift_files", labels, inst.MissionDriftFiles)
		if inst.A2SRTTSeconds != nil {
			b.add("dzo_instance_a2s_rtt_seconds", labels, *inst.A2SRTTSeconds)
		}
		b.addInt("dzo_instance_mods_pending_update", labels, inst.ModsPendingUpdate)
	}

	b.declare("dzo_backup_count", "Complete snapshots of the instance.", "gauge")
	b.declare("dzo_backup_last_success_timestamp", "Unix time of the newest snapshot, by reason.", "gauge")
	for _, bs := range s.Backups {
		b.addInt("dzo_backup_count", map[string]string{"instance": bs.Instance}, bs.Count)
		for _, reason := range sortedKeys(bs.LastSuccess) {
			b.add("dzo_backup_last_success_timestamp", map[string]string{"instance": bs.Instance, "reason": reason}, float64(bs.LastSuccess[reason]))
		}
	}

	b.declare("dzo_logs_archive_bytes", "Size of the archive of rotated profile logs.", "gauge")
	b.declare("dzo_profile_unmatched_bytes", "Size of large files in profiles/ that no log rotation rule matches.", "gauge")
	for _, ls := range s.Logs {
		b.addInt("dzo_logs_archive_bytes", map[string]string{"instance": ls.Instance}, int(ls.ArchiveBytes))
		b.addInt("dzo_profile_unmatched_bytes", map[string]string{"instance": ls.Instance}, int(ls.UnmatchedBytes))
	}

	return b.render(w)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
