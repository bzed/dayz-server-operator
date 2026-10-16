// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"context"
	"strings"
	"testing"
	"time"
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
	if !st.Connected || st.Players != 2 || st.Vehicles != 1 || st.Markers != 1 || st.Events != 1 || st.Hello.World != "chernarusplus" {
		t.Fatalf("status = %+v", st)
	}
	players := h.Players()
	if players[0].Vehicle != "" || players[1].Vehicle != "v1" {
		t.Fatalf("vehicle link wrong: %+v", players)
	}
	if got := h.Layers(); len(got) != 1 || got[0].Icon != "ufo" {
		t.Fatalf("layers = %+v", got)
	}
	if len(h.Vehicles()) != 1 || len(h.Events()) != 1 || len(h.Markers("ufo")) != 1 || len(h.Markers("other")) != 0 {
		t.Fatal("accessors")
	}
}

func TestPartialSyncKeepsOtherState(t *testing.T) {
	h, _ := testHub()
	h.Sync(SyncRequest{Proto: 1, Hello: true, Players: []Player{{SteamID: "1"}}, Vehicles: []Vehicle{{ID: "v"}}})
	h.Sync(SyncRequest{Proto: 1, Players: []Player{}})
	if len(h.Players()) != 0 || len(h.Vehicles()) != 1 {
		t.Fatal("a sync without vehicles must keep them")
	}
}

func TestConnectedExpires(t *testing.T) {
	h, c := testHub()
	if h.Status().Connected {
		t.Fatal("connected before any sync")
	}
	hello(h)
	c.advance(onlineWindow + time.Second)
	if h.Status().Connected {
		t.Fatal("still connected after the window")
	}
}

func TestSubmitOffline(t *testing.T) {
	h, _ := testHub()
	_, err := h.Submit(context.Background(), Command{Kind: KindMessage, Text: "hi"})
	if err != ErrOffline {
		t.Fatalf("err = %v, want ErrOffline", err)
	}
}

func TestCommandRoundTrip(t *testing.T) {
	h, _ := testHub()
	hello(h)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan Result, 1)
	go func() {
		r, err := h.Submit(ctx, Command{Kind: KindMessage, Text: "hello"})
		if err != nil {
			t.Error(err)
		}
		done <- r
	}()
	var cmd Command
	for cmd.ID == "" {
		time.Sleep(time.Millisecond)
		if r := h.Sync(SyncRequest{Proto: 1}); len(r.Commands) > 0 {
			cmd = r.Commands[0]
		}
	}
	if cmd.Kind != KindMessage || cmd.Style != "chat" || cmd.Text != "hello" {
		t.Fatalf("cmd = %+v", cmd)
	}
	h.Sync(SyncRequest{Proto: 1, Results: []Result{{ID: cmd.ID, OK: true}}})
	if r := <-done; !r.OK || r.ID != cmd.ID {
		t.Fatalf("result = %+v", r)
	}
	if r, ok := h.Result(cmd.ID); !ok || !bool(r.OK) {
		t.Fatal("Result lookup")
	}
	if r := h.Sync(SyncRequest{Proto: 1}); len(r.Commands) != 0 {
		t.Fatal("a finished command must not be sent again")
	}
}

func TestResendAndTimeout(t *testing.T) {
	h, c := testHub()
	hello(h)
	id, err := h.Enqueue(Command{Kind: KindVehicleDelete, Vehicle: "v"})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Sync(SyncRequest{Proto: 1}).Commands; len(got) != 1 {
		t.Fatalf("first sync commands = %d", len(got))
	}
	if got := h.Sync(SyncRequest{Proto: 1}).Commands; len(got) != 0 {
		t.Fatal("must not resend immediately")
	}
	c.advance(resendAfter)
	if got := h.Sync(SyncRequest{Proto: 1}).Commands; len(got) != 1 || got[0].ID != id {
		t.Fatal("must resend an unanswered command")
	}
	c.advance(commandTimeout)
	if got := h.Sync(SyncRequest{Proto: 1}).Commands; len(got) != 0 {
		t.Fatal("expired command still sent")
	}
	r, ok := h.Result(id)
	if !ok || bool(r.OK) || r.Message != ErrTimeout.Error() {
		t.Fatalf("result = %+v ok=%v", r, ok)
	}
	if _, ok := h.Result("nope"); ok {
		t.Fatal("unknown id has no result")
	}
}

