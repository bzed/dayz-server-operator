.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Web interface
=============

``dzo web`` serves the web interface. It is a thin layer over the :doc:`API
<api>`: every button is one API call, so what you can do in the browser you can
also do with ``dzo player``, ``dzo vehicle`` or a script.

The web interface only ever talks to installations through their API. It can
run on the same host as ``dzo serve`` (the default), or on another host, and
one web interface serves **one dzo installation**.

.. code-block:: sh

   sudo -iu dayz dzo token create web --role admin --web > /var/lib/dzo/secrets/web.token
   sudo -iu dayz dzo serve &      # the API (to try it by hand, see below)
   sudo -iu dayz dzo web          # http://127.0.0.1:8081 (to try it by hand, see below)

``dzo instance apply <name>`` (or ``dzo units sync``) writes ``dzo-web.service`` and starts it as soon
as the web interface is set up, that is when ``web.api`` in ``config.yaml`` is set or the
token file above (``<paths.secrets>/web.token``) exists. ``web.enabled: true`` forces the unit on (it then
fails until the token exists), ``false`` keeps it off. It is not written before that, because the web
interface is optional and cannot start without a token. ``dzo-serve.service`` is written when an
instance runs the dzo-admin mod. With the units running, ``dzo serve`` and ``dzo web`` by hand would
find their ports taken: stop the unit first (``systemctl --user stop dzo-web``) if you want to run one
in the foreground, for example to see its output.

Another host
------------

On another host than ``dzo serve``, set ``web.api`` in ``/etc/dzo/config.yaml``. The token is
created *on the installation*:

.. code-block:: yaml

   web:                             # excerpt; every key of ``web:`` is explained in :doc:`configuration`
     api:
       url: "https://dzo.example.invalid:8080"
       token_file: /var/lib/dzo/secrets/web.token
       ca_file: /etc/dzo/tls/serve-ca.pem   # only for a private CA

For several installations run one web interface for each.

.. _web-https:

HTTPS
-----

``dzo serve`` and ``dzo web`` both take a ``tls`` block (``cert_file`` and ``key_file``, optionally
``client_ca_file``; :doc:`configuration`) and then speak HTTPS. You bring the certificate, from certbot
or any other tool, or you put a reverse proxy in front. dzo has no ACME client of its own: it would
need ports 80 and 443, privileges and certificate state that the host's own tooling already handles.

A renewed certificate is picked up by itself. The files are checked on new connections (every few
seconds at most) and on ``systemctl --user reload dzo-web`` (SIGHUP), without a restart and without
dropping connections. A new pair that does not load is logged and the old one stays active. The CA
files (``client_ca_file``, ``web.api.ca_file``) reload the same way. A certbot renewal needs no deploy
hook beyond writing the files where ``tls`` points.

``web.listen`` on a non-loopback address is accepted when ``web.tls`` is set (or
``allow_insecure_http``). ``dzo web`` reaches ``dzo serve`` over HTTPS when ``serve.tls`` is set; for
a private CA give it ``web.api.ca_file``, and ``serve.tls.client_ca_file`` lets only hosts with a
certificate of that CA reach the API.

Port 443 without a proxy
~~~~~~~~~~~~~~~~~~~~~~~~

dzo runs as the ``dayz`` user in user units, which cannot be given ``CAP_NET_BIND_SERVICE``. Two ways
work:

* Lower the unprivileged port limit to 443 with ``/etc/sysctl.d/50-dzo.conf``::

     net.ipv4.ip_unprivileged_port_start = 443

  Simple, but every local user may then bind port 443 and above.

* Keep the limit and redirect 443 to ``web.listen`` (for example ``0.0.0.0:8443``) with nftables::

     table inet dzo {
       chain prerouting {
         type nat hook prerouting priority dstnat; policy accept;
         tcp dport 443 redirect to :8443
       }
     }

``setcap`` on the binary is not suitable: every package upgrade drops it, and it applies to every
user who runs the binary.

