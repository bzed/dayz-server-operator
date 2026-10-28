// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"
)

// NewSecret returns prefix plus 32 random bytes in hex, and the hash that is
// stored instead of it.
func NewSecret(prefix string) (secret, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("secret: %w", err)
	}
	secret = prefix + hex.EncodeToString(b)
	return secret, HashSecret(secret), nil
}

// HashSecret is the SHA-256 of a session secret or pending-login id, in hex.
func HashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

var codeAlphabet = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewRecoveryCodes returns ten codes like abcde-fghij and their hashes.
func NewRecoveryCodes() (codes, hashes []string, err error) {
	for i := 0; i < 10; i++ {
		b := make([]byte, 7) // 56 bits, 11 base32 characters; ten are used
		if _, err := rand.Read(b); err != nil {
			return nil, nil, fmt.Errorf("recovery code: %w", err)
		}
		s := strings.ToLower(codeAlphabet.EncodeToString(b))[:10]
		c := s[:5] + "-" + s[5:]
		codes, hashes = append(codes, c), append(hashes, HashRecoveryCode(c))
	}
	return codes, hashes, nil
}

// HashRecoveryCode hashes a code ignoring case, dashes and spaces.
func HashRecoveryCode(code string) string {
	n := strings.NewReplacer("-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(code)))
	return HashSecret("recovery:" + n)
}
