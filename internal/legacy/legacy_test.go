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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func convertFake(t testing.TB) *Result {
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
	require.NoError(t, err)
	return res
}

func TestConvertBuildsAValidSite(t *testing.T) {
	res := convertFake(t)
	dir := t.TempDir()
	for n, data := range res.Files {
		p := filepath.Join(dir, filepath.FromSlash(n))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, data, 0o600))
	}
	tree, err := site.LoadTree(dir)
	require.NoError(t, err, "the converted site must validate")
	assert.Len(t, tree.Instances, 3, "instances: %v", tree.InstanceNames())
	inst := tree.Instances["a"]
	assert.Equal(t, 3402, inst.Ports.Game, "ports with the offset")
	assert.Equal(t, 3403, inst.Ports.RCon, "ports with the offset")
	assert.Equal(t, 37116, inst.Ports.Query, "ports with the offset")
	assert.Equal(t, "4", inst.Params.CPUCount)
	require.NotNil(t, inst.Params.LimitFPS)
	assert.Equal(t, 200, *inst.Params.LimitFPS)
	assert.Equal(t, []string{"-freezecheck", "-netlog"}, inst.Params.Extra)
	require.Len(t, inst.Mods, 2)
	assert.EqualValues(t, 1559212036, inst.Mods[0].ID)
	assert.EqualValues(t, 2291785308, inst.Mods[1].ID)
	assert.Equal(t, "/var/log/dayz/x", inst.Container.LogzDir)
	assert.Len(t, inst.Container.Mounts, 1)
	assert.Equal(t, "b", inst.Container.Env["A"])
	assert.Equal(t, []string{"login-times"}, inst.Overlays)
	assert.Empty(t, inst.MissionSource.Git, "a dayzOffline map uses the default source")
	b := tree.Instances["b"]
	assert.Equal(t, "https://example.invalid/Deer.git", b.MissionSource.Git)
	assert.Equal(t, "empty.deerisle", b.MissionSource.Path)
	assert.Empty(t, b.FallbackMission)
	assert.Len(t, b.Hooks.PreStart, 1, "an active pre_start.sh becomes a hook")
	li, ok := tree.IntegrationFor("a", 2291785546)
	require.True(t, ok, "the alias must find the integration")
	assert.EqualValues(t, 2291785308, li.Mod)
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
		assert.Contains(t, y, want, "integration.yaml")
	}
	assert.NotContains(t, y, "init.c:", "an init.c patch the legacy start never applied must not be activated")
	assert.Contains(t, res.Files, "integrations/mods/2291785308/files/init.c.chern", "the init.c file is kept")
	assert.NotContains(t, y, "cfgeventspawns.xml", "CFGEVENTSPAWNS=local without a file must be dropped")
	s := string(res.Files["integrations/mods/2291785308/hooks/start.sh"])
	assert.True(t, strings.HasPrefix(s, "#!/usr/bin/env bash\ncd \"$DZO_STAGING\" || exit 1\n"), "start.sh: %q", s)
	assert.Contains(t, res.Files, "integrations/mods/2291785308/files/extra.txt", "an unused file is kept")
	// b has a different types.xml: shared is the version two branches have, b gets an override
	assert.Contains(t, string(res.Files["integrations/mods/2291785308/files/types.xml"]), `name="A"`, "shared types.xml")
	assert.Contains(t, string(res.Files["instances/b/integrations/2291785308/files/types.xml"]), "DIFFERENT", "override")
	assert.NotContains(t, res.Files, "instances/a/integrations/2291785308/integration.yaml", "a uses the shared integration")
}

