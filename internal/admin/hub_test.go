// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func testHub() (*Hub, *clock) {
	c := &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	h := NewHub("alpha")
	h.now = c.now
	return h, c
}

func TestSyncSetsDrivenAt(t *testing.T) {
	h, c := testHub()
	h.Sync(SyncRequest{Proto: 1, Hello: true, Vehicles: []Vehicle{{ID: "v1", Driver: "76561198000000001", DriverName: "Ann", DrivenAgo: 90}}})
	got := h.Vehicles()
	require.Len(t, got, 1)
	require.Equal(t, c.now().Add(-90*time.Second), got[0].DrivenAt)
	require.Equal(t, "76561198000000001", got[0].Driver)
	require.Equal(t, "Ann", got[0].DriverName)
}

func TestSyncIgnoresBadDrives(t *testing.T) {
	h, _ := testHub()
	for _, v := range []Vehicle{
		{ID: "a", Driver: "76561198000000001", DriverName: "n", DrivenAgo: -1},
		{ID: "b", Driver: "76561198000000001", DriverName: "n", DrivenAgo: math.NaN()},
		{ID: "c", Driver: "76561198000000001", DriverName: "n", DrivenAgo: math.Inf(1)},
		{ID: "d", Driver: "76561198000000001", DriverName: "n", DrivenAgo: 401 * 24 * 3600},
		{ID: "e", Driver: "not-a-steam-id", DriverName: "n", DrivenAgo: 10},
	} {
		h.Sync(SyncRequest{Proto: 1, Hello: true, Vehicles: []Vehicle{v}})
		got := h.Vehicles()
		require.Len(t, got, 1, v.ID)
		require.True(t, got[0].DrivenAt.IsZero(), v.ID)
		require.Empty(t, got[0].Driver, v.ID)
		require.Empty(t, got[0].DriverName, v.ID)
		require.Zero(t, got[0].DrivenAgo, v.ID)
	}
}

func TestSyncClipsTheDriverName(t *testing.T) {
	h, _ := testHub()
	h.Sync(SyncRequest{Proto: 1, Hello: true, Vehicles: []Vehicle{{ID: "v", Driver: "76561198000000001", DriverName: strings.Repeat("ä", 500), DrivenAgo: 1}}})
	require.Len(t, []rune(h.Vehicles()[0].DriverName), 65)
}

func TestVehicleJSONHidesDrivenAt(t *testing.T) {
	b, err := json.Marshal(Vehicle{ID: "v", DrivenAt: time.Now()})
	require.NoError(t, err)
	require.NotContains(t, string(b), "DrivenAt")
	require.NotContains(t, string(b), "driven")
	require.NotContains(t, string(b), "driver")
}

func hello(h *Hub) {
	h.Sync(SyncRequest{Proto: 1, Hello: true, ModVersion: "0.1.0", World: "chernarusplus", Players: []Player{}})
}

func TestSyncStoresStateAndLinksVehicles(t *testing.T) {
	h, _ := testHub()
	h.Sync(SyncRequest{
		Proto: 1, Hello: true, ModVersion: "0.1.0", World: "chernarusplus",
		Players:  []Player{{SteamID: "7656", Name: "a"}, {SteamID: "7657", Name: "b"}},
		Vehicles: []Vehicle{{ID: "v1", Occupants: []string{"7657"}}},
		Markers:  []Marker{{Layer: "ufo", ID: "1"}},
		Layers:   []Layer{{Name: "ufo", Icon: "ufo"}},
		Events:   []Event{{ID: "e1"}},
	})
	st := h.Status()
	require.True(t, st.Connected)
	require.Equal(t, 2, st.Players)
	require.Equal(t, 1, st.Vehicles)
	require.Equal(t, 1, st.Markers)
	require.Equal(t, 1, st.Events)
	require.Equal(t, "chernarusplus", st.Hello.World)
	players := h.Players()
	require.Empty(t, players[0].Vehicle)
	require.Equal(t, "v1", players[1].Vehicle)
	got := h.Layers()
	require.Len(t, got, 1)
	require.Equal(t, "ufo", got[0].Icon)
	require.Len(t, h.Vehicles(), 1)
	require.Len(t, h.Events(), 1)
	require.Len(t, h.Markers("ufo"), 1)
	require.Empty(t, h.Markers("other"))
}

