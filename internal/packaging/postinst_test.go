// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package packaging

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postinst is the shared post-install script the packages install as
// /usr/libexec/dzo/postinst, relative to this package's directory.
const postinst = "../../packaging/common/dzo-postinst.sh"

// fakeSubid is the UID the fake id reports for dayz.
const fakeSubid = "1001"

// env is one run of the post-install script against a temporary root: DZO_ROOT
// makes it read and create its files there, and a fake PATH replaces the
// system commands that would change the real machine (usermod, loginctl,
// install, id, runuser) with commands that only append their arguments to a
// log file.
type env struct {
	root string
	log  string
	bin  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh is not available")
	}
	e := &env{
		root: t.TempDir(),
		log:  filepath.Join(t.TempDir(), "calls.log"),
		bin:  filepath.Join(t.TempDir(), "bin"),
	}
	require.NoError(t, os.MkdirAll(e.bin, 0o750))
	for _, cmd := range []struct{ name, body string }{
		{"usermod", `printf 'usermod %s\n' "$*" >> "$DZO_TEST_LOG"`},
		{"loginctl", `printf 'loginctl %s\n' "$*" >> "$DZO_TEST_LOG"`},
		{"install", `printf 'install %s\n' "$*" >> "$DZO_TEST_LOG"`},
		{"runuser", `printf 'runuser %s\n' "$*" >> "$DZO_TEST_LOG"`},
		// id dayz exists and has the fake UID; anything else reports success.
		{"id", "case \"$1\" in\n-u) echo " + fakeSubid + " ;;\nesac"},
	} {
		path := filepath.Join(e.bin, cmd.name)
		e.write(t, path, "#!/bin/sh\n"+cmd.body+"\n")
		require.NoError(t, os.Chmod(path, 0o755))
	}
	return e
}

func (e *env) write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func (e *env) subids(t *testing.T, subuid, subgid string) {
	t.Helper()
	e.write(t, filepath.Join(e.root, "etc", "subuid"), subuid)
	e.write(t, filepath.Join(e.root, "etc", "subgid"), subgid)
}

// run starts the script with the given arguments and returns its combined
// output.
func (e *env) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "/bin/sh", append([]string{postinst}, args...)...)
	cmd.Env = []string{
		"DZO_ROOT=" + e.root,
		"DZO_TEST_LOG=" + e.log,
		"PATH=" + e.bin + ":/usr/bin:/bin",
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// calls is what the fake commands have logged so far, one line per call.
func (e *env) calls(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(e.log)
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(b)
}

func TestPostinstConfigureAssignsTheFirstRangeAndLingers(t *testing.T) {
	e := newEnv(t)
	e.subids(t, "", "")

	out, err := e.run(t, "configure")
	require.NoError(t, err, "output: %s", out)

	calls := e.calls(t)
	assert.Contains(t, calls, "install -d -o dayz -g dayz -m 0750 "+filepath.Join(e.root, "var", "lib", "dzo"))
	assert.Contains(t, calls, "usermod --add-subuids 100000-165535 --add-subgids 100000-165535 dayz")
	assert.Contains(t, calls, "loginctl enable-linger dayz")
	assert.NotContains(t, calls, "runuser")
}

func TestPostinstPicksAFreeRangeInEachSubidFile(t *testing.T) {
	tests := []struct {
		name           string
		subuid, subgid string
		want           string // the usermod call; empty means none
	}{
		{
			name:   "another user owns the default range",
			subuid: "alice:100000:65536\n",
			subgid: "alice:100000:65536\n",
			want:   "usermod --add-subuids 165536-231071 --add-subgids 165536-231071 dayz",
		},
		{
			name:   "dayz already has a range",
			subuid: "dayz:300000:65536\n",
			subgid: "dayz:300000:65536\n",
		},
		{
			name:   "subuid and subgid are chosen separately",
			subuid: "alice:100000:65536\n",
			subgid: "",
			want:   "usermod --add-subuids 165536-231071 --add-subgids 100000-165535 dayz",
		},
		{
			name:   "only the file dayz is missing from is extended",
			subuid: "dayz:300000:65536\n",
			subgid: "",
			want:   "usermod --add-subgids 100000-165535 dayz",
		},
		{
			name:   "unsorted overlapping ranges push the start up",
			subuid: "bob:200000:65536\nalice:100000:70000\n",
			subgid: "",
			want:   "usermod --add-subuids 265536-331071 --add-subgids 100000-165535 dayz",
		},
		{
			name:   "the last line needs no newline",
			subuid: "alice:100000:65536",
			subgid: "",
			want:   "usermod --add-subuids 165536-231071 --add-subgids 100000-165535 dayz",
		},
		{
			name:   "comments, blank and malformed lines are ignored",
			subuid: "# alice:100000:65536\n\nno-fields\ncarol:abc:10\ndave:10\nerin:100000\n",
			subgid: "",
			want:   "usermod --add-subuids 100000-165535 --add-subgids 100000-165535 dayz",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.subids(t, tc.subuid, tc.subgid)

			out, err := e.run(t, "configure")
			require.NoError(t, err, "output: %s", out)

			calls := e.calls(t)
			if tc.want == "" {
				assert.NotContains(t, calls, "usermod")
				return
			}
			assert.Contains(t, calls, tc.want)
		})
	}
}

