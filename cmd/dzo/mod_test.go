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
func newInstallEnv(t *testing.T) *installEnv {
	t.Helper()
	e := &installEnv{data: t.TempDir(), fake: t.TempDir(), details: stubDetails{111: {PublishedFileID: 111, Result: 1, TimeUpdated: 10}}}
	t.Setenv("FAKE", e.fake)
	e.steamcmd = filepath.Join(t.TempDir(), "steamcmd")
	writeFile(t, e.steamcmd, fakeSteamcmd)
	if err := os.Chmod(e.steamcmd, 0o755); err != nil { //nolint:gosec // fixture
		t.Fatal(err)
	}
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

func (e *installEnv) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	if args[1] == "list" { // read-only, no steamcmd
		return runCmd(t, append(args, "--config", e.cfg)...)
	}
	return runCmd(t, append(args, "--config", e.cfg, "--steamcmd", e.steamcmd)...)
}

func (e *installEnv) downloads(t *testing.T) int {
	data, _ := os.ReadFile(filepath.Join(e.fake, "calls"))
	return strings.Count(string(data), "workshop_download_item") + strings.Count(string(data), "app_update")
}

func TestProductInstallAndUpdate(t *testing.T) {
	e := newInstallEnv(t)
	if out, err := e.run(t, "product", "update", "dayz-stable"); err == nil {
		t.Fatalf("update before install must fail:\n%s", out)
	}
	out, err := e.run(t, "product", "install", "dayz-stable")
	if err != nil || !strings.Contains(out, "build 500 installed") {
		t.Fatalf("install = %v\n%s", err, out)
	}
	if _, err := e.run(t, "product", "install", "dayz-stable"); err == nil {
		t.Fatal("a second install must fail")
	}
	if out, err = e.run(t, "product", "update", "dayz-stable"); err != nil || !strings.Contains(out, "already stored") {
		t.Fatalf("update = %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(e.fake, "buildid"), "501")
	if out, err = e.run(t, "product", "update", "dayz-stable"); err != nil || !strings.Contains(out, "build 501 installed") {
		t.Fatalf("update to a new build = %v\n%s", err, out)
	}
	if out, err = e.run(t, "product", "update", "dayz-stable", "--force"); err != nil || !strings.Contains(out, "501-r1") {
		t.Fatalf("forced update = %v\n%s", err, out)
	}
	if _, err := e.run(t, "product", "install", "nope"); err == nil {
		t.Error("an unknown product must fail")
	}
}

func TestProductInstallNeedsSteamAccount(t *testing.T) {
	e := newInstallEnv(t)
	writeFile(t, e.cfg, "paths:\n  data: "+e.data+"\n")
	if _, err := e.run(t, "product", "install", "dayz-stable"); err == nil || !strings.Contains(err.Error(), "steam.account") {
		t.Errorf("err = %v", err)
	}
}

func TestModUpdateListRefresh(t *testing.T) {
	e := newInstallEnv(t)
	out, err := e.run(t, "mod", "list")
	if err != nil || strings.Count(out, "not installed") != 2 {
		t.Fatalf("list before install = %v\n%s", err, out)
	}

	out, err = e.run(t, "mod", "update")
	if err != nil || !strings.Contains(out, "111") || !strings.Contains(out, "installed 10") || !strings.Contains(out, "tools") {
		t.Fatalf("update = %v\n%s", err, out)
	}
	if out, err = e.run(t, "mod", "list", "x"); err != nil || strings.Contains(out, "not installed") || !strings.Contains(out, "@111\tclient\t10") {
		t.Fatalf("list after install = %v\n%s", err, out)
	}

	// generations survive re-runs: nothing is downloaded again
	n := e.downloads(t)
	if out, err = e.run(t, "mod", "update", "x"); err != nil || strings.Count(out, "up to date") != 2 || e.downloads(t) != n {
		t.Fatalf("re-run = %v\n%s", err, out)
	}

	// refresh needs --force and a selection
	if _, err := e.run(t, "mod", "refresh", "111"); err == nil {
		t.Error("refresh without --force must fail")
	}
	if _, err := e.run(t, "mod", "refresh", "--force"); err == nil {
		t.Error("refresh without ids or --all must fail")
	}
	if _, err := e.run(t, "mod", "refresh", "999", "--force"); err == nil || !strings.Contains(err.Error(), "not used") {
		t.Errorf("unknown ref: %v", err)
	}
	if out, err = e.run(t, "mod", "refresh", "111", "--force"); err != nil || !strings.Contains(out, "installed 10-r1") {
		t.Fatalf("refresh = %v\n%s", err, out)
	}
	if out, err = e.run(t, "mod", "refresh", "--all", "--instance", "x", "--force"); err != nil || !strings.Contains(out, "10-r2") {
		t.Fatalf("refresh --all = %v\n%s", err, out)
	}
	if _, err := e.run(t, "mod", "update", "nope"); err == nil {
		t.Error("an unknown instance must fail")
	}
	if _, err := e.run(t, "mod", "list", "nope"); err == nil {
		t.Error("an unknown instance must fail")
	}
}

func TestModUpdateReportsFailures(t *testing.T) {
	e := newInstallEnv(t)
	e.details[333] = product.FileDetails{PublishedFileID: 333, Result: 1, TimeUpdated: 1} // no fixture: steamcmd fails
	writeFile(t, filepath.Join(e.data, "site", "instances", "x", "instance.yaml"), strings.Replace(
		readFile(t, filepath.Join(e.data, "site", "instances", "x", "instance.yaml")), "  - {id: 111}", "  - {id: 111}\n  - {id: 333}", 1))
	out, err := e.run(t, "mod", "update")
	if err == nil || !strings.Contains(out, "FAILED") || !strings.Contains(out, "installed 10") {
		t.Fatalf("update = %v\n%s (one failure must not stop the others, but must fail the command)", err, out)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestModAdd(t *testing.T) {
	e := newInstallEnv(t)
	inst := filepath.Join(e.data, "site", "instances", "x", "instance.yaml")

	if out, err := e.run(t, "mod", "add", "222", "--instance", "x", "--server"); err == nil {
		t.Fatalf("a mod Steam does not know must not be added:\n%s", out)
	}
	if strings.Contains(readFile(t, inst), "222") {
		t.Fatal("a failed install must leave instance.yaml alone")
	}

	e.details[222] = product.FileDetails{PublishedFileID: 222, Result: 1, TimeUpdated: 20}
	if out, err := e.run(t, "mod", "add", "222", "--instance", "x", "--server"); err != nil || !strings.Contains(out, "installed 20") {
		t.Fatalf("add = %v\n%s", err, out)
	}
	if got := readFile(t, inst); !strings.Contains(got, "- {id: 222, server: true}") || !strings.Contains(got, "# x") {
		t.Errorf("instance.yaml = %s", got)
	}
	if _, err := e.run(t, "mod", "add", "222", "--instance", "x"); err == nil {
		t.Error("adding the same mod twice must fail")
	}
	if _, err := e.run(t, "mod", "add", "ghost", "--instance", "x"); err == nil {
		t.Error("an unknown local mod must fail")
	}
	if _, err := e.run(t, "mod", "add", "tools", "--instance", "nope"); err == nil {
		t.Error("an unknown instance must fail")
	}
	if out, err := e.run(t, "mod", "list", "x"); err != nil || !strings.Contains(out, "@222\tserver\t20") {
		t.Errorf("list = %v\n%s", err, out)
	}
}

func TestModUpdateRefusesAShippedServermodThatDiffersFromCompat(t *testing.T) {
	e := newInstallEnv(t)
	writeFile(t, filepath.Join(e.data, "site", "localmods", servermods.CompatFile),
		"dzo: test\nservermods:\n  tools:\n    commit: abc\n    pbos:\n      addons/t.pbo: "+strings.Repeat("0", 64)+"\n")

	out, err := e.run(t, "mod", "update", "x")
	if err == nil || !strings.Contains(out+err.Error(), "differs from the tested set") {
		t.Fatalf("a servermod that differs from compat.yaml must be refused: %v\n%s", err, out)
	}
	if out, err := e.run(t, "mod", "update", "x", "--ignore-compat"); err != nil || !strings.Contains(out, "tools") {
		t.Fatalf("--ignore-compat must allow it: %v\n%s", err, out)
	}
}

func TestModAddDebugClient(t *testing.T) {
	e := newInstallEnv(t)
	inst := filepath.Join(e.data, "site", "instances", "x", "instance.yaml")
	mod := filepath.Join(e.data, "site", "localmods", "dbg")
	writeFile(t, filepath.Join(mod, "addons", "d.pbo"), string(buildPBO("p/d")))
	writeFile(t, filepath.Join(mod, "keys", "Dbg.bikey"), "public key")

	// --client alone is refused, with the reason; so are the combinations that make no sense
	if _, err := e.run(t, "mod", "add", "dbg", "--instance", "x", "--client"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("--client without --force: %v", err)
	}
	if _, err := e.run(t, "mod", "add", "dbg", "--instance", "x", "--force"); err == nil || !strings.Contains(err.Error(), "only confirms --client") {
		t.Fatalf("--force alone: %v", err)
	}
	if _, err := e.run(t, "mod", "add", "111", "--instance", "x", "--client", "--force"); err == nil || !strings.Contains(err.Error(), "local mods") {
		t.Fatalf("--client with a workshop id: %v", err)
	}
	if _, err := e.run(t, "mod", "add", "dbg", "--instance", "x", "--client", "--force", "--server"); err == nil {
		t.Fatal("--client and --server exclude each other")
	}
	// the mod is not signed: nothing is added
	if out, err := e.run(t, "mod", "add", "dbg", "--instance", "x", "--client", "--force"); err == nil || !strings.Contains(out, "bisign") {
		t.Fatalf("an unsigned mod: %v\n%s", err, out)
	}
	if strings.Contains(readFile(t, inst), "dbg") {
		t.Fatal("a failed import must leave instance.yaml alone")
	}

	writeFile(t, filepath.Join(mod, "addons", "d.pbo.Dbg.bisign"), "signature")
	out, err := e.run(t, "mod", "add", "dbg", "--instance", "x", "--client", "--force")
	if err != nil || !strings.Contains(out, "installed") || !strings.Contains(out, "warning: x loads the local client mod dbg") {
		t.Fatalf("add = %v\n%s", err, out)
	}
	if got := readFile(t, inst); !strings.Contains(got, "- {local: dbg, debug_client: true}") {
		t.Errorf("instance.yaml = %s", got)
	}
	if out, err := e.run(t, "mod", "list", "x"); err != nil || !strings.Contains(out, "@dbg\tclient (debug, local)\t") {
		t.Errorf("list = %v\n%s", err, out)
	}
	if out, err := runCmd(t, "site", "validate", "--config", e.cfg); err != nil || !strings.Contains(out, "warning: x loads the local client mod dbg") {
		t.Errorf("validate = %v\n%s", err, out)
	}
	// a later update keeps importing it as a signed client mod, with its keys
	if out, err := e.run(t, "mod", "update", "x"); err != nil || strings.Contains(out, "FAILED") {
		t.Errorf("update = %v\n%s", err, out)
	}
}

func TestModAddLocalIsAServermodByDefault(t *testing.T) {
	e := newInstallEnv(t)
	writeFile(t, filepath.Join(e.data, "site", "localmods", "more", "addons", "m.pbo"), string(buildPBO("p/m")))
	if out, err := e.run(t, "mod", "add", "more", "--instance", "x"); err != nil {
		t.Fatalf("add = %v\n%s", err, out)
	}
	if got := readFile(t, filepath.Join(e.data, "site", "instances", "x", "instance.yaml")); !strings.Contains(got, "- {local: more, server: true}") {
		t.Errorf("instance.yaml = %s", got)
	}
}
