.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Web interface
=============

``dzo web`` serves the web interface. It is a thin layer over the :doc:`API
<api>`. Every button is one API call, so what you can do in the browser you
can also do with ``dzo player``, ``dzo vehicle`` or a script.

The web interface only ever talks to installations through their API. It can
run on the same host as ``dzo serve`` (the default), or on another host. One
web interface serves **one dzo installation**.

.. code-block:: sh

   sudo -iu dayz dzo token create web --role admin --web > /var/lib/dzo/secrets/web.token
   sudo -iu dayz dzo serve &      # the API (to try it by hand, see below)
   sudo -iu dayz dzo web          # http://127.0.0.1:8081 (to try it by hand, see below)

``dzo instance apply <name>`` (or ``dzo units sync``) writes ``dzo-web.service`` and starts it
as soon as the web interface is set up. That is when ``web.api`` in ``config.yaml``
is set, or when the token file above (``<paths.secrets>/web.token``) exists.
``web.enabled: true`` forces the unit on (it then
fails until the token exists), ``false`` keeps it off. It is not written before that, because the web
interface is optional and cannot start without a token. ``dzo-serve.service`` is written when an
instance runs the dzo-admin mod. With the units running, ``dzo serve`` and ``dzo web`` by hand would
find their ports taken. Stop the unit first (``systemctl --user stop dzo-web``)
if you want to run one in the foreground, for example to see its output.

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
or any other tool, or you put a reverse proxy in front. dzo has no ACME client of its own. A client
would need ports 80 and 443, privileges and certificate state that the host's
own tooling already handles.

A renewed certificate is picked up by itself. ``dzo web`` checks the files on new connections
(every few seconds at most) and on ``systemctl --user reload dzo-web`` (SIGHUP).
It reads them without a restart and without
dropping connections. A new pair that does not load is logged and the old one stays active. The CA
file ``client_ca_file`` reloads the same way; ``web.api.ca_file`` is read at start, so after replacing that CA restart ``dzo web``. A certbot renewal needs no deploy
hook beyond writing the files where ``tls`` points.

``web.listen`` on a non-loopback address is accepted when ``web.tls`` is set (or
``allow_insecure_http``). ``dzo web`` reaches ``dzo serve`` over HTTPS when
``serve.tls`` is set. For a private CA, give it ``web.api.ca_file``.
``serve.tls.client_ca_file`` lets only hosts with a
certificate of that CA reach the API.

Port 443 without a proxy
~~~~~~~~~~~~~~~~~~~~~~~~

dzo runs as the ``dayz`` user in user units. A user unit cannot be given
``CAP_NET_BIND_SERVICE``. Two ways
work:

* Lower the unprivileged port limit to 443 with ``/etc/sysctl.d/50-dzo.conf``::

     net.ipv4.ip_unprivileged_port_start = 443

  Simple, but every local user can then bind port 443 and above.

* Keep the limit and redirect 443 to ``web.listen`` (for example ``0.0.0.0:8443``) with nftables::

     table inet dzo {
       chain prerouting {
         type nat hook prerouting priority dstnat; policy accept;
         tcp dport 443 redirect to :8443
       }
     }

``setcap`` on the binary is not suitable. Every package upgrade drops it, and
it applies to every user who runs the binary.

.. _web-account:

Account and people pages
------------------------

The **account page** (your name in the header) is for everybody:

* your username (the header shows it; only an administrator gives and changes it) and a password, so that you
  can sign in without Steam (the second step stays). Changing a password asks for the current one, and a
  wrong one counts like a failed login.
  Without a username there is no password login: an administrator gives you one;
* the authenticator app: set up a new one with a QR code and prove it with a
  first code. The new code replaces the old one;
* passkeys: register more, see when you last used each, remove one;
* new recovery codes (shown once, the old ones stop working);
* "log out everywhere else", which ends your other sessions.

The last second factor cannot be removed. Logging out is in the header.

