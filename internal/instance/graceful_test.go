// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/battleye"
)

// A real battleye.Client's Command method must satisfy Commander (that
// is the whole point of this interface): checked at compile time so a
// future signature change in either package fails the build, not a
// runtime type assertion somewhere else.
var _ Commander = (*battleye.Client)(nil)

func TestParsePlayerIDs(t *testing.T) {
	raw := `Players on server:
[#] [IP Address]:[Port] [Ping] [GUID] [Name]
--------------------------------------------------
0    127.0.0.1:2304        50   b1234567890abcdef1234567890abcd1(OK) PlayerOne
1    127.0.0.1:2305        30   b1234567890abcdef1234567890abcd2(OK) PlayerTwo
(2 players in total)
`
	ids := ParsePlayerIDs(raw)
	require.Len(t, ids, 2)
	require.Equal(t, 0, ids[0])
	assert.Equal(t, 1, ids[1])
}

func TestParsePlayerIDsEmpty(t *testing.T) {
	raw := `Players on server:
[#] [IP Address]:[Port] [Ping] [GUID] [Name]
--------------------------------------------------
(0 players in total)
`
	ids := ParsePlayerIDs(raw)
	assert.Empty(t, ids)
}

// fakeCommander records every command sent to it and returns scripted
// responses/errors in order (or a default response after they're
// exhausted).
type fakeCommander struct {
	responses map[string][]string
	errs      map[string]error
	sent      []string
}

func (f *fakeCommander) Command(_ context.Context, command string) (string, error) {
	f.sent = append(f.sent, command)
	if err := f.errs[command]; err != nil {
		return "", err
	}
	if resps, ok := f.responses[command]; ok && len(resps) > 0 {
		resp := resps[0]
		f.responses[command] = resps[1:]
		return resp, nil
	}
	return "", nil
}

func TestLockAndUnlock(t *testing.T) {
	c := &fakeCommander{}
	err := Lock(t.Context(), c)
	require.NoError(t, err, "Lock")
	err = Unlock(t.Context(), c)
	require.NoError(t, err, "Unlock")
	assert.True(t, equalStrings(c.sent, []string{"#lock", "#unlock"}), "sent = %v", c.sent)
}

func TestAnnounce(t *testing.T) {
	c := &fakeCommander{}
	err := Announce(t.Context(), c, "Restart in 5 minutes")
	require.NoError(t, err, "Announce")
	assert.Equal(t, "say -1 Restart in 5 minutes", c.sent[0])
}

func TestKickAllStopsWhenNoPlayersLeft(t *testing.T) {
	playersOutput := `0    127.0.0.1:2304        50   guid1(OK) PlayerOne
(1 players in total)
`
	c := &fakeCommander{responses: map[string][]string{
		"players": {playersOutput, "(0 players in total)\n"},
	}}
	err := KickAll(t.Context(), c, "restart", 3)
	require.NoError(t, err, "KickAll")
	// Expect: players, kick 0, players (empty -> stop early), #kick -1.
	want := []string{"players", "kick 0 restart", "players", "#kick -1"}
	assert.True(t, equalStrings(c.sent, want), "sent = %v, want %v", c.sent, want)
}

func TestKickAllRunsAllPassesWhenPlayersKeepReconnecting(t *testing.T) {
	playersOutput := `0    127.0.0.1:2304        50   guid1(OK) PlayerOne
(1 players in total)
`
	c := &fakeCommander{responses: map[string][]string{
		"players": {playersOutput, playersOutput, playersOutput},
	}}
	err := KickAll(t.Context(), c, "restart", 3)
	require.NoError(t, err, "KickAll")
	kickCount := 0
	for _, s := range c.sent {
		if s == "kick 0 restart" {
			kickCount++
		}
	}
	assert.Equal(t, 3, kickCount)
	assert.Equal(t, "#kick -1", c.sent[len(c.sent)-1])
}

func TestKickAllPlayersCommandErrorPropagates(t *testing.T) {
	c := &fakeCommander{errs: map[string]error{"players": errors.New("connection lost")}}
	err := KickAll(t.Context(), c, "restart", 3)
	require.Error(t, err)
}

func TestKickAllKickCommandErrorPropagates(t *testing.T) {
	playersOutput := "0    127.0.0.1:2304        50   guid1(OK) PlayerOne\n(1 players in total)\n"
	c := &fakeCommander{
		responses: map[string][]string{"players": {playersOutput}},
		errs:      map[string]error{"kick 0 restart": errors.New("kick failed")},
	}
	err := KickAll(t.Context(), c, "restart", 3)
	require.Error(t, err)
}

func TestGracefulRestartFullSequence(t *testing.T) {
	playersOutput := "0    127.0.0.1:2304        50   guid1(OK) PlayerOne\n(1 players in total)\n"
	c := &fakeCommander{responses: map[string][]string{"players": {playersOutput, "(0 players in total)\n"}}}

	argsFile := "/dev/null"
	script := writeFakeSystemctl(t, argsFile, "", 0)
	lc := Lifecycle{Command: script}

	var slept time.Duration
	opts := GracefulRestartOptions{
		Reason: "maintenance", Delay: 5 * time.Second,
		Sleep: func(d time.Duration) { slept = d },
	}
	err := GracefulRestart(t.Context(), c, lc, "deerisle", opts)
	require.NoError(t, err, "GracefulRestart")
	assert.Equal(t, "#lock", c.sent[0])
	assert.Equal(t, "#kick -1", c.sent[len(c.sent)-1])
	assert.Equal(t, 5*time.Second, slept)
}

func TestGracefulRestartLockFailureAbortsBeforeRestart(t *testing.T) {
	c := &fakeCommander{errs: map[string]error{"#lock": errors.New("rcon down")}}
	script := writeFakeSystemctl(t, "/dev/null", "", 0)
	lc := Lifecycle{Command: script}

	err := GracefulRestart(t.Context(), c, lc, "deerisle", GracefulRestartOptions{})
	require.Error(t, err)
}

func TestGracefulRestartDefaultPassesAndSleep(t *testing.T) {
	opts := GracefulRestartOptions{}
	assert.Equal(t, 3, opts.passes())
	opts.sleep(0) // must not block or panic with the default sleep
}
