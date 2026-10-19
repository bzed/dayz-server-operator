// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/steam"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fake struct {
	calls  []string
	exists map[string]bool // image tags that exist
	fail   map[string]bool // command prefixes that fail
}

func (f *fake) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	for p := range f.fail {
		if strings.HasPrefix(line, p) {
			return []byte("it broke"), errors.New("exit status 1")
		}
	}
	if strings.HasPrefix(line, "podman image exists ") {
		if f.exists[args[2]] {
			return nil, nil
		}
		return nil, errors.New("exit status 1")
	}
	return nil, nil
}

func (f *fake) ran(prefix string) int {
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

type env struct {
	cfg    *config.Config
	images string
	units  string
	f      *fake
	out    bytes.Buffer
}

func newEnv(t testing.TB) *env {
	t.Helper()
	root := t.TempDir()
	cfg, err := config.Parse([]byte("paths:\n  data: " + filepath.Join(root, "data") + "\n"))
	require.NoError(t, err)
	images := filepath.Join(root, "images")
	for _, i := range Images {
		require.NoError(t, os.MkdirAll(filepath.Join(images, i.Dir), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(images, i.Dir, "Containerfile"), []byte("FROM scratch\n"), 0o600))
	}
	return &env{cfg: cfg, images: images, units: filepath.Join(root, "units"), f: &fake{exists: map[string]bool{}, fail: map[string]bool{}}}
}

func (e *env) run(t testing.TB, mod func(*Options)) error {
	t.Helper()
	e.out.Reset()
	o := Options{Cfg: e.cfg, ImagesDir: e.images, UnitDir: e.units, DzoBinary: "/usr/bin/dzo", Out: &e.out, Exec: e.f.run, Geteuid: func() int { return 1000 }}
	if mod != nil {
		mod(&o)
	}
	return Run(t.Context(), o)
}

func TestDryRunChangesNothing(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, e.run(t, func(o *Options) { o.DryRun = true }), e.out.String())
	assert.NoDirExists(t, e.cfg.Paths.Data, "a dry run must not create directories")
	assert.NoDirExists(t, e.units, "a dry run must not write units")
	assert.Zero(t, e.f.ran("podman build"), "a dry run must not build anything: %v", e.f.calls)
	assert.Zero(t, e.f.ran("systemctl"), "a dry run must not enable anything: %v", e.f.calls)
	for _, want := range []string{"todo  create paths.instances", "todo  build image localhost/dzo-runtime:latest", "todo  build image localhost/dzo-steamcmd:latest", "todo  install dzo-image-refresh.timer", "warn  steam.account", "warn  site.remote"} {
		assert.Contains(t, e.out.String(), want)
	}
}

func TestSetupCreatesBuildsAndIsIdempotent(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, e.run(t, nil), e.out.String())
	fi, err := os.Stat(e.cfg.Paths.Secrets)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm(), "secrets must be 0700")
	fi, err = os.Stat(e.cfg.Paths.Instances)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o750), fi.Mode().Perm(), "instances must be 0750")
	assert.Equal(t, 1, e.f.ran("podman build --pull=newer -t localhost/dzo-runtime:latest"), "both images must be built once: %v", e.f.calls)
	assert.Equal(t, 1, e.f.ran("podman build --pull=newer -t localhost/dzo-steamcmd:latest"), "both images must be built once: %v", e.f.calls)
	timer, err := os.ReadFile(filepath.Join(e.units, "dzo-image-refresh.timer"))
	require.NoError(t, err)
	assert.Contains(t, string(timer), "OnCalendar=weekly")
	service, _ := os.ReadFile(filepath.Join(e.units, "dzo-image-refresh.service"))
	assert.Contains(t, string(service), "ExecStart=/usr/bin/dzo setup --images-only")
	assert.Equal(t, 1, e.f.ran("systemctl --user daemon-reload"), "the timer must be enabled: %v", e.f.calls)
	assert.Equal(t, 1, e.f.ran("systemctl --user enable --now dzo-image-refresh.timer"), "the timer must be enabled: %v", e.f.calls)

	// a second run: the images exist, nothing is built
	e.f.exists["localhost/dzo-runtime:latest"], e.f.exists["localhost/dzo-steamcmd:latest"] = true, true
	e.f.calls = nil
	require.NoError(t, e.run(t, nil))
	assert.Zero(t, e.f.ran("podman build"), "existing images must be left alone: %v", e.f.calls)
	assert.Contains(t, e.out.String(), "ok    image localhost/dzo-runtime:latest exists")
}

