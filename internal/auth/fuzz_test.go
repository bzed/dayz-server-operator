// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func FuzzClientAddr(f *testing.F) {
	f.Add("10.0.0.1:80", "6.6.6.6, 1.2.3.4", "")
	f.Add("[2001:db8::1]:80", "fe80::1%eth0", "x")
	f.Add("garbage", ",,,", "::ffff:1.2.3.4")
	f.Add("", "\x00", "")
	p, _ := ParseProxies([]string{"10.0.0.1", "10.0.1.0/24", "2001:db8::/32"})
	f.Fuzz(func(t *testing.T, remote, xff, real string) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-For", xff)
		r.Header.Set("X-Real-IP", real)
		require.True(t, p.ClientAddr(r).IsValid(), "%q %q %q", remote, xff, real)
	})
}

func FuzzSteamClaimedID(f *testing.F) {
	f.Add("https://steamcommunity.com/openid/id/76561198000000001")
	f.Add("https://steamcommunity.com/openid/id/")
	f.Add("")
	f.Fuzz(func(t *testing.T, claimed string) {
		if id, ok := steamIDFromClaimed(claimed); ok {
			require.Len(t, id, 17, claimed)
		}
	})
}

func FuzzCheckSigned(f *testing.F) {
	f.Add("signed,op_endpoint,claimed_id,identity,return_to,response_nonce", "id_res")
	f.Add("", "")
	f.Fuzz(func(_ *testing.T, signed, mode string) {
		q := url.Values{"openid.signed": {signed}, "openid.mode": {mode}}
		_ = signedCovers(q)
	})
}
