// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package integrate

import (
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizerNamesAgree(t *testing.T) {
	for _, n := range site.NormalizerNames {
		assert.True(t, KnownNormalizer(n), "%s is not implemented", n)
	}
	assert.False(t, KnownNormalizer("nope"))
}

func TestNormalize(t *testing.T) {
	bare := []byte("<event name=\"A\"><nominal>1</nominal></event>\n<event name=\"B\"/>\n")
	all := []string{NormEventPosDefRoot, NormWrapRoot, NormXMLDecl}

	out, err := Normalize("events.xml", bare, all)
	require.NoError(t, err)
	s := string(out)
	assert.True(t, strings.HasPrefix(s, "<?xml version"), "bare list:\n%s", s)
	assert.Contains(t, s, "<events>\n<event name=\"A\">")
	assert.True(t, strings.HasSuffix(s, "</events>\n"), "bare list:\n%s", s)

	// a proper file stays the same except for the declaration
	ok := []byte("<types><type name=\"X\"/></types>")
	out, _ = Normalize("types.xml", ok, []string{NormWrapRoot})
	assert.Equal(t, string(ok), string(out), "a single root must not be wrapped")
	out, _ = Normalize("types.xml", append([]byte("\xef\xbb\xbf"), ok...), []string{NormXMLDecl})
	assert.True(t, strings.HasPrefix(string(out), "<?xml"), "declaration: %q", out)
	assert.NotContains(t, string(out), "\xef\xbb\xbf", "BOM")
	again, _ := Normalize("types.xml", out, []string{NormXMLDecl})
	assert.Equal(t, string(out), string(again), "the declaration must not be added twice")

	// <events> to <eventposdef>, keeping the declaration
	spawns := []byte(`<?xml version="1.0"?><events><event name="A"><pos x="1"/></event></events>`)
	out, _ = Normalize("cfgeventspawns.xml", spawns, []string{NormEventPosDefRoot})
	assert.True(t, strings.HasPrefix(string(out), "<?xml"), "eventposdef root: %s", out)
	assert.Contains(t, string(out), "<eventposdef><event")
	assert.Contains(t, string(out), "</eventposdef>")
	assert.NotContains(t, string(out), "<events")
	out, _ = Normalize("events.xml", spawns, []string{NormEventPosDefRoot})
	assert.Equal(t, string(spawns), string(out), "only cfgeventspawns.xml is renamed")

	// not XML, unknown normalizer, no known root, broken input
	js := []byte("{}")
	out, err = Normalize("cfg.json", js, all)
	require.NoError(t, err)
	assert.Equal(t, "{}", string(out))
	_, err = Normalize("types.xml", ok, []string{"x"})
	require.Error(t, err, "an unknown normalizer must fail")
	_, err = Normalize("custom.xml", bare, []string{NormWrapRoot})
	require.Error(t, err, "a bare list in a file of unknown kind must fail")
	if _, err = Normalize("types.xml", []byte("<a><b></a>"), []string{NormWrapRoot}); err != nil {
		t.Logf("broken xml: %v", err) // tolerated either way: the validation after the merge finds it
	}
}
