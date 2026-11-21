// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package units

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/api"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/product"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t testing.TB, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

type env struct {
	t     testing.TB
	data  string
	opts  Options
	lc    instance.Lifecycle
	calls func() []string
}

func newEnv(t testing.TB) *env {
	t.Helper()
	data := t.TempDir()
	cfg, err := config.Parse([]byte("paths:\n  data: " + data + "\n"))
	require.NoError(t, err)
	e := &env{t: t, data: data}
	e.opts = Options{Cfg: cfg, DzoBin: "/usr/bin/dzo", QuadletDir: filepath.Join(data, "q"), UnitDir: filepath.Join(data, "u")}

	logf := filepath.Join(data, "systemctl.log")
	sc := filepath.Join(data, "systemctl")
	write(t, sc, "#!/bin/sh\necho \"$@\" >> "+logf+"\n")
	require.NoError(t, os.Chmod(sc, 0o755)) //nolint:gosec // fixture
	e.lc = instance.Lifecycle{Command: sc, UserMode: true}
	e.calls = func() []string {
		b, _ := os.ReadFile(logf)
		if len(b) == 0 {
			return nil
		}
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}

	write(t, filepath.Join(data, "site", "site.yaml"), "image: localhost/dzo-runtime:test\nlocal_mods:\n  tools: {path: "+filepath.Join(data, "toolsrc")+"}\n")
	write(t, filepath.Join(data, "site", "instances", "x", "instance.yaml"), `name: x
product: dayz-stable
map: m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2302, rcon: 2306, query: 27016}
network: host
mods:
  - {local: tools, server: true}
restarts: {schedule: ["04:00"], announce: {minutes: 30, lock: 7, kick: 3}}
backup: {schedule: ["00:00", "Sat 12:30"]}
updates: {check_interval: 30m, restart_minute: 0, restart_announce: {minutes: 30, lock: 7, kick: 3}}
`)
	write(t, filepath.Join(data, "site", "instances", "y", "instance.yaml"), `name: y
product: dayz-experimental
map: m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2402, rcon: 2406, query: 27026}
network: host
`)
	e.install("dayz-stable", "1")
	e.localMod("tools", "g1")
	return e
}

func (e *env) install(prod, build string) {
	e.t.Helper()
	store := product.ProductStore(e.opts.Cfg.Paths.Cache, prod)
	dir, err := store.NewGeneration(build)
	require.NoError(e.t, err)
	write(e.t, filepath.Join(dir, "DayZServer"), "bin")
	require.NoError(e.t, store.SetCurrent(build))
}

func (e *env) localMod(name, gen string) {
	e.t.Helper()
	store := product.LocalModStore(e.opts.Cfg.Paths.Cache, name)
	dir, err := store.NewGeneration(gen)
	require.NoError(e.t, err)
	write(e.t, filepath.Join(dir, "addons", "a.pbo"), gen)
	require.NoError(e.t, store.SetCurrent(gen))
}

func (e *env) tree() *site.Tree {
	e.t.Helper()
	tree, err := site.LoadTree(e.opts.Cfg.Paths.Site)
	require.NoError(e.t, err)
	return tree
}

// remote switches the environment to podman.mode remote with a binary that
// exists, as the appliance has one.
func (e *env) remote() {
	e.t.Helper()
	bin := filepath.Join(e.data, "dzo")
	write(e.t, bin, "dzo v1")
	require.NoError(e.t, os.Chmod(bin, 0o755)) //nolint:gosec // fixture
	e.opts.Cfg.Binary = bin
	e.opts.Cfg.Podman.Mode = config.PodmanRemote
}

func (e *env) sync(dry bool) Result {
	e.t.Helper()
	res, err := Sync(e.t.Context(), e.opts, e.tree(), e.lc, dry)
	require.NoError(e.t, err)
	return res
}

