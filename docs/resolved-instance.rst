.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

How an instance is resolved
===========================

Every command that acts on an instance first *resolves* it: the operator
configuration, the site repository and the download cache are combined into one
complete description. ``dzo instance show <name>`` prints it as YAML, and
``dzo config validate`` resolves every instance in the site checkout.

.. code-block:: sh

   dzo instance show deerisle
   dzo instance show deerisle --quadlet     # the container unit dzo would generate

Defaults
--------

``site.yaml`` can set ``image`` (the runtime image, default
``localhost/dzo-runtime:latest``) and a ``defaults`` block. It may contain
``params``, ``updates``, ``restarts``, ``health``, ``restart_limit``, ``notify``
and ``container``. Whatever an instance leaves unset is taken from there, field
by field; a value set in ``instance.yaml`` always wins.

.. code-block:: yaml

   image: localhost/dzo-runtime:latest
   defaults:
     updates: {policy: notify, check_interval: 1h}
     health: {startup_timeout: 45m}

Unknown keys in ``site.yaml`` and ``instance.yaml`` are errors, so a typo is
never silently ignored.

Mods
----

An entry in ``mods`` is either a workshop id or a local servermod:

.. code-block:: yaml

   mods:
     - {id: 1559212036}
     - {local: dzo-admin, server: true}

A local mod must set ``server: true`` (or, for debugging only,
``debug_client: true``, see :ref:`debug-client-mods`) and have a name of letters, digits,
``_``, ``.`` and ``-`` that is not purely numeric. It is read from
``localmods/<name>/`` in the site repository, or from a ``local_mods`` entry in
``site.yaml``:

.. code-block:: yaml

   local_mods:
     tools: {url: https://<host>/tools-1.0.tar.gz, sha256: <hash>}
     other: {path: /srv/build/@Other}

Inside the container a workshop mod is mounted as ``/dayz/@<id>`` and a local
mod as ``/dayz/@<name>``.

What is not installed yet
-------------------------

Resolving never downloads anything. The server build and mod generations are
looked up in the cache; whatever is missing is listed under ``missing`` in the
output (and as a note by ``dzo config validate``). The container unit is only
built once nothing is missing, because it mounts the exact generations.

Checks
------

Resolving fails for an unknown product, mission preset, overlay or local mod, a
malformed ``container.mounts`` entry, and for two instances that use the same
port.
