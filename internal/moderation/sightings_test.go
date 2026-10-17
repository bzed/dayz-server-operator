// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"testing"
	"time"
)

func TestSightingsAndSharedIPs(t *testing.T) {
	s, _ := newStore(t)
	ga, gb := guidOf(t, steamA), guidOf(t, steamB)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Seen(ctx, "alpha", ga, "192.0.2.7", "alice", t0))
	must(s.Seen(ctx, "alpha", ga, "192.0.2.7", "alice2", t0.Add(time.Hour))) // same row, newer name
	must(s.Seen(ctx, "beta", ga, "198.51.100.1", "alice", t0))
	must(s.Seen(ctx, "alpha", gb, "192.0.2.7", "bob", t0)) // shares the first IP
	ids, err := s.Identities(ctx, steamA)
	if err != nil || len(ids) != 2 {
		t.Fatalf("Identities = %+v, %v", ids, err)
	}
	byIP := map[string]Sighting{}
	for _, x := range ids {
		byIP[x.IP] = x
	}
	if x := byIP["192.0.2.7"]; x.SharedWith != 1 || x.Name != "alice2" || !x.FirstSeen.Equal(t0) || !x.LastSeen.Equal(t0.Add(time.Hour)) {
		t.Fatalf("shared IP sighting %+v", x)
	}
	if x := byIP["198.51.100.1"]; x.SharedWith != 0 {
		t.Fatalf("own IP sighting %+v", x)
	}
	if err := s.Seen(ctx, "alpha", "nope", "192.0.2.7", "x", t0); err == nil {
		t.Fatal("an invalid GUID was recorded")
	}
}
