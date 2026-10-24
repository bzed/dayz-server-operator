#!/bin/bash
# SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests the dzo-admin actions that need a connected player, with a real DayZ client: a local server
# with dzo-admin (and the probe servermod testmods/dzo-client-probe), a real `dzo serve`, and the
# DayZ client started headless (the dayz-dev skill's scripts). Checks, through `dzo player|vehicle`:
#   players state, message (three styles, notification fields, expansion refused), teleport, refusal while in a vehicle, spawn_item into the
#   inventory, the hands and the ground (with a class watch rule on the item), an unknown class, and
#   the crew of a vehicle.
# Needs, on a machine that does not run dzo instances: the Steam DayZ Server tool (stable 223350, or
# --exp 1042420), the DayZ client of the same build, a running logged-in Steam, sway, and the dayz-dev
# skill's scripts (DAYZ_DEV_SCRIPTS, default ~/.claude/skills/dayz-dev/scripts). One game at a time.
# --no-client runs only the checks that need no player (motorbike, event scan, territory flags and their raise) and starts no
# game, so it can run while a DayZ client is open. With --expansion it also loads Expansion and checks an Expansion
# territory (made by the probe on the server, no client needed).
# --bans runs the server with BattlEye and captures how its bans.txt behaves (ban spike V1).
# --expansion runs the server and the client with DayZ Expansion (CF, Dabs Framework, Expansion Licensed and Bundle
# from the Steam workshop) and checks the Expansion notification types of dzo-admin, with a screenshot of each in
# $DZO_SHOTS_DIR (default: the work directory, kept); it stops after those checks (the player is still choosing a
# spawn point).
# --old-mod runs the dzo-admin that main pins (before the message ways) with this dzo, as on a host whose
# instances were not restarted yet, and checks that the notification the restart countdown sends still arrives.
# Usage: scripts/test-client.sh [--exp] [--keep] [--no-client] [--bans] [--expansion] [--old-mod]
set -euo pipefail
cd "$(dirname "$0")/.."
dev=${DAYZ_DEV_SCRIPTS:-$HOME/.claude/skills/dayz-dev/scripts}
exp=() keep=0 noclient=0 bans=0 expansion=0 oldmod=0 product=stable
for a in "$@"; do
	case $a in
	--exp) exp=(-e) product=experimental ;;
	--keep) keep=1 ;;
	--no-client) noclient=1 ;;
	--bans) bans=1 ;;
	--expansion) expansion=1 ;;
	--old-mod) oldmod=1 ;;
	*) echo "usage: $0 [--exp] [--keep] [--no-client] [--bans] [--expansion] [--old-mod]" >&2; exit 2 ;;
	esac
done
[ -x "$dev/dayz-client-headless.sh" ] || { echo "dayz-dev scripts not found in $dev (DAYZ_DEV_SCRIPTS)" >&2; exit 2; }
[ "$noclient" = 1 ] || "$dev/dayz-client-headless.sh" status >&2 || { echo "a DayZ client is running; one game at a time" >&2; exit 2; }

work=$(mktemp -d /tmp/dzcl.XXXXXX)
tree=$work/tree
pids=()
cleanup() {
	"$dev/dayz-client-headless.sh" "${exp[@]}" stop "$work/client" >/dev/null 2>&1 || true
	for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done
	sleep 5
	for p in "${pids[@]}"; do kill -9 "$p" 2>/dev/null || true; done
	if [ "$keep" = 1 ]; then echo "kept $work"; else rm -rf "$work"; fi
}
trap cleanup EXIT INT TERM

fail=0 pass=0
ok() { echo "PASS $1"; pass=$((pass + 1)); }
bad() { echo "FAIL $1"; fail=$((fail + 1)); }
# verdict <name> <status>: PASS when the status is 0.
verdict() { if [ "$2" = 0 ]; then ok "$1"; else bad "$1"; fi; }
waitfor() { timeout "$1" bash -c "until $2; do sleep 3; done"; }

# web_tests: the browser (internal/web/live_test.go, chromedp) against this real server and dzo serve. It
# needs Chrome/Chromium and the JavaScript assets of docs/development.rst (DZO_E2E_CHROME, DZO_E2E_ASSETS).
web_tests() {
	local player=dzotester s=0
	[ "$noclient" = 1 ] && player=
	DZO_LIVE_API="http://127.0.0.1:$api" DZO_LIVE_TOKEN_FILE="$work/api.token" DZO_LIVE_INSTANCE=t1 DZO_LIVE_PLAYER="$player" \
		go test -tags 'e2e live' -count=1 -run '^TestLive' ./internal/web/ >"$work/web.log" 2>&1 || s=$?
	[ "$s" = 0 ] || cat "$work/web.log" >&2
	verdict "web UI against the real server" $s
}

