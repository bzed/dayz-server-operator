// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package steam

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyPasswordPrompt(t *testing.T) {
	event, kind, _ := classify("Logging in user 'bob' to Steam Public...\npassword: ")
	require.Equal(t, EventPrompt, event)
	require.Equal(t, PromptPassword, kind)
}

func TestClassifyGuardCodePrompt(t *testing.T) {
	cases := []string{
		"Please enter the Steam Guard code from that message.\nSteam Guard code: ",
		"Two-factor code: ",
		"You can enter the code at any time",
	}
	for _, c := range cases {
		event, kind, _ := classify(c)
		require.Equal(t, EventPrompt, event)
		assert.Equal(t, PromptGuardCode, kind)
	}
}

func TestClassifyAppConfirmWaiting(t *testing.T) {
	event, _, _ := classify("Please confirm this login in the Steam Mobile app...")
	require.Equal(t, EventAppConfirmWaiting, event)
}

func TestClassifySuccess(t *testing.T) {
	cases := []string{
		"Waiting for user info...OK",
		"Logged in OK",
	}
	for _, c := range cases {
		event, _, _ := classify(c)
		assert.Equal(t, EventSuccess, event)
	}
}

func TestClassifySuccessWithColourCodes(t *testing.T) {
	// Captured from a real steamcmd on a terminal: colour codes sit inside the messages.
	cases := []string{
		"Logging in user 'x' [U:1:0] to Steam Public...\x1b[0mOK\n\x1b[0mWaiting for client config...",
		"Waiting for user info...\x1b[0mOK\n",
	}
	for _, c := range cases {
		event, _, _ := classify(c)
		assert.Equal(t, EventSuccess, event)
	}
}

func TestClassifyFailures(t *testing.T) {
	cases := map[string]FailureReason{
		"FAILED (Invalid Password)":                                    FailureInvalidPassword,
		"FAILED (Rate Limit Exceeded)":                                 FailureRateLimited,
		"FAILED (Two-factor code mismatch)":                            FailureGuardCodeMismatch,
		"FAILED (No subscription)":                                     FailureNoSubscription,
		"FAILED (Account Logon Denied)":                                FailureAccountLogonDenied,
		"ERROR! Login Failure: something else went unexpectedly wrong": FailureUnknown,
	}
	for input, want := range cases {
		event, _, reason := classify(input)
		if !assert.Equal(t, EventFailure, event, "classify(%q) event", input) {
			continue
		}
		assert.Equal(t, want, reason)
	}
}

func TestClassifyCachedCredentialsNotFoundIsNoFailure(t *testing.T) {
	// Real steamcmd: the line is followed by the password prompt.
	event, _, _ := classify("Cached credentials not found.\n\n")
	require.Equal(t, EventNone, event)
	event, kind, _ := classify("Cached credentials not found.\n\npassword: ")
	require.Equal(t, EventPrompt, event)
	require.Equal(t, PromptPassword, kind)
}

func TestClassifyNoneOnUnrecognisedOutput(t *testing.T) {
	event, kind, reason := classify("Redirecting stderr to '/root/Steam/logs/stderr.txt'\n")
	require.Equal(t, EventNone, event)
	require.Equal(t, PromptNone, kind)
	require.Empty(t, reason)
}

func TestClassifyRateLimitBeatsGenericFailure(t *testing.T) {
	// "FAILED" alone would match the generic catch-all; rate limiting must
	// win since it needs different handling (backoff, not a bad password).
	event, _, reason := classify("FAILED (Rate Limit Exceeded)")
	require.Equal(t, EventFailure, event)
	require.Equal(t, FailureRateLimited, reason)
}

func TestPromptKindString(t *testing.T) {
	cases := map[PromptKind]string{
		PromptNone:      "none",
		PromptPassword:  "password",
		PromptGuardCode: "guard_code",
		PromptKind(99):  "none",
	}
	for kind, want := range cases {
		got := kind.String()
		assert.Equal(t, want, got)
	}
}
