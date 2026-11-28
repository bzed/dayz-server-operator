// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pending of an account change is not a first factor: it must not end in a new session.
func TestAccountPendingIsNoLogin(t *testing.T) {
	l := newLoginEnv(t)
	l.ann(g("alpha", RoleModerator))
	session := l.loggedIn()
	code, out := l.call("POST", "/api/v1/account/recovery", session, "")
	require.Equal(t, 200, code, "%v", out)
	codes, _ := out["recovery_codes"].([]any)
	require.NotEmpty(t, codes, "%v", out)
	code, out = l.call("POST", "/api/v1/account/totp/begin", session, "")
	require.Equal(t, 200, code, "%v", out)
	pending, _ := out["pending"].(string)
	require.NotEmpty(t, pending)
	code, out, _ = l.second("recovery", pending, codes[0].(string))
	assert.Equal(t, 401, code, "no session without a first factor: %v", out)
	assert.NotContains(t, out, "session")
	code, out, _ = l.second("totp", pending, l.totpCode())
	assert.Equal(t, 401, code, "%v", out)
}
