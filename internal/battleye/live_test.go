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

// TestLiveSessionRestart watches a Session while the game server is restarted by hand (spike S4:
// behaviour on a server restart). Set DZO_LIVE_BE_WATCH to the number of seconds to watch; restart
// the server during that time. It logs when the connection is up, lost and back, and fails when
// the session never comes back.
func TestLiveSessionRestart(t *testing.T) {
	addr, pw, watch := os.Getenv("DZO_LIVE_BE_ADDR"), os.Getenv("DZO_LIVE_BE_PASSWORD"), os.Getenv("DZO_LIVE_BE_WATCH")
	if addr == "" || pw == "" || watch == "" {
		t.Skip("set DZO_LIVE_BE_ADDR, DZO_LIVE_BE_PASSWORD and DZO_LIVE_BE_WATCH (seconds)")
	}
	secs, err := time.ParseDuration(watch + "s")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	s := &Session{Addr: addr, Password: pw, MinBackoff: time.Second, MaxBackoff: 5 * time.Second,
		Log: func(f string, a ...any) {
			t.Logf("%5.1fs session: "+f, append([]any{time.Since(start).Seconds()}, a...)...)
		}}
	defer func() { _ = s.Close() }()
	var ups, downs int
	was := true
	for time.Since(start) < secs {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := s.Command(ctx, "players")
		cancel()
		if (err == nil) != was {
			was = err == nil
			t.Logf("%5.1fs commands %s (err=%v)", time.Since(start).Seconds(), map[bool]string{true: "work again", false: "fail"}[was], err)
			if was {
				ups++
			} else {
				downs++
			}
		}
		time.Sleep(time.Second)
	}
	if downs > 0 && !was {
		t.Errorf("the session did not come back after the restart")
	}
	t.Logf("connection lost %d time(s), came back %d time(s)", downs, ups)
}
