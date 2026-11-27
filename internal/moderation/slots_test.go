// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/battleye"
	"github.com/stretchr/testify/require"
)

func TestEnforcerSlotTableIsBounded(t *testing.T) {
	e := newSvc(t, "alpha")
	r := &running{inst: Instance{Name: "alpha"}, rcon: newFakeRCon()}
	slots := map[int]*slot{}
	long := strings.Repeat("A", 3000)
	for i := -5; i < 6000; i++ {
		e.svc.handle(t.Context(), r, slots, battleye.Event{Kind: battleye.EventConnect, PlayerID: i, PlayerName: long, Address: "192.0.2.7:2304"})
		e.svc.handle(t.Context(), r, slots, battleye.Event{Kind: battleye.EventGUID, PlayerID: i + 10000, PlayerName: long, GUID: "abc"})
	}
	require.LessOrEqual(t, len(slots), maxSlot+1)
	for _, sl := range slots {
		require.LessOrEqual(t, len(sl.name), maxName)
	}
	require.Contains(t, slots, 0)
	require.Contains(t, slots, maxSlot)
	require.NotContains(t, slots, maxSlot+1)
	require.NotContains(t, slots, -1)
}

func TestReconnectClearsTheSlots(t *testing.T) {
	e := newSvc(t, "alpha")
	r := e.rcon["alpha"]
	g := guidOf(t, steamA)
	r.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 3, PlayerName: "alice", Address: "192.0.2.7:2304"}
	r.events <- battleye.Event{Kind: battleye.EventReconnect}
	// The GUID line of an unknown slot has no address, so no sighting is stored.
	r.events <- battleye.Event{Kind: battleye.EventGUID, PlayerID: 3, PlayerName: "alice", GUID: g}
	r.events <- battleye.Event{Kind: battleye.EventChat, Channel: "Global", PlayerName: "alice", Message: "done"}
	eventually(t, "the chat event", func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return len(e.seen) > 0 && strings.HasSuffix(e.seen[len(e.seen)-1], "done")
	})
	ids, err := e.st.Identities(t.Context(), steamA)
	require.NoError(t, err)
	require.Empty(t, ids)
}
