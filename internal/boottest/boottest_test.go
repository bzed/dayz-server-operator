// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package boottest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/woozymasta/pbo"

	"github.com/bzed/dayz-server-operator/internal/admin"
)

// The tests boot a fake DayZServer: this test binary, started through a shell
// script called DayZServer. FAKE (comma separated) picks what it does.
func TestMain(m *testing.M) {
	if os.Getenv("DZO_FAKE_SERVER") == "1" {
		fakeServer()
		return
	}
	os.Exit(m.Run())
}

func fakeServer() {
	fake := map[string]bool{}
	for _, f := range strings.Split(os.Getenv("FAKE"), ",") {
		fake[f] = true
	}
	var profiles string
	for _, a := range os.Args[1:] {
		if v, ok := strings.CutPrefix(a, "-profiles="); ok {
			profiles = v
		}
	}
	cfg, _ := os.ReadFile("serverDZ.cfg")
	query := 0
	if m := regexp.MustCompile(`steamQueryPort\s*=\s*(\d+)`).FindSubmatch(cfg); m != nil {
		query, _ = strconv.Atoi(string(m[1]))
	}
	num := func(env string, def int) int {
		if v, err := strconv.Atoi(os.Getenv(env)); err == nil {
			return v
		}
		return def
	}
	if fake["crash"] {
		_ = os.WriteFile(filepath.Join(profiles, "DayZServer_x.RPT"), []byte("12:00:00 starting\n12:00:01 Cannot open mission\n"), 0o600)
		os.Exit(3)
	}
	script := fmt.Sprintf("SCRIPT       : Module: Game; loaded %dx files; 1379x classes\nSCRIPT       : Module: World; loaded %dx files\nSCRIPT       : Module: Mission; loaded %dx files\n",
		num("FAKE_GAME", 416), num("FAKE_WORLD", 2123), num("FAKE_MISSION", 209))
	if !fake["nomission"] {
		script += "SCRIPT       : Module: $CurrentDir:mpmissions/dayzOffline.x/init.c; loaded 1x files\n"
	}
	if fake["scripterr"] {
		script += "SCRIPT    (E): Can't find variable 'x'\nscripts/4_world/m.c(12): error\n"
	}
	_ = os.WriteFile(filepath.Join(profiles, "script_x.log"), []byte(script), 0o600)
	errlog := "-----\nLog error.log started at 02.10. 18:54:00\n\nENTITY    (W): Unknown object class 'pond' (dz\\water\\pond.p3d)\nENTITY       : Load entity type 'Land_X'\nANIMATION (E): Can't load @mod/Anims/cfg/skeletons.anim.xml\n"
	if fake["newerr"] {
		errlog += "XML (E): types.xml line 5: unexpected end\n"
	}
	rpt := "12:00:00 Version 1.29\n12:00:01 Mission read\n12:00:02 MOD LOADED marker\n12:00:03 !!! [CE][VehicleRespawner] (PRIBoat) :: Init: \"VehicleBoat\" - Failed to spawn the requested amount (17 < 22) within 66 attempts.\n"
	if fake["storagedirs"] {
		rpt += "12:00:03 [StorageDirs] :: Selected storage directory: /x/storage_2/\n"
	}
	if fake["ceerr"] {
		rpt += "12:00:04 !!! [ERROR][XML] :: load [db/types.xml] failed\n12:00:04 !!! [CE][offlineDB] :: Failed to read types file 'db/types.xml'.\n"
	}
	_ = os.WriteFile(filepath.Join(profiles, "DayZServer_x.RPT"), []byte(rpt), 0o600)
	_ = os.WriteFile(filepath.Join(profiles, "error.log"), []byte(errlog), 0o600)
	if fake["mdmp"] {
		_ = os.WriteFile(filepath.Join(profiles, "x.mdmp"), []byte("dump"), 0o600)
	}
	if b, err := os.ReadFile(filepath.Join(profiles, "dzo", "admin", "config.json")); err == nil && !fake["noadmin"] {
		var c admin.ModConfig
		if json.Unmarshal(b, &c) == nil {
			body, _ := json.Marshal(admin.SyncRequest{Token: c.Token, Proto: admin.ProtocolVersion, ModVersion: "t", World: "w", Hello: true})
			resp, err := http.Post(strings.TrimRight(c.Endpoint, "/")+"/mod/v1/sync", "application/json", bytes.NewReader(body))
			if err == nil {
				_ = resp.Body.Close()
			}
		}
	}
	if fake["hang"] {
		signal.Ignore(syscall.SIGTERM)
	}
	if !fake["silent"] {
		conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: query})
		if err == nil {
			go func() {
				buf := make([]byte, 1024)
				for {
					_, remote, err := conn.ReadFromUDP(buf)
					if err != nil {
						return
					}
					body := append([]byte{0xFF, 0xFF, 0xFF, 0xFF, 'I', 17, 0, 0, 0, 0, 0, 0}, make([]byte, 8)...)
					_, _ = conn.WriteToUDP(body, remote)
				}
			}()
		}
	}
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	if fake["hang"] {
		select {} // ignores SIGTERM: only SIGKILL ends it
	}
	<-term
}

