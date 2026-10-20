// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	err := validInstance().Validate()
	require.NoError(t, err, "Validate")
}

func TestInstanceValidateMissingFields(t *testing.T) {
	i := Instance{}
	err := i.Validate()
	require.Error(t, err)
	for _, want := range []string{"name is required", "product is required", "map is required", "ports.game is required"} {
		assert.Contains(t, err.Error(), want)
	}
}

func TestInstanceValidateBadNetwork(t *testing.T) {
	i := validInstance()
	i.Network = "bridge"
	err := i.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "network must be")
}

func TestInstanceValidateDuplicateMods(t *testing.T) {
	i := validInstance()
	i.Mods = []ModRef{{ID: 1}, {ID: 1}}
	err := i.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate entry 1")
}

func TestInstanceValidateZeroModID(t *testing.T) {
	i := validInstance()
	i.Mods = []ModRef{{ID: 0}}
	err := i.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "exactly one of id or local")
}

func TestInstanceValidateBadUpdatePolicy(t *testing.T) {
	i := validInstance()
	i.Updates.Policy = "sometimes"
	err := i.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "updates.policy")
}

func TestInstanceValidateAcceptsEmptyUpdatePolicy(t *testing.T) {
	i := validInstance()
	i.Updates.Policy = ""
	err := i.Validate()
	require.NoError(t, err, "Validate()")
}

func TestInstanceDefaultMissionSource(t *testing.T) {
	i := validInstance()
	i.MissionSource = MissionSource{}
	i.Map = "dayzOffline.chernarusplus"
	err := i.Validate()
	require.NoError(t, err, "a Bohemia map needs no mission_source")
	i.MissionSource = MissionSource{Ref: "DZ_1.29"} // the default repository, pinned
	err = i.Validate()
	require.NoError(t, err, "a ref alone must be allowed")
	i.MissionSource = MissionSource{}
	i.Map = "empty.deerisle"
	err = i.Validate()
	require.Error(t, err, "a map the Central Economy repository does not ship needs a mission_source")
	i.MissionSource = MissionSource{Path: "empty.deerisle"}
	err = i.Validate()
	require.NoError(t, err, "an explicit path is a deliberate choice")
}

func TestMissionSourceValidateRequiresExactlyOne(t *testing.T) {
	cases := []MissionSource{
		{},                      // neither
		{Git: "g", Preset: "p"}, // both
	}
	for _, c := range cases {
		err := c.Validate()
		require.Error(t, err)
	}
}

func TestMissionSourceValidateGitNeedsRefAndPath(t *testing.T) {
	err := (MissionSource{Git: "g"}).Validate()
	require.Error(t, err)
	err = (MissionSource{Git: "g", Ref: "main"}).Validate()
	require.Error(t, err)
	err = (MissionSource{Git: "g", Ref: "main", Path: "p"}).Validate()
	require.NoError(t, err, "Validate()")
}

