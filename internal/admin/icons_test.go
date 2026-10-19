// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIconNamesAndUnknownIcons(t *testing.T) {
	dir := t.TempDir()
	css := filepath.Join(dir, "fork-awesome", "css")
	err := os.MkdirAll(css, 0o750)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(css, "fork-awesome.css"), []byte(".fa-fire:before{content:\"\\f06d\"}\n.fa-user-secret:before{content:\"\\f21b\"}\n.fa{display:inline-block}\n"), 0o600)
	require.NoError(t, err)
	names := IconNames(dir)
	// helicopter is not in Fork Awesome; dzo ships it itself (web/static/icons).
	require.True(t, names["fire"])
	require.True(t, names["user-secret"])
	require.True(t, names["helicopter"])
	require.Len(t, names, 3)
	rules := []WatchRule{{Layer: "a", Icon: "fire"}, {Layer: "b", Icon: "fa-user-secret"}, {Layer: "c", Icon: "firee"}, {Layer: "d"}, {Layer: "e", Icon: "helicopter"}}
	got := UnknownIcons(rules, names)
	assert.Equal(t, []string{"firee"}, got)
	require.Nil(t, IconNames(t.TempDir()))
	assert.Nil(t, UnknownIcons(rules, nil))
}

func TestWatchRuleIconAndColorValidation(t *testing.T) {
	ok := WatchRule{Layer: "l", Classes: []string{"X"}, Icon: "fire", Color: "#c0392b"}
	err := ok.Validate()
	require.NoError(t, err)
	for _, w := range []WatchRule{
		{Layer: "l", Classes: []string{"X"}, Icon: "Fire"},
		{Layer: "l", Classes: []string{"X"}, Icon: "fa fa-fire"},
		{Layer: "l", Classes: []string{"X"}, Color: "#12"},
		{Layer: "l", Classes: []string{"X"}, Color: "url(x)"},
		{Layer: "l", Classes: []string{"X"}, Visibility: "root"},
	} {
		assert.Error(t, w.Validate(), "%+v should be rejected", w)
	}
}

func TestHubFillsMarkerStyleFromTheLayer(t *testing.T) {
	h := NewHub("x")
	h.Styles = map[string]Layer{"fires": {Icon: "fire", Color: "#e67e22"}}
	h.Sync(SyncRequest{
		Markers: []Marker{{Layer: "fires", ID: "1"}, {Layer: "fires", ID: "2", Color: "red"}, {Layer: "mod", ID: "3"}, {Layer: "plain", ID: "4"}},
		Layers:  []Layer{{Name: "mod", Icon: "car", Color: "blue"}},
	})
	byID := map[string]Marker{}
	for _, m := range h.Markers("") {
		byID[m.ID] = m
	}
	for id, want := range map[string][2]string{"1": {"fire", "#e67e22"}, "2": {"fire", "red"}, "3": {"car", "blue"}, "4": {"", ""}} {
		m := byID[id]
		require.Equal(t, want[0], m.Icon)
		assert.Equal(t, want[1], m.Color)
	}
}

func TestHubAppliesTheVisibilityOfAWatchRule(t *testing.T) {
	h := NewHub("x")
	h.Styles = map[string]Layer{"events": {Visibility: "viewer"}, "secret": {}}
	h.Sync(SyncRequest{Markers: []Marker{
		{Layer: "events", ID: "1", Source: "watch", Visibility: "admin"},
		{Layer: "events", ID: "2", Source: "api", Visibility: "admin"},
		{Layer: "secret", ID: "3", Source: "watch", Visibility: "admin"},
	}})
	got := map[string]string{}
	for _, m := range h.Markers("") {
		got[m.ID] = m.Visibility
	}
	require.Equal(t, "viewer", got["1"])
	require.Equal(t, "admin", got["2"])
	assert.Equal(t, "admin", got["3"])
	for _, l := range h.Layers() {
		if l.Name == "events" {
			assert.Equal(t, "viewer", l.Visibility, "layer events visibility")
		}
	}
}