func TestSyncWritesTheUnitsAnInstallationNeeds(t *testing.T) {
	e := newEnv(t)
	res := e.sync(false)
	for _, f := range []string{
		"q/dzo-x.container",
		"u/dzo-restart-x.timer", "u/dzo-restart-x.service", "u/dzo-backup-x.timer", "u/dzo-backup-x.service",
		"u/dzo-update-check.timer", "u/dzo-update-check.service", "u/dzo-backup-prune.timer", "u/dzo-status.timer", "u/dzo-exporter.service",
	} {
		b, err := os.ReadFile(filepath.Join(e.data, f))
		require.NoError(t, err, f)
		assert.True(t, strings.HasPrefix(string(b), Header), "%s:\n%s", f, b)
	}
	assert.NoFileExists(t, filepath.Join(e.data, "q", "dzo-y.container"), "an instance without its server build has no unit")
	require.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "instance y is not deployed")
	read := func(f string) string { b, _ := os.ReadFile(filepath.Join(e.data, f)); return string(b) }
	assert.Contains(t, read("u/dzo-restart-x.service"), "ExecStart=/usr/bin/dzo restart x\n")
	assert.Contains(t, read("u/dzo-restart-x.timer"), "OnCalendar=*-*-* 03:30:00")
	bt := read("u/dzo-backup-x.timer")
	assert.Contains(t, bt, "OnCalendar=*-*-* 00:00:00\n")
	assert.Contains(t, bt, "OnCalendar=Sat *-*-* 12:30:00\n")
	assert.Contains(t, read("u/dzo-backup-x.service"), "backup create x --reason scheduled")
	assert.Contains(t, read("u/dzo-backup-x.timer"), "Persistent=true")
	assert.Contains(t, read("u/dzo-update-check.timer"), "OnCalendar=*:0/30", "update check follows the smallest check_interval")
	assert.Contains(t, read("u/dzo-update-check.timer"), "OnCalendar=*:30:00", "update check follows the smallest check_interval")
	assert.Contains(t, read("u/dzo-update-check.service"), "update check --apply")
	assert.Contains(t, read("u/dzo-exporter.service"), "ExecReload=/bin/kill -HUP $MAINPID")
	assert.Contains(t, read("u/dzo-exporter.service"), "ExecStart=/usr/bin/dzo exporter")
	assert.Contains(t, read("q/dzo-x.container"), "Image=localhost/dzo-runtime:test")
	calls := strings.Join(e.calls(), "\n")
	assert.Contains(t, calls, "--user daemon-reload")
	assert.Contains(t, calls, "enable --now")
	assert.Contains(t, calls, "dzo-exporter.service")
	assert.Contains(t, calls, "dzo-restart-x.timer")
	assert.NotContains(t, calls, "dzo-x.service", "a game server must never be started by a sync")
}

func TestSyncIsIdempotentAndRemovesWhatNoLongerApplies(t *testing.T) {
	e := newEnv(t)
	first := e.sync(false)
	n := len(e.calls())
	second := e.sync(false)
	assert.Empty(t, second.Written, "a second sync must change nothing")
	assert.Equal(t, len(first.Written), second.Unchanged, "a second sync must change nothing")
	if got := e.calls(); len(got) == n || strings.Contains(strings.Join(got[n:], "\n"), "daemon-reload") {
		t.Logf("calls after the second sync: %v", got[n:])
	}
	assert.NotContains(t, strings.Join(e.calls()[n:], "\n"), "daemon-reload", "no daemon-reload when nothing changed")

	// the schedule goes: its timer must go, with a disable first
	write(t, filepath.Join(e.data, "site", "instances", "x", "instance.yaml"), `name: x
product: dayz-stable
map: m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2302, rcon: 2306, query: 27016}
network: host
mods:
  - {local: tools, server: true}
`)
	// a unit that is not ours stays
	write(t, filepath.Join(e.data, "u", "dzo-image-refresh.timer"), "[Timer]\nOnCalendar=weekly\n")
	third := e.sync(false)
	assert.Len(t, third.Removed, 4, "restart timer+service, backup timer+service") // removed = %v
	assert.NoFileExists(t, filepath.Join(e.data, "u", "dzo-restart-x.timer"), "the restart timer must be gone")
	assert.FileExists(t, filepath.Join(e.data, "u", "dzo-image-refresh.timer"), "a unit without dzo's header is not ours and must stay")
	assert.Contains(t, strings.Join(e.calls(), "\n"), "disable --now dzo-backup-x.service dzo-backup-x.timer dzo-restart-x.service dzo-restart-x.timer")
}

func TestDryRunWritesNothing(t *testing.T) {
	e := newEnv(t)
	res := e.sync(true)
	assert.NotEmpty(t, res.Written, "a dry run reports what it would write")
	assert.NoDirExists(t, e.opts.UnitDir, "a dry run must not create anything")
	assert.Empty(t, e.calls(), "a dry run must not call systemctl")
}

