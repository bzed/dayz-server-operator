// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package boottest

import (
	"bytes"
	"context"
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
	if b, err := os.ReadFile(filepath.Join(profiles, "dzo-admin", "config.json")); err == nil && !fake["noadmin"] {
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
	t       *testing.T
	root    string
	server  string
	mission string
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{t: t, root: root, server: filepath.Join(root, "server"), mission: filepath.Join(root, "staged")}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(e.server, "DayZServer"), "#!/bin/sh\nexec env DZO_FAKE_SERVER=1 '"+exe+"' \"$@\"\n")
	if err := os.Chmod(filepath.Join(e.server, "DayZServer"), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
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

func mustRun(t *testing.T, c Config) *Result {
	t.Helper()
	r, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBootPassesAndBuildsTheTree(t *testing.T) {
	e := newEnv(t)
	c := e.config("")
	c.Mods = []Mod{{Name: "@CF", Dir: filepath.Join(e.root, "cf")}, {Name: "@dzo-admin", Dir: filepath.Join(e.root, "adm"), Server: true}}
	write(t, filepath.Join(e.root, "cf", "addons", "x"), "x")
	write(t, filepath.Join(e.root, "adm", "addons", "x"), "x")
	r := mustRun(t, c)
	if Failed(r.Checks) {
		t.Fatalf("a good boot must pass:\n%s", Text(r.Checks))
	}
	tree := c.TreeDir
	for _, link := range []string{"DayZServer", "addons", "@CF", "@dzo-admin"} {
		if fi, err := os.Lstat(filepath.Join(tree, link)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s must be a symlink: %v", link, err)
		}
	}
	for _, absent := range []string{"core.123", "@Stray"} {
		if _, err := os.Lstat(filepath.Join(tree, absent)); err == nil {
			t.Errorf("%s must not be in the tree", absent)
		}
	}
	if fi, err := os.Lstat(filepath.Join(tree, "battleye", "BEServer_x64.so")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("battleye must be a real dir of links to the BattlEye binaries")
	}
	if b, _ := os.ReadFile(filepath.Join(tree, "mpmissions", "dayzOffline.x", "init.c")); string(b) != "staged" {
		t.Errorf("the staged mission must be copied: %q", b)
	}
	if fi, err := os.Lstat(filepath.Join(tree, "mpmissions", "dayzOffline.x", "init.c")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Error("the mission must be a real copy, not links")
	}
	cfg, _ := os.ReadFile(filepath.Join(tree, "serverDZ.cfg"))
	for _, want := range []string{`hostname = "[dzo boot test] My Server";`, `template = "dayzOffline.x";`, "steamQueryPort = " + strconv.Itoa(r.Ports.Query) + ";"} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("serverDZ.cfg lacks %q:\n%s", want, cfg)
		}
	}
	if strings.Contains(string(cfg), `"secret"`) {
		t.Error("the password must be replaced so that nobody can join")
	}
	if b, _ := os.ReadFile(filepath.Join(tree, "keys", "dayz.bikey")); string(b) != "k" {
		t.Errorf("keys: %q", b)
	}
	be, _ := os.ReadFile(filepath.Join(tree, "profiles", "battleye", "beserver_x64.cfg"))
	if !strings.Contains(string(be), "RConPort "+strconv.Itoa(r.Ports.RCon)) {
		t.Errorf("BattlEye seed: %s", be)
	}
	if r.Ports.Game == r.Ports.Query || r.Ports.Query == r.Ports.RCon {
		t.Errorf("ports must differ: %+v", r.Ports)
	}
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
			if s, d := status(r.Checks, tc.check); s != tc.want || !strings.Contains(d, tc.detail) {
				t.Errorf("%s = %s %q, want %s containing %q\n%s", tc.check, s, d, tc.want, tc.detail, Text(r.Checks))
			}
			if !Failed(r.Checks) {
				t.Error("the report must fail")
			}
		})
	}
}

