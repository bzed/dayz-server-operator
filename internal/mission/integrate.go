// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/beevik/etree"

	"github.com/bzed/dayz-server-operator/internal/ce"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// ContribFile is one file a contributor (a mod integration or an overlay) brings. Name is the
// file's name in the mission ("types.xml", "cfggameplay.json", "env/zombie_territories.xml"), or
// for an extra file any relative path below the contributor's folder.
type ContribFile struct {
	Name string
	Data []byte
}

// Contribution is what one mod integration or overlay adds to the mission (§C6 steps 3 and 4).
type Contribution struct {
	Name   string // for the report: "mod 2291785308 (DayZExpansionCore)", "overlay loadout"
	Folder string // mod_<id> or custom_<name>: where its CE data and extra files go
	Files  []ContribFile
	// Overlay declares which of the contributor's extra files cfggameplay.json must reference.
	Overlay site.OverlayManifest
	// AfterMerge runs once the contribution is merged into the staging tree (its post_merge hooks).
	AfterMerge func(staging string) error
}

// IntegrateResult reports what Integrate did.
type IntegrateResult struct {
	Touched   []string // staging paths written or merged, relative, sorted
	Conflicts []string // two contributors replaced the same file: the later one won
	Notes     []string
}

// The merge strategies of §C6, by lower-case file name.
var (
	ceTypes = map[string]string{"types.xml": "types", "cfgspawnabletypes.xml": "spawnabletypes", "events.xml": "events"}
	// xmlAppend: file name -> mission path and root element.
	xmlAppend = map[string]struct{ path, root string }{
		"mapgrouppos.xml":        {"mapgrouppos.xml", "map"},
		"mapgroupproto.xml":      {"mapgroupproto.xml", "prototype"},
		"cfgeventgroups.xml":     {"cfgeventgroups.xml", "eventgroupdef"},
		"cfgeventspawns.xml":     {"cfgeventspawns.xml", "eventposdef"},
		"cfgenvironment.xml":     {"cfgenvironment.xml", "env"},
		"cfgrandompresets.xml":   {"cfgrandompresets.xml", "randompresets"},
		"zombie_territories.xml": {"env/zombie_territories.xml", "territory-type"},
	}
	jsonAppendKeys = map[string][]string{
		"cfggameplay.json":            {"objectSpawnersArr", "spawnGearPresetFiles", "playerRestrictedAreaFiles"},
		"cfgundergroundtriggers.json": {"Triggers"},
		"cfgeffectarea.json":          {"Areas", "SafePositions"},
	}
	replaceFiles = map[string]string{"cfgweather.xml": "cfgweather.xml", "messages.xml": "db/messages.xml"}
)

// Integrate merges the contributions into the staging tree, in order: a later contributor wins
// where a file is replaced, and the CE folders are registered in the same order. Nothing outside
// the staging tree is touched.
func Integrate(staging string, cs []Contribution) (IntegrateResult, error) {
	var res IntegrateResult
	touched := map[string]bool{}
	replacedBy := map[string]string{}
	touch := func(rel string) { touched[rel] = true }

	for _, c := range cs {
		var ceFiles []ce.CEFile
		var extras []string // folder-relative names of the extra files
		for _, f := range c.Files {
			if err := safeRel(f.Name); err != nil {
				return res, fmt.Errorf("mission: %s: %w", c.Name, err)
			}
			// Only a file at the top of the contribution (or the two names with their mission
			// folder) has a strategy; "spawns/types.xml" is an extra file like any other.
			base := strategyName(f.Name)
			var err error
			switch {
			case ceTypes[base] != "":
				rel := path.Join(c.Folder, base)
				err = WriteFile(staging, rel, f.Data)
				touch(rel)
				ceFiles = append(ceFiles, ce.CEFile{Name: base, Type: ceTypes[base]})
			case base == "globals.xml":
				rel := path.Join(c.Folder, base)
				err = WriteFile(staging, rel, f.Data)
				touch(rel)
				res.Notes = append(res.Notes, c.Name+": globals.xml is not a Central Economy file type, it is copied to "+c.Folder+"/ but not registered")
			case xmlAppend[base].path != "":
				x := xmlAppend[base]
				err = MergeXMLFile(staging, x.path, x.root, f.Data, "name")
				touch(x.path)
			case jsonAppendKeys[base] != nil:
				err = MergeJSONFile(staging, base, f.Data, jsonAppendKeys[base])
				touch(base)
			case replaceFiles[base] != "":
				target := replaceFiles[base]
				if prev := replacedBy[target]; prev != "" {
					res.Conflicts = append(res.Conflicts, fmt.Sprintf("%s: replaced by %s, which was replaced before by %s (the later one wins)", target, c.Name, prev))
				}
				replacedBy[target] = c.Name
				err = WriteFile(staging, target, f.Data)
				touch(target)
			case base == "init.c":
				err = patchInit(staging, f.Data)
				touch("init.c")
			default:
				rel := path.Join(c.Folder, f.Name)
				err = WriteFile(staging, rel, f.Data)
				touch(rel)
				extras = append(extras, f.Name)
			}
			if err != nil {
				return res, fmt.Errorf("mission: %s: %s: %w", c.Name, f.Name, err)
			}
		}
		if len(ceFiles) > 0 {
			if err := RegisterCEFolder(staging, "cfgeconomycore.xml", c.Folder, ceFiles); err != nil {
				return res, err
			}
			touch("cfgeconomycore.xml")
		}
		if frag, err := overlayReferences(c, extras); err != nil {
			return res, err
		} else if frag != nil {
			if err := MergeJSONFile(staging, "cfggameplay.json", frag, jsonAppendKeys["cfggameplay.json"]); err != nil {
				return res, err
			}
			touch("cfggameplay.json")
		}
		if c.AfterMerge != nil {
			if err := c.AfterMerge(staging); err != nil {
				return res, fmt.Errorf("mission: %s: post_merge: %w", c.Name, err)
			}
		}
	}
	for rel := range touched {
		res.Touched = append(res.Touched, rel)
	}
	sort.Strings(res.Touched)
	return res, nil
}

