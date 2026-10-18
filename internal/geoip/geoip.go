// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package geoip looks up where an IP address is: the country (for the flag
// next to a player), and with MaxMind's GeoLite2 databases also the city, the
// location and the autonomous system.
//
// Two database families are read, and they are different formats:
//
//   - GeoLite2-City, -Country and -ASN (.mmdb, MaxMind DB), kept up to date by
//     geoipupdate in /var/lib/GeoIP. They give everything.
//   - GeoIP.dat and GeoIPv6.dat (legacy binary trees) from Debian's
//     geoip-database in /usr/share/GeoIP. They give the country only and are
//     the fallback when no GeoLite2 country data is found.
//
// The files are reopened when they change, so geoipupdate needs no restart.
package geoip

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/geoip2-golang/v2"
)

// Default directories of the two families.
const (
	DefaultDir       = "/var/lib/GeoIP"
	DefaultLegacyDir = "/usr/share/GeoIP"
)

// recheck is how often the files are looked at for a change.
const recheck = time.Minute

// Info is what is known about an address. Any field can be empty: a legacy
// database has only Country, and a private address has nothing.
type Info struct {
	Country     string   `json:"country,omitempty"` // ISO 3166-1 alpha-2, upper case
	CountryName string   `json:"country_name,omitempty"`
	City        string   `json:"city,omitempty"`
	Lat         *float64 `json:"lat,omitempty"`
	Lon         *float64 `json:"lon,omitempty"`
	ASN         uint     `json:"asn,omitempty"`
	ASOrg       string   `json:"as_org,omitempty"`
}

// Empty reports whether nothing is known.
func (i Info) Empty() bool { return i.Country == "" && i.City == "" && i.ASN == 0 }

// CountryOnly drops everything but the country.
func (i Info) CountryOnly() Info {
	return Info{Country: i.Country, CountryName: i.CountryName}
}

type fileState struct {
	mod  time.Time
	size int64
}

// DB is a set of GeoIP databases. The zero value finds nothing; use Open. A
// DB is safe for concurrent use.
type DB struct {
	dir, legacyDir string
	now            func() time.Time

	mu      sync.Mutex
	checked time.Time
	state   map[string]fileState
	city    *geoip2.Reader
	country *geoip2.Reader
	asn     *geoip2.Reader
	v4, v6  *legacyDB
}

// Open reads the databases found in dir (GeoLite2 *.mmdb) and legacyDir
// (GeoIP.dat, GeoIPv6.dat). Missing files are not an error: they are looked
// for again later, when geoipupdate or the package has put them there.
func Open(dir, legacyDir string) *DB {
	d := &DB{dir: dir, legacyDir: legacyDir, now: time.Now}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reload()
	return d
}

// Close releases the files.
func (d *DB) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closeAll()
}

func (d *DB) closeAll() {
	for _, r := range []*geoip2.Reader{d.city, d.country, d.asn} {
		if r != nil {
			_ = r.Close() // read-only
		}
	}
	for _, l := range []*legacyDB{d.v4, d.v6} {
		if l != nil {
			_ = l.Close() // read-only
		}
	}
	d.city, d.country, d.asn, d.v4, d.v6 = nil, nil, nil, nil, nil
}

func (d *DB) paths() []string {
	return []string{
		filepath.Join(d.dir, "GeoLite2-City.mmdb"),
		filepath.Join(d.dir, "GeoLite2-Country.mmdb"),
		filepath.Join(d.dir, "GeoLite2-ASN.mmdb"),
		filepath.Join(d.legacyDir, "GeoIP.dat"),
		filepath.Join(d.legacyDir, "GeoIPv6.dat"),
	}
}

func stat(p string) fileState {
	st, err := os.Stat(p)
	if err != nil {
		return fileState{}
	}
	return fileState{mod: st.ModTime(), size: st.Size()}
}

// refresh reopens the databases if a file appeared, changed or went away.
// Called with d.mu held, at most once per recheck.
func (d *DB) refresh() {
	if t := d.now(); t.Sub(d.checked) < recheck {
		return
	}
	for _, p := range d.paths() {
		if d.state[p] != stat(p) {
			d.reload()
			return
		}
	}
	d.checked = d.now()
}

func (d *DB) reload() {
	d.closeAll()
	d.checked = d.now()
	d.state = map[string]fileState{}
	p := d.paths()
	for _, f := range p {
		d.state[f] = stat(f)
	}
	d.city = openMMDB(p[0])
	d.country = openMMDB(p[1])
	d.asn = openMMDB(p[2])
	d.v4 = openLegacyDB(p[3], 32)
	d.v6 = openLegacyDB(p[4], 128)
}

func openMMDB(path string) *geoip2.Reader {
	r, err := geoip2.Open(path)
	if err != nil {
		return nil
	}
	return r
}

func openLegacyDB(path string, depth int) *legacyDB {
	l, err := openLegacy(path, depth)
	if err != nil {
		return nil
	}
	return l
}

// Available reports whether any database is loaded, and so whether the
// lookups can find anything.
func (d *DB) Available() bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.refresh()
	return d.city != nil || d.country != nil || d.asn != nil || d.v4 != nil || d.v6 != nil
}

// Lookup returns what is known about an address given as text. An address
// that is not public (private, loopback, link-local) or does not parse yields
// an empty Info.
func (d *DB) Lookup(ip string) Info {
	if d == nil {
		return Info{}
	}
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return Info{}
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() {
		return Info{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.refresh()
	var out Info
	switch {
	case d.city != nil:
		if c, err := d.city.City(a); err == nil && c.HasData() {
			out.Country = strings.ToUpper(c.Country.ISOCode)
			out.CountryName = c.Country.Names.English
			out.City = c.City.Names.English
			if c.Location.HasCoordinates() {
				lat, lon := *c.Location.Latitude, *c.Location.Longitude
				out.Lat, out.Lon = &lat, &lon
			}
		}
	case d.country != nil:
		if c, err := d.country.Country(a); err == nil && c.HasData() {
			out.Country = strings.ToUpper(c.Country.ISOCode)
			out.CountryName = c.Country.Names.English
		}
	}
	if out.Country == "" { // no GeoLite2 data: the legacy databases know the country
		l := d.v6
		if a.Is4() {
			l = d.v4
		}
		if l != nil {
			out.Country = l.country(a)
		}
	}
	if d.asn != nil {
		if r, err := d.asn.ASN(a); err == nil && r.HasData() {
			out.ASN, out.ASOrg = r.AutonomousSystemNumber, r.AutonomousSystemOrganization
		}
	}
	return out
}
