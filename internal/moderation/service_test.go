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

	"github.com/stretchr/testify/require"

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
	// seen are the events the service handed to OnEvent, as "instance:kind".
	seen []string
}

func newSvc(t testing.TB, names ...string) *svcEnv {
	t.Helper()
	st, _ := newStore(t)
	e := &svcEnv{st: st, rcon: map[string]*fakeRCon{}, dirs: map[string]string{}, tick: make(chan time.Time), now: t0, audit: filepath.Join(t.TempDir(), "audit.jsonl")}
	e.svc = NewService(st, admin.NewAudit(e.audit), t.Logf)
	e.svc.Tick = e.tick
	e.svc.OnEvent = func(instance string, ev battleye.Event) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.seen = append(e.seen, instance+":"+ev.Message)
	}
	e.svc.Now = func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }
	var insts []Instance
	for _, n := range names {
		e.rcon[n], e.dirs[n] = newFakeRCon(), t.TempDir()
		insts = append(insts, Instance{Name: n, BEDir: e.dirs[n], RConAddr: "127.0.0.1:1", RConPass: "pw"})
	}
	e.svc.Dial = func(i Instance) RCon { return e.rcon[i.Name] }
	ctx, cancel := context.WithCancel(t.Context())
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
	b, _ := e.st.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, Reason: "x", Message: "cheater", Scope: ScopeInstances, Instances: []string{"alpha"}}, t0)
	res := e.svc.Changed(t.Context(), b, nil)
	require.Len(t, res, 1)
	require.Equal(t, "ok", res["alpha"])
	require.Contains(t, e.file("alpha"), guidOf(t, steamA)+" -1 cheater")
	require.NotEmpty(t, e.rcon["alpha"].sent("loadBans"), "no loadBans on alpha")
	require.Empty(t, e.file("beta"))
	// Moving the ban to beta syncs both: alpha loses it.
	in := b.Input()
	in.Instances = []string{"beta"}
	b2, _ := e.st.UpdateBan(t.Context(), b.ID, "a", in, t0)
	res = e.svc.Changed(t.Context(), b2, &b)
	require.Equal(t, "ok", res["alpha"])
	require.Equal(t, "ok", res["beta"])
	require.NotContains(t, e.file("alpha"), "cheater", "after the move: alpha %q beta %q", e.file("alpha"), e.file("beta"))
	require.Contains(t, e.file("beta"), "cheater", "after the move: alpha %q beta %q", e.file("alpha"), e.file("beta"))
}

func TestRevokeOneOfTwoKeepsTheOther(t *testing.T) {
	e := newSvc(t, "alpha")
	b1, _ := e.st.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, Reason: "x", Message: "global", Scope: ScopeGlobal}, t0)
	b2, _ := e.st.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, Reason: "y", Message: "local", Scope: ScopeInstances, Instances: []string{"alpha"}, ExpiresAt: at(time.Hour)}, t0)
	e.svc.Changed(t.Context(), b2, nil)
	r1, _ := e.st.RevokeBan(t.Context(), b1.ID, "a", "appeal", t0)
	e.svc.Changed(t.Context(), r1, &b1)
	f := e.file("alpha")
	require.Contains(t, f, guidOf(t, steamA))
	require.Contains(t, f, "local")
}

func TestPendingIsRetriedOnTick(t *testing.T) {
	e := newSvc(t, "alpha")
	e.rcon["alpha"].setFail(errors.New("server down"))
	b, _ := e.st.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, Reason: "x", Scope: ScopeGlobal}, t0)
	res := e.svc.Changed(t.Context(), b, nil)
	require.True(t, strings.HasPrefix(res["alpha"], "pending"), "Changed = %v", res)
	require.True(t, e.svc.Pending()["alpha"], "alpha not pending")
	require.Contains(t, e.file("alpha"), guidOf(t, steamA), "the file is written even when RCon fails")
	e.rcon["alpha"].setFail(nil)
	e.tick <- t0
	eventually(t, "alpha synced", func() bool { return !e.svc.Pending()["alpha"] })
	require.NotEmpty(t, e.rcon["alpha"].sent("loadBans"), "the retry sent no loadBans")
}

