// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveServer talks to a real DayZServer's BattlEye RCon (spike S4). It only runs with
// DZO_LIVE_BE_ADDR (host:port) and DZO_LIVE_BE_PASSWORD set; it sends harmless commands only
// (players, an empty keep-alive, an unknown command) and logs what the server answers.
func TestLiveServer(t *testing.T) {
	addr, pw := os.Getenv("DZO_LIVE_BE_ADDR"), os.Getenv("DZO_LIVE_BE_PASSWORD")
	if addr == "" || pw == "" {
		t.Skip("set DZO_LIVE_BE_ADDR and DZO_LIVE_BE_PASSWORD to run against a real server")
	}
	c, err := Dial(addr, pw, WithKeepAliveInterval(2*time.Second))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, cmd := range []string{"players", "this-is-no-command", "#unlock"} {
		out, err := c.Command(ctx, cmd)
		t.Logf("%q -> err=%v %q", cmd, err, out)
		if err != nil {
			t.Errorf("%s: %v", cmd, err)
		}
	}

	// Does the server answer the empty keep-alive command? Watch the packets for a while.
	got := c.lastRXTime()
	time.Sleep(7 * time.Second)
	after := c.lastRXTime()
	t.Logf("last packet from the server: before the wait %s, after %s (a keep-alive every 2 s)", got.Format(time.RFC3339Nano), after.Format(time.RFC3339Nano))
	if !after.After(got) {
		t.Errorf("the server did not answer the keep-alives: no packet in 7 s")
	}
}
