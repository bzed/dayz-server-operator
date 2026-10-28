// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// touchEvery bounds how often a session's last-used time is written.
const touchEvery = time.Minute

// nonceKeep is how long Steam nonces are remembered; Steam accepts none that old.
const nonceKeep = time.Hour

// LoginSession is a logged-in browser (Session is taken by the player history).
type LoginSession struct {
	SteamID, Client   string
	Created, LastUsed time.Time
}

// CreateSession stores a session under the hash of its secret.
func (s *Store) CreateSession(ctx context.Context, hash, steamID, client string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO login_sessions (hash, steam_id, client, created_at, last_used) VALUES (?, ?, ?, ?, ?)`,
		hash, steamID, client, now.Unix(), now.Unix())
	return wrap("create session", err)
}

// Session finds a session that is neither idle for longer than idle nor older than max, and notes the use
// (at most once a minute). Anything else is ErrNotFound.
func (s *Store) Session(ctx context.Context, hash string, now time.Time, idle, max time.Duration) (LoginSession, error) {
	var out LoginSession
	var created, used int64
	err := s.db.QueryRowContext(ctx, `SELECT steam_id, client, created_at, last_used FROM login_sessions WHERE hash = ?`, hash).
		Scan(&out.SteamID, &out.Client, &created, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return LoginSession{}, ErrNotFound
	}
	if err != nil {
		return LoginSession{}, wrap("session", err)
	}
	out.Created, out.LastUsed = time.Unix(created, 0).UTC(), time.Unix(used, 0).UTC()
	if now.Sub(out.Created) > max || now.Sub(out.LastUsed) > idle {
		_ = s.DeleteSession(ctx, hash) // best effort: PruneLogins gets it otherwise
		return LoginSession{}, ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `UPDATE login_sessions SET last_used = ? WHERE hash = ? AND last_used < ?`, now.Unix(), hash, now.Add(-touchEvery).Unix())
	if err != nil {
		return LoginSession{}, wrap("session", err)
	}
	if n, _ := res.RowsAffected(); n == 1 { //nolint:errcheck // sqlite reports it
		out.LastUsed = time.Unix(now.Unix(), 0).UTC()
	}
	return out, nil
}

// DeleteSession ends one session.
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM login_sessions WHERE hash = ?`, hash)
	return wrap("delete session", err)
}

// DeleteSessions ends all sessions of a person except keepHash ("" keeps none).
func (s *Store) DeleteSessions(ctx context.Context, steamID, keepHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM login_sessions WHERE steam_id = ? AND hash <> ?`, steamID, keepHash)
	return wrap("delete sessions", err)
}

// PendingLogin is a login between its first and second step, or an enrolment under way. Via says how the
// first step was done: password, steam, or account (a person adding a second factor while logged in).
type PendingLogin struct {
	SteamID, Via string
	Enrol        bool
	Ceremony     []byte // WebAuthn session data
	TOTPSecret   []byte // the secret being enrolled
	Expires      time.Time
}

// CreatePending stores a pending login under the hash of its id.
func (s *Store) CreatePending(ctx context.Context, hash string, p PendingLogin) error {
	enrol := 0
	if p.Enrol {
		enrol = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO pending_logins (hash, steam_id, via, enrol, ceremony, totp_secret, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hash, p.SteamID, p.Via, enrol, p.Ceremony, p.TOTPSecret, p.Expires.Unix())
	return wrap("create pending login", err)
}

// Pending returns a pending login that has not expired, or ErrNotFound.
func (s *Store) Pending(ctx context.Context, hash string, now time.Time) (PendingLogin, error) {
	var p PendingLogin
	var enrol int
	var exp int64
	err := s.db.QueryRowContext(ctx, `SELECT steam_id, via, enrol, ceremony, totp_secret, expires_at FROM pending_logins WHERE hash = ? AND expires_at > ?`, hash, now.Unix()).
		Scan(&p.SteamID, &p.Via, &enrol, &p.Ceremony, &p.TOTPSecret, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return PendingLogin{}, ErrNotFound
	}
	if err != nil {
		return PendingLogin{}, wrap("pending login", err)
	}
	p.Enrol, p.Expires = enrol != 0, time.Unix(exp, 0).UTC()
	return p, nil
}

// UpdatePending changes the ceremony and secret of a pending login.
func (s *Store) UpdatePending(ctx context.Context, hash string, p PendingLogin) error {
	return s.changeOne(ctx, "update pending login", `UPDATE pending_logins SET ceremony = ?, totp_secret = ? WHERE hash = ?`, p.Ceremony, p.TOTPSecret, hash)
}

// DeletePending removes a pending login.
func (s *Store) DeletePending(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pending_logins WHERE hash = ?`, hash)
	return wrap("delete pending login", err)
}

// UseSteamNonce remembers a Steam response nonce. It is true for the first caller only.
func (s *Store) UseSteamNonce(ctx context.Context, nonce string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO steam_nonces (nonce, seen_at) VALUES (?, ?) ON CONFLICT (nonce) DO NOTHING`, nonce, now.Unix())
	if err != nil {
		return false, wrap("use steam nonce", err)
	}
	n, _ := res.RowsAffected() //nolint:errcheck // sqlite reports it
	return n == 1, nil
}

// PruneLogins deletes expired sessions and pending logins, and nonces older than an hour.
func (s *Store) PruneLogins(ctx context.Context, now time.Time, idle, max time.Duration) error {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM login_sessions WHERE created_at < ? OR last_used < ?`, now.Add(-max).Unix(), now.Add(-idle).Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM pending_logins WHERE expires_at <= ?`, now.Unix()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM steam_nonces WHERE seen_at < ?`, now.Add(-nonceKeep).Unix())
		return err
	})
	return wrap("prune logins", err)
}
