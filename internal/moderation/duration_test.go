// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"testing"
	"time"
)

func TestExpiryFrom(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Duration{"30m": 30 * time.Minute, "12h": 12 * time.Hour, "7d": 7 * 24 * time.Hour, "2w": 14 * 24 * time.Hour} {
		got, err := ExpiryFrom(in, now)
		if err != nil || got == nil || !got.Equal(now.Add(want)) {
			t.Errorf("ExpiryFrom(%q) = %v, %v", in, got, err)
		}
	}
	if got, err := ExpiryFrom("permanent", now); err != nil || got != nil {
		t.Errorf("permanent = %v, %v", got, err)
	}
	for _, bad := range []string{"", "0d", "-1d", "7", "7y", "d", "99999999w"} {
		if _, err := ExpiryFrom(bad, now); err == nil {
			t.Errorf("ExpiryFrom(%q) accepted", bad)
		}
	}
	if FormatExpiry(nil) != "permanent" || FormatExpiry(&now) != "2026-10-16T12:00:00Z" {
		t.Error("FormatExpiry")
	}
}
