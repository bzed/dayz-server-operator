.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Backups and restores
====================

Every instance lives in its own btrfs subvolume. A backup is a read-only btrfs
snapshot of that subvolume: it takes milliseconds and costs almost no space
until files change. A snapshot of a running server is crash-consistent, like a
power cut; the snapshots taken before a change are taken with the server
stopped.

dzo creates the instance directory as a subvolume on its first
``dzo instance render``, as the unprivileged service user, and everything below
works without root. ``paths.instances`` and ``paths.snapshots`` must be on the
same btrfs filesystem; on any other filesystem an instance runs, but
``dzo backup`` refuses it and says why. ``dzo setup`` reports whether the
filesystem is mounted with ``user_subvol_rm_allowed``, which makes deleting
snapshots instant; without it dzo clears the read-only flag and removes the tree
file by file, which is slower but needs no privileges.

Snapshots live in ``<paths.snapshots>/<instance>/<time>-<reason>``, and
``index.json`` next to them records each one (reason, time, state, pinned). dzo
only ever acts on snapshots in that index.

When snapshots are taken
------------------------

.. list-table::
   :header-rows: 1

   * - Reason
     - When
     - Default
   * - ``update``
     - before ``dzo instance upgrade`` switches to a new server build
     - on
   * - ``mod_update``
     - before new mod versions are applied, including automatic updates
     - on
   * - ``mission_update``
     - before a new pristine mission is applied
     - on
   * - ``config_change``
     - before site repository changes are applied
     - on
   * - ``render``
     - before every start, including crash restarts
     - off
   * - ``scheduled``
     - periodically while running (``backup.schedule``)
     - off
   * - ``manual``
     - ``dzo backup create <name>``
     - –
   * - ``destructive``
     - before ``mission reinit``, ``wipe`` and restores
     - always

If a snapshot fails, the change is not applied, the server starts unchanged and
you are alerted.

Retention
---------

The backup policy is the ``backup:`` block in the site repository. Put it in
``instances/<name>/instance.yaml`` for one instance, or under ``defaults:`` in
``site.yaml`` for every instance that does not set its own (see
:doc:`configuration`). A key the instance leaves unset is taken from the
defaults, so the instance only needs the values that differ (``keep_by_reason``
is taken as a whole, not per reason). Keys set in neither place get the
built-in values. Commit the change
and run ``dzo site pull`` on the host; ``dzo site validate`` checks the file.

.. code-block:: yaml

   # instances/<name>/instance.yaml
   backup:
     before: [update, mod_update, mission_update, config_change]
     schedule: null            # e.g. "*-*-* 00/6:00"
     keep: 20
     keep_by_reason: {mod_update: 8, update: 10, scheduled: 12}
     max_age: 30d
     min_keep: 3               # never fewer, regardless of age
     min_free_bytes: 50GiB     # prune the oldest first to keep this free

The same block as a default for all instances:

.. code-block:: yaml

   # site.yaml
   defaults:
     backup:
       keep: 20
       max_age: 30d

Old snapshots are pruned after each successful snapshot, by the daily
``dzo-backup-prune.timer``, and on demand:

.. code-block:: sh

   dzo backup prune [<instance>] --dry-run
   dzo backup pin <instance> <id>     # never pruned automatically
   dzo backup unpin <instance> <id>

dzo only deletes snapshots it has recorded. Unknown directories in the snapshot
folder are reported, not deleted, unless you pass ``--delete-orphans`` (or
``--adopt-orphans`` to enter them into the index). ``keep`` is the total per
instance, pinned snapshots included; the newest snapshot and ``min_keep``
snapshots are never removed, whatever the other limits say. A snapshot that is
being restored from is protected for the duration of the restore.

Listing and comparing
---------------------

.. code-block:: sh

   dzo backup create <instance> [--reason manual]
   dzo backup list [<instance>]
   dzo backup diff <instance> <id> [<id2>|live]

Snapshots are plain read-only directories, so you can also browse them directly
or download files in the web interface.

Restoring
---------

Full restore:

.. code-block:: sh

   dzo restore deerisle <id>      # stops the instance if it runs, starts it again afterwards

The instance is stopped, a safety snapshot is taken, the current data is moved
aside (and kept as a snapshot), the backup becomes the live instance, and the
server starts.

Restore only part of an instance, for example the world or one mod's data (the
world is below ``storage/<map>``):

.. code-block:: sh

   dzo restore deerisle <id> --path storage/empty.deerisle/storage_1

A restore takes a ``pre_restore`` snapshot first, so restoring the wrong snapshot
can be undone, and the data it replaces is kept as a snapshot of reason
``replaced``. If the restore fails, the instance is left as it was.

What is not there yet: the periodic snapshots (``backup.schedule``) and the daily
prune as systemd timers, the snapshot before a server or mod update (those
changes are not automated yet), and the backup of the database, which does not
exist yet.

Offsite copies
--------------

Snapshots stay on the same filesystem, so they do not protect against disk
loss. Add a ``post_backup`` hook that copies new snapshots elsewhere, for
example with restic or borg; example hooks are shipped in
``/usr/share/doc/dzo/examples/backup-hooks/``.
