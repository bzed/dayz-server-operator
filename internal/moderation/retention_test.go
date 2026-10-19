// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const day = 24 * time.Hour

func TestRefsSurviveVacuum(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Seen(t.Context(), "alpha", guidOf(t, steamA), "192.0.2.7", "alice", t0)
	_ = s.Seen(t.Context(), "alpha", guidOf(t, steamB), "198.51.100.1", "bob", t0)
	ids, _ := s.Identities(t.Context(), steamA)
	b, err := s.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, IPRefs: []string{ids[0].Ref}, Reason: "x", Scope: ScopeGlobal}, t0)
	require.NoError(t, err)
	var tref string
	for _, tg := range b.Targets {
		if tg.Kind == TargetIP {
			tref = tg.Ref
		}
	}
	// Rows deleted before the ones we keep would move rowids on a vacuum.
	_, err = s.db.ExecContext(t.Context(), `DELETE FROM sightings WHERE guid = ?`, guidOf(t, steamB))
	require.NoError(t, err)
	err = s.Vacuum(t.Context())
	require.NoError(t, err)
	ids2, _ := s.Identities(t.Context(), steamA)
	b2, _ := s.GetBan(t.Context(), b.ID)
	var tref2 string
	for _, tg := range b2.Targets {
		if tg.Kind == TargetIP {
			tref2 = tg.Ref
		}
	}
	require.Len(t, ids2, 1, "refs moved: %v -> %v, %s -> %s", ids[0].Ref, ids2, tref, tref2)
	require.Equal(t, ids[0].Ref, ids2[0].Ref, "refs moved: %v -> %v, %s -> %s", ids[0].Ref, ids2, tref, tref2)
	require.Equal(t, tref, tref2, "refs moved: %v -> %v, %s -> %s", ids[0].Ref, ids2, tref, tref2)
}

func TestPurgeDropsOldAddressesButKeepsThePlayerAndBannedAddresses(t *testing.T) {
	s, _ := newStore(t)
	ga := guidOf(t, steamA)
	old := t0.Add(-100 * day)
	_ = s.Seen(t.Context(), "alpha", ga, "192.0.2.1", "alice-old", old)
	_ = s.Seen(t.Context(), "alpha", ga, "192.0.2.2", "alice", old.Add(day))
	_ = s.Seen(t.Context(), "alpha", ga, "192.0.2.3", "alice", old.Add(2*day)) // used for a ban below
	_ = s.Seen(t.Context(), "alpha", ga, "192.0.2.4", "alice", t0.Add(-10*day))
	_ = s.Seen(t.Context(), "beta", ga, "192.0.2.5", "alice", old)
	_, err := s.CreateBan(t.Context(), "a", BanInput{IPs: []string{"192.0.2.3"}, Reason: "x", Scope: ScopeGlobal}, t0)
	require.NoError(t, err)
	n, err := s.PurgeAddresses(t.Context(), t0.Add(-90*day))
	require.NoError(t, err)
	require.Equal(t, int64(3), n)
	ids, _ := s.Identities(t.Context(), steamA)
	got := map[string]Sighting{}
	for _, x := range ids {
		got[x.Instance+"/"+x.IP] = x
	}
	require.Len(t, ids, 4)
	for _, k := range []string{"alpha/192.0.2.3", "alpha/192.0.2.4", "alpha/", "beta/"} {
		_, ok := got[k]
		assert.True(t, ok, "no %q in %+v", k, got)
	}
	merged := got["alpha/"]
	require.Equal(t, "alice", merged.Name)
	require.True(t, merged.FirstSeen.Equal(old), "the merged row keeps the newest name and the whole period: %+v", merged)
	require.True(t, merged.LastSeen.Equal(old.Add(day)), "the merged row keeps the newest name and the whole period: %+v", merged)
	ip, _, _ := s.CurrentIP(t.Context(), "alpha", steamA)
	require.Equal(t, "192.0.2.4", ip)
	_, err = s.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, IPRefs: []string{merged.Ref}, Reason: "x", Scope: ScopeGlobal}, t0)
	require.Error(t, err, "a ban by a dropped address was accepted")
	n, err = s.PurgeAddresses(t.Context(), t0.Add(-90*day))
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
}

func TestVacuumIsDueOncePerPeriod(t *testing.T) {
	s, _ := newStore(t)
	due, err := s.VacuumDue(t.Context(), t0, 7*day)
	require.NoError(t, err)
	require.True(t, due)
	err = s.MarkVacuumed(t.Context(), t0)
	require.NoError(t, err)
	due, _ = s.VacuumDue(t.Context(), t0.Add(6*day), 7*day)
	require.False(t, due, "due after 6 days")
	due, _ = s.VacuumDue(t.Context(), t0.Add(7*day), 7*day)
	require.True(t, due, "not due after 7 days")
}

func TestMigrationKeepsTheDataOfTheFirstSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dzo.sqlite")
	db, err := sql.Open("sqlite", dsn(path, false))
	require.NoError(t, err)
	first, _ := migrationsFS.ReadFile("migrations/sqlite/0001_init.sql")
	g := guidOf(t, steamA)
	for _, q := range []string{string(first), `CREATE TABLE schema_version (version INTEGER NOT NULL)`, `INSERT INTO schema_version VALUES (1)`,
		`INSERT INTO sightings VALUES ('` + g + `', '192.0.2.7', 'alice', 'alpha', 1, 2)`,
		`INSERT INTO bans (id, reason, message, created_by, created_at, scope) VALUES (1, 'x', 'm', 'a', 1, 'global')`,
		`INSERT INTO ban_targets VALUES (1, 'ip', '192.0.2.7', 0)`} {
		_, err := db.Exec(q)
		require.NoError(t, err, "%s", q)
	}
	_ = db.Close()
	s, err := Open(t.Context(), path)
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	ids, err := s.Identities(t.Context(), steamA)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	require.Equal(t, "192.0.2.7", ids[0].IP)
	require.NotEmpty(t, ids[0].Ref)
	b, err := s.GetBan(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, b.Targets, 1)
	require.NotEmpty(t, b.Targets[0].Ref)
}
