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

   dzo restart deerisle --minutes 30 --lock 7 --kick 3 --text "Restart"
   dzo restart deerisle --now
   dzo restart deerisle --cancel

A graceful restart runs on a timeline. Every number is minutes before the restart:

``minutes``
   the countdown starts (30 in the example)
``lock``
   the server is locked against new logins (7)
``kick``
   the players are kicked (3). The countdown ends here; the service is stopped
   when the restart time is reached.

The countdown: the first message when it starts, then at 20 and 15 minutes
left over RCon (``say``), and from 10 minutes left on once a minute as an on-screen notification, plus
"<text> NOW" 10 seconds before the kick. All times count down to the kick. With 30/7/3 the first message reads "in 27 minutes".
Each message is ``<text> in <time>``; the lock is announced as well (in the same notification when
one is due then). The kick tries up to three passes, then kicks everyone. The countdown is dzo's own; the server's
built-in countdown and shutdown of ``messages.xml`` are never used (see ``messages`` in
:doc:`configuration`).

How the countdown reaches the players is ``announce.ways`` (in ``restarts.announce`` and in ``updates.restart_announce``), a list of rules:

.. code-block:: yaml

   restarts:
     announce:
       minutes: 30
       lock: 7
       kick: 3
       ways:
         - {way: rcon, at: [30m, 20m, 15m]}                              # the default, above 10 minutes
         - {within: 10m, way: notification, every: 1m, now: 10s}         # the default, the last 10 minutes
         # or, with DayZ Expansion: {within: 10m, way: expansion, type: baguette, every: 1m, now: 10s}

``way``
   ``rcon`` (BattlEye ``say``, works without ``dzo-admin``), ``chat`` (a red chat line through ``dzo-admin``),
   ``notification`` (the on-screen notification of the game) or ``expansion`` (an Expansion notification;
   ``type`` is ``toast``, ``baguette``, ``activity``, ``killfeed``, ``market`` or ``garage``).
``within``
   the rule applies while at most this much is left until the kick. Without it, the rule covers the whole
   countdown, above the other rules' ``within``.
``every`` or ``at``
   the interval, or the exact times left (not both). On screen (``notification``, ``expansion``) the
   announcements must be at least a minute apart.
``now``
   one more announcement "<text> NOW" this long before the kick; only one rule may set it.

