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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The file BattlEye itself wrote (V1) must survive our parser and formatter unchanged: then what we
// write is what BattlEye writes.
func TestBansTxtRoundTripsTheCapture(t *testing.T) {
	golden, err := os.ReadFile("testdata/v1/bans.txt")
	require.NoError(t, err)
	es := ParseBansTxt(golden)
	require.GreaterOrEqual(t, len(es), 3)
	got := string(FormatBansTxt(es))
	require.Equal(t, string(golden), got)
	byValue := map[string]Entry{}
	for _, e := range es {
		byValue[e.Value] = e
	}
	e := byValue["0123456789abcdef0123456789abcdef"]
	require.Equal(t, int64(-1), e.Expires)
	assert.Equal(t, "permanent reason with spaces", e.Reason)
	e = byValue["fedcba9876543210fedcba9876543210"]
	assert.Positive(t, e.Expires)
	e = byValue["192.0.2.7"]
	assert.Equal(t, int64(-1), e.Expires)
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
	require.Len(t, es, 2)
	got := map[string]Entry{es[0].Value: es[0], es[1].Value: es[1]}
	for _, w := range want {
		assert.Equal(t, w, got[w.Value])
	}
	// beta: the global one-hour GUID ban and beta's own permanent IP ban.
	beta := map[string]Entry{}
	for _, e := range Entries(bans, "beta") {
		beta[e.Value] = e
	}
	require.Len(t, beta, 2)
	require.Equal(t, t0.Add(time.Hour).Unix(), beta[g].Expires)
	assert.Equal(t, int64(-1), beta["198.51.100.1"].Expires)
}

func TestBEReasonSanitises(t *testing.T) {
	for in, want := range map[string]string{
		"line\nbreak\r\tand\x00nul": "line break and nul",
		"  spaced   out  ":          "spaced out",
		"":                          "banned",
	} {
		got := BEReason(in)
		assert.Equal(t, want, got)
	}
	long := BEReason(strings.Repeat("ä", 1000))
	require.LessOrEqual(t, len(long), beReasonMax, "long reason: %d bytes, valid UTF-8 %t", len(long), utf8.ValidString(long))
	assert.True(t, utf8.ValidString(long), "long reason: %d bytes, valid UTF-8 %t", len(long), utf8.ValidString(long))
}

func TestWriteBansFileAndChangedOutside(t *testing.T) {
	dir := t.TempDir()
	changed, _, err := ChangedOutside(dir)
	require.NoError(t, err)
	require.False(t, changed)
	err = os.WriteFile(filepath.Join(dir, BansTxt), []byte("0123456789abcdef0123456789abcdef -1 by hand\n"), 0o600)
	require.NoError(t, err)
	changed, content, err := ChangedOutside(dir)
	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, string(content), "by hand")
	w, err := WriteBansFile(dir, []byte("x -1 y\n"))
	require.NoError(t, err)
	require.True(t, w)
	w, _ = WriteBansFile(dir, []byte("x -1 y\n"))
	require.False(t, w, "unchanged content was written again")
	changed, _, _ = ChangedOutside(dir)
	require.False(t, changed, "our own file counts as changed outside")
	info, _ := os.Stat(filepath.Join(dir, BansTxt))
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestImportEntries(t *testing.T) {
	s, _ := newStore(t)
	g := guidOf(t, steamA)
	_, _ = s.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, Reason: "x", Scope: ScopeGlobal}, t0)
	n, err := s.ImportEntries(t.Context(), "alpha", []Entry{
		{Value: g, Expires: -1, Reason: "already banned"},
		{Value: "fedcba9876543210fedcba9876543210", Expires: t0.Add(time.Hour).Unix(), Reason: "timed"},
		{Value: "192.0.2.7", Expires: -1, Reason: ""},
		{Value: "11111111111111111111111111111111", Expires: t0.Add(-time.Hour).Unix(), Reason: "expired"},
		{Value: "garbage", Expires: -1, Reason: "skipped"},
	}, t0)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	bs, _ := s.ListBans(t.Context(), BanFilter{Query: "192.0.2.7", Now: t0})
	require.Len(t, bs, 1)
	require.Equal(t, "import:bans.txt", bs[0].CreatedBy)
	require.Equal(t, ScopeInstances, bs[0].Scope)
	require.Equal(t, "alpha", bs[0].Instances[0])
	require.Equal(t, DefaultMessage, bs[0].Message)
}

func TestRenderAtStart(t *testing.T) {
	s, path := newStore(t)
	be := t.TempDir()
	msg, err := RenderAtStart(t.Context(), path+".none", "alpha", be, t0)
	require.NoError(t, err)
	require.Contains(t, msg, "no ban database")
	_, _ = s.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, Reason: "x", Message: "cheater", Scope: ScopeGlobal}, t0)
	msg, err = RenderAtStart(t.Context(), path, "alpha", be, t0)
	require.NoError(t, err)
	require.Contains(t, msg, "1 entr")
	b, _ := os.ReadFile(filepath.Join(be, BansTxt))
	require.Contains(t, string(b), guidOf(t, steamA)+" -1 cheater")
	_ = os.WriteFile(filepath.Join(be, BansTxt), []byte("edited by hand\n"), 0o600)
	msg, err = RenderAtStart(t.Context(), path, "alpha", be, t0)
	require.NoError(t, err)
	require.Contains(t, msg, "changed outside")
	b, _ = os.ReadFile(filepath.Join(be, BansTxt))
	require.Equal(t, "edited by hand\n", string(b), "the start path overwrote a hand-edited bans.txt")
}