func TestPartialSyncKeepsOtherState(t *testing.T) {
	h, _ := testHub()
	h.Sync(SyncRequest{Proto: 1, Hello: true, Players: []Player{{SteamID: "1"}}, Vehicles: []Vehicle{{ID: "v"}}})
	h.Sync(SyncRequest{Proto: 1, Players: []Player{}})
	require.Empty(t, h.Players())
	require.Len(t, h.Vehicles(), 1)
}

func TestConnectedExpires(t *testing.T) {
	h, c := testHub()
	require.False(t, h.Status().Connected)
	hello(h)
	c.advance(onlineWindow + time.Second)
	require.False(t, h.Status().Connected)
}

func TestSubmitOffline(t *testing.T) {
	h, _ := testHub()
	_, err := h.Submit(t.Context(), Command{Kind: KindMessage, Text: "hi"})
	require.ErrorIs(t, err, ErrOffline)
}

func TestCommandRoundTrip(t *testing.T) {
	h, _ := testHub()
	hello(h)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan Result, 1)
	go func() {
		r, err := h.Submit(ctx, Command{Kind: KindMessage, Text: "hello"})
		assert.NoError(t, err)
		done <- r
	}()
	var cmd Command
	require.Eventually(t, func() bool {
		if r := h.Sync(SyncRequest{Proto: 1}); len(r.Commands) > 0 {
			cmd = r.Commands[0]
		}
		return cmd.ID != ""
	}, 5*time.Second, time.Millisecond, "the command is handed out")
	require.Equal(t, KindMessage, cmd.Kind)
	require.Equal(t, "chat", cmd.Style)
	require.Equal(t, "hello", cmd.Text)
	h.Sync(SyncRequest{Proto: 1, Results: []Result{{ID: cmd.ID, OK: true}}})
	r := <-done
	require.True(t, bool(r.OK))
	require.Equal(t, cmd.ID, r.ID)
	r, ok, err := h.Result(cmd.ID)
	require.NoError(t, err)
	require.True(t, ok, "Result lookup")
	require.True(t, bool(r.OK))
	sr := h.Sync(SyncRequest{Proto: 1})
	require.Empty(t, sr.Commands, "a finished command must not be sent again")
}

func TestResendAndTimeout(t *testing.T) {
	h, c := testHub()
	hello(h)
	id, err := h.Enqueue(Command{Kind: KindVehicleDelete, Vehicle: "v"})
	require.NoError(t, err)
	got := h.Sync(SyncRequest{Proto: 1}).Commands
	require.Len(t, got, 1, "first sync commands")
	got = h.Sync(SyncRequest{Proto: 1}).Commands
	require.Empty(t, got, "must not resend immediately")
	c.advance(resendAfter)
	got = h.Sync(SyncRequest{Proto: 1}).Commands
	require.Len(t, got, 1, "must resend an unanswered command")
	require.Equal(t, id, got[0].ID)
	c.advance(commandTimeout)
	got = h.Sync(SyncRequest{Proto: 1}).Commands
	require.Empty(t, got, "expired command still sent")
	r, ok, err := h.Result(id)
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, bool(r.OK))
	require.Equal(t, ErrTimeout.Error(), r.Message)
	_, ok, err = h.Result("nope")
	require.False(t, ok)
	require.ErrorIs(t, err, ErrUnknownCommand)
}