// strategyName maps a contributed file name to the key of the strategy tables, or "" when the
// file is an extra file.
func strategyName(name string) string {
	n := strings.ToLower(name)
	switch n {
	case "env/zombie_territories.xml":
		return "zombie_territories.xml"
	case "db/messages.xml":
		return "messages.xml"
	}
	if strings.Contains(n, "/") {
		return ""
	}
	return n
}

// overlayReferences builds the cfggameplay.json fragment that references an overlay's declared
// files with their path below the mission: custom_<name>/<file>.
func overlayReferences(c Contribution, extras []string) ([]byte, error) {
	lists := []struct {
		section, key string
		globs        []string
	}{
		{"WorldsData", "objectSpawnersArr", c.Overlay.ObjectSpawners},
		{"PlayerData", "spawnGearPresetFiles", c.Overlay.SpawnGearPresets},
		{"WorldsData", "playerRestrictedAreaFiles", c.Overlay.RestrictedAreas},
	}
	frag := map[string]map[string][]string{}
	for _, l := range lists {
		var refs []string
		for _, g := range l.globs {
			for _, e := range extras {
				if ok, err := path.Match(g, e); err != nil {
					return nil, fmt.Errorf("mission: %s: bad pattern %q: %w", c.Name, g, err)
				} else if ok {
					refs = append(refs, path.Join(c.Folder, e))
				}
			}
		}
		if len(refs) > 0 {
			sort.Strings(refs)
			if frag[l.section] == nil {
				frag[l.section] = map[string][]string{}
			}
			frag[l.section][l.key] = refs
		}
	}
	if len(frag) == 0 {
		return nil, nil
	}
	return json.Marshal(frag)
}

// safeRel rejects names that would leave the staging tree.
func safeRel(name string) error {
	clean := path.Clean(name)
	if name == "" || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsRune(name, 0) {
		return fmt.Errorf("file name %q is not a relative path inside the mission", name)
	}
	return nil
}

// patchInit applies a unified diff to init.c in the staging tree with patch(1) (a contributor's
// init.c is a patch, not a file).
func patchInit(staging string, diff []byte) error {
	target := filepath.Join(staging, "init.c")
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("the mission has no init.c to patch")
	}
	cmd := exec.Command("patch", "-s", "-p0", "--no-backup-if-mismatch", target) //nolint:gosec // fixed arguments, the target is below the staging directory
	cmd.Stdin = bytes.NewReader(diff)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("patching init.c: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ValidateStaging checks what the merge produced (§C6 step 5): every touched XML and JSON file
// parses, and every <ce folder> of cfgeconomycore.xml exists with the files it names. Any error
// means the live mission must stay as it is.
func ValidateStaging(staging string, touched []string) error {
	var errs []string
	for _, rel := range touched {
		data, err := os.ReadFile(filepath.Join(staging, rel)) //nolint:gosec // a path Integrate wrote below the staging tree
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		switch strings.ToLower(path.Ext(rel)) {
		case ".xml":
			if err := etree.NewDocument().ReadFromBytes(data); err != nil {
				errs = append(errs, fmt.Sprintf("%s: not well-formed XML: %v", rel, err))
			}
		case ".json":
			if !json.Valid(data) {
				errs = append(errs, rel+": not valid JSON")
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(staging, "cfgeconomycore.xml")); err == nil { //nolint:gosec // inside the staging tree
		doc := etree.NewDocument()
		if err := doc.ReadFromBytes(data); err == nil {
			for _, ceEl := range doc.FindElements("//ce") {
				folder := ceEl.SelectAttrValue("folder", "")
				for _, f := range ceEl.SelectElements("file") {
					name := f.SelectAttrValue("name", "")
					if _, err := os.Stat(filepath.Join(staging, folder, name)); err != nil {
						errs = append(errs, fmt.Sprintf("cfgeconomycore.xml: <ce folder=%q> names %s, which does not exist", folder, name))
					}
				}
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("mission: staging is not valid:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}
