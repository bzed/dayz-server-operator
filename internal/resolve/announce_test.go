// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package resolve

import (
	"reflect"
	"testing"
	"time"

	"github.com/bzed/dayz-server-operator/internal/instance"
	"github.com/bzed/dayz-server-operator/internal/site"
)

func TestAnnouncementOfCopiesTheTimelineAndRules(t *testing.T) {
	a := site.Announce{Minutes: 30, Lock: 7, Kick: 3, Ways: []site.AnnounceWay{
		{Within: site.Duration(10 * time.Minute), Way: "expansion", Type: "baguette", Every: site.Duration(time.Minute), Now: site.Duration(10 * time.Second)},
		{Way: "rcon", At: []site.Duration{site.Duration(30 * time.Minute)}},
	}}
	got := AnnouncementOf(a, "Restart")
	want := instance.Announcement{Minutes: 30, Lock: 7, Kick: 3, Text: "Restart", Rules: []instance.AnnounceRule{
		{Within: 10 * time.Minute, Way: "expansion", Type: "baguette", Every: time.Minute, Now: 10 * time.Second},
		{Way: "rcon", At: []time.Duration{30 * time.Minute}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AnnouncementOf = %+v, want %+v", got, want)
	}
}

func TestAnnouncementOfWithoutWaysLeavesTheDefaultToTheCountdown(t *testing.T) {
	got := AnnouncementOf(site.Announce{Minutes: 5}, "")
	if got.Rules != nil || got.Minutes != 5 {
		t.Errorf("AnnouncementOf = %+v", got)
	}
}
