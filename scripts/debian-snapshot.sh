#!/bin/sh
# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Give a development build of the Debian package a snapshot version: put a new
# entry on top of debian/changelog, <latest version>+git<commits>.<sha>. The top
# entry of the changelog is the latest release; the upcoming one is not in it
# yet. "+" sorts after the release it builds on and before any later release
# (0.1.0-1+git5.abc < 0.1.0-2 < 0.1.1-1), and the commit count only grows, so
# snapshots stay in order. At the commit of the release itself (no commits since
# its tag) the changelog is built as it is.
#
#   scripts/debian-snapshot.sh        (in a git checkout with tag history)
set -eu

cd "$(dirname "$0")/.."
top=$(dpkg-parsechangelog -S Version)
dist=$(dpkg-parsechangelog -S Distribution)

last=$(git describe --tags --abbrev=0 2>/dev/null || true)
if [ -n "$last" ]; then
	count=$(git rev-list --count "$last"..HEAD)
else
	count=$(git rev-list --count HEAD)
fi
if [ "$count" -eq 0 ]; then
	echo "$top"
	exit 0
fi
sha=$(git rev-parse --short=7 HEAD)
version="$top+git$count.$sha"

tmp=$(mktemp)
{
	printf '%s (%s) %s; urgency=medium\n\n' "$(dpkg-parsechangelog -S Source)" "$version" "$dist"
	printf '  * Snapshot build of %s.\n\n' "$sha"
	printf ' -- %s  %s\n\n' "$(dpkg-parsechangelog -S Maintainer)" "$(date -R)"
	cat debian/changelog
} >"$tmp"
cat "$tmp" >debian/changelog
rm -f "$tmp"
echo "$version"