# server_tests: needs the server, dzo serve and the probe, no client.
server_tests() {
# 1.30 only: Motorbike is not a Car, dzo-admin keeps a registry for it. 1.29 has no motorbikes.
# The world already has motorbikes of the economy: the probe's is the id that is new.
vl() { "$work/dzo" vehicle list t1 --config "$work/config.yaml" --token-file "$work/api.token" "$@"; }
vl | awk '$2 == "motorbike" {print $1}' | sort >"$work/bikes.before"
touch "$tree/profiles/dzo_probe/bike"
s=0
bike=
for _ in $(seq 20); do
	bike=$(vl | awk '$2 == "motorbike" && /Motorbike_01_Blue/ {print $1}' | sort | comm -13 "$work/bikes.before" - | head -1)
	[ -n "$bike" ] && break
	sleep 3
done
[ -n "$bike" ] || s=1
verdict "motorbike: listed with kind motorbike" $s
s=0
"$work/dzo" vehicle repair t1 "$bike" --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || s=1
verdict "motorbike: repair" $s
s=0
"$work/dzo" vehicle repair t1 "$bike" --scope missing --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || s=1
verdict "motorbike: add missing parts" $s
s=0
"$work/dzo" vehicle flip t1 "$bike" --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || s=1
verdict "motorbike: flip" $s
s=0
"$work/dzo" vehicle delete t1 "$bike" --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || s=1
waitfor 30 "! '$work/dzo' vehicle list t1 --config '$work/config.yaml' --token-file '$work/api.token' | grep -q '^$bike '" || s=1
verdict "motorbike: delete" $s

# Missing parts: bare cars (as the economy leaves them) get their parts, the headlights in both slots and the battery.
touch "$tree/profiles/dzo_probe/bare"
s=0
waitfor 60 "[ \$('$work/dzo' vehicle list t1 --config '$work/config.yaml' --token-file '$work/api.token' | awk '\$3 ~ /^(Offroad_02|Hatchback_02|CivilianSedan|OffroadHatchback)\$/ && \$4 > 7390 && \$4 < 7440' | wc -l) = 4 ]" || s=1
verdict "repair: the bare cars are listed" $s
for id in $(vl | awk '$3 ~ /^(Offroad_02|Hatchback_02|CivilianSedan|OffroadHatchback)$/ && $4 > 7390 && $4 < 7440 {print $1}'); do
	"$work/dzo" vehicle repair t1 "$id" --scope missing --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || s=1
done
verdict "repair: add missing parts to the bare cars" $s
sleep 2
touch "$tree/profiles/dzo_probe/parts"
waitfor 20 "grep -q 'DZO-PROBE parts bare' '$script'" || true
sleep 4
for car in Offroad_02 Hatchback_02 CivilianSedan OffroadHatchback; do
	s=0
	line=$(grep "DZO-PROBE parts bare $car " "$script" | tail -1 || true)
	[ "$(grep -o 'HeadlightH7' <<<"$line" | wc -l || true)" = 2 ] || s=1
	grep -Eq 'CarBattery|TruckBattery' <<<"$line" || s=1
	[ "$s" = 0 ] || echo "$line" >&2
	verdict "repair: $car has both headlights and the battery" $s
done

# A vehicle spawned on the map comes with its parts.
s=0
"$work/dzo" vehicle spawn t1 Offroad_02 --x 7600 --z 7600 --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || s=1
verdict "spawn vehicle: at a position" $s
touch "$tree/profiles/dzo_probe/parts"
sleep 5
s=0
line=$(grep "DZO-PROBE parts spawned Offroad_02 " "$script" | tail -1 || true)
[ "$(grep -o 'HeadlightH7' <<<"$line" | wc -l)" = 2 ] && grep -q 'CarBattery' <<<"$line" || s=1
[ "$s" = 0 ] || echo "$line" >&2
verdict "spawn vehicle: with both headlights and the battery" $s

# More than a stack holds is more stacks, on the ground at a position.
s=0
"$work/dzo" player spawn t1 Nail --qty 500 --x 7450 --z 7450 --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || s=1
verdict "spawn item: at a position" $s
touch "$tree/profiles/dzo_probe/ground"
sleep 5
s=0
line=$(grep 'DZO-PROBE ground Nail' "$script" | tail -1 || true)
case $line in *'quantity=500'*) ;; *) s=1 ;; esac
case $line in *'stacks=1 '*) s=1 ;; esac
[ "$s" = 0 ] || echo "$line" >&2
verdict "spawn item: 500 nails are several stacks that add up" $s

# Quantity: default leaves what the game makes, max is one full stack, a number goes to SetQuantity (0 is
# empty, and no item at all for a class that cannot exist empty).
for q in "Canteen default" "Pot max" "WaterBottle 0" "Rag 0" "Nail 5" "Ammo_556x45 3"; do
	set -- $q
	"$work/dzo" player spawn t1 "$1" --qty "$2" --x 7460 --z 7460 --config "$work/config.yaml" --token-file "$work/api.token" >/dev/null || echo "spawn $q refused" >&2
done
sleep 4
touch "$tree/profiles/dzo_probe/items"
sleep 6
items=$(grep 'DZO-PROBE items 7460' "$script" | tail -1 || true)
n() { grep -o " $1=[0-9.]*/[0-9]*" <<<"$items" | wc -l || true; }
has() { grep -q " $1 " <<<"$items "; }
s=0; has 'Canteen=1000/1000' || s=1
verdict "spawn item: quantity default leaves a canteen full" $s
s=0; has 'Pot=2000/2000' || s=1
verdict "spawn item: quantity max fills a pot" $s
s=0; has 'WaterBottle=0/1000' || s=1
verdict "spawn item: quantity 0 makes a water bottle empty" $s
s=0; [ "$(n Rag)" = 0 ] || s=1
verdict "spawn item: quantity 0 of a rag is no item at all" $s
s=0; has 'Nail=5/99' || s=1
verdict "spawn item: quantity 5 of nails" $s
s=0; [ "$(n Ammo_556x45)" = 3 ] || s=1
[ "$s" = 0 ] || echo "$items" >&2
verdict "spawn item: quantity 3 of ammo with a stack size of 1 is three items" $s

# Central Economy events are found by the class of their object: a plain building object (no script class of its
# own, like the wrecks the events place) is a marker when a watch rule selects its class, wildcards included.
touch "$tree/profiles/dzo_probe/event"
ev() { curl -s -H "Authorization: Bearer $(cat "$work/api.token")" "http://127.0.0.1:$api/api/v1/instances/t1/events"; }
mk() { curl -s -H "Authorization: Bearer $(cat "$work/api.token")" "http://127.0.0.1:$api/api/v1/instances/t1/map/markers"; }
s=0
waitfor 60 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -q 'Police cars'" || s=1
verdict "event object: the police car wreck is a marker of its layer" $s
s=0
mk | grep -o '{[^{}]*"layer":"Police cars"[^{}]*}' | grep -q '"icon":"car"' || s=1
verdict "event object: with the icon of the layer" $s
s=0
waitfor 40 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -o '{[^{}]*\"layer\":\"Police cars\"[^{}]*}' | grep -Eq '\"x\":7500[,.}]'" || s=1 # the server's own police events are markers too
verdict "event object: at the place of the wreck" $s

# A dynamic gas zone (as a thrown M18 smoke grenade makes it) is an event with the size of the zone.
touch "$tree/profiles/dzo_probe/gas"
s=0
waitfor 60 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/events | grep -q '\"x\":73[0-9][0-9],[^}]*\"effect_area\"'" || s=1
verdict "gas zone: reported as an event at the place it was thrown" $s
s=0
ev | grep -o '"x":73[0-9][0-9][^}]*' | grep -q '"radius":120' || s=1
[ "$s" = 0 ] || ev >&2
verdict "gas zone: with the radius of the zone (120 m)" $s

