<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Player actions and the ban site: design

## Problem

1. The player action panel is broken (the form rows run off the right edge and are cut). Cause:
   `td:last-child { text-align: right; white-space: nowrap }` in `app.css` is meant for the button column,
   but the panel is a single `td` that is also the last child, so its forms are right aligned and never wrap.
2. The panel keeps growing: message, teleport, give, and now ban. It lacks kick, kill, heal and fix gear.
3. Bans are managed from the panel's form and a bare list. Evidence cannot be added or unlinked on the ban page,
   and the scope of a new ban (this instance, every instance, chosen ones) is a form detail.

## Decisions

- **The player panel only acts on the player.** No ban fields in it. It has one `Ban…` link that opens the ban
  site's new-ban form with the Steam ID and the current instance filled in.
- **Bans are managed on the ban site** (`/b/{backend}/bans`): list, new ban, ban page (edit, revoke,
  evidence, history). The scope is chosen on the new-ban form, defaulting to **this instance only**:
  `This instance (alpha)` / `Every instance of this installation` / `Chosen instances`.
  The global choice needs `ban.global`, as before.
- **Evidence is managed on the ban page** (and on the new-ban form): add a note, a log excerpt or an image,
  link or unlink existing evidence of the same player, view the image. Same permissions as the API.
- **Player actions, grouped by what they do to the player:**
  - *Tell and move*: Message, Teleport (these two are forms).
  - *Help*: Heal, Fix gear (one click each, a toast shows the result).
  - *Remove*: Kick (opens a dialog that asks for a reason, 1-64 bytes, shown to the player), Kill (needs one
    confirmation), Ban… (link).
  - *Give item*: one form, collapsed under a `Give item…` disclosure so the panel is short by default.
- **Backends**: kick uses BattlEye RCon from `dzo serve` (the same connection the ban enforcer uses), so it
  needs no mod change. Kill, heal and fix gear need the mod: new command kinds `player_kill`, `player_heal`
  and `player_fixgear`. That changes the mod's code, so it gets a new PBO and signature (the one case where
  rebuilding is wanted).
- **Permissions**: `player.kick` (moderator and up), `player.kill`, `player.heal`, `player.fixgear`
  (operator and up). Kick and kill are audited like every action.

## IP addresses and GDPR

- The popup over a player's name shows the Steam ID, the ping and, with `player.ip`, the current address.
  It never shows the history.
- `player.ip` (operator, admin): the address of the current connection, and later the data derived from
  it (ISP, country), since support and abuse handling need it.
- `ip.history` (admin only): the history of addresses, and any address stored in a ban, its history or
  evidence. `ban.ip` still decides who may ban an address.
- Matching by address needs no display: ban forms and the API use opaque references (`ref`, `ip_refs`);
  the list shows "shared with N other players". Addresses stay out of the audit log. The references are
  what lets the store keep hashes instead of addresses later.
- Not done yet, proposed: retention. Sightings older than 90 days are deleted by the minute sweep, with the
  period configurable; ban targets stay as long as the ban does. Access to the history should be audited.

## Look

The existing language stays: survey paper, wet asphalt, Barlow Condensed for names and headings, flare red only
for danger. The panel becomes a sheet laid out as a grid, not a stack of full-width forms:

```
 Revan  76561199594878298                                         [Close]
 ─────────────────────────────────────────────────────────────────────────
 Tell          [ Direct message ............................ ] [Message]
 Move          [ x ] [ z ]   or to [ player ▾ ]                [Teleport]
 Help          [Heal] [Fix gear]
 Remove        [Kick…]  [Kill]                                  [Ban…]
 ▸ Give item…
```

- The left column names the group in plain words (sentence case, no all-caps). The groups are what an admin
  thinks in, so they are the structure, not decoration.
- Danger sits at the right and in flare red: `Kill` and `Ban…`. `Kick…` is neutral because it is reversible.
- The dialogs (kick reason, kill confirm) use the native `<dialog>`: focus is trapped, Esc closes, no script
  needed beyond `showModal()`.
- On a narrow screen the group label goes above its controls, and the panel never exceeds the viewport.

The ban site gets the same treatment: the list as ruled rows with a state chip (active in green, expired
muted, revoked struck through), the new-ban form as a sheet with the player on top (name and the identities
dzo has seen) and a scope selector as three large choices, and the ban page with the evidence as a strip of
thumbnails and notes with `Add evidence` and `Unlink`.

## Delivery, in order, each its own commit with tests

1. Fix the panel overflow and regroup the panel (no new actions yet).
2. Kick with a reason (API, client, web dialog, audit, `player.kick`).
3. Ban site: scope selector with "this instance" as default, evidence add and unlink on the ban page.
4. Kill, heal, fix gear: mod commands, API, web, new PBO.

## Not in scope

Banning from the map's context menu, bulk actions on several players.
