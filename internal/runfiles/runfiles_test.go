// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package runfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestKeysFromBuildAndClientMods(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "build", "keys", "dayz.bikey"), "a")
	write(t, filepath.Join(root, "cf", "Keys", "cf.bikey"), "b")
	write(t, filepath.Join(root, "other", "key", "DAYZ.bikey"), "dup")
	write(t, filepath.Join(root, "other", "keys", "readme.txt"), "x")
	keys, err := Keys(filepath.Join(root, "build"), []string{filepath.Join(root, "cf"), filepath.Join(root, "other")})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, k := range keys {
		names = append(names, filepath.Base(k))
	}
	if strings.Join(names, " ") != "dayz.bikey cf.bikey" {
		t.Errorf("keys = %v (one key per name, no other files)", names)
	}
}

func TestServerCfgEnforcesTemplateAndQueryPort(t *testing.T) {
	src := "hostname = \"x\";\nmaxPlayers = 60;\nclass Missions\n{\n    class DayZ\n    {\n        template=\"wrong\"; // Mission\n    };\n};\n"
	f, err := ServerCfg([]byte(src), "dayzOffline.chernarusplus", 27016)
	if err != nil {
		t.Fatal(err)
	}
	got := f.String()
	for _, want := range []string{`template = "dayzOffline.chernarusplus";`, "steamQueryPort = 27016;", `hostname = "x";`, "maxPlayers = 60;", "class Missions", "class DayZ"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "wrong") || strings.Count(got, "template") != 1 {
		t.Errorf("the template of Missions/DayZ must be replaced, not duplicated:\n%s", got)
	}
	// a file without the block gets one
	f, err = ServerCfg([]byte("hostname = \"x\";\n"), "m.x", 0)
	if err != nil || !strings.Contains(f.String(), "class Missions") || strings.Contains(f.String(), "steamQueryPort") {
		t.Errorf("created block: %v\n%s", err, f)
	}
	if _, err := ServerCfg([]byte("this is not a config"), "m", 1); err == nil {
		t.Error("a broken serverDZ.cfg must be an error")
	}
}

func TestRConPasswordIsCreatedOnceAndPrivate(t *testing.T) {
	dir := t.TempDir()
	a, err := RConPassword(dir, "x")
	if err != nil || len(a) != 24 {
		t.Fatalf("password %q: %v", a, err)
	}
	b, _ := RConPassword(dir, "x")
	c, _ := RConPassword(dir, "y")
	if a != b || a == c {
		t.Errorf("a password is stable per instance and differs between instances: %q %q %q", a, b, c)
	}
	if fi, err := os.Stat(filepath.Join(dir, "rcon", "x")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the password file must be 0600: %v %v", fi, err)
	}
}

func TestWriteRendersTheRuntimeFiles(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "k", "a.bikey"), "key")
	write(t, filepath.Join(root, "runtime", "keys", "stale.bikey"), "old")
	write(t, filepath.Join(root, "profiles", "battleye", "beserver_x64_active_1a2b.cfg"), "old port")
	err := Write(Input{
		RuntimeDir: filepath.Join(root, "runtime"), ProfilesDir: filepath.Join(root, "profiles"),
		ServerCfg: []byte("hostname = \"h\";\n"), Template: "t", QueryPort: 27016, RConPort: 2306, RConPass: "pw", Keys: []string{filepath.Join(root, "k", "a.bikey")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "runtime", "keys", "stale.bikey")); !os.IsNotExist(err) {
		t.Error("keys of a removed mod must go")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "runtime", "keys", "a.bikey")); string(b) != "key" {
		t.Errorf("key not copied: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "runtime", "serverDZ.cfg")); !strings.Contains(string(b), "steamQueryPort = 27016;") {
		t.Errorf("serverDZ.cfg:\n%s", b)
	}
	be, _ := os.ReadFile(filepath.Join(root, "profiles", "battleye", "beserver_x64.cfg"))
	if string(be) != "RConPassword pw\nRestrictRCon 0\nRConPort 2306\n" {
		t.Errorf("BattlEye seed:\n%s", be)
	}
	if _, err := os.Stat(filepath.Join(root, "profiles", "battleye", "beserver_x64_active_1a2b.cfg")); !os.IsNotExist(err) {
		t.Error("a stale BattlEye working copy would keep the old port")
	}
	if fi, _ := os.Stat(filepath.Join(root, "runtime", "serverDZ.cfg")); fi.Mode().Perm() != 0o600 {
		t.Error("serverDZ.cfg holds passwords: 0600")
	}
}

func TestBattlEyeCfgBindsRConWhenAsked(t *testing.T) {
	if got := BattlEyeCfg("pw", 2306, ""); strings.Contains(got, "RConIP") {
		t.Errorf("no address, no RConIP line: %q", got)
	}
	if got := BattlEyeCfg("pw", 2306, "127.0.0.1"); !strings.HasSuffix(got, "RConPort 2306\nRConIP 127.0.0.1\n") {
		t.Errorf("cfg = %q", got)
	}
}
