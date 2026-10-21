// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package resolve

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/quadlet"
	"github.com/bzed/dayz-server-operator/internal/site"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixture loads the synthetic site repo and a config whose data dir is a
// temp dir, optionally with everything "installed" in the cache.
func fixture(t testing.TB, installed bool) (*config.Config, *site.Tree, string) {
	t.Helper()
	data := t.TempDir()
	cfg := config.Default()
	cfg.Paths = config.Paths{
		Data: data, Instances: data + "/instances", Snapshots: data + "/snapshots",
		Cache: data + "/cache", Secrets: data + "/secrets", DB: data + "/db", Site: "testdata/site",
	}
	if installed {
		for _, p := range [][2]string{
			{"products/dayz-stable", "100"},
			{"products/dayz-experimental", "200"},
			{"workshop/221100/1559212036", "111"},
			{"workshop/221100/1828439124", "222"},
			{"local/dzo-admin", "abcd"},
		} {
			gen := filepath.Join(cfg.Paths.Cache, p[0], p[1])
			require.NoError(t, os.MkdirAll(gen, 0o750))
			require.NoError(t, os.Symlink(p[1], filepath.Join(cfg.Paths.Cache, p[0], "current")))
		}
	}
	tree, err := site.LoadTree("testdata/site")
	require.NoError(t, err)
	return cfg, tree, data
}

func golden(t testing.TB, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), got, "%s differs (run with -update to accept)", name)
}

func render(t testing.TB, inst *Instance, data string) string {
	t.Helper()
	out, err := yaml.Marshal(inst)
	require.NoError(t, err)
	s := string(out)
	if inst.Quadlet.Name != "" {
		unit, err := quadlet.RenderContainer(inst.Quadlet)
		require.NoError(t, err)
		s += "---\n" + unit
	}
	return strings.ReplaceAll(s, data, "/DATA")
}

func TestGoldenInstalled(t *testing.T) {
	cfg, tree, data := fixture(t, true)
	all, err := ResolveAll(cfg, tree)
	require.NoError(t, err)
	require.Len(t, all, 2)
	for _, inst := range all {
		assert.Empty(t, inst.Missing, "%s: unexpected Missing", inst.Name)
		golden(t, inst.Name+".golden", render(t, inst, data))
	}
}

func TestMissingWhenNotInstalled(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	inst, err := Resolve(cfg, tree, "alpha")
	require.NoError(t, err)
	assert.Len(t, inst.Missing, 3, "want 3 missing")
	assert.Empty(t, inst.Quadlet.Name, "want no quadlet")
}

func TestDefaultsMergeAndInstanceWins(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	alpha, _ := Resolve(cfg, tree, "alpha")
	assert.EqualValues(t, "auto", alpha.Updates.Policy, "instance policy must win")
	assert.NotZero(t, alpha.Updates.CheckInterval, "site check_interval must fill in")
	assert.Equal(t, "C", alpha.Container.Env["LANG"], "container.env not inherited")
	beta, _ := Resolve(cfg, tree, "beta")
	require.NotNil(t, beta.Notify.Discord, "notify.discord [] (send nothing) must survive defaults")
	assert.Empty(t, *beta.Notify.Discord, "notify.discord [] (send nothing) must survive defaults")
	assert.Equal(t, site.DriftWarnBackupOverwrite, beta.Mission.Drift, "drift default")
}

func TestPresetOverride(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	raw := tree.Instances["alpha"]
	raw.MissionSource.Ref = "v2"
	tree.Instances["alpha"] = raw
	inst, err := Resolve(cfg, tree, "alpha")
	require.NoError(t, err)
	got := inst.Mission.Source
	assert.NotEmpty(t, got.Git)
	assert.Equal(t, "v2", got.Ref)
	assert.Equal(t, "dayzOffline.chernarusplus", got.Path)
}

func TestDefaultMissionSourceIsCentralEconomy(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	raw := tree.Instances["alpha"]
	raw.MissionSource = site.MissionSource{}
	raw.Map = "dayzOffline.enoch"
	tree.Instances["alpha"] = raw
	inst, err := Resolve(cfg, tree, "alpha")
	require.NoError(t, err)
	want := site.MissionSource{Git: site.CentralEconomyRepo, Ref: "master", Path: "dayzOffline.enoch"}
	assert.Equal(t, want, inst.Mission.Source)
	raw.MissionSource = site.MissionSource{Ref: "DZ_1.29"}
	tree.Instances["alpha"] = raw
	inst, _ = Resolve(cfg, tree, "alpha")
	assert.Equal(t, "DZ_1.29", inst.Mission.Source.Ref, "a ref must pin the default repository")
	assert.Equal(t, site.CentralEconomyRepo, inst.Mission.Source.Git, "a ref must pin the default repository")
}

