.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Bans and evidence
=================

dzo bans players by Steam ID, BattlEye GUID or IP address. It can ban for a time or for good, on one
instance or on every instance of an installation. It keeps who banned whom and why, every change, and the evidence
(log lines, notes, screenshots) filed against a player. The tools are ``dzo ban`` and ``dzo evidence``,
the :doc:`web interface <web>` and the :doc:`JSON API <api>`. These tools need ``dzo serve`` to be running. The
bans themselves still take effect when it is not (see below).

Bans
----

A ban covers one person through several **targets**:

* the Steam ID, and the BattlEye GUID dzo derives from it (``md5("BE" + the Steam ID as a little-endian
  64-bit number)``, checked against a real server);
* optional extra GUIDs;
* optional IPv4 addresses. BattlEye bans single addresses only, so there are no ranges and no IPv6.

A ban lasts ``30m``, ``12h``, ``7d``, ``2w`` or ``permanent`` (at most ten years, longer means permanent).
It has a **reason** that only admins see (at most 1000 characters) and a **message** that dzo shows the
player (default: ``You are banned from this server``). BattlEye keeps at most 64 bytes of the message,
so dzo refuses a longer one instead of cutting it silently. Put the details, the log lines and the
screenshots in the reason and the evidence. Those stay in the database and dzo never sends them to the player.

**Scope**: ``instances`` lists the instances a ban applies to, ``global`` means every instance of this
installation. A ban can be edited (reason, message, expiry, scope, targets, linked evidence) and revoked.
A revoked ban stays in the list and in its history. Every change is recorded with who made it and the old
and new values.

When one person has several bans, the longest one decides. ``bans.txt`` has one line for each target, with
the latest expiry. Revoking one ban leaves the others in force.

Shared IP addresses
-------------------

dzo never bans an IP address by itself. Mobile networks put many players behind one address, so an IP ban
is a blunt tool. The ban form lists the addresses a player was seen with. It warns when other players
were seen with the same address ("shared with N other players"). Add an IP only when you have a reason.

IP addresses and privacy
------------------------

An IP address is personal data. dzo keeps what it needs to ban and to support players, and shows an address
only to those who need it:

.. list-table::
   :header-rows: 1

   * - Data
     - Permission
     - Roles
   * - The address a player is connected from now (the player's info popup, ``GET
       /instances/{instance}/players/{steamid}/connection``)
     - ``player.ip``
     - operator, admin
   * - The history of addresses a player was seen with, and any address stored in a ban, in its history
       or in evidence
     - ``ip.history``
     - admin
   * - Banning an address
     - ``ban.ip``
     - operator, admin
   * - The player database: names, sessions, what BattlEye and Steam say (:ref:`player-history`)
     - ``player.history``
     - moderator, operator, admin

Players are still matched by address without showing it. The ban form lists the addresses a player was
seen with as "Address (name on instance, last seen), shared with N other players". Ticking one bans it.
The API gives each such address an opaque reference (``ref``, and ``ip_refs`` when a ban is made or edited)
instead of the address. A reference only works for the player being banned or the ban being edited.
For a ban that is not global, a sighting reference also needs a sighting on one of the ban's instances.
Other references get the same error, ``unknown address reference``, as references that do not exist. Without
``ip.history`` a ban shows "address hidden", and editing it keeps its addresses. Addresses are never written to
the audit log. dzo can store them hashed in the future; the references keep that possible. Reading the
history of a player's addresses is recorded in the audit log (``ip.history``, without the addresses).

**Retention.** dzo keeps who was seen, under which name, on which instance and when, for good. It keeps the
address of a sighting for 90 days (``serve.ip_retention_days``, 0 keeps them for ever), then drops it, unless a ban uses that address. The rows of one player on
one instance that lose their address merge into one that keeps the newest name and the whole period.
``dzo serve`` does this every minute with the other upkeep. It also vacuums the database file once a week (``serve.vacuum_days``, 0 turns it off)
to give back the space of deleted rows. The references (``ref``) are ids from a sequence, so they never
change and are never reused, vacuum or not.