type env struct {
	t       testing.TB
	root    string
	server  string
	mission string
}

func write(t testing.TB, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

func newEnv(t testing.TB) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{t: t, root: root, server: filepath.Join(root, "server"), mission: filepath.Join(root, "staged")}
	exe, err := os.Executable()
	require.NoError(t, err)
	write(t, filepath.Join(e.server, "DayZServer"), "#!/bin/sh\nexec env DZO_FAKE_SERVER=1 '"+exe+"' \"$@\"\n")
	require.NoError(t, os.Chmod(filepath.Join(e.server, "DayZServer"), 0o755)) //nolint:gosec // test fixture
	write(t, filepath.Join(e.server, "addons", "a.pbo"), "pbo")
	write(t, filepath.Join(e.server, "battleye", "BEServer_x64.so"), "be")
	write(t, filepath.Join(e.server, "keys", "dayz.bikey"), "k")
	write(t, filepath.Join(e.server, "mpmissions", "dayzOffline.x", "init.c"), "vanilla")
	write(t, filepath.Join(e.server, "core.123"), "dump")
	write(t, filepath.Join(e.server, "@Stray", "x"), "x")
	write(t, filepath.Join(e.server, "serverDZ.cfg"), "hostname = \"orig\";\n")
	write(t, filepath.Join(e.mission, "init.c"), "staged")
	write(t, filepath.Join(e.mission, "db", "types.xml"), "types")
	return e
}

func (e *env) config(fake string) Config {
	e.t.Setenv("FAKE", fake)
	return Config{
		ServerDir: e.server, TreeDir: filepath.Join(e.root, "tree"), Template: "dayzOffline.x", MissionDir: e.mission,
		ServerCfg: []byte("hostname = \"My Server\";\npassword = \"secret\";\ntemplate = \"wrong\";\n"),
		Keys:      []string{filepath.Join(e.server, "keys", "dayz.bikey")},
		Timeout:   10 * time.Second, Settle: 50 * time.Millisecond,
	}
}

func status(cs []Check, name string) (Status, string) {
	for _, c := range cs {
		if c.Name == name {
			return c.Status, c.Detail
		}
	}
	return "", ""
}

func mustRun(t testing.TB, c Config) *Result {
	t.Helper()
	r, err := Run(t.Context(), c)
	require.NoError(t, err)
	return r
}