func TestFinishedCommandsAreForgotten(t *testing.T) {
	h, c := testHub()
	hello(h)
	done, err := h.Enqueue(Command{Kind: KindVehicleDelete, Vehicle: "a"})
	require.NoError(t, err)
	open, err := h.Enqueue(Command{Kind: KindVehicleDelete, Vehicle: "b"})
	require.NoError(t, err)
	h.Sync(SyncRequest{Proto: 1, Results: []Result{{ID: done, OK: true}}})

	c.advance(resultKeep - time.Second)
	h.Sync(SyncRequest{Proto: 1})
	_, ok, err := h.Result(done)
	require.NoError(t, err, "a result is kept for resultKeep")
	require.True(t, ok)

	c.advance(2 * time.Second)
	h.Sync(SyncRequest{Proto: 1})
	_, _, err = h.Result(done)
	require.ErrorIs(t, err, ErrUnknownCommand, "a result older than resultKeep is dropped")
	r, ok, err := h.Result(open)
	require.NoError(t, err, "a command that timed out keeps its result for resultKeep too")
	require.True(t, ok)
	require.Equal(t, ErrTimeout.Error(), r.Message)
	_, err = h.Wait(t.Context(), done)
	require.Error(t, err)
}

func TestSubmitContextTimeout(t *testing.T) {
	h, _ := testHub()
	hello(h)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	_, err := h.Submit(ctx, Command{Kind: KindVehicleDelete, Vehicle: "v"})
	require.ErrorIs(t, err, ErrTimeout)
}

func TestValidate(t *testing.T) {
	h, _ := testHub()
	h.Deny = []string{"Land_*"}
	h.Allow = []string{"Ammo*", "AKM"}
	hello(h)
	cases := []struct {
		name string
		cmd  Command
		want string
	}{
		{"no text", Command{Kind: KindMessage}, "needs text"},
		{"long text", Command{Kind: KindMessage, Text: strings.Repeat("x", 401)}, "longer"},
		{"style", Command{Kind: KindMessage, Text: "x", Style: "loud"}, "style"},
		{"tp nothing", Command{Kind: KindTeleport, SteamID: "1"}, "position"},
		{"tp no player", Command{Kind: KindTeleport, X: 1}, "steam_id"},
		{"kill no player", Command{Kind: KindPlayerKill}, "steam_id"},
		{"heal no player", Command{Kind: KindPlayerHeal}, "steam_id"},
		{"fix gear no player", Command{Kind: KindPlayerFixGear}, "steam_id"},
		{"spawn bad class", Command{Kind: KindSpawnItem, SteamID: "1", Type: "a b"}, "valid class"},
		{"spawn denied", Command{Kind: KindSpawnItem, SteamID: "1", Type: "Land_Wall"}, "denied"},
		{"spawn not allowed", Command{Kind: KindSpawnItem, SteamID: "1", Type: "M4A1"}, "allow list"},
		{"spawn target", Command{Kind: KindSpawnItem, SteamID: "1", Type: "AKM", Target: "moon"}, "target"},
		{"spawn health", Command{Kind: KindSpawnItem, SteamID: "1", Type: "AKM", Health: 2}, "health"},
		{"spawn no class", Command{Kind: KindSpawnItem, SteamID: "1"}, "needs type"},
		{"spawn at nowhere", Command{Kind: KindSpawnItem, Type: "AKM"}, "position"},
		{"spawn at into hands", Command{Kind: KindSpawnItem, Type: "AKM", X: 1, Z: 2, Target: "hands"}, "ground"},
		{"spawn vehicle no class", Command{Kind: KindSpawnVehicle, X: 1, Z: 2}, "needs type"},
		{"spawn vehicle denied", Command{Kind: KindSpawnVehicle, Type: "Land_Wall", X: 1, Z: 2}, "denied"},
		{"spawn vehicle nowhere", Command{Kind: KindSpawnVehicle, Type: "AKM"}, "position"},
		{"repair scope", Command{Kind: KindVehicleRepair, Vehicle: "v", Scope: "x"}, "scope"},
		{"repair no vehicle", Command{Kind: KindVehicleRepair}, "vehicle"},
		{"flip no vehicle", Command{Kind: KindVehicleFlip}, "vehicle"},
		{"move no vehicle", Command{Kind: KindVehicleMove, X: 1, Z: 2}, "vehicle"},
		{"move nowhere", Command{Kind: KindVehicleMove, Vehicle: "v"}, "position"},
		{"delete no vehicle", Command{Kind: KindVehicleDelete}, "vehicle"},
		{"raise no flag", Command{Kind: KindFlagRaise}, "flag"},
		{"kind", Command{Kind: "explode"}, "unknown command"},
		{"quantity below -2", Command{Kind: KindSpawnItem, SteamID: "1", Type: "AKM", Quantity: -3}, "quantity"},
		{"quantity between", Command{Kind: KindSpawnItem, SteamID: "1", Type: "AKM", Quantity: -1.5}, "quantity"},
	}
	for _, tc := range cases {
		_, err := h.Enqueue(tc.cmd)
		require.ErrorContains(t, err, tc.want)
	}
	for _, ok := range []Command{
		{Kind: KindSpawnItem, SteamID: "1", Type: "AKM"},
		{Kind: KindSpawnItem, SteamID: "1", Type: "Ammo_556x45", Target: "hands", Quantity: 5, Health: 1},
		{Kind: KindSpawnItem, Type: "AKM", X: 100, Z: 200, Quantity: 20},
		{Kind: KindSpawnItem, Type: "Ammo_556x45", X: 100, Z: 200, Quantity: QuantityMax},
		{Kind: KindSpawnItem, Type: "Ammo_556x45", X: 100, Z: 200, Quantity: QuantityDefault},
		{Kind: KindSpawnItem, Type: "Ammo_556x45", X: 100, Z: 200, Quantity: 0},
		{Kind: KindSpawnVehicle, Type: "AKM", X: 100, Z: 200},
		{Kind: KindVehicleRepair, Vehicle: "v", Scope: "wheels"},
		{Kind: KindVehicleRepair, Vehicle: "v", Scope: "missing"},
		{Kind: KindVehicleFlip, Vehicle: "v"},
		{Kind: KindVehicleMove, Vehicle: "v", X: 100, Z: 200},
		{Kind: KindFlagRaise, Flag: "f1"},
		{Kind: KindTeleport, SteamID: "1", ToSteamID: "2"},
		{Kind: KindPlayerKill, SteamID: "1"},
		{Kind: KindPlayerHeal, SteamID: "1"},
		{Kind: KindPlayerFixGear, SteamID: "1"},
	} {
		_, err := h.Enqueue(ok)
		assert.NoError(t, err, "%+v", ok)
	}
}

