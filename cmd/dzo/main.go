// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Command dzo is the dayz-server-operator CLI.
package main

import (
	"errors"
	"os"
)

func main() {
	err := newRootCmd().Execute()
	if err == nil {
		return
	}
	var codeErr *exitCodeErr
	if errors.As(err, &codeErr) {
		os.Exit(codeErr.code)
	}
	var exitErr *checkExitErr
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.code)
	}
	os.Exit(1)
}