func TestVanillaBaselineAndModuleChecks(t *testing.T) {
	e := newEnv(t)
	vanilla := mustRun(t, e.config("newerr"))
	bl := RecordBaseline(vanilla.Profiles)
	if bl.Modules["Game"] != 416 || bl.Modules["World"] != 2123 || len(bl.Noise) != 3 {
		t.Fatalf("baseline = %+v", bl)
	}
	path := filepath.Join(e.root, "cache", "baseline.json")
	if err := bl.Save(path); err != nil {
		t.Fatal(err)
	}
	if bl, err := LoadBaseline(path); err != nil || bl.Modules["Mission"] != 209 {
		t.Fatalf("load: %+v %v", bl, err)
	}

	// without a baseline the counts are information and the engine lines cannot be judged
	r := mustRun(t, e.config("newerr"))
	if s, _ := status(r.Checks, "modules"); s != Info {
		t.Errorf("modules without baseline = %s", s)
	}
	if s, _ := status(r.Checks, "engine"); s != Info {
		t.Errorf("engine without baseline = %s", s)
	}

	// mods that ship scripts raise the count: pass
	t.Setenv("FAKE_GAME", "418")
	c := e.config("")
	c.Baseline, c.ModScripts = bl, map[string]int{"Game": 2}
	r = mustRun(t, c)
	if s, d := status(r.Checks, "modules"); s != Pass {
		t.Errorf("modules = %s %s", s, d)
	}
	if s, _ := status(r.Checks, "engine"); s != Pass {
		t.Errorf("the baseline's own noise must not be reported: %s", Text(r.Checks))
	}

	// mods that ship scripts, count unchanged: they were not loaded
	t.Setenv("FAKE_GAME", "416")
	c = e.config("")
	c.Baseline, c.ModScripts = bl, map[string]int{"Game": 2}
	r = mustRun(t, c)
	if s, d := status(r.Checks, "modules"); s != Fail || !strings.Contains(d, "were not loaded") {
		t.Errorf("modules = %s %s", s, d)
	}

	// a count that is not baseline + shipped is only a warning (formula unproven)
	t.Setenv("FAKE_GAME", "420")
	c = e.config("")
	c.Baseline, c.ModScripts = bl, map[string]int{"Game": 2}
	if s, _ := status(mustRun(t, c).Checks, "modules"); s != Warn {
		t.Errorf("modules = %s, want warn", s)
	}

	// an error line vanilla does not log: a warning, shown verbatim
	t.Setenv("FAKE_GAME", "416")
	c = e.config("newerr")
	c.Baseline = &Baseline{Modules: bl.Modules, Noise: []string{"ENTITY    (W): Unknown object class 'pond' (dz\\water\\pond.p3d)", "!!! [CE][VehicleRespawner] (PRIBoat) :: Init: \"VehicleBoat\" - Failed to spawn the requested amount (3 < 9) within 6 attempts."}}
	r = mustRun(t, c)
	if s, d := status(r.Checks, "engine"); s != Warn || !strings.Contains(d, "types.xml line 5") {
		t.Errorf("engine = %s %s", s, d)
	}
	if Failed(r.Checks) {
		t.Errorf("new error lines alone must not fail the boot:\n%s", Text(r.Checks))
	}
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
	if pass != 1 || fail != 2 {
		t.Errorf("expect: %d pass, %d fail:\n%s", pass, fail, Text(r.Checks))
	}
	if s, d := status(r.Checks, "dzo-admin"); s != Pass {
		t.Errorf("dzo-admin = %s %s", s, d)
	}

	e = newEnv(t)
	c = e.config("noadmin")
	c.Admin = &admin.ModConfig{}
	if s, _ := status(mustRun(t, c).Checks, "dzo-admin"); s != Fail {
		t.Error("a mod that never makes contact must fail")
	}
}

func TestAServerThatIgnoresSIGTERMIsKilled(t *testing.T) {
	old := stopGrace
	stopGrace = 300 * time.Millisecond
	t.Cleanup(func() { stopGrace = old })
	e := newEnv(t)
	start := time.Now()
	r := mustRun(t, e.config("hang"))
	if time.Since(start) > 8*time.Second {
		t.Errorf("stopping took %s", time.Since(start))
	}
	if Failed(r.Checks) {
		t.Errorf("the run was fine:\n%s", Text(r.Checks))
	}
}