func TestMoveRefusesACoveredVehicle(t *testing.T) {
	h, _ := testHub()
	hello(h)
	h.Sync(SyncRequest{Proto: 1, Vehicles: []Vehicle{{ID: "v1", Kind: "car", Type: "Hatchback_02", Covered: true}, {ID: "v2", Kind: "car", Type: "Hatchback_02"}}})
	_, err := h.Enqueue(Command{Kind: KindVehicleMove, Vehicle: "v1", X: 1, Z: 2})
	require.ErrorContains(t, err, "covered")
	_, err = h.Enqueue(Command{Kind: KindVehicleMove, Vehicle: "v2", X: 1, Z: 2})
	require.NoError(t, err)
}

func TestSpawnAtPositionDefaultsToGround(t *testing.T) {
	h, _ := testHub()
	hello(h)
	_, err := h.Enqueue(Command{Kind: KindSpawnItem, Type: "AKM", X: 5, Z: 6})
	require.NoError(t, err)
	r := h.Sync(SyncRequest{Proto: 1})
	require.Len(t, r.Commands, 1)
	require.Equal(t, "ground", r.Commands[0].Target)
	require.Empty(t, r.Commands[0].SteamID)
}

func TestSpawnVehicleChecksVehicleTypes(t *testing.T) {
	h, _ := testHub()
	h.Sync(SyncRequest{Proto: 1, Hello: true, Types: &TypesChunk{Hash: "i", Offset: 0, Total: 1, Names: []string{"AKM"}},
		VehicleTypes: &TypesChunk{Hash: "v", Offset: 0, Total: 2, Names: []string{"Offroad_02", "Hatchback_02"}}})
	got := h.VehicleTypes("off", 5)
	require.Len(t, got, 1)
	require.Equal(t, "Offroad_02", got[0])
	_, err := h.Enqueue(Command{Kind: KindSpawnVehicle, Type: "Offroad_02", X: 1, Z: 2})
	require.NoError(t, err)
	// An item is no vehicle, and a vehicle is no item.
	_, err = h.Enqueue(Command{Kind: KindSpawnVehicle, Type: "AKM", X: 1, Z: 2})
	require.ErrorContains(t, err, "unknown class")
	_, err = h.Enqueue(Command{Kind: KindSpawnItem, Type: "Offroad_02", X: 1, Z: 2})
	require.ErrorContains(t, err, "unknown class")
	// A vehicle chunk that does not follow the ones before asks the mod to start again.
	r := h.Sync(SyncRequest{Proto: 1, VehicleTypes: &TypesChunk{Hash: "w", Offset: 3, Total: 9, Names: []string{"x"}}})
	require.True(t, r.ResendTypes)
}

