// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moddeps

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Patch is one CfgPatches child class: the name other addons'
// requiredAddons entries refer to, and what it itself requires.
type Patch struct {
	Name           string
	RequiredAddons []string
}

// ExtractPatches reads every child class of root's top-level CfgPatches
// class. A config with no CfgPatches class returns (nil, nil): that is not
// itself an error here (some configs - e.g. a mission config.cpp - never
// have one), though a real mod PBO missing CfgPatches would be a problem
// for the caller to judge.
func ExtractPatches(root *Class) ([]Patch, error) {
	patches, ok := root.Classes["CfgPatches"]
	if !ok {
		return nil, nil
	}
	result := make([]Patch, 0, len(patches.Classes))
	for name, cls := range patches.Classes {
		p := Patch{Name: name}
		if req, ok := cls.Properties["requiredAddons"]; ok {
			if !req.IsArray {
				return nil, fmt.Errorf("moddeps: CfgPatches.%s.requiredAddons is not an array", name)
			}
			p.RequiredAddons = req.Array
		}
		result = append(result, p)
	}
	return result, nil
}

// ExtractPatchesFromConfig extracts CfgPatches from data, which is either
// a rapified config.bin (detected by its magic) or config.cpp source.
func ExtractPatchesFromConfig(data []byte) ([]Patch, error) {
	parse := ParseConfig
	if IsRapified(data) {
		parse = ParseRapified
	}
	root, err := parse(data)
	if err != nil && !IsRapified(data) {
		// Real config.cpp files use enum blocks, macros and expressions the parser does not
		// know; only CfgPatches matters here, so retry on that block alone.
		if block := cfgPatchesSource(string(data)); block != "" {
			root, err = ParseConfig([]byte(block))
		}
	}
	if err != nil {
		return nil, err
	}
	return ExtractPatches(root)
}

// cfgPatchesSource cuts "class CfgPatches { ... };" out of config.cpp source by brace matching
// (skipping comments and strings), or returns "" when there is none.
func cfgPatchesSource(src string) string {
	start := -1
	depth := 0
	for i := 0; i < len(src); i++ {
		switch c := src[i]; {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return ""
			}
			i += end + 3
		case c == '"':
			for i++; i < len(src); i++ {
				if src[i] == '"' {
					if i+1 < len(src) && src[i+1] == '"' { // "" is a quote inside a string
						i++
						continue
					}
					break
				}
			}
		case start < 0 && depth == 0 && (c == 'c' || c == 'C'):
			if m := cfgPatchesHead.FindStringIndex(src[i:]); m != nil && m[0] == 0 && (i == 0 || !isIdentPart(src[i-1])) {
				start = i
				i += m[1] - 1
				depth = 1
			}
		case start >= 0 && c == '{':
			depth++
		case start >= 0 && c == '}':
			depth--
			if depth == 0 {
				return src[start:i+1] + ";"
			}
		}
	}
	return ""
}

var cfgPatchesHead = regexp.MustCompile(`(?i)^class\s+CfgPatches\s*\{`)

// ExtractPatchesFromPBO reads every config.bin of the PBO (or the config.cpp of
// a folder that has no config.bin) and extracts their CfgPatches entries;
// compressed entries are decompressed transparently. Terrain and object packs
// keep one config per subfolder (bushes\config.bin, ...), and the engine loads
// them all as parts of the one addon.
func ExtractPatchesFromPBO(pbo *PBO) ([]Patch, error) {
	var configs []Entry
	has := map[string]bool{} // folders with a config.bin
	for _, e := range pbo.Entries {
		dir, base := splitEntry(e.Name)
		if base == "config.bin" {
			has[dir] = true
			configs = append(configs, e)
		}
	}
	for _, e := range pbo.Entries {
		if dir, base := splitEntry(e.Name); base == "config.cpp" && !has[dir] {
			configs = append(configs, e)
		}
	}
	if len(configs) == 0 {
		return nil, errors.New("moddeps: PBO has no config.bin or config.cpp entry")
	}
	var all []Patch
	for _, e := range configs {
		data, err := pbo.ReadEntry(e)
		if err != nil {
			return nil, err
		}
		// Some tools store LZSS data in an entry that is not flagged as compressed.
		if e.PackingMethod != packingCompressed && !IsRapified(data) && len(data) > 5 && IsRapified(data[1:]) {
			if un, err := decompressLZSS(data, -1); err == nil && IsRapified(un) {
				data = un
			}
		}
		patches, err := ExtractPatchesFromConfig(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name, err)
		}
		all = append(all, patches...)
	}
	return all, nil
}

// splitEntry returns the lower-cased folder and file name of a PBO entry name ("/" and "\" both separate).
func splitEntry(name string) (dir, base string) {
	name = strings.ToLower(strings.ReplaceAll(name, `\`, "/"))
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}
