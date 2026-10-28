// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 specifies HMAC-SHA1, which authenticator apps expect
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"time"
)

const totpPeriod = 30

// NewTOTPSecret returns a fresh 20-byte secret.
func NewTOTPSecret() ([]byte, error) {
	s := make([]byte, 20)
	if _, err := rand.Read(s); err != nil {
		return nil, fmt.Errorf("totp secret: %w", err)
	}
	return s, nil
}

// TOTPURI is the otpauth:// address an authenticator app takes (as a QR code).
func TOTPURI(secret []byte, issuer, account string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{"secret": {base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)},
		"issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // G115: steps are positive times
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1000000)
}

// TOTPStep checks a six-digit code against the steps around now (one step
// early or late is fine) and returns the step it matched. Storing the step and
// refusing it a second time is the caller's job.
func TOTPStep(secret []byte, code string, now time.Time) (step int64, ok bool) {
	if len(code) != 6 {
		return 0, false
	}
	for i := 0; i < 6; i++ {
		if code[i] < '0' || code[i] > '9' {
			return 0, false
		}
	}
	cur := now.Unix() / totpPeriod
	found := int64(-1)
	for _, s := range []int64{cur - 1, cur, cur + 1} {
		if s < 0 {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, s)), []byte(code)) == 1 {
			found = s
		}
	}
	return found, found >= 0
}
