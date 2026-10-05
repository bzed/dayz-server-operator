// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func validInstance() Instance {
	return Instance{
		Name:          "deerisle",
		Product:       "dayz-stable",
		Map:           "empty.deerisle",
		MissionSource: MissionSource{Git: "https://example.invalid/mission.git", Ref: "main", Path: "empty.deerisle"},
		Ports:         Ports{Game: 8302, RCon: 8303, Query: 8716},
		Network:       NetworkHost,
	}
}

func TestInstanceValidateOK(t *testing.T) {
	if err := validInstance().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestInstanceValidateMissingFields(t *testing.T) {
	i := Instance{}
	err := i.Validate()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"name is required", "product is required", "map is required", "ports.game is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
}

func TestInstanceValidateBadNetwork(t *testing.T) {
	i := validInstance()
	i.Network = "bridge"
	if err := i.Validate(); err == nil || !strings.Contains(err.Error(), "network must be") {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestInstanceValidateDuplicateMods(t *testing.T) {
	i := validInstance()
	i.Mods = []ModRef{{ID: 1}, {ID: 1}}
	if err := i.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate entry 1") {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestInstanceValidateZeroModID(t *testing.T) {
	i := validInstance()
	i.Mods = []ModRef{{ID: 0}}
	if err := i.Validate(); err == nil || !strings.Contains(err.Error(), "exactly one of id or local") {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestInstanceValidateBadUpdatePolicy(t *testing.T) {
	i := validInstance()
	i.Updates.Policy = "sometimes"
	if err := i.Validate(); err == nil || !strings.Contains(err.Error(), "updates.policy") {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestInstanceValidateAcceptsEmptyUpdatePolicy(t *testing.T) {
	i := validInstance()
	i.Updates.Policy = ""
	if err := i.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestInstanceDefaultMissionSource(t *testing.T) {
	i := validInstance()
	i.MissionSource = MissionSource{}
	i.Map = "dayzOffline.chernarusplus"
	if err := i.Validate(); err != nil {
		t.Errorf("a Bohemia map needs no mission_source: %v", err)
	}
	i.MissionSource = MissionSource{Ref: "DZ_1.29"} // the default repository, pinned
	if err := i.Validate(); err != nil {
		t.Errorf("a ref alone must be allowed: %v", err)
	}
	i.MissionSource = MissionSource{}
	i.Map = "empty.deerisle"
	if err := i.Validate(); err == nil {
		t.Error("a map the Central Economy repository does not ship needs a mission_source")
	}
	i.MissionSource = MissionSource{Path: "empty.deerisle"}
	if err := i.Validate(); err != nil {
		t.Errorf("an explicit path is a deliberate choice: %v", err)
	}
}

func TestMissionSourceValidateRequiresExactlyOne(t *testing.T) {
	cases := []MissionSource{
		{},                      // neither
		{Git: "g", Preset: "p"}, // both
	}
	for _, c := range cases {
		if err := c.Validate(); err == nil {
			t.Errorf("Validate(%+v) should fail", c)
		}
	}
}

func TestMissionSourceValidateGitNeedsRefAndPath(t *testing.T) {
	if err := (MissionSource{Git: "g"}).Validate(); err == nil {
		t.Fatal("expected an error for missing ref/path")
	}
	if err := (MissionSource{Git: "g", Ref: "main"}).Validate(); err == nil {
		t.Fatal("expected an error for missing path")
	}
	if err := (MissionSource{Git: "g", Ref: "main", Path: "p"}).Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestMissionSourceValidatePresetAlone(t *testing.T) {
	if err := (MissionSource{Preset: "deerisle"}).Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestInstanceYAMLRoundTrip(t *testing.T) {
	const doc = `
name: deerisle
product: dayz-stable
map: empty.deerisle
mission_source:
  git: https://example.invalid/mission.git
  ref: main
  path: "empty.deerisle"
fallback_mission: dayzOffline.chernarusplus
ports: {game: 8302, rcon: 8303, query: 8716}
network: host
params: {cpuCount: auto, extra: ["-netlog", "-adminlog"]}
mods:
  - {id: 1559212036}
  - {id: 1828439124, server: true}
overlays: [login-times, stamina]
updates:
  policy: auto
  check_interval: 1h
  restart_announce: {minutes: 12, lock: 5, delay: 2, text: "MOD UPDATE!"}
restarts:
  schedule: ["*-*-* 00/4:00"]
health: {startup_timeout: 45m, interval: 60s, retries: 5}
restart_limit: {burst: 5, interval: 30m}
notify: {discord: [default, deerisle-admins]}
container:
  mounts: ["/var/lib/GeoIP:/var/lib/GeoIP:ro"]
hooks:
  pre_start: ["hooks/traderstocks.sh"]
`
	var i Instance
	if err := yaml.Unmarshal([]byte(doc), &i); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := i.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if i.Name != "deerisle" || i.Ports.RCon != 8303 || i.Params.CPUCount != "auto" {
		t.Errorf("Instance = %+v", i)
	}
	if len(i.Mods) != 2 || i.Mods[1].Server != true {
		t.Fatalf("Mods = %+v", i.Mods)
	}
	if i.Updates.CheckInterval.Std().String() != "1h0m0s" {
		t.Errorf("CheckInterval = %v", i.Updates.CheckInterval.Std())
	}
	if i.Notify.Discord == nil || len(*i.Notify.Discord) != 2 {
		t.Fatalf("Notify.Discord = %+v", i.Notify.Discord)
	}
}

func TestNotifyDiscordOmittedVsEmpty(t *testing.T) {
	var withNil Instance
	if err := yaml.Unmarshal([]byte("name: a\n"), &withNil); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if withNil.Notify.Discord != nil {
		t.Errorf("expected nil Discord when omitted, got %+v", withNil.Notify.Discord)
	}

	var withEmpty Instance
	if err := yaml.Unmarshal([]byte("name: a\nnotify: {discord: []}\n"), &withEmpty); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if withEmpty.Notify.Discord == nil || len(*withEmpty.Notify.Discord) != 0 {
		t.Errorf("expected a non-nil empty Discord slice, got %+v", withEmpty.Notify.Discord)
	}
}

func TestIntegrationValidateOK(t *testing.T) {
	i := Integration{
		Mod:  2291785308,
		Name: "DayZExpansionCore",
		Files: map[string]FileSource{
			"types.xml": {Source: "local", Path: "files/types.xml"},
		},
	}
	if err := i.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestIntegrationValidateMissingFields(t *testing.T) {
	if err := (Integration{}).Validate(); err == nil {
		t.Fatal("expected an error")
	}
}

func TestFileSourceValidate(t *testing.T) {
	cases := []struct {
		name    string
		f       FileSource
		wantErr string
	}{
		{"local ok", FileSource{Source: "local", Path: "p"}, ""},
		{"local missing path", FileSource{Source: "local"}, "requires path"},
		{"url ok", FileSource{Source: "url", URL: "https://example.invalid"}, ""},
		{"url missing", FileSource{Source: "url"}, "requires url"},
		{"mod ok", FileSource{Source: "mod", Path: "./info/events.xml"}, ""},
		{"mod missing", FileSource{Source: "mod"}, "requires path"},
		{"unknown source", FileSource{Source: "ftp"}, "unknown source"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.f.Validate("key")
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("Validate() = %v, want containing %q", err, c.wantErr)
			}
		})
	}
}

func TestIntegrationYAMLParse(t *testing.T) {
	const doc = `
mod: 2291785308
name: DayZExpansionCore
files:
  types.xml:          {source: local, path: files/types.xml}
  cfgspawnabletypes.xml: {source: url, url: "https://example.invalid/x.xml", sha256: abc}
  cfgeventspawns.xml: {source: local, path: files/cfgeventspawns.xml, maps: [dayzOffline.chernarusplus]}
  events.xml:         {source: mod,   path: ./info/events.xml}
normalize: [eventposdef-root, wrap-root, xml-decl]
hooks:
  post_merge: [hooks/remove-static-trains.sh]
aliases: [2291785546]
`
	var i Integration
	if err := yaml.Unmarshal([]byte(doc), &i); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := i.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(i.Files) != 4 || len(i.Aliases) != 1 || i.Aliases[0] != 2291785546 {
		t.Errorf("Integration = %+v", i)
	}
	if i.Files["cfgeventspawns.xml"].Maps[0] != "dayzOffline.chernarusplus" {
		t.Errorf("Files[cfgeventspawns.xml] = %+v", i.Files["cfgeventspawns.xml"])
	}
}

func TestOverlayManifestYAML(t *testing.T) {
	const doc = `
object_spawners: ["custom_lost_bmps/LOSTBMPS.json"]
spawn_gear_presets: ["presets/loadout.json"]
`
	var o OverlayManifest
	if err := yaml.Unmarshal([]byte(doc), &o); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(o.ObjectSpawners) != 1 || len(o.SpawnGearPresets) != 1 {
		t.Errorf("OverlayManifest = %+v", o)
	}
}

func TestDurationInvalid(t *testing.T) {
	var d Duration
	err := yaml.Unmarshal([]byte(`"not a duration"`), &d)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestDurationEmpty(t *testing.T) {
	var d Duration
	if err := yaml.Unmarshal([]byte(`""`), &d); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if d != 0 {
		t.Errorf("Duration = %v, want 0", d)
	}
}

func TestDurationMarshal(t *testing.T) {
	d := Duration(90 * 1e9) // 90s in nanoseconds
	out, err := yaml.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "1m30s") {
		t.Errorf("Marshal() = %q", out)
	}
}

func TestModRefValidate(t *testing.T) {
	tests := []struct {
		name    string
		ref     ModRef
		wantErr string
	}{
		{"workshop", ModRef{ID: 5}, ""},
		{"local servermod", ModRef{Local: "dzo-admin", Server: true}, ""},
		{"both", ModRef{ID: 5, Local: "x", Server: true}, "exactly one"},
		{"local client mod", ModRef{Local: "x"}, "server: true"},
		{"local bad name", ModRef{Local: "-x", Server: true}, "must match"},
		{"local numeric", ModRef{Local: "123", Server: true}, "purely numeric"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Validate() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestInstanceDuplicateLocalMods(t *testing.T) {
	i := validInstance()
	i.Mods = []ModRef{{Local: "a", Server: true}, {Local: "a", Server: true}}
	if err := i.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate entry a") {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestAdminConfigYAMLAndValidate(t *testing.T) {
	var inst Instance
	src := `
name: a
admin: {players_s: 2, allow_spawn: false, deny_classes: ["Land_*"]}
admin_map:
  watch:
    - {layer: fire, classes: [FireplaceBase], icon: fire, cluster: 50, max: 100}
`
	if err := yaml.Unmarshal([]byte(src), &inst); err != nil {
		t.Fatal(err)
	}
	if inst.Admin.PlayersS != 2 || inst.Admin.AllowSpawn == nil || *inst.Admin.AllowSpawn || len(inst.AdminMap.Watch) != 1 || inst.AdminMap.Watch[0].Cluster != 50 {
		t.Fatalf("parsed = %+v", inst)
	}
	v := validInstance()
	v.Admin = inst.Admin
	v.AdminMap = inst.AdminMap
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	v.Admin.VehiclesS = -1
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "vehicles_s") {
		t.Fatalf("negative interval: %v", err)
	}
	v = validInstance()
	v.AdminMap.Watch = append(v.AdminMap.Watch, inst.AdminMap.Watch[0])
	v.AdminMap.Watch[0].OnlyIf = "burning"
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "only_if") {
		t.Fatalf("only_if must be rejected: %v", err)
	}
}

func TestAgeParsesDaysAndDurations(t *testing.T) {
	var s struct {
		A Age `yaml:"a"`
		B Age `yaml:"b"`
		C Age `yaml:"c"`
	}
	if err := yaml.Unmarshal([]byte("a: 30d\nb: 90m\nc: \"\"\n"), &s); err != nil {
		t.Fatal(err)
	}
	if s.A.Std() != 30*24*time.Hour || s.B.Std() != 90*time.Minute || s.C != 0 {
		t.Errorf("ages = %v %v %v", s.A.Std(), s.B.Std(), s.C.Std())
	}
	for _, bad := range []string{"a: xd", "a: -3d", "a: soon"} {
		if err := yaml.Unmarshal([]byte(bad), &s); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
