// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var faSelectorRe = regexp.MustCompile(`\.fa-([a-z0-9]+(?:-[a-z0-9]+)*):before`)

// IconNames returns the icon names of the Fork Awesome stylesheet below assets (Debian's
// fonts-fork-awesome, served with the other web assets), or nil if it is not installed there.
func IconNames(assets string) map[string]bool {
	css, err := os.ReadFile(filepath.Join(assets, "fork-awesome", "css", "fork-awesome.css")) //nolint:gosec // the configured web assets directory
	if err != nil {
		return nil
	}
	names := map[string]bool{}
	for _, m := range faSelectorRe.FindAllStringSubmatch(string(css), -1) {
		names[m[1]] = true
	}
	// The icons dzo ships itself, because Fork Awesome has none (see web/static/app.css).
	for _, n := range ownIcons {
		names[n] = true
	}
	return names
}

// ownIcons are the icons the web interface draws itself, next to the ones of Fork Awesome.
var ownIcons = []string{"helicopter"}

// UnknownIcons lists the icons of the watch rules that the stylesheet does not have. names comes
// from IconNames; without it nothing can be said, and nothing is reported.
func UnknownIcons(rules []WatchRule, names map[string]bool) []string {
	if len(names) == 0 {
		return nil
	}
	var out []string
	for _, w := range rules {
		if n := strings.TrimPrefix(w.Icon, "fa-"); n != "" && !names[n] {
			out = append(out, w.Icon)
		}
	}
	return out
}
