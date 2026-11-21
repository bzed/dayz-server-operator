// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWarnMissingWebDirs(t *testing.T) {
	dir := t.TempDir()
	var got []string
	logf := func(f string, a ...any) { got = append(got, fmt.Sprintf(f, a...)) }

	warnMissingWebDirs(logf, "", "")
	require.Empty(t, got, "the built-in files need no warning")

	warnMissingWebDirs(logf, dir, filepath.Join(dir, "gone"))
	require.Len(t, got, 1, "one warning for the missing flags dir")
	assert.Equal(t, "web.flags_dir "+filepath.Join(dir, "gone")+" is not a directory; serving the built-in copies", got[0])

	got = nil
	file := filepath.Join(dir, "a-file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	warnMissingWebDirs(logf, file, dir)
	require.Len(t, got, 1, "one warning for the file that is no directory")
	assert.Equal(t, "web.assets "+file+" is not a directory; serving the built-in copies", got[0])
}
