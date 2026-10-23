.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Admin map and the dzo-admin mod
===============================

The web interface has a live map of each server: players, vehicles, active
events and markers from other mods. Dragging a player's icon teleports the player, and
the context menu of every icon offers its actions.

Teleporting by drag
-------------------

Press a player's icon (operators and admins only) and drag it. From the moment you press until you confirm or
cancel, the icon does not follow the live updates, so it neither jumps away nor is replaced under the pointer; the
latest report of the player is kept and applied afterwards. The icon sits exactly under the pointer, also when
you move fast, and the drop point is where you let go (clamped to the world). Other icons ignore the pointer
while you drag, so crossing them neither opens their tooltips nor interrupts the drag. A confirmation asks
``Teleport <name>?`` with the coordinates. You can pan and zoom to check the drop point before you answer; the
confirmation stays with the icon. Teleport sends the command and the icon goes to the player's reported position once it is
done; if the game server refuses, or you cancel, the icon goes back to where the player is now, not to
where the drag began. If the player disconnects while you decide, the confirmation closes and nothing is sent.
A plain click on an icon opens its menu as before.

The dzo-admin server mod
------------------------

Everything that changes the game goes through **dzo-admin**, a small server-side
mod (clients do not need it). It reports players and vehicles and carries out
admin actions:

* direct messages and broadcasts (in chat in yellow, red or green, or as an
  on-screen notification),
* teleport (to a map position or to another player),
* heal a player (every damage zone, blood, shock, broken bones, bleeding, diseases, stamina and
  unconsciousness; food and water stay as they are), repair everything a player carries and wears, or kill them,
* spawn items into a player's inventory, hands or on the ground, and items and vehicles at a point of the map,
* repair vehicles (including refuelling and fitting missing parts), flip them upright, or delete them,
* raise a territory flag (fills its timer).

The mod connects *out* to dzo: about once a second it posts what changed and the
results of finished actions to ``dzo serve``, and gets the next actions in the
reply. Nothing connects into the game server. The mod knows persistent
vehicles of the car type (``CarScript``, which includes most modded cars) and, on DayZ 1.30,
motorbikes (``MotorbikeScript``); both can be repaired and deleted. Each vehicle has a kind,
``car``, ``motorbike``, ``bicycle`` or ``helicopter``, shown in the vehicle list, with an icon of its own on the map.
With DayZ Expansion loaded the mod also reports the vehicle's owner (the name on its key chain), and the
vehicles under an Expansion cover (``*_Cover``, stored by Expansion, not in the world as cars) as
``covered`` vehicles (drawn in amber instead of white): they are on the map and in the list with their class, place and owner, but have no
health, fuel or crew and no repair, flip or delete actions. Without Expansion nothing changes.
Bicycles and the helicopters of mods are cars to the game; dzo gives a car the kind whose class list has its class:
``bicycle`` for the DayZBicycle mod (``Old_Bike*``, ``Lady_Bicycle*``, ``MMTArma``, the classes of its event) and
``helicopter`` for the helicopters of DayZ Expansion (``ExpansionMh6*``, ``ExpansionMerlin*``, ``ExpansionUh1h*``,
``ExpansionGyrocopter*``) and the Mosquito of Deer Isle (``RFMosquito``). ``admin.vehicle_kinds`` adds the classes of
other mods. Boats are not reported yet.

Markers have no frame, so that the icon gets all the room, and are told apart by shape and edge: players are round dots
in the colour of their health, with a white ring; vehicles are white icons with a dark edge; events and other markers are
icons in the colour of their layer with a light edge. Zoomed out they shrink, the players least.

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
   admin:                         # every key is optional
     disabled: false              # default; true switches the integration off
     sync_ms: 1000                # defaults: 1000 ms, then seconds below
     players_s: 5
     vehicles_s: 60
     markers_s: 10
     events_s: 30
     allow_spawn: true            # default
     allow_classes: []            # default: none
     deny_classes: ["Land_*"]     # trailing * = prefix
     vehicle_kinds: {bicycle: ["Bike_*"], helicopter: ["MyHeli_*"]}   # default: none

Run ``dzo mod add dzo-admin --instance deerisle --server`` to install it, then
start the instance. dzo recognises the mod by the name ``dzo-admin``.

