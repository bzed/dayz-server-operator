.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Web interface
=============

``dzo web`` serves the web interface. It is a thin layer over the :doc:`API
<api>`: every button is one API call, so what you can do in the browser you can
also do with ``dzo player``, ``dzo vehicle`` or a script.

The web interface only ever talks to installations through their API. It can
run on the same host as ``dzo serve`` (the default), or on another host, and
one web interface can show **several dzo installations** side by side.

.. code-block:: sh

   sudo -iu dayz dzo token create web --role operator --delegate > /var/lib/dzo/secrets/web.token
   sudo -iu dayz dzo serve &      # the API (to try it by hand, see below)
   sudo -iu dayz dzo web          # http://127.0.0.1:8081 (to try it by hand, see below)

``dzo instance apply <name>`` (or ``dzo units sync``) writes ``dzo-web.service`` and starts it as soon
as the web interface is set up, that is when ``web.backends`` in ``config.yaml`` lists a backend or the
token file above (``<paths.secrets>/web.token``) exists. ``web.enabled: true`` forces the unit on (it then
fails until the token exists), ``false`` keeps it off. It is not written before that, because the web
interface is optional and cannot start without a token. ``dzo-serve.service`` is written when an
instance runs the dzo-admin mod. With the units running, ``dzo serve`` and ``dzo web`` by hand would
find their ports taken: stop the unit first (``systemctl --user stop dzo-web``) if you want to run one
in the foreground, for example to see its output.

Several installations
---------------------

List one backend per installation in ``/etc/dzo/config.yaml``. Each needs an API
token created *on that installation*:

.. code-block:: yaml

   web:                             # excerpt; every key of ``web:`` is explained in :doc:`configuration`
     backends:                      # name, url and token_file are all required
       - {name: eu, url: "https://dzo-eu.example.invalid", token_file: /var/lib/dzo/secrets/eu.token}
       - {name: us, url: "https://dzo-us.example.invalid", token_file: /var/lib/dzo/secrets/us.token}

The dashboard shows every installation with its instances. An installation that
is down is shown with its error; the others keep working.

Access control
--------------

.. warning::

   The web interface has **no login of its own yet** (no users, passwords or
   second factor). Anyone who can reach it acts with the role of the backend
   token. Keep it on localhost, or put it behind a reverse proxy that
   authenticates your users (with multi-factor login) and terminates HTTPS.
   dzo refuses to listen on a non-local address unless
   ``web.allow_insecure_http`` is set.

Set ``web.user_header`` to the header your proxy fills with the signed-in user
(for example ``X-Forwarded-User``). That name goes into the audit log next to
each action. Only use this behind a proxy that strips the header from incoming
requests.

What the interface does about browser attacks: every change needs a per-session
token that the pages hand to htmx (and a same-origin check), responses carry a
strict content security policy without inline scripts, and pages cannot be
framed.

Roles
-----

The role of a token decides what the interface may do. Without a permission the
action fails with a clear message.

