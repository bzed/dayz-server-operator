// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Repo is a checkout of the site config repo (D16): remote URL and branch
// are configurable, and pull/commit/push behaviour is configurable per
// deployment. It shells out to the system `git` binary rather than
// vendoring a Go git implementation (lazy engineering, D35): git is
// already a hard package dependency (debian/control) and every operation
// here is a plain, well-documented git command.
type Repo struct {
	Dir           string
	RemoteURL     string
	Branch        string
	DeployKeyPath string // optional SSH private key for git+ssh remotes
	AutoPush      bool
}

// ErrDirty is returned by Pull when the checkout has uncommitted local
// changes: automatic pulls must never silently overwrite operator or web
// edits that have not been committed yet.
var ErrDirty = fmt.Errorf("site: checkout has uncommitted local changes")

func (r *Repo) env() []string {
	env := os.Environ()
	if r.DeployKeyPath != "" {
		ssh := fmt.Sprintf("ssh -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new", r.DeployKeyPath)
		env = append(env, "GIT_SSH_COMMAND="+ssh)
	}
	return env
}

func (r *Repo) run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // args are fixed git subcommands with operator-configured paths/URLs, not raw external input
	cmd.Dir = dir
	cmd.Env = r.env()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("site: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// Cloned reports whether Dir already holds a git checkout.
func (r *Repo) Cloned() bool {
	info, err := os.Stat(r.Dir + "/.git")
	return err == nil && (info.IsDir() || info.Mode().IsRegular()) // a worktree's .git is a file
}

// Clone clones RemoteURL/Branch into Dir, which must not exist yet (or
// must be empty).
func (r *Repo) Clone(ctx context.Context) error {
	if r.RemoteURL == "" {
		return fmt.Errorf("site: remote URL is not configured")
	}
	args := []string{"clone", "--branch", r.Branch, "--single-branch", r.RemoteURL, r.Dir}
	if _, err := r.run(ctx, "", args...); err != nil {
		return err
	}
	return nil
}

// IsDirty reports whether the checkout has uncommitted changes (staged,
// unstaged or untracked).
func (r *Repo) IsDirty(ctx context.Context) (bool, error) {
	out, err := r.run(ctx, r.Dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// Status returns `git status --short --branch` of the checkout.
func (r *Repo) Status(ctx context.Context) (string, error) {
	return r.run(ctx, r.Dir, "status", "--short", "--branch")
}

// Pull fast-forwards the checkout to the remote branch's tip. It refuses
// (ErrDirty) when the checkout has uncommitted local changes, rather than
// overwriting them.
func (r *Repo) Pull(ctx context.Context) error {
	dirty, err := r.IsDirty(ctx)
	if err != nil {
		return err
	}
	if dirty {
		return ErrDirty
	}
	if _, err := r.run(ctx, r.Dir, "fetch", "origin", r.Branch); err != nil {
		return err
	}
	if _, err := r.run(ctx, r.Dir, "merge", "--ff-only", "origin/"+r.Branch); err != nil {
		return err
	}
	return nil
}

// Commit stages every change and commits it under the given author
// ("Name <email>"). It returns (false, nil) rather than an error when
// there was nothing to commit.
func (r *Repo) Commit(ctx context.Context, message, author string) (bool, error) {
	if _, err := r.run(ctx, r.Dir, "add", "-A"); err != nil {
		return false, err
	}
	dirty, err := r.IsDirty(ctx)
	if err != nil {
		return false, err
	}
	if !dirty {
		return false, nil
	}
	args := []string{"commit", "-m", message}
	if author != "" {
		args = append(args, "--author", author)
	}
	if _, err := r.run(ctx, r.Dir, args...); err != nil {
		return false, err
	}
	return true, nil
}

// Push pushes the current branch to origin, if AutoPush is set. It is a
// no-op (not an error) when AutoPush is false, so callers can call it
// unconditionally after Commit.
func (r *Repo) Push(ctx context.Context) error {
	if !r.AutoPush {
		return nil
	}
	_, err := r.run(ctx, r.Dir, "push", "origin", "HEAD:"+r.Branch)
	return err
}

// HeadCommit returns the checkout's current commit hash.
func (r *Repo) HeadCommit(ctx context.Context) (string, error) {
	out, err := r.run(ctx, r.Dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
