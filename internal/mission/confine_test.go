// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// The game container writes the live mission. These tests act as a container that plants
// links, FIFOs and a forged manifest, and check that nothing outside the live mission is read,
// written or deleted.

func TestRenderDoesNotFollowDirectorySymlinksInLiveMission(t *testing.T) {
	tr := newTree(t)
	writeTree(t, tr.pristine, map[string]string{"db/types.xml": "<types/>", "init.c": "c"})
	outside := t.TempDir()
	writeTree(t, outside, map[string]string{"victim.db": "HOST-SECRET"})
	require.NoError(t, os.MkdirAll(tr.live, 0o750))
	require.NoError(t, os.Symlink(outside, filepath.Join(tr.live, "db")))
	require.NoError(t, os.Symlink(outside, filepath.Join(tr.live, "EditorFiles")))
	// A manifest in the old place, written by the container, must be ignored.
	forged := filepath.Join(filepath.Dir(tr.live), ".dzo-manifest.json")
	require.NoError(t, os.WriteFile(forged, []byte(`{"version":1,"entries":{"EditorFiles/victim.db":{"origin":"generated","source_hash":"x","written_hash":"x"}}}`), 0o600))

	_, _, err := Render(RenderInput{
		PristineDir: tr.pristine, LiveDir: tr.live, ManifestPath: tr.manifest, FileHistoryDir: tr.history,
	})
	require.Error(t, err, "a link below the live mission that leaves it must fail the render")

	assert.Equal(t, "HOST-SECRET", readString(t, filepath.Join(outside, "victim.db")), "outside file must stay")
	assert.NoFileExists(t, filepath.Join(outside, "types.xml"), "nothing is created outside the live mission")
	assert.NoDirExists(t, tr.history, "the outside file must not be copied into filehistory")
}

func TestApplyForgedManifestCannotDeleteThroughLink(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	outside := t.TempDir()
	writeTree(t, outside, map[string]string{"victim.db": "HOST-SECRET"})
	require.NoError(t, os.Symlink(outside, filepath.Join(live, "EditorFiles")))
	m := NewManifest()
	m.Entries["EditorFiles/victim.db"] = Entry{Origin: OriginGenerated, SourceHash: "x", WrittenHash: "x"}
	plan := Plan{Entries: []PlanEntry{{Path: "EditorFiles/victim.db", Action: ActionObsolete, Origin: OriginGenerated}}}

	hist := filepath.Join(t.TempDir(), "filehistory")
	_, err := Apply(plan, staging, live, hist, m, Options{})
	require.Error(t, err)
	assert.Equal(t, "HOST-SECRET", readString(t, filepath.Join(outside, "victim.db")))
	assert.NoDirExists(t, hist)
}

func TestApplyRefusesLiveDirThatIsASymlink(t *testing.T) {
	staging := t.TempDir()
	writeTree(t, staging, map[string]string{"init.c": "c"})
	real := t.TempDir()
	live := filepath.Join(t.TempDir(), "m")
	require.NoError(t, os.Symlink(real, live))
	plan := Plan{Entries: []PlanEntry{{Path: "init.c", Action: ActionNew, Origin: OriginPristine, StagingHash: HashBytes([]byte("c"))}}}
	_, err := Apply(plan, staging, live, filepath.Join(t.TempDir(), "h"), NewManifest(), Options{})
	require.ErrorContains(t, err, "not a plain directory")
	assert.NoFileExists(t, filepath.Join(real, "init.c"))

	_, err = Classify(staging, live, NewManifest(), nil)
	require.ErrorContains(t, err, "not a plain directory")
}

func TestRollbackDoesNotWriteThroughLink(t *testing.T) {
	hist := t.TempDir()
	writeTree(t, hist, map[string]string{"20260101T000000.000000000Z/db/types.xml": "old"})
	live := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(live, "db")))
	m := NewManifest()
	m.Entries["db/types.xml"] = Entry{Origin: OriginPristine}
	_, _, err := Rollback(hist, live, m, "")
	require.Error(t, err)
	assert.NoFileExists(t, filepath.Join(outside, "types.xml"))
}

func TestClassifyAndApplyReplaceFIFOAndLeafSymlink(t *testing.T) {
	staging := t.TempDir()
	writeTree(t, staging, map[string]string{"init.c": "new", "cfg.xml": "new"})
	live := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(outside, []byte("HOST-SECRET"), 0o600))
	require.NoError(t, unix.Mkfifo(filepath.Join(live, "init.c"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(live, "cfg.xml")))
	m := NewManifest()
	for _, p := range []string{"init.c", "cfg.xml"} {
		m.Entries[p] = Entry{Origin: OriginPristine, SourceHash: HashBytes([]byte("new")), WrittenHash: HashBytes([]byte("new"))}
	}

	done := make(chan struct{})
	var plan Plan
	var err error
	go func() {
		defer close(done)
		plan, err = Classify(staging, live, m, nil)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "Classify blocks on a FIFO")
	}
	require.NoError(t, err)
	assert.Equal(t, ActionDrift, findEntry(t, plan, "init.c").Action)
	assert.Equal(t, ActionDrift, findEntry(t, plan, "cfg.xml").Action)

	hist := filepath.Join(t.TempDir(), "h")
	_, err = Apply(plan, staging, live, hist, m, Options{})
	require.NoError(t, err)
	assert.Equal(t, "new", readString(t, filepath.Join(live, "init.c")))
	assert.Equal(t, "new", readString(t, filepath.Join(live, "cfg.xml")))
	assert.Equal(t, "HOST-SECRET", readString(t, outside), "the link target is not written through")
	assert.NoFileExists(t, filepath.Join(hist, "cfg.xml"), "a link is not backed up (never read)")
}

func TestLegacyManifestInMpmissionsIsIgnoredAndRemoved(t *testing.T) {
	tr := newTree(t)
	writeTree(t, tr.pristine, map[string]string{"init.c": "c"})
	legacy := filepath.Join(filepath.Dir(tr.live), ".dzo-manifest.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(legacy), 0o750))
	require.NoError(t, os.WriteFile(legacy, []byte(`{"version":1,"entries":{"EditorFiles/a":{"origin":"generated","source_hash":"x","written_hash":"x"}}}`), 0o600))
	writeTree(t, tr.live, map[string]string{"EditorFiles/a": "keep me"})

	plan, _ := tr.render(t, false)
	assert.Empty(t, plan.Obsolete(), "the old manifest is not read")
	assert.Equal(t, "keep me", readString(t, filepath.Join(tr.live, "EditorFiles", "a")))
	require.NoError(t, RemoveLegacyManifest(filepath.Dir(tr.live)))
	assert.NoFileExists(t, legacy)
	require.NoError(t, RemoveLegacyManifest(filepath.Dir(tr.live)), "a missing file is fine")
}
