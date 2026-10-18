// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package servermods

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lockFor(t *testing.T, root string) (*Lock, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "dist")
	res, err := Build(context.Background(), root, out)
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewLock(context.Background(), root, out, res)
	if err != nil {
		t.Fatal(err)
	}
	return l, out
}

func TestLockPinsTheBuildAndAcceptsTheSameBuild(t *testing.T) {
	root, commit := gitFixture(t)
	l, _ := lockFor(t, root)
	path := filepath.Join(t.TempDir(), "servermods.lock")
	if err := l.Write(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "# ") || !strings.Contains(string(b), commit) || !strings.Contains(string(b), "addons/demo.pbo") {
		t.Fatalf("lock file:\n%s", b)
	}
	pinned, err := ReadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := lockFor(t, root) // a second build of the same sources
	if err := pinned.Check(again); err != nil {
		t.Fatalf("the same build must match its lock: %v", err)
	}
}

func TestLockRefusesAChangedPBOAndSaysHowToUpdate(t *testing.T) {
	root, _ := gitFixture(t)
	l, _ := lockFor(t, root)
	pinned := &Lock{Servermods: map[string]ModCompat{"demo": {Commit: l.Servermods["demo"].Commit, PBOs: map[string]string{"addons/demo.pbo": strings.Repeat("0", 64)}}}}
	err := pinned.Check(l)
	if err == nil || !strings.Contains(err.Error(), "demo") || !strings.Contains(err.Error(), "addons/demo.pbo") || !strings.Contains(err.Error(), "make servermods-lock") {
		t.Fatalf("a changed PBO: %v", err)
	}
}

func TestLockRefusesAnOtherCommitAndAddedOrRemovedMods(t *testing.T) {
	root, _ := gitFixture(t)
	l, _ := lockFor(t, root)
	other := &Lock{Servermods: map[string]ModCompat{"demo": {Commit: "deadbeef", PBOs: l.Servermods["demo"].PBOs}}}
	if err := other.Check(l); err == nil || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("another commit: %v", err)
	}
	empty := &Lock{Servermods: map[string]ModCompat{}}
	if err := empty.Check(l); err == nil || !strings.Contains(err.Error(), "not in the lock") {
		t.Fatalf("a mod that is not locked: %v", err)
	}
	extra := &Lock{Servermods: map[string]ModCompat{"demo": l.Servermods["demo"], "gone": {Commit: "x"}}}
	if err := extra.Check(l); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("a locked mod that was not built: %v", err)
	}
}

func TestReadLockOfAMissingFileSaysHowToCreateIt(t *testing.T) {
	if _, err := ReadLock(filepath.Join(t.TempDir(), "servermods.lock")); err == nil || !strings.Contains(err.Error(), "make servermods-lock") {
		t.Fatalf("missing lock: %v", err)
	}
}