The **people page** (``People`` in the header) is shown to admins on every instance only. It lists everybody
with a grant. It lets you add a person by Steam ID and **username**, change their username and
grants, disable them, reset them or delete them. The username is required: it is the login name and
what people see, 3 to 32 of ``a-z 0-9 . _ -``, unique, not case-sensitive. Real names are not asked
for. The grants (``alpha=operator,
*=viewer``) are a role per instance, ``*`` for all of them. A reset removes the password, the second
factors and the sessions; the username stays. The person then signs in through Steam and sets up a
second factor again. Your own row has no controls. Nobody can change their own access in the web
interface or the API; another admin, or ``dzo user`` on
the serve host, does that. Changes are in the audit log.

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

      dzo user grant 76561198000000001 --role admin --username ann

4. Open ``serve.web_url``, choose **Sign in through Steam**, then set up your second factor (authenticator app
   or passkey) and keep the recovery codes. Add everybody else on the people page (:ref:`web-account`).

**If Steam sign-in does not work (yet)**, give the admin a password on the host instead of step 4's Steam
sign-in::

   dzo user password ann      # or the Steam ID; asks twice, no echo

(a password is 12 to 256 bytes; piped in, one line is read). Then log in with the username from step 3 and that
password. The password is only the first factor. The page asks for the second
factor to be set up (authenticator app or passkey) and shows the recovery
codes. Every later login needs the password and that factor. Set up the second
factor before you give the password to anybody: whoever knows the
password can set up the factor. ``dzo user password`` again
changes it, ``dzo user reset`` removes it.

**What signing in through Steam needs.** Steam's sign-in is OpenID: there is no Steam Web API key, no
registration with Valve and no secret. It needs two things. First, ``serve.web_url`` has to be the
address the *browser* uses. Steam sends the browser back to it
(``<web_url>/login/steam/return``). It also names the site Steam shows on its sign-in
page. Behind a reverse proxy this is the proxy's public address. Second, ``dzo serve`` has to reach
``https://steamcommunity.com`` outbound (HTTPS, port 443). It asks Steam to confirm every
sign-in. Nothing needs to reach ``dzo serve`` from Steam. Without Steam, a person who already
has a username and password can still log in with those.

.. _web-login:

Login
-----

Everybody who opens the web interface logs in. A login has two steps, always:

1. **A first factor**: username and password, or **Sign in through Steam**.
2. **A second factor**: a code from an authenticator app (TOTP), a **passkey**, or a one-time **recovery code**.

A passkey is only ever the second step. Without ``serve.web_url`` (:doc:`configuration`) logins are off and
the pages say so. ``dzo web`` only relays the steps. ``dzo serve`` checks the
passwords, talks to Steam, holds the sessions and decides every permission.
The web interface keeps just a cookie.

**First visit.** Someone (an admin in the users page, or ``dzo user grant`` on the host) grants a Steam ID a
role. That person opens the web interface and signs in through Steam. A Steam ID without a grant gets "no
access"; nothing is stored for it. The person now has no second factor. So the page asks
them to set one up: scan a QR code with an authenticator app, or register a passkey. Then they see their
recovery codes, once. Afterwards they can set a password on the account page. Their username is the
one the administrator gave them, so they can also log in without Steam; the second
step stays.

**Sessions.** A session ends after ``serve.login.session_idle_hours`` (default 12) without a request, and
after ``serve.login.session_max_days`` (default 7) at the latest. Logging out, ``dzo user reset``, disabling a
person and removing their last grant end it at once. Changing or removing the password also ends it,
and so does removing a second factor (an authenticator app or a
passkey). That works in the browser or with ``dzo user
password``. Every other session of that person ends, so a stolen session does not outlive the
change. The session that made it stays (the command ends all of them). There is no limit on parallel sessions. The cookie is ``HttpOnly`` and ``SameSite=Strict``;
on HTTPS (``serve.web_url`` starts with ``https://``) it is ``__Host-dzo_session``, ``Secure`` and host-only.
The half-finished login between the two steps lives five minutes in a ``SameSite=Lax`` cookie
(``dzo_login``). Steam sends the browser back with a navigation from another site, and a Strict cookie
would not survive that navigation. The return from Steam carries a random state. The state must
match that cookie; this stops a prepared link that logs a victim into the attacker's account. The
return answers with a small page that moves on by itself. The next request then starts on
this site.

**Guessing.** Five wrong passwords or codes per account and per client address are free. Every further
attempt then makes the wait longer (one second, doubling, up to 15 minutes); a success clears the account. Waiting
logins get "too many failed logins" whether or not the username exists. Behind a reverse proxy, set
``web.trusted_proxies`` to the proxy's address. Only then is its ``X-Forwarded-For`` believed (the
right-most address that is not a trusted proxy). The browser's real address is what the login log
and the audit log show.

