-- SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
-- SPDX-License-Identifier: AGPL-3.0-or-later

-- The Bohemia Interactive GUID of a player: the hashed Steam ID PlayerIdentity.GetId() reports,
-- which DayZ Expansion knows players by (the garage stores it as the owner). Like the guid column
-- it is a derivation of the Steam ID, filled when a player joins; rows written before the column
-- existed are backfilled by the store on open (SQLite has no SHA-256, so the migration cannot).

ALTER TABLE players ADD COLUMN bi_guid TEXT NOT NULL DEFAULT '';

CREATE INDEX players_bi_guid ON players (bi_guid);
