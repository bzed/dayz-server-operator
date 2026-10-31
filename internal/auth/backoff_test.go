// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/bzed/dayz-server-operator/internal/auth"
)

func TestBackoff(t *testing.T) {
	now := time.Unix(1000, 0)
	b := auth.NewBackoff(func() time.Time { return now })
	for i := 0; i < 5; i++ {
		assert.Zero(t, b.Wait("acct:ann"), "failure %d is free", i)
		b.Fail("acct:ann")
	}
	assert.Zero(t, b.Wait("acct:ann"), "five failures are free")
	b.Fail("acct:ann")
	assert.Equal(t, time.Second, b.Wait("acct:ann"), "the sixth")
	now = now.Add(400 * time.Millisecond)
	assert.Equal(t, 600*time.Millisecond, b.Wait("acct:ann"), "it runs down")
	now = now.Add(time.Second)
	assert.Zero(t, b.Wait("acct:ann"))
	b.Fail("acct:ann")
	assert.Equal(t, 2*time.Second, b.Wait("acct:ann"), "doubling")
	for i := 0; i < 20; i++ {
		b.Fail("acct:ann")
	}
	assert.Equal(t, 15*time.Minute, b.Wait("acct:ann"), "capped")
	assert.Zero(t, b.Wait("acct:bob"), "other accounts are unaffected")
}

func TestBackoffSuccessResets(t *testing.T) {
	now := time.Unix(1000, 0)
	b := auth.NewBackoff(func() time.Time { return now })
	for i := 0; i < 8; i++ {
		b.Fail("acct:ann", "ip:1.2.3.4")
	}
	assert.Positive(t, b.Wait("acct:ann"))
	b.Success("acct:ann")
	assert.Zero(t, b.Wait("acct:ann"))
	assert.Positive(t, b.Wait("ip:1.2.3.4"), "the address keeps its count")
}

func TestBackoffForgets(t *testing.T) {
	now := time.Unix(1000, 0)
	b := auth.NewBackoff(func() time.Time { return now })
	for i := 0; i < 30; i++ {
		b.Fail("acct:ann")
	}
	now = now.Add(61 * time.Minute)
	assert.Zero(t, b.Wait("acct:ann"), "forgotten after an hour without failures")
	b.Fail("acct:ann")
	assert.Zero(t, b.Wait("acct:ann"), "and counting from zero again")
}

func TestBackoffLargestOfSeveralKeys(t *testing.T) {
	now := time.Unix(1000, 0)
	b := auth.NewBackoff(func() time.Time { return now })
	for i := 0; i < 6; i++ {
		b.Fail("ip:1.2.3.4")
	}
	for i := 0; i < 8; i++ {
		b.Fail("acct:ann")
	}
	assert.Equal(t, 4*time.Second, b.Wait("ip:1.2.3.4", "acct:ann"))
	assert.Equal(t, time.Second, b.Wait("ip:1.2.3.4", "acct:nobody"))
}

func TestBackoffConcurrent(t *testing.T) {
	b := auth.NewBackoff(time.Now)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				b.Fail("k")
				_ = b.Wait("k")
				b.Success("other")
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
