// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestIconNamesAndUnknownIcons(t *testing.T) {
	dir := t.TempDir()
	css := filepath.Join(dir, "fork-awesome", "css")
	if err := os.MkdirAll(css, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(css, "fork-awesome.css"), []byte(".fa-fire:before{content:\"\\f06d\"}\n.fa-user-secret:before{content:\"\\f21b\"}\n.fa{display:inline-block}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	names := IconNames(dir)
	if !names["fire"] || !names["user-secret"] || len(names) != 2 {
		t.Fatalf("names = %v", names)
	}
	rules := []WatchRule{{Layer: "a", Icon: "fire"}, {Layer: "b", Icon: "fa-user-secret"}, {Layer: "c", Icon: "firee"}, {Layer: "d"}}
	if got := UnknownIcons(rules, names); !slices.Equal(got, []string{"firee"}) {
		t.Errorf("unknown = %v", got)
	}
	if IconNames(t.TempDir()) != nil || UnknownIcons(rules, nil) != nil {
		t.Error("without the stylesheet nothing can be checked, and nothing is reported")
	}
}

func TestWatchRuleIconAndColorValidation(t *testing.T) {
	ok := WatchRule{Layer: "l", Classes: []string{"X"}, Icon: "fire", Color: "#c0392b"}
	if err := ok.Validate(); err != nil {
		t.Error(err)
	}
	for _, w := range []WatchRule{
		{Layer: "l", Classes: []string{"X"}, Icon: "Fire"},
		{Layer: "l", Classes: []string{"X"}, Icon: "fa fa-fire"},
		{Layer: "l", Classes: []string{"X"}, Color: "#12"},
		{Layer: "l", Classes: []string{"X"}, Color: "url(x)"},
	} {
		if w.Validate() == nil {
			t.Errorf("%+v should be rejected", w)
		}
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
		if m := byID[id]; m.Icon != want[0] || m.Color != want[1] {
			t.Errorf("marker %s = icon %q color %q, want %v", id, m.Icon, m.Color, want)
		}
	}
}
