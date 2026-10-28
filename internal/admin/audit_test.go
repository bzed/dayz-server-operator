// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var auditNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func newTestAudit(t *testing.T) *Audit {
	t.Helper()
	return NewAudit(filepath.Join(t.TempDir(), "audit.jsonl"))
}

func TestForgetClientsBlanksOld(t *testing.T) {
	a := newTestAudit(t)
	for i, age := range []time.Duration{100 * 24 * time.Hour, 10 * 24 * time.Hour} {
		require.NoError(t, a.Log(AuditEntry{Time: auditNow.Add(-age), Actor: "web:" + strconv.Itoa(i), Client: "203.0.113." + strconv.Itoa(i), Action: "kick", OK: true}))
	}
	require.NoError(t, a.Log(AuditEntry{Time: auditNow.Add(-200 * 24 * time.Hour), Actor: "bot", Action: "kick"}))
	n, err := a.ForgetClients(auditNow.Add(-90 * 24 * time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1, n, "only the entry that had an address and is older")
	got, err := a.Tail("", 10)
	require.NoError(t, err)
	require.Len(t, got, 3, "no entry is dropped")
	// newest in the file first: the 200-day-old bot, the 10-day-old, the 100-day-old
	assert.Equal(t, "bot", got[0].Actor)
	assert.Equal(t, "203.0.113.1", got[1].Client, "recent enough")
	assert.Empty(t, got[2].Client, "blanked")
	assert.Equal(t, "web:0", got[2].Actor, "the rest of the entry stays")
	assert.Equal(t, "kick", got[2].Action)
}

func TestForgetClientsKeepsUnparsableLines(t *testing.T) {
	a := newTestAudit(t)
	require.NoError(t, a.Log(AuditEntry{Time: auditNow.Add(-100 * 24 * time.Hour), Client: "203.0.113.1", Action: "x"}))
	f, err := os.OpenFile(a.Path, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString("not json\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	_, err = a.ForgetClients(auditNow.Add(-90 * 24 * time.Hour))
	require.NoError(t, err)
	b, err := os.ReadFile(a.Path)
	require.NoError(t, err)
	assert.Contains(t, string(b), "not json\n")
	assert.NotContains(t, string(b), "203.0.113.1")
}

func TestForgetClientsNoChangeNoRewrite(t *testing.T) {
	a := newTestAudit(t)
	require.NoError(t, a.Log(AuditEntry{Time: auditNow.Add(-time.Hour), Client: "203.0.113.1", Action: "x"}))
	old := auditNow.Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(a.Path, old, old))
	n, err := a.ForgetClients(auditNow.Add(-90 * 24 * time.Hour))
	require.NoError(t, err)
	assert.Zero(t, n)
	fi, err := os.Stat(a.Path)
	require.NoError(t, err)
	assert.True(t, fi.ModTime().Equal(old), "the file was not touched")

	missing := NewAudit(filepath.Join(t.TempDir(), "none.jsonl"))
	n, err = missing.ForgetClients(auditNow)
	require.NoError(t, err, "no log yet is fine")
	assert.Zero(t, n)
}

func TestForgetClientsKeepsMode0600(t *testing.T) {
	a := newTestAudit(t)
	require.NoError(t, a.Log(AuditEntry{Time: auditNow.Add(-100 * 24 * time.Hour), Client: "203.0.113.1", Action: "x"}))
	_, err := a.ForgetClients(auditNow.Add(-90 * 24 * time.Hour))
	require.NoError(t, err)
	fi, err := os.Stat(a.Path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	entries, err := os.ReadDir(filepath.Dir(a.Path))
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp", "no temporary file is left")
	}
}

func TestForgetClientsWithConcurrentLog(t *testing.T) {
	a := newTestAudit(t)
	for i := 0; i < 50; i++ {
		require.NoError(t, a.Log(AuditEntry{Time: auditNow.Add(-100 * 24 * time.Hour), Client: "203.0.113.1", Action: "old" + strconv.Itoa(i)}))
	}
	// a second Audit on the same file is another process, like `dzo user`
	other := NewAudit(a.Path)
	var wg sync.WaitGroup
	const writers, each = 4, 25
	for w := 0; w < writers; w++ {
		l := a
		if w%2 == 1 {
			l = other
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				assert.NoError(t, l.Log(AuditEntry{Time: auditNow, Client: "198.51.100.1", Action: "new" + strconv.Itoa(w) + "-" + strconv.Itoa(i)}))
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			_, err := a.ForgetClients(auditNow.Add(-90 * 24 * time.Hour))
			assert.NoError(t, err)
		}
	}()
	wg.Wait()
	got, err := a.Tail("", 0)
	require.NoError(t, err)
	assert.Len(t, got, 50+writers*each, "no entry is lost")
}
