// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"encoding/json"
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

func FuzzSyncBody(f *testing.F) {
	f.Add(`{"protocol":1,"has":["feed"],"feed":[{"seq":1,"kind":"kill","name":"a","ai":1,"has_pos":true,"x":1.5,"distance":12}]}`)
	f.Add(`{"has":["players"],"feed":[{"seq":-3}]}`)
	f.Add(`{"feed":[{"seq":2,"ai":"x"}]}`)
	f.Fuzz(func(t *testing.T, body string) {
		var req SyncRequest
		if json.Unmarshal([]byte(body), &req) != nil {
			return
		}
		req.Normalise()
		h := NewHub("fuzz")
		h.Sync(req)
		require.LessOrEqual(t, len(h.Feed(0, 0)), FeedKeep)
	})
}