func TestBootPassesAndBuildsTheTree(t *testing.T) {
	e := newEnv(t)
	c := e.config("")
	c.Mods = []Mod{{Name: "@CF", Dir: filepath.Join(e.root, "cf")}, {Name: "@dzo-admin", Dir: filepath.Join(e.root, "adm"), Server: true}}
	write(t, filepath.Join(e.root, "cf", "addons", "x"), "x")
	write(t, filepath.Join(e.root, "adm", "addons", "x"), "x")
	r := mustRun(t, c)
	require.False(t, Failed(r.Checks), "a good boot must pass:\n%s", Text(r.Checks))
	tree := c.TreeDir
	for _, link := range []string{"DayZServer", "addons", "@CF", "@dzo-admin"} {
		fi, err := os.Lstat(filepath.Join(tree, link))
		if assert.NoError(t, err, link) {
			assert.NotZero(t, fi.Mode()&os.ModeSymlink, "%s must be a symlink", link)
		}
	}
	for _, absent := range []string{"core.123", "@Stray"} {
		assert.NoFileExists(t, filepath.Join(tree, absent), "%s must not be in the tree", absent)
	}
	fi, err := os.Lstat(filepath.Join(tree, "battleye", "BEServer_x64.so"))
	if assert.NoError(t, err, "battleye must be a real dir of links to the BattlEye binaries") {
		assert.NotZero(t, fi.Mode()&os.ModeSymlink, "battleye must be a real dir of links to the BattlEye binaries")
	}
	b, _ := os.ReadFile(filepath.Join(tree, "mpmissions", "dayzOffline.x", "init.c"))
	assert.Equal(t, "staged", string(b), "the staged mission must be copied")
	fi, err = os.Lstat(filepath.Join(tree, "mpmissions", "dayzOffline.x", "init.c"))
	if assert.NoError(t, err, "the mission must be a real copy, not links") {
		assert.Zero(t, fi.Mode()&os.ModeSymlink, "the mission must be a real copy, not links")
	}
	cfg, _ := os.ReadFile(filepath.Join(tree, "serverDZ.cfg"))
	for _, want := range []string{`hostname = "[dzo boot test] My Server";`, `template = "dayzOffline.x";`, "steamQueryPort = " + strconv.Itoa(r.Ports.Query) + ";"} {
		assert.Contains(t, string(cfg), want, "serverDZ.cfg")
	}
	assert.NotContains(t, string(cfg), `"secret"`, "the password must be replaced so that nobody can join")
	b, _ = os.ReadFile(filepath.Join(tree, "keys", "dayz.bikey"))
	assert.Equal(t, "k", string(b), "keys")
	be, _ := os.ReadFile(filepath.Join(tree, "profiles", "battleye", "beserver_x64.cfg"))
	assert.Contains(t, string(be), "RConPort "+strconv.Itoa(r.Ports.RCon), "BattlEye seed")
	assert.NotEqual(t, r.Ports.Game, r.Ports.Query, "ports must differ")
	assert.NotEqual(t, r.Ports.Query, r.Ports.RCon, "ports must differ")
}

func TestBootFailures(t *testing.T) {
	for _, tc := range []struct {
		name, fake, check string
		want              Status
		detail            string
	}{
		{"compile error", "scripterr", "scripts", Fail, "Can't find variable"},
		{"server exits before it answers", "crash", "ready", Fail, "exited"},
		{"server never answers", "silent", "ready", Fail, "did not answer"},
		{"crash dump", "mdmp", "crash", Fail, "x.mdmp"},
		{"mission never loads", "nomission", "mission", Fail, "never loaded"},
		{"central economy errors", "ceerr", "ce", Fail, "Failed to read types file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			c := e.config(tc.fake)
			c.Timeout = 3 * time.Second
			r := mustRun(t, c)
			s, d := status(r.Checks, tc.check)
			assert.Equal(t, tc.want, s, "%s = %s %q\n%s", tc.check, s, d, Text(r.Checks))
			assert.Contains(t, d, tc.detail, "%s\n%s", tc.check, Text(r.Checks))
			assert.True(t, Failed(r.Checks), "the report must fail")
		})
	}
}