func TestStopMethods(t *testing.T) {
	cfg, tree, _ := fixture(t, true)
	raw := tree.Instances["alpha"]
	no := false
	raw.Stop = site.StopConfig{Method: site.StopKill, IgnoreAsserts: &no}
	tree.Instances["alpha"] = raw
	inst, err := Resolve(cfg, tree, "alpha")
	require.NoError(t, err)
	q := inst.Quadlet
	assert.Empty(t, q.ExecStop, "kill: no shutdown request")
	assert.Equal(t, killStopTimeout, q.StopTimeout, "kill: a one-second grace")
	require.NotEmpty(t, q.Exec, "ignore_asserts: false must leave the plain command line")
	assert.Equal(t, "./DayZServer", q.Exec[0], "ignore_asserts: false must leave the plain command line")
	raw.Stop = site.StopConfig{}
	tree.Instances["alpha"] = raw
	inst, _ = Resolve(cfg, tree, "alpha")
	q = inst.Quadlet
	require.Len(t, q.ExecStop, 1, "default must ask over RCon first")
	assert.Contains(t, q.ExecStop[0], "instance shutdown alpha --timeout 30s", "default must ask over RCon first")
	require.GreaterOrEqual(t, len(q.Exec), 3, "default must feed the server a stdin that answers the assertion prompt and quote every word: %v", q.Exec)
	assert.Equal(t, "/bin/sh", q.Exec[0])
	assert.True(t, strings.HasSuffix(q.Exec[2], " </stdin"), "stdin: %v", q.Exec)
	assert.Contains(t, q.Exec[2], "'-mod=@1559212036'")
}

func TestResolveErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config.Config, *site.Tree)
		want   string
	}{
		{"no instance", func(_ *config.Config, t *site.Tree) { delete(t.Instances, "alpha") }, "no instance"},
		{"unknown product", func(c *config.Config, _ *site.Tree) { delete(c.Products, "dayz-stable") }, "unknown product"},
		{"unknown preset", func(_ *config.Config, t *site.Tree) { delete(t.Maps, "vanilla") }, "unknown mission preset"},
		{"missing overlay", func(_ *config.Config, t *site.Tree) {
			i := t.Instances["alpha"]
			i.Overlays = []string{"nope"}
			t.Instances["alpha"] = i
		}, "overlay \"nope\""},
		{"unknown local mod", func(_ *config.Config, t *site.Tree) {
			i := t.Instances["alpha"]
			i.Mods = []site.ModRef{{Local: "ghost", Server: true}}
			t.Instances["alpha"] = i
		}, "local mod \"ghost\""},
		{"bad mount", func(_ *config.Config, t *site.Tree) {
			i := t.Instances["alpha"]
			i.Container.Mounts = []string{"nodest"}
			t.Instances["alpha"] = i
		}, "src:dst"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, tree, _ := fixture(t, false)
			tt.mutate(cfg, tree)
			_, err := Resolve(cfg, tree, "alpha")
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestResolveAllPortConflict(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	b := tree.Instances["beta"]
	b.Ports.Query = 2303
	tree.Instances["beta"] = b
	_, err := ResolveAll(cfg, tree)
	require.ErrorContains(t, err, "port 2303", "want a port conflict")
}

func TestResolveAllPropagatesError(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	delete(cfg.Products, "dayz-experimental")
	_, err := ResolveAll(cfg, tree)
	require.Error(t, err)
}

func TestBrokenCurrentLink(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	// "current" as a regular file makes Readlink fail with a real error.
	dir := filepath.Join(cfg.Paths.Cache, "products", "dayz-stable")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "current"), nil, 0o600))
	_, err := Resolve(cfg, tree, "alpha")
	require.Error(t, err, "want an error for an unreadable current link")
	// the same for a mod
	require.NoError(t, os.Remove(filepath.Join(dir, "current")))
	mod := filepath.Join(cfg.Paths.Cache, "workshop", "221100", "1559212036")
	require.NoError(t, os.MkdirAll(mod, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(mod, "current"), nil, 0o600))
	_, err = Resolve(cfg, tree, "alpha")
	require.Error(t, err, "want an error for an unreadable mod current link")
}

func TestQuadletPublishPortsAndMemory(t *testing.T) {
	cfg, tree, _ := fixture(t, true)
	beta, err := Resolve(cfg, tree, "beta")
	require.NoError(t, err)
	assert.Len(t, beta.Quadlet.Ports, 3)
	assert.Equal(t, "24G", beta.Quadlet.Memory)
}

func TestAdminEnabled(t *testing.T) {
	inst := &Instance{Mods: []Mod{{ID: 1}, {Local: AdminName, Server: true}}}
	assert.True(t, inst.AdminEnabled(), "the local dzo-admin mod enables the integration")
	inst.Admin.Disabled = true
	assert.False(t, inst.AdminEnabled(), "admin.disabled switches it off")
	assert.False(t, (&Instance{Mods: []Mod{{ID: 1}}}).AdminEnabled(), "without the mod there is no integration")
}

