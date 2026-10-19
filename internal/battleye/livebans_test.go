// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package battleye

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestLiveBans captures how a real DayZ server's BattlEye stores bans (spike V1 of the ban design). It
// only runs with DZO_LIVE_BE_ADDR, DZO_LIVE_BE_PASSWORD, DZO_LIVE_BE_DIR (the server's BattlEye
// directory) and DZO_LIVE_CAPTURE (where the captures go) set, against a server nobody plays on: it
// adds bans and removes every ban at the end.
func TestLiveBans(t *testing.T) {
	addr, pw := os.Getenv("DZO_LIVE_BE_ADDR"), os.Getenv("DZO_LIVE_BE_PASSWORD")
	dir, out := os.Getenv("DZO_LIVE_BE_DIR"), os.Getenv("DZO_LIVE_CAPTURE")
	if addr == "" || pw == "" || dir == "" || out == "" {
		t.Skip("set DZO_LIVE_BE_ADDR, DZO_LIVE_BE_PASSWORD, DZO_LIVE_BE_DIR and DZO_LIVE_CAPTURE")
	}
	err := os.MkdirAll(out, 0o750)
	require.NoError(t, err)
	c, err := Dial(addr, pw)
	require.NoError(t, err, "Dial")
	defer func() { _ = c.Close() }()
	run := func(cmd string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		res, err := c.Command(ctx, cmd)
		t.Logf("%q -> err=%v\n%s", cmd, err, res)
		require.NoError(t, err, "%s", cmd)
		return res
	}
	save := func(name, content string) {
		t.Helper()
		err := os.WriteFile(filepath.Join(out, name), []byte(content), 0o600)
		require.NoError(t, err)
	}
	readFile := func() string {
		b, err := os.ReadFile(filepath.Join(dir, "bans.txt")) //nolint:gosec // the test server's own file
		if err != nil {
			return "<" + err.Error() + ">"
		}
		return string(b)
	}

	const g1, g2, g3, g4 = "0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210",
		"11111111111111111111111111111111", "22222222222222222222222222222222"
	long := strings.Repeat("x", 300)
	// Q1-Q5: what the server writes for each kind of ban and reason.
	for _, cmd := range []string{
		"addBan " + g1 + " 0 permanent reason with spaces",
		"addBan " + g2 + " 5 timed äöü \"quoted\" 100%",
		"addBan 192.0.2.7 0 ip ban",
		"addBan 2001:db8::7 0 ipv6 ban",
		"addBan " + g3 + " 0 " + long,
	} {
		run(cmd)
	}
	save("bans-after-add.out", run("bans"))
	save("bans-file-after-add.txt", readFile()) // Q8: written without writeBans?
	run("writeBans")
	save("bans.txt", readFile())

	// Q6: a file in our format with a comment line, loaded with loadBans.
	expiry := time.Now().Add(90 * time.Second).Unix()
	own := fmt.Sprintf("// written by the dzo V1 test\n%s -1 own permanent\n%s %d own timed\n198.51.100.9 -1 own ip\n", g1, g4, expiry)
	err = os.WriteFile(filepath.Join(dir, "bans.txt"), []byte(own), 0o600)
	require.NoError(t, err)
	run("loadBans")
	save("bans-after-load.out", run("bans"))

	// Q7: is the timed ban listed after its expiry, and does writeBans drop it?
	// A live capture: the server's clock decides, there is nothing to poll.
	select {
	case <-t.Context().Done():
	case <-time.After(time.Until(time.Unix(expiry+5, 0))):
	}
	save("bans-after-expiry.out", run("bans"))
	run("writeBans")
	save("bans.txt.after-expiry", readFile())

	// Clean up: removeBan 0 until the list is empty. The numbers shift on every removal.
	num := regexp.MustCompile(`(?m)^\s*(\d+)\s`)
	var removed strings.Builder
	for i := 0; i < 50 && num.MatchString(run("bans")); i++ {
		removed.WriteString(run("removeBan 0") + "\n")
	}
	save("remove.out", removed.String())
	run("writeBans")
}
