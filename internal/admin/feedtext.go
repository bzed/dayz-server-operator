// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"regexp"
	"strings"
)

// aiFactionKey matches the untranslated stringtable key the game reports for an Expansion AI faction.
var aiFactionKey = regexp.MustCompile(`#STR_EXPANSION_AI_FACTION_([A-Z0-9_]+)`)

// delocalize turns the stringtable keys of the Expansion AI factions that the mod could not translate
// on the server ("#STR_EXPANSION_AI_FACTION_WEST") into readable text ("West"). Other text is kept.
func delocalize(s string) string {
	if !strings.Contains(s, "#STR_EXPANSION_AI_FACTION_") {
		return s
	}
	return aiFactionKey.ReplaceAllStringFunc(s, func(m string) string {
		words := strings.Split(strings.ToLower(aiFactionKey.FindStringSubmatch(m)[1]), "_")
		for i, w := range words {
			if w != "" {
				words[i] = strings.ToUpper(w[:1]) + w[1:]
			}
		}
		return strings.Join(words, " ")
	})
}
