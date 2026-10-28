// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/auth"
)

func req(remote string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

func TestClientAddr(t *testing.T) {
	proxies, err := auth.ParseProxies([]string{"10.0.0.1", "10.0.1.0/24", "2001:db8::/32"})
	require.NoError(t, err)
	xff := func(v string) map[string]string { return map[string]string{"X-Forwarded-For": v} }
	for name, c := range map[string]struct {
		proxies auth.Proxies
		r       *http.Request
		want    string
	}{
		"no proxies":                  {nil, req("192.0.2.9:1234", xff("1.1.1.1")), "192.0.2.9"},
		"trusted proxy":               {proxies, req("10.0.0.1:80", xff("6.6.6.6, 1.2.3.4")), "1.2.3.4"},
		"forged left part ignored":    {proxies, req("10.0.0.1:80", xff("9.9.9.9, 6.6.6.6, 1.2.3.4")), "1.2.3.4"},
		"chain of proxies":            {proxies, req("10.0.0.1:80", xff("1.2.3.4, 10.0.1.7")), "1.2.3.4"},
		"all hops trusted":            {proxies, req("10.0.0.1:80", xff("10.0.1.5, 10.0.1.7")), "10.0.1.5"},
		"real ip without xff":         {proxies, req("10.0.0.1:80", map[string]string{"X-Real-IP": "1.2.3.4"}), "1.2.3.4"},
		"real ip ignored with xff":    {proxies, req("10.0.0.1:80", map[string]string{"X-Real-IP": "5.5.5.5", "X-Forwarded-For": "1.2.3.4"}), "1.2.3.4"},
		"garbage":                     {proxies, req("10.0.0.1:80", xff("not-an-address")), "10.0.0.1"},
		"garbage among good":          {proxies, req("10.0.0.1:80", xff("1.2.3.4, garbage")), "10.0.0.1"},
		"empty entry":                 {proxies, req("10.0.0.1:80", xff("1.2.3.4, ")), "10.0.0.1"},
		"garbage real ip":             {proxies, req("10.0.0.1:80", map[string]string{"X-Real-IP": "x"}), "10.0.0.1"},
		"untrusted peer sends xff":    {proxies, req("192.0.2.9:80", xff("1.2.3.4")), "192.0.2.9"},
		"trusted without headers":     {proxies, req("10.0.0.1:80", nil), "10.0.0.1"},
		"ipv6 client":                 {proxies, req("[2001:db8::1]:80", xff("2a00::1")), "2a00::1"},
		"ipv6 zone dropped":           {proxies, req("10.0.0.1:80", xff("fe80::1%eth0")), "fe80::1"},
		"v4 in v6 unmapped":           {proxies, req("10.0.0.1:80", xff("::ffff:1.2.3.4")), "1.2.3.4"},
		"peer v4 in v6 matches proxy": {proxies, req("[::ffff:10.0.0.1]:80", xff("1.2.3.4")), "1.2.3.4"},
		"peer without port":           {proxies, req("192.0.2.9", nil), "192.0.2.9"},
	} {
		t.Run(name, func(t *testing.T) {
			got := c.proxies.ClientAddr(c.r)
			assert.True(t, got.IsValid())
			assert.Equal(t, c.want, got.String())
		})
	}
}

func TestParseProxies(t *testing.T) {
	_, err := auth.ParseProxies([]string{"10.0.0.1", "nonsense"})
	require.ErrorContains(t, err, `"nonsense"`)
	p, err := auth.ParseProxies(nil)
	require.NoError(t, err)
	assert.Empty(t, p)
}
