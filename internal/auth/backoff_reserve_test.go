// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/auth"
)

func TestBackoffReserveIsAtomic(t *testing.T) {
	now := time.Unix(1000, 0)
	b := auth.NewBackoff(func() time.Time { return now })
	passed := 0
	for i := 0; i < 20; i++ { // no Fail in between: the attempts are all in flight
		if b.Reserve("ip:x") == 0 {
			passed++
		}
	}
	assert.Equal(t, 6, passed, "only the free attempts and the one that starts the wait pass, however many arrive before any ends")
	assert.Equal(t, time.Second, b.Wait("ip:x"))
	assert.NotZero(t, b.Reserve("ip:x", "acct:y"))
	assert.Zero(t, b.Wait("acct:y"), "a refused attempt counts on no key")
}

func TestBackoffRefund(t *testing.T) {
	now := time.Unix(1000, 0)
	b := auth.NewBackoff(func() time.Time { return now })
	for i := 0; i < 50; i++ {
		require.Zero(t, b.Reserve("ip:x"))
		b.Refund("ip:x") // successful logins do not use up the free attempts
	}
	for i := 0; i < 6; i++ {
		require.Zero(t, b.Reserve("ip:x"))
	}
	assert.NotZero(t, b.Reserve("ip:x"))
	b.Refund("ip:x")
	assert.Zero(t, b.Wait("ip:x"), "a refund brings a free attempt back")
	b.Refund("nothing")
}