func TestVanillaBaselineAndModuleChecks(t *testing.T) {
	e := newEnv(t)
	vanilla := mustRun(t, e.config("newerr"))
	bl := RecordBaseline(vanilla.Profiles)
	require.Equal(t, 416, bl.Modules["Game"], "baseline = %+v", bl)
	require.Equal(t, 2123, bl.Modules["World"], "baseline = %+v", bl)
	require.Len(t, bl.Noise, 3, "baseline = %+v", bl)
	path := filepath.Join(e.root, "cache", "baseline.json")
	require.NoError(t, bl.Save(path))
	loaded, err := LoadBaseline(path)
	require.NoError(t, err, "load")
	require.Equal(t, 209, loaded.Modules["Mission"])

	// without a baseline the counts are information and the engine lines cannot be judged
	r := mustRun(t, e.config("newerr"))
	s, _ := status(r.Checks, "modules")
	assert.Equal(t, Info, s, "modules without baseline")
	s, _ = status(r.Checks, "engine")
	assert.Equal(t, Info, s, "engine without baseline")

	// mods that ship scripts raise the count: pass
	t.Setenv("FAKE_GAME", "418")
	c := e.config("")
	c.Baseline, c.ModScripts = bl, map[string]int{"Game": 2}
	r = mustRun(t, c)
	s, d := status(r.Checks, "modules")
	assert.Equal(t, Pass, s, "modules = %s %s", s, d)
	s, _ = status(r.Checks, "engine")
	assert.Equal(t, Pass, s, "the baseline's own noise must not be reported: %s", Text(r.Checks))

	// mods that ship scripts, count unchanged: they were not loaded
	t.Setenv("FAKE_GAME", "416")
	c = e.config("")
	c.Baseline, c.ModScripts = bl, map[string]int{"Game": 2}
	r = mustRun(t, c)
	s, d = status(r.Checks, "modules")
	assert.Equal(t, Fail, s, "modules = %s %s", s, d)
	assert.Contains(t, d, "were not loaded")

	// a count that is not baseline + shipped is only a warning (formula unproven)
	t.Setenv("FAKE_GAME", "420")
	c = e.config("")
	c.Baseline, c.ModScripts = bl, map[string]int{"Game": 2}
	s, _ = status(mustRun(t, c).Checks, "modules")
	assert.Equal(t, Warn, s, "modules")

	// an error line vanilla does not log: a warning, shown verbatim
	t.Setenv("FAKE_GAME", "416")
	c = e.config("newerr")
	c.Baseline = &Baseline{Modules: bl.Modules, Noise: []string{"ENTITY    (W): Unknown object class 'pond' (dz\\water\\pond.p3d)", "!!! [CE][VehicleRespawner] (PRIBoat) :: Init: \"VehicleBoat\" - Failed to spawn the requested amount (3 < 9) within 6 attempts."}}
	r = mustRun(t, c)
	s, d = status(r.Checks, "engine")
	assert.Equal(t, Warn, s, "engine = %s %s", s, d)
	assert.Contains(t, d, "types.xml line 5")
	assert.False(t, Failed(r.Checks), "new error lines alone must not fail the boot:\n%s", Text(r.Checks))
}

func TestExpectAndAdminChecks(t *testing.T) {
	e := newEnv(t)
	c := e.config("")
	c.Expect = []string{`MOD LOADED \w+`, `never printed`, `(`}
	c.Admin = &admin.ModConfig{}
	r := mustRun(t, c)
	var pass, fail int
	for _, ch := range r.Checks {
		if ch.Name == "expect" {
			if ch.Status == Pass {
				pass++
			} else {
				fail++
			}
		}
	}
	assert.Equal(t, 1, pass, "expect checks:\n%s", Text(r.Checks))
	assert.Equal(t, 2, fail, "expect checks:\n%s", Text(r.Checks))
	s, d := status(r.Checks, "dzo-admin")
	assert.Equal(t, Pass, s, "dzo-admin = %s %s", s, d)

	e = newEnv(t)
	c = e.config("noadmin")
	c.Admin = &admin.ModConfig{}
	s, _ = status(mustRun(t, c).Checks, "dzo-admin")
	assert.Equal(t, Fail, s, "a mod that never makes contact must fail")
}

func TestAServerThatIgnoresSIGTERMIsKilled(t *testing.T) {
	old := stopGrace
	stopGrace = 300 * time.Millisecond
	t.Cleanup(func() { stopGrace = old })
	e := newEnv(t)
	start := time.Now()
	r := mustRun(t, e.config("hang"))
	assert.LessOrEqual(t, time.Since(start), 8*time.Second, "stopping took too long")
	assert.False(t, Failed(r.Checks), "the run was fine:\n%s", Text(r.Checks))
}

func TestArgsRefuseAbsoluteModPaths(t *testing.T) {
	args, err := Args(2302, []Mod{{Name: "@A"}, {Name: "@B"}, {Name: "@S", Server: true}}, []string{"-cpuCount=2"})
	require.NoError(t, err)
	got := strings.Join(args, " ")
	for _, want := range []string{"-mod=@A;@B", "-servermod=@S", "-port=2302", "-profiles=profiles", "-config=serverDZ.cfg", "-cpuCount=2"} {
		assert.Contains(t, got, want, "args")
	}
	for _, bad := range []string{"/abs/@A", "../@A", "@A;@B", "A", "@a/b"} {
		_, err := Args(1, []Mod{{Name: bad}}, nil)
		assert.Error(t, err, "mod name %q must be refused", bad)
	}
}

