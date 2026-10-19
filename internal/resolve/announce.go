// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package resolve

import (
	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// AnnouncementOf is the countdown of an announce: block with the text of the messages. Without
// announce.ways the rules stay empty, which makes the countdown use its default (the same as
// site.Announce.EffectiveWays).
func AnnouncementOf(a site.Announce, text string) instance.Announcement {
	out := instance.Announcement{Minutes: a.Minutes, Lock: a.Lock, Kick: a.Kick, Text: text}
	for _, w := range a.Ways {
		r := instance.AnnounceRule{Within: w.Within.Std(), Way: w.Way, Type: w.Type, Every: w.Every.Std(), Now: w.Now.Std()}
		for _, at := range w.At {
			r.At = append(r.At, at.Std())
		}
		out.Rules = append(out.Rules, r)
	}
	return out
}
