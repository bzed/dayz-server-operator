// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package servermods

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitFixture is fixture with the servermod "demo" committed in its own repo.
func gitFixture(t *testing.T) (root, commit string) {
	t.Helper()
	root = fixture(t)
	dir := filepath.Join(root, "demo")
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "x"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil { //nolint:gosec // test
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output() //nolint:gosec // test
	if err != nil {
		t.Fatal(err)
	}
	return root, strings.TrimSpace(string(out))
}

func buildCompat(t *testing.T, root, out string) {
	t.Helper()
	res, err := Build(context.Background(), root, out)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteCompat(context.Background(), root, out, "1.2.3", "", res); err != nil {
		t.Fatal(err)
	}
}

func TestCompatRecordsCommitAndPBOHashes(t *testing.T) {
	root, commit := gitFixture(t)
	out := filepath.Join(t.TempDir(), "dist")
	buildCompat(t, root, out)

	c, err := ReadCompat(filepath.Join(out, CompatFile))
	if err != nil {
		t.Fatal(err)
	}
	m := c.Servermods["demo"]
	if c.DZO != "1.2.3" || m.Commit != commit || len(m.PBOs) != 1 || len(m.PBOs["addons/demo.pbo"]) != 64 {
		t.Fatalf("compat = %+v", c)
	}
	if err := CheckShipped(filepath.Join(out, "demo")); err != nil {
		t.Fatalf("the freshly built mod must pass: %v", err)
	}

	// the same source gives the same compat.yaml
	out2 := filepath.Join(t.TempDir(), "dist")
	buildCompat(t, root, out2)
	a, _ := os.ReadFile(filepath.Join(out, CompatFile))
	b, _ := os.ReadFile(filepath.Join(out2, CompatFile))
	if !bytes.Equal(a, b) {
		t.Errorf("compat.yaml is not reproducible:\n%s\n%s", a, b)
	}
}

func TestCompatRefusesADifferentBuild(t *testing.T) {
	root, _ := gitFixture(t)
	out := filepath.Join(t.TempDir(), "dist")
	buildCompat(t, root, out)
	mod := filepath.Join(out, "demo")

	pbo := filepath.Join(mod, "addons", "demo.pbo")
	b, _ := os.ReadFile(pbo)
	if err := os.WriteFile(pbo, append(b, 0), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckShipped(mod); err == nil || !strings.Contains(err.Error(), "demo.pbo has sha256") {
		t.Errorf("a changed PBO must be refused, got %v", err)
	}
	if err := os.WriteFile(pbo, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "addons", "extra.pbo"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckShipped(mod); err == nil || !strings.Contains(err.Error(), "extra.pbo is not part of the tested set") {
		t.Errorf("an extra PBO must be refused, got %v", err)
	}
	_ = os.Remove(filepath.Join(mod, "addons", "extra.pbo"))
	_ = os.Remove(pbo)
	if err := CheckShipped(mod); err == nil || !strings.Contains(err.Error(), "demo.pbo is missing") {
		t.Errorf("a missing PBO must be refused, got %v", err)
	}
}

func TestCheckShippedLeavesOtherModsAlone(t *testing.T) {
	// no compat.yaml next to the mod
	if err := CheckShipped(filepath.Join(t.TempDir(), "mine")); err != nil {
		t.Errorf("a mod without compat.yaml must pass: %v", err)
	}
	// a compat.yaml that does not list it
	root, _ := gitFixture(t)
	out := filepath.Join(t.TempDir(), "dist")
	buildCompat(t, root, out)
	if err := CheckShipped(filepath.Join(out, "somebody-elses")); err != nil {
		t.Errorf("an unlisted mod must pass: %v", err)
	}
	// a compat.yaml that is broken must not be ignored silently
	if err := os.WriteFile(filepath.Join(out, CompatFile), []byte("servermods: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckShipped(filepath.Join(out, "demo")); err == nil {
		t.Error("an unreadable compat.yaml must be an error")
	}
}

func TestWriteCompatNeedsACheckout(t *testing.T) {
	root := fixture(t) // not a git repo
	out := filepath.Join(t.TempDir(), "dist")
	res, err := Build(context.Background(), root, out)
	if err != nil {
		t.Fatal(err)
	}
	// An error from git must not be hidden: without the commit the file is useless.
	if err := WriteCompat(context.Background(), root, out, "1", "", res); err == nil || !strings.Contains(err.Error(), "commit") {
		t.Errorf("want a commit error, got %v", err)
	}
}

func TestCheckInstalledFindsAnOldGeneration(t *testing.T) {
	root, _ := gitFixture(t)
	out := filepath.Join(t.TempDir(), "dist")
	buildCompat(t, root, out)
	packaged := filepath.Join(out, "demo")

	// The installed generation is a copy under another name.
	installed := filepath.Join(t.TempDir(), "0123abcd")
	pbo, err := os.ReadFile(filepath.Join(packaged, "addons", "demo.pbo"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(installed, "addons", "demo.pbo"), string(pbo))
	if err := CheckInstalled("demo", installed, packaged); err != nil {
		t.Fatalf("identical copy: %v", err)
	}
	write(t, filepath.Join(installed, "addons", "demo.pbo"), "an older build")
	if err := CheckInstalled("demo", installed, packaged); err == nil || !strings.Contains(err.Error(), "demo.pbo") {
		t.Fatalf("old copy: %v", err)
	}
	// Not one of ours (no compat.yaml next to it): nothing to compare with.
	if err := CheckInstalled("demo", installed, filepath.Join(t.TempDir(), "demo")); err != nil {
		t.Fatalf("no compat.yaml: %v", err)
	}
}
