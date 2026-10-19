// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRemote creates a bare git repo with one commit on branch main, and
// returns its filesystem path (usable as a "URL" for local git clones).
func newTestRemote(t testing.TB) string {
	t.Helper()
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare", "--initial-branch=main", ".")

	seed := t.TempDir()
	runGit(t, seed, "init", "--initial-branch=main", ".")
	runGit(t, seed, "config", "user.email", "test@example.invalid")
	runGit(t, seed, "config", "user.name", "Test")
	err := os.WriteFile(filepath.Join(seed, "site.yaml"), []byte("defaults: {}\n"), 0o600)
	require.NoError(t, err, "write fixture")
	runGit(t, seed, "add", "-A")
	runGit(t, seed, "commit", "-m", "initial")
	runGit(t, seed, "remote", "add", "origin", remote)
	runGit(t, seed, "push", "origin", "main")

	return remote
}

func runGit(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err)
	return string(out)
}

func TestCloneAndPull(t *testing.T) {
	remote := newTestRemote(t)
	dir := filepath.Join(t.TempDir(), "checkout")

	r := &Repo{Dir: dir, RemoteURL: remote, Branch: "main"}
	require.False(t, r.Cloned(), "Cloned() should be false before Clone")
	err := r.Clone(t.Context())
	require.NoError(t, err, "Clone")
	require.True(t, r.Cloned(), "Cloned() should be true after Clone")
	_, err = os.Stat(filepath.Join(dir, "site.yaml"))
	require.NoError(t, err)

	dirty, err := r.IsDirty(t.Context())
	require.NoError(t, err, "IsDirty")
	assert.False(t, dirty, "fresh clone should not be dirty")

	err = r.Pull(t.Context())
	require.NoError(t, err, "Pull on an up-to-date clone")
}

func TestCloneRejectsEmptyRemoteURL(t *testing.T) {
	r := &Repo{Dir: t.TempDir(), Branch: "main"}
	err := r.Clone(t.Context())
	require.Error(t, err)
}

func TestPullFetchesNewCommits(t *testing.T) {
	remote := newTestRemote(t)
	dir := filepath.Join(t.TempDir(), "checkout")
	r := &Repo{Dir: dir, RemoteURL: remote, Branch: "main"}
	err := r.Clone(t.Context())
	require.NoError(t, err, "Clone")

	// Push a second commit from a different clone, simulating another
	// actor (an admin editing the site repo) updating the remote.
	other := filepath.Join(t.TempDir(), "other")
	runGit(t, filepath.Dir(other), "clone", remote, other)
	runGit(t, other, "config", "user.email", "test@example.invalid")
	runGit(t, other, "config", "user.name", "Test")
	err = os.WriteFile(filepath.Join(other, "new-file.yaml"), []byte("x: 1\n"), 0o600)
	require.NoError(t, err, "write fixture")
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-m", "second commit")
	runGit(t, other, "push", "origin", "main")

	err = r.Pull(t.Context())
	require.NoError(t, err, "Pull")
	_, err = os.Stat(filepath.Join(dir, "new-file.yaml"))
	require.NoError(t, err)
}

func TestPullRefusesWhenDirty(t *testing.T) {
	remote := newTestRemote(t)
	dir := filepath.Join(t.TempDir(), "checkout")
	r := &Repo{Dir: dir, RemoteURL: remote, Branch: "main"}
	err := r.Clone(t.Context())
	require.NoError(t, err, "Clone")
	err = os.WriteFile(filepath.Join(dir, "site.yaml"), []byte("defaults: {edited: true}\n"), 0o600)
	require.NoError(t, err, "write")

	err = r.Pull(t.Context())
	require.Equal(t, ErrDirty, err)
}

func TestCommitAndPush(t *testing.T) {
	remote := newTestRemote(t)
	dir := filepath.Join(t.TempDir(), "checkout")
	r := &Repo{Dir: dir, RemoteURL: remote, Branch: "main", AutoPush: true}
	err := r.Clone(t.Context())
	require.NoError(t, err, "Clone")
	runGit(t, dir, "config", "user.email", "dzo@example.invalid")
	runGit(t, dir, "config", "user.name", "dzo")

	err = os.MkdirAll(filepath.Join(dir, "instances"), 0o750)
	require.NoError(t, err, "mkdir")
	err = os.WriteFile(filepath.Join(dir, "instances/new.yaml"), []byte("name: new\n"), 0o600)
	require.NoError(t, err, "write")

	committed, err := r.Commit(t.Context(), "dzo mod add", "dzo <dzo@example.invalid>")
	require.NoError(t, err, "Commit")
	require.True(t, committed, "expected Commit to report a new commit")

	err = r.Push(t.Context())
	require.NoError(t, err, "Push")

	// Verify the remote actually received it, by cloning it again.
	verify := filepath.Join(t.TempDir(), "verify")
	r2 := &Repo{Dir: verify, RemoteURL: remote, Branch: "main"}
	err = r2.Clone(t.Context())
	require.NoError(t, err, "Clone (verify)")
	_, err = os.Stat(filepath.Join(verify, "instances/new.yaml"))
	require.NoError(t, err)
}

func TestCommitNoOpWhenClean(t *testing.T) {
	remote := newTestRemote(t)
	dir := filepath.Join(t.TempDir(), "checkout")
	r := &Repo{Dir: dir, RemoteURL: remote, Branch: "main"}
	err := r.Clone(t.Context())
	require.NoError(t, err, "Clone")
	runGit(t, dir, "config", "user.email", "dzo@example.invalid")
	runGit(t, dir, "config", "user.name", "dzo")

	committed, err := r.Commit(t.Context(), "nothing changed", "")
	require.NoError(t, err, "Commit")
	require.False(t, committed, "Commit should report no commit when nothing changed")
}

func TestPushWithoutAutoPushIsNoOp(t *testing.T) {
	remote := newTestRemote(t)
	dir := filepath.Join(t.TempDir(), "checkout")
	r := &Repo{Dir: dir, RemoteURL: remote, Branch: "main", AutoPush: false}
	err := r.Clone(t.Context())
	require.NoError(t, err, "Clone")
	err = r.Push(t.Context())
	require.NoError(t, err)
}

func TestHeadCommit(t *testing.T) {
	remote := newTestRemote(t)
	dir := filepath.Join(t.TempDir(), "checkout")
	r := &Repo{Dir: dir, RemoteURL: remote, Branch: "main"}
	err := r.Clone(t.Context())
	require.NoError(t, err, "Clone")
	head, err := r.HeadCommit(t.Context())
	require.NoError(t, err, "HeadCommit")
	assert.Len(t, head, 40)
}

func TestRunReportsGitErrors(t *testing.T) {
	r := &Repo{Dir: t.TempDir(), Branch: "main"}
	_, err := r.run(t.Context(), r.Dir, "not-a-real-git-command")
	require.Error(t, err)
}

func TestDeployKeySetsGitSSHCommand(t *testing.T) {
	r := &Repo{DeployKeyPath: "/tmp/does-not-need-to-exist"}
	env := r.env()
	found := false
	for _, e := range env {
		if e == "GIT_SSH_COMMAND=ssh -i /tmp/does-not-need-to-exist -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new" {
			found = true
		}
	}
	assert.True(t, found, "env() = %v, missing GIT_SSH_COMMAND", env)
}
