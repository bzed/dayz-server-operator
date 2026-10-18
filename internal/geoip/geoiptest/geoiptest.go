// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package geoiptest builds small GeoIP databases for tests of the packages that use internal/geoip.
package geoiptest

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/geoip"
)

// Legacy opens a DB whose legacy IPv4 country database (GeoIP.dat) maps each given IPv4 prefix to a country
// code, for example {"198.51.100.0/24": "DE"}. Everything else is unknown.
func Legacy(t *testing.T, countries map[string]string) *geoip.DB {
	t.Helper()
	type node struct{ kid, leaf [2]int }
	nodes := []node{{[2]int{-1, -1}, [2]int{-1, -1}}}
	for p, code := range countries {
		pre := netip.MustParsePrefix(p)
		b := pre.Addr().As4()
		n := 0
		for i := range pre.Bits() {
			bit := int(b[i/8]>>(7-i%8)) & 1
			if i == pre.Bits()-1 {
				nodes[n].leaf[bit] = geoip.LegacyRecord(geoip.LegacyIndex(code))
				break
			}
			if nodes[n].kid[bit] < 0 {
				nodes = append(nodes, node{[2]int{-1, -1}, [2]int{-1, -1}})
				nodes[n].kid[bit] = len(nodes) - 1
			}
			n = nodes[n].kid[bit]
		}
	}
	var out []byte
	for _, n := range nodes {
		for bit := range 2 {
			v := geoip.LegacyNone
			switch {
			case n.kid[bit] >= 0:
				v = n.kid[bit]
			case n.leaf[bit] >= 0:
				v = n.leaf[bit]
			}
			out = append(out, byte(v&0xff), byte(v>>8&0xff), byte(v>>16&0xff))
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "GeoIP.dat"), out, 0o600); err != nil {
		t.Fatal(err)
	}
	db := geoip.Open(t.TempDir(), dir)
	t.Cleanup(db.Close)
	return db
}
