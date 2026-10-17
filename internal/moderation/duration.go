// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"strconv"
	"time"
)

// maxBanDays bounds a timed ban; longer means permanent.
const maxBanDays = 10 * 365

// ExpiryFrom turns a ban duration ("30m", "12h", "7d", "2w" or "permanent") into the expiry, nil
// meaning permanent.
func ExpiryFrom(s string, now time.Time) (*time.Time, error) {
	if s == "permanent" {
		return nil, nil
	}
	if len(s) < 2 {
		return nil, invalid("invalid ban duration %q (e.g. 30m, 12h, 7d, 2w or permanent)", s)
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 1 {
		return nil, invalid("invalid ban duration %q (e.g. 30m, 12h, 7d, 2w or permanent)", s)
	}
	var unit time.Duration
	switch s[len(s)-1] {
	case 'm':
		unit = time.Minute
	case 'h':
		unit = time.Hour
	case 'd':
		unit = 24 * time.Hour
	case 'w':
		unit = 7 * 24 * time.Hour
	default:
		return nil, invalid("invalid ban duration %q (e.g. 30m, 12h, 7d, 2w or permanent)", s)
	}
	if time.Duration(n) > maxBanDays*24*time.Hour/unit {
		return nil, invalid("ban duration %q is longer than %d days; use permanent", s, maxBanDays)
	}
	t := now.Add(time.Duration(n) * unit)
	return &t, nil
}

// FormatExpiry prints an expiry for people and the CLI.
func FormatExpiry(t *time.Time) string {
	if t == nil {
		return "permanent"
	}
	return t.UTC().Format(time.RFC3339)
}
