// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func FuzzParseQuantity(f *testing.F) {
	for _, s := range []string{"", "default", "max", "-1", "-2", "0", "5", " 2.5 ", "1e400", "NaN", "-0.5", "abc"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q, err := ParseQuantity(s)
		if err != nil {
			return
		}
		require.False(t, math.IsNaN(q) || math.IsInf(q, 0))
		require.True(t, q >= 0 || q == QuantityDefault || q == QuantityMax, "quantity %v", q)
	})
}
