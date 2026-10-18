// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/battleye"
)

type fakeRCon struct {
	mu      sync.Mutex
	cmds    []string
	players string
	fail    error
	events  chan battleye.Event
}

func newFakeRCon() *fakeRCon { return &fakeRCon{events: make(chan battleye.Event, 8)} }

func (f *fakeRCon) Command(_ context.Context, cmd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return "", f.fail
	}
	f.cmds = append(f.cmds, cmd)
	if cmd == "players" {
		return f.players, nil
	}
	return "", nil
}
func (f *fakeRCon) Events() <-chan battleye.Event { return f.events }
func (f *fakeRCon) Close() error                  { return nil }
func (f *fakeRCon) sent(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}
func (f *fakeRCon) setFail(err error) { f.mu.Lock(); f.fail = err; f.mu.Unlock() }

type svcEnv struct {
	svc   *Service
	st    *Store
	rcon  map[string]*fakeRCon
	dirs  map[string]string
	tick  chan time.Time
	now   time.Time
	mu    sync.Mutex
	audit string
}

func newSvc(t *testing.T, names ...string) *svcEnv {
	t.Helper()
	st, _ := newStore(t)
	e := &svcEnv{st: st, rcon: map[string]*fakeRCon{}, dirs: map[string]string{}, tick: make(chan time.Time), now: t0, audit: filepath.Join(t.TempDir(), "audit.jsonl")}
	e.svc = NewService(st, admin.NewAudit(e.audit), t.Logf)
	e.svc.Tick = e.tick
	e.svc.Now = func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }
	var insts []Instance
	for _, n := range names {
		e.rcon[n], e.dirs[n] = newFakeRCon(), t.TempDir()
		insts = append(insts, Instance{Name: n, BEDir: e.dirs[n], RConAddr: "127.0.0.1:1", RConPass: "pw"})
	}
	e.svc.Dial = func(i Instance) RCon { return e.rcon[i.Name] }
	ctx, cancel := context.WithCancel(context.Background())
	e.svc.Start(ctx)
	e.svc.SetInstances(insts)
	t.Cleanup(func() { cancel(); e.svc.Wait() })
	return e
}

func (e *svcEnv) setNow(t time.Time) { e.mu.Lock(); e.now = t; e.mu.Unlock() }

func (e *svcEnv) file(name string) string {
	b, _ := os.ReadFile(filepath.Join(e.dirs[name], BansTxt))
	return string(b)
}

func TestChangedRendersAndReloads(t *testing.T) {
	e := newSvc(t, "alpha", "beta")
	b, _ := e.st.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Message: "cheater", Scope: ScopeInstances, Instances: []string{"alpha"}}, t0)
	res := e.svc.Changed(ctx, b, nil)
	if len(res) != 1 || res["alpha"] != "ok" {
		t.Fatalf("Changed = %v", res)
	}
	if !strings.Contains(e.file("alpha"), guidOf(t, steamA)+" -1 cheater") {
		t.Fatalf("alpha bans.txt %q", e.file("alpha"))
	}
	if len(e.rcon["alpha"].sent("loadBans")) == 0 {
		t.Fatal("no loadBans on alpha")
	}
	if e.file("beta") != "" {
		t.Fatal("beta got an alpha ban")
	}
	// Moving the ban to beta syncs both: alpha loses it.
	in := b.Input()
	in.Instances = []string{"beta"}
	b2, _ := e.st.UpdateBan(ctx, b.ID, "a", in, t0)
	if res := e.svc.Changed(ctx, b2, &b); res["alpha"] != "ok" || res["beta"] != "ok" {
		t.Fatalf("Changed after the move = %v", res)
	}
	if strings.Contains(e.file("alpha"), "cheater") || !strings.Contains(e.file("beta"), "cheater") {
		t.Fatalf("after the move: alpha %q beta %q", e.file("alpha"), e.file("beta"))
	}
}

func TestRevokeOneOfTwoKeepsTheOther(t *testing.T) {
	e := newSvc(t, "alpha")
	b1, _ := e.st.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Message: "global", Scope: ScopeGlobal}, t0)
	b2, _ := e.st.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "y", Message: "local", Scope: ScopeInstances, Instances: []string{"alpha"}, ExpiresAt: at(time.Hour)}, t0)
	e.svc.Changed(ctx, b2, nil)
	r1, _ := e.st.RevokeBan(ctx, b1.ID, "a", "appeal", t0)
	e.svc.Changed(ctx, r1, &b1)
	if f := e.file("alpha"); !strings.Contains(f, guidOf(t, steamA)) || !strings.Contains(f, "local") {
		t.Fatalf("the remaining ban is gone: %q", f)
	}
}

