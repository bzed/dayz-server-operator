// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package servermods

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// CompatFile is the file next to the packed servermods that records the tested
// set (D39). It is written by `make servermods`, shipped with the package, and
// read when a servermod from the same directory is activated.
const CompatFile = "compat.yaml"

// Compat is compat.yaml: the servermods a dzo release was built and tested
// with.
type Compat struct {
	DZO string `yaml:"dzo"`
	// ServerBuild is the DayZServer build the set was boot-tested on. The
	// boot test fills it in; it is empty for a set that was only built.
	ServerBuild string               `yaml:"server_build,omitempty"`
	Servermods  map[string]ModCompat `yaml:"servermods"`
}

// ModCompat pins one servermod: the commit of its submodule and the hash of
// every PBO built from it, by path below the mod's directory.
type ModCompat struct {
	Commit string            `yaml:"commit"`
	PBOs   map[string]string `yaml:"pbos"`
}

func gitCommit(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-c", "safe.directory=*", "-C", dir, "rev-parse", "HEAD").Output() //nolint:gosec // dir is a servermod checkout under the build tree; safe.directory because a package build often runs as another user than the checkout's owner
	if err != nil {
		return "", fmt.Errorf("servermods: cannot read the commit of %s (is it a git checkout?): %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// WriteCompat writes outRoot/compat.yaml for the results of Build, with the
// commit of every servermod checkout below srcRoot. Like the PBOs it is
// reproducible: nothing in it depends on the time or the machine.
func WriteCompat(ctx context.Context, srcRoot, outRoot, dzoVersion, serverBuild string, res []Result) error {
	c := Compat{DZO: dzoVersion, ServerBuild: serverBuild, Servermods: map[string]ModCompat{}}
	for _, r := range res {
		m, ok := c.Servermods[r.Mod]
		if !ok {
			commit, err := gitCommit(ctx, filepath.Join(srcRoot, r.Mod))
			if err != nil {
				return err
			}
			m = ModCompat{Commit: commit, PBOs: map[string]string{}}
		}
		rel, err := filepath.Rel(filepath.Join(outRoot, r.Mod), r.PBO)
		if err != nil {
			return err
		}
		m.PBOs[filepath.ToSlash(rel)] = r.SHA256
		c.Servermods[r.Mod] = m
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outRoot, CompatFile), b, 0o600)
}

// ReadCompat reads a compat.yaml.
func ReadCompat(path string) (*Compat, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator's own file
	if err != nil {
		return nil, err
	}
	var c Compat
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// Check compares the PBOs below modDir/addons with the hashes pinned for the
// servermod modDir (named like its directory). Both a changed and an extra or
// missing PBO are errors.
func (c *Compat) Check(modDir string) error {
	return c.check(filepath.Base(modDir), modDir)
}

// check is Check for a servermod whose directory is not named like it (an installed generation).
func (c *Compat) check(name, modDir string) error {
	want, ok := c.Servermods[name]
	if !ok {
		return nil
	}
	have, err := filepath.Glob(filepath.Join(modDir, "addons", "*.pbo"))
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var bad []string
	for _, p := range have {
		rel, _ := filepath.Rel(modDir, p)
		rel = filepath.ToSlash(rel)
		seen[rel] = true
		sum, err := fileSHA256(p)
		if err != nil {
			return err
		}
		switch w, pinned := want.PBOs[rel]; {
		case !pinned:
			bad = append(bad, rel+" is not part of the tested set")
		case w != sum:
			bad = append(bad, fmt.Sprintf("%s has sha256 %.12s, the tested build has %.12s", rel, sum, w))
		}
	}
	for rel := range want.PBOs {
		if !seen[rel] {
			bad = append(bad, rel+" is missing")
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("servermod %s differs from the tested set in %s: %s", name, CompatFile, strings.Join(bad, "; "))
}

// CheckShipped checks a servermod directory against the compat.yaml next to
// it, if there is one. A directory without a neighbouring compat.yaml, or one
// that is not listed in it, is not one of ours and passes. Pass it as
// product.LocalSource.Check.
func CheckShipped(modDir string) error {
	c, err := ReadCompat(filepath.Join(filepath.Dir(modDir), CompatFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.Check(modDir)
}

// CheckInstalled compares the installed copy of the shipped servermod name (a generation directory
// of the cache) with the tested set in the compat.yaml next to packagedDir, the directory the mod
// is imported from. A mod with no neighbouring compat.yaml, or not listed in it, passes.
func CheckInstalled(name, installedDir, packagedDir string) error {
	c, err := ReadCompat(filepath.Join(filepath.Dir(packagedDir), CompatFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.check(name, installedDir)
}
