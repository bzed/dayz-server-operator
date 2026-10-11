// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o640); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

func findEntry(t *testing.T, plan Plan, path string) PlanEntry {
	t.Helper()
	for _, e := range plan.Entries {
		if e.Path == path {
			return e
		}
	}
	t.Fatalf("no plan entry for %q in %+v", path, plan.Entries)
	return PlanEntry{}
}

func TestClassifyFirstRenderIsAllNew(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	writeTree(t, staging, map[string]string{
		"db/types.xml": "types-v1",
		"init.c":       "init-v1",
	})

	plan, err := Classify(staging, live, NewManifest(), nil)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if len(plan.Changed()) != 2 {
		t.Fatalf("Changed() = %+v", plan.Changed())
	}
	for _, e := range plan.Changed() {
		if e.Action != ActionNew {
			t.Errorf("entry %s: action = %v, want New", e.Path, e.Action)
		}
		if e.Origin != OriginPristine {
			t.Errorf("entry %s: origin = %v, want pristine", e.Path, e.Origin)
		}
	}
}

func TestClassifyGeneratedOrigin(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	writeTree(t, staging, map[string]string{
		"mod_123/types.xml":        "x",
		"custom_loadout/gear.json": "y",
		"EditorFiles/a.dze":        "z",
	})
	plan, err := Classify(staging, live, NewManifest(), nil)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if findEntry(t, plan, "mod_123/types.xml").Origin != OriginGenerated {
		t.Error("mod_123/types.xml should be Origin=generated")
	}
	if findEntry(t, plan, "custom_loadout/gear.json").Origin != OriginGenerated {
		t.Error("custom_loadout/gear.json should be Origin=generated")
	}
	if findEntry(t, plan, "EditorFiles/a.dze").Origin != OriginGenerated {
		t.Error("EditorFiles/a.dze should be Origin=generated")
	}
}

func TestClassifyUnchanged(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	content := "types-v1"
	writeTree(t, staging, map[string]string{"db/types.xml": content})
	writeTree(t, live, map[string]string{"db/types.xml": content})

	hash := HashBytes([]byte(content))
	m := NewManifest()
	m.Entries["db/types.xml"] = Entry{Origin: OriginPristine, SourceHash: hash, WrittenHash: hash}

	plan, err := Classify(staging, live, m, nil)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if !plan.Empty() {
		t.Fatalf("expected an empty plan, got %+v", plan.Entries)
	}
	if findEntry(t, plan, "db/types.xml").Action != ActionUnchanged {
		t.Error("expected ActionUnchanged")
	}
}

func TestClassifyUpdateWhenStagingChangesButLiveUntouched(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	oldContent := "v1"
	newContent := "v2"
	writeTree(t, staging, map[string]string{"db/types.xml": newContent})
	writeTree(t, live, map[string]string{"db/types.xml": oldContent})

	m := NewManifest()
	oldHash := HashBytes([]byte(oldContent))
	m.Entries["db/types.xml"] = Entry{Origin: OriginPristine, SourceHash: oldHash, WrittenHash: oldHash}

	plan, err := Classify(staging, live, m, nil)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if findEntry(t, plan, "db/types.xml").Action != ActionUpdate {
		t.Errorf("expected ActionUpdate, got %+v", plan.Entries)
	}
}

func TestClassifyDriftWhenLiveChangedUnderneath(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	original := "v1"
	writeTree(t, staging, map[string]string{"db/types.xml": original}) // staging unchanged
	writeTree(t, live, map[string]string{"db/types.xml": "modified-by-something-else"})

	m := NewManifest()
	hash := HashBytes([]byte(original))
	m.Entries["db/types.xml"] = Entry{Origin: OriginPristine, SourceHash: hash, WrittenHash: hash}

	plan, err := Classify(staging, live, m, nil)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if findEntry(t, plan, "db/types.xml").Action != ActionDrift {
		t.Errorf("expected ActionDrift, got %+v", plan.Entries)
	}
}

func TestClassifyNewWhenLiveFileMissingDespiteManifest(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir() // nothing written here
	content := "v1"
	writeTree(t, staging, map[string]string{"db/types.xml": content})

	m := NewManifest()
	hash := HashBytes([]byte(content))
	m.Entries["db/types.xml"] = Entry{Origin: OriginPristine, SourceHash: hash, WrittenHash: hash}

	plan, err := Classify(staging, live, m, nil)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if findEntry(t, plan, "db/types.xml").Action != ActionNew {
		t.Errorf("expected ActionNew when the live file is missing, got %+v", plan.Entries)
	}
}

