// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func snaps(reasons ...Reason) []Snapshot {
	var out []Snapshot
	for i, r := range reasons {
		out = append(out, Snapshot{ID: fmt.Sprintf("s%02d", i), Reason: r, State: Complete, Created: t0.Add(time.Duration(i) * time.Hour)})
	}
	return out
}

func ids(ss []Snapshot) string {
	s := ""
	for _, x := range ss {
		s += x.ID + " "
	}
	return s
}

func TestSelectKeepsTheNewest(t *testing.T) {
	ss := snaps(Manual, Manual, Manual, Manual, Manual)
	got := Select(ss, Policy{Keep: 2, MinKeep: 1}, t0.Add(10*time.Hour))
	assert.Equal(t, "s00 s01 s02 ", ids(got))
	got = Select(ss, Policy{}, t0.Add(time.Hour))
	assert.Empty(t, got)
}

func TestSelectPerReasonAndAge(t *testing.T) {
	ss := snaps(ModUpdate, Update, ModUpdate, ModUpdate, Manual, ModUpdate)
	got := Select(ss, Policy{KeepByReason: map[Reason]int{ModUpdate: 2}, MinKeep: 1}, t0.Add(10*time.Hour))
	assert.Equal(t, "s00 s02 ", ids(got))
	got = Select(ss, Policy{MaxAge: 3*time.Hour + time.Minute, MinKeep: 1}, t0.Add(6*time.Hour))
	assert.Equal(t, "s00 s01 s02 ", ids(got))
}

func TestSelectProtections(t *testing.T) {
	ss := snaps(Manual, Manual, Manual, Manual)
	ss[0].Pinned = true
	ss[1].State = Creating
	ss[2].State = Deleting
	got := Select(ss, Policy{Keep: 1, MaxAge: time.Minute, MinKeep: 0}, t0.Add(48*time.Hour))
	assert.Empty(t, got)

	ss = snaps(Manual, Manual, Manual, Manual, Manual)
	got = Select(ss, Policy{MaxAge: time.Minute, MinKeep: 3}, t0.Add(48*time.Hour))
	assert.Equal(t, "s00 s01 ", ids(got))
	// a pinned snapshot counts toward min_keep
	ss[4].Pinned = true
	got = Select(ss, Policy{MaxAge: time.Minute, MinKeep: 3}, t0.Add(48*time.Hour))
	assert.Equal(t, "s00 s01 ", ids(got))
}

func TestSelectProperties(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	states := []State{Complete, Complete, Complete, Creating, Deleting, Missing}
	for n := 0; n < 3000; n++ {
		var ss []Snapshot
		for i := 0; i < rnd.Intn(30); i++ {
			ss = append(ss, Snapshot{
				ID: fmt.Sprintf("s%02d", i), Reason: Reasons[rnd.Intn(len(Reasons))], State: states[rnd.Intn(len(states))],
				Created: t0.Add(time.Duration(rnd.Intn(500)) * time.Hour), Pinned: rnd.Intn(5) == 0,
			})
		}
		p := Policy{Keep: rnd.Intn(8), MaxAge: time.Duration(rnd.Intn(300)) * time.Hour, MinKeep: rnd.Intn(5),
			KeepByReason: map[Reason]int{Reasons[rnd.Intn(len(Reasons))]: rnd.Intn(4)}}
		got := Select(ss, p, t0.Add(500*time.Hour))
		again := Select(ss, p, t0.Add(500*time.Hour))
		require.Equal(t, ids(got), ids(again))
		deleted := map[string]bool{}
		for _, d := range got {
			deleted[d.ID] = true
			require.False(t, d.Pinned)
			require.Equal(t, Complete, d.State)
		}
		var complete []Snapshot
		for _, s := range ss {
			if s.State == Complete {
				complete = append(complete, s)
			}
		}
		if len(complete) == 0 {
			continue
		}
		newest := complete[0]
		for _, s := range complete {
			if s.Created.After(newest.Created) {
				newest = s
			}
		}
		for _, s := range complete { // a tie for newest: at least one of them stays
			if s.Created.Equal(newest.Created) && !deleted[s.ID] {
				newest = s
				break
			}
		}
		require.False(t, deleted[newest.ID], "the newest complete snapshot must stay: %+v policy %+v", newest, p)
		left := len(complete) - len(got)
		require.GreaterOrEqual(t, left, min(p.MinKeep, len(complete)), "complete snapshots left, min_keep %d (had %d)", p.MinKeep, len(complete))
	}
}

func TestOldestForSpace(t *testing.T) {
	ss := snaps(Manual, Manual, Manual, Manual)
	ss[1].Pinned = true
	got := Oldest(ss, 2)
	assert.Equal(t, "s00 s02 ", ids(got))
	got = Oldest(ss, 4)
	assert.Empty(t, got)
	got = Oldest(nil, 1)
	assert.Nil(t, got)
}

func TestValidReason(t *testing.T) {
	require.True(t, ValidReason(Manual))
	assert.False(t, ValidReason("whatever"))
}