func TestSubmitContextTimeout(t *testing.T) {
	h, _ := testHub()
	hello(h)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := h.Submit(ctx, Command{Kind: KindVehicleDelete, Vehicle: "v"}); err != ErrTimeout {
		t.Fatalf("err = %v", err)
	}
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
		{"delete no vehicle", Command{Kind: KindVehicleDelete}, "vehicle"},
		{"kind", Command{Kind: "explode"}, "unknown command"},
	}
	for _, tc := range cases {
		_, err := h.Enqueue(tc.cmd)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	for _, ok := range []Command{
		{Kind: KindSpawnItem, SteamID: "1", Type: "AKM"},
		{Kind: KindSpawnItem, SteamID: "1", Type: "Ammo_556x45", Target: "hands", Quantity: 5, Health: 1},
		{Kind: KindSpawnItem, Type: "AKM", X: 100, Z: 200, Quantity: 20},
		{Kind: KindSpawnVehicle, Type: "AKM", X: 100, Z: 200},
		{Kind: KindVehicleRepair, Vehicle: "v", Scope: "wheels"},
		{Kind: KindVehicleRepair, Vehicle: "v", Scope: "missing"},
		{Kind: KindVehicleFlip, Vehicle: "v"},
		{Kind: KindTeleport, SteamID: "1", ToSteamID: "2"},
	} {
		if _, err := h.Enqueue(ok); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
}

func TestSpawnAtPositionDefaultsToGround(t *testing.T) {
	h, _ := testHub()
	hello(h)
	if _, err := h.Enqueue(Command{Kind: KindSpawnItem, Type: "AKM", X: 5, Z: 6}); err != nil {
		t.Fatal(err)
	}
	r := h.Sync(SyncRequest{Proto: 1})
	if len(r.Commands) != 1 || r.Commands[0].Target != "ground" || r.Commands[0].SteamID != "" {
		t.Fatalf("commands = %+v", r.Commands)
	}
}

func TestSpawnVehicleChecksVehicleTypes(t *testing.T) {
	h, _ := testHub()
	h.Sync(SyncRequest{Proto: 1, Hello: true, Types: &TypesChunk{Hash: "i", Offset: 0, Total: 1, Names: []string{"AKM"}},
		VehicleTypes: &TypesChunk{Hash: "v", Offset: 0, Total: 2, Names: []string{"Offroad_02", "Hatchback_02"}}})
	if got := h.VehicleTypes("off", 5); len(got) != 1 || got[0] != "Offroad_02" {
		t.Fatalf("search = %v", got)
	}
	if _, err := h.Enqueue(Command{Kind: KindSpawnVehicle, Type: "Offroad_02", X: 1, Z: 2}); err != nil {
		t.Fatal(err)
	}
	// An item is no vehicle, and a vehicle is no item.
	if _, err := h.Enqueue(Command{Kind: KindSpawnVehicle, Type: "AKM", X: 1, Z: 2}); err == nil || !strings.Contains(err.Error(), "unknown class") {
		t.Fatalf("item as vehicle: %v", err)
	}
	if _, err := h.Enqueue(Command{Kind: KindSpawnItem, Type: "Offroad_02", X: 1, Z: 2}); err == nil || !strings.Contains(err.Error(), "unknown class") {
		t.Fatalf("vehicle as item: %v", err)
	}
	// A vehicle chunk that does not follow the ones before asks the mod to start again.
	if r := h.Sync(SyncRequest{Proto: 1, VehicleTypes: &TypesChunk{Hash: "w", Offset: 3, Total: 9, Names: []string{"x"}}}); !r.ResendTypes {
		t.Fatal("a broken vehicle list must be sent again")
	}
}

func TestSpawnChecksKnownTypes(t *testing.T) {
	h, _ := testHub()
	h.Sync(SyncRequest{Proto: 1, Hello: true, Types: &TypesChunk{Hash: "h", Offset: 0, Total: 3, Names: []string{"B", "A"}}})
	// Incomplete list: nothing is rejected on it yet.
	if _, err := h.Enqueue(Command{Kind: KindSpawnItem, SteamID: "1", Type: "Zed"}); err != nil {
		t.Fatalf("incomplete list must not reject: %v", err)
	}
	h.Sync(SyncRequest{Proto: 1, Types: &TypesChunk{Hash: "h", Offset: 2, Total: 3, Names: []string{"C"}}})
	h.Sync(SyncRequest{Proto: 1, Types: &TypesChunk{Hash: "h", Offset: 9, Total: 3, Names: []string{"X"}}}) // out of order, ignored
	if got := h.Types("", 0); len(got) != 3 || got[0] != "A" {
		t.Fatalf("types = %v", got)
	}
	if got := h.Types("b", 10); len(got) != 1 || got[0] != "B" {
		t.Fatalf("search = %v", got)
	}
	if got := h.Types("", 2); len(got) != 2 {
		t.Fatalf("limit = %v", got)
	}
	if _, err := h.Enqueue(Command{Kind: KindSpawnItem, SteamID: "1", Type: "Zed"}); err == nil || !strings.Contains(err.Error(), "unknown class") {
		t.Fatalf("err = %v", err)
	}
	if _, err := h.Enqueue(Command{Kind: KindSpawnItem, SteamID: "1", Type: "A"}); err != nil {
		t.Fatal(err)
	}
	// A new hash restarts the transfer.
	h.Sync(SyncRequest{Proto: 1, Types: &TypesChunk{Hash: "h2", Offset: 0, Total: 1, Names: []string{"Q"}}})
	if got := h.Types("", 0); len(got) != 1 || got[0] != "Q" {
		t.Fatalf("restart = %v", got)
	}
}

func TestSubscribe(t *testing.T) {
	h, _ := testHub()
	ctx, cancel := context.WithCancel(context.Background())
	ch := h.Subscribe(ctx)
	h.Sync(SyncRequest{Proto: 1, Hello: true, Players: []Player{{SteamID: "1"}}})
	kinds := map[string]bool{}
	for len(ch) > 0 {
		kinds[(<-ch).Kind] = true
	}
	if !kinds["status"] || !kinds["players"] {
		t.Fatalf("kinds = %v", kinds)
	}
	cancel()
	for range ch { // closed after cancel
	}
	// A full buffer must not block Sync.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	_ = h.Subscribe(ctx2)
	for i := 0; i < 100; i++ {
		h.Sync(SyncRequest{Proto: 1, Players: []Player{}})
	}
}

func TestMatchGlobs(t *testing.T) {
	if !MatchGlobs([]string{"A*", "B"}, "Abc") || !MatchGlobs([]string{"B"}, "B") || MatchGlobs([]string{"B"}, "Bc") || MatchGlobs([]string{""}, "") || MatchGlobs(nil, "x") {
		t.Fatal("MatchGlobs")
	}
	if !MatchGlobs([]string{"Wreck_*_DE"}, "Wreck_Train_DE") || MatchGlobs([]string{"Wreck_*_DE"}, "Wreck_Train") || MatchGlobs([]string{"ab*ba"}, "aba") || !MatchGlobs([]string{"*_DE"}, "X_DE") {
		t.Fatal("MatchGlobs with the star inside")
	}
}

func TestHubAsksForTheHelloItMissed(t *testing.T) {
	h := NewHub("alpha")
	// dzo serve restarted under a running server: the first sync it sees is no hello.
	if r := h.Sync(SyncRequest{Proto: 1}); !r.Hello {
		t.Error("a hub that does not know the world must ask for the hello")
	}
	if !h.Status().Connected {
		t.Error("a mod without a hello is still connected")
	}
	if r := h.Sync(SyncRequest{Proto: 1, Hello: true, World: "chernarusplus"}); r.Hello {
		t.Error("the hello arrived: stop asking")
	}
	if got := h.Status().Hello.World; got != "chernarusplus" {
		t.Errorf("world = %q", got)
	}
	if r := h.Sync(SyncRequest{Proto: 1}); r.Hello {
		t.Error("a known world is not asked for again")
	}
}

func TestSyncVehicleKind(t *testing.T) {
	r := SyncRequest{Vehicles: []Vehicle{{ID: "a"}, {ID: "b", Kind: "motorbike"}}}
	r.Normalise()
	if r.Vehicles[0].Kind != "car" || r.Vehicles[1].Kind != "motorbike" {
		t.Errorf("kinds = %q, %q", r.Vehicles[0].Kind, r.Vehicles[1].Kind)
	}
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
		if got[id] != k {
			t.Errorf("vehicle %s: kind %q, want %q", id, got[id], k)
		}
	}
}