.. _player-history:

Player history
--------------

``dzo serve`` keeps a database of every player who connected to one of its instances, in the same SQLite file as the
bans. It is built from the live player list that ``dzo-admin`` reports, so it needs the mod. For each player (by
Steam ID, with the BattlEye GUID derived from it) it records every name used and when. It records each stay as a session:
the instance, when the player joined and when the player left. If ``dzo serve`` stops, or the mod sends nothing for
10 seconds, the sessions end at the last contact or at the stop. Sessions are kept for a year
(``serve.session_retention_days``). Once a player has no session left, has not been seen for that time, and no ban,
evidence, note or web account refers to the player, dzo deletes the player and the names. Players that a ban,
evidence, note or account refers to are kept for good. The mod can report at most 256 players in one sync.

BattlEye kicks are not recorded: they are dzo's own.

**BattlEye's global bans.** ``dzo serve`` asks BattlEye's master server whether a player's GUID is globally banned. It asks for
DayZ and, because it is the same Steam account, for Arma 3 and Arma 2: Operation Arrowhead. This is information for the
player page only. The DayZ server enforces BattlEye's global bans itself when a player joins, and dzo neither enforces
nor duplicates that. There is no option for it, and it is always on. BattlEye publishes no API for this. dzo uses the
UDP query that game servers use: one datagram with the GUID to 57.129.90.115, the address of
``dayz1.battleye.com``. The port is 2354 for DayZ, 2344 for Arma 3 and 2324 for Arma 2: OA. Arma Reforger has none. The game server already sends every
joining player's GUID there, so nothing new is disclosed. dzo asks gently. It asks a new player once for each of the
three games and keeps an answer for 7 days. It asks again only the players seen in the last 30 days, and never more
than one packet a second. After three rounds without any answer, it leaves BattlEye alone for an hour. A silent master is
"no answer" on the page, never "not banned".

**Steam.** With ``steam.profile_lookup`` (default on) the player page also shows what Steam says: the link to the
profile and the name. It also shows whether the profile is private and whether the account has a VAC or
community ban. With a Web API key
(``steam.web_api_key_file``, :doc:`configuration`) it adds more: the number of VAC and game bans, and the days since the
last ban. It also adds the account's age and country, the Steam level, the DayZ playtime and the player's public
friends who also played on your servers. Answers are kept for 24 hours. A moderator can press **Check now** on a player's page to ask both sources at
once. That works at most once per 10 minutes per player.

How bans are enforced
---------------------

Enforcement goes through BattlEye's ``bans.txt`` and a live check in ``dzo serve``.

``bans.txt``
   One ban per line: ``<GUID or IP> <expiry> <message>``. The expiry is ``-1`` for a permanent ban and a
   Unix time otherwise. See the FAQ for what a real server was seen to do with it. dzo renders it from the
   database at **every start** of an instance (``dzo instance render``), so bans hold while ``dzo serve``
   is down. It renders it again in ``dzo serve`` after every change and once a minute, so that expired
   bans leave the file. After writing, serve sends RCon ``loadBans``.

The enforcer
   For every instance with an RCon password, ``dzo serve`` keeps an RCon session. When a player connects,
   the enforcer looks up the GUID and the IP in the active bans. It kicks a banned player with the ban
   message. When dzo creates or edits a ban, it checks the players online on the affected instances at
   once. Every kick is written to the audit log (``ban.kick``).

Pending instances
   If an instance's server is down or RCon fails when a ban is made, dzo stores the ban anyway. The answer
   says ``pending`` for that instance, and the next sweep (every minute) or the next start syncs it.

