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
     - persistent vehicles: id, kind (``car`` or ``motorbike``), class, position, health, fuel, crew
   * - ``GET /instances/<i>/events``
     - active events (effect areas, markers from mods)
   * - ``GET /instances/<i>/layers``
     - marker layers you may see
   * - ``GET /instances/<i>/map/markers[?layer=<name>]``
     - markers you may see
   * - ``GET /instances/<i>/types[?q=<text>&limit=<n>]``
     - search the item classes the server reported: public (scope 2) items, weapons, magazines and clothing with a model, without map objects (``Land_*``, ``StaticObj_*``, ``bldr_*``), debug and placing helpers, and the classes known to be unfinished or to crash a server. The web interface offers them as you type, and ``spawn_item`` refuses any class that is not in the list
   * - ``GET /instances/<i>/stream``
     - server-sent events ``status``, ``players``, ``vehicles``, ``markers``,
       ``events`` and ``result``; the current state first, then every change
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
   * - ``POST /instances/<i>/message`` ``{"text", "style"}``
     - ``operator``
     - message to everyone
   * - ``POST /instances/<i>/players/<steamid>/message`` ``{"text", "style"}``
     - ``moderator``
     - direct message (``style``: ``chat``, ``important``, ``notification``)
   * - ``POST /instances/<i>/players/<steamid>/teleport`` ``{"x", "z", "y"?}`` or ``{"to_steam_id"}``
     - ``operator``
     - teleport; without ``y`` the player lands on the surface
   * - ``POST /instances/<i>/players/<steamid>/give`` ``{"type", "quantity", "health", "target"}``
     - ``operator``
     - spawn an item (``target``: ``inventory``, ``hands``, ``ground``;
       ``health`` 0 to 1)
   * - ``POST /instances/<i>/vehicles/<id>/repair`` ``{"scope"}``
     - ``operator``
     - ``all`` (repair, refuel and fit missing parts), ``engine``, ``parts``,
       ``wheels``, ``fluids`` (refuel) or ``missing`` (fit missing parts)
   * - ``POST /instances/<i>/vehicles/<id>/flip``
     - ``operator``
     - stand the vehicle upright on its wheels
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

Errors
------

Errors are ``{"error": "<text>"}`` with a matching status: ``401`` for a missing
or invalid token, ``403`` for a role without the permission, ``404`` for an
unknown instance or one the token may not use.

Markers and roles
-----------------

A marker is shown to roles at least as high as its ``visibility``. Markers
without one are visible to ``admin`` only.
