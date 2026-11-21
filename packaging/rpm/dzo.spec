# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# The rpm repackages what CI already built: the static binary (make build-static)
# and the payload of the Debian package (servermods, images, presets, the HTML
# documentation and the man pages). Nothing is compiled here, so the rpm and the
# .deb ship the same files and dzo needs no Go toolchain on a Fedora or RHEL
# build host. scripts/build-rpm.sh assembles the tree and passes the two paths
# below.
#
# Version and Release come from debian/changelog through build-rpm.sh (rpm
# cannot read it). A development build appends "+git<commits>.<sha>" to the
# Debian revision, the same string scripts/debian-snapshot.sh puts there, so rpm
# orders the versions like dpkg: 0.8.2-1 < 0.8.2-1+git43.abc1234 < 0.8.2-2.
%{!?dzo_version:%global dzo_version 0.0}
%{!?dzo_release:%global dzo_release 0}
%{!?dzo_payload:%{error:dzo_payload must point at the assembled payload directory}}
%{!?dzo_license:%{error:dzo_license must point at the LICENSE file}}

Name:           dzo
Version:        %{dzo_version}
Release:        %{dzo_release}
Summary:        DayZ dedicated server operator
# SPDX identifier; the dzo-admin servermod in the payload is AGPL too.
License:        AGPL-3.0-or-later
URL:            https://github.com/bzed/dayz-server-operator
# The payload holds the linux/amd64 binary; there is no other architecture.
ExclusiveArch:  x86_64
Requires:       podman >= 5.4
Requires:       systemd >= 257
Requires:       git
Requires:       patch
Requires:       passt
Requires:       shadow-utils
# useradd/groupadd run in %pre, before the rest of the transaction is installed
Requires(pre):  shadow-utils
Recommends:     btrfs-progs
Recommends:     geoipupdate

# The prebuilt, stripped binary has no debug information to package.
%global debug_package %{nil}

%description
dzo manages DayZ dedicated server instances on top of rootless podman
quadlets: it installs and updates server builds and workshop mods, renders
per-instance missions and configuration, and provides a built-in BattlEye RCon
client, container health checks and btrfs-snapshot backups.

This package creates the "dayz" regular user and its data directories, but
never starts any game server on its own; use "dzo setup" after installation to
configure and bring up instances.

%prep

%build

%install
rm -rf %{buildroot}
install -d %{buildroot}%{_bindir}
install -d %{buildroot}%{_sysconfdir}/dzo
install -d %{buildroot}%{_libexecdir}/dzo
install -d %{buildroot}%{_datadir}/dzo/presets/maps
install -d %{buildroot}%{_datadir}/doc/dzo/html
install -d %{buildroot}%{_mandir}/man1
install -d %{buildroot}%{_prefix}/lib/tmpfiles.d
install -d %{buildroot}%{_datadir}/licenses/dzo

install -m 0755 %{dzo_payload}/dzo %{buildroot}%{_bindir}/dzo
install -m 0644 %{dzo_payload}/config.yaml %{buildroot}%{_sysconfdir}/dzo/config.yaml
install -m 0755 %{dzo_payload}/postinst %{buildroot}%{_libexecdir}/dzo/postinst
install -m 0755 %{dzo_payload}/steamcmd-container %{buildroot}%{_libexecdir}/dzo/steamcmd-container
install -m 0644 %{dzo_payload}/dzo.tmpfiles %{buildroot}%{_prefix}/lib/tmpfiles.d/dzo.conf
cp -a %{dzo_payload}/servermods %{buildroot}%{_datadir}/dzo/servermods
cp -a %{dzo_payload}/images %{buildroot}%{_datadir}/dzo/images
cp -a %{dzo_payload}/presets/maps/. %{buildroot}%{_datadir}/dzo/presets/maps/
cp -a %{dzo_payload}/html/. %{buildroot}%{_datadir}/doc/dzo/html/
# Sphinx writes its doctrees and a buildinfo file into the output tree; the .deb
# does not ship them either (dh_install's docs/_build/html/* does not match
# hidden entries).
rm -rf %{buildroot}%{_datadir}/doc/dzo/html/.doctrees \
	%{buildroot}%{_datadir}/doc/dzo/html/.buildinfo
