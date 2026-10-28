-- SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
-- SPDX-License-Identifier: AGPL-3.0-or-later

-- The people who may use the web interface, what each may do per instance, how they log in, and the state of
-- logins in progress. All of it lives here so that every dzo serve sharing this database can check any session.
-- Times are unix seconds. Secrets (session ids, pending-login ids, recovery codes) are stored as hashes.

CREATE TABLE people (
	steam_id       TEXT    PRIMARY KEY,
	name           TEXT    NOT NULL DEFAULT '',
	username       TEXT    UNIQUE,                -- lower case; NULL until the person sets a password
	password_hash  TEXT    NOT NULL DEFAULT '',   -- argon2id PHC string
	totp_secret    BLOB,
	totp_last_step INTEGER NOT NULL DEFAULT 0,    -- the newest accepted step, so a code works once
	disabled       INTEGER NOT NULL DEFAULT 0,
	created_at     INTEGER NOT NULL
);

CREATE TABLE grants (
	steam_id TEXT NOT NULL REFERENCES people (steam_id) ON DELETE CASCADE,
	instance TEXT NOT NULL,                       -- an instance name, or * for all of them
	role     TEXT NOT NULL,
	PRIMARY KEY (steam_id, instance)
);

CREATE TABLE passkeys (
	id        BLOB    PRIMARY KEY,                -- the WebAuthn credential id
	steam_id  TEXT    NOT NULL REFERENCES people (steam_id) ON DELETE CASCADE,
	name      TEXT    NOT NULL DEFAULT '',
	data      BLOB    NOT NULL,                   -- the credential as JSON
	created_at INTEGER NOT NULL,
	last_used INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX passkeys_person ON passkeys (steam_id, created_at);

CREATE TABLE recovery_codes (
	steam_id TEXT NOT NULL REFERENCES people (steam_id) ON DELETE CASCADE,
	hash     TEXT NOT NULL,
	PRIMARY KEY (steam_id, hash)
);

CREATE TABLE login_sessions (
	hash       TEXT    PRIMARY KEY,
	steam_id   TEXT    NOT NULL REFERENCES people (steam_id) ON DELETE CASCADE,
	client     TEXT    NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	last_used  INTEGER NOT NULL
);
CREATE INDEX login_sessions_person ON login_sessions (steam_id);

-- A login whose first factor is done and whose second is still to come (or an enrolment under way).
CREATE TABLE pending_logins (
	hash        TEXT    PRIMARY KEY,
	steam_id    TEXT    NOT NULL REFERENCES people (steam_id) ON DELETE CASCADE,
	via         TEXT    NOT NULL,                 -- password, steam or account
	enrol       INTEGER NOT NULL DEFAULT 0,
	ceremony    BLOB,                             -- WebAuthn session data
	totp_secret BLOB,                             -- a TOTP secret being enrolled
	expires_at  INTEGER NOT NULL
);

-- Steam OpenID response nonces already used.
CREATE TABLE steam_nonces (
	nonce   TEXT    PRIMARY KEY,
	seen_at INTEGER NOT NULL
);
