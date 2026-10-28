// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/auth"
)

func TestNewSecret(t *testing.T) {
	s, h, err := auth.NewSecret("dzs_")
	require.NoError(t, err)
	assert.Regexp(t, `^dzs_[0-9a-f]{64}$`, s)
	assert.Equal(t, auth.HashSecret(s), h)
	assert.Regexp(t, `^[0-9a-f]{64}$`, h)
	assert.NotEqual(t, s, h)
	s2, _, _ := auth.NewSecret("dzs_")
	assert.NotEqual(t, s, s2)
}

func TestRecoveryCodes(t *testing.T) {
	codes, hashes, err := auth.NewRecoveryCodes()
	require.NoError(t, err)
	require.Len(t, codes, 10)
	require.Len(t, hashes, 10)
	seen := map[string]bool{}
	re := regexp.MustCompile(`^[a-z2-7]{5}-[a-z2-7]{5}$`)
	for i, c := range codes {
		assert.Regexp(t, re, c)
		assert.False(t, seen[c], "unique")
		seen[c] = true
		assert.Equal(t, auth.HashRecoveryCode(c), hashes[i])
	}
	assert.Equal(t, auth.HashRecoveryCode("ABCDE-fghij"), auth.HashRecoveryCode("abcdefghij"))
	assert.Equal(t, auth.HashRecoveryCode(" abcde-fghij "), auth.HashRecoveryCode("abcdefghij"))
	assert.NotEqual(t, auth.HashRecoveryCode("abcde-fghik"), auth.HashRecoveryCode("abcdefghij"))
}