func TestSpawnChecksKnownTypes(t *testing.T) {
	h, _ := testHub()
	h.Sync(SyncRequest{Proto: 1, Hello: true, Types: &TypesChunk{Hash: "h", Offset: 0, Total: 3, Names: []string{"B", "A"}}})
	// Incomplete list: nothing is rejected on it yet.
	_, err := h.Enqueue(Command{Kind: KindSpawnItem, SteamID: "1", Type: "Zed"})
	require.NoError(t, err, "incomplete list must not reject")
	h.Sync(SyncRequest{Proto: 1, Types: &TypesChunk{Hash: "h", Offset: 2, Total: 3, Names: []string{"C"}}})
	h.Sync(SyncRequest{Proto: 1, Types: &TypesChunk{Hash: "h", Offset: 9, Total: 3, Names: []string{"X"}}}) // out of order, ignored
	got := h.Types("", 0)
	require.Len(t, got, 3)
	require.Equal(t, "A", got[0])
	got = h.Types("b", 10)
	require.Len(t, got, 1)
	require.Equal(t, "B", got[0])
	got = h.Types("", 2)
	require.Len(t, got, 2)
	_, err = h.Enqueue(Command{Kind: KindSpawnItem, SteamID: "1", Type: "Zed"})
	require.ErrorContains(t, err, "unknown class")
	_, err = h.Enqueue(Command{Kind: KindSpawnItem, SteamID: "1", Type: "A"})
	require.NoError(t, err)
	// A new hash restarts the transfer.
	h.Sync(SyncRequest{Proto: 1, Types: &TypesChunk{Hash: "h2", Offset: 0, Total: 1, Names: []string{"Q"}}})
	got = h.Types("", 0)
	require.Len(t, got, 1)
	require.Equal(t, "Q", got[0])
}

func TestSubscribe(t *testing.T) {
	h, _ := testHub()
	ctx, cancel := context.WithCancel(t.Context())
	ch := h.Subscribe(ctx)
	h.Sync(SyncRequest{Proto: 1, Hello: true, Players: []Player{{SteamID: "1"}}})
	kinds := map[string]bool{}
	for len(ch) > 0 {
		kinds[(<-ch).Kind] = true
	}
	require.True(t, kinds["status"])
	require.True(t, kinds["players"])
	cancel()
	for range ch { // closed after cancel
	}
	// A full buffer must not block Sync.
	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()
	_ = h.Subscribe(ctx2)
	for i := 0; i < 100; i++ {
		h.Sync(SyncRequest{Proto: 1, Players: []Player{}})
	}
}

