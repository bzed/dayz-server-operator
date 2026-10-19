// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenMan(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "man")
	out, err := runCmd(t, "gen-man", dir)
	require.NoError(t, err, "gen-man: %s", out)
	require.Contains(t, out, "man pages written")
	b, err := os.ReadFile(filepath.Join(dir, "dzo-instance-upgrade.1"))
	require.NoError(t, err)
	s := string(b)
	for _, want := range []string{".TH \"DZO-INSTANCE-UPGRADE\"", ".SH NAME", "\\-\\-build", ".SH SEE ALSO", "dzo\\-instance (1)"} {
		assert.Contains(t, s, want, "man page lacks %q", want)
	}
	top, _ := os.ReadFile(filepath.Join(dir, "dzo.1"))
	assert.Contains(t, string(top), ".SH COMMANDS", "dzo.1 does not list the commands")
	assert.Contains(t, string(top), "dzo\\-start(1)", "dzo.1 does not list the commands")
	_, err = os.Stat(filepath.Join(dir, "dzo-gen-man.1"))
	require.Error(t, err, "the hidden command has no man page")
	assert.Equal(t, "\\&.dot\n\\&'quote\na\\-b\\ec", roff(".dot\n'quote\na-b\\c"), "roff")
}
