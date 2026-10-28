// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ErrUsernameTaken is returned when another person already has the login name.
var ErrUsernameTaken = errors.New("moderation: that username is taken")

// Grant gives a role on an instance, or on every instance ("*").
type Grant struct {
	Instance string `json:"instance"`
	Role     string `json:"role"`
}

// Person is someone who may use the web interface, identified by their Steam ID.
type Person struct {
	SteamID  string    `json:"steam_id"`
	Name     string    `json:"name"`
	Username string    `json:"username,omitempty"`
	Disabled bool      `json:"disabled,omitempty"`
	Created  time.Time `json:"created"`
	Grants   []Grant   `json:"grants"`
}

var (
	grantRoles  = map[string]bool{"viewer": true, "moderator": true, "operator": true, "admin": true}
	grantInstRE = regexp.MustCompile(`^([A-Za-z0-9_.-]{1,64}|\*)$`)
)

func checkPerson(p Person) error {
	if !ValidSteamID(p.SteamID) {
		return invalid("%q is not a Steam ID", p.SteamID)
	}
	seen := map[string]bool{}
	for _, g := range p.Grants {
		switch {
		case !grantInstRE.MatchString(g.Instance):
			return invalid("%q is not an instance name or *", g.Instance)
		case !grantRoles[g.Role]:
			return invalid("%q is not a role (viewer, moderator, operator, admin)", g.Role)
		case seen[g.Instance]:
			return invalid("two grants for %q", g.Instance)
		}
		seen[g.Instance] = true
	}
	return nil
}

func normUsername(u string) string { return strings.ToLower(strings.TrimSpace(u)) }

const personCols = `steam_id, name, COALESCE(username, ''), disabled, created_at`

func scanPerson(row interface{ Scan(...any) error }) (Person, error) {
	var p Person
	var dis int
	var created int64
	if err := row.Scan(&p.SteamID, &p.Name, &p.Username, &dis, &created); err != nil {
		return Person{}, err
	}
	p.Disabled, p.Created = dis != 0, time.Unix(created, 0).UTC()
	return p, nil
}

func (s *Store) grantsOf(ctx context.Context, steamID string) ([]Grant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance, role FROM grants WHERE steam_id = ? ORDER BY instance`, steamID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.Instance, &g.Role); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// People lists everybody, oldest first.
func (s *Store) People(ctx context.Context) ([]Person, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+personCols+` FROM people ORDER BY created_at, steam_id`)
	if err != nil {
		return nil, wrap("people", err)
	}
	var out []Person
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			_ = rows.Close()
			return nil, wrap("people", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, wrap("people", err)
	}
	_ = rows.Close() // the single connection is needed for the grants below
	for i := range out {
		if out[i].Grants, err = s.grantsOf(ctx, out[i].SteamID); err != nil {
			return nil, wrap("people", err)
		}
	}
	return out, nil
}

func (s *Store) person(ctx context.Context, where string, arg string) (Person, error) {
	p, err := scanPerson(s.db.QueryRowContext(ctx, `SELECT `+personCols+` FROM people WHERE `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return Person{}, ErrNotFound
	}
	if err != nil {
		return Person{}, wrap("person", err)
	}
	if p.Grants, err = s.grantsOf(ctx, p.SteamID); err != nil {
		return Person{}, wrap("person", err)
	}
	return p, nil
}

// Person returns one person, or ErrNotFound.
func (s *Store) Person(ctx context.Context, steamID string) (Person, error) {
	return s.person(ctx, `steam_id = ?`, steamID)
}

// PersonByUsername finds a person by login name (case-insensitive), or ErrNotFound.
func (s *Store) PersonByUsername(ctx context.Context, username string) (Person, error) {
	return s.person(ctx, `username = ?`, normUsername(username))
}

// PutPerson creates a person or updates their name and disabled flag, and replaces their grants. Credentials
// are left alone. Disabling a person, or removing their last grant, ends their sessions.
func (s *Store) PutPerson(ctx context.Context, p Person, now time.Time) error {
	if err := checkPerson(p); err != nil {
		return err
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		dis := 0
		if p.Disabled {
			dis = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO people (steam_id, name, disabled, created_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (steam_id) DO UPDATE SET name = excluded.name, disabled = excluded.disabled`,
			p.SteamID, p.Name, dis, now.Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM grants WHERE steam_id = ?`, p.SteamID); err != nil {
			return err
		}
		grants := append([]Grant(nil), p.Grants...)
		sort.Slice(grants, func(i, j int) bool { return grants[i].Instance < grants[j].Instance })
		for _, g := range grants {
			if _, err := tx.ExecContext(ctx, `INSERT INTO grants (steam_id, instance, role) VALUES (?, ?, ?)`, p.SteamID, g.Instance, g.Role); err != nil {
				return err
			}
		}
		if p.Disabled || len(grants) == 0 {
			_, err := tx.ExecContext(ctx, `DELETE FROM login_sessions WHERE steam_id = ?`, p.SteamID)
			return err
		}
		return nil
	})
	return wrap("put person", err)
}

// DeletePerson removes a person with everything that belongs to them.
func (s *Store) DeletePerson(ctx context.Context, steamID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM people WHERE steam_id = ?`, steamID)
	if err != nil {
		return wrap("delete person", err)
	}
	if n, _ := res.RowsAffected(); n == 0 { //nolint:errcheck // sqlite reports it
		return ErrNotFound
	}
	return nil
}

// ResetCredentials removes the password, username, TOTP, passkeys, recovery codes, sessions and pending
// logins of a person; the person and their grants stay, and they enrol again through Steam.
func (s *Store) ResetCredentials(ctx context.Context, steamID string) error {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE people SET username = NULL, password_hash = '', totp_secret = NULL, totp_last_step = 0 WHERE steam_id = ?`, steamID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 { //nolint:errcheck // sqlite reports it
			return ErrNotFound
		}
		for _, q := range []string{
			`DELETE FROM passkeys WHERE steam_id = ?`, `DELETE FROM recovery_codes WHERE steam_id = ?`,
			`DELETE FROM login_sessions WHERE steam_id = ?`, `DELETE FROM pending_logins WHERE steam_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, steamID); err != nil {
				return err
			}
		}
		return nil
	})
	return wrap("reset credentials", err)
}