func TestPendingIsWhatChangedSinceTheUnitWasWritten(t *testing.T) {
	e := newEnv(t)
	inst := func() *resolve.Instance {
		i, err := resolve.Resolve(e.opts.Cfg, e.tree(), "x")
		require.NoError(t, err)
		return i
	}
	assert.Nil(t, Pending(inst()), "an instance that was never deployed has nothing pending")
	e.sync(false)
	assert.Empty(t, Pending(inst()), "just deployed")
	g, ok := Applied(inst())
	require.True(t, ok)
	assert.Equal(t, "1", g.Build)
	assert.Equal(t, "g1", g.Mods["@tools"])
	e.localMod("tools", "g2")
	assert.Equal(t, []string{"@tools"}, Pending(inst()), "a new mod generation")
	e.install("dayz-stable", "2")
	assert.Equal(t, []string{"@tools", "build"}, Pending(inst()), "a new build too")
	e.sync(false)
	assert.Len(t, Pending(inst()), 2, "a plain sync does not deploy pending updates")
	e.opts.Deploy = []string{"x"}
	e.sync(false)
	assert.Equal(t, []string{"build"}, Pending(inst()), "deploying brings the mods up to date, the build waits for an upgrade")
	g, _ = Applied(inst())
	assert.Equal(t, "1", g.Build, "the unit still runs build 1")
	i := inst()
	assert.Equal(t, "1", i.Product.Build, "pinned")
	assert.Equal(t, "2", i.Product.Latest, "latest")
	require.NoError(t, resolve.PinBuild(inst().Paths.Runtime, "2"))
	e.sync(false)
	assert.Empty(t, Pending(inst()), "after the upgrade nothing is pending")
	g, _ = Applied(inst())
	assert.Equal(t, "2", g.Build, "the unit runs build 2 now")
}

func TestCalendar(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "hourly", 30 * time.Minute: "*:0/30", 15 * time.Minute: "*:0/15", 7 * time.Minute: "hourly", time.Hour: "hourly",
		2 * time.Hour: "*-*-* 0/2:00", 6 * time.Hour: "*-*-* 0/6:00", 5 * time.Hour: "daily", 24 * time.Hour: "daily",
	} {
		assert.Equal(t, want, Calendar(d), "Calendar(%s)", d)
	}
}

func TestConfigPathIsPassedOnWhenItIsNotTheDefault(t *testing.T) {
	e := newEnv(t)
	e.opts.ConfigPath = "/srv/dzo/config.yaml"
	files, _, err := Desired(e.opts, e.tree())
	require.NoError(t, err)
	for _, f := range files {
		if f.Name == "dzo-update-check.service" {
			assert.Contains(t, f.Content, "update check --apply --config /srv/dzo/config.yaml", "the units must run with the same config")
		}
	}
	e.opts.ConfigPath = DefaultConfig
	files, _, _ = Desired(e.opts, e.tree())
	for _, f := range files {
		assert.NotContains(t, f.Content, "--config", "the default config path is not repeated")
	}
}

func TestPendingUpdatesAreHeldBackUntilTheInstanceIsDeployed(t *testing.T) {
	e := newEnv(t)
	e.sync(false)
	unit := filepath.Join(e.data, "q", "dzo-x.container")
	before, _ := os.ReadFile(unit)

	e.localMod("tools", "g2") // an update lands in the cache
	res := e.sync(false)
	after, _ := os.ReadFile(unit)
	assert.Equal(t, string(before), string(after), "a pending update must not reach the unit of a running server")
	warned := false
	for _, w := range res.Warnings {
		warned = warned || strings.Contains(w, "instance x: @tools pending")
	}
	assert.True(t, warned, "warnings = %v", res.Warnings)
	inst, _ := resolve.Resolve(e.opts.Cfg, e.tree(), "x")
	assert.Len(t, Pending(inst), 1, "the update must stay pending")
	for _, f := range res.Removed {
		assert.False(t, strings.HasSuffix(f, "dzo-x.container"), "a held unit must not be removed as stale")
	}

	e.opts.Deploy = []string{"x"}
	e.sync(false)
	after, _ = os.ReadFile(unit)
	assert.NotEqual(t, string(before), string(after), "deploying writes the new generation")
	assert.Contains(t, string(after), "g2", "deploying writes the new generation")
	assert.Empty(t, Pending(inst), "deployed")

	// "*" deploys everything
	e.localMod("tools", "g3")
	e.opts.Deploy = []string{"*"}
	e.sync(false)
	after, _ = os.ReadFile(unit)
	assert.Contains(t, string(after), "g3", "* deploys every instance")
}

