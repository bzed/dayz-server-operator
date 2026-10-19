// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package geoip

import (
	"encoding/binary"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// trie is a binary search tree over address bits, the shape of both database
// families. Leaves hold a value; nodes are numbered in creation order.
type trie struct {
	kids [][2]int // child node, or -1
	leaf [][2]int // leaf value, or -1
}

func newTrie() *trie { t := &trie{}; t.node(); return t }

func (t *trie) node() int {
	t.kids = append(t.kids, [2]int{-1, -1})
	t.leaf = append(t.leaf, [2]int{-1, -1})
	return len(t.kids) - 1
}

// insert puts value under the first bits bits of addr.
func (t *trie) insert(addr netip.Addr, bits, value int) {
	b := addr.AsSlice()
	n := 0
	for i := range bits {
		bit := int(b[i/8]>>(7-i%8)) & 1
		if i == bits-1 {
			t.leaf[n][bit] = value
			return
		}
		if t.kids[n][bit] < 0 {
			c := t.node()
			t.kids[n][bit] = c
		}
		n = t.kids[n][bit]
	}
}

// legacyFile writes a country database with the given address -> country index entries.
func legacyFile(t testing.TB, path string, entries map[string]int) {
	t.Helper()
	tr := newTrie()
	for p, idx := range entries {
		pre := netip.MustParsePrefix(p)
		tr.insert(pre.Addr(), pre.Bits(), countryBegin+idx)
	}
	var out []byte
	rec := func(v int) { out = append(out, byte(v), byte(v>>8), byte(v>>16)) }
	for i := range tr.kids {
		for bit := range 2 {
			switch {
			case tr.kids[i][bit] >= 0:
				rec(tr.kids[i][bit])
			case tr.leaf[i][bit] >= 0:
				rec(tr.leaf[i][bit])
			default:
				rec(countryBegin) // unknown
			}
		}
	}
	require.NoError(t, os.WriteFile(path, out, 0o600))
}

func countryIndex(code string) int {
	for i, c := range legacyCountries {
		if c == code {
			return i
		}
	}
	return -1
}

func TestLegacyCountries(t *testing.T) {
	require.Len(t, legacyCountries, 256)
	require.Equal(t, "--", legacyCountries[0])
	require.Equal(t, "US", legacyCountries[countryIndex("US")])
	require.Equal(t, 225, countryIndex("US")) // libGeoIP's order
	require.Equal(t, 56, countryIndex("DE"))
}

func TestLegacyFallbackReadsCountryOnly(t *testing.T) {
	leg := t.TempDir()
	legacyFile(t, filepath.Join(leg, "GeoIP.dat"), map[string]int{"77.0.0.0/8": countryIndex("DE"), "8.0.0.0/8": countryIndex("US")})
	legacyFile(t, filepath.Join(leg, "GeoIPv6.dat"), map[string]int{"2001:4860::/32": countryIndex("US")})
	db := Open(t.TempDir(), leg)
	defer db.Close()

	require.True(t, db.Available())
	require.Equal(t, Info{Country: "DE"}, db.Lookup("77.1.2.3"))
	require.Equal(t, Info{Country: "US"}, db.Lookup("8.8.8.8"))
	require.Equal(t, Info{Country: "US"}, db.Lookup("2001:4860:4860::8888"))
	require.Equal(t, Info{Country: "DE"}, db.Lookup("::ffff:77.1.2.3"), "an IPv4-mapped address uses the IPv4 database")
	require.True(t, db.Lookup("9.9.9.9").Empty(), "not in the tree")
	require.True(t, db.Lookup("2606:4700::1").Empty())
}

func TestLookupRefusesNonPublicAndGarbage(t *testing.T) {
	leg := t.TempDir()
	legacyFile(t, filepath.Join(leg, "GeoIP.dat"), map[string]int{"0.0.0.0/1": countryIndex("DE"), "128.0.0.0/1": countryIndex("DE")})
	db := Open(t.TempDir(), leg)
	defer db.Close()
	for _, ip := range []string{"", "nonsense", "10.0.0.1", "192.168.1.1", "127.0.0.1", "169.254.1.1", "::1", "fe80::1", "0.0.0.0"} {
		require.True(t, db.Lookup(ip).Empty(), ip)
	}
	require.Equal(t, "DE", db.Lookup(" 77.1.2.3 ").Country)
}

func TestNilAndEmptyDB(t *testing.T) {
	var nilDB *DB
	require.True(t, nilDB.Lookup("8.8.8.8").Empty())
	require.False(t, nilDB.Available())
	db := Open(t.TempDir(), t.TempDir())
	defer db.Close()
	require.False(t, db.Available())
	require.True(t, db.Lookup("8.8.8.8").Empty())
}

func TestBrokenLegacyFileIsIgnored(t *testing.T) {
	leg := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(leg, "GeoIP.dat"), []byte("xx"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(leg, "GeoIPv6.dat"), make([]byte, 60), 0o600)) // all nodes point at node 0
	db := Open(t.TempDir(), leg)
	defer db.Close()
	require.True(t, db.Lookup("8.8.8.8").Empty())
	_ = db.Lookup("2001:db8::1") // must not panic
}