.. _web-account:

Account and people pages
------------------------

The **account page** (your name in the header) is for everybody:

* username and password, so that you can sign in without Steam (the second step stays); changing a password
  asks for the current one, and a wrong one counts like a failed login;
* the authenticator app: set up a new one with a QR code and prove it with a first code, which replaces the
  old one;
* passkeys: register more, see when each was used, remove one;
* new recovery codes (shown once, the old ones stop working);
* "log out everywhere else", which ends your other sessions.

The last second factor cannot be removed. Logging out is in the header.

The **people page** (``People`` in the header) is shown to admins on every instance only. It lists everybody
with a grant and lets you add a person by Steam ID, change their name and grants (``alpha=operator,
*=viewer``: a role per instance, ``*`` for all of them), disable them, reset them (password, username, second
factors and sessions are removed; they sign in through Steam and set up a second factor again) or delete
them. Your own row has no controls: nobody can change their own access in the web interface or the API;
another admin, or ``dzo user`` on the serve host, does that. Changes are in the audit log.

.. _web-setup-login:

Setting up logins (the minimum)
-------------------------------

On the host of ``dzo serve``:

1. Set ``serve.web_url`` in ``/etc/dzo/config.yaml`` to the address people type into their browser, for example
   ``https://dzo.example.org`` (scheme and host, no path; ``https``, or ``http`` for ``localhost`` only), and
   restart ``dzo serve``.
2. Make the token of the web interface and give it to ``dzo web``: ``dzo token create web --role admin --web >
   <paths.secrets>/web.token`` (with ``web.api`` on another host, :ref:`web-https`).
3. Make the first admin, with your own Steam64 ID (the 17-digit number from your Steam profile address, or
   from a site like steamid.io)::

      dzo user grant 76561198000000001 --role admin --name Ann

4. Open ``serve.web_url``, choose **Sign in through Steam**, then set up your second factor (authenticator app
   or passkey) and keep the recovery codes. Add everybody else on the people page (:ref:`web-account`).

**If Steam sign-in does not work (yet)**, give the admin a password on the host instead of step 4's Steam
sign-in::

   dzo user password 76561198000000001 --username ann      # asks twice, no echo

(a password is 12 to 256 bytes; piped in, one line is read). Then log in with that username and password. It
is only the first factor: the page asks for the second factor to be set up (authenticator app or passkey) and
shows the recovery codes, and every later login needs the password and that factor. Until it is set up,
whoever knows the password can set it up, so hand the password over safely; ``dzo user password`` again
changes it, ``dzo user reset`` removes it.

**What signing in through Steam needs.** Steam's sign-in is OpenID: there is no Steam Web API key, no
registration with Valve and no secret. It needs two things. First, ``serve.web_url`` has to be the address
the *browser* uses: Steam sends the browser back to it (``<web_url>/login/steam/return``), and it also names
the site Steam shows on its sign-in page; behind a reverse proxy this is the proxy's public address. Second,
``dzo serve`` has to reach ``https://steamcommunity.com`` outbound (HTTPS, port 443), because it asks Steam to
confirm every sign-in; nothing needs to reach ``dzo serve`` from Steam. Without Steam, a person who already
has a username and password can still log in with those.

.. _web-login:

Login
-----

Everybody who opens the web interface logs in. A login has two steps, always:

1. **A first factor**: username and password, or **Sign in through Steam**.
2. **A second factor**: a code from an authenticator app (TOTP), a **passkey**, or a one-time **recovery code**.

A passkey is only ever the second step. Without ``serve.web_url`` (:doc:`configuration`) logins are off and
the pages say so. ``dzo web`` only relays the steps: ``dzo serve`` checks the passwords, talks to Steam, holds
the sessions and decides every permission; the web interface keeps just a cookie.