func TestMissionSourceValidatePresetAlone(t *testing.T) {
	err := (MissionSource{Preset: "deerisle"}).Validate()
	require.NoError(t, err, "Validate()")
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
  restart_announce: {minutes: 12, lock: 5, kick: 2, text: "MOD UPDATE! Restart"}
restarts:
  schedule: ["04:00", "Sat 16:00"]
health: {startup_timeout: 45m, interval: 60s, retries: 5}
restart_limit: {burst: 5, interval: 30m}
notify: {discord: [default, deerisle-admins]}
container:
  mounts: ["/var/lib/GeoIP:/var/lib/GeoIP:ro"]
hooks:
  pre_start: ["hooks/traderstocks.sh"]
`
	var i Instance
	err := yaml.Unmarshal([]byte(doc), &i)
	require.NoError(t, err, "Unmarshal")
	err = i.Validate()
	require.NoError(t, err, "Validate")
	require.Equal(t, "deerisle", i.Name)
	require.Equal(t, 8303, i.Ports.RCon)
	assert.Equal(t, "auto", i.Params.CPUCount)
	require.Len(t, i.Mods, 2)
	require.True(t, i.Mods[1].Server, "Mods = %+v", i.Mods)
	assert.Equal(t, "1h0m0s", i.Updates.CheckInterval.Std().String())
	require.NotNil(t, i.Notify.Discord)
	require.Len(t, *i.Notify.Discord, 2)
}

func TestNotifyDiscordOmittedVsEmpty(t *testing.T) {
	var withNil Instance
	err := yaml.Unmarshal([]byte("name: a\n"), &withNil)
	require.NoError(t, err, "Unmarshal")
	assert.Nil(t, withNil.Notify.Discord)

	var withEmpty Instance
	err = yaml.Unmarshal([]byte("name: a\nnotify: {discord: []}\n"), &withEmpty)
	require.NoError(t, err, "Unmarshal")
	require.NotNil(t, withEmpty.Notify.Discord)
	assert.Empty(t, *withEmpty.Notify.Discord)
}

func TestIntegrationValidateOK(t *testing.T) {
	i := Integration{
		Mod:  2291785308,
		Name: "DayZExpansionCore",
		Files: map[string]FileSource{
			"types.xml": {Source: "local", Path: "files/types.xml"},
		},
	}
	err := i.Validate()
	require.NoError(t, err, "Validate")
}

func TestIntegrationValidateMissingFields(t *testing.T) {
	err := (Integration{}).Validate()
	require.Error(t, err)
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
				require.NoError(t, err)
				return
			}
			require.Error(t, err, "Validate() = %v, want containing %q", err, c.wantErr)
			require.Contains(t, err.Error(), c.wantErr)
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
	err := yaml.Unmarshal([]byte(doc), &i)
	require.NoError(t, err, "Unmarshal")
	err = i.Validate()
	require.NoError(t, err, "Validate")
	require.Len(t, i.Files, 4)
	require.Len(t, i.Aliases, 1)
	assert.Equal(t, uint64(2291785546), i.Aliases[0])
	assert.Equal(t, "dayzOffline.chernarusplus", i.Files["cfgeventspawns.xml"].Maps[0])
}

func TestOverlayManifestYAML(t *testing.T) {
	const doc = `
object_spawners: ["custom_lost_bmps/LOSTBMPS.json"]
spawn_gear_presets: ["presets/loadout.json"]
`
	var o OverlayManifest
	err := yaml.Unmarshal([]byte(doc), &o)
	require.NoError(t, err, "Unmarshal")
	require.Len(t, o.ObjectSpawners, 1)
	assert.Len(t, o.SpawnGearPresets, 1)
}

func TestDurationInvalid(t *testing.T) {
	var d Duration
	err := yaml.Unmarshal([]byte(`"not a duration"`), &d)
	require.Error(t, err)
}

func TestDurationEmpty(t *testing.T) {
	var d Duration
	err := yaml.Unmarshal([]byte(`""`), &d)
	require.NoError(t, err, "Unmarshal")
	assert.Equal(t, Duration(0), d)
}

func TestDurationMarshal(t *testing.T) {
	d := Duration(90 * 1e9) // 90s in nanoseconds
	out, err := yaml.Marshal(d)
	require.NoError(t, err, "Marshal")
	assert.Contains(t, string(out), "1m30s")
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
		{"local debug client mod", ModRef{Local: "x", DebugClient: true}, ""},
		{"debug client and server", ModRef{Local: "x", Server: true, DebugClient: true}, "both"},
		{"debug client needs a local mod", ModRef{ID: 5, DebugClient: true}, "only for local mods"},
		{"local bad name", ModRef{Local: "-x", Server: true}, "must match"},
		{"local numeric", ModRef{Local: "123", Server: true}, "purely numeric"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func TestInstanceDuplicateLocalMods(t *testing.T) {
	i := validInstance()
	i.Mods = []ModRef{{Local: "a", Server: true}, {Local: "a", Server: true}}
	err := i.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate entry a")
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
	err := yaml.Unmarshal([]byte(src), &inst)
	require.NoError(t, err)
	require.Equal(t, 2, inst.Admin.PlayersS)
	require.NotNil(t, inst.Admin.AllowSpawn)
	require.False(t, *inst.Admin.AllowSpawn, "parsed = %+v", inst)
	require.Len(t, inst.AdminMap.Watch, 1)
	require.Equal(t, 50, inst.AdminMap.Watch[0].Cluster)
	v := validInstance()
	v.Admin = inst.Admin
	v.AdminMap = inst.AdminMap
	err = v.Validate()
	require.NoError(t, err)
	v.Admin.VehiclesS = -1
	err = v.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "vehicles_s")
	v = validInstance()
	v.AdminMap.Watch = append(v.AdminMap.Watch, inst.AdminMap.Watch[0])
	v.AdminMap.Watch[0].OnlyIf = "burning"
	err = v.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "only_if")
}

func TestAgeParsesDaysAndDurations(t *testing.T) {
	var s struct {
		A Age `yaml:"a"`
		B Age `yaml:"b"`
		C Age `yaml:"c"`
	}
	err := yaml.Unmarshal([]byte("a: 30d\nb: 90m\nc: \"\"\n"), &s)
	require.NoError(t, err)
	require.Equal(t, 30*24*time.Hour, s.A.Std(), "ages = %v %v %v", s.A.Std(), s.B.Std(), s.C.Std())
	require.Equal(t, 90*time.Minute, s.B.Std(), "ages = %v %v %v", s.A.Std(), s.B.Std(), s.C.Std())
	assert.Equal(t, Age(0), s.C, "ages = %v %v %v", s.A.Std(), s.B.Std(), s.C.Std())
	for _, bad := range []string{"a: xd", "a: -3d", "a: soon"} {
		err := yaml.Unmarshal([]byte(bad), &s)
		require.Error(t, err, "%q must be rejected", bad)
	}
}

func TestLogRuleTakesAPlainString(t *testing.T) {
	var c LogsConfig
	err := yaml.Unmarshal([]byte("rotate:\n  - '^RFMosquito_.*\\.log'\n  - {match: '\\.mdmp$', keep_newest: 2, max_age: 14d}\n"), &c)
	require.NoError(t, err)
	require.Len(t, c.Rotate, 2)
	assert.Equal(t, `^RFMosquito_.*\.log`, c.Rotate[0].Match)
	assert.Nil(t, c.Rotate[0].KeepNewest)
	assert.Equal(t, `\.mdmp$`, c.Rotate[1].Match)
	require.NotNil(t, c.Rotate[1].KeepNewest)
	assert.Equal(t, 2, *c.Rotate[1].KeepNewest)
	assert.Equal(t, 14*24*time.Hour, c.Rotate[1].MaxAge.Std())
	dec := yaml.NewDecoder(strings.NewReader("rotate:\n  - {match: x, typo: 1}\n"))
	dec.KnownFields(true)
	err = dec.Decode(&c)
	require.Error(t, err, "the mapping form still rejects unknown keys")
}

func TestAdminConfigVehicleKinds(t *testing.T) {
	err := (AdminConfig{VehicleKinds: map[string][]string{"bicycle": {"Tandem*"}, "helicopter": {"Heli_*"}}}).Validate()
	require.NoError(t, err)
	err = (AdminConfig{VehicleKinds: map[string][]string{"rocket": {"X"}}}).Validate()
	require.Error(t, err, "an unknown kind must fail: its icon would not exist")
}
