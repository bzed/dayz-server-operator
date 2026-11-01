// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

type fakeRecords struct {
	recs map[string]moderation.VehicleRecord
	err  error
}

func (f fakeRecords) VehicleRecords(context.Context, string) (map[string]moderation.VehicleRecord, error) {
	return f.recs, f.err
}

func syncVehicles(e *env) {
	e.alpha.Sync(admin.SyncRequest{Proto: 1, Hello: true, ModVersion: "0.1.0", World: "chernarusplus",
		Vehicles: []admin.Vehicle{{ID: "v1", Type: "OffroadHatchback"}, {ID: "v2", Type: "Sedan_02"}}})
}

func TestVehicleListHasTheLastDriver(t *testing.T) {
	e := newEnv(t)
	syncVehicles(e)
	driven := time.Unix(1700000000, 0).UTC()
	first := time.Unix(1690000000, 0).UTC()
	e.srv.Vehicles = fakeRecords{recs: map[string]moderation.VehicleRecord{
		"v1": {ID: "v1", Driver: "76561198000000001", DriverName: "alice", DrivenAt: driven, FirstSeen: first},
		"v2": {ID: "v2", FirstSeen: first},
	}}
	c, b := e.do("GET", "/api/v1/instances/alpha/vehicles", e.token(RoleViewer, nil, false), "")
	require.Equal(t, 200, c, "%s", b)
	var got []admin.Vehicle
	require.NoError(t, json.Unmarshal(b, &got))
	require.Len(t, got, 2)
	byID := map[string]admin.Vehicle{got[0].ID: got[0], got[1].ID: got[1]}
	v1, v2 := byID["v1"], byID["v2"]
	require.Equal(t, "76561198000000001", v1.LastDriver)
	require.Equal(t, "alice", v1.LastDriverName)
	require.NotNil(t, v1.LastDriven)
	require.True(t, v1.LastDriven.Equal(driven))
	require.NotNil(t, v1.FirstSeen)
	require.True(t, v1.FirstSeen.Equal(first))
	require.Empty(t, v2.LastDriver)
	require.Nil(t, v2.LastDriven)
	require.NotNil(t, v2.FirstSeen)
}

func TestVehicleListWithoutRecords(t *testing.T) {
	e := newEnv(t)
	syncVehicles(e)
	c, b := e.do("GET", "/api/v1/instances/alpha/vehicles", e.token(RoleViewer, nil, false), "")
	require.Equal(t, 200, c, "%s", b)
	var got []admin.Vehicle
	require.NoError(t, json.Unmarshal(b, &got))
	require.Len(t, got, 2)
	for _, v := range got {
		require.Empty(t, v.LastDriver)
		require.Nil(t, v.LastDriven)
		require.Nil(t, v.FirstSeen)
	}
}

func TestVehicleListRecordError(t *testing.T) {
	e := newEnv(t)
	syncVehicles(e)
	e.srv.Vehicles = fakeRecords{err: errors.New("disk")}
	c, _ := e.do("GET", "/api/v1/instances/alpha/vehicles", e.token(RoleViewer, nil, false), "")
	require.Equal(t, 500, c)
}