# A territory flag is a marker of the Flags layer for operators: the icon, the place, and the refresher time left as ttl.
touch "$tree/profiles/dzo_probe/flag"
fl() { mk | grep -o '{[^{}]*"layer":"Flags"[^{}]*}'; }
s=0
waitfor 60 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -q '\"layer\":\"Flags\"'" || s=1
verdict "flag: a territory flag is a marker of the Flags layer" $s
s=0
fl | grep -q '"icon":"flag"' && fl | grep -Eq '"x":76[0-9][0-9]' && fl | grep -Eq '"visibility":"operator"' || s=1
[ "$s" = 0 ] || fl >&2
verdict "flag: with the flag icon, at its place, for operators" $s
s=0
fl | grep -Eq '"ttl":1[67][0-9]{5}' || s=1
verdict "flag: the ttl is the refresher time left (about 20 days)" $s
s=0
curl -s -H "Authorization: Bearer $(cat "$work/api.token")" "http://127.0.0.1:$api/api/v1/instances/t1/layers" | grep -o '{[^{}]*"name":"Flags"[^{}]*}' | grep -q '"visibility":"operator"' || s=1
verdict "flag: the Flags layer is defined for operators" $s
# Raising the flag (API, CLI and the map's menu all end in this command) fills the timer: 40 days.
fid=$(fl | sed -n 's/.*"id":"\(f[0-9.-]*\)".*/\1/p' | head -1)
s=0
"$work/dzo" flag raise t1 "$fid" --config "$work/config.yaml" --token-file "$work/api.token" 2>&1 | grep -q ok || s=1
verdict "flag: raise ($fid) is carried out" $s
s=0
waitfor 40 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -o '{[^{}]*\"layer\":\"Flags\"[^{}]*}' | grep -Eq '\"ttl\":3[34][0-9]{5}'" || s=1
verdict "flag: after raise the timer is full (40 days)" $s
s=0
out=$("$work/dzo" flag raise t1 f0.0.0.0 --config "$work/config.yaml" --token-file "$work/api.token" 2>&1) && s=1
echo "$out" | grep -qi 'flag not found' || { s=1; echo "$out" >&2; }
verdict "flag: raise of an unknown flag is refused" $s
# mkl <layer>: the markers of a layer, one compact JSON object per line (props and points hold braces, which grep -o cannot span).
mkl() { mk | python3 -I -c 'import json,sys;[print(json.dumps(m,separators=(",",":"),ensure_ascii=False)) for m in json.load(sys.stdin) if m.get("layer")==sys.argv[1]]' "$1" || true; }
# The experimental build does not always have the rebuilding of 1.30 yet (no rebuildable types, the tunnel is a plain house): then
# these checks are skipped, and dzo-admin must only stay quiet (the script log check above).
touch "$tree/profiles/dzo_probe/rebuild"
sleep 8
rebuilt_tests() {
# A rebuilt house (two parts built by the probe) is a marker of the "Rebuilt houses" layer for operators, with the parts, the
# weakest part and the time until decay wears it down (the part stays, at its floor: ttl = (health - floor) / rate).
rb() { mkl "Rebuilt houses"; }
s=0
waitfor 60 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -q '\"layer\":\"Rebuilt houses\"'" || s=1
verdict "rebuilt: a rebuilt house is a marker of the Rebuilt houses layer" $s
s=0
rb | grep -q '"icon":"home"' && rb | grep -q '"source":"rebuilt"' && rb | grep -q '"visibility":"operator"' && rb | grep -Eq '"x":79[0-9][0-9]' || s=1
[ "$s" = 0 ] || { rb >&2; mk >&2; }
verdict "rebuilt: with the house icon, at its place, for operators" $s
s=0
rb | grep -Eq '"k":"parts","v":"2/[0-9]+"' && rb | grep -Eq '"k":"weakest","v":"[a-z0-9_]+ 100%"' && rb | grep -q '"k":"floor","v":"30%"' || s=1
[ "$s" = 0 ] || rb >&2
verdict "rebuilt: parts built, the weakest part and its decay floor" $s
s=0
rb | grep -Eq '"ttl":17[34][0-9][0-9]' || s=1
[ "$s" = 0 ] || rb >&2
verdict "rebuilt: the ttl is the time until decay reaches the floor (70% of 25000 HP at 1 HP/s, a few ticks of decay in)" $s
s=0
curl -s -H "Authorization: Bearer $(cat "$work/api.token")" "http://127.0.0.1:$api/api/v1/instances/t1/layers" | grep -o '{[^{}]*"name":"Rebuilt houses"[^{}]*}' | grep -q '"visibility":"operator"' || s=1
verdict "rebuilt: the Rebuilt houses layer is defined for operators" $s
# A well, when the game has one that can be rebuilt (Land_WaterWell_Brick does in 1.30).
touch "$tree/profiles/dzo_probe/rebuildwell"
s=0
waitfor 60 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -q '\"layer\":\"Rebuilt wells\"'" || s=1
verdict "rebuilt: a rebuilt well is a marker of the Rebuilt wells layer" $s
s=0
mkl "Rebuilt wells" | grep -q '"icon":"tint"' || s=1
verdict "rebuilt: a well has the tint icon" $s
# The holes of the tunnel entrances a rope ladder can be built on: marked for everybody, in another colour with a usable ladder.
tn() { mkl "Tunnel entrances" | grep -E '"x":80[0-9][0-9]' || true; }
touch "$tree/profiles/dzo_probe/tunnel"
s=0
waitfor 60 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -Eq '\"layer\":\"Tunnel entrances\".*\"x\":80[0-9][0-9]'" || s=1
verdict "tunnel: an entrance without a ladder is marked" $s
s=0
tn | grep -q '"color":"#6b5b45"' && tn | grep -q '"k":"ladder","v":"none"' && tn | grep -q '"visibility":"viewer"' || s=1
[ "$s" = 0 ] || tn >&2
verdict "tunnel: in the plain colour, for everybody" $s
touch "$tree/profiles/dzo_probe/tunnelladder"
s=0
waitfor 60 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers  | python3 -I -c 'import json,sys;sys.exit(0 if any(m.get(\"layer\")==\"Tunnel entrances\" and 7990<m[\"x\"]<8010 and {\"k\":\"ladder\",\"v\":\"usable\"} in m[\"props\"] for m in json.load(sys.stdin)) else 1)'" || s=1
verdict "tunnel: with a built ladder it says usable" $s
s=0
tn | grep -q '"color":"#2a8553"' || s=1
verdict "tunnel: and has the other colour" $s
}
if grep -q 'DZO-PROBE rebuild: type [A-Za-z]' "$script"; then
	rebuilt_tests
