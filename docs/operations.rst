.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Running servers
===============

Start, stop, status
-------------------

.. code-block:: sh

   dzo start deerisle
   dzo stop deerisle            # stops and stays stopped
   dzo status [deerisle]        # build, mods, running, uptime, players, pending updates
   dzo logs deerisle -f         # server console from journald

``dzo start`` returns when the server answers Steam queries, not just when the
container runs. Under the hood these are plain systemd user units
(``dzo-deerisle.service``), so ``systemctl --user`` and ``journalctl --user``
work too.

.. _stopping-the-server:

Stopping the server
-------------------

Every stop goes through the unit: ``dzo stop``, every restart, an update, a reboot.
``stop.method`` in ``instance.yaml`` (or the site defaults) chooses how:

``rcon`` (default)
   ``dzo instance shutdown`` sends ``#shutdown`` over RCon and waits up to
   ``stop.timeout`` (30 s) for the process to exit; then podman stops the container
   (``SIGTERM``, ``SIGKILL`` after 120 s). The restart is immediate.
``kill``
   No request: ``SIGTERM`` and, one second later, ``SIGKILL``.

**Why stdin matters.** The experimental (diag) builds of DayZ raise an assertion at
shutdown: ``Assertion failed ... enf_scriptmodule.cpp ... Script is leaking! Check
log!`` followed by ``(A)bort (R)etry (I)gnore``. Vanilla leaks a script instance
(``BunkerBroadcastManager``) at every shutdown, so this happens without any mod. The
build waits for the answer on standard input. With stdin at end-of-file (a container, a
systemd service, ``nohup``) the read never blocks and never ends: the process spins at
100 % of a core after "Destroying game" and has to be killed. Seen on 1.30
experimental (1.30.164014); the stable 1.29 server shows no such prompt. Other builds
or modded maps may raise assertions of their own at shutdown.

dzo therefore starts every server with a stdin that holds the answer ``i`` (Ignore,
``stop.ignore_asserts``, default on). Measured on 1.30 experimental, a stop then takes
3 seconds (``SIGTERM``) to 13 seconds (RCon ``#shutdown``) instead of hanging. A server
that is stuck anyway is killed after the timeouts, and the unit does not stay "failed"
after a clean stop.

Graceful restarts
-----------------

.. code-block:: sh

   dzo restart deerisle --minutes 30 --lock 3 --delay 3 --text "Restart"
   dzo restart deerisle --now
   dzo restart deerisle --cancel

A graceful restart

#. announces the restart in game on a countdown,
#. locks the server a few minutes before the end,
#. kicks the remaining players (several passes, then everyone),
#. waits the delay and restarts the service.

dzo talks to the server over its own BattlEye RCon client. The restart itself
goes through the unit's stop (see :ref:`stopping-the-server`). If RCon does not
answer, the server is restarted through systemd right away.

The restart runs as its own systemd unit, so it continues if your SSH session
ends. When a maintenance restart and an update restart fall together, they are
merged into one.

Scheduled restarts
------------------

``restarts.schedule`` in ``instance.yaml`` takes systemd calendar expressions:

.. code-block:: yaml

   restarts:
     schedule: ["*-*-* 02,06,10,14,18,22:00"]
     announce: {minutes: 30, lock: 3, delay: 3}

``dzo instance apply <name>`` turns them into ``dzo-restart-<name>.timer``.
One-off restarts ("today at 18:30") can be scheduled in the web interface; they
also become systemd timers.

Before any planned restart dzo renders the instance in a dry run while the
server is still running. If that fails, the restart is skipped, the server keeps
running and you are alerted. A broken change in the site repository therefore
never takes a server down.

Health checks and crash loops
-----------------------------

Every server has two checks, run inside the container:

startup
   waits until the server answers Steam queries. Long mission loads do not count
   as failures until ``health.startup_timeout``.
liveness
   probes the running server. After ``interval`` × ``retries`` without an answer
   (default 5 minutes) the container is killed and systemd restarts it.

Three brakes stop endless restarts:

* the pre-restart dry-run render described above;
* a failed render during a crash restart marks the instance ``failed-render``.
  It is not tried again until its inputs change or you run
  ``dzo instance ack-failure <name>``;
* ``restart_limit``: after ``burst`` starts within ``interval`` the unit stays
  failed, Icinga turns critical and Discord reports the crash loop.

Console and RCon
----------------

.. code-block:: sh

   dzo rcon exec --instance deerisle players        # one command, the instance's own port and password
   dzo rcon exec --addr 127.0.0.1:2303 --password <password> players

An interactive console and ``dzo rcon rotate`` (a new password) are not built yet; the
password lives in ``<paths.secrets>/rcon/<instance>``.

The RCon password is generated per instance and bound to localhost where the
network mode allows it.

Logs
----

* Server console: journald (``dzo logs <name>``).
* Profile logs (``*.RPT``, ``script*.log``, ``*.ADM``, crash dumps) are rotated
  into ``profiles/logs/<timestamp>/`` before each start.
* After a crash dzo writes a crash summary (the tails of the RPT, script log and
  ``error.log``) to the journal and to Discord.

Instances
---------

.. code-block:: sh

   dzo instance create <name>
   dzo instance apply <name>          # regenerate units and timers after config changes
   dzo instance clone <old> <new>     # copy the data into a new instance
   dzo instance remove <name>         # asks whether to keep its snapshots
   dzo wipe <name>                    # wipe the world, after confirmation and a snapshot

Instances cannot be renamed in place, because the name is part of unit names,
paths and the database. Clone to the new name and remove the old instance.
