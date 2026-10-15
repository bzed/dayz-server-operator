// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import "testing"

func TestValidatePriority(t *testing.T) {
	if err := ValidatePriority([]string{"76561198000000001", "76561198000000002"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{{"123"}, {"7656119800000000a"}, {"76561198000000001", "76561198000000001"}} {
		if ValidatePriority(bad) == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
