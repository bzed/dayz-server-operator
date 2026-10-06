// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRollbackRestoresManagedFiles(t *testing.T) {
	root := t.TempDir()
	hist, live := filepath.Join(root, "filehistory"), filepath.Join(root, "live")
	put := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
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
	if err != nil || len(sets) != 2 || sets[0] > sets[1] {
		t.Fatalf("sets = %v %v", sets, err)
	}
	set, restored, err := Rollback(hist, live, m, "")
	if err != nil || !strings.HasPrefix(set, "20260102") || strings.Join(restored, ",") != "a.xml,db/b.xml" {
		t.Fatalf("set=%s restored=%v err=%v", set, restored, err)
	}
	if b, _ := os.ReadFile(filepath.Join(live, "a.xml")); string(b) != "newer-a" { //nolint:gosec // test fixture
		t.Errorf("a.xml = %q", b)
	}
	if _, err := os.Stat(filepath.Join(live, "foreign.txt")); err == nil {
		t.Error("a file that is not in the manifest must not be restored")
	}
	if m.Entries["a.xml"].WrittenHash != HashBytes([]byte("newer-a")) {
		t.Error("the manifest must record the restored content")
	}
	if set, _, err := Rollback(hist, live, m, "20260101"); err != nil || !strings.HasPrefix(set, "20260101") {
		t.Errorf("a named set: %s %v", set, err)
	}
	if _, _, err := Rollback(hist, live, m, "1999"); err == nil || !strings.Contains(err.Error(), "20260102") {
		t.Errorf("an unknown set names the ones there are: %v", err)
	}
	if _, _, err := Rollback(filepath.Join(root, "none"), live, m, ""); err == nil {
		t.Error("no history at all must be an error")
	}
	if sets, err := HistorySets(filepath.Join(root, "none")); err != nil || sets != nil {
		t.Errorf("a missing history directory is no sets: %v %v", sets, err)
	}
}
