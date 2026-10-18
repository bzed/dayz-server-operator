-- SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
-- SPDX-License-Identifier: AGPL-3.0-or-later

-- Sightings and ban targets get real ids from a sequence (AUTOINCREMENT never reuses one, and a VACUUM
-- keeps them), which the API hands out as opaque references. A sighting's address may be dropped after
-- the retention period, so it can be NULL; the player's row stays.
CREATE TABLE sightings_new (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	guid       TEXT    NOT NULL,
	ip         TEXT,
	name       TEXT    NOT NULL,
	instance   TEXT    NOT NULL,
	first_seen INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL,
	UNIQUE (guid, ip, instance)
);
INSERT INTO sightings_new (guid, ip, name, instance, first_seen, last_seen)
	SELECT guid, ip, name, instance, first_seen, last_seen FROM sightings ORDER BY rowid;
DROP TABLE sightings;
ALTER TABLE sightings_new RENAME TO sightings;
CREATE INDEX sightings_ip ON sightings (ip);
CREATE INDEX sightings_guid ON sightings (guid);

CREATE TABLE ban_targets_new (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	ban_id  INTEGER NOT NULL REFERENCES bans (id) ON DELETE CASCADE,
	kind    TEXT    NOT NULL CHECK (kind IN ('steam', 'guid', 'ip')),
	value   TEXT    NOT NULL,
	derived INTEGER NOT NULL DEFAULT 0,
	UNIQUE (ban_id, kind, value)
);
INSERT INTO ban_targets_new (ban_id, kind, value, derived)
	SELECT ban_id, kind, value, derived FROM ban_targets ORDER BY rowid;
DROP TABLE ban_targets;
ALTER TABLE ban_targets_new RENAME TO ban_targets;
CREATE INDEX ban_targets_value ON ban_targets (value);

-- Small facts about the database itself, such as when it was last vacuumed.
CREATE TABLE meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
