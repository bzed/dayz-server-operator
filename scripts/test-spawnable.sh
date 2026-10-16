#!/bin/bash
# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Finds the item classes that crash or break a server when they are spawned. A local server runs the
# probe servermod testmods/dzo-spawn-probe, which creates every public item class once, lets it fill
# itself like a debug spawn does, and deletes it. When the server dies, the last class it tried is
# put on a skip list and the run starts again; script errors are attributed to the class that was
# being created. The two lists are printed at the end: the classes that must not be offered by
# dzo-admin (see DZOAdminWorld.UnsafeType). Needs the Steam DayZ Server tool; no client, no dzo serve.
# Usage: scripts/test-spawnable.sh [--exp] [--max-runs N]
set -euo pipefail
cd "$(dirname "$0")/.."
dev=${DAYZ_DEV_SCRIPTS:-$HOME/.claude/skills/dayz-dev/scripts}
exp=() max=40
while [ $# -gt 0 ]; do
	case $1 in
	--exp) exp=(-e) ;;
	--max-runs) max=$2; shift ;;
	*) echo "usage: $0 [--exp] [--max-runs N]" >&2; exit 2 ;;
	esac
	shift
done
work=$(mktemp -d /tmp/dzspawn.XXXXXX)
tree=$work/tree
pid=
cleanup() {
	[ -n "$pid" ] && kill "$pid" 2>/dev/null || true
	sleep 3
	[ -n "$pid" ] && kill -9 "$pid" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT INT TERM

go build -o "$work/dzo" ./cmd/dzo
mkdir "$work/src" && cp -r testmods/dzo-spawn-probe "$work/src/" && git -C "$work/src/dzo-spawn-probe" init -q &&
	git -C "$work/src/dzo-spawn-probe" add -A && git -C "$work/src/dzo-spawn-probe" -c user.name=t -c user.email=t@t commit -qm probe
"$work/dzo" servermods build --src "$work/src" --out "$work/built" >/dev/null
"$dev/make-server-tree.sh" "$tree" "$("$dev/find-dayzserver.sh" "${exp[@]}")" >/dev/null
game=$("$dev/free-ports.sh" 1)
sed -i 's/^password = .*/password = "";/' "$tree/serverDZ.cfg"
grep -q '^BattlEye' "$tree/serverDZ.cfg" || sed -i '1i BattlEye = 0;' "$tree/serverDZ.cfg"
sed -i 's/^BattlEye = .*/BattlEye = 0;/' "$tree/serverDZ.cfg"
ln -sfn "$work/built/dzo-spawn-probe" "$tree/@dzo-spawn-probe"
mkdir -p "$tree/profiles/dzo_probe"
touch "$tree/profiles/dzo_probe/run" "$tree/profiles/dzo_probe/skip"
: >"$work/crashed"
: >"$work/errors"

for run in $(seq "$max"); do
	rm -f "$tree"/profiles/script_*.log "$tree/profiles/dzo_probe/current"
	(cd "$tree" && ulimit -c 0 && exec ./DayZServer -config=serverDZ.cfg -profiles=profiles -servermod=@dzo-spawn-probe \
		-port="$game" -nosplash -nopause -dologs) >"$work/server.log" 2>&1 &
	pid=$!
	finished=0
	# A run ends when the probe says it is done, or the server dies.
	while kill -0 "$pid" 2>/dev/null; do
		if grep -q 'DZO-SPAWNPROBE done' "$tree"/profiles/script_*.log 2>/dev/null; then finished=1; break; fi
		sleep 5
	done
	if [ "$finished" = 1 ]; then
		echo "run $run: finished ($(grep -h 'DZO-SPAWNPROBE done' "$tree"/profiles/script_*.log | tail -1))"
		# Script errors, attributed to the class tried last before them.
		awk '/DZO-SPAWNPROBE try /{c=$NF} /SCRIPT +\(E\)/{ if (c != "") print c }' "$tree"/profiles/script_*.log | sort -u >>"$work/errors"
		break
	fi
	culprit=$(tail -1 "$tree/profiles/dzo_probe/current" 2>/dev/null || true)
	[ -n "$culprit" ] || { echo "the server died before trying any class" >&2; tail -20 "$work/server.log" >&2; exit 2; }
	echo "run $run: the server died on $culprit"
	echo "$culprit" >>"$work/crashed"
	echo "$culprit" >>"$tree/profiles/dzo_probe/skip"
	pid=
	sleep 2
done
echo "== classes that crash the server when spawned:"
cat "$work/crashed"
echo "== classes that cause script errors when spawned:"
sort -u "$work/errors"
