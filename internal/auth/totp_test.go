// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth_test

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/auth"
)

// RFC 6238 appendix B, SHA-1, cut to six digits.
func TestTOTPRFC6238(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, v := range []struct {
		unix int64
		code string
		step int64
	}{{59, "287082", 1}, {1111111109, "081804", 37037036}, {1234567890, "005924", 41152263}} {
		step, ok := auth.TOTPStep(secret, v.code, time.Unix(v.unix, 0))
		require.True(t, ok, v.code)
		assert.Equal(t, v.step, step, v.code)
	}
	_, ok := auth.TOTPStep(secret, "287083", time.Unix(59, 0))
	assert.False(t, ok)
}

func TestTOTPWindow(t *testing.T) {
	secret := []byte("12345678901234567890")
	at := func(unix int64) time.Time { return time.Unix(unix, 0) }
	// the code of step 37037036 (T=1111111109) one step early, one step late, two steps off
	for unix, want := range map[int64]bool{1111111109: true, 1111111109 + 30: true, 1111111109 - 30: true, 1111111109 + 60: false, 1111111109 - 60: false} {
		_, ok := auth.TOTPStep(secret, "081804", at(unix))
		assert.Equal(t, want, ok, fmt.Sprint(unix))
	}
}

func TestTOTPRejectsNonDigits(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, c := range []string{"", "28708", "2870822", "28708a", " 287082", "+87082", "２８７０８２"} {
		_, ok := auth.TOTPStep(secret, c, time.Unix(59, 0))
		assert.False(t, ok, "%q", c)
	}
}

func TestTOTPSecretAndURI(t *testing.T) {
	s, err := auth.NewTOTPSecret()
	require.NoError(t, err)
	assert.Len(t, s, 20)
	s2, _ := auth.NewTOTPSecret()
	assert.NotEqual(t, s, s2)

	u, err := url.Parse(auth.TOTPURI([]byte("12345678901234567890"), "dzo main", "Ann <a@b>"))
	require.NoError(t, err)
	assert.Equal(t, "otpauth", u.Scheme)
	assert.Equal(t, "totp", u.Host)
	assert.Equal(t, "/dzo main:Ann <a@b>", u.Path)
	q := u.Query()
	assert.Equal(t, "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", q.Get("secret"))
	assert.Equal(t, "dzo main", q.Get("issuer"))
	assert.Equal(t, "SHA1", q.Get("algorithm"))
	assert.Equal(t, "6", q.Get("digits"))
	assert.Equal(t, "30", q.Get("period"))
}