The interface protects against browser attacks in three ways. Every change needs a token of the
session that the pages hand to htmx, and every change goes through a same-origin
check. Responses carry a strict content security policy without inline scripts.
Pages cannot be framed. Behind HTTPS (:ref:`web-https`) nothing here needs a
reverse proxy; dzo refuses to listen on a non-local address without ``web.tls`` unless
``web.allow_insecure_http`` is set.

People and the first admin
--------------------------

People are identified by their Steam ID. Each person has a role per instance
(``viewer``, ``moderator``, ``operator`` or ``admin``, below), or on ``*`` for every
instance. Make the first admin on the host of ``dzo serve``. Nothing
else is needed on that host:

.. code-block:: sh

   dzo user grant 76561198000000001 --role admin --username ann          # every instance
   dzo user grant 76561198000000002 --role operator --username bob --instance chernarus --instance livonia
   dzo user list

``dzo user`` also revokes, disables, deletes and resets (:doc:`cli`); an admin on ``*`` does the same through
the API (:doc:`api`).

Roles
-----

A person's role **on an instance** decides what the interface offers them there. The interface
shows controls only where the role allows them. An operator on ``chernarus`` and a viewer on
``livonia`` sees the buttons on the first and none on the second. The switcher does not list
instances without a role. That hiding is a convenience: ``dzo serve`` checks every action against the
instance it touches, so a forged request gets a refusal with a clear message. The effective role
is the lower of the person's grant and the role of the web token. The roles are:

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

Every page reaches every other page in one click. The header has the **instance switcher**: a menu of
every installation's instances, with a dot for the connection state of the mod. Escape or a click
outside closes it. The header also has the links **Bans**, **Audit log** and **docs**. The first two
go to the installation of the page you are on. On the dashboard they go to the first
installation. The other installations are in the switcher. Pages of an instance (**Overview**, **Map**,
**Events**) share an *instance bar* under the header. It shows the instance's name, its live facts and
the three tabs, and the current tab is underlined. An installation that cannot be reached is marked
``unreachable`` in the switcher; the page still works.
The lists in the switcher are kept for 15 seconds.

Pages
-----

Dashboard
   All installations and their instances. The page shows the dzo-admin connection state, the world,
   the in-game time (sun or moon) and the players online. It also shows the vehicles, how long the
   game server has been up and the version of the dzo-admin mod. It also shows the time to the next
   scheduled restart (``now`` when it is overdue). An instance whose mod is
   not connected shows the schedule only. On a phone the table keeps the
   instance, the state, the players and the next restart. The
   map icon after an instance's name opens its map.