func TestDesiredRemoteWritesService(t *testing.T) {
	e := newEnv(t)
	e.remote()
	files, _, err := Desired(e.opts, e.tree())
	require.NoError(t, err)
	var service *File
	for i := range files {
		f := &files[i]
		assert.False(t, strings.HasSuffix(f.Name, ".container"), "remote mode writes no quadlet: %+v", *f)
		if f.Name == "dzo-x.service" {
			service = f
		}
	}
	require.NotNil(t, service, "want dzo-x.service")
	assert.Equal(t, Systemd, service.Dir, "the supervised unit is a plain systemd unit")
	assert.True(t, service.Install, "the instance unit must be enabled (and never started)")
	assert.Contains(t, service.Content, "Type=notify\n")
	assert.Contains(t, service.Content, "ExecStart=/usr/bin/dzo instance supervise x -- run --name dzo-x --replace --rm --network host ",
		"the unit carries the supervisor and the podman run arguments")

	e.sync(false)
	assert.NoFileExists(t, filepath.Join(e.opts.QuadletDir, "dzo-x.container"), "no quadlet in remote mode")
	assert.FileExists(t, filepath.Join(e.opts.UnitDir, "dzo-x.service"))
}

// assertNeverStops fails for every systemctl call that stops or disables unit.
func assertNeverStops(t *testing.T, calls []string, unit string) {
	t.Helper()
	for _, c := range calls {
		if !strings.Contains(c, unit) {
			continue
		}
		for _, verb := range []string{"disable", "stop", "start", "--now"} {
			// "start" also covers "restart".
			assert.NotContains(t, c, verb, "a sync must never stop or start %s: %q", unit, c)
		}
	}
}

func TestSyncModeSwitchKeepsRunningInstance(t *testing.T) {
	e := newEnv(t)
	e.sync(false) // local: a quadlet
	container := filepath.Join(e.opts.QuadletDir, "dzo-x.container")
	service := filepath.Join(e.opts.UnitDir, "dzo-x.service")
	assert.FileExists(t, container)

	// local -> remote: the quadlet goes, the supervised unit comes, nothing is stopped
	e.remote()
	res := e.sync(false)
	assert.Contains(t, res.Removed, container, "the quadlet must be replaced")
	assert.NoFileExists(t, container)
	assert.FileExists(t, service)
	calls := strings.Join(e.calls(), "\n")
	assert.Contains(t, calls, "enable dzo-x.service", "the supervised unit must be enabled")
	assertNeverStops(t, e.calls(), "dzo-x.service")

	// remote -> local: the supervised unit goes, the quadlet comes back, still nothing is stopped
	e.opts.Cfg.Podman.Mode = config.PodmanLocal
	res = e.sync(false)
	assert.Contains(t, res.Removed, service, "the supervised unit must be replaced")
	assert.NoFileExists(t, service)
	assert.FileExists(t, container)
	assertNeverStops(t, e.calls(), "dzo-x.service")
}

func TestSyncRemoteHoldsPendingUpdate(t *testing.T) {
	e := newEnv(t)
	e.remote()
	e.sync(false)
	unit := filepath.Join(e.opts.UnitDir, "dzo-x.service")
	before, err := os.ReadFile(unit)
	require.NoError(t, err)
	assert.Contains(t, string(before), "g1", "the deployed unit carries the generation it runs")
	inst := func() *resolve.Instance {
		i, err := resolve.Resolve(e.opts.Cfg, e.tree(), "x")
		require.NoError(t, err)
		return i
	}

	e.localMod("tools", "g2") // an update lands in the cache
	res := e.sync(false)
	after, err := os.ReadFile(unit)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a pending update must not change the unit")
	assert.Contains(t, string(after), "g1", "the held unit keeps the arguments of the generation it runs")
	assert.Len(t, Pending(inst()), 1, "the hold is looked up in the systemd dir: the update stays pending")
	warned := false
	for _, w := range res.Warnings {
		warned = warned || strings.Contains(w, "instance x: @tools pending")
	}
	assert.True(t, warned, "warnings = %v", res.Warnings)
	assert.NotContains(t, res.Removed, unit, "a held unit must not be removed as stale")

	e.opts.Deploy = []string{"x"}
	e.sync(false)
	after, err = os.ReadFile(unit)
	require.NoError(t, err)
	assert.Contains(t, string(after), "g2", "deploying writes the new generation into the unit")
	assert.Empty(t, Pending(inst()), "deploying records the new generations")
}

