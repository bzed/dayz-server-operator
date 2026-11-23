.. SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
.. SPDX-License-Identifier: AGPL-3.0-or-later

Development
===========

Principles
----------

dzo is written the lazy way: lazy meaning efficient, not careless.

* **Build what is needed now.** No features, options or abstractions "for
  later". The project adds no interface with one implementation, no factory for
  one product, and no setting for a value that never changes.
* **Reuse before writing.** In this order: code that already exists in dzo, the
  Go standard library, a feature of the platform (systemd, podman, btrfs, the
  database). Then use an existing well-maintained library. New code comes last.
* **Boring over clever.** Short, obvious code that someone can debug at 3 am.
* **Never lazy about safety.** Input validation, error handling that prevents
  data loss, security measures and tests are not simplified away.
* A deliberate shortcut with a known limit gets a ``ponytail:`` comment that
  names the limit and the upgrade path.

.. _documentation-writing-style:

Documentation writing style
---------------------------

Written documentation (``README.md``, ``docs/``, the cheat sheet) follows the
ASD-STE100 rules, Simplified Technical English, to 80%. The ratio is a working
target, not a licence: by default every sentence follows the rules below. The
20% margin absorbs the sentence that stays clearer broken, and the terms the
standard does not know. Technical names keep their exact form (``dzo``,
quadlet, Steam Guard, BattlEye); the prose around them gets simple, not the
terms. The reader to write for is an operator who reads English as a second
language, once, and acts.

Sentences
~~~~~~~~~

* One instruction per sentence. A procedural sentence starts with the verb,
  in the imperative: "Stop the instance before you change the file."
* A procedural sentence has 20 words at most. A descriptive sentence has 25
  words at most. Split a long sentence into two sentences, or into a list.
* Two or more steps in a row become a vertical list, numbered when the order
  matters.
* A paragraph stays at six sentences, and holds one topic.

Verbs
~~~~~

* Write in the present tense. Replace "will" and "shall" with the present:
  "the file is written when the restart runs" becomes "the restart writes the
  file".
* Write procedural sentences in the active voice, with the actor as the
  subject. In descriptive sentences, allow the passive only when the receiver
  of the action is the topic and the actor does not matter. Example: "Bans
  are kept in the audit database".
* Do not use the progressive form. "the timer is running" becomes "the timer
  runs".
* Write conditions with "if" and "if not". "Should", "might", "could" and
  "may" hide whether something is a requirement, a possibility or a guess.
  Write "must" for a requirement, "can" for a possibility.

Words
~~~~~

* Use the simplest word that keeps the meaning: "use" not "utilise", "before"
  not "prior to", "to" not "in order to", "about" not "approximately",
  "start" not "commence", "stop" not "terminate", "enough" not "sufficient",
  "get" not "obtain".
* Do not stack more than three nouns in a row. Write "the retention policy of
  the snapshot" instead of "the snapshot retention policy".
* Numbers, units and defaults go in digits: "3 GB", "24 hours", "the default
  is 7".
* Write the full term on first use, the short form after it: "a scoped token
  (a per-purpose API key)".

Building
--------

dzo is a single static Go binary built with the current upstream Go toolchain.
The repository vendors the modules, so builds work offline.

.. code-block:: sh

   make build      # static binary
   make servermods # pack the dzo-admin mod into dist/servermods/ (needs the submodules; see below)
   make vendor-web # re-download the vendored web files into internal/web/vendor and check their sums
   make test       # tests with race detector and coverage gate
   make lint       # includes reuse lint (licence headers)
   make licenses   # dependency licences must be AGPL-compatible
   make docs       # this documentation (needs python3-sphinx)
   make deb        # Debian package

The vendored web files
~~~~~~~~~~~~~~~~~~~~~~

