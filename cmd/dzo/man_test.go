// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenMan(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "man")
	out, err := runCmd(t, "gen-man", dir)
	if err != nil || !strings.Contains(out, "man pages written") {
		t.Fatalf("%v %s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "dzo-instance-upgrade.1"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{".TH \"DZO-INSTANCE-UPGRADE\"", ".SH NAME", "\\-\\-build", ".SH SEE ALSO", "dzo\\-instance (1)"} {
		if !strings.Contains(s, want) {
			t.Errorf("man page lacks %q:\n%s", want, s)
		}
	}
	top, _ := os.ReadFile(filepath.Join(dir, "dzo.1"))
	if !strings.Contains(string(top), ".SH COMMANDS") || !strings.Contains(string(top), "dzo\\-start(1)") {
		t.Errorf("dzo.1 does not list the commands:\n%s", top)
	}
	if _, err := os.Stat(filepath.Join(dir, "dzo-gen-man.1")); err == nil {
		t.Error("the hidden command has no man page")
	}
	if got := roff(".dot\n'quote\na-b\\c"); got != "\\&.dot\n\\&'quote\na\\-b\\ec" {
		t.Errorf("roff = %q", got)
	}
}
