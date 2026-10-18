// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package servermods

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LockFile is the file in the repository that pins the servermods: the commit of every submodule and the hash
// of every PBO built from it. CI builds the mods and refuses a build that differs, so a change of a servermod
// shows up as a change of this file in the same commit, and a build that is not reproducible is noticed.
const LockFile = "servermods.lock"

// REUSE-IgnoreStart
// lockHeader is the licence header the generated file carries.
const lockHeader = "# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>\n" +
	"# SPDX-License-Identifier: AGPL-3.0-or-later\n\n" +
	"# The servermods dzo builds: the commit of each and the sha256 of every PBO built from it.\n" +
	"# CI refuses a build that differs. Update it with `make servermods-lock` after a servermod changed.\n"

// REUSE-IgnoreEnd

// Lock is the content of servermods.lock. It has the shape of the servermods part of compat.yaml.
type Lock struct {
	Servermods map[string]ModCompat `yaml:"servermods"`
}

// NewLock pins the results of Build.
func NewLock(ctx context.Context, srcRoot, outRoot string, res []Result) (*Lock, error) {
	mods, err := compatOf(ctx, srcRoot, outRoot, res)
	if err != nil {
		return nil, err
	}
	return &Lock{Servermods: mods}, nil
}

// Write writes the lock file.
func (l *Lock) Write(path string) error {
	b, err := yaml.Marshal(l)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(lockHeader), b...), 0o644) //nolint:gosec // a file of the repository, read by everyone
}

// ReadLock reads a lock file.
func ReadLock(path string) (*Lock, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a file of the repository
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("servermods: %s does not exist; create it with `make servermods-lock`", path)
	}
	if err != nil {
		return nil, err
	}
	var l Lock
	if err := yaml.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &l, nil
}

// Check compares what was built with the lock. Every difference is listed: another commit, a changed, extra
// or missing PBO, a mod that is not locked and a locked mod that was not built.
func (l *Lock) Check(built *Lock) error {
	var bad []string
	for name, have := range built.Servermods {
		want, ok := l.Servermods[name]
		if !ok {
			bad = append(bad, name+" is not in the lock")
			continue
		}
		if have.Commit != want.Commit {
			bad = append(bad, fmt.Sprintf("%s: commit %.12s, locked %.12s", name, have.Commit, want.Commit))
		}
		for rel, sum := range have.PBOs {
			switch w, pinned := want.PBOs[rel]; {
			case !pinned:
				bad = append(bad, fmt.Sprintf("%s: %s is not in the lock", name, rel))
			case w != sum:
				bad = append(bad, fmt.Sprintf("%s: %s has sha256 %.12s, locked %.12s", name, rel, sum, w))
			}
		}
		for rel := range want.PBOs {
			if _, ok := have.PBOs[rel]; !ok {
				bad = append(bad, fmt.Sprintf("%s: %s was not built", name, rel))
			}
		}
	}
	for name := range l.Servermods {
		if _, ok := built.Servermods[name]; !ok {
			bad = append(bad, name+" is locked but was not built")
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("the servermods differ from %s:\n  %s\nif a servermod changed on purpose, run `make servermods-lock` and commit the lock file", LockFile, strings.Join(bad, "\n  "))
}
