// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package runfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t testing.TB, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

func TestKeysFromBuildAndClientMods(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "build", "keys", "dayz.bikey"), "a")
	write(t, filepath.Join(root, "cf", "Keys", "cf.bikey"), "b")
	write(t, filepath.Join(root, "other", "key", "DAYZ.bikey"), "dup")
	write(t, filepath.Join(root, "other", "keys", "readme.txt"), "x")
	keys, err := Keys(filepath.Join(root, "build"), []string{filepath.Join(root, "cf"), filepath.Join(root, "other")})
	require.NoError(t, err)
	var names []string
	for _, k := range keys {
		names = append(names, filepath.Base(k))
	}
	assert.Equal(t, []string{"dayz.bikey", "cf.bikey"}, names, "one key per name, no other files")
}

func TestServerCfgEnforcesTemplateAndQueryPort(t *testing.T) {
	src := "hostname = \"x\";\nmaxPlayers = 60;\nclass Missions\n{\n    class DayZ\n    {\n        template=\"wrong\"; // Mission\n    };\n};\n"
	f, err := ServerCfg([]byte(src), "dayzOffline.chernarusplus", 27016)
	require.NoError(t, err)
	got := f.String()
	for _, want := range []string{`template = "dayzOffline.chernarusplus";`, "steamQueryPort = 27016;", `hostname = "x";`, "maxPlayers = 60;", "class Missions", "class DayZ"} {
		assert.Contains(t, got, want)
	}
	assert.NotContains(t, got, "wrong", "the template of Missions/DayZ must be replaced")
	assert.Equal(t, 1, strings.Count(got, "template"), "the template must not be duplicated:\n%s", got)
	// a file without the block gets one
	f, err = ServerCfg([]byte("hostname = \"x\";\n"), "m.x", 0)
	require.NoError(t, err)
	assert.Contains(t, f.String(), "class Missions")
	assert.NotContains(t, f.String(), "steamQueryPort")
	_, err = ServerCfg([]byte("this is not a config"), "m", 1)
	assert.Error(t, err, "a broken serverDZ.cfg must be an error")
}

func TestRConPasswordIsCreatedOnceAndPrivate(t *testing.T) {
	dir := t.TempDir()
	a, err := RConPassword(dir, "x")
	require.NoError(t, err)
	require.Len(t, a, 24)
	b, _ := RConPassword(dir, "x")
	c, _ := RConPassword(dir, "y")
	assert.Equal(t, a, b, "a password is stable per instance")
	assert.NotEqual(t, a, c, "a password differs between instances")
	fi, err := os.Stat(filepath.Join(dir, "rcon", "x"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "the password file must be 0600")
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
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "runtime", "keys", "stale.bikey"))
	assert.True(t, os.IsNotExist(err), "keys of a removed mod must go")
	b, _ := os.ReadFile(filepath.Join(root, "runtime", "keys", "a.bikey"))
	assert.Equal(t, "key", string(b), "key not copied")
	b, _ = os.ReadFile(filepath.Join(root, "runtime", "serverDZ.cfg"))
	assert.Contains(t, string(b), "steamQueryPort = 27016;")
	be, _ := os.ReadFile(filepath.Join(root, "profiles", "battleye", "beserver_x64.cfg"))
	assert.Equal(t, "RConPassword pw\nRestrictRCon 0\nRConPort 2306\n", string(be), "BattlEye seed")
	_, err = os.Stat(filepath.Join(root, "profiles", "battleye", "beserver_x64_active_1a2b.cfg"))
	assert.True(t, os.IsNotExist(err), "a stale BattlEye working copy would keep the old port")
	fi, _ := os.Stat(filepath.Join(root, "runtime", "serverDZ.cfg"))
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "serverDZ.cfg holds passwords: 0600")
}

func TestBattlEyeCfgBindsRConWhenAsked(t *testing.T) {
	assert.NotContains(t, BattlEyeCfg("pw", 2306, ""), "RConIP", "no address, no RConIP line")
	assert.True(t, strings.HasSuffix(BattlEyeCfg("pw", 2306, "127.0.0.1"), "RConPort 2306\nRConIP 127.0.0.1\n"))
}

func TestCustomKeys(t *testing.T) {
	site := t.TempDir()
	put := func(rel string) {
		p := filepath.Join(site, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, []byte(rel), 0o600))
	}
	put("keys/site.bikey")
	put("instances/x/keys/own.bikey")
	put("instances/x/overlays/mine/o.bikey")
	put("overlays/shared/s.bikey")
	put("overlays/shared/Site.bikey") // same name as keys/site.bikey, ignored
	put("overlays/shared/readme.txt")
	put("instances/y/keys/other.bikey")
	got, err := CustomKeys(site, "x", []string{"mine", "shared", "missing"})
	require.NoError(t, err)
	var names []string
	for _, g := range got {
		names = append(names, filepath.Base(g))
	}
	assert.Equal(t, []string{"site.bikey", "own.bikey", "o.bikey", "s.bikey"}, names)
}

func TestPriorityFile(t *testing.T) {
	assert.Equal(t, "76561198000000001;76561198000000002;", string(PriorityFile([]string{"76561198000000001", "76561198000000002"})))
	assert.Empty(t, PriorityFile(nil), "no ids")
}
