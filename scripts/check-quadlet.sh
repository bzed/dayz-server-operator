#!/bin/sh
# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Check the .container units dzo renders against the podman quadlet generator of
# the distribution they run on. dzo writes the quadlets itself (internal/quadlet)
# instead of calling `podman generate systemd`, so nothing in the Go tests can
# notice when a later podman drops or renames one of the keys it uses; running
# the distribution's own generator in its container does (CI job quadlet-check).
#
# Usage: scripts/check-quadlet.sh <dir-or-file>...
#
# A directory is searched for *.container (a quadlet unit) and *.golden (the
# resolved instance followed by --- and the rendered unit, of which the part
# after the marker is used: internal/resolve/testdata/alpha.golden). Point it at
# internal/resolve/testdata to check what dzo actually renders.
#
# Fails when the generator exits non-zero, when it reports an unsupported key or
# option, or when it silently skips a unit instead of converting it. The volume
# sources and the image in the units need not exist: the dryrun only converts,
# it neither resolves paths nor talks to podman.
set -eu

# The generator: the systemd user generator podman installs, or the quadlet
# binary itself (its -user flag makes it behave as the user generator). The path
# differs per distribution and release, so probe rather than assume.
# QUADLET_GENERATOR overrides the probe.
find_generator() {
	if [ -n "${QUADLET_GENERATOR:-}" ]; then
		printf '%s\n' "$QUADLET_GENERATOR"
		return 0
	fi
	for p in \
		/usr/lib/systemd/user-generators/podman-user-generator \
		/usr/libexec/podman/quadlet \
		/usr/lib/podman/quadlet; do
		if [ -x "$p" ]; then
			printf '%s\n' "$p"
			return 0
		fi
	done
	for c in podman-user-generator quadlet; do
		if command -v "$c" >/dev/null 2>&1; then
			command -v "$c"
			return 0
		fi
	done
	return 1
}

if [ $# -eq 0 ]; then
	echo "usage: $0 <dir-or-file>..." >&2
	exit 2
fi

gen=$(find_generator) || {
	echo "no podman quadlet generator found (set QUADLET_GENERATOR)" >&2
	exit 2
}

# QUADLET_UNIT_DIRS is the only input the generator reads from us; the default
# dirs stay in effect, but they are empty in a bare distribution image.
work=$(mktemp -d "${TMPDIR:-/var/tmp}/dzo-quadlet.XXXXXX")
trap 'rm -rf "$work"' EXIT
units="$work/units"
mkdir "$units"
names=""

add_unit() { # <file with a quadlet unit> <name without .container>
	if [ -e "$units/$2.container" ]; then
		echo "two inputs render $2.container; check each file once" >&2
		exit 2
	fi
	cp "$1" "$units/$2.container"
	names="$names $2"
}

add_input() { # <file>
	base=$(basename "$1")
	case "$base" in
	*.golden)
		if ! grep -qx -- '---' "$1"; then
			echo "$1: no rendered quadlet (no --- separator)" >&2
			exit 1
		fi
		name=${base%.golden}
		awk '/^---$/{f=1;next} f' "$1" >"$work/$name.container"
		add_unit "$work/$name.container" "$name"
		;;
	*.container)
		add_unit "$1" "${base%.container}"
		;;
	*)
		echo "skipping $1: not a .container or .golden file" >&2
		;;
	esac
}

for arg in "$@"; do
	if [ ! -d "$arg" ]; then
		add_input "$arg"
		continue
	fi
	for f in "$arg"/*.container "$arg"/*.golden; do
		[ -e "$f" ] && add_input "$f"
	done
done

if [ -z "$names" ]; then
	echo "no .container or .golden files to check" >&2
	exit 2
fi
count=$(printf '%s\n' "$names" | wc -w)

# The generator's own help says which flags this version takes; probe it instead
# of assuming, so that a renamed flag shows up as a failure here.
help_text=$("$gen" --help 2>&1 || "$gen" -help 2>&1 || true)
flags=""
case "$help_text" in
*-dryrun*) flags="$flags -dryrun" ;;
esac
case "$help_text" in
*-user*) flags="$flags -user" ;;
esac
if [ -z "$flags" ]; then
	echo "warning: $gen documents neither -dryrun nor -user; running it without flags" >&2
fi

set +e
version=$("$gen" -version 2>&1 | head -n 1)
# shellcheck disable=SC2086 # $flags is a list of separate flags, not one argument
out=$(QUADLET_UNIT_DIRS="$units" "$gen" $flags 2>&1)
rc=$?
set -e
printf 'generator: %s%s %s\n' "$gen" "$flags" "$version"
printf '%s\n' "$out"

fail=0
if [ "$rc" -ne 0 ]; then
	echo "FAIL: $gen exited $rc" >&2
	fail=1
fi
if printf '%s\n' "$out" | grep -qi 'unsupported'; then
	echo "FAIL: the generator reports an unsupported key or option (above)" >&2
	fail=1
fi
for name in $names; do
	if ! printf '%s\n' "$out" | grep -qx -- "---$name.service---"; then
		echo "FAIL: the generator did not convert $name.container" >&2
		fail=1
	fi
done

if [ "$fail" -ne 0 ]; then
	exit 1
fi
echo "OK: $count unit(s) converted by $gen"