func TestSweepDropsExpiredBans(t *testing.T) {
	e := newSvc(t, "alpha")
	b, _ := e.st.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, Reason: "x", Scope: ScopeGlobal, ExpiresAt: at(time.Minute)}, t0)
	e.svc.Changed(t.Context(), b, nil)
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
	cctx, cancel := context.WithCancel(t.Context())
	svc.Start(cctx)
	svc.SetInstances([]Instance{{Name: "alpha", BEDir: dir}})
	defer func() { cancel(); svc.Wait() }()
	eventually(t, "the import", func() bool {
		bs, _ := st.ListBans(t.Context(), BanFilter{Query: "0123456789abcdef0123456789abcdef", Now: t0})
		return len(bs) == 1 && bs[0].CreatedBy == "import:bans.txt"
	})
	eventually(t, "bans.txt rewritten as ours", func() bool { c, _, _ := ChangedOutside(dir); return !c })
}

func TestKickOnConnectByGUIDAndIP(t *testing.T) {
	e := newSvc(t, "alpha")
	g := guidOf(t, steamA)
	_, _ = e.st.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, Reason: "x", Message: "cheater", Scope: ScopeGlobal}, t0)
	_, _ = e.st.CreateBan(t.Context(), "a", BanInput{IPs: []string{"198.51.100.9"}, Reason: "y", Message: "ip banned", Scope: ScopeGlobal}, t0)
	r := e.rcon["alpha"]
	r.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 3, PlayerName: "alice", Address: "192.0.2.7:2304"}
	r.events <- battleye.Event{Kind: battleye.EventGUID, PlayerID: 3, PlayerName: "alice", GUID: g}
	eventually(t, "the GUID kick", func() bool { return len(r.sent("kick 3 ")) == 1 })
	k := r.sent("kick 3 ")[0]
	require.Equal(t, "kick 3 cheater", k)
	r.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 4, PlayerName: "mallory", Address: "198.51.100.9:2304"}
	eventually(t, "the IP kick", func() bool { return len(r.sent("kick 4 ")) == 1 })
	ids, _ := e.st.Identities(t.Context(), steamA)
	require.Len(t, ids, 1)
	require.Equal(t, "192.0.2.7", ids[0].IP)
	require.Equal(t, "alice", ids[0].Name)
	b, _ := os.ReadFile(e.audit)
	require.Contains(t, string(b), `"ban.kick"`, "no audit entry for the kick")
}

func TestNoKickWithoutBan(t *testing.T) {
	e := newSvc(t, "alpha")
	r := e.rcon["alpha"]
	g := guidOf(t, steamB)
	r.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 1, PlayerName: "bob", Address: "192.0.2.8:2304"}
	r.events <- battleye.Event{Kind: battleye.EventGUID, PlayerID: 1, PlayerName: "bob", GUID: g}
	eventually(t, "the sighting", func() bool { ids, _ := e.st.Identities(t.Context(), steamB); return len(ids) == 1 })
	k := r.sent("kick")
	require.Empty(t, k)
}

func TestNewBanKicksOnlinePlayer(t *testing.T) {
	e := newSvc(t, "alpha")
	r := e.rcon["alpha"]
	g := guidOf(t, steamA)
	r.mu.Lock()
	r.players = "0   192.0.2.7:2304   15   " + g + "(OK) alice\n1   192.0.2.8:2304   15   " + guidOf(t, steamB) + "(OK) bob\n"
	r.mu.Unlock()
	b, _ := e.st.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, Reason: "x", Message: "bye", Scope: ScopeGlobal}, t0)
	e.svc.Changed(t.Context(), b, nil)
	k := r.sent("kick")
	require.Len(t, k, 1)
	require.Equal(t, "kick 0 bye", k[0])
}