func TestMatchGlobs(t *testing.T) {
	require.True(t, MatchGlobs([]string{"A*", "B"}, "Abc"))
	require.True(t, MatchGlobs([]string{"B"}, "B"))
	require.False(t, MatchGlobs([]string{"B"}, "Bc"))
	require.False(t, MatchGlobs([]string{""}, ""))
	require.False(t, MatchGlobs(nil, "x"))
	require.True(t, MatchGlobs([]string{"Wreck_*_DE"}, "Wreck_Train_DE"))
	require.False(t, MatchGlobs([]string{"Wreck_*_DE"}, "Wreck_Train"))
	require.False(t, MatchGlobs([]string{"ab*ba"}, "aba"))
	require.True(t, MatchGlobs([]string{"*_DE"}, "X_DE"))
}

func TestHubAsksForTheHelloItMissed(t *testing.T) {
	h := NewHub("alpha")
	// dzo serve restarted under a running server: the first sync it sees is no hello.
	r := h.Sync(SyncRequest{Proto: 1})
	assert.True(t, r.Hello)
	assert.True(t, h.Status().Connected)
	r = h.Sync(SyncRequest{Proto: 1, Hello: true, World: "chernarusplus"})
	assert.False(t, r.Hello)
	got := h.Status().Hello.World
	assert.Equal(t, "chernarusplus", got)
	r = h.Sync(SyncRequest{Proto: 1})
	assert.False(t, r.Hello)
}

func TestSyncVehicleKind(t *testing.T) {
	r := SyncRequest{Vehicles: []Vehicle{{ID: "a"}, {ID: "b", Kind: "motorbike"}}}
	r.Normalise()
	require.Equal(t, "car", r.Vehicles[0].Kind)
	assert.Equal(t, "motorbike", r.Vehicles[1].Kind)
}

// Bicycles and the helicopters of mods are cars to the game (they derive from CarScript); the hub gives
// the vehicles whose class matches the classes of a kind that kind, for the icon and the list.
func TestSyncMarksVehicleKindsByClass(t *testing.T) {
	h := NewHub("x")
	h.Kinds = DefaultVehicleKinds()
	h.Sync(SyncRequest{Has: []string{"vehicles"}, Vehicles: []Vehicle{
		{ID: "1", Kind: "car", Type: "Old_Bike_Red"}, {ID: "2", Kind: "car", Type: "Lady_Bicycle_Purple"},
		{ID: "3", Kind: "car", Type: "Old_BikeArma"}, {ID: "4", Kind: "car", Type: "MMTArma"},
		{ID: "5", Kind: "car", Type: "Offroad_02"}, {ID: "6", Kind: "motorbike", Type: "Old_Bike"},
		{ID: "7", Kind: "car", Type: "ExpansionMh6_Wasp"}, {ID: "8", Kind: "car", Type: "ExpansionUh1h"},
		{ID: "9", Kind: "car", Type: "ExpansionMerlin_Ion"}, {ID: "10", Kind: "car", Type: "ExpansionGyrocopter"},
		{ID: "11", Kind: "car", Type: "RFMosquito"}, {ID: "12", Kind: "car", Type: "ExpansionUAZ"},
	}})
	got := map[string]string{}
	for _, v := range h.Vehicles() {
		got[v.ID] = v.Kind
	}
	want := map[string]string{"1": "bicycle", "2": "bicycle", "3": "bicycle", "4": "bicycle", "5": "car", "6": "motorbike", "7": "helicopter", "8": "helicopter", "9": "helicopter", "10": "helicopter", "11": "helicopter", "12": "car"}
	for id, k := range want {
		assert.Equal(t, k, got[id])
	}
}

func TestStatusCarriesTheNextRestartAndAnnouncesChanges(t *testing.T) {
	h := NewHub("x")
	next := time.Date(2026, 10, 16, 5, 0, 0, 0, time.UTC)
	h.NextRestart = func(time.Time) time.Time { return next }
	h.Sync(SyncRequest{Hello: true, World: "w"})
	ch := h.Subscribe(t.Context())
	s := h.Status()
	require.NotNil(t, s.NextRestart)
	require.True(t, s.NextRestart.Equal(next))
	h.Sync(SyncRequest{Has: []string{"players"}, Players: []Player{{SteamID: "1"}}})
	select {
	case ev := <-ch:
		for ev.Kind != "status" {
			ev = <-ch
		}
		s := ev.Data.(Status)
		assert.Equal(t, 1, s.Players)
	case <-time.After(time.Second):
		require.FailNow(t, "no status event when the player count changed")
	}
	h2 := NewHub("y")
	assert.Nil(t, h2.Status().NextRestart)
}

