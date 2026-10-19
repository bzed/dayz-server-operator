// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package units

import (
	"context"
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
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

type env struct {
	t     *testing.T
	data  string
	opts  Options
	lc    instance.Lifecycle
	calls func() []string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	data := t.TempDir()
	cfg, err := config.Parse([]byte("paths:\n  data: " + data + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, data: data}
	e.opts = Options{Cfg: cfg, DzoBin: "/usr/bin/dzo", QuadletDir: filepath.Join(data, "q"), UnitDir: filepath.Join(data, "u")}

	logf := filepath.Join(data, "systemctl.log")
	sc := filepath.Join(data, "systemctl")
	write(t, sc, "#!/bin/sh\necho \"$@\" >> "+logf+"\n")
	if err := os.Chmod(sc, 0o755); err != nil { //nolint:gosec // fixture
		t.Fatal(err)
	}
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
	if err != nil {
		e.t.Fatal(err)
	}
	write(e.t, filepath.Join(dir, "DayZServer"), "bin")
	if err := store.SetCurrent(build); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) localMod(name, gen string) {
	e.t.Helper()
	store := product.LocalModStore(e.opts.Cfg.Paths.Cache, name)
	dir, err := store.NewGeneration(gen)
	if err != nil {
		e.t.Fatal(err)
	}
	write(e.t, filepath.Join(dir, "addons", "a.pbo"), gen)
	if err := store.SetCurrent(gen); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) tree() *site.Tree {
	e.t.Helper()
	tree, err := site.LoadTree(e.opts.Cfg.Paths.Site)
	if err != nil {
		e.t.Fatal(err)
	}
	return tree
}

func (e *env) sync(dry bool) Result {
	e.t.Helper()
	res, err := Sync(context.Background(), e.opts, e.tree(), e.lc, dry)
	if err != nil {
		e.t.Fatal(err)
	}
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
		if err != nil || !strings.HasPrefix(string(b), Header) {
			t.Errorf("%s: %v\n%s", f, err, b)
		}
	}
	if _, err := os.Stat(filepath.Join(e.data, "q", "dzo-y.container")); !os.IsNotExist(err) {
		t.Error("an instance without its server build has no unit")
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "instance y is not deployed") {
		t.Errorf("warnings = %v", res.Warnings)
	}
	read := func(f string) string { b, _ := os.ReadFile(filepath.Join(e.data, f)); return string(b) }
	if !strings.Contains(read("u/dzo-restart-x.service"), "ExecStart=/usr/bin/dzo restart x\n") || !strings.Contains(read("u/dzo-restart-x.timer"), "OnCalendar=*-*-* 03:30:00") {
		t.Errorf("restart units:\n%s\n%s", read("u/dzo-restart-x.service"), read("u/dzo-restart-x.timer"))
	}
	if bt := read("u/dzo-backup-x.timer"); !strings.Contains(bt, "OnCalendar=*-*-* 00:00:00\n") || !strings.Contains(bt, "OnCalendar=Sat *-*-* 12:30:00\n") {
		t.Errorf("backup timer:\n%s", bt)
	}
	if !strings.Contains(read("u/dzo-backup-x.service"), "backup create x --reason scheduled") || !strings.Contains(read("u/dzo-backup-x.timer"), "Persistent=true") {
		t.Errorf("backup units:\n%s", read("u/dzo-backup-x.service"))
	}
	if !strings.Contains(read("u/dzo-update-check.timer"), "OnCalendar=*:0/30") || !strings.Contains(read("u/dzo-update-check.timer"), "OnCalendar=*:30:00") || !strings.Contains(read("u/dzo-update-check.service"), "update check --apply") {
		t.Errorf("update check follows the smallest check_interval:\n%s", read("u/dzo-update-check.timer"))
	}
	if !strings.Contains(read("u/dzo-exporter.service"), "ExecReload=/bin/kill -HUP $MAINPID") || !strings.Contains(read("u/dzo-exporter.service"), "ExecStart=/usr/bin/dzo exporter") {
		t.Errorf("exporter unit:\n%s", read("u/dzo-exporter.service"))
	}
	if !strings.Contains(read("q/dzo-x.container"), "Image=localhost/dzo-runtime:test") {
		t.Errorf("container unit:\n%s", read("q/dzo-x.container"))
	}
	calls := strings.Join(e.calls(), "\n")
	if !strings.Contains(calls, "--user daemon-reload") || !strings.Contains(calls, "enable --now") || !strings.Contains(calls, "dzo-exporter.service") || !strings.Contains(calls, "dzo-restart-x.timer") {
		t.Errorf("systemctl calls:\n%s", calls)
	}
	if strings.Contains(calls, "dzo-x.service") {
		t.Errorf("a game server must never be started by a sync:\n%s", calls)
	}
}

func TestSyncIsIdempotentAndRemovesWhatNoLongerApplies(t *testing.T) {
	e := newEnv(t)
	first := e.sync(false)
	n := len(e.calls())
	second := e.sync(false)
	if len(second.Written) != 0 || second.Unchanged != len(first.Written) {
		t.Errorf("a second sync must change nothing: %+v", second)
	}
	if got := e.calls(); len(got) == n || strings.Contains(strings.Join(got[n:], "\n"), "daemon-reload") {
		t.Logf("calls after the second sync: %v", got[n:])
	}
	if strings.Contains(strings.Join(e.calls()[n:], "\n"), "daemon-reload") {
		t.Error("no daemon-reload when nothing changed")
	}

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
	if len(third.Removed) != 4 { // restart timer+service, backup timer+service
		t.Errorf("removed = %v", third.Removed)
	}
	if _, err := os.Stat(filepath.Join(e.data, "u", "dzo-restart-x.timer")); !os.IsNotExist(err) {
		t.Error("the restart timer must be gone")
	}
	if _, err := os.Stat(filepath.Join(e.data, "u", "dzo-image-refresh.timer")); err != nil {
		t.Error("a unit without dzo's header is not ours and must stay")
	}
	if !strings.Contains(strings.Join(e.calls(), "\n"), "disable --now dzo-backup-x.service dzo-backup-x.timer dzo-restart-x.service dzo-restart-x.timer") {
		t.Errorf("calls:\n%s", strings.Join(e.calls(), "\n"))
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	e := newEnv(t)
	res := e.sync(true)
	if len(res.Written) == 0 {
		t.Error("a dry run reports what it would write")
	}
	if _, err := os.Stat(e.opts.UnitDir); !os.IsNotExist(err) {
		t.Error("a dry run must not create anything")
	}
	if len(e.calls()) != 0 {
		t.Errorf("a dry run must not call systemctl: %v", e.calls())
	}
}

func TestPendingIsWhatChangedSinceTheUnitWasWritten(t *testing.T) {
	e := newEnv(t)
	inst := func() *resolve.Instance {
		i, err := resolve.Resolve(e.opts.Cfg, e.tree(), "x")
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	if p := Pending(inst()); p != nil {
		t.Errorf("an instance that was never deployed has nothing pending: %v", p)
	}
	e.sync(false)
	if p := Pending(inst()); len(p) != 0 {
		t.Errorf("just deployed: %v", p)
	}
	if g, ok := Applied(inst()); !ok || g.Build != "1" || g.Mods["@tools"] != "g1" {
		t.Errorf("applied = %+v %v", g, ok)
	}
	e.localMod("tools", "g2")
	if p := Pending(inst()); len(p) != 1 || p[0] != "@tools" {
		t.Errorf("a new mod generation: %v", p)
	}
	e.install("dayz-stable", "2")
	if p := Pending(inst()); strings.Join(p, ",") != "@tools,build" {
		t.Errorf("a new build too: %v", p)
	}
	e.sync(false)
	if p := Pending(inst()); len(p) != 2 {
		t.Errorf("a plain sync does not deploy pending updates: %v", p)
	}
	e.opts.Deploy = []string{"x"}
	e.sync(false)
	if p := Pending(inst()); strings.Join(p, ",") != "build" {
		t.Errorf("deploying brings the mods up to date, the build waits for an upgrade: %v", p)
	}
	if g, _ := Applied(inst()); g.Build != "1" {
		t.Errorf("the unit still runs build 1: %+v", g)
	}
	if i := inst(); i.Product.Build != "1" || i.Product.Latest != "2" {
		t.Errorf("pinned %s, latest %s", i.Product.Build, i.Product.Latest)
	}
	if err := resolve.PinBuild(inst().Paths.Runtime, "2"); err != nil {
		t.Fatal(err)
	}
	e.sync(false)
	if p := Pending(inst()); len(p) != 0 {
		t.Errorf("after the upgrade nothing is pending: %v", p)
	}
	if g, _ := Applied(inst()); g.Build != "2" {
		t.Errorf("the unit runs build 2 now: %+v", g)
	}
}

func TestCalendar(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "hourly", 30 * time.Minute: "*:0/30", 15 * time.Minute: "*:0/15", 7 * time.Minute: "hourly", time.Hour: "hourly",
		2 * time.Hour: "*-*-* 0/2:00", 6 * time.Hour: "*-*-* 0/6:00", 5 * time.Hour: "daily", 24 * time.Hour: "daily",
	} {
		if got := Calendar(d); got != want {
			t.Errorf("Calendar(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestConfigPathIsPassedOnWhenItIsNotTheDefault(t *testing.T) {
	e := newEnv(t)
	e.opts.ConfigPath = "/srv/dzo/config.yaml"
	files, _, err := Desired(e.opts, e.tree())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Name == "dzo-update-check.service" && !strings.Contains(f.Content, "update check --apply --config /srv/dzo/config.yaml") {
			t.Errorf("the units must run with the same config:\n%s", f.Content)
		}
	}
	e.opts.ConfigPath = DefaultConfig
	files, _, _ = Desired(e.opts, e.tree())
	for _, f := range files {
		if strings.Contains(f.Content, "--config") {
			t.Errorf("the default config path is not repeated:\n%s", f.Content)
		}
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
	if string(before) != string(after) {
		t.Errorf("a pending update must not reach the unit of a running server:\n%s", after)
	}
	warned := false
	for _, w := range res.Warnings {
		warned = warned || strings.Contains(w, "instance x: @tools pending")
	}
	if !warned {
		t.Errorf("warnings = %v", res.Warnings)
	}
	inst, _ := resolve.Resolve(e.opts.Cfg, e.tree(), "x")
	if p := Pending(inst); len(p) != 1 {
		t.Errorf("the update must stay pending: %v", p)
	}
	for _, f := range res.Removed {
		if strings.HasSuffix(f, "dzo-x.container") {
			t.Error("a held unit must not be removed as stale")
		}
	}

	e.opts.Deploy = []string{"x"}
	e.sync(false)
	after, _ = os.ReadFile(unit)
	if string(before) == string(after) || !strings.Contains(string(after), "g2") {
		t.Errorf("deploying writes the new generation:\n%s", after)
	}
	if p := Pending(inst); len(p) != 0 {
		t.Errorf("deployed: %v", p)
	}

	// "*" deploys everything
	e.localMod("tools", "g3")
	e.opts.Deploy = []string{"*"}
	e.sync(false)
	if after, _ = os.ReadFile(unit); !strings.Contains(string(after), "g3") {
		t.Error("* deploys every instance")
	}
}

func unitNames(t *testing.T, e *env) map[string]string {
	t.Helper()
	files, _, err := Desired(e.opts, e.tree())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files {
		out[f.Name] = f.Content
	}
	return out
}

func TestServeAndWebUnits(t *testing.T) {
	e := newEnv(t)
	got := unitNames(t, e)
	if _, ok := got["dzo-serve.service"]; ok {
		t.Error("without an instance that runs dzo-admin, dzo serve is not needed")
	}
	if _, ok := got["dzo-web.service"]; ok {
		t.Error("the web interface is optional: off until it is set up")
	}
	// set up: this host's API token exists
	write(t, filepath.Join(e.opts.Cfg.Paths.Secrets, "web.token"), "tok")
	if _, ok := unitNames(t, e)["dzo-web.service"]; !ok {
		t.Error("with web.token in place the web interface is set up")
	}
	off := false
	e.opts.Cfg.Web.Enabled = &off
	if _, ok := unitNames(t, e)["dzo-web.service"]; ok {
		t.Error("web.enabled: false switches the unit off")
	}
	e.opts.Cfg.Web.Enabled = nil
	if err := os.Remove(filepath.Join(e.opts.Cfg.Paths.Secrets, "web.token")); err != nil {
		t.Fatal(err)
	}
	// set up: backends of its own
	e.opts.Cfg.Web.Backends = []config.Backend{{Name: "main", URL: "http://127.0.0.1:8080", TokenFile: "/x"}}
	if _, ok := unitNames(t, e)["dzo-web.service"]; !ok {
		t.Error("with web.backends the web interface is set up")
	}
	e.opts.Cfg.Web.Backends = nil

	yes, no := true, false
	e.opts.Cfg.Serve.Enabled, e.opts.Cfg.Web.Enabled = &yes, &yes
	got = unitNames(t, e)
	if s := got["dzo-serve.service"]; !strings.Contains(s, "ExecStart=/usr/bin/dzo serve\n") || !strings.Contains(s, "ExecReload=/bin/kill -HUP $MAINPID") {
		t.Errorf("serve unit:\n%s", s)
	}
	if s := got["dzo-web.service"]; !strings.Contains(s, "ExecStart=/usr/bin/dzo web\n") || !strings.Contains(s, "WantedBy=default.target") {
		t.Errorf("web unit:\n%s", s)
	}

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
	if _, ok := unitNames(t, e)["dzo-serve.service"]; !ok {
		t.Error("an instance that runs dzo-admin needs dzo serve")
	}
	e.opts.Cfg.Serve.Enabled = &no
	if _, ok := unitNames(t, e)["dzo-serve.service"]; ok {
		t.Error("serve.enabled: false switches the unit off")
	}
}

func adminEnv(t *testing.T) *env {
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

func TestSyncCreatesTheAnnounceToken(t *testing.T) {
	e := adminEnv(t)
	tree, err := site.LoadTree(filepath.Join(e.data, "site"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.opts.Cfg.Paths.Secrets, "announce.token")
	if _, err := Sync(context.Background(), e.opts, tree, e.lc, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a dry run must not create the token")
	}
	if _, err := Sync(context.Background(), e.opts, tree, e.lc, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("announce.token: %v %v", info, err)
	}
	first, _ := os.ReadFile(path)
	store := api.NewTokenStore(filepath.Join(e.opts.Cfg.Paths.Secrets, "api-tokens.json"))
	p, ok := store.Lookup(strings.TrimSpace(string(first)))
	if !ok || p.Token.Role != api.RoleAnnouncer {
		t.Fatalf("token = %+v %v, want a valid announcer token", p, ok)
	}
	if _, err := Sync(context.Background(), e.opts, tree, e.lc, false); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(path); string(again) != string(first) {
		t.Error("a valid token must be kept")
	}
	toks, _ := store.List()
	if err := store.Revoke(toks[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), e.opts, tree, e.lc, false); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(path); string(again) == string(first) {
		t.Error("a revoked token must be replaced")
	}
}

func TestSyncWithoutTheAdminModMakesNoAnnounceToken(t *testing.T) {
	e := newEnv(t)
	tree, err := site.LoadTree(filepath.Join(e.data, "site"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), e.opts, tree, e.lc, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.opts.Cfg.Paths.Secrets, "announce.token")); err == nil {
		t.Error("no instance runs dzo-admin: no token needed")
	}
}