func TestConvertOverlaysAndReport(t *testing.T) {
	res := convertFake(t)
	assert.Equal(t, "<variables/>", string(res.Files["overlays/login-times/globals.xml"]), "login-times is the same on two branches: shared")
	assert.Equal(t, "<variables>other</variables>", string(res.Files["instances/b/overlays/login-times/globals.xml"]), "b's own login-times is an override")
	assert.Contains(t, res.Files, "instances/b/overlays/stamina/cfggameplay.json", "an overlay only b has is b's own")
	assert.NotContains(t, res.Files, "overlays/stamina/cfggameplay.json", "an overlay one branch has is not shared")
	for _, want := range []string{"## Shared", "## Instance a", "files/custom-disabled/old", "does nothing active", "LogZ", "start.sh became a post_merge hook", "INIT=local did not work in the legacy start"} {
		assert.Contains(t, res.Report, want, "report")
	}
	assert.Equal(t, 1, strings.Count(res.Report, "mod 2291785308: the integration is not the same"), "a difference is reported once per mod")
	cfg := string(res.Files["instances/a/serverDZ.cfg"])
	assert.NotContains(t, cfg, "hunter2", "the passwords must be replaced")
	assert.NotContains(t, cfg, "admin-secret", "the passwords must be replaced")
	assert.Equal(t, 2, strings.Count(cfg, `"CHANGE-ME"`), "the passwords must be replaced:\n%s", cfg)
	assert.Contains(t, cfg, "password2 = 1;", "nothing else may be replaced")
	assert.Contains(t, res.Report, "2 password value(s) were replaced", "the report must say that the passwords were replaced")
	assert.Nil(t, res.Files["instances/a/messages.xml"], "messages.xml is not copied")
	assert.NotNil(t, res.Files["instances/a/serverDZ.cfg"], "serverDZ.cfg is copied")
	assert.Contains(t, res.Report, "files/messages.xml is not copied", "the report says so")
	assert.Contains(t, string(res.Files["site.yaml"]), "localhost/dzo-runtime:latest", "site.yaml")
}

func TestConvertErrorsAndHelpers(t *testing.T) {
	_, err := Convert(fake{}, Options{})
	assert.Error(t, err, "no branch must fail")
	_, err = Convert(fake{}, Options{Refs: []string{"nope"}})
	assert.Error(t, err, "an unknown branch must fail")
	// a branch without serverDZ.cfg and server.json still converts, and says so
	res, err := Convert(fake{"x": {"files/messages.xml": "m"}}, Options{Refs: []string{"x"}})
	require.NoError(t, err)
	assert.Contains(t, res.Report, "files/serverDZ.cfg is missing")
	env := parseEnv([]byte("export A=\"x y\"  # c\nB='z'\nC=v # c\nD=${A}1\n\nnot a line\n"))
	assert.Equal(t, "x y", env["A"], "parseEnv")
	assert.Equal(t, "z", env["B"], "parseEnv")
	assert.Equal(t, "v", env["C"], "parseEnv")
	assert.Equal(t, "x y1", env["D"], "parseEnv")
	assert.Equal(t, "https://raw.githubusercontent.com/o/r/main/a%20b/x.xml", rawURL("https://github.com/o/r/blob/main/a%20b/x.xml"))
	assert.Equal(t, "https://example.invalid/x", rawURL("https://example.invalid/x"))
	assert.False(t, hasActiveCommand([]byte("#!/bin/bash\nX=1\nmkdir -p a\nset -e\n# run x\nexit 0\n")), "hasActiveCommand")
	assert.True(t, hasActiveCommand([]byte("curl -s x\n")), "hasActiveCommand")
	got := string(adaptStartScript([]byte("echo hi\n")))
	assert.True(t, strings.HasPrefix(got, "#!/usr/bin/env bash\ncd "), "adaptStartScript without a shebang: %q", got)
}

func TestGitSource(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
		out, err := c.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run("init", "-q", "-b", "main")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "files"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "files", "serverDZ.cfg"), []byte("template=\"dayzOffline.x\";\n"), 0o600))
	require.NoError(t, os.Symlink("1234", filepath.Join(repo, "files", "link")))
	run("add", "-A")
	run("commit", "-q", "-m", "x")
	g := Git{Repo: repo}
	es, err := g.List("main")
	require.NoError(t, err)
	require.Len(t, es, 2)
	for _, e := range es {
		if e.Path == "files/link" {
			assert.Equal(t, "120000", e.Mode, "mode of the link")
		}
	}
	b, err := g.Show("main", "files/link")
	require.NoError(t, err, "show link")
	assert.Equal(t, "1234", string(b))
	_, err = g.List("nope")
	assert.Error(t, err, "an unknown ref must fail")
}
