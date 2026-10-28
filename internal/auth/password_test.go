// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/auth"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := auth.HashPassword("correct horse battery")
	require.NoError(t, err)
	assert.True(t, auth.CheckPassword(h, "correct horse battery"))
}

func TestPasswordWrong(t *testing.T) {
	h, err := auth.HashPassword("correct horse battery")
	require.NoError(t, err)
	assert.False(t, auth.CheckPassword(h, "correct horse batterY"))
	assert.False(t, auth.CheckPassword("", "x"))
	assert.False(t, auth.CheckPassword("$argon2id$garbage", "x"))
	assert.False(t, auth.CheckPassword("$argon2id$v=19$m=999999999,t=3,p=2$AAAA$AAAA", "x"), "absurd cost parameters are refused, not run")
}

func TestPasswordLength(t *testing.T) {
	_, err := auth.HashPassword(strings.Repeat("a", 11))
	require.ErrorIs(t, err, auth.ErrPasswordLength)
	_, err = auth.HashPassword(strings.Repeat("a", 257))
	require.ErrorIs(t, err, auth.ErrPasswordLength)
	_, err = auth.HashPassword(strings.Repeat("a", 12))
	require.NoError(t, err)
	_, err = auth.HashPassword(strings.Repeat("a", 256))
	require.NoError(t, err)
}

func TestPasswordHashFormat(t *testing.T) {
	h, err := auth.HashPassword("correct horse battery")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$"), h)
	h2, err := auth.HashPassword("correct horse battery")
	require.NoError(t, err)
	assert.NotEqual(t, h, h2, "a fresh salt each time")
}
