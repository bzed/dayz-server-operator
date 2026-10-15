.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Admin map and the dzo-admin mod
===============================

The web interface has a live map of each server: players, vehicles, active
events and markers from other mods. Dragging a player's icon teleports the player, and
the context menu of every icon offers its actions.

The dzo-admin server mod
------------------------

Everything that changes the game goes through **dzo-admin**, a small server-side
mod (clients do not need it). It reports players and vehicles and carries out
admin actions:

* direct messages and broadcasts (in chat, as an important message, or as an
  on-screen notification),
* teleport (to a map position or to another player),
* spawn items into a player's inventory, hands or on the ground,
* repair or delete vehicles.

The mod connects *out* to dzo: about once a second it posts what changed and the
results of finished actions to ``dzo serve``, and gets the next actions in the
reply. Nothing connects into the game server. The mod knows persistent
vehicles of the car type (``CarScript``, which includes most modded cars) and, on DayZ 1.30,
motorbikes (``MotorbikeScript``); both can be repaired and deleted. Each vehicle has a kind,
``car`` or ``motorbike``, shown in the vehicle list and as a round icon on the map. Boats and
helicopters are not reported yet.

Installing it
~~~~~~~~~~~~~

dzo-admin is a local server mod, not a Steam Workshop mod. The dzo package ships
the packed mod in ``/usr/share/dzo/servermods/dzo-admin`` (a source checkout
builds it with ``make servermods`` into ``dist/servermods/dzo-admin``; the
output is reproducible, so the PBO hash is the same on every build). The
directory also holds ``compat.yaml``, which records the commit of the dzo-admin
source and the hash of every PBO that was built from it. ``dzo mod add`` and
``dzo mod update`` check the packed mod against it and refuse a PBO that
differs; ``--ignore-compat`` uses it anyway, for example for a build of your
own. Point ``site.yaml`` at it and enable it per instance:

.. code-block:: yaml

   # site.yaml
   local_mods:
     dzo-admin: {path: /usr/share/dzo/servermods/dzo-admin}

   # instances/deerisle/instance.yaml
   mods:
     - {local: dzo-admin, server: true}
   admin:
     deny_classes: ["Land_*"]

Run ``dzo mod add dzo-admin --instance deerisle --server`` to install it, then
start the instance. dzo recognises the mod by the name ``dzo-admin``.

Every time the instance starts, ``dzo instance render`` writes the mod's
configuration to ``profiles/dzo-admin/config.json`` with a **new token** (the
file is readable by the owner only and is not part of the site repository). The
token is the only protection of the endpoint, so keep ``serve.mod_listen`` on
the local host. With ``network: host`` the mod connects to ``127.0.0.1``; with
``network: publish`` to ``host.containers.internal``.

Settings in ``instance.yaml`` under ``admin`` (see :doc:`configuration`):

* ``sync_ms``, ``players_s``, ``vehicles_s``, ``markers_s``, ``events_s``: how
  often the mod polls for actions and pushes each kind of state;
* ``allow_spawn``, ``allow_classes``, ``deny_classes``: which item classes
  admins may spawn. A class must be a valid class name, must not match a deny
  entry, and, if an allow list exists, must match it. dzo also rejects classes
  the server did not report.

Without dzo-admin the web interface cannot show the map or run actions. Other
features that do not need it (status, RCon) are unaffected.

Check that it works: ``dzo player list <instance>`` shows the online players
once the mod has connected, and the instance page shows "dzo-admin connected".

Map background
--------------

The map shows the map's own satellite imagery, built by dzo from the game's
data. Without it the map draws a plain 1 km grid; everything else works the
same.

Which imagery a map uses is decided by the world name: the dzo-admin mod reports the
world of the running server (``chernarusplus``, ``enoch``, ``sakhal``, ``deerisle``, ...), and
the web interface uses the tile set of that name, lower-cased, if one has been built
(``dzo map tiles status`` lists them). Nothing is chosen per instance and nothing is built
by itself: ``map_tiles.maps.<name>.source`` below only tells ``dzo map tiles build <name>``
where the source file is. Until it has been run, the map shows the plain grid and the title
says "no map imagery". The map needs the instance to be running with dzo-admin, because the world
name comes from it.

