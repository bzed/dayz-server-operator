.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

.. _container:

In a container (the appliance)
==============================

dzo also ships as a container image, ``ghcr.io/bzed/dzo``: Debian 13 (trixie)
with systemd as PID 1, the ``dzo`` package and ``steamcmd`` installed. This page
calls it the *appliance*.

The appliance does not run the game servers itself. It talks to the rootless
podman of a host user through that user's podman socket, and every game server
is an ordinary container of that podman, next to the appliance. That is what
makes the appliance replaceable: it can be restarted, updated or thrown away
without stopping a server.

When to use it
--------------

Use the appliance when the host's distribution is not one of the
:doc:`supported ones <installation>`, or when the host should run nothing but
containers and keep its base system untouched. On Debian 13 and the other
supported distributions the native package is the tested and simpler path; the
commands and the behaviour inside the appliance are the same either way.

Requirements on the host
------------------------

Everything here is about the **host**, not about the appliance:

* **Linux on x86_64** with **systemd as init**. An instance's container health
  checks are systemd units of the host's podman; without systemd they never run
  and no instance would ever become ready.
* **Rootless podman 5.4 or newer** for the host user, with subordinate UID and
  GID ranges in ``/etc/subuid`` and ``/etc/subgid``, as every rootless podman
  installation needs. A newer podman, 6 included, is fine. Check the *server*
  behind the socket, not the client: ``podman version --format
  '{{.Server.Version}}'``.
* **The user's podman socket**, started at boot:
  ``systemctl --user enable --now podman.socket``. It is the appliance's only
  way into the host's podman.
* **Lingering for that user**: ``loginctl enable-linger <user>``. The user
  manager - and with it the socket and the appliance - then runs at boot without
  a login.
* **systemd cgroups**: ``podman info --format '{{.Host.CgroupManager}}'`` must
  print ``systemd``, on a cgroup v2 host. The memory and CPU limits of the game
  containers need the user's cgroup delegation, which current distributions give
  by default.
* **btrfs** for ``paths.instances`` and ``paths.snapshots``, on the same
  filesystem, if you want the snapshot backups; exactly as on a native install
  (see :doc:`installation`). Without btrfs dzo runs, but takes no snapshot
  backups.
* **Disk space** for the server builds, mods and snapshots, in the host
  directory that becomes the appliance's data directory.

The image is built for amd64 only, like the DayZ server itself.

Security: use a dedicated host user
-----------------------------------

Mounting the user's podman socket into the appliance gives everything inside it
full control over that user's containers: start, stop, remove, inspect and
``exec`` into any of them, and through their bind mounts read and write the
files they see. That is dzo's job, but it also means that anything dzo runs -
the hooks of your site repository, a Steam download, a command you type with the
wrapper - acts with those rights.

Run the appliance as a dedicated host user that owns the servers and nothing
else, not as your login account, and do not put containers into that user's
podman that you would not hand dzo. The quadlet adds no capabilities and no
devices; its only addition is the user mapping that lets systemd be PID 1 inside
while the data directory stays yours.

Running it
----------

The host side is in the source repository's ``contrib/appliance/``: the quadlet
(``dzo.container``), a small wrapper (``dzo``) and the image's ``Containerfile``
below ``images/appliance/``. Install the first two for the host user that runs
the servers:

.. code-block:: sh

   install -Dm0644 contrib/appliance/dzo.container ~/.config/containers/systemd/dzo.container
   install -Dm0755 contrib/appliance/dzo ~/.local/bin/dzo
   systemctl --user daemon-reload
   systemctl --user enable --now dzo.service

The quadlet starts ``ghcr.io/bzed/dzo:latest`` as the container ``dzo`` with:

* systemd mode and the image's ``USER 0``, so systemd is PID 1;
  ``UserNS=keep-id:uid=1000,gid=1000`` maps the host user to the appliance's
  ``dayz`` (UID 1000) and back on the mounted data directory. Do **not** add a
  ``User=`` line: ``keep-id`` would then run PID 1 as the unprivileged user and
  systemd cannot start at all.
* host networking, so ``dzo serve`` (the API and the ``dzo-admin`` endpoint),
  ``dzo web`` and the exporter listen on the host's loopback as on a native
  install, and the game servers are reachable as before.
* the user's socket at ``/run/host-podman.sock``. The image's podman is remote
  (``CONTAINER_HOST``), so every podman call in dzo - image builds, ``inspect``,
  the debug shell, the instance supervisor - reaches the host.
* the data directory at the same absolute path on both sides (see
  `The same path on both sides`_).
