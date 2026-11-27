// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import "strings"

// PlayerBySender finds the online player who sent a chat line that BattlEye printed as "name: text".
// The line has no boundary between the name and the text, so a name that contains ": " is cut at its
// first ": ". When another online player's name could also have sent the line (it, then ": ", is the
// start of "name: text"), the sender is ambiguous and no player is returned: a Steam ID that may be
// the wrong player's is worse than none.
func (h *Hub) PlayerBySender(name, text string) (Player, bool) {
	p, ok := h.PlayerByName(name)
	if !ok {
		return Player{}, false
	}
	line := name + ": " + text
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, o := range h.players {
		if o.Name != name && strings.HasPrefix(line, o.Name+": ") {
			return Player{}, false
		}
	}
	return p, true
}