Instance
   The page shows three things. Broadcast message is one: you can send to everyone, by chat colour,
   on screen or with RCon ``say``. When ``dzo-admin`` reports DayZ Expansion, you can also send it
   as an Expansion notification of a type you pick, with an optional title and duration. The player
   list (refreshed every 5 seconds) is another: it has a panel of actions per player. The actions
   are message, teleport to coordinates or to another player, heal, fix gear and kick with a reason.
   You can also kill after a confirmation and give item with a search over the server's item
   classes. The vehicle list with repair (everything, or only body, parts,
   wheels, fuel or missing parts), flip and delete is the third. Moving a
   vehicle is on the map. The
   actions open directly under the player or vehicle they apply to.
   The vehicle list also shows who drove each vehicle last. The name
   links to that player, and the Steam ID is in the tooltip. It shows the
   time as "3d 0h ago" with the exact time (UTC) in the tooltip. Only the driver's
   seat counts, passengers do not. A vehicle nobody has been seen driving since dzo first saw it reads "not since" and that
   date (the day dzo first saw the vehicle). This needs a ``dzo-admin`` that reports drivers, and the column is
   empty without a record database. The position links to the map, which
   opens zoomed in on the vehicle
   with the class as the label. After the class, the label shows the owner (the Expansion keychain)
   and the last driver when they are known, for example
   ``Offroad_02 (owner Bob, last driver Dora)``. Owner and last driver are named apart, also when they are the same person.

   Click a column header to sort a table in the browser. A second click on the same header reverses
   the order. Until you click a header, the vehicle table sorts by owner, then class, and the player
   table sorts by name. The table keeps the chosen order when it refreshes.
   The player table sorts by Name, Health and Ping. The vehicle table sorts by Class, Kind, Health,
   Fuel, Owner, Last driver, Last driven and Crew. Position and Actions do not sort.

   "Last driven" sorts by the time itself. A vehicle with "not since" sorts by the date dzo first saw
   it. A vehicle with "-" sorts first in ascending order.

   Vehicles in the virtual garage of DayZ Expansion are not in the world. The list shows them apart,
   under **Stored in the Expansion garage**: class, position with the map link, stored by, owners and
   a note. The garage knows players by their BI GUID (the hashed Steam ID the game
   reports, not a Steam ID). Dzo therefore resolves them against the player database. "Stored
   by" and the owners show
   the player's name and
   link to the player's page (the GUID is in the tooltip). You can then act on them - message, kick, ban.
   A GUID of no known player reads ``unknown`` with the GUID in the tooltip. A garage owner is always
   connected to the server that keeps the garage, so this means a wiped or foreign database. The garage
   of a territory shows ``territory #<id>`` instead of a player. The position link opens the map on the
   stored position, with ``<class> (stored by <player>)`` as the label and that one vehicle as a marker.
   Otherwise, the map does not draw stored vehicles. A vehicle whose entity-storage file is missing
   can never come back; its note says ``no storage file``. The **Actions** button opens the vehicle
   panel. For a stored vehicle, it offers two forms to people who can teleport players. **Retrieve**
   takes the vehicle out of the garage and places it at an x/z position, standing on the ground
   there. The owners who are online are told in-game. **Move spawn point** changes where the vehicle
   comes back when an owner retrieves it. The vehicle stays in the
   garage, and its entry keeps the height of the ground. It then comes back standing on
   the ground, not in the air. The map marker of the position link offers the same two actions in
   its menu. The menu has **Retrieve to coordinates…** and **Move spawn point to coordinates…**
   beside **Remove marker**. You can also drag the marker to move the spawn point (see the Map
   page). The
   panel forms, the map menu and the drag call the same two actions, so all need the teleport
   permission. A viewer sees only **Remove marker** there. Both actions need a ``dzo-admin`` that
   reports the Expansion garage
   (the ``garage`` feature). With an
   older one the API refuses with ``the dzo-admin mod of this instance does not report the garage; update
   it`` (see :doc:`api`).

   **Clean up** sits above the vehicle list. People who can delete vehicles see the button; a viewer
   sees no button and the two routes answer ``403``. The button deletes the vehicles nobody has driven
   for a chosen time, in two steps. First, you choose the threshold: 2 weeks
   (14 days), 1 month (30), 2 months (60) or a number of days from 1 to 3650. Anything else shows
   ``days must be a whole number from 1 to 3650``. The preview then lists, oldest first, the vehicles
   with no one inside that were last driven longer ago than that. Each row shows class, kind,
   position with the map link, last driver, last driven and health. A vehicle never
   seen driven counts from the day dzo first saw it. Vehicles with crew inside, covered and
   garage-stored Expansion vehicles are never listed and never deleted. With no match the page says ``No
   vehicle has gone unused for N days.``. The page adds
   ``dzo has been tracking vehicles on this instance only since <date>; no vehicle can be older
   than that yet.`` when dzo has tracked the instance for less than N days. Second, ``Delete N
   vehicles`` asks once more in a
   browser dialog (``This cannot be undone.``) and sends exactly the previewed vehicles. It sends at
   most 500 per cleanup: when more match, the preview lists the 500 longest unused
   and says how many match in all. The cleanup then runs again for the rest.
   The API checks every one again at that moment: a vehicle driven or boarded since the preview is
   skipped. The API sends the others to the game, and the page waits for the
   mod's answer (up to 25 seconds). The result reads ``Deleted N vehicles; skipped M:``
   followed by each skipped vehicle with its reason: N counts the vehicles the mod confirmed. A
   vehicle the mod refused is listed with the mod's message.
   Vehicles the mod did not answer for in time are listed apart, under ``No answer yet for K, the mod
   may still delete them; reload the vehicle list to see:``. Their deletes stay
   queued, so the vehicle list shows later whether they are gone.
   Deletions never use ``force``. Every vehicle writes an audit entry
   (``vehicle_delete``, detail ``cleanup: not driven for N days``; see :doc:`api`). The step needs a
   ``dzo-admin`` that reports drivers (0.8.0). With an older mod the page shows the API's refusal
   instead of a list, because every vehicle
   would look unused.

   Banning is a link
   in the panel to the ban form with the player filled in. Hover over a player's name (or tap its
   ``i`` button on a phone). You see the Steam ID, the ping and, with the ``player.ip``
   permission (operators and admins), the address the player is connected from now. The history of
   addresses is not shown there; it needs ``ip.history`` (:doc:`moderation`). The country flag before a
   name and the country in that info come from the GeoIP databases (:doc:`geoip`). With ``player.ip``
   the info also shows the city,
   the approximate location and the provider. The
   panel's ``Profile`` button opens the player's page (see Players below). The panel shows the
   verdicts of BattlEye, Steam and our bans in one line before you act. A player who is flagged has
   a red marker after the name in the player table. The flag is a BattlEye global ban, a Steam ban
   or a ban of ours. The player table has no Steam ID column; the
   panel shows it. On a phone the table shows only the name, the health and the actions.

