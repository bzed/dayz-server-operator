// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package a2s

import "testing"

func FuzzParseInfoResponse(f *testing.F) {
	f.Add(append([]byte{0xFF, 0xFF, 0xFF, 0xFF, 'I'}, []byte("body")...))
	f.Add([]byte{'I', 17, 's', 'e', 'r', 'v', 0, 'm', 0, 'f', 0, 'g', 0})
	f.Add([]byte{})
	f.Fuzz(func(_ *testing.T, body []byte) { _, _ = ParseInfoResponse(body) })
}
