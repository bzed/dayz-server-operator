// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const bannedGUID, cleanGUID = "7604904247cf01f911670144f088664a", "a0d1158281d8639495a1908b5a802470"

// fakeMaster answers the packets it gets with reply(request); a nil reply stays silent. It counts the packets.
type fakeMaster struct {
	conn    net.PacketConn
	packets atomic.Int32
}

func newFakeMaster(t *testing.T, reply func(req []byte) [][]byte) *fakeMaster {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	f := &fakeMaster{conn: conn}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 512)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			f.packets.Add(1)
			for _, r := range reply(append([]byte(nil), buf[:n]...)) {
				_, _ = conn.WriteTo(r, from)
			}
		}
	}()
	t.Cleanup(func() { _ = conn.Close(); <-done })
	return f
}

func (f *fakeMaster) master() *Master {
	port := f.conn.LocalAddr().(*net.UDPAddr).Port
	return &Master{Host: "127.0.0.1", Port: func(Game) int { return port }, Timeout: 50 * time.Millisecond, Tries: 2}
}

func echo(req []byte, reason string) [][]byte {
	return [][]byte{append(append([]byte(nil), req[:4]...), reason...)}
}

func TestCheckClean(t *testing.T) {
	f := newFakeMaster(t, func(req []byte) [][]byte { return echo(req, "") })
	got, err := f.master().Check(t.Context(), Games[0], cleanGUID)
	require.NoError(t, err)
	assert.Equal(t, GlobalBan{}, got)
}

func TestCheckBanned(t *testing.T) {
	f := newFakeMaster(t, func(req []byte) [][]byte {
		assert.Equal(t, "....."+bannedGUID, string(req), "the request is five dots and the GUID")
		return echo(req, "Global Ban #760490")
	})
	got, err := f.master().Check(t.Context(), Games[0], bannedGUID)
	require.NoError(t, err)
	assert.Equal(t, GlobalBan{Banned: true, Reason: "Global Ban #760490"}, got)
}

func TestCheckZeroIsClean(t *testing.T) {
	f := newFakeMaster(t, func(req []byte) [][]byte { return echo(req, "0") })
	got, err := f.master().Check(t.Context(), Games[0], cleanGUID)
	require.NoError(t, err)
	assert.False(t, got.Banned)
}

func TestCheckIgnoresAStrayAnswer(t *testing.T) {
	f := newFakeMaster(t, func(req []byte) [][]byte {
		return [][]byte{[]byte("xxxxGlobal Ban #1"), echo(req, "")[0]}
	})
	got, err := f.master().Check(t.Context(), Games[0], cleanGUID)
	require.NoError(t, err)
	assert.False(t, got.Banned, "an answer with the wrong header is not ours")
	f2 := newFakeMaster(t, func(req []byte) [][]byte { return [][]byte{[]byte("abc")} })
	_, err = f2.master().Check(t.Context(), Games[0], cleanGUID)
	require.ErrorIs(t, err, ErrNoAnswer, "a short packet is no answer")
}

func TestCheckNoAnswer(t *testing.T) {
	f := newFakeMaster(t, func([]byte) [][]byte { return nil })
	_, err := f.master().Check(t.Context(), Games[0], cleanGUID)
	require.ErrorIs(t, err, ErrNoAnswer)
	assert.EqualValues(t, 2, f.packets.Load(), "Tries packets, no more")
}

func TestCheckRefusesABadGUID(t *testing.T) {
	f := newFakeMaster(t, func(req []byte) [][]byte { return echo(req, "") })
	for _, g := range []string{"", "xyz", strings.ToUpper(cleanGUID), cleanGUID + "00", "....." + cleanGUID[5:]} {
		_, err := f.master().Check(t.Context(), Games[0], g)
		require.Error(t, err, g)
		require.NotErrorIs(t, err, ErrNoAnswer)
	}
	assert.Zero(t, f.packets.Load(), "no packet leaves for a bad GUID")
}

func TestCheckStripsControlCharacters(t *testing.T) {
	f := newFakeMaster(t, func(req []byte) [][]byte { return echo(req, " Global\x00 Ban\r\n\x1b[31m #1 \n") })
	got, err := f.master().Check(t.Context(), Games[0], bannedGUID)
	require.NoError(t, err)
	assert.Equal(t, "Global Ban[31m #1", got.Reason)
}

func TestCheckCutsALongReason(t *testing.T) {
	f := newFakeMaster(t, func(req []byte) [][]byte { return echo(req, strings.Repeat("x", 400)) })
	got, err := f.master().Check(t.Context(), Games[0], bannedGUID)
	require.NoError(t, err)
	assert.Len(t, got.Reason, 256)
}

func TestCheckStopsWithTheContext(t *testing.T) {
	f := newFakeMaster(t, func([]byte) [][]byte { return nil })
	m := f.master()
	m.Timeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := m.Check(ctx, Games[0], cleanGUID)
	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestGamesHaveDistinctPorts(t *testing.T) {
	seen := map[int]bool{}
	ids := map[string]bool{}
	for _, g := range Games {
		assert.False(t, seen[g.Port], "%s shares a port", g.ID)
		assert.False(t, ids[g.ID])
		seen[g.Port], ids[g.ID] = true, true
	}
	assert.Equal(t, 2354, Games[0].Port)
	assert.Equal(t, "dayz", Games[0].ID, "DayZ comes first")
}
