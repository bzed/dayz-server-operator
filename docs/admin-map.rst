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
reply. Nothing connects into the game server. The mod only knows persistent
vehicles of the car type (``CarScript``, which includes most modded cars); boats
and helicopters are not reported yet.

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
       - {layer: campfire,  classes: ["FireplaceBase"], icon: fire, max: 200}
       - {layer: ufo_crash, classes: ["UFO_Crash_Site*"], icon: ufo, label: "UFO"}
       - {layer: ai_convoy, classes: ["eAIBase"], icon: ai, cluster: 50}

Rules apply to items (including fireplaces) and cars; buildings, animals and
players cannot be watched this way yet. Subclasses match too, and a ``*`` at the end of a class name matches a prefix.
``max`` limits the markers of a layer. ``cluster`` is a hint for the map
display. ``only_if`` is not supported yet and is rejected, so that it is never
silently ignored.

2. Script API
~~~~~~~~~~~~~

A mod calls the API only when dzo-admin is present:

.. code-block:: c

   DZOAdmin_Map.Upsert("ufo_crash", m_CrashId, GetPosition(), "ufo", "UFO crash", props, 3600);
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
     - presentation
   * - ``props``
     - key/value pairs shown in the tooltip
   * - ``ttl``
     - lifetime in seconds
   * - ``visibility``
     - lowest role that sees the marker (``admin`` by default)

Icons
~~~~~

There is no list of icons to choose from yet. ``icon`` is a free name (``fire``,
``ufo`` and ``ai`` in the examples above are just names): dzo-admin sends it with
the marker, and it appears in the marker's ``icon`` field of the API, but the map
does not use it. It draws every marker as a coloured shape: a dot for a point, or
the circle, polygon or line the marker asks for, in the marker's ``color`` (a CSS
colour such as ``#c0392b``; the default is purple). What is shown on hover is the
``label`` (the class name for a watch rule) and the ``props``. A watch rule has no
``color`` setting; a mod that uses the script API sets the colour of its markers
and layers in code.

Choose icon names you would want to see as pictures later, and keep them short and
lowercase. Built-in icons and icons shipped inside mod PBOs are planned; when they
exist this page will list the names the map knows, and a name it does not know
will fall back to the coloured shape, as every name does today.
