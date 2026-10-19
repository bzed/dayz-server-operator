// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package runfiles

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHex(t *testing.T) {
	a, b := Hex(4), Hex(4)
	assert.Len(t, a, 8)
	assert.Len(t, b, 8)
	assert.NotEqual(t, a, b)
}
