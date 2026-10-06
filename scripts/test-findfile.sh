#!/bin/bash
# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Boots a DayZ Experimental (1.30) server with CF-Test and the probe servermod
# (testmods/dzo-findfile-probe), which looks up files with CF.FindFileEx on $profile: and $mission:
# and logs whether they were found. Needs, on a machine that does not run dzo instances:
#   * the DayZ Server Exp tool (Steam app 1042420) and CF-Test (workshop item 1625463737, subscribe
#     to it in the Steam client), or --server <dir> --workshop <dir with 1625463737/>
#   * network access (the vanilla mission is fetched from Bohemia's central economy repo)
# Usage: scripts/test-findfile.sh [--server <dir|steam>] [--workshop <dir>] [--keep]
set -euo pipefail
cd "$(dirname "$0")/.."

server=steam workshop="" keep=()
while [ $# -gt 0 ]; do
	case $1 in
	--server) server=$2; shift 2 ;;
	--workshop) workshop=$2; shift 2 ;;
	--keep) keep=(--keep-tree); shift ;;
	*) echo "usage: $0 [--server <dir|steam>] [--workshop <dir>] [--keep]" >&2; exit 2 ;;
	esac
done
if [ -z "$workshop" ]; then
	for d in "$HOME/.steam/steam" "$HOME/.local/share/Steam" "$HOME/.steam/debian-installation"; do
		[ -d "$d/steamapps/workshop/content/221100/1625463737" ] && workshop="$d/steamapps/workshop/content/221100" && break
	done
fi
[ -d "$workshop/1625463737" ] || { echo "CF-Test (1625463737) not found: subscribe to it in the Steam client or pass --workshop" >&2; exit 2; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
go build -o "$work/dzo" ./cmd/dzo
"$work/dzo" servermods build --src testmods --out "$work/built" >/dev/null
mkdir "$work/mods"
ln -s "$(readlink -f "$workshop/1625463737")" "$work/mods/1625463737"
ln -s "$work/built/dzo-findfile-probe" "$work/mods/dzo-findfile-probe"
cat >"$work/config.yaml" <<Y
paths:
  data: $work/data
  cache: $work/cache
  site: $PWD/scripts/findfile-site
Y

# --expect patterns: every probe line must say OK; the run fails if the probe never reported.
XDG_CONFIG_HOME="$work/xdg" "$work/dzo" test boot findfile --config "$work/config.yaml" \
	--server "$server" --mods-from "$work/mods" --settle 15s "${keep[@]}" \
	--out "$work/logs" \
	--expect 'DZO-PROBE profile-rpt OK' --expect 'DZO-PROBE profile-files OK' \
	--expect 'DZO-PROBE mission-xml OK' --expect 'DZO-PROBE mission-db OK' --expect 'DZO-PROBE mission-init OK' \
	--expect 'DZO-PROBE RESULT OK' || rc=$?
grep -h 'DZO-PROBE' "$work"/logs/*.RPT 2>/dev/null | sed 's/^/  /' | sort -u || true
exit "${rc:-0}"
