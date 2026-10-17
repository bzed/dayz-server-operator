// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

const steamA, steamB = "76561198000000001", "76561198000000002"

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db", "dzo.sqlite")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func guidOf(t *testing.T, steam string) string {
	t.Helper()
	g, err := DeriveGUID(steam)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func at(d time.Duration) *time.Time { x := t0.Add(d); return &x }