func TestStatusCarriesTheGameTime(t *testing.T) {
	h := NewHub("x")
	h.Sync(SyncRequest{Hello: true, World: "w"})
	assert.Empty(t, h.Status().GameTime)
	ch := h.Subscribe(t.Context())
	h.Sync(SyncRequest{Has: []string{"clock"}, Clock: 14*60 + 5})
	got := h.Status().GameTime
	assert.Equal(t, "14:05", got)
	select {
	case ev := <-ch:
		s, ok := ev.Data.(Status)
		require.True(t, ok)
		require.Equal(t, "status", ev.Kind)
		assert.Equal(t, "14:05", s.GameTime)
	case <-time.After(time.Second):
		require.FailNow(t, "no status event for the new time")
	}
}

func TestHubAsksForTheClassListAgainWhenAChunkDoesNotFit(t *testing.T) {
	h := NewHub("x")
	h.Sync(SyncRequest{Hello: true, World: "w"})
	// the first chunk was lost (dzo restarted): the next one starts at 2
	r := h.Sync(SyncRequest{Types: &TypesChunk{Hash: "h", Offset: 2, Total: 4, Names: []string{"c", "d"}}})
	require.True(t, r.ResendTypes, "a chunk that does not follow should make the hub ask again")
	r = h.Sync(SyncRequest{Types: &TypesChunk{Hash: "h", Offset: 0, Total: 4, Names: []string{"a", "b"}}})
	assert.False(t, r.ResendTypes)
	h.Sync(SyncRequest{Types: &TypesChunk{Hash: "h", Offset: 2, Total: 4, Names: []string{"c", "d"}}})
	got := h.Types("", 0)
	assert.Len(t, got, 4)
}

// queued enqueues cmd and returns the command as the mod receives it.
func queued(t testing.TB, h *Hub, cmd Command) Command {
	t.Helper()
	_, err := h.Enqueue(cmd)
	require.NoError(t, err, "Enqueue")
	r := h.Sync(SyncRequest{Proto: 1})
	require.Len(t, r.Commands, 1)
	return r.Commands[0]
}

func TestMessageExpansionNeedsTheFeature(t *testing.T) {
	h, _ := testHub()
	hello(h)
	_, err := h.Enqueue(Command{Kind: KindMessage, Style: "expansion", NotifyType: "toast", Text: "x"})
	require.ErrorContains(t, err, "expansion notifications are not available")
	h.Sync(SyncRequest{Proto: 1, Hello: true, World: "w", Features: []string{"expansion"}})
	_, err = h.Enqueue(Command{Kind: KindMessage, Style: "expansion", NotifyType: "toast", Text: "x"})
	require.NoError(t, err, "with the feature")
}

func TestMessageExpansionTypeIsChecked(t *testing.T) {
	h, _ := testHub()
	h.Sync(SyncRequest{Proto: 1, Hello: true, World: "w", Features: []string{"expansion"}})
	_, err := h.Enqueue(Command{Kind: KindMessage, Style: "expansion", NotifyType: "popup", Text: "x"})
	require.ErrorContains(t, err, "baguette")
	c := queued(t, h, Command{Kind: KindMessage, Style: "expansion", Text: "x"})
	assert.Equal(t, "toast", c.NotifyType)
}