else
	echo "SKIP rebuilt houses and wells, tunnel entrances: this build has no rebuildable buildings"
fi
# A sandstorm is a thin area of two strengths and a centre marker with heading, speed and time left; everybody sees it.
sn() { mkl Sandstorm; }
touch "$tree/profiles/dzo_probe/storm"
s=0
waitfor 60 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -q '\"id\":\"storm\"'" || s=1
verdict "sandstorm: an active storm has a centre marker" $s
s=0
sn | grep -q '"id":"storm"' && sn | grep -Eq '"k":"heading","v":"[NSEW]+ \([0-9]+°\)"' && sn | grep -Eq '"k":"speed","v":"[0-9]+ km/h"' && sn | grep -Eq '"k":"strength","v":"[0-9]+%"' && sn | grep -q '"source":"sandstorm"' && sn | grep -q '"visibility":"viewer"' || s=1
[ "$s" = 0 ] || sn | grep '"id":"storm"' >&2
verdict "sandstorm: the centre has heading, speed and strength, for everybody" $s
s=0
waitfor 90 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -q '\"shape\":\"polygon\".*\"source\":\"sandstorm\"'" || s=1 # the storm fades in over 30 s: its intensity grows from nothing
[ "$(sn | grep -c '"shape":"polygon".*"k":"band","v":"light"')" -ge 1 ] || s=1 # grep -c, not -q: pipefail turns the early exit into a failure
echo "sandstorm polygons: $(sn | grep -c '"shape":"polygon"')" >&2
verdict "sandstorm: the storm is an area of light (and heavy) rectangles" $s
touch "$tree/profiles/dzo_probe/stormstop"
s=0
waitfor 90 "! curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -q '\"layer\":\"Sandstorm\"'" || s=1
verdict "sandstorm: nothing is reported once the storm is over" $s
if [ "$expansion" = 1 ]; then
	touch "$tree/profiles/dzo_probe/territory"
	tr() { mk | grep -o '{[^{}]*"layer":"Territories"[^{}]*}'; }
	s=0
	waitfor 60 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -q '\"layer\":\"Territories\"'" || s=1
	verdict "territory: an Expansion territory is a marker of the Territories layer" $s
	s=0
	tr | grep -q '"label":"Probe Camp"' && tr | grep -q '"shape":"circle"' && tr | grep -Eq '"radius":[1-9]' && tr | grep -q '"visibility":"operator"' && tr | grep -q '"source":"territory"' || s=1
	[ "$s" = 0 ] || { tr >&2; mk >&2; }
	verdict "territory: a circle with the name, the radius of the territory, for operators" $s
	s=0
	mk | grep -q '"k":"territory","v":"Probe Camp"' || s=1
	verdict "territory: its flag names the territory" $s
fi
# The game events of the event feed, made by the probe without a player (characters without an identity).
touch "$tree/profiles/dzo_probe/feed"
waitfor 40 "grep -q 'DZO-PROBE feed: done' '$script'" || true
sleep 8 # the events reach dzo with the next syncs
ev() { grep -E "$1" "$work/events.jsonl" | wc -l; }
s=0; [ "$(ev '"kind":"kill".*"weapon":"M4-A1","distance":(99|100|101)')" -ge 1 ] || s=1
verdict "event feed: a rifle kill with weapon and distance" $s
s=0; [ "$(ev '"kind":"death".*"has_pos":true,"x":705.*"cause":"self"')" -ge 1 ] || s=1
verdict "event feed: a death without a killer is self" $s
s=0; [ "$(ev '"kind":"build".*"object":"wall_base_down".*"text":"Fence"')" = 1 ] || s=1
verdict "event feed: building a fence part" $s
s=0; [ "$(ev '"kind":"dismantle".*"object":"wall_wood_down"')" = 1 ] || s=1
verdict "event feed: dismantling a fence part" $s
s=0; [ "$(ev '"kind":"destroy".*"object":"wall_base_down"')" = 1 ] && [ "$(ev '"kind":"destroy".*"object":"wall_wood_down"')" = 0 ] || s=1
verdict "event feed: a raid is one destroy, the parts that fall with it are not reported" $s
s=0; [ "$(ev '"kind":"flag_raised"')" = 1 ] && [ "$(ev '"kind":"flag_lowered"')" = 1 ] || s=1
verdict "event feed: a flag raised and lowered" $s
if [ "$expansion" = 1 ]; then
	s=0; [ "$(ev '"kind":"kill".*"target_name":"AI \\"[^"\\]+\\"( \([A-Za-z ]+\))?".*"target_ai":true')" -ge 1 ] || s=1
	[ "$s" = 0 ] || grep '"target_ai"' "$work/events.jsonl" >&2
	verdict "event feed: an Expansion AI victim is named, AI \"Name\" (Faction), without a raw string key" $s
fi
# Stashes are not driven here: a dig action run by hand does not move the item, only a player's does.
web_tests
}


go build -o "$work/dzo" ./cmd/dzo
if [ "$oldmod" = 1 ]; then
	mkdir -p "$work/oldsrc/dzo-admin"
	git -C servermods/dzo-admin archive "$(git rev-parse main:servermods/dzo-admin)" | tar -x -C "$work/oldsrc/dzo-admin"
	git -C "$work/oldsrc/dzo-admin" init -q && git -C "$work/oldsrc/dzo-admin" add -A && git -C "$work/oldsrc/dzo-admin" -c user.name=t -c user.email=t@t commit -qm old
	"$work/dzo" servermods build --src "$work/oldsrc" --out "$work/built" >/dev/null
