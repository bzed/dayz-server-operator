// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Attempts in flight at the same time count toward the free ones: the backoff is not checked and counted
// in two separate steps.
func TestConcurrentPasswordAttemptsAreThrottled(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	l.srv.hashSlots = make(chan struct{}, 64) // this test is about the backoff, not the slots
	const n = 16
	var wg sync.WaitGroup
	codes := make(chan int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, _ := l.passwordStep("user"+strconv.Itoa(i), "wrong horse battery", "X-Dzo-Client", "192.0.2.80")
			codes <- code
		}()
	}
	wg.Wait()
	close(codes)
	count := map[int]int{}
	for c := range codes {
		count[c]++
	}
	assert.Equal(t, 6, count[401], "five free failures and the one that starts the wait: %v", count)
	assert.Equal(t, n-6, count[429], "the rest is refused before it hashes: %v", count)
}

// The password hashes that run at once are bounded; a full house answers 429 at once and does not count
// as a guess.
func TestPasswordHashSlotsAreBounded(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	require.GreaterOrEqual(t, cap(l.srv.hashSlots), 2)
	for i := 0; i < cap(l.srv.hashSlots); i++ {
		l.srv.hashSlots <- struct{}{}
	}
	for i := 0; i < 20; i++ {
		code, _, h := l.passwordStep("ann", "wrong horse battery", "X-Dzo-Client", "192.0.2.81")
		require.Equal(t, 429, code, "attempt %d", i)
		assert.Equal(t, "1", h.Get("Retry-After"))
	}
	for i := 0; i < cap(l.srv.hashSlots); i++ {
		<-l.srv.hashSlots
	}
	code, _, _ := l.passwordStep("ann", password, "X-Dzo-Client", "192.0.2.81")
	assert.Equal(t, 200, code, "the refused attempts used up none of the free ones")
	assert.Empty(t, l.srv.hashSlots, "every slot is given back")
}

func TestBackoffIPv6IsPerPrefix(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	for i := 0; i < 6; i++ {
		_, _, _ = l.passwordStep("user"+strconv.Itoa(i), "wrong horse battery", "X-Dzo-Client", "2001:db8:1:2::"+strconv.Itoa(i+1))
	}
	code, _, _ := l.passwordStep("ann", password, "X-Dzo-Client", "2001:db8:1:2:ffff::7")
	assert.Equal(t, 429, code, "addresses inside one /64 share a key")
	code, _, _ = l.passwordStep("ann", password, "X-Dzo-Client", "2001:db8:1:3::1")
	assert.Equal(t, 200, code, "another /64 does not")
}
