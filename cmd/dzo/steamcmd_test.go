// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultSteamcmd(t *testing.T) {
	const wrapper = "/usr/libexec/dzo/steamcmd-container"
	lookPath := func(path string, found bool) func(string) (string, error) {
		return func(name string) (string, error) {
			assert.Equal(t, "steamcmd", name, "only the host's steamcmd decides")
			if !found {
				return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
			}
			return path, nil
		}
	}
	assert.Equal(t, "steamcmd", defaultSteamcmd(lookPath("/usr/bin/steamcmd", true), wrapper),
		"a host steamcmd is run by its name from PATH")
	assert.Equal(t, wrapper, defaultSteamcmd(lookPath("", false), wrapper),
		"without a host steamcmd the dzo-steamcmd container wrapper is the fallback")
}
