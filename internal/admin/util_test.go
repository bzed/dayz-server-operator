// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import "testing"

func TestParseQuantity(t *testing.T) {
	for in, want := range map[string]float64{
		"": QuantityDefault, "default": QuantityDefault, " Default ": QuantityDefault, "-1": QuantityDefault,
		"max": QuantityMax, "MAX": QuantityMax, "-2": QuantityMax,
		"0": 0, "5": 5, " 2.5 ": 2.5,
	} {
		got, err := ParseQuantity(in)
		if err != nil || got != want {
			t.Errorf("ParseQuantity(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"lots", "-3", "-0.5", "NaN", "Inf", "1e999"} {
		if _, err := ParseQuantity(in); err == nil {
			t.Errorf("ParseQuantity(%q) did not fail", in)
		}
	}
}
