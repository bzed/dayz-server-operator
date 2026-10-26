// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t testing.TB, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func commitAll(t testing.TB, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "x"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
}

// renderSetup builds a data dir with a one-instance site repo whose mission
// source is a local git repo, plus an installed product build that ships a
// fallback mission. It returns the config path and the instance root.
func renderSetup(t testing.TB) (cfg, instRoot, missionRepo string) {
	t.Helper()
	return renderSetupIn(t, t.TempDir())
}

// renderSetupIn is renderSetup with the data directory chosen by the caller.
func renderSetupIn(t testing.TB, data string) (cfg, instRoot, missionRepo string) {
	t.Helper()
	missionRepo = filepath.Join(t.TempDir(), "mission")
	writeFile(t, filepath.Join(missionRepo, "empty.m", "init.c"), "init")
	writeFile(t, filepath.Join(missionRepo, "empty.m", "db", "types.xml"), "types")
	commitAll(t, missionRepo)

	writeFile(t, filepath.Join(data, "site", "instances", "x", "instance.yaml"), `name: x
product: dayz-stable
map: empty.m
mission_source: {git: `+missionRepo+`, ref: main, path: empty.m}
fallback_mission: dayzOffline.fb
ports: {game: 2302, rcon: 2306, query: 27016}
network: host
`)
	writeFile(t, filepath.Join(data, "site", "instances", "x", "serverDZ.cfg"), "hostname = \"x\";\ntemplate = \"stale\";\n")
	build := filepath.Join(data, "cache", "products", "dayz-stable", "1")
	writeFile(t, filepath.Join(build, "mpmissions", "dayzOffline.fb", "cfgweather.xml"), "weather")
	writeFile(t, filepath.Join(build, "keys", "dayz.bikey"), "bikey")
	require.NoError(t, os.Symlink("1", filepath.Join(data, "cache", "products", "dayz-stable", "current")))
	cfg = filepath.Join(data, "config.yaml")
	writeFile(t, cfg, "paths:\n  data: "+data+"\n")
	return cfg, filepath.Join(data, "instances", "x"), missionRepo
}

func TestInstanceRender(t *testing.T) {
	cfg, root, repo := renderSetup(t)
	live := filepath.Join(root, "mpmissions", "empty.m")

	out, err := runCmd(t, "instance", "render", "x", "--config", cfg, "--dry-run")
	require.NoError(t, err, "render --dry-run: %s", out)
	assert.Contains(t, out, "+ init.c (new)", "dry-run output")
	assert.NotContains(t, out, "written:", "dry-run output")
	_, err = os.Stat(live)
	require.ErrorIs(t, err, os.ErrNotExist, "dry-run must not create the live mission")

	out, err = runCmd(t, "instance", "render", "x", "--config", cfg)
	require.NoError(t, err, "render: %s", out)
	assert.Contains(t, out, "written: 4", "init.c, types.xml, cfgweather.xml from the fallback, the generated messages.xml")
	b, _ := os.ReadFile(filepath.Join(live, "db", "messages.xml"))
	assert.NotContains(t, string(b), "<message>", "a render writes an empty messages.xml, dzo sends the messages itself")
	b, _ = os.ReadFile(filepath.Join(live, "cfgweather.xml"))
	assert.Equal(t, "weather", string(b), "fallback file")
	// the files the start needs besides the mission
	b, _ = os.ReadFile(filepath.Join(root, "runtime", "serverDZ.cfg"))
	assert.Contains(t, string(b), `template = "empty.m";`, "runtime/serverDZ.cfg")
	assert.Contains(t, string(b), "steamQueryPort = 27016;", "runtime/serverDZ.cfg")
	b, _ = os.ReadFile(filepath.Join(root, "runtime", "keys", "dayz.bikey"))
	assert.Equal(t, "bikey", string(b), "runtime/keys/dayz.bikey")
	b, _ = os.ReadFile(filepath.Join(root, "profiles", "battleye", "beserver_x64.cfg"))
	assert.Contains(t, string(b), "RConPort 2306", "BattlEye seed")

	// The mission repo moves on; a plain render must not fetch, --update-pristine must.
	writeFile(t, filepath.Join(repo, "empty.m", "init.c"), "init2")
	commitAll2(t, repo)
	out, err = runCmd(t, "instance", "render", "x", "--config", cfg)
	require.NoError(t, err, "plain render: %s", out)
	require.Contains(t, out, "no changes", "plain render")
	out, err = runCmd(t, "instance", "render", "x", "--config", cfg, "--update-pristine")
	require.NoError(t, err, "--update-pristine render: %s", out)
	require.Contains(t, out, "~ init.c (update)", "--update-pristine render")
	b, _ = os.ReadFile(filepath.Join(live, "init.c"))
	assert.Equal(t, "init2", string(b), "init.c")
}

