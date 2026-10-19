// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package product

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// buildPBO hand-assembles a minimal PBO: a "Vers" header carrying prefix (if
// non-empty), one file entry, the terminating record and the data.
func buildPBO(prefix string) []byte {
	var b bytes.Buffer
	str := func(s string) { b.WriteString(s); b.WriteByte(0) }
	u32 := func(v uint32) { _ = binary.Write(&b, binary.LittleEndian, v) }
	str("")
	u32(0x56657273)
	u32(0)
	u32(0)
	u32(0)
	u32(0)
	if prefix != "" {
		str("prefix")
		str(prefix)
	}
	str("")
	data := []byte("class CfgPatches {};")
	str("config.cpp")
	u32(0)
	u32(uint32(len(data)))
	u32(0)
	u32(0)
	u32(uint32(len(data)))
	str("")
	for i := 0; i < 5; i++ {
		u32(0)
	}
	b.Write(data)
	return b.Bytes()
}

func writeTree(t testing.TB, root string, files map[string][]byte) {
	t.Helper()
	for rel, data := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		err := os.MkdirAll(filepath.Dir(p), 0o750)
		require.NoError(t, err)
		err = os.WriteFile(p, data, 0o644)
		require.NoError(t, err)
	}
}

// modFiles is a valid workshop mod, or a valid local mod when local.
func modFiles(local bool) map[string][]byte {
	f := map[string][]byte{"addons/a.pbo": buildPBO("dzo/a")}
	if !local {
		f["meta.cpp"] = []byte(`name = "A";`)
	}
	return f
}

// fakeSteam is a scripted steamcmd: `+app_update` copies $FAKE/app into the
// install dir and writes an app manifest with the build id in $FAKE/buildid,
// `+workshop_download_item` copies $FAKE/mod/<id> (or reports a failure when
// there is none). Every invocation is appended to $FAKE/calls.
const fakeSteam = `#!/bin/sh
echo "$*" >> "$FAKE/calls"
echo "Waiting for user info...OK"
dir=""
while [ $# -gt 0 ]; do
  case "$1" in
    +force_install_dir) dir="$2"; shift ;;
    +app_update)
      app="$2"; shift
      if [ -d "$FAKE/app" ]; then
        mkdir -p "$dir/steamapps"; cp -R "$FAKE/app/." "$dir/"
        printf '"AppState"\n{\n\t"buildid"\t\t"%s"\n}\n' "$(cat "$FAKE/buildid")" > "$dir/steamapps/appmanifest_$app.acf"
        echo "Success! App '$app' fully installed."
      fi ;;
    +workshop_download_item)
      app="$2"; id="$3"; shift 2
      if [ -d "$FAKE/mod/$id" ]; then
        mkdir -p "$dir/steamapps/workshop/content/$app/$id"; cp -R "$FAKE/mod/$id/." "$dir/steamapps/workshop/content/$app/$id/"
        if [ -f "$FAKE/timeupdated/$id" ]; then
          printf '"AppWorkshop"\n{\n\t"WorkshopItemsInstalled"\n\t{\n\t\t"%s"\n\t\t{\n\t\t\t"size"\t"1"\n\t\t\t"timeupdated"\t"%s"\n\t\t}\n\t}\n}\n' "$id" "$(cat "$FAKE/timeupdated/$id")" > "$dir/steamapps/workshop/appworkshop_$app.acf"
        fi
        echo "Success. Downloaded item $id to \"$dir\" (1 bytes)"
      else
        echo "ERROR! Download item $id failed (Failure)."
      fi ;;
  esac
  shift
done
`

// newFakeSteam returns the script path and the $FAKE dir (set in the environment).
func newFakeSteam(t testing.TB) (script, fake string) {
	t.Helper()
	fake = t.TempDir()
	t.Setenv("FAKE", fake)
	script = filepath.Join(t.TempDir(), "steamcmd")
	err := os.WriteFile(script, []byte(fakeSteam), 0o755)
	require.NoError(t, err)
	return script, fake
}

// stubDetails answers file-details lookups from a fixed map.
type stubDetails map[uint64]FileDetails

func (s stubDetails) get(_ context.Context, ids []uint64) (map[uint64]FileDetails, error) {
	out := map[uint64]FileDetails{}
	for _, id := range ids {
		if d, ok := s[id]; ok {
			out[id] = d
		}
	}
	return out, nil
}
