// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSightingsAndSharedIPs(t *testing.T) {
	s, _ := newStore(t)
	ga, gb := guidOf(t, steamA), guidOf(t, steamB)
	must := func(err error) {
		t.Helper()
		require.NoError(t, err)
	}
	must(s.Seen(t.Context(), "alpha", ga, "192.0.2.7", "alice", t0))
	must(s.Seen(t.Context(), "alpha", ga, "192.0.2.7", "alice2", t0.Add(time.Hour))) // same row, newer name
	must(s.Seen(t.Context(), "beta", ga, "198.51.100.1", "alice", t0))
	must(s.Seen(t.Context(), "alpha", gb, "192.0.2.7", "bob", t0)) // shares the first IP
	ids, err := s.Identities(t.Context(), steamA)
	require.NoError(t, err)
	require.Len(t, ids, 2)
	byIP := map[string]Sighting{}
	for _, x := range ids {
		byIP[x.IP] = x
	}
	x := byIP["192.0.2.7"]
	require.Equal(t, 1, x.SharedWith)
	require.Equal(t, "alice2", x.Name)
	require.True(t, x.FirstSeen.Equal(t0), "shared IP sighting %+v", x)
	require.True(t, x.LastSeen.Equal(t0.Add(time.Hour)), "shared IP sighting %+v", x)
	x = byIP["198.51.100.1"]
	require.Equal(t, 0, x.SharedWith)
	err = s.Seen(t.Context(), "alpha", "nope", "192.0.2.7", "x", t0)
	require.Error(t, err, "an invalid GUID was recorded")
}
