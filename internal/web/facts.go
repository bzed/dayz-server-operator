// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/admin"
)

// span formats a duration like map.js span(): "<1m", "12m", "3h 12m", "2d 4h".
func span(d time.Duration) string {
	s := int64(d / time.Second)
	if s < 60 {
		return "<1m"
	}
	m, h, days := s/60%60, s/3600%24, s/86400
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// factsView is what an instance's overview shows about its game server: the facts that used to be only in the
// map's title. The texts are ready to print; the live map updates them in place.
type factsView struct {
	Connected bool
	World     string
	Mod       string
	Clock     string // the in-game time "14:05", empty when the mod does not report one
	Day       bool   // the clock is between 06:00 and 18:00, for the sun or moon glyph
	Players   int
	Up        string // "up 3h 12m"
	Restart   string // "restart in 47m" or "restart now"; empty without a schedule
	RestartAt time.Time
}

func factsOf(s admin.Status, now time.Time) factsView {
	f := factsView{Connected: s.Connected, Players: s.Players}
	if s.Hello != nil {
		f.World, f.Mod = s.Hello.World, s.Hello.ModVersion
	}
	if s.Connected {
		if s.Hello != nil && !s.Hello.Since.IsZero() {
			f.Up = "up " + span(now.Sub(s.Hello.Since))
		}
		if hr, err := strconv.Atoi(strings.SplitN(s.GameTime, ":", 2)[0]); err == nil {
			f.Clock, f.Day = s.GameTime, hr >= 6 && hr < 18
		}
	}
	if s.NextRestart != nil {
		f.RestartAt = *s.NextRestart
		if s.NextRestart.After(now) {
			f.Restart = "restart in " + span(s.NextRestart.Sub(now))
		} else {
			f.Restart = "restart now"
		}
	}
	return f
}
