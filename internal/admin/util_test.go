// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseQuantity(t *testing.T) {
	for in, want := range map[string]float64{
		"": QuantityDefault, "default": QuantityDefault, " Default ": QuantityDefault, "-1": QuantityDefault,
		"max": QuantityMax, "MAX": QuantityMax, "-2": QuantityMax,
		"0": 0, "5": 5, " 2.5 ": 2.5,
	} {
		got, err := ParseQuantity(in)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	for _, in := range []string{"lots", "-3", "-0.5", "NaN", "Inf", "1e999"} {
		_, err := ParseQuantity(in)
		assert.Error(t, err, "ParseQuantity(%q) did not fail", in)
	}
}
