// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/product"
	"github.com/bzed/dayz-server-operator/internal/servermods"
)

// buildPBO is a minimal PBO with a prefix header and one file.
func buildPBO(prefix string) []byte {
	var b bytes.Buffer
	str := func(s string) { b.WriteString(s); b.WriteByte(0) }
	u32 := func(v uint32) { _ = binary.Write(&b, binary.LittleEndian, v) }
	str("")
	u32(0x56657273)
	for i := 0; i < 4; i++ {
		u32(0)
	}
	str("prefix")
	str(prefix)
	str("")
	str("config.cpp")
	u32(0)
	u32(3)
	u32(0)
	u32(0)
	u32(3)
	str("")
	for i := 0; i < 5; i++ {
		u32(0)
	}
	b.WriteString("abc")
	return b.Bytes()
}

// fakeSteamcmd is a scripted steamcmd, see internal/product's fixtures.
const fakeSteamcmd = `#!/bin/sh
echo "$*" >> "$FAKE/calls"
echo "Waiting for user info...OK"
dir=""
while [ $# -gt 0 ]; do
  case "$1" in
    +force_install_dir) dir="$2"; shift ;;
    +app_update)
      app="$2"; shift
      mkdir -p "$dir/steamapps"
      printf '"AppState"\n{\n\t"buildid"\t\t"%s"\n}\n' "$(cat "$FAKE/buildid")" > "$dir/steamapps/appmanifest_$app.acf"
      echo "Success! App '$app' fully installed." ;;
    +workshop_download_item)
      app="$2"; id="$3"; shift 2
      if [ -d "$FAKE/mod/$id" ]; then
        mkdir -p "$dir/steamapps/workshop/content/$app/$id"; cp -R "$FAKE/mod/$id/." "$dir/steamapps/workshop/content/$app/$id/"
        echo "Success. Downloaded item $id to \"$dir\" (1 bytes)"
      else
        echo "ERROR! Download item $id failed (Failure)."
      fi ;;
  esac
  shift
done
`

type stubDetails map[uint64]product.FileDetails

func (s stubDetails) get(_ context.Context, ids []uint64) (map[uint64]product.FileDetails, error) {
	out := map[uint64]product.FileDetails{}
	for _, id := range ids {
		if d, ok := s[id]; ok {
			out[id] = d
		}
	}
	return out, nil
}

type installEnv struct {
	cfg, steamcmd, fake, data string
	details                   stubDetails
}

// newInstallEnv builds a config with a steam account, a site with one
// instance (workshop mod 111, local servermod "tools") and a fake steamcmd.
func newInstallEnv(t testing.TB) *installEnv {
	t.Helper()
	e := &installEnv{data: t.TempDir(), fake: t.TempDir(), details: stubDetails{111: {PublishedFileID: 111, Result: 1, TimeUpdated: 10}}}
	t.Setenv("FAKE", e.fake)
	e.steamcmd = filepath.Join(t.TempDir(), "steamcmd")
	writeFile(t, e.steamcmd, fakeSteamcmd)
	require.NoError(t, os.Chmod(e.steamcmd, 0o755)) //nolint:gosec // fixture
	writeFile(t, filepath.Join(e.fake, "buildid"), "500")
	writeFile(t, filepath.Join(e.fake, "mod", "111", "meta.cpp"), "x")
	writeFile(t, filepath.Join(e.fake, "mod", "111", "addons", "a.pbo"), string(buildPBO("p/a")))
	writeFile(t, filepath.Join(e.fake, "mod", "222", "meta.cpp"), "x")
	writeFile(t, filepath.Join(e.fake, "mod", "222", "addons", "b.pbo"), string(buildPBO("p/b")))

	e.cfg = filepath.Join(e.data, "config.yaml")
	writeFile(t, e.cfg, "paths:\n  data: "+e.data+"\nsteam:\n  account: bob\n")
	writeFile(t, filepath.Join(e.data, "site", "instances", "x", "instance.yaml"), `# x
name: x
product: dayz-stable
map: m
mission_source: {git: g, ref: r, path: p}
ports: {game: 2302}
network: host
mods:
  - {id: 111}
  - {local: tools, server: true}
`)
	writeFile(t, filepath.Join(e.data, "site", "localmods", "tools", "addons", "t.pbo"), string(buildPBO("p/t")))

	old := steamDetails
	steamDetails = e.details.get
	t.Cleanup(func() { steamDetails = old })
	return e
}

func (e *installEnv) run(t testing.TB, args ...string) (string, error) {
	t.Helper()
	if args[1] == "list" { // read-only, no steamcmd
		return runCmd(t, append(args, "--config", e.cfg)...)
	}
	return runCmd(t, append(args, "--config", e.cfg, "--steamcmd", e.steamcmd)...)
}

func (e *installEnv) downloads(t testing.TB) int {
	data, _ := os.ReadFile(filepath.Join(e.fake, "calls"))
	return strings.Count(string(data), "workshop_download_item") + strings.Count(string(data), "app_update")
}