func TestImagesOnlyRebuildsAndPrunes(t *testing.T) {
	e := newEnv(t)
	e.f.exists["localhost/dzo-runtime:latest"], e.f.exists["localhost/dzo-steamcmd:latest"] = true, true
	require.NoError(t, e.run(t, func(o *Options) { o.ImagesOnly = true }), e.out.String())
	assert.Equal(t, 2, e.f.ran("podman build"), "both images must be rebuilt: %v", e.f.calls)
	assert.Equal(t, 1, e.f.ran("podman image prune -f"), "the old ones must be pruned: %v", e.f.calls)
	assert.NoDirExists(t, e.cfg.Paths.Data, "--images-only must not touch anything else")
}

func TestSetupRefusesRoot(t *testing.T) {
	e := newEnv(t)
	err := e.run(t, func(o *Options) { o.Geteuid = func() int { return 0 } })
	require.Error(t, err, "root must be refused")
	require.Contains(t, e.out.String(), "not as root")
	assert.Empty(t, e.f.calls, "nothing may run as root")
}

func TestSetupReportsProblems(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, os.Remove(filepath.Join(e.images, "runtime", "Containerfile")))
	e.f.fail["podman build --pull=newer -t localhost/dzo-steamcmd"] = true
	err := e.run(t, nil)
	require.ErrorContains(t, err, "2 problem", e.out.String())
	for _, want := range []string{"error runtime/Containerfile is missing", "error build image localhost/dzo-steamcmd:latest", "it broke"} {
		assert.Contains(t, strings.ReplaceAll(e.out.String(), e.images+"/", ""), want)
	}

	// no podman at all
	e = newEnv(t)
	e.f.fail = nil
	err = e.run(t, func(o *Options) {
		o.Exec = func(context.Context, string, ...string) ([]byte, error) { return nil, exec.ErrNotFound }
	})
	require.Error(t, err, "a missing podman must be an error")
	assert.Contains(t, e.out.String(), "podman is not installed")
}

func TestSetupWarnsWhenSystemdIsUnavailable(t *testing.T) {
	e := newEnv(t)
	e.f.fail["systemctl"] = true
	require.NoError(t, e.run(t, nil), "a missing user session is a warning, not an error")
	assert.Contains(t, e.out.String(), "warn  wrote the dzo-image-refresh units")
}

func TestSetupSteamHandOff(t *testing.T) {
	e := newEnv(t)
	e.cfg.Steam.Account = "bob"
	require.NoError(t, e.run(t, func(o *Options) { o.DryRun = true }), "not logged in must hand off")
	require.Contains(t, e.out.String(), "todo  log in to Steam")
	require.Contains(t, e.out.String(), "dzo steam login --user bob")
	now := time.Now()
	require.NoError(t, os.MkdirAll(e.cfg.Paths.Secrets, 0o700))
	require.NoError(t, (steam.Status{Account: "bob", LastSuccessAt: &now}).Save(filepath.Join(e.cfg.Paths.Secrets, "steam-status.json")))
	require.NoError(t, e.run(t, func(o *Options) { o.DryRun = true }), "a logged-in account is fine")
	require.Contains(t, e.out.String(), "ok    steam account bob is logged in")
}

