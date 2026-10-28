.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

JSON API
========

``dzo serve`` provides a JSON API under ``/api/v1`` for scripts, bots, the
``dzo player`` and ``dzo vehicle`` commands, and the web interface. The API is
the only interface of an installation that clients need: it contains nothing
specific to the host, so a client can use several installations by keeping one
base URL and one token for each.

Running it
----------

``dzo instance apply`` (``dzo units sync``) writes ``dzo-serve.service`` and enables it as soon as an
instance runs the dzo-admin mod, which needs the endpoint; ``serve.enabled: true`` or ``false`` in
``config.yaml`` forces it on or off. ``systemctl --user reload dzo-serve`` re-reads the site (the same as
``SIGHUP``).

``dzo serve`` listens on ``serve.listen`` (default ``127.0.0.1:8080``) for the
API and on ``serve.mod_listen`` (default ``127.0.0.1:2400``) for the
:doc:`dzo-admin mods <admin-map>`. Both refuse non-local addresses unless
``serve.allow_insecure_http`` is set: put a reverse proxy with HTTPS in front
of the API. Send ``SIGHUP`` after changing the site repository; new instances
appear and the live state of the others is kept.

Tokens
------

Every request needs ``Authorization: Bearer <token>``. Create tokens on the host:

.. code-block:: sh

   dzo token create deploy-bot --role operator --instance deerisle --ttl 720h
   dzo token list
   dzo token revoke <id>

A token has a role (:doc:`web`), optionally a list of instances it may touch,
and an expiry of at most one year. The secret is shown once; only a hash is
stored. A token created with ``--delegate`` (for the web interface) may name the
person it acts for in the ``X-Dzo-Actor`` header, and the origin in
``X-Dzo-Source``; both go into the audit log. For other tokens these headers are
ignored.

A token created with ``--web`` is for ``dzo web`` and the only kind that acts for logged-in people
(:doc:`web`). On its own it can do nothing: every request must also carry the login session of a
person in ``X-Dzo-Session``, and then acts as that person. Without the header the answer is ``403 this
token needs a user session``, for an unknown or expired session ``401 session expired``, for a
disabled person or one with no role on any existing instance ``403``, and while ``serve.web_url`` is
not set (user logins are off) ``503 logins are not configured``. The audit actor is ``<token
name>:<Steam ID>``; ``X-Dzo-Actor`` is ignored. A token is either ``--delegate`` or ``--web``, and a web
token has the role viewer, moderator, operator or admin.

Roles per instance
~~~~~~~~~~~~~~~~~~

A person has grants: a role on an instance, or on ``*`` (every instance, also later ones). Their
effective role on an instance is the higher of the grant on the instance and the grant on ``*``,
**capped by the web token**: it is the lower of that and the token's role, and nothing at all on
instances the token's list does not include. An instance without a role does not exist for them
(``404``, and it is left out of lists). Being moderator on one instance never unlocks anything on
another. A token without a person is checked against its own role on the instances it may touch, as it
always was.

Routes without an instance in the path check the permission like this:

.. list-table::
   :header-rows: 1
   :widths: 35 65

   * - Route
     - Rule
   * - ``GET /audit``
     - ``audit.view`` is installation-wide: admin on ``*`` (and a token for every instance)
   * - ``GET /instances``, ``GET /``, ``GET /tiles/...``
     - ``view`` on at least one instance; the list holds the instances with any role
   * - ``POST /bans``
     - ``ban.instance`` on every instance of the ban; ``ban.permanent`` (bans over 7 days) and
       ``ban.ip`` on every one of them too; a global ban needs ``ban.global``, which is installation-wide
   * - ``GET /bans``, ``GET /bans/<id>``, ``GET /bans/<id>/history``
     - ``ban.view`` on at least one instance of the ban (a global ban: anywhere); the lists keep only
       those bans. IP addresses in a ban are shown only with ``ip.history`` on every instance of it
   * - ``PUT /bans/<id>``, ``POST /bans/<id>/revoke``
     - as for creating, on the old and the new instances; only the creator or whoever holds
       ``ban.revoke_any`` (installation-wide) may change a ban
   * - ``GET|POST /evidence``, ``GET /evidence/<id>[/file]``
     - ``ban.view`` and ``evidence.add`` on the evidence's instance; lists keep the instances where it holds
   * - ``GET /players/<steamid>/identities``
     - ``ban.view`` on at least one instance; sightings are kept for the instances where it holds, and
       their addresses are shown only with ``ip.history`` on that instance
   * - ``GET /players``, ``GET /players/<steamid>``, ``POST /players/<steamid>/lookup``
     - ``player.history`` on at least one instance; everything about instances where it does not hold is
       left out (sessions, names per instance, bans, friends seen there)