func TestStatusCarriesTheNextRestartAndAnnouncesChanges(t *testing.T) {
	h := NewHub("x")
	next := time.Date(2026, 10, 16, 5, 0, 0, 0, time.UTC)
	h.NextRestart = func(time.Time) time.Time { return next }
	h.Sync(SyncRequest{Hello: true, World: "w"})
	ch := h.Subscribe(context.Background())
	if s := h.Status(); s.NextRestart == nil || !s.NextRestart.Equal(next) {
		t.Fatalf("next restart = %v", s.NextRestart)
	}
	h.Sync(SyncRequest{Has: []string{"players"}, Players: []Player{{SteamID: "1"}}})
	select {
	case ev := <-ch:
		for ev.Kind != "status" {
			ev = <-ch
		}
		if s := ev.Data.(Status); s.Players != 1 {
			t.Errorf("players = %d", s.Players)
		}
	case <-time.After(time.Second):
		t.Fatal("no status event when the player count changed")
	}
	h2 := NewHub("y")
	if h2.Status().NextRestart != nil {
		t.Error("no schedule, no next restart")
	}
}

func TestStatusCarriesTheGameTime(t *testing.T) {
	h := NewHub("x")
	h.Sync(SyncRequest{Hello: true, World: "w"})
	if h.Status().GameTime != "" {
		t.Error("an older mod sends no clock")
	}
	ch := h.Subscribe(context.Background())
	h.Sync(SyncRequest{Has: []string{"clock"}, Clock: 14*60 + 5})
	if got := h.Status().GameTime; got != "14:05" {
		t.Errorf("game time = %q", got)
	}
	select {
	case ev := <-ch:
		if s, ok := ev.Data.(Status); !ok || ev.Kind != "status" || s.GameTime != "14:05" {
			t.Errorf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no status event for the new time")
	}
}

func TestHubAsksForTheClassListAgainWhenAChunkDoesNotFit(t *testing.T) {
	h := NewHub("x")
	h.Sync(SyncRequest{Hello: true, World: "w"})
	// the first chunk was lost (dzo restarted): the next one starts at 2
	r := h.Sync(SyncRequest{Types: &TypesChunk{Hash: "h", Offset: 2, Total: 4, Names: []string{"c", "d"}}})
	if !r.ResendTypes {
		t.Fatal("a chunk that does not follow should make the hub ask again")
	}
	if r := h.Sync(SyncRequest{Types: &TypesChunk{Hash: "h", Offset: 0, Total: 4, Names: []string{"a", "b"}}}); r.ResendTypes {
		t.Error("the restart of the transfer fits")
	}
	h.Sync(SyncRequest{Types: &TypesChunk{Hash: "h", Offset: 2, Total: 4, Names: []string{"c", "d"}}})
	if got := h.Types("", 0); len(got) != 4 {
		t.Errorf("types = %v", got)
	}
}