Events
   A live log of what happens in the game, in the style of ``tail -f`` (the ``Events`` tab). It
   shows kills (also by Expansion AI) with weapon and distance, deaths, joins and leaves, kicks, base
   building and raids,
   and combination locks cut. It also shows flags raised and lowered, stashes buried and dug up,
   in-game chat and the
   actions of the operators.
   The newest 200 events are there when the page opens. The feed is kept over restarts of ``dzo
   serve`` (see :ref:`event-feed`). New events are added
   at the bottom as they happen, and the page keeps at most 1000 rows. It follows the end of the
   log while you are there; if you scroll up to read, a "new events" button brings you back. A reconnect
   fetches what was missed. The checkboxes at the top hide groups of events (remembered in the browser).
   A player's name links to the player's row in the player list (``#player-<steamid>``). The row is
   then scrolled into view and highlighted for a few seconds. For a player who is not online, the
   page says so. A position links to the map, opened with the place marked
   (see :doc:`admin-map`).
   Needs the ``view`` role. Users who can broadcast (``operator`` and up) also get an input line: its text goes to all players as chat.
   Kills and deaths come from the dzo-admin mod (0.5.0 or newer). Chat comes from BattlEye's
   RCon, so RCon must be reachable, as it must for bans. The same feed is on the console with ``dzo events``
   (:doc:`cli`) and in Discord with the ``killfeed`` (:doc:`configuration`).

Live map
   The map shows players, vehicles, active events and marker layers, and it updates as the server
   reports. The marker layers include the rebuilt houses and wells with the time until decay wears
   them down. They also include an active sandstorm as a thin sand-coloured wash. A player's icon
   goes from red to green with the player's health (a thick red ring: unconscious, dashed: dead).
   The instance bar is also on the overview and the events page, as a snapshot there. It shows the
   in-game time (with a sun or moon), the players online and the uptime (since ``dzo serve`` last
   saw the mod start). If the instance has a ``restarts.schedule``, it also shows the time to the
   next scheduled restart. The bar sits on the map's satellite imagery if you built it (see
   :doc:`admin-map`). The map always fits the window and zooms in to a few
   pixels per metre; ``Fit`` shows the whole world again. Icons sit exactly on
   their position. **Drag a player's icon** and drop it to teleport the player
   there. The icon stays under the pointer and at the drop point until you answer (see
   :doc:`admin-map`). The crosshair and the
   coordinate readout show the landing place of the icon. You confirm before anything
   happens. **Drag a vehicle's icon** to move it the same way. Drag the marker of a
   stored vehicle to move its spawn point. The position link of the vehicle list opens the map with
   it. That is where the vehicle comes back when an owner retrieves it from the garage. When the
   answer says no, the marker snaps back. **Right-click** for the actions on a player, vehicle,
   event or marker. You can also click, or press the
   menu key on a focused icon. Right-click empty ground
   to teleport a player there. You can also spawn an item there (as many stacks as the quantity
   needs) or spawn a vehicle there. Menu entries
   you lack the role for are not shown. See :doc:`admin-map`.

