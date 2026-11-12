// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// The mod sends the vehicles as raw JSON; the stored fields are DayZ Expansion's virtual garage.
func TestSyncParsesStoredVehicle(t *testing.T) {
	body := []byte(`{"token":"t","protocol":1,"has":["vehicles"],"vehicles":[{"id":"g1.2.3.4","kind":"car","type":"OffroadHatchback","x":100,"y":0,"z":200,"yaw":0,"stored":true,"stored_by":"76561198000000001","territory_id":0,"owners":["76561198000000001"],"broken":false}]}`)
	var req SyncRequest
	require.NoError(t, json.Unmarshal(body, &req))
	req.Normalise()
	h, _ := testHub()
	h.Sync(req)
	got := h.Vehicles()
	require.Len(t, got, 1)
	v := got[0]
	require.True(t, bool(v.Stored))
	require.Equal(t, "76561198000000001", v.StoredBy)
	require.Zero(t, v.TerritoryID)
	require.Len(t, v.Owners, 1)
	require.Equal(t, "76561198000000001", v.Owners[0])
	require.False(t, bool(v.Broken))
}

// An old mod sends none of the stored fields: they parse to their zero values.
func TestSyncParsesVehicleWithoutStoredFields(t *testing.T) {
	body := []byte(`{"token":"t","protocol":1,"has":["vehicles"],"vehicles":[{"id":"g1.2.3.4","kind":"car","type":"OffroadHatchback","x":100,"y":0,"z":200,"yaw":0}]}`)
	var req SyncRequest
	require.NoError(t, json.Unmarshal(body, &req))
	req.Normalise()
	h, _ := testHub()
	h.Sync(req)
	got := h.Vehicles()
	require.Len(t, got, 1)
	require.False(t, bool(got[0].Stored))
	require.Nil(t, got[0].Owners)
}
