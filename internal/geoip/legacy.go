// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package geoip

import (
	"fmt"
	"net/netip"
	"os"
)

const (
	// countryBegin is the first record value of a country edition that is a
	// country (index + countryBegin) and not the next node of the tree.
	countryBegin = 16776960
	recordLen    = 3 // bytes per record; a node is two records
)

// legacyDB reads a legacy GeoIP country database (GeoIP.dat for IPv4,
// GeoIPv6.dat for IPv6): a binary search tree with 3-byte records, one bit of
// the address per level. These files hold the country only.
type legacyDB struct {
	f     *os.File
	size  int64
	depth int // 32 for IPv4, 128 for IPv6
}

func openLegacy(path string, depth int) (*legacyDB, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the path is the configured GeoIP directory plus a fixed name
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close() // the Stat error is the one to report
		return nil, err
	}
	if st.Size() < 2*recordLen {
		_ = f.Close() // reporting the format error
		return nil, fmt.Errorf("geoip: %s is too short for a country database", path)
	}
	return &legacyDB{f: f, size: st.Size(), depth: depth}, nil
}

func (l *legacyDB) Close() error { return l.f.Close() }

// country returns the ISO code of ip ("" if unknown). ip must match the
// database: IPv4 for the 32-level one, IPv6 for the 128-level one.
func (l *legacyDB) country(ip netip.Addr) string {
	b := ip.AsSlice()
	if len(b)*8 != l.depth {
		return ""
	}
	var rec [2 * recordLen]byte
	node := int64(0)
	for level := range l.depth {
		off := node * 2 * recordLen
		if off+2*recordLen > l.size {
			return ""
		}
		if _, err := l.f.ReadAt(rec[:], off); err != nil {
			return ""
		}
		bit := int(b[level/8]>>(7-level%8)) & 1
		v := int64(rec[bit*recordLen]) | int64(rec[bit*recordLen+1])<<8 | int64(rec[bit*recordLen+2])<<16
		if v >= countryBegin {
			idx := v - countryBegin
			if idx <= 0 || idx >= int64(len(legacyCountries)) {
				return ""
			}
			return legacyCountries[idx]
		}
		node = v
	}
	return ""
}

// LegacyIndex is the position of an ISO country code in the legacy databases' country table, -1 if it has none.
// It lets tests write such a database.
func LegacyIndex(code string) int {
	for i, c := range legacyCountries {
		if c == code {
			return i
		}
	}
	return -1
}

// LegacyNone is the tree record for "unknown".
const LegacyNone = countryBegin

// LegacyRecord is the record that ends a lookup in the country with index idx.
func LegacyRecord(idx int) int { return countryBegin + idx }