else
	"$work/dzo" servermods build --src servermods --out "$work/built" >/dev/null
fi
mkdir "$work/tsrc" && cp -r testmods/dzo-client-probe "$work/tsrc/" && git -C "$work/tsrc/dzo-client-probe" init -q && git -C "$work/tsrc/dzo-client-probe" add -A && git -C "$work/tsrc/dzo-client-probe" -c user.name=t -c user.email=t@t commit -qm probe # dzo-be-test is built with armake2, not by dzo
"$work/dzo" servermods build --src "$work/tsrc" --out "$work/tbuilt" >/dev/null

srv=$("$dev/find-dayzserver.sh" "${exp[@]}")
"$dev/make-server-tree.sh" "$tree" "$srv" >/dev/null
set -- $("$dev/free-ports.sh" 4)
game=$1 query=$2 api=$3 mod=$4
cfg=$tree/serverDZ.cfg
grep -q '^steamQueryPort' "$cfg" || sed -i '1i steamQueryPort = 0;' "$cfg"
sed -i "s/^steamQueryPort = .*/steamQueryPort = $query;/" "$cfg"
grep -q '^BattlEye' "$cfg" || sed -i '1i BattlEye = 0;' "$cfg" # the headless client has no BattlEye
sed -i 's/^BattlEye = .*/BattlEye = 0;/; s/^password = .*/password = "";/' "$cfg"
rcon=$((query + 1))
if [ "$bans" = 1 ]; then
	# The ban checks need BattlEye: RCon on localhost, an absolute -BEpath, and a client that runs
	# BattlEye (PROTON_BATTLEYE_RUNTIME, see docs/development.rst).
	sed -i 's/^BattlEye = .*/BattlEye = 1;/' "$cfg"
	rconpw=$(head -c12 /dev/urandom | od -An -tx1 | tr -d ' \n')
	mkdir -p "$tree/profiles/battleye" "$work/secrets/rcon" "$work/data/instances/t1"
	printf 'RConPassword %s\nRConPort %d\nRConIP 127.0.0.1\n' "$rconpw" "$rcon" >"$tree/profiles/battleye/beserver_x64.cfg"
	echo "$rconpw" >"$work/secrets/rcon/t1"
	chmod 600 "$tree/profiles/battleye/beserver_x64.cfg" "$work/secrets/rcon/t1"
	# dzo serve finds the instance's profiles (and so its bans.txt) under paths.instances.
	ln -sfn "$tree/profiles" "$work/data/instances/t1/profiles"
	export PROTON_BATTLEYE_RUNTIME=${PROTON_BATTLEYE_RUNTIME:-$HOME/.local/share/Steam/steamapps/common/Proton BattlEye Runtime}
fi

# The operator side: a config, a one-instance site, the instance's mod token and an API token.
mkdir -p "$work/site/instances/t1" "$work/secrets/admin" "$tree/profiles/dzo/admin"
cp -r scripts/findfile-site/integrations "$work/site/"
echo 'local_mods: {}' >"$work/site/site.yaml"
cat >"$work/site/instances/t1/instance.yaml" <<Y
name: t1
product: dayz-$product
map: dayzOffline.chernarusplus
mission_source: {preset: vanilla-chernarusplus}
ports: {game: $game, rcon: $rcon, query: $query}
network: host
mods:
  - {local: dzo-admin, server: true}
welcome: "Welcome to #name, port #port (default colour)"
messages:
  - {text: "Rules: be nice (yellow)", on_connect: true, color: yellow}
  - {text: "Homepage: example.org (green)", on_connect: true, color: green, delay: 0}
Y
mkdir -p "$work/site/localmods/dzo-admin/addons"
cat >"$work/config.yaml" <<Y
paths: {data: $work/data, cache: $work/cache, site: $work/site, secrets: $work/secrets}
serve: {installation: t, listen: "127.0.0.1:$api", mod_listen: "127.0.0.1:$mod"}
Y
tok=$(head -c16 /dev/urandom | od -An -tx1 | tr -d ' \n')
echo "$tok" >"$work/secrets/admin/t1.token"
echo "{\"version\":1,\"endpoint\":\"http://127.0.0.1:$mod/\",\"token\":\"$tok\",\"sync_ms\":500,\"players_s\":2,\"vehicles_s\":5,\"markers_s\":10,\"events_s\":5,\"allow_spawn\":true,\"watch\":[{\"layer\":\"Police cars\",\"classes\":[\"Land_Wreck_sed01_aban1_police*\"],\"icon\":\"car\",\"label\":\"Police car\"},{\"layer\":\"lights\",\"classes\":[\"Chemlight_Red\"],\"icon\":\"flare\",\"label\":\"watched chemlight\",\"max\":20}]}" >"$tree/profiles/dzo/admin/config.json"
chmod 600 "$work/secrets/admin/t1.token" "$tree/profiles/dzo/admin/config.json"
"$work/dzo" token create tester --role admin --config "$work/config.yaml" >"$work/api.token"
chmod 600 "$work/api.token"
dzo() { "$work/dzo" "$@" --config "$work/config.yaml" --token-file "$work/api.token"; }

