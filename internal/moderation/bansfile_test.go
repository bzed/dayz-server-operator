// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The file BattlEye itself wrote (V1) must survive our parser and formatter unchanged: then what we
// write is what BattlEye writes.
func TestBansTxtRoundTripsTheCapture(t *testing.T) {
	golden, err := os.ReadFile("testdata/v1/bans.txt")
	if err != nil {
		t.Fatal(err)
	}
	es := ParseBansTxt(golden)
	if len(es) < 3 {
		t.Fatalf("parsed %d entries from the capture: %+v", len(es), es)
	}
	if got := string(FormatBansTxt(es)); got != string(golden) {
		t.Fatalf("round trip differs:\n--- got\n%s--- captured\n%s", got, golden)
	}
	byValue := map[string]Entry{}
	for _, e := range es {
		byValue[e.Value] = e
	}
	if e := byValue["0123456789abcdef0123456789abcdef"]; e.Expires != -1 || e.Reason != "permanent reason with spaces" {
		t.Errorf("permanent GUID ban %+v", e)
	}
	if e := byValue["fedcba9876543210fedcba9876543210"]; e.Expires <= 0 {
		t.Errorf("timed GUID ban %+v", e)
	}
	if e := byValue["192.0.2.7"]; e.Expires != -1 {
		t.Errorf("IP ban %+v", e)
	}
}

func TestEntriesMergeKeepsLongest(t *testing.T) {
	g := guidOf(t, steamA)
	bans := []Ban{
		{ID: 1, Message: "one hour", Scope: ScopeGlobal, ExpiresAt: at(time.Hour), Targets: []Target{{Kind: TargetSteam, Value: steamA}, {Kind: TargetGUID, Value: g, Derived: true}}},
		{ID: 2, Message: "forever here", Scope: ScopeInstances, Instances: []string{"alpha"}, Targets: []Target{{Kind: TargetGUID, Value: g}, {Kind: TargetIP, Value: "192.0.2.7"}}},
		{ID: 3, Message: "beta only", Scope: ScopeInstances, Instances: []string{"beta"}, Targets: []Target{{Kind: TargetIP, Value: "198.51.100.1"}}},
	}
	es := Entries(bans, "alpha")
	want := []Entry{{Value: g, Expires: -1, Reason: "forever here"}, {Value: "192.0.2.7", Expires: -1, Reason: "forever here"}}
	if len(es) != 2 {
		t.Fatalf("entries %+v", es)
	}
	got := map[string]Entry{es[0].Value: es[0], es[1].Value: es[1]}
	for _, w := range want {
		if got[w.Value] != w {
			t.Errorf("entry %+v, want %+v", got[w.Value], w)
		}
	}
	// beta: the global one-hour GUID ban and beta's own permanent IP ban.
	beta := map[string]Entry{}
	for _, e := range Entries(bans, "beta") {
		beta[e.Value] = e
	}
	if len(beta) != 2 || beta[g].Expires != t0.Add(time.Hour).Unix() || beta["198.51.100.1"].Expires != -1 {
		t.Errorf("beta entries %+v", beta)
	}
}

func TestBEReasonSanitises(t *testing.T) {
	for in, want := range map[string]string{
		"line\nbreak\r\tand\x00nul": "line break and nul",
		"  spaced   out  ":          "spaced out",
		"":                          "banned",
	} {
		if got := BEReason(in); got != want {
			t.Errorf("BEReason(%q) = %q, want %q", in, got, want)
		}
	}
	long := BEReason(strings.Repeat("ä", 1000))
	if len(long) > beReasonMax || !utf8.ValidString(long) {
		t.Errorf("long reason: %d bytes, valid UTF-8 %t", len(long), utf8.ValidString(long))
	}
}

func TestWriteBansFileAndChangedOutside(t *testing.T) {
	dir := t.TempDir()
	if changed, _, err := ChangedOutside(dir); err != nil || changed {
		t.Fatalf("no file: %t, %v", changed, err)
	}
	if err := os.WriteFile(filepath.Join(dir, BansTxt), []byte("0123456789abcdef0123456789abcdef -1 by hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, content, err := ChangedOutside(dir); err != nil || !changed || !strings.Contains(string(content), "by hand") {
		t.Fatalf("hand-written file: %t, %v", changed, err)
	}
	if w, err := WriteBansFile(dir, []byte("x -1 y\n")); err != nil || !w {
		t.Fatalf("WriteBansFile = %t, %v", w, err)
	}
	if w, _ := WriteBansFile(dir, []byte("x -1 y\n")); w {
		t.Fatal("unchanged content was written again")
	}
	if changed, _, _ := ChangedOutside(dir); changed {
		t.Fatal("our own file counts as changed outside")
	}
	info, _ := os.Stat(filepath.Join(dir, BansTxt))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}

func TestImportEntries(t *testing.T) {
	s, _ := newStore(t)
	g := guidOf(t, steamA)
	_, _ = s.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Scope: ScopeGlobal}, t0)
	n, err := s.ImportEntries(ctx, "alpha", []Entry{
		{Value: g, Expires: -1, Reason: "already banned"},
		{Value: "fedcba9876543210fedcba9876543210", Expires: t0.Add(time.Hour).Unix(), Reason: "timed"},
		{Value: "192.0.2.7", Expires: -1, Reason: ""},
		{Value: "11111111111111111111111111111111", Expires: t0.Add(-time.Hour).Unix(), Reason: "expired"},
		{Value: "garbage", Expires: -1, Reason: "skipped"},
	}, t0)
	if err != nil || n != 2 {
		t.Fatalf("imported %d, %v", n, err)
	}
	bs, _ := s.ListBans(ctx, BanFilter{Query: "192.0.2.7", Now: t0})
	if len(bs) != 1 || bs[0].CreatedBy != "import:bans.txt" || bs[0].Scope != ScopeInstances || bs[0].Instances[0] != "alpha" || bs[0].Message != DefaultMessage {
		t.Fatalf("imported IP ban %+v", bs)
	}
}

func TestRenderAtStart(t *testing.T) {
	s, path := newStore(t)
	be := t.TempDir()
	if msg, err := RenderAtStart(ctx, path+".none", "alpha", be, t0); err != nil || !strings.Contains(msg, "no ban database") {
		t.Fatalf("no database: %q, %v", msg, err)
	}
	_, _ = s.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "x", Message: "cheater", Scope: ScopeGlobal}, t0)
	msg, err := RenderAtStart(ctx, path, "alpha", be, t0)
	if err != nil || !strings.Contains(msg, "1 entr") {
		t.Fatalf("render: %q, %v", msg, err)
	}
	b, _ := os.ReadFile(filepath.Join(be, BansTxt))
	if !strings.Contains(string(b), guidOf(t, steamA)+" -1 cheater") {
		t.Fatalf("bans.txt %q", b)
	}
	_ = os.WriteFile(filepath.Join(be, BansTxt), []byte("edited by hand\n"), 0o600)
	if msg, err := RenderAtStart(ctx, path, "alpha", be, t0); err != nil || !strings.Contains(msg, "changed outside") {
		t.Fatalf("changed outside: %q, %v", msg, err)
	}
	if b, _ := os.ReadFile(filepath.Join(be, BansTxt)); string(b) != "edited by hand\n" {
		t.Fatal("the start path overwrote a hand-edited bans.txt")
	}
}
