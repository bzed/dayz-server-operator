// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package ce

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustMergeJSON(t *testing.T, base, overlay string, appendKeys []string) map[string]any {
	t.Helper()
	out, err := MergeJSON([]byte(base), []byte(overlay), appendKeys)
	if err != nil {
		t.Fatalf("MergeJSON: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("re-parse merged JSON: %v\noutput:\n%s", err, out)
	}
	return result
}

func TestMergeJSONScalarOverlayWins(t *testing.T) {
	result := mustMergeJSON(t, `{"a": 1, "b": 2}`, `{"b": 20, "c": 3}`, nil)
	if result["a"] != float64(1) || result["b"] != float64(20) || result["c"] != float64(3) {
		t.Errorf("result = %+v", result)
	}
}

func TestMergeJSONNestedObjectsMergeRecursively(t *testing.T) {
	base := `{"weather": {"rain": 1, "fog": 2}}`
	overlay := `{"weather": {"fog": 20, "wind": 3}}`
	result := mustMergeJSON(t, base, overlay, nil)
	weather := result["weather"].(map[string]any)
	if weather["rain"] != float64(1) || weather["fog"] != float64(20) || weather["wind"] != float64(3) {
		t.Errorf("weather = %+v", weather)
	}
}

func TestMergeJSONArrayReplacedByDefault(t *testing.T) {
	base := `{"tags": ["a", "b"]}`
	overlay := `{"tags": ["c"]}`
	result := mustMergeJSON(t, base, overlay, nil)
	tags := result["tags"].([]any)
	if len(tags) != 1 || tags[0] != "c" {
		t.Errorf("tags = %+v, want overlay to replace the array", tags)
	}
}

func TestMergeJSONArrayAppendedForListedKeys(t *testing.T) {
	base := `{"Triggers": [1, 2]}`
	overlay := `{"Triggers": [3, 4]}`
	result := mustMergeJSON(t, base, overlay, []string{"Triggers"})
	triggers := result["Triggers"].([]any)
	want := []float64{1, 2, 3, 4}
	if len(triggers) != len(want) {
		t.Fatalf("Triggers = %+v", triggers)
	}
	for i, w := range want {
		if triggers[i] != w {
			t.Errorf("Triggers[%d] = %v, want %v", i, triggers[i], w)
		}
	}
}

func TestMergeJSONAppendKeyOnlyAffectsArrays(t *testing.T) {
	// "Triggers" is in appendKeys, but here it's not an array on both
	// sides, so the normal (overlay wins) rule must still apply instead of
	// panicking.
	base := `{"Triggers": {"count": 1}}`
	overlay := `{"Triggers": {"count": 2}}`
	result := mustMergeJSON(t, base, overlay, []string{"Triggers"})
	triggers := result["Triggers"].(map[string]any)
	if triggers["count"] != float64(2) {
		t.Errorf("Triggers = %+v", triggers)
	}
}

func TestMergeJSONNewKeyFromOverlayOnly(t *testing.T) {
	result := mustMergeJSON(t, `{}`, `{"only": "here"}`, nil)
	if result["only"] != "here" {
		t.Errorf("result = %+v", result)
	}
}

func TestMergeJSONDeterministicOutput(t *testing.T) {
	base := `{"z": 1, "a": {"y": 2, "x": 3}}`
	overlay := `{}`
	out1, err := MergeJSON([]byte(base), []byte(overlay), nil)
	if err != nil {
		t.Fatalf("MergeJSON: %v", err)
	}
	out2, err := MergeJSON([]byte(base), []byte(overlay), nil)
	if err != nil {
		t.Fatalf("MergeJSON: %v", err)
	}
	if string(out1) != string(out2) {
		t.Fatalf("merge is not deterministic:\n%s\nvs\n%s", out1, out2)
	}
	// Keys must be sorted in the output.
	wantOrder := `{
  "a": {
    "x": 3,
    "y": 2
  },
  "z": 1
}`
	if string(out1) != wantOrder {
		t.Errorf("output = %s, want %s", out1, wantOrder)
	}
}

func TestMergeJSONEmptyObjectAndArray(t *testing.T) {
	out, err := MergeJSON([]byte(`{"a": {}, "b": []}`), []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("MergeJSON: %v", err)
	}
	want := "{\n  \"a\": {},\n  \"b\": []\n}"
	if string(out) != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestMergeJSONInvalidBase(t *testing.T) {
	if _, err := MergeJSON([]byte("not json"), []byte("{}"), nil); err == nil {
		t.Fatal("expected an error for invalid base JSON")
	}
}

func TestMergeJSONInvalidOverlay(t *testing.T) {
	if _, err := MergeJSON([]byte("{}"), []byte("not json"), nil); err == nil {
		t.Fatal("expected an error for invalid overlay JSON")
	}
}

func TestMergeJSONTopLevelArray(t *testing.T) {
	out, err := MergeJSON([]byte(`[1,2]`), []byte(`[3,4]`), nil)
	if err != nil {
		t.Fatalf("MergeJSON: %v", err)
	}
	// Top-level arrays are not objects, so the overlay replaces wholesale.
	var result []any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(result) != 2 || result[0] != float64(3) {
		t.Errorf("result = %+v", result)
	}
}

func TestMergeJSONAppendKeysDropDuplicates(t *testing.T) {
	base := []byte(`{"WorldsData":{"objectSpawnersArr":["a.json","b.json"]}}`)
	overlay := []byte(`{"WorldsData":{"objectSpawnersArr":["b.json","c.json","c.json"]}}`)
	out, err := MergeJSON(base, overlay, []string{"objectSpawnersArr"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		WorldsData struct {
			ObjectSpawnersArr []string `json:"objectSpawnersArr"`
		} `json:"WorldsData"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.WorldsData.ObjectSpawnersArr, ",") != "a.json,b.json,c.json" {
		t.Errorf("list = %v, want each file once, base order first", got.WorldsData.ObjectSpawnersArr)
	}
}