func TestFreePortsAreFreeAndApart(t *testing.T) {
	ps, err := FreePorts(3)
	require.NoError(t, err)
	require.Len(t, ps, 3)
	for i, p := range ps {
		for _, q := range ps[i+1:] {
			d := q - p
			assert.False(t, d < 10 && d > -10, "ports %d and %d are closer than 10", p, q)
		}
		assert.False(t, p <= 2310 || (p >= 27015 && p <= 27017), "default port %d", p)
	}
}

func TestTreeMustNotBeInsideTheServer(t *testing.T) {
	e := newEnv(t)
	c := e.config("")
	c.TreeDir = filepath.Join(e.server, "tree")
	_, err := Run(t.Context(), c)
	assert.ErrorContains(t, err, "inside the server install", "a tree inside the server install must be refused")
	c = e.config("")
	c.ServerDir = filepath.Join(e.root, "empty")
	require.NoError(t, os.MkdirAll(c.ServerDir, 0o750))
	_, err = Run(t.Context(), c)
	assert.ErrorContains(t, err, "no DayZServer", "a server dir without DayZServer")
	c = e.config("")
	c.Mods = []Mod{{Name: "@Missing", Dir: filepath.Join(e.root, "nope")}}
	_, err = Run(t.Context(), c)
	assert.Error(t, err, "a missing mod dir must be an error of the test, not a server failure")
}

func TestGuard(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, Guard(filepath.Join(dir, "instances"), filepath.Join(dir, "quadlets")), "a development machine passes")
	write(t, filepath.Join(dir, "instances", "hashima", "x"), "x")
	assert.ErrorContains(t, Guard(filepath.Join(dir, "instances"), filepath.Join(dir, "quadlets")), "never runs where dzo manages instances", "instance directories must refuse")
	dir = t.TempDir()
	write(t, filepath.Join(dir, "q", "dzo-hashima.container"), "[Container]")
	assert.Error(t, Guard(filepath.Join(dir, "instances"), filepath.Join(dir, "q")), "a dzo quadlet must refuse")
	write(t, filepath.Join(dir, "q2", "other.container"), "[Container]")
	assert.NoError(t, Guard(filepath.Join(dir, "instances"), filepath.Join(dir, "q2")), "quadlets of other things do not count")
}

func TestFindSteamServer(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".steam", "debian-installation")
	other := filepath.Join(home, "games", "SteamLibrary")
	write(t, filepath.Join(root, "steamapps", "libraryfolders.vdf"), "\"libraryfolders\"\n{\n\t\"0\"\n\t{\n\t\t\"path\"\t\t\""+root+"\"\n\t}\n\t\"1\"\n\t{\n\t\t\"path\"\t\t\""+other+"\"\n\t}\n}\n")
	write(t, filepath.Join(other, "steamapps", "appmanifest_223350.acf"), "\"AppState\"\n{\n\t\"StateFlags\"\t\t\"4\"\n\t\"installdir\"\t\t\"DayZServer\"\n\t\"buildid\"\t\t\"24570360\"\n}\n")
	write(t, filepath.Join(other, "steamapps", "appmanifest_1042420.acf"), "\"AppState\"\n{\n\t\"StateFlags\"\t\t\"1026\"\n\t\"installdir\"\t\t\"DayZServerExp\"\n}\n")
	write(t, filepath.Join(other, "steamapps", "workshop", "content", "221100", "1559212036", "meta.cpp"), "x")

	libs := Libraries(SteamRoots(home))
	require.Len(t, libs, 2, "libraries")
	s, err := FindSteamServer(libs, 223350)
	require.NoError(t, err)
	require.Equal(t, "24570360", s.Build)
	require.True(t, strings.HasSuffix(s.Dir, filepath.Join("SteamLibrary", "steamapps", "common", "DayZServer")), "server dir = %s", s.Dir)
	_, err = FindSteamServer(libs, 1042420)
	assert.ErrorContains(t, err, "still being installed", "an update in progress must be refused")
	_, err = FindSteamServer(libs, 999)
	assert.ErrorContains(t, err, "steam://install/999", "a missing server must say how to install it")
	d, err := WorkshopMod(libs, 221100, 1559212036)
	require.NoError(t, err, "workshop mod")
	assert.True(t, strings.HasSuffix(d, "1559212036"), "workshop mod = %q", d)
	_, err = WorkshopMod(libs, 221100, 42)
	assert.ErrorContains(t, err, "subscribe", "a missing workshop item must say what to do")
}