**First visit.** Someone (an admin in the users page, or ``dzo user grant`` on the host) grants a Steam ID a
role. That person opens the web interface and signs in through Steam. A Steam ID without a grant gets "no
access", and nothing is stored for it. Because there is no second factor yet, the person is asked to set one
up: scan a QR code with an authenticator app, or register a passkey. Then they see their recovery codes, once.
Afterwards they may set a username and a password on the account page, so that they can also log in without
Steam; the second step stays.

**Sessions.** A session ends after ``serve.login.session_idle_hours`` (default 12) without a request, and
after ``serve.login.session_max_days`` (default 7) at the latest. Logging out, ``dzo user reset``, disabling a
person and removing their last grant end it at once. The cookie is ``HttpOnly`` and ``SameSite=Strict``;
on HTTPS (``serve.web_url`` starts with ``https://``) it is ``__Host-dzo_session``, ``Secure`` and host-only.
The half-finished login between the two steps lives five minutes in a ``SameSite=Lax`` cookie
(``dzo_login``): Steam sends the browser back with a navigation from another site, which a Strict cookie
would not survive. The return from Steam carries a random state that must match that cookie, so that nobody
can log a victim into the attacker's account with a prepared link, and it answers with a small page that moves
on by itself, so that the next request starts on this site.

**Guessing.** Five wrong passwords or codes per account and per client address are free, then every further
attempt makes the wait longer (one second, doubling, up to 15 minutes); a success clears the account. Waiting
logins get "too many failed logins" whether or not the username exists. Behind a reverse proxy, set
``web.trusted_proxies`` to the proxy's address: only then is its ``X-Forwarded-For`` believed (the right-most
address that is not a trusted proxy), and the browser's real address is what the login log and the audit log
show.

What the interface does about browser attacks: every change needs a token of the session that the pages hand
to htmx (and a same-origin check), responses carry a strict content security policy without inline scripts,
and pages cannot be framed. Behind HTTPS (:ref:`web-https`) nothing here needs a reverse proxy; dzo refuses
to listen on a non-local address without ``web.tls`` unless ``web.allow_insecure_http`` is set.

People and the first admin
--------------------------

People are identified by their Steam ID and have a role per instance (``viewer``, ``moderator``, ``operator``
or ``admin``, below), or on ``*`` for every instance. The first admin is made on the host of ``dzo serve``,
where nothing else is needed:

.. code-block:: sh

   dzo user grant 76561198000000001 --role admin --name Ann          # every instance
   dzo user grant 76561198000000002 --role operator --instance chernarus --instance livonia
   dzo user list

``dzo user`` also revokes, disables, deletes and resets (:doc:`cli`); an admin on ``*`` does the same through
the API (:doc:`api`).

Roles
-----

A person's role **on an instance** decides what the interface offers them there: controls are shown only
where the role allows them (an operator on ``chernarus`` and a viewer on ``livonia`` sees the buttons on the
first and none on the second; instances without a role are not listed). That hiding is a convenience: ``dzo
serve`` checks every action against the instance it touches, so a forged request gets a refusal with a clear
message. The effective role is the lower of the person's grant and the role of the web token. The roles are:

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
   with repair (everything, or only body, parts, wheels, fuel or missing parts), flip and delete (moving a vehicle is on the map). The
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
   Players, vehicles, active events and marker layers (including the rebuilt houses and wells with the time until decay wears them down, and an active sandstorm as a thin sand-coloured wash) on a map that updates as
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
   the Steam ID and the BattlEye GUID, an instance filter, "Only online players" (who is on an instance now; with an instance chosen, on that one) and "Only flagged players", most recently seen first. Clicking
   the header of the Name, Last seen, First seen, BattlEye, Steam or Here column sorts the whole list (not only the page) by
   it; a second click reverses it. Name starts A to Z, the others with the newest or the flagged players first; the arrow in the
   header shows the order, and the choice is part of the page's address, so it survives paging and can be shared. Instances is
   not sortable. The list has a red mark in the BattlEye, Steam and Here columns for a player something flags, with a
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
