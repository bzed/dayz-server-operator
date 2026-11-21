#!/bin/sh
# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# dzo's post-install step, shared by every package format so the distributions
# cannot drift apart: the .deb runs it from debian/postinst after debhelper's
# own snippets (so the dayz user from sysusers.d exists), the rpm %post and the
# Arch .install hook call it directly.
#
#   postinst configure   a fresh install
#   postinst upgrade     an upgrade
#
# "upgrade" does everything "configure" does and then refreshes the generated
# units and restarts dzo's own services. It repeats the configuration on
# purpose: an upgrade is also the first run after the sysusers.d file created
# the dayz user, and it must not depend on an earlier run having done the
# subuid and linger setup.
#
# DZO_ROOT prefixes every path the script reads or creates (/etc/subuid,
# /etc/subgid, /var/lib/dzo, /run/user/<uid>, /etc/dzo/config.yaml). It is
# empty in production and set by internal/packaging/postinst_test.go, which
# runs the script against a temporary root.
set -e

DZO_ROOT=${DZO_ROOT:-}
DZO_ROOT=${DZO_ROOT%/}

# The subordinate id range rootless podman maps container ids to.
DZO_SUBID_START=100000
DZO_SUBID_COUNT=65536

# dzo_free_subid_range FILE
#
# Prints the lowest range of DZO_SUBID_COUNT ids at or above DZO_SUBID_START
# that overlaps no range in FILE, as "start-end". FILE holds lines of
# "name:start:count"; blank lines, comments and malformed lines are ignored,
# and a missing file counts as empty. The start only ever grows, so reading the
# file again after it moved ends once no line pushes it any further.
dzo_free_subid_range() {
	dzo_start=$DZO_SUBID_START
	dzo_moved=1
	while [ "$dzo_moved" = 1 ]; do
		dzo_moved=0
		if [ -r "$1" ]; then
			# The "|| [ -n ... ]" catches a last line without a newline,
			# which read otherwise drops: an unterminated range still
			# overlaps.
			while IFS=: read -r dzo_name dzo_begin dzo_size dzo_extra \
				|| [ -n "$dzo_name" ]; do
				case "$dzo_name" in
				'' | '#'*) continue ;;
				esac
				case "$dzo_begin" in
				'' | *[!0-9]*) continue ;;
				esac
				case "$dzo_size" in
				'' | *[!0-9]*) continue ;;
				esac
				# A fourth field, a zero size, or a number longer than a
				# subid file holds: not ours to interpret.
				if [ -n "$dzo_extra" ] || [ "$dzo_size" -eq 0 ] \
					|| [ "${#dzo_begin}" -gt 9 ] || [ "${#dzo_size}" -gt 9 ]; then
					continue
				fi
				dzo_end=$((dzo_start + DZO_SUBID_COUNT - 1))
				if [ "$dzo_begin" -le "$dzo_end" ] \
					&& [ "$dzo_start" -le $((dzo_begin + dzo_size - 1)) ]; then
					dzo_start=$((dzo_begin + dzo_size))
					dzo_moved=1
				fi
			done <"$1"
		fi
	done
	printf '%s-%s\n' "$dzo_start" $((dzo_start + DZO_SUBID_COUNT - 1))
}

case "${1:-}" in
configure | upgrade) ;;
*)
	echo "usage: $0 configure|upgrade" >&2
	exit 2
	;;
esac

# The home and data directory is persistent state, not a tmpfiles.d entry
# (sysusers.d does not create home directories). Never chown recursively.
install -d -o dayz -g dayz -m 0750 "$DZO_ROOT/var/lib/dzo"

# sysusers.d creates the "dayz" system user, but it does not allocate a
# subuid/subgid range, which rootless podman needs. Assign one if none exists
# yet; leave an existing range alone so a reinstall/upgrade never disturbs a
# running instance's container namespace mapping. The two files are chosen
# independently (a host may hold a range for another user in one of them only)
# and both ranges go into one usermod call.
if command -v usermod >/dev/null 2>&1; then
	dzo_add_subuids=
	dzo_add_subgids=
	if ! grep -q '^dayz:' "$DZO_ROOT/etc/subuid" 2>/dev/null; then
		dzo_add_subuids=$(dzo_free_subid_range "$DZO_ROOT/etc/subuid")
	fi
	if ! grep -q '^dayz:' "$DZO_ROOT/etc/subgid" 2>/dev/null; then
		dzo_add_subgids=$(dzo_free_subid_range "$DZO_ROOT/etc/subgid")
	fi
	if [ -n "$dzo_add_subuids" ] && [ -n "$dzo_add_subgids" ]; then
		usermod --add-subuids "$dzo_add_subuids" --add-subgids "$dzo_add_subgids" dayz || true
	elif [ -n "$dzo_add_subuids" ]; then
		usermod --add-subuids "$dzo_add_subuids" dayz || true
	elif [ -n "$dzo_add_subgids" ]; then
		usermod --add-subgids "$dzo_add_subgids" dayz || true
	fi
fi

# Podman under systemd user sessions needs the session to keep running without
# an interactive login (D-Bus activation, timers).
if command -v loginctl >/dev/null 2>&1; then
	loginctl enable-linger dayz || true
fi

# An upgrade: rewrite the generated units for the new version and restart the services that run
# from the binary (the exporter, serve, web). The package manager replaces the file by renaming,
# so every process keeps the old binary until it is restarted. A game server is never restarted
# here: it keeps running on the old binary (mounted into its container) and picks up the new units
# and the new binary at its next restart, which dzo schedules or the operator does
# (dzo restart <name>).
if [ "$1" = "upgrade" ] && id dayz >/dev/null 2>&1; then
	uid=$(id -u dayz)
	bus="$DZO_ROOT/run/user/$uid/bus"
	if [ -S "$bus" ] && command -v runuser >/dev/null 2>&1; then
		as_dayz() {
			runuser -u dayz -- env "XDG_RUNTIME_DIR=$DZO_ROOT/run/user/$uid" "DBUS_SESSION_BUS_ADDRESS=unix:path=$bus" "$@"
		}
		if [ -r "$DZO_ROOT/etc/dzo/config.yaml" ] && [ -d "$DZO_ROOT/var/lib/dzo/site" ]; then
			as_dayz /usr/bin/dzo units sync || echo "dzo: units sync failed after the upgrade; run it as dayz" >&2
		fi
		for unit in dzo-exporter dzo-serve dzo-web; do
			if as_dayz systemctl --user is-active --quiet "$unit.service"; then
				as_dayz systemctl --user restart "$unit.service" || true
			fi
		done
	fi
fi

# No game server is ever started here: bringing up an instance is always an
# explicit "dzo setup"/"dzo instance start" step (D18/§C14).

exit 0