func TestSetupClonesTheSiteRepo(t *testing.T) {
	e := newEnv(t)
	origin := filepath.Join(t.TempDir(), "origin")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", origin},
		{"-C", origin, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "--allow-empty", "-m", "x"},
	} {
		out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput() //nolint:gosec // test
		require.NoError(t, err, "git %v: %s", args, out)
	}
	e.cfg.Site.Remote = origin
	require.NoError(t, e.run(t, func(o *Options) { o.DryRun = true }), "dry run")
	require.Contains(t, e.out.String(), "todo  clone")
	require.NoError(t, e.run(t, nil), "clone")
	require.Contains(t, e.out.String(), "ok    cloned the site repo")
	require.NoError(t, e.run(t, nil), "second run")
	require.Contains(t, e.out.String(), "ok    site repo")
	e.cfg.Site.Remote = filepath.Join(t.TempDir(), "nothing")
	e.cfg.Paths.Site = filepath.Join(t.TempDir(), "other")
	require.Error(t, e.run(t, nil), "a failing clone must be an error")
	require.Contains(t, e.out.String(), "error clone the site repo")
}

func TestPathsProblems(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(e.cfg.Paths.Cache), 0o750))
	require.NoError(t, os.WriteFile(e.cfg.Paths.Cache, []byte("x"), 0o600))
	require.Error(t, e.run(t, func(o *Options) { o.DryRun = true }), "a file where a directory belongs must be an error")
	require.Contains(t, e.out.String(), "paths.cache")
	require.Contains(t, e.out.String(), "is not a directory")
	if os.Geteuid() != 0 {
		e := newEnv(t)
		require.NoError(t, os.MkdirAll(e.cfg.Paths.Data, 0o500))
		require.Error(t, e.run(t, nil), "a read-only data dir must be an error")
		assert.True(t, strings.Contains(e.out.String(), "not writable") || strings.Contains(e.out.String(), "create paths"), e.out.String())
	}
}

// The refresh timer runs without the options of the setup run that wrote it, so a non-default
// config or images directory has to be in its command line.
func TestRefreshCommand(t *testing.T) {
	cases := []struct {
		name string
		r    runner
		want string
	}{
		{"defaults", runner{Options: Options{ImagesDir: DefaultImagesDir}}, "/usr/bin/dzo setup --images-only"},
		{"own images dir", runner{Options: Options{ImagesDir: "/var/lib/dzo/share/dzo/images"}}, "/usr/bin/dzo setup --images-only --images-dir /var/lib/dzo/share/dzo/images"},
		{"config and quoting", runner{Options: Options{ImagesDir: DefaultImagesDir, ConfigPath: "/home/a b/config.yaml"}}, `/usr/bin/dzo setup --images-only --config "/home/a b/config.yaml"`},
		{"percent", runner{Options: Options{ImagesDir: DefaultImagesDir, ConfigPath: "/x/100%.yaml"}}, `/usr/bin/dzo setup --images-only --config "/x/100%%.yaml"`},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, c.r.refreshCommand("/usr/bin/dzo"), c.name)
	}
}

func TestSetupChecksTheUserCanReadItsJournal(t *testing.T) {
	for _, c := range []struct {
		name   string
		uid    int
		groups []string
		want   string
	}{
		{"regular user", 1000, nil, "regular user"},
		{"system user in no journal group", 998, []string{"dayz"}, "keeps no journal for it"},
		{"system user in systemd-journal", 998, []string{"dayz", "systemd-journal"}, "member of systemd-journal"},
		{"system user in adm", 998, []string{"adm"}, "member of adm"},
	} {
		e := newEnv(t)
		require.NoError(t, e.run(t, func(o *Options) {
			o.Geteuid = func() int { return c.uid }
			o.Groups = func() []string { return c.groups }
		}), "%s: %s", c.name, e.out.String())
		assert.Contains(t, e.out.String(), c.want, c.name)
	}
}