The imagery comes from the data PBO of the map, which holds the satellite tiles
and, for each, the world rectangle it covers. dzo reads the position, the
orientation and the overlap of every tile from that, so it does not matter which
corner a map numbers its tiles from, or how large the map is. The PBO is:

* **vanilla maps**: the DayZ *client's* ``worlds_<map>_data.pbo``
  (``Addons/worlds_chernarusplus_data.pbo``, ``Addons/worlds_enoch_data.pbo`` for
  Livonia, ``sakhal/Addons/worlds_sakhal_data.pbo``). The dedicated server's
  copy of these files only has stubs, and dzo refuses it with a message that says
  so.
* **modded maps**: the map mod's data PBO, for example DeerIsle's ``Addons/data.pbo``.

.. code-block:: sh

   dzo map tiles build enoch /path/to/DayZ/Addons/worlds_enoch_data.pbo
   dzo map tiles status

or name the file once in ``/etc/dzo/config.yaml`` (the key is the world name of
the mission) and run ``dzo map tiles build <map>``:

.. code-block:: yaml

   map_tiles:
     maps:
       enoch: {source: /srv/dayz/mapsources/worlds_enoch_data.pbo}

Building takes a few seconds and about 60 MB of disk per map. A
source that was built before is skipped unless you pass ``--force``; ``dzo map
tiles status`` says when the source file changed since. Tiles are kept below
``paths.cache`` and served by ``dzo serve`` to authenticated users only, so
the web interface shows them to everyone who may see the map, and to nobody
else. The imagery is Bohemia Interactive's artwork, which is why dzo builds
it on your host from your own game files and does not ship it.

Active events
-------------

dzo-admin reports effect areas (for example contaminated zones) with their
position and radius, and events that other mods publish through the marker API.
They are shown as a map layer and available at ``/events`` in the API. Events of
the Central Economy (helicopter crashes, convoys) are not detected yet; add
them with watch rules below until they are.

Markers from other mods
-----------------------

Other mods can put their content on the admin map (UFO crashes, AI convoys,
camp fires, trader zones, …) without depending on dzo-admin. They keep working
when dzo-admin is not installed. There are three ways, from easiest to most
flexible.

1. Watch rules (no code)
~~~~~~~~~~~~~~~~~~~~~~~~

Configure per instance which object classes appear on the map:

.. code-block:: yaml

   admin_map:
     watch:
       - {layer: campfire,  classes: ["FireplaceBase"], icon: fire, color: "#e67e22", max: 200}
       - {layer: ufo_crash, classes: ["UFO_Crash_Site*"], icon: rocket, color: "#8e44ad", label: "UFO"}
       - {layer: ai_convoy, classes: ["eAIBase"], icon: android, color: red, cluster: 50}