func TestModScriptsCountsPerModule(t *testing.T) {
	dir := t.TempDir()
	var inputs []pbo.Input
	for _, p := range []string{`scripts\3_Game\a.c`, `scripts/3_Game/b.c`, `scripts/4_World/c.c`, `scripts/5_Mission/d.c`, `scripts/5_Mission/e.c`, `scripts/3_Game/readme.txt`, `config.cpp`} {
		inputs = append(inputs, pbo.Input{Path: p, ModTime: time.Unix(0, 0).UTC(), Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader([]byte("x"))), nil }})
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "addons"), 0o750))
	_, err := pbo.PackFile(t.Context(), filepath.Join(dir, "addons", "m.pbo"), inputs, pbo.PackOptions{})
	require.NoError(t, err)
	got, err := ModScripts(dir)
	require.NoError(t, err)
	assert.Equal(t, 2, got["Game"], "scripts = %v", got)
	assert.Equal(t, 1, got["World"], "scripts = %v", got)
	assert.Equal(t, 2, got["Mission"], "scripts = %v", got)
	none, _ := ModScripts(t.TempDir())
	assert.Empty(t, none, "no addons")
}

func TestBaselinePathChangesWithTheServerBinary(t *testing.T) {
	e := newEnv(t)
	a, err := BaselinePath("/cache", "dayz-stable", e.server)
	require.NoError(t, err)
	write(t, filepath.Join(e.server, "DayZServer"), "a different, updated binary")
	b, _ := BaselinePath("/cache", "dayz-stable", e.server)
	assert.NotEqual(t, a, b)
	assert.True(t, strings.HasPrefix(a, "/cache/boottest/baseline/dayz-stable/"), "path %q", a)
	_, err = BaselinePath("/cache", "p", t.TempDir())
	assert.Error(t, err, "no server")
}

func TestEvaluateOnLogFixtures(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "script_1.log"), "SCRIPT       : Module: Game; loaded 416x files\n")
	cs := Evaluate(Eval{Profiles: dir, Ready: true})
	s, _ := status(cs, "scripts")
	assert.Equal(t, Pass, s, "clean log: %s", Text(cs))
	empty := Evaluate(Eval{Profiles: t.TempDir(), Ready: true})
	s, d := status(empty, "scripts")
	assert.Equal(t, Fail, s, "no log at all must fail, not pass")
	assert.Contains(t, d, "no script log")
	txt := Text([]Check{{Name: "x", Status: Fail, Detail: "first\nsecond"}})
	assert.Contains(t, txt, "FAIL  x")
	assert.Contains(t, txt, "second")
}

// The logs of a real 1.29 server (S9), trimmed: the same instance booted with a
// working mission and with one thing broken on purpose each. The baseline is
// the vanilla server of that build.
func TestRealServerLogs(t *testing.T) {
	bl, err := LoadBaseline("testdata/s9/baseline.json")
	require.NoError(t, err)
	mods := map[string]int{"Game": 6, "World": 2, "Mission": 1} // what dzo-admin ships
	for _, tc := range []struct {
		dir    string
		failed []string // checks that must fail
		detail string   // that must appear in one of the failing checks
	}{
		{"main", nil, ""},
		{"badxml", []string{"ce"}, "[ERROR][XML] :: load [$CurrentDir:mpmissions/dayzOffline.chernarusplus/db/types.xml] failed"},
		{"unknowncls", nil, ""}, // only a warning, see below
		{"badce", []string{"ce"}, "nonexistent_folder/types.xml"},
		{"missingfile", []string{"ce"}, "types_missing.xml"},
		{"badscript", []string{"mission", "scripts"}, "Can't find variable 'undefined_variable_dzo'"},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			ready := tc.dir != "badscript"
			cs := Evaluate(Eval{Profiles: filepath.Join("testdata", "s9", tc.dir), Ready: true, NoMission: !ready, Baseline: bl, ModScripts: mods})
			var failed []string
			var text strings.Builder
			for _, c := range cs {
				if c.Status == Fail {
					failed = append(failed, c.Name)
					text.WriteString(c.Detail)
				}
			}
			assert.Equal(t, strings.Join(tc.failed, ","), strings.Join(failed, ","), "failed checks:\n%s", Text(cs))
			assert.Contains(t, text.String(), tc.detail, "the failing checks must show it:\n%s", Text(cs))
			s, d := status(cs, "modules")
			assert.Equal(t, Pass, s, "modules = %s %s", s, d)
			if tc.dir == "unknowncls" {
				s, d := status(cs, "engine")
				assert.Equal(t, Warn, s, "an unknown class is shown as a warning: %s %s", s, d)
				assert.Contains(t, d, "Type 'NoSuchClassDzoTest' will be ignored")
			}
		})
	}
}

