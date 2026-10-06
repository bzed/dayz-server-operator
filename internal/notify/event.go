// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package notify implements dzo's Notifier interface and a Discord webhook
// implementation (§C9, FR-18b, Q14): mod update detected/downloaded/
// applied, server build available, restart scheduled/started/finished,
// health unhealthy/recovered, crash loop, render/validation failed, drift
// detected, Steam login required, job failed. Mail/Matrix/etc. can be
// added later behind the same Notifier interface.
package notify

import "context"

// Kind identifies the event, and selects its message template.
type Kind string

const (
	KindModUpdateDetected      Kind = "mod_update_detected"
	KindModUpdateDownloaded    Kind = "mod_update_downloaded"
	KindModUpdateApplied       Kind = "mod_update_applied"
	KindModRefreshResult       Kind = "mod_refresh_result"
	KindProductUpdateAvailable Kind = "product_update_available"
	KindRestartScheduled       Kind = "restart_scheduled"
	KindRestartStarted         Kind = "restart_started"
	KindRestartFinished        Kind = "restart_finished"
	KindHealthUnhealthy        Kind = "health_unhealthy"
	KindHealthRecovered        Kind = "health_recovered"
	KindCrashLoop              Kind = "crash_loop"
	KindCrash                  Kind = "crash"
	KindRenderFailed           Kind = "render_failed"
	KindDriftDetected          Kind = "drift_detected"
	KindSteamLoginRequired     Kind = "steam_login_required"
	KindJobFailed              Kind = "job_failed"
)

// Event is one notification. Data feeds the Kind's message template; the
// fields a template can use are documented next to each default template.
type Event struct {
	Kind     Kind
	Instance string // empty for global (non-instance) events
	Data     map[string]any

	// Targets selects which named webhooks receive this event. nil means
	// "use the configured default"; a non-nil empty slice means "send to
	// nobody" (Q14) - callers translate site.NotifyConfig.Discord into
	// this directly.
	Targets *[]string
}

// Notifier delivers one Event. Implementations must not block the caller
// indefinitely; ctx should carry a reasonable deadline.
type Notifier interface {
	Notify(ctx context.Context, ev Event) error
}
