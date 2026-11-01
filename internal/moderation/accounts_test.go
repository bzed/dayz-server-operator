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

func putAnn(t *testing.T, s *Store, grants ...Grant) {
	t.Helper()
	require.NoError(t, s.PutPerson(t.Context(), Person{SteamID: steamA, Name: "Ann", Grants: grants}, t0))
}

func TestPutPersonReplacesGrants(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"alpha", "operator"}, Grant{"*", "viewer"})
	p, err := s.Person(t.Context(), steamA)
	require.NoError(t, err)
	assert.Equal(t, "Ann", p.Name)
	assert.Equal(t, t0.Unix(), p.Created.Unix())
	assert.Equal(t, []Grant{{"*", "viewer"}, {"alpha", "operator"}}, p.Grants, "sorted by instance")

	later := t0.Add(time.Hour)
	require.NoError(t, s.PutPerson(t.Context(), Person{SteamID: steamA, Name: "Anna", Grants: []Grant{{"beta", "moderator"}}}, later))
	p, err = s.Person(t.Context(), steamA)
	require.NoError(t, err)
	assert.Equal(t, "Anna", p.Name)
	assert.Equal(t, t0.Unix(), p.Created.Unix(), "created stays")
	assert.Equal(t, []Grant{{"beta", "moderator"}}, p.Grants)

	people, err := s.People(t.Context())
	require.NoError(t, err)
	assert.Len(t, people, 1)
	_, err = s.Person(t.Context(), steamB)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestPutPersonValidates(t *testing.T) {
	s, _ := newStore(t)
	for name, p := range map[string]Person{
		"steam id":  {SteamID: "123", Grants: nil},
		"role":      {SteamID: steamA, Grants: []Grant{{"alpha", "root"}}},
		"detector":  {SteamID: steamA, Grants: []Grant{{"alpha", "detector"}}},
		"instance":  {SteamID: steamA, Grants: []Grant{{"a b", "viewer"}}},
		"empty":     {SteamID: steamA, Grants: []Grant{{"", "viewer"}}},
		"duplicate": {SteamID: steamA, Grants: []Grant{{"alpha", "viewer"}, {"alpha", "admin"}}},
	} {
		err := s.PutPerson(t.Context(), p, t0)
		var inv *InvalidError
		assert.ErrorAs(t, err, &inv, name)
	}
}

func TestCredentialsSurvivePutPerson(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"*", "admin"})
	require.NoError(t, s.SetPassword(t.Context(), steamA, "ann", "hash"))
	require.NoError(t, s.SetTOTP(t.Context(), steamA, []byte("secret")))
	putAnn(t, s, Grant{"alpha", "viewer"})
	c, err := s.Credentials(t.Context(), steamA)
	require.NoError(t, err)
	assert.Equal(t, "hash", c.PasswordHash)
	assert.Equal(t, []byte("secret"), c.TOTPSecret)
}

func TestDisableDropsSessions(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"*", "admin"})
	require.NoError(t, s.CreateSession(t.Context(), "h1", steamA, "1.2.3.4", t0))

	require.NoError(t, s.PutPerson(t.Context(), Person{SteamID: steamA, Name: "Ann", Grants: []Grant{{"*", "admin"}}}, t0))
	_, err := s.Session(t.Context(), "h1", t0, time.Hour, time.Hour)
	require.NoError(t, err, "a plain update keeps the session")

	require.NoError(t, s.PutPerson(t.Context(), Person{SteamID: steamA, Name: "Ann", Disabled: true, Grants: []Grant{{"*", "admin"}}}, t0))
	_, err = s.Session(t.Context(), "h1", t0, time.Hour, time.Hour)
	require.ErrorIs(t, err, ErrNotFound, "disabling ends the sessions")

	putAnn(t, s, Grant{"*", "admin"})
	require.NoError(t, s.CreateSession(t.Context(), "h2", steamA, "", t0))
	putAnn(t, s)
	_, err = s.Session(t.Context(), "h2", t0, time.Hour, time.Hour)
	require.ErrorIs(t, err, ErrNotFound, "removing the last grant ends the sessions")
}

