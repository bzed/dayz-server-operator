// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package tlsfiles

import "time"

// SetClock replaces the clock the re-check interval is measured with.
func (p *Pair) SetClock(now func() time.Time) { p.w.setClock(now) }

// SetClock replaces the clock the re-check interval is measured with.
func (p *Pool) SetClock(now func() time.Time) { p.w.setClock(now) }
