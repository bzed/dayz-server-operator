// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package steam

import "testing"

func TestClassifyPasswordPrompt(t *testing.T) {
	event, kind, _ := classify("Logging in user 'bob' to Steam Public...\npassword: ")
	if event != EventPrompt || kind != PromptPassword {
		t.Fatalf("event=%v kind=%v, want EventPrompt/PromptPassword", event, kind)
	}
}

func TestClassifyGuardCodePrompt(t *testing.T) {
	cases := []string{
		"Please enter the Steam Guard code from that message.\nSteam Guard code: ",
		"Two-factor code: ",
		"You can enter the code at any time",
	}
	for _, c := range cases {
		event, kind, _ := classify(c)
		if event != EventPrompt || kind != PromptGuardCode {
			t.Errorf("classify(%q) = event=%v kind=%v, want EventPrompt/PromptGuardCode", c, event, kind)
		}
	}
}

func TestClassifyAppConfirmWaiting(t *testing.T) {
	event, _, _ := classify("Please confirm this login in the Steam Mobile app...")
	if event != EventAppConfirmWaiting {
		t.Fatalf("event = %v, want EventAppConfirmWaiting", event)
	}
}

func TestClassifySuccess(t *testing.T) {
	cases := []string{
		"Waiting for user info...OK",
		"Logged in OK",
	}
	for _, c := range cases {
		event, _, _ := classify(c)
		if event != EventSuccess {
			t.Errorf("classify(%q) = %v, want EventSuccess", c, event)
		}
	}
}

func TestClassifySuccessWithColourCodes(t *testing.T) {
	// Captured from a real steamcmd on a terminal: colour codes sit inside the messages.
	cases := []string{
		"Logging in user 'x' [U:1:0] to Steam Public...\x1b[0mOK\n\x1b[0mWaiting for client config...",
		"Waiting for user info...\x1b[0mOK\n",
	}
	for _, c := range cases {
		if event, _, _ := classify(c); event != EventSuccess {
			t.Errorf("classify(%q) = %v, want EventSuccess", c, event)
		}
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
		if event != EventFailure {
			t.Errorf("classify(%q) event = %v, want EventFailure", input, event)
			continue
		}
		if reason != want {
			t.Errorf("classify(%q) reason = %v, want %v", input, reason, want)
		}
	}
}

func TestClassifyCachedCredentialsNotFoundIsNoFailure(t *testing.T) {
	// Real steamcmd: the line is followed by the password prompt.
	if event, _, _ := classify("Cached credentials not found.\n\n"); event != EventNone {
		t.Fatalf("event = %v, want EventNone", event)
	}
	if event, kind, _ := classify("Cached credentials not found.\n\npassword: "); event != EventPrompt || kind != PromptPassword {
		t.Fatalf("event=%v kind=%v, want the password prompt", event, kind)
	}
}

func TestClassifyNoneOnUnrecognisedOutput(t *testing.T) {
	event, kind, reason := classify("Redirecting stderr to '/root/Steam/logs/stderr.txt'\n")
	if event != EventNone || kind != PromptNone || reason != "" {
		t.Fatalf("event=%v kind=%v reason=%v, want all zero", event, kind, reason)
	}
}

func TestClassifyRateLimitBeatsGenericFailure(t *testing.T) {
	// "FAILED" alone would match the generic catch-all; rate limiting must
	// win since it needs different handling (backoff, not a bad password).
	event, _, reason := classify("FAILED (Rate Limit Exceeded)")
	if event != EventFailure || reason != FailureRateLimited {
		t.Fatalf("event=%v reason=%v, want EventFailure/FailureRateLimited", event, reason)
	}
}

func TestPromptKindString(t *testing.T) {
	cases := map[PromptKind]string{
		PromptNone:      "none",
		PromptPassword:  "password",
		PromptGuardCode: "guard_code",
		PromptKind(99):  "none",
	}
	for kind, want := range cases {
		if got := kind.String(); got != want {
			t.Errorf("PromptKind(%d).String() = %q, want %q", kind, got, want)
		}
	}
}
