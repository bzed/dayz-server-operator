.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Bans and evidence
=================

dzo bans players by Steam ID, BattlEye GUID or IP address, for a time or for good, on one instance or on
every instance of an installation. It keeps who banned whom and why, every change, and the evidence
(log lines, notes, screenshots) filed against a player. The tools are ``dzo ban`` and ``dzo evidence``,
the :doc:`web interface <web>` and the :doc:`JSON API <api>`. They need ``dzo serve`` to be running; the
bans themselves are enforced even when it is not (see below).

Bans
----

A ban covers one person through several **targets**:

* the Steam ID, and the BattlEye GUID dzo derives from it (``md5("BE" + the Steam ID as a little-endian
  64-bit number)``, checked against a real server);
* optional extra GUIDs;
* optional IPv4 addresses. BattlEye bans single addresses only, so there are no ranges and no IPv6.

A ban lasts ``30m``, ``12h``, ``7d``, ``2w`` or ``permanent`` (at most ten years, longer means permanent).
It has a **reason**, which only admins see (at most 1000 characters), and a **message** that the player
is shown (default: ``You are banned from this server``). BattlEye keeps at most 64 bytes of the message,
so dzo refuses a longer one instead of cutting it silently. Put the details, the log lines and the
screenshots in the reason and the evidence: those stay in the database and are never sent to the player.

**Scope**: ``instances`` lists the instances a ban applies to, ``global`` means every instance of this
installation. A ban can be edited (reason, message, expiry, scope, targets, linked evidence) and revoked.
A revoked ban stays in the list and in its history. Every change is recorded with who made it and the old
and new values.

When one person has several bans, the longest one decides: ``bans.txt`` has one line for each target, with
the latest expiry, and revoking one ban leaves the others in force.

Shared IP addresses
-------------------

dzo never bans an IP address by itself. Mobile networks put many players behind one address, so an IP ban
is a blunt tool. The ban form lists the addresses a player was seen with, and warns when other players
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

Players are still matched by address without showing it: the ban form lists the addresses a player was
seen with as "Address (name on instance, last seen), shared with N other players", and ticking one bans it.
The API gives each such address an opaque reference (``ref``, and ``ip_refs`` when a ban is made or edited)
instead of the address, and a reference only works for the player being banned or the ban being edited. Without
``ip.history`` a ban shows "address hidden", and editing it keeps its addresses. Addresses are never written to
the audit log. They may be stored hashed in the future; the references keep that possible. Reading the
history of a player's addresses is recorded in the audit log (``ip.history``, without the addresses).

**Retention.** dzo keeps who was seen, under which name, on which instance and when, for good. It keeps the
address of a sighting for 90 days, then drops it, unless a ban uses that address. The rows of one player on
one instance that lose their address merge into one that keeps the newest name and the whole period.
``dzo serve`` does this every minute with the other upkeep. It also vacuums the database file once a week
to give back the space of deleted rows. The references (``ref``) are ids from a sequence, so they never
change and are never reused, vacuum or not.

How bans are enforced
---------------------

Enforcement goes through BattlEye's ``bans.txt`` and a live check in ``dzo serve``.

``bans.txt``
   One ban per line, ``<GUID or IP> <expiry> <message>``, where the expiry is ``-1`` for a permanent ban and a
   Unix time otherwise (see the FAQ for what a real server was seen to do with it). dzo renders it from the
   database at **every start** of an instance (``dzo instance render``), so bans hold while ``dzo serve``
   is down, and again in ``dzo serve`` after every change and once a minute, so that expired bans leave
   the file. After writing, serve sends RCon ``loadBans``.

The enforcer
   For every instance with an RCon password, ``dzo serve`` keeps an RCon session. When a player connects,
   the enforcer looks up the GUID and the IP in the active bans and kicks a banned one with the ban message.
   When a ban is created or edited, the players who are online on the affected instances are checked at
   once. Every kick is written to the audit log (``ban.kick``).

Pending instances
   If an instance's server is down or RCon fails when a ban is made, the ban is stored anyway. The answer
   says ``pending`` for that instance, and the next sweep (every minute) or the next start syncs it.

Changes outside dzo
   dzo records the SHA-256 of what it wrote in ``profiles/battleye/.dzo-bans.sha256``. When ``bans.txt``
   differs from that (hand edits, or another tool's ``addBan``), ``dzo serve`` imports the active entries
   that are not banned yet as instance bans created by ``import:bans.txt`` and renders again. The start
   path never overwrites a file changed outside dzo. **Remove bans through dzo**: a line deleted from
   ``bans.txt`` by hand comes back at the next render.

A new or rebuilt server
   The database is the source of truth, not ``bans.txt``. Copy ``dzo.sqlite`` (and the ``evidence``
   directory next to it) to the new installation, and every start of an instance renders ``bans.txt`` from it,
   so the new server knows all active bans before the first player connects. Nothing needs to be exported from
   the old server's ``bans.txt``.

DayZ's own ``ban.txt``
   Not used. BattlEye enforces by GUID and by IP and is kept in sync from the database as described above,
   so a second list that the game server reads would only be one more place to get out of step.

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
   * - ``moderator``
     - see bans and evidence; ban by Steam ID or GUID on instances the token may use, for at most 7 days;
       file evidence; change or revoke their own bans
   * - ``operator``
     - also permanent and longer bans, and IP bans
   * - ``admin``
     - also global bans, and changing or revoking anyone's ban

A token restricted to some instances (``--instance``) sees only the bans that apply to its instances,
cannot create global bans, and gets the same "unknown instance" answer for an instance it may not use as
for one that does not exist, so it cannot probe instance names.

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
backend; the same ban is then created on each of them. Evidence links stay with the first backend,
because the evidence lives in that installation's database.

Where the data lives
--------------------

The database is ``<paths.db>/dzo.sqlite`` and the screenshots are in ``<paths.db>/evidence/``. Back both
up with the rest of ``paths.db`` (see :doc:`backups`). The database is SQLite in the pure-Go
``modernc.org/sqlite`` driver, so dzo needs no extra system package.