Reading
-------

All paths start with ``/api/v1``. Responses are JSON.

.. list-table::
   :header-rows: 1
   :widths: 40 60

   * - Request
     - Answer
   * - ``GET /``
     - installation name, dzo version, API version, your role
   * - ``GET /instances``
     - instances you may see, each with the mod's connection state and counts
   * - ``GET /instances/<i>``
     - one instance's state, with ``next_restart`` when the instance has a ``restarts.schedule``
   * - ``GET /instances/<i>/players``
     - online players: Steam ID, name, position, health, ping, vehicle
   * - ``GET /instances/<i>/vehicles``
     - persistent vehicles: id, kind (``car``, ``motorbike``, ``boat``, ``bicycle`` or ``helicopter``), class, position, health, fuel, crew, and with DayZ Expansion ``owner`` and ``covered``
   * - ``GET /instances/<i>/events``
     - active events (effect areas, markers from mods)
   * - ``GET /instances/<i>/layers``
     - marker layers you may see
   * - ``GET /instances/<i>/map/markers[?layer=<name>]``
     - markers you may see
   * - ``GET /instances/<i>/types[?q=<text>&limit=<n>]``
     - search the item classes the server reported: public (scope 2) items, weapons, magazines and clothing, without the classes that crash a vanilla server or raise script errors when they are created (found with ``scripts/test-spawnable.sh``; run it for a server with mods). The web interface offers them as you type, and ``spawn_item`` refuses any class that is not in the list
   * - ``GET /instances/<i>/vehicle-types[?q=<text>&limit=<n>]``
     - search the vehicle classes the server reported: public cars, boats and helicopters of the game and the loaded mods. ``spawn-vehicle`` refuses any class that is not in the list. The mod does not know which of a mod's vehicles are broken: try them on a test server
   * - ``GET /instances/<i>/stream``
     - server-sent events ``status``, ``players``, ``vehicles``, ``markers``,
       ``events``, ``result`` and ``feed``; the current state first, then every change.
       ``?only=feed`` sends just the ``feed`` events and no snapshot
   * - ``GET /instances/<i>/feed[?after=<id>&limit=<n>]``
     - the game events (see :ref:`event-feed`), oldest first: those with an ``id`` above ``after``
       (at most ``limit``), or the newest ``limit`` without ``after``. A reader that remembers the last
       ``id`` it saw asks for everything after it, so it loses nothing while the events are still
       in the ring of 1000
   * - ``GET /audit[?instance=<i>&limit=<n>]``
     - audit entries, newest first (``admin`` role)

Players are identified by their Steam ID only.

Acting
------

