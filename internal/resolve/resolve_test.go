// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package resolve

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/quadlet"
	"github.com/bzed/dayz-server-operator/internal/site"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixture loads the synthetic site repo and a config whose data dir is a
// temp dir, optionally with everything "installed" in the cache.
func fixture(t *testing.T, installed bool) (*config.Config, *site.Tree, string) {
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
			if err := os.MkdirAll(gen, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(p[1], filepath.Join(cfg.Paths.Cache, p[0], "current")); err != nil {
				t.Fatal(err)
			}
		}
	}
	tree, err := site.LoadTree("testdata/site")
	if err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	return cfg, tree, data
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs (run with -update to accept):\n%s", name, got)
	}
}

func render(t *testing.T, inst *Instance, data string) string {
	t.Helper()
	out, err := yaml.Marshal(inst)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if inst.Quadlet.Name != "" {
		unit, err := quadlet.RenderContainer(inst.Quadlet)
		if err != nil {
			t.Fatal(err)
		}
		s += "---\n" + unit
	}
	return strings.ReplaceAll(s, data, "/DATA")
}

func TestGoldenInstalled(t *testing.T) {
	cfg, tree, data := fixture(t, true)
	all, err := ResolveAll(cfg, tree)
	if err != nil {
		t.Fatalf("ResolveAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d instances, want 2", len(all))
	}
	for _, inst := range all {
		if len(inst.Missing) != 0 {
			t.Errorf("%s: unexpected Missing %v", inst.Name, inst.Missing)
		}
		golden(t, inst.Name+".golden", render(t, inst, data))
	}
}

func TestMissingWhenNotInstalled(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	inst, err := Resolve(cfg, tree, "alpha")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(inst.Missing) != 3 || inst.Quadlet.Name != "" {
		t.Errorf("Missing = %v, Quadlet.Name = %q; want 3 missing and no quadlet", inst.Missing, inst.Quadlet.Name)
	}
}

func TestDefaultsMergeAndInstanceWins(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	alpha, _ := Resolve(cfg, tree, "alpha")
	if alpha.Updates.Policy != "auto" || alpha.Updates.CheckInterval == 0 {
		t.Errorf("updates = %+v: instance policy must win, site check_interval must fill in", alpha.Updates)
	}
	if alpha.Container.Env["LANG"] != "C" {
		t.Errorf("container.env not inherited: %v", alpha.Container.Env)
	}
	beta, _ := Resolve(cfg, tree, "beta")
	if beta.Notify.Discord == nil || len(*beta.Notify.Discord) != 0 {
		t.Errorf("notify.discord [] (send nothing) must survive defaults: %v", beta.Notify.Discord)
	}
	if beta.Mission.Drift != site.DriftWarnBackupOverwrite {
		t.Errorf("drift default = %q", beta.Mission.Drift)
	}
}

