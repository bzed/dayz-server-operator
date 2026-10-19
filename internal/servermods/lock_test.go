// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package servermods

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lockFor(t testing.TB, root string) (*Lock, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "dist")
	res, err := Build(t.Context(), root, out)
	require.NoError(t, err)
	l, err := NewLock(t.Context(), root, out, res)
	require.NoError(t, err)
	return l, out
}

func TestLockPinsTheBuildAndAcceptsTheSameBuild(t *testing.T) {
	root, commit := gitFixture(t)
	l, _ := lockFor(t, root)
	path := filepath.Join(t.TempDir(), "servermods.lock")
	err := l.Write(path)
	require.NoError(t, err)
	b, _ := os.ReadFile(path)
	require.True(t, strings.HasPrefix(string(b), "# "), "lock file:\n%s", b)
	require.Contains(t, string(b), commit)
	require.Contains(t, string(b), "addons/demo.pbo")
	pinned, err := ReadLock(path)
	require.NoError(t, err)
	again, _ := lockFor(t, root) // a second build of the same sources
	err = pinned.Check(again)
	require.NoError(t, err, "the same build must match its lock")
}

func TestLockRefusesAChangedPBOAndSaysHowToUpdate(t *testing.T) {
	root, _ := gitFixture(t)
	l, _ := lockFor(t, root)
	pinned := &Lock{Servermods: map[string]ModCompat{"demo": {Commit: l.Servermods["demo"].Commit, PBOs: map[string]string{"addons/demo.pbo": strings.Repeat("0", 64)}}}}
	err := pinned.Check(l)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "demo")
	assert.Contains(t, err.Error(), "addons/demo.pbo")
	assert.Contains(t, err.Error(), "make servermods-lock")
}

func TestLockRefusesAnOtherCommitAndAddedOrRemovedMods(t *testing.T) {
	root, _ := gitFixture(t)
	l, _ := lockFor(t, root)
	other := &Lock{Servermods: map[string]ModCompat{"demo": {Commit: "deadbeef", PBOs: l.Servermods["demo"].PBOs}}}
	err := other.Check(l)
	require.ErrorContains(t, err, "commit")
	empty := &Lock{Servermods: map[string]ModCompat{}}
	err = empty.Check(l)
	require.ErrorContains(t, err, "not in the lock")
	extra := &Lock{Servermods: map[string]ModCompat{"demo": l.Servermods["demo"], "gone": {Commit: "x"}}}
	err = extra.Check(l)
	require.ErrorContains(t, err, "gone")
}

func TestReadLockOfAMissingFileSaysHowToCreateIt(t *testing.T) {
	_, err := ReadLock(filepath.Join(t.TempDir(), "servermods.lock"))
	require.ErrorContains(t, err, "make servermods-lock")
}
