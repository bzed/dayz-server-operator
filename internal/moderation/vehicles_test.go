// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func syncV(t *testing.T, s *Store, inst string, now time.Time, seen ...VehicleSeen) {
	t.Helper()
	if seen == nil {
		seen = []VehicleSeen{}
	}
	require.NoError(t, s.SyncVehicles(t.Context(), inst, seen, now))
}

func records(t *testing.T, s *Store, inst string) map[string]VehicleRecord {
	t.Helper()
	m, err := s.VehicleRecords(t.Context(), inst)
	require.NoError(t, err)
	return m
}

func TestSyncVehiclesRecordsAndKeepsTheDriver(t *testing.T) {
	s, _ := newStore(t)
	t1, t2 := t0, t0.Add(time.Hour)
	syncV(t, s, "a", t1, VehicleSeen{ID: "A", Type: "Offroad_02", Driver: steamA, DriverName: "alice", DrivenAt: t1})
	syncV(t, s, "a", t2, VehicleSeen{ID: "A", Type: "Offroad_02"})
	r := records(t, s, "a")["A"]
	require.Equal(t, steamA, r.Driver)
	require.Equal(t, "alice", r.DriverName)
	require.True(t, r.DrivenAt.Equal(t1))
	require.True(t, r.LastSeen.Equal(t2))
	require.True(t, r.FirstSeen.Equal(t1))
}

func TestSyncVehiclesNeverGoesBack(t *testing.T) {
	s, _ := newStore(t)
	t1, t2 := t0, t0.Add(time.Hour)
	syncV(t, s, "a", t2, VehicleSeen{ID: "A", Type: "T", Driver: steamA, DriverName: "alice", DrivenAt: t2})
	syncV(t, s, "a", t2, VehicleSeen{ID: "A", Type: "T", Driver: steamB, DriverName: "bob", DrivenAt: t1})
	r := records(t, s, "a")["A"]
	require.Equal(t, steamA, r.Driver)
	require.True(t, r.DrivenAt.Equal(t2))
	syncV(t, s, "a", t2, VehicleSeen{ID: "A", Type: "T", Driver: steamB, DriverName: "bob", DrivenAt: t2.Add(time.Minute)})
	require.Equal(t, steamB, records(t, s, "a")["A"].Driver)
}

func TestSyncVehiclesHidesAndRestores(t *testing.T) {
	s, _ := newStore(t)
	syncV(t, s, "a", t0,
		VehicleSeen{ID: "A", Type: "T", Driver: steamA, DriverName: "alice", DrivenAt: t0},
		VehicleSeen{ID: "B", Type: "T"})
	syncV(t, s, "a", t0.Add(time.Hour))
	require.Empty(t, records(t, s, "a"))
	syncV(t, s, "a", t0.Add(2*time.Hour), VehicleSeen{ID: "A", Type: "T"})
	m := records(t, s, "a")
	require.Len(t, m, 1)
	require.Equal(t, steamA, m["A"].Driver)
	require.True(t, m["A"].FirstSeen.Equal(t0))
}

func TestSyncVehiclesTypeChangeResets(t *testing.T) {
	s, _ := newStore(t)
	syncV(t, s, "a", t0, VehicleSeen{ID: "A", Type: "Offroad_02", Driver: steamA, DriverName: "alice", DrivenAt: t0})
	later := t0.Add(time.Hour)
	syncV(t, s, "a", later, VehicleSeen{ID: "A", Type: "Hatchback_02"})
	r := records(t, s, "a")["A"]
	require.Equal(t, "Hatchback_02", r.Type)
	require.Empty(t, r.Driver)
	require.True(t, r.DrivenAt.IsZero())
	require.True(t, r.FirstSeen.Equal(later))
}

func TestSyncVehiclesIsPerInstance(t *testing.T) {
	s, _ := newStore(t)
	syncV(t, s, "a", t0, VehicleSeen{ID: "A", Type: "T", Driver: steamA, DriverName: "x", DrivenAt: t0})
	syncV(t, s, "b", t0, VehicleSeen{ID: "A", Type: "T", Driver: steamB, DriverName: "y", DrivenAt: t0})
	syncV(t, s, "b", t0.Add(time.Hour))
	require.Equal(t, steamA, records(t, s, "a")["A"].Driver)
	require.Empty(t, records(t, s, "b"))
}

func TestSyncVehiclesClipsAndRejects(t *testing.T) {
	s, _ := newStore(t)
	syncV(t, s, "a", t0,
		VehicleSeen{ID: strings.Repeat("x", 65), Type: "T"},
		VehicleSeen{ID: "bad", Type: "T", Driver: "nope", DriverName: "x", DrivenAt: t0},
		VehicleSeen{ID: "long", Type: "T", Driver: steamA, DriverName: strings.Repeat("n", 500), DrivenAt: t0})
	m := records(t, s, "a")
	require.Len(t, m, 2)
	require.Empty(t, m["bad"].Driver)
	require.True(t, m["bad"].DrivenAt.IsZero())
	require.Equal(t, clip(strings.Repeat("n", 500), maxName), m["long"].DriverName)
}

func TestForgetVehicles(t *testing.T) {
	s, _ := newStore(t)
	syncV(t, s, "a", t0, VehicleSeen{ID: "old", Type: "T"}, VehicleSeen{ID: "new", Type: "T"}, VehicleSeen{ID: "vis", Type: "T"})
	syncV(t, s, "a", t0.Add(time.Hour), VehicleSeen{ID: "new", Type: "T"}, VehicleSeen{ID: "vis", Type: "T"})
	syncV(t, s, "a", t0.Add(48*time.Hour), VehicleSeen{ID: "vis", Type: "T"})
	// old hidden at +1h, new hidden at +48h, vis visible
	n, err := s.ForgetVehicles(t.Context(), "a", t0.Add(24*time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	var cnt int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM vehicles`).Scan(&cnt))
	require.Equal(t, 2, cnt)
	require.Len(t, records(t, s, "a"), 1)
}

func TestLastUse(t *testing.T) {
	first, driven := t0, t0.Add(time.Hour)
	require.True(t, VehicleRecord{FirstSeen: first}.LastUse().Equal(first))
	require.True(t, VehicleRecord{FirstSeen: first, DrivenAt: driven}.LastUse().Equal(driven))
}
