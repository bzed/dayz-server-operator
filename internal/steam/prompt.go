// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package steam drives an interactive steamcmd login over a pseudo-terminal
// (§C7 "Steam authentication"): steamcmd needs a human for the password and
// a Steam Guard factor (e-mail code, mobile-authenticator code, or an
// in-app confirmation), and its cached session can expire at any time, so
// dzo treats login as a recurring interactive operator task rather than a
// one-time setup.
//
// steamcmd does not document its interactive prompts or failure messages;
// the classification table in this package is reverse-engineered from
// widely reported community usage, not Valve's source, and has not been
// exercised against a live Steam account from this environment (no Steam
// access here). Treat unrecognised steamcmd output as a gap in the table to
// fill in once real-Steam verification is possible, not as proof the state
// machine itself is wrong: Run keeps reading until it recognises a prompt,
// a success marker, or a failure marker, or the process exits/times out.
package steam

import (
	"regexp"
	"strings"
)

// PromptKind identifies which piece of interactive input steamcmd is
// currently waiting for.
type PromptKind int

const (
	PromptNone PromptKind = iota
	PromptPassword
	PromptGuardCode
)

// String renders k for logs and error messages.
func (k PromptKind) String() string {
	switch k {
	case PromptPassword:
		return "password"
	case PromptGuardCode:
		return "guard_code"
	default:
		return "none"
	}
}

// Event is what one chunk of steamcmd pty output means to the login state
// machine.
type Event int

const (
	// EventNone means nothing recognisable has appeared yet; keep reading.
	EventNone Event = iota
	// EventPrompt means steamcmd is waiting for input (see the returned
	// PromptKind).
	EventPrompt
	// EventAppConfirmWaiting means steamcmd is waiting for the user to
	// confirm the login in the Steam mobile app; no text input is expected.
	EventAppConfirmWaiting
	// EventSuccess means steamcmd reported a successful login.
	EventSuccess
	// EventFailure means steamcmd reported a login failure (see the
	// returned FailureReason).
	EventFailure
)

// FailureReason classifies a failed login attempt from steamcmd's own
// message, so callers (notifications, `dzo steam status`) can show
// something more specific than "failed".
type FailureReason string

const (
	FailureInvalidPassword    FailureReason = "invalid_password"
	FailureGuardCodeMismatch  FailureReason = "guard_code_mismatch"
	FailureAccountLogonDenied FailureReason = "account_logon_denied"
	//nolint:gosec // G101: this is a FailureReason label describing an absent cached session, not a credential value
	FailureCachedCredentialsGone FailureReason = "cached_credentials_not_found"
	FailureRateLimited           FailureReason = "rate_limited"
	FailureNoSubscription        FailureReason = "no_subscription"
	FailureUnknown               FailureReason = "unknown"
)

// ansiRe matches the colour codes steamcmd prints on a terminal, which sit
// inside its messages ("Waiting for user info...\x1b[0mOK").
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// classification is one entry in the marker table: any of substrs matching
// (case-insensitively) inside newly observed steamcmd output triggers this
// outcome.
type classification struct {
	event   Event
	kind    PromptKind
	reason  FailureReason
	substrs []string
}

// markers is steamcmd's known interactive vocabulary, ordered most specific
// first: some phrases are substrings of a more generic failure message
// (e.g. any classified failure also contains "failed"), so the specific
// entries must be tried before the catch-alls.
var markers = []classification{
	{event: EventFailure, reason: FailureRateLimited, substrs: []string{"rate limit exceeded"}},
	{event: EventFailure, reason: FailureInvalidPassword, substrs: []string{"invalid password"}},
	{event: EventFailure, reason: FailureGuardCodeMismatch, substrs: []string{
		"two-factor code mismatch", "steam guard code is incorrect", "invalidloginauthcode",
	}},
	{event: EventFailure, reason: FailureNoSubscription, substrs: []string{"no subscription"}},
	// "Cached credentials not found." is no failure: steamcmd prints it and
	// then asks for the password (seen with a real steamcmd), so it is not a marker.
	{event: EventFailure, reason: FailureAccountLogonDenied, substrs: []string{"account logon denied"}},
	{event: EventFailure, reason: FailureUnknown, substrs: []string{"login failure", "failed ("}},
	{event: EventAppConfirmWaiting, substrs: []string{"confirm", "mobile app"}},
	{event: EventPrompt, kind: PromptGuardCode, substrs: []string{
		"steam guard code", "two-factor code", "enter the code",
	}},
	{event: EventPrompt, kind: PromptPassword, substrs: []string{"password:"}},
	{event: EventSuccess, substrs: []string{"logged in ok", "waiting for user info...ok", "to steam public...ok"}},
}

// classify scans buf (accumulated, not-yet-handled steamcmd output) for the
// first marker whose substring appears. It returns EventNone if nothing
// recognisable has appeared yet.
func classify(buf string) (event Event, kind PromptKind, reason FailureReason) {
	lower := strings.ToLower(ansiRe.ReplaceAllString(buf, ""))
	for _, m := range markers {
		for _, s := range m.substrs {
			if strings.Contains(lower, s) {
				return m.event, m.kind, m.reason
			}
		}
	}
	return EventNone, PromptNone, ""
}