Players
   The player database of an installation (``Players`` in the header). It needs the ``player.history``
   permission, that is the moderator role or higher, :doc:`moderation`. The list has a search over
   every name a player ever used, the Steam ID, the BattlEye GUID and the BI GUID. The BI GUID is
   the hashed Steam ID the game reports (what DayZ Expansion knows players by - paste a garage GUID
   to find its player). The list also has an instance filter. It also has "Only
   online players" (who is on an instance now; with an instance chosen, on that one). It also has
   "Only flagged players", most recently seen first. Clicking
   the header of the Name, Last seen, First seen, BattlEye, Steam or Here column sorts the whole
   list (not only the page) by it. A second click reverses it. Name starts A to Z, the others with
   the newest or the flagged
   players first. The arrow in the header shows the order, and the choice is part of the page's
   address, so it survives paging and can be shared. Instances is
   not sortable. The list shows a red mark in the BattlEye, Steam and Here columns for a player
   something flags. A player's page shows, under the name
   with the other names used, the Steam ID and the BI GUID beside it in the header. The header also
   shows the ID types the game and its mods know the player by. Below that is a **verdict row** of
   three cells; each cell answers one question and says how old the answer is:

   * **BattlEye**: whether BattlEye's master server lists the GUID as globally banned, one line per game (DayZ first, then
     Arma 3 and Arma 2). A ban in an Arma game alone is shown in red with the game's name, because it is the same Steam
     account. "No answer" means the master did not answer and is never "not banned"; "Not asked yet" is a new player.
   * **Steam**: VAC, game and community bans. Without a Web API key that is "VAC banned" or "No bans" from the public
     profile; with one, the counts and the days since the last ban, also for private profiles.
     "Private profile, no ban data" appears when a private profile gives nothing more. A line below
     lists what Steam said about the account. It shows the name, public or private, since when, the
     country, the Steam level and the DayZ hours, whatever the source gave. It also lists the
     player's public
     Steam friends who also played on your servers.
   * **Here**: our own bans of the player: "Banned permanently" or "Banned until", with the ban number and scope, linked
     to the ban; a past ban shows muted as "Ban expired" or "Ban revoked".

   Only a cell that reports a ban is red; "not banned" is plain text, not green, because the sources cannot promise more.
   **Check now** asks BattlEye and Steam again and swaps the row (once per 10 minutes per player).
   dzo otherwise refreshes the answers in the background (:ref:`player-history`). Below the row
   are the instances played with first and last day,
   sessions and time, the names and the newest 50 sessions. From the ban form's data it also shows
   where the player was seen (addresses only with ``ip.history``). **Ban…** opens the ban form with the
   player filled in. **Steam profile** opens
   the profile on steamcommunity.com in a new tab. On a phone the three cells stack in the same order. The Steam ID of a ban's
   target links to the player's page.

Bans
   One page per installation (``Bans`` in the header). It has
   the list of bans with a state filter and
   a search by Steam ID, GUID, IP, text or player name. It also has
   a ban page with its targets, scope, evidence,
   history, an edit form and revoke. A player's panel has a **Ban…** button. It opens the form with
   the Steam ID, the instance, the addresses the player was seen with and the evidence filed against
   them. The addresses show only with ``ip.history``, otherwise as "Address", with shared ones marked. The reason is a multi-line field and the duration defaults to permanent.
   "Where does the ban apply?" defaults to every instance of this installation (``ban.global``). The
   other choices are the instance the link came from and chosen instances. For roles without
   ``ban.global`` the default is the link's instance. "Also send
   to" repeats the ban on other installations. On the ban page you manage the evidence. Add a note,
   log lines or a screenshot (filed against the banned
   player and linked to the ban). You can also unlink an item, or link other evidence filed against
   the player. See :doc:`moderation`.

Audit log
   Who did what, when, from where (web, CLI or API), and the result. Needs the
   ``admin`` role.

Documentation
   This documentation is served under ``/docs/`` without login.

Not available yet
-----------------

These are planned and are not part of the web interface today:

* users with passwords and TOTP
* single sign-on
* restart timers and scheduled broadcasts
* editors for server configuration
