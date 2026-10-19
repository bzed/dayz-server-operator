// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

const steamA, steamB = "76561198000000001", "76561198000000002"

func newStore(t testing.TB) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db", "dzo.sqlite")
	s, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func guidOf(t testing.TB, steam string) string {
	t.Helper()
	g, err := DeriveGUID(steam)
	require.NoError(t, err)
	return g
}

// eventually waits for cond to hold, for 5 s at most. It waits on a condition, not for a fixed time.
func eventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 5*time.Second, 5*time.Millisecond, "timed out waiting for %s", what)
}

func at(d time.Duration) *time.Time { x := t0.Add(d); return &x }
