<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# V1: BattlEye's bans.txt, captured from a stable DayZ server

Captured with `scripts/test-client.sh --bans --no-client --keep` (BattlEye on, RCon on localhost) and
`internal/battleye/livebans_test.go`. The files next to this one are the raw captures.

- **Q1 Line format.** `<GUID or IP> <expiry> <reason>`, single spaces, one ban per line, no header:
  `0123456789abcdef0123456789abcdef -1 permanent reason with spaces` (`bans.txt`). The reason is the rest
  of the line.
- **Q2 Permanent.** `-1`.
- **Q3 Timed.** An absolute Unix time in seconds: `fedcba9876543210fedcba9876543210 1792278317 timed ...`
  (added with 5 minutes). `bans` lists the minutes left (`4`), and `perm` for `-1`.
- **Q4 Reason text.** Spaces, UTF-8 (`äöü`), `"` and `%` survive: `timed äöü "quoted" 100%`. The reason
  is **cut to 64 characters**: the 300-character reason of the capture is 64 `x` in `bans.txt`. dzo must
  not rely on longer reasons surviving.
- **Q5 IPv6.** `addBan 2001:db8::7 0 ipv6 ban` is **silently not added** (neither in `bans` nor in the
  file). dzo bans IPv4 addresses only.
- **Q6 Comments.** A `//` line in a file loaded with `loadBans` is ignored (`bans-after-load.out` lists
  exactly 3 bans), and `writeBans` does not keep it (`bans.txt.after-expiry` has no comment). So a header
  comment in `bans.txt` does not survive a `writeBans`; dzo detects outside changes with a hash sidecar.
- **Q7 Expired bans.** `bans` still lists an expired ban, with `-` as the minutes left
  (`bans-after-expiry.out`), and `writeBans` drops it (`bans.txt.after-expiry`).
- **Q8 addBan writes the file.** The file already holds the new bans right after `addBan`, without
  `writeBans` (`bans-file-after-add.txt`).
- **Q9 Refused join.** A banned player is kicked at the join with the BattlEye dialog "You were kicked off
  the game. BattlEye (Admin Ban (<reason>))", where `<reason>` is the (64 character) message of the
  `bans.txt` line. A revoke lets the player join again without a restart, and an IPv4 ban refuses the
  address the same way (V3a, V3c, V3d of `scripts/test-client.sh --bans`).