func TestClassifyObsoleteWhenRemovedFromStaging(t *testing.T) {
	staging := t.TempDir() // empty: nothing ships this file anymore
	live := t.TempDir()
	writeTree(t, live, map[string]string{"mod_999/types.xml": "old"})

	m := NewManifest()
	m.Entries["mod_999/types.xml"] = Entry{Origin: OriginGenerated, SourceHash: "h", WrittenHash: "h"}

	plan, err := Classify(staging, live, m, nil)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if len(plan.Obsolete()) != 1 || plan.Obsolete()[0].Path != "mod_999/types.xml" {
		t.Fatalf("Obsolete() = %+v", plan.Obsolete())
	}
	// Never counted as something to write.
	if len(plan.Changed()) != 0 {
		t.Fatalf("Changed() = %+v, want none", plan.Changed())
	}
}

func TestClassifyHardExcludesStorage(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	writeTree(t, staging, map[string]string{
		"storage_1/base.pbo": "should never be touched",
		"db/types.xml":       "fine",
	})

	plan, err := Classify(staging, live, NewManifest(), nil)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	for _, e := range plan.Entries {
		if e.Path == "storage_1/base.pbo" {
			t.Fatalf("storage_1/* must never appear in the plan, got %+v", e)
		}
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("plan.Entries = %+v, want only db/types.xml", plan.Entries)
	}
}

func TestClassifyRespectsConfiguredUnmanaged(t *testing.T) {
	staging := t.TempDir()
	live := t.TempDir()
	writeTree(t, staging, map[string]string{
		"expansion/traders/config.json": "should stay foreign",
		"db/types.xml":                  "fine",
	})

	plan, err := Classify(staging, live, NewManifest(), []string{"expansion/**"})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	for _, e := range plan.Entries {
		if e.Path == "expansion/traders/config.json" {
			t.Fatalf("unmanaged path must never appear in the plan, got %+v", e)
		}
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("plan.Entries = %+v, want only db/types.xml", plan.Entries)
	}
}

func TestClassifyUnmanagedPathDroppedFromManifestBecomesObsoleteNotForeignSilently(t *testing.T) {
	// If a path was previously managed and is now marked unmanaged, it
	// must be reported as Obsolete (dropped from the manifest), not
	// silently forgotten.
	staging := t.TempDir()
	live := t.TempDir()
	writeTree(t, staging, map[string]string{"expansion/traders/config.json": "x"})
	writeTree(t, live, map[string]string{"expansion/traders/config.json": "x"})

	m := NewManifest()
	hash := HashBytes([]byte("x"))
	m.Entries["expansion/traders/config.json"] = Entry{Origin: OriginPristine, SourceHash: hash, WrittenHash: hash}

	plan, err := Classify(staging, live, m, []string{"expansion/**"})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if len(plan.Obsolete()) != 1 || plan.Obsolete()[0].Path != "expansion/traders/config.json" {
		t.Fatalf("Obsolete() = %+v", plan.Obsolete())
	}
}

func TestClassifyEmptyStagingAndLive(t *testing.T) {
	plan, err := Classify(t.TempDir(), t.TempDir(), NewManifest(), nil)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if !plan.Empty() {
		t.Fatalf("expected an empty plan, got %+v", plan.Entries)
	}
}

func TestClassifyMissingStagingDirErrors(t *testing.T) {
	_, err := Classify(filepath.Join(t.TempDir(), "nope"), t.TempDir(), NewManifest(), nil)
	if err == nil {
		t.Fatal("expected an error for a missing staging dir")
	}
}

func TestActionString(t *testing.T) {
	cases := map[Action]string{
		ActionNew:       "new",
		ActionUpdate:    "update",
		ActionDrift:     "drift",
		ActionUnchanged: "unchanged",
		ActionObsolete:  "obsolete",
		Action(99):      "unknown",
	}
	for a, want := range cases {
		if got := a.String(); got != want {
			t.Errorf("Action(%d).String() = %q, want %q", a, got, want)
		}
	}
}
