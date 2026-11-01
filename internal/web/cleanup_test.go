// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// cleanupFixture gives the mod the given features and four vehicles: two driven 40 days ago, one 3 days ago
// and one driven 40 days ago with a person inside.
func cleanupFixture(t *testing.T, role api.Role, features ...string) *env {
	t.Helper()
	e := newEnv(t, role)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, ModVersion: "0.8.0", World: "enoch", Features: features,
		Vehicles: []admin.Vehicle{
			{ID: "o1", Kind: "car", Type: "Offroad_02", Owner: "Bob", X: 7500, Z: 7600},
			{ID: "o2", Kind: "car", Type: "Sedan_02", X: 100, Z: 200},
			{ID: "r1", Kind: "car", Type: "Truck_01"},
			{ID: "c1", Kind: "car", Type: "Hatchback_02", Occupants: []string{"alice"}},
		}})
	now := time.Now()
	old, first := now.Add(-40*24*time.Hour), now.Add(-90*24*time.Hour)
	require.NoError(t, e.st.SyncVehicles(t.Context(), "alpha", []moderation.VehicleSeen{
		{ID: "o1", Type: "Offroad_02", Driver: "76561198000000001", DriverName: "Dora", DrivenAt: old},
		{ID: "o2", Type: "Sedan_02", Driver: "76561198000000002", DriverName: "Eve", DrivenAt: old},
		{ID: "r1", Type: "Truck_01", Driver: "76561198000000002", DriverName: "Eve", DrivenAt: now.Add(-3 * 24 * time.Hour)},
		{ID: "c1", Type: "Hatchback_02", Driver: "76561198000000002", DriverName: "Eve", DrivenAt: old},
	}, first))
	return e
}

func TestCleanupPageNeedsDelete(t *testing.T) {
	e := cleanupFixture(t, api.RoleViewer, admin.FeatureDriver)
	_, body, _ := e.get("/b/main/i/alpha")
	require.NotContains(t, body, "vehicles/cleanup")
	code, _, _ := e.get("/b/main/i/alpha/vehicles/cleanup?days=30")
	require.Equal(t, 403, code)
	code, _ = e.post("/b/main/i/alpha/vehicles/cleanup", url.Values{"days": {"30"}, "id": {"o1"}}, nil)
	require.Equal(t, 403, code)

	e = cleanupFixture(t, api.RoleAdmin, admin.FeatureDriver)
	_, body, _ = e.get("/b/main/i/alpha")
	require.Contains(t, body, `hx-get="/b/main/i/alpha/vehicles/cleanup"`)
}

func TestCleanupStepOffersThresholds(t *testing.T) {
	e := cleanupFixture(t, api.RoleAdmin, admin.FeatureDriver)
	code, body, _ := e.get("/b/main/i/alpha/vehicles/cleanup")
	require.Equal(t, 200, code)
	for _, v := range []string{`value="14"`, `value="30"`, `value="60"`, `min="1" max="3650"`} {
		require.Contains(t, body, v)
	}
	require.NotContains(t, body, `name="id"`, "nothing is listed before a threshold is chosen")
}

func TestCleanupPreview(t *testing.T) {
	e := cleanupFixture(t, api.RoleAdmin, admin.FeatureDriver)
	code, body, _ := e.get("/b/main/i/alpha/vehicles/cleanup?days=30")
	require.Equal(t, 200, code)
	require.Contains(t, body, `<a href="/b/main/i/alpha/map?label=Offroad_02&#43;%28Bob%29&amp;x=7500&amp;z=7600">7500 / 7600</a>`)
	require.Contains(t, body, `<input type="hidden" name="id" value="o1">`)
	require.Contains(t, body, `<input type="hidden" name="id" value="o2">`)
	require.NotContains(t, body, `value="r1"`, "a vehicle driven 3 days ago is not listed")
	require.NotContains(t, body, `value="c1"`, "a vehicle with crew is not listed")
	require.Contains(t, body, `<input type="hidden" name="days" value="30">`)
	require.Contains(t, body, "Delete 2 vehicles")
	require.Contains(t, body, `hx-confirm="Delete 2 vehicles? This cannot be undone."`)
}

func TestCleanupPreviewNothingToDelete(t *testing.T) {
	e := cleanupFixture(t, api.RoleAdmin, admin.FeatureDriver)
	_, body, _ := e.get("/b/main/i/alpha/vehicles/cleanup?days=100")
	require.Contains(t, body, "No vehicle has gone unused for 100 days.")
	require.NotContains(t, body, `name="id"`)
}

