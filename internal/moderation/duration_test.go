// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpiryFrom(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Duration{"30m": 30 * time.Minute, "12h": 12 * time.Hour, "7d": 7 * 24 * time.Hour, "2w": 14 * 24 * time.Hour} {
		got, err := ExpiryFrom(in, now)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.True(t, got.Equal(now.Add(want)))
	}
	got, err := ExpiryFrom("permanent", now)
	require.NoError(t, err)
	assert.Nil(t, got)
	for _, bad := range []string{"", "0d", "-1d", "7", "7y", "d", "99999999w"} {
		_, err := ExpiryFrom(bad, now)
		require.Error(t, err, "ExpiryFrom(%q) accepted", bad)
	}
	require.Equal(t, "permanent", FormatExpiry(nil))
	assert.Equal(t, "2026-10-16T12:00:00Z", FormatExpiry(&now))
}