func TestPendingIsRetriedOnTick(t *testing.T) {
	e := newSvc(t, "alpha")
	e.rcon["alpha"].setFail(errors.New("server down"))
	b, _ := e.st.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Scope: ScopeGlobal}, t0)
	if res := e.svc.Changed(ctx, b, nil); !strings.HasPrefix(res["alpha"], "pending") {
		t.Fatalf("Changed = %v", res)
	}
	if !e.svc.Pending()["alpha"] {
		t.Fatal("alpha not pending")
	}
	if !strings.Contains(e.file("alpha"), guidOf(t, steamA)) {
		t.Fatal("the file is written even when RCon fails")
	}
	e.rcon["alpha"].setFail(nil)
	e.tick <- t0
	eventually(t, "alpha synced", func() bool { return !e.svc.Pending()["alpha"] })
	if len(e.rcon["alpha"].sent("loadBans")) == 0 {
		t.Fatal("the retry sent no loadBans")
	}
}

func TestSweepDropsExpiredBans(t *testing.T) {
	e := newSvc(t, "alpha")
	b, _ := e.st.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Scope: ScopeGlobal, ExpiresAt: at(time.Minute)}, t0)
	e.svc.Changed(ctx, b, nil)
	e.setNow(t0.Add(2 * time.Minute))
	e.tick <- t0
	eventually(t, "the expired ban leaves bans.txt", func() bool { return !strings.Contains(e.file("alpha"), guidOf(t, steamA)) })
}

func TestOutsideChangesAreImported(t *testing.T) {
	st, _ := newStore(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, BansTxt), []byte("0123456789abcdef0123456789abcdef -1 added by another tool\n"), 0o600)
	svc := NewService(st, admin.NewAudit(filepath.Join(t.TempDir(), "a.jsonl")), t.Logf)
	r := newFakeRCon()
	svc.Dial = func(Instance) RCon { return r }
	svc.Tick = make(chan time.Time)
	svc.Now = func() time.Time { return t0 }
	cctx, cancel := context.WithCancel(context.Background())
	svc.Start(cctx)
	svc.SetInstances([]Instance{{Name: "alpha", BEDir: dir}})
	defer func() { cancel(); svc.Wait() }()
	eventually(t, "the import", func() bool {
		bs, _ := st.ListBans(ctx, BanFilter{Query: "0123456789abcdef0123456789abcdef", Now: t0})
		return len(bs) == 1 && bs[0].CreatedBy == "import:bans.txt"
	})
	eventually(t, "bans.txt rewritten as ours", func() bool { c, _, _ := ChangedOutside(dir); return !c })
}

func TestKickOnConnectByGUIDAndIP(t *testing.T) {
	e := newSvc(t, "alpha")
	g := guidOf(t, steamA)
	_, _ = e.st.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Message: "cheater", Scope: ScopeGlobal}, t0)
	_, _ = e.st.CreateBan(ctx, "a", BanInput{IPs: []string{"198.51.100.9"}, Reason: "y", Message: "ip banned", Scope: ScopeGlobal}, t0)
	r := e.rcon["alpha"]
	r.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 3, PlayerName: "alice", Address: "192.0.2.7:2304"}
	r.events <- battleye.Event{Kind: battleye.EventGUID, PlayerID: 3, PlayerName: "alice", GUID: g}
	eventually(t, "the GUID kick", func() bool { return len(r.sent("kick 3 ")) == 1 })
	if k := r.sent("kick 3 ")[0]; k != "kick 3 cheater" {
		t.Fatalf("kick %q", k)
	}
	r.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 4, PlayerName: "mallory", Address: "198.51.100.9:2304"}
	eventually(t, "the IP kick", func() bool { return len(r.sent("kick 4 ")) == 1 })
	ids, _ := e.st.Identities(ctx, steamA)
	if len(ids) != 1 || ids[0].IP != "192.0.2.7" || ids[0].Name != "alice" {
		t.Fatalf("sighting %+v", ids)
	}
	if b, _ := os.ReadFile(e.audit); !strings.Contains(string(b), `"ban.kick"`) {
		t.Fatal("no audit entry for the kick")
	}
}

func TestNoKickWithoutBan(t *testing.T) {
	e := newSvc(t, "alpha")
	r := e.rcon["alpha"]
	g := guidOf(t, steamB)
	r.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 1, PlayerName: "bob", Address: "192.0.2.8:2304"}
	r.events <- battleye.Event{Kind: battleye.EventGUID, PlayerID: 1, PlayerName: "bob", GUID: g}
	eventually(t, "the sighting", func() bool { ids, _ := e.st.Identities(ctx, steamB); return len(ids) == 1 })
	if k := r.sent("kick"); len(k) != 0 {
		t.Fatalf("kicked %v", k)
	}
}

