// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package ce

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustMergeJSON(t testing.TB, base, overlay string, appendKeys []string) map[string]any {
	t.Helper()
	out, err := MergeJSON([]byte(base), []byte(overlay), appendKeys)
	require.NoError(t, err, "MergeJSON")
	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result), "re-parse merged JSON, output:\n%s", out)
	return result
}

func TestMergeJSONScalarOverlayWins(t *testing.T) {
	result := mustMergeJSON(t, `{"a": 1, "b": 2}`, `{"b": 20, "c": 3}`, nil)
	assert.Equal(t, map[string]any{"a": float64(1), "b": float64(20), "c": float64(3)}, result)
}

func TestMergeJSONNestedObjectsMergeRecursively(t *testing.T) {
	base := `{"weather": {"rain": 1, "fog": 2}}`
	overlay := `{"weather": {"fog": 20, "wind": 3}}`
	result := mustMergeJSON(t, base, overlay, nil)
	weather := result["weather"].(map[string]any)
	assert.Equal(t, map[string]any{"rain": float64(1), "fog": float64(20), "wind": float64(3)}, weather)
}

func TestMergeJSONArrayReplacedByDefault(t *testing.T) {
	base := `{"tags": ["a", "b"]}`
	overlay := `{"tags": ["c"]}`
	result := mustMergeJSON(t, base, overlay, nil)
	tags := result["tags"].([]any)
	assert.Equal(t, []any{"c"}, tags, "overlay replaces the array")
}

func TestMergeJSONArrayAppendedForListedKeys(t *testing.T) {
	base := `{"Triggers": [1, 2]}`
	overlay := `{"Triggers": [3, 4]}`
	result := mustMergeJSON(t, base, overlay, []string{"Triggers"})
	triggers := result["Triggers"].([]any)
	want := []float64{1, 2, 3, 4}
	require.Len(t, triggers, len(want))
	for i, w := range want {
		assert.Equal(t, w, triggers[i])
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
	assert.Equal(t, float64(2), triggers["count"])
}

func TestMergeJSONNewKeyFromOverlayOnly(t *testing.T) {
	result := mustMergeJSON(t, `{}`, `{"only": "here"}`, nil)
	assert.Equal(t, "here", result["only"])
}

func TestMergeJSONDeterministicOutput(t *testing.T) {
	base := `{"z": 1, "a": {"y": 2, "x": 3}}`
	overlay := `{}`
	out1, err := MergeJSON([]byte(base), []byte(overlay), nil)
	require.NoError(t, err, "MergeJSON")
	out2, err := MergeJSON([]byte(base), []byte(overlay), nil)
	require.NoError(t, err, "MergeJSON")
	require.Equal(t, string(out2), string(out1))
	// Keys must be sorted in the output.
	wantOrder := `{
  "a": {
    "x": 3,
    "y": 2
  },
  "z": 1
}`
	assert.Equal(t, wantOrder, string(out1))
}

func TestMergeJSONEmptyObjectAndArray(t *testing.T) {
	out, err := MergeJSON([]byte(`{"a": {}, "b": []}`), []byte(`{}`), nil)
	require.NoError(t, err, "MergeJSON")
	want := "{\n  \"a\": {},\n  \"b\": []\n}"
	assert.Equal(t, want, string(out))
}

func TestMergeJSONInvalidBase(t *testing.T) {
	_, err := MergeJSON([]byte("not json"), []byte("{}"), nil)
	require.Error(t, err, "expected an error for invalid base JSON")
}

func TestMergeJSONInvalidOverlay(t *testing.T) {
	_, err := MergeJSON([]byte("{}"), []byte("not json"), nil)
	require.Error(t, err, "expected an error for invalid overlay JSON")
}

func TestMergeJSONTopLevelArray(t *testing.T) {
	out, err := MergeJSON([]byte(`[1,2]`), []byte(`[3,4]`), nil)
	require.NoError(t, err, "MergeJSON")
	// Top-level arrays are not objects, so the overlay replaces wholesale.
	var result []any
	err = json.Unmarshal(out, &result)
	require.NoError(t, err, "re-parse")
	assert.Equal(t, []any{float64(3), float64(4)}, result)
}

func TestMergeJSONAppendKeysDropDuplicates(t *testing.T) {
	base := []byte(`{"WorldsData":{"objectSpawnersArr":["a.json","b.json"]}}`)
	overlay := []byte(`{"WorldsData":{"objectSpawnersArr":["b.json","c.json","c.json"]}}`)
	out, err := MergeJSON(base, overlay, []string{"objectSpawnersArr"})
	require.NoError(t, err)
	var got struct {
		WorldsData struct {
			ObjectSpawnersArr []string `json:"objectSpawnersArr"`
		} `json:"WorldsData"`
	}
	err = json.Unmarshal(out, &got)
	require.NoError(t, err)
	assert.Equal(t, "a.json,b.json,c.json", strings.Join(got.WorldsData.ObjectSpawnersArr, ","))
}