start_serve() {
	"$work/dzo" serve --config "$work/config.yaml" >"$work/serve.log" 2>&1 &
	pids+=($!)
	# The event feed of the whole run, as `dzo events -f --json` prints it, started once the API answers.
	waitfor 30 "curl -sf http://127.0.0.1:$api/healthz >/dev/null"
	"$work/dzo" events t1 -f --json -n 1000 --config "$work/config.yaml" --token-file "$work/api.token" >"$work/events.jsonl" 2>"$work/events.err" &
	pids+=($!)
}
# feed_has <kind> <field> <value> [<field> <value>]: the event feed has such an event.
feed_has() { grep -Eq "\"kind\":\"$1\".*\"$2\":\"$3\"${4:+.*\"$4\":\"$5\"}" "$work/events.jsonl"; }
[ "$bans" = 1 ] || start_serve
ln -sfn "$work/built/dzo-admin" "$tree/@dzo-admin"
ln -sfn "$work/tbuilt/dzo-client-probe" "$tree/@dzo-client-probe"
modargs=() clientmods=() smods= cmods=
if [ "$expansion" = 1 ]; then
	# server folder -> workshop id, and the folder the client knows the same mod by
	ws=${WORKSHOP:-$HOME/.local/share/Steam/steamapps/workshop/content/221100}
	for m in CF:1559212036:@9dd22c91 DabsFramework:2545327648:@1bbd85f5 Licensed:2116157322:@0891b3df Bundle:2572331007:@829b91b4; do
		IFS=: read -r name id cname <<<"$m"
		ln -sfn "$ws/$id" "$tree/@$name"
		for k in "$ws/$id"/keys/*.bikey; do ln -sf "$k" "$tree/keys/"; done
		smods="$smods@$name;"
		cmods="$cmods$cname;"
	done
	modargs=("-mod=${smods%;}")
	clientmods=("-mod=${cmods%;}")
fi
beargs=()
[ "$bans" = 1 ] && beargs=("-BEpath=$tree/profiles/battleye")
(cd "$tree" && ulimit -c 0 && exec ./DayZServer -config=serverDZ.cfg -profiles=profiles \
	"${modargs[@]}" "-servermod=@dzo-admin;@dzo-client-probe" -port="$game" -nosplash -nopause -dologs -adminlog "${beargs[@]}") >"$work/server.log" 2>&1 &
server=$!
pids+=("$server")
echo "== $product server $srv, game port $game, work dir $work"
until grep -q 'CREATED -> CONNECTED' "$work/server.log" 2>/dev/null; do
	sleep 2
	kill -0 "$server" 2>/dev/null || { echo "server died" >&2; tail -20 "$work/server.log" >&2; exit 2; }
done
sleep 8
script=$(ls "$tree"/profiles/script_*.log | tail -1)
adm=$(ls "$tree"/profiles/*.ADM | tail -1)
# The experimental server logs 'Leaked BunkerBroadcastManager' without any mod.
s=0
grep -E 'SCRIPT +\(E\)' "$script" | grep -qv 'BunkerBroadcast' && s=1
verdict "scripts compile: no SCRIPT (E) beyond vanilla noise" $s

if [ "$bans" = 1 ]; then
	waitfor 60 "'$work/dzo' rcon exec --addr 127.0.0.1:$rcon --password '$rconpw' version >/dev/null 2>&1" || { bad "rcon: BattlEye answers"; exit 1; }
	ok "rcon: BattlEye answers"
	s=0
	DZO_LIVE_BE_ADDR=127.0.0.1:$rcon DZO_LIVE_BE_PASSWORD=$rconpw DZO_LIVE_BE_DIR=$tree/profiles/battleye \
		DZO_LIVE_CAPTURE=$work/capture/v1 go test -count=1 -run TestLiveBans -v ./internal/battleye >"$work/v1.log" 2>&1 || s=1
	[ "$s" = 0 ] || tail -40 "$work/v1.log" >&2
	verdict "V1: BattlEye ban file capture" $s
	echo "== V1 captures in $work/capture/v1 (use --keep)"
	start_serve
	if [ "$noclient" = 1 ]; then
		echo "== $pass passed, $fail failed"
		[ "$fail" = 0 ]
		exit
	fi
fi

if [ "$noclient" = 1 ]; then
	server_tests
	echo "== $pass passed, $fail failed"
	[ "$fail" = 0 ]
	exit
fi

"$dev/dayz-client-headless.sh" "${exp[@]}" start "$work/client" -name=dzotester "${clientmods[@]}" -connect=127.0.0.1 -port="$game" >/dev/null 2>&1
waitfor 240 "grep -q 'dzotester.*is connected' '$adm'" || { bad "client joined"; exit 1; }
ok "client joined"
waitfor 90 "'$work/dzo' player list t1 --config '$work/config.yaml' --token-file '$work/api.token' | grep -q dzotester" || { bad "players: state lists the player"; exit 1; }
ok "players: state lists the player"
# The server messages of the instance (welcome and messages:) are sent by dzo serve as soon as the
# player is in the game. Nothing in the client reports a chat line, so the screenshot is the evidence.
sleep 4
"$dev/dayz-client-headless.sh" "${exp[@]}" shot "$work/client" "$work/welcome.png" >/dev/null 2>&1 || true
id=$(dzo player list t1 | awk '/dzotester/{print $1}')
s=0
waitfor 30 "grep -Eq '\"kind\":\"connect\".*\"steam_id\":\"$id\"' '$work/events.jsonl'" || s=1
verdict "event feed: the client's join (connect with its Steam ID)" $s
if [ "$bans" = 1 ]; then
	mkdir -p "$work/capture/v2"
	"$work/dzo" rcon exec --addr "127.0.0.1:$rcon" --password "$rconpw" players >"$work/capture/v2/players.out"
	guid=$(grep dzotester "$work/capture/v2/players.out" | grep -oE '[0-9a-f]{32}' | head -1)
	echo "$id $guid" >"$work/capture/v2/pair.txt"
	want=$(python3 -I -c 'import hashlib,struct,sys;print(hashlib.md5(b"BE"+struct.pack("<Q",int(sys.argv[1]))).hexdigest())' "$id")
	s=1
	[ -n "$guid" ] && [ "$guid" = "$want" ] && s=0
	verdict "V2: GUID = md5(BE + little-endian Steam ID) ($id -> $guid)" $s
fi
if [ "$bans" = 1 ]; then
	adm_kicks() { grep -c "dzotester.*kicked" "$adm" 2>/dev/null || true; }
	client() { "$dev/dayz-client-headless.sh" "${exp[@]}" "$@" "$work/client" -name=dzotester -connect=127.0.0.1 -port="$game" >/dev/null; }
	# stop ends sway, and the game with it, in the background: start refuses while a game still runs.
	stop_client() {
		"$dev/dayz-client-headless.sh" "${exp[@]}" stop "$work/client" >/dev/null 2>&1 || true
		waitfor 90 "'$dev/dayz-client-headless.sh' status >/dev/null 2>&1"
	}
	joined() { waitfor "$1" "grep -c 'dzotester.*is connected' '$adm' | grep -qx '$2'"; }

	# V3b: a new ban kicks the player who is online, with the ban message.
	s=0
	dzo ban add --steam "$id" --reason "V3 test" --message "dzo V3 ban" --duration 10m --instance t1 | grep -q 't1: ok' || s=1
	waitfor 30 "'$work/dzo' rcon exec --addr 127.0.0.1:$rcon --password '$rconpw' players | grep -vq dzotester" || s=1
	grep -q 'dzo V3 ban' "$work/serve.log" "$adm" "$tree"/profiles/*.RPT 2>/dev/null || true # the message's place is recorded by V3a below
	verdict "V3b: a new ban kicks the online player" $s
	s=0
	grep -q "$(python3 -I -c 'import hashlib,struct,sys;print(hashlib.md5(b"BE"+struct.pack("<Q",int(sys.argv[1]))).hexdigest())' "$id") [0-9]* dzo V3 ban" "$tree/profiles/battleye/bans.txt" || s=1
	verdict "V3: bans.txt holds the derived GUID with the expiry and message" $s

	# V3a: a banned player who connects is refused.
	stop_client
	before=$(grep -c 'dzotester.*is connected' "$adm" || true)
	client start
	s=0
	sleep 60 # the client needs this long to reach the server; a refused join leaves no trace to poll for in the ADM
	[ "$(grep -c 'dzotester.*is connected' "$adm" || true)" = "$before" ] || s=1
	verdict "V3a: a banned player cannot join" $s
	"$dev/dayz-client-headless.sh" "${exp[@]}" shot "$work/client" "$work/v3a-refused.png" >/dev/null 2>&1 || true
	stop_client

	# V3c: a revoked ban lets the player in again.
	banid=$(dzo ban list --state active --query "$id" | awk 'NR == 2 {print $1}')
	dzo ban revoke "$banid" --reason "V3 done" >/dev/null
	client start
	s=0
	joined 240 "$((before + 1))" || s=1
	verdict "V3c: after the revoke the player joins" $s
	stop_client

	# V3d: an IP ban refuses the player's address (127.0.0.1 on a local test).
	dzo ban add --ip 127.0.0.1 --reason "V3 ip" --duration 10m --instance t1 >/dev/null
	before=$(grep -c 'dzotester.*is connected' "$adm" || true)
	client start
	s=0
	sleep 60 # as for V3a
	[ "$(grep -c 'dzotester.*is connected' "$adm" || true)" = "$before" ] || s=1
	verdict "V3d: an IP ban refuses the address" $s
	stop_client
	echo "== $pass passed, $fail failed (ban stage)"
	[ "$fail" = 0 ]
	exit
fi
probe() { grep -h 'DZO-PROBE player' "$script" | tail -1; }
sleep 12 # the character is still loading for a while after the join

for style in important chat notification; do
	s=0
	dzo player msg t1 "$id" "test $style" --style "$style" | grep -q ok || s=1
	verdict "message ($style)" $s
done
s=0
dzo player msg t1 all "to everyone" | grep -q ok || s=1
verdict "message to all" $s
if [ "$oldmod" = 1 ]; then
	# What the restart countdown sends (apiNotifier): a notification to everyone; the old mod ignores the new fields.
	s=0
	dzo player msg t1 all "Server restart in 5 minutes" --style notification | grep -q ok || s=1
	verdict "old mod: the countdown's notification is accepted" $s
	sleep 2
	"$dev/dayz-client-headless.sh" "${exp[@]}" shot "$work/client" "${DZO_SHOTS_DIR:-$work}/old-mod-notification.png" >/dev/null 2>&1 || true
	s=1
	out=$(dzo player msg t1 all "x" --style expansion --type toast 2>&1 || true)
	grep -q 'not available' <<<"$out" && s=0
	verdict "old mod: expansion is refused, so the countdown sends a notification" $s
	echo "== $pass passed, $fail failed (old mod stage)"
	[ -n "${DZO_SHOTS_DIR:-}" ] || keep=1
	[ "$fail" = 0 ]
	exit
fi
# Notification fields: a title and a duration; Expansion is not loaded here, so the way is refused
# (the mod's hello carries its features, see the mod's own log line).
s=0
dzo player msg t1 all "notification with a title" --style notification --title Restart --seconds 5 | grep -q ok || s=1
verdict "message (notification with title and seconds)" $s
if [ "$expansion" = 1 ]; then
	s=1
	grep -q 'DZOAdmin.*features: expansion' "$script" && s=0
	verdict "dzo-admin reports expansion" $s
	shots=${DZO_SHOTS_DIR:-$work/shots}
	mkdir -p "$shots"
	sleep 10
	"$dev/dayz-client-headless.sh" "${exp[@]}" shot "$work/client" "$shots/00-before.png" >/dev/null 2>&1 || true
	i=1
	for ty in toast baguette activity killfeed market garage; do
		s=0
		dzo player msg t1 all "Server restart in 10 minutes" --style expansion --type "$ty" --title "dzo $ty" --seconds 6 | grep -q ok || s=1
		sleep 3
		"$dev/dayz-client-headless.sh" "${exp[@]}" shot "$work/client" "$shots/0$i-expansion-$ty.png" >/dev/null 2>&1 || true
		verdict "message (expansion $ty)" $s
		sleep 8
		i=$((i + 1))
	done
	s=0
	dzo player msg t1 all "plain notification" --style notification --title notification --seconds 6 | grep -q ok || s=1
	sleep 3
	"$dev/dayz-client-headless.sh" "${exp[@]}" shot "$work/client" "$shots/07-notification.png" >/dev/null 2>&1 || true
	verdict "message (notification with Expansion loaded)" $s
	echo "screenshots in $shots"
	[ -n "${DZO_SHOTS_DIR:-}" ] || keep=1
	echo "== $pass passed, $fail failed (expansion stage)"
	[ "$fail" = 0 ]
	exit
fi
s=1
out=$(dzo player msg t1 all "no expansion here" --style expansion --type toast 2>&1 || true)
grep -q 'not available' <<<"$out" && s=0
verdict "message (expansion is refused without Expansion)" $s
s=1
grep -q 'DZOAdmin.*features:' "$script" && ! grep -q 'DZOAdmin.*features:.*expansion' "$script" && s=0
verdict "dzo-admin reports its features, without expansion" $s
sleep 2
"$dev/dayz-client-headless.sh" "${exp[@]}" shot "$work/client" "$work/messages.png" >/dev/null 2>&1 || true

s=1
if dzo player tp t1 "$id" --x 7500 --z 7500 | grep -q ok; then
	sleep 6
	dzo player list t1 | grep -Eq 'dzotester +750[0-9] +7500' && s=0
fi
verdict "teleport" $s

chemlights() { probe | sed -n 's/.*chemlight=\([0-9]*\).*/\1/p'; }
before=$(chemlights)
dzo player give t1 "$id" Chemlight_Red | grep -q ok || true
sleep 5
after=$(chemlights)
s=1
[ "$after" = "$((before + 1))" ] && s=0
verdict "spawn_item into the inventory ($before -> $after)" $s
dzo player give t1 "$id" Chemlight_Red --target hands | grep -q ok || true
sleep 5
s=1
probe | grep -q 'hands=Chemlight_Red' && s=0
verdict "spawn_item into the hands" $s
# With attachments: the item is given the kit it needs (the game's debug spawn, else the kit found in the configs).
for t in "Flashlight --attachments" "PersonalRadio" "Rangefinder --attachments"; do
	dzo player give t1 "$id" $t | grep -q ok || true
