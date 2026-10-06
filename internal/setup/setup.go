// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package setup is `dzo setup`: the first-time steps after installing the
// package, run as the service user. It creates and checks the data
// directories, builds the two container images, tells the operator what is
// left to do for Steam, clones the site repo and installs the weekly image
// refresh timer. Every step is idempotent, so it can be run again after a
// package upgrade, and --dry-run only reports what it would do.
package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/bzed/dayz-server-operator/internal/btrfs"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/bzed/dayz-server-operator/internal/steam"
)

// DefaultImagesDir is where the package ships the Containerfiles.
const DefaultImagesDir = "/usr/share/dzo/images"

// Images are the container images dzo builds: the directory below the images
// dir, and the tag the instance quadlets and the steamcmd jobs refer to.
var Images = []struct{ Dir, Tag string }{
	{"runtime", "localhost/dzo-runtime:latest"},
	{"steamcmd", "localhost/dzo-steamcmd:latest"},
}

const (
	minFreeGiB   = 10
	refreshTimer = "dzo-image-refresh"
)

// Exec runs a command and returns its combined output; tests replace it.
type Exec func(ctx context.Context, name string, args ...string) ([]byte, error)

// Options configure Run.
type Options struct {
	Cfg       *config.Config
	ImagesDir string // default DefaultImagesDir
	UnitDir   string // systemd user unit directory, default ~/.config/systemd/user
	DzoBinary string // what the refresh timer runs, default this executable
	DryRun    bool
	// ImagesOnly rebuilds the images (pulling the base image again), prunes
	// the old ones, and does nothing else. The weekly timer runs it.
	ImagesOnly bool
	Out        io.Writer
	Exec       Exec
	Geteuid    func() int // default os.Geteuid
}

type runner struct {
	Options
	problems int
}

func (r *runner) say(level, format string, a ...any) {
	if level == "error" {
		r.problems++
	}
	_, _ = fmt.Fprintf(r.Out, "%-5s %s\n", level, fmt.Sprintf(format, a...))
}

// Run performs the setup steps and reports each one. It returns an error if
// anything that must work does not; warnings are not errors.
func Run(ctx context.Context, o Options) error {
	if o.ImagesDir == "" {
		o.ImagesDir = DefaultImagesDir
	}
	if o.Exec == nil {
		o.Exec = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // fixed tools (podman, systemctl) with arguments built here
		}
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Geteuid == nil {
		o.Geteuid = os.Geteuid
	}
	r := &runner{Options: o}
	if r.Geteuid() == 0 {
		r.say("error", "run dzo setup as the service user, not as root (sudo -iu dayz dzo setup): rootless podman and the user's systemd need it")
		return errors.New("setup: must not run as root")
	}
	if o.ImagesOnly {
		r.images(ctx, true)
	} else {
		r.paths()
		r.images(ctx, false)
		r.steam()
		r.site(ctx)
		r.refreshTimer(ctx)
	}
	if r.problems > 0 {
		return fmt.Errorf("setup: %d problem(s), see above", r.problems)
	}
	return nil
}

func (r *runner) paths() {
	p := r.Cfg.Paths
	for _, d := range []struct {
		name, dir string
		mode      os.FileMode
	}{
		{"data", p.Data, 0o750}, {"instances", p.Instances, 0o750}, {"snapshots", p.Snapshots, 0o750}, {"logs", p.Logs, 0o750},
		{"cache", p.Cache, 0o750}, {"secrets", p.Secrets, 0o700}, {"db", p.DB, 0o750},
	} {
		switch fi, err := os.Stat(d.dir); {
		case err == nil && !fi.IsDir():
			r.say("error", "paths.%s %s is not a directory", d.name, d.dir)
		case err == nil:
			if err := writable(d.dir); err != nil {
				r.say("error", "paths.%s %s is not writable for this user: %v", d.name, d.dir, err)
				continue
			}
			r.say("ok", "paths.%s %s", d.name, d.dir)
		case r.DryRun:
			r.say("todo", "create paths.%s %s (%04o)", d.name, d.dir, d.mode)
		default:
			if err := os.MkdirAll(d.dir, d.mode); err != nil {
				r.say("error", "create paths.%s %s: %v", d.name, d.dir, err)
				continue
			}
			r.say("ok", "created paths.%s %s (%04o)", d.name, d.dir, d.mode)
		}
	}
	r.filesystems()
}

func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".dzo-setup-*")
	if err != nil {
		return err
	}
	_ = f.Close()
	return os.Remove(f.Name())
}

// existing is dir, or its nearest ancestor that exists, so a directory that
// setup has not created yet (dry run) can still be checked.
func existing(dir string) string {
	for {
		if _, err := os.Stat(dir); err == nil || filepath.Dir(dir) == dir {
			return dir
		}
		dir = filepath.Dir(dir)
	}
}

func device(path string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Dev), nil //nolint:unconvert // Dev is uint32 on some platforms
}

