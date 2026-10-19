// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func fakeProcRoot(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "1234"), 0o755), "mkdir")
	require.NoError(t, os.WriteFile(filepath.Join(root, "1234", "comm"), []byte("DayZServer\n"), 0o600), "write comm")
	return root
}

func fakeA2SForCLI(t testing.TB) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err, "listen")
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 4096)
		for {
			n, remote, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_ = n
			body := append([]byte{17}, make([]byte, 14)...)
			resp := append([]byte{0xFF, 0xFF, 0xFF, 0xFF, 'I'}, body...)
			if _, err := conn.WriteToUDP(resp, remote); err != nil {
				return
			}
		}
	}()
	return conn.LocalAddr().String()
}

func TestHealthStartupSuccess(t *testing.T) {
	_, err := runCmd(t, "health", "startup",
		"--query", fakeA2SForCLI(t),
		"--proc-root", fakeProcRoot(t),
		"--timeout", "2s",
	)
	require.NoError(t, err, "health startup")
}

func TestHealthStartupMissingQuery(t *testing.T) {
	_, err := runCmd(t, "health", "startup", "--proc-root", fakeProcRoot(t))
	require.Error(t, err, "expected an error for missing --query")
}

func TestHealthLiveProcessNotRunning(t *testing.T) {
	_, err := runCmd(t, "health", "live",
		"--query", fakeA2SForCLI(t),
		"--proc-root", t.TempDir(),
		"--timeout", "500ms",
	)
	require.Error(t, err, "expected an error when the process isn't running")
}