* named volumes for ``/etc/dzo`` (the configuration), ``/var/lib/dzo`` (the
  ``dayz`` home: the Steam session, the user's units, caches) and
  ``/var/log/journal`` (so ``dzo logs`` history survives a replaced container).
* ``StopTimeout=90`` and ``TimeoutStopSec=150``, the time the servers get to
  stop when the appliance stops, and ``Restart=on-failure``, so a crashed or
  killed appliance comes back without a login. See
  `Stopping and starting the appliance`_.

The wrapper runs dzo inside the appliance as ``dayz``, with the user session's
environment and a terminal when there is one, so the command line is the one of
a native install:

.. code-block:: sh

   dzo version
   dzo setup
   dzo steam login --user <steam account>

``DZO_CONTAINER`` names another appliance container (the default is ``dzo``).
Without the wrapper, the same call is ``podman exec -it -u dayz -e
XDG_RUNTIME_DIR=/run/user/1000 dzo dzo <command>``.

The configuration is ``/etc/dzo/config.yaml`` inside the appliance, in the
``dzo-etc`` volume. The image seeds it with ``podman.mode: remote`` and
``paths.data: /srv/dzo``; the volume keeps your edits, and so do image updates.
Write a new version from the host with ``podman cp``, which lands in the volume:

.. code-block:: sh

   podman cp config.yaml dzo:/etc/dzo/config.yaml
   dzo setup                     # re-checks the paths and the directories

First run
---------

``dzo setup`` does what it does on a native host - directories, the two runtime
images, the site checkout, the weekly image refresh timer - and adds the three
checks of `The three remote checks`_. Then follow :doc:`quickstart`: the Steam
login, the product install, the site repository, ``dzo instance apply`` and
``dzo start``.

.. _container-same-path:

The same path on both sides
---------------------------

The host's podman resolves every bind mount, and the game containers are its
containers. Every path dzo mounts into a game container must therefore exist
under the **same absolute path** on the host and in the appliance. The quadlet
does that for the data directory (``Volume=/srv/dzo:/srv/dzo``), and everything
dzo puts below it - server builds, mods, the instance directories, missions,
profiles - is visible on both sides.

To use another data directory, change it in both places at once: the quadlet's
``Volume=`` source and ``paths.data`` in the appliance's ``config.yaml``. Then
``systemctl --user daemon-reload`` and ``systemctl --user restart dzo.service``;
the restart stops the servers. The rule also applies to directories outside
``paths.data``: a site checkout, download cache or secrets directory that is not
below it must be mounted at its own path too. The boot test therefore runs below
``<paths.data>/boottest`` in remote mode, where the host can see it.

.. _container-instances:

How an instance runs
--------------------

With ``podman.mode: remote``, which the image sets, the instances are not
quadlets. ``dzo units sync`` writes ``dzo-<name>.service`` as a plain user unit
of the same name, ``Type=notify``, whose ``ExecStart`` is a hidden supervisor
followed by the container's ``podman run`` arguments:

.. code-block:: ini

   [Service]
   Type=notify
   ExecStart=/usr/bin/dzo instance supervise deerisle -- run --name dzo-deerisle --replace --rm …

The unit carries the arguments so that it holds the container it was rendered
for, also across a crash restart - the way a quadlet's paths hold the
generations. ``dzo instance supervise`` is internal; there is nothing to run by
hand. The supervisor

* starts the container through the socket, or attaches to it when it is already
  running, after a crash of the supervisor or a new appliance container,
* passes the container's output to the unit's journal, so ``dzo logs`` shows the
  console as on a native install,
* tells systemd that the unit is ready once the container's health check passes,
  which is what a quadlet's ``Notify=healthy`` does,
* stops the container with the instance's stop timeout on ``SIGTERM`` and exits
  with the container's exit code, so ``SuccessExitStatus=255`` and the crash
  summary keep their meaning, and
* attaches again when the connection to the socket breaks while the container
  keeps running, instead of exiting, which would count as a crash against the
  unit's start limit.

``dzo units sync`` **enables** the units; it does not start them. A change of
``podman.mode`` never stops a running server: the new kind of unit takes effect
at its next restart. The same holds for a pending update - a running server
keeps its unit, and with it its generations, until it restarts.

The health check of a game container runs a copy of the dzo binary. The host's
podman cannot see the appliance's ``/usr/bin/dzo``, so ``dzo units sync`` copies
the ``binary`` to ``<paths.data>/bin/dzo`` - atomically, and only when the
content changed - and the containers mount that copy. A running server keeps the
copy it started with, as it keeps ``/usr/bin/dzo`` on a native install; its next
restart uses the new one. ``dzo test boot`` runs its container below
``<paths.data>/boottest`` for the same reason.

What differs from a native install
----------------------------------

* **The servers are the host user's containers.** ``podman ps`` on the host
  lists them as ``dzo-<name>``; they are removed when stopped (``--rm``). The
  appliance has no containers of its own.
* **Hooks and site scripts run inside the appliance**, with the image's tools:
  the podman client, steamcmd, git, patch and the usual Debian utilities. They
  cannot use a program installed on the host, and only the host paths the
  quadlet mounts exist for them.
* **The console is in the unit's journal while a supervisor is attached.** When
  none is attached - between a supervisor's death and its restart - the server
  keeps running, but its output is only in the host's ``podman logs
  dzo-<name>``.
* **A restart of the host's podman service ends the console in the appliance.**
  ``systemctl --user restart podman.service`` (a podman update, for instance)
  leaves the server running, but the appliance's attached client gets no more
  output: ``dzo logs`` stops there and ``podman logs dzo-<name>`` on the host
  continues, until the instance's next restart. Exit codes and stops still work,
  so ``dzo status``, ``dzo stop`` and the rest behave normally. The service does
  not stop for idleness while an attach is open, so this takes an explicit
  restart or an upgrade.
* **Stopping the appliance stops the servers**, cleanly and one instance after
  another (see below). A host reboot would end them anyway, and an update of dzo
  inside the appliance does not need a stop.
* **``dzo test boot`` and ``dzo shell``** run their containers on the host too,
  with paths below the data directory.

.. _container-stopping:

Stopping and starting the appliance
-----------------------------------

``systemctl --user stop dzo.service`` or ``podman stop dzo`` stops the
appliance. Inside it systemd stops the instance units first, and each supervisor
stops its game server with the instance's stop timeout; then the appliance shuts
down. Both values in the quadlet must outlast that: every instance gets
``stop.timeout`` (30 s by default) plus the 30 s its supervised unit adds, and
the instances stop one after another. The shipped ``StopTimeout=90`` and
``TimeoutStopSec=150`` cover one instance with the defaults; raise both when you
raise ``stop.timeout`` or run several instances.

The quadlet starts the container with ``--rm``, so ``podman stop dzo`` and
``podman kill dzo`` remove the appliance container and ``podman start dzo`` has
nothing to start. Use the unit instead:

.. code-block:: sh

   systemctl --user start dzo.service

``Restart=on-failure`` brings a crashed or killed appliance back by itself, with
systemd's start limit as the brake; an appliance that exited cleanly, as after
``podman stop``, stays stopped.

Updating
--------

dzo is updated **inside** the running appliance, as on a Debian host, because
the image has dzo's apt repository configured:

.. code-block:: sh

   podman exec -it dzo apt update
   podman exec -it dzo apt upgrade

The package's post-install steps rewrite the units and restart the services that
run from the binary (``dzo serve``, ``dzo web``, the exporter), but never a game
server. A running server keeps the old binary, which is mounted into its
container; the next restart, scheduled or ``dzo restart <name>``, uses the new
one. No server has to stop for an update.

A new image is only needed for a fresh Debian base (CI rebuilds it weekly) or a
dzo version that apt does not offer. It replaces the whole container:

.. code-block:: sh

   podman pull ghcr.io/bzed/dzo:latest
   systemctl --user restart dzo.service

That stops the appliance and with it the servers; see
`Stopping and starting the appliance`_. The named volumes keep ``/etc/dzo``, the
``dayz`` home and the journal, and the data directory lives on the host.

.. _container-remote-checks:

The three remote checks
-----------------------

In ``podman.mode: remote``, ``dzo setup`` adds three lines about the other side
of ``CONTAINER_HOST``. Each has its usual failure and what to do about it:

``ok podman server 5.8.6``
   The host's podman is new enough. ``error the host's podman 4.9.3 is too old:
   dzo needs podman 5.4 or newer`` means the server behind the socket is old:
   upgrade podman on the host. ``error the host's podman server version: …``
   means the socket did not answer: check ``systemctl --user status
   podman.socket``, the socket mount and ``CONTAINER_HOST``.

``ok paths.data /srv/dzo is mounted at the same path on the host``
   The check writes a random file below ``paths.data``, starts the runtime image
   on the **host** with ``paths.data`` mounted read-only and reads the file
   back. ``error paths.data … is not mounted at the same path on the host``
   means the appliance and the host see different directories: the quadlet's
   ``Volume=`` is missing, or its source and ``paths.data`` are not the same
   absolute path, or one of them is a symlink that resolves differently. Make
   them the same path. ``error the runtime image … is missing`` means the image
   was not built; ``dzo setup --images-only`` builds it.

``ok the host's podman uses systemd cgroups``
   The health checks are systemd units on the host; with the ``cgroupfs`` driver
   they never run, and no instance would ever become ready.
   ``error the host's podman does not use systemd: …`` means the host user's
   podman uses ``cgroupfs``: set ``cgroup_manager = "systemd"`` in the user's
   ``~/.config/containers/containers.conf`` (or in
   ``/etc/containers/containers.conf``), make sure the host boots with cgroup v2
   (``/sys/fs/cgroup/cgroup.controllers`` exists) and that the user's cgroups are
   delegated, then restart the socket.

A dry run (``dzo setup --dry-run``) runs the first and the third check and says
``info a dry run cannot check whether paths.data … is mounted at the same path
on the host`` instead of starting a container.

If a server does not start, look at its unit from the appliance:

.. code-block:: sh

   dzo status
   dzo logs <name>          # the supervisor's errors are in the journal too

``podman socket … unreachable`` means the socket was not there when the unit
started: enable it on the host (``systemctl --user enable --now
podman.socket``) and start the instance again.

Not verified yet
----------------

CI builds the image from the ``.deb`` of the same run, boots it and checks
systemd, ``CONTAINER_HOST``, ``podman ps`` through the host's socket and ``dzo
setup --dry-run``. A real DayZ server and a client through the appliance is
manual, like the rest of the release checklist: see
:ref:`release-checklist-container-appliance`. The supervisor itself runs
against a real podman socket with ``make test-appliance`` (see
:doc:`development`).