``internal/web/vendor`` holds the pinned copies of the JavaScript, CSS, images and fonts the web
interface serves. It pins htmx 2.0.4, Leaflet 1.7.1 and Fork Awesome 1.2.0 (the versions Debian
trixie packages). It also pins the 4:3 flags of the linuxmint SVG collection of ISO country
flags, the set Debian's ``iso-flags-svg`` is built from. They are upstream's release artefacts
(the npm tarballs, the GitHub tarball of the collection's commit). ``scripts/vendor-web.sh``
(``make vendor-web``) extracts them. It pins the sha256 of every archive and then checks the
extracted files against ``internal/web/vendor/SHA256SUMS``. A build downloads nothing: a version
bump is ``make vendor-web ARGS=--update`` and a commit of the files together with the sums. The
licences are recorded in ``REUSE.toml``, ``LICENSES/`` and ``debian/copyright``.

The collection ships no release tags, so its pin is a commit
(``f2cf92fc2fb200a9132b39672091fc6717a3ad32``, master of 2025-07-16). Only the 4:3 shape goes in, and
only ``<code>.svg`` for a two-letter ISO 3166-1 alpha-2 code. The script leaves out the square flags,
the regions and organisations (``_scotland.svg``, ``_united_nations.svg``, …) and ``brl.svg``,
because a flag is looked up by exactly that code. The flags claim no copyright and are in the public
domain (``LicenseRef-PublicDomain``); the SVGs say so in their RDF metadata.

.. _apt-repository:

The apt repository
~~~~~~~~~~~~~~~~~~

The ``Docs`` workflow (``.github/workflows/docs.yml``) starts when CI has
passed on ``main``. It takes the package that the ``deb`` job of that CI run
built (the ``dzo-deb`` artifact). It runs ``scripts/build-apt-repo.sh``
(reprepro, configured in ``apt/conf/distributions``) and publishes the result
under ``/apt/`` next to the documentation. The repository holds the latest
build of ``main`` only. Besides the codename's directory (``dists/trixie``),
the script publishes the suite that the installation guide tells users to
configure, ``dists/stable``, as a symlink to it. ``reprepro createsymlinks``
creates that symlink from ``Suite: stable`` in ``apt/conf/distributions``. Apt
follows it when a sources entry says ``Suites: stable``. ``Suites: trixie``
keeps working.

Versions: the top entry of ``debian/changelog`` is always the latest release.
The developer adds the upcoming one when it is cut. In CI,
``scripts/debian-snapshot.sh`` adds a changelog entry
``<version at the top of debian/changelog>+git<commits since the last tag>.<sha>``
before the build. ``+`` sorts after that release
and before the next one (``0.1.0-1+git5.abc`` is older than ``0.1.0-2`` and
``0.2.0-1``). The commit count only grows, so snapshots stay in order. The
commit of a release itself is built as it is.

Signing: the key is the repository secret ``APT_GPG_PRIVATE_KEY`` (an
ASCII-armored secret key without a passphrase). The public half is
``apt/dzo-archive-keyring.asc``; the workflow fails if the two do not match. It
signs the dnf and pacman repositories too (``rpm/dzo-repo-keyring.asc`` and
``arch/dzo-repo-keyring.asc``, see below).

To replace the key, create one in a throwaway GnuPG home, never in your own
keyring:

.. code-block:: sh

   export GNUPGHOME=$(mktemp -d)
   gpg --batch --passphrase '' --quick-gen-key "dzo apt repository <bernd@bzed.de>" ed25519 sign never
   gpg --armor --export > apt/dzo-archive-keyring.asc
   cp apt/dzo-archive-keyring.asc rpm/dzo-repo-keyring.asc
   cp apt/dzo-archive-keyring.asc arch/dzo-repo-keyring.asc
   gpg --armor --export-secret-keys | gh secret set APT_GPG_PRIVATE_KEY
   rm -rf "$GNUPGHOME"

Then commit the new public keys and update the fingerprint here and in the
README. Users have to fetch the new key.

.. _dnf-repository:

The dnf repository
~~~~~~~~~~~~~~~~~~

The same ``Docs`` run publishes the rpm repository under ``/rpm/`` from the
``dzo-rpm`` artifact, which CI's ``rpm`` job builds. ``scripts/build-rpm-repo.sh``
signs every package with ``rpmsign``, runs ``createrepo_c`` and then signs
``repodata/repomd.xml`` for dnf's ``repo_gpgcheck``. It also writes the public key
as ``dzo-repo-keyring.asc`` and copies ``rpm/dzo.repo`` next to it. That file is
the repository file that the installation guide tells users to add. Both
repositories use the same key: ``rpm --import`` accepts its EdDSA key on Fedora
43/44 and CentOS Stream 10. A check against those images confirmed that when the
repository was added, so one trusted dzo key covers apt and dnf. The workflow
fails if the exported public key differs from ``rpm/dzo-repo-keyring.asc``.

The rpm is a package-only build. ``scripts/build-rpm.sh`` puts the binary from
the ``build`` job and the payload that the ``deb`` job uploads into rpmbuild's
tree. The payload (``dzo-payload``) holds the servermods, the images, the
presets, the HTML documentation, the generated man pages and the cheat sheet.
``packaging/rpm/dzo.spec`` copies them into place. The .deb and the rpm
therefore ship the same files, and a Fedora build host needs no Go toolchain.
The release is the Debian revision from the top ``debian/changelog`` entry; a
development build appends ``+git<commits>.<sha>`` to it, as
``scripts/debian-snapshot.sh`` does for the .deb. Rpm orders that exactly like
dpkg: ``0.8.2-1`` < ``0.8.2-1+git43.abc1234`` < ``0.8.2-2``, so ``dnf upgrade``
picks up every snapshot that ``apt upgrade`` would.

The rpm creates the ``dayz`` user in ``%pre`` instead of shipping
``dzo.sysusers``. Rpm's own sysusers.d handling and Fedora's
``%sysusers_create_compat`` both allocate a system UID below 1000, but dzo
needs a regular user for its journal (``dzo logs``). So the scriptlet takes the
first free UID at or above 1000, the result the sysusers entry gives on Debian.
``rpmlint`` runs with ``packaging/rpm/dzo.rpmlintrc``, which filters only
what a Go package cannot change (the statically linked binary, DayZ in rpmlint's
spelling data) and says why.

.. _pacman-repository:

The pacman repository
~~~~~~~~~~~~~~~~~~~~~

The same ``Docs`` run publishes the pacman repository under ``/arch/`` from the
``dzo-arch-pkg`` artifact, which CI's ``arch`` job builds. ``makepkg`` runs as a
non-root build user in an ``archlinux:latest`` container. It only assembles the
binary and the payload that the ``deb`` job uploads (``dzo-payload``). So
``packaging/arch/PKGBUILD`` ships the same files as ``packaging/rpm/dzo.spec``,
and an Arch build host needs no Go toolchain. Both Arch jobs are
``continue-on-error``: the distribution is a rolling release. To build the
package locally, run ``make build-static``. Assemble ``arch-payload`` as the
``deb`` job does (``dist/servermods``, ``images``, ``contrib``, ``html``,
``man``, the cheat sheet; the PKGBUILD's header lists them). Then run
``cd packaging/arch && makepkg -f --nodeps``, which needs ``base-devel``.

``scripts/build-arch-repo.sh`` signs every package with a detached signature, runs
``repo-add --sign`` and publishes ``dzo.db`` with its signature as
``dzo.db.sig``. (``repo-add`` writes both as links into ``dzo.db.tar.gz``.
GitHub Pages serves a static tree, so the script replaces them with regular
files.) The script also writes the public key as ``dzo-repo-keyring.asc`` and
copies ``arch/dzo.conf`` next to it. That file is the ``[dzo]`` stanza that the
installation guide makes users include from ``/etc/pacman.conf``, with
``SigLevel = Required DatabaseRequired``. The workflow fails if the exported
public key differs from ``arch/dzo-repo-keyring.asc``, which is the same key as
``apt/`` and ``rpm/``.

Versions: ``pkgver`` is the upstream version from the top ``debian/changelog``
entry and ``pkgrel`` the Debian revision. A development build appends the commit
count since the last tag to ``pkgrel`` (``0.8.2-1.43``). That is the only place
where ``vercmp`` keeps a snapshot after the release and before the next Debian
revision (``0.8.2-1`` < ``0.8.2-1.43`` < ``0.8.2-2`` < ``0.8.3-1``). The same
snapshot in ``pkgver`` would sort after ``0.8.2-2``, and ``pkgrel`` accepts
integers only. So the sha stays out of the package version (``dzo version``
prints it).

The account: Arch's ``systemd-sysusers`` hook creates users from
``/usr/lib/sysusers.d`` in the post-transaction hooks, after ``post_install``,
while ``postinst`` already needs the ``dayz`` user. ``packaging/arch/dzo.install``
therefore runs ``systemd-sysusers`` itself before ``postinst``, which keeps
``packaging/common/dzo.sysusers`` the one description of the account. That
entry gives a regular UID from 1000-59999 (59999 on a fresh host), the result
the .deb gets. The rpm cannot do that, because rpm's own sysusers handling
allocates a system UID below 1000.

Building and testing the servermod
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

``make servermods`` packs ``servermods/*/src/*`` with dzo's own Go packer (no
DayZ Tools, no armake2) into ``dist/servermods/<mod>/addons/*.pbo`` and writes
``compat.yaml``. The output is reproducible, and the server never sends
servermods to clients, so nothing needs a signature or a key. The scripts go in
as plain text, and dzo does not rapify ``config.cpp``; the server reads both.

``servermods.lock`` in the repository pins the submodule commit and the sha256 of every PBO. CI (the
``servermods`` job, ``make servermods-check``) builds the mods on GitHub and fails when they differ from the
lock. That failure also catches a build that is not reproducible. After a change of a servermod, run
``make servermods-lock`` and commit the lock file with the new submodule commit. You must push the
mod's commit to its own repository first, or CI cannot check it out.

A servermod targets DayZ 1.30, the current stable version (Steam app 223350); there is no
version-specific code for older builds. Boot it on the stable server and on the experimental
one (app 1042420, "DayZ Server Exp"). The experimental one shows what the next build
changes: one server tree per version, never two servers on one tree and never default
ports. ``dzo test boot --server <dir>`` takes the experimental install directory;
``--server steam`` is the stable one. Each server build needs its own vanilla baseline
(``dzo test boot --vanilla``).

The 1.30 ``FindFile`` bug has a test of its own. ``make test-findfile`` (``scripts/test-findfile.sh``)
boots an experimental server with CF-Test (workshop item 1625463737, subscribed in the Steam
client) and the servermod ``testmods/dzo-findfile-probe``. The probe waits five seconds after the
mission started and writes two files into ``$profile:dzo_probe/``. It looks up files with
``CF.FindFileEx`` on ``$profile:`` (the server's ``.RPT``, the two probe files) and on ``$mission:``
(``*.xml``, ``db/*.xml``, ``*.c``). Each lookup logs a ``DZO-PROBE <name> OK|FAIL`` line and the run ends
with ``DZO-PROBE RESULT OK|FAIL``; the script passes the lines to ``dzo test boot --expect``. ``control``
lines show what the engine's own ``FindFile`` finds for the same patterns (nothing for ``$profile:`` on
1.30, which is the bug). Like every boot test it needs a machine that does not run dzo instances, and
network access for the vanilla mission.

The player actions of dzo-admin need a connected player, so they have their own test:
``make test-client`` (``scripts/test-client.sh``, ``--exp`` for the experimental build). It builds the servermod and
the probe servermod ``testmods/dzo-client-probe``. It starts a local server with both (BattlEye
off, no password, free ports), a real ``dzo serve`` and the DayZ client
headless. The client runs under sway, through the ``dayz-dev`` skill's
scripts (``DAYZ_DEV_SCRIPTS``).
under sway, through the ``dayz-dev`` skill's scripts (``DAYZ_DEV_SCRIPTS``). With ``dzo player`` and ``dzo vehicle``,
it checks the players state, the messages in the three styles, and teleport (and its refusal in
a vehicle). Further checks cover ``spawn_item`` into the inventory, the hands and the
ground. They also cover an unknown class, a class watch rule on an item, and
the crew of a car. It also follows the event
feed (``dzo events -f --json``) for the whole run. It checks the feed for the client's join and
leave, and for the kill of the player as a death. The probe has a marker file that makes the
game events happen without a player. It checks for a rifle shot, a death without a killer,
building, dismantling and destroying a fence, and a flag raised and lowered, each once. Stashes
need a real dig. The probe mod logs what the player has and, on a
marker file, seats the player in a new car. It is a test aid and never part of a release. It needs
the Steam DayZ Server tool, the client of the same build, a logged-in Steam and one game at a
time. On a machine that runs dzo instances, run it elsewhere.

``--expansion`` loads DayZ Expansion (CF, Dabs Framework, Expansion Licensed and the bundle) on the
server and the client. It also checks the Expansion notification types of dzo-admin. It also
runs the Expansion virtual garage checks of the server part. The run writes a garage JSON (a BI
GUID owner, two vehicles) into the mission storage. The probe servermod
``testmods/dzo-garage-probe`` makes their entity-storage files at mission start. So without a
player the whole flow runs: the stored vehicles are listed. A retrieve places the car standing
on the ground at its new place. A spawn point move keeps the height of the ground there. A
restore spawns a vehicle exactly at that height, without snapping it to the surface.
``--bike-view`` loads the client mod
``testmods/dzo-client-view`` instead. That mod logs where the *client* sees every vehicle around
the player (``DZO-VIEW`` lines in the client's own script log). The server's vehicle list only
says where the server has a vehicle. The check is that a moved bike appears at the new place and
on the ground, in the client's view. That check is the only one that catches a move that never
reaches the players.

The BattlEye event patterns of ``internal/battleye`` come from a real capture
(see :doc:`faq`). To repeat it you need a client that runs BattlEye. The headless
client of the ``dayz-dev`` skill starts ``DayZ_x64.exe`` without it. So set
``PROTON_BATTLEYE_RUNTIME="$STEAM_ROOT/steamapps/common/Proton BattlEye Runtime"`` in its launch
script, as Steam does. Build and sign ``testmods/dzo-be-test`` (``dayz-mod-pack.sh -C testmods/dzo-be-test
keygen`` once, then ``build``). Link ``build/@DZOBETest`` into the client's DayZ directory. Put its
``keys/DZOBETest.bikey`` into the server's keys, or add the mod with ``dzo mod add dzo-be-test
--instance <name> --client --force`` (see :ref:`debug-client-mods`). Then join with ``-mod=@DZOBETest``.
A client with the mod sends two chat lines a minute after the mission started. ``dzo rcon console
<instance>`` prints the events, and ``kick <id> <reason>`` over RCon gives the kick line. A local server needs an
absolute ``-BEpath`` and an ``RConIP`` in ``profiles/battleye/beserver_x64.cfg``.

The ban design (:doc:`moderation`) got the same verification. ``scripts/test-client.sh --bans`` runs
the server **with BattlEye** (RCon on localhost, an absolute ``-BEpath``) and checks, in this
order:

* V1, what BattlEye writes to ``bans.txt`` (``internal/battleye/livebans_test.go``, gated by
  ``DZO_LIVE_BE_ADDR``, ``DZO_LIVE_BE_PASSWORD``, ``DZO_LIVE_BE_DIR`` and ``DZO_LIVE_CAPTURE``, with the
  captures in ``internal/moderation/testdata/v1``)
* V2, that the GUID of a connected player is ``md5("BE" + the Steam ID)`` (``testdata/v2``)
* V3, that a new ban kicks the online player, a banned player cannot join, a revoked one can,
  and an IP ban refuses the address.

``--bans --no-client`` runs only V1 and
needs no game. It needs the BattlEye client described above.

A quick check without a configured instance: symlink ``dist/servermods/dzo-admin``
into a tree of each server as ``@dzo-admin``. Start it with
``-servermod=@dzo-admin`` (a relative path). Compare the script module file
counts with an unmodded boot of the same version. Both of these must hold:

* no ``SCRIPT    (E)`` line that the vanilla boot does not also log (the
  experimental server logs ``Leaked 'BunkerBroadcastManager'`` without any mod);
* the counts rise by the mod's files: ``DZO_ADMIN`` appears in the ``defines``
  of the ``Module:`` lines, and the Game, World and Mission counts are higher
  than vanilla.

Measured with dzo-admin at ``a0e644b``:

.. list-table::
   :header-rows: 1

   * - Server
     - Game
     - World
     - Mission
   * - 1.29.163709 (build 24570360), vanilla / with mod
     - 416 / 422
     - 2123 / 2125
     - 209 / 210
   * - 1.30.164014 (build 25319221), vanilla / with mod
     - 440 / 446
     - 2321 / 2323
     - 219 / 220

Tests
-----

The test suite needs no Steam account and no DayZ server: fakes and recorded
fixtures replace steamcmd, systemd, podman, RCon and the Steam API.
Coverage must stay at 85 % or more; CI fails below that.

Test conventions
~~~~~~~~~~~~~~~~

Tests use `testify <https://github.com/stretchr/testify>`_.

* ``require`` by default; ``assert`` only for independent checks after which
  the test still makes sense.
* No ``t.Fatal*`` or ``t.Error*`` (``t.Skip*`` and ``t.Log*`` are fine).
* ``t.Context()``, ``t.TempDir()``, ``t.Setenv()`` and ``t.Chdir()`` instead of
  their global counterparts.
* Helpers start with ``t.Helper()``.
* No ``time.Sleep``: wait with ``require.Eventually``, a channel or a fake
  clock.
* The linters in ``.golangci.yml`` enforce this for test files: ``testifylint``
  (all checks), ``thelper``, ``usetesting`` and ``forbidigo`` (``time.Sleep``,
  ``t.Fatal*``, ``t.Error*``).
* ``make test`` and ``make test-race`` run with ``-shuffle=on``. A failure
  prints the seed; reproduce it with ``go test -shuffle=<seed> ./pkg/``, then
  fix the order dependency in the test.
* A package that starts goroutines has a ``TestMain`` with
  ``goleak.VerifyTestMain``, so a goroutine that outlives its context fails the
  package. ``goleak.IgnoreTopFunction`` is only for third-party goroutines, with a
  comment naming the library.
* Fuzzing and the browser tests are described below.

Fuzzing
~~~~~~~

The parsers of untrusted input have native Go fuzz targets: BattlEye packets
and events, the A2S info response and ``serverDZ.cfg``. A further target
covers the quantity of the spawn commands. Their seeds run in every ``go test``.
``make fuzz`` fuzzes each target for ``FUZZTIME`` (default 10 s); CI runs the same in
the ``fuzz`` job
and uploads the crashing input on failure. To fuzz one target longer::

   go test ./internal/servercfg -run '^$' -fuzz '^FuzzServercfgParse$' -fuzztime 5m

A crash is a real bug. Go leaves the input in ``testdata/fuzz/<Target>/``;
commit that file together with the fix, it is the regression test.

Quadlet compatibility
~~~~~~~~~~~~~~~~~~~~~

dzo renders the ``.container`` quadlets itself (``internal/quadlet``) instead of
calling ``podman generate systemd``. So every key it writes is a promise about
the podman of the distribution the instance runs on. No Go test can keep that
promise: the program that reads the file is podman's own generator.
``scripts/check-quadlet.sh internal/resolve/testdata`` takes the rendered units
out of the golden files. A ``<name>.golden`` is the resolved instance, a ``---``
line and the rendered unit. The script runs the distribution's generator on
them (``QUADLET_UNIT_DIRS`` pointing at them, ``-dryrun -user``). The paths and
flags it probes, and ``QUADLET_GENERATOR``, are in the script. It fails when the
generator exits non-zero, reports an unsupported key or option, or silently
skips a unit instead of converting it. Either way it prints the generator's
output. A dryrun only converts: the volume sources and the image of the
units need not exist, and it starts no container. So the script runs anywhere
podman is installed, as root or not.

CI runs it in the ``quadlet-check`` job and, against the installed package's
podman, in every ``package-install`` job. ``debian:trixie`` (podman 5.4, the
version dzo requires) and ``registry.fedoraproject.org/fedora:44`` fail the
build. ``quay.io/centos/centos:stream10`` and ``archlinux:latest`` carry podman 6
and are allowed to fail. So a key that the next podman drops shows up in the log
before it shows up in a user's journal.

Browser end-to-end tests
~~~~~~~~~~~~~~~~~~~~~~~~

``make e2e`` drives the web UI in a headless Chrome or Chromium with
`chromedp <https://github.com/chromedp/chromedp>`_ (build tag ``e2e``, files
``internal/web/e2e*_test.go``, tests ``TestE2E*``). chromedp is pure Go and
talks the DevTools protocol to the browser that is installed anyway (the
cheat sheet build needs one). Selenium needs a matching WebDriver binary, and
Playwright downloads its own browsers at test time. The cost is that the tests
cover only Chrome-based browsers.

The tests run against the fake backends of ``newEnv`` but with the JavaScript
dzo serves. htmx, Leaflet and Fork Awesome are compiled into the binary, so
the tests need nothing but a browser. A missing browser fails the test, it
never skips it.

``DZO_E2E_CHROME``
   the browser binary; default: chromedp's lookup (``google-chrome``,
   ``chromium``)

``DZO_E2E_ASSETS``
   optional: a directory with ``htmx/``, ``leaflet/`` and ``fork-awesome/``.
   It overrides the built-in files file by file, for a run against another
   version (an htmx 4 beta, say). Unset, the tests use the built-in copies.

The browser reports every JavaScript exception, console error and CSP
violation, and the test fails on any of them. An unexpected ``confirm()``
dialog fails the test too; a test that expects one answers it through
``browser.dialogs``. To watch a run while debugging, add
``chromedp.Flag("headless", false)`` to the allocator options in
``newBrowser``; never commit that.

CI runs ``make e2e`` in a ``debian:trixie`` container with Debian's
``chromium``, as an unprivileged user with the sandbox on, and a second run in
``debian:forky``. The JavaScript is compiled into the binary, so a suite only
brings the browser of its release. ``debian:forky`` covers the browser that the
next Debian release ships.

On a machine that is not Debian trixie, ``make e2e-container`` runs the same
suite in a ``debian:trixie`` podman container. The container is built from
``images/e2e/Containerfile`` with the Chromium of the CI job. The checkout is
bind-mounted and the tests run as your own user
(never as root, and without chowning your checkout). The Go caches persist in
the ``dzo-e2e-home-<suite>`` volume, so the toolchain and module downloads
happen once. ``DZO_E2E_SUITE`` builds from another Debian suite (``forky``, the
other suite CI runs). ``DZO_E2E_TARGET`` runs another make target
in the container, and ``DZO_E2E_PODMAN_OPTS`` replaces the sandbox options (a
host without AppArmor drops ``--security-opt apparmor=unconfined`` there). On
an SELinux host, add ``:Z`` to the checkout bind mount.

The supervisor against a real podman
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

``make test-appliance`` runs ``TestSuperviseRealPodman`` (build tag
``appliance``, ``internal/supervise/appliance_test.go``) against a real
rootless podman socket. The test starts a ``debian:trixie-slim`` container with
``sleep infinity`` and a health check through ``supervise.Run``, the code
behind ``dzo instance supervise``. It waits for ``READY=1``, stops the run the
way systemd does and checks that ``--rm`` left no container behind. The test
runs only with ``DZO_APPLIANCE_PODMAN=1``, and it needs
``systemctl --user enable --now podman.socket``; it pulls the image when the
store does not have it. It is not part of ``make check``: CI has no podman
socket.

``CONTAINER_HOST``
   the podman endpoint, as podman itself reads it; without it the test uses
   ``unix://$XDG_RUNTIME_DIR/podman/podman.sock``, the rootless socket.

The container name carries a random suffix, and the test removes only that
container. So the test can run next to a real installation on the same host.

The browser against a real DayZ server
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

CI cannot start DayZ, so the tests above run against fakes. The fakes can lie:
a field the real mod sends differently, an error text, timing. ``make
test-client`` (``scripts/test-client.sh``, see below) starts a real server with
dzo-admin, a real ``dzo serve`` and, unless ``--no-client``, the headless DayZ
client. Its last check drives the web UI over that stack with the same browser
harness (``internal/web/live_test.go``, build tags ``e2e live``, tests
``TestLive*``). Run it by hand, with ``DZO_E2E_CHROME`` as above (and
``DZO_E2E_ASSETS`` for JavaScript other than the built-in copy); it fails
without a browser. The script sets:

``DZO_LIVE_API``
   the base URL of the API of the running ``dzo serve``; without it the tests
   fail (the ``live`` tag is an explicit opt-in)

``DZO_LIVE_TOKEN_FILE``
   a web token (``dzo token create web --role admin --web``)

``DZO_LIVE_DB``
   the database of that ``dzo serve``. The test logs the admin ``76561198000000077`` (granted by the
   script) in by making a session there, because the pages need a login

``DZO_LIVE_INSTANCE``
   the instance, ``t1``

``DZO_LIVE_PLAYER``
   the client's player name, ``dzotester``; empty with ``--no-client``, which
   skips ``TestLivePlayer``

What the live run catches that the CI tests did not gets a fake-backed
regression test in the same commit as the fix. The live tier finds the bug, and
CI keeps it fixed.

Boot test with a real server
----------------------------

Unit tests cannot prove that DayZ accepts what dzo renders. ``dzo test boot``
starts a real ``DayZServer`` headless on a render result and checks the logs.

.. warning::

   Only on a development machine or a dedicated test machine. ``dzo test boot``
   refuses to run on a host where dzo manages instances (instance directories
   below ``paths.instances``, or dzo quadlets for the user). There is no option
   to override that.

.. code-block:: sh

   dzo test boot --vanilla deerisle   # once per server build: record the baseline
   dzo test boot deerisle             # boot the instance
   dzo test boot deerisle --expect 'MyMod: loaded' --out ./logs --keep-tree

``--server steam`` (the default) uses the "DayZ Server" tool installed by your
Steam client (Library, Tools). dzo finds it through Steam's own manifests, and
neither installs it nor writes into Steam's directories; ``--server <dir>``
uses another installation. Mods come from your client's workshop folder
(``--mods-from steam-client``, the default with ``--server steam``), from dzo's
cache (``--mods-from cache``), or from a directory of ``@Mod`` folders. dzo-admin
and other local servermods always come from dzo's cache. With ``--container`` the
server runs in the instance's runtime image instead (podman). The tree, the
server install and the mods are mounted at their own paths, the install and the
mods as overlays. So the links of the tree resolve, and nothing is written into
them. ``--port-from`` and ``--port-to`` choose the range of the test ports on a host that reserves
one for the game.

The test builds a throwaway tree. The tree holds symlinks to the server install
and a copy of the mission as a render would apply it. Your ``serverDZ.cfg`` gets
test ports, a random password and a hostname that starts with ``[dzo boot test]``.
The test starts the server on free ports and stops it afterwards. The Steam query
answers a couple of seconds after the start, long before the mission has loaded.
Dzo waits for
the query, then for the mission's scripts, then for ``--settle`` seconds (20 by
default), and with dzo-admin for the mod's first contact. ``--timeout`` limits
the whole wait. The exit code is 0 if everything passed, 1 if a check failed and
2 if the test cannot run.

.. list-table::
   :header-rows: 1
   :widths: 18 82

   * - Check
     - What fails it
   * - ``ready``
     - the server exited or did not answer the Steam query in time
   * - ``mission``
     - the query answers but the mission's scripts never load
   * - ``crash``
     - a crash dump was written
   * - ``scripts``
     - a script compile error (``SCRIPT (E)``, the tag is space-padded), or no
       script log at all
   * - ``modules``
     - a mod ships scripts for a module but its file count did not rise over the
       vanilla baseline: the scripts were not loaded (an absolute mod path or a
       PBO without a prefix loads nothing and prints no error)
   * - ``ce``
     - an XML file that does not parse, a ``<ce folder>`` or a file that does not
       exist, a types file that cannot be read
   * - ``expect``
     - an ``--expect`` regular expression that is not in the script log or the RPT
   * - ``dzo-admin``
     - the mod never made contact at the test endpoint

Everything else that is new against the baseline is shown as a warning, verbatim.
An example is a class name in ``types.xml`` that does not exist
(``Type 'X' will be ignored``). Bohemia's own missions have a few of
those too.

The vanilla baseline holds, per server build, the number of script files each
module loads and the error lines an unmodded server logs. A server installed
through Steam logs several hundred of them, because its map data is stripped.
Without a baseline the counts and error lines are only shown. The baseline is
cached below ``paths.cache`` and keyed by the server binary, so a game update
needs a new one.

What a real 1.29 server logs for broken input
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

These lines come from a capture with a real server (build 24570360) and are the
fixtures the checks are tested against (``internal/boottest/testdata``).

.. list-table::
   :header-rows: 1
   :widths: 40 60

   * - Input
     - Log
   * - ``types.xml`` that is not well-formed
     - ``!!! [ERROR][XML] :: load [...types.xml] failed``, ``Error reading end
       tag. line N``, ``!!! [CE][offlineDB] :: Failed to read types file``
   * - ``<ce folder>`` that does not exist, or a file that does not
     - ``!!! [ERROR][XML] :: load [...] failed``, ``Failed to open file``,
       ``!!! [CE][offlineDB] :: Failed to read types file``
   * - class that does not exist in ``types.xml``
     - ``!!! [CE][offlineDB] :: Type 'X' will be ignored. (Type does not exist.
       (Typo?))``
   * - script error in the mission's ``init.c``
     - ``SCRIPT    (E): @"...init.c,99": Can't find variable 'x'`` and
       ``Can't compile mission init script``; the mission scripts never load
   * - broken ``cfggameplay.json``
     - nothing: the file is read only if ``serverDZ.cfg`` has
       ``enableCfgGameplayFile = 1``, and not checked by this test

The central economy marks its errors with ``!!!``. ``error.log`` also holds
plain information lines (``ENTITY : Load entity type ...``) that differ from run
to run; only its ``(E)`` and ``(W)`` lines count.

Logs are kept in ``--out`` (default ``./boottest-<instance>-<time>/``), and the
whole tree with ``--keep-tree``.

Booting by hand
~~~~~~~~~~~~~~~

Without dzo, the same procedure: build a tree of symlinks to the Steam server
install (never write into it). Give the server free ports for the game, the
Steam query (``steamQueryPort`` in ``serverDZ.cfg``) and RCon (``RConPort`` in
``profiles/battleye/beserver_x64.cfg``). Pass mods as paths relative to the tree
(absolute ones are silently ignored). Run it from the tree with ``ulimit -c 0``
and stop it with ``timeout -k 15``. Then read ``script_*.log``, the ``.RPT`` and
``error.log`` in ``profiles/``.

.. _release-checklist-other-distributions:

Release checklist for other distributions
-----------------------------------------

The CI jobs of the other distributions (:doc:`installation`, "CI, per
distribution") install the package and run ``dzo version`` and ``dzo setup
--dry-run``. They let the distribution's own podman generate the quadlets. That
is all a container can do: it has no booted systemd and no DayZ. So it says
nothing about SELinux, a Steam login, a server or a client. This list is the
rest, and it is the **only** real check on Ubuntu, Fedora, the RHEL 10 family
and Arch. Run it

* on a VM of the distribution (never in a container: SELinux's enforcing mode,
  a real user systemd, the kernel and podman's runtime are part of the test),
* once per dzo release line (0.8, 0.9, …), and
* when a distribution release joins or leaves the matrix (a new Fedora, a new
  Ubuntu LTS, the next Debian stable).

1. **Install from the repository**, with the commands of that distribution's
   section in :doc:`installation`, as a user would: the package the repository
   serves for the release, its dependencies from the distribution, ``dzo version``
   and ``dzo --help``.
2. **``dzo setup``** as ``dayz`` (not ``--dry-run``): the directories, both
   container images, the site clone and the image-refresh timer. Every step must
   report ``ok``. A ``warn`` about a missing btrfs is expected on the RHEL 10
   family; any ``error`` ends the checklist here.
3. **The Steam login** with a real account, including Steam Guard:
   ``dzo steam login --user <account>``, then ``dzo steam status``. On Fedora, the
   RHEL 10 family and Arch this runs steamcmd in the ``localhost/dzo-steamcmd``
   image through the pty of the wrapper. No one has tried that with a real
   account. This step is what decides whether those distributions stay
   "Supported", so it is also a release blocker.
4. **A real server and a client.** ``dzo product install dayz-stable``, create an
   instance (:doc:`quickstart`), ``dzo start <instance>``, ``dzo status`` and
   ``dzo logs <instance>``, then join from a workstation with the DayZ client,
   BattlEye on. This is the only check of the podman 6 quadlets (CentOS Stream
   10, Arch) and of SELinux in enforcing mode (Fedora, RHEL).
5. **A restart** while the client is connected: ``dzo restart <instance>
   --minutes 1`` announces, locks, kicks and starts the server again, and the
   client can rejoin.
6. **Backup and restore** where btrfs exists (Debian, Ubuntu, Fedora, Arch):
   ``dzo backup create <instance>``, ``dzo backup list <instance>``, then
   ``dzo restore <instance> <snapshot>`` of one path and of the whole instance.
   On the RHEL 10 family the kernel has no btrfs, so the step does not apply.
   Note that in the release notes instead of skipping it silently (the
   distribution's own backup is the answer there).
7. **The web interface**: ``dzo instance apply <instance>`` starts ``dzo-serve``
   and ``dzo-web``; log in, and check the player list and the map with the client
   from step 4 still connected (:doc:`web`).
8. **An upgrade with a server running.** With the server from step 4 up, install
   the next build the repository serves (``apt upgrade``, ``dnf upgrade dzo``,
   ``pacman -Syu``). ``dzo version`` must print the new one. The exporter,
   ``dzo serve`` and ``dzo web`` must come back by themselves. The game server
   must run on the old binary until its next restart and then start on the new
   one. **A package upgrade never restarts a game server**. If
   it does, or if a server does not come back, that is a bug, not a checklist
   item.

Note in the release's ``debian/changelog`` entry which distribution passed which
step. A "Supported" distribution that fails a step drops to "Best effort" in the
matrix (and in the README) until it passes again. A "Best effort" distribution
that passes all eight steps for a release line can move up.

.. _release-checklist-container-appliance:

Release checklist for the container appliance
---------------------------------------------

The appliance (``images/appliance/``) is Debian trixie and follows the Debian
steps above, but its game servers are containers of the *host's* podman. The
host quadlet and the wrapper live in ``contrib/appliance/``. CI builds and
boots the image and runs ``dzo setup --dry-run`` inside it; it cannot run DayZ
or a client. ``make test-appliance`` covers the supervisor against a real
podman socket (see `The supervisor against a real podman`_). Once per release
line, on a host with the appliance, the following is the rest:

1. **A real server and a client.** Install the quadlet and the wrapper, start
   the appliance, then ``dzo setup`` (the three remote checks ``ok``), a site
   with one instance, ``dzo instance create``, ``dzo instance apply`` and
   ``dzo start <instance>``. The instance's unit becomes active when the
   server is healthy, ``podman ps`` on the host lists ``dzo-<instance>``, and
   a DayZ client joins.
2. **The stop.** ``podman stop dzo`` stops the server before the appliance:
   the RCon shutdown is in the instance's journal, and ``dzo-<instance>`` is
   gone on the host within the quadlet's ``StopTimeout``.
3. **An upgrade with a server running.** ``apt upgrade`` inside the appliance
   (or ``apt install --reinstall`` of the next ``.deb``) leaves the server
   alone: its ``StartedAt`` does not change, and the next ``dzo restart
   <instance>`` runs the new binary.
4. **A killed appliance.** ``podman kill dzo``: the quadlet's
   ``Restart=on-failure`` starts the appliance again (``systemctl --user start
   dzo.service`` if the start limit stopped it). Its supervisor attaches to the
   running server, and the server's ``StartedAt`` does not change. The quadlet
   starts the appliance with ``--rm``, so ``podman stop`` and ``podman kill``
   remove the appliance container and ``podman start dzo`` has nothing to
   start. The unit is the way back.

Contributing
------------

* Code and documentation change together: a change in behaviour updates the
  matching page here.
* The documentation is plain reStructuredText in ``docs/``, built with Sphinx.
  ``make docs`` must build without warnings.
* dzo is licensed under the AGPL-3.0-or-later. The repository follows the
  `REUSE <https://reuse.software/>`_ specification: every new file starts with
  SPDX headers in the file's comment syntax, for example in Go:

  .. code-block:: go

     // SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
     // SPDX-License-Identifier: AGPL-3.0-or-later

  Files that cannot carry comments (images, test fixtures) get a
  ``<file>.license`` file next to them or an entry in ``REUSE.toml``.
  ``make lint`` runs ``reuse lint`` and fails on files without licence
  information.