``icon`` and ``color`` give the markers of the layer their look, see :ref:`map-icons`.
Watch rules can also be set once for all instances, under ``defaults:`` in ``site.yaml``
(``defaults: admin_map: watch: [...]``). They are merged with the rules of an instance **by layer
name, and the instance wins**: a rule of the instance replaces the default rule of the same layer
as a whole (its ``classes``, ``icon``, ``color`` and the rest are not mixed with the default's),
default rules for other layers stay, and the instance's own layers are added after them. There is
no way to remove a default rule for one instance; give the layer an instance rule that matches
nothing you care about (for example ``classes: ["NoSuchClass"]``) if you have to.
``dzo instance show <name>`` prints the merged list.

Rules apply to items (including fireplaces) and cars; buildings, animals and
players cannot be watched this way yet. Subclasses match too, and a ``*`` at the end of a class name matches a prefix.
``max`` limits the markers of a layer. ``cluster`` is a hint for the map
display. ``only_if`` is not supported yet and is rejected, so that it is never
silently ignored.

2. Script API
~~~~~~~~~~~~~

A mod calls the API only when dzo-admin is present:

.. code-block:: c

   DZOAdmin_Map.Upsert("ufo_crash", m_CrashId, GetPosition(), "rocket", "UFO crash", props, 3600);
   DZOAdmin_Map.Track("ai_convoy", convoyLeader, "truck", "Convoy " + m_Name);  // follows the entity
   DZOAdmin_Map.Remove("ufo_crash", m_CrashId);

Functions: ``Upsert``, ``Circle``, ``Path``, ``Track``, ``Untrack``, ``Remove``,
``ClearLayer`` and ``DefineLayer``. dzo-admin defines ``DZO_ADMIN`` for scripts,
so the ``#ifdef`` above compiles only when it is installed. Do not list it in
your mod's ``requiredAddons``. The dzo-admin repository has a small example mod
(``examples/marker-demo``) that shows how to call the API without a hard
dependency.

3. File drop
~~~~~~~~~~~~

Write ``$profile:dzo-admin/markers/<layer>.json``, either a JSON list of
markers or ``{"markers": [...]}``. The layer name defaults to the file name,
and dzo re-reads the files every few seconds. This works for any mod or
external tool, and also when the mod cannot use ``#ifdef``.

Marker fields
~~~~~~~~~~~~~

.. list-table::
   :header-rows: 1

   * - Field
     - Meaning
   * - ``layer``
     - category; each layer is a toggle on the map
   * - ``id``
     - unique within the layer
   * - ``shape``
     - ``point`` (default), ``circle``, ``polygon`` or ``polyline``
   * - ``position`` / ``points``
     - world coordinates (x, z)
   * - ``entity``
     - follow an object; the marker moves with it and disappears with it
   * - ``icon``, ``color``, ``label``
     - presentation: an icon name (:ref:`map-icons`) and a colour; a marker that sets neither gets those of its layer
   * - ``props``
     - key/value pairs shown in the tooltip
   * - ``ttl``
     - lifetime in seconds
   * - ``visibility``
     - lowest role that sees the marker (``admin`` by default)

.. _map-icons:

Icons and colours
~~~~~~~~~~~~~~~~~

A point marker is drawn as a round badge with an icon in it, in the marker's colour.
The icons are those of `Fork Awesome <https://forkaweso.me/Fork-Awesome/icons/>`_ (a
community fork of Font Awesome 4.7, SIL OFL), which comes with the ``fonts-fork-awesome``
package that dzo depends on. ``icon`` is the icon's name without the ``fa-``
prefix: ``fire``, ``car``, ``truck``, ``plane``, ``ambulance``, ``bus``, ``tree``,
``bomb``, ``crosshairs``, ``user-secret``, ``rocket``, ``android``, ``flag``, ``home``,
``medkit``, ``key``, ``gift``, ``trophy``, ``wrench``, ``star``, ``bolt``, ``eye``
and about nine hundred more. Browse the gallery on the Fork Awesome site, or list
the names installed on the host:

.. code-block:: sh

   grep -o '^\.fa-[a-z0-9-]*' /usr/share/javascript/fork-awesome/css/fork-awesome.css | sed 's/^\.fa-//' | sort -u

``color`` is a CSS colour: ``#rgb``, ``#rrggbb`` (``#rrggbbaa`` with transparency), or a name
such as ``red`` or ``orange``. Circles, polygons and lines use it for their outline.

Where the look comes from, first match wins:

1. the marker itself (``icon`` and ``color`` in the API, the file drop or the script API);
2. the ``icon`` and ``color`` of the ``admin_map.watch`` rule for its layer in ``instance.yaml``;
3. the layer the mod defined with ``DZOAdmin_Map.DefineLayer(name, icon, color)``.

A marker with no icon, or with a name the font does not have, is a plain dot, and with no
colour the default purple. ``dzo site validate`` warns about an icon name of a watch rule that
the installed font does not know, and rejects a malformed name or colour. Lines, circles and
polygons have no icon. Icons shipped inside mod PBOs are not supported.
