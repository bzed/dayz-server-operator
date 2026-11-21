// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package packaging has no code of its own. It is the home of the tests for
// the packaging files that live outside the Go tree (packaging/common), which
// the .deb postinst, the rpm %post and the Arch .install hook all call. A Go
// test that runs those shell scripts against a temporary root, a fake PATH and
// a log of the commands they run is the only way to have them checked by
// `make test` like every other test.
package packaging