``dzo instance render`` compares the installed copy of dzo-admin with the build in
``compat.yaml`` next to the packaged mod and prints a warning, with the ``dzo mod update``
to run, when they differ. A package upgrade does not touch the copies in the instances: an
older mod can look for its config in another place and then sits idle without a word.

Every time the instance starts, ``dzo instance render`` writes the mod's
configuration to ``profiles/dzo/admin/config.json`` with a **new token** (the
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
  the server did not report;
* ``vehicle_kinds``: class names (one ``*`` allowed) by kind, ``bicycle`` or ``helicopter``, of other
  mods, on top of the classes dzo knows:
  ``vehicle_kinds: {helicopter: ["MyHeli_*"], bicycle: ["Tandem*"]}``.

Without dzo-admin the web interface cannot show the map or run actions. Other
features that do not need it (status, RCon) are unaffected.

Check that it works: ``dzo player list <instance>`` shows the online players
once the mod has connected, and the instance page shows "dzo-admin connected".

``dzo serve`` logs the connections (``journalctl --user -u dzo-serve``, or its stderr). At start and
after a reload it says per instance whether the token file exists. While running it logs ``mod
connected``, ``mod reconnected``, ``mod restarted`` (the server restarted and sent its hello) and
``mod lost`` (no sync for 10 seconds), and the refusals, once a minute per address and reason: a
token that matches no instance (the line names the instances without a token file), a protocol
mismatch, an unreadable body. Tokens are never logged. An instance that stays silent in this log
has a mod that does not reach the endpoint at all: check ``endpoint`` in the mod's
``$profile:dzo/admin/config.json`` and that the instance really runs with the mod.

Links to a place
----------------

The map page opens on a place when the link has the world coordinates in metres: ``/b/<backend>/i/<instance>/map?x=7500&z=7600&label=Kill``
(``x`` east, ``z`` north, ``label`` optional, up to 80 characters). The place gets a marker with the label as its tooltip
and the map zooms to it. Click the marker for "Remove marker", zoom and copy. The events page
(:doc:`web`) links the position of every event this way, and the link can be shared. A link whose ``x`` and ``z`` are not plain numbers
inside the world is ignored and the map opens as usual.

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

dzo-admin reports effect areas (contaminated zones) with their position and
radius, and events that other mods publish through the marker API. They are shown
as a map layer and available at ``/events`` in the API.

The events of the Central Economy are shown the way mods show theirs: by the class of
an object that belongs to the event. An event places a distinct object in the world, the
wreck of the helicopter, the police car, the tank, a locked container, and a class watch
rule (see below) turns every object of such a class into a marker with an icon. dzo ships
rules for the events of the vanilla missions, so nothing has to be configured, and no
spawn positions or event files are involved: an event that a mission or a mod adds, or
reconfigures through other files, is found as soon as a rule names its class. The
watch covers items, vehicles and buildings (which is what the wrecks, crashes, containers and
cisterns are), and a marker is gone when its object is.

The shipped rules, one layer each (the layer is switched on and off in the map's layer
control):

.. list-table::
   :header-rows: 1

   * - Layer
     - Icon
     - Classes (subclasses match too)
   * - Helicopter crashes
     - helicopter
     - ``Wreck_UH1Y``, ``Wreck_Mi8_Crashed``, ``StaticObj_Wreck_Mi24_TK*``, ``StaticObj_Wreck_Chinook*`` (the ``_DE`` event variants too)
   * - Santa's sleigh
     - gift
     - ``Wreck_SantasSleigh``
   * - Airplane crates
     - archive
     - ``StaticObj_Misc_SupplyBox*_DE``
   * - Trains
     - train
     - ``StaticObj_Wreck_Train_*_DE``
   * - Police cars
     - car
     - the police wrecks of the maps (``Land_Wreck_sed01_aban1_police*`` and its siblings)
   * - Ambulances
     - ambulance
     - ``Land_Wreck_S1023_Medic_Beige_DE``, ``Land_jmc_van_ambulance`` (Deer Isle)
   * - Water trucks
     - tint
     - ``Land_Wreck_Truck01_Aban1_Cistern*``, ``…Aban2_Cistern*`` (the ``_DE`` event variants too)
   * - Military wrecks
     - truck
     - ``StaticObj_Wreck_*_DE``, ``Land_Wreck_*_TK_DE``, ``Land_Wreck_Uaz`` (convoys)
   * - Roadblock wrecks
     - bullhorn
     - ``Land_Wreck_*_DE``, ``Land_wreck_*_DE`` (roadblocks, abandoned vehicles)
   * - Locked containers
     - lock
     - ``ContainerLockedBase``, ``Land_Mil_Container*_DE`` (the military containers of Nasdara)
   * - Fishing boats, military boats
     - ship, anchor
     - ``Land_Boat_Small9_DE``, ``StaticObj_PatrolBoat_Military_DE``
   * - Scientists
     - flask
     - ``StaticObj_FrozenScientist_DE``
   * - Survivor camps
     - fire
     - ``Land_jmc_ce_oven``, the camp fire of the Deer Isle survivor camps
   * - Submarines, Pinkies van, skeletons
     - anchor, birthday-cake, frown-o
     - ``Land_STAG_Submarine*``, ``Land_jmc_van_pinkies``, ``Land_jmc_skeleton_*`` (Deer Isle)
   * - Bonfires, Christmas trees
     - fire, tree
     - ``Bonfire``, ``ChristmasTree``
   * - Brick piles, plank piles
     - cubes
     - ``PileOfBricks``, ``PileOfWoodenPlanks``

The first rule that matches a class wins, so the specific rules stand before the
catch-alls. A rule of your own with the same layer name (in ``admin_map: watch`` of an
instance or ``defaults:``) replaces the shipped one; ``admin_map: {vanilla_events: false}``
turns all of them off for an instance. Vehicles that events spawn are in the vehicle
layer, and infected, animals and the berries and mushrooms are not shown.

Contaminated areas are events of the mod itself: a static or dynamic gas zone (a thrown
M18 smoke grenade) is reported as soon as it is created, with the radius of the zone
(120 m for a dynamic one), and the map draws that circle around the icon.

Event feed
----------

From version 0.5.0 the mod also reports what happens in the game: kills (Expansion AI too, with the
weapon and the distance), deaths and their cause, joins and leaves, base building and raids, combination
locks cut, flags raised and lowered, and stashes buried and dug up. Which fields each kind has is in the
mod's README; what dzo does with them is in :ref:`event-feed` (the API and the file kept on disk),
the events page (:doc:`web`), ``dzo events`` (:doc:`cli`) and the ``killfeed`` of an instance
(:doc:`configuration`). The mod buffers at most 256 events while dzo cannot be reached and sends them with
the next sync; dzo counts each event once. Chat and BattlEye kicks come from RCon, not from the mod. Cutting
a combination lock is deprecated in 1.30 and no item offers it, so ``lock_cut`` appears only with a mod that brings
the action back.

Flags and territories
---------------------

Every territory flag (the flagpole of base building) is a marker on the **Flags** layer, drawn
with the ``flag`` icon. Hovering over it shows how long the flag has left: *goes down in 23d 4h*
is the time the flag's refresher still runs, which is what ``dzo-admin`` reads from the game
(``TerritoryFlag.m_RefresherTimeRemaining``); the game adds one final refresh of 5 days after it,
which is not counted. A flag whose refresher is used up says *goes down now*.

With DayZ Expansion loaded, a flag that is an Expansion territory also gets a thin circle of the
territory's size (``TerritorySize`` of the Expansion territory settings) on the **Territories**
layer. The circle is only an outline: it has no name or tooltip on the map and clicks pass through it.
The name of the territory is the title of the menu of its flag (right-click the flag).
Without Expansion there is no Territories layer.

Operators (and admins) can right-click a flag and choose **Raise flag (fills the timer)**: the flag
goes to the top of its pole and its timer is filled to the full 40 days, as if a player had raised a new flag
(``dzo flag raise``, ``POST /instances/<i>/flags/<id>/raise`` in the API, permission ``flag.raise``).
The flag's id is the id of its marker; it is stable over restarts of the server.

Both layers are visible to the roles ``operator`` and ``admin`` only, since they show where the
bases are; they appear in ``/map/markers`` and ``/layers`` of the API for those roles and carry
the remaining time as ``ttl`` (seconds). This needs dzo-admin 0.4.0; an older mod shows no flags.

Rebuilt houses and wells
------------------------

DayZ 1.30 lets players rebuild some map buildings (houses, and wells where the game has them) part by part with the
rebuilding mechanic. Every such building with at least one part built is a marker on the **Rebuilt houses** layer
(``home`` icon) or the **Rebuilt wells** layer (``tint`` icon), visible to ``operator`` and ``admin`` like the flags. Hovering
over one shows:

* ``worn down in 2h 3m``: the time until decay has worn the first of its parts down to its floor (the marker's ``ttl``, in
  seconds). **Decay does not remove the building or its parts.** A decaying part loses health until it reaches a fraction of
  its full health (its floor) and then stays there, weakened. The time is for the weakest decaying part to get there. It is
  shorter while a sandstorm is active (decay runs 1.5 times as fast, and the marker says ``sandstorm: decaying faster``). It is
  missing when nothing is left to decay: ``decay: worn down`` when every built part is at its floor, and ``decay: off`` when
  the server has ``disableBaseDecay`` set in ``cfggameplay.json``.
* ``parts: 3/12``: built parts of all parts, ``weakest: wall_1 64%``: the built part with the lowest health, and ``floor: 40%``:
  the health at which its decay stops.
* ``locks: 2 code locks, 1 combination lock`` when the building carries any.
* for a well, ``water: drinkable`` or ``water: none yet``: the brick well only gives water once its roof is built.

A building with no part built is not shown. The first rebuildable well of the game is ``Land_WaterWell_Brick``, the houses
are the ``Land_House_*`` types. This needs dzo-admin 0.6.0; an older mod shows nothing on these layers.

Tunnel entrances
~~~~~~~~~~~~~~~~

The tunnel entrances of the irrigation tunnels (Nasdara) are holes a rope ladder can be built on. The **Tunnel entrances**
layer marks every one of them for every role, in a plain brown, and in green with ``ladder: usable`` once a ladder is built on
it (``ladder: none`` otherwise). Needs dzo-admin 0.6.0.

Sandstorms
----------

While a sandstorm is active (DayZ 1.30, on maps that have them), the **Sandstorm** layer shows where it is as a thin
sand-coloured wash under everything else, and one marker at its centre. The wash has two strengths: a light one where
the storm is felt at all and a darker one where it is strong. It is sampled from the game on a grid of about 320 m, so its
edge is approximate and blocky; it takes no clicks or hovers, so it never gets in the way of players and vehicles. The
centre marker has an arrow that points the way the storm moves; hovering over it shows ``heading`` (a compass direction
with the degrees), ``speed`` (km/h), ``strength`` (percent) and ``reaches its end in 25m``, the time until the storm arrives
at the end of its path at its current speed (``arrived`` once it has). The layer is visible to every role and is empty
when there is no storm. It needs dzo-admin 0.6.0; maps without sandstorms never show it.

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
       # all keys: layer and classes are required
       - {layer: campfire,  classes: ["FireplaceBase"], icon: fire, color: "#e67e22", max: 200,
          label: "Campfire", visibility: viewer, cluster: 0}
       - {layer: ufo_crash, classes: ["UFO_Crash_Site*"], icon: rocket, color: "#8e44ad", label: "UFO"}
       - {layer: ai_convoy, classes: ["eAIBase"], icon: android, color: red, cluster: 50}

``icon`` and ``color`` give the markers of the layer their look, see :ref:`map-icons`.
``visibility`` (``viewer``, ``moderator``, ``operator`` or ``admin``) is the lowest role that sees the
markers of the layer; without it they are admin-only. The shipped event rules use ``viewer``.
Watch rules can also be set once for all instances, under ``defaults:`` in ``site.yaml``
(``defaults: admin_map: watch: [...]``). They are merged with the rules of an instance **by layer
name, and the instance wins**: a rule of the instance replaces the default rule of the same layer
as a whole (its ``classes``, ``icon``, ``color`` and the rest are not mixed with the default's),
default rules for other layers stay, and the instance's own layers are added after them. There is
no way to remove a default rule for one instance; give the layer an instance rule that matches
nothing you care about (for example ``classes: ["NoSuchClass"]``) if you have to.
``dzo instance show <name>`` prints the merged list.

Rules apply to items (including fireplaces) and cars; buildings, animals and
players cannot be watched this way yet. Subclasses match too, and one ``*`` in a class name stands for any text (``Wreck_*``, ``*_DE``, ``StaticObj_Wreck_*_DE``).
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

Write ``$profile:dzo/admin/markers/<layer>.json``, either a JSON list of
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
and about nine hundred more, plus ``helicopter`` (Fork Awesome has none; dzo ships the
Font Awesome Free one, CC BY 4.0). Browse the gallery on the Fork Awesome site, or list
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