func TestUsernameUnique(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"*", "admin"})
	require.NoError(t, s.PutPerson(t.Context(), Person{SteamID: steamB, Name: "Bob", Grants: []Grant{{"*", "viewer"}}}, t0))
	require.NoError(t, s.SetPassword(t.Context(), steamA, "Ann", "h"))
	p, err := s.PersonByUsername(t.Context(), "ANN")
	require.NoError(t, err)
	assert.Equal(t, steamA, p.SteamID)
	assert.Equal(t, "ann", p.Username, "login names are case-insensitive and stored in lower case")

	err = s.SetPassword(t.Context(), steamB, "aNN", "h2")
	require.ErrorIs(t, err, ErrUsernameTaken)
	require.NoError(t, s.SetPassword(t.Context(), steamA, "ann", "h3"), "one may set their own name again")
	require.NoError(t, s.SetPassword(t.Context(), steamA, "", "h4"), "an empty name keeps the old one")
	p, _ = s.Person(t.Context(), steamA)
	assert.Equal(t, "ann", p.Username)
	_, err = s.PersonByUsername(t.Context(), "nobody")
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, s.SetPassword(t.Context(), "76561198000000099", "x", "h"), ErrNotFound)
}

func TestResetCredentials(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"alpha", "operator"})
	ctx := t.Context()
	require.NoError(t, s.SetPassword(ctx, steamA, "ann", "h"))
	require.NoError(t, s.SetTOTP(ctx, steamA, []byte("s")))
	require.NoError(t, s.AddPasskey(ctx, steamA, Passkey{ID: []byte("k1"), Name: "key", Data: []byte("{}"), Created: t0}))
	require.NoError(t, s.SetRecoveryCodes(ctx, steamA, []string{"a", "b"}))
	require.NoError(t, s.CreateSession(ctx, "h1", steamA, "", t0))
	require.NoError(t, s.CreatePending(ctx, "p1", PendingLogin{SteamID: steamA, Via: "steam", Expires: t0.Add(time.Hour)}))

	require.NoError(t, s.ResetCredentials(ctx, steamA))
	c, err := s.Credentials(ctx, steamA)
	require.NoError(t, err)
	assert.Equal(t, Credentials{}, c)
	_, err = s.PersonByUsername(ctx, "ann")
	require.NoError(t, err, "the username is who the person is: it stays")
	_, err = s.Session(ctx, "h1", t0, time.Hour, time.Hour)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.Pending(ctx, "p1", t0)
	require.ErrorIs(t, err, ErrNotFound)
	p, err := s.Person(ctx, steamA)
	require.NoError(t, err)
	assert.Equal(t, []Grant{{"alpha", "operator"}}, p.Grants, "person and grants stay")
	require.ErrorIs(t, s.ResetCredentials(ctx, steamB), ErrNotFound)
}