Changes outside dzo
   dzo records the SHA-256 of what it wrote in ``profiles/battleye/.dzo-bans.sha256``. When ``bans.txt``
   differs from that (hand edits, or another tool's ``addBan``), ``dzo serve`` imports the active entries
   that are not banned yet. It makes them instance bans created by ``import:bans.txt`` and renders again. The start
   path never overwrites a file changed outside dzo. **Remove bans through dzo**: a line deleted from
   ``bans.txt`` by hand comes back at the next render.

   The game container can write to ``profiles/``. dzo therefore reads ``bans.txt`` as a regular file of at
   most 1 MiB and never follows a symbolic link. A ``bans.txt`` that is a link, a FIFO or larger gives an
   error in the log, and dzo does not import it. dzo writes the files in ``profiles/battleye`` with a
   temporary file and a rename inside ``profiles/``. A link in the way is replaced, and a link that leads
   out of ``profiles/`` makes the write fail.

A new or rebuilt server
   The database is the source of truth, not ``bans.txt``. Copy ``dzo.sqlite`` (and the ``evidence``
   directory next to it, or the newest copy from ``<paths.db>/backup``, see :doc:`backups`) to the new installation. Every start of an instance renders ``bans.txt`` from it,
   so the new server knows all active bans before the first player connects. Nothing needs to be exported from
   the old server's ``bans.txt``.

DayZ's own ``ban.txt``
   Not used. BattlEye enforces by GUID and by IP, and dzo keeps it in sync from the database as described above.
   A second list that the game server reads would only be one more place to get out of step.

Who may do what
---------------

.. list-table::
   :header-rows: 1
   :widths: 20 80

   * - Role
     - Ban permissions
   * - ``detector``
     - only file evidence (``POST /evidence``); sees nothing else. For cheat detectors and scripts.
   * - ``viewer``
     - none
   * - ``announcer``
     - none
   * - ``moderator``
     - see bans and evidence; ban by Steam ID or GUID on instances the token may use, for at most 7 days;
       file evidence; change or revoke their own bans
   * - ``operator``
     - also permanent and longer bans, and IP bans
   * - ``admin``
     - also global bans, and changing or revoking anyone's ban

A token restricted to some instances (``--instance``) sees only the bans that apply to its instances, and cannot create global bans. It gets the same
"unknown instance" answer for an instance it is not allowed to use as for one that does not exist. So it
cannot probe instance names.

Evidence
--------

Evidence is filed against a Steam ID, a GUID or an IP, for an instance. It is one of:

* ``log``: log lines, at most 64 KiB, for example from a detector;
* ``note``: an admin's note;
* ``image``: a PNG, JPEG or WebP screenshot of at most 5 MiB, with a caption. dzo checks the file's
  content, not its name or type, and serves it with a fixed image type and ``nosniff``, never as HTML.

A ban can link evidence items. A detector files evidence like this:

.. code-block:: sh

   curl -H "Authorization: Bearer $DZO_TOKEN" -H 'Content-Type: application/json' \
     -d '{"steam_id":"76561198000000001","instance":"chernarus","kind":"log","body":"aimbot: 14 headshots in 9 s"}' \
     http://127.0.0.1:8080/api/v1/evidence

and an image with ``dzo evidence add --steam 76561198000000001 --instance chernarus --image shot.png
--caption "speed hack"``.

dzo also records which GUIDs and IPs each player was seen with, per instance (the *sightings*). They feed
the ban form and ``GET /players/<steamid>/identities``.

Several installations
---------------------

The web interface can show several installations. The ban form offers "also send to" for every other
backend; dzo then creates the same ban on each of them. Evidence links stay with the first backend,
because the evidence lives in that installation's database.

Where the data lives
--------------------

The database is ``<paths.db>/dzo.sqlite`` and the screenshots are in ``<paths.db>/evidence/``. Back both
up with the rest of ``paths.db`` (see :doc:`backups`); ``dzo serve`` also writes regular copies of the
database to ``<paths.db>/backup`` (``serve.db_backup_hours``, ``serve.db_backup_keep``). The database is SQLite in the pure-Go
``modernc.org/sqlite`` driver, so dzo needs no extra system package.