// A fake podman: `run` changes into the -w directory and runs the command after the image
// name, `rm` records that it was called.
func TestBootInAContainer(t *testing.T) {
	e := newEnv(t)
	c := e.config("")
	write(t, filepath.Join(e.root, "cf", "addons", "x"), "x")
	c.Mods = []Mod{{Name: "@CF", Dir: filepath.Join(e.root, "cf")}}
	log := filepath.Join(e.root, "podman.log")
	fake := filepath.Join(e.root, "podman")
	script := `#!/bin/sh
echo "$@" >> ` + log + `
if [ "$1" = rm ]; then exit 0; fi
shift
while [ $# -gt 0 ]; do
  case "$1" in
    -w) cd "$2"; shift 2;;
    --rm) shift;;
    --name|--network|-v) shift 2;;
    *) break;;
  esac
done
shift # the image
exec "$@"
`
	require.NoError(t, os.WriteFile(fake, []byte(script), 0o755)) //nolint:gosec // test fixture
	c.Image, c.Podman = "localhost/dzo-runtime:test", fake
	r := mustRun(t, c)
	require.False(t, Failed(r.Checks), "a boot in a container must pass:\n%s", Text(r.Checks))
	b, _ := os.ReadFile(log)
	tree, _ := filepath.Abs(c.TreeDir)
	srv, _ := filepath.Abs(e.server)
	cf, _ := filepath.Abs(filepath.Join(e.root, "cf"))
	for _, want := range []string{
		"run --rm --name dzo-boottest-", "--network host", "-v " + tree + ":" + tree, "-v " + srv + ":" + srv + ":O",
		"-v " + cf + ":" + cf + ":O", "-w " + tree, "localhost/dzo-runtime:test /bin/sh -c", "rm -f -t 0 dzo-boottest-",
	} {
		assert.Contains(t, string(b), want, "podman was not called with it")
	}
	s, _ := os.ReadFile(filepath.Join(tree, "stdin"))
	assert.Equal(t, "i\n", string(s), "stdin file")
}

// 1.30 logs no script module for init.c; the economy's storage directory says the mission started.
func TestMissionLoadedOnTheStorageDirectoryLine(t *testing.T) {
	e := newEnv(t)
	r := mustRun(t, e.config("nomission,storagedirs"))
	st, d := status(r.Checks, "mission")
	require.NotEqual(t, Fail, st, "mission check = %s %s", st, d)
}

func TestShutdownLeakReportsAreNotScriptErrors(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "script_x.log"), "SCRIPT       : Module: Game; loaded 1x files\n"+
		"09:06:09.911   SCRIPT    (E): Leaked 'BunkerBroadcastManager' script instance (1x)!\n"+
		"09:06:09.911   SCRIPT    (E): ==== Total Leaks (2x)! ====\n")
	cs := Evaluate(Eval{Profiles: dir, Ready: true})
	st, d := status(cs, "scripts")
	assert.Equal(t, Pass, st, "scripts = %s %s", st, d)
	assert.Contains(t, d, "2 leak report")
	write(t, filepath.Join(dir, "script_y.log"), "SCRIPT    (E): Can't find variable 'x'\n")
	st, _ = status(Evaluate(Eval{Profiles: dir, Ready: true}), "scripts")
	assert.Equal(t, Fail, st, "a real script error must still fail")
}
