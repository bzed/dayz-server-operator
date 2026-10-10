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

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	cfg, err := config.Parse([]byte("paths:\n  data: " + filepath.Join(root, "data") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	images := filepath.Join(root, "images")
	for _, i := range Images {
		if err := os.MkdirAll(filepath.Join(images, i.Dir), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(images, i.Dir, "Containerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &env{cfg: cfg, images: images, units: filepath.Join(root, "units"), f: &fake{exists: map[string]bool{}, fail: map[string]bool{}}}
}

func (e *env) run(t *testing.T, mod func(*Options)) error {
	t.Helper()
	e.out.Reset()
	o := Options{Cfg: e.cfg, ImagesDir: e.images, UnitDir: e.units, DzoBinary: "/usr/bin/dzo", Out: &e.out, Exec: e.f.run, Geteuid: func() int { return 1000 }}
	if mod != nil {
		mod(&o)
	}
	return Run(context.Background(), o)
}

func TestDryRunChangesNothing(t *testing.T) {
	e := newEnv(t)
	if err := e.run(t, func(o *Options) { o.DryRun = true }); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if _, err := os.Stat(e.cfg.Paths.Data); !os.IsNotExist(err) {
		t.Error("a dry run must not create directories")
	}
	if _, err := os.Stat(e.units); !os.IsNotExist(err) {
		t.Error("a dry run must not write units")
	}
	if e.f.ran("podman build") != 0 || e.f.ran("systemctl") != 0 {
		t.Errorf("a dry run must not build or enable anything: %v", e.f.calls)
	}
	for _, want := range []string{"todo  create paths.instances", "todo  build image localhost/dzo-runtime:latest", "todo  build image localhost/dzo-steamcmd:latest", "todo  install dzo-image-refresh.timer", "warn  steam.account", "warn  site.remote"} {
		if !strings.Contains(e.out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, e.out.String())
		}
	}
}

func TestSetupCreatesBuildsAndIsIdempotent(t *testing.T) {
	e := newEnv(t)
	if err := e.run(t, nil); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	fi, err := os.Stat(e.cfg.Paths.Secrets)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("secrets must be 0700: %v %v", fi, err)
	}
	if fi, err = os.Stat(e.cfg.Paths.Instances); err != nil || fi.Mode().Perm() != 0o750 {
		t.Errorf("instances must be 0750: %v %v", fi, err)
	}
	if e.f.ran("podman build --pull=newer -t localhost/dzo-runtime:latest") != 1 || e.f.ran("podman build --pull=newer -t localhost/dzo-steamcmd:latest") != 1 {
		t.Errorf("both images must be built once: %v", e.f.calls)
	}
	timer, err := os.ReadFile(filepath.Join(e.units, "dzo-image-refresh.timer"))
	if err != nil || !strings.Contains(string(timer), "OnCalendar=weekly") {
		t.Errorf("timer: %v\n%s", err, timer)
	}
	service, _ := os.ReadFile(filepath.Join(e.units, "dzo-image-refresh.service"))
	if !strings.Contains(string(service), "ExecStart=/usr/bin/dzo setup --images-only") {
		t.Errorf("service:\n%s", service)
	}
	if e.f.ran("systemctl --user daemon-reload") != 1 || e.f.ran("systemctl --user enable --now dzo-image-refresh.timer") != 1 {
		t.Errorf("the timer must be enabled: %v", e.f.calls)
	}

	// a second run: the images exist, nothing is built
	e.f.exists["localhost/dzo-runtime:latest"], e.f.exists["localhost/dzo-steamcmd:latest"] = true, true
	e.f.calls = nil
	if err := e.run(t, nil); err != nil {
		t.Fatal(err)
	}
	if e.f.ran("podman build") != 0 || !strings.Contains(e.out.String(), "ok    image localhost/dzo-runtime:latest exists") {
		t.Errorf("existing images must be left alone: %v\n%s", e.f.calls, e.out.String())
	}
}

func TestImagesOnlyRebuildsAndPrunes(t *testing.T) {
	e := newEnv(t)
	e.f.exists["localhost/dzo-runtime:latest"], e.f.exists["localhost/dzo-steamcmd:latest"] = true, true
	if err := e.run(t, func(o *Options) { o.ImagesOnly = true }); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if e.f.ran("podman build") != 2 || e.f.ran("podman image prune -f") != 1 {
		t.Errorf("both images must be rebuilt and the old ones pruned: %v", e.f.calls)
	}
	if _, err := os.Stat(e.cfg.Paths.Data); !os.IsNotExist(err) {
		t.Error("--images-only must not touch anything else")
	}
}

func TestSetupRefusesRoot(t *testing.T) {
	e := newEnv(t)
	if err := e.run(t, func(o *Options) { o.Geteuid = func() int { return 0 } }); err == nil || !strings.Contains(e.out.String(), "not as root") {
		t.Fatalf("root must be refused: %v\n%s", err, e.out.String())
	}
	if len(e.f.calls) != 0 {
		t.Errorf("nothing may run as root: %v", e.f.calls)
	}
}

func TestSetupReportsProblems(t *testing.T) {
	e := newEnv(t)
	if err := os.Remove(filepath.Join(e.images, "runtime", "Containerfile")); err != nil {
		t.Fatal(err)
	}
	e.f.fail["podman build --pull=newer -t localhost/dzo-steamcmd"] = true
	err := e.run(t, nil)
	if err == nil || !strings.Contains(err.Error(), "2 problem") {
		t.Fatalf("want two problems, got %v\n%s", err, e.out.String())
	}
	for _, want := range []string{"error runtime/Containerfile is missing", "error build image localhost/dzo-steamcmd:latest", "it broke"} {
		if !strings.Contains(strings.ReplaceAll(e.out.String(), e.images+"/", ""), want) {
			t.Errorf("missing %q in:\n%s", want, e.out.String())
		}
	}

	// no podman at all
	e = newEnv(t)
	e.f.fail = nil
	err = e.run(t, func(o *Options) {
		o.Exec = func(context.Context, string, ...string) ([]byte, error) { return nil, exec.ErrNotFound }
	})
	if err == nil || !strings.Contains(e.out.String(), "podman is not installed") {
		t.Errorf("a missing podman must be an error: %v\n%s", err, e.out.String())
	}
}

func TestSetupWarnsWhenSystemdIsUnavailable(t *testing.T) {
	e := newEnv(t)
	e.f.fail["systemctl"] = true
	if err := e.run(t, nil); err != nil {
		t.Fatalf("a missing user session is a warning, not an error: %v", err)
	}
	if !strings.Contains(e.out.String(), "warn  wrote the dzo-image-refresh units") {
		t.Errorf("output:\n%s", e.out.String())
	}
}

func TestSetupSteamHandOff(t *testing.T) {
	e := newEnv(t)
	e.cfg.Steam.Account = "bob"
	if err := e.run(t, func(o *Options) { o.DryRun = true }); err != nil || !strings.Contains(e.out.String(), "todo  log in to Steam") || !strings.Contains(e.out.String(), "dzo steam login --user bob") {
		t.Fatalf("not logged in must hand off: %v\n%s", err, e.out.String())
	}
	now := time.Now()
	if err := os.MkdirAll(e.cfg.Paths.Secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (steam.Status{Account: "bob", LastSuccessAt: &now}).Save(filepath.Join(e.cfg.Paths.Secrets, "steam-status.json")); err != nil {
		t.Fatal(err)
	}
	if err := e.run(t, func(o *Options) { o.DryRun = true }); err != nil || !strings.Contains(e.out.String(), "ok    steam account bob is logged in") {
		t.Fatalf("a logged-in account is fine: %v\n%s", err, e.out.String())
	}
}

func TestSetupClonesTheSiteRepo(t *testing.T) {
	e := newEnv(t)
	origin := filepath.Join(t.TempDir(), "origin")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", origin},
		{"-C", origin, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "--allow-empty", "-m", "x"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil { //nolint:gosec // test
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	e.cfg.Site.Remote = origin
	if err := e.run(t, func(o *Options) { o.DryRun = true }); err != nil || !strings.Contains(e.out.String(), "todo  clone") {
		t.Fatalf("dry run: %v\n%s", err, e.out.String())
	}
	if err := e.run(t, nil); err != nil || !strings.Contains(e.out.String(), "ok    cloned the site repo") {
		t.Fatalf("clone: %v\n%s", err, e.out.String())
	}
	if err := e.run(t, nil); err != nil || !strings.Contains(e.out.String(), "ok    site repo") {
		t.Fatalf("second run: %v\n%s", err, e.out.String())
	}
	e.cfg.Site.Remote = filepath.Join(t.TempDir(), "nothing")
	e.cfg.Paths.Site = filepath.Join(t.TempDir(), "other")
	if err := e.run(t, nil); err == nil || !strings.Contains(e.out.String(), "error clone the site repo") {
		t.Fatalf("a failing clone must be an error: %v\n%s", err, e.out.String())
	}
}

func TestPathsProblems(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(filepath.Dir(e.cfg.Paths.Cache), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.cfg.Paths.Cache, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.run(t, func(o *Options) { o.DryRun = true }); err == nil || !strings.Contains(e.out.String(), "paths.cache") || !strings.Contains(e.out.String(), "is not a directory") {
		t.Fatalf("a file where a directory belongs must be an error: %v\n%s", err, e.out.String())
	}
	if e := newEnv(t); true {
		if os.Geteuid() != 0 {
			if err := os.MkdirAll(e.cfg.Paths.Data, 0o500); err != nil {
				t.Fatal(err)
			}
			if err := e.run(t, nil); err == nil || !strings.Contains(e.out.String(), "not writable") && !strings.Contains(e.out.String(), "create paths") {
				t.Fatalf("a read-only data dir must be an error: %v\n%s", err, e.out.String())
			}
		}
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
		if got := c.r.refreshCommand("/usr/bin/dzo"); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
