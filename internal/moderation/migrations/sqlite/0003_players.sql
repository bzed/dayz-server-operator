-- SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
-- SPDX-License-Identifier: AGPL-3.0-or-later

-- The players who ever connected to one of our servers: who, under which names, when and where, and the cached
-- answers of BattlEye's master server (per game) and of Steam about them. Keyed by Steam ID; the BattlEye GUID
-- is derived from it, so the sightings of 0001 join on players.guid.
CREATE TABLE players (
	steam_id   TEXT    PRIMARY KEY,
	guid       TEXT    NOT NULL,
	name       TEXT    NOT NULL,
	first_seen INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL
);
CREATE INDEX players_guid ON players (guid);
CREATE INDEX players_last_seen ON players (last_seen);

CREATE TABLE player_names (
	steam_id   TEXT    NOT NULL REFERENCES players (steam_id) ON DELETE CASCADE,
	name       TEXT    NOT NULL,
	first_seen INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL,
	PRIMARY KEY (steam_id, name)
);

CREATE TABLE player_sessions (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	steam_id   TEXT    NOT NULL REFERENCES players (steam_id) ON DELETE CASCADE,
	instance   TEXT    NOT NULL,
	name       TEXT    NOT NULL,
	started_at INTEGER NOT NULL,
	ended_at   INTEGER
);
CREATE INDEX player_sessions_player ON player_sessions (steam_id, started_at);
CREATE INDEX player_sessions_open ON player_sessions (instance) WHERE ended_at IS NULL;

CREATE TABLE be_status (
	guid       TEXT    NOT NULL,
	game       TEXT    NOT NULL,
	state      TEXT    NOT NULL,
	banned     INTEGER NOT NULL DEFAULT 0,
	reason     TEXT    NOT NULL DEFAULT '',
	checked_at INTEGER NOT NULL,
	PRIMARY KEY (guid, game)
);

CREATE TABLE steam_info (
	steam_id   TEXT    PRIMARY KEY,
	state      TEXT    NOT NULL,
	banned     INTEGER NOT NULL DEFAULT 0,
	data       TEXT    NOT NULL,
	checked_at INTEGER NOT NULL
);

CREATE TABLE steam_friends (
	steam_id  TEXT NOT NULL,
	friend_id TEXT NOT NULL,
	PRIMARY KEY (steam_id, friend_id)
);
CREATE INDEX steam_friends_friend ON steam_friends (friend_id);
