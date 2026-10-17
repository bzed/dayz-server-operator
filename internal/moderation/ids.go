// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"crypto/md5" //nolint:gosec // BattlEye defines the GUID as an MD5; nothing here relies on it for security
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// ValidSteamID reports whether s is a Steam64 ID of an individual account (17 digits, 7656119…).
func ValidSteamID(s string) bool {
	if len(s) != 17 || !strings.HasPrefix(s, "7656119") {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

// ValidGUID reports whether s is a BattlEye GUID as the server prints it: 32 lowercase hex digits.
func ValidGUID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// ParseIP returns the canonical text of an IPv4 address. BattlEye bans single addresses; ranges,
// ports, IPv6 and the unspecified address are refused.
func ParseIP(s string) (string, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return "", invalid("invalid IP address %q", s)
	}
	a = a.Unmap()
	if !a.Is4() || a.IsUnspecified() {
		return "", invalid("IP bans take a single IPv4 address, not %q", s)
	}
	return a.String(), nil
}

// DeriveGUID returns the BattlEye GUID of a Steam ID: md5("BE" + the ID as little-endian uint64),
// verified against a real server (testdata/v2).
func DeriveGUID(steamID string) (string, error) {
	if !ValidSteamID(steamID) {
		return "", invalid("invalid Steam ID %q", steamID)
	}
	id, err := strconv.ParseUint(steamID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("moderation: steam id: %w", err)
	}
	var b [10]byte
	b[0], b[1] = 'B', 'E'
	binary.LittleEndian.PutUint64(b[2:], id)
	sum := md5.Sum(b[:]) //nolint:gosec // see the import
	return hex.EncodeToString(sum[:]), nil
}

// InvalidError is a request the store refuses: the API answers 400 with its text.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &InvalidError{Msg: fmt.Sprintf(format, a...)} }
