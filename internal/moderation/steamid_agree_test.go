// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/moderation"
)

// admin cannot import moderation, so it carries its own copy of the Steam ID rule
// (the hub drops a driver that is no valid ID); both must agree.
func TestAdminSteamIDRuleAgreesWithModeration(t *testing.T) {
	for _, s := range []string{"76561198000000001", "", "7656", "7656119800000000x", "86561198000000001", "765611980000000011", "76561190000000000", "7656119800000000a", " 76561198000000001", "76561198000000001\n"} {
		h := admin.NewHub("t")
		h.Sync(admin.SyncRequest{Proto: 1, Hello: true, Vehicles: []admin.Vehicle{{ID: "v", Driver: s, DrivenAgo: 5}}})
		kept := h.Vehicles()[0].Driver != ""
		require.Equal(t, moderation.ValidSteamID(s), kept, "%q", s)
	}
}