Notifications go through ``dzo-admin`` and the API of ``dzo serve``: the restart needs an API token, ``$DZO_TOKEN`` or
the file ``announce.token`` in the secrets directory. ``dzo instance apply`` (and ``dzo units sync``) creates that file with a token of the ``announcer`` role (it can only read the instance's status and broadcast) whenever an instance runs ``dzo-admin`` and the file is missing, or its token was revoked or has expired (tokens last a year, so run ``apply`` again by then). If the mod is not
connected, the API or the token is missing, or the message is refused, the same text goes out over RCon ``say`` and the
restart continues on time. ``expansion`` becomes a plain notification when the mod does not report Expansion. A ``dzo-admin`` from before these ways (it runs until its instance is restarted onto the new mod) reports no features: the on-screen notifications still arrive, as plain notifications of ten seconds titled "Server", and the title and duration of a rule are ignored.

dzo talks to the server over its own BattlEye RCon client. The restart itself
goes through the unit's stop (see :ref:`stopping-the-server`). If RCon does not
answer, the server is restarted through systemd right away. If a RCon command fails in the middle of the countdown (a timeout, a
dropped connection), dzo logs it, skips that command and connects again for the next one; a failed
lock or kick is tried once more on the new connection. The server is stopped at the time asked for
in any case.

The restart runs as its own systemd unit, so it continues if your SSH session
ends. When a maintenance restart and an update restart fall together, they are
merged into one.

.. _graceful-stop:

Stopping with a countdown
-------------------------

``dzo stop`` stops at once. To be nice to the players, give it the countdown of a restart:

.. code-block:: sh

   dzo stop deerisle --graceful                   # the restarts.announce numbers
   dzo stop deerisle --minutes 10 --lock 3 --kick 1 --text "Server maintenance"
   dzo stop deerisle --cancel                     # end a countdown, unlock the server

Any of ``--graceful``, ``--minutes``, ``--lock``, ``--kick`` and ``--text`` turns the countdown on;
the numbers you leave out come from the instance's ``restarts.announce``. The timeline, the
messages and the RCon handling are those of the restart above, with the messages saying
"Server shutdown" (or your ``--text``) instead of "restart". When the time is up the service is
stopped and stays stopped. An instance that is not running is not announced to, and without RCon
the stop goes ahead at once. ``dzo stop <name> --cancel`` (and ``dzo restart <name> --cancel``)
ends either countdown.

The countdown runs in the command itself (only scheduled restarts run as a unit of their own), so
it ends with your session. For a long one, run it inside ``tmux``, or as
``systemd-run --user dzo stop deerisle --graceful``.

Scheduled restarts
------------------

``restarts.schedule`` in ``instance.yaml`` lists the times of day the server restarts, as ``HH:MM``, or
``Mon,Thu HH:MM`` for some weekdays only (Mon, Tue, Wed, Thu, Fri, Sat, Sun). ``*:MM`` restarts every
hour at that minute, and ``hourly`` is short for ``*:00``:

.. code-block:: yaml

   restarts:
     schedule: ["02:00", "06:00", "10:00", "14:00", "18:00", "22:00"]
     announce: {minutes: 30, lock: 7, kick: 3}

``dzo instance apply <name>`` turns them into ``dzo-restart-<name>.timer``. The times are the
restart itself: the timer fires ``announce.minutes`` earlier, when the countdown starts (04:00 with
``minutes: 30`` fires at 03:30). Give every server its own times, so they do not go down together.
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

When the render or a ``pre_start`` hook fails in ``ExecStartPre``, dzo prints a
banner of ``!!!`` lines with the instance name and the error to stderr. Under
systemd the lines have the error priority, so ``journalctl -u <unit>`` and
``systemctl status`` show them prominently.

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

.. code-block:: sh

   dzo rcon console deerisle      # one command per line; server messages (connects, chat, kicks) as they come
   dzo rcon rotate deerisle --restart   # a new password; the server reads it at its start

The console reconnects when the server restarts. A new RCon password takes effect at the next start, so
``rotate`` refuses a running instance unless ``--restart`` is given. The password lives in
``<paths.secrets>/rcon/<instance>``.

The RCon password is generated per instance. RCon is reachable on the host only (127.0.0.1)
unless ``rcon_bind`` in ``instance.yaml`` or in the site defaults says otherwise, see
:doc:`configuration`. To let external RCon tools connect, set ``rcon_bind: 0.0.0.0`` (or one
address of the host) and allow the RCon port in your firewall for those tools only.

Logs
----

* Server console: journald (``dzo logs <name>``).
* Profile logs are rotated into an archive outside the instance, see below.
* After an unclean exit the unit's ``ExecStopPost`` runs ``dzo logs crash-summary``: the last 25 lines
  of the newest ``error.log``, script log, crash log and RPT go to the journal and to Discord. It
  runs before the next start rotates the files.

Profile log rotation
~~~~~~~~~~~~~~~~~~~~

The profiles directory collects logs from the server, BattlEye and mods. ``logs:`` in ``instance.yaml`` (or
the site defaults) is a list of regular expressions, matched against the path below ``profiles/``:

.. code-block:: yaml

   logs:
     rotate:                 # added to the site's list; the default is the server's own files
       - {match: '^DayZServer(_x64)?_.*\.RPT$'}
       - {match: '^script_.*\.log$'}
       - {match: '\.mdmp$', max_age: 14d}
       - {match: '^VPPAdminTools/Logs/.*\.txt$'}
       - {match: '^battleye/.*\.log$', keep_newest: 2}
     keep_newest: 1          # per rule, the newest matches stay in place
     min_age: 10m            # a file changed more recently is never touched
     archive: {compress: gzip, max_age: 90d, max_size: 50GiB}

Per rule the newest ``keep_newest`` files stay (the server may still write them) and every older match is
moved to ``<paths.logs>/<instance>/<date>/<path>.gz``: copied through gzip, synced, and only then removed.
Only regular files below ``profiles/`` are touched, links are not followed, and ``serverDZ.cfg``, the
BattlEye configs, ``dzo-admin/`` and every ``.json``/``.xml`` file are never moved, even if a rule matches
them. The default rules are the ``*.RPT``, ``script_*.log``, ``crash_*.log``, ``error_*.log``, ``warning_*.log`` and ``*.ADM`` of the server and
``*.mdmp`` crash dumps (14 days).

It runs before every start (``ExecStartPre``, a failure does not stop the start) and hourly for all
instances (``dzo-logs.timer``), which also applies the archive's ``max_age`` and ``max_size`` (oldest
first). ``dzo logs rotate <name> --dry-run`` lists what it would move and the large files no rule matches
(new mod logs show up there, except those of ``dzo_logz`` in ``dzo/logz/logs/``); ``dzo logs archive <name> [--since 7d] [--match <regexp>]`` lists the archive.

Instances
---------

.. code-block:: sh

   dzo instance create <name>
   dzo instance apply <name>          # regenerate units and timers after config changes
   dzo instance clone <old> <new>     # copy the data into a new instance of the site repository
   dzo instance remove <name>         # stops it, removes units and data, asks before
   dzo wipe <name>                    # wipe the world, after confirmation and a snapshot

``clone`` needs the new instance in the site repository (its own ports and settings) and the same map. It
copies the old instance's directory with a btrfs snapshot, gives the copy its own profiles, keys and
RCon password, and keeps the server build the old one runs. A running source needs ``--force`` (the copy
is crash-consistent). ``remove`` deletes the data directory and, unless ``--keep-snapshots``, the
snapshots, and removes the instance's units; then delete ``instances/<name>/`` from the site repository.

Development: ``dzo instance render`` (render only, nothing is started) and ``dzo shell`` (a container with
the instance's mounts and a shell, without starting the server) are the "development mode"; a server is
started only by ``dzo start``.

Instances cannot be renamed in place, because the name is part of unit names,
paths and the database. Clone to the new name and remove the old instance.