func TestMessageTitleAndSecondsLimits(t *testing.T) {
	h, _ := testHub()
	hello(h)
	for name, c := range map[string]Command{
		"title":    {Kind: KindMessage, Style: "notification", Text: "x", Title: strings.Repeat("t", 65)},
		"seconds":  {Kind: KindMessage, Style: "notification", Text: "x", Seconds: 61},
		"negative": {Kind: KindMessage, Style: "notification", Text: "x", Seconds: -1},
	} {
		_, err := h.Enqueue(c)
		require.Error(t, err, "%s: accepted", name)
	}
	got := queued(t, h, Command{Kind: KindMessage, Style: "notification", Text: "x"})
	require.Equal(t, "Server", got.Title)
	assert.Equal(t, 10, got.Seconds)
}

func TestHelloKeepsFeatures(t *testing.T) {
	h, _ := testHub()
	hello(h)
	assert.False(t, h.Status().Hello.Has("expansion"))
	h.Sync(SyncRequest{Proto: 1, Hello: true, World: "w", Features: []string{"expansion"}})
	hl := h.Status().Hello
	require.True(t, hl.Has("expansion"))
	assert.Len(t, hl.Features, 1)
	var none *Hello
	assert.False(t, none.Has("expansion"), "nil hello has a feature")
}

func TestMessageStyleRConIsRefusedByTheHub(t *testing.T) {
	h, _ := testHub()
	hello(h)
	_, err := h.Enqueue(Command{Kind: KindMessage, Style: "rcon", Text: "x"})
	require.Error(t, err, "the hub accepted the rcon style")
}

// The mod reports territory flags and Expansion territories as markers: the remaining time is the ttl, the
// circle has its radius, and the layers the mod defines keep their visibility.
func TestHubKeepsFlagMarkers(t *testing.T) {
	h, _ := testHub()
	h.Sync(SyncRequest{
		Proto: 1, Hello: true, ModVersion: "0.4.0", World: "chernarusplus",
		Markers: []Marker{
			{Layer: "Flags", ID: "f1", Shape: "point", X: 7600, Z: 7600, Icon: "flag", Label: "Flag", TTL: 86400, Source: "flags", Visibility: "operator"},
			{Layer: "Territories", ID: "t1", Shape: "circle", X: 7600, Z: 7600, Radius: 150, Label: "Camp", Source: "territory", Visibility: "operator"},
		},
		Layers: []Layer{{Name: "Flags", Icon: "flag", Visibility: "operator"}, {Name: "Territories", Visibility: "operator"}},
	})
	f := h.Markers("Flags")
	require.Len(t, f, 1)
	require.Equal(t, 86400, f[0].TTL)
	require.Equal(t, "flags", f[0].Source)
	require.Equal(t, "flag", f[0].Icon)
	require.Equal(t, "operator", f[0].Visibility)
	tr := h.Markers("Territories")
	require.Len(t, tr, 1)
	assert.InDelta(t, 150, tr[0].Radius, 1e-9)
	require.Equal(t, "Camp", tr[0].Label)
	require.Equal(t, "territory", tr[0].Source)
	require.Equal(t, "operator", tr[0].Visibility)
	got := h.Layers()
	require.Len(t, got, 2)
	require.Equal(t, "operator", got[0].Visibility)
	require.Equal(t, "operator", got[1].Visibility)
}

func TestWaitReturnsTheEnqueuedResult(t *testing.T) {
	h, _ := testHub()
	hello(h)
	id, err := h.Enqueue(Command{Kind: KindVehicleDelete, Vehicle: "v"})
	require.NoError(t, err)
	h.Sync(SyncRequest{Proto: 1, Results: []Result{{ID: id, OK: true, Message: "gone"}}})
	r, err := h.Wait(t.Context(), id)
	require.NoError(t, err)
	require.True(t, bool(r.OK))
	require.Equal(t, "gone", r.Message)

	id, err = h.Enqueue(Command{Kind: KindVehicleDelete, Vehicle: "w"})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r, err = h.Wait(ctx, id)
	require.ErrorIs(t, err, ErrTimeout)
	require.Equal(t, id, r.ID)

	_, err = h.Wait(t.Context(), "nope")
	require.Error(t, err)
}
