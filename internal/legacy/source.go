// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package legacy converts the git repositories of the legacy dayzdockerserver (one branch per
// server) into an example site configuration (§C21). It reads with `git ls-tree` and `git show`
// only: no checkout, no volumes, no network.
package legacy

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Entry is one file of a branch.
type Entry struct {
	Mode string // 100644, 100755, 120000 (a symbolic link: the content is its target)
	Path string
}

// Source gives read access to the files of a branch.
type Source interface {
	// List returns every file of ref.
	List(ref string) ([]Entry, error)
	// Show returns the content of one file of ref.
	Show(ref, path string) ([]byte, error)
}

// Git reads a repository with the git binary.
type Git struct{ Repo string }

func (g Git) run(args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", g.Repo}, args...)...) //nolint:gosec // fixed subcommands; ref and path come from the operator
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// List implements Source.
func (g Git) List(ref string) ([]Entry, error) {
	b, err := g.run("ls-tree", "-r", "-z", ref)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, rec := range strings.Split(string(b), "\x00") {
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) < 3 {
			continue
		}
		out = append(out, Entry{Mode: f[0], Path: path})
	}
	return out, nil
}

// Show implements Source.
func (g Git) Show(ref, path string) ([]byte, error) { return g.run("show", ref+":"+path) }
