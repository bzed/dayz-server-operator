// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package integrate

import (
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/site"
)

func TestNormalizerNamesAgree(t *testing.T) {
	for _, n := range site.NormalizerNames {
		if !KnownNormalizer(n) {
			t.Errorf("%s is not implemented", n)
		}
	}
	if KnownNormalizer("nope") {
		t.Error("nope is known")
	}
}

func TestNormalize(t *testing.T) {
	bare := []byte("<event name=\"A\"><nominal>1</nominal></event>\n<event name=\"B\"/>\n")
	all := []string{NormEventPosDefRoot, NormWrapRoot, NormXMLDecl}

	out, err := Normalize("events.xml", bare, all)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "<?xml version") || !strings.Contains(s, "<events>\n<event name=\"A\">") || !strings.HasSuffix(s, "</events>\n") {
		t.Errorf("bare list:\n%s", s)
	}

	// a proper file stays the same except for the declaration
	ok := []byte("<types><type name=\"X\"/></types>")
	out, _ = Normalize("types.xml", ok, []string{NormWrapRoot})
	if string(out) != string(ok) {
		t.Errorf("a single root must not be wrapped: %s", out)
	}
	out, _ = Normalize("types.xml", append([]byte("\xef\xbb\xbf"), ok...), []string{NormXMLDecl})
	if !strings.HasPrefix(string(out), "<?xml") || strings.Contains(string(out), "\xef\xbb\xbf") {
		t.Errorf("declaration / BOM: %q", out)
	}
	again, _ := Normalize("types.xml", out, []string{NormXMLDecl})
	if string(again) != string(out) {
		t.Error("the declaration must not be added twice")
	}

	// <events> to <eventposdef>, keeping the declaration
	spawns := []byte(`<?xml version="1.0"?><events><event name="A"><pos x="1"/></event></events>`)
	out, _ = Normalize("cfgeventspawns.xml", spawns, []string{NormEventPosDefRoot})
	if !strings.HasPrefix(string(out), "<?xml") || !strings.Contains(string(out), "<eventposdef><event") || !strings.Contains(string(out), "</eventposdef>") || strings.Contains(string(out), "<events") {
		t.Errorf("eventposdef root: %s", out)
	}
	if out, _ = Normalize("events.xml", spawns, []string{NormEventPosDefRoot}); string(out) != string(spawns) {
		t.Error("only cfgeventspawns.xml is renamed")
	}

	// not XML, unknown normalizer, no known root, broken input
	js := []byte("{}")
	if out, err = Normalize("cfg.json", js, all); err != nil || string(out) != "{}" {
		t.Errorf("json: %v %s", err, out)
	}
	if _, err = Normalize("types.xml", ok, []string{"x"}); err == nil {
		t.Error("an unknown normalizer must fail")
	}
	if _, err = Normalize("custom.xml", bare, []string{NormWrapRoot}); err == nil {
		t.Error("a bare list in a file of unknown kind must fail")
	}
	if _, err = Normalize("types.xml", []byte("<a><b></a>"), []string{NormWrapRoot}); err != nil {
		t.Logf("broken xml: %v", err) // tolerated either way: the validation after the merge finds it
	}
}