func TestCleanupPreviewSaysTrackingIsYoung(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	e.hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, ModVersion: "0.8.0", Features: []string{admin.FeatureDriver},
		Vehicles: []admin.Vehicle{{ID: "o1", Kind: "car", Type: "Offroad_02"}}})
	first := time.Now().Add(-3 * 24 * time.Hour)
	require.NoError(t, e.st.SyncVehicles(t.Context(), "alpha", []moderation.VehicleSeen{{ID: "o1", Type: "Offroad_02"}}, first))
	_, body, _ := e.get("/b/main/i/alpha/vehicles/cleanup?days=14")
	require.Contains(t, body, "dzo has been tracking vehicles on this instance only since "+first.UTC().Format("2006-01-02")+"; no vehicle can be older than that yet.")
	require.NotContains(t, body, "<form hx-post")
}

func TestCleanupBadDays(t *testing.T) {
	e := cleanupFixture(t, api.RoleAdmin, admin.FeatureDriver)
	for _, d := range []string{"0", "3651", "abc"} {
		_, body, _ := e.get("/b/main/i/alpha/vehicles/cleanup?days=" + d)
		require.Contains(t, body, "days must be a whole number from 1 to 3650", d)
		require.NotContains(t, body, `name="id"`)
	}
}

func TestCleanupConfirm(t *testing.T) {
	e := cleanupFixture(t, api.RoleAdmin, admin.FeatureDriver)
	code, body := e.post("/b/main/i/alpha/vehicles/cleanup", url.Values{"days": {"30"}, "id": {"o1", "o2", "c1"}}, nil)
	require.Equal(t, 200, code)
	require.Contains(t, body, "Deleted 2 vehicles; skipped 1:")
	require.Contains(t, body, "c1")
	require.Contains(t, body, "crew inside")
	n := 0
	for _, c := range e.hub.Sync(admin.SyncRequest{Proto: 1}).Commands {
		if c.Kind == admin.KindVehicleDelete {
			n++
		}
	}
	require.Equal(t, 2, n)
}

func TestCleanupConfirmRefusesBadInput(t *testing.T) {
	e := cleanupFixture(t, api.RoleAdmin, admin.FeatureDriver)
	_, body := e.post("/b/main/i/alpha/vehicles/cleanup", url.Values{"days": {"30"}}, nil)
	require.Contains(t, body, "flash err")
}

func TestCleanupCSRF(t *testing.T) {
	e := cleanupFixture(t, api.RoleAdmin, admin.FeatureDriver)
	code, _ := e.post("/b/main/i/alpha/vehicles/cleanup", url.Values{"days": {"30"}, "id": {"o1"}}, map[string]string{"X-CSRF-Token": "wrong"})
	require.Equal(t, 403, code)
}

func TestCleanupOldMod(t *testing.T) {
	e := cleanupFixture(t, api.RoleAdmin)
	_, body, _ := e.get("/b/main/i/alpha/vehicles/cleanup?days=30")
	require.Contains(t, body, "does not report drivers")
	require.Contains(t, body, "flash err")
	require.NotContains(t, body, `name="id"`)
}

func TestCleanupPreviewListsOnlyTheOldest500(t *testing.T) {
	e := newEnv(t, api.RoleAdmin)
	const n = 501
	vs := make([]admin.Vehicle, 0, n)
	seen := make([]moderation.VehicleSeen, 0, n)
	now := time.Now()
	for i := range n {
		id := fmt.Sprintf("v%03d", i)
		vs = append(vs, admin.Vehicle{ID: id, Kind: "car", Type: "Offroad_02"})
		// v000 is the most recently driven, v500 the oldest.
		seen = append(seen, moderation.VehicleSeen{ID: id, Type: "Offroad_02", Driver: "76561198000000001", DrivenAt: now.Add(-time.Duration(40+i) * time.Hour * 24)})
	}
	e.hub.Sync(admin.SyncRequest{Proto: 1, Hello: true, ModVersion: "0.8.0", Features: []string{admin.FeatureDriver}, Vehicles: vs})
	require.NoError(t, e.st.SyncVehicles(t.Context(), "alpha", seen, now.Add(-1000*24*time.Hour)))
	_, body, _ := e.get("/b/main/i/alpha/vehicles/cleanup?days=30")
	require.Equal(t, 500, strings.Count(body, `name="id"`))
	require.Contains(t, body, `value="v500"`, "the oldest are listed")
	require.NotContains(t, body, `value="v000"`, "the newest is left for the next round")
	require.Contains(t, body, "Delete 500 vehicles")
	require.Contains(t, body, "501 vehicles have gone unused for 30 days; the 500 longest unused are listed. Delete them, then run the cleanup again for the rest.")
}