.. list-table::
   :header-rows: 1

   * - Role
     - May
   * - ``detector``
     - only file evidence against players (no web access; for scripts)
   * - ``viewer``
     - see instances, players, vehicles, events and public map markers
   * - ``announcer``
     - only see the status of an instance and broadcast (no web access; for the restart countdown's notifications, see :doc:`operations`)
   * - ``moderator``
     - also send direct messages to players, kick them, see bans and ban for up to 7 days (:doc:`moderation`)
   * - ``operator``
     - also broadcast, teleport, heal, fix gear and kill players, spawn items, repair and delete vehicles, give long and IP bans, and see the address a player is connected from
   * - ``admin``
     - everything, including the audit log, global bans, the history of IP addresses and markers that are admin-only

Finding your way
----------------

Every page reaches every other page in one click. The header has the **instance switcher** (a menu of every
installation's instances, with a dot for the connection state of the mod; Escape or a click outside closes it) and
the links **Bans**, **Audit log** and **docs**. The first two go to the installation of the page you are on, on the
dashboard to the first installation (the others are in the switcher). Pages of an instance (**Overview**, **Map**,
**Events**) share an *instance bar* under the header: the instance's name, its live facts and the three tabs,
the current one underlined. An installation that cannot be reached is marked ``unreachable`` in the switcher; the page still works.
The lists in the switcher are kept for 15 seconds.

Pages
-----

Dashboard
   All installations and their instances, with the dzo-admin connection state, the world, the in-game time (sun or moon),
   the players online, the vehicles, how long the game server has been up, the time to the next scheduled restart (``now`` when it
   is overdue) and the version of the dzo-admin mod. An instance whose mod is not connected shows
   the schedule only. On a phone the table keeps the instance, the state, the players and the next restart. The
   map icon after an instance's name opens its map.

Instance
   Broadcast message (to everyone, by chat colour, on screen, RCon ``say`` or, when ``dzo-admin`` reports DayZ Expansion,
   as an Expansion notification of a type you pick, with an optional title and duration), the player list (refreshed every 5 seconds) with a panel of
   actions per player (message, teleport to coordinates or to another player, heal, fix gear, kick with a reason, kill after a confirmation,
   give item with a search over the server's item classes), and the vehicle list
   with repair (everything, or only body, parts, wheels, fuel or missing parts), flip and delete. The
   actions open directly under the player or vehicle they apply to. Banning is a link
   in the panel to the ban form with the player filled in. Hovering over a player's name (or tapping its
   ``i`` button on a phone) shows the Steam ID, the ping and, with the ``player.ip`` permission
   (operators and admins), the address the player is connected from now. The history of addresses is not
   shown there; it needs ``ip.history`` (:doc:`moderation`). The country flag before a name and the
   country in that info come from the GeoIP databases (:doc:`geoip`); with ``player.ip`` the info also shows the city,
   the approximate location and the provider. The
   panel's ``Profile`` button opens the player's page (see Players below), and the panel shows the verdicts of BattlEye, Steam and our bans in one line before you act. A player who is flagged (a BattlEye global ban, a Steam ban or a ban of ours) has a red marker after the name in the player table. The player table has no Steam ID column; the
   panel shows it. On a phone the table shows only the name, the health and the actions.

Events
   A live log of what happens in the game, in the style of ``tail -f`` (the ``Events`` tab): kills (also by Expansion AI) with weapon and distance, deaths, joins and leaves, kicks, base building and raids,
   combination locks cut, flags raised and lowered, stashes buried and dug up, in-game chat and the actions of the operators.
   The newest 200 events are there when the page opens (the feed is kept over restarts of ``dzo serve``, see :ref:`event-feed`)
   and new ones are added at the bottom as they happen; the page keeps at most 1000 rows. It follows the end of the
   log while you are there; if you scroll up to read, a "new events" button brings you back. A reconnect
   fetches what was missed. The checkboxes at the top hide groups of events (remembered in the browser).
   A player's name links to the player's row in the player list (``#player-<steamid>``: the row is scrolled into view and
   highlighted for a few seconds; for a player who is not online the page says so); a position links to the map, opened with the place marked
   (see :doc:`admin-map`).
   Needs the ``view`` role. Users who may broadcast (``operator`` and up) also get an input line: its text is
   sent to all players as chat. Kills and deaths come from the dzo-admin mod (0.5.0 or newer); chat comes from BattlEye's
   RCon, so RCon must be reachable, as it must for bans. The same feed is on the console with ``dzo events``
   (:doc:`cli`) and in Discord with the ``killfeed`` (:doc:`configuration`).

Live map
   Players, vehicles, active events and marker layers (including the rebuilt houses and wells with the time until decay wears them down) on a map that updates as
   the server reports. A player's icon goes from red to green with the player's health
   (a thick red ring: unconscious, dashed: dead). The instance bar (also on the overview and the events page, as a snapshot there) shows the in-game time (with a sun or moon),
   the players online, the
   uptime (since ``dzo serve`` last saw the mod start) and, if the instance has a
   ``restarts.schedule``, the time to the next scheduled restart, on the map's satellite imagery if you built it (see
   :doc:`admin-map`). The map always fits the window and zooms in to a few
   pixels per metre; ``Fit`` shows the whole world again. Icons sit exactly on
   their position. **Drag a player's icon** and drop it to teleport the player
   there (the icon stays under the pointer and at the drop point until you answer, see :doc:`admin-map`); the crosshair and the coordinate readout show where it will land, and
   you confirm before anything happens. **Right-click** (or click, or press the
   menu key on a focused icon) for the actions on a player, vehicle, event or
   marker; right-click empty ground to teleport a player there, spawn an item there (as many stacks as the quantity needs) or spawn a vehicle there. Menu entries
   you lack the role for are not shown. See :doc:`admin-map`.

Players
   The player database of an installation (``Players`` in the header; it needs the ``player.history`` permission,
   that is the moderator role or higher, :doc:`moderation`). The list has a search over every name a player ever used,
   the Steam ID and the BattlEye GUID, an instance filter and "Only flagged players", most recently seen first, with a
   red mark in the BattlEye, Steam and Here columns for a player something flags. A player's page shows, under the name
   with the other names used, a **verdict row** of three cells that each answer one question and say how old the answer is:

   * **BattlEye**: whether BattlEye's master server lists the GUID as globally banned, one line per game (DayZ first, then
     Arma 3 and Arma 2). A ban in an Arma game alone is shown in red with the game's name, because it is the same Steam
     account. "No answer" means the master did not answer and is never "not banned"; "Not asked yet" is a new player.
   * **Steam**: VAC, game and community bans. Without a Web API key that is "VAC banned" or "No bans" from the public
     profile; with one, the counts and the days since the last ban, also for private profiles. "Private profile, no ban
     data" appears when a private profile gives nothing more. A line below lists what Steam said about the account: the
     name, public or private, since when, the country, the Steam level and the DayZ hours, whatever the source gave, and
     the player's public Steam friends who also played on your servers.
   * **Here**: our own bans of the player: "Banned permanently" or "Banned until", with the ban number and scope, linked
     to the ban; a past ban is shown muted as "Ban expired" or "Ban revoked".

   Only a cell that reports a ban is red; "not banned" is plain text, not green, because the sources cannot promise more.
   **Check now** asks BattlEye and Steam again and swaps the row (once per 10 minutes per player); the answers are otherwise
   refreshed in the background (:ref:`player-history`). Below the row are the instances played with first and last day,
   sessions and time, the names, the newest 50 sessions and, from the ban form's data, where the player was seen
   (addresses only with ``ip.history``). **Ban…** opens the ban form with the player filled in, **Steam profile** opens
   the profile on steamcommunity.com in a new tab. On a phone the three cells stack in the same order. The Steam ID of a ban's
   target links to the player's page.

Bans
   One page per installation (``Bans`` in the header): the list of bans with a state filter and
   a search by Steam ID, GUID, IP, text or player name; a ban page with its targets, scope, evidence,
   history, an edit form and revoke. A player's panel has a **Ban…** button that opens the form with the
   Steam ID, the instance, the addresses the player was seen with (shown only with ``ip.history``, otherwise as
   "Address", with shared ones marked) and
   the evidence filed against them. The reason is a multi-line field and the duration defaults to permanent.
   "Where does the ban apply?" defaults to every instance of this installation (``ban.global``); the other
   choices are the instance the link came from and chosen instances (the default for roles without ``ban.global``: the link's instance). "Also send
   to" repeats the ban on other installations. On the ban page, evidence is managed: add a note, log lines or
   a screenshot (filed against the banned player and linked to the ban), unlink an item, or link other
   evidence filed against the player. See :doc:`moderation`.

Audit log
   Who did what, when, from where (web, CLI or API), and the result. Needs the
   ``admin`` role.

Documentation
   This documentation is served under ``/docs/`` without login.

Not available yet
-----------------

These are planned and are not part of the web interface today: users with
passwords and TOTP, single sign-on, restart timers and scheduled
broadcasts, and editors for server configuration.