func TestHooksBecomeUnitLines(t *testing.T) {
	cfg, tree, _ := fixture(t, true)
	raw := tree.Instances["alpha"]
	raw.Hooks = site.HooksConfig{PreStart: []string{"hooks/a.sh"}, PostStop: []string{"hooks/b.sh"}}
	tree.Instances["alpha"] = raw
	inst, err := Resolve(cfg, tree, "alpha")
	require.NoError(t, err)
	q := inst.Quadlet
	require.Len(t, q.PreStart, 3, "pre_start must follow the render")
	assert.Equal(t, "/usr/bin/dzo instance hook alpha pre_start", q.PreStart[2])
	assert.Equal(t, "/usr/bin/dzo instance render alpha", q.PreStart[1])
	require.Len(t, q.PostStop, 2)
	assert.Equal(t, "/usr/bin/dzo instance hook alpha post_stop", q.PostStop[1])
	assert.Equal(t, "/usr/bin/dzo logs crash-summary alpha", q.PostStop[0])
	raw.Hooks = site.HooksConfig{}
	tree.Instances["alpha"] = raw
	inst, _ = Resolve(cfg, tree, "alpha")
	assert.Len(t, inst.Quadlet.PreStart, 2, "no hooks, no extra lines")
	assert.Len(t, inst.Quadlet.PostStop, 1, "no hooks, no extra lines")
}

func TestInstanceIsPinnedToItsBuild(t *testing.T) {
	cfg, tree, _ := fixture(t, true)
	require.NoError(t, os.MkdirAll(filepath.Join(cfg.Paths.Cache, "products", "dayz-stable", "99"), 0o750))
	runtime := filepath.Join(cfg.Paths.Instances, "alpha", "runtime")
	build := func() (string, string) {
		inst, err := Resolve(cfg, tree, "alpha")
		require.NoError(t, err)
		return inst.Product.Build, inst.Product.Latest
	}
	b, l := build()
	assert.Equal(t, "100", b, "without a pin the newest build runs")
	assert.Equal(t, "100", l, "without a pin the newest build runs")
	require.NoError(t, PinBuild(runtime, "99"))
	b, l = build()
	assert.Equal(t, "99", b, "pinned: build")
	assert.Equal(t, "100", l, "pinned: latest")
	require.NoError(t, PinBuild(runtime, "gone"))
	b, _ = build()
	assert.Equal(t, "100", b, "a pin to a build that is not installed any more falls back to the newest")
	require.NoError(t, os.Remove(BuildPinFile(runtime)))
	require.NoError(t, os.WriteFile(filepath.Join(runtime, "generations.json"), []byte(`{"build":"99","mods":{}}`), 0o600))
	b, _ = build()
	assert.Equal(t, "99", b, "an instance deployed before the pin existed stays on its applied build")
}

func TestUnitsUseTheConfiguredBinary(t *testing.T) {
	cfg, tree, _ := fixture(t, true)
	cfg.Binary = "/opt/dzo/bin/dzo"
	inst, err := Resolve(cfg, tree, "alpha")
	require.NoError(t, err)
	q := inst.Quadlet
	require.NotEmpty(t, q.PreStart)
	assert.Equal(t, "/opt/dzo/bin/dzo instance render alpha", q.PreStart[len(q.PreStart)-1])
	require.NotEmpty(t, q.Volumes)
	assert.Equal(t, "/opt/dzo/bin/dzo", q.Volumes[0].Source)
}