func TestMMDBGivesCityLocationAndASN(t *testing.T) {
	dir := t.TempDir()
	cityDB(t, filepath.Join(dir, "GeoLite2-City.mmdb"))
	asnDB(t, filepath.Join(dir, "GeoLite2-ASN.mmdb"))
	leg := t.TempDir() // the legacy files are not used while GeoLite2 knows the address
	legacyFile(t, filepath.Join(leg, "GeoIP.dat"), map[string]int{"8.0.0.0/8": countryIndex("FR"), "9.0.0.0/8": countryIndex("NL")})
	db := Open(dir, leg)
	defer db.Close()

	got := db.Lookup("8.8.8.8")
	require.Equal(t, "US", got.Country)
	require.Equal(t, "United States", got.CountryName)
	require.Equal(t, "Mountain View", got.City)
	require.NotNil(t, got.Lat)
	require.InDelta(t, 37.386, *got.Lat, 1e-9)
	require.InDelta(t, -122.0838, *got.Lon, 1e-9)
	require.Equal(t, uint(15169), got.ASN)
	require.Equal(t, "Google LLC", got.ASOrg)
	require.Equal(t, Info{Country: "US", CountryName: "United States"}, got.CountryOnly())

	// GeoLite2 does not know 9.9.9.9; the legacy database does, and it has no ASN.
	require.Equal(t, Info{Country: "NL"}, db.Lookup("9.9.9.9"))
}

func TestCountryMMDBWithoutCity(t *testing.T) {
	dir := t.TempDir()
	countryDB(t, filepath.Join(dir, "GeoLite2-Country.mmdb"))
	db := Open(dir, t.TempDir())
	defer db.Close()
	require.Equal(t, Info{Country: "US", CountryName: "United States"}, db.Lookup("8.8.8.8"))
}

func TestFilesAreReopenedOnChange(t *testing.T) {
	dir, leg := t.TempDir(), t.TempDir()
	now := time.Now()
	db := Open(dir, leg)
	defer db.Close()
	db.now = func() time.Time { return now }
	require.True(t, db.Lookup("8.8.8.8").Empty())

	cityDB(t, filepath.Join(dir, "GeoLite2-City.mmdb")) // geoipupdate ran
	require.True(t, db.Lookup("8.8.8.8").Empty(), "not looked at again before the interval")
	now = now.Add(2 * recheck)
	require.Equal(t, "US", db.Lookup("8.8.8.8").Country)

	now = now.Add(2 * recheck) // nothing changed: the same readers stay
	require.Equal(t, "US", db.Lookup("8.8.8.8").Country)

	require.NoError(t, os.Remove(filepath.Join(dir, "GeoLite2-City.mmdb")))
	now = now.Add(2 * recheck)
	require.True(t, db.Lookup("8.8.8.8").Empty())
}

// --- a minimal MaxMind DB writer: IPv4 tree, 24-bit records ---

func mmdbString(s string) []byte { return append(mmdbCtl(2, len(s)), s...) }

