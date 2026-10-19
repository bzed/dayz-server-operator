// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package servermods

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitFixture is fixture with the servermod "demo" committed in its own repo.
func gitFixture(t testing.TB) (root, commit string) {
	t.Helper()
	root = fixture(t)
	dir := filepath.Join(root, "demo")
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "x"},
	} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput() //nolint:gosec // test
		require.NoError(t, err, "git %v\n%s", args, out)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output() //nolint:gosec // test
	require.NoError(t, err)
	return root, strings.TrimSpace(string(out))
}

func buildCompat(t testing.TB, root, out string) {
	t.Helper()
	res, err := Build(t.Context(), root, out)
	require.NoError(t, err)
	err = WriteCompat(t.Context(), root, out, "1.2.3", "", res)
	require.NoError(t, err)
}

func TestCompatRecordsCommitAndPBOHashes(t *testing.T) {
	root, commit := gitFixture(t)
	out := filepath.Join(t.TempDir(), "dist")
	buildCompat(t, root, out)

	c, err := ReadCompat(filepath.Join(out, CompatFile))
	require.NoError(t, err)
	m := c.Servermods["demo"]
	require.Equal(t, "1.2.3", c.DZO)
	require.Equal(t, commit, m.Commit)
	require.Len(t, m.PBOs, 1)
	require.Len(t, m.PBOs["addons/demo.pbo"], 64)
	err = CheckShipped(filepath.Join(out, "demo"))
	require.NoError(t, err, "the freshly built mod must pass")

	// the same source gives the same compat.yaml
	out2 := filepath.Join(t.TempDir(), "dist")
	buildCompat(t, root, out2)
	a, _ := os.ReadFile(filepath.Join(out, CompatFile))
	b, _ := os.ReadFile(filepath.Join(out2, CompatFile))
	assert.True(t, bytes.Equal(a, b), "compat.yaml is not reproducible:\n%s\n%s", a, b)
}

func TestCompatRefusesADifferentBuild(t *testing.T) {
	root, _ := gitFixture(t)
	out := filepath.Join(t.TempDir(), "dist")
	buildCompat(t, root, out)
	mod := filepath.Join(out, "demo")

	pbo := filepath.Join(mod, "addons", "demo.pbo")
	b, _ := os.ReadFile(pbo)
	err := os.WriteFile(pbo, append(b, 0), 0o600)
	require.NoError(t, err)
	err = CheckShipped(mod)
	assert.ErrorContains(t, err, "demo.pbo has sha256")
	err = os.WriteFile(pbo, b, 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(mod, "addons", "extra.pbo"), []byte("x"), 0o600)
	require.NoError(t, err)
	err = CheckShipped(mod)
	assert.ErrorContains(t, err, "extra.pbo is not part of the tested set")
	_ = os.Remove(filepath.Join(mod, "addons", "extra.pbo"))
	_ = os.Remove(pbo)
	err = CheckShipped(mod)
	assert.ErrorContains(t, err, "demo.pbo is missing")
}

func TestCheckShippedLeavesOtherModsAlone(t *testing.T) {
	// no compat.yaml next to the mod
	err := CheckShipped(filepath.Join(t.TempDir(), "mine"))
	assert.NoError(t, err, "a mod without compat.yaml must pass")
	// a compat.yaml that does not list it
	root, _ := gitFixture(t)
	out := filepath.Join(t.TempDir(), "dist")
	buildCompat(t, root, out)
	err = CheckShipped(filepath.Join(out, "somebody-elses"))
	assert.NoError(t, err, "an unlisted mod must pass")
	// a compat.yaml that is broken must not be ignored silently
	err = os.WriteFile(filepath.Join(out, CompatFile), []byte("servermods: [\n"), 0o600)
	require.NoError(t, err)
	err = CheckShipped(filepath.Join(out, "demo"))
	assert.Error(t, err, "an unreadable compat.yaml must be an error")
}

func TestWriteCompatNeedsACheckout(t *testing.T) {
	root := fixture(t) // not a git repo
	out := filepath.Join(t.TempDir(), "dist")
	res, err := Build(t.Context(), root, out)
	require.NoError(t, err)
	// An error from git must not be hidden: without the commit the file is useless.
	err = WriteCompat(t.Context(), root, out, "1", "", res)
	assert.ErrorContains(t, err, "commit")
}

func TestCheckInstalledFindsAnOldGeneration(t *testing.T) {
	root, _ := gitFixture(t)
	out := filepath.Join(t.TempDir(), "dist")
	buildCompat(t, root, out)
	packaged := filepath.Join(out, "demo")

	// The installed generation is a copy under another name.
	installed := filepath.Join(t.TempDir(), "0123abcd")
	pbo, err := os.ReadFile(filepath.Join(packaged, "addons", "demo.pbo"))
	require.NoError(t, err)
	write(t, filepath.Join(installed, "addons", "demo.pbo"), string(pbo))
	err = CheckInstalled("demo", installed, packaged)
	require.NoError(t, err, "identical copy")
	write(t, filepath.Join(installed, "addons", "demo.pbo"), "an older build")
	err = CheckInstalled("demo", installed, packaged)
	require.ErrorContains(t, err, "demo.pbo")
	// Not one of ours (no compat.yaml next to it): nothing to compare with.
	err = CheckInstalled("demo", installed, filepath.Join(t.TempDir(), "demo"))
	require.NoError(t, err, "no compat.yaml")
}
