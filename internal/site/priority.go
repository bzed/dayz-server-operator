// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"fmt"
	"regexp"
)

var reSteam64 = regexp.MustCompile(`^7656119\d{10}$`)

// ValidatePriority checks the priority list: Steam64 IDs (17 digits, 7656119...), no duplicates.
func ValidatePriority(ids []string) error {
	seen := map[string]bool{}
	for i, id := range ids {
		switch {
		case !reSteam64.MatchString(id):
			return fmt.Errorf("priority[%d]: %q is not a Steam64 ID (17 digits starting with 7656119)", i, id)
		case seen[id]:
			return fmt.Errorf("priority[%d]: %s is listed twice", i, id)
		}
		seen[id] = true
	}
	return nil
}
