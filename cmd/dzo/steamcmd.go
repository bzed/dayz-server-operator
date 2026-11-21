// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// defaultSteamcmd is the steamcmd program the download commands and
// `dzo steam login` run by default: the host's own steamcmd when there is one,
// else wrapper, which runs steamcmd from the dzo-steamcmd image (for hosts
// whose distribution packages no steamcmd, Fedora, RHEL and Arch).
func defaultSteamcmd(lookPath func(string) (string, error), wrapper string) string {
	if _, err := lookPath("steamcmd"); err == nil {
		return "steamcmd"
	}
	return wrapper
}
