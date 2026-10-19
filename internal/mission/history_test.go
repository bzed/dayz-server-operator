// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRollbackRestoresManagedFiles(t *testing.T) {
	root := t.TempDir()
	hist, live := filepath.Join(root, "filehistory"), filepath.Join(root, "live")
	put := func(p, body string) {
		t.Helper()
		err := os.MkdirAll(filepath.Dir(p), 0o750)
		require.NoError(t, err)
		err = os.WriteFile(p, []byte(body), 0o600)
		require.NoError(t, err)
	}
	put(filepath.Join(hist, "20260101T000000.000000000Z", "a.xml"), "old-a")
	put(filepath.Join(hist, "20260102T000000.000000000Z", "a.xml"), "newer-a")
	put(filepath.Join(hist, "20260102T000000.000000000Z", "db", "b.xml"), "newer-b")
	put(filepath.Join(hist, "20260102T000000.000000000Z", "foreign.txt"), "not ours")
	put(filepath.Join(live, "a.xml"), "current-a")
	m := NewManifest()
	m.Entries["a.xml"] = Entry{Origin: OriginPristine}
	m.Entries["db/b.xml"] = Entry{Origin: OriginPristine}

	sets, err := HistorySets(hist)
	require.NoError(t, err)
	require.Len(t, sets, 2)
	require.LessOrEqual(t, sets[0], sets[1])
	set, restored, err := Rollback(hist, live, m, "")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(set, "20260102"))
	require.Equal(t, "a.xml,db/b.xml", strings.Join(restored, ","))
	b, _ := os.ReadFile(filepath.Join(live, "a.xml"))
	assert.Equal(t, "newer-a", string(b))
	_, err = os.Stat(filepath.Join(live, "foreign.txt"))
	require.Error(t, err, "a file that is not in the manifest must not be restored")
	assert.Equal(t, HashBytes([]byte("newer-a")), m.Entries["a.xml"].WrittenHash, "the manifest must record the restored content")
	set, _, err = Rollback(hist, live, m, "20260101")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(set, "20260101"))
	_, _, err = Rollback(hist, live, m, "1999")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "20260102")
	_, _, err = Rollback(filepath.Join(root, "none"), live, m, "")
	require.Error(t, err, "no history at all must be an error")
	sets, err = HistorySets(filepath.Join(root, "none"))
	require.NoError(t, err)
	assert.Nil(t, sets)
}
