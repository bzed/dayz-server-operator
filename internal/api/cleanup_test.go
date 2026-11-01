// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

var cleanupNow = time.Unix(1_800_000_000, 0).UTC()

func daysAgo(d int) time.Time { return cleanupNow.Add(-time.Duration(d) * 24 * time.Hour) }

// cleanupEnv has a mod with five vehicles: old (driven 40 d ago), recent (10 d), never (never driven,
// first seen 41 d ago), crewed (40 d, occupant) and covered (40 d, Expansion cover).
func cleanupEnv(t *testing.T, features ...string) *env {
	t.Helper()
	e := newEnv(t)
	e.srv.now = func() time.Time { return cleanupNow }
	e.alpha.Sync(admin.SyncRequest{Proto: 1, Hello: true, ModVersion: "0.8.0", World: "chernarusplus", Features: features,
		Vehicles: []admin.Vehicle{
			{ID: "old", Type: "Sedan_02"}, {ID: "recent", Type: "Sedan_02"}, {ID: "never", Type: "Sedan_02"},
			{ID: "crewed", Type: "Sedan_02", Occupants: []string{"alice"}}, {ID: "covered", Type: "Sedan_02", Covered: true},
		}})
	e.srv.Vehicles = fakeRecords{recs: map[string]moderation.VehicleRecord{
		"old":     {ID: "old", Driver: "76561198000000001", DriverName: "alice", DrivenAt: daysAgo(40), FirstSeen: daysAgo(90)},
		"recent":  {ID: "recent", Driver: "76561198000000001", DriverName: "alice", DrivenAt: daysAgo(10), FirstSeen: daysAgo(90)},
		"never":   {ID: "never", FirstSeen: daysAgo(41)},
		"crewed":  {ID: "crewed", FirstSeen: daysAgo(40)},
		"covered": {ID: "covered", FirstSeen: daysAgo(40)},
	}}
	return e
}

func ids(vs []admin.Vehicle) []string {
	out := []string{}
	for _, v := range vs {
		out = append(out, v.ID)
	}
	return out
}

func TestUnusedListsOnlyOldEmptyVehicles(t *testing.T) {
	e := cleanupEnv(t, admin.FeatureDriver)
	c, b := e.do("GET", "/api/v1/instances/alpha/vehicles/unused?days=30", e.token(RoleOperator, nil, false), "")
	require.Equal(t, 200, c, "%s", b)
	var got UnusedVehicles
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, 30, got.Days)
	require.Equal(t, []string{"never", "old"}, ids(got.Vehicles)) // oldest use first: never (41 d), old (40 d)
	require.NotNil(t, got.Since)
	require.True(t, got.Since.Equal(daysAgo(90)))
}

func TestUnusedRefusesBadDays(t *testing.T) {
	e := cleanupEnv(t, admin.FeatureDriver)
	for _, q := range []string{"", "?days=0", "?days=3651", "?days=abc"} {
		c, b := e.do("GET", "/api/v1/instances/alpha/vehicles/unused"+q, e.token(RoleOperator, nil, false), "")
		require.Equal(t, 400, c, "%q", q)
		require.Contains(t, string(b), "days must be a whole number from 1 to 3650")
	}
}

func TestUnusedRefusesAnOldMod(t *testing.T) {
	e := cleanupEnv(t)
	op := e.token(RoleOperator, nil, false)
	c, b := e.do("GET", "/api/v1/instances/alpha/vehicles/unused?days=30", op, "")
	require.Equal(t, 409, c)
	require.Contains(t, string(b), "the dzo-admin mod of this instance does not report drivers; update it to 0.8.0")
	c, b = e.do("POST", "/api/v1/instances/alpha/vehicles/cleanup", op, `{"days":30,"ids":["old"]}`)
	require.Equal(t, 409, c)
	require.Contains(t, string(b), "does not report drivers")
	require.Empty(t, e.alpha.Sync(admin.SyncRequest{Proto: 1}).Commands)
}

