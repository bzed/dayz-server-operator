// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package supervise_test

import (
	"testing"

	"go.uber.org/goleak"
)

// The supervisor starts a goroutine for the health poll and one for every
// child it waits for: a goroutine that outlives Run fails the package.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