func TestProductInstallAndUpdate(t *testing.T) {
	e := newInstallEnv(t)
	out, err := e.run(t, "product", "update", "dayz-stable")
	require.Error(t, err, "update before install must fail:\n%s", out)
	out, err = e.run(t, "product", "install", "dayz-stable")
	require.NoError(t, err, "install: %s", out)
	require.Contains(t, out, "build 500 installed", "install")
	_, err = e.run(t, "product", "install", "dayz-stable")
	require.Error(t, err, "a second install must fail")
	out, err = e.run(t, "product", "update", "dayz-stable")
	require.NoError(t, err, "update: %s", out)
	require.Contains(t, out, "already stored", "update")
	writeFile(t, filepath.Join(e.fake, "buildid"), "501")
	out, err = e.run(t, "product", "update", "dayz-stable")
	require.NoError(t, err, "update to a new build: %s", out)
	require.Contains(t, out, "build 501 installed", "update to a new build")
	out, err = e.run(t, "product", "update", "dayz-stable", "--force")
	require.NoError(t, err, "forced update: %s", out)
	require.Contains(t, out, "501-r1", "forced update")
	_, err = e.run(t, "product", "install", "nope")
	assert.Error(t, err, "an unknown product must fail")
}

func TestProductInstallNeedsSteamAccount(t *testing.T) {
	e := newInstallEnv(t)
	writeFile(t, e.cfg, "paths:\n  data: "+e.data+"\n")
	_, err := e.run(t, "product", "install", "dayz-stable")
	assert.ErrorContains(t, err, "steam.account")
}

func TestModUpdateListRefresh(t *testing.T) {
	e := newInstallEnv(t)
	out, err := e.run(t, "mod", "list")
	require.NoError(t, err, "list before install: %s", out)
	require.Equal(t, 2, strings.Count(out, "not installed"), "list before install: %s", out)

	out, err = e.run(t, "mod", "update")
	require.NoError(t, err, "update: %s", out)
	require.Contains(t, out, "111", "update")
	require.Contains(t, out, "installed 10", "update")
	require.Contains(t, out, "tools", "update")
	out, err = e.run(t, "mod", "list", "x")
	require.NoError(t, err, "list after install: %s", out)
	require.NotContains(t, out, "not installed", "list after install")
	require.Contains(t, out, "@111\tclient\t10", "list after install")

	// generations survive re-runs: nothing is downloaded again
	n := e.downloads(t)
	out, err = e.run(t, "mod", "update", "x")
	require.NoError(t, err, "re-run: %s", out)
	require.Equal(t, 2, strings.Count(out, "up to date"), "re-run: %s", out)
	require.Equal(t, n, e.downloads(t), "re-run downloads again")

	// refresh needs --force and a selection
	_, err = e.run(t, "mod", "refresh", "111")
	assert.Error(t, err, "refresh without --force must fail")
	_, err = e.run(t, "mod", "refresh", "--force")
	assert.Error(t, err, "refresh without ids or --all must fail")
	_, err = e.run(t, "mod", "refresh", "999", "--force")
	assert.ErrorContains(t, err, "not used", "unknown ref")
	out, err = e.run(t, "mod", "refresh", "111", "--force")
	require.NoError(t, err, "refresh: %s", out)
	require.Contains(t, out, "installed 10-r1", "refresh")
	out, err = e.run(t, "mod", "refresh", "--all", "--instance", "x", "--force")
	require.NoError(t, err, "refresh --all: %s", out)
	require.Contains(t, out, "10-r2", "refresh --all")
	_, err = e.run(t, "mod", "update", "nope")
	assert.Error(t, err, "an unknown instance must fail")
	_, err = e.run(t, "mod", "list", "nope")
	assert.Error(t, err, "an unknown instance must fail")
}

func TestModUpdateReportsFailures(t *testing.T) {
	e := newInstallEnv(t)
	e.details[333] = product.FileDetails{PublishedFileID: 333, Result: 1, TimeUpdated: 1} // no fixture: steamcmd fails
	writeFile(t, filepath.Join(e.data, "site", "instances", "x", "instance.yaml"), strings.Replace(
		readFile(t, filepath.Join(e.data, "site", "instances", "x", "instance.yaml")), "  - {id: 111}", "  - {id: 111}\n  - {id: 333}", 1))
	out, err := e.run(t, "mod", "update")
	require.Error(t, err, "one failure must fail the command: %s", out)
	require.Contains(t, out, "FAILED", "update (one failure must not stop the others)")
	require.Contains(t, out, "installed 10", "update (one failure must not stop the others)")
}

func readFile(t testing.TB, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	return string(b)
}