install -m 0644 %{dzo_payload}/dzo-cheatsheet.pdf %{buildroot}%{_datadir}/doc/dzo/dzo-cheatsheet.pdf
install -m 0644 %{dzo_payload}/man/*.1 %{buildroot}%{_mandir}/man1/
install -m 0644 %{dzo_license} %{buildroot}%{_datadir}/licenses/dzo/LICENSE

# The payload comes from a build tree with a restrictive umask (the servermod
# packer writes 0600/0750); the .deb gets 0644/0755 from dh_fixperms, rpm has no
# such step.
find %{buildroot} -type d -exec chmod 0755 {} +
find %{buildroot} -type f -exec chmod 0644 {} +
chmod 0755 %{buildroot}%{_bindir}/dzo \
	%{buildroot}%{_libexecdir}/dzo/postinst \
	%{buildroot}%{_libexecdir}/dzo/steamcmd-container

# The HTML documentation carries a downloadable copy of the cheatsheet;
# hardlink the two instead of shipping the PDF twice. (%fdupes is no longer in
# Fedora's rpm macros, and this is the only duplicate.)
cheatsheet=%{buildroot}%{_datadir}/doc/dzo/dzo-cheatsheet.pdf
html_copy=$(find %{buildroot}%{_datadir}/doc/dzo/html -name dzo-cheatsheet.pdf -print -quit)
[ -z "$html_copy" ] || ln -f "$cheatsheet" "$html_copy"

%pre
# rpm's own sysusers.d handling allocates a system UID (below 1000), and
# %sysusers_create_compat does the same with useradd -r; neither can pick a UID
# from the 1000-59999 range the sysusers file reserves for dayz. dzo needs a
# regular user: journald keeps a per-user journal only for UIDs of 1000 and up,
# and `dzo logs` reads that one. So the package creates the account here, as the
# .deb's sysusers file would, and from the first free UID at or above 1000 so an
# existing administrative account does not collide. An existing dayz user is
# left as it is (an upgrade must not disturb a running instance).
if ! getent group dayz >/dev/null 2>&1; then
    groupadd -r dayz
fi
if ! getent passwd dayz >/dev/null 2>&1; then
    uid=1000
    while getent passwd "$uid" >/dev/null 2>&1; do
        uid=$((uid + 1))
        if [ "$uid" -gt 59999 ]; then
            echo "dzo: no free UID left in 1000-59999 for the dayz user" >&2
            exit 1
        fi
    done
    useradd -M -g dayz -d /var/lib/dzo -s /sbin/nologin -u "$uid" \
        -c "DayZ server operator" dayz
fi
exit 0

%post
# One post-install script for every package format; the package manager passes
# 1 on a fresh install and 2 or more on an upgrade, dpkg passes the old version
# or nothing.
if [ "$1" -eq 1 ]; then
    /usr/libexec/dzo/postinst configure
else
    /usr/libexec/dzo/postinst upgrade
fi

%files
%license %{_datadir}/licenses/dzo/LICENSE
%config(noreplace) %{_sysconfdir}/dzo/config.yaml
%{_bindir}/dzo
%{_libexecdir}/dzo/postinst
%{_libexecdir}/dzo/steamcmd-container
%{_prefix}/lib/tmpfiles.d/dzo.conf
%dir %{_datadir}/dzo
%{_datadir}/dzo/servermods
%{_datadir}/dzo/images
%{_datadir}/dzo/presets
%{_mandir}/man1/*.1*
%doc %{_datadir}/doc/dzo/dzo-cheatsheet.pdf
%doc %{_datadir}/doc/dzo/html

%changelog
* Sat Nov 21 2026 Bernd Zeimetz <bernd@bzed.de> - 0.8.2-1
- Repackage the CI-built static binary and the payload of the Debian package
  for Fedora 43 and later and the RHEL 10 family.
