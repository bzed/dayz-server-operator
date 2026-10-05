// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package runfiles

import "testing"

func TestHex(t *testing.T) {
	a, b := Hex(4), Hex(4)
	if len(a) != 8 || len(b) != 8 || a == b {
		t.Errorf("Hex(4) = %q, %q: want two different 8-character strings", a, b)
	}
}
