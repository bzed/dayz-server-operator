// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Action is what Apply must do for one path.
type Action int

const (
	// ActionNew: the path has no manifest entry yet (or its live file is
	// missing) - write it, nothing to back up.
	ActionNew Action = iota
	// ActionUpdate: staging content changed since the last render, and the
	// live file still matches what dzo wrote last time - write it.
	ActionUpdate
	// ActionDrift: the live file no longer matches what dzo last wrote
	// (something else changed it) - back up, warn, then overwrite (Q11).
	ActionDrift
	// ActionUnchanged: staging content is identical to last time, and the
	// live file is untouched - nothing to do.
	ActionUnchanged
	// ActionObsolete: the path was managed before but is no longer part of
	// staging (removed from pristine/generated inputs) - drop it from the
	// manifest and report it. It is never deleted from disk; that needs an
	// explicit "dzo mission prune".
	ActionObsolete
)

func (a Action) String() string {
	switch a {
	case ActionNew:
		return "new"
	case ActionUpdate:
		return "update"
	case ActionDrift:
		return "drift"
	case ActionUnchanged:
		return "unchanged"
	case ActionObsolete:
		return "obsolete"
	default:
		return "unknown"
	}
}

// PlanEntry is one path's classification.
type PlanEntry struct {
	Path        string
	Action      Action
	Origin      Origin // meaningless for ActionObsolete's target write; carried from the manifest there
	StagingHash string // "" for ActionObsolete
}

// Plan is the result of Classify: what Apply must do to bring the live
// mission in line with staging, without ever touching a path outside this
// set.
type Plan struct {
	Entries []PlanEntry
}

// Changed returns the entries Apply must actually write (new/update/drift),
// in sorted path order.
func (p Plan) Changed() []PlanEntry {
	var out []PlanEntry
	for _, e := range p.Entries {
		if e.Action == ActionNew || e.Action == ActionUpdate || e.Action == ActionDrift {
			out = append(out, e)
		}
	}
	return out
}

// Obsolete returns the entries no longer present in staging.
func (p Plan) Obsolete() []PlanEntry {
	var out []PlanEntry
	for _, e := range p.Entries {
		if e.Action == ActionObsolete {
			out = append(out, e)
		}
	}
	return out
}

// Empty reports whether applying this plan would change anything on disk.
func (p Plan) Empty() bool {
	return len(p.Changed()) == 0 && len(p.Obsolete()) == 0
}

// isGenerated reports whether relPath is below a generated-content directory
// (§C6: mod_*, custom_* and EditorFiles); everything else in staging is
// pristine-managed.
func isGenerated(relPath string) bool {
	first, _, _ := strings.Cut(relPath, "/")
	return strings.HasPrefix(first, "mod_") || strings.HasPrefix(first, "custom_") || first == "EditorFiles"
}

// isHardExcluded reports whether relPath must never be written or
// classified, regardless of configuration: storage_* holds live game/mod
// data and is excluded even if pristine happens to contain a matching path
// (§C6).
func isHardExcluded(relPath string) bool {
	first, _, _ := strings.Cut(relPath, "/")
	return strings.HasPrefix(first, "storage_")
}

// Classify walks stagingDir and compares it against liveDir + manifest to
// produce an apply plan. unmanaged is the instance's configured glob list
// (mission.unmanaged); paths matching it stay foreign even if pristine
// ships them, exactly like a hard exclusion.
func Classify(stagingDir, liveDir string, manifest *Manifest, unmanaged []string) (Plan, error) {
	observed := map[string]bool{}
	var plan Plan

	err := filepath.WalkDir(stagingDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(stagingDir, path)
		if err != nil {
			return fmt.Errorf("mission: relativize %s: %w", path, err)
		}
		rel = filepath.ToSlash(rel)

		if isHardExcluded(rel) || MatchAny(unmanaged, rel) {
			return nil
		}
		observed[rel] = true

		stagingHash, err := HashFile(path)
		if err != nil {
			return err
		}
		origin := OriginPristine
		if isGenerated(rel) {
			origin = OriginGenerated
		}

		entry, existed := manifest.Entries[rel]
		liveHash, err := HashFile(filepath.Join(liveDir, rel))
		if err != nil {
			return err
		}

		var action Action
		switch {
		case !existed || liveHash == "":
			action = ActionNew
		case liveHash != entry.WrittenHash:
			action = ActionDrift
		case stagingHash != entry.SourceHash:
			action = ActionUpdate
		default:
			action = ActionUnchanged
		}

		plan.Entries = append(plan.Entries, PlanEntry{Path: rel, Action: action, Origin: origin, StagingHash: stagingHash})
		return nil
	})
	if err != nil {
		return Plan{}, fmt.Errorf("mission: walk staging: %w", err)
	}

	for _, path := range manifest.Paths() {
		if observed[path] {
			continue
		}
		plan.Entries = append(plan.Entries, PlanEntry{Path: path, Action: ActionObsolete, Origin: manifest.Entries[path].Origin})
	}

	sort.Slice(plan.Entries, func(i, j int) bool { return plan.Entries[i].Path < plan.Entries[j].Path })
	return plan, nil
}
