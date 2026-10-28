// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Passkey is a registered WebAuthn credential. Data is the credential as JSON.
type Passkey struct {
	ID       []byte
	Name     string
	Data     []byte
	Created  time.Time
	LastUsed time.Time // zero until used
}

// Credentials is what a person logs in with.
type Credentials struct {
	PasswordHash string
	TOTPSecret   []byte
	Passkeys     []Passkey
	RecoveryLeft int
}

// Credentials returns the credentials of a person, or ErrNotFound.
func (s *Store) Credentials(ctx context.Context, steamID string) (Credentials, error) {
	var c Credentials
	err := s.db.QueryRowContext(ctx, `SELECT password_hash, totp_secret FROM people WHERE steam_id = ?`, steamID).Scan(&c.PasswordHash, &c.TOTPSecret)
	if errors.Is(err, sql.ErrNoRows) {
		return Credentials{}, ErrNotFound
	}
	if err != nil {
		return Credentials{}, wrap("credentials", err)
	}
	if len(c.TOTPSecret) == 0 {
		c.TOTPSecret = nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, data, created_at, last_used FROM passkeys WHERE steam_id = ? ORDER BY created_at, id`, steamID)
	if err != nil {
		return Credentials{}, wrap("credentials", err)
	}
	for rows.Next() {
		var k Passkey
		var created, used int64
		if err := rows.Scan(&k.ID, &k.Name, &k.Data, &created, &used); err != nil {
			_ = rows.Close()
			return Credentials{}, wrap("credentials", err)
		}
		k.Created = time.Unix(created, 0).UTC()
		if used > 0 {
			k.LastUsed = time.Unix(used, 0).UTC()
		}
		c.Passkeys = append(c.Passkeys, k)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return Credentials{}, wrap("credentials", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM recovery_codes WHERE steam_id = ?`, steamID).Scan(&c.RecoveryLeft); err != nil {
		return Credentials{}, wrap("credentials", err)
	}
	return c, nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// SetPassword stores the password hash and the login name of a person. An empty username keeps the one they
// have. Login names are case-insensitive and stored in lower case; a name another person has gives
// ErrUsernameTaken.
func (s *Store) SetPassword(ctx context.Context, steamID, username, hash string) error {
	name := normUsername(username)
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var res sql.Result
		var err error
		if name == "" {
			res, err = tx.ExecContext(ctx, `UPDATE people SET password_hash = ? WHERE steam_id = ?`, hash, steamID)
		} else {
			res, err = tx.ExecContext(ctx, `UPDATE people SET password_hash = ?, username = ? WHERE steam_id = ?`, hash, name, steamID)
		}
		if isUnique(err) {
			return ErrUsernameTaken
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 { //nolint:errcheck // sqlite reports it
			return ErrNotFound
		}
		return nil
	})
	return wrap("set password", err)
}

// ClearPassword removes the password and the login name of a person; the name is free again.
func (s *Store) ClearPassword(ctx context.Context, steamID string) error {
	return s.changeOne(ctx, "clear password", `UPDATE people SET password_hash = '', username = NULL WHERE steam_id = ?`, steamID)
}

// SetTOTP stores a new TOTP secret and forgets the steps used with the old one.
func (s *Store) SetTOTP(ctx context.Context, steamID string, secret []byte) error {
	return s.changeOne(ctx, "set totp", `UPDATE people SET totp_secret = ?, totp_last_step = 0 WHERE steam_id = ?`, secret, steamID)
}

// ClearTOTP removes the TOTP secret.
func (s *Store) ClearTOTP(ctx context.Context, steamID string) error {
	return s.changeOne(ctx, "clear totp", `UPDATE people SET totp_secret = NULL, totp_last_step = 0 WHERE steam_id = ?`, steamID)
}

// changeOne runs a statement that must change one row.
func (s *Store) changeOne(ctx context.Context, what, q string, args ...any) error {
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return wrap(what, err)
	}
	if n, _ := res.RowsAffected(); n == 0 { //nolint:errcheck // sqlite reports it
		return ErrNotFound
	}
	return nil
}

// UseTOTPStep records that the code of step was used. It is true for the first caller only, and only for a step
// newer than any used before: one conditional statement, so two requests with the same code cannot both pass.
func (s *Store) UseTOTPStep(ctx context.Context, steamID string, step int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE people SET totp_last_step = ? WHERE steam_id = ? AND totp_secret IS NOT NULL AND totp_last_step < ?`, step, steamID, step)
	if err != nil {
		return false, wrap("use totp step", err)
	}
	n, _ := res.RowsAffected() //nolint:errcheck // sqlite reports it
	return n == 1, nil
}

// AddPasskey registers a passkey.
func (s *Store) AddPasskey(ctx context.Context, steamID string, p Passkey) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO passkeys (id, steam_id, name, data, created_at) VALUES (?, ?, ?, ?, ?)`,
		p.ID, steamID, p.Name, p.Data, p.Created.Unix())
	return wrap("add passkey", err)
}

// UpdatePasskey stores a passkey's credential data (its sign counter changes) and when it was used.
func (s *Store) UpdatePasskey(ctx context.Context, steamID string, id, data []byte, used time.Time) error {
	return s.changeOne(ctx, "update passkey", `UPDATE passkeys SET data = ?, last_used = ? WHERE id = ? AND steam_id = ?`, data, used.Unix(), id, steamID)
}

// DeletePasskey removes one of a person's passkeys.
func (s *Store) DeletePasskey(ctx context.Context, steamID string, id []byte) error {
	return s.changeOne(ctx, "delete passkey", `DELETE FROM passkeys WHERE id = ? AND steam_id = ?`, id, steamID)
}

// SetRecoveryCodes replaces the recovery codes (their hashes) of a person.
func (s *Store) SetRecoveryCodes(ctx context.Context, steamID string, hashes []string) error {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE steam_id = ?`, steamID); err != nil {
			return err
		}
		for _, h := range hashes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_codes (steam_id, hash) VALUES (?, ?)`, steamID, h); err != nil {
				return err
			}
		}
		return nil
	})
	return wrap("set recovery codes", err)
}

// UseRecoveryCode spends a recovery code. It is true for the first caller only.
func (s *Store) UseRecoveryCode(ctx context.Context, steamID, hash string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM recovery_codes WHERE steam_id = ? AND hash = ?`, steamID, hash)
	if err != nil {
		return false, wrap("use recovery code", err)
	}
	n, _ := res.RowsAffected() //nolint:errcheck // sqlite reports it
	return n == 1, nil
}
