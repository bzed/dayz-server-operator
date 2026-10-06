// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"fmt"
	"os"
	"path/filepath"
)

// BaseFiles are the mission files every instance needs, and that a map's
// mission repo may lack: they are filled in from the fallback mission (§C6,
// legacy step 5). It is the legacy refresh list.
var BaseFiles = []string{
	"env/zombie_territories.xml", "cfgrandompresets.xml", "mapgrouppos.xml", "mapgroupproto.xml",
	"cfgeconomycore.xml", "cfgenvironment.xml", "cfgeventgroups.xml", "cfgeventspawns.xml",
	"cfggameplay.json", "cfgundergroundtriggers.json", "cfgeffectarea.json", "cfgweather.xml", "init.c",
}

// RenderInput describes one instance's mission render.
type RenderInput struct {
	PristineDir    string
	FallbackDir    string // optional: mission dir to take missing BaseFiles from
	LiveDir        string
	ManifestPath   string
	FileHistoryDir string
	Unmanaged      []string
	DryRun         bool
	Options        Options
	// Contributions are merged into the staging tree after the pristine mission, in order (the
	// instance's mods, then its overlays). OnIntegrated, if set, gets what the merge reported.
	Contributions []Contribution
	OnIntegrated  func(IntegrateResult)
}

// Render builds the staging tree from the pristine mission (plus the
// fallback fill), classifies it against the live mission and, unless DryRun,
// applies it in place and saves the manifest. The first render into an empty
// live dir is the one-time initialisation; later renders only touch files in
// the manifest or new in pristine. Nothing is written when the plan fails.
func Render(in RenderInput) (Plan, Report, error) {
	staging, err := BuildStaging(in)
	if err != nil {
		return Plan{}, Report{}, err
	}
	defer os.RemoveAll(staging) //nolint:errcheck // scratch dir

	manifest, err := LoadManifest(in.ManifestPath)
	if err != nil {
		return Plan{}, Report{}, err
	}
	plan, err := Classify(staging, in.LiveDir, manifest, in.Unmanaged)
	if err != nil || in.DryRun {
		return plan, Report{}, err
	}
	if err := os.MkdirAll(filepath.Dir(in.ManifestPath), 0o750); err != nil {
		return plan, Report{}, fmt.Errorf("mission: %w", err)
	}
	report, err := Apply(plan, staging, in.LiveDir, in.FileHistoryDir, manifest, in.Options)
	if err != nil {
		return plan, report, err
	}
	return plan, report, manifest.Save(in.ManifestPath)
}

// BuildStaging writes what a render would apply, the pristine mission plus the
// fallback fill, into a new temporary directory and returns it; the caller
// removes it. It touches neither the live mission nor the manifest, which is
// what the boot test feeds to a disposable server tree.
func BuildStaging(in RenderInput) (string, error) {
	staging, err := os.MkdirTemp("", "dzo-staging-")
	if err != nil {
		return "", fmt.Errorf("mission: %w", err)
	}
	if err := CopyPristine(in.PristineDir, staging); err != nil {
		_ = os.RemoveAll(staging)
		return "", err
	}
	if in.FallbackDir != "" {
		if err := fillFromFallback(in.FallbackDir, staging); err != nil {
			_ = os.RemoveAll(staging)
			return "", err
		}
	}
	if len(in.Contributions) > 0 {
		res, err := Integrate(staging, in.Contributions)
		if err == nil {
			err = ValidateStaging(staging, res.Touched)
		}
		if err != nil {
			_ = os.RemoveAll(staging)
			return "", err
		}
		if in.OnIntegrated != nil {
			in.OnIntegrated(res)
		}
	}
	return staging, nil
}

func fillFromFallback(fallbackDir, staging string) error {
	if fi, err := os.Stat(fallbackDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("mission: fallback mission %s not found", fallbackDir)
	}
	for _, rel := range BaseFiles {
		if _, err := os.Stat(filepath.Join(staging, rel)); err == nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(fallbackDir, rel)) //nolint:gosec // rel is from the fixed BaseFiles list
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("mission: read fallback %s: %w", rel, err)
		}
		if err := WriteFile(staging, rel, data); err != nil {
			return err
		}
	}
	return nil
}