func TestDeletePersonCascades(t *testing.T) {
	s, path := newStore(t)
	putAnn(t, s, Grant{"*", "admin"})
	ctx := t.Context()
	require.NoError(t, s.AddPasskey(ctx, steamA, Passkey{ID: []byte("k1"), Data: []byte("{}"), Created: t0}))
	require.NoError(t, s.SetRecoveryCodes(ctx, steamA, []string{"a"}))
	require.NoError(t, s.CreateSession(ctx, "h1", steamA, "", t0))
	require.NoError(t, s.DeletePerson(ctx, steamA))
	require.ErrorIs(t, s.DeletePerson(ctx, steamA), ErrNotFound)

	db, err := sql.Open("sqlite", dsn(path, true))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	for _, tbl := range []string{"people", "grants", "passkeys", "recovery_codes", "login_sessions"} {
		var n int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM `+tbl).Scan(&n))
		assert.Zero(t, n, tbl)
	}
}

func TestPasskeys(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"*", "admin"})
	ctx := t.Context()
	require.NoError(t, s.AddPasskey(ctx, steamA, Passkey{ID: []byte("k1"), Name: "laptop", Data: []byte(`{"n":1}`), Created: t0}))
	require.NoError(t, s.AddPasskey(ctx, steamA, Passkey{ID: []byte("k2"), Name: "phone", Data: []byte(`{}`), Created: t0.Add(time.Minute)}))
	c, err := s.Credentials(ctx, steamA)
	require.NoError(t, err)
	require.Len(t, c.Passkeys, 2)
	assert.Equal(t, "laptop", c.Passkeys[0].Name, "oldest first")
	assert.True(t, c.Passkeys[0].LastUsed.IsZero())

	used := t0.Add(time.Hour)
	require.NoError(t, s.UpdatePasskey(ctx, steamA, []byte("k1"), []byte(`{"n":2}`), used))
	c, _ = s.Credentials(ctx, steamA)
	assert.Equal(t, []byte(`{"n":2}`), c.Passkeys[0].Data)
	assert.Equal(t, used.Unix(), c.Passkeys[0].LastUsed.Unix())
	require.ErrorIs(t, s.UpdatePasskey(ctx, steamA, []byte("nope"), nil, used), ErrNotFound)

	require.ErrorIs(t, s.DeletePasskey(ctx, steamB, []byte("k1")), ErrNotFound, "another person's key is not theirs to delete")
	require.NoError(t, s.DeletePasskey(ctx, steamA, []byte("k1")))
	require.ErrorIs(t, s.DeletePasskey(ctx, steamA, []byte("k1")), ErrNotFound)
}

func TestTOTPAndRecoveryCounts(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"*", "admin"})
	ctx := t.Context()
	require.NoError(t, s.SetTOTP(ctx, steamA, []byte("s1")))
	require.NoError(t, s.SetRecoveryCodes(ctx, steamA, []string{"a", "b", "c"}))
	c, _ := s.Credentials(ctx, steamA)
	assert.Equal(t, 3, c.RecoveryLeft)
	require.NoError(t, s.SetRecoveryCodes(ctx, steamA, []string{"x"}))
	c, _ = s.Credentials(ctx, steamA)
	assert.Equal(t, 1, c.RecoveryLeft, "a new set replaces the old one")
	ok, err := s.UseRecoveryCode(ctx, steamA, "a")
	require.NoError(t, err)
	assert.False(t, ok, "an old code")
	require.NoError(t, s.ClearTOTP(ctx, steamA))
	c, _ = s.Credentials(ctx, steamA)
	assert.Nil(t, c.TOTPSecret)
}

func raceOnce(t *testing.T, fn func() (bool, error)) int {
	t.Helper()
	const n = 8
	res := make(chan bool, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			<-start
			ok, err := fn()
			assert.NoError(t, err)
			res <- ok
		}()
	}
	close(start)
	won := 0
	for i := 0; i < n; i++ {
		if <-res {
			won++
		}
	}
	return won
}

func TestUseTOTPStepOnce(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"*", "admin"})
	require.NoError(t, s.SetTOTP(t.Context(), steamA, []byte("s")))
	assert.Equal(t, 1, raceOnce(t, func() (bool, error) { return s.UseTOTPStep(t.Context(), steamA, 41152263) }))
	ok, err := s.UseTOTPStep(t.Context(), steamA, 41152263)
	require.NoError(t, err)
	assert.False(t, ok, "the same step again")
	ok, _ = s.UseTOTPStep(t.Context(), steamA, 41152262)
	assert.False(t, ok, "an earlier step")
	ok, _ = s.UseTOTPStep(t.Context(), steamA, 41152264)
	assert.True(t, ok, "a later step")
	ok, _ = s.UseTOTPStep(t.Context(), steamB, 41152265)
	assert.False(t, ok, "no such person")
}

func TestUseRecoveryCodeOnce(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"*", "admin"})
	require.NoError(t, s.SetRecoveryCodes(t.Context(), steamA, []string{"h1", "h2"}))
	assert.Equal(t, 1, raceOnce(t, func() (bool, error) { return s.UseRecoveryCode(t.Context(), steamA, "h1") }))
	c, _ := s.Credentials(t.Context(), steamA)
	assert.Equal(t, 1, c.RecoveryLeft)
}

func TestSessionIdleAndMax(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"*", "admin"})
	ctx := t.Context()
	idle, max := 12*time.Hour, 7*24*time.Hour
	require.NoError(t, s.CreateSession(ctx, "h1", steamA, "1.2.3.4", t0))
	got, err := s.Session(ctx, "h1", t0.Add(time.Hour), idle, max)
	require.NoError(t, err)
	assert.Equal(t, steamA, got.SteamID)
	assert.Equal(t, "1.2.3.4", got.Client)
	assert.Equal(t, t0.Unix(), got.Created.Unix())

	// busy: seen every 10 hours, so never idle, but dead after the maximum
	at := t0
	for i := 0; i < 16; i++ {
		at = at.Add(10 * time.Hour)
		if _, err := s.Session(ctx, "h1", at, idle, max); err != nil {
			break
		}
	}
	_, err = s.Session(ctx, "h1", t0.Add(max+time.Hour), idle, max)
	require.ErrorIs(t, err, ErrNotFound, "past the maximum")

	require.NoError(t, s.CreateSession(ctx, "h2", steamA, "", t0))
	_, err = s.Session(ctx, "h2", t0.Add(idle+time.Minute), idle, max)
	require.ErrorIs(t, err, ErrNotFound, "idle for too long")
	_, err = s.Session(ctx, "unknown", t0, idle, max)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestSessionTouchThrottled(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"*", "admin"})
	ctx := t.Context()
	require.NoError(t, s.CreateSession(ctx, "h1", steamA, "", t0))
	got, _ := s.Session(ctx, "h1", t0.Add(30*time.Second), time.Hour, time.Hour)
	assert.Equal(t, t0.Unix(), got.LastUsed.Unix(), "within a minute nothing is written")
	got, _ = s.Session(ctx, "h1", t0.Add(2*time.Minute), time.Hour, time.Hour)
	assert.Equal(t, t0.Add(2*time.Minute).Unix(), got.LastUsed.Unix(), "after a minute it is touched")
	got, _ = s.Session(ctx, "h1", t0.Add(2*time.Minute+10*time.Second), time.Hour, time.Hour)
	assert.Equal(t, t0.Add(2*time.Minute).Unix(), got.LastUsed.Unix())
}

func TestDeleteSessions(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"*", "admin"})
	ctx := t.Context()
	for _, h := range []string{"h1", "h2", "h3"} {
		require.NoError(t, s.CreateSession(ctx, h, steamA, "", t0))
	}
	require.NoError(t, s.DeleteSession(ctx, "h1"))
	require.NoError(t, s.DeleteSessions(ctx, steamA, "h2"))
	_, err := s.Session(ctx, "h2", t0, time.Hour, time.Hour)
	require.NoError(t, err, "the one kept")
	_, err = s.Session(ctx, "h3", t0, time.Hour, time.Hour)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, s.DeleteSessions(ctx, steamA, ""))
	_, err = s.Session(ctx, "h2", t0, time.Hour, time.Hour)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestPendingExpires(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"*", "admin"})
	ctx := t.Context()
	p := PendingLogin{SteamID: steamA, Via: "password", Enrol: true, Ceremony: []byte("c"), TOTPSecret: []byte("t"), Expires: t0.Add(5 * time.Minute)}
	require.NoError(t, s.CreatePending(ctx, "p1", p))
	got, err := s.Pending(ctx, "p1", t0.Add(4*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, "password", got.Via)
	assert.True(t, got.Enrol)
	assert.Equal(t, []byte("c"), got.Ceremony)
	assert.Equal(t, []byte("t"), got.TOTPSecret)
	assert.Equal(t, p.Expires.Unix(), got.Expires.Unix())

	got.Ceremony = []byte("c2")
	require.NoError(t, s.UpdatePending(ctx, "p1", got))
	got, _ = s.Pending(ctx, "p1", t0)
	assert.Equal(t, []byte("c2"), got.Ceremony)
	require.ErrorIs(t, s.UpdatePending(ctx, "nope", got), ErrNotFound)

	_, err = s.Pending(ctx, "p1", t0.Add(5*time.Minute))
	require.ErrorIs(t, err, ErrNotFound, "expired")
	require.NoError(t, s.DeletePending(ctx, "p1"))
	_, err = s.Pending(ctx, "p1", t0)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestSteamNonceOnce(t *testing.T) {
	s, _ := newStore(t)
	assert.Equal(t, 1, raceOnce(t, func() (bool, error) { return s.UseSteamNonce(t.Context(), "n1", t0) }))
	ok, err := s.UseSteamNonce(t.Context(), "n2", t0)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestPruneLogins(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"*", "admin"})
	ctx := t.Context()
	idle, max := 12*time.Hour, 7*24*time.Hour
	require.NoError(t, s.CreateSession(ctx, "old", steamA, "", t0))
	now := t0.Add(8 * 24 * time.Hour)
	require.NoError(t, s.CreateSession(ctx, "new", steamA, "", now))
	require.NoError(t, s.CreatePending(ctx, "p-old", PendingLogin{SteamID: steamA, Via: "steam", Expires: t0.Add(5 * time.Minute)}))
	require.NoError(t, s.CreatePending(ctx, "p-new", PendingLogin{SteamID: steamA, Via: "steam", Expires: now.Add(5 * time.Minute)}))
	_, _ = s.UseSteamNonce(ctx, "n-old", t0)
	_, _ = s.UseSteamNonce(ctx, "n-new", now)

	require.NoError(t, s.PruneLogins(ctx, now, idle, max))

	_, err := s.Session(ctx, "new", now, idle, max)
	require.NoError(t, err)
	_, err = s.Pending(ctx, "p-new", now)
	require.NoError(t, err)
	ok, _ := s.UseSteamNonce(ctx, "n-new", now)
	assert.False(t, ok, "a recent nonce is still remembered")
	ok, _ = s.UseSteamNonce(ctx, "n-old", now)
	assert.True(t, ok, "an old nonce was pruned (Steam would no longer accept it)")

	var n int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM login_sessions`).Scan(&n))
	assert.Equal(t, 1, n)
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM pending_logins`).Scan(&n))
	assert.Equal(t, 1, n)
}

func TestMigration0004FromV3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dzo.sqlite")
	db, err := sql.Open("sqlite", dsn(path, false))
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`)
	require.NoError(t, err)
	for i, f := range []string{"0001_init.sql", "0002_ids_and_retention.sql", "0003_players.sql"} {
		body, err := migrationsFS.ReadFile("migrations/sqlite/" + f)
		require.NoError(t, err)
		_, err = db.Exec(string(body))
		require.NoError(t, err, f)
		_, err = db.Exec(`INSERT INTO schema_version VALUES (?)`, i+1)
		require.NoError(t, err)
	}
	for _, q := range []string{
		`INSERT INTO bans (id, reason, message, created_by, created_at, scope) VALUES (1, 'x', 'm', 'a', 1, 'global')`,
		`INSERT INTO players (steam_id, guid, name, first_seen, last_seen) VALUES ('76561198000000001', 'g', 'alice', 1, 2)`,
	} {
		_, err := db.Exec(q)
		require.NoError(t, err, q)
	}
	_ = db.Close()

	s, err := Open(t.Context(), path)
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	b, err := s.GetBan(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, "x", b.Reason, "old rows are intact")
	var v int
	require.NoError(t, s.db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v))
	assert.Equal(t, 5, v)
	people, err := s.People(t.Context())
	require.NoError(t, err)
	assert.Empty(t, people)
}

