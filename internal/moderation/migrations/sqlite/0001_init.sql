-- SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
-- SPDX-License-Identifier: AGPL-3.0-or-later

-- Bans, their targets, scope and history; evidence; GUID/IP sightings. Times are Unix seconds, so the
-- same statements work on PostgreSQL later (with BIGSERIAL ids in its own migration set).
CREATE TABLE bans (
	id            INTEGER PRIMARY KEY,
	reason        TEXT    NOT NULL,
	message       TEXT    NOT NULL,
	created_by    TEXT    NOT NULL,
	created_at    INTEGER NOT NULL,
	expires_at    INTEGER,
	scope         TEXT    NOT NULL CHECK (scope IN ('global', 'instances')),
	revoked_by    TEXT,
	revoked_at    INTEGER,
	revoke_reason TEXT
);
CREATE TABLE ban_instances (
	ban_id   INTEGER NOT NULL REFERENCES bans (id) ON DELETE CASCADE,
	instance TEXT    NOT NULL,
	PRIMARY KEY (ban_id, instance)
);
CREATE TABLE ban_targets (
	ban_id  INTEGER NOT NULL REFERENCES bans (id) ON DELETE CASCADE,
	kind    TEXT    NOT NULL CHECK (kind IN ('steam', 'guid', 'ip')),
	value   TEXT    NOT NULL,
	derived INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (ban_id, kind, value)
);
CREATE INDEX ban_targets_value ON ban_targets (value);
CREATE TABLE ban_history (
	id     INTEGER PRIMARY KEY,
	ban_id INTEGER NOT NULL REFERENCES bans (id) ON DELETE CASCADE,
	at     INTEGER NOT NULL,
	actor  TEXT    NOT NULL,
	action TEXT    NOT NULL,
	change TEXT    NOT NULL
);
CREATE INDEX ban_history_ban ON ban_history (ban_id);
CREATE TABLE evidence (
	id          INTEGER PRIMARY KEY,
	steam_id    TEXT,
	guid        TEXT,
	ip          TEXT,
	instance    TEXT    NOT NULL,
	source      TEXT    NOT NULL,
	kind        TEXT    NOT NULL CHECK (kind IN ('log', 'note', 'image')),
	body        TEXT    NOT NULL,
	file_sha256 TEXT,
	file_type   TEXT,
	created_at  INTEGER NOT NULL,
	CHECK (steam_id IS NOT NULL OR guid IS NOT NULL OR ip IS NOT NULL)
);
CREATE INDEX evidence_guid ON evidence (guid);
CREATE INDEX evidence_ip ON evidence (ip);
CREATE TABLE ban_evidence (
	ban_id      INTEGER NOT NULL REFERENCES bans (id) ON DELETE CASCADE,
	evidence_id INTEGER NOT NULL REFERENCES evidence (id),
	PRIMARY KEY (ban_id, evidence_id)
);
CREATE TABLE sightings (
	guid       TEXT    NOT NULL,
	ip         TEXT    NOT NULL,
	name       TEXT    NOT NULL,
	instance   TEXT    NOT NULL,
	first_seen INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL,
	PRIMARY KEY (guid, ip, instance)
);
CREATE INDEX sightings_ip ON sightings (ip);
