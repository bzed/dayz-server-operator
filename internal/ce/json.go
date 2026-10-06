// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ce implements the merge strategies the render pipeline needs to
// combine mission, mod and overlay data (FR-09/FR-09a): JSON deep merge
// with "mod wins" semantics and append-instead-of-replace for selected list
// keys (cfggameplay.json, cfgundergroundtriggers.json, cfgeffectarea.json),
// plus generic XML child merge/registration for the economy config
// (cfgeconomycore.xml <ce folder> entries).
//
// The exact merge semantics of the legacy `xmlmerge` tool on real mod files
// are pinned down by spike S3 (§E of the plan), which has not run yet in
// this environment (it needs the mod XML fixtures from the legacy repos).
// This package therefore only implements the generic, documented
// primitives; per-file-type quirks (mapgroup*, cfgeventspawns, ...) are
// layered on top once S3 has verified fixtures.
package ce

import (
	"encoding/json"
	"fmt"
	"sort"
)

// MergeJSON deep-merges overlay into base ("mod wins", FR-09a): matching
// object keys are merged recursively, matching keys in appendKeys whose
// values are arrays on both sides are concatenated (base first, then
// overlay) instead of being replaced, and every other key in overlay
// replaces the one in base. Neither input is mutated.
func MergeJSON(base, overlay []byte, appendKeys []string) ([]byte, error) {
	var baseVal, overlayVal any
	if err := json.Unmarshal(base, &baseVal); err != nil {
		return nil, fmt.Errorf("ce: parse base JSON: %w", err)
	}
	if err := json.Unmarshal(overlay, &overlayVal); err != nil {
		return nil, fmt.Errorf("ce: parse overlay JSON: %w", err)
	}

	appendSet := make(map[string]bool, len(appendKeys))
	for _, k := range appendKeys {
		appendSet[k] = true
	}

	merged := deepMerge(baseVal, overlayVal, appendSet)

	out, err := marshalDeterministic(merged)
	if err != nil {
		return nil, fmt.Errorf("ce: marshal merged JSON: %w", err)
	}
	return out, nil
}

func deepMerge(base, overlay any, appendKeys map[string]bool) any {
	baseMap, baseIsMap := base.(map[string]any)
	overlayMap, overlayIsMap := overlay.(map[string]any)
	if baseIsMap && overlayIsMap {
		result := make(map[string]any, len(baseMap)+len(overlayMap))
		for k, v := range baseMap {
			result[k] = v
		}
		for k, ov := range overlayMap {
			bv, exists := result[k]
			if !exists {
				result[k] = ov
				continue
			}
			if appendKeys[k] {
				if bArr, ok1 := bv.([]any); ok1 {
					if oArr, ok2 := ov.([]any); ok2 {
						result[k] = appendUnique(bArr, oArr)
						continue
					}
				}
			}
			result[k] = deepMerge(bv, ov, appendKeys)
		}
		return result
	}
	// Non-object values (including arrays, unless handled by appendKeys
	// one level up): the overlay always wins.
	return overlay
}

// appendUnique concatenates base and overlay, dropping overlay entries that base (or an
// earlier overlay entry) already holds, so two contributors that list the same file do not
// register it twice. Entries are compared by their JSON form.
func appendUnique(base, overlay []any) []any {
	out := append([]any{}, base...)
	seen := map[string]bool{}
	for _, v := range out {
		b, _ := json.Marshal(v)
		seen[string(b)] = true
	}
	for _, v := range overlay {
		b, _ := json.Marshal(v)
		if seen[string(b)] {
			continue
		}
		seen[string(b)] = true
		out = append(out, v)
	}
	return out
}

// marshalDeterministic renders v with sorted object keys and 2-space
// indentation, so repeated renders of the same logical content produce
// byte-identical output (NFR-02).
func marshalDeterministic(v any) ([]byte, error) {
	rendered, err := renderValue(v, "")
	if err != nil {
		return nil, err
	}
	return []byte(rendered), nil
}

func renderValue(v any, indent string) (string, error) {
	switch val := v.(type) {
	case map[string]any:
		return renderObject(val, indent)
	case []any:
		return renderArray(val, indent)
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
}

func renderObject(m map[string]any, indent string) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	inner := indent + "  "
	out := "{\n"
	for i, k := range keys {
		keyJSON, err := json.Marshal(k)
		if err != nil {
			return "", err
		}
		valStr, err := renderValue(m[k], inner)
		if err != nil {
			return "", err
		}
		out += inner + string(keyJSON) + ": " + valStr
		if i < len(keys)-1 {
			out += ","
		}
		out += "\n"
	}
	out += indent + "}"
	return out, nil
}

func renderArray(a []any, indent string) (string, error) {
	if len(a) == 0 {
		return "[]", nil
	}
	inner := indent + "  "
	out := "[\n"
	for i, v := range a {
		valStr, err := renderValue(v, inner)
		if err != nil {
			return "", err
		}
		out += inner + valStr
		if i < len(a)-1 {
			out += ","
		}
		out += "\n"
	}
	out += indent + "]"
	return out, nil
}