func TestPostinstUpgradeWithoutAPrivateBusDoesNotRunAnything(t *testing.T) {
	e := newEnv(t)
	e.subids(t, "", "")

	out, err := e.run(t, "upgrade")
	require.NoError(t, err, "output: %s", out)

	calls := e.calls(t)
	// An upgrade is also a first configuration: it creates the directory and
	// the subid range even when no earlier run did.
	assert.Contains(t, calls, "install -d -o dayz -g dayz -m 0750 "+filepath.Join(e.root, "var", "lib", "dzo"))
	assert.Contains(t, calls, "usermod --add-subuids 100000-165535 --add-subgids 100000-165535 dayz")
	assert.Contains(t, calls, "loginctl enable-linger dayz")
	assert.NotContains(t, calls, "runuser")
}

func TestPostinstUpgradeSyncsTheUnitsAndRestartsTheServices(t *testing.T) {
	e := newEnv(t)
	e.subids(t, "", "")
	e.write(t, filepath.Join(e.root, "etc", "dzo", "config.yaml"), "paths:\n  data: /srv/dzo\n")
	require.NoError(t, os.MkdirAll(filepath.Join(e.root, "var", "lib", "dzo", "site"), 0o750))
	runtimeDir := filepath.Join(e.root, "run", "user", fakeSubid)
	require.NoError(t, os.MkdirAll(runtimeDir, 0o750))
	bus, err := net.Listen("unix", filepath.Join(runtimeDir, "bus"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = bus.Close() })

	out, err := e.run(t, "upgrade")
	require.NoError(t, err, "output: %s", out)

	calls := e.calls(t)
	asDayz := "runuser -u dayz -- env XDG_RUNTIME_DIR=" + runtimeDir +
		" DBUS_SESSION_BUS_ADDRESS=unix:path=" + filepath.Join(runtimeDir, "bus") + " "
	assert.Contains(t, calls, asDayz+"/usr/bin/dzo units sync")
	for _, unit := range []string{"dzo-exporter", "dzo-serve", "dzo-web"} {
		assert.Contains(t, calls, asDayz+"systemctl --user is-active --quiet "+unit+".service")
		assert.Contains(t, calls, asDayz+"systemctl --user restart "+unit+".service")
	}
}

func TestPostinstRefusesAnUnknownAction(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, "purge")
	require.Error(t, err)
	assert.Contains(t, out, "usage")
	assert.Empty(t, e.calls(t))
}
