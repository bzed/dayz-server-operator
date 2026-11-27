// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/beevik/etree"

	"github.com/bzed/dayz-server-operator/internal/ce"
)

// CopyPristine seeds a staging tree from the instance's pristine mission
// (§C6 step 1: "Staging = pristine tree"). It is a plain recursive copy;
// every subsequent merge operation reads its "base" from files this places
// in stagingDir and overwrites them in place.
func CopyPristine(pristineDir, stagingDir string) error {
	return filepath.WalkDir(pristineDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(pristineDir, path)
		if err != nil {
			return fmt.Errorf("mission: relativize %s: %w", path, err)
		}
		dst := filepath.Join(stagingDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o750)
		}
		// The pristine mission is a checkout of an outside repository: git materialises its
		// links, and a link must never be followed to a host file. Only directories and
		// regular files are taken; readFileSafe opens without following a link and checks the
		// opened descriptor, which closes the gap between this check and the open.
		if !d.Type().IsRegular() {
			return fmt.Errorf("mission: pristine %s is not a regular file or directory (links and special files are rejected)", rel)
		}
		data, err := readFileSafe(path, maxMissionFile)
		if err != nil {
			return fmt.Errorf("mission: read pristine %s: %w", rel, err)
		}
		return writeFileAtomic(dst, data, 0o640)
	})
}

// WriteFile writes content to stagingDir/relPath, creating parent
// directories as needed. This backs the "replace" and "copy-extra"
// strategies (cfgweather.xml, messages.xml, mod/overlay extra files):
// FR-09.
func WriteFile(stagingDir, relPath string, content []byte) error {
	return writeFileAtomic(filepath.Join(stagingDir, relPath), content, 0o640)
}

// MergeJSONFile deep-merges overlay into stagingDir/relPath (creating it
// as "{}" if it doesn't exist yet - a mod/overlay contributing to a merge
// target pristine doesn't ship), using appendKeys for the list fields that
// concatenate instead of replace (FR-09a: cfggameplay.json's
// objectSpawnersArr/spawnGearPresetFiles/..., cfgundergroundtriggers'
// Triggers, cfgeffectarea's Areas/SafePositions).
func MergeJSONFile(stagingDir, relPath string, overlay []byte, appendKeys []string) error {
	path := filepath.Join(stagingDir, relPath)
	base, err := os.ReadFile(path) //nolint:gosec // path is stagingDir + a caller-configured relative merge target, not external input
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("mission: read staging %s: %w", relPath, err)
		}
		base = []byte("{}")
	}
	merged, err := ce.MergeJSON(base, overlay, appendKeys)
	if err != nil {
		return fmt.Errorf("mission: merge %s: %w", relPath, err)
	}
	return writeFileAtomic(path, merged, 0o640)
}

// MergeXMLFile merges overlay's root children into stagingDir/relPath's
// root (creating it from rootTag if the merge target doesn't exist yet),
// matching existing elements by matchAttr (FR-09: mapgrouppos,
// mapgroupproto, cfgeventgroups, cfgeventspawns, cfgenvironment,
// cfgrandompresets, zombie_territories - exact per-file semantics to be
// pinned down against real xmlmerge output in spike S3).
func MergeXMLFile(stagingDir, relPath, rootTag string, overlay []byte, matchAttr string) error {
	path := filepath.Join(stagingDir, relPath)
	base := etree.NewDocument()
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec // path is stagingDir + a caller-configured relative merge target
		if err := base.ReadFromBytes(data); err != nil {
			return fmt.Errorf("mission: parse staging %s: %w", relPath, err)
		}
	} else if os.IsNotExist(err) {
		base.SetRoot(base.CreateElement(rootTag))
	} else {
		return fmt.Errorf("mission: read staging %s: %w", relPath, err)
	}

	overlayDoc := etree.NewDocument()
	if err := overlayDoc.ReadFromBytes(overlay); err != nil {
		return fmt.Errorf("mission: parse overlay for %s: %w", relPath, err)
	}

	if err := ce.MergeXMLChildren(base, overlayDoc, matchAttr); err != nil {
		return fmt.Errorf("mission: merge %s: %w", relPath, err)
	}

	base.Indent(2)
	out, err := base.WriteToBytes()
	if err != nil {
		return fmt.Errorf("mission: serialize %s: %w", relPath, err)
	}
	return writeFileAtomic(path, out, 0o640)
}

// RegisterCEFolder appends a <ce folder="folder"> entry (with files, in
// order) to stagingDir/relPath's <economycore> root (FR-09a). Callers must
// call this once per mod/overlay folder, in the instance's configured mod
// order, so registration order stays deterministic (A8#1).
func RegisterCEFolder(stagingDir, relPath, folder string, files []ce.CEFile) error {
	path := filepath.Join(stagingDir, relPath)
	doc := etree.NewDocument()
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec // path is stagingDir + a caller-configured relative merge target
		if err := doc.ReadFromBytes(data); err != nil {
			return fmt.Errorf("mission: parse staging %s: %w", relPath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("mission: read staging %s: %w", relPath, err)
	}

	if err := ce.AppendCEFolder(doc, folder, files); err != nil {
		return fmt.Errorf("mission: register CE folder %s: %w", folder, err)
	}

	doc.Indent(2)
	out, err := doc.WriteToBytes()
	if err != nil {
		return fmt.Errorf("mission: serialize %s: %w", relPath, err)
	}
	return writeFileAtomic(path, out, 0o640)
}
