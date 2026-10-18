// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

const day = 24 * time.Hour

func TestRefsSurviveVacuum(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Seen(ctx, "alpha", guidOf(t, steamA), "192.0.2.7", "alice", t0)
	_ = s.Seen(ctx, "alpha", guidOf(t, steamB), "198.51.100.1", "bob", t0)
	ids, _ := s.Identities(ctx, steamA)
	b, err := s.CreateBan(ctx, "a", BanInput{SteamID: steamA, IPRefs: []string{ids[0].Ref}, Reason: "x", Scope: ScopeGlobal}, t0)
	if err != nil {
		t.Fatal(err)
	}
	var tref string
	for _, tg := range b.Targets {
		if tg.Kind == TargetIP {
			tref = tg.Ref
		}
	}
	// Rows deleted before the ones we keep would move rowids on a vacuum.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sightings WHERE guid = ?`, guidOf(t, steamB)); err != nil {
		t.Fatal(err)
	}
	if err := s.Vacuum(ctx); err != nil {
		t.Fatal(err)
	}
	ids2, _ := s.Identities(ctx, steamA)
	b2, _ := s.GetBan(ctx, b.ID)
	var tref2 string
	for _, tg := range b2.Targets {
		if tg.Kind == TargetIP {
			tref2 = tg.Ref
		}
	}
	if len(ids2) != 1 || ids2[0].Ref != ids[0].Ref || tref2 != tref {
		t.Fatalf("refs moved: %v -> %v, %s -> %s", ids[0].Ref, ids2, tref, tref2)
	}
}

func TestPurgeDropsOldAddressesButKeepsThePlayerAndBannedAddresses(t *testing.T) {
	s, _ := newStore(t)
	ga := guidOf(t, steamA)
	old := t0.Add(-100 * day)
	_ = s.Seen(ctx, "alpha", ga, "192.0.2.1", "alice-old", old)
	_ = s.Seen(ctx, "alpha", ga, "192.0.2.2", "alice", old.Add(day))
	_ = s.Seen(ctx, "alpha", ga, "192.0.2.3", "alice", old.Add(2*day)) // used for a ban below
	_ = s.Seen(ctx, "alpha", ga, "192.0.2.4", "alice", t0.Add(-10*day))
	_ = s.Seen(ctx, "beta", ga, "192.0.2.5", "alice", old)
	if _, err := s.CreateBan(ctx, "a", BanInput{IPs: []string{"192.0.2.3"}, Reason: "x", Scope: ScopeGlobal}, t0); err != nil {
		t.Fatal(err)
	}
	n, err := s.PurgeAddresses(ctx, t0.Add(-90*day))
	if err != nil || n != 3 {
		t.Fatalf("purged %d, %v", n, err)
	}
	ids, _ := s.Identities(ctx, steamA)
	got := map[string]Sighting{}
	for _, x := range ids {
		got[x.Instance+"/"+x.IP] = x
	}
	if len(ids) != 4 {
		t.Fatalf("want alpha: banned, recent and one merged row without address, beta: one; got %+v", ids)
	}
	for _, k := range []string{"alpha/192.0.2.3", "alpha/192.0.2.4", "alpha/", "beta/"} {
		if _, ok := got[k]; !ok {
			t.Errorf("no %q in %+v", k, got)
		}
	}
	merged := got["alpha/"]
	if merged.Name != "alice" || !merged.FirstSeen.Equal(old) || !merged.LastSeen.Equal(old.Add(day)) {
		t.Fatalf("the merged row keeps the newest name and the whole period: %+v", merged)
	}
	if ip, _, _ := s.CurrentIP(ctx, "alpha", steamA); ip != "192.0.2.4" {
		t.Fatalf("current IP %q", ip)
	}
	if _, err := s.CreateBan(ctx, "a", BanInput{SteamID: steamA, IPRefs: []string{merged.Ref}, Reason: "x", Scope: ScopeGlobal}, t0); err == nil {
		t.Fatal("a ban by a dropped address was accepted")
	}
	if n, err := s.PurgeAddresses(ctx, t0.Add(-90*day)); err != nil || n != 0 {
		t.Fatalf("second purge: %d %v", n, err)
	}
}

func TestVacuumIsDueOncePerPeriod(t *testing.T) {
	s, _ := newStore(t)
	if due, err := s.VacuumDue(ctx, t0, 7*day); err != nil || !due {
		t.Fatalf("never vacuumed: %v %v", due, err)
	}
	if err := s.MarkVacuumed(ctx, t0); err != nil {
		t.Fatal(err)
	}
	if due, _ := s.VacuumDue(ctx, t0.Add(6*day), 7*day); due {
		t.Fatal("due after 6 days")
	}
	if due, _ := s.VacuumDue(ctx, t0.Add(7*day), 7*day); !due {
		t.Fatal("not due after 7 days")
	}
}

func TestMigrationKeepsTheDataOfTheFirstSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dzo.sqlite")
	db, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := migrationsFS.ReadFile("migrations/sqlite/0001_init.sql")
	g := guidOf(t, steamA)
	for _, q := range []string{string(first), `CREATE TABLE schema_version (version INTEGER NOT NULL)`, `INSERT INTO schema_version VALUES (1)`,
		`INSERT INTO sightings VALUES ('` + g + `', '192.0.2.7', 'alice', 'alpha', 1, 2)`,
		`INSERT INTO bans (id, reason, message, created_by, created_at, scope) VALUES (1, 'x', 'm', 'a', 1, 'global')`,
		`INSERT INTO ban_targets VALUES (1, 'ip', '192.0.2.7', 0)`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = db.Close()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ids, err := s.Identities(ctx, steamA)
	if err != nil || len(ids) != 1 || ids[0].IP != "192.0.2.7" || ids[0].Ref == "" {
		t.Fatalf("sightings after the migration: %+v %v", ids, err)
	}
	b, err := s.GetBan(ctx, 1)
	if err != nil || len(b.Targets) != 1 || b.Targets[0].Ref == "" {
		t.Fatalf("ban after the migration: %+v %v", b, err)
	}
}
