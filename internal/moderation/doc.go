// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package moderation keeps bans, ban evidence and player sightings in a SQLite database, renders
// BattlEye's bans.txt from them, and enforces bans on running servers through RCon
// (docs/superpowers/specs/2026-10-16-player-bans-design.md). dzo serve is the only writer; the start
// path of an instance opens the database read-only to render bans.txt.
package moderation