func (r *runner) filesystems() {
	p := r.Cfg.Paths
	inst, snap := existing(p.Instances), existing(p.Snapshots)
	di, err1 := device(inst)
	ds, err2 := device(snap)
	if err1 == nil && err2 == nil && di != ds {
		r.say("error", "paths.snapshots (%s) is not on the same filesystem as paths.instances (%s): a snapshot cannot cross filesystems", p.Snapshots, p.Instances)
	}
	var st syscall.Statfs_t
	if on, err := btrfs.IsBtrfs(inst); err == nil && !on {
		r.say("warn", "paths.instances %s is not on btrfs; the btrfs snapshot backups (docs: Backups) need it", p.Instances)
	} else if err == nil && !btrfs.UserSubvolRmAllowed(snap) {
		r.say("info", "paths.snapshots %s is mounted without user_subvol_rm_allowed: deleting old snapshots works but takes time proportional to their size; the mount option makes it instant", p.Snapshots)
	}
	if err := syscall.Statfs(existing(p.Data), &st); err == nil {
		if free := st.Bavail * uint64(st.Bsize) >> 30; free < minFreeGiB { //nolint:gosec // block size is positive
			r.say("warn", "only %d GiB free below paths.data %s; a server build and a few mods need about %d GiB", free, p.Data, minFreeGiB)
		}
	}
}

func (r *runner) images(ctx context.Context, rebuild bool) {
	for _, img := range Images {
		dir := filepath.Join(r.ImagesDir, img.Dir)
		file := filepath.Join(dir, "Containerfile")
		if _, err := os.Stat(file); err != nil {
			r.say("error", "%s is missing: the package is incomplete", file)
			continue
		}
		_, err := r.Exec(ctx, "podman", "image", "exists", img.Tag)
		if errors.Is(err, exec.ErrNotFound) {
			r.say("error", "podman is not installed")
			return
		}
		switch {
		case err == nil && !rebuild:
			r.say("ok", "image %s exists", img.Tag)
		case r.DryRun:
			r.say("todo", "build image %s from %s", img.Tag, file)
		default:
			if out, err := r.Exec(ctx, "podman", "build", "--pull=newer", "-t", img.Tag, "-f", file, dir); err != nil {
				r.say("error", "build image %s: %v\n%s", img.Tag, err, strings.TrimSpace(string(out)))
				continue
			}
			r.say("ok", "built image %s", img.Tag)
		}
	}
	if rebuild && !r.DryRun {
		// the previous builds are untagged now
		if out, err := r.Exec(ctx, "podman", "image", "prune", "-f"); err != nil {
			r.say("warn", "podman image prune: %v\n%s", err, strings.TrimSpace(string(out)))
		}
	}
}

func (r *runner) steam() {
	acct := r.Cfg.Steam.Account
	if acct == "" {
		r.say("warn", "steam.account is not set in config.yaml; mod and server downloads need it")
		return
	}
	st, err := steam.LoadStatus(filepath.Join(r.Cfg.Paths.Secrets, "steam-status.json"))
	if err == nil && st.Account == acct && st.LastSuccessAt != nil && !st.AuthRequired {
		r.say("ok", "steam account %s is logged in", acct)
		return
	}
	r.say("todo", "log in to Steam (needs your password and Steam Guard code): dzo steam login --user %s", acct)
}

func (r *runner) site(ctx context.Context) {
	s := r.Cfg.Site
	if s.Remote == "" {
		r.say("warn", "site.remote is not set in config.yaml; there is no site config to read yet")
		return
	}
	branch := s.Branch
	if branch == "" {
		branch = "main"
	}
	repo := &site.Repo{Dir: r.Cfg.Paths.Site, RemoteURL: s.Remote, Branch: branch, DeployKeyPath: s.DeployKeyPath, AutoPush: s.AutoPush}
	switch {
	case repo.Cloned():
		r.say("ok", "site repo %s is checked out", repo.Dir)
	case r.DryRun:
		r.say("todo", "clone %s (%s) to %s", s.Remote, branch, repo.Dir)
	default:
		if err := repo.Clone(ctx); err != nil {
			r.say("error", "clone the site repo: %v", err)
			return
		}
		r.say("ok", "cloned the site repo to %s", repo.Dir)
	}
}

func (r *runner) refreshTimer(ctx context.Context) {
	dir := r.UnitDir
	if dir == "" {
		cfg, err := os.UserConfigDir()
		if err != nil {
			r.say("error", "cannot find the user's systemd directory: %v", err)
			return
		}
		dir = filepath.Join(cfg, "systemd", "user")
	}
	bin := r.DzoBinary
	if bin == "" {
		if bin, _ = os.Executable(); bin == "" {
			bin = "/usr/bin/dzo"
		}
	}
	t := instance.TimerUnit{
		Name: refreshTimer, Description: "Rebuild the dzo container images (security updates)",
		ExecStart: bin + " setup --images-only", OnCalendar: []string{"weekly"}, Persistent: true,
	}
	if r.DryRun {
		r.say("todo", "install %s.timer in %s and enable it", refreshTimer, dir)
		return
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		r.say("error", "create %s: %v", dir, err)
		return
	}
	if _, _, err := instance.WriteTimerUnit(dir, t); err != nil {
		r.say("error", "write %s units: %v", refreshTimer, err)
		return
	}
	for _, args := range [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", refreshTimer + ".timer"}} {
		if out, err := r.Exec(ctx, "systemctl", args...); err != nil {
			r.say("warn", "wrote the %s units to %s, but systemctl %s failed (no user session? run: loginctl enable-linger): %v %s", refreshTimer, dir, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
			return
		}
	}
	r.say("ok", "weekly image refresh timer %s.timer is enabled", refreshTimer)
}
