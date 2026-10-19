// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package servercfg

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func FuzzServercfgParse(f *testing.F) {
	f.Add([]byte(sample))
	f.Add([]byte(""))
	f.Add([]byte("a[] = {1,2,\"x\"};"))
	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := Parse(data)
		if err != nil {
			return
		}
		again, err := Parse(file.Bytes())
		require.NoError(t, err, "rendered output must parse again")
		require.Equal(t, file.Bytes(), again.Bytes(), "rendering is a fixpoint")
	})
}