func TestModAdd(t *testing.T) {
	e := newInstallEnv(t)
	inst := filepath.Join(e.data, "site", "instances", "x", "instance.yaml")

	out, err := e.run(t, "mod", "add", "222", "--instance", "x", "--server")
	require.Error(t, err, "a mod Steam does not know must not be added:\n%s", out)
	require.NotContains(t, readFile(t, inst), "222", "a failed install must leave instance.yaml alone")

	e.details[222] = product.FileDetails{PublishedFileID: 222, Result: 1, TimeUpdated: 20}
	out, err = e.run(t, "mod", "add", "222", "--instance", "x", "--server")
	require.NoError(t, err, "add: %s", out)
	require.Contains(t, out, "installed 20", "add")
	got := readFile(t, inst)
	assert.Contains(t, got, "- {id: 222, server: true}", "instance.yaml")
	assert.Contains(t, got, "# x", "instance.yaml")
	_, err = e.run(t, "mod", "add", "222", "--instance", "x")
	assert.Error(t, err, "adding the same mod twice must fail")
	_, err = e.run(t, "mod", "add", "ghost", "--instance", "x")
	assert.Error(t, err, "an unknown local mod must fail")
	_, err = e.run(t, "mod", "add", "tools", "--instance", "nope")
	assert.Error(t, err, "an unknown instance must fail")
	out, err = e.run(t, "mod", "list", "x")
	assert.NoError(t, err, "list: %s", out)
	assert.Contains(t, out, "@222\tserver\t20", "list")
}

func TestModUpdateRefusesAShippedServermodThatDiffersFromCompat(t *testing.T) {
	e := newInstallEnv(t)
	writeFile(t, filepath.Join(e.data, "site", "localmods", servermods.CompatFile),
		"dzo: test\nservermods:\n  tools:\n    commit: abc\n    pbos:\n      addons/t.pbo: "+strings.Repeat("0", 64)+"\n")

	out, err := e.run(t, "mod", "update", "x")
	require.Error(t, err, "a servermod that differs from compat.yaml must be refused: %s", out)
	require.Contains(t, out+err.Error(), "differs from the tested set", "a servermod that differs from compat.yaml must be refused")
	out, err = e.run(t, "mod", "update", "x", "--ignore-compat")
	require.NoError(t, err, "--ignore-compat must allow it: %s", out)
	require.Contains(t, out, "tools", "--ignore-compat must allow it")
}

func TestModAddDebugClient(t *testing.T) {
	e := newInstallEnv(t)
	inst := filepath.Join(e.data, "site", "instances", "x", "instance.yaml")
	mod := filepath.Join(e.data, "site", "localmods", "dbg")
	writeFile(t, filepath.Join(mod, "addons", "d.pbo"), string(buildPBO("p/d")))
	writeFile(t, filepath.Join(mod, "keys", "Dbg.bikey"), "public key")

	// --client alone is refused, with the reason; so are the combinations that make no sense
	_, err := e.run(t, "mod", "add", "dbg", "--instance", "x", "--client")
	require.ErrorContains(t, err, "--force", "--client without --force")
	_, err = e.run(t, "mod", "add", "dbg", "--instance", "x", "--force")
	require.ErrorContains(t, err, "only confirms --client", "--force alone")
	_, err = e.run(t, "mod", "add", "111", "--instance", "x", "--client", "--force")
	require.ErrorContains(t, err, "local mods", "--client with a workshop id")
	_, err = e.run(t, "mod", "add", "dbg", "--instance", "x", "--client", "--force", "--server")
	require.Error(t, err, "--client and --server exclude each other")
	// the mod is not signed: nothing is added
	out, err := e.run(t, "mod", "add", "dbg", "--instance", "x", "--client", "--force")
	require.Error(t, err, "an unsigned mod: %s", out)
	require.Contains(t, out, "bisign", "an unsigned mod")
	require.NotContains(t, readFile(t, inst), "dbg", "a failed import must leave instance.yaml alone")

	writeFile(t, filepath.Join(mod, "addons", "d.pbo.Dbg.bisign"), "signature")
	out, err = e.run(t, "mod", "add", "dbg", "--instance", "x", "--client", "--force")
	require.NoError(t, err, "add: %s", out)
	require.Contains(t, out, "installed", "add")
	require.Contains(t, out, "warning: x loads the local client mod dbg", "add")
	assert.Contains(t, readFile(t, inst), "- {local: dbg, debug_client: true}", "instance.yaml")
	out, err = e.run(t, "mod", "list", "x")
	assert.NoError(t, err, "list: %s", out)
	assert.Contains(t, out, "@dbg\tclient (debug, local)\t", "list")
	out, err = runCmd(t, "site", "validate", "--config", e.cfg)
	assert.NoError(t, err, "validate: %s", out)
	assert.Contains(t, out, "warning: x loads the local client mod dbg", "validate")
	// a later update keeps importing it as a signed client mod, with its keys
	out, err = e.run(t, "mod", "update", "x")
	assert.NoError(t, err, "update: %s", out)
	assert.NotContains(t, out, "FAILED", "update")
}

func TestModAddLocalIsAServermodByDefault(t *testing.T) {
	e := newInstallEnv(t)
	writeFile(t, filepath.Join(e.data, "site", "localmods", "more", "addons", "m.pbo"), string(buildPBO("p/m")))
	out, err := e.run(t, "mod", "add", "more", "--instance", "x")
	require.NoError(t, err, "add: %s", out)
	got := readFile(t, filepath.Join(e.data, "site", "instances", "x", "instance.yaml"))
	assert.Contains(t, got, "- {local: more, server: true}", "instance.yaml")
}