done
sleep 6
s=1
probe | grep -q 'Flashlight:[^;]*Battery' && s=0
verdict "spawn_item with attachments: a flashlight gets its battery" $s
s=1
probe | grep -q 'Rangefinder:[^;]*Battery' && s=0
verdict "spawn_item with attachments: a rangefinder gets its battery" $s
s=0
probe | grep -q 'PersonalRadio:' && s=1
verdict "spawn_item without attachments: a bare radio" $s
s=1
out=$(dzo player give t1 "$id" NoSuchThing 2>&1 || true)
case $out in *'unknown class'*) s=0 ;; esac
verdict "spawn_item refuses an unknown class" $s

# A class watch rule hooks ItemBase: an item on the ground becomes a marker.
dzo player give t1 "$id" Chemlight_Red --target ground | grep -q ok || true
s=0
waitfor 60 "curl -s -H 'Authorization: Bearer $(cat "$work/api.token")' http://127.0.0.1:$api/api/v1/instances/t1/map/markers | grep -q 'watched chemlight'" || s=1
verdict "class watch rule: a chemlight on the ground becomes a marker" $s

touch "$tree/profiles/dzo_probe/board"
s=0
waitfor 30 "grep -h 'DZO-PROBE player' '$script' | tail -1 | grep -q 'transport=true'" || s=1
verdict "probe: the player sits in a car" $s
s=1
out=$(dzo player tp t1 "$id" --x 100 --z 100 2>&1 || true)
case $out in *'in a vehicle'*) s=0 ;; esac
verdict "teleport is refused in a vehicle" $s
s=0
waitfor 30 "'$work/dzo' vehicle list t1 --config '$work/config.yaml' --token-file '$work/api.token' | grep -q 'Offroad_02.*crew 1'" || s=1
verdict "vehicle state: the car has crew 1" $s