.. list-table::
   :header-rows: 1
   :widths: 45 25 30

   * - Request and JSON body
     - Needs
     - Does
   * - ``POST /instances/<i>/message`` ``{"text", "style", "notify_type", "title", "seconds"}``
     - ``operator``
     - message to everyone
   * - ``POST /instances/<i>/players/<steamid>/message`` ``{"text", "style", "notify_type", "title", "seconds"}``
     - ``moderator``
     - direct message. ``style``: ``chat`` (yellow), ``important`` (red), ``friendly`` (green), ``blue``, ``notification`` (on screen),
       ``expansion`` (an Expansion notification of ``notify_type``: ``toast`` (default), ``baguette``, ``activity``, ``killfeed``,
       ``market``, ``garage``; refused unless the instance's ``dzo-admin`` reports Expansion, see ``hello.features`` of the instance status),
       and for the message to everyone ``rcon`` (BattlEye ``say``, no mod needed). ``title`` (at most 64 bytes, default ``Server``) and
       ``seconds`` (default 10, at most 60) shape the two notification styles
   * - ``POST /instances/<i>/players/<steamid>/kick`` ``{"reason"}``
     - ``moderator``
     - kick an online player through BattlEye; the reason is shown to them (1 to 64 bytes). ``404`` if they are not online
   * - ``GET /instances/<i>/players/<steamid>/connection``
     - ``operator``
     - the address the player is connected from (``player.ip``)
   * - ``POST /instances/<i>/players/<steamid>/heal``, ``.../fixgear``, ``.../kill``
     - ``operator``
     - heal the player, repair everything they carry and wear, or kill them (no body). Each fails for a player who is not online, and heal and fix gear for a dead one
   * - ``POST /instances/<i>/players/<steamid>/teleport`` ``{"x", "z", "y"?}`` or ``{"to_steam_id"}``
     - ``operator``
     - teleport; without ``y`` the player lands on the surface
   * - ``POST /instances/<i>/players/<steamid>/give`` ``{"type", "quantity", "health", "target", "attachments"}``
     - ``operator``
     - spawn an item (``target``: ``inventory``, ``hands``, ``ground``;
       ``health`` 0 to 1; ``attachments`` true: the item gets the kit the game's own debug spawn
       gives it, such as a battery for a flashlight; when that leaves it bare, the mod fits the parts it
       found in the item configs, cached in ``profiles/dzo/admin/kits.json`` and scanned again when the
       class list changes or a part no longer fits). ``quantity`` is passed to the game's
       ``SetQuantity``: ``0`` makes the item empty (a class that cannot exist empty is deleted by the game),
       a number above what one stack holds makes as many stacks as it needs, at most 50. ``-1`` leaves the
       quantity at what the game makes of a new item (also when ``quantity`` is left out) and ``-2`` fills one
       stack to the class's maximum; the web forms and ``dzo player`` take ``default`` and ``max`` for them
   * - ``POST /instances/<i>/spawn`` ``{"type", "quantity", "health", "x", "z", "attachments"}``
     - ``operator``
     - spawn an item on the ground at a position, as ``give`` with ``target`` ``ground`` does at a player
   * - ``POST /instances/<i>/spawn-vehicle`` ``{"type", "x", "z"}``
     - ``operator``
     - spawn a vehicle on the ground at a position, with the parts and fuel of the game's debug spawn
   * - ``POST /instances/<i>/vehicles/<id>/repair`` ``{"scope"}``
     - ``operator``
     - ``all`` (repair, refuel and fit missing parts), ``engine``, ``parts``,
       ``wheels``, ``fluids`` (refuel) or ``missing`` (fit missing parts)
   * - ``POST /instances/<i>/vehicles/<id>/flip``
     - ``operator``
     - stand the vehicle upright on its wheels
   * - ``POST /instances/<i>/vehicles/<id>/move`` ``{"x", "z"}``
     - ``operator`` (``player.teleport``)
     - put the vehicle on the ground at ``x``/``z``, upright, at rest and with its heading, to pull a stuck one out. Players inside
       stay seated and move with it, but only up to ``admin.move_occupied_max`` metres (default 200; 0 refuses those moves), with
       their damage and the vehicle's switched off for about 3 seconds. Refused: a position outside the world, a covered
       Expansion vehicle, a vehicle that is not there. Needs dzo-admin 0.7.0.
   * - ``POST /instances/<i>/flags/<id>/raise``
     - ``operator``
     - raise a territory flag to the top of its pole and fill its timer (the 40 days a freshly raised flag has); ``<id>`` is the id of its marker on the ``Flags`` layer
   * - ``DELETE /instances/<i>/vehicles/<id>[?force=1]``
     - ``operator``
     - delete a vehicle (``force``: even with crew inside)

Actions are carried out by the game server, so the call waits for its answer:

* ``200`` with ``{"id", "ok": true}``: done.
* ``422`` with ``{"ok": false, "message"}``: the game server refused (for
  example the player is not online).
* ``503``: the instance's dzo-admin is not connected. ``504``: it did not answer
  in time.
* ``400``: the request is invalid, or the class is denied on this instance.
* ``429``: rate limit (120 actions per minute per token, 30 per minute per
  target).
* ``?wait=<seconds>`` changes how long to wait (default 10, at most 25).
  ``?wait=0`` returns ``202`` with a command ``id`` at once; poll
  ``GET /instances/<i>/commands/<id>`` (``202`` while pending).

Every action, including refused and rate-limited ones, is written to the audit
log (``${paths.db}/audit.jsonl``) with the actor, the source, the target and the
result.

.. _event-feed:

Event feed
----------

``dzo serve`` keeps the newest 1000 game events of every instance and serves them on
``GET /instances/<i>/feed`` and the ``feed`` events of the stream. They are also written to
``${paths.db}/feed/<instance>.jsonl`` (mode ``0600``, one JSON object per line), so the feed is not empty
after ``dzo serve`` restarts. The file holds chat and positions, which is personal data: it keeps at most the
newest 1000 events per instance and is safe to delete, the feed then starts empty.

Every event has an ``id`` (dzo's, increasing per instance), a ``time`` and a ``kind``. The other fields are
present when they apply:

.. list-table::
   :header-rows: 1
   :widths: 25 75

   * - Field
     - Meaning
   * - ``steam_id``, ``name``, ``ai``
     - the actor: the killer, the player who built, joined, chatted. ``ai`` is true for an Expansion AI (no Steam ID)
   * - ``target_steam_id``, ``target_name``, ``target_ai``
     - the victim of a ``kill`` or ``death``, or the player an admin action was for
   * - ``has_pos``, ``x``, ``y``, ``z``
     - where it happened (the victim's position for a kill)
   * - ``weapon``, ``distance``
     - a ``kill``: the weapon (``fists`` without one, a vehicle for a road kill) and the distance in metres, 0 for melee
   * - ``cause``
     - a ``death``: ``self``, ``infected``, ``animal``, ``explosion``, ``vehicle`` or ``other``
   * - ``object``, ``channel``, ``text``
     - the part, item or killer class; the chat channel; the chat text, kick reason or the details of the event

The kinds:

.. list-table::
   :header-rows: 1
   :widths: 25 75

   * - Kind
     - Happened
   * - ``kill``, ``death``
     - a player or an AI was killed by someone, or died without a killer
   * - ``connect``, ``disconnect``
     - a player joined or left
   * - ``chat``, ``kick``
     - in-game chat and BattlEye kicks, from BattlEye RCon (needs RCon, as bans do)
   * - ``build``, ``dismantle``, ``destroy``, ``lock_cut``
     - base building and raiding: a part built, taken apart or destroyed, a combination lock cut
   * - ``flag_raised``, ``flag_lowered``
     - a territory flag moved
   * - ``stash_buried``, ``stash_dug_up``
     - a stash buried or dug up
   * - ``admin``
     - an action of an operator (kick, ban, teleport, broadcast, ...), from the audit log
   * - ``dropped``
     - the mod's buffer overflowed; ``text`` is the number of events lost

Bans and evidence
-----------------

These routes need the ban database of ``dzo serve`` (``503`` without it) and are described in
:doc:`moderation`. A restricted token sees and changes only the bans that apply to its instances.

.. list-table::
   :header-rows: 1
   :widths: 45 20 35

   * - Request and JSON body
     - Needs
     - Does
   * - ``GET /bans[?state=active|expired|revoked&instance=&q=&limit=]``
     - ``moderator``
     - bans, newest first (``q``: Steam ID, GUID, IP, text in the reason or a player name; ``limit`` at most 1000)
   * - ``POST /bans`` ``{"steam_id", "guids", "ips", "reason", "message", "expires_at", "scope", "instances", "evidence"}``
     - ``moderator``
     - create a ban; ``expires_at`` null is permanent. ``201`` with ``{"ban", "sync"}``, where ``sync`` says ``ok`` or
       ``pending: <why>`` per instance. A moderator gives bans of up to 7 days; longer ones and IPs need
       ``operator``, ``scope`` ``global`` needs ``admin`` and a token for every instance
   * - ``GET /bans/<id>``
     - ``moderator``
     - one ban
   * - ``PUT /bans/<id>`` (the full ban, as for ``POST``)
     - ``moderator``
     - change a ban; only its creator or an ``admin`` may
   * - ``POST /bans/<id>/revoke`` ``{"reason"}``
     - ``moderator``
     - end a ban now; it stays in the list and the history
   * - ``GET /bans/<id>/history``
     - ``moderator``
     - every change with the actor and the old and new values
   * - ``GET /evidence?steam_id=&guid=&ip=``
     - ``moderator``
     - evidence filed against a player
   * - ``POST /evidence`` ``{"steam_id" or "guid" or "ip", "instance", "kind", "body"}`` or ``multipart/form-data`` with the same fields and a ``file``
     - ``detector``
     - file a log (``kind`` ``log``, ``body`` at most 64 KiB), a note, or an image (PNG, JPEG or WebP, at most 5 MiB; ``body`` is the caption)
   * - ``GET /evidence/<id>``, ``GET /evidence/<id>/file``
     - ``moderator``
     - one item, and its image (always served as an image type, with ``nosniff``)
   * - ``GET /players/<steamid>/identities``
     - ``moderator``
     - the GUIDs and IPs the player was seen with, per instance, and how many other Steam IDs share each IP
   * - ``GET /players?q=&instance=&flagged=&online=&sort=&dir=&limit=&offset=``
     - ``moderator`` (``player.history``)
     - the player database: ``{"players": [...], "total": n}``, most recently seen first. ``q`` is a Steam ID, a GUID or a
       part of any name the player used; ``flagged=1`` keeps players with a BattlEye ban, a Steam ban or a ban of ours; ``online=1`` keeps the players who are on an
       instance now (with ``instance`` set, on that one);
       ``sort`` orders by ``name``, ``last`` (last seen), ``first`` (first seen), ``be`` (BattlEye ban), ``steam`` (Steam
       ban) or ``here`` (banned on our servers), ascending, or descending with ``dir=desc``; another value is a 400. Without ``sort``
       the newest come first. ``limit`` defaults to 50 and is at most 200. A token for some instances only sees players seen on them
   * - ``GET /players/<steamid>``
     - ``moderator`` (``player.history``)
     - one player: names, per-instance totals, the newest 50 sessions, BattlEye's answer per game (``be``), what Steam
       said, our bans that name the player (``bans``), where the player was seen (``identities``, addresses only with
       ``ip.history``), the Steam friends who also played here (``friends_seen``, at most 10) and the Steam ``profile_url``
   * - ``POST /players/<steamid>/lookup``
     - ``moderator`` (``player.history``)
     - ask BattlEye and Steam about the player now and answer the fresh record (audited as ``player.lookup``). ``429`` for a
       player looked up in the last 10 minutes, ``502`` when a source fails, ``503`` without lookups

Errors
------

Errors are ``{"error": "<text>"}`` with a matching status: ``401`` for a missing
or invalid token, ``403`` for a role without the permission, ``404`` for an
unknown instance or one the token may not use.

Markers and roles
-----------------

A marker is shown to roles at least as high as its ``visibility``. Markers
without one are visible to ``admin`` only.