func TestKickMessageIsSanitised(t *testing.T) {
	e := newSvc(t, "alpha")
	r := e.rcon["alpha"]
	_, _ = e.st.CreateBan(t.Context(), "a", BanInput{IPs: []string{"192.0.2.7"}, Reason: "x", Message: "two\nlines; #kick -1", Scope: ScopeGlobal}, t0)
	r.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 2, PlayerName: "x", Address: "192.0.2.7:2304"}
	eventually(t, "the kick", func() bool { return len(r.sent("kick 2 ")) == 1 })
	k := r.sent("kick 2 ")[0]
	require.Equal(t, "kick 2 two lines; #kick -1", k)
}

func TestSweepDropsOldAddressesAndVacuumsWeekly(t *testing.T) {
	e := newSvc(t)
	ga := guidOf(t, steamA)
	_ = e.st.Seen(t.Context(), "alpha", ga, "192.0.2.1", "alice", t0.Add(-100*24*time.Hour))
	_ = e.st.Seen(t.Context(), "alpha", ga, "192.0.2.2", "alice", t0.Add(-1*time.Hour))
	e.tick <- t0
	eventually(t, "old address dropped", func() bool {
		ids, _ := e.st.Identities(t.Context(), steamA)
		return len(ids) == 2 && ids[1].IP == ""
	})
	eventually(t, "vacuum recorded", func() bool { due, _ := e.st.VacuumDue(t.Context(), t0, 7*24*time.Hour); return !due })
	// A week later the vacuum is due again.
	e.setNow(t0.Add(7 * 24 * time.Hour))
	e.tick <- t0
	eventually(t, "vacuum repeated", func() bool {
		due, _ := e.st.VacuumDue(t.Context(), t0.Add(7*24*time.Hour), 7*24*time.Hour)
		return !due
	})
}

func TestNegativeRetentionKeepsAddresses(t *testing.T) {
	e := newSvc(t)
	e.svc.AddressRetention = -1
	_ = e.st.Seen(t.Context(), "alpha", guidOf(t, steamA), "192.0.2.1", "alice", t0.Add(-1000*24*time.Hour))
	e.tick <- t0
	e.tick <- t0 // the second tick is taken only after the first sweep finished
	ids, _ := e.st.Identities(t.Context(), steamA)
	require.Len(t, ids, 1)
	require.NotEmpty(t, ids[0].IP)
}

func TestKickRemovesOnePlayerWithTheirMessage(t *testing.T) {
	e := newSvc(t, "alpha")
	r := e.rcon["alpha"]
	r.mu.Lock()
	r.players = "0   192.0.2.7:2304   15   " + guidOf(t, steamA) + "(OK) alice\n1   192.0.2.8:2304   15   " + guidOf(t, steamB) + "(OK) bob\n"
	r.mu.Unlock()
	err := e.svc.Kick(t.Context(), "alpha", steamB, "AFK for\nhours; #kick 0")
	require.NoError(t, err)
	got := r.sent("kick")
	require.Len(t, got, 1)
	require.Equal(t, "kick 1 AFK for hours; #kick 0", got[0])
	err = e.svc.Kick(t.Context(), "alpha", "76561198000000003", "x")
	require.ErrorIs(t, err, ErrNotOnline, "a player who is not online")
	err = e.svc.Kick(t.Context(), "nosuch", steamA, "x")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNotOnline)
}

func TestNegativeVacuumEveryNeverVacuums(t *testing.T) {
	e := newSvc(t)
	e.svc.VacuumEvery = -1
	e.tick <- t0
	e.tick <- t0 // the second tick is taken only after the first sweep finished
	due, _ := e.st.VacuumDue(t.Context(), t0, 7*24*time.Hour)
	require.True(t, due, "the database was vacuumed although that is turned off")
}

func TestServiceOnEventChat(t *testing.T) {
	e := newSvc(t, "alpha")
	r := e.rcon["alpha"]
	r.events <- battleye.Event{Kind: battleye.EventConnect, PlayerID: 1, PlayerName: "bob", Address: "nonsense"} // dropped by the ban logic, still reported
	r.events <- battleye.Event{Kind: battleye.EventChat, Channel: "Global", PlayerName: "bob", Message: "hello"}
	eventually(t, "the chat callback", func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return len(e.seen) == 2
	})
	e.mu.Lock()
	defer e.mu.Unlock()
	require.Equal(t, "alpha:hello", e.seen[1])
}