server_tests
# Heal, fix gear and kill, last: a dead player cannot be used for anything after.
# pfield reads a number of the probe's latest player line; waitpf waits (30 s) for "<field> <test> <value>".
id=$(dzo player list t1 | awk '/dzotester/{print $1}') # server_tests reuses the variable
pfield() { probe | sed -n "s/.* $1=\([0-9]*\).*/\1/p"; }
waitpf() { local i; for i in $(seq 1 15); do [ "$(pfield "$1")" ] && [ "$(pfield "$1")" "$2" "$3" ] && return 0; sleep 2; done; return 1; }
touch "$tree/profiles/dzo_probe/hurt"
s=1
waitpf hp -lt 50 && s=0
verdict "probe: the player is hurt (hp $(pfield hp))" $s
s=1
[ "$(pfield gear)" -le 15 ] 2>/dev/null && s=0
verdict "probe: the gear is worn out (worst item at $(pfield gear)%)" $s
s=0
dzo player heal t1 "$id" | grep -q ok || s=1
waitpf hp -gt 90 || s=1
waitpf blood -gt 4000 || s=1
verdict "player heal restores health (hp $(pfield hp), blood $(pfield blood))" $s
s=0
dzo player fixgear t1 "$id" | grep -q ok || s=1
waitpf gear -eq 100 || s=1
verdict "player fixgear repairs everything (worst item at $(pfield gear)%)" $s
s=1
out=$(dzo player kill t1 "$id" 2>&1 || true)
case $out in *--yes*) s=0 ;; esac
verdict "player kill asks for --yes" $s
s=0
dzo player kill t1 "$id" --yes | grep -q ok || s=1
waitpf alive -eq 0 || s=1
verdict "player kill kills the player" $s
s=0
waitfor 30 "grep -Eq '\"kind\":\"death\".*\"target_steam_id\":\"$id\".*\"cause\":\"self\"' '$work/events.jsonl'" || s=1
verdict "event feed: the kill is a death with cause self" $s
s=1
out=$(dzo player heal t1 "$id" 2>&1 || true)
case $out in *dead*) s=0 ;; esac
verdict "player heal is refused for a dead player" $s

# The client leaves: the feed reports it with the Steam ID, although the identity may be gone by then.
"$dev/dayz-client-headless.sh" "${exp[@]}" stop "$work/client" >/dev/null 2>&1 || true
s=0
waitfor 90 "grep -Eq '\"kind\":\"disconnect\".*\"steam_id\":\"$id\"' '$work/events.jsonl'" || s=1
verdict "event feed: the client's leave (disconnect with its Steam ID)" $s
s=0
grep -q '"kind":"dropped"' "$work/events.jsonl" && s=1
verdict "event feed: no event was dropped" $s
s=0
[ -z "$(grep -o '"seq":[0-9]*' "$work/events.jsonl" | sort | uniq -d | head -1)" ] || s=1
verdict "event feed: the mod's sequence numbers never repeat (a resent batch counts once)" $s
echo "SKIP event feed: chat (the headless client cannot type; BattlEye is off here, chat is covered by the unit tests)"

echo "== $pass passed, $fail failed"
[ "$fail" = 0 ]
