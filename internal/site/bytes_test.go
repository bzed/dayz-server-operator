// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		err := yaml.Unmarshal([]byte(`"`+in+`"`), &b)
		require.NoError(t, err, "%q = %d, %v; want %d", in, b, err, want)
		assert.Equal(t, want, b, "%q = %d, %v; want %d", in, b, err, want)
	}
	for _, in := range []string{"GiB", "10IB", "10XB", "-5GiB", "1.5GiB"} {
		var b Bytes
		err := yaml.Unmarshal([]byte(`"`+in+`"`), &b)
		require.Error(t, err)
	}
}
