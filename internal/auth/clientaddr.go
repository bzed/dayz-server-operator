// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// Proxies are the reverse proxies whose forwarding headers are believed.
type Proxies []netip.Prefix

// ParseProxies reads addresses and CIDR networks.
func ParseProxies(entries []string) (Proxies, error) {
	var out Proxies
	for _, e := range entries {
		if p, err := netip.ParsePrefix(e); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(e)
		if err != nil {
			return nil, fmt.Errorf("%q is not an address or a CIDR network", e)
		}
		a = a.Unmap().WithZone("")
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

func (p Proxies) trusted(a netip.Addr) bool {
	for _, n := range p {
		if n.Contains(a) {
			return true
		}
	}
	return false
}

// parseAddr reads an address, dropping a zone and unmapping ::ffff:a.b.c.d.
func parseAddr(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, false
	}
	return a.WithZone("").Unmap(), true
}

// ClientAddr is the address of the browser behind r. Forwarding headers count
// only when the connection comes from a trusted proxy: then it is the
// right-most X-Forwarded-For entry that is not itself a trusted proxy (a client
// cannot inject one by sending the header), the left-most when every hop is
// trusted, or X-Real-IP when there is no X-Forwarded-For. A value that does not
// parse falls back to the proxy's address. The result is always valid.
func (p Proxies) ClientAddr(r *http.Request) netip.Addr {
	peer := netip.AddrFrom4([4]byte{})
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		peer = ap.Addr().WithZone("").Unmap()
	} else if a, ok := parseAddr(r.RemoteAddr); ok {
		peer = a
	}
	if len(p) == 0 || !p.trusted(peer) {
		return peer
	}
	if h := r.Header.Values("X-Forwarded-For"); len(h) > 0 {
		parts := strings.Split(strings.Join(h, ","), ",")
		var first netip.Addr
		for i := len(parts) - 1; i >= 0; i-- {
			a, ok := parseAddr(parts[i])
			if !ok {
				return peer
			}
			if !p.trusted(a) {
				return a
			}
			first = a
		}
		return first
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		if a, ok := parseAddr(v); ok {
			return a
		}
	}
	return peer
}