func TestClearPassword(t *testing.T) {
	s, _ := newStore(t)
	putAnn(t, s, Grant{"*", "admin"})
	ctx := t.Context()
	require.NoError(t, s.SetPassword(ctx, steamA, "ann", "hash"))
	require.NoError(t, s.ClearPassword(ctx, steamA))
	c, err := s.Credentials(ctx, steamA)
	require.NoError(t, err)
	assert.Empty(t, c.PasswordHash)
	_, err = s.PersonByUsername(ctx, "ann")
	require.NoError(t, err, "the username stays")
	require.ErrorIs(t, s.ClearPassword(ctx, steamB), ErrNotFound)
}

func TestPutPersonSetsTheUsername(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	require.NoError(t, s.PutPerson(ctx, Person{SteamID: steamA, Username: " Ann ", Grants: []Grant{{"*", "admin"}}}, t0))
	p, err := s.PersonByUsername(ctx, "ANN")
	require.NoError(t, err)
	assert.Equal(t, steamA, p.SteamID)
	assert.Equal(t, "ann", p.Username, "stored in lower case")

	// an empty username keeps the one there is
	require.NoError(t, s.PutPerson(ctx, Person{SteamID: steamA, Grants: []Grant{{"alpha", "viewer"}}}, t0))
	p, _ = s.Person(ctx, steamA)
	assert.Equal(t, "ann", p.Username)
	require.NoError(t, s.PutPerson(ctx, Person{SteamID: steamA, Username: "ann.admin", Grants: []Grant{{"alpha", "viewer"}}}, t0))
	p, _ = s.Person(ctx, steamA)
	assert.Equal(t, "ann.admin", p.Username, "an admin renames")

	// a name another person has: nothing is stored
	err = s.PutPerson(ctx, Person{SteamID: steamB, Username: "ANN.admin", Grants: []Grant{{"*", "viewer"}}}, t0)
	require.ErrorIs(t, err, ErrUsernameTaken)
	_, err = s.Person(ctx, steamB)
	require.ErrorIs(t, err, ErrNotFound, "the person was not created either")
}
