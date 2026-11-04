-- SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
-- SPDX-License-Identifier: AGPL-3.0-or-later

-- One row per vehicle of an instance: who drove it last and when. A vehicle missing from a snapshot is
-- hidden (hidden_since), not deleted, so it comes back with its driver; rows hidden for long are forgotten.
-- Times are unix seconds. driven_at is NULL for a vehicle never seen driven.

CREATE TABLE vehicles (
	instance        TEXT    NOT NULL,
	vehicle_id      TEXT    NOT NULL,
	type            TEXT    NOT NULL DEFAULT '',
	driver_steam_id TEXT    NOT NULL DEFAULT '',
	driver_name     TEXT    NOT NULL DEFAULT '',
	driven_at       INTEGER,
	first_seen      INTEGER NOT NULL,
	last_seen       INTEGER NOT NULL,
	hidden_since    INTEGER,
	PRIMARY KEY (instance, vehicle_id)
);

CREATE INDEX vehicles_hidden ON vehicles (instance, hidden_since);