func TestArgsRefuseAbsoluteModPaths(t *testing.T) {
	args, err := Args(2302, []Mod{{Name: "@A"}, {Name: "@B"}, {Name: "@S", Server: true}}, []string{"-cpuCount=2"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	for _, want := range []string{"-mod=@A;@B", "-servermod=@S", "-port=2302", "-profiles=profiles", "-config=serverDZ.cfg", "-cpuCount=2"} {
		if !strings.Contains(got, want) {
			t.Errorf("args lack %q: %s", want, got)
		}
	}
	for _, bad := range []string{"/abs/@A", "../@A", "@A;@B", "A", "@a/b"} {
		if _, err := Args(1, []Mod{{Name: bad}}, nil); err == nil {
			t.Errorf("mod name %q must be refused", bad)
		}
	}
}

func TestFreePortsAreFreeAndApart(t *testing.T) {
	ps, err := FreePorts(3)
	if err != nil || len(ps) != 3 {
		t.Fatalf("%v %v", ps, err)
	}
	for i, p := range ps {
		for _, q := range ps[i+1:] {
			if d := q - p; d < 10 && d > -10 {
				t.Errorf("ports %d and %d are closer than 10", p, q)
			}
		}
		if p <= 2310 || (p >= 27015 && p <= 27017) {
			t.Errorf("default port %d", p)
		}
	}
}

func TestTreeMustNotBeInsideTheServer(t *testing.T) {
	e := newEnv(t)
	c := e.config("")
	c.TreeDir = filepath.Join(e.server, "tree")
	if _, err := Run(context.Background(), c); err == nil || !strings.Contains(err.Error(), "inside the server install") {
		t.Errorf("a tree inside the server install must be refused: %v", err)
	}
	c = e.config("")
	c.ServerDir = filepath.Join(e.root, "empty")
	if err := os.MkdirAll(c.ServerDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), c); err == nil || !strings.Contains(err.Error(), "no DayZServer") {
		t.Errorf("a server dir without DayZServer: %v", err)
	}
	c = e.config("")
	c.Mods = []Mod{{Name: "@Missing", Dir: filepath.Join(e.root, "nope")}}
	if _, err := Run(context.Background(), c); err == nil {
		t.Error("a missing mod dir must be an error of the test, not a server failure")
	}
}

func TestGuard(t *testing.T) {
	dir := t.TempDir()
	if err := Guard(filepath.Join(dir, "instances"), filepath.Join(dir, "quadlets")); err != nil {
		t.Errorf("a development machine passes: %v", err)
	}
	write(t, filepath.Join(dir, "instances", "hashima", "x"), "x")
	if err := Guard(filepath.Join(dir, "instances"), filepath.Join(dir, "quadlets")); err == nil || !strings.Contains(err.Error(), "never runs where dzo manages instances") {
		t.Errorf("instance directories must refuse: %v", err)
	}
	dir = t.TempDir()
	write(t, filepath.Join(dir, "q", "dzo-hashima.container"), "[Container]")
	if err := Guard(filepath.Join(dir, "instances"), filepath.Join(dir, "q")); err == nil {
		t.Error("a dzo quadlet must refuse")
	}
	write(t, filepath.Join(dir, "q2", "other.container"), "[Container]")
	if err := Guard(filepath.Join(dir, "instances"), filepath.Join(dir, "q2")); err != nil {
		t.Errorf("quadlets of other things do not count: %v", err)
	}
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
	if len(libs) != 2 {
		t.Fatalf("libraries = %v", libs)
	}
	s, err := FindSteamServer(libs, 223350)
	if err != nil || s.Build != "24570360" || !strings.HasSuffix(s.Dir, filepath.Join("SteamLibrary", "steamapps", "common", "DayZServer")) {
		t.Fatalf("server = %+v %v", s, err)
	}
	if _, err := FindSteamServer(libs, 1042420); err == nil || !strings.Contains(err.Error(), "still being installed") {
		t.Errorf("an update in progress must be refused: %v", err)
	}
	if _, err := FindSteamServer(libs, 999); err == nil || !strings.Contains(err.Error(), "steam://install/999") {
		t.Errorf("a missing server must say how to install it: %v", err)
	}
	if d, err := WorkshopMod(libs, 221100, 1559212036); err != nil || !strings.HasSuffix(d, "1559212036") {
		t.Errorf("workshop mod = %q %v", d, err)
	}
	if _, err := WorkshopMod(libs, 221100, 42); err == nil || !strings.Contains(err.Error(), "subscribe") {
		t.Errorf("a missing workshop item must say what to do: %v", err)
	}
}

func TestModScriptsCountsPerModule(t *testing.T) {
	dir := t.TempDir()
	var inputs []pbo.Input
	for _, p := range []string{`scripts\3_Game\a.c`, `scripts/3_Game/b.c`, `scripts/4_World/c.c`, `scripts/5_Mission/d.c`, `scripts/5_Mission/e.c`, `scripts/3_Game/readme.txt`, `config.cpp`} {
		inputs = append(inputs, pbo.Input{Path: p, ModTime: time.Unix(0, 0).UTC(), Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader([]byte("x"))), nil }})
	}
	if err := os.MkdirAll(filepath.Join(dir, "addons"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := pbo.PackFile(context.Background(), filepath.Join(dir, "addons", "m.pbo"), inputs, pbo.PackOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := ModScripts(dir)
	if err != nil || got["Game"] != 2 || got["World"] != 1 || got["Mission"] != 2 {
		t.Errorf("scripts = %v %v", got, err)
	}
	if got, _ := ModScripts(t.TempDir()); len(got) != 0 {
		t.Errorf("no addons: %v", got)
	}
}

func TestBaselinePathChangesWithTheServerBinary(t *testing.T) {
	e := newEnv(t)
	a, err := BaselinePath("/cache", "dayz-stable", e.server)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(e.server, "DayZServer"), "a different, updated binary")
	b, _ := BaselinePath("/cache", "dayz-stable", e.server)
	if a == b || !strings.HasPrefix(a, "/cache/boottest/baseline/dayz-stable/") {
		t.Errorf("paths %q %q", a, b)
	}
	if _, err := BaselinePath("/cache", "p", t.TempDir()); err == nil {
		t.Error("no server")
	}
}

func TestEvaluateOnLogFixtures(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "script_1.log"), "SCRIPT       : Module: Game; loaded 416x files\n")
	cs := Evaluate(Eval{Profiles: dir, Ready: true})
	if s, _ := status(cs, "scripts"); s != Pass {
		t.Errorf("clean log: %s", Text(cs))
	}
	empty := Evaluate(Eval{Profiles: t.TempDir(), Ready: true})
	if s, d := status(empty, "scripts"); s != Fail || !strings.Contains(d, "no script log") {
		t.Errorf("no log at all must fail, not pass: %s %s", s, d)
	}
	txt := Text([]Check{{Name: "x", Status: Fail, Detail: "first\nsecond"}})
	if !strings.Contains(txt, "FAIL  x") || !strings.Contains(txt, "second") {
		t.Errorf("text = %q", txt)
	}
}