func TestSyncModeSwitchWithPendingUpdateWritesTheNewUnit(t *testing.T) {
	e := newEnv(t)
	e.sync(false) // local: the quadlet is what exists
	inst := func() *resolve.Instance {
		i, err := resolve.Resolve(e.opts.Cfg, e.tree(), "x")
		require.NoError(t, err)
		return i
	}
	e.localMod("tools", "g2")

	// Nothing to hold: the unit of the other mode is what is there, so the
	// switch must write (and record) the new one instead of waiting.
	e.remote()
	res := e.sync(false)
	assert.FileExists(t, filepath.Join(e.opts.UnitDir, "dzo-x.service"))
	assert.Empty(t, Pending(inst()), "a mode switch writes the new unit and records the generations")
	for _, w := range res.Warnings {
		assert.NotContains(t, w, "pending", "nothing is held across a mode switch, warnings = %v", res.Warnings)
	}
}

func unitNames(t testing.TB, e *env) map[string]string {
	t.Helper()
	files, _, err := Desired(e.opts, e.tree())
	require.NoError(t, err)
	out := map[string]string{}
	for _, f := range files {
		out[f.Name] = f.Content
	}
	return out
}

func TestServeAndWebUnits(t *testing.T) {
	e := newEnv(t)
	got := unitNames(t, e)
	assert.NotContains(t, got, "dzo-serve.service", "without an instance that runs dzo-admin, dzo serve is not needed")
	assert.NotContains(t, got, "dzo-web.service", "the web interface is optional: off until it is set up")
	// set up: this host's API token exists
	write(t, filepath.Join(e.opts.Cfg.Paths.Secrets, "web.token"), "tok")
	assert.Contains(t, unitNames(t, e), "dzo-web.service", "with web.token in place the web interface is set up")
	off := false
	e.opts.Cfg.Web.Enabled = &off
	assert.NotContains(t, unitNames(t, e), "dzo-web.service", "web.enabled: false switches the unit off")
	e.opts.Cfg.Web.Enabled = nil
	require.NoError(t, os.Remove(filepath.Join(e.opts.Cfg.Paths.Secrets, "web.token")))
	// set up: an API of its own
	e.opts.Cfg.Web.API.URL, e.opts.Cfg.Web.API.TokenFile = "https://serve.example", "/x"
	assert.Contains(t, unitNames(t, e), "dzo-web.service", "with web.api the web interface is set up")
	e.opts.Cfg.Web.API.URL, e.opts.Cfg.Web.API.TokenFile = "", ""

	yes, no := true, false
	e.opts.Cfg.Serve.Enabled, e.opts.Cfg.Web.Enabled = &yes, &yes
	got = unitNames(t, e)
	assert.Contains(t, got["dzo-serve.service"], "ExecStart=/usr/bin/dzo serve\n")
	assert.Contains(t, got["dzo-serve.service"], "ExecReload=/bin/kill -HUP $MAINPID")
	assert.Contains(t, got["dzo-web.service"], "ExecStart=/usr/bin/dzo web\n")
	assert.Contains(t, got["dzo-web.service"], "ExecReload=/bin/kill -HUP $MAINPID", "a reload re-reads the certificate files")
	assert.Contains(t, got["dzo-web.service"], "WantedBy=default.target")

	// an instance with the dzo-admin mod needs serve, unless it is switched off
	write(t, filepath.Join(e.data, "site", "site.yaml"), "image: localhost/dzo-runtime:test\nlocal_mods:\n  tools: {path: "+filepath.Join(e.data, "toolsrc")+"}\n  dzo-admin: {path: "+filepath.Join(e.data, "toolsrc")+"}\n")
	write(t, filepath.Join(e.data, "site", "instances", "y", "instance.yaml"), `name: y
product: dayz-experimental
map: m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2402, rcon: 2406, query: 27026}
network: host
mods:
  - {local: dzo-admin, server: true}
`)
	e.opts.Cfg.Serve.Enabled = nil
	assert.Contains(t, unitNames(t, e), "dzo-serve.service", "an instance that runs dzo-admin needs dzo serve")
	e.opts.Cfg.Serve.Enabled = &no
	assert.NotContains(t, unitNames(t, e), "dzo-serve.service", "serve.enabled: false switches the unit off")
}