func TestRConBind(t *testing.T) {
	cfg, tree, _ := fixture(t, true)
	alpha, _ := Resolve(cfg, tree, "alpha") // network: host
	beta, _ := Resolve(cfg, tree, "beta")   // network: publish
	rconPort := func(i *Instance) quadlet.Port {
		for _, p := range i.Quadlet.Ports {
			if p.ContainerPort == i.Ports.RCon {
				return p
			}
		}
		return quadlet.Port{}
	}

	// the default is the loopback address, for both network modes
	assert.Equal(t, "127.0.0.1:"+strconv.Itoa(alpha.Ports.RCon), alpha.RConAddr(), "host default")
	assert.Equal(t, "127.0.0.1", alpha.RConListen(), "host default")
	assert.Empty(t, beta.RConListen(), "publish default")
	assert.Equal(t, "127.0.0.1", rconPort(beta).HostIP, "publish default")

	// every interface: BattlEye gets no RConIP, the port is published everywhere, dzo uses loopback
	tree.Site.Defaults.RConBind = "0.0.0.0"
	alpha, _ = Resolve(cfg, tree, "alpha")
	beta, _ = Resolve(cfg, tree, "beta")
	assert.Empty(t, alpha.RConListen(), "host, all interfaces")
	assert.Equal(t, "127.0.0.1:"+strconv.Itoa(alpha.Ports.RCon), alpha.RConAddr(), "host, all interfaces")
	assert.Empty(t, rconPort(beta).HostIP, "publish, all interfaces")

	// one address: BattlEye listens there, and dzo has to connect there
	tree.Site.Defaults.RConBind = "192.0.2.7"
	alpha, _ = Resolve(cfg, tree, "alpha")
	beta, _ = Resolve(cfg, tree, "beta")
	assert.Equal(t, "192.0.2.7", alpha.RConListen(), "host, one address")
	assert.Equal(t, "192.0.2.7:"+strconv.Itoa(alpha.Ports.RCon), alpha.RConAddr(), "host, one address")
	assert.Empty(t, beta.RConListen(), "publish, one address")
	assert.Equal(t, "192.0.2.7", rconPort(beta).HostIP, "publish, one address")
	assert.Equal(t, "192.0.2.7:"+strconv.Itoa(beta.Ports.RCon), beta.RConAddr(), "publish, one address")

	// the instance wins over the site default
	raw := tree.Instances["alpha"]
	raw.RConBind = "::1"
	tree.Instances["alpha"] = raw
	alpha, _ = Resolve(cfg, tree, "alpha")
	assert.Equal(t, "[::1]:"+strconv.Itoa(alpha.Ports.RCon), alpha.RConAddr(), "instance rcon_bind")
}

func TestAdminMapWatchRulesMergeWithTheSiteDefaults(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	tree.Site.Defaults.AdminMap.Watch = []admin.WatchRule{{Layer: "fires", Classes: []string{"FireplaceBase"}, Icon: "fire"}}
	raw := tree.Instances["alpha"]
	raw.AdminMap.Watch = []admin.WatchRule{{Layer: "ufo", Classes: []string{"UFO*"}, Icon: "rocket"}}
	tree.Instances["alpha"] = raw
	alpha, err := Resolve(cfg, tree, "alpha")
	require.NoError(t, err)
	w := alpha.AdminMap.Watch
	require.Len(t, w, 2, "alpha watch: the default rule and the instance's own")
	assert.Equal(t, "fires", w[0].Layer)
	assert.Equal(t, "ufo", w[1].Layer)
	beta, _ := Resolve(cfg, tree, "beta")
	w = beta.AdminMap.Watch
	require.Len(t, w, 1, "beta watch: only the default")
	assert.Equal(t, "fires", w[0].Layer)
}

func TestMessagesDefaultsAndInstanceWins(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	empty := ""
	own := "Own welcome"
	tree.Site.Defaults.Welcome = &own
	tree.Site.Defaults.Messages = []site.Message{{Text: "Site info", Repeat: 30}}
	raw := tree.Instances["beta"]
	raw.Welcome = &empty
	raw.Messages = []site.Message{{Text: "Beta info", Repeat: 10}}
	tree.Instances["beta"] = raw

	alpha, err := Resolve(cfg, tree, "alpha")
	require.NoError(t, err)
	require.NotNil(t, alpha.Welcome, "the site defaults fill in")
	assert.Equal(t, "Own welcome", *alpha.Welcome, "the site defaults fill in")
	require.Len(t, alpha.Messages, 1, "the site defaults fill in")
	assert.Equal(t, "Site info", alpha.Messages[0].Text)
	beta, err := Resolve(cfg, tree, "beta")
	require.NoError(t, err)
	require.NotNil(t, beta.Welcome, "its own welcome (even empty) and list replace the defaults")
	assert.Empty(t, *beta.Welcome, "its own welcome (even empty) replaces the default")
	require.Len(t, beta.Messages, 1, "its own list replaces the defaults")
	assert.Equal(t, "Beta info", beta.Messages[0].Text)
}

func TestKillfeedUnknownWebhook(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	cfg.Notify.Discord = []config.DiscordWebhook{{Name: "pvp", URL: "https://example.invalid/hook"}}
	alpha := tree.Instances["alpha"]
	alpha.Killfeed = site.KillfeedConfig{Discord: []string{"pvp"}}
	tree.Instances["alpha"] = alpha
	inst, err := Resolve(cfg, tree, "alpha")
	require.NoError(t, err)
	require.Equal(t, []string{"pvp"}, inst.Killfeed.Discord)

	alpha.Killfeed = site.KillfeedConfig{Discord: []string{"pvp", "nowhere"}}
	tree.Instances["alpha"] = alpha
	_, err = Resolve(cfg, tree, "alpha")
	require.Error(t, err)
	require.Contains(t, err.Error(), `"nowhere"`)
	require.Contains(t, err.Error(), "killfeed.discord")
}