func TestNewBanKicksOnlinePlayer(t *testing.T) {
	e := newSvc(t, "alpha")
	r := e.rcon["alpha"]
	g := guidOf(t, steamA)
	r.mu.Lock()
	r.players = "0   192.0.2.7:2304   15   " + g + "(OK) alice\n1   192.0.2.8:2304   15   " + guidOf(t, steamB) + "(OK) bob\n"
	r.mu.Unlock()
	b, _ := e.st.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Message: "bye", Scope: ScopeGlobal}, t0)
	e.svc.Changed(ctx, b, nil)
	if k := r.sent("kick"); len(k) != 1 || k[0] != "kick 0 bye" {
		t.Fatalf("kicks %v", k)
	}
}

func TestKickMessageIsSanitised(t *testing.T) {
	e := newSvc(t, "alpha")
	r := e.rcon["alpha"]
	_, _ = e.st.CreateBan(ctx, "a", BanInput{IPs: []string{"192.0.2.7"}, Reason: "x", Message: "two\nlines; #kick -1", Scope: ScopeGlobal}, t0)
	r.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 2, PlayerName: "x", Address: "192.0.2.7:2304"}
	eventually(t, "the kick", func() bool { return len(r.sent("kick 2 ")) == 1 })
	if k := r.sent("kick 2 ")[0]; k != "kick 2 two lines; #kick -1" {
		t.Fatalf("kick %q", k)
	}
}

func TestSweepDropsOldAddressesAndVacuumsWeekly(t *testing.T) {
	e := newSvc(t)
	ga := guidOf(t, steamA)
	_ = e.st.Seen(ctx, "alpha", ga, "192.0.2.1", "alice", t0.Add(-100*24*time.Hour))
	_ = e.st.Seen(ctx, "alpha", ga, "192.0.2.2", "alice", t0.Add(-1*time.Hour))
	e.tick <- t0
	eventually(t, "old address dropped", func() bool {
		ids, _ := e.st.Identities(ctx, steamA)
		return len(ids) == 2 && ids[1].IP == ""
	})
	eventually(t, "vacuum recorded", func() bool { due, _ := e.st.VacuumDue(ctx, t0, 7*24*time.Hour); return !due })
	// A week later the vacuum is due again.
	e.setNow(t0.Add(7 * 24 * time.Hour))
	e.tick <- t0
	eventually(t, "vacuum repeated", func() bool { due, _ := e.st.VacuumDue(ctx, t0.Add(7*24*time.Hour), 7*24*time.Hour); return !due })
}

func TestNegativeRetentionKeepsAddresses(t *testing.T) {
	e := newSvc(t)
	e.svc.AddressRetention = -1
	_ = e.st.Seen(ctx, "alpha", guidOf(t, steamA), "192.0.2.1", "alice", t0.Add(-1000*24*time.Hour))
	e.tick <- t0
	e.tick <- t0 // the second tick is taken only after the first sweep finished
	ids, _ := e.st.Identities(ctx, steamA)
	if len(ids) != 1 || ids[0].IP == "" {
		t.Fatalf("the address was dropped: %+v", ids)
	}
}

func TestKickRemovesOnePlayerWithTheirMessage(t *testing.T) {
	e := newSvc(t, "alpha")
	r := e.rcon["alpha"]
	r.mu.Lock()
	r.players = "0   192.0.2.7:2304   15   " + guidOf(t, steamA) + "(OK) alice\n1   192.0.2.8:2304   15   " + guidOf(t, steamB) + "(OK) bob\n"
	r.mu.Unlock()
	if err := e.svc.Kick(ctx, "alpha", steamB, "AFK for\nhours; #kick 0"); err != nil {
		t.Fatal(err)
	}
	if got := r.sent("kick"); len(got) != 1 || got[0] != "kick 1 AFK for hours; #kick 0" {
		t.Fatalf("kick commands: %q", got)
	}
	if err := e.svc.Kick(ctx, "alpha", "76561198000000003", "x"); !errors.Is(err, ErrNotOnline) {
		t.Fatalf("a player who is not online: %v", err)
	}
	if err := e.svc.Kick(ctx, "nosuch", steamA, "x"); err == nil || errors.Is(err, ErrNotOnline) {
		t.Fatalf("an unknown instance: %v", err)
	}
}

func TestNegativeVacuumEveryNeverVacuums(t *testing.T) {
	e := newSvc(t)
	e.svc.VacuumEvery = -1
	e.tick <- t0
	e.tick <- t0 // the second tick is taken only after the first sweep finished
	if due, _ := e.st.VacuumDue(ctx, t0, 7*24*time.Hour); !due {
		t.Fatal("the database was vacuumed although that is turned off")
	}
}
