// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package auth holds the primitives of web logins: password hashing, TOTP,
// random secrets and recovery codes, Steam OpenID verification, the address of
// a client behind proxies, and the delay after failed logins. It has no
// database and no HTTP routing.
package auth
