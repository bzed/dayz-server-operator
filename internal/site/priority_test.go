// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatePriority(t *testing.T) {
	err := ValidatePriority([]string{"76561198000000001", "76561198000000002"})
	require.NoError(t, err)
	for _, bad := range [][]string{{"123"}, {"7656119800000000a"}, {"76561198000000001", "76561198000000001"}} {
		assert.Error(t, ValidatePriority(bad))
	}
}