// The logs of a real 1.29 server (S9), trimmed: the same instance booted with a
// working mission and with one thing broken on purpose each. The baseline is
// the vanilla server of that build.
func TestRealServerLogs(t *testing.T) {
	bl, err := LoadBaseline("testdata/s9/baseline.json")
	if err != nil {
		t.Fatal(err)
	}
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
			if strings.Join(failed, ",") != strings.Join(tc.failed, ",") {
				t.Errorf("failed checks = %v, want %v\n%s", failed, tc.failed, Text(cs))
			}
			if !strings.Contains(text.String(), tc.detail) {
				t.Errorf("the failing checks must show %q:\n%s", tc.detail, Text(cs))
			}
			if s, d := status(cs, "modules"); s != Pass {
				t.Errorf("modules = %s %s", s, d)
			}
			if tc.dir == "unknowncls" {
				if s, d := status(cs, "engine"); s != Warn || !strings.Contains(d, "Type 'NoSuchClassDzoTest' will be ignored") {
					t.Errorf("an unknown class is shown as a warning: %s %s", s, d)
				}
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
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	c.Image, c.Podman = "localhost/dzo-runtime:test", fake
	r := mustRun(t, c)
	if Failed(r.Checks) {
		t.Fatalf("a boot in a container must pass:\n%s", Text(r.Checks))
	}
	b, _ := os.ReadFile(log)
	tree, _ := filepath.Abs(c.TreeDir)
	srv, _ := filepath.Abs(e.server)
	cf, _ := filepath.Abs(filepath.Join(e.root, "cf"))
	for _, want := range []string{
		"run --rm --name dzo-boottest-", "--network host", "-v " + tree + ":" + tree, "-v " + srv + ":" + srv + ":O",
		"-v " + cf + ":" + cf + ":O", "-w " + tree, "localhost/dzo-runtime:test /bin/sh -c", "rm -f -t 0 dzo-boottest-",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("podman was not called with %q:\n%s", want, b)
		}
	}
	if s, _ := os.ReadFile(filepath.Join(tree, "stdin")); string(s) != "i\n" {
		t.Errorf("stdin file: %q", s)
	}
}

// 1.30 logs no script module for init.c; the economy's storage directory says the mission started.
func TestMissionLoadedOnTheStorageDirectoryLine(t *testing.T) {
	e := newEnv(t)
	r := mustRun(t, e.config("nomission,storagedirs"))
	if st, d := status(r.Checks, "mission"); st == Fail {
		t.Fatalf("mission check = %s %s", st, d)
	}
}

func TestShutdownLeakReportsAreNotScriptErrors(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "script_x.log"), "SCRIPT       : Module: Game; loaded 1x files\n"+
		"09:06:09.911   SCRIPT    (E): Leaked 'BunkerBroadcastManager' script instance (1x)!\n"+
		"09:06:09.911   SCRIPT    (E): ==== Total Leaks (2x)! ====\n")
	cs := Evaluate(Eval{Profiles: dir, Ready: true})
	if st, d := status(cs, "scripts"); st != Pass || !strings.Contains(d, "2 leak report") {
		t.Errorf("scripts = %s %s", st, d)
	}
	write(t, filepath.Join(dir, "script_y.log"), "SCRIPT    (E): Can't find variable 'x'\n")
	if st, _ := status(Evaluate(Eval{Profiles: dir, Ready: true}), "scripts"); st != Fail {
		t.Error("a real script error must still fail")
	}
}