func mmdbCtl(typ, size int) []byte {
	var ext []byte
	if size >= 29 { // one extra size byte, enough for the strings here
		ext, size = []byte{byte(size - 29)}, 29
	}
	if typ <= 7 {
		return append([]byte{byte(typ<<5 | size)}, ext...)
	}
	return append([]byte{byte(size), byte(typ - 7)}, ext...)
}

func mmdbMap(kv ...[]byte) []byte {
	out := mmdbCtl(7, len(kv)/2)
	for _, b := range kv {
		out = append(out, b...)
	}
	return out
}

func mmdbUint(typ int, v uint64) []byte { // 5 = uint16, 6 = uint32
	var raw []byte
	for ; v > 0; v >>= 8 {
		raw = append([]byte{byte(v)}, raw...)
	}
	return append(mmdbCtl(typ, len(raw)), raw...)
}

func mmdbDouble(f float64) []byte {
	b := binary.BigEndian.AppendUint64(nil, math.Float64bits(f))
	return append(mmdbCtl(3, 8), b...)
}

// writeMMDB stores one record for 8.0.0.0/8.
func writeMMDB(t testing.TB, path, dbType string, record []byte) {
	t.Helper()
	tr := newTrie()
	tr.insert(netip.MustParseAddr("8.0.0.0"), 8, 0) // leaf 0 = the data at offset 0
	nodes := len(tr.kids)
	var tree []byte
	rec := func(v int) { tree = append(tree, byte(v>>16), byte(v>>8), byte(v)) }
	for i := range tr.kids {
		for bit := range 2 {
			switch {
			case tr.kids[i][bit] >= 0:
				rec(tr.kids[i][bit])
			case tr.leaf[i][bit] >= 0:
				rec(nodes + 16 + tr.leaf[i][bit])
			default:
				rec(nodes) // no data
			}
		}
	}
	meta := mmdbMap(
		mmdbString("node_count"), mmdbUint(6, uint64(nodes)),
		mmdbString("record_size"), mmdbUint(5, 24),
		mmdbString("ip_version"), mmdbUint(5, 4),
		mmdbString("database_type"), mmdbString(dbType),
		mmdbString("binary_format_major_version"), mmdbUint(5, 2),
		mmdbString("binary_format_minor_version"), mmdbUint(5, 0),
		mmdbString("languages"), append(mmdbCtl(11, 1), mmdbString("en")...),
		mmdbString("description"), mmdbMap(),
	)
	file := append(append(append(tree, make([]byte, 16)...), record...), []byte("\xab\xcd\xefMaxMind.com")...)
	require.NoError(t, os.WriteFile(path, append(file, meta...), 0o600))
}

func names(en string) []byte {
	return mmdbMap(mmdbString("names"), mmdbMap(mmdbString("en"), mmdbString(en)))
}

func countryRec() []byte {
	return mmdbMap(mmdbString("country"), mmdbMap(mmdbString("iso_code"), mmdbString("US"), mmdbString("names"), mmdbMap(mmdbString("en"), mmdbString("United States"))))
}

func countryDB(t testing.TB, path string) { writeMMDB(t, path, "GeoLite2-Country", countryRec()) }

func cityDB(t testing.TB, path string) {
	rec := mmdbMap(
		mmdbString("city"), names("Mountain View"),
		mmdbString("country"), mmdbMap(mmdbString("iso_code"), mmdbString("US"), mmdbString("names"), mmdbMap(mmdbString("en"), mmdbString("United States"))),
		mmdbString("location"), mmdbMap(mmdbString("latitude"), mmdbDouble(37.386), mmdbString("longitude"), mmdbDouble(-122.0838)),
	)
	writeMMDB(t, path, "GeoLite2-City", rec)
}

func asnDB(t testing.TB, path string) {
	writeMMDB(t, path, "GeoLite2-ASN", mmdbMap(
		mmdbString("autonomous_system_number"), mmdbUint(6, 15169),
		mmdbString("autonomous_system_organization"), mmdbString("Google LLC"),
	))
}