func adminEnv(t testing.TB) *env {
	t.Helper()
	e := newEnv(t)
	write(t, filepath.Join(e.data, "site", "site.yaml"), "image: localhost/dzo-runtime:test\nlocal_mods:\n  tools: {path: "+filepath.Join(e.data, "toolsrc")+"}\n  dzo-admin: {path: "+filepath.Join(e.data, "toolsrc")+"}\n")
	write(t, filepath.Join(e.data, "site", "instances", "y", "instance.yaml"), `name: y
product: dayz-experimental
map: m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2402, rcon: 2406, query: 27026}
network: host
mods:
  - {local: dzo-admin, server: true}
`)
	return e
}

func TestSyncCopiesHealthBinary(t *testing.T) {
	e := newEnv(t)
	e.remote()
	dst := filepath.Join(e.data, "bin", "dzo")

	e.sync(true)
	assert.NoFileExists(t, dst, "a dry run must not copy the health binary")

	e.sync(false)
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "dzo v1", string(got))
	info, err := os.Stat(dst)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(), "the host's podman and the container must be able to read and run the copy")
	unit, err := os.ReadFile(filepath.Join(e.opts.UnitDir, "dzo-x.service"))
	require.NoError(t, err)
	assert.Contains(t, string(unit), dst+":/usr/local/bin/dzo:ro", "the unit mounts the copy, not the appliance's binary")

	// The same content is not written again: rewriting it would give the copy a
	// new inode for nothing, and a running container pins the one it has.
	e.sync(false)
	again, err := os.Stat(dst)
	require.NoError(t, err)
	assert.True(t, os.SameFile(info, again), "an unchanged binary must not be rewritten")
}

func TestSyncReplacesHealthBinaryAtomically(t *testing.T) {
	e := newEnv(t)
	e.remote()
	e.sync(false)
	dst := filepath.Join(e.data, "bin", "dzo")

	old, err := os.Open(dst)
	require.NoError(t, err)
	defer old.Close() //nolint:errcheck // read-only fixture

	write(t, e.opts.Cfg.Binary, "dzo v2")
	e.sync(false)

	// A bind mount pins the inode: what a running container already opened still
	// reads the binary it started with, exactly as with /usr/bin/dzo on a host.
	buf := make([]byte, len("dzo v1"))
	_, err = io.ReadFull(old, buf)
	require.NoError(t, err)
	assert.Equal(t, "dzo v1", string(buf), "the open handle keeps the old binary")
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "dzo v2", string(got), "the path has the new binary")
}

func TestSyncCreatesTheAnnounceToken(t *testing.T) {
	e := adminEnv(t)
	tree, err := site.LoadTree(filepath.Join(e.data, "site"))
	require.NoError(t, err)
	path := filepath.Join(e.opts.Cfg.Paths.Secrets, "announce.token")
	_, err = Sync(t.Context(), e.opts, tree, e.lc, true)
	require.NoError(t, err)
	require.NoFileExists(t, path, "a dry run must not create the token")
	_, err = Sync(t.Context(), e.opts, tree, e.lc, false)
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "announce.token")
	first, _ := os.ReadFile(path)
	store := api.NewTokenStore(filepath.Join(e.opts.Cfg.Paths.Secrets, "api-tokens.json"))
	p, ok := store.Lookup(strings.TrimSpace(string(first)))
	require.True(t, ok, "want a valid announcer token")
	require.Equal(t, api.RoleAnnouncer, p.Token.Role)
	_, err = Sync(t.Context(), e.opts, tree, e.lc, false)
	require.NoError(t, err)
	again, _ := os.ReadFile(path)
	assert.Equal(t, string(first), string(again), "a valid token must be kept")
	toks, _ := store.List()
	require.NoError(t, store.Revoke(toks[0].ID))
	_, err = Sync(t.Context(), e.opts, tree, e.lc, false)
	require.NoError(t, err)
	again, _ = os.ReadFile(path)
	assert.NotEqual(t, string(first), string(again), "a revoked token must be replaced")
}

func TestSyncWithoutTheAdminModMakesNoAnnounceToken(t *testing.T) {
	e := newEnv(t)
	tree, err := site.LoadTree(filepath.Join(e.data, "site"))
	require.NoError(t, err)
	_, err = Sync(t.Context(), e.opts, tree, e.lc, false)
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(e.opts.Cfg.Paths.Secrets, "announce.token"), "no instance runs dzo-admin: no token needed")
}
