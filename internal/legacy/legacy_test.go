// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package legacy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/site"
)

// fake is a Source over maps: ref -> path -> content; a content starting with "->" is a link.
type fake map[string]map[string]string

func (f fake) List(ref string) ([]Entry, error) {
	files, ok := f[ref]
	if !ok {
		return nil, os.ErrNotExist
	}
	var out []Entry
	for p, c := range files {
		mode := "100644"
		if strings.HasPrefix(c, "->") {
			mode = "120000"
		}
		out = append(out, Entry{Mode: mode, Path: p})
	}
	return out, nil
}

func (f fake) Show(ref, p string) ([]byte, error) {
	c, ok := f[ref][p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(strings.TrimPrefix(c, "->")), nil
}

const dz = "#!/bin/bash\nparameters=\"-cpuCount=4 -limitFPS=200 -config=${CFG} -port=${port} -freezecheck -BEpath=x -profiles=y -netlog\"\n"

func branchFiles(template, port string) map[string]string {
	return map[string]string{
		"files/serverDZ.cfg":                   "hostname = \"x\";\npassword = \"hunter2\";\npasswordAdmin=\"admin-secret\";\npassword2 = 1;\ntemplate=\"" + template + "\"; // c\n",
		"files/messages.xml":                   "<messages/>",
		"config/containers/server.json":        `{"ports": ["` + port + `:` + port + `/udp", "` + port[:3] + `3:` + port[:3] + `3/udp", "37016:37016/udp"], "volumes": ["/var/log/dayz/x:/profiles/logz/logs", "mods:/mods", "/var/lib/GeoIP:/var/lib/GeoIP:ro"], "environment": {"A": "b"}}`,
		"server/bin/dz":                        dz,
		"files/servermods":                     "LogZ\nCF\n",
		"files/mods/@CF":                       "->1559212036",
		"files/mods/@Types":                    "->2291785308",
		"files/mods/2291785546":                "->2291785308",
		"files/mods/2291785308/xml.env":        "# c\nTYPES=local\nEVENTS=./info/events.xml\nCFGSPAWNABLETYPES=https://example.invalid/s.xml\nINIT=local\nCFGEVENTSPAWNS=local\n",
		"files/mods/2291785308/types.xml":      "<type name=\"A\"/>\n<type name=\"B\"/>",
		"files/mods/2291785308/init.c.chern":   "--- init.c\n+++ init.c.new\n",
		"files/mods/2291785308/start.sh":       "#!/usr/bin/env bash\nxmlstarlet ed -L -d '//x' a.xml\n",
		"files/mods/2291785308/extra.txt":      "x",
		"files/mods/default/map.env":           "MAP=\"d\"\nMPDIR=\"dayzOffline.*\"\nREPO=\"https://example.invalid/ce.git\"\n",
		"files/custom/login-times/globals.xml": "<variables/>",
		"files/custom/README.md":               "readme",
		"files/custom-disabled/old/x.json":     "{}",
		"files/bin/pre_start.sh":               "#!/bin/bash\nmkdir -p /x\nexit 0\n",
	}
}

func convertFake(t *testing.T) *Result {
	t.Helper()
	other := branchFiles("empty.deerisle", "4302")
	other["files/mods/1602372402/map.env"] = "DIR=\"Deer\"\nREPO=\"https://example.invalid/${DIR}.git\"\nMPDIR=\"empty.deerisle\"\n"
	other["files/mods/2291785308/types.xml"] = "<type name=\"DIFFERENT\"/>"
	other["files/bin/pre_start.sh"] = "#!/bin/bash\n/files/bin/update_dayz_weather --lat 1\n"
	other["files/bin/update_dayz_weather"] = "#!/usr/bin/python3\n"
	other["files/custom/login-times/globals.xml"] = "<variables>other</variables>"
	other["files/custom/stamina/cfggameplay.json"] = "{}"
	src := fake{"a": branchFiles("dayzOffline.chernarusplus", "3302"), "b": other, "c": branchFiles("dayzOffline.enoch", "5302")}
	res, err := Convert(src, Options{Refs: []string{"a", "b", "c"}, PortOffset: 100})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestConvertBuildsAValidSite(t *testing.T) {
	res := convertFake(t)
	dir := t.TempDir()
	for n, data := range res.Files {
		p := filepath.Join(dir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := site.LoadTree(dir)
	if err != nil {
		t.Fatalf("the converted site must validate: %v", err)
	}
	if len(tree.Instances) != 3 {
		t.Errorf("instances: %v", tree.InstanceNames())
	}
	inst := tree.Instances["a"]
	if inst.Ports.Game != 3402 || inst.Ports.RCon != 3403 || inst.Ports.Query != 37116 {
		t.Errorf("ports with the offset: %+v", inst.Ports)
	}
	if inst.Params.CPUCount != "4" || inst.Params.LimitFPS == nil || *inst.Params.LimitFPS != 200 || strings.Join(inst.Params.Extra, " ") != "-freezecheck -netlog" {
		t.Errorf("params: %+v", inst.Params)
	}
	if len(inst.Mods) != 2 {
		t.Fatalf("mods: %+v", inst.Mods)
	}
	if inst.Mods[0].ID != 1559212036 || inst.Mods[1].ID != 2291785308 {
		t.Errorf("mods: %+v", inst.Mods)
	}
	if inst.Container.LogzDir != "/var/log/dayz/x" || len(inst.Container.Mounts) != 1 || inst.Container.Env["A"] != "b" {
		t.Errorf("container: %+v", inst.Container)
	}
	if len(inst.Overlays) != 1 || inst.Overlays[0] != "login-times" {
		t.Errorf("overlays: %v", inst.Overlays)
	}
	if inst.MissionSource.Git != "" || inst.FallbackMission != "" {
		t.Errorf("a dayzOffline map uses the default source: %+v", inst.MissionSource)
	}
	b := tree.Instances["b"]
	if b.MissionSource.Git != "https://example.invalid/Deer.git" || b.MissionSource.Path != "empty.deerisle" || b.FallbackMission == "" {
		t.Errorf("b mission: %+v", b)
	}
	if len(b.Hooks.PreStart) != 1 {
		t.Errorf("an active pre_start.sh becomes a hook: %+v", b.Hooks)
	}
	if li, ok := tree.IntegrationFor("a", 2291785546); !ok || li.Mod != 2291785308 {
		t.Errorf("the alias must find the integration: %v %v", li, ok)
	}
}

func TestConvertIntegrationDetails(t *testing.T) {
	res := convertFake(t)
	y := string(res.Files["integrations/mods/2291785308/integration.yaml"])
	for _, want := range []string{
		"types.xml: {source: local, path: files/types.xml}",
		"events.xml: {source: mod, path: \"./info/events.xml\"}",
		"cfgspawnabletypes.xml: {source: url, url: \"https://example.invalid/s.xml\"}",
		"normalize: [eventposdef-root, wrap-root, xml-decl]",
		"post_merge: [hooks/start.sh]",
		"aliases: [2291785546]",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("integration.yaml lacks %q:\n%s", want, y)
		}
	}
	if strings.Contains(y, "init.c:") {
		t.Errorf("an init.c patch the legacy start never applied must not be activated:\n%s", y)
	}
	if _, ok := res.Files["integrations/mods/2291785308/files/init.c.chern"]; !ok {
		t.Error("the init.c file is kept")
	}
	if strings.Contains(y, "cfgeventspawns.xml") {
		t.Errorf("CFGEVENTSPAWNS=local without a file must be dropped:\n%s", y)
	}
	if s := string(res.Files["integrations/mods/2291785308/hooks/start.sh"]); !strings.HasPrefix(s, "#!/usr/bin/env bash\ncd \"$DZO_STAGING\" || exit 1\n") {
		t.Errorf("start.sh: %q", s)
	}
	if _, ok := res.Files["integrations/mods/2291785308/files/extra.txt"]; !ok {
		t.Error("an unused file is kept")
	}
	// b has a different types.xml: shared is the version two branches have, b gets an override
	if s := string(res.Files["integrations/mods/2291785308/files/types.xml"]); !strings.Contains(s, `name="A"`) {
		t.Errorf("shared types.xml: %s", s)
	}
	if s := string(res.Files["instances/b/integrations/2291785308/files/types.xml"]); !strings.Contains(s, "DIFFERENT") {
		t.Errorf("override: %s", s)
	}
	if _, ok := res.Files["instances/a/integrations/2291785308/integration.yaml"]; ok {
		t.Error("a uses the shared integration")
	}
}

func TestConvertOverlaysAndReport(t *testing.T) {
	res := convertFake(t)
	if string(res.Files["overlays/login-times/globals.xml"]) != "<variables/>" {
		t.Error("login-times is the same on two branches: shared")
	}
	if string(res.Files["instances/b/overlays/login-times/globals.xml"]) != "<variables>other</variables>" {
		t.Error("b's own login-times is an override")
	}
	if _, ok := res.Files["instances/b/overlays/stamina/cfggameplay.json"]; !ok {
		t.Error("an overlay only b has is b's own")
	}
	if _, ok := res.Files["overlays/stamina/cfggameplay.json"]; ok {
		t.Error("an overlay one branch has is not shared")
	}
	for _, want := range []string{"## Shared", "## Instance a", "files/custom-disabled/old", "does nothing active", "LogZ", "start.sh became a post_merge hook", "INIT=local did not work in the legacy start"} {
		if !strings.Contains(res.Report, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if strings.Count(res.Report, "mod 2291785308: the integration is not the same") != 1 {
		t.Error("a difference is reported once per mod")
	}
	cfg := string(res.Files["instances/a/serverDZ.cfg"])
	if strings.Contains(cfg, "hunter2") || strings.Contains(cfg, "admin-secret") || strings.Count(cfg, `"CHANGE-ME"`) != 2 || !strings.Contains(cfg, "password2 = 1;") {
		t.Errorf("the passwords must be replaced and nothing else:\n%s", cfg)
	}
	if !strings.Contains(res.Report, "2 password value(s) were replaced") {
		t.Error("the report must say that the passwords were replaced")
	}
	if string(res.Files["instances/a/messages.xml"]) != "<messages/>" || res.Files["instances/a/serverDZ.cfg"] == nil {
		t.Error("serverDZ.cfg and messages.xml are copied")
	}
	if !strings.Contains(string(res.Files["site.yaml"]), "localhost/dzo-runtime:latest") {
		t.Error("site.yaml")
	}
}

func TestConvertErrorsAndHelpers(t *testing.T) {
	if _, err := Convert(fake{}, Options{}); err == nil {
		t.Error("no branch must fail")
	}
	if _, err := Convert(fake{}, Options{Refs: []string{"nope"}}); err == nil {
		t.Error("an unknown branch must fail")
	}
	// a branch without serverDZ.cfg and server.json still converts, and says so
	res, err := Convert(fake{"x": {"files/messages.xml": "m"}}, Options{Refs: []string{"x"}})
	if err != nil || !strings.Contains(res.Report, "files/serverDZ.cfg is missing") {
		t.Fatalf("%v\n%s", err, res.Report)
	}
	if env := parseEnv([]byte("export A=\"x y\"  # c\nB='z'\nC=v # c\nD=${A}1\n\nnot a line\n")); env["A"] != "x y" || env["B"] != "z" || env["C"] != "v" || env["D"] != "x y1" {
		t.Errorf("parseEnv: %v", env)
	}
	if got := rawURL("https://github.com/o/r/blob/main/a%20b/x.xml"); got != "https://raw.githubusercontent.com/o/r/main/a%20b/x.xml" || rawURL("https://example.invalid/x") != "https://example.invalid/x" {
		t.Errorf("rawURL: %s", got)
	}
	if hasActiveCommand([]byte("#!/bin/bash\nX=1\nmkdir -p a\nset -e\n# run x\nexit 0\n")) || !hasActiveCommand([]byte("curl -s x\n")) {
		t.Error("hasActiveCommand")
	}
	if got := string(adaptStartScript([]byte("echo hi\n"))); !strings.HasPrefix(got, "#!/usr/bin/env bash\ncd ") {
		t.Errorf("adaptStartScript without a shebang: %q", got)
	}
}

func TestGitSource(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(repo, "files"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "files", "serverDZ.cfg"), []byte("template=\"dayzOffline.x\";\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("1234", filepath.Join(repo, "files", "link")); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "x")
	g := Git{Repo: repo}
	es, err := g.List("main")
	if err != nil || len(es) != 2 {
		t.Fatalf("%v %v", es, err)
	}
	for _, e := range es {
		if e.Path == "files/link" && e.Mode != "120000" {
			t.Errorf("mode of the link: %s", e.Mode)
		}
	}
	if b, err := g.Show("main", "files/link"); err != nil || string(b) != "1234" {
		t.Errorf("show link: %q %v", b, err)
	}
	if _, err := g.List("nope"); err == nil {
		t.Error("an unknown ref must fail")
	}
}