func commitAll2(t testing.TB, dir string) {
	t.Helper()
	c := exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-qam", "y")
	c.Dir = dir
	out, err := c.CombinedOutput()
	require.NoError(t, err, "git commit: %s", out)
}

func TestInstanceRenderFailures(t *testing.T) {
	cfg, _, repo := renderSetup(t)
	_, err := runCmd(t, "instance", "render", "nope", "--config", cfg)
	require.Error(t, err, "unknown instance must fail")
	require.NoError(t, os.RemoveAll(repo))
	_, err = runCmd(t, "instance", "render", "x", "--config", cfg)
	assert.Error(t, err, "unfetchable pristine must fail")
}

func TestInstanceRenderNeedsServerCfgBeforeTouchingTheMission(t *testing.T) {
	cfg, root, _ := renderSetup(t)
	data := filepath.Dir(cfg)
	require.NoError(t, os.Remove(filepath.Join(data, "site", "instances", "x", "serverDZ.cfg")))
	for _, args := range [][]string{{"--dry-run"}, {}} {
		out, err := runCmd(t, append([]string{"instance", "render", "x", "--config", cfg}, args...)...)
		if assert.Error(t, err, "render %v without serverDZ.cfg must fail: %s", args, out) {
			assert.Contains(t, out+err.Error(), "serverDZ.cfg is missing", "render %v without serverDZ.cfg", args)
		}
	}
	_, err := os.Stat(filepath.Join(root, "mpmissions"))
	require.ErrorIs(t, err, os.ErrNotExist, "the live mission must not be touched when the start would fail anyway")
	writeFile(t, filepath.Join(data, "site", "instances", "x", "serverDZ.cfg"), "not a config")
	_, err = runCmd(t, "instance", "render", "x", "--config", cfg)
	assert.Error(t, err, "a serverDZ.cfg that does not parse must fail")
}

// A file removed from instances/<name>/EditorFiles or MapStreamDZE must be gone after the next render.
func TestRenderRemovesDZEFilesThatLeftTheSite(t *testing.T) {
	cfg, root, _ := renderSetup(t)
	instDir := filepath.Join(filepath.Dir(cfg), "site", "instances", "x")
	editor := filepath.Join(root, "mpmissions", "empty.m", "EditorFiles")
	stream := filepath.Join(root, "profiles", "dzo", "mapstream", "DZE")
	for _, d := range []string{"EditorFiles", "MapStreamDZE"} {
		writeFile(t, filepath.Join(instDir, d, "keep.dze"), "K")
		writeFile(t, filepath.Join(instDir, d, "sub", "drop.dze"), "D")
	}

	out, err := runCmd(t, "instance", "render", "x", "--config", cfg)
	require.NoError(t, err, "first render: %s", out)
	for _, d := range []string{editor, stream} {
		require.FileExists(t, filepath.Join(d, "keep.dze"))
		require.FileExists(t, filepath.Join(d, "sub", "drop.dze"))
	}

	for _, d := range []string{"EditorFiles", "MapStreamDZE"} {
		require.NoError(t, os.RemoveAll(filepath.Join(instDir, d, "sub")))
	}
	out, err = runCmd(t, "instance", "render", "x", "--config", cfg)
	require.NoError(t, err, "second render: %s", out)
	for _, d := range []string{editor, stream} {
		assert.FileExists(t, filepath.Join(d, "keep.dze"), d)
		assert.NoDirExists(t, filepath.Join(d, "sub"), "the removed file and its folder must be gone from %s", d)
	}
}