func TestCleanupDeletesAndSkips(t *testing.T) {
	e := cleanupEnv(t, admin.FeatureDriver)
	// crewed: boarded since the preview; recent: driven since; ghost: gone.
	c, b := e.do("POST", "/api/v1/instances/alpha/vehicles/cleanup", e.token(RoleOperator, nil, false),
		`{"days":30,"ids":["old","crewed","recent","ghost","covered"]}`)
	require.Equal(t, 200, c, "%s", b)
	var got CleanupResult
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, []string{"old"}, got.Deleted)
	reasons := map[string]string{}
	for _, s := range got.Skipped {
		reasons[s.ID] = s.Reason
	}
	require.Equal(t, map[string]string{
		"crewed": "crew inside", "recent": "used since the preview", "ghost": "not on the map", "covered": "covered by Expansion",
	}, reasons)
	cmds := e.alpha.Sync(admin.SyncRequest{Proto: 1}).Commands
	require.Len(t, cmds, 1)
	require.Equal(t, admin.KindVehicleDelete, cmds[0].Kind)
	require.Equal(t, "old", cmds[0].Vehicle)
	require.False(t, cmds[0].Force)
}

func TestCleanupAudit(t *testing.T) {
	e := cleanupEnv(t, admin.FeatureDriver)
	c, b := e.do("POST", "/api/v1/instances/alpha/vehicles/cleanup", e.token(RoleOperator, nil, false),
		`{"days":30,"ids":["old","crewed"]}`)
	require.Equal(t, 200, c, "%s", b)
	entries, err := e.srv.Audit.Tail("alpha", 10)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	byTarget := map[string]admin.AuditEntry{entries[0].Target: entries[0], entries[1].Target: entries[1]}
	old := byTarget["old"]
	require.Equal(t, "vehicle_delete", old.Action)
	require.Equal(t, "cleanup: not driven for 30 days", old.Detail)
	require.True(t, old.OK)
	crewed := byTarget["crewed"]
	require.Equal(t, "vehicle_delete", crewed.Action)
	require.False(t, crewed.OK)
	require.Equal(t, "crew inside", crewed.Result)
}

func TestCleanupLimits(t *testing.T) {
	e := cleanupEnv(t, admin.FeatureDriver)
	op := e.token(RoleOperator, nil, false)
	many := make([]string, 501)
	for i := range many {
		many[i] = fmt.Sprintf("v%d", i)
	}
	body, _ := json.Marshal(CleanupRequest{Days: 30, IDs: many})
	c, b := e.do("POST", "/api/v1/instances/alpha/vehicles/cleanup", op, string(body))
	require.Equal(t, 400, c)
	require.Contains(t, string(b), "at most 500 vehicles per cleanup")
	c, _ = e.do("POST", "/api/v1/instances/alpha/vehicles/cleanup", op, `{"days":30,"ids":[]}`)
	require.Equal(t, 400, c)
	c, b = e.do("POST", "/api/v1/instances/alpha/vehicles/cleanup", op, `{"days":0,"ids":["old"]}`)
	require.Equal(t, 400, c)
	require.Contains(t, string(b), "days must be a whole number from 1 to 3650")
	viewer := e.token(RoleViewer, nil, false)
	c, _ = e.do("POST", "/api/v1/instances/alpha/vehicles/cleanup", viewer, `{"days":30,"ids":["old"]}`)
	require.Equal(t, 403, c)
	c, _ = e.do("GET", "/api/v1/instances/alpha/vehicles/unused?days=30", viewer, "")
	require.Equal(t, 403, c)
}

func TestCleanupOfflineMod(t *testing.T) {
	e := newEnv(t)
	op := e.token(RoleOperator, nil, false)
	c, b := e.do("POST", "/api/v1/instances/alpha/vehicles/cleanup", op, `{"days":30,"ids":["old"]}`)
	require.Equal(t, 503, c, "%s", b)
	c, _ = e.do("GET", "/api/v1/instances/alpha/vehicles/unused?days=30", op, "")
	require.Equal(t, 503, c)
}

func TestUnusedOf(t *testing.T) {
	first := daysAgo(40)
	vs := []admin.Vehicle{{ID: "nofirst"}, {ID: "a", FirstSeen: &first}}
	require.Equal(t, []string{"a"}, ids(unusedOf(vs, 30, cleanupNow)))
	require.Empty(t, unusedOf(vs, 40, cleanupNow), "exactly 40 days is not more than 40")
}
