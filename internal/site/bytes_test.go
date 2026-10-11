// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBytesParsesUnits(t *testing.T) {
	for in, want := range map[string]Bytes{
		"1024":   1024,
		"10B":    10,
		"500MiB": 500 << 20,
		"10GiB":  10 << 30,
		"10gib":  10 << 30,
		"50GB":   50_000_000_000,
		"2TB":    2_000_000_000_000,
		"1 KiB":  1024,
		"3G":     3_000_000_000,
	} {
		var b Bytes
		if err := yaml.Unmarshal([]byte(`"`+in+`"`), &b); err != nil || b != want {
			t.Errorf("%q = %d, %v; want %d", in, b, err, want)
		}
	}
	for _, in := range []string{"GiB", "10IB", "10XB", "-5GiB", "1.5GiB"} {
		var b Bytes
		if err := yaml.Unmarshal([]byte(`"`+in+`"`), &b); err == nil {
			t.Errorf("%q should be rejected, got %d", in, b)
		}
	}
}