func TestPresetOverride(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	raw := tree.Instances["alpha"]
	raw.MissionSource.Ref = "v2"
	tree.Instances["alpha"] = raw
	inst, err := Resolve(cfg, tree, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if got := inst.Mission.Source; got.Git == "" || got.Ref != "v2" || got.Path != "dayzOffline.chernarusplus" {
		t.Errorf("source = %+v", got)
	}
}

func TestDefaultMissionSourceIsCentralEconomy(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	raw := tree.Instances["alpha"]
	raw.MissionSource = site.MissionSource{}
	raw.Map = "dayzOffline.enoch"
	tree.Instances["alpha"] = raw
	inst, err := Resolve(cfg, tree, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	want := site.MissionSource{Git: site.CentralEconomyRepo, Ref: "master", Path: "dayzOffline.enoch"}
	if got := inst.Mission.Source; got != want {
		t.Errorf("source = %+v, want %+v", got, want)
	}
	raw.MissionSource = site.MissionSource{Ref: "DZ_1.29"}
	tree.Instances["alpha"] = raw
	inst, _ = Resolve(cfg, tree, "alpha")
	if inst.Mission.Source.Ref != "DZ_1.29" || inst.Mission.Source.Git != site.CentralEconomyRepo {
		t.Errorf("a ref must pin the default repository: %+v", inst.Mission.Source)
	}
}

func TestStopMethods(t *testing.T) {
	cfg, tree, _ := fixture(t, true)
	raw := tree.Instances["alpha"]
	no := false
	raw.Stop = site.StopConfig{Method: site.StopKill, IgnoreAsserts: &no}
	tree.Instances["alpha"] = raw
	inst, err := Resolve(cfg, tree, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	q := inst.Quadlet
	if len(q.ExecStop) != 0 || q.StopTimeout != killStopTimeout {
		t.Errorf("kill: ExecStop=%v StopTimeout=%v, want no shutdown request and a one-second grace", q.ExecStop, q.StopTimeout)
	}
	if len(q.Exec) == 0 || q.Exec[0] != "./DayZServer" {
		t.Errorf("ignore_asserts: false must leave the plain command line: %v", q.Exec)
	}
	raw.Stop = site.StopConfig{}
	tree.Instances["alpha"] = raw
	inst, _ = Resolve(cfg, tree, "alpha")
	q = inst.Quadlet
	if len(q.ExecStop) != 1 || !strings.Contains(q.ExecStop[0], "instance shutdown alpha --timeout 30s") {
		t.Errorf("default must ask over RCon first: %v", q.ExecStop)
	}
	if len(q.Exec) < 3 || q.Exec[0] != "/bin/sh" || !strings.HasSuffix(q.Exec[2], " </stdin") || !strings.Contains(q.Exec[2], "'-mod=@1559212036'") {
		t.Errorf("default must feed the server a stdin that answers the assertion prompt and quote every word: %v", q.Exec)
	}
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
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Resolve() = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestResolveAllPortConflict(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	b := tree.Instances["beta"]
	b.Ports.Query = 2303
	tree.Instances["beta"] = b
	if _, err := ResolveAll(cfg, tree); err == nil || !strings.Contains(err.Error(), "port 2303") {
		t.Fatalf("ResolveAll() = %v, want a port conflict", err)
	}
}

func TestResolveAllPropagatesError(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	delete(cfg.Products, "dayz-experimental")
	if _, err := ResolveAll(cfg, tree); err == nil {
		t.Fatal("want an error")
	}
}

func TestBrokenCurrentLink(t *testing.T) {
	cfg, tree, _ := fixture(t, false)
	// "current" as a regular file makes Readlink fail with a real error.
	dir := filepath.Join(cfg.Paths.Cache, "products", "dayz-stable")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "current"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(cfg, tree, "alpha"); err == nil {
		t.Fatal("want an error for an unreadable current link")
	}
	// the same for a mod
	if err := os.Remove(filepath.Join(dir, "current")); err != nil {
		t.Fatal(err)
	}
	mod := filepath.Join(cfg.Paths.Cache, "workshop", "221100", "1559212036")
	if err := os.MkdirAll(mod, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "current"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(cfg, tree, "alpha"); err == nil {
		t.Fatal("want an error for an unreadable mod current link")
	}
}

func TestQuadletPublishPortsAndMemory(t *testing.T) {
	cfg, tree, _ := fixture(t, true)
	beta, err := Resolve(cfg, tree, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if len(beta.Quadlet.Ports) != 3 || beta.Quadlet.Memory != "24G" {
		t.Errorf("quadlet = %+v", beta.Quadlet)
	}
}

func TestAdminEnabled(t *testing.T) {
	inst := &Instance{Mods: []Mod{{ID: 1}, {Local: AdminName, Server: true}}}
	if !inst.AdminEnabled() {
		t.Error("the local dzo-admin mod enables the integration")
	}
	inst.Admin.Disabled = true
	if inst.AdminEnabled() {
		t.Error("admin.disabled switches it off")
	}
	if (&Instance{Mods: []Mod{{ID: 1}}}).AdminEnabled() {
		t.Error("without the mod there is no integration")
	}
}
