#!/bin/sh
# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Give a development build of the Debian package a snapshot version: put a new
# entry on top of debian/changelog, <upcoming version>~git<commits>.<sha>-<rev>.
# The upcoming release is the version at the top of the changelog (the entry that
# is not released yet), and "~" sorts before it, so a snapshot is always older
# than the release it leads to and newer than the earlier snapshots (the commit
# count only grows). Bump debian/changelog right after a release, or the
# snapshots would sort before that release.
#
#   scripts/debian-snapshot.sh        (in a git checkout with tag history)
set -eu

cd "$(dirname "$0")/.."
top=$(dpkg-parsechangelog -S Version)
dist=$(dpkg-parsechangelog -S Distribution)
upstream=${top%-*}
rev=${top##*-}
[ "$upstream" != "$top" ] || rev=1

last=$(git describe --tags --abbrev=0 2>/dev/null || true)
if [ -n "$last" ]; then
	count=$(git rev-list --count "$last"..HEAD)
else
	count=$(git rev-list --count HEAD)
fi
sha=$(git rev-parse --short=7 HEAD)
version="$upstream~git$count.$sha-$rev"

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
